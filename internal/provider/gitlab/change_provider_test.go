package gitlab

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
		RepositoryRef:       "gitlab://gitlab.com/group/subgroup/project",
		LocalRepositoryPath: repo,
		BranchRef:           "forgelane/issue-456",
		CommitSHAs:          []string{head},
	})
	if err != nil {
		t.Fatalf("push branch: %v", err)
	}

	if result.BranchProviderRef != "gitlab://gitlab.com/group/subgroup/project/branches/forgelane/issue-456" {
		t.Fatalf("unexpected branch provider ref %q", result.BranchProviderRef)
	}
	if got := strings.TrimSpace(gitOutput(t, remote, "rev-parse", "refs/heads/forgelane/issue-456")); got != head {
		t.Fatalf("unexpected pushed head %q, want %q", got, head)
	}
}

func TestChangeProviderPushesSelfHostedBranchWithCanonicalHost(t *testing.T) {
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
		RepositoryRef:       "gitlab://gitlab.example.com/group/subgroup/project",
		LocalRepositoryPath: repo,
		BranchRef:           "forgelane/issue-456",
		CommitSHAs:          []string{head},
	})
	if err != nil {
		t.Fatalf("push branch: %v", err)
	}

	if result.BranchProviderRef != "gitlab://gitlab.example.com/group/subgroup/project/branches/forgelane/issue-456" {
		t.Fatalf("unexpected branch provider ref %q", result.BranchProviderRef)
	}
}

func TestChangeProviderRequiresTokenForHTTPSBranchPush(t *testing.T) {
	t.Setenv("FORGELANE_GITLAB_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")

	provider := NewChangeProvider(ChangeProviderOptions{})
	_, err := provider.PushChangeSetBranch(context.Background(), workflow.ChangeBranchPushPlan{
		RepositoryRef:       "gitlab://gitlab.com/group/subgroup/project",
		LocalRepositoryPath: t.TempDir(),
		BranchRef:           "forgelane/issue-456",
	})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "missing GitLab token for branch push") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestChangeProviderCreatesGitLabDraftMergeRequest(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fsubgroup%2Fproject/merge_requests" {
			t.Fatalf("unexpected path %s", r.URL.EscapedPath())
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "provider-token" {
			t.Fatalf("unexpected PRIVATE-TOKEN header %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["source_branch"] != "forgelane/issue-456" || body["target_branch"] != "main" || !strings.HasPrefix(body["title"].(string), "Draft: ") {
			t.Fatalf("unexpected draft MR body %#v", body)
		}
		if _, ok := body["draft"]; ok {
			t.Fatalf("unexpected unsupported draft request field %#v", body)
		}
		return jsonResponse(http.StatusCreated, `{
			"iid": 11,
			"web_url": "https://gitlab.com/group/subgroup/project/-/merge_requests/11",
			"state": "opened",
			"draft": true
		}`)
	})
	provider := NewChangeProvider(ChangeProviderOptions{
		BaseURL: "https://gitlab.test/api/v4",
		Token:   "provider-token",
		Client:  client,
	})

	result, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		ChangeSetID:       1,
		WorkItemRef:       "gitlab://gitlab.com/group/subgroup/project/issues/456",
		RepositoryRef:     "gitlab://gitlab.com/group/subgroup/project",
		BaseBranch:        "main",
		BranchRef:         "forgelane/issue-456",
		BranchProviderRef: "gitlab://gitlab.com/group/subgroup/project/branches/forgelane/issue-456",
		CommitSHAs:        []string{"abc123"},
	})
	if err != nil {
		t.Fatalf("create draft MR: %v", err)
	}

	if result.ChangeRef != "gitlab://gitlab.com/group/subgroup/project/merge_requests/11" || !result.Draft {
		t.Fatalf("unexpected draft MR result %#v", result)
	}
	if result.ProviderSnapshot["iid"] != float64(11) || result.ProviderSnapshot["draft"] != true {
		t.Fatalf("unexpected provider snapshot %#v", result.ProviderSnapshot)
	}
}

