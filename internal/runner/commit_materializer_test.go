package runner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liiujinfu/forgelane/internal/runner"
	"github.com/liiujinfu/forgelane/internal/workflow"
)

func TestGitCommitMaterializerCommitsRepositoryDeltaOnly(t *testing.T) {
	workspace := newWorkspaceRepo(t)
	writeFile(t, filepath.Join(workspace.Paths.Repo, "tracked.txt"), "tracked after\n")
	if err := os.Remove(filepath.Join(workspace.Paths.Repo, "deleted.txt")); err != nil {
		t.Fatalf("delete tracked file: %v", err)
	}
	writeFile(t, filepath.Join(workspace.Paths.Repo, "new.txt"), "untracked\n")
	writeFile(t, filepath.Join(workspace.Paths.Logs, "stdout.log"), "log output\n")
	writeFile(t, filepath.Join(workspace.Paths.Artifacts, "artifact.txt"), "artifact\n")
	writeFile(t, filepath.Join(workspace.Paths.Tmp, "secret.txt"), "secret\n")
	writeFile(t, filepath.Join(workspace.Paths.Root, "credentials.json"), "credential\n")
	writeFile(t, filepath.Join(workspace.Paths.Root, "runner-metadata.json"), "metadata\n")

	materializer := runner.GitCommitMaterializer{}
	snapshot, err := materializer.SnapshotRepository(context.Background(), workspace)
	if err != nil {
		t.Fatalf("snapshot repository: %v", err)
	}
	result, err := materializer.MaterializeRepositoryChanges(context.Background(), workspace, snapshot)
	if err != nil {
		t.Fatalf("materialize repository changes: %v", err)
	}
	if len(result.CommitRefs) != 1 {
		t.Fatalf("expected one local commit ref, got %#v", result.CommitRefs)
	}

	commit := result.CommitRefs[0]
	if commit.SHA == "" {
		t.Fatalf("expected commit SHA, got %#v", commit)
	}
	if commit.Subject != "Materialize AgentRun 42 repository changes" {
		t.Fatalf("unexpected commit subject %q", commit.Subject)
	}
	if commit.AuthorName != "ForgeLane" || commit.AuthorEmail != "forgelane@localhost" {
		t.Fatalf("expected deterministic ForgeLane identity, got %#v", commit)
	}

	status := gitOutput(t, workspace.Paths.Repo, "status", "--porcelain=v1")
	if status != "" {
		t.Fatalf("expected clean repository after materialization, got:\n%s", status)
	}

	tree := gitOutput(t, workspace.Paths.Repo, "ls-tree", "-r", "--name-only", "HEAD")
	for _, want := range []string{"new.txt", "tracked.txt"} {
		if !strings.Contains(tree, want+"\n") {
			t.Fatalf("expected committed tree to contain %s, got:\n%s", want, tree)
		}
	}
	for _, unwanted := range []string{"deleted.txt", "logs/stdout.log", "artifacts/artifact.txt", "tmp/secret.txt", "credentials.json", "runner-metadata.json"} {
		if strings.Contains(tree, unwanted+"\n") {
			t.Fatalf("expected committed tree to exclude %s, got:\n%s", unwanted, tree)
		}
	}
}

func TestGitCommitMaterializerReportsAgentCreatedCommits(t *testing.T) {
	workspace := newWorkspaceRepo(t)
	materializer := runner.GitCommitMaterializer{}
	snapshot, err := materializer.SnapshotRepository(context.Background(), workspace)
	if err != nil {
		t.Fatalf("snapshot repository: %v", err)
	}

	writeFile(t, filepath.Join(workspace.Paths.Repo, "agent-commit.txt"), "agent commit\n")
	git(t, workspace.Paths.Repo, "add", "agent-commit.txt")
	git(t, workspace.Paths.Repo, "commit", "-m", "agent-created commit")

	result, err := materializer.MaterializeRepositoryChanges(context.Background(), workspace, snapshot)
	if err != nil {
		t.Fatalf("materialize repository changes: %v", err)
	}
	if len(result.CommitRefs) != 1 {
		t.Fatalf("expected one agent-created commit ref, got %#v", result.CommitRefs)
	}
	if result.CommitRefs[0].Subject != "agent-created commit" {
		t.Fatalf("expected agent-created commit subject, got %#v", result.CommitRefs[0])
	}
	if result.CommitRefs[0].AuthorEmail != "setup@example.com" {
		t.Fatalf("expected original agent commit author to be preserved, got %#v", result.CommitRefs[0])
	}
}

func TestGitCommitMaterializerSkipsDeliveryForCleanRepository(t *testing.T) {
	workspace := newWorkspaceRepo(t)
	materializer := runner.GitCommitMaterializer{}
	snapshot, err := materializer.SnapshotRepository(context.Background(), workspace)
	if err != nil {
		t.Fatalf("snapshot repository: %v", err)
	}
	before := strings.TrimSpace(gitOutput(t, workspace.Paths.Repo, "rev-parse", "HEAD"))

	result, err := materializer.MaterializeRepositoryChanges(context.Background(), workspace, snapshot)
	if err != nil {
		t.Fatalf("materialize repository changes: %v", err)
	}
	if len(result.CommitRefs) != 0 {
		t.Fatalf("expected no commit refs for clean repository, got %#v", result.CommitRefs)
	}
	if !result.DeliverySkipped || result.DeliverySkipReason != "no_repository_changes" {
		t.Fatalf("expected no-change delivery skip, got %#v", result)
	}
	after := strings.TrimSpace(gitOutput(t, workspace.Paths.Repo, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("expected no local commit for clean repository, before %s after %s", before, after)
	}
}

func TestGitCommitMaterializerRejectsWorkspaceMetadataInsideRepo(t *testing.T) {
	workspace := newWorkspaceRepo(t)
	workspace.Paths.Logs = filepath.Join(workspace.Paths.Repo, "logs")

	_, err := (runner.GitCommitMaterializer{}).SnapshotRepository(context.Background(), workspace)
	if err == nil {
		t.Fatal("expected metadata path inside repo to fail")
	}
	if !strings.Contains(err.Error(), "must stay outside repository") {
		t.Fatalf("expected metadata boundary error, got %v", err)
	}
}

func newWorkspaceRepo(t *testing.T) workflow.Workspace {
	t.Helper()

	root := t.TempDir()
	workspace := workflow.Workspace{
		AgentRunID: 42,
		Paths: workflow.WorkspacePaths{
			Root:      root,
			Repo:      filepath.Join(root, "repo"),
			Logs:      filepath.Join(root, "logs"),
			Artifacts: filepath.Join(root, "artifacts"),
			Tmp:       filepath.Join(root, "tmp"),
		},
	}
	for _, dir := range []string{workspace.Paths.Repo, workspace.Paths.Logs, workspace.Paths.Artifacts, workspace.Paths.Tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	git(t, workspace.Paths.Repo, "init")
	git(t, workspace.Paths.Repo, "config", "user.email", "setup@example.com")
	git(t, workspace.Paths.Repo, "config", "user.name", "Setup User")
	writeFile(t, filepath.Join(workspace.Paths.Repo, "tracked.txt"), "tracked before\n")
	writeFile(t, filepath.Join(workspace.Paths.Repo, "deleted.txt"), "delete me\n")
	git(t, workspace.Paths.Repo, "add", ".")
	git(t, workspace.Paths.Repo, "commit", "-m", "initial")
	return workspace
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
