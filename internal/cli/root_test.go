package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liiujinfu/forgelane/internal/workflow"
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

func TestIssueListReadyUsesDefaultReadinessLabelWithoutWritingWorkItems(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected bare init to persist current ForgeProject: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		listIssues: []workitems.ProviderIssue{
			{
				ProviderRef:         "github://github.com/owner/repo/issues/123",
				RepositoryRef:       "github://github.com/owner/repo",
				Provider:            "github",
				ProviderIssueNumber: 123,
				Title:               "Ready implementation slice",
				Status:              "open",
				RawStatus:           "open",
				URL:                 "https://github.com/owner/repo/issues/123",
				ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
			},
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "issue", "list", "--ready")
	if err != nil {
		t.Fatalf("expected ready issue list to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Ready issues for github://github.com/owner/repo",
		"#123 Ready implementation slice",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected issue list output to contain %q, got:\n%s", want, stdout)
		}
	}
	if fakeProvider.calls != 0 {
		t.Fatalf("expected issue list not to import selected issues, got %d GetIssue calls", fakeProvider.calls)
	}
	if fakeProvider.listCalls != 1 {
		t.Fatalf("expected one provider issue list call, got %d", fakeProvider.listCalls)
	}
	if got := fakeProvider.lastList.Repository.String(); got != "github://github.com/owner/repo" {
		t.Fatalf("unexpected issue list repository %q", got)
	}
	if strings.Join(fakeProvider.lastList.Labels, ",") != "ready-for-agent" {
		t.Fatalf("expected default ready-for-agent label, got %#v", fakeProvider.lastList.Labels)
	}
	assertTableCount(t, homeDir, "work_items", 0)
	assertTableCount(t, homeDir, "agent_runs", 0)
	assertTableCount(t, homeDir, "events", 0)
}

func TestIssueListReadyUsesWorkflowContractReadinessLabel(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	contract := `{
  "version": 1,
  "agent": {"default_preset": "harmless-echo"},
  "tracker": {"labels": {"ready_for_agent": "agent-ready"}},
  "verification": {"test_command": "go test ./...", "evidence": [], "manual_review": []},
  "approvals": {"provider_mutations": "", "privileged_actions": ""}
}
`
	if err := os.WriteFile(filepath.Join(workingDir, "forgelane.workflow.json"), []byte(contract), 0o644); err != nil {
		t.Fatalf("write workflow contract: %v", err)
	}
	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected bare init to persist current ForgeProject: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{}
	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "issue", "list", "--ready")
	if err != nil {
		t.Fatalf("expected ready issue list to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "No ready issues for github://github.com/owner/repo") {
		t.Fatalf("expected empty ready issue output, got:\n%s", stdout)
	}
	if strings.Join(fakeProvider.lastList.Labels, ",") != "agent-ready" {
		t.Fatalf("expected workflow contract ready label, got %#v", fakeProvider.lastList.Labels)
	}
	assertTableCount(t, homeDir, "work_items", 0)
}

func TestIssueStartImportsSelectedWorkItemAndUsesDraftPRDeliveryPath(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected bare init to persist current ForgeProject: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Start issue-first delivery",
			Body:                "Issue start should reuse the existing delivery path.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true},
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "issue", "start", "--agent-preset", "harmless-echo", "123")
	if err != nil {
		t.Fatalf("expected issue start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Started AgentRun 1",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Status: completed",
		"Branch: forgelane/issue-123",
		"Delivery: draft PR ready",
		"ChangeSet ID: 1",
		"Provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
		"Draft PR: github://github.com/owner/repo/pulls/10",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected issue start output to contain %q, got:\n%s", want, stdout)
		}
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected issue start to import only the selected WorkItem, got %d GetIssue calls", fakeProvider.calls)
	}
	if fakeProvider.listCalls != 0 {
		t.Fatalf("expected issue start not to list candidate issues, got %d list calls", fakeProvider.listCalls)
	}
	if len(changeProvider.calls) != 1 {
		t.Fatalf("expected one fake Change Provider push, got %#v", changeProvider.calls)
	}
	if len(changeProvider.draftPRCalls) != 1 {
		t.Fatalf("expected one fake Change Provider draft PR call, got %#v", changeProvider.draftPRCalls)
	}
	assertTableCount(t, homeDir, "work_items", 1)
	assertTableCount(t, homeDir, "agent_runs", 1)
	assertTableCount(t, homeDir, "change_sets", 1)
}

func TestIssueStartRefreshesSelectedWorkItemWhenCached(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected bare init to persist current ForgeProject: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Old cached title",
			Body:                "The cached WorkItem should not be reused by issue start.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "123"); err != nil {
		t.Fatalf("expected initial work item import to succeed: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider.issue.Title = "Fresh selected title"
	fakeProvider.issue.ProviderUpdatedAt = time.Date(2026, 5, 31, 9, 10, 11, 0, time.UTC)
	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "issue", "start", "--agent-preset", "harmless-echo", "123")
	if err != nil {
		t.Fatalf("expected issue start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Started AgentRun 1") {
		t.Fatalf("expected issue start output, got:\n%s", stdout)
	}
	if fakeProvider.calls != 2 {
		t.Fatalf("expected issue start to refresh the selected provider issue after initial import, got %d GetIssue calls", fakeProvider.calls)
	}
	if got := readWorkItemTitle(t, homeDir, "github://github.com/owner/repo/issues/123"); got != "Fresh selected title" {
		t.Fatalf("expected issue start to refresh cached WorkItem title, got %q", got)
	}
	assertTableCount(t, homeDir, "work_items", 1)
	assertTableCount(t, homeDir, "agent_runs", 1)
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
		t.Fatal("expected PR issue response to fail import")
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

func TestRunsCreateImportsWorkItemAndCreatesPlannedRunSpec(t *testing.T) {
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
			Title:               "Create an AgentRun and immutable RunSpec",
			Body:                "Create ForgeLane-owned execution state without running an agent.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Created AgentRun",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Status: planned",
		"ControlAction ID:",
		"RunSpec ID:",
		"Branch: forgelane/issue-123",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs create output to contain %q, got:\n%s", want, stdout)
		}
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected missing WorkItem snapshot to be imported through provider once, got %d calls", fakeProvider.calls)
	}

	assertTableCount(t, homeDir, "work_items", 1)
	assertTableCount(t, homeDir, "control_actions", 1)
	assertTableCount(t, homeDir, "agent_runs", 1)
	assertTableCount(t, homeDir, "run_specs", 1)
	assertEventTypes(t, homeDir, []string{
		"work_item.imported",
		"control_action.succeeded",
		"agent_run.created",
		"run_spec.created",
	})

	db := openStateDB(t, homeDir)
	defer db.Close()

	var runID int64
	var workItemID int64
	var status string
	if err := db.QueryRow("SELECT id, work_item_id, status FROM agent_runs").Scan(&runID, &workItemID, &status); err != nil {
		t.Fatalf("query AgentRun: %v", err)
	}
	if status != "planned" {
		t.Fatalf("expected AgentRun status planned, got %q", status)
	}
	if workItemID == 0 {
		t.Fatal("expected AgentRun to reference a WorkItem")
	}

	var specJSON string
	if err := db.QueryRow("SELECT spec_json FROM run_specs WHERE agent_run_id = ?", runID).Scan(&specJSON); err != nil {
		t.Fatalf("query RunSpec: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		t.Fatalf("expected RunSpec JSON to decode: %v\n%s", err, specJSON)
	}
	if got := spec["branch"]; got != "forgelane/issue-123" {
		t.Fatalf("expected RunSpec branch forgelane/issue-123, got %#v", got)
	}
	workItemSnapshot, ok := spec["work_item"].(map[string]any)
	if !ok {
		t.Fatalf("expected RunSpec work_item object, got %#v", spec["work_item"])
	}
	if got := workItemSnapshot["provider_ref"]; got != "github://github.com/owner/repo/issues/123" {
		t.Fatalf("expected RunSpec WorkItem ProviderRef, got %#v", got)
	}
	if got := workItemSnapshot["provider_status_raw"]; got != "open" {
		t.Fatalf("expected RunSpec WorkItem raw provider status, got %#v", got)
	}
	if got := workItemSnapshot["url"]; got != "https://github.com/owner/repo/issues/123" {
		t.Fatalf("expected RunSpec WorkItem URL, got %#v", got)
	}
	if got := workItemSnapshot["imported_at"]; got == "" {
		t.Fatalf("expected RunSpec WorkItem imported_at, got %#v", got)
	}
	if got := workItemSnapshot["body_snapshot"]; got != "Create ForgeLane-owned execution state without running an agent." {
		t.Fatalf("expected RunSpec WorkItem body snapshot, got %#v", got)
	}
	repoSnapshot, ok := spec["repo"].(map[string]any)
	if !ok {
		t.Fatalf("expected RunSpec repo object, got %#v", spec["repo"])
	}
	if got := repoSnapshot["ref"]; got != "github://github.com/owner/repo" {
		t.Fatalf("expected RunSpec repo ref, got %#v", got)
	}
	agentAdapter, ok := spec["agent_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("expected RunSpec agent_adapter object, got %#v", spec["agent_adapter"])
	}
	if got := agentAdapter["kind"]; got != "command" {
		t.Fatalf("expected generic command AgentAdapter, got %#v", got)
	}
	if got := agentAdapter["preset"]; got != "codex" {
		t.Fatalf("expected missing workflow contract to fall back to codex preset, got %#v", got)
	}
	grants, ok := agentAdapter["credential_grants"].([]any)
	if !ok || len(grants) != 1 {
		t.Fatalf("expected default Codex RunSpec to declare one credential grant, got %#v", agentAdapter["credential_grants"])
	}
	grant, ok := grants[0].(map[string]any)
	if !ok {
		t.Fatalf("expected credential grant object, got %#v", grants[0])
	}
	if got := grant["kind"]; got != "openai_api_key" {
		t.Fatalf("expected OPENAI API key credential grant, got %#v", got)
	}
	if got := grant["secret_id"]; got != "env:OPENAI_API_KEY" {
		t.Fatalf("expected env-backed OPENAI_API_KEY secret id, got %#v", got)
	}
}

func TestRunsCreateUsesWorkflowContractDefaultAgentPreset(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	subdir := filepath.Join(workingDir, "nested")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	homeDir := t.TempDir()
	withWorkingDir(t, subdir)
	withHomeDir(t, homeDir)
	contract := `{
  "version": 1,
  "agent": {
    "default_preset": "harmless-echo"
  }
}`
	if err := os.WriteFile(filepath.Join(workingDir, "forgelane.workflow.json"), []byte(contract), 0o644); err != nil {
		t.Fatalf("write workflow contract: %v", err)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Use workflow contract agent preset",
			Body:                "RunSpec creation should load the repo-owned workflow contract.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	_, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}

	db := openStateDB(t, homeDir)
	defer db.Close()

	var specJSON string
	if err := db.QueryRow("SELECT spec_json FROM run_specs").Scan(&specJSON); err != nil {
		t.Fatalf("query RunSpec: %v", err)
	}
	var spec map[string]any
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		t.Fatalf("expected RunSpec JSON to decode: %v\n%s", err, specJSON)
	}
	agentAdapter, ok := spec["agent_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("expected RunSpec agent_adapter object, got %#v", spec["agent_adapter"])
	}
	if got := agentAdapter["preset"]; got != "harmless-echo" {
		t.Fatalf("expected workflow contract default preset harmless-echo, got %#v", got)
	}
	if grants, ok := agentAdapter["credential_grants"].([]any); ok && len(grants) != 0 {
		t.Fatalf("expected harmless preset to avoid credential grants, got %#v", grants)
	}
}

func TestRunsCreateReusesWorkItemSnapshotButCreatesNewAgentRunEachTime(t *testing.T) {
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
			Title:               "Repeatable AgentRun creation",
			Body:                "Every run creation is a new bounded attempt.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "work-items", "import", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected WorkItem import to succeed: %v\nstderr:\n%s", err, stderr)
	}
	for i := 0; i < 2; i++ {
		stdout, stderr, err := executeForTestWithOptions(t, Options{
			WorkItemProvider: fakeProvider,
		}, "runs", "create", "github://github.com/owner/repo/issues/123")
		if err != nil {
			t.Fatalf("expected runs create #%d to succeed: %v\nstderr:\n%s", i+1, err, stderr)
		}
		if stderr != "" {
			t.Fatalf("expected no stderr, got %q", stderr)
		}
		if !strings.Contains(stdout, "Status: planned") {
			t.Fatalf("expected planned run output, got:\n%s", stdout)
		}
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected runs create to reuse cached WorkItem without provider calls, got %d calls", fakeProvider.calls)
	}

	assertTableCount(t, homeDir, "work_items", 1)
	assertTableCount(t, homeDir, "control_actions", 2)
	assertTableCount(t, homeDir, "agent_runs", 2)
	assertTableCount(t, homeDir, "run_specs", 2)
	assertEventTypes(t, homeDir, []string{
		"work_item.imported",
		"control_action.succeeded",
		"agent_run.created",
		"run_spec.created",
		"control_action.succeeded",
		"agent_run.created",
		"run_spec.created",
	})
}

