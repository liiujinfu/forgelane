package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/liiujinfu/forgelane/internal/workitems"
)

const defaultAPIBaseURL = "https://api.github.com"

// Options configures the read-only GitHub issue provider.
type Options struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

// IssueProvider reads GitHub issues as WorkItem snapshots.
type IssueProvider struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewIssueProvider creates a read-only GitHub issue provider.
func NewIssueProvider(options Options) *IssueProvider {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	token := options.Token
	if token == "" {
		token = os.Getenv("FORGELANE_GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &IssueProvider{
		baseURL: baseURL,
		token:   token,
		client:  client,
	}
}

// GetIssue reads one GitHub issue snapshot.
func (provider *IssueProvider) GetIssue(ctx context.Context, ref workitems.ProviderRef) (workitems.ProviderIssue, error) {
	endpoint := fmt.Sprintf(
		"%s/repos/%s/issues/%d",
		provider.baseURL,
		escapeRepositoryPath(ref.RepositoryPath),
		ref.IssueNumber,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("create GitHub issue request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if provider.token != "" {
		request.Header.Set("Authorization", "Bearer "+provider.token)
	}

	response, err := provider.client.Do(request)
	if err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("GitHub provider failure reading issue %s: %w", ref.String(), err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return workitems.ProviderIssue{}, workitems.NotFoundError{ProviderRef: ref.String()}
	case http.StatusUnauthorized, http.StatusForbidden:
		return workitems.ProviderIssue{}, workitems.AuthError{ProviderRef: ref.String()}
	default:
		return workitems.ProviderIssue{}, workitems.ProviderError{
			ProviderRef: ref.String(),
			StatusCode:  response.StatusCode,
		}
	}

	var payload issuePayload
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("decode GitHub issue %s: %w", ref.String(), err)
	}
	if payload.PullRequest != nil {
		return workitems.ProviderIssue{}, workitems.NotIssueError{ProviderRef: ref.String()}
	}

	updatedAt, err := time.Parse(time.RFC3339, payload.UpdatedAt)
	if err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("decode GitHub issue updated_at for %s: %w", ref.String(), err)
	}

	status := normalizeGitHubIssueState(payload.State)
	return workitems.ProviderIssue{
		ProviderRef:         ref.String(),
		RepositoryRef:       ref.RepositoryRef(),
		Provider:            ref.Provider,
		ProviderIssueNumber: payload.Number,
		Title:               payload.Title,
		Body:                payload.body(),
		Status:              status,
		RawStatus:           payload.State,
		URL:                 payload.HTMLURL,
		ProviderUpdatedAt:   updatedAt,
	}, nil
}

// ListIssues reads issue candidates from GitHub without importing local WorkItem state.
func (provider *IssueProvider) ListIssues(ctx context.Context, input workitems.ProviderIssueListInput) ([]workitems.ProviderIssue, error) {
	endpoint, err := url.Parse(fmt.Sprintf(
		"%s/repos/%s/issues",
		provider.baseURL,
		escapeRepositoryPath(input.Repository.RepositoryPath),
	))
	if err != nil {
		return nil, fmt.Errorf("create GitHub issue list URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("state", "open")
	query.Set("per_page", "100")
	if len(input.Labels) > 0 {
		query.Set("labels", strings.Join(input.Labels, ","))
	}
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create GitHub issue list request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if provider.token != "" {
		request.Header.Set("Authorization", "Bearer "+provider.token)
	}

	response, err := provider.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GitHub provider failure listing issues %s: %w", input.Repository.String(), err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("auth or permission failure listing GitHub issues: %s", input.Repository.String())
	default:
		return nil, fmt.Errorf("GitHub provider failure listing issues %s: HTTP %d", input.Repository.String(), response.StatusCode)
	}

	var payloads []issuePayload
	if err := json.NewDecoder(response.Body).Decode(&payloads); err != nil {
		return nil, fmt.Errorf("decode GitHub issue list %s: %w", input.Repository.String(), err)
	}

	issues := make([]workitems.ProviderIssue, 0, len(payloads))
	for _, payload := range payloads {
		if payload.PullRequest != nil {
			continue
		}
		issue, err := githubIssueFromPayload(input.Repository, payload)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

func githubIssueFromPayload(repository workitems.ProviderRepositoryRef, payload issuePayload) (workitems.ProviderIssue, error) {
	ref := repository.IssueRef(payload.Number)
	updatedAt, err := time.Parse(time.RFC3339, payload.UpdatedAt)
	if err != nil {
		return workitems.ProviderIssue{}, fmt.Errorf("decode GitHub issue updated_at for %s: %w", ref.String(), err)
	}
	status := normalizeGitHubIssueState(payload.State)
	return workitems.ProviderIssue{
		ProviderRef:         ref.String(),
		RepositoryRef:       ref.RepositoryRef(),
		Provider:            ref.Provider,
		ProviderIssueNumber: payload.Number,
		Title:               payload.Title,
		Body:                payload.body(),
		Status:              status,
		RawStatus:           payload.State,
		URL:                 payload.HTMLURL,
		ProviderUpdatedAt:   updatedAt,
	}, nil
}

type issuePayload struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	Body        *string         `json:"body"`
	State       string          `json:"state"`
	HTMLURL     string          `json:"html_url"`
	UpdatedAt   string          `json:"updated_at"`
	PullRequest json.RawMessage `json:"pull_request"`
}

func (payload issuePayload) body() string {
	if payload.Body == nil {
		return ""
	}
	return *payload.Body
}

func escapeRepositoryPath(repositoryPath string) string {
	parts := strings.Split(repositoryPath, "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func normalizeGitHubIssueState(state string) string {
	switch state {
	case "open", "closed":
		return state
	default:
		return "unknown"
	}
}
