// Package workitems contains WorkItem import boundary types.
package workitems

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Provider reads provider-owned issue snapshots.
type Provider interface {
	GetIssue(context.Context, ProviderRef) (ProviderIssue, error)
}

// ProviderIssueLister reads provider-owned issue candidates without importing them.
type ProviderIssueLister interface {
	ListIssues(context.Context, ProviderIssueListInput) ([]ProviderIssue, error)
}

// ProviderRepositoryRef identifies a provider-backed repository without selecting one issue.
type ProviderRepositoryRef struct {
	Provider       string
	ProviderHost   string
	RepositoryPath string
}

// String returns the canonical provider-backed project reference.
func (ref ProviderRepositoryRef) String() string {
	return fmt.Sprintf("%s://%s/%s", ref.Provider, ref.ProviderHost, ref.RepositoryPath)
}

// IssueRef returns the canonical issue ProviderRef for an issue number in this repository.
func (ref ProviderRepositoryRef) IssueRef(issueNumber int) ProviderRef {
	return ProviderRef{
		Provider:       ref.Provider,
		ProviderHost:   ref.ProviderHost,
		RepositoryPath: ref.RepositoryPath,
		IssueNumber:    issueNumber,
	}
}

// ProviderIssueListInput scopes a read-only provider issue candidate listing.
type ProviderIssueListInput struct {
	Repository ProviderRepositoryRef
	Labels     []string
}

// ProviderRef is a canonical ForgeLane reference to a provider-owned issue.
type ProviderRef struct {
	Provider       string
	ProviderHost   string
	RepositoryPath string
	IssueNumber    int
}

// ParseProviderRef parses a canonical issue ProviderRef.
func ParseProviderRef(raw string) (ProviderRef, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ProviderRef{}, fmt.Errorf("invalid WorkItem ProviderRef %q", raw)
	}
	if (parsed.Scheme == "https" || parsed.Scheme == "http") && (parsed.Host == "github.com" || parsed.Host == "gitlab.com") {
		return ProviderRef{}, fmt.Errorf("raw %s issue URLs are not supported; use %s://%s/owner/repo/issues/123", providerName(parsed.Host), providerScheme(parsed.Host), parsed.Host)
	}
	if parsed.Scheme != "github" && parsed.Scheme != "gitlab" {
		return ProviderRef{}, fmt.Errorf("unsupported WorkItem provider %q", parsed.Scheme)
	}
	if parsed.Scheme == "github" && parsed.Host != "github.com" {
		return ProviderRef{}, fmt.Errorf("unsupported GitHub provider host %q", parsed.Host)
	}
	if parsed.Scheme == "gitlab" && parsed.Host == "" {
		return ProviderRef{}, fmt.Errorf("unsupported GitLab provider host %q", parsed.Host)
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	issueMarker := -1
	for index, part := range parts {
		if part == "issues" {
			issueMarker = index
			break
		}
	}
	if issueMarker < 0 || issueMarker != len(parts)-2 {
		return ProviderRef{}, fmt.Errorf("invalid %s issue ProviderRef %q", providerName(parsed.Scheme), raw)
	}
	repositoryParts := parts[:issueMarker]
	if parsed.Scheme == "github" && len(repositoryParts) != 2 {
		return ProviderRef{}, fmt.Errorf("invalid GitHub issue ProviderRef %q", raw)
	}
	if parsed.Scheme == "gitlab" && len(repositoryParts) < 2 {
		return ProviderRef{}, fmt.Errorf("invalid GitLab issue ProviderRef %q", raw)
	}
	for _, part := range repositoryParts {
		if !validProviderPathPart(part) {
			return ProviderRef{}, fmt.Errorf("invalid %s issue ProviderRef %q", providerName(parsed.Scheme), raw)
		}
	}

	issueNumber, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || issueNumber <= 0 {
		return ProviderRef{}, fmt.Errorf("invalid %s issue ProviderRef %q", providerName(parsed.Scheme), raw)
	}

	return ProviderRef{
		Provider:       parsed.Scheme,
		ProviderHost:   parsed.Host,
		RepositoryPath: strings.Join(repositoryParts, "/"),
		IssueNumber:    issueNumber,
	}, nil
}

// String returns the canonical issue ProviderRef.
func (ref ProviderRef) String() string {
	return fmt.Sprintf("%s://%s/%s/issues/%d", ref.Provider, ref.ProviderHost, ref.RepositoryPath, ref.IssueNumber)
}

// RepositoryRef returns the canonical provider-backed project reference.
func (ref ProviderRef) RepositoryRef() string {
	return fmt.Sprintf("%s://%s/%s", ref.Provider, ref.ProviderHost, ref.RepositoryPath)
}

