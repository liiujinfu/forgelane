package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/liiujinfu/forgelane/internal/workflow"
)

// GetActiveChangeSetByChangeRef returns the active ChangeSet mapped to a provider PR ref.
func (store *Store) GetActiveChangeSetByChangeRef(changeRef string) (workflow.ChangeSet, error) {
	var changeSet workflow.ChangeSet
	err := store.db.QueryRow(`
SELECT
	id,
	work_item_id,
	work_item_ref,
	provider,
	repository_ref,
	base_branch,
	branch_ref,
	branch_provider_ref,
	change_ref,
	change_draft,
	provider_snapshot,
	status,
	created_by_run_id,
	active_run_id,
	created_at,
	updated_at
FROM change_sets
WHERE change_ref = ?
	AND status NOT IN ('merged', 'closed', 'abandoned')
ORDER BY id DESC
LIMIT 1`, changeRef).Scan(
		&changeSet.ID,
		&changeSet.WorkItemID,
		&changeSet.WorkItemRef,
		&changeSet.Provider,
		&changeSet.RepositoryRef,
		&changeSet.BaseBranch,
		&changeSet.BranchRef,
		&changeSet.BranchProviderRef,
		&changeSet.ChangeRef,
		&changeSet.ChangeDraft,
		&changeSet.ProviderSnapshot,
		&changeSet.Status,
		&changeSet.CreatedByRunID,
		&changeSet.ActiveRunID,
		&changeSet.CreatedAt,
		&changeSet.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return workflow.ChangeSet{}, ErrChangeSetNotFound
	}
	if err != nil {
		return workflow.ChangeSet{}, fmt.Errorf("query active ChangeSet for %s: %w", changeRef, err)
	}
	commitRefs, err := store.listCommitRefsForChangeSet(changeSet.ID)
	if err != nil {
		return workflow.ChangeSet{}, err
	}
	changeSet.CommitRefs = commitRefs
	return changeSet, nil
}

// SyncChangeFeedback replaces the current compact feedback snapshot for an active ChangeSet.
func (store *Store) SyncChangeFeedback(changeSetID int64, agentRunID int64, snapshot workflow.ChangeFeedbackSnapshot, plan workflow.ControlActionPlan) (workflow.ChangeFeedbackSyncResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := store.db.Begin()
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("begin Change feedback sync transaction: %w", err)
	}
	defer tx.Rollback()

	changeSet, err := scanChangeSetByIDTx(tx, changeSetID)
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, err
	}
	if changeSet.ActiveRunID != agentRunID {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("ChangeSet %d is active for AgentRun %d; expected AgentRun %d", changeSet.ID, changeSet.ActiveRunID, agentRunID)
	}
	forgeProjectID, err := forgeProjectIDForWorkItemTx(tx, changeSet.WorkItemID)
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, err
	}

	actionInput, err := json.Marshal(plan.Input)
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("encode ControlAction input: %w", err)
	}
	actionResult, err := tx.Exec(`
INSERT INTO control_actions (
	type,
	target_type,
	target_ref,
	requested_by,
	reason,
	input,
	status,
	created_at,
	decided_at,
	result_event_refs
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		plan.Type,
		plan.TargetType,
		plan.TargetRef,
		plan.RequestedBy,
		plan.Reason,
		string(actionInput),
		plan.Status,
		now,
		now,
		"[]",
	)
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("insert Change feedback ControlAction: %w", err)
	}
	controlActionID, err := actionResult.LastInsertId()
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("read inserted Change feedback ControlAction id: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM change_feedback_items WHERE change_set_id = ?", changeSetID); err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("replace Change feedback items for ChangeSet %d: %w", changeSetID, err)
	}
	commitRefIDs, err := commitRefIDsBySHAForChangeSetTx(tx, changeSetID)
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, err
	}
	for _, item := range snapshot.Items {
		itemSnapshot, err := json.Marshal(item.ProviderSnapshot)
		if err != nil {
			return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("encode Change feedback item provider snapshot: %w", err)
		}
		var commitRefID any
		if id, ok := commitRefIDs[item.CommitSHA]; ok {
			commitRefID = id
		}
		if _, err := tx.Exec(`
INSERT INTO change_feedback_items (
	change_set_id,
	agent_run_id,
	control_action_id,
	commit_ref_id,
	sync_event_id,
	provider,
	repository_ref,
	change_ref,
	provider_ref,
	kind,
	actionable,
	summary,
	body,
	path,
	line,
	commit_sha,
	state,
	provider_snapshot,
	synced_at,
	created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			changeSetID,
			agentRunID,
			controlActionID,
			commitRefID,
			nil,
			snapshot.Provider,
			snapshot.RepositoryRef,
			snapshot.ChangeRef,
			item.ProviderRef,
			item.Kind,
			boolInt(item.Actionable),
			item.Summary,
			item.Body,
			item.Path,
			item.Line,
			item.CommitSHA,
			item.State,
			string(itemSnapshot),
			now,
			now,
		); err != nil {
			return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("insert Change feedback item %s: %w", item.ProviderRef, err)
		}
	}

	events := make([]workflow.Event, 0, 2)
	controlEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "control_action.succeeded",
		OccurredAt:      now,
		ForgeProjectID:  forgeProjectID,
		SubjectType:     "control_action",
		SubjectRef:      fmt.Sprintf("control_action:%d", controlActionID),
		WorkItemID:      changeSet.WorkItemID,
		WorkItemRef:     changeSet.WorkItemRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		ProviderRef:     snapshot.ChangeRef,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"type":              plan.Type,
			"status":            plan.Status,
			"agent_run_id":      agentRunID,
			"change_set_id":     changeSetID,
			"change_ref":        snapshot.ChangeRef,
		},
	})
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, err
	}
	events = append(events, controlEvent)

	actionableItems := workflow.ActionableChangeFeedbackItems(snapshot.Items)
	syncEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "change_feedback.synced",
		OccurredAt:      now,
		ForgeProjectID:  forgeProjectID,
		SubjectType:     "change_set",
		SubjectRef:      fmt.Sprintf("change_set:%d", changeSetID),
		WorkItemID:      changeSet.WorkItemID,
		WorkItemRef:     changeSet.WorkItemRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		ProviderRef:     snapshot.ChangeRef,
		Payload: map[string]any{
			"change_set_id":             changeSetID,
			"agent_run_id":              agentRunID,
			"work_item_ref":             changeSet.WorkItemRef,
			"change_ref":                snapshot.ChangeRef,
			"head_sha":                  snapshot.HeadSHA,
			"feedback_count":            len(snapshot.Items),
			"actionable_feedback_count": len(actionableItems),
		},
	})
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, err
	}
	events = append(events, syncEvent)
	if _, err := tx.Exec(`
UPDATE change_feedback_items
SET sync_event_id = ?
WHERE change_set_id = ?
	AND control_action_id = ?`, syncEvent.ID, changeSetID, controlActionID); err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("link Change feedback items to sync Event %d: %w", syncEvent.ID, err)
	}

	resultEventIDs := make([]int64, 0, len(events))
	for _, event := range events {
		resultEventIDs = append(resultEventIDs, event.ID)
	}
	resultEventRefs, err := json.Marshal(resultEventIDs)
	if err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("encode Change feedback ControlAction result Event refs: %w", err)
	}
	if _, err := tx.Exec("UPDATE control_actions SET result_event_refs = ? WHERE id = ?", string(resultEventRefs), controlActionID); err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("update Change feedback ControlAction result Event refs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return workflow.ChangeFeedbackSyncResult{}, fmt.Errorf("commit Change feedback sync transaction: %w", err)
	}

	return workflow.ChangeFeedbackSyncResult{
		ControlAction:   workflow.ControlAction{ID: controlActionID, Type: plan.Type, Status: plan.Status},
		ChangeSet:       changeSet,
		Snapshot:        snapshot,
		Items:           snapshot.Items,
		ActionableItems: actionableItems,
		Events:          events,
	}, nil
}

