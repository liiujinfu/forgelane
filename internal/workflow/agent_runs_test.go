package workflow_test

import (
	"encoding/json"
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
