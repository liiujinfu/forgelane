package github

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

func TestChangeProviderPushesBranchWithGitTransport(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)

	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "ForgeLane Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("source\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "initial")
	head := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "HEAD"))

	provider := NewChangeProvider(ChangeProviderOptions{
		PushRemoteURL: remote,
	})
	result, err := provider.PushChangeSetBranch(context.Background(), workflow.ChangeBranchPushPlan{
		ChangeSetID:         1,
		RepositoryRef:       "github://github.com/owner/repo",
		LocalRepositoryPath: repo,
		BranchRef:           "forgelane/issue-123",
		CommitSHAs:          []string{head},
	})
	if err != nil {
		t.Fatalf("push branch: %v", err)
	}

	if result.BranchProviderRef != "github://github.com/owner/repo/branches/forgelane/issue-123" {
		t.Fatalf("unexpected branch provider ref %q", result.BranchProviderRef)
	}
	if got := strings.TrimSpace(gitOutput(t, remote, "rev-parse", "refs/heads/forgelane/issue-123")); got != head {
		t.Fatalf("unexpected pushed head %q, want %q", got, head)
	}
}

func TestChangeProviderRequiresTokenForHTTPSBranchPush(t *testing.T) {
	t.Setenv("FORGELANE_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	provider := NewChangeProvider(ChangeProviderOptions{})
	_, err := provider.PushChangeSetBranch(context.Background(), workflow.ChangeBranchPushPlan{
		RepositoryRef:       "github://github.com/owner/repo",
		LocalRepositoryPath: t.TempDir(),
		BranchRef:           "forgelane/issue-123",
	})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "missing GitHub token for branch push") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestChangeProviderCreatesGitHubDraftPR(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/repos/owner/repo/pulls" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer provider-token" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["head"] != "forgelane/issue-123" || body["base"] != "main" || body["draft"] != true {
			t.Fatalf("unexpected draft PR body %#v", body)
		}
		return jsonResponse(http.StatusCreated, `{
			"number": 10,
			"html_url": "https://github.com/owner/repo/pull/10",
			"state": "open",
			"draft": true
		}`)
	})
	provider := NewChangeProvider(ChangeProviderOptions{
		BaseURL: "https://api.github.test",
		Token:   "provider-token",
		Client:  client,
	})

	result, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		ChangeSetID:       1,
		WorkItemRef:       "github://github.com/owner/repo/issues/123",
		RepositoryRef:     "github://github.com/owner/repo",
		BaseBranch:        "main",
		BranchRef:         "forgelane/issue-123",
		BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
		CommitSHAs:        []string{"abc123"},
	})
	if err != nil {
		t.Fatalf("create draft PR: %v", err)
	}

	if result.ChangeRef != "github://github.com/owner/repo/pulls/10" || !result.Draft {
		t.Fatalf("unexpected draft PR result %#v", result)
	}
	if result.ProviderSnapshot["number"] != float64(10) || result.ProviderSnapshot["draft"] != true {
		t.Fatalf("unexpected provider snapshot %#v", result.ProviderSnapshot)
	}
}

func TestChangeProviderRequiresTokenForGitHubDraftPR(t *testing.T) {
	t.Setenv("FORGELANE_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	provider := NewChangeProvider(ChangeProviderOptions{})
	_, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		RepositoryRef: "github://github.com/owner/repo",
		BaseBranch:    "main",
		BranchRef:     "forgelane/issue-123",
	})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "missing GitHub token for draft PR delivery") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestChangeProviderReportsGitHubDraftPRProviderFailure(t *testing.T) {
	client := fakeHTTPClient(func(_ *http.Request) *http.Response {
		return jsonResponse(http.StatusForbidden, `{"message":"forbidden"}`)
	})
	provider := NewChangeProvider(ChangeProviderOptions{
		BaseURL: "https://api.github.test",
		Token:   "provider-token",
		Client:  client,
	})

	_, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		RepositoryRef: "github://github.com/owner/repo",
		BaseBranch:    "main",
		BranchRef:     "forgelane/issue-123",
	})
	if err == nil {
		t.Fatal("expected provider failure")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestChangeProviderUpdatesExistingGitHubDraftPR(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.Method != http.MethodPatch {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/repos/owner/repo/pulls/10" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := body["head"]; ok {
			t.Fatalf("unexpected head field on update %#v", body)
		}
		if _, ok := body["draft"]; ok {
			t.Fatalf("unexpected draft field on update %#v", body)
		}
		if body["base"] != "main" {
			t.Fatalf("unexpected update body %#v", body)
		}
		return jsonResponse(http.StatusOK, `{
			"number": 10,
			"html_url": "https://github.com/owner/repo/pull/10",
			"state": "open",
			"draft": true
		}`)
	})
	provider := NewChangeProvider(ChangeProviderOptions{
		BaseURL: "https://api.github.test",
		Token:   "provider-token",
		Client:  client,
	})

	result, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		ChangeSetID:       1,
		WorkItemRef:       "github://github.com/owner/repo/issues/123",
		RepositoryRef:     "github://github.com/owner/repo",
		BaseBranch:        "main",
		BranchRef:         "forgelane/issue-123",
		ExistingChangeRef: "github://github.com/owner/repo/pulls/10",
		CommitSHAs:        []string{"abc123"},
	})
	if err != nil {
		t.Fatalf("update draft PR: %v", err)
	}
	if result.ChangeRef != "github://github.com/owner/repo/pulls/10" || !result.Draft {
		t.Fatalf("unexpected draft PR result %#v", result)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
