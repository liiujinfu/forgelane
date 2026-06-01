package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
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

func TestExecuteAgentRunCommandPersistsMaterializedCommitRefs(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommandAndMaterialize(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "completed" {
		t.Fatalf("expected successful command to complete AgentRun, got %q", result.AgentRun.Status)
	}
	if len(result.CommitRefs) != 1 {
		t.Fatalf("expected one commit ref in execution result, got %#v", result.CommitRefs)
	}
	if result.CommitRefs[0].RepositoryRef != "github://github.com/owner/repo" || result.CommitRefs[0].SHA != "abc123" || result.CommitRefs[0].Subject != "Materialize AgentRun 1 repository changes" {
		t.Fatalf("unexpected execution commit ref: %#v", result.CommitRefs[0])
	}
	if got := eventTypes(result.Events); got != "repository_commit.materialized,change_set.created,agent_command.completed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	detail, err := instanceStore.GetAgentRunDetail(runID)
	if err != nil {
		t.Fatalf("get AgentRun detail: %v", err)
	}
	if len(detail.CommitRefs) != 1 {
		t.Fatalf("expected one persisted commit ref in run detail, got %#v", detail.CommitRefs)
	}
	if detail.CommitRefs[0].RepositoryRef != "github://github.com/owner/repo" || detail.CommitRefs[0].SHA != "abc123" || detail.CommitRefs[0].AuthorEmail != "forgelane@localhost" {
		t.Fatalf("unexpected persisted commit ref: %#v", detail.CommitRefs[0])
	}
}

func TestExecuteAgentRunCommandCreatesActiveChangeSetFromLocalCommitRefs(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommandAndMaterialize(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.ChangeSet == nil {
		t.Fatalf("expected execution result to include active ChangeSet")
	}
	if result.ChangeSet.Provider != "github" ||
		result.ChangeSet.RepositoryRef != "github://github.com/owner/repo" ||
		result.ChangeSet.BaseBranch != "main" ||
		result.ChangeSet.BranchRef != "forgelane/issue-123" ||
		result.ChangeSet.CreatedByRunID != runID ||
		result.ChangeSet.ActiveRunID != runID ||
		len(result.ChangeSet.CommitRefs) != 1 ||
		result.ChangeSet.CommitRefs[0].SHA != "abc123" {
		t.Fatalf("unexpected execution ChangeSet: %#v", result.ChangeSet)
	}
	if got := eventTypes(result.Events); got != "repository_commit.materialized,change_set.created,agent_command.completed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	detail, err := instanceStore.GetAgentRunDetail(runID)
	if err != nil {
		t.Fatalf("get AgentRun detail: %v", err)
	}
	if detail.ChangeSet == nil {
		t.Fatalf("expected run detail to include active ChangeSet")
	}
	if detail.ChangeSet.ID != result.ChangeSet.ID ||
		detail.ChangeSet.Status != "planned" ||
		len(detail.ChangeSet.CommitRefs) != 1 ||
		detail.ChangeSet.CommitRefs[0].SHA != "abc123" {
		t.Fatalf("unexpected detail ChangeSet: %#v", detail.ChangeSet)
	}
}

func TestExecuteAgentRunCommandPushesChangeSetBranchThroughChangeProvider(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
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
				"state":  "open",
				"draft":  true,
			},
		},
	}

	result, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		changeProvider,
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command and deliver: %v", err)
	}
	if len(changeProvider.calls) != 1 {
		t.Fatalf("expected one Change Provider push, got %#v", changeProvider.calls)
	}
	if len(changeProvider.draftPRCalls) != 1 {
		t.Fatalf("expected one Change Provider draft PR call, got %#v", changeProvider.draftPRCalls)
	}
	call := changeProvider.calls[0]
	if call.ChangeSetID != result.ChangeSet.ID ||
		call.RepositoryRef != "github://github.com/owner/repo" ||
		call.BranchRef != "forgelane/issue-123" ||
		call.CommitSHAs[0] != "abc123" {
		t.Fatalf("unexpected Change Provider push plan: %#v", call)
	}
	draftPRCall := changeProvider.draftPRCalls[0]
	if draftPRCall.ChangeSetID != result.ChangeSet.ID ||
		draftPRCall.RepositoryRef != "github://github.com/owner/repo" ||
		draftPRCall.BranchRef != "forgelane/issue-123" ||
		draftPRCall.BranchProviderRef != "github://github.com/owner/repo/branches/forgelane/issue-123" ||
		draftPRCall.ExistingChangeRef != "" ||
		draftPRCall.CommitSHAs[0] != "abc123" {
		t.Fatalf("unexpected Change Provider draft PR plan: %#v", draftPRCall)
	}
	if result.ChangeSet == nil ||
		result.ChangeSet.Status != "draft_open" ||
		result.ChangeSet.BranchProviderRef != "github://github.com/owner/repo/branches/forgelane/issue-123" ||
		result.ChangeSet.ChangeRef != "github://github.com/owner/repo/pulls/10" ||
		!result.ChangeSet.ChangeDraft ||
		!strings.Contains(result.ChangeSet.ProviderSnapshot, `"draft":true`) {
		t.Fatalf("expected draft-open ChangeSet, got %#v", result.ChangeSet)
	}
	if got := eventTypes(result.Events); got != "repository_commit.materialized,change_set.created,agent_command.completed,control_action.executing,change_set.branch_push_started,change_set.branch_push_succeeded,control_action.executing,change_set.draft_pr_started,change_set.draft_pr_succeeded" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	detail, err := instanceStore.GetAgentRunDetail(runID)
	if err != nil {
		t.Fatalf("get AgentRun detail: %v", err)
	}
	if detail.ChangeSet == nil ||
		detail.ChangeSet.Status != "draft_open" ||
		detail.ChangeSet.BranchProviderRef != "github://github.com/owner/repo/branches/forgelane/issue-123" ||
		detail.ChangeSet.ChangeRef != "github://github.com/owner/repo/pulls/10" ||
		!detail.ChangeSet.ChangeDraft ||
		!strings.Contains(detail.ChangeSet.ProviderSnapshot, `"number":10`) ||
		len(detail.ChangeSet.CommitRefs) != 1 {
		t.Fatalf("unexpected persisted draft-open ChangeSet: %#v", detail.ChangeSet)
	}
}

