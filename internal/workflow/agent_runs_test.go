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
		WorkItem: importResult.WorkItem,
	})
	if err != nil {
		t.Fatalf("create planned AgentRun: %v", err)
	}
	paths := workflow.WorkspacePaths{
		Root:      filepath.Join(t.TempDir(), "workspace"),
		Repo:      filepath.Join(t.TempDir(), "workspace", "repo"),
		Logs:      filepath.Join(t.TempDir(), "workspace", "logs"),
		Artifacts: filepath.Join(t.TempDir(), "workspace", "artifacts"),
		Tmp:       filepath.Join(t.TempDir(), "workspace", "tmp"),
	}
	if _, err := instanceStore.AllocateWorkspace(createResult.AgentRun.ID, paths); err != nil {
		t.Fatalf("allocate Workspace: %v", err)
	}
	if _, err := instanceStore.MarkWorkspaceReady(createResult.AgentRun.ID); err != nil {
		t.Fatalf("mark Workspace ready: %v", err)
	}

	result, err := workflow.ExecuteAgentRunCommand(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		startedCaptureErrorRunner{},
		createResult.AgentRun.ID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "failed" {
		t.Fatalf("expected capture error to fail AgentRun, got %q", result.AgentRun.Status)
	}
	if got := eventTypes(result.Events); got != "agent_command.completed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

type staticCommandPlanner struct{}

func (staticCommandPlanner) PlanAgentCommand(workflow.AgentCommandPlanInput) (workflow.AgentCommandPlan, error) {
	return workflow.AgentCommandPlan{Executable: "true"}, nil
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
