package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liiujinfu/forgelane/internal/workitems"
)

// PlannedAgentRunStore persists planned AgentRun state and matching Events atomically.
type PlannedAgentRunStore interface {
	CreatePlannedAgentRun(PlannedAgentRunPlan) (AgentRunCreateResult, error)
}

// CreatePlannedAgentRunInput describes the WorkItem snapshot used to start an AgentRun.
type CreatePlannedAgentRunInput struct {
	WorkItem       WorkItemSnapshot
	AgentPreset    string
	CommandTimeout time.Duration
}

// WorkItemSnapshot is the cached provider-owned WorkItem state captured in a RunSpec.
type WorkItemSnapshot struct {
	ID                  int64
	ForgeProjectID      int64
	ProviderRef         string
	Provider            string
	RepositoryRef       string
	ProviderIssueNumber int
	Title               string
	Body                string
	Status              string
	ProviderStatusRaw   string
	URL                 string
	ProviderUpdatedAt   string
	ImportedAt          string
	RefreshedAt         string
}

// PlannedAgentRunPlan is the workflow decision for creating a planned AgentRun.
type PlannedAgentRunPlan struct {
	WorkItem       WorkItemSnapshot
	Status         string
	Branch         string
	AgentPreset    string
	CommandTimeout time.Duration
	ControlAction  ControlActionPlan
}

// ControlActionPlan is the workflow decision for the operator action starting a run.
type ControlActionPlan struct {
	Type        string
	TargetType  string
	TargetRef   string
	RequestedBy string
	Reason      string
	Input       map[string]any
	Status      string
}

// EventPlan is an Event payload and subject chosen by workflow.
type EventPlan struct {
	Type        string
	SubjectType string
	SubjectRef  string
	Payload     map[string]any
}

// PlannedAgentRunIDs are persistence identities assigned while writing the plan.
type PlannedAgentRunIDs struct {
	ControlActionID int64
	AgentRunID      int64
	RunSpecID       int64
}

// Event is a persisted audit event.
type Event struct {
	ID          int64
	Type        string
	OccurredAt  string
	Actor       string
	SubjectType string
	SubjectRef  string
}

// AgentRun is a persisted bounded agent attempt.
type AgentRun struct {
	ID         int64
	WorkItemID int64
	Status     string
	CreatedAt  string
	UpdatedAt  string
}

// RunSpec is the immutable execution input snapshot for one AgentRun.
type RunSpec struct {
	ID         int64
	AgentRunID int64
	SpecJSON   string
	CreatedAt  string
}

// AgentRunCreateResult is the outcome of creating AgentRun execution state.
type AgentRunCreateResult struct {
	ControlAction ControlAction
	AgentRun      AgentRun
	RunSpec       RunSpec
	Branch        string
	ChangeSet     *ChangeSet
	Events        []Event
}

// RunnerJob is the runner-facing execution request for one AgentRun.
type RunnerJob struct {
	ID         int64
	AgentRunID int64
	Status     string
	CreatedAt  string
	UpdatedAt  string
}

// WorkspacePaths are the filesystem paths leased for one Workspace.
type WorkspacePaths struct {
	Root      string
	Repo      string
	Logs      string
	Artifacts string
	Tmp       string
}

// Workspace is the persisted execution filesystem lease for one AgentRun.
type Workspace struct {
	ID             int64
	AgentRunID     int64
	RunnerJobID    int64
	Status         string
	Paths          WorkspacePaths
	FailureMessage string
	CreatedAt      string
	UpdatedAt      string
}

// LogSegment indexes one contiguous stdout/stderr byte range in a Workspace log file.
type LogSegment struct {
	ID           int64
	AgentRunID   int64
	Stream       string
	Sequence     int64
	ByteStart    int64
	ByteEnd      int64
	Preview      string
	ArtifactPath string
	Truncated    bool
	CreatedAt    string
}

// CommitRef records one local repository commit produced by an AgentRun.
type CommitRef struct {
	ID            int64
	AgentRunID    int64
	ChangeSetID   int64
	RepositoryRef string
	SHA           string
	Subject       string
	AuthorName    string
	AuthorEmail   string
	CreatedAt     string
}

// ChangeSet records one ForgeLane-owned delivery artifact for a WorkItem.
type ChangeSet struct {
	ID                int64
	WorkItemID        int64
	WorkItemRef       string
	Provider          string
	RepositoryRef     string
	BaseBranch        string
	BranchRef         string
	BranchProviderRef string
	ChangeRef         string
	ChangeDraft       bool
	ProviderSnapshot  string
	Status            string
	CreatedByRunID    int64
	ActiveRunID       int64
	CommitRefs        []CommitRef
	CreatedAt         string
	UpdatedAt         string
}