func TestChangeProviderCreatesSelfHostedGitLabDraftMergeRequest(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Scheme != "https" || r.URL.Host != "gitlab.example.com" {
			t.Fatalf("unexpected request URL %s", r.URL.String())
		}
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fsubgroup%2Fproject/merge_requests" {
			t.Fatalf("unexpected path %s", r.URL.EscapedPath())
		}
		return jsonResponse(http.StatusCreated, `{
			"iid": 11,
			"web_url": "https://gitlab.example.com/group/subgroup/project/-/merge_requests/11",
			"state": "opened",
			"draft": true
		}`)
	})
	provider := NewChangeProvider(ChangeProviderOptions{
		Token:  "provider-token",
		Client: client,
	})

	result, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		ChangeSetID:       1,
		WorkItemRef:       "gitlab://gitlab.example.com/group/subgroup/project/issues/456",
		RepositoryRef:     "gitlab://gitlab.example.com/group/subgroup/project",
		BaseBranch:        "main",
		BranchRef:         "forgelane/issue-456",
		BranchProviderRef: "gitlab://gitlab.example.com/group/subgroup/project/branches/forgelane/issue-456",
		CommitSHAs:        []string{"abc123"},
	})
	if err != nil {
		t.Fatalf("create draft MR: %v", err)
	}

	if result.ChangeRef != "gitlab://gitlab.example.com/group/subgroup/project/merge_requests/11" || !result.Draft {
		t.Fatalf("unexpected draft MR result %#v", result)
	}
}

func TestChangeProviderRequiresTokenForGitLabDraftMergeRequest(t *testing.T) {
	t.Setenv("FORGELANE_GITLAB_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")

	provider := NewChangeProvider(ChangeProviderOptions{})
	_, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		RepositoryRef: "gitlab://gitlab.com/group/subgroup/project",
		BaseBranch:    "main",
		BranchRef:     "forgelane/issue-456",
	})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "missing GitLab token for draft MR delivery") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestChangeProviderReportsGitLabDraftMergeRequestProviderFailure(t *testing.T) {
	client := fakeHTTPClient(func(_ *http.Request) *http.Response {
		return jsonResponse(http.StatusForbidden, `{"message":"forbidden"}`)
	})
	provider := NewChangeProvider(ChangeProviderOptions{
		BaseURL: "https://gitlab.test/api/v4",
		Token:   "provider-token",
		Client:  client,
	})

	_, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		RepositoryRef: "gitlab://gitlab.com/group/subgroup/project",
		BaseBranch:    "main",
		BranchRef:     "forgelane/issue-456",
	})
	if err == nil {
		t.Fatal("expected provider failure")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestChangeProviderUpdatesExistingGitLabDraftMergeRequest(t *testing.T) {
	client := fakeHTTPClient(func(r *http.Request) *http.Response {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fsubgroup%2Fproject/merge_requests/11" {
			t.Fatalf("unexpected path %s", r.URL.EscapedPath())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := body["source_branch"]; ok {
			t.Fatalf("unexpected source_branch field on update %#v", body)
		}
		if _, ok := body["draft"]; ok {
			t.Fatalf("unexpected unsupported draft request field %#v", body)
		}
		title, ok := body["title"].(string)
		if !ok || !strings.HasPrefix(title, "Draft: ") || body["target_branch"] != "main" {
			t.Fatalf("unexpected update body %#v", body)
		}
		return jsonResponse(http.StatusOK, `{
			"iid": 11,
			"web_url": "https://gitlab.com/group/subgroup/project/-/merge_requests/11",
			"state": "opened",
			"draft": true
		}`)
	})
	provider := NewChangeProvider(ChangeProviderOptions{
		BaseURL: "https://gitlab.test/api/v4",
		Token:   "provider-token",
		Client:  client,
	})

	result, err := provider.CreateOrUpdateDraftPR(context.Background(), workflow.ChangeDraftPRPlan{
		ChangeSetID:       1,
		WorkItemRef:       "gitlab://gitlab.com/group/subgroup/project/issues/456",
		RepositoryRef:     "gitlab://gitlab.com/group/subgroup/project",
		BaseBranch:        "main",
		BranchRef:         "forgelane/issue-456",
		ExistingChangeRef: "gitlab://gitlab.com/group/subgroup/project/merge_requests/11",
		CommitSHAs:        []string{"abc123"},
	})
	if err != nil {
		t.Fatalf("update draft MR: %v", err)
	}
	if result.ChangeRef != "gitlab://gitlab.com/group/subgroup/project/merge_requests/11" || !result.Draft {
		t.Fatalf("unexpected draft MR result %#v", result)
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