func TestRunsCreateResolvesIssueNumberThroughCurrentForgeProject(t *testing.T) {
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
			Title:               "Create from shorthand issue number",
			Body:                "Resolve numeric issue shorthand before creating run state.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "123")
	if err != nil {
		t.Fatalf("expected shorthand runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Created AgentRun",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Branch: forgelane/issue-123",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected shorthand runs create output to contain %q, got:\n%s", want, stdout)
		}
	}

	assertTableCount(t, homeDir, "work_items", 1)
	assertTableCount(t, homeDir, "control_actions", 1)
	assertTableCount(t, homeDir, "agent_runs", 1)
	assertTableCount(t, homeDir, "run_specs", 1)
}

func TestRunsShowDisplaysAgentRunDetailFromLocalState(t *testing.T) {
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
			Title:               "Show AgentRun detail",
			Body:                "Read the AgentRun state spine from SQLite.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"AgentRun 1",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Status: planned",
		"Created:",
		"Updated:",
		"RunSpec ID:",
		"Branch: forgelane/issue-123",
		"Repository: github://github.com/owner/repo",
		"AgentAdapter: command",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs show output to contain %q, got:\n%s", want, stdout)
		}
	}
	if fakeProvider.calls != 1 {
		t.Fatalf("expected runs show to read local state without provider calls, got %d calls", fakeProvider.calls)
	}
}

func TestRunsAttentionRecordsFeedbackRequestAndAnswer(t *testing.T) {
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
			Title:               "Request user attention",
			Body:                "Runs can wait for human feedback without process stdin.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	requestStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "request-attention", strconv.FormatInt(runID, 10), "feedback", "Which test boundary should I use?")
	if err != nil {
		t.Fatalf("expected feedback attention request to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Requested feedback for AgentRun 1",
		"Status: requested",
		"ControlAction ID:",
		"Event: control_action.requested",
		"Event: run_attention.feedback_requested",
	} {
		if !strings.Contains(requestStdout, want) {
			t.Fatalf("expected request output to contain %q, got:\n%s", want, requestStdout)
		}
	}

	showStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"Pending attention:",
		"- feedback #2: Which test boundary should I use?",
		"Next: forgelane runs send 1 <message>",
	} {
		if !strings.Contains(showStdout, want) {
			t.Fatalf("expected runs show output to contain %q, got:\n%s", want, showStdout)
		}
	}

	sendStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "send", strconv.FormatInt(runID, 10), "Use the CLI boundary.")
	if err != nil {
		t.Fatalf("expected runs send to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Sent feedback for AgentRun 1",
		"Resolved attention request: 2",
		"ControlAction ID:",
		"Event: control_action.succeeded",
		"Event: run_attention.feedback_sent",
	} {
		if !strings.Contains(sendStdout, want) {
			t.Fatalf("expected send output to contain %q, got:\n%s", want, sendStdout)
		}
	}

	showStdout, stderr, err = executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed after feedback: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(showStdout, "Pending attention:") {
		t.Fatalf("expected feedback answer to clear pending attention, got:\n%s", showStdout)
	}

	eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "events", "list", "--run", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected events list to succeed: %v\nstderr:\n%s", err, stderr)
	}
	assertInOrder(t, eventsStdout, []string{
		"run_attention.feedback_requested",
		"run_attention.feedback_sent",
	})
	assertTableCount(t, homeDir, "control_actions", 3)
	actions := readControlActions(t, homeDir)
	assertControlAction(t, actions[1], "request_feedback", "succeeded", "agent", map[string]any{
		"kind":    "feedback",
		"message": "Which test boundary should I use?",
	})
	assertControlAction(t, actions[2], "send_feedback", "succeeded", "local", map[string]any{
		"request_control_action_id": float64(2),
		"message":                   "Use the CLI boundary.",
	})
	requestResolutionEvent := readEventPayloadForControlAction(t, homeDir, "control_action.succeeded", 2)
	if got := requestResolutionEvent["control_action_id"]; got != float64(2) {
		t.Fatalf("expected request resolution Event to target request ControlAction 2, got %#v", requestResolutionEvent)
	}
	if got := requestResolutionEvent["resolved_by_control_action_id"]; got != float64(3) {
		t.Fatalf("expected request resolution Event to reference response ControlAction 3, got %#v", requestResolutionEvent)
	}
	if got := requestResolutionEvent["status"]; got != "succeeded" {
		t.Fatalf("expected request resolution Event status succeeded, got %#v", requestResolutionEvent)
	}
	feedbackEvent := readEventPayload(t, homeDir, "run_attention.feedback_sent")
	if got := feedbackEvent["message"]; got != "Use the CLI boundary." {
		t.Fatalf("expected feedback Event payload to include answer, got %#v", feedbackEvent)
	}
	if got := feedbackEvent["request_control_action_id"]; got != float64(2) {
		t.Fatalf("expected feedback Event payload to reference request ControlAction 2, got %#v", feedbackEvent)
	}
}

func TestRunsAttentionResolvesApprovalRequest(t *testing.T) {
	tests := []struct {
		action      string
		wantOutput  []string
		wantEvents  []string
		clearsState bool
	}{
		{
			action: "approve",
			wantOutput: []string{
				"Approved attention request for AgentRun 1",
				"Resolved attention request: 2",
				"ControlAction ID:",
				"Event: control_action.succeeded",
				"Event: run_attention.approval_approved",
			},
			wantEvents: []string{
				"run_attention.approval_requested",
				"run_attention.approval_approved",
			},
			clearsState: true,
		},
		{
			action: "reject",
			wantOutput: []string{
				"Rejected attention request for AgentRun 1",
				"Resolved attention request: 2",
				"ControlAction ID:",
				"Event: control_action.succeeded",
				"Event: run_attention.approval_rejected",
			},
			wantEvents: []string{
				"run_attention.approval_requested",
				"run_attention.approval_rejected",
			},
			clearsState: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
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
					Title:               "Resolve approval attention",
					Body:                "Runs can wait for human approval without process stdin.",
					Status:              "open",
					RawStatus:           "open",
					URL:                 "https://github.com/owner/repo/issues/123",
					ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
				},
			}

			createStdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "runs", "create", "github://github.com/owner/repo/issues/123")
			if err != nil {
				t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
			}
			runID := extractCreatedAgentRunID(t, createStdout)

			if _, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "runs", "request-attention", strconv.FormatInt(runID, 10), "approval", "May I continue with the requested cleanup?"); err != nil {
				t.Fatalf("expected approval attention request to succeed: %v\nstderr:\n%s", err, stderr)
			}

			showStdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "runs", "show", strconv.FormatInt(runID, 10))
			if err != nil {
				t.Fatalf("expected runs show to succeed: %v\nstderr:\n%s", err, stderr)
			}
			for _, want := range []string{
				"Pending attention:",
				"- approval #2: May I continue with the requested cleanup?",
				"Next: forgelane runs approve 1 <approve|reject>",
			} {
				if !strings.Contains(showStdout, want) {
					t.Fatalf("expected runs show output to contain %q, got:\n%s", want, showStdout)
				}
			}

			approveStdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "runs", "approve", strconv.FormatInt(runID, 10), tt.action, "Decision note")
			if err != nil {
				t.Fatalf("expected runs approve %s to succeed: %v\nstderr:\n%s", tt.action, err, stderr)
			}
			if stderr != "" {
				t.Fatalf("expected no stderr, got %q", stderr)
			}
			for _, want := range tt.wantOutput {
				if !strings.Contains(approveStdout, want) {
					t.Fatalf("expected runs approve output to contain %q, got:\n%s", want, approveStdout)
				}
			}

			showStdout, stderr, err = executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "runs", "show", strconv.FormatInt(runID, 10))
			if err != nil {
				t.Fatalf("expected runs show to succeed after approval decision: %v\nstderr:\n%s", err, stderr)
			}
			if tt.clearsState && strings.Contains(showStdout, "Pending attention:") {
				t.Fatalf("expected approval decision to clear pending attention, got:\n%s", showStdout)
			}

			eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "events", "list", "--run", strconv.FormatInt(runID, 10))
			if err != nil {
				t.Fatalf("expected events list to succeed: %v\nstderr:\n%s", err, stderr)
			}
			assertInOrder(t, eventsStdout, tt.wantEvents)
			assertTableCount(t, homeDir, "control_actions", 3)
			actions := readControlActions(t, homeDir)
			resolvedRequestStatus := "succeeded"
			if tt.action == "reject" {
				resolvedRequestStatus = "rejected"
			}
			assertControlAction(t, actions[1], "request_approval", resolvedRequestStatus, "agent", map[string]any{
				"kind":    "approval",
				"message": "May I continue with the requested cleanup?",
			})
			assertControlAction(t, actions[2], tt.action, "succeeded", "local", map[string]any{
				"request_control_action_id": float64(2),
				"decision":                  tt.action,
				"message":                   "Decision note",
			})
			requestResolutionEventType := "control_action.succeeded"
			if tt.action == "reject" {
				requestResolutionEventType = "control_action.rejected"
			}
			requestResolutionEvent := readEventPayloadForControlAction(t, homeDir, requestResolutionEventType, 2)
			if got := requestResolutionEvent["control_action_id"]; got != float64(2) {
				t.Fatalf("expected request resolution Event to target request ControlAction 2, got %#v", requestResolutionEvent)
			}
			if got := requestResolutionEvent["resolved_by_control_action_id"]; got != float64(3) {
				t.Fatalf("expected request resolution Event to reference response ControlAction 3, got %#v", requestResolutionEvent)
			}
			if got := requestResolutionEvent["status"]; got != resolvedRequestStatus {
				t.Fatalf("expected request resolution Event status %q, got %#v", resolvedRequestStatus, requestResolutionEvent)
			}
			eventType := "run_attention.approval_approved"
			if tt.action == "reject" {
				eventType = "run_attention.approval_rejected"
			}
			approvalEvent := readEventPayload(t, homeDir, eventType)
			if got := approvalEvent["decision"]; got != tt.action {
				t.Fatalf("expected approval Event payload decision %q, got %#v", tt.action, approvalEvent)
			}
			if got := approvalEvent["request_control_action_id"]; got != float64(2) {
				t.Fatalf("expected approval Event payload to reference request ControlAction 2, got %#v", approvalEvent)
			}
		})
	}
}

func TestEventsListRunDisplaysOrderedAgentRunTimeline(t *testing.T) {
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
			Title:               "List AgentRun events",
			Body:                "Display the run timeline from SQLite Events.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "events", "list", "--run", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected events list to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Events for AgentRun 1",
		"control_action.succeeded",
		"agent_run.created",
		"run_spec.created",
		"Subject:",
		"Occurred:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected events list output to contain %q, got:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "work_item.imported") {
		t.Fatalf("expected events list --run to omit WorkItem-only events, got:\n%s", stdout)
	}
	assertInOrder(t, stdout, []string{
		"control_action.succeeded",
		"agent_run.created",
		"run_spec.created",
	})
	if fakeProvider.calls != 1 {
		t.Fatalf("expected events list to read local state without provider calls, got %d calls", fakeProvider.calls)
	}
}