// ChangeSetPlan is the provider-neutral delivery state chosen after local commits exist.
type ChangeSetPlan struct {
	WorkItemID     int64
	WorkItemRef    string
	Provider       string
	RepositoryRef  string
	BaseBranch     string
	BranchRef      string
	Status         string
	CreatedByRunID int64
	ActiveRunID    int64
}

// ChangeBranchPushPlan is the workflow request to publish a ChangeSet branch.
type ChangeBranchPushPlan struct {
	ChangeSetID         int64
	WorkItemRef         string
	Provider            string
	RepositoryRef       string
	LocalRepositoryPath string
	BranchRef           string
	CommitSHAs          []string
}

// ChangeBranchPushResult is the provider evidence from a successful branch push.
type ChangeBranchPushResult struct {
	ChangeSetID       int64
	BranchProviderRef string
	PushedCommitSHAs  []string
}

// ChangeDraftPRPlan is the workflow request to create or update the reviewable draft PR/MR.
type ChangeDraftPRPlan struct {
	ChangeSetID       int64
	WorkItemRef       string
	Provider          string
	RepositoryRef     string
	BaseBranch        string
	BranchRef         string
	BranchProviderRef string
	ExistingChangeRef string
	CommitSHAs        []string
}

// ChangeDraftPRResult is the provider evidence from a successful draft PR/MR create or update.
type ChangeDraftPRResult struct {
	ChangeSetID      int64
	ChangeRef        string
	Draft            bool
	ProviderSnapshot map[string]any
}

// CommitRefPlan is a local commit ref ready to persist after repository materialization.
type CommitRefPlan struct {
	SHA         string
	Subject     string
	AuthorName  string
	AuthorEmail string
}

// RepositorySnapshot captures the Workspace repository baseline before agent execution.
type RepositorySnapshot struct {
	HeadSHA string
}

// RepositoryChangeMaterialization is the result of turning Workspace repo changes into local commits.
type RepositoryChangeMaterialization struct {
	CommitRefs         []CommitRefPlan
	DeliverySkipped    bool
	DeliverySkipReason string
}

// AgentRunPrepareResult is the outcome of preparing runner state for execution.
type AgentRunPrepareResult struct {
	AgentRun   AgentRun
	RunnerJob  RunnerJob
	Workspace  Workspace
	CommitRefs []CommitRef
	ChangeSet  *ChangeSet
	Events     []Event
}

// AgentRunControlResult is the outcome of a human ControlAction against an AgentRun.
type AgentRunControlResult struct {
	ControlAction ControlAction
	AgentRun      AgentRun
	RunnerJob     RunnerJob
	Workspace     *Workspace
	Events        []Event
}

// AgentRunDetail is the read model for inspecting one AgentRun.
type AgentRunDetail struct {
	AgentRun           AgentRun
	WorkItem           WorkItemSnapshot
	RunSpec            RunSpec
	Workspace          *Workspace
	CommitRefs         []CommitRef
	ChangeSet          *ChangeSet
	DeliverySkipped    bool
	DeliverySkipReason string
}

// AgentCommandPlanInput is the immutable RunSpec and Workspace context needed to plan a command.
type AgentCommandPlanInput struct {
	RunSpecJSON string
	AgentRunID  int64
	Workspace   Workspace
}

// AgentCommandPlan is the process-level command chosen by an AgentAdapter.
type AgentCommandPlan struct {
	Executable       string
	Args             []string
	WorkingDirectory string
	Env              []string
	StdoutPath       string
	StderrPath       string
	RedactValues     []string
}

// AgentCommandPlanner translates RunSpec adapter config into a process command.
type AgentCommandPlanner interface {
	PlanAgentCommand(AgentCommandPlanInput) (AgentCommandPlan, error)
}

// LogSegmentPlan is a log segment ready to persist after command execution.
type LogSegmentPlan struct {
	Stream       string
	Sequence     int64
	ByteStart    int64
	ByteEnd      int64
	Preview      string
	ArtifactPath string
	Truncated    bool
}

// AgentCommandRunResult is the evidence returned by a process runner.
type AgentCommandRunResult struct {
	ExitCode       int
	Duration       time.Duration
	StdoutBytes    int64
	StderrBytes    int64
	LogSegments    []LogSegmentPlan
	ProcessStarted bool
}

// AgentCommandRunner executes an AgentCommandPlan.
type AgentCommandRunner interface {
	RunAgentCommand(context.Context, AgentCommandPlan) (AgentCommandRunResult, error)
}

// RepositoryChangeMaterializer materializes Workspace repository changes after a successful command.
type RepositoryChangeMaterializer interface {
	SnapshotRepository(context.Context, Workspace) (RepositorySnapshot, error)
	MaterializeRepositoryChanges(context.Context, Workspace, RepositorySnapshot) (RepositoryChangeMaterialization, error)
}

