package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

// ChangeProviderOptions configures GitLab branch and draft MR delivery.
type ChangeProviderOptions struct {
	BaseURL       string
	Token         string
	Client        *http.Client
	PushRemoteURL string
}

// ChangeProvider publishes ChangeSets to GitLab-owned delivery artifacts.
type ChangeProvider struct {
	baseURL       string
	token         string
	client        *http.Client
	pushRemoteURL string
}

// NewChangeProvider creates a GitLab ChangeProvider.
func NewChangeProvider(options ChangeProviderOptions) *ChangeProvider {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	token := options.Token
	if token == "" {
		token = os.Getenv("FORGELANE_GITLAB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
	}
	client := options.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &ChangeProvider{
		baseURL:       baseURL,
		token:         token,
		client:        client,
		pushRemoteURL: options.PushRemoteURL,
	}
}

// PushChangeSetBranch publishes the current Workspace HEAD to the ForgeLane-managed branch.
func (provider *ChangeProvider) PushChangeSetBranch(ctx context.Context, plan workflow.ChangeBranchPushPlan) (workflow.ChangeBranchPushResult, error) {
	repo, err := parseGitLabRepositoryRef(plan.RepositoryRef)
	if err != nil {
		return workflow.ChangeBranchPushResult{}, err
	}
	remoteURL := provider.pushRemoteURL
	if remoteURL == "" {
		remoteURL = "https://" + repo.host + "/" + repo.path + ".git"
	}
	if requiresToken(remoteURL) && provider.token == "" {
		return workflow.ChangeBranchPushResult{}, fmt.Errorf("missing GitLab token for branch push")
	}

	env, cleanup, err := provider.gitCredentialEnv(remoteURL)
	if err != nil {
		return workflow.ChangeBranchPushResult{}, err
	}
	defer cleanup()

	if err := runGitPush(ctx, plan.LocalRepositoryPath, env, remoteURL, plan.BranchRef); err != nil {
		return workflow.ChangeBranchPushResult{}, fmt.Errorf("push GitLab branch: %w", err)
	}
	return workflow.ChangeBranchPushResult{
		ChangeSetID:       plan.ChangeSetID,
		BranchProviderRef: fmt.Sprintf("gitlab://%s/%s/branches/%s", repo.host, repo.path, plan.BranchRef),
		PushedCommitSHAs:  append([]string(nil), plan.CommitSHAs...),
	}, nil
}

// CreateOrUpdateDraftPR creates the first draft MR or refreshes the existing one.
func (provider *ChangeProvider) CreateOrUpdateDraftPR(ctx context.Context, plan workflow.ChangeDraftPRPlan) (workflow.ChangeDraftPRResult, error) {
	if provider.token == "" {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("missing GitLab token for draft MR delivery")
	}
	repo, err := parseGitLabRepositoryRef(plan.RepositoryRef)
	if err != nil {
		return workflow.ChangeDraftPRResult{}, err
	}

	method := http.MethodPost
	endpoint := fmt.Sprintf("%s/projects/%s/merge_requests", provider.apiBaseURL(repo.host), url.PathEscape(repo.path))
	payload := map[string]any{
		"title":         draftTitleForChange(plan.WorkItemRef),
		"description":   bodyForChange(plan.WorkItemRef, plan.CommitSHAs),
		"source_branch": plan.BranchRef,
		"target_branch": plan.BaseBranch,
	}
	if plan.ExistingChangeRef != "" {
		number, err := parseGitLabMergeRequestNumber(plan.ExistingChangeRef)
		if err != nil {
			return workflow.ChangeDraftPRResult{}, err
		}
		method = http.MethodPut
		endpoint = fmt.Sprintf("%s/projects/%s/merge_requests/%d", provider.apiBaseURL(repo.host), url.PathEscape(repo.path), number)
		delete(payload, "source_branch")
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("encode GitLab draft MR request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, &body)
	if err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("create GitLab draft MR request: %w", err)
	}
	request.Header.Set("PRIVATE-TOKEN", provider.token)
	request.Header.Set("Content-Type", "application/json")

	response, err := provider.client.Do(request)
	if err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("GitLab draft MR delivery failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("GitLab draft MR delivery failed: HTTP %d", response.StatusCode)
	}

	var snapshot map[string]any
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("decode GitLab draft MR response: %w", err)
	}
	number, ok := numberFromSnapshot(snapshot["iid"])
	if !ok {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("decode GitLab draft MR response: missing iid")
	}
	draft, _ := snapshot["draft"].(bool)
	return workflow.ChangeDraftPRResult{
		ChangeSetID:      plan.ChangeSetID,
		ChangeRef:        fmt.Sprintf("gitlab://%s/%s/merge_requests/%d", repo.host, repo.path, number),
		Draft:            draft,
		ProviderSnapshot: compactSnapshot(snapshot, "iid", "state", "draft", "web_url"),
	}, nil
}

func (provider *ChangeProvider) apiBaseURL(providerHost string) string {
	if provider.baseURL != "" {
		return provider.baseURL
	}
	if providerHost == "" {
		return defaultAPIBaseURL
	}
	return "https://" + providerHost + "/api/v4"
}

func (provider *ChangeProvider) gitCredentialEnv(remoteURL string) ([]string, func(), error) {
	env := append([]string(nil), os.Environ()...)
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	if !requiresToken(remoteURL) {
		return env, func() {}, nil
	}

	dir, err := os.MkdirTemp("", "forgelane-git-askpass-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create Git askpass helper: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	askpassPath := filepath.Join(dir, "askpass.sh")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n*Username*) printf %%s 'oauth2' ;;\n*) printf %%s %s ;;\nesac\n", shellSingleQuote(provider.token))
	if err := os.WriteFile(askpassPath, []byte(script), 0o700); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write Git askpass helper: %w", err)
	}
	env = append(env, "GIT_ASKPASS="+askpassPath)
	return env, cleanup, nil
}