func TestRunsPrepareCreatesWorkspaceAndShowsState(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Prepare a Workspace",
			Body:                "Lease an isolated workspace before agent execution.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	prepareStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "prepare", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs prepare to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}

	workspaceRoot := filepath.Join(homeDir, ".forgelane", "workspaces", "run-1")
	for _, want := range []string{
		"Prepared AgentRun 1",
		"RunnerJob ID:",
		"Workspace ID:",
		"Workspace: " + workspaceRoot,
		"Status: ready",
	} {
		if !strings.Contains(prepareStdout, want) {
			t.Fatalf("expected runs prepare output to contain %q, got:\n%s", want, prepareStdout)
		}
	}

	for _, dir := range []string{"repo", "logs", "artifacts", "tmp"} {
		path := filepath.Join(workspaceRoot, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected workspace directory %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %s to be a directory", path)
		}
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "repo", "README.md")); err != nil {
		t.Fatalf("expected repository preparation under repo/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("expected repository files to stay under repo/, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workingDir, ".git", "worktrees")); !os.IsNotExist(err) {
		t.Fatalf("expected source repository metadata not to gain worktrees, stat err: %v", err)
	}

	showStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Status: preparing",
		"Workspace status: ready",
		"Workspace: " + workspaceRoot,
		"Workspace repo: " + filepath.Join(workspaceRoot, "repo"),
	} {
		if !strings.Contains(showStdout, want) {
			t.Fatalf("expected runs show output to contain %q, got:\n%s", want, showStdout)
		}
	}

	eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "events", "list", "--run", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected events list to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	assertInOrder(t, eventsStdout, []string{
		"agent_run.created",
		"workspace.allocated",
		"workspace.prepared",
	})

	assertTableCount(t, homeDir, "runner_jobs", 1)
	assertTableCount(t, homeDir, "workspaces", 1)
}

func TestRunsStopAndRetryExposeAgentRunControlActions(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Control an AgentRun",
			Body:                "Stop and retry should be auditable CLI controls.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "prepare", strconv.FormatInt(runID, 10)); err != nil {
		t.Fatalf("expected runs prepare to succeed: %v\nstderr:\n%s", err, stderr)
	}
	instanceStore, err := openInitializedStore()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := instanceStore.MarkAgentCommandStarted(runID); err != nil {
		instanceStore.Close()
		t.Fatalf("mark AgentRun running: %v", err)
	}
	instanceStore.Close()

	stopStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "stop", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs stop to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Stop requested for AgentRun 1",
		"Status: cancelled",
		"ControlAction ID:",
		"Event: control_action.succeeded",
		"Event: agent_run.cancel_requested",
		"Event: agent_run.cancelled",
	} {
		if !strings.Contains(stopStdout, want) {
			t.Fatalf("expected runs stop output to contain %q, got:\n%s", want, stopStdout)
		}
	}

	retryStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "retry", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs retry to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Retried AgentRun 1 as AgentRun 2",
		"Status: planned",
		"ControlAction ID:",
		"RunSpec ID:",
		"Event: control_action.succeeded",
		"Event: agent_run.created",
		"Event: run_spec.created",
	} {
		if !strings.Contains(retryStdout, want) {
			t.Fatalf("expected runs retry output to contain %q, got:\n%s", want, retryStdout)
		}
	}

	assertTableCount(t, homeDir, "control_actions", 3)
	assertTableCount(t, homeDir, "agent_runs", 2)
	assertTableCount(t, homeDir, "run_specs", 2)
	assertEventTypes(t, homeDir, []string{
		"work_item.imported",
		"control_action.succeeded",
		"agent_run.created",
		"run_spec.created",
		"workspace.allocated",
		"workspace.prepared",
		"agent_command.started",
		"control_action.succeeded",
		"agent_run.cancel_requested",
		"agent_run.cancelled",
		"control_action.succeeded",
		"agent_run.created",
		"run_spec.created",
	})
}

func TestRunsPrepareFailureRetainsFailedWorkspaceState(t *testing.T) {
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
			Title:               "Retain failed Workspace",
			Body:                "Preparation failures should be auditable.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "prepare", strconv.FormatInt(runID, 10))
	if err == nil {
		t.Fatal("expected runs prepare to fail outside a Git repository")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "prepare Workspace repository:") {
		t.Fatalf("expected checkout preparation failure, got:\n%s", stderr)
	}

	workspaceRoot := filepath.Join(homeDir, ".forgelane", "workspaces", "run-1")
	for _, dir := range []string{"logs", "artifacts", "tmp"} {
		path := filepath.Join(workspaceRoot, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected retained workspace directory %s: %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %s to be a directory", path)
		}
	}

	showStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed after failed prepare: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Status: failed",
		"Workspace status: failed",
		"Workspace: " + workspaceRoot,
	} {
		if !strings.Contains(showStdout, want) {
			t.Fatalf("expected runs show output to contain %q, got:\n%s", want, showStdout)
		}
	}

	eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "events", "list", "--run", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected events list to succeed after failed prepare: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	assertInOrder(t, eventsStdout, []string{
		"workspace.allocated",
		"workspace.prepare_failed",
	})
	assertTableCount(t, homeDir, "runner_jobs", 1)
	assertTableCount(t, homeDir, "workspaces", 1)
}

func TestRunsExecuteHarmlessPresetCapturesLogsAndScrubsEnvironment(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)
	t.Setenv("FORGELANE_GITHUB_TOKEN", "sensitive-forgelane-github-token")
	t.Setenv("GITHUB_TOKEN", "sensitive-provider-token")
	t.Setenv("GH_TOKEN", "sensitive-gh-token")
	t.Setenv("FORGELANE_GITLAB_TOKEN", "sensitive-forgelane-gitlab-token")
	t.Setenv("GITLAB_TOKEN", "sensitive-gitlab-token")

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Execute a harmless AgentAdapter preset",
			Body:                "Run a scrubbed command inside the prepared Workspace.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "prepare", strconv.FormatInt(runID, 10)); err != nil {
		t.Fatalf("expected runs prepare to succeed: %v\nstderr:\n%s", err, stderr)
	}

	executeStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "execute", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs execute to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Executed AgentRun 1",
		"Status: completed",
		"Delivery: skipped (no repository changes)",
		"Event: repository_delivery.skipped",
		"Event: agent_command.completed",
	} {
		if !strings.Contains(executeStdout, want) {
			t.Fatalf("expected runs execute output to contain %q, got:\n%s", want, executeStdout)
		}
	}

	showStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(showStdout, "Status: completed") {
		t.Fatalf("expected completed AgentRun, got:\n%s", showStdout)
	}
	if !strings.Contains(showStdout, "Delivery: skipped (no repository changes)") {
		t.Fatalf("expected run detail to show skipped delivery, got:\n%s", showStdout)
	}

	logsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "logs", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs logs to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	workspaceRepo := filepath.Join(homeDir, ".forgelane", "workspaces", "run-1", "repo")
	workspaceRepo, err = filepath.EvalSymlinks(workspaceRepo)
	if err != nil {
		t.Fatalf("resolve Workspace repo path: %v", err)
	}
	for _, want := range []string{
		"forgelane harmless stdout",
		"forgelane harmless stderr",
		"cwd=" + workspaceRepo,
		"provider-token=absent",
	} {
		if !strings.Contains(logsStdout, want) {
			t.Fatalf("expected runs logs output to contain %q, got:\n%s", want, logsStdout)
		}
	}
	for _, secret := range []string{"sensitive-forgelane-github-token", "sensitive-provider-token", "sensitive-gh-token", "sensitive-forgelane-gitlab-token", "sensitive-gitlab-token"} {
		if strings.Contains(logsStdout, secret) {
			t.Fatalf("expected scrubbed provider token %q, got logs:\n%s", secret, logsStdout)
		}
	}
	if strings.Contains(logsStdout, "provider-token=present") {
		t.Fatalf("expected scrubbed provider token, got logs:\n%s", logsStdout)
	}

	eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "events", "list", "--run", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected events list to succeed: %v\nstderr:\n%s", err, stderr)
	}
	assertInOrder(t, eventsStdout, []string{
		"workspace.prepared",
		"agent_command.started",
		"repository_delivery.skipped",
		"agent_command.completed",
	})
	if strings.Contains(eventsStdout, "forgelane harmless stdout") {
		t.Fatalf("expected command output to stay out of Events, got:\n%s", eventsStdout)
	}

	assertLogSegmentStreams(t, homeDir, []string{"stdout", "stderr"})
	for _, path := range []string{
		filepath.Join(homeDir, ".forgelane", "workspaces", "run-1", "logs", "stdout.log"),
		filepath.Join(homeDir, ".forgelane", "workspaces", "run-1", "logs", "stderr.log"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected log file %s: %v", path, err)
		}
	}
	if got := strings.TrimSpace(gitOutput(t, workspaceRepo, "rev-list", "--count", "HEAD")); got != "1" {
		t.Fatalf("expected no local delivery commit, got %s commits", got)
	}
	assertTableCount(t, homeDir, "commit_refs", 0)
	if fakeProvider.calls != 1 {
		t.Fatalf("expected no provider calls after initial WorkItem import, got %d calls", fakeProvider.calls)
	}
}

func TestRunsExecuteMaterializesRepositoryChangesIntoLocalCommit(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Materialize repository changes",
			Body:                "Commit local Workspace repository changes.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true},
		},
	}
	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "create", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "prepare", strconv.FormatInt(runID, 10)); err != nil {
		t.Fatalf("expected runs prepare to succeed: %v\nstderr:\n%s", err, stderr)
	}

	workspaceRoot := filepath.Join(homeDir, ".forgelane", "workspaces", "run-1")
	if err := os.WriteFile(filepath.Join(workspaceRoot, "artifacts", "outside-repo.txt"), []byte("artifact\n"), 0o644); err != nil {
		t.Fatalf("write workspace artifact: %v", err)
	}

	executeStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "execute", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs execute to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Executed AgentRun 1",
		"Status: completed",
		"ChangeSet: 1 draft_open forgelane/issue-123",
		"Event: repository_commit.materialized",
		"Event: change_set.created",
		"Event: agent_command.completed",
		"Event: change_set.branch_push_succeeded",
		"Event: change_set.draft_pr_succeeded",
	} {
		if !strings.Contains(executeStdout, want) {
			t.Fatalf("expected runs execute output to contain %q, got:\n%s", want, executeStdout)
		}
	}

	workspaceRepo := filepath.Join(workspaceRoot, "repo")
	commitSubject := gitOutput(t, workspaceRepo, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(commitSubject) != "Materialize AgentRun 1 repository changes" {
		t.Fatalf("unexpected materialized commit subject %q", commitSubject)
	}
	commitAuthor := gitOutput(t, workspaceRepo, "log", "-1", "--pretty=%an <%ae>")
	if strings.TrimSpace(commitAuthor) != "ForgeLane <forgelane@localhost>" {
		t.Fatalf("unexpected materialized commit author %q", commitAuthor)
	}
	tree := gitOutput(t, workspaceRepo, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(tree, "forgelane-agent-output.txt\n") {
		t.Fatalf("expected committed agent output file, got:\n%s", tree)
	}
	if strings.Contains(tree, "artifacts/outside-repo.txt\n") {
		t.Fatalf("expected workspace artifact outside repo to stay out of commit, got:\n%s", tree)
	}

	showStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"ChangeSet: 1 draft_open forgelane/issue-123",
		"ChangeSet active run: 1",
		"ChangeSet commits: 1",
		"ChangeSet provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
		"ChangeSet provider change: github://github.com/owner/repo/pulls/10",
		"ChangeSet draft: true",
		"Commit refs:",
		"github://github.com/owner/repo@",
		"Materialize AgentRun 1 repository changes",
	} {
		if !strings.Contains(showStdout, want) {
			t.Fatalf("expected runs show output to contain %q, got:\n%s", want, showStdout)
		}
	}

	eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
	}, "events", "list", "--run", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected events list to succeed: %v\nstderr:\n%s", err, stderr)
	}
	assertInOrder(t, eventsStdout, []string{
		"agent_command.started",
		"repository_commit.materialized",
		"change_set.created",
		"agent_command.completed",
		"change_set.branch_push_succeeded",
		"change_set.draft_pr_succeeded",
	})

	assertTableCount(t, homeDir, "change_sets", 1)
	assertTableCount(t, homeDir, "commit_refs", 1)
	assertTableCount(t, homeDir, "control_actions", 3)
}