// ChangeProvider mutates provider-owned change state outside the AgentAdapter process.
type ChangeProvider interface {
	PushChangeSetBranch(context.Context, ChangeBranchPushPlan) (ChangeBranchPushResult, error)
	CreateOrUpdateDraftPR(context.Context, ChangeDraftPRPlan) (ChangeDraftPRResult, error)
}

// BranchPushStartResult records the auditable permission boundary for provider branch mutation.
type BranchPushStartResult struct {
	ControlAction ControlAction
	Events        []Event
}

// DraftPRStartResult records the auditable permission boundary for provider draft PR mutation.
type DraftPRStartResult struct {
	ControlAction ControlAction
	Events        []Event
}

// AgentCommandCompletion records terminal command execution evidence.
type AgentCommandCompletion struct {
	Status             string
	ExitCode           int
	Duration           time.Duration
	StdoutBytes        int64
	StderrBytes        int64
	LogSegments        []LogSegmentPlan
	CommitRefs         []CommitRefPlan
	ChangeSet          *ChangeSetPlan
	DeliverySkipped    bool
	DeliverySkipReason string
	FailureDetail      string
}

// AgentCommandExecutionStore persists command execution state and matching Events.
type AgentCommandExecutionStore interface {
	GetAgentRunDetail(int64) (AgentRunDetail, error)
	MarkAgentCommandStarted(int64) (Event, error)
	MarkAgentCommandCompleted(int64, AgentCommandCompletion) (AgentRunPrepareResult, error)
	MarkAgentCommandFailed(int64, string) (AgentRunPrepareResult, error)
	MarkChangeSetBranchPushStarted(int64, int64) (BranchPushStartResult, error)
	MarkChangeSetBranchPushSucceeded(int64, ChangeBranchPushResult, int64) (AgentRunPrepareResult, error)
	MarkChangeSetBranchPushFailed(int64, int64, int64, string) (AgentRunPrepareResult, error)
	MarkChangeSetDraftPRStarted(int64, int64) (DraftPRStartResult, error)
	MarkChangeSetDraftPRSucceeded(int64, ChangeDraftPRResult, int64) (AgentRunPrepareResult, error)
	MarkChangeSetDraftPRFailed(int64, int64, int64, string) (AgentRunPrepareResult, error)
}

// ControlAction is a persisted operator request to change the delivery loop.
type ControlAction struct {
	ID     int64
	Type   string
	Status string
}

// WorkspacePreparationStore persists Workspace preparation state.
type WorkspacePreparationStore interface {
	GetAgentRunDetail(int64) (AgentRunDetail, error)
	AllocateWorkspace(int64, WorkspacePaths) (AgentRunPrepareResult, error)
	MarkWorkspaceReady(int64) (AgentRunPrepareResult, error)
	MarkWorkspaceFailed(int64, string) (AgentRunPrepareResult, error)
}

// AgentRunControlStore persists human ControlActions and matching AgentRun Events.
type AgentRunControlStore interface {
	RequestAgentRunStop(int64, ControlActionPlan) (AgentRunControlResult, error)
}

// AgentRunRetryStore persists retry ControlActions and new AgentRun attempts.
type AgentRunRetryStore interface {
	GetAgentRunDetail(int64) (AgentRunDetail, error)
	CreateRetryAgentRun(int64, PlannedAgentRunPlan) (AgentRunCreateResult, error)
}

// RequestAgentRunStop records a human stop request for an active AgentRun.
func RequestAgentRunStop(store AgentRunControlStore, runID int64) (AgentRunControlResult, error) {
	return store.RequestAgentRunStop(runID, ControlActionPlan{
		Type:        "stop",
		TargetType:  "agent_run",
		TargetRef:   fmt.Sprintf("agent_run:%d", runID),
		RequestedBy: "local",
		Reason:      "forgelane runs stop",
		Input: map[string]any{
			"agent_run_id": runID,
		},
		Status: "succeeded",
	})
}