func TestExecuteAgentRunCommandRecordsRecoverableChangeProviderPushFailure(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
	changeProvider := &recordingChangeProvider{pushErr: errors.New("provider rejected branch update with token SECRET_VALUE")}

	result, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		changeProvider,
		runID,
	)
	if err == nil {
		t.Fatal("expected branch push failure")
	}
	if !strings.Contains(err.Error(), "push ChangeSet branch") {
		t.Fatalf("expected branch push error, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRET_VALUE") {
		t.Fatalf("expected provider error detail to be sanitized, got %v", err)
	}
	if result.ChangeSet == nil ||
		result.ChangeSet.Status != "branch_push_failed" ||
		result.ChangeSet.ChangeRef != "" ||
		len(result.ChangeSet.CommitRefs) != 1 {
		t.Fatalf("expected recoverable branch-push-failed ChangeSet in result, got %#v", result.ChangeSet)
	}
	if got := eventTypes(result.Events); got != "repository_commit.materialized,change_set.created,agent_command.completed,control_action.executing,change_set.branch_push_started,change_set.branch_push_failed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	detail, err := instanceStore.GetAgentRunDetail(runID)
	if err != nil {
		t.Fatalf("get AgentRun detail: %v", err)
	}
	if detail.ChangeSet == nil ||
		detail.ChangeSet.Status != "branch_push_failed" ||
		detail.ChangeSet.ChangeRef != "" ||
		len(detail.ChangeSet.CommitRefs) != 1 {
		t.Fatalf("expected persisted ChangeSet to remain recoverable, got %#v", detail.ChangeSet)
	}
	events, err := instanceStore.ListEventsForAgentRun(runID)
	if err != nil {
		t.Fatalf("list Events: %v", err)
	}
	if got := eventTypes(events); !strings.Contains(got, "change_set.branch_push_failed") {
		t.Fatalf("expected branch push failure Event, got %s", got)
	}
}

