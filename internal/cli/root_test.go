package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	t.Setenv("GITHUB_TOKEN", "sensitive-provider-token")
	t.Setenv("GH_TOKEN", "sensitive-gh-token")
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
	for _, secret := range []string{"sensitive-provider-token", "sensitive-gh-token", "sensitive-gitlab-token"} {
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
		"ChangeSet: 1 planned forgelane/issue-123",
		"Event: repository_commit.materialized",
		"Event: change_set.created",
		"Event: agent_command.completed",
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
		"ChangeSet: 1 planned forgelane/issue-123",
		"ChangeSet active run: 1",
		"ChangeSet commits: 1",
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
	})

	assertTableCount(t, homeDir, "change_sets", 1)
	assertTableCount(t, homeDir, "commit_refs", 1)
	assertTableCount(t, homeDir, "control_actions", 1)
}

func TestRunsExecutePushesBranchThroughChangeProviderWithoutAgentProviderCredentials(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "sensitive-provider-token")
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
		result: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
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
		"ChangeSet: 1 branch_ready forgelane/issue-123",
		"Event: control_action.executing",
		"Event: change_set.branch_push_started",
		"Event: change_set.branch_push_succeeded",
	} {
		if !strings.Contains(executeStdout, want) {
			t.Fatalf("expected runs execute output to contain %q, got:\n%s", want, executeStdout)
		}
	}
	if len(changeProvider.calls) != 1 {
		t.Fatalf("expected one fake Change Provider push, got %#v", changeProvider.calls)
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
	script := `if [ -n "${GITHUB_TOKEN:-}" ] || [ -n "${GH_TOKEN:-}" ] || [ -n "${GITLAB_TOKEN:-}" ]; then
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
	result workflow.ChangeBranchPushResult
	err    error
	calls  []workflow.ChangeBranchPushPlan
}

func (provider *recordingChangeProvider) PushChangeSetBranch(_ context.Context, plan workflow.ChangeBranchPushPlan) (workflow.ChangeBranchPushResult, error) {
	provider.calls = append(provider.calls, plan)
	if provider.err != nil {
		return workflow.ChangeBranchPushResult{}, provider.err
	}
	return provider.result, nil
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