func TestRunsRequestChangesAndCloseExposeChangeSetControls(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Review and close a ChangeSet",
			Body:                "ChangeSet controls should be auditable CLI controls.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true},
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}

	requestStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "request-changes", "1", "Please add focused regression tests.")
	if err != nil {
		t.Fatalf("expected runs request-changes to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Requested changes for ChangeSet 1",
		"AgentRun: 1",
		"Status: changes_requested",
		"ControlAction ID:",
		"Branch: forgelane/issue-123",
		"ChangeSet provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
		"ChangeSet provider change: github://github.com/owner/repo/pulls/10",
		"Event: control_action.succeeded",
		"Event: change_set.changes_requested",
	} {
		if !strings.Contains(requestStdout, want) {
			t.Fatalf("expected runs request-changes output to contain %q, got:\n%s", want, requestStdout)
		}
	}

	closeStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "close", "1", "Close local delivery path.")
	if err != nil {
		t.Fatalf("expected runs close to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Closed ChangeSet 1",
		"AgentRun: 1",
		"Status: closed",
		"ControlAction ID:",
		"Branch: forgelane/issue-123",
		"ChangeSet provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
		"ChangeSet provider change: github://github.com/owner/repo/pulls/10",
		"Event: control_action.succeeded",
		"Event: change_set.closed",
	} {
		if !strings.Contains(closeStdout, want) {
			t.Fatalf("expected runs close output to contain %q, got:\n%s", want, closeStdout)
		}
	}

	db := openStateDB(t, homeDir)
	defer db.Close()
	var status string
	if err := db.QueryRow("SELECT status FROM change_sets WHERE id = 1").Scan(&status); err != nil {
		t.Fatalf("query ChangeSet status: %v", err)
	}
	if status != "closed" {
		t.Fatalf("expected ChangeSet to be closed, got %q", status)
	}
	assertTableCount(t, homeDir, "control_actions", 5)
}

func TestRunsStartDeliversRepositoryChangesToDraftPR(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Start the primary delivery path",
			Body:                "Create one command for issue-to-draft-PR delivery.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true},
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Started AgentRun 1",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Status: completed",
		"Branch: forgelane/issue-123",
		"Delivery: draft PR ready",
		"ChangeSet ID: 1",
		"Provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
		"Draft PR: github://github.com/owner/repo/pulls/10",
		"Next: forgelane runs evidence 1",
		"Next: forgelane runs show 1",
		"Next: forgelane runs logs 1",
		"Next: forgelane runs stop 1",
		"Next: forgelane runs retry 1",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs start output to contain %q, got:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "draft_open") {
		t.Fatalf("expected product summary not to expose internal status, got:\n%s", stdout)
	}
	if len(changeProvider.calls) != 1 {
		t.Fatalf("expected one fake Change Provider push, got %#v", changeProvider.calls)
	}
	if len(changeProvider.draftPRCalls) != 1 {
		t.Fatalf("expected one fake Change Provider draft PR call, got %#v", changeProvider.draftPRCalls)
	}
	assertTableCount(t, homeDir, "agent_runs", 1)
	assertTableCount(t, homeDir, "runner_jobs", 1)
	assertTableCount(t, homeDir, "workspaces", 1)
	assertTableCount(t, homeDir, "change_sets", 1)
	assertTableCount(t, homeDir, "commit_refs", 1)
	assertTableCount(t, homeDir, "control_actions", 3)

	eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "events", "list", "--run", "1")
	if err != nil {
		t.Fatalf("expected events list after successful start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	assertInOrder(t, eventsStdout, []string{
		"change_set.branch_push_started",
		"change_set.branch_push_succeeded",
		"change_set.draft_pr_started",
		"change_set.draft_pr_succeeded",
	})
}

func TestRunsEvidenceSummarizesCompletedDraftPRDelivery(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Summarize completed delivery evidence",
			Body:                "Show review evidence without correlating multiple raw commands.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true},
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "evidence", "1")
	if err != nil {
		t.Fatalf("expected runs evidence to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Delivery evidence for AgentRun 1",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Status: completed",
		"Branch: forgelane/issue-123",
		"ChangeSet status: draft_open",
		"Provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
		"Draft PR: github://github.com/owner/repo/pulls/10",
		"Commit refs:",
		"github://github.com/owner/repo@",
		"Materialize AgentRun 1 repository changes",
		"Logs: forgelane runs logs 1",
		"Log previews:",
		"stdout #1 logs/stdout.log: provider-token=absent",
		"Events:",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs evidence output to contain %q, got:\n%s", want, stdout)
		}
	}
	assertInOrder(t, stdout, []string{
		"agent_command.started",
		"repository_commit.materialized",
		"change_set.created",
		"agent_command.completed",
		"change_set.branch_push_started",
		"change_set.branch_push_succeeded",
		"change_set.draft_pr_started",
		"change_set.draft_pr_succeeded",
	})
}

func TestRunsReportSummarizesOperatorRun(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Summarize run status for operators",
			Body:                "Show the run without piecing together raw commands.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true, "state": "open"},
		},
		providerPRReport: workflow.ProviderPRReport{
			Ref:         "github://github.com/owner/repo/pulls/10",
			Title:       "Draft: ForgeLane delivery",
			State:       "open",
			Draft:       true,
			HeadSHA:     "abc123",
			CheckStatus: "success",
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		ChangeProvider: changeProvider,
	}, "runs", "report", "1")
	if err != nil {
		t.Fatalf("expected runs report to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Run report for AgentRun 1",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Run status: completed",
		"ChangeSet status: draft_open",
		"Provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
		"PR: github://github.com/owner/repo/pulls/10",
		"Check status: success",
		"Check source: provider_pr",
		"Commits:",
		"github://github.com/owner/repo@",
		"Materialize AgentRun 1 repository changes",
		"Logs: forgelane runs logs 1",
		"Key events:",
		"change_set.draft_pr_succeeded",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs report output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestRunsReportJSONSummarizesOperatorRun(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Summarize run status as JSON",
			Body:                "Show a stable report for scripts.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true, "state": "open"},
		},
		providerPRReport: workflow.ProviderPRReport{
			Ref:         "github://github.com/owner/repo/pulls/10",
			Title:       "Draft: ForgeLane delivery",
			State:       "open",
			Draft:       true,
			HeadSHA:     "abc123",
			CheckStatus: "success",
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		ChangeProvider: changeProvider,
	}, "runs", "report", "1", "--json")
	if err != nil {
		t.Fatalf("expected runs report --json to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}

	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("expected runs report JSON to decode: %v\n%s", err, stdout)
	}
	agentRun := report["agent_run"].(map[string]any)
	if agentRun["id"] != float64(1) || agentRun["status"] != "completed" {
		t.Fatalf("unexpected agent_run summary: %#v", agentRun)
	}
	workItem := report["work_item"].(map[string]any)
	if workItem["provider_ref"] != "github://github.com/owner/repo/issues/123" || workItem["title"] != "Summarize run status as JSON" {
		t.Fatalf("unexpected work_item summary: %#v", workItem)
	}
	changeSet := report["change_set"].(map[string]any)
	if changeSet["status"] != "draft_open" ||
		changeSet["provider_branch"] != "github://github.com/owner/repo/branches/forgelane/issue-123" ||
		changeSet["pr_ref"] != "github://github.com/owner/repo/pulls/10" {
		t.Fatalf("unexpected change_set summary: %#v", changeSet)
	}
	providerPR := report["provider_pr"].(map[string]any)
	if providerPR["ref"] != "github://github.com/owner/repo/pulls/10" || providerPR["check_status"] != "success" {
		t.Fatalf("unexpected provider_pr summary: %#v", providerPR)
	}
	checkStatus := report["check_status"].(map[string]any)
	if checkStatus["status"] != "success" || checkStatus["source"] != "provider_pr" {
		t.Fatalf("unexpected top-level check_status summary: %#v", checkStatus)
	}
	commits := report["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("expected one commit, got %#v", commits)
	}
	logs := report["logs"].(map[string]any)
	if logs["command"] != "forgelane runs logs 1" {
		t.Fatalf("unexpected logs summary: %#v", logs)
	}
	keyEvents := report["key_events"].([]any)
	if len(keyEvents) == 0 {
		t.Fatalf("expected key_events in JSON report: %#v", report)
	}
}

func TestPRReportSummarizesMappedProviderPR(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Summarize PR status for operators",
			Body:                "Show PR state with local delivery state.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true, "state": "open"},
		},
		providerPRReport: workflow.ProviderPRReport{
			Ref:         "github://github.com/owner/repo/pulls/10",
			Title:       "Draft: ForgeLane delivery",
			State:       "open",
			Draft:       true,
			URL:         "https://github.com/owner/repo/pull/10",
			HeadSHA:     "abc123",
			CheckStatus: "success",
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		ChangeProvider: changeProvider,
	}, "pr", "report", "10")
	if err != nil {
		t.Fatalf("expected pr report to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"PR report for github://github.com/owner/repo/pulls/10",
		"Provider state: open",
		"Draft: true",
		"Check status: success",
		"Actionable feedback: not_available",
		"Warning: " + prReportFeedbackNotSyncedWarning,
		"ChangeSet: 1 draft_open forgelane/issue-123",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Related AgentRuns:",
		"- AgentRun 1 completed",
		"Commits:",
		"github://github.com/owner/repo@",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected pr report output to contain %q, got:\n%s", want, stdout)
		}
	}
	if len(changeProvider.prReportCalls) != 1 || changeProvider.prReportCalls[0].String() != "github://github.com/owner/repo/pulls/10" {
		t.Fatalf("expected one provider PR report call, got %#v", changeProvider.prReportCalls)
	}
}

func TestPRReportJSONSummarizesMappedProviderPR(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Summarize PR status as JSON",
			Body:                "Show PR state with local delivery state.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true, "state": "open"},
		},
		providerPRReport: workflow.ProviderPRReport{
			Ref:         "github://github.com/owner/repo/pulls/10",
			Title:       "Draft: ForgeLane delivery",
			State:       "open",
			Draft:       true,
			URL:         "https://github.com/owner/repo/pull/10",
			HeadSHA:     "abc123",
			CheckStatus: "success",
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		ChangeProvider: changeProvider,
	}, "pr", "report", "10", "--json")
	if err != nil {
		t.Fatalf("expected pr report --json to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("expected pr report JSON to decode: %v\n%s", err, stdout)
	}
	providerPR := report["provider_pr"].(map[string]any)
	if providerPR["ref"] != "github://github.com/owner/repo/pulls/10" || providerPR["check_status"] != "success" {
		t.Fatalf("unexpected provider_pr summary: %#v", providerPR)
	}
	changeSet := report["change_set"].(map[string]any)
	if changeSet["status"] != "draft_open" || changeSet["active_for_retry"] != true {
		t.Fatalf("unexpected change_set summary: %#v", changeSet)
	}
	relatedRuns := report["related_agent_runs"].([]any)
	if len(relatedRuns) != 1 {
		t.Fatalf("expected one related run, got %#v", relatedRuns)
	}
	commits := report["commits"].([]any)
	if len(commits) != 1 {
		t.Fatalf("expected one commit, got %#v", commits)
	}
	feedback := report["actionable_feedback"].(map[string]any)
	if feedback["status"] != prReportFeedbackStatusNotAvailable || feedback["warning"] != prReportFeedbackNotSyncedWarning {
		t.Fatalf("unexpected actionable_feedback summary: %#v", feedback)
	}
	warnings := report["warnings"].([]any)
	if len(warnings) != 1 || warnings[0] != prReportFeedbackNotSyncedWarning {
		t.Fatalf("expected actionable feedback warning, got %#v", warnings)
	}
}

