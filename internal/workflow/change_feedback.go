package workflow

import (
	"context"
	"fmt"
	"strings"
)

// ChangeFeedbackProvider reads provider-owned PR review, comment, and CI state.
type ChangeFeedbackProvider interface {
	ReadChangeFeedback(context.Context, ChangeFeedbackReadPlan) (ChangeFeedbackSnapshot, error)
}

// ChangeFeedbackReadPlan is the provider-neutral request to refresh PR feedback.
type ChangeFeedbackReadPlan struct {
	ChangeSetID   int64
	WorkItemRef   string
	Provider      string
	RepositoryRef string
	ChangeRef     string
	BranchRef     string
	HeadSHA       string
}

// ChangeFeedbackSnapshot is the compact ForgeLane-owned snapshot of provider PR feedback.
type ChangeFeedbackSnapshot struct {
	Provider      string
	RepositoryRef string
	ChangeRef     string
	HeadSHA       string
	Items         []ChangeFeedbackItem
}

// ChangeFeedbackItem is one compact feedback item from a provider-owned review surface.
type ChangeFeedbackItem struct {
	ProviderRef      string
	Kind             string
	Actionable       bool
	Summary          string
	Body             string
	Path             string
	Line             int
	CommitSHA        string
	State            string
	ProviderSnapshot map[string]any
}

// ChangeFeedbackSyncStore persists a compact provider feedback snapshot.
type ChangeFeedbackSyncStore interface {
	SyncChangeFeedback(int64, int64, ChangeFeedbackSnapshot, ControlActionPlan) (ChangeFeedbackSyncResult, error)
}

// ChangeFeedbackSyncResult is the local persistence outcome for one feedback refresh.
type ChangeFeedbackSyncResult struct {
	ControlAction   ControlAction
	ChangeSet       ChangeSet
	Snapshot        ChangeFeedbackSnapshot
	Items           []ChangeFeedbackItem
	ActionableItems []ChangeFeedbackItem
	Events          []Event
}

// NewChangeFeedbackReadPlan derives a provider feedback refresh request from an active ChangeSet.
func NewChangeFeedbackReadPlan(changeSet ChangeSet) ChangeFeedbackReadPlan {
	return ChangeFeedbackReadPlan{
		ChangeSetID:   changeSet.ID,
		WorkItemRef:   changeSet.WorkItemRef,
		Provider:      changeSet.Provider,
		RepositoryRef: changeSet.RepositoryRef,
		ChangeRef:     changeSet.ChangeRef,
		BranchRef:     changeSet.BranchRef,
		HeadSHA:       latestChangeSetCommitSHA(changeSet),
	}
}

// SyncChangeFeedback refreshes provider feedback and records the compact local snapshot.
func SyncChangeFeedback(ctx context.Context, store ChangeFeedbackSyncStore, provider ChangeFeedbackProvider, changeSet ChangeSet) (ChangeFeedbackSyncResult, error) {
	if provider == nil {
		return ChangeFeedbackSyncResult{}, fmt.Errorf("missing ChangeFeedbackProvider for provider %q", changeSet.Provider)
	}
	if strings.TrimSpace(changeSet.ChangeRef) == "" {
		return ChangeFeedbackSyncResult{}, fmt.Errorf("ChangeSet %d has no provider PR ref", changeSet.ID)
	}
	plan := NewChangeFeedbackReadPlan(changeSet)
	snapshot, err := provider.ReadChangeFeedback(ctx, plan)
	if err != nil {
		return ChangeFeedbackSyncResult{}, err
	}
	snapshot = normalizeChangeFeedbackSnapshot(snapshot, plan)
	actionableItems := ActionableChangeFeedbackItems(snapshot.Items)
	return store.SyncChangeFeedback(changeSet.ID, changeSet.ActiveRunID, snapshot, ControlActionPlan{
		Type:        "sync_change_feedback",
		TargetType:  "change_set",
		TargetRef:   fmt.Sprintf("change_set:%d", changeSet.ID),
		RequestedBy: "local",
		Reason:      "forgelane pr sync-feedback",
		Input: map[string]any{
			"change_set_id":             changeSet.ID,
			"agent_run_id":              changeSet.ActiveRunID,
			"change_ref":                changeSet.ChangeRef,
			"head_sha":                  snapshot.HeadSHA,
			"feedback_count":            len(snapshot.Items),
			"actionable_feedback_count": len(actionableItems),
		},
		Status: "succeeded",
	})
}

// ActionableChangeFeedbackItems returns only feedback that should drive an AgentRun retry.
func ActionableChangeFeedbackItems(items []ChangeFeedbackItem) []ChangeFeedbackItem {
	actionable := make([]ChangeFeedbackItem, 0, len(items))
	for _, item := range items {
		if item.Actionable {
			actionable = append(actionable, item)
		}
	}
	return actionable
}

func normalizeChangeFeedbackSnapshot(snapshot ChangeFeedbackSnapshot, plan ChangeFeedbackReadPlan) ChangeFeedbackSnapshot {
	if snapshot.Provider == "" {
		snapshot.Provider = plan.Provider
	}
	if snapshot.RepositoryRef == "" {
		snapshot.RepositoryRef = plan.RepositoryRef
	}
	if snapshot.ChangeRef == "" {
		snapshot.ChangeRef = plan.ChangeRef
	}
	if snapshot.HeadSHA == "" {
		snapshot.HeadSHA = plan.HeadSHA
	}
	for index := range snapshot.Items {
		if snapshot.Items[index].CommitSHA == "" {
			snapshot.Items[index].CommitSHA = snapshot.HeadSHA
		}
	}
	return snapshot
}

func latestChangeSetCommitSHA(changeSet ChangeSet) string {
	if len(changeSet.CommitRefs) == 0 {
		return ""
	}
	return changeSet.CommitRefs[len(changeSet.CommitRefs)-1].SHA
}