func forgeProjectIDForWorkItemTx(tx *sql.Tx, workItemID int64) (int64, error) {
	var forgeProjectID int64
	if err := tx.QueryRow("SELECT forge_project_id FROM work_items WHERE id = ?", workItemID).Scan(&forgeProjectID); err != nil {
		return 0, fmt.Errorf("query ForgeProject for WorkItem %d: %w", workItemID, err)
	}
	return forgeProjectID, nil
}

func commitRefIDsBySHAForChangeSetTx(tx *sql.Tx, changeSetID int64) (map[string]int64, error) {
	rows, err := tx.Query(`
SELECT id, sha
FROM commit_refs
WHERE change_set_id = ?`, changeSetID)
	if err != nil {
		return nil, fmt.Errorf("query CommitRefs for ChangeSet %d: %w", changeSetID, err)
	}
	defer rows.Close()

	commitRefIDs := make(map[string]int64)
	for rows.Next() {
		var id int64
		var sha string
		if err := rows.Scan(&id, &sha); err != nil {
			return nil, fmt.Errorf("scan CommitRef for ChangeSet %d: %w", changeSetID, err)
		}
		commitRefIDs[sha] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CommitRefs for ChangeSet %d: %w", changeSetID, err)
	}
	return commitRefIDs, nil
}

func scanChangeSetByIDTx(tx *sql.Tx, changeSetID int64) (workflow.ChangeSet, error) {
	var changeSet workflow.ChangeSet
	err := tx.QueryRow(`
SELECT
	id,
	work_item_id,
	work_item_ref,
	provider,
	repository_ref,
	base_branch,
	branch_ref,
	branch_provider_ref,
	change_ref,
	change_draft,
	provider_snapshot,
	status,
	created_by_run_id,
	active_run_id,
	created_at,
	updated_at
FROM change_sets
WHERE id = ?`, changeSetID).Scan(
		&changeSet.ID,
		&changeSet.WorkItemID,
		&changeSet.WorkItemRef,
		&changeSet.Provider,
		&changeSet.RepositoryRef,
		&changeSet.BaseBranch,
		&changeSet.BranchRef,
		&changeSet.BranchProviderRef,
		&changeSet.ChangeRef,
		&changeSet.ChangeDraft,
		&changeSet.ProviderSnapshot,
		&changeSet.Status,
		&changeSet.CreatedByRunID,
		&changeSet.ActiveRunID,
		&changeSet.CreatedAt,
		&changeSet.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return workflow.ChangeSet{}, ErrChangeSetNotFound
	}
	if err != nil {
		return workflow.ChangeSet{}, fmt.Errorf("query ChangeSet %d: %w", changeSetID, err)
	}
	return changeSet, nil
}