func TestPRReportShowsUnmappedProviderPRReadOnly(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	changeProvider := &recordingChangeProvider{
		providerPRReport: workflow.ProviderPRReport{
			Ref:         "github://github.com/owner/repo/pulls/99",
			Title:       "A provider-owned PR",
			State:       "open",
			Draft:       false,
			URL:         "https://github.com/owner/repo/pull/99",
			HeadSHA:     "def456",
			CheckStatus: "pending",
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		ChangeProvider: changeProvider,
	}, "pr", "report", "99")
	if err != nil {
		t.Fatalf("expected unmapped pr report to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"PR report for github://github.com/owner/repo/pulls/99",
		"Provider state: open",
		"Check status: pending",
		"Actionable feedback: not_available",
		"Warning: " + prReportFeedbackNotSyncedWarning,
		"ChangeSet: none (unmapped)",
		"Warning: PR is not mapped to an active ForgeLane ChangeSet for retry",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected unmapped pr report output to contain %q, got:\n%s", want, stdout)
		}
	}
	if len(changeProvider.prReportCalls) != 1 || changeProvider.prReportCalls[0].String() != "github://github.com/owner/repo/pulls/99" {
		t.Fatalf("expected one provider PR report call, got %#v", changeProvider.prReportCalls)
	}
	assertTableCount(t, homeDir, "change_sets", 0)
	assertTableCount(t, homeDir, "work_items", 0)
}

func TestPRReportWarnsForInactiveMappedChangeSet(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Close stale PR mapping",
			Body:                "Show historical state but no retry mapping.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "github://github.com/owner/repo/pulls/10",
			Draft:            true,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": true, "state": "open"},
		},
		providerPRReport: workflow.ProviderPRReport{
			Ref:         "github://github.com/owner/repo/pulls/10",
			Title:       "Draft: ForgeLane delivery",
			State:       "closed",
			Draft:       false,
			CheckStatus: "success",
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if _, stderr, err := executeForTest(t, "runs", "close", "1", "operator closed"); err != nil {
		t.Fatalf("expected runs close to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		ChangeProvider: changeProvider,
	}, "pr", "report", "10", "--json")
	if err != nil {
		t.Fatalf("expected pr report to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("expected pr report JSON to decode: %v\n%s", err, stdout)
	}
	changeSet := report["change_set"].(map[string]any)
	if changeSet["status"] != "closed" || changeSet["active_for_retry"] != false {
		t.Fatalf("unexpected inactive change_set summary: %#v", changeSet)
	}
	warnings := report["warnings"].([]any)
	if len(warnings) != 2 ||
		warnings[0] != prReportFeedbackNotSyncedWarning ||
		warnings[1] != "PR is not mapped to an active ForgeLane ChangeSet for retry" {
		t.Fatalf("expected feedback and inactive retry warnings, got %#v", warnings)
	}
}

func TestPRReportRejectsInvalidPRNumber(t *testing.T) {
	stdout, stderr, err := executeForTest(t, "pr", "report", "abc")
	if err == nil {
		t.Fatal("expected invalid PR number to fail")
	}
	if !strings.Contains(err.Error(), "invalid PR number") {
		t.Fatalf("expected invalid PR number error, got %v\nstderr:\n%s", err, stderr)
	}
	if stdout != "" {
		t.Fatalf("expected no stdout for invalid PR number, got:\n%s", stdout)
	}
}

func TestPRReportReturnsClearProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "provider PR not found",
			err:  fmt.Errorf("GitHub PR not found: github://github.com/owner/repo/pulls/404"),
			want: "GitHub PR not found",
		},
		{
			name: "provider auth failure",
			err:  fmt.Errorf("auth or permission failure reading GitHub PR: github://github.com/owner/repo/pulls/10"),
			want: "auth or permission failure reading GitHub PR",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workingDir := t.TempDir()
			homeDir := t.TempDir()
			runGit(t, workingDir, "init")
			runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
			withWorkingDir(t, workingDir)
			withHomeDir(t, homeDir)

			if _, stderr, err := executeForTest(t, "init"); err != nil {
				t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
			}

			stdout, stderr, err := executeForTestWithOptions(t, Options{
				ChangeProvider: &recordingChangeProvider{providerPRErr: tc.err},
			}, "pr", "report", "10")
			if err == nil {
				t.Fatal("expected provider error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v\nstderr:\n%s", tc.want, err, stderr)
			}
			if stdout != "" {
				t.Fatalf("expected no stdout for provider error, got:\n%s", stdout)
			}
		})
	}
}

func TestPRReportRejectsConfiguredProviderWithoutReporter(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init"); err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		ChangeProvider: changeProviderWithoutReporter{},
	}, "pr", "report", "10")
	if err == nil {
		t.Fatal("expected unsupported configured provider error")
	}
	if !strings.Contains(err.Error(), "configured ChangeProvider does not support PR reports") {
		t.Fatalf("unexpected error %v\nstderr:\n%s", err, stderr)
	}
	if stdout != "" {
		t.Fatalf("expected no stdout for unsupported configured provider, got:\n%s", stdout)
	}
}

func TestRunsEvidenceReportsNoChangeDeliverySkip(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Summarize no-change evidence",
			Body:                "No-change delivery should be explicit review evidence.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected no-change runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "evidence", "1")
	if err != nil {
		t.Fatalf("expected runs evidence to succeed: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"Delivery evidence for AgentRun 1",
		"Status: completed",
		"Delivery: skipped (no_repository_changes)",
		"Commit refs: none",
		"ChangeSet status: none (delivery skipped)",
		"Logs: forgelane runs logs 1",
		"logs/stdout.log: forgelane harmless stdout",
		"repository_delivery.skipped",
		"agent_command.completed",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs evidence output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestRunsEvidenceReportsProviderDeliveryFailures(t *testing.T) {
	tests := []struct {
		name           string
		changeProvider workflow.ChangeProvider
		wantErr        string
		wantEvidence   []string
	}{
		{
			name: "branch push failure",
			changeProvider: &recordingChangeProvider{
				pushErr: os.ErrPermission,
			},
			wantErr: "push ChangeSet branch failed",
			wantEvidence: []string{
				"Delivery: failed branch push",
				"ChangeSet status: branch_push_failed",
				"Commit refs:",
				"change_set.branch_push_started",
				"change_set.branch_push_failed",
			},
		},
		{
			name: "draft PR failure",
			changeProvider: &recordingChangeProvider{
				pushResult: workflow.ChangeBranchPushResult{
					ChangeSetID:       1,
					BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
					PushedCommitSHAs:  []string{"abc123"},
				},
				draftPRErr: os.ErrPermission,
			},
			wantErr: "create or update draft PR failed",
			wantEvidence: []string{
				"Delivery: failed draft PR",
				"ChangeSet status: branch_ready",
				"Provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
				"Commit refs:",
				"change_set.branch_push_succeeded",
				"change_set.draft_pr_started",
				"change_set.draft_pr_failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			homeDir := t.TempDir()
			runGit(t, workingDir, "init")
			runGit(t, workingDir, "config", "user.email", "test@example.com")
			runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
			runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
			if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
				t.Fatalf("write source repo file: %v", err)
			}
			runGit(t, workingDir, "add", "README.md")
			runGit(t, workingDir, "commit", "-m", "initial")
			withWorkingDir(t, workingDir)
			withHomeDir(t, homeDir)

			fakeProvider := &recordingWorkItemProvider{
				issue: workitems.ProviderIssue{
					ProviderRef:         "github://github.com/owner/repo/issues/123",
					RepositoryRef:       "github://github.com/owner/repo",
					Provider:            "github",
					ProviderIssueNumber: 123,
					Title:               "Summarize provider delivery failure",
					Body:                "Provider failures should remain reviewable evidence.",
					Status:              "open",
					RawStatus:           "open",
					URL:                 "https://github.com/owner/repo/issues/123",
					ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
				},
			}

			if _, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider:    fakeProvider,
				AgentCommandPlanner: changingCommandPlanner{},
				ChangeProvider:      tt.changeProvider,
			}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123"); err == nil {
				t.Fatal("expected runs start to fail")
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error to contain %q, got %v\nstderr:\n%s", tt.wantErr, err, stderr)
			}

			stdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "runs", "evidence", "1")
			if err != nil {
				t.Fatalf("expected runs evidence to succeed: %v\nstderr:\n%s", err, stderr)
			}
			for _, want := range tt.wantEvidence {
				if !strings.Contains(stdout, want) {
					t.Fatalf("expected runs evidence output to contain %q, got:\n%s", want, stdout)
				}
			}
		})
	}
}

func TestRunsStartIssueNumberNoChangeSkipsDeliveryWithoutChangeProvider(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Start shorthand no-change delivery",
			Body:                "No repository changes should skip delivery artifacts.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	if _, stderr, err := executeForTest(t, "init", "--provider", "github"); err != nil {
		t.Fatalf("expected init to configure current ForgeProject: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "start", "--agent-preset", "harmless-echo", "123")
	if err != nil {
		t.Fatalf("expected shorthand runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Started AgentRun 1",
		"WorkItem: github://github.com/owner/repo/issues/123",
		"Status: completed",
		"Branch: forgelane/issue-123",
		"Delivery: skipped (no repository changes)",
		"Next: forgelane runs show 1",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs start output to contain %q, got:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "ChangeSet ID:") {
		t.Fatalf("expected no ChangeSet output for no-change run, got:\n%s", stdout)
	}
	assertTableCount(t, homeDir, "agent_runs", 1)
	assertTableCount(t, homeDir, "runner_jobs", 1)
	assertTableCount(t, homeDir, "workspaces", 1)
	assertTableCount(t, homeDir, "change_sets", 0)
	assertTableCount(t, homeDir, "commit_refs", 0)
	assertTableCount(t, homeDir, "control_actions", 1)
}

func TestRunsStartReportsRecoverableDeliveryFailures(t *testing.T) {
	tests := []struct {
		name           string
		changeProvider workflow.ChangeProvider
		wantErr        string
		wantStart      []string
		wantShow       []string
		wantEvents     []string
	}{
		{
			name:    "missing ChangeProvider",
			wantErr: "missing ChangeProvider",
			wantStart: []string{
				"Started AgentRun 1",
				"WorkItem: github://github.com/owner/repo/issues/123",
				"Status: completed",
				"Branch: forgelane/issue-123",
				"Delivery: failed (deliver ChangeSet 1: missing ChangeProvider for provider \"github\")",
				"ChangeSet ID: 1",
				"Next: forgelane runs show 1",
				"Next: forgelane runs retry 1",
			},
			wantShow: []string{
				"Status: completed",
				"ChangeSet: 1 planned forgelane/issue-123",
				"ChangeSet commits: 1",
			},
		},
		{
			name: "branch push failure",
			changeProvider: &recordingChangeProvider{
				pushErr: os.ErrPermission,
			},
			wantErr: "push ChangeSet branch failed",
			wantStart: []string{
				"Started AgentRun 1",
				"WorkItem: github://github.com/owner/repo/issues/123",
				"Status: completed",
				"Branch: forgelane/issue-123",
				"Delivery: failed (push ChangeSet branch failed)",
				"ChangeSet ID: 1",
				"Next: forgelane runs show 1",
				"Next: forgelane runs retry 1",
			},
			wantShow: []string{
				"Status: completed",
				"ChangeSet: 1 branch_push_failed forgelane/issue-123",
				"ChangeSet commits: 1",
			},
			wantEvents: []string{
				"change_set.branch_push_started",
				"change_set.branch_push_failed",
			},
		},
		{
			name: "draft PR failure",
			changeProvider: &recordingChangeProvider{
				pushResult: workflow.ChangeBranchPushResult{
					ChangeSetID:       1,
					BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
					PushedCommitSHAs:  []string{"abc123"},
				},
				draftPRErr: os.ErrPermission,
			},
			wantErr: "create or update draft PR failed",
			wantStart: []string{
				"Started AgentRun 1",
				"WorkItem: github://github.com/owner/repo/issues/123",
				"Status: completed",
				"Branch: forgelane/issue-123",
				"Delivery: failed (create or update draft PR failed)",
				"ChangeSet ID: 1",
				"Provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
				"Next: forgelane runs show 1",
				"Next: forgelane runs retry 1",
			},
			wantShow: []string{
				"Status: completed",
				"ChangeSet: 1 branch_ready forgelane/issue-123",
				"ChangeSet provider branch: github://github.com/owner/repo/branches/forgelane/issue-123",
				"ChangeSet commits: 1",
			},
			wantEvents: []string{
				"change_set.branch_push_started",
				"change_set.branch_push_succeeded",
				"change_set.draft_pr_started",
				"change_set.draft_pr_failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workingDir := t.TempDir()
			homeDir := t.TempDir()
			runGit(t, workingDir, "init")
			runGit(t, workingDir, "config", "user.email", "test@example.com")
			runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
			runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
			if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
				t.Fatalf("write source repo file: %v", err)
			}
			runGit(t, workingDir, "add", "README.md")
			runGit(t, workingDir, "commit", "-m", "initial")
			withWorkingDir(t, workingDir)
			withHomeDir(t, homeDir)

			fakeProvider := &recordingWorkItemProvider{
				issue: workitems.ProviderIssue{
					ProviderRef:         "github://github.com/owner/repo/issues/123",
					RepositoryRef:       "github://github.com/owner/repo",
					Provider:            "github",
					ProviderIssueNumber: 123,
					Title:               "Start recoverable delivery failure",
					Body:                "Delivery failures should keep inspectable ChangeSet state.",
					Status:              "open",
					RawStatus:           "open",
					URL:                 "https://github.com/owner/repo/issues/123",
					ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
				},
			}

			stdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider:    fakeProvider,
				AgentCommandPlanner: changingCommandPlanner{},
				ChangeProvider:      tt.changeProvider,
			}, "runs", "start", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123")
			if err == nil {
				t.Fatal("expected runs start to fail")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error to contain %q, got %v\nstderr:\n%s", tt.wantErr, err, stderr)
			}
			for _, want := range tt.wantStart {
				if !strings.Contains(stdout, want) {
					t.Fatalf("expected failed runs start output to contain %q, got:\n%s", want, stdout)
				}
			}
			if strings.Contains(stdout, "branch_push_failed") || strings.Contains(stdout, "branch_ready") {
				t.Fatalf("expected product summary not to expose internal delivery status, got:\n%s", stdout)
			}

			showStdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, "runs", "show", "1")
			if err != nil {
				t.Fatalf("expected runs show after failed start to succeed: %v\nstderr:\n%s", err, stderr)
			}
			for _, want := range tt.wantShow {
				if !strings.Contains(showStdout, want) {
					t.Fatalf("expected runs show output to contain %q, got:\n%s", want, showStdout)
				}
			}

			if len(tt.wantEvents) > 0 {
				eventsStdout, stderr, err := executeForTestWithOptions(t, Options{
					WorkItemProvider: fakeProvider,
				}, "events", "list", "--run", "1")
				if err != nil {
					t.Fatalf("expected events list after failed start to succeed: %v\nstderr:\n%s", err, stderr)
				}
				assertInOrder(t, eventsStdout, tt.wantEvents)
			}
		})
	}
}

