package cli

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liiujinfu/forgelane/internal/workitems"
	_ "modernc.org/sqlite"
)

func executeForTest(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return executeForTestWithOptions(t, Options{}, args...)
}

func executeForTestWithOptions(t *testing.T, options Options, args ...string) (string, string, error) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	options.Stdout = &stdout
	options.Stderr = &stderr
	cmd := NewRootCommand(options)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestWorkItemImportPersistsSnapshotAndShowReadsLocalCache(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	providerUpdatedAt := time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC)
	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Persist a GitHub WorkItem snapshot",
			Body:                "Import the provider-owned issue before any AgentRun exists.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   providerUpdatedAt,
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected work item import to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Imported WorkItem github://github.com/owner/repo/issues/123",
		"Repository: github://github.com/owner/repo",
		"Issue: 123",
		"Title: Persist a GitHub WorkItem snapshot",
		"Status: open",
		"Provider updated: 2026-05-30T09:10:11Z",
		"Event: work_item.imported",
		"Event ID:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected import output to contain %q, got:\n%s", want, stdout)
		}
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected import to call provider once, got %d", fakeProvider.calls)
	}

	stdout, stderr, err = executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "show", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected work item show to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"WorkItem github://github.com/owner/repo/issues/123",
		"Repository: github://github.com/owner/repo",
		"Issue: 123",
		"Title: Persist a GitHub WorkItem snapshot",
		"Status: open",
		"Provider updated: 2026-05-30T09:10:11Z",
		"Imported:",
		"Refreshed:",
		"Body:",
		"Import the provider-owned issue before any AgentRun exists.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected show output to contain %q, got:\n%s", want, stdout)
		}
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected show to read local cache without calling provider again, got %d calls", fakeProvider.calls)
	}
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestWorkItemShowDoesNotInitializeMissingStore(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "work-items", "show", "github://github.com/owner/repo/issues/123")
	if err == nil {
		t.Fatal("expected show without a local store to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "database not initialized") {
		t.Fatalf("expected missing database error, got:\n%s", stderr)
	}
	stateDir := filepath.Join(homeDir, ".forgelane")
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("show should not create state directory %s, stat err: %v", stateDir, err)
	}
}

func TestWorkItemImportNormalizesUnsupportedStatus(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Normalize unsupported provider status",
			Body:                "Only open, closed, and unknown are persisted as normalized status.",
			Status:              "triaged",
			RawStatus:           "triaged",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected work item import to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Status: unknown") {
		t.Fatalf("expected import output to show normalized unknown status, got:\n%s", stdout)
	}

	status, rawStatus := readWorkItemStatus(t, homeDir, "github://github.com/owner/repo/issues/123")
	if status != "unknown" {
		t.Fatalf("expected normalized status unknown, got %q", status)
	}
	if rawStatus != "triaged" {
		t.Fatalf("expected raw provider status to be preserved, got %q", rawStatus)
	}
}

func TestWorkItemImportRefreshesExistingSnapshotAndAppendsEvent(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Persist a GitHub WorkItem snapshot",
			Body:                "Import the provider-owned issue before any AgentRun exists.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected first import to succeed: %v\nstderr:\n%s", err, stderr)
	}
	firstImport := readOnlyWorkItem(t, homeDir, "github://github.com/owner/repo/issues/123")

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected second import to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Event: work_item.refreshed") {
		t.Fatalf("expected second import output to describe refresh event, got:\n%s", stdout)
	}

	secondImport := readOnlyWorkItem(t, homeDir, "github://github.com/owner/repo/issues/123")
	if firstImport.id != secondImport.id {
		t.Fatalf("expected repeated import to update same WorkItem id, got first=%d second=%d", firstImport.id, secondImport.id)
	}
	if firstImport.importedAt != secondImport.importedAt {
		t.Fatalf("expected imported_at to be preserved, got first=%q second=%q", firstImport.importedAt, secondImport.importedAt)
	}
	if secondImport.refreshedAt == "" {
		t.Fatal("expected refreshed_at to be set")
	}
	assertEventTypes(t, homeDir, []string{"work_item.imported", "work_item.refreshed"})
}