func TestExecuteAgentRunCommandFailsClearlyWithoutChangeProviderAfterLocalCommit(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		nil,
		runID,
	)
	if err == nil {
		t.Fatal("expected missing ChangeProvider failure")
	}
	if !strings.Contains(err.Error(), "missing ChangeProvider") {
		t.Fatalf("expected missing ChangeProvider error, got %v", err)
	}
	if result.ChangeSet == nil ||
		result.ChangeSet.Status != "planned" ||
		len(result.ChangeSet.CommitRefs) != 1 {
		t.Fatalf("expected local ChangeSet state to remain inspectable, got %#v", result.ChangeSet)
	}
	if got := eventTypes(result.Events); got != "repository_commit.materialized,change_set.created,agent_command.completed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

func TestExecuteAgentRunCommandSkipsDraftPRForNoChangeRun(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
	changeProvider := &recordingChangeProvider{}

	result, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		noChangeRepositoryMaterializer{},
		changeProvider,
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command and deliver: %v", err)
	}
	if result.ChangeSet != nil {
		t.Fatalf("expected no ChangeSet for no-change run, got %#v", result.ChangeSet)
	}
	if len(changeProvider.calls) != 0 || len(changeProvider.draftPRCalls) != 0 {
		t.Fatalf("expected no Change Provider calls for no-change run, got pushes=%#v draft_prs=%#v", changeProvider.calls, changeProvider.draftPRCalls)
	}
	if got := eventTypes(result.Events); strings.Contains(got, "draft_pr") {
		t.Fatalf("expected no draft PR Events, got %s", got)
	}
}

func TestExecuteAgentRunCommandRecordsRecoverableDraftPRFailure(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRErr: errors.New("provider rejected draft PR create with token SECRET_VALUE"),
	}

	result, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		changeProvider,
		runID,
	)
	if err == nil {
		t.Fatal("expected draft PR failure")
	}
	if !strings.Contains(err.Error(), "create or update draft PR") {
		t.Fatalf("expected draft PR error, got %v", err)
	}
	if strings.Contains(err.Error(), "SECRET_VALUE") {
		t.Fatalf("expected provider error detail to be sanitized, got %v", err)
	}
	if result.ChangeSet == nil ||
		result.ChangeSet.Status != "branch_ready" ||
		result.ChangeSet.BranchProviderRef != "github://github.com/owner/repo/branches/forgelane/issue-123" ||
		result.ChangeSet.ChangeRef != "" ||
		len(result.ChangeSet.CommitRefs) != 1 {
		t.Fatalf("expected recoverable branch-ready ChangeSet in result, got %#v", result.ChangeSet)
	}
	if got := eventTypes(result.Events); got != "repository_commit.materialized,change_set.created,agent_command.completed,control_action.executing,change_set.branch_push_started,change_set.branch_push_succeeded,control_action.executing,change_set.draft_pr_started,change_set.draft_pr_failed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

func TestExecuteAgentRunCommandRecordsRecoverableInvalidDraftPRResult(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
	changeProvider := &recordingChangeProvider{
		pushResult: workflow.ChangeBranchPushResult{
			ChangeSetID:       1,
			BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
			PushedCommitSHAs:  []string{"abc123"},
		},
		draftPRResult: workflow.ChangeDraftPRResult{
			ChangeSetID:      1,
			ChangeRef:        "",
			Draft:            false,
			ProviderSnapshot: map[string]any{"number": float64(10), "draft": false},
		},
	}

	result, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		changeProvider,
		runID,
	)
	if err == nil {
		t.Fatal("expected invalid draft PR result failure")
	}
	if !strings.Contains(err.Error(), "invalid result") {
		t.Fatalf("expected invalid draft PR result error, got %v", err)
	}
	if result.ChangeSet == nil ||
		result.ChangeSet.Status != "branch_ready" ||
		result.ChangeSet.BranchProviderRef != "github://github.com/owner/repo/branches/forgelane/issue-123" ||
		result.ChangeSet.ChangeRef != "" {
		t.Fatalf("expected recoverable branch-ready ChangeSet, got %#v", result.ChangeSet)
	}
	if got := eventTypes(result.Events); got != "repository_commit.materialized,change_set.created,agent_command.completed,control_action.executing,change_set.branch_push_started,change_set.branch_push_succeeded,control_action.executing,change_set.draft_pr_started,change_set.draft_pr_failed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}
}