func TestRunsExecuteFailsClearlyWhenDeliveryProviderIsMissing(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Materialize repository changes",
			Body:                "Commit local Workspace repository changes.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "create", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "prepare", strconv.FormatInt(runID, 10)); err != nil {
		t.Fatalf("expected runs prepare to succeed: %v\nstderr:\n%s", err, stderr)
	}

	_, stderr, err = executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "execute", strconv.FormatInt(runID, 10))
	if err == nil {
		t.Fatal("expected runs execute to fail without a ChangeProvider")
	}
	if !strings.Contains(err.Error(), "missing ChangeProvider") {
		t.Fatalf("expected missing ChangeProvider error, got %v\nstderr:\n%s", err, stderr)
	}

	showStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "show", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs show to succeed after delivery failure: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"Status: completed",
		"ChangeSet: 1 planned forgelane/issue-123",
		"ChangeSet commits: 1",
	} {
		if !strings.Contains(showStdout, want) {
			t.Fatalf("expected runs show output to contain %q, got:\n%s", want, showStdout)
		}
	}
}

func TestRunsExecutePushesBranchThroughChangeProviderWithoutAgentProviderCredentials(t *testing.T) {
	t.Setenv("FORGELANE_GITHUB_TOKEN", "sensitive-forgelane-github-token")
	t.Setenv("GITHUB_TOKEN", "sensitive-provider-token")
	t.Setenv("FORGELANE_GITLAB_TOKEN", "sensitive-forgelane-gitlab-token")
	t.Setenv("GITLAB_TOKEN", "sensitive-gitlab-token")
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Push ForgeLane branch",
			Body:                "Push the ChangeSet branch through the Change Provider.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID: 1,
			ChangeRef:   "github://github.com/owner/repo/pulls/10",
			Draft:       true,
			ProviderSnapshot: map[string]any{
				"number": float64(10),
				"draft":  true,
			},
		},
	}

	createStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "create", "--agent-preset", "harmless-echo", "github://github.com/owner/repo/issues/123")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	runID := extractCreatedAgentRunID(t, createStdout)

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "prepare", strconv.FormatInt(runID, 10)); err != nil {
		t.Fatalf("expected runs prepare to succeed: %v\nstderr:\n%s", err, stderr)
	}

	executeStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider:    fakeProvider,
		AgentCommandPlanner: tokenCheckingChangingCommandPlanner{},
		ChangeProvider:      changeProvider,
	}, "runs", "execute", strconv.FormatInt(runID, 10))
	if err != nil {
		t.Fatalf("expected runs execute to succeed: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"ChangeSet: 1 draft_open forgelane/issue-123",
		"Event: control_action.executing",
		"Event: change_set.branch_push_started",
		"Event: change_set.branch_push_succeeded",
		"Event: change_set.draft_pr_started",
		"Event: change_set.draft_pr_succeeded",
	} {
		if !strings.Contains(executeStdout, want) {
			t.Fatalf("expected runs execute output to contain %q, got:\n%s", want, executeStdout)
		}
	}
	if len(changeProvider.calls) != 1 {
		t.Fatalf("expected one fake Change Provider push, got %#v", changeProvider.calls)
	}
	if len(changeProvider.draftPRCalls) != 1 {
		t.Fatalf("expected one fake Change Provider draft PR call, got %#v", changeProvider.draftPRCalls)
	}

	logsStdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "logs", strconv.FormatInt(runID, 10), "--stream", "stdout")
	if err != nil {
		t.Fatalf("expected runs logs to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(logsStdout, "provider-token=absent") {
		t.Fatalf("expected AgentAdapter log to prove provider token was absent, got:\n%s", logsStdout)
	}
}

func TestRunReadCommandsReturnClearErrorsForInvalidAndUnknownRunIDs(t *testing.T) {
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
			Title:               "Clear missing run errors",
			Body:                "Missing runs should fail clearly.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected runs create to initialize state: %v\nstderr:\n%s", err, stderr)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "runs show invalid id",
			args: []string{"runs", "show", "abc"},
			want: "invalid AgentRun id: abc",
		},
		{
			name: "runs evidence invalid id",
			args: []string{"runs", "evidence", "abc"},
			want: "invalid AgentRun id: abc",
		},
		{
			name: "runs report invalid id",
			args: []string{"runs", "report", "abc"},
			want: "invalid AgentRun id: abc",
		},
		{
			name: "events list invalid id",
			args: []string{"events", "list", "--run", "abc"},
			want: "invalid AgentRun id: abc",
		},
		{
			name: "runs show unknown id",
			args: []string{"runs", "show", "999"},
			want: "AgentRun not found: 999",
		},
		{
			name: "runs evidence unknown id",
			args: []string{"runs", "evidence", "999"},
			want: "AgentRun not found: 999",
		},
		{
			name: "runs report unknown id",
			args: []string{"runs", "report", "999"},
			want: "AgentRun not found: 999",
		},
		{
			name: "events list unknown id",
			args: []string{"events", "list", "--run", "999"},
			want: "AgentRun not found: 999",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := executeForTestWithOptions(t, Options{
				WorkItemProvider: fakeProvider,
			}, tc.args...)
			if err == nil {
				t.Fatal("expected command to fail")
			}
			if stdout != "" {
				t.Fatalf("expected no stdout, got %q", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("expected stderr to contain %q, got:\n%s", tc.want, stderr)
			}
		})
	}
}

func TestRunsCreateIssueNumberFailsWithoutCurrentForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef: "github://github.com/owner/repo/issues/123",
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "123")
	if err == nil {
		t.Fatal("expected shorthand runs create without current ForgeProject to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "pass a full ProviderRef or run forgelane init") {
		t.Fatalf("expected shorthand guidance, got:\n%s", stderr)
	}
	if fakeProvider.calls != 0 {
		t.Fatalf("expected shorthand resolution failure not to call provider, got %d calls", fakeProvider.calls)
	}
	assertTableCount(t, homeDir, "work_items", 0)
	assertTableCount(t, homeDir, "agent_runs", 0)
	assertTableCount(t, homeDir, "run_specs", 0)
	assertTableCount(t, homeDir, "events", 0)
}

func TestRunsCreateFullProviderRefImportDoesNotEnableIssueNumberShorthand(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "github://github.com/owner/repo/issues/123",
			RepositoryRef:       "github://github.com/owner/repo",
			Provider:            "github",
			ProviderIssueNumber: 123,
			Title:               "Full ref import does not initialize shorthand",
			Body:                "Import-side ForgeProject rows are not explicit repository config.",
			Status:              "open",
			RawStatus:           "open",
			URL:                 "https://github.com/owner/repo/issues/123",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	if _, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "github://github.com/owner/repo/issues/123"); err != nil {
		t.Fatalf("expected full ProviderRef runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProvider: fakeProvider,
	}, "runs", "create", "123")
	if err == nil {
		t.Fatal("expected shorthand runs create without forgelane init to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "pass a full ProviderRef or run forgelane init") {
		t.Fatalf("expected shorthand guidance, got:\n%s", stderr)
	}
	assertTableCount(t, homeDir, "work_items", 1)
	assertTableCount(t, homeDir, "agent_runs", 1)
	assertTableCount(t, homeDir, "run_specs", 1)
}

func TestRunsCreateSelectsWorkItemProviderFromGitLabFullRef(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "gitlab://gitlab.com/group/subgroup/project/issues/456",
			RepositoryRef:       "gitlab://gitlab.com/group/subgroup/project",
			Provider:            "gitlab",
			ProviderIssueNumber: 456,
			Title:               "Deliver GitLab ChangeSet",
			Body:                "GitLab full refs should select the GitLab WorkItem provider.",
			Status:              "open",
			RawStatus:           "opened",
			URL:                 "https://gitlab.com/group/subgroup/project/-/issues/456",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	var selectedProvider string

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProviderFactory: func(ref workitems.ProviderRef) (workitems.Provider, error) {
			selectedProvider = ref.Provider
			return fakeProvider, nil
		},
	}, "runs", "create", "--agent-preset", "harmless-echo", "gitlab://gitlab.com/group/subgroup/project/issues/456")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if selectedProvider != "gitlab" {
		t.Fatalf("expected GitLab provider selection, got %q", selectedProvider)
	}
	for _, want := range []string{
		"WorkItem: gitlab://gitlab.com/group/subgroup/project/issues/456",
		"Branch: forgelane/issue-456",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs create output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestRunsStartDeliversGitLabFullRefThroughSelectedChangeProvider(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://gitlab.com/group/subgroup/project")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "gitlab://gitlab.com/group/subgroup/project/issues/456",
			RepositoryRef:       "gitlab://gitlab.com/group/subgroup/project",
			Provider:            "gitlab",
			ProviderIssueNumber: 456,
			Title:               "Deliver GitLab ChangeSet",
			Body:                "GitLab full refs should select the GitLab WorkItem and ChangeProvider.",
			Status:              "open",
			RawStatus:           "opened",
			URL:                 "https://gitlab.com/group/subgroup/project/-/issues/456",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "gitlab://gitlab.com/group/subgroup/project/branches/forgelane/issue-456",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID: 1,
			ChangeRef:   "gitlab://gitlab.com/group/subgroup/project/merge_requests/11",
			Draft:       true,
			ProviderSnapshot: map[string]any{
				"iid":   float64(11),
				"draft": true,
			},
		},
	}
	var selectedWorkItemProvider string
	var selectedChangeProvider string

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProviderFactory: func(ref workitems.ProviderRef) (workitems.Provider, error) {
			selectedWorkItemProvider = ref.Provider
			return fakeProvider, nil
		},
		ChangeProviderFactory: func(provider string) (workflow.ChangeProvider, error) {
			selectedChangeProvider = provider
			return changeProvider, nil
		},
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "start", "--agent-preset", "harmless-echo", "gitlab://gitlab.com/group/subgroup/project/issues/456")
	if err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if selectedWorkItemProvider != "gitlab" || selectedChangeProvider != "gitlab" {
		t.Fatalf("expected GitLab provider selection, got work_item=%q change=%q", selectedWorkItemProvider, selectedChangeProvider)
	}
	for _, want := range []string{
		"WorkItem: gitlab://gitlab.com/group/subgroup/project/issues/456",
		"Delivery: draft PR ready",
		"Provider branch: gitlab://gitlab.com/group/subgroup/project/branches/forgelane/issue-456",
		"Draft PR: gitlab://gitlab.com/group/subgroup/project/merge_requests/11",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs start output to contain %q, got:\n%s", want, stdout)
		}
	}
	if len(changeProvider.calls) != 1 || changeProvider.calls[0].Provider != "gitlab" {
		t.Fatalf("expected one GitLab branch push call, got %#v", changeProvider.calls)
	}
	if len(changeProvider.draftPRCalls) != 1 || changeProvider.draftPRCalls[0].Provider != "gitlab" {
		t.Fatalf("expected one GitLab draft MR call, got %#v", changeProvider.draftPRCalls)
	}
}