func TestWorkItemImportAndShowResolveIssueNumberThroughCurrentForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--provider", "github"); err != nil {
		t.Fatalf("expected init to persist current ForgeProject: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Shorthand import",
			Body:                "Resolve numeric issue shorthand through the current ForgeProject.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "123")
	if err != nil {
		t.Fatalf("expected shorthand import to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Imported WorkItem github://github.com/owner/repo/issues/123") {
		t.Fatalf("expected import output to include canonical ProviderRef, got:\n%s", stdout)
	}

	stdout, stderr, err = executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "show", "--issue", "123")
	if err != nil {
		t.Fatalf("expected shorthand show to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "WorkItem github://github.com/owner/repo/issues/123") {
		t.Fatalf("expected show output to include canonical ProviderRef, got:\n%s", stdout)
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected shorthand show to read local cache without provider call, got %d calls", fakeProvider.calls)
	}
}

func TestWorkItemImportIssueNumberDoesNotGuessSingleForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo"); err != nil {
		t.Fatalf("expected init to persist a ForgeProject: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
		},
	}
	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "123")
	if err == nil {
		t.Fatal("expected shorthand import without origin remote to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "missing or unsupported origin remote") {
		t.Fatalf("expected missing origin error, got:\n%s", stderr)
	}
	if fakeProvider.calls != 0 {
		t.Fatalf("expected shorthand resolution failure not to call provider, got %d calls", fakeProvider.calls)
	}
}

func TestWorkItemShowSupportsExplicitLocalIDLookup(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Show by local id",
			Body:                "Local id lookup is a debugging fallback.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected import to succeed: %v\nstderr:\n%s", err, stderr)
	}
	workItem := readOnlyWorkItem(t, homeDir, "github://github.com/owner/repo/issues/123")

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "show", "--id", strconv.FormatInt(workItem.id, 10))
	if err != nil {
		t.Fatalf("expected show --id to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "WorkItem github://github.com/owner/repo/issues/123") {
		t.Fatalf("expected show output to include canonical ProviderRef, got:\n%s", stdout)
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected show --id to read local cache without provider call, got %d calls", fakeProvider.calls)
	}
}

func TestWorkItemImportRejectsPullRequestResponsesWithoutWritingState(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		err: workitems.NotIssueError{
			ProviderRef: "github://github.com/owner/repo/issues/123",
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "github://github.com/owner/repo/issues/123")
	if err == nil {
		t.Fatal("expected PR/MR issue response to fail import")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "not an issue WorkItem") {
		t.Fatalf("expected not-an-issue error, got:\n%s", stderr)
	}
	assertTableCount(t, homeDir, "work_items", 0)
	assertTableCount(t, homeDir, "events", 0)
}

func TestWorkItemImportRejectsRawGitHubWebURLBeforeProviderRead(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "https://github.com/owner/repo/issues/123")
	if err == nil {
		t.Fatal("expected raw GitHub web URL to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "raw GitHub issue URLs are not supported") {
		t.Fatalf("expected unsupported provider error, got:\n%s", stderr)
	}
	if fakeProvider.calls != 0 {
		t.Fatalf("expected invalid input not to call provider, got %d calls", fakeProvider.calls)
	}
}

func TestRootHelpShowsSkeletonCommands(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "--help")
	if err != nil {
		t.Fatalf("expected help to succeed: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}

	for _, want := range []string{
		"ForgeLane is an agentic software delivery control plane.",
		"init",
		"version",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected help output to contain %q, got:\n%s", want, stdout)
		}
	}

	if strings.Contains(stdout, "completion") {
		t.Fatalf("expected help output not to expose completion command, got:\n%s", stdout)
	}
}