// ProviderIssue is a provider-owned issue snapshot normalized for import.
type ProviderIssue struct {
	ProviderRef         string
	RepositoryRef       string
	Provider            string
	ProviderIssueNumber int
	Title               string
	Body                string
	Status              string
	RawStatus           string
	URL                 string
	ProviderUpdatedAt   time.Time
}

// WorkItemImport is the normalized WorkItem snapshot intake decision.
type WorkItemImport struct {
	Issue ProviderIssue
	Ref   ProviderRef
}

// ImportEventInput supplies persistence identities assigned during an import transaction.
type ImportEventInput struct {
	Existing          bool
	WorkItemID        int64
	ForgeProjectID    int64
	ProviderUpdatedAt string
}

// ImportEventPlan is the audit Event chosen for a WorkItem import transaction.
type ImportEventPlan struct {
	Type        string
	SubjectType string
	SubjectRef  string
	ProviderRef string
	Payload     map[string]any
}

// NewWorkItemImport normalizes a provider-owned issue snapshot for ForgeLane intake.
func NewWorkItemImport(issue ProviderIssue) (WorkItemImport, error) {
	ref, err := ParseProviderRef(issue.ProviderRef)
	if err != nil {
		return WorkItemImport{}, err
	}
	return WorkItemImport{
		Issue: issue.Normalize(ref),
		Ref:   ref,
	}, nil
}

// EventPlan returns the audit Event for the import or refresh outcome.
func (importDecision WorkItemImport) EventPlan(input ImportEventInput) ImportEventPlan {
	eventType := "work_item.imported"
	if input.Existing {
		eventType = "work_item.refreshed"
	}
	return ImportEventPlan{
		Type:        eventType,
		SubjectType: "work_item",
		SubjectRef:  importDecision.Issue.ProviderRef,
		ProviderRef: importDecision.Issue.ProviderRef,
		Payload: map[string]any{
			"provider_ref":        importDecision.Issue.ProviderRef,
			"repository_ref":      importDecision.Issue.RepositoryRef,
			"provider_updated_at": input.ProviderUpdatedAt,
			"work_item_id":        input.WorkItemID,
			"forge_project_id":    input.ForgeProjectID,
		},
	}
}

// Normalize anchors provider data to the parsed ProviderRef identity.
func (issue ProviderIssue) Normalize(ref ProviderRef) ProviderIssue {
	issue.ProviderRef = ref.String()
	issue.RepositoryRef = ref.RepositoryRef()
	issue.Provider = ref.Provider
	issue.ProviderIssueNumber = ref.IssueNumber
	statusSource := issue.Status
	rawStatus := issue.RawStatus
	if statusSource == "" {
		statusSource = rawStatus
	}
	if rawStatus == "" {
		rawStatus = statusSource
	}
	issue.Status = normalizeStatus(statusSource)
	if rawStatus == "" {
		rawStatus = issue.Status
	}
	issue.RawStatus = rawStatus
	return issue
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "open":
		return "open"
	case "closed":
		return "closed"
	default:
		return "unknown"
	}
}

// NotFoundError reports a provider-owned issue that does not exist or is hidden.
type NotFoundError struct {
	ProviderRef string
}

func (err NotFoundError) Error() string {
	return fmt.Sprintf("issue not found: %s", err.ProviderRef)
}

// NotIssueError reports a provider response that points at a PR, not an issue WorkItem.
type NotIssueError struct {
	ProviderRef string
}

func (err NotIssueError) Error() string {
	return fmt.Sprintf("not an issue WorkItem: %s", err.ProviderRef)
}

// AuthError reports a provider auth or permission failure.
type AuthError struct {
	ProviderRef string
}

func (err AuthError) Error() string {
	return fmt.Sprintf("auth or permission failure reading issue: %s", err.ProviderRef)
}

// ProviderError reports an unclassified provider read failure.
type ProviderError struct {
	ProviderRef string
	StatusCode  int
}

func (err ProviderError) Error() string {
	return fmt.Sprintf("%s provider failure reading issue %s: HTTP %d", providerNameFromRef(err.ProviderRef), err.ProviderRef, err.StatusCode)
}

func providerName(provider string) string {
	switch provider {
	case "github", "github.com":
		return "GitHub"
	case "gitlab", "gitlab.com":
		return "GitLab"
	default:
		return provider
	}
}

func providerScheme(host string) string {
	switch host {
	case "github.com":
		return "github"
	case "gitlab.com":
		return "gitlab"
	default:
		return host
	}
}

func providerNameFromRef(providerRef string) string {
	parsed, err := url.Parse(providerRef)
	if err != nil {
		return "provider"
	}
	name := providerName(parsed.Scheme)
	if name == "" {
		return "provider"
	}
	return name
}

func validProviderPathPart(part string) bool {
	if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part {
		return false
	}
	for _, char := range part {
		if char < 0x21 || char > 0x7e || strings.ContainsRune(`\/?#[]@!$&'()*+,;=`, char) {
			return false
		}
	}
	return true
}