// RequestAgentRunRetry records a retry ControlAction and creates a fresh planned AgentRun.
func RequestAgentRunRetry(store AgentRunRetryStore, priorRunID int64) (AgentRunCreateResult, error) {
	prior, err := store.GetAgentRunDetail(priorRunID)
	if err != nil {
		return AgentRunCreateResult{}, err
	}
	if !isTerminalAgentRunStatus(prior.AgentRun.Status) {
		return AgentRunCreateResult{}, fmt.Errorf("AgentRun %d is %s; expected terminal run", priorRunID, prior.AgentRun.Status)
	}
	plan, err := NewPlannedAgentRunPlan(prior.WorkItem, "")
	if err != nil {
		return AgentRunCreateResult{}, err
	}
	plan.ControlAction = ControlActionPlan{
		Type:        "retry",
		TargetType:  "agent_run",
		TargetRef:   fmt.Sprintf("agent_run:%d", priorRunID),
		RequestedBy: "local",
		Reason:      "forgelane runs retry",
		Input: map[string]any{
			"prior_agent_run_id": priorRunID,
			"work_item_ref":      prior.WorkItem.ProviderRef,
		},
		Status: "succeeded",
	}
	return store.CreateRetryAgentRun(priorRunID, plan)
}

func isTerminalAgentRunStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}

// WorkspacePreparer performs filesystem preparation after the Workspace is allocated.
type WorkspacePreparer interface {
	PrepareWorkspace(WorkspacePreparation) error
}

// WorkspacePreparation is the filesystem preparation request chosen by workflow.
type WorkspacePreparation struct {
	Paths                 WorkspacePaths
	ExpectedRepositoryRef string
}