func TestRunsStartDeliversSelfHostedGitLabFullRefThroughSelectedChangeProvider(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "config", "user.email", "test@example.com")
	runGit(t, workingDir, "config", "user.name", "ForgeLane Test")
	runGit(t, workingDir, "remote", "add", "origin", "https://gitlab.example.com/group/subgroup/project")
	if err := os.WriteFile(filepath.Join(workingDir, "README.md"), []byte("source repo\n"), 0o644); err != nil {
		t.Fatalf("write source repo file: %v", err)
	}
	runGit(t, workingDir, "add", "README.md")
	runGit(t, workingDir, "commit", "-m", "initial")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "gitlab://gitlab.example.com/group/subgroup/project/issues/456",
			RepositoryRef:       "gitlab://gitlab.example.com/group/subgroup/project",
			Provider:            "gitlab",
			ProviderIssueNumber: 456,
			Title:               "Deliver self-hosted GitLab ChangeSet",
			Body:                "Self-hosted GitLab refs should flow through workspace prepare and delivery.",
			Status:              "open",
			RawStatus:           "opened",
			URL:                 "https://gitlab.example.com/group/subgroup/project/-/issues/456",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "gitlab://gitlab.example.com/group/subgroup/project/branches/forgelane/issue-456",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID: 1,
			ChangeRef:   "gitlab://gitlab.example.com/group/subgroup/project/merge_requests/11",
			Draft:       true,
			ProviderSnapshot: map[string]any{
				"iid":   float64(11),
				"draft": true,
			},
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProviderFactory: func(ref workitems.ProviderRef) (workitems.Provider, error) {
			if ref.String() != "gitlab://gitlab.example.com/group/subgroup/project/issues/456" {
				t.Fatalf("unexpected WorkItem ref %s", ref.String())
			}
			return fakeProvider, nil
		},
		ChangeProviderFactory: func(provider string) (workflow.ChangeProvider, error) {
			if provider != "gitlab" {
				t.Fatalf("unexpected ChangeProvider %s", provider)
			}
			return changeProvider, nil
		},
		AgentCommandPlanner: changingCommandPlanner{},
	}, "runs", "start", "--agent-preset", "harmless-echo", "gitlab://gitlab.example.com/group/subgroup/project/issues/456")
	if err != nil {
		t.Fatalf("expected runs start to succeed: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"WorkItem: gitlab://gitlab.example.com/group/subgroup/project/issues/456",
		"Provider branch: gitlab://gitlab.example.com/group/subgroup/project/branches/forgelane/issue-456",
		"Draft PR: gitlab://gitlab.example.com/group/subgroup/project/merge_requests/11",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected runs start output to contain %q, got:\n%s", want, stdout)
		}
	}
}

func TestRunsCreateResolvesGitLabIssueNumberFromConfiguredForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://gitlab.com/group/subgroup/project")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--provider", "gitlab"); err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "gitlab://gitlab.com/group/subgroup/project/issues/456",
			RepositoryRef:       "gitlab://gitlab.com/group/subgroup/project",
			Provider:            "gitlab",
			ProviderIssueNumber: 456,
			Title:               "Resolve GitLab shorthand",
			Body:                "Numeric issue shorthand should resolve through the GitLab ForgeProject.",
			Status:              "open",
			RawStatus:           "opened",
			URL:                 "https://gitlab.com/group/subgroup/project/-/issues/456",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProviderFactory: func(ref workitems.ProviderRef) (workitems.Provider, error) {
			if ref.String() != "gitlab://gitlab.com/group/subgroup/project/issues/456" {
				t.Fatalf("unexpected resolved ref %s", ref.String())
			}
			return fakeProvider, nil
		},
	}, "runs", "create", "--agent-preset", "harmless-echo", "456")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "WorkItem: gitlab://gitlab.com/group/subgroup/project/issues/456") {
		t.Fatalf("expected GitLab WorkItem in output, got:\n%s", stdout)
	}
}