func TestExecuteAgentRunCommandUpdatesExistingDraftPRForActiveChangeSet(t *testing.T) {
	instanceStore, firstRunID := preparedAgentRun(t)
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

	firstResult, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		changeProvider,
		firstRunID,
	)
	if err != nil {
		t.Fatalf("execute first AgentRun command and deliver: %v", err)
	}
	if firstResult.ChangeSet == nil || firstResult.ChangeSet.ChangeRef == "" {
		t.Fatalf("expected first run to open a draft PR, got %#v", firstResult.ChangeSet)
	}

	changeProvider.pushResult = workflow.ChangeBranchPushResult{
		ChangeSetID:       firstResult.ChangeSet.ID,
		BranchProviderRef: "github://github.com/owner/repo/branches/forgelane/issue-123",
		PushedCommitSHAs:  []string{"abc123", "def456"},
	}
	changeProvider.draftPRResult = workflow.ChangeDraftPRResult{
		ChangeSetID:      firstResult.ChangeSet.ID,
		ChangeRef:        "github://github.com/owner/repo/pulls/10",
		Draft:            true,
		ProviderSnapshot: map[string]any{"number": float64(10), "draft": true, "updated": true},
	}

	secondRunID := createRetryAgentRunForExistingWorkItem(t, instanceStore, firstRunID)
	secondResult, err := workflow.ExecuteAgentRunCommandAndDeliver(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		secondCommitRepositoryChangeMaterializer{},
		changeProvider,
		secondRunID,
	)
	if err != nil {
		t.Fatalf("execute second AgentRun command and deliver: %v", err)
	}
	if len(changeProvider.draftPRCalls) != 2 {
		t.Fatalf("expected two Change Provider draft PR calls, got %#v", changeProvider.draftPRCalls)
	}
	updateCall := changeProvider.draftPRCalls[1]
	if updateCall.ChangeSetID != firstResult.ChangeSet.ID ||
		updateCall.ExistingChangeRef != "github://github.com/owner/repo/pulls/10" ||
		len(updateCall.CommitSHAs) != 2 ||
		updateCall.CommitSHAs[1] != "def456" {
		t.Fatalf("expected retry to update existing draft PR, got %#v", updateCall)
	}
	if secondResult.ChangeSet == nil ||
		secondResult.ChangeSet.ID != firstResult.ChangeSet.ID ||
		secondResult.ChangeSet.ChangeRef != "github://github.com/owner/repo/pulls/10" ||
		len(secondResult.ChangeSet.CommitRefs) != 2 {
		t.Fatalf("unexpected retry ChangeSet: %#v", secondResult.ChangeSet)
	}
}

