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

// AgentRunPrepareResult is the outcome of preparing runner state for execution.
type AgentRunPrepareResult struct {
	AgentRun  AgentRun
	RunnerJob RunnerJob
	Workspace Workspace
	Events    []Event
}

// AgentRunDetail is the read model for inspecting one AgentRun.
type AgentRunDetail struct {
	AgentRun  AgentRun
	WorkItem  WorkItemSnapshot
	RunSpec   RunSpec
	Workspace *Workspace
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

// AgentCommandCompletion records terminal command execution evidence.
type AgentCommandCompletion struct {
	Status        string
	ExitCode      int
	Duration      time.Duration
	StdoutBytes   int64
	StderrBytes   int64
	LogSegments   []LogSegmentPlan
	FailureDetail string
}

// AgentCommandExecutionStore persists command execution state and matching Events.
type AgentCommandExecutionStore interface {
	GetAgentRunDetail(int64) (AgentRunDetail, error)
	MarkAgentCommandStarted(int64) (Event, error)
	MarkAgentCommandCompleted(int64, AgentCommandCompletion) (AgentRunPrepareResult, error)
	MarkAgentCommandFailed(int64, string) (AgentRunPrepareResult, error)
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

	completed, err := store.MarkAgentCommandCompleted(runID, AgentCommandCompletion{
		Status:        commandStatus(runCtx, result, runErr),
		ExitCode:      commandExitCode(result, runErr),
		Duration:      result.Duration,
		StdoutBytes:   result.StdoutBytes,
		StderrBytes:   result.StderrBytes,
		LogSegments:   result.LogSegments,
		FailureDetail: commandFailureDetail(runErr),
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	return completed, nil
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