func TestRunsCreateResolvesSelfHostedGitLabIssueNumberFromConfiguredForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://gitlab.example.com/group/subgroup/project")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	if _, stderr, err := executeForTest(t, "init", "--provider", "gitlab"); err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}

	fakeProvider := &recordingWorkItemProvider{
		issue: workitems.ProviderIssue{
			ProviderRef:         "gitlab://gitlab.example.com/group/subgroup/project/issues/456",
			RepositoryRef:       "gitlab://gitlab.example.com/group/subgroup/project",
			Provider:            "gitlab",
			ProviderIssueNumber: 456,
			Title:               "Resolve self-hosted GitLab shorthand",
			Body:                "Numeric issue shorthand should preserve the configured GitLab host.",
			Status:              "open",
			RawStatus:           "opened",
			URL:                 "https://gitlab.example.com/group/subgroup/project/-/issues/456",
			ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
		},
	}

	stdout, stderr, err := executeForTestWithOptions(t, Options{
		WorkItemProviderFactory: func(ref workitems.ProviderRef) (workitems.Provider, error) {
			if ref.String() != "gitlab://gitlab.example.com/group/subgroup/project/issues/456" {
				t.Fatalf("unexpected resolved ref %s", ref.String())
			}
			return fakeProvider, nil
		},
	}, "runs", "create", "--agent-preset", "harmless-echo", "456")
	if err != nil {
		t.Fatalf("expected runs create to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "WorkItem: gitlab://gitlab.example.com/group/subgroup/project/issues/456") {
		t.Fatalf("expected self-hosted GitLab WorkItem in output, got:\n%s", stdout)
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

func TestWorkflowInitCreatesDefaultContract(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	subdir := filepath.Join(workingDir, "nested")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("create subdir: %v", err)
	}
	withWorkingDir(t, subdir)
	withHomeDir(t, t.TempDir())

	stdout, stderr, err := executeForTest(t, "workflow", "init")
	if err != nil {
		t.Fatalf("expected workflow init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Created workflow contract forgelane.workflow.json") {
		t.Fatalf("expected workflow init output to describe created contract, got:\n%s", stdout)
	}

	content, err := os.ReadFile(filepath.Join(workingDir, "forgelane.workflow.json"))
	if err != nil {
		t.Fatalf("read workflow contract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(subdir, "forgelane.workflow.json")); !os.IsNotExist(err) {
		t.Fatalf("workflow init should create the contract at the repository root, subdir stat err: %v", err)
	}
	var contract map[string]any
	if err := json.Unmarshal(content, &contract); err != nil {
		t.Fatalf("decode workflow contract: %v\n%s", err, content)
	}
	agent, ok := contract["agent"].(map[string]any)
	if !ok || agent["default_preset"] != "codex" {
		t.Fatalf("expected default codex agent preset, got %#v", contract["agent"])
	}
	tracker, ok := contract["tracker"].(map[string]any)
	if !ok {
		t.Fatalf("expected tracker section, got %#v", contract["tracker"])
	}
	labels, ok := tracker["labels"].(map[string]any)
	if !ok {
		t.Fatalf("expected tracker labels, got %#v", tracker["labels"])
	}
	for role, label := range map[string]string{
		"trigger":         "forgelane",
		"ready_for_agent": "ready-for-agent",
		"needs_info":      "needs-info",
		"ready_for_human": "ready-for-human",
	} {
		if labels[role] != label {
			t.Fatalf("expected tracker label %s=%q, got %#v", role, label, labels[role])
		}
	}
	verification, ok := contract["verification"].(map[string]any)
	if !ok || verification["test_command"] != "go test ./..." {
		t.Fatalf("expected verification test command, got %#v", contract["verification"])
	}
	if evidence, ok := verification["evidence"].([]any); !ok || len(evidence) == 0 {
		t.Fatalf("expected verification evidence requirements, got %#v", verification["evidence"])
	}
	approvals, ok := contract["approvals"].(map[string]any)
	if !ok || approvals["provider_mutations"] == "" || approvals["privileged_actions"] == "" {
		t.Fatalf("expected approval policy hints, got %#v", contract["approvals"])
	}
	notes, ok := contract["automation_notes"].([]any)
	if !ok || len(notes) == 0 {
		t.Fatalf("expected automation notes, got %#v", contract["automation_notes"])
	}
	if _, ok := contract["watcher"]; ok {
		t.Fatalf("workflow contract should document future watcher behavior without watcher config, got %#v", contract["watcher"])
	}
	if strings.Contains(string(content), "run_id") || strings.Contains(string(content), "secret") || strings.Contains(string(content), "last_error") {
		t.Fatalf("workflow contract should not contain instance or run state:\n%s", content)
	}
}

func TestWorkflowInitRefusesToOverwriteExistingContract(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	withWorkingDir(t, workingDir)
	withHomeDir(t, t.TempDir())
	contractPath := filepath.Join(workingDir, "forgelane.workflow.json")
	existing := []byte("{\"version\":99}\n")
	if err := os.WriteFile(contractPath, existing, 0o644); err != nil {
		t.Fatalf("write existing workflow contract: %v", err)
	}

	stdout, stderr, err := executeForTest(t, "workflow", "init")
	if err == nil {
		t.Fatal("expected workflow init to refuse existing contract")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "workflow contract forgelane.workflow.json already exists") {
		t.Fatalf("expected existing contract error, got:\n%s", stderr)
	}
	content, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read workflow contract: %v", err)
	}
	if string(content) != string(existing) {
		t.Fatalf("workflow init should not overwrite existing contract:\n%s", content)
	}
}

func TestInitWithWorkflowCreatesDefaultContract(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--repo-url", "https://github.com/owner/repo", "--with-workflow")
	if err != nil {
		t.Fatalf("expected init with workflow to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	for _, want := range []string{
		"Configured ForgeProject github://github.com/owner/repo",
		"Created workflow contract forgelane.workflow.json",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected init output to contain %q, got:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(workingDir, "forgelane.workflow.json")); err != nil {
		t.Fatalf("expected workflow contract to exist: %v", err)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
}

func TestPlainInitSuggestsWorkflowContractWithoutCreatingIt(t *testing.T) {
	workingDir := t.TempDir()
	runGit(t, workingDir, "init")
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
	if !strings.Contains(stdout, "Workflow contract missing; run `forgelane workflow init` or `forgelane init --with-workflow` to create forgelane.workflow.json") {
		t.Fatalf("expected init output to suggest explicit workflow contract creation, got:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(workingDir, "forgelane.workflow.json")); !os.IsNotExist(err) {
		t.Fatalf("plain init should not create workflow contract, stat err: %v", err)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
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

func TestInitWithGitLabRepoShorthandPersistsForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "gitlab", "--repo", "group/subgroup/project")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject gitlab://gitlab.com/group/subgroup/project") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"gitlab://gitlab.com/group/subgroup/project"})
}

func TestInitWithSelfHostedGitLabRepoURLPersistsForgeProject(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "gitlab", "--repo-url", "https://gitlab.example.com/group/subgroup/project.git")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject gitlab://gitlab.example.com/group/subgroup/project") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"gitlab://gitlab.example.com/group/subgroup/project"})
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

func TestInitInfersGitLabForgeProjectFromOriginRemote(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "git@gitlab.com:group/subgroup/project.git")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject gitlab://gitlab.com/group/subgroup/project") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"gitlab://gitlab.com/group/subgroup/project"})
}

func TestInitInfersSelfHostedGitLabForgeProjectFromOriginRemote(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "git@gitlab.example.com:group/subgroup/project.git")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init", "--provider", "gitlab")
	if err != nil {
		t.Fatalf("expected init to succeed: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject gitlab://gitlab.example.com/group/subgroup/project") {
		t.Fatalf("expected init output to describe configured ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"gitlab://gitlab.example.com/group/subgroup/project"})
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

func TestInitAcceptsSupportedGitLabRemoteURLForms(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
	}{
		{name: "https", repoURL: "https://gitlab.com/group/subgroup/project"},
		{name: "https git suffix", repoURL: "https://gitlab.com/group/subgroup/project.git"},
		{name: "ssh scp", repoURL: "git@gitlab.com:group/subgroup/project.git"},
		{name: "ssh url", repoURL: "ssh://git@gitlab.com/group/subgroup/project.git"},
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
			assertForgeProjects(t, homeDir, []string{"gitlab://gitlab.com/group/subgroup/project"})
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
			name:    "invalid GitLab repo ref",
			args:    []string{"init", "--provider", "gitlab", "--repo", "group"},
			wantErr: `invalid GitLab repository ref "group"`,
		},
		{
			name:    "branch webpage url",
			args:    []string{"init", "--repo-url", "https://github.com/owner/repo/tree/main"},
			wantErr: `invalid GitHub repository URL "https://github.com/owner/repo/tree/main"`,
		},
		{
			name:    "ambiguous shorthand in repo url",
			args:    []string{"init", "--repo-url", "owner/repo"},
			wantErr: `unsupported repository URL "owner/repo"`,
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

func TestBareInitRejectsUnsupportedOriginRemote(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://git.example.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init")
	if err == nil {
		t.Fatal("expected bare init with unsupported origin to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, `unsupported repository URL "https://git.example.com/owner/repo"`) {
		t.Fatalf("expected unsupported origin error, got:\n%s", stderr)
	}
	stateDir := filepath.Join(homeDir, ".forgelane")
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("unsupported bare init should not create state directory %s, stat err: %v", stateDir, err)
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

func TestInitInfersOriginWithoutProvider(t *testing.T) {
	workingDir := t.TempDir()
	homeDir := t.TempDir()
	runGit(t, workingDir, "init")
	runGit(t, workingDir, "remote", "add", "origin", "https://github.com/owner/repo")
	withWorkingDir(t, workingDir)
	withHomeDir(t, homeDir)

	stdout, stderr, err := executeForTest(t, "init")
	if err != nil {
		t.Fatalf("expected bare init to infer origin: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "Configured ForgeProject github://github.com/owner/repo") {
		t.Fatalf("expected init output to describe inferred ForgeProject, got:\n%s", stdout)
	}

	assertNoRepoLocalConfig(t, workingDir)
	assertForgeProjects(t, homeDir, []string{"github://github.com/owner/repo"})
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

type changingCommandPlanner struct{}

func (changingCommandPlanner) PlanAgentCommand(input workflow.AgentCommandPlanInput) (workflow.AgentCommandPlan, error) {
	return workflow.AgentCommandPlan{
		Executable:       "sh",
		Args:             []string{"-c", "printf 'forgelane test change\\n' > forgelane-agent-output.txt"},
		WorkingDirectory: input.Workspace.Paths.Repo,
		Env:              []string{"PATH=" + os.Getenv("PATH")},
		StdoutPath:       filepath.Join(input.Workspace.Paths.Logs, "stdout.log"),
		StderrPath:       filepath.Join(input.Workspace.Paths.Logs, "stderr.log"),
	}, nil
}

type tokenCheckingChangingCommandPlanner struct{}

func (tokenCheckingChangingCommandPlanner) PlanAgentCommand(input workflow.AgentCommandPlanInput) (workflow.AgentCommandPlan, error) {
	script := `if [ -n "${FORGELANE_GITHUB_TOKEN:-}" ] || [ -n "${GITHUB_TOKEN:-}" ] || [ -n "${GH_TOKEN:-}" ] || [ -n "${FORGELANE_GITLAB_TOKEN:-}" ] || [ -n "${GITLAB_TOKEN:-}" ]; then
  printf 'provider-token=present\n'
else
  printf 'provider-token=absent\n'
fi
printf 'forgelane test change\n' > forgelane-agent-output.txt
`
	return workflow.AgentCommandPlan{
		Executable:       "sh",
		Args:             []string{"-c", script},
		WorkingDirectory: input.Workspace.Paths.Repo,
		Env:              []string{"PATH=" + os.Getenv("PATH")},
		StdoutPath:       filepath.Join(input.Workspace.Paths.Logs, "stdout.log"),
		StderrPath:       filepath.Join(input.Workspace.Paths.Logs, "stderr.log"),
	}, nil
}

type recordingChangeProvider struct {
	pushResult       workflow.ChangeBranchPushResult
	pushErr          error
	calls            []workflow.ChangeBranchPushPlan
	draftPRResult    workflow.ChangeDraftPRResult
	draftPRErr       error
	draftPRCalls     []workflow.ChangeDraftPRPlan
	providerPRReport workflow.ProviderPRReport
	providerPRErr    error
	prReportCalls    []workflow.ProviderPRRef
}

func (provider *recordingChangeProvider) PushChangeSetBranch(_ context.Context, plan workflow.ChangeBranchPushPlan) (workflow.ChangeBranchPushResult, error) {
	provider.calls = append(provider.calls, plan)
	if provider.pushErr != nil {
		return workflow.ChangeBranchPushResult{}, provider.pushErr
	}
	return provider.pushResult, nil
}

func (provider *recordingChangeProvider) CreateOrUpdateDraftPR(_ context.Context, plan workflow.ChangeDraftPRPlan) (workflow.ChangeDraftPRResult, error) {
	provider.draftPRCalls = append(provider.draftPRCalls, plan)
	if provider.draftPRErr != nil {
		return workflow.ChangeDraftPRResult{}, provider.draftPRErr
	}
	return provider.draftPRResult, nil
}

func (provider *recordingChangeProvider) GetProviderPR(_ context.Context, ref workflow.ProviderPRRef) (workflow.ProviderPRReport, error) {
	provider.prReportCalls = append(provider.prReportCalls, ref)
	if provider.providerPRErr != nil {
		return workflow.ProviderPRReport{}, provider.providerPRErr
	}
	report := provider.providerPRReport
	if report.Ref == "" {
		report.Ref = ref.String()
	}
	if report.Provider == "" {
		report.Provider = ref.Provider
	}
	if report.Repository == "" {
		report.Repository = ref.RepositoryRef()
	}
	if report.Number == 0 {
		report.Number = ref.Number
	}
	return report, nil
}

type changeProviderWithoutReporter struct{}

func (changeProviderWithoutReporter) PushChangeSetBranch(context.Context, workflow.ChangeBranchPushPlan) (workflow.ChangeBranchPushResult, error) {
	return workflow.ChangeBranchPushResult{}, nil
}

func (changeProviderWithoutReporter) CreateOrUpdateDraftPR(context.Context, workflow.ChangeDraftPRPlan) (workflow.ChangeDraftPRResult, error) {
	return workflow.ChangeDraftPRResult{}, nil
}

func gitOutput(t *testing.T, workingDir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = workingDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
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

func readWorkItemTitle(t *testing.T, homeDir string, providerRef string) string {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	var title string
	err := db.QueryRow(
		"SELECT title FROM work_items WHERE provider_ref = ?",
		providerRef,
	).Scan(&title)
	if err != nil {
		t.Fatalf("query WorkItem title %s: %v", providerRef, err)
	}
	return title
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

type controlActionAudit struct {
	actionType  string
	status      string
	requestedBy string
	input       map[string]any
}

func readControlActions(t *testing.T, homeDir string) []controlActionAudit {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	rows, err := db.Query("SELECT type, status, requested_by, input FROM control_actions ORDER BY id")
	if err != nil {
		t.Fatalf("query ControlActions: %v", err)
	}
	defer rows.Close()

	var actions []controlActionAudit
	for rows.Next() {
		var action controlActionAudit
		var inputJSON string
		if err := rows.Scan(&action.actionType, &action.status, &action.requestedBy, &inputJSON); err != nil {
			t.Fatalf("scan ControlAction: %v", err)
		}
		if err := json.Unmarshal([]byte(inputJSON), &action.input); err != nil {
			t.Fatalf("decode ControlAction input %q: %v", inputJSON, err)
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ControlActions: %v", err)
	}
	return actions
}

func assertControlAction(t *testing.T, action controlActionAudit, wantType string, wantStatus string, wantRequestedBy string, wantInput map[string]any) {
	t.Helper()

	if action.actionType != wantType || action.status != wantStatus || action.requestedBy != wantRequestedBy {
		t.Fatalf("unexpected ControlAction: got type=%q status=%q requested_by=%q input=%#v; want type=%q status=%q requested_by=%q",
			action.actionType,
			action.status,
			action.requestedBy,
			action.input,
			wantType,
			wantStatus,
			wantRequestedBy,
		)
	}
	for key, want := range wantInput {
		if got := action.input[key]; got != want {
			t.Fatalf("expected ControlAction %s input[%q] = %#v, got %#v in %#v", wantType, key, want, got, action.input)
		}
	}
}

func readEventPayload(t *testing.T, homeDir string, eventType string) map[string]any {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	var payloadJSON string
	if err := db.QueryRow("SELECT payload FROM events WHERE type = ? ORDER BY id DESC LIMIT 1", eventType).Scan(&payloadJSON); err != nil {
		t.Fatalf("query Event payload %s: %v", eventType, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode Event payload %s: %v\n%s", eventType, err, payloadJSON)
	}
	return payload
}

func readEventPayloadForControlAction(t *testing.T, homeDir string, eventType string, controlActionID int64) map[string]any {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	var payloadJSON string
	if err := db.QueryRow("SELECT payload FROM events WHERE type = ? AND control_action_id = ? ORDER BY id DESC LIMIT 1", eventType, controlActionID).Scan(&payloadJSON); err != nil {
		t.Fatalf("query Event payload %s for ControlAction %d: %v", eventType, controlActionID, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode Event payload %s for ControlAction %d: %v\n%s", eventType, controlActionID, err, payloadJSON)
	}
	return payload
}

func assertLogSegmentStreams(t *testing.T, homeDir string, wantStreams []string) {
	t.Helper()

	db := openStateDB(t, homeDir)
	defer db.Close()

	rows, err := db.Query("SELECT DISTINCT stream FROM log_segments ORDER BY stream")
	if err != nil {
		t.Fatalf("query LogSegment streams: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var stream string
		if err := rows.Scan(&stream); err != nil {
			t.Fatalf("scan LogSegment stream: %v", err)
		}
		got = append(got, stream)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate LogSegment streams: %v", err)
	}
	slices.Sort(wantStreams)
	if strings.Join(got, "\n") != strings.Join(wantStreams, "\n") {
		t.Fatalf("unexpected LogSegment streams:\n got: %q\nwant: %q", got, wantStreams)
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

func extractCreatedAgentRunID(t *testing.T, stdout string) int64 {
	t.Helper()

	for _, line := range strings.Split(stdout, "\n") {
		idText, ok := strings.CutPrefix(line, "Created AgentRun ")
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil {
			t.Fatalf("parse created AgentRun id from %q: %v", line, err)
		}
		return id
	}
	t.Fatalf("created AgentRun id not found in output:\n%s", stdout)
	return 0
}

func assertInOrder(t *testing.T, text string, values []string) {
	t.Helper()

	previous := -1
	for _, value := range values {
		index := strings.Index(text, value)
		if index == -1 {
			t.Fatalf("expected %q in text:\n%s", value, text)
		}
		if index <= previous {
			t.Fatalf("expected %q to appear after previous value in text:\n%s", value, text)
		}
		previous = index
	}
}

type recordingWorkItemProvider struct {
	issue      workitems.ProviderIssue
	listIssues []workitems.ProviderIssue
	err        error
	listErr    error
	calls      int
	listCalls  int
	lastList   workitems.ProviderIssueListInput
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

func (provider *recordingWorkItemProvider) ListIssues(_ context.Context, input workitems.ProviderIssueListInput) ([]workitems.ProviderIssue, error) {
	provider.listCalls++
	provider.lastList = input
	if provider.listErr != nil {
		return nil, provider.listErr
	}
	return slices.Clone(provider.listIssues), nil
}