func TestExecuteAgentRunCommandClaimsExistingActiveChangeSetFromLocalCommitRefs(t *testing.T) {
	instanceStore, firstRunID := preparedAgentRun(t)

	firstResult, err := workflow.ExecuteAgentRunCommandAndMaterialize(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		firstRunID,
	)
	if err != nil {
		t.Fatalf("execute first AgentRun command: %v", err)
	}
	if firstResult.ChangeSet == nil {
		t.Fatalf("expected first AgentRun to create ChangeSet")
	}

	secondRunID := createRetryAgentRunForExistingWorkItem(t, instanceStore, firstRunID)
	secondResult, err := workflow.ExecuteAgentRunCommandAndMaterialize(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		secondCommitRepositoryChangeMaterializer{},
		secondRunID,
	)
	if err != nil {
		t.Fatalf("execute second AgentRun command: %v", err)
	}
	if secondResult.ChangeSet == nil {
		t.Fatalf("expected second AgentRun to claim ChangeSet")
	}
	if secondResult.ChangeSet.ID != firstResult.ChangeSet.ID ||
		secondResult.ChangeSet.CreatedByRunID != firstRunID ||
		secondResult.ChangeSet.ActiveRunID != secondRunID ||
		len(secondResult.ChangeSet.CommitRefs) != 2 ||
		secondResult.ChangeSet.CommitRefs[0].SHA != "abc123" ||
		secondResult.ChangeSet.CommitRefs[1].SHA != "def456" {
		t.Fatalf("unexpected claimed ChangeSet: %#v", secondResult.ChangeSet)
	}
	if got := eventTypes(secondResult.Events); got != "repository_commit.materialized,change_set.claimed,agent_command.completed" {
		t.Fatalf("unexpected second Event sequence %s", got)
	}

	secondDetail, err := instanceStore.GetAgentRunDetail(secondRunID)
	if err != nil {
		t.Fatalf("get second AgentRun detail: %v", err)
	}
	if secondDetail.ChangeSet == nil ||
		secondDetail.ChangeSet.ID != firstResult.ChangeSet.ID ||
		secondDetail.ChangeSet.ActiveRunID != secondRunID ||
		len(secondDetail.ChangeSet.CommitRefs) != 2 {
		t.Fatalf("unexpected second detail ChangeSet: %#v", secondDetail.ChangeSet)
	}
}

func TestStoreRejectsMismatchedActiveChangeSetClaim(t *testing.T) {
	instanceStore, firstRunID := preparedAgentRun(t)

	if _, err := workflow.ExecuteAgentRunCommandAndMaterialize(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		firstRunID,
	); err != nil {
		t.Fatalf("execute first AgentRun command: %v", err)
	}

	secondRunID := createRetryAgentRunForExistingWorkItem(t, instanceStore, firstRunID)
	_, err := instanceStore.MarkAgentCommandCompleted(secondRunID, workflow.AgentCommandCompletion{
		Status: "completed",
		CommitRefs: []workflow.CommitRefPlan{
			{
				SHA:         "def456",
				Subject:     "Materialize AgentRun 2 repository changes",
				AuthorName:  "ForgeLane",
				AuthorEmail: "forgelane@localhost",
			},
		},
		ChangeSet: &workflow.ChangeSetPlan{
			WorkItemID:     1,
			WorkItemRef:    "github://github.com/owner/repo/issues/123",
			Provider:       "github",
			RepositoryRef:  "github://github.com/owner/repo",
			BaseBranch:     "main",
			BranchRef:      "forgelane/alternate-branch",
			Status:         "planned",
			CreatedByRunID: secondRunID,
			ActiveRunID:    secondRunID,
		},
	})
	if err == nil {
		t.Fatalf("expected mismatched ChangeSet claim to fail")
	}
	if !strings.Contains(err.Error(), "does not match active ChangeSet") {
		t.Fatalf("expected ChangeSet invariant error, got %v", err)
	}
}