func runGitPush(ctx context.Context, repo string, env []string, remoteURL string, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "push", remoteURL, "HEAD:refs/heads/"+branch)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

type gitLabRepositoryRef struct {
	host string
	path string
}

func parseGitLabRepositoryRef(raw string) (gitLabRepositoryRef, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return gitLabRepositoryRef{}, fmt.Errorf("invalid GitLab repository ref %q", raw)
	}
	if parsed.Scheme != "gitlab" || parsed.Host == "" {
		return gitLabRepositoryRef{}, fmt.Errorf("invalid GitLab repository ref %q", raw)
	}
	repo := strings.Trim(parsed.Path, "/")
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return gitLabRepositoryRef{}, fmt.Errorf("invalid GitLab repository ref %q", raw)
	}
	for _, part := range parts {
		if !validProviderPathPart(part) {
			return gitLabRepositoryRef{}, fmt.Errorf("invalid GitLab repository ref %q", raw)
		}
	}
	return gitLabRepositoryRef{host: parsed.Host, path: repo}, nil
}

func parseGitLabMergeRequestNumber(raw string) (int, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid GitLab merge request ref %q", raw)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if parsed.Scheme != "gitlab" || parsed.Host == "" || len(parts) < 4 || parts[len(parts)-2] != "merge_requests" {
		return 0, fmt.Errorf("invalid GitLab merge request ref %q", raw)
	}
	number, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid GitLab merge request ref %q", raw)
	}
	return number, nil
}

func requiresToken(remoteURL string) bool {
	parsed, err := url.Parse(remoteURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" || parsed.Scheme == "http"
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func titleForChange(workItemRef string) string {
	return "ForgeLane delivery for " + workItemRef
}

func draftTitleForChange(workItemRef string) string {
	return "Draft: " + titleForChange(workItemRef)
}

func bodyForChange(workItemRef string, commitSHAs []string) string {
	var builder strings.Builder
	builder.WriteString("ForgeLane draft delivery for ")
	builder.WriteString(workItemRef)
	if len(commitSHAs) > 0 {
		builder.WriteString("\n\nCommits:\n")
		for _, sha := range commitSHAs {
			builder.WriteString("- ")
			builder.WriteString(sha)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func numberFromSnapshot(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed <= 0 || typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	case int:
		return typed, typed > 0
	default:
		return 0, false
	}
}

func compactSnapshot(snapshot map[string]any, keys ...string) map[string]any {
	compact := make(map[string]any)
	for _, key := range keys {
		if value, ok := snapshot[key]; ok {
			compact[key] = value
		}
	}
	return compact
}

func validProviderPathPart(part string) bool {
	if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part {
		return false
	}
	for _, char := range part {
		if char < 0x21 || char > 0x7e || strings.ContainsRune(`\?#[]@!$&'()*+,;=`, char) {
			return false
		}
	}
	return true
}
