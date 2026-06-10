package github

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

// ChangeProviderOptions configures GitHub branch and draft PR delivery.
type ChangeProviderOptions struct {
	BaseURL       string
	Token         string
	Client        *http.Client
	PushRemoteURL string
}

// ChangeProvider publishes ChangeSets to GitHub-owned delivery artifacts.
type ChangeProvider struct {
	baseURL       string
	token         string
	client        *http.Client
	pushRemoteURL string
}

// NewChangeProvider creates a GitHub ChangeProvider.
func NewChangeProvider(options ChangeProviderOptions) *ChangeProvider {
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
	return &ChangeProvider{
		baseURL:       baseURL,
		token:         token,
		client:        client,
		pushRemoteURL: options.PushRemoteURL,
	}
}

// PushChangeSetBranch publishes the current Workspace HEAD to the ForgeLane-managed branch.
func (provider *ChangeProvider) PushChangeSetBranch(ctx context.Context, plan workflow.ChangeBranchPushPlan) (workflow.ChangeBranchPushResult, error) {
	repo, err := parseGitHubRepositoryRef(plan.RepositoryRef)
	if err != nil {
		return workflow.ChangeBranchPushResult{}, err
	}
	remoteURL := provider.pushRemoteURL
	if remoteURL == "" {
		remoteURL = "https://github.com/" + repo + ".git"
	}
	if requiresToken(remoteURL) && provider.token == "" {
		return workflow.ChangeBranchPushResult{}, fmt.Errorf("missing GitHub token for branch push")
	}

	env, cleanup, err := provider.gitCredentialEnv(remoteURL)
	if err != nil {
		return workflow.ChangeBranchPushResult{}, err
	}
	defer cleanup()

	if err := runGitPush(ctx, plan.LocalRepositoryPath, env, remoteURL, plan.BranchRef); err != nil {
		return workflow.ChangeBranchPushResult{}, fmt.Errorf("push GitHub branch: %w", err)
	}
	return workflow.ChangeBranchPushResult{
		ChangeSetID:       plan.ChangeSetID,
		BranchProviderRef: fmt.Sprintf("github://github.com/%s/branches/%s", repo, plan.BranchRef),
		PushedCommitSHAs:  append([]string(nil), plan.CommitSHAs...),
	}, nil
}

// CreateOrUpdateDraftPR creates the first draft PR or refreshes the existing one.
func (provider *ChangeProvider) CreateOrUpdateDraftPR(ctx context.Context, plan workflow.ChangeDraftPRPlan) (workflow.ChangeDraftPRResult, error) {
	if provider.token == "" {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("missing GitHub token for draft PR delivery")
	}
	repo, err := parseGitHubRepositoryRef(plan.RepositoryRef)
	if err != nil {
		return workflow.ChangeDraftPRResult{}, err
	}

	method := http.MethodPost
	endpoint := fmt.Sprintf("%s/repos/%s/pulls", provider.baseURL, escapeRepositoryPath(repo))
	payload := map[string]any{
		"title": titleForChange(plan.WorkItemRef),
		"body":  bodyForChange(plan.WorkItemRef, plan.CommitSHAs),
		"head":  plan.BranchRef,
		"base":  plan.BaseBranch,
		"draft": true,
	}
	if plan.ExistingChangeRef != "" {
		number, err := parseGitHubPullNumber(plan.ExistingChangeRef)
		if err != nil {
			return workflow.ChangeDraftPRResult{}, err
		}
		method = http.MethodPatch
		endpoint = fmt.Sprintf("%s/repos/%s/pulls/%d", provider.baseURL, escapeRepositoryPath(repo), number)
		delete(payload, "head")
		delete(payload, "draft")
	}

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("encode GitHub draft PR request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, &body)
	if err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("create GitHub draft PR request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+provider.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	response, err := provider.client.Do(request)
	if err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("GitHub draft PR delivery failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("GitHub draft PR delivery failed: HTTP %d", response.StatusCode)
	}

	var snapshot map[string]any
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("decode GitHub draft PR response: %w", err)
	}
	number, ok := numberFromSnapshot(snapshot["number"])
	if !ok {
		return workflow.ChangeDraftPRResult{}, fmt.Errorf("decode GitHub draft PR response: missing number")
	}
	draft, _ := snapshot["draft"].(bool)
	return workflow.ChangeDraftPRResult{
		ChangeSetID:      plan.ChangeSetID,
		ChangeRef:        fmt.Sprintf("github://github.com/%s/pulls/%d", repo, number),
		Draft:            draft,
		ProviderSnapshot: compactSnapshot(snapshot, "number", "state", "draft", "html_url"),
	}, nil
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
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n*Username*) printf %%s 'x-access-token' ;;\n*) printf %%s %s ;;\nesac\n", shellSingleQuote(provider.token))
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

func parseGitHubRepositoryRef(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid GitHub repository ref %q", raw)
	}
	if parsed.Scheme != "github" || parsed.Host != "github.com" {
		return "", fmt.Errorf("invalid GitHub repository ref %q", raw)
	}
	repo := strings.Trim(parsed.Path, "/")
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || !validProviderPathPart(parts[0]) || !validProviderPathPart(parts[1]) {
		return "", fmt.Errorf("invalid GitHub repository ref %q", raw)
	}
	return repo, nil
}

func parseGitHubPullNumber(raw string) (int, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid GitHub pull ref %q", raw)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if parsed.Scheme != "github" || parsed.Host != "github.com" || len(parts) != 4 || parts[2] != "pulls" {
		return 0, fmt.Errorf("invalid GitHub pull ref %q", raw)
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("invalid GitHub pull ref %q", raw)
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
		if char < 0x21 || char > 0x7e || strings.ContainsRune(`\/?#[]@!$&'()*+,;=`, char) {
			return false
		}
	}
	return true
}