func TestExecuteAgentRunCommandRecordsSkippedDeliveryWhenNoRepositoryChanges(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	result, err := workflow.ExecuteAgentRunCommandAndMaterialize(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		noChangeRepositoryMaterializer{},
		runID,
	)
	if err != nil {
		t.Fatalf("execute AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "completed" {
		t.Fatalf("expected successful command to complete AgentRun, got %q", result.AgentRun.Status)
	}
	if len(result.CommitRefs) != 0 {
		t.Fatalf("expected no CommitRefs for no-change delivery, got %#v", result.CommitRefs)
	}
	if got := eventTypes(result.Events); got != "repository_delivery.skipped,agent_command.completed" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	events, err := instanceStore.ListEventsForAgentRun(runID)
	if err != nil {
		t.Fatalf("list Events: %v", err)
	}
	if got := eventTypes(events); got != "control_action.succeeded,agent_run.created,run_spec.created,workspace.allocated,workspace.prepared,agent_command.started,repository_delivery.skipped,agent_command.completed" {
		t.Fatalf("unexpected persisted Event sequence %s", got)
	}
	detail, err := instanceStore.GetAgentRunDetail(runID)
	if err != nil {
		t.Fatalf("get AgentRun detail: %v", err)
	}
	if !detail.DeliverySkipped || detail.DeliverySkipReason != "no_repository_changes" {
		t.Fatalf("expected explicit no-change delivery outcome in run detail, got %#v", detail)
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

func TestRequestAgentRunStopRecordsControlActionAndCancelRequest(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
	if _, err := instanceStore.MarkAgentCommandStarted(runID); err != nil {
		t.Fatalf("mark AgentRun running: %v", err)
	}

	result, err := workflow.RequestAgentRunStop(instanceStore, runID)
	if err != nil {
		t.Fatalf("request AgentRun stop: %v", err)
	}
	if result.ControlAction.Type != "stop" || result.ControlAction.Status != "succeeded" {
		t.Fatalf("unexpected stop ControlAction: %#v", result.ControlAction)
	}
	if result.AgentRun.Status != "cancelled" {
		t.Fatalf("expected stop request to mark AgentRun cancelled, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "cancelled" {
		t.Fatalf("expected stop request to cancel RunnerJob, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "control_action.succeeded,agent_run.cancel_requested,agent_run.cancelled" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	events, err := instanceStore.ListEventsForAgentRun(runID)
	if err != nil {
		t.Fatalf("list Events: %v", err)
	}
	if got := eventTypes(events); got != "control_action.succeeded,agent_run.created,run_spec.created,workspace.allocated,workspace.prepared,agent_command.started,control_action.succeeded,agent_run.cancel_requested,agent_run.cancelled" {
		t.Fatalf("unexpected persisted Event sequence %s", got)
	}
}

func TestRequestAgentRunStopRejectsPreparedAgentRun(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)

	_, err := workflow.RequestAgentRunStop(instanceStore, runID)
	if err == nil || !strings.Contains(err.Error(), "expected running") {
		t.Fatalf("expected stop to reject non-running AgentRun, got %v", err)
	}
}

func TestAgentCommandCompletionDoesNotOverwriteStoppedRun(t *testing.T) {
	instanceStore, runID := preparedAgentRun(t)
	if _, err := instanceStore.MarkAgentCommandStarted(runID); err != nil {
		t.Fatalf("mark AgentRun running: %v", err)
	}
	if _, err := workflow.RequestAgentRunStop(instanceStore, runID); err != nil {
		t.Fatalf("request AgentRun stop: %v", err)
	}

	result, err := instanceStore.MarkAgentCommandCompleted(runID, workflow.AgentCommandCompletion{
		Status:   "completed",
		ExitCode: 0,
	})
	if err != nil {
		t.Fatalf("complete stopped AgentRun command: %v", err)
	}
	if result.AgentRun.Status != "cancelled" {
		t.Fatalf("expected stopped AgentRun to remain cancelled, got %q", result.AgentRun.Status)
	}
	if result.RunnerJob.Status != "cancelled" {
		t.Fatalf("expected stopped RunnerJob to remain cancelled, got %q", result.RunnerJob.Status)
	}
	if got := eventTypes(result.Events); got != "" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	events, err := instanceStore.ListEventsForAgentRun(runID)
	if err != nil {
		t.Fatalf("list Events: %v", err)
	}
	if got := eventTypes(events); strings.Contains(got, "agent_command.completed") {
		t.Fatalf("expected stopped run not to record command completion, got %s", got)
	}
}

func TestRequestAgentRunRetryCreatesNewRunSpecForTerminalRun(t *testing.T) {
	instanceStore, priorRunID := preparedAgentRun(t)
	failed, err := workflow.ExecuteAgentRunCommand(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		nonZeroExitCommandRunner{},
		priorRunID,
	)
	if err != nil {
		t.Fatalf("fail prior AgentRun: %v", err)
	}
	if failed.AgentRun.Status != "failed" {
		t.Fatalf("expected prior AgentRun to be failed, got %q", failed.AgentRun.Status)
	}

	result, err := workflow.RequestAgentRunRetry(instanceStore, priorRunID)
	if err != nil {
		t.Fatalf("request AgentRun retry: %v", err)
	}
	if result.ControlAction.Type != "retry" || result.ControlAction.Status != "succeeded" {
		t.Fatalf("unexpected retry ControlAction: %#v", result.ControlAction)
	}
	if result.AgentRun.ID == priorRunID {
		t.Fatalf("expected retry to create a new AgentRun, reused %d", priorRunID)
	}
	if result.AgentRun.Status != "planned" {
		t.Fatalf("expected retry AgentRun to be planned, got %q", result.AgentRun.Status)
	}
	if result.RunSpec.AgentRunID != result.AgentRun.ID || result.RunSpec.ID == 0 {
		t.Fatalf("expected retry to create a new RunSpec for the new AgentRun, got %#v", result.RunSpec)
	}
	if result.Branch != "forgelane/issue-123" {
		t.Fatalf("expected retry to keep WorkItem branch, got %q", result.Branch)
	}
	if got := eventTypes(result.Events); got != "control_action.succeeded,agent_run.created,run_spec.created" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	retryDetail, err := instanceStore.GetAgentRunDetail(result.AgentRun.ID)
	if err != nil {
		t.Fatalf("get retry AgentRun detail: %v", err)
	}
	if retryDetail.RunSpec.ID == failed.AgentRun.ID {
		t.Fatalf("expected retry RunSpec to be distinct from prior run")
	}
}

func TestRequestAgentRunRetryTargetsExistingActiveChangeSet(t *testing.T) {
	instanceStore, priorRunID := preparedAgentRun(t)
	completed, err := workflow.ExecuteAgentRunCommandAndMaterialize(
		context.Background(),
		instanceStore,
		staticCommandPlanner{},
		successfulCommandRunner{},
		staticRepositoryChangeMaterializer{},
		priorRunID,
	)
	if err != nil {
		t.Fatalf("complete prior AgentRun with ChangeSet: %v", err)
	}
	if completed.ChangeSet == nil {
		t.Fatalf("expected prior AgentRun to create active ChangeSet")
	}

	result, err := workflow.RequestAgentRunRetry(instanceStore, priorRunID)
	if err != nil {
		t.Fatalf("request AgentRun retry: %v", err)
	}
	if result.ChangeSet == nil {
		t.Fatalf("expected retry result to include targeted ChangeSet")
	}
	if result.ChangeSet.ID != completed.ChangeSet.ID ||
		result.ChangeSet.BranchRef != completed.ChangeSet.BranchRef ||
		result.ChangeSet.ActiveRunID != result.AgentRun.ID {
		t.Fatalf("expected retry to target existing active ChangeSet, got %#v", result.ChangeSet)
	}
	if got := eventTypes(result.Events); got != "control_action.succeeded,agent_run.created,run_spec.created,change_set.retry_targeted" {
		t.Fatalf("unexpected Event sequence %s", got)
	}

	retryDetail, err := instanceStore.GetAgentRunDetail(result.AgentRun.ID)
	if err != nil {
		t.Fatalf("get retry AgentRun detail: %v", err)
	}
	if retryDetail.ChangeSet == nil ||
		retryDetail.ChangeSet.ID != completed.ChangeSet.ID ||
		retryDetail.ChangeSet.ActiveRunID != result.AgentRun.ID {
		t.Fatalf("expected retry detail to show targeted active ChangeSet, got %#v", retryDetail.ChangeSet)
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

type staticRepositoryChangeMaterializer struct{}

func (staticRepositoryChangeMaterializer) SnapshotRepository(context.Context, workflow.Workspace) (workflow.RepositorySnapshot, error) {
	return workflow.RepositorySnapshot{HeadSHA: "before"}, nil
}

func (staticRepositoryChangeMaterializer) MaterializeRepositoryChanges(context.Context, workflow.Workspace, workflow.RepositorySnapshot) (workflow.RepositoryChangeMaterialization, error) {
	return workflow.RepositoryChangeMaterialization{
		CommitRefs: []workflow.CommitRefPlan{
			{
				SHA:         "abc123",
				Subject:     "Materialize AgentRun 1 repository changes",
				AuthorName:  "ForgeLane",
				AuthorEmail: "forgelane@localhost",
			},
		},
	}, nil
}

type secondCommitRepositoryChangeMaterializer struct{}

func (secondCommitRepositoryChangeMaterializer) SnapshotRepository(context.Context, workflow.Workspace) (workflow.RepositorySnapshot, error) {
	return workflow.RepositorySnapshot{HeadSHA: "abc123"}, nil
}

func (secondCommitRepositoryChangeMaterializer) MaterializeRepositoryChanges(context.Context, workflow.Workspace, workflow.RepositorySnapshot) (workflow.RepositoryChangeMaterialization, error) {
	return workflow.RepositoryChangeMaterialization{
		CommitRefs: []workflow.CommitRefPlan{
			{
				SHA:         "def456",
				Subject:     "Materialize AgentRun 2 repository changes",
				AuthorName:  "ForgeLane",
				AuthorEmail: "forgelane@localhost",
			},
		},
	}, nil
}

type noChangeRepositoryMaterializer struct{}

func (noChangeRepositoryMaterializer) SnapshotRepository(context.Context, workflow.Workspace) (workflow.RepositorySnapshot, error) {
	return workflow.RepositorySnapshot{HeadSHA: "before"}, nil
}

func (noChangeRepositoryMaterializer) MaterializeRepositoryChanges(context.Context, workflow.Workspace, workflow.RepositorySnapshot) (workflow.RepositoryChangeMaterialization, error) {
	return workflow.RepositoryChangeMaterialization{
		DeliverySkipped:    true,
		DeliverySkipReason: "no_repository_changes",
	}, nil
}

type recordingChangeProvider struct {
	pushResult    workflow.ChangeBranchPushResult
	pushErr       error
	calls         []workflow.ChangeBranchPushPlan
	draftPRResult workflow.ChangeDraftPRResult
	draftPRErr    error
	draftPRCalls  []workflow.ChangeDraftPRPlan
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

func createRetryAgentRunForExistingWorkItem(t *testing.T, instanceStore *store.Store, priorRunID int64) int64 {
	t.Helper()

	priorDetail, err := instanceStore.GetAgentRunDetail(priorRunID)
	if err != nil {
		t.Fatalf("get prior AgentRun detail: %v", err)
	}
	createResult, err := workflow.CreatePlannedAgentRun(instanceStore, workflow.CreatePlannedAgentRunInput{
		WorkItem: priorDetail.WorkItem,
	})
	if err != nil {
		t.Fatalf("create retry AgentRun: %v", err)
	}
	workspaceRoot := filepath.Join(t.TempDir(), "retry-workspace")
	paths := workflow.WorkspacePaths{
		Root:      workspaceRoot,
		Repo:      filepath.Join(workspaceRoot, "repo"),
		Logs:      filepath.Join(workspaceRoot, "logs"),
		Artifacts: filepath.Join(workspaceRoot, "artifacts"),
		Tmp:       filepath.Join(workspaceRoot, "tmp"),
	}
	if _, err := instanceStore.AllocateWorkspace(createResult.AgentRun.ID, paths); err != nil {
		t.Fatalf("allocate retry Workspace: %v", err)
	}
	if _, err := instanceStore.MarkWorkspaceReady(createResult.AgentRun.ID); err != nil {
		t.Fatalf("mark retry Workspace ready: %v", err)
	}
	return createResult.AgentRun.ID
}