func TestVersionCommandShowsDevelopmentDefaults(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "version")
	if err != nil {
		t.Fatalf("expected version command to succeed: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}

	for _, want := range []string{
		"Version: dev",
		"Commit: unknown",
		"Date: unknown",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected version output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestRootVersionFlagUsesCobraVersionWiring(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "--version")
	if err != nil {
		t.Fatalf("expected --version to succeed: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "forgelane version dev") {
		t.Fatalf("expected cobra version output, got:\n%s", stdout)
	}
}

func TestUnknownCommandWritesErrorToStderr(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "definitely-not-a-command")
	if err == nil {
		t.Fatal("expected unknown command to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, `unknown command "definitely-not-a-command" for "forgelane"`) {
		t.Fatalf("expected unknown command error on stderr, got:\n%s", stderr)
	}
}

func TestInitWithRepoURLPersistsNormalizedGitHubForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitWithGitHubRepoShorthandPersistsSameForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github", "--repo", "owner/repo")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitInfersGitHubForgeProjectFromOriginRemote(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitAcceptsSupportedGitHubRemoteURLForms(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
	}{
		{name: "https", repoURL: "https://github.com/owner/repo"},
		{name: "https git suffix", repoURL: "https://github.com/owner/repo.git"},
		{name: "https query and fragment ignored", repoURL: "https://github.com/owner/repo?tab=readme#readme"},
		{name: "ssh scp", repoURL: "git@github.com:owner/repo.git"},
		{name: "ssh url", repoURL: "ssh://git@github.com/owner/repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			homeDir := t.TempDir()
			withWorkingDir(t, workingDir)
			withHomeDir(t, homeDir)

			_, stderr, err := executeForTest(t, "init", "--repo-url", tt.repoURL)
			if err != nil {
				t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
			}
			if stderr != "" {
				t.Fatalf("expected no stderr, got %q", stderr)
			}

			assertNoRepoLocalConfig(t, workingDir)
			assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
		})
	}
}

func TestInitRejectsInvalidInputsWithClearErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "unsupported provider",
			args:    []string{"init", "--provider", "gitlab", "--repo", "owner/repo"},
			wantErr: `unsupported provider "gitlab"`,
		},
		{
			name:    "invalid repo ref",
			args:    []string{"init", "--provider", "github", "--repo", "owner"},
			wantErr: `invalid GitHub repository ref "owner"`,
		},
		{
			name:    "repo ref with owner whitespace",
			args:    []string{"init", "--provider", "github", "--repo", "bad owner/repo"},
			wantErr: `invalid GitHub repository ref "bad owner/repo"`,
		},
		{
			name:    "repo ref with repository whitespace",
			args:    []string{"init", "--provider", "github", "--repo", "owner/bad repo"},
			wantErr: `invalid GitHub repository ref "owner/bad repo"`,
		},
		{
			name:    "repo ref with dot segment",
			args:    []string{"init", "--provider", "github", "--repo", "owner/."},
			wantErr: `invalid GitHub repository ref "owner/."`,
		},
		{
			name:    "unsupported remote url",
			args:    []string{"init", "--repo-url", "https://gitlab.com/owner/repo"},
			wantErr: `unsupported GitHub repository URL "https://gitlab.com/owner/repo"`,
		},
		{
			name:    "branch webpage url",
			args:    []string{"init", "--repo-url", "https://github.com/owner/repo/tree/main"},
			wantErr: `invalid GitHub repository URL "https://github.com/owner/repo/tree/main"`,
		},
		{
			name:    "ambiguous shorthand in repo url",
			args:    []string{"init", "--repo-url", "owner/repo"},
			wantErr: `unsupported GitHub repository URL "owner/repo"`,
		},
		{
			name:    "missing origin asks for repo url",
			args:    []string{"init", "--provider", "github"},
			wantErr: "missing or unsupported origin remote; pass --repo-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withWorkingDir(t, t.TempDir())
			withHomeDir(t, t.TempDir())

			stdout, stderr, err := executeForTest(t, tt.args...)
			if err == nil {
				t.Fatal("expected init to fail")
			}
			if stdout != "" {
				t.Fatalf("expected no stdout, got %q", stdout)
			}
			if !strings.Contains(stderr, tt.wantErr) {
				t.Fatalf("expected stderr to contain %q, got:\n%s", tt.wantErr, stderr)
			}
		})
	}
}

func TestInitInferenceInspectsOnlyOrigin(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://gitlab.com/owner/repo")
	runGit(t, workingDir, "remote", "add", "upstream", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, t.TempDir())

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github")
	if err == nil {
		t.Fatal("expected init to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, `unsupported GitHub repository URL "https://gitlab.com/owner/repo"`) {
		t.Fatalf("expected origin-only failure, got:\n%s", stderr)
	}
}

func TestInitExplicitRepositoryWinsOverInferredOrigin(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/origin/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "github", "--repo", "explicit/repo")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/explicit/repo") {
		t.Fatalf("expected init output to describe explicit ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/explicit/repo"})
}

func TestInitDoesNotInferOriginWithoutProvider(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, t.TempDir())

	stdout, stderr, err := executeForTest(t, "init")
	if err == nil {
		t.Fatal("expected init to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "missing repository; pass --repo-url") {
		t.Fatalf("expected missing repository error, got:\n%s", stderr)
	}
}

func TestInitIsIdempotentForMatchingForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo"); err != nil {
		t.Fatalf("expected first init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	stdout, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("expected matching init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestInitPersistsMultipleForgeProjectsInGlobalState(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo"); err != nil {
		t.Fatalf("expected first init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/other/repo")
	if err != nil {
		t.Fatalf("expected second ForgeProject init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/other/repo") {
		t.Fatalf("expected second ForgeProject init output, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{
		"github://github.com/owner/repo",
		"github://github.com/other/repo",
	})
}

func withWorkingDir(t *testing.T, workingDir string) {
	t.Helper()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current working directory: %v", err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
}

func runGit(t *testing.T, workingDir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = workingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func withHomeDir(t *testing.T, homeDir string) {
	t.Helper()

	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
}

func assertNoRepoLocalConfig(t *testing.T, workingDir string) {
	t.Helper()

	configPath := filepath.Join(workingDir, ".forgelane", "repository.json")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected no repo-local repository config at %s, stat err: %v", configPath, err)
	}
}

func assertForgeProjects(t *testing.T, homeDir string, wantRefs []string) {
	t.Helper()
	wantRefs = slices.Clone(wantRefs)
	slices.Sort(wantRefs)

	dbPath := filepath.Join(homeDir, ".forgelane", "forgelane.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open ForgeLane database: %v", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT provider_ref FROM forge_projects ORDER BY provider_ref")
	if err != nil {
		t.Fatalf("query ForgeProjects: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var providerRef string
		if err := rows.Scan(&providerRef); err != nil {
			t.Fatalf("scan ForgeProject: %v", err)
		}
		got = append(got, providerRef)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ForgeProjects: %v", err)
	}

	if strings.Join(got, "\n") != strings.Join(wantRefs, "\n") {
		t.Fatalf("unexpected ForgeProjects:\n got: %q\nwant: %q", got, wantRefs)
	}
}

type readWorkItem struct {
	id          int64
	importedAt  string
	refreshedAt string
}

func readOnlyWorkItem(t *testing.T, homeDir string, providerRef string) readWorkItem {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	var workItem readWorkItem
	err := db.QueryRow(
		"SELECT id, imported_at, refreshed_at FROM work_items WHERE provider_ref = ?",
		providerRef,
	).Scan(&workItem.id, &workItem.importedAt, &workItem.refreshedAt)
	if err != nil {
		t.Fatalf("query WorkItem %s: %v", providerRef, err)
	}
	return workItem
}

func readWorkItemStatus(t *testing.T, homeDir string, providerRef string) (string, string) {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	var status string
	var rawStatus string
	err := db.QueryRow(
		"SELECT status, provider_status_raw FROM work_items WHERE provider_ref = ?",
		providerRef,
	).Scan(&status, &rawStatus)
	if err != nil {
		t.Fatalf("query WorkItem status %s: %v", providerRef, err)
	}
	return status, rawStatus
}

func assertEventTypes(t *testing.T, homeDir string, wantTypes []string) {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	rows, err := db.Query("SELECT type FROM events ORDER BY id")
	if err != nil {
		t.Fatalf("query Event types: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("scan Event type: %v", err)
		}
		got = append(got, eventType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate Event types: %v", err)
	}
	if strings.Join(got, "\n") != strings.Join(wantTypes, "\n") {
		t.Fatalf("unexpected Event types:\n got: %q\nwant: %q", got, wantTypes)
	}
}

func assertTableCount(t *testing.T, homeDir string, table string, want int) {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("unexpected %s count: got %d, want %d", table, got, want)
	}
}

func openStateDB(t *testing.T, homeDir string) *sql.DB {
	t.Helper()

	dbPath := filepath.Join(homeDir, ".forgelane", "forgelane.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open ForgeLane database: %v", err)
	}
	return db
}

type recordingWorkItemProvider struct {
	issue workitems.ProviderIssue
	err   error
	calls int
}

func (provider *recordingWorkItemProvider) GetIssue(_ context.Context, providerRef workitems.ProviderRef) (workitems.ProviderIssue, error) {
	provider.calls++
	if provider.err != nil {
		return workitems.ProviderIssue{}, provider.err
	}
	if got := providerRef.String(); got != provider.issue.ProviderRef {
		return workitems.ProviderIssue{}, workitems.NotFoundError{ProviderRef: got}
	}
	return provider.issue, nil
}
