package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/liiujinfu/forgelane/internal/workitems"
)

// PlannedAgentRunStore persists planned AgentRun state and matching Events atomically.
type PlannedAgentRunStore interface {
	CreatePlannedAgentRun(PlannedAgentRunPlan) (AgentRunCreateResult, error)
}

// CreatePlannedAgentRunInput describes the WorkItem snapshot used to start an AgentRun.
type CreatePlannedAgentRunInput struct {
	WorkItem WorkItemSnapshot
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
	WorkItem      WorkItemSnapshot
	Status        string
	Branch        string
	ControlAction ControlActionPlan
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
	plan, err := NewPlannedAgentRunPlan(input.WorkItem)
	if err != nil {
		return AgentRunCreateResult{}, err
	}
	return store.CreatePlannedAgentRun(plan)
}

// NewPlannedAgentRunPlan captures the workflow semantics for the first planned AgentRun state.
func NewPlannedAgentRunPlan(workItem WorkItemSnapshot) (PlannedAgentRunPlan, error) {
	ref, err := workitems.ParseProviderRef(workItem.ProviderRef)
	if err != nil {
		return PlannedAgentRunPlan{}, err
	}
	branch := fmt.Sprintf("forgelane/issue-%d", ref.IssueNumber)
	return PlannedAgentRunPlan{
		WorkItem: workItem,
		Status:   "planned",
		Branch:   branch,
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
			Kind:      "command",
			Preset:    "codex",
			EnvPolicy: "scrubbed",
		},
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode RunSpec JSON: %w", err)
	}
	return string(encoded), nil
}

type runSpecSnapshot struct {
	RunID        string                      `json:"run_id"`
	WorkItem     runSpecWorkItemSnapshot     `json:"work_item"`
	Repo         runSpecRepoSnapshot         `json:"repo"`
	Branch       string                      `json:"branch"`
	AgentAdapter runSpecAgentAdapterSnapshot `json:"agent_adapter"`
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
	Kind      string `json:"kind"`
	Preset    string `json:"preset"`
	EnvPolicy string `json:"env_policy"`
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