// PrepareAgentRunWorkspace leases and prepares the Workspace for an existing AgentRun.
func PrepareAgentRunWorkspace(store WorkspacePreparationStore, preparer WorkspacePreparer, runID int64, paths WorkspacePaths) (AgentRunPrepareResult, error) {
	allocated, err := store.AllocateWorkspace(runID, paths)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	detail, err := store.GetAgentRunDetail(runID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := preparer.PrepareWorkspace(WorkspacePreparation{
		Paths:                 paths,
		ExpectedRepositoryRef: detail.WorkItem.RepositoryRef,
	}); err != nil {
		if _, markErr := store.MarkWorkspaceFailed(runID, compactFailure(err)); markErr != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("prepare Workspace repository: %w; mark Workspace failed: %v", err, markErr)
		}
		return AgentRunPrepareResult{}, fmt.Errorf("prepare Workspace repository: %w", err)
	}
	ready, err := store.MarkWorkspaceReady(runID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	ready.Events = append(allocated.Events, ready.Events...)
	return ready, nil
}

// ExecuteAgentRunCommand invokes the AgentAdapter command for a prepared AgentRun.
func ExecuteAgentRunCommand(ctx context.Context, store AgentCommandExecutionStore, planner AgentCommandPlanner, runner AgentCommandRunner, runID int64) (AgentRunPrepareResult, error) {
	return ExecuteAgentRunCommandAndMaterialize(ctx, store, planner, runner, noopRepositoryChangeMaterializer{}, runID)
}

// ExecuteAgentRunCommandAndMaterialize invokes the AgentAdapter and commits repository changes on success.
func ExecuteAgentRunCommandAndMaterialize(ctx context.Context, store AgentCommandExecutionStore, planner AgentCommandPlanner, runner AgentCommandRunner, materializer RepositoryChangeMaterializer, runID int64) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(runID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.Workspace == nil {
		return AgentRunPrepareResult{}, fmt.Errorf("Workspace not prepared for AgentRun %d", runID)
	}
	if detail.Workspace.Status != "ready" {
		return AgentRunPrepareResult{}, fmt.Errorf("Workspace for AgentRun %d is %s; expected ready", runID, detail.Workspace.Status)
	}
	if detail.AgentRun.Status != "preparing" {
		return AgentRunPrepareResult{}, fmt.Errorf("AgentRun %d is %s; expected preparing", runID, detail.AgentRun.Status)
	}

	plan, err := planner.PlanAgentCommand(AgentCommandPlanInput{
		RunSpecJSON: detail.RunSpec.SpecJSON,
		AgentRunID:  detail.AgentRun.ID,
		Workspace:   *detail.Workspace,
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	timeout, err := commandTimeout(detail.RunSpec.SpecJSON)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if _, err := store.MarkAgentCommandStarted(runID); err != nil {
		return AgentRunPrepareResult{}, err
	}
	repositorySnapshot, err := materializer.SnapshotRepository(ctx, *detail.Workspace)
	if err != nil {
		if _, markErr := store.MarkAgentCommandFailed(runID, compactFailure(err)); markErr != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("snapshot Workspace repository: %w; mark AgentRun failed: %v", err, markErr)
		}
		return AgentRunPrepareResult{}, fmt.Errorf("snapshot Workspace repository: %w", err)
	}

	runCtx := ctx
	cancelRunCtx := func() {}
	if timeout > 0 {
		runCtx, cancelRunCtx = context.WithTimeout(ctx, timeout)
	}
	defer cancelRunCtx()

	result, runErr := runner.RunAgentCommand(runCtx, plan)
	if runErr != nil && !result.ProcessStarted {
		status := commandStatus(runCtx, result, runErr)
		if status != "failed" {
			return store.MarkAgentCommandCompleted(runID, AgentCommandCompletion{
				Status:        status,
				ExitCode:      commandExitCode(result, runErr),
				FailureDetail: commandFailureDetail(runErr),
			})
		}
		if _, markErr := store.MarkAgentCommandFailed(runID, compactFailure(runErr)); markErr != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("execute AgentAdapter command: %w; mark AgentRun failed: %v", runErr, markErr)
		}
		return AgentRunPrepareResult{}, fmt.Errorf("execute AgentAdapter command: %w", runErr)
	}

	var commitRefs []CommitRefPlan
	var deliverySkipped bool
	var deliverySkipReason string
	if commandStatus(runCtx, result, runErr) == "completed" {
		materialized, err := materializer.MaterializeRepositoryChanges(ctx, *detail.Workspace, repositorySnapshot)
		if err != nil {
			failureDetail := compactFailure(fmt.Errorf("materialize Workspace repository changes: %w", err))
			if _, markErr := store.MarkAgentCommandCompleted(runID, AgentCommandCompletion{
				Status:        "failed",
				ExitCode:      commandExitCode(result, runErr),
				Duration:      result.Duration,
				StdoutBytes:   result.StdoutBytes,
				StderrBytes:   result.StderrBytes,
				LogSegments:   result.LogSegments,
				FailureDetail: failureDetail,
			}); markErr != nil {
				return AgentRunPrepareResult{}, fmt.Errorf("materialize Workspace repository changes: %w; mark AgentRun failed: %v", err, markErr)
			}
			return AgentRunPrepareResult{}, fmt.Errorf("materialize Workspace repository changes: %w", err)
		}
		commitRefs = materialized.CommitRefs
		deliverySkipped = materialized.DeliverySkipped
		deliverySkipReason = materialized.DeliverySkipReason
	}

	var changeSet *ChangeSetPlan
	if commandStatus(runCtx, result, runErr) == "completed" && len(commitRefs) > 0 {
		changeSet, err = NewChangeSetPlan(detail)
		if err != nil {
			return AgentRunPrepareResult{}, err
		}
	}

	completed, err := store.MarkAgentCommandCompleted(runID, AgentCommandCompletion{
		Status:             commandStatus(runCtx, result, runErr),
		ExitCode:           commandExitCode(result, runErr),
		Duration:           result.Duration,
		StdoutBytes:        result.StdoutBytes,
		StderrBytes:        result.StderrBytes,
		LogSegments:        result.LogSegments,
		CommitRefs:         commitRefs,
		ChangeSet:          changeSet,
		DeliverySkipped:    deliverySkipped,
		DeliverySkipReason: deliverySkipReason,
		FailureDetail:      commandFailureDetail(runErr),
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	return completed, nil
}

// ExecuteAgentRunCommandAndDeliver materializes local commits, creates or claims a ChangeSet, then pushes its branch through the Change Provider.
func ExecuteAgentRunCommandAndDeliver(ctx context.Context, store AgentCommandExecutionStore, planner AgentCommandPlanner, runner AgentCommandRunner, materializer RepositoryChangeMaterializer, changeProvider ChangeProvider, runID int64) (AgentRunPrepareResult, error) {
	completed, err := ExecuteAgentRunCommandAndMaterialize(ctx, store, planner, runner, materializer, runID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if completed.ChangeSet == nil || len(completed.CommitRefs) == 0 {
		return completed, nil
	}
	if changeProvider == nil {
		return completed, fmt.Errorf("deliver ChangeSet %d: missing ChangeProvider for provider %q", completed.ChangeSet.ID, completed.ChangeSet.Provider)
	}

	pushPlan := NewChangeBranchPushPlan(completed.Workspace, *completed.ChangeSet)
	started, err := store.MarkChangeSetBranchPushStarted(runID, completed.ChangeSet.ID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	completed.Events = append(completed.Events, started.Events...)

	pushResult, err := changeProvider.PushChangeSetBranch(ctx, pushPlan)
	if err != nil {
		failed, markErr := store.MarkChangeSetBranchPushFailed(runID, completed.ChangeSet.ID, started.ControlAction.ID, "provider branch push failed")
		if markErr != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("push ChangeSet branch failed; mark branch push failed: %v", markErr)
		}
		failed.Events = append(completed.Events, failed.Events...)
		return failed, fmt.Errorf("push ChangeSet branch failed")
	}

	branchReady, err := store.MarkChangeSetBranchPushSucceeded(runID, pushResult, started.ControlAction.ID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	branchReady.Events = append(completed.Events, branchReady.Events...)
	if branchReady.ChangeSet == nil || len(branchReady.ChangeSet.CommitRefs) == 0 {
		return branchReady, nil
	}

	draftPRPlan := NewChangeDraftPRPlan(*branchReady.ChangeSet)
	draftStarted, err := store.MarkChangeSetDraftPRStarted(runID, branchReady.ChangeSet.ID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	branchReady.Events = append(branchReady.Events, draftStarted.Events...)

	draftPRResult, err := changeProvider.CreateOrUpdateDraftPR(ctx, draftPRPlan)
	if err != nil {
		failed, markErr := store.MarkChangeSetDraftPRFailed(runID, branchReady.ChangeSet.ID, draftStarted.ControlAction.ID, "provider draft PR create or update failed")
		if markErr != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("create or update draft PR failed; mark draft PR failed: %v", markErr)
		}
		failed.Events = append(branchReady.Events, failed.Events...)
		return failed, fmt.Errorf("create or update draft PR failed")
	}
	if draftPRResult.ChangeRef == "" || !draftPRResult.Draft {
		failed, markErr := store.MarkChangeSetDraftPRFailed(runID, branchReady.ChangeSet.ID, draftStarted.ControlAction.ID, "provider draft PR result missing draft PR ref or draft status")
		if markErr != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("create or update draft PR returned invalid result; mark draft PR failed: %v", markErr)
		}
		failed.Events = append(branchReady.Events, failed.Events...)
		return failed, fmt.Errorf("create or update draft PR returned invalid result")
	}

	delivered, err := store.MarkChangeSetDraftPRSucceeded(runID, draftPRResult, draftStarted.ControlAction.ID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	delivered.Events = append(branchReady.Events, delivered.Events...)
	return delivered, nil
}

// NewChangeBranchPushPlan derives provider branch-push input from a persisted ChangeSet.
func NewChangeBranchPushPlan(workspace Workspace, changeSet ChangeSet) ChangeBranchPushPlan {
	commitSHAs := make([]string, 0, len(changeSet.CommitRefs))
	for _, ref := range changeSet.CommitRefs {
		commitSHAs = append(commitSHAs, ref.SHA)
	}
	return ChangeBranchPushPlan{
		ChangeSetID:         changeSet.ID,
		WorkItemRef:         changeSet.WorkItemRef,
		Provider:            changeSet.Provider,
		RepositoryRef:       changeSet.RepositoryRef,
		LocalRepositoryPath: workspace.Paths.Repo,
		BranchRef:           changeSet.BranchRef,
		CommitSHAs:          commitSHAs,
	}
}

// NewChangeDraftPRPlan derives provider draft PR input from a branch-ready ChangeSet.
func NewChangeDraftPRPlan(changeSet ChangeSet) ChangeDraftPRPlan {
	commitSHAs := make([]string, 0, len(changeSet.CommitRefs))
	for _, ref := range changeSet.CommitRefs {
		commitSHAs = append(commitSHAs, ref.SHA)
	}
	return ChangeDraftPRPlan{
		ChangeSetID:       changeSet.ID,
		WorkItemRef:       changeSet.WorkItemRef,
		Provider:          changeSet.Provider,
		RepositoryRef:     changeSet.RepositoryRef,
		BaseBranch:        changeSet.BaseBranch,
		BranchRef:         changeSet.BranchRef,
		BranchProviderRef: changeSet.BranchProviderRef,
		ExistingChangeRef: changeSet.ChangeRef,
		CommitSHAs:        commitSHAs,
	}
}

// NewChangeSetPlan derives the active ChangeSet identity from the immutable RunSpec.
func NewChangeSetPlan(detail AgentRunDetail) (*ChangeSetPlan, error) {
	var spec runSpecSnapshot
	if err := json.Unmarshal([]byte(detail.RunSpec.SpecJSON), &spec); err != nil {
		return nil, fmt.Errorf("decode RunSpec ChangeSet fields: %w", err)
	}
	if spec.Repo.BaseBranch == "" {
		return nil, fmt.Errorf("RunSpec %d missing repo.base_branch", detail.RunSpec.ID)
	}
	if spec.Branch == "" {
		return nil, fmt.Errorf("RunSpec %d missing branch", detail.RunSpec.ID)
	}
	return &ChangeSetPlan{
		WorkItemID:     detail.WorkItem.ID,
		WorkItemRef:    detail.WorkItem.ProviderRef,
		Provider:       detail.WorkItem.Provider,
		RepositoryRef:  detail.WorkItem.RepositoryRef,
		BaseBranch:     spec.Repo.BaseBranch,
		BranchRef:      spec.Branch,
		Status:         "planned",
		CreatedByRunID: detail.AgentRun.ID,
		ActiveRunID:    detail.AgentRun.ID,
	}, nil
}

type noopRepositoryChangeMaterializer struct{}

func (noopRepositoryChangeMaterializer) SnapshotRepository(context.Context, Workspace) (RepositorySnapshot, error) {
	return RepositorySnapshot{}, nil
}

func (noopRepositoryChangeMaterializer) MaterializeRepositoryChanges(context.Context, Workspace, RepositorySnapshot) (RepositoryChangeMaterialization, error) {
	return RepositoryChangeMaterialization{}, nil
}

func commandStatus(ctx context.Context, result AgentCommandRunResult, err error) string {
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "timed_out"
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return "cancelled"
		}
	}
	if commandExitCode(result, err) == 0 {
		return "completed"
	}
	return "failed"
}

func commandExitCode(result AgentCommandRunResult, err error) int {
	if err != nil && result.ExitCode == 0 {
		return -1
	}
	return result.ExitCode
}

func commandFailureDetail(err error) string {
	if err == nil {
		return ""
	}
	return compactFailure(err)
}

func compactFailure(err error) string {
	const limit = 500
	message := err.Error()
	if len(message) <= limit {
		return message
	}
	return message[:limit] + "..."
}

// CreatePlannedAgentRun creates a planned AgentRun through the transactional store Adapter.
func CreatePlannedAgentRun(store PlannedAgentRunStore, input CreatePlannedAgentRunInput) (AgentRunCreateResult, error) {
	plan, err := NewPlannedAgentRunPlan(input.WorkItem, input.AgentPreset)
	if err != nil {
		return AgentRunCreateResult{}, err
	}
	if input.CommandTimeout > 0 {
		plan.CommandTimeout = input.CommandTimeout
	}
	return store.CreatePlannedAgentRun(plan)
}

// NewPlannedAgentRunPlan captures the workflow semantics for the first planned AgentRun state.
func NewPlannedAgentRunPlan(workItem WorkItemSnapshot, agentPreset string) (PlannedAgentRunPlan, error) {
	ref, err := workitems.ParseProviderRef(workItem.ProviderRef)
	if err != nil {
		return PlannedAgentRunPlan{}, err
	}
	if agentPreset == "" {
		agentPreset = "codex"
	}
	branch := fmt.Sprintf("forgelane/issue-%d", ref.IssueNumber)
	return PlannedAgentRunPlan{
		WorkItem:       workItem,
		Status:         "planned",
		Branch:         branch,
		AgentPreset:    agentPreset,
		CommandTimeout: defaultCommandTimeout,
		ControlAction: ControlActionPlan{
			Type:        "start",
			TargetType:  "work_item",
			TargetRef:   workItem.ProviderRef,
			RequestedBy: "local",
			Reason:      "forgelane runs create",
			Input: map[string]any{
				"provider_ref": workItem.ProviderRef,
			},
			Status: "succeeded",
		},
	}, nil
}

// EncodeRunSpec returns the immutable RunSpec snapshot for the assigned AgentRun id.
func (plan PlannedAgentRunPlan) EncodeRunSpec(runID int64) (string, error) {
	ref, err := workitems.ParseProviderRef(plan.WorkItem.ProviderRef)
	if err != nil {
		return "", err
	}
	owner, name, err := splitRepositoryPath(ref.RepositoryPath)
	if err != nil {
		return "", err
	}
	spec := runSpecSnapshot{
		RunID: fmt.Sprintf("run_%d", runID),
		WorkItem: runSpecWorkItemSnapshot{
			ID:                plan.WorkItem.ID,
			Provider:          plan.WorkItem.Provider,
			ProviderRef:       plan.WorkItem.ProviderRef,
			RepositoryRef:     plan.WorkItem.RepositoryRef,
			ProviderIssue:     plan.WorkItem.ProviderIssueNumber,
			Title:             plan.WorkItem.Title,
			BodySnapshot:      plan.WorkItem.Body,
			Status:            plan.WorkItem.Status,
			ProviderStatusRaw: plan.WorkItem.ProviderStatusRaw,
			URL:               plan.WorkItem.URL,
			ProviderUpdatedAt: plan.WorkItem.ProviderUpdatedAt,
			ImportedAt:        plan.WorkItem.ImportedAt,
			RefreshedAt:       plan.WorkItem.RefreshedAt,
		},
		Repo: runSpecRepoSnapshot{
			Provider:   ref.Provider,
			Host:       ref.ProviderHost,
			Owner:      owner,
			Name:       name,
			Ref:        ref.RepositoryRef(),
			BaseBranch: "main",
		},
		Branch: plan.Branch,
		AgentAdapter: runSpecAgentAdapterSnapshot{
			Kind:             "command",
			Preset:           plan.AgentPreset,
			EnvPolicy:        "scrubbed",
			CredentialGrants: credentialGrantsForAgentPreset(plan.AgentPreset),
		},
		TimeoutMilliseconds: plan.CommandTimeout.Milliseconds(),
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode RunSpec JSON: %w", err)
	}
	return string(encoded), nil
}

type runSpecSnapshot struct {
	RunID               string                      `json:"run_id"`
	WorkItem            runSpecWorkItemSnapshot     `json:"work_item"`
	Repo                runSpecRepoSnapshot         `json:"repo"`
	Branch              string                      `json:"branch"`
	AgentAdapter        runSpecAgentAdapterSnapshot `json:"agent_adapter"`
	TimeoutMilliseconds int64                       `json:"timeout_milliseconds"`
}

type runSpecWorkItemSnapshot struct {
	ID                int64  `json:"id"`
	Provider          string `json:"provider"`
	ProviderRef       string `json:"provider_ref"`
	RepositoryRef     string `json:"repository_ref"`
	ProviderIssue     int    `json:"provider_issue"`
	Title             string `json:"title"`
	BodySnapshot      string `json:"body_snapshot"`
	Status            string `json:"status"`
	ProviderStatusRaw string `json:"provider_status_raw"`
	URL               string `json:"url"`
	ProviderUpdatedAt string `json:"provider_updated_at"`
	ImportedAt        string `json:"imported_at"`
	RefreshedAt       string `json:"refreshed_at"`
}

type runSpecRepoSnapshot struct {
	Provider   string `json:"provider"`
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Name       string `json:"name"`
	Ref        string `json:"ref"`
	BaseBranch string `json:"base_branch"`
}

type runSpecAgentAdapterSnapshot struct {
	Kind             string                           `json:"kind"`
	Preset           string                           `json:"preset"`
	EnvPolicy        string                           `json:"env_policy"`
	CredentialGrants []runSpecCredentialGrantSnapshot `json:"credential_grants,omitempty"`
}

type runSpecCredentialGrantSnapshot struct {
	Kind     string `json:"kind"`
	SecretID string `json:"secret_id"`
	Env      string `json:"env"`
}

func credentialGrantsForAgentPreset(agentPreset string) []runSpecCredentialGrantSnapshot {
	if agentPreset != "codex" {
		return nil
	}
	return []runSpecCredentialGrantSnapshot{
		{
			Kind:     "openai_api_key",
			SecretID: "env:OPENAI_API_KEY",
			Env:      "OPENAI_API_KEY",
		},
	}
}

const defaultCommandTimeout = 30 * time.Minute

func commandTimeout(specJSON string) (time.Duration, error) {
	var spec struct {
		TimeoutMilliseconds int64 `json:"timeout_milliseconds"`
	}
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return 0, fmt.Errorf("decode RunSpec timeout: %w", err)
	}
	if spec.TimeoutMilliseconds <= 0 {
		return 0, nil
	}
	return time.Duration(spec.TimeoutMilliseconds) * time.Millisecond, nil
}

// EventPlans returns the audit Events that must be written with the planned AgentRun state.
func (plan PlannedAgentRunPlan) EventPlans(ids PlannedAgentRunIDs) []EventPlan {
	return []EventPlan{
		{
			Type:        "control_action.succeeded",
			SubjectType: "control_action",
			SubjectRef:  fmt.Sprintf("control_action:%d", ids.ControlActionID),
			Payload: map[string]any{
				"control_action_id": ids.ControlActionID,
				"type":              plan.ControlAction.Type,
				"status":            plan.ControlAction.Status,
				"agent_run_id":      ids.AgentRunID,
				"work_item_id":      plan.WorkItem.ID,
				"provider_ref":      plan.WorkItem.ProviderRef,
			},
		},
		{
			Type:        "agent_run.created",
			SubjectType: "agent_run",
			SubjectRef:  fmt.Sprintf("agent_run:%d", ids.AgentRunID),
			Payload: map[string]any{
				"agent_run_id": ids.AgentRunID,
				"work_item_id": plan.WorkItem.ID,
				"provider_ref": plan.WorkItem.ProviderRef,
				"status":       plan.Status,
			},
		},
		{
			Type:        "run_spec.created",
			SubjectType: "run_spec",
			SubjectRef:  fmt.Sprintf("run_spec:%d", ids.RunSpecID),
			Payload: map[string]any{
				"agent_run_id": ids.AgentRunID,
				"run_spec_id":  ids.RunSpecID,
				"branch":       plan.Branch,
			},
		},
	}
}

func splitRepositoryPath(repositoryPath string) (string, string, error) {
	parts := strings.Split(repositoryPath, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repository path %q", repositoryPath)
	}
	return parts[0], parts[1], nil
}
