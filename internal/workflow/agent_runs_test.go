package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	store "github.com/liiujinfu/forgelane/internal/store/sqlite"
	"github.com/liiujinfu/forgelane/internal/workflow"
	"github.com/liiujinfu/forgelane/internal/workitems"
)

func TestCreatePlannedAgentRunCreatesRunSpecAndEvents(t *testing.T) {
	instanceStore, err := store.Open(filepath.Join(t.TempDir(), "forgelane.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer instanceStore.Close()
	if err := instanceStore.Initialize(); err != nil {
		t.Fatalf("initialize store: %v", err)
	}

	importResult, err := instanceStore.ImportWorkItem(workitems.ProviderIssue{
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
	})
	if err != nil {
		t.Fatalf("import WorkItem: %v", err)
	}

	result, err := workflow.CreatePlannedAgentRun(instanceStore, workflow.CreatePlannedAgentRunInput{
		WorkItem: workflow.WorkItemSnapshot{
			ID:                  importResult.WorkItem.ID,
			ForgeProjectID:      importResult.WorkItem.ForgeProjectID,
			ProviderRef:         importResult.WorkItem.ProviderRef,
			Provider:            importResult.WorkItem.Provider,
			RepositoryRef:       importResult.WorkItem.RepositoryRef,
			ProviderIssueNumber: importResult.WorkItem.ProviderIssueNumber,
			Title:               importResult.WorkItem.Title,
			Body:                importResult.WorkItem.Body,
			Status:              importResult.WorkItem.Status,
			ProviderStatusRaw:   importResult.WorkItem.ProviderStatusRaw,
			URL:                 importResult.WorkItem.URL,
			ProviderUpdatedAt:   importResult.WorkItem.ProviderUpdatedAt,
			ImportedAt:          importResult.WorkItem.ImportedAt,
			RefreshedAt:         importResult.WorkItem.RefreshedAt,
		},
	})
	if err != nil {
		t.Fatalf("create planned AgentRun: %v", err)
	}

	if result.AgentRun.Status != "planned" {
		t.Fatalf("expected planned AgentRun, got %q", result.AgentRun.Status)
	}
	if result.Branch != "forgelane/issue-123" {
		t.Fatalf("expected branch forgelane/issue-123, got %q", result.Branch)
	}
	if got := eventTypes(result.Events); got != "control_action.succeeded,agent_run.created,run_spec.created" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	var spec map[string]any
	if err := json.Unmarshal([]byte(result.RunSpec.SpecJSON), &spec); err != nil {
		t.Fatalf("decode RunSpec JSON: %v\n%s", err, result.RunSpec.SpecJSON)
	}
	if got := spec["branch"]; got != "forgelane/issue-123" {
		t.Fatalf("expected RunSpec branch forgelane/issue-123, got %#v", got)
	}
	if got := spec["run_id"]; got != "run_1" {
		t.Fatalf("expected RunSpec run_id run_1, got %#v", got)
	}
	agentAdapter, ok := spec["agent_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("expected RunSpec agent_adapter object, got %#v", spec["agent_adapter"])
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
}

func TestExecuteAgentRunCommandFailsWhenStartedRunnerReportsCaptureError(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommand(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		startedCaptureErrorRunner{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "failed" {
		t.Fatalf("expected capture error to fail AgentRun, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "failed" {
		t.Fatalf("expected capture error to fail RunnerJob, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "agent_command.failed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

func TestExecuteAgentRunCommandRecordsSuccessfulTerminalOutcome(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommand(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "completed" {
		t.Fatalf("expected successful command to complete AgentRun, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "completed" {
		t.Fatalf("expected successful command to complete RunnerJob, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "agent_command.completed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	events, err := instanceStore.ListEventsForAgentRun(runID)
	if err != nil {
		t.Fatalf("list Events: %v", err)
	}
	if got := eventTypes(events); got != "control_action.succeeded,agent_run.created,run_spec.created,workspace.allocated,workspace.prepared,agent_command.started,agent_command.completed" {
		t.Fatalf("unexpected persisted Event sequence %s", got)
	}
}

func TestExecuteAgentRunCommandRecordsNonZeroExitAsFailedWithLogs(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommand(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		nonZeroExitCommandRunner{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "failed" {
		t.Fatalf("expected non-zero command to fail AgentRun, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "failed" {
		t.Fatalf("expected non-zero command to fail RunnerJob, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "agent_command.failed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	segments, err := instanceStore.ListLogSegmentsForAgentRun(runID, "")
	if err != nil {
		t.Fatalf("list LogSegments: %v", err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected one persisted LogSegment, got %d", len(segments))
	}
	if segments[0].Stream != "stderr" || segments[0].Preview != "failed command\n" {
		t.Fatalf("unexpected persisted LogSegment: %#v", segments[0])
	}
}

func TestExecuteAgentRunCommandEnforcesRunSpecTimeout(t *testing.T) {
	instanceStore, runID := preparedAgentRunWithOptions(t, preparedAgentRunOptions{
		CommandTimeout: 10 * time.Millisecond,
	})

	result, err := workflow.ExecuteAgentRunCommand(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		blockUntilContextDoneRunner{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "timed_out" {
		t.Fatalf("expected timeout to mark AgentRun timed_out, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "timed_out" {
		t.Fatalf("expected timeout to mark RunnerJob timed_out, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "agent_command.timed_out" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

func TestExecuteAgentRunCommandRecordsCancellationDistinctly(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := workflow.ExecuteAgentRunCommand(
		ctx,
		instanceStore,
		staticCommandPlanner{},
		blockUntilContextDoneRunner{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "cancelled" {
		t.Fatalf("expected cancellation to mark AgentRun cancelled, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "cancelled" {
		t.Fatalf("expected cancellation to mark RunnerJob cancelled, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "agent_command.cancelled" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

func TestExecuteAgentRunCommandRecordsPreStartCancellationDistinctly(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommand(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		preStartCancelledRunner{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "cancelled" {
		t.Fatalf("expected pre-start cancellation to mark AgentRun cancelled, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "cancelled" {
		t.Fatalf("expected pre-start cancellation to mark RunnerJob cancelled, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "agent_command.cancelled" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

type staticCommandPlanner struct{}

func (staticCommandPlanner) PlanAgentCommand(workflow.AgentCommandPlanInput) (workflow.AgentCommandPlan, error) {
	return workflow.AgentCommandPlan{Executable: "true"}, nil
}

type successfulCommandRunner struct{}

func (successfulCommandRunner) RunAgentCommand(context.Context, workflow.AgentCommandPlan) (workflow.AgentCommandRunResult, error) {
	return workflow.AgentCommandRunResult{
		ExitCode:       0,
		ProcessStarted: true,
	}, nil
}

type nonZeroExitCommandRunner struct{}

func (nonZeroExitCommandRunner) RunAgentCommand(context.Context, workflow.AgentCommandPlan) (workflow.AgentCommandRunResult, error) {
	return workflow.AgentCommandRunResult{
		ExitCode:       2,
		StderrBytes:    int64(len("failed command\n")),
		LogSegments:    []workflow.LogSegmentPlan{{Stream: "stderr", Sequence: 1, ByteStart: 0, ByteEnd: int64(len("failed command\n")), Preview: "failed command\n", ArtifactPath: "logs/stderr.log"}},
		ProcessStarted: true,
	}, errors.New("exit status 2")
}

type blockUntilContextDoneRunner struct{}

func (blockUntilContextDoneRunner) RunAgentCommand(ctx context.Context, _ workflow.AgentCommandPlan) (workflow.AgentCommandRunResult, error) {
	<-ctx.Done()
	return workflow.AgentCommandRunResult{
		ExitCode:       -1,
		ProcessStarted: true,
	}, ctx.Err()
}

type preStartCancelledRunner struct{}

func (preStartCancelledRunner) RunAgentCommand(context.Context, workflow.AgentCommandPlan) (workflow.AgentCommandRunResult, error) {
	return workflow.AgentCommandRunResult{}, context.Canceled
}

type startedCaptureErrorRunner struct{}

func (startedCaptureErrorRunner) RunAgentCommand(context.Context, workflow.AgentCommandPlan) (workflow.AgentCommandRunResult, error) {
	return workflow.AgentCommandRunResult{
		ExitCode:       0,
		ProcessStarted: true,
	}, errors.New("write stdout log file: permission denied")
}

func eventTypes(events []workflow.Event) string {
	if len(events) == 0 {
		return ""
	}
	types := events[0].Type
	for _, event := range events[1:] {
		types += "," + event.Type
	}
	return types
}

func preparedAgentRun(t *testing.T) (*store.Store, int64) {
	return preparedAgentRunWithOptions(t, preparedAgentRunOptions{})
}

type preparedAgentRunOptions struct {
	CommandTimeout time.Duration
}

func preparedAgentRunWithOptions(t *testing.T, options preparedAgentRunOptions) (*store.Store, int64) {
	t.Helper()

	instanceStore, err := store.Open(filepath.Join(t.TempDir(), "forgelane.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := instanceStore.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	if err := instanceStore.Initialize(); err != nil {
		t.Fatalf("initialize store: %v", err)
	}

	importResult, err := instanceStore.ImportWorkItem(workitems.ProviderIssue{
		ProviderRef:         "github://github.com/owner/repo/issues/123",
		RepositoryRef:       "github://github.com/owner/repo",
		Provider:            "github",
		ProviderIssueNumber: 123,
		Title:               "Execute command",
		Body:                "Run command.",
		Status:              "open",
		RawStatus:           "open",
		URL:                 "https://github.com/owner/repo/issues/123",
		ProviderUpdatedAt:   time.Date(2026, 5, 30, 9, 10, 11, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("import WorkItem: %v", err)
	}
	createResult, err := workflow.CreatePlannedAgentRun(instanceStore, workflow.CreatePlannedAgentRunInput{
		WorkItem:       importResult.WorkItem,
		CommandTimeout: options.CommandTimeout,
	})
	if err != nil {
		t.Fatalf("create planned AgentRun: %v", err)
	}
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	paths := workflow.WorkspacePaths{
		Root:      workspaceRoot,
		Repo:      filepath.Join(workspaceRoot, "repo"),
		Logs:      filepath.Join(workspaceRoot, "logs"),
		Artifacts: filepath.Join(workspaceRoot, "artifacts"),
		Tmp:       filepath.Join(workspaceRoot, "tmp"),
	}
	if _, err := instanceStore.AllocateWorkspace(createResult.AgentRun.ID, paths); err != nil {
		t.Fatalf("allocate Workspace: %v", err)
	}
	if _, err := instanceStore.MarkWorkspaceReady(createResult.AgentRun.ID); err != nil {
		t.Fatalf("mark Workspace ready: %v", err)
	}
	return instanceStore, createResult.AgentRun.ID
}
