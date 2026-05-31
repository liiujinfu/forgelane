package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

const (
	forgeLaneCommitAuthorName  = "ForgeLane"
	forgeLaneCommitAuthorEmail = "forgelane@localhost"
)

// GitCommitMaterializer turns local Workspace repository changes into a ForgeLane-owned commit.
type GitCommitMaterializer struct{}

// SnapshotRepository captures the Workspace repository HEAD before agent execution.
func (GitCommitMaterializer) SnapshotRepository(ctx context.Context, workspace workflow.Workspace) (workflow.RepositorySnapshot, error) {
	if err := validateWorkspaceMetadataOutsideRepo(workspace); err != nil {
		return workflow.RepositorySnapshot{}, err
	}
	sha, err := runGit(ctx, workspace.Paths.Repo, "rev-parse", "HEAD")
	if err != nil {
		return workflow.RepositorySnapshot{}, fmt.Errorf("snapshot Workspace repository HEAD: %w", err)
	}
	return workflow.RepositorySnapshot{HeadSHA: strings.TrimSpace(string(sha))}, nil
}

// MaterializeRepositoryChanges commits tracked and untracked project files under repo/.
func (GitCommitMaterializer) MaterializeRepositoryChanges(ctx context.Context, workspace workflow.Workspace, snapshot workflow.RepositorySnapshot) (workflow.RepositoryChangeMaterialization, error) {
	if err := validateWorkspaceMetadataOutsideRepo(workspace); err != nil {
		return workflow.RepositoryChangeMaterialization{}, err
	}
	changed, err := repositoryHasChanges(ctx, workspace.Paths.Repo)
	if err != nil {
		return workflow.RepositoryChangeMaterialization{}, err
	}
	if changed {
		if _, err := runGit(ctx, workspace.Paths.Repo, "add", "-A", "."); err != nil {
			return workflow.RepositoryChangeMaterialization{}, fmt.Errorf("stage Workspace repository changes: %w", err)
		}
		staged, err := repositoryHasStagedChanges(ctx, workspace.Paths.Repo)
		if err != nil {
			return workflow.RepositoryChangeMaterialization{}, err
		}
		if staged {
			subject := fmt.Sprintf("Materialize AgentRun %d repository changes", workspace.AgentRunID)
			if _, err := runGitWithForgeLaneIdentity(ctx, workspace.Paths.Repo, "commit", "--no-gpg-sign", "-m", subject); err != nil {
				return workflow.RepositoryChangeMaterialization{}, fmt.Errorf("commit Workspace repository changes: %w", err)
			}
		}
	}

	commitRefs, err := commitRefsAfter(ctx, workspace.Paths.Repo, snapshot.HeadSHA)
	if err != nil {
		return workflow.RepositoryChangeMaterialization{}, err
	}
	return workflow.RepositoryChangeMaterialization{CommitRefs: commitRefs}, nil
}

func commitRefsAfter(ctx context.Context, repo string, baseSHA string) ([]workflow.CommitRefPlan, error) {
	if baseSHA == "" {
		return nil, nil
	}
	output, err := runGit(ctx, repo, "rev-list", "--reverse", baseSHA+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("list materialized commit refs: %w", err)
	}

	var refs []workflow.CommitRefPlan
	for _, sha := range strings.Fields(string(output)) {
		subject, err := runGit(ctx, repo, "log", "-1", "--pretty=%s", sha)
		if err != nil {
			return nil, fmt.Errorf("read materialized commit subject: %w", err)
		}
		authorName, err := runGit(ctx, repo, "log", "-1", "--pretty=%an", sha)
		if err != nil {
			return nil, fmt.Errorf("read materialized commit author name: %w", err)
		}
		authorEmail, err := runGit(ctx, repo, "log", "-1", "--pretty=%ae", sha)
		if err != nil {
			return nil, fmt.Errorf("read materialized commit author email: %w", err)
		}
		refs = append(refs, workflow.CommitRefPlan{
			SHA:         sha,
			Subject:     strings.TrimSpace(string(subject)),
			AuthorName:  strings.TrimSpace(string(authorName)),
			AuthorEmail: strings.TrimSpace(string(authorEmail)),
		})
	}
	return refs, nil
}

func validateWorkspaceMetadataOutsideRepo(workspace workflow.Workspace) error {
	repo, err := filepath.Abs(workspace.Paths.Repo)
	if err != nil {
		return fmt.Errorf("resolve Workspace repository path: %w", err)
	}
	for label, path := range map[string]string{
		"logs":      workspace.Paths.Logs,
		"artifacts": workspace.Paths.Artifacts,
		"tmp":       workspace.Paths.Tmp,
	} {
		abs, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve Workspace %s path: %w", label, err)
		}
		rel, err := filepath.Rel(repo, abs)
		if err != nil {
			return fmt.Errorf("compare Workspace %s path to repository: %w", label, err)
		}
		if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return fmt.Errorf("Workspace %s path %s must stay outside repository %s", label, path, workspace.Paths.Repo)
		}
	}
	return nil
}

func repositoryHasChanges(ctx context.Context, repo string) (bool, error) {
	output, err := runGit(ctx, repo, "status", "--porcelain=v1", "-z")
	if err != nil {
		return false, fmt.Errorf("inspect Workspace repository changes: %w", err)
	}
	return len(output) > 0, nil
}

func repositoryHasStagedChanges(ctx context.Context, repo string) (bool, error) {
	output, err := runGit(ctx, repo, "diff", "--cached", "--quiet", "--exit-code")
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*gitExitError); ok && exitErr.ExitCode == 1 {
		return true, nil
	}
	return false, fmt.Errorf("inspect staged Workspace repository changes: %w\n%s", err, output)
}

func runGit(ctx context.Context, repo string, args ...string) ([]byte, error) {
	return runGitCommand(ctx, repo, nil, args...)
}

func runGitWithForgeLaneIdentity(ctx context.Context, repo string, args ...string) ([]byte, error) {
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+forgeLaneCommitAuthorName,
		"GIT_AUTHOR_EMAIL="+forgeLaneCommitAuthorEmail,
		"GIT_COMMITTER_NAME="+forgeLaneCommitAuthorName,
		"GIT_COMMITTER_EMAIL="+forgeLaneCommitAuthorEmail,
	)
	return runGitCommand(ctx, repo, env, args...)
}

func runGitCommand(ctx context.Context, repo string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	if env != nil {
		cmd.Env = env
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := append(stdout.Bytes(), stderr.Bytes()...)
	if err != nil {
		return output, gitCommandError(err)
	}
	return output, nil
}

type gitExitError struct {
	Err      error
	ExitCode int
}

func (err *gitExitError) Error() string {
	return err.Err.Error()
}

func (err *gitExitError) Unwrap() error {
	return err.Err
}

func gitCommandError(err error) error {
	if exitErr, ok := err.(*exec.ExitError); ok {
		return &gitExitError{Err: err, ExitCode: exitErr.ExitCode()}
	}
	return err
}
