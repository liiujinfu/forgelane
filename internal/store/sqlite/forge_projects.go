package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/liiujinfu/forgelane/internal/workflow"
	"github.com/liiujinfu/forgelane/internal/workitems"
	_ "modernc.org/sqlite"
)

// ErrWorkItemNotFound reports a missing cached WorkItem snapshot.
var ErrWorkItemNotFound = errors.New("WorkItem not found")

// ErrAgentRunNotFound reports a missing AgentRun current-state row.
var ErrAgentRunNotFound = errors.New("AgentRun not found")

// Store owns access to ForgeLane's instance-global SQLite database.
type Store struct {
	db *sql.DB
}

// ForgeProject is the persisted provider-backed project identity.
type ForgeProject struct {
	ID             int64
	Provider       string
	ProviderHost   string
	RepositoryPath string
	ProviderRef    string
	Initialized    bool
}

// Open opens the SQLite store, creating the parent state directory when needed.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create ForgeLane state directory: %w", err)
	}

	return open(path)
}

// OpenReadOnly opens an existing SQLite store without creating state.
func OpenReadOnly(path string) (*Store, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("ForgeLane database not initialized; run forgelane init or work-items import")
	}
	if err != nil {
		return nil, fmt.Errorf("inspect ForgeLane database: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("ForgeLane database path is a directory: %s", path)
	}

	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "mode=ro",
	}).String()
	return open(dsn)
}

func open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open ForgeLane database: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases database resources.
func (store *Store) Close() error {
	return store.db.Close()
}

// Initialize creates the explicit v0 state schema used by repository init.
func (store *Store) Initialize() error {
	const schema = `
CREATE TABLE IF NOT EXISTS forge_projects (
	id INTEGER PRIMARY KEY,
	provider TEXT NOT NULL,
	provider_host TEXT NOT NULL,
	repository_path TEXT NOT NULL,
	provider_ref TEXT NOT NULL,
	initialized_at TEXT,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
	UNIQUE(provider_ref),
	UNIQUE(provider, provider_host, repository_path)
);

CREATE TABLE IF NOT EXISTS work_items (
	id INTEGER PRIMARY KEY,
	forge_project_id INTEGER NOT NULL REFERENCES forge_projects(id),
	provider_ref TEXT NOT NULL UNIQUE,
	provider TEXT NOT NULL,
	repository_ref TEXT NOT NULL,
	provider_issue_number INTEGER NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN ('open', 'closed', 'unknown')),
	provider_status_raw TEXT NOT NULL,
	url TEXT NOT NULL,
	provider_updated_at TEXT NOT NULL,
	imported_at TEXT NOT NULL,
	refreshed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_work_items_project_issue
ON work_items(forge_project_id, provider_issue_number);

CREATE TABLE IF NOT EXISTS agent_runs (
	id INTEGER PRIMARY KEY,
	work_item_id INTEGER NOT NULL REFERENCES work_items(id),
	status TEXT NOT NULL CHECK(status IN (
		'planned',
		'queued',
		'preparing',
		'running',
		'cancel_requested',
		'finalizing',
		'completed',
		'failed',
		'cancelled',
		'timed_out'
	)),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_work_item_id
ON agent_runs(work_item_id);

CREATE TABLE IF NOT EXISTS run_specs (
	id INTEGER PRIMARY KEY,
	agent_run_id INTEGER NOT NULL REFERENCES agent_runs(id),
	spec_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(agent_run_id)
);

CREATE TABLE IF NOT EXISTS runner_jobs (
	id INTEGER PRIMARY KEY,
	agent_run_id INTEGER NOT NULL REFERENCES agent_runs(id),
	status TEXT NOT NULL CHECK(status IN (
		'preparing',
		'ready',
		'running',
		'completed',
		'failed',
		'cancelled',
		'timed_out'
	)),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(agent_run_id)
);

CREATE TABLE IF NOT EXISTS workspaces (
	id INTEGER PRIMARY KEY,
	agent_run_id INTEGER NOT NULL REFERENCES agent_runs(id),
	runner_job_id INTEGER NOT NULL REFERENCES runner_jobs(id),
	status TEXT NOT NULL CHECK(status IN ('allocated', 'ready', 'failed')),
	root_path TEXT NOT NULL,
	repo_path TEXT NOT NULL,
	logs_path TEXT NOT NULL,
	artifacts_path TEXT NOT NULL,
	tmp_path TEXT NOT NULL,
	failure_message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	UNIQUE(agent_run_id)
);

CREATE TABLE IF NOT EXISTS log_segments (
	id INTEGER PRIMARY KEY,
	agent_run_id INTEGER NOT NULL REFERENCES agent_runs(id),
	stream TEXT NOT NULL CHECK(stream IN ('stdout', 'stderr')),
	sequence INTEGER NOT NULL,
	byte_start INTEGER NOT NULL,
	byte_end INTEGER NOT NULL,
	preview TEXT NOT NULL,
	artifact_path TEXT NOT NULL,
	truncated INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	UNIQUE(agent_run_id, sequence)
);

CREATE TABLE IF NOT EXISTS change_sets (
	id INTEGER PRIMARY KEY,
	work_item_id INTEGER NOT NULL REFERENCES work_items(id),
	work_item_ref TEXT NOT NULL,
	provider TEXT NOT NULL,
	repository_ref TEXT NOT NULL,
	base_branch TEXT NOT NULL,
	branch_ref TEXT NOT NULL,
	branch_provider_ref TEXT NOT NULL DEFAULT '',
	change_ref TEXT NOT NULL DEFAULT '',
	change_draft INTEGER NOT NULL DEFAULT 0,
	provider_snapshot TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL CHECK(status IN (
		'planned',
		'branch_ready',
		'branch_push_failed',
		'draft_open',
		'under_review',
		'changes_requested',
		'approved',
		'merged',
		'closed',
		'abandoned'
	)),
	created_by_run_id INTEGER NOT NULL REFERENCES agent_runs(id),
	active_run_id INTEGER NOT NULL REFERENCES agent_runs(id),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS commit_refs (
	id INTEGER PRIMARY KEY,
	agent_run_id INTEGER NOT NULL REFERENCES agent_runs(id),
	change_set_id INTEGER REFERENCES change_sets(id),
	repository_ref TEXT NOT NULL,
	sha TEXT NOT NULL,
	subject TEXT NOT NULL,
	author_name TEXT NOT NULL,
	author_email TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(agent_run_id, sha)
);

CREATE TABLE IF NOT EXISTS control_actions (
	id INTEGER PRIMARY KEY,
	type TEXT NOT NULL,
	target_type TEXT NOT NULL,
	target_ref TEXT NOT NULL,
	requested_by TEXT NOT NULL,
	reason TEXT NOT NULL,
	input TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN (
		'requested',
		'accepted',
		'rejected',
		'executing',
		'succeeded',
		'failed',
		'cancelled'
	)),
	created_at TEXT NOT NULL,
	decided_at TEXT,
	result_event_refs TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY,
	type TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	actor TEXT NOT NULL,
	forge_project_id INTEGER REFERENCES forge_projects(id),
	subject_type TEXT NOT NULL,
	subject_ref TEXT NOT NULL,
	work_item_id INTEGER REFERENCES work_items(id),
	work_item_ref TEXT,
	agent_run_id INTEGER,
	control_action_id INTEGER REFERENCES control_actions(id),
	change_set_id INTEGER,
	provider_ref TEXT,
	correlation_id TEXT,
	payload TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_work_item_id ON events(work_item_id);
CREATE INDEX IF NOT EXISTS idx_events_forge_project_id ON events(forge_project_id);
CREATE INDEX IF NOT EXISTS idx_events_agent_run_id ON events(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_runner_jobs_agent_run_id ON runner_jobs(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_agent_run_id ON workspaces(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_log_segments_agent_run_id ON log_segments(agent_run_id, sequence);
CREATE INDEX IF NOT EXISTS idx_change_sets_work_item_id ON change_sets(work_item_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_change_sets_one_active_per_work_item
ON change_sets(work_item_id)
WHERE status NOT IN ('merged', 'closed', 'abandoned');
CREATE INDEX IF NOT EXISTS idx_commit_refs_agent_run_id ON commit_refs(agent_run_id);`

	if _, err := store.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize ForgeLane database schema: %w", err)
	}
	if err := store.ensureColumn("forge_projects", "initialized_at", "TEXT"); err != nil {
		return err
	}
	if err := store.ensureColumn("events", "control_action_id", "INTEGER REFERENCES control_actions(id)"); err != nil {
		return err
	}
	if err := store.ensureColumn("commit_refs", "repository_ref", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := store.ensureColumn("commit_refs", "change_set_id", "INTEGER REFERENCES change_sets(id)"); err != nil {
		return err
	}
	if err := store.ensureColumn("change_sets", "branch_provider_ref", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := store.ensureColumn("change_sets", "change_draft", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := store.ensureColumn("change_sets", "provider_snapshot", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := store.db.Exec("CREATE INDEX IF NOT EXISTS idx_events_control_action_id ON events(control_action_id)"); err != nil {
		return fmt.Errorf("initialize ControlAction event index: %w", err)
	}
	if _, err := store.db.Exec("CREATE INDEX IF NOT EXISTS idx_commit_refs_change_set_id ON commit_refs(change_set_id)"); err != nil {
		return fmt.Errorf("initialize CommitRef ChangeSet index: %w", err)
	}
	return nil
}

func (store *Store) ensureColumn(table string, column string, definition string) error {
	hasColumn, err := store.tableHasColumn(table, column)
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	if _, err := store.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
	}
	return nil
}

func (store *Store) tableHasColumn(table string, column string) (bool, error) {
	rows, err := store.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, fmt.Errorf("inspect %s schema: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate %s schema: %w", table, err)
	}
	return false, nil
}

// WorkItem is a persisted WorkItem snapshot.
type WorkItem = workflow.WorkItemSnapshot

// Event is a persisted audit event.
type Event = workflow.Event

// WorkItemImportResult is the outcome of an atomic WorkItem import.
type WorkItemImportResult struct {
	WorkItem WorkItem
	Event    Event
}

// AgentRun is a persisted bounded agent attempt.
type AgentRun = workflow.AgentRun

// RunnerJob is the runner-facing execution request for one AgentRun.
type RunnerJob = workflow.RunnerJob

// WorkspacePaths are the filesystem paths leased for one Workspace.
type WorkspacePaths = workflow.WorkspacePaths

// Workspace is the persisted execution filesystem lease for one AgentRun.
type Workspace = workflow.Workspace

// LogSegment indexes one stdout/stderr range in a Workspace log file.
type LogSegment = workflow.LogSegment

// CommitRef records one local repository commit produced by an AgentRun.
type CommitRef = workflow.CommitRef

// ChangeSet records one ForgeLane-owned delivery artifact for a WorkItem.
type ChangeSet = workflow.ChangeSet

// RunSpec is the immutable execution input snapshot for one AgentRun.
type RunSpec = workflow.RunSpec

// AgentRunCreateResult is the outcome of creating AgentRun execution state.
type AgentRunCreateResult = workflow.AgentRunCreateResult

// AgentRunPrepareResult is the outcome of preparing runner state for execution.
type AgentRunPrepareResult = workflow.AgentRunPrepareResult

// AgentRunDetail is the read model for inspecting one AgentRun.
type AgentRunDetail = workflow.AgentRunDetail

// ControlAction is a persisted operator request to change the delivery loop.
type ControlAction = workflow.ControlAction

// ImportWorkItem persists a provider-owned issue snapshot and matching audit Event.
func (store *Store) ImportWorkItem(issue workitems.ProviderIssue) (WorkItemImportResult, error) {
	importDecision, err := workitems.NewWorkItemImport(issue)
	if err != nil {
		return WorkItemImportResult{}, err
	}
	issue = importDecision.Issue
	ref := importDecision.Ref

	tx, err := store.db.Begin()
	if err != nil {
		return WorkItemImportResult{}, fmt.Errorf("begin WorkItem import transaction: %w", err)
	}
	defer tx.Rollback()

	forgeProjectID, err := upsertForgeProjectTx(tx, ForgeProject{
		Provider:       ref.Provider,
		ProviderHost:   ref.ProviderHost,
		RepositoryPath: ref.RepositoryPath,
		ProviderRef:    ref.RepositoryRef(),
		Initialized:    false,
	})
	if err != nil {
		return WorkItemImportResult{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	providerUpdatedAt := issue.ProviderUpdatedAt.UTC().Format(time.RFC3339)
	var existingID int64
	var importedAt string
	err = tx.QueryRow(
		"SELECT id, imported_at FROM work_items WHERE provider_ref = ?",
		issue.ProviderRef,
	).Scan(&existingID, &importedAt)

	existing := true
	var workItemID int64
	switch {
	case err == sql.ErrNoRows:
		existing = false
		importedAt = now
		result, err := tx.Exec(`
INSERT INTO work_items (
	forge_project_id,
	provider_ref,
	provider,
	repository_ref,
	provider_issue_number,
	title,
	body,
	status,
	provider_status_raw,
	url,
	provider_updated_at,
	imported_at,
	refreshed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			forgeProjectID,
			issue.ProviderRef,
			issue.Provider,
			issue.RepositoryRef,
			issue.ProviderIssueNumber,
			issue.Title,
			issue.Body,
			issue.Status,
			issue.RawStatus,
			issue.URL,
			providerUpdatedAt,
			importedAt,
			now,
		)
		if err != nil {
			return WorkItemImportResult{}, fmt.Errorf("insert WorkItem %s: %w", issue.ProviderRef, err)
		}
		workItemID, err = result.LastInsertId()
		if err != nil {
			return WorkItemImportResult{}, fmt.Errorf("read inserted WorkItem id: %w", err)
		}
	case err != nil:
		return WorkItemImportResult{}, fmt.Errorf("lookup WorkItem %s: %w", issue.ProviderRef, err)
	default:
		workItemID = existingID
		if _, err := tx.Exec(`
UPDATE work_items
SET forge_project_id = ?,
	provider = ?,
	repository_ref = ?,
	provider_issue_number = ?,
	title = ?,
	body = ?,
	status = ?,
	provider_status_raw = ?,
	url = ?,
	provider_updated_at = ?,
	refreshed_at = ?
WHERE id = ?`,
			forgeProjectID,
			issue.Provider,
			issue.RepositoryRef,
			issue.ProviderIssueNumber,
			issue.Title,
			issue.Body,
			issue.Status,
			issue.RawStatus,
			issue.URL,
			providerUpdatedAt,
			now,
			workItemID,
		); err != nil {
			return WorkItemImportResult{}, fmt.Errorf("update WorkItem %s: %w", issue.ProviderRef, err)
		}
	}

	eventPlan := importDecision.EventPlan(workitems.ImportEventInput{
		Existing:          existing,
		WorkItemID:        workItemID,
		ForgeProjectID:    forgeProjectID,
		ProviderUpdatedAt: providerUpdatedAt,
	})
	payload, err := json.Marshal(eventPlan.Payload)
	if err != nil {
		return WorkItemImportResult{}, fmt.Errorf("encode WorkItem event payload: %w", err)
	}

	eventResult, err := tx.Exec(`
INSERT INTO events (
	type,
	occurred_at,
	actor,
	forge_project_id,
	subject_type,
	subject_ref,
	work_item_id,
	work_item_ref,
	provider_ref,
	payload
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventPlan.Type,
		now,
		"forgelane",
		forgeProjectID,
		eventPlan.SubjectType,
		eventPlan.SubjectRef,
		workItemID,
		issue.ProviderRef,
		eventPlan.ProviderRef,
		string(payload),
	)
	if err != nil {
		return WorkItemImportResult{}, fmt.Errorf("append WorkItem import event: %w", err)
	}
	eventID, err := eventResult.LastInsertId()
	if err != nil {
		return WorkItemImportResult{}, fmt.Errorf("read inserted Event id: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return WorkItemImportResult{}, fmt.Errorf("commit WorkItem import: %w", err)
	}

	workItem, err := store.GetWorkItemByProviderRef(issue.ProviderRef)
	if err != nil {
		return WorkItemImportResult{}, err
	}
	return WorkItemImportResult{
		WorkItem: workItem,
		Event: Event{
			ID:   eventID,
			Type: eventPlan.Type,
		},
	}, nil
}

// GetWorkItemByProviderRef returns a WorkItem snapshot by canonical ProviderRef.
func (store *Store) GetWorkItemByProviderRef(providerRef string) (WorkItem, error) {
	return store.getWorkItem("provider_ref = ?", providerRef)
}

// GetWorkItemByID returns a WorkItem snapshot by local id.
func (store *Store) GetWorkItemByID(id int64) (WorkItem, error) {
	return store.getWorkItem("id = ?", id)
}

func (store *Store) getWorkItem(where string, arg any) (WorkItem, error) {
	const query = `
SELECT
	id,
	forge_project_id,
	provider_ref,
	provider,
	repository_ref,
	provider_issue_number,
	title,
	body,
	status,
	provider_status_raw,
	url,
	provider_updated_at,
	imported_at,
	refreshed_at
FROM work_items
WHERE `

	var workItem WorkItem
	err := store.db.QueryRow(query+where, arg).Scan(
		&workItem.ID,
		&workItem.ForgeProjectID,
		&workItem.ProviderRef,
		&workItem.Provider,
		&workItem.RepositoryRef,
		&workItem.ProviderIssueNumber,
		&workItem.Title,
		&workItem.Body,
		&workItem.Status,
		&workItem.ProviderStatusRaw,
		&workItem.URL,
		&workItem.ProviderUpdatedAt,
		&workItem.ImportedAt,
		&workItem.RefreshedAt,
	)
	if err == sql.ErrNoRows {
		return WorkItem{}, fmt.Errorf("%w: %s", ErrWorkItemNotFound, arg)
	}
	if err != nil {
		return WorkItem{}, fmt.Errorf("query WorkItem: %w", err)
	}
	return workItem, nil
}

// GetAgentRunDetail returns current state for one AgentRun and its immutable RunSpec.
func (store *Store) GetAgentRunDetail(id int64) (AgentRunDetail, error) {
	const query = `
SELECT
	agent_runs.id,
	agent_runs.work_item_id,
	agent_runs.status,
	agent_runs.created_at,
	agent_runs.updated_at,
	work_items.id,
	work_items.forge_project_id,
	work_items.provider_ref,
	work_items.provider,
	work_items.repository_ref,
	work_items.provider_issue_number,
	work_items.title,
	work_items.body,
	work_items.status,
	work_items.provider_status_raw,
	work_items.url,
	work_items.provider_updated_at,
	work_items.imported_at,
	work_items.refreshed_at,
	run_specs.id,
	run_specs.agent_run_id,
	run_specs.spec_json,
	run_specs.created_at
FROM agent_runs
JOIN work_items ON work_items.id = agent_runs.work_item_id
JOIN run_specs ON run_specs.agent_run_id = agent_runs.id
WHERE agent_runs.id = ?`

	var detail AgentRunDetail
	err := store.db.QueryRow(query, id).Scan(
		&detail.AgentRun.ID,
		&detail.AgentRun.WorkItemID,
		&detail.AgentRun.Status,
		&detail.AgentRun.CreatedAt,
		&detail.AgentRun.UpdatedAt,
		&detail.WorkItem.ID,
		&detail.WorkItem.ForgeProjectID,
		&detail.WorkItem.ProviderRef,
		&detail.WorkItem.Provider,
		&detail.WorkItem.RepositoryRef,
		&detail.WorkItem.ProviderIssueNumber,
		&detail.WorkItem.Title,
		&detail.WorkItem.Body,
		&detail.WorkItem.Status,
		&detail.WorkItem.ProviderStatusRaw,
		&detail.WorkItem.URL,
		&detail.WorkItem.ProviderUpdatedAt,
		&detail.WorkItem.ImportedAt,
		&detail.WorkItem.RefreshedAt,
		&detail.RunSpec.ID,
		&detail.RunSpec.AgentRunID,
		&detail.RunSpec.SpecJSON,
		&detail.RunSpec.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return AgentRunDetail{}, fmt.Errorf("%w: %d", ErrAgentRunNotFound, id)
	}
	if err != nil {
		return AgentRunDetail{}, fmt.Errorf("query AgentRun detail: %w", err)
	}
	workspace, err := store.getWorkspaceForAgentRun(id)
	if err != nil {
		return AgentRunDetail{}, err
	}
	detail.Workspace = workspace
	commitRefs, err := store.listCommitRefsForAgentRun(id)
	if err != nil {
		return AgentRunDetail{}, err
	}
	detail.CommitRefs = commitRefs
	changeSet, err := store.getChangeSetForAgentRun(id)
	if err != nil {
		return AgentRunDetail{}, err
	}
	detail.ChangeSet = changeSet
	deliverySkipped, deliverySkipReason, err := store.getDeliverySkipForAgentRun(id)
	if err != nil {
		return AgentRunDetail{}, err
	}
	detail.DeliverySkipped = deliverySkipped
	detail.DeliverySkipReason = deliverySkipReason
	return detail, nil
}

func (store *Store) getDeliverySkipForAgentRun(agentRunID int64) (bool, string, error) {
	var payloadJSON string
	err := store.db.QueryRow(`
SELECT payload
FROM events
WHERE agent_run_id = ?
	AND type = 'repository_delivery.skipped'
ORDER BY id DESC
LIMIT 1`, agentRunID).Scan(&payloadJSON)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("query delivery skip Event for AgentRun %d: %w", agentRunID, err)
	}

	var payload struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return false, "", fmt.Errorf("decode delivery skip Event for AgentRun %d: %w", agentRunID, err)
	}
	if payload.Reason == "" {
		payload.Reason = "no_repository_changes"
	}
	return true, payload.Reason, nil
}

func (store *Store) getWorkspaceForAgentRun(agentRunID int64) (*Workspace, error) {
	var workspace Workspace
	err := store.db.QueryRow(`
SELECT
	id,
	agent_run_id,
	runner_job_id,
	status,
	root_path,
	repo_path,
	logs_path,
	artifacts_path,
	tmp_path,
	failure_message,
	created_at,
	updated_at
FROM workspaces
WHERE agent_run_id = ?`, agentRunID).Scan(
		&workspace.ID,
		&workspace.AgentRunID,
		&workspace.RunnerJobID,
		&workspace.Status,
		&workspace.Paths.Root,
		&workspace.Paths.Repo,
		&workspace.Paths.Logs,
		&workspace.Paths.Artifacts,
		&workspace.Paths.Tmp,
		&workspace.FailureMessage,
		&workspace.CreatedAt,
		&workspace.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query Workspace for AgentRun %d: %w", agentRunID, err)
	}
	return &workspace, nil
}

func (store *Store) listCommitRefsForAgentRun(agentRunID int64) ([]CommitRef, error) {
	hasChangeSetID, err := store.tableHasColumn("commit_refs", "change_set_id")
	if err != nil {
		return nil, err
	}
	if !hasChangeSetID {
		return store.listLegacyCommitRefsForAgentRun(agentRunID)
	}

	rows, err := store.db.Query(`
SELECT id, agent_run_id, COALESCE(change_set_id, 0), repository_ref, sha, subject, author_name, author_email, created_at
FROM commit_refs
WHERE agent_run_id = ?
ORDER BY id ASC`, agentRunID)
	if err != nil {
		return nil, fmt.Errorf("query CommitRefs for AgentRun %d: %w", agentRunID, err)
	}
	defer rows.Close()

	var refs []CommitRef
	for rows.Next() {
		var ref CommitRef
		if err := rows.Scan(
			&ref.ID,
			&ref.AgentRunID,
			&ref.ChangeSetID,
			&ref.RepositoryRef,
			&ref.SHA,
			&ref.Subject,
			&ref.AuthorName,
			&ref.AuthorEmail,
			&ref.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan CommitRef for AgentRun %d: %w", agentRunID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CommitRefs for AgentRun %d: %w", agentRunID, err)
	}
	return refs, nil
}

func (store *Store) listLegacyCommitRefsForAgentRun(agentRunID int64) ([]CommitRef, error) {
	rows, err := store.db.Query(`
SELECT id, agent_run_id, repository_ref, sha, subject, author_name, author_email, created_at
FROM commit_refs
WHERE agent_run_id = ?
ORDER BY id ASC`, agentRunID)
	if err != nil {
		return nil, fmt.Errorf("query CommitRefs for AgentRun %d: %w", agentRunID, err)
	}
	defer rows.Close()

	var refs []CommitRef
	for rows.Next() {
		var ref CommitRef
		if err := rows.Scan(
			&ref.ID,
			&ref.AgentRunID,
			&ref.RepositoryRef,
			&ref.SHA,
			&ref.Subject,
			&ref.AuthorName,
			&ref.AuthorEmail,
			&ref.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan CommitRef for AgentRun %d: %w", agentRunID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CommitRefs for AgentRun %d: %w", agentRunID, err)
	}
	return refs, nil
}

func (store *Store) getChangeSetForAgentRun(agentRunID int64) (*ChangeSet, error) {
	hasChangeSetID, err := store.tableHasColumn("change_sets", "id")
	if err != nil {
		return nil, err
	}
	if !hasChangeSetID {
		return nil, nil
	}
	hasCommitChangeSetID, err := store.tableHasColumn("commit_refs", "change_set_id")
	if err != nil {
		return nil, err
	}
	if !hasCommitChangeSetID {
		return nil, nil
	}

	var changeSet ChangeSet
	err = store.db.QueryRow(`
SELECT
	c.id,
	c.work_item_id,
	c.work_item_ref,
	c.provider,
	c.repository_ref,
	c.base_branch,
	c.branch_ref,
	c.branch_provider_ref,
	c.change_ref,
	c.change_draft,
	c.provider_snapshot,
	c.status,
	c.created_by_run_id,
	c.active_run_id,
	c.created_at,
	c.updated_at
FROM change_sets c
WHERE c.active_run_id = ?
	OR EXISTS (
		SELECT 1
		FROM commit_refs r
		WHERE r.agent_run_id = ?
			AND r.change_set_id = c.id
	)
ORDER BY CASE WHEN c.active_run_id = ? THEN 0 ELSE 1 END, c.id DESC
LIMIT 1`, agentRunID, agentRunID, agentRunID).Scan(
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
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query ChangeSet for AgentRun %d: %w", agentRunID, err)
	}

	commitRefs, err := store.listCommitRefsForChangeSet(changeSet.ID)
	if err != nil {
		return nil, err
	}
	changeSet.CommitRefs = commitRefs
	return &changeSet, nil
}

func (store *Store) listCommitRefsForChangeSet(changeSetID int64) ([]CommitRef, error) {
	rows, err := store.db.Query(`
SELECT id, agent_run_id, COALESCE(change_set_id, 0), repository_ref, sha, subject, author_name, author_email, created_at
FROM commit_refs
WHERE change_set_id = ?
ORDER BY id ASC`, changeSetID)
	if err != nil {
		return nil, fmt.Errorf("query CommitRefs for ChangeSet %d: %w", changeSetID, err)
	}
	defer rows.Close()

	var refs []CommitRef
	for rows.Next() {
		var ref CommitRef
		if err := rows.Scan(
			&ref.ID,
			&ref.AgentRunID,
			&ref.ChangeSetID,
			&ref.RepositoryRef,
			&ref.SHA,
			&ref.Subject,
			&ref.AuthorName,
			&ref.AuthorEmail,
			&ref.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan CommitRef for ChangeSet %d: %w", changeSetID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CommitRefs for ChangeSet %d: %w", changeSetID, err)
	}
	return refs, nil
}

// ListEventsForAgentRun returns the audit timeline for one AgentRun in append order.
func (store *Store) ListEventsForAgentRun(agentRunID int64) ([]Event, error) {
	if _, err := store.GetAgentRunDetail(agentRunID); err != nil {
		return nil, err
	}

	rows, err := store.db.Query(`
SELECT id, type, occurred_at, actor, subject_type, subject_ref
FROM events
WHERE agent_run_id = ?
ORDER BY id ASC`, agentRunID)
	if err != nil {
		return nil, fmt.Errorf("query Events for AgentRun %d: %w", agentRunID, err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var event Event
		if err := rows.Scan(
			&event.ID,
			&event.Type,
			&event.OccurredAt,
			&event.Actor,
			&event.SubjectType,
			&event.SubjectRef,
		); err != nil {
			return nil, fmt.Errorf("scan Event for AgentRun %d: %w", agentRunID, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Events for AgentRun %d: %w", agentRunID, err)
	}
	return events, nil
}

// ListLogSegmentsForAgentRun returns persisted log segment indexes in stream order.
func (store *Store) ListLogSegmentsForAgentRun(agentRunID int64, stream string) ([]LogSegment, error) {
	if _, err := store.GetAgentRunDetail(agentRunID); err != nil {
		return nil, err
	}

	query := `
SELECT id, agent_run_id, stream, sequence, byte_start, byte_end, preview, artifact_path, truncated, created_at
FROM log_segments
WHERE agent_run_id = ?`
	args := []any{agentRunID}
	if stream != "" {
		query += " AND stream = ?"
		args = append(args, stream)
	}
	query += " ORDER BY sequence ASC"

	rows, err := store.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query LogSegments for AgentRun %d: %w", agentRunID, err)
	}
	defer rows.Close()

	var segments []LogSegment
	for rows.Next() {
		var segment LogSegment
		var truncated int
		if err := rows.Scan(
			&segment.ID,
			&segment.AgentRunID,
			&segment.Stream,
			&segment.Sequence,
			&segment.ByteStart,
			&segment.ByteEnd,
			&segment.Preview,
			&segment.ArtifactPath,
			&truncated,
			&segment.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan LogSegment for AgentRun %d: %w", agentRunID, err)
		}
		segment.Truncated = truncated != 0
		segments = append(segments, segment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate LogSegments for AgentRun %d: %w", agentRunID, err)
	}
	return segments, nil
}

// MarkAgentCommandStarted records the transition into local command execution.
func (store *Store) MarkAgentCommandStarted(agentRunID int64) (Event, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return Event{}, err
	}
	if detail.Workspace == nil {
		return Event{}, fmt.Errorf("Workspace not prepared for AgentRun %d", agentRunID)
	}
	if detail.AgentRun.Status != "preparing" {
		return Event{}, fmt.Errorf("AgentRun %d is %s; expected preparing", agentRunID, detail.AgentRun.Status)
	}
	if detail.Workspace.Status != "ready" {
		return Event{}, fmt.Errorf("Workspace for AgentRun %d is %s; expected ready", agentRunID, detail.Workspace.Status)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return Event{}, fmt.Errorf("begin Agent command start transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE agent_runs SET status = ?, updated_at = ? WHERE id = ?", "running", now, agentRunID); err != nil {
		return Event{}, fmt.Errorf("mark AgentRun %d running: %w", agentRunID, err)
	}
	if _, err := tx.Exec("UPDATE runner_jobs SET status = ?, updated_at = ? WHERE id = ?", "running", now, detail.Workspace.RunnerJobID); err != nil {
		return Event{}, fmt.Errorf("mark RunnerJob %d running: %w", detail.Workspace.RunnerJobID, err)
	}
	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:           "agent_command.started",
		OccurredAt:     now,
		ForgeProjectID: detail.WorkItem.ForgeProjectID,
		SubjectType:    "agent_run",
		SubjectRef:     fmt.Sprintf("agent_run:%d", agentRunID),
		WorkItemID:     detail.WorkItem.ID,
		WorkItemRef:    detail.WorkItem.ProviderRef,
		AgentRunID:     agentRunID,
		Payload: map[string]any{
			"agent_run_id":  agentRunID,
			"runner_job_id": detail.Workspace.RunnerJobID,
			"workspace_id":  detail.Workspace.ID,
		},
	})
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, fmt.Errorf("commit Agent command start: %w", err)
	}
	return event, nil
}

// MarkAgentCommandCompleted records command output, terminal status, and completion Event.
func (store *Store) MarkAgentCommandCompleted(agentRunID int64, completion workflow.AgentCommandCompletion) (AgentRunPrepareResult, error) {
	status := completion.Status
	if status == "" {
		status = "completed"
		if completion.ExitCode != 0 {
			status = "failed"
		}
	}
	return store.finishAgentCommand(agentRunID, status, agentCommandTerminalEventType(status), completion)
}

// MarkAgentCommandFailed records failure before the command process could start.
func (store *Store) MarkAgentCommandFailed(agentRunID int64, failureMessage string) (AgentRunPrepareResult, error) {
	return store.finishAgentCommand(agentRunID, "failed", "agent_command.failed", workflow.AgentCommandCompletion{
		Status:        "failed",
		ExitCode:      -1,
		FailureDetail: failureMessage,
	})
}

func agentCommandTerminalEventType(status string) string {
	switch status {
	case "failed":
		return "agent_command.failed"
	case "timed_out":
		return "agent_command.timed_out"
	case "cancelled":
		return "agent_command.cancelled"
	default:
		return "agent_command.completed"
	}
}

// MarkChangeSetBranchPushStarted records the explicit provider mutation boundary.
func (store *Store) MarkChangeSetBranchPushStarted(agentRunID int64, changeSetID int64) (workflow.BranchPushStartResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return workflow.BranchPushStartResult{}, err
	}
	if detail.ChangeSet == nil || detail.ChangeSet.ID != changeSetID {
		return workflow.BranchPushStartResult{}, fmt.Errorf("ChangeSet %d not active for AgentRun %d", changeSetID, agentRunID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return workflow.BranchPushStartResult{}, fmt.Errorf("begin ChangeSet branch push start transaction: %w", err)
	}
	defer tx.Rollback()

	actionInput, err := json.Marshal(map[string]any{
		"change_set_id":  changeSetID,
		"agent_run_id":   agentRunID,
		"provider":       detail.ChangeSet.Provider,
		"repository_ref": detail.ChangeSet.RepositoryRef,
		"branch_ref":     detail.ChangeSet.BranchRef,
		"commit_refs":    len(detail.ChangeSet.CommitRefs),
	})
	if err != nil {
		return workflow.BranchPushStartResult{}, fmt.Errorf("encode branch push ControlAction input: %w", err)
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
		"push_branch",
		"change_set",
		fmt.Sprintf("change_set:%d", changeSetID),
		"local",
		"forgelane runs execute",
		string(actionInput),
		"executing",
		now,
		now,
		"[]",
	)
	if err != nil {
		return workflow.BranchPushStartResult{}, fmt.Errorf("insert branch push ControlAction: %w", err)
	}
	controlActionID, err := actionResult.LastInsertId()
	if err != nil {
		return workflow.BranchPushStartResult{}, fmt.Errorf("read branch push ControlAction id: %w", err)
	}

	controlEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "control_action.executing",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "control_action",
		SubjectRef:      fmt.Sprintf("control_action:%d", controlActionID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"change_set_id":     changeSetID,
			"action_type":       "push_branch",
			"status":            "executing",
		},
	})
	if err != nil {
		return workflow.BranchPushStartResult{}, err
	}
	branchEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "change_set.branch_push_started",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "change_set",
		SubjectRef:      fmt.Sprintf("change_set:%d", changeSetID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"change_set_id":     changeSetID,
			"agent_run_id":      agentRunID,
			"provider":          detail.ChangeSet.Provider,
			"repository_ref":    detail.ChangeSet.RepositoryRef,
			"branch_ref":        detail.ChangeSet.BranchRef,
			"commit_refs":       len(detail.ChangeSet.CommitRefs),
		},
	})
	if err != nil {
		return workflow.BranchPushStartResult{}, err
	}
	resultEventRefs, err := json.Marshal([]int64{controlEvent.ID, branchEvent.ID})
	if err != nil {
		return workflow.BranchPushStartResult{}, fmt.Errorf("encode branch push ControlAction result Event refs: %w", err)
	}
	if _, err := tx.Exec("UPDATE control_actions SET result_event_refs = ? WHERE id = ?", string(resultEventRefs), controlActionID); err != nil {
		return workflow.BranchPushStartResult{}, fmt.Errorf("update branch push ControlAction result Event refs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return workflow.BranchPushStartResult{}, fmt.Errorf("commit ChangeSet branch push start: %w", err)
	}
	return workflow.BranchPushStartResult{
		ControlAction: workflow.ControlAction{
			ID:     controlActionID,
			Type:   "push_branch",
			Status: "executing",
		},
		Events: []Event{controlEvent, branchEvent},
	}, nil
}

// MarkChangeSetBranchPushSucceeded marks a ChangeSet branch as provider-ready.
func (store *Store) MarkChangeSetBranchPushSucceeded(agentRunID int64, push workflow.ChangeBranchPushResult, controlActionID int64) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.ChangeSet == nil || detail.ChangeSet.ID != push.ChangeSetID {
		return AgentRunPrepareResult{}, fmt.Errorf("ChangeSet %d not active for AgentRun %d", push.ChangeSetID, agentRunID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("begin ChangeSet branch push success transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
UPDATE change_sets
SET status = ?, branch_provider_ref = ?, updated_at = ?
WHERE id = ?`, "branch_ready", push.BranchProviderRef, now, push.ChangeSetID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark ChangeSet %d branch_ready: %w", push.ChangeSetID, err)
	}
	if _, err := tx.Exec(`
UPDATE control_actions
SET status = ?, decided_at = ?
WHERE id = ?`, "succeeded", now, controlActionID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark branch push ControlAction %d succeeded: %w", controlActionID, err)
	}
	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "change_set.branch_push_succeeded",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "change_set",
		SubjectRef:      fmt.Sprintf("change_set:%d", push.ChangeSetID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     push.ChangeSetID,
		Payload: map[string]any{
			"control_action_id":   controlActionID,
			"change_set_id":       push.ChangeSetID,
			"agent_run_id":        agentRunID,
			"provider":            detail.ChangeSet.Provider,
			"repository_ref":      detail.ChangeSet.RepositoryRef,
			"branch_ref":          detail.ChangeSet.BranchRef,
			"branch_provider_ref": push.BranchProviderRef,
			"pushed_commits":      len(push.PushedCommitSHAs),
		},
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := appendControlActionEventRefTx(tx, controlActionID, event.ID); err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("commit ChangeSet branch push success: %w", err)
	}

	updated, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	return agentRunPrepareResultFromDetail(updated, []Event{event}), nil
}

// MarkChangeSetBranchPushFailed records a recoverable provider mutation failure.
func (store *Store) MarkChangeSetBranchPushFailed(agentRunID int64, changeSetID int64, controlActionID int64, failureMessage string) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.ChangeSet == nil || detail.ChangeSet.ID != changeSetID {
		return AgentRunPrepareResult{}, fmt.Errorf("ChangeSet %d not active for AgentRun %d", changeSetID, agentRunID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("begin ChangeSet branch push failure transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
UPDATE change_sets
SET status = ?, updated_at = ?
WHERE id = ?`, "branch_push_failed", now, changeSetID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark ChangeSet %d branch_push_failed: %w", changeSetID, err)
	}
	if _, err := tx.Exec(`
UPDATE control_actions
SET status = ?, decided_at = ?
WHERE id = ?`, "failed", now, controlActionID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark branch push ControlAction %d failed: %w", controlActionID, err)
	}
	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "change_set.branch_push_failed",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "change_set",
		SubjectRef:      fmt.Sprintf("change_set:%d", changeSetID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"change_set_id":     changeSetID,
			"agent_run_id":      agentRunID,
			"provider":          detail.ChangeSet.Provider,
			"repository_ref":    detail.ChangeSet.RepositoryRef,
			"branch_ref":        detail.ChangeSet.BranchRef,
			"failure_detail":    failureMessage,
			"recoverable":       true,
		},
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := appendControlActionEventRefTx(tx, controlActionID, event.ID); err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("commit ChangeSet branch push failure: %w", err)
	}

	updated, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	return agentRunPrepareResultFromDetail(updated, []Event{event}), nil
}

// MarkChangeSetDraftPRStarted records the explicit provider mutation boundary for draft PR creation or update.
func (store *Store) MarkChangeSetDraftPRStarted(agentRunID int64, changeSetID int64) (workflow.DraftPRStartResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return workflow.DraftPRStartResult{}, err
	}
	if detail.ChangeSet == nil || detail.ChangeSet.ID != changeSetID {
		return workflow.DraftPRStartResult{}, fmt.Errorf("ChangeSet %d not active for AgentRun %d", changeSetID, agentRunID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return workflow.DraftPRStartResult{}, fmt.Errorf("begin ChangeSet draft PR start transaction: %w", err)
	}
	defer tx.Rollback()

	actionInput, err := json.Marshal(map[string]any{
		"change_set_id":       changeSetID,
		"agent_run_id":        agentRunID,
		"provider":            detail.ChangeSet.Provider,
		"repository_ref":      detail.ChangeSet.RepositoryRef,
		"branch_ref":          detail.ChangeSet.BranchRef,
		"branch_provider_ref": detail.ChangeSet.BranchProviderRef,
		"existing_change_ref": detail.ChangeSet.ChangeRef,
		"commit_refs":         len(detail.ChangeSet.CommitRefs),
	})
	if err != nil {
		return workflow.DraftPRStartResult{}, fmt.Errorf("encode draft PR ControlAction input: %w", err)
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
		"create_or_update_draft_pr",
		"change_set",
		fmt.Sprintf("change_set:%d", changeSetID),
		"local",
		"forgelane runs execute",
		string(actionInput),
		"executing",
		now,
		now,
		"[]",
	)
	if err != nil {
		return workflow.DraftPRStartResult{}, fmt.Errorf("insert draft PR ControlAction: %w", err)
	}
	controlActionID, err := actionResult.LastInsertId()
	if err != nil {
		return workflow.DraftPRStartResult{}, fmt.Errorf("read draft PR ControlAction id: %w", err)
	}

	controlEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "control_action.executing",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "control_action",
		SubjectRef:      fmt.Sprintf("control_action:%d", controlActionID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"change_set_id":     changeSetID,
			"action_type":       "create_or_update_draft_pr",
			"status":            "executing",
		},
	})
	if err != nil {
		return workflow.DraftPRStartResult{}, err
	}
	draftEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "change_set.draft_pr_started",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "change_set",
		SubjectRef:      fmt.Sprintf("change_set:%d", changeSetID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		Payload: map[string]any{
			"control_action_id":   controlActionID,
			"change_set_id":       changeSetID,
			"agent_run_id":        agentRunID,
			"provider":            detail.ChangeSet.Provider,
			"repository_ref":      detail.ChangeSet.RepositoryRef,
			"branch_ref":          detail.ChangeSet.BranchRef,
			"existing_change_ref": detail.ChangeSet.ChangeRef,
			"commit_refs":         len(detail.ChangeSet.CommitRefs),
		},
	})
	if err != nil {
		return workflow.DraftPRStartResult{}, err
	}
	resultEventRefs, err := json.Marshal([]int64{controlEvent.ID, draftEvent.ID})
	if err != nil {
		return workflow.DraftPRStartResult{}, fmt.Errorf("encode draft PR ControlAction result Event refs: %w", err)
	}
	if _, err := tx.Exec("UPDATE control_actions SET result_event_refs = ? WHERE id = ?", string(resultEventRefs), controlActionID); err != nil {
		return workflow.DraftPRStartResult{}, fmt.Errorf("update draft PR ControlAction result Event refs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return workflow.DraftPRStartResult{}, fmt.Errorf("commit ChangeSet draft PR start: %w", err)
	}
	return workflow.DraftPRStartResult{
		ControlAction: workflow.ControlAction{
			ID:     controlActionID,
			Type:   "create_or_update_draft_pr",
			Status: "executing",
		},
		Events: []Event{controlEvent, draftEvent},
	}, nil
}

// MarkChangeSetDraftPRSucceeded marks a ChangeSet draft PR as provider-ready.
func (store *Store) MarkChangeSetDraftPRSucceeded(agentRunID int64, draftPR workflow.ChangeDraftPRResult, controlActionID int64) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.ChangeSet == nil || detail.ChangeSet.ID != draftPR.ChangeSetID {
		return AgentRunPrepareResult{}, fmt.Errorf("ChangeSet %d not active for AgentRun %d", draftPR.ChangeSetID, agentRunID)
	}
	providerSnapshot, err := json.Marshal(draftPR.ProviderSnapshot)
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("encode draft PR provider snapshot: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("begin ChangeSet draft PR success transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
UPDATE change_sets
SET status = ?, change_ref = ?, change_draft = ?, provider_snapshot = ?, updated_at = ?
WHERE id = ?`, "draft_open", draftPR.ChangeRef, boolInt(draftPR.Draft), string(providerSnapshot), now, draftPR.ChangeSetID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark ChangeSet %d draft_open: %w", draftPR.ChangeSetID, err)
	}
	if _, err := tx.Exec(`
UPDATE control_actions
SET status = ?, decided_at = ?
WHERE id = ?`, "succeeded", now, controlActionID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark draft PR ControlAction %d succeeded: %w", controlActionID, err)
	}
	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "change_set.draft_pr_succeeded",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "change_set",
		SubjectRef:      fmt.Sprintf("change_set:%d", draftPR.ChangeSetID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     draftPR.ChangeSetID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"change_set_id":     draftPR.ChangeSetID,
			"agent_run_id":      agentRunID,
			"provider":          detail.ChangeSet.Provider,
			"repository_ref":    detail.ChangeSet.RepositoryRef,
			"branch_ref":        detail.ChangeSet.BranchRef,
			"change_ref":        draftPR.ChangeRef,
			"draft":             draftPR.Draft,
		},
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := appendControlActionEventRefTx(tx, controlActionID, event.ID); err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("commit ChangeSet draft PR success: %w", err)
	}

	updated, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	return agentRunPrepareResultFromDetail(updated, []Event{event}), nil
}

// MarkChangeSetDraftPRFailed records a recoverable draft PR provider mutation failure.
func (store *Store) MarkChangeSetDraftPRFailed(agentRunID int64, changeSetID int64, controlActionID int64, failureMessage string) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.ChangeSet == nil || detail.ChangeSet.ID != changeSetID {
		return AgentRunPrepareResult{}, fmt.Errorf("ChangeSet %d not active for AgentRun %d", changeSetID, agentRunID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("begin ChangeSet draft PR failure transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
UPDATE change_sets
SET status = ?, updated_at = ?
WHERE id = ?`, "branch_ready", now, changeSetID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark ChangeSet %d branch_ready after draft PR failure: %w", changeSetID, err)
	}
	if _, err := tx.Exec(`
UPDATE control_actions
SET status = ?, decided_at = ?
WHERE id = ?`, "failed", now, controlActionID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark draft PR ControlAction %d failed: %w", controlActionID, err)
	}
	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "change_set.draft_pr_failed",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "change_set",
		SubjectRef:      fmt.Sprintf("change_set:%d", changeSetID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		ChangeSetID:     changeSetID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"change_set_id":     changeSetID,
			"agent_run_id":      agentRunID,
			"provider":          detail.ChangeSet.Provider,
			"repository_ref":    detail.ChangeSet.RepositoryRef,
			"branch_ref":        detail.ChangeSet.BranchRef,
			"failure_detail":    failureMessage,
			"recoverable":       true,
		},
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := appendControlActionEventRefTx(tx, controlActionID, event.ID); err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("commit ChangeSet draft PR failure: %w", err)
	}

	updated, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	return agentRunPrepareResultFromDetail(updated, []Event{event}), nil
}

func agentRunPrepareResultFromDetail(detail AgentRunDetail, events []Event) AgentRunPrepareResult {
	result := AgentRunPrepareResult{
		AgentRun:   detail.AgentRun,
		CommitRefs: detail.CommitRefs,
		ChangeSet:  detail.ChangeSet,
		Events:     events,
	}
	if detail.Workspace != nil {
		result.Workspace = *detail.Workspace
		result.RunnerJob = RunnerJob{
			ID:         detail.Workspace.RunnerJobID,
			AgentRunID: detail.AgentRun.ID,
			Status:     detail.AgentRun.Status,
			CreatedAt:  detail.Workspace.CreatedAt,
			UpdatedAt:  detail.AgentRun.UpdatedAt,
		}
	}
	return result
}

func (store *Store) finishAgentCommand(agentRunID int64, status string, eventType string, completion workflow.AgentCommandCompletion) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.Workspace == nil {
		return AgentRunPrepareResult{}, fmt.Errorf("Workspace not prepared for AgentRun %d", agentRunID)
	}
	if detail.AgentRun.Status == "cancelled" {
		return agentRunPrepareResultFromDetail(detail, nil), nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("begin Agent command completion transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE agent_runs SET status = ?, updated_at = ? WHERE id = ?", status, now, agentRunID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark AgentRun %d %s: %w", agentRunID, status, err)
	}
	if _, err := tx.Exec("UPDATE runner_jobs SET status = ?, updated_at = ? WHERE id = ?", status, now, detail.Workspace.RunnerJobID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark RunnerJob %d %s: %w", detail.Workspace.RunnerJobID, status, err)
	}
	for _, segment := range completion.LogSegments {
		if _, err := tx.Exec(`
INSERT INTO log_segments (
	agent_run_id,
	stream,
	sequence,
	byte_start,
	byte_end,
	preview,
	artifact_path,
	truncated,
	created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			agentRunID,
			segment.Stream,
			segment.Sequence,
			segment.ByteStart,
			segment.ByteEnd,
			segment.Preview,
			segment.ArtifactPath,
			boolInt(segment.Truncated),
			now,
		); err != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("insert LogSegment for AgentRun %d: %w", agentRunID, err)
		}
	}
	var commitRefs []CommitRef
	var materializationEvents []Event
	for _, ref := range completion.CommitRefs {
		result, err := tx.Exec(`
INSERT INTO commit_refs (
	agent_run_id,
	repository_ref,
	sha,
	subject,
	author_name,
	author_email,
	created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			agentRunID,
			detail.WorkItem.RepositoryRef,
			ref.SHA,
			ref.Subject,
			ref.AuthorName,
			ref.AuthorEmail,
			now,
		)
		if err != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("insert CommitRef for AgentRun %d: %w", agentRunID, err)
		}
		refID, err := result.LastInsertId()
		if err != nil {
			return AgentRunPrepareResult{}, fmt.Errorf("read inserted CommitRef id: %w", err)
		}
		commitRefs = append(commitRefs, CommitRef{
			ID:            refID,
			AgentRunID:    agentRunID,
			RepositoryRef: detail.WorkItem.RepositoryRef,
			SHA:           ref.SHA,
			Subject:       ref.Subject,
			AuthorName:    ref.AuthorName,
			AuthorEmail:   ref.AuthorEmail,
			CreatedAt:     now,
		})
		materializationEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
			Type:           "repository_commit.materialized",
			OccurredAt:     now,
			ForgeProjectID: detail.WorkItem.ForgeProjectID,
			SubjectType:    "commit_ref",
			SubjectRef:     fmt.Sprintf("commit_ref:%d", refID),
			WorkItemID:     detail.WorkItem.ID,
			WorkItemRef:    detail.WorkItem.ProviderRef,
			AgentRunID:     agentRunID,
			Payload: map[string]any{
				"agent_run_id":   agentRunID,
				"runner_job_id":  detail.Workspace.RunnerJobID,
				"workspace_id":   detail.Workspace.ID,
				"commit_ref_id":  refID,
				"repository_ref": detail.WorkItem.RepositoryRef,
				"sha":            ref.SHA,
				"subject":        ref.Subject,
				"author_name":    ref.AuthorName,
				"author_email":   ref.AuthorEmail,
			},
		})
		if err != nil {
			return AgentRunPrepareResult{}, err
		}
		materializationEvents = append(materializationEvents, materializationEvent)
	}
	if completion.DeliverySkipped {
		reason := completion.DeliverySkipReason
		if reason == "" {
			reason = "no_repository_changes"
		}
		skippedEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
			Type:           "repository_delivery.skipped",
			OccurredAt:     now,
			ForgeProjectID: detail.WorkItem.ForgeProjectID,
			SubjectType:    "agent_run",
			SubjectRef:     fmt.Sprintf("agent_run:%d", agentRunID),
			WorkItemID:     detail.WorkItem.ID,
			WorkItemRef:    detail.WorkItem.ProviderRef,
			AgentRunID:     agentRunID,
			Payload: map[string]any{
				"agent_run_id":   agentRunID,
				"runner_job_id":  detail.Workspace.RunnerJobID,
				"workspace_id":   detail.Workspace.ID,
				"repository_ref": detail.WorkItem.RepositoryRef,
				"reason":         reason,
				"commit_refs":    len(completion.CommitRefs),
				"provider_ref":   detail.WorkItem.ProviderRef,
			},
		})
		if err != nil {
			return AgentRunPrepareResult{}, err
		}
		materializationEvents = append(materializationEvents, skippedEvent)
	}

	var changeSet *ChangeSet
	if completion.ChangeSet != nil && len(commitRefs) > 0 {
		var changeSetEvent Event
		changeSet, changeSetEvent, err = store.createOrClaimChangeSetTx(tx, detail, *completion.ChangeSet, commitRefs, now)
		if err != nil {
			return AgentRunPrepareResult{}, err
		}
		for i := range commitRefs {
			commitRefs[i].ChangeSetID = changeSet.ID
		}
		materializationEvents = append(materializationEvents, changeSetEvent)
	}

	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:           eventType,
		OccurredAt:     now,
		ForgeProjectID: detail.WorkItem.ForgeProjectID,
		SubjectType:    "agent_run",
		SubjectRef:     fmt.Sprintf("agent_run:%d", agentRunID),
		WorkItemID:     detail.WorkItem.ID,
		WorkItemRef:    detail.WorkItem.ProviderRef,
		AgentRunID:     agentRunID,
		Payload: map[string]any{
			"agent_run_id":   agentRunID,
			"runner_job_id":  detail.Workspace.RunnerJobID,
			"workspace_id":   detail.Workspace.ID,
			"status":         status,
			"exit_code":      completion.ExitCode,
			"success":        status == "completed",
			"duration_ms":    completion.Duration.Milliseconds(),
			"stdout_bytes":   completion.StdoutBytes,
			"stderr_bytes":   completion.StderrBytes,
			"log_segments":   len(completion.LogSegments),
			"failure_detail": completion.FailureDetail,
			"stdout_preview": "",
			"stderr_preview": "",
			"commit_refs":    len(completion.CommitRefs),
			"provider_ref":   detail.WorkItem.ProviderRef,
		},
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("commit Agent command completion: %w", err)
	}

	workspace := *detail.Workspace
	return AgentRunPrepareResult{
		AgentRun: AgentRun{
			ID:         detail.AgentRun.ID,
			WorkItemID: detail.AgentRun.WorkItemID,
			Status:     status,
			CreatedAt:  detail.AgentRun.CreatedAt,
			UpdatedAt:  now,
		},
		RunnerJob: RunnerJob{
			ID:         workspace.RunnerJobID,
			AgentRunID: agentRunID,
			Status:     status,
			CreatedAt:  workspace.CreatedAt,
			UpdatedAt:  now,
		},
		Workspace:  workspace,
		CommitRefs: commitRefs,
		ChangeSet:  changeSet,
		Events:     append(materializationEvents, event),
	}, nil
}

func (store *Store) createOrClaimChangeSetTx(tx *sql.Tx, detail AgentRunDetail, plan workflow.ChangeSetPlan, commitRefs []CommitRef, now string) (*ChangeSet, Event, error) {
	changeSet, found, err := scanActiveChangeSetTx(tx, plan.WorkItemID)
	if err != nil {
		return nil, Event{}, err
	}

	eventType := "change_set.claimed"
	if found {
		if err := validateChangeSetClaim(changeSet, plan); err != nil {
			return nil, Event{}, err
		}
		if _, err := tx.Exec(`
UPDATE change_sets
SET active_run_id = ?, updated_at = ?
WHERE id = ?`, plan.ActiveRunID, now, changeSet.ID); err != nil {
			return nil, Event{}, fmt.Errorf("claim ChangeSet %d for AgentRun %d: %w", changeSet.ID, plan.ActiveRunID, err)
		}
		changeSet.ActiveRunID = plan.ActiveRunID
		changeSet.UpdatedAt = now
	} else {
		if plan.Status == "" {
			plan.Status = "planned"
		}
		result, err := tx.Exec(`
INSERT INTO change_sets (
	work_item_id,
	work_item_ref,
	provider,
	repository_ref,
	base_branch,
	branch_ref,
	status,
	created_by_run_id,
	active_run_id,
	created_at,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			plan.WorkItemID,
			plan.WorkItemRef,
			plan.Provider,
			plan.RepositoryRef,
			plan.BaseBranch,
			plan.BranchRef,
			plan.Status,
			plan.CreatedByRunID,
			plan.ActiveRunID,
			now,
			now,
		)
		if err != nil {
			return nil, Event{}, fmt.Errorf("insert ChangeSet for WorkItem %d: %w", plan.WorkItemID, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, Event{}, fmt.Errorf("read inserted ChangeSet id: %w", err)
		}
		changeSet = ChangeSet{
			ID:             id,
			WorkItemID:     plan.WorkItemID,
			WorkItemRef:    plan.WorkItemRef,
			Provider:       plan.Provider,
			RepositoryRef:  plan.RepositoryRef,
			BaseBranch:     plan.BaseBranch,
			BranchRef:      plan.BranchRef,
			Status:         plan.Status,
			CreatedByRunID: plan.CreatedByRunID,
			ActiveRunID:    plan.ActiveRunID,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		eventType = "change_set.created"
	}

	for _, ref := range commitRefs {
		if _, err := tx.Exec(`
UPDATE commit_refs
SET change_set_id = ?
WHERE id = ?`, changeSet.ID, ref.ID); err != nil {
			return nil, Event{}, fmt.Errorf("link CommitRef %d to ChangeSet %d: %w", ref.ID, changeSet.ID, err)
		}
	}
	linkedRefs, err := listCommitRefsForChangeSetTx(tx, changeSet.ID)
	if err != nil {
		return nil, Event{}, err
	}
	changeSet.CommitRefs = linkedRefs

	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:           eventType,
		OccurredAt:     now,
		ForgeProjectID: detail.WorkItem.ForgeProjectID,
		SubjectType:    "change_set",
		SubjectRef:     fmt.Sprintf("change_set:%d", changeSet.ID),
		WorkItemID:     detail.WorkItem.ID,
		WorkItemRef:    detail.WorkItem.ProviderRef,
		AgentRunID:     detail.AgentRun.ID,
		ChangeSetID:    changeSet.ID,
		Payload: map[string]any{
			"change_set_id":     changeSet.ID,
			"work_item_id":      changeSet.WorkItemID,
			"work_item_ref":     changeSet.WorkItemRef,
			"provider":          changeSet.Provider,
			"repository_ref":    changeSet.RepositoryRef,
			"base_branch":       changeSet.BaseBranch,
			"branch_ref":        changeSet.BranchRef,
			"status":            changeSet.Status,
			"created_by_run_id": changeSet.CreatedByRunID,
			"active_run_id":     changeSet.ActiveRunID,
			"commit_refs":       len(linkedRefs),
		},
	})
	if err != nil {
		return nil, Event{}, err
	}
	return &changeSet, event, nil
}

func validateChangeSetClaim(changeSet ChangeSet, plan workflow.ChangeSetPlan) error {
	if changeSet.Provider != plan.Provider ||
		changeSet.RepositoryRef != plan.RepositoryRef ||
		changeSet.BaseBranch != plan.BaseBranch ||
		changeSet.BranchRef != plan.BranchRef {
		return fmt.Errorf(
			"ChangeSet claim for AgentRun %d does not match active ChangeSet %d: got provider=%q repository_ref=%q base_branch=%q branch_ref=%q; active provider=%q repository_ref=%q base_branch=%q branch_ref=%q",
			plan.ActiveRunID,
			changeSet.ID,
			plan.Provider,
			plan.RepositoryRef,
			plan.BaseBranch,
			plan.BranchRef,
			changeSet.Provider,
			changeSet.RepositoryRef,
			changeSet.BaseBranch,
			changeSet.BranchRef,
		)
	}
	return nil
}

func listCommitRefsForChangeSetTx(tx *sql.Tx, changeSetID int64) ([]CommitRef, error) {
	rows, err := tx.Query(`
SELECT id, agent_run_id, COALESCE(change_set_id, 0), repository_ref, sha, subject, author_name, author_email, created_at
FROM commit_refs
WHERE change_set_id = ?
ORDER BY id ASC`, changeSetID)
	if err != nil {
		return nil, fmt.Errorf("query CommitRefs for ChangeSet %d: %w", changeSetID, err)
	}
	defer rows.Close()

	var refs []CommitRef
	for rows.Next() {
		var ref CommitRef
		if err := rows.Scan(
			&ref.ID,
			&ref.AgentRunID,
			&ref.ChangeSetID,
			&ref.RepositoryRef,
			&ref.SHA,
			&ref.Subject,
			&ref.AuthorName,
			&ref.AuthorEmail,
			&ref.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan CommitRef for ChangeSet %d: %w", changeSetID, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CommitRefs for ChangeSet %d: %w", changeSetID, err)
	}
	return refs, nil
}

func scanActiveChangeSetTx(tx *sql.Tx, workItemID int64) (ChangeSet, bool, error) {
	var changeSet ChangeSet
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
WHERE work_item_id = ?
	AND status NOT IN ('merged', 'closed', 'abandoned')
ORDER BY id DESC
LIMIT 1`, workItemID).Scan(
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
		return ChangeSet{}, false, nil
	}
	if err != nil {
		return ChangeSet{}, false, fmt.Errorf("query active ChangeSet for WorkItem %d: %w", workItemID, err)
	}
	return changeSet, true, nil
}

func scanActiveChangeSetForAgentRunTx(tx *sql.Tx, agentRunID int64) (ChangeSet, bool, error) {
	var changeSet ChangeSet
	err := tx.QueryRow(`
SELECT
	c.id,
	c.work_item_id,
	c.work_item_ref,
	c.provider,
	c.repository_ref,
	c.base_branch,
	c.branch_ref,
	c.branch_provider_ref,
	c.change_ref,
	c.change_draft,
	c.provider_snapshot,
	c.status,
	c.created_by_run_id,
	c.active_run_id,
	c.created_at,
	c.updated_at
FROM change_sets c
WHERE c.status NOT IN ('merged', 'closed', 'abandoned')
	AND (
		c.created_by_run_id = ?
		OR c.active_run_id = ?
		OR EXISTS (
			SELECT 1
			FROM commit_refs r
			WHERE r.agent_run_id = ?
				AND r.change_set_id = c.id
		)
	)
ORDER BY CASE
	WHEN c.active_run_id = ? THEN 0
	WHEN c.created_by_run_id = ? THEN 1
	ELSE 2
END, c.id DESC
LIMIT 1`, agentRunID, agentRunID, agentRunID, agentRunID, agentRunID).Scan(
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
		return ChangeSet{}, false, nil
	}
	if err != nil {
		return ChangeSet{}, false, fmt.Errorf("query active ChangeSet for AgentRun %d: %w", agentRunID, err)
	}
	return changeSet, true, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// AllocateWorkspace creates runner preparation state and records the Workspace lease.
func (store *Store) AllocateWorkspace(agentRunID int64, paths WorkspacePaths) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.AgentRun.Status != "planned" {
		return AgentRunPrepareResult{}, fmt.Errorf("AgentRun %d is %s; expected planned", agentRunID, detail.AgentRun.Status)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("begin Workspace allocation transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE agent_runs SET status = ?, updated_at = ? WHERE id = ?", "preparing", now, agentRunID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark AgentRun %d preparing: %w", agentRunID, err)
	}

	jobResult, err := tx.Exec(`
INSERT INTO runner_jobs (agent_run_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?)`,
		agentRunID,
		"preparing",
		now,
		now,
	)
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("insert RunnerJob for AgentRun %d: %w", agentRunID, err)
	}
	jobID, err := jobResult.LastInsertId()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("read inserted RunnerJob id: %w", err)
	}

	workspaceResult, err := tx.Exec(`
INSERT INTO workspaces (
	agent_run_id,
	runner_job_id,
	status,
	root_path,
	repo_path,
	logs_path,
	artifacts_path,
	tmp_path,
	created_at,
	updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		agentRunID,
		jobID,
		"allocated",
		paths.Root,
		paths.Repo,
		paths.Logs,
		paths.Artifacts,
		paths.Tmp,
		now,
		now,
	)
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("insert Workspace for AgentRun %d: %w", agentRunID, err)
	}
	workspaceID, err := workspaceResult.LastInsertId()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("read inserted Workspace id: %w", err)
	}

	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:           "workspace.allocated",
		OccurredAt:     now,
		ForgeProjectID: detail.WorkItem.ForgeProjectID,
		SubjectType:    "workspace",
		SubjectRef:     fmt.Sprintf("workspace:%d", workspaceID),
		WorkItemID:     detail.WorkItem.ID,
		WorkItemRef:    detail.WorkItem.ProviderRef,
		AgentRunID:     agentRunID,
		Payload: map[string]any{
			"agent_run_id":   agentRunID,
			"runner_job_id":  jobID,
			"workspace_id":   workspaceID,
			"root_path":      paths.Root,
			"repo_path":      paths.Repo,
			"logs_path":      paths.Logs,
			"artifacts_path": paths.Artifacts,
			"tmp_path":       paths.Tmp,
			"status":         "allocated",
		},
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("commit Workspace allocation: %w", err)
	}

	return AgentRunPrepareResult{
		AgentRun: AgentRun{
			ID:         detail.AgentRun.ID,
			WorkItemID: detail.AgentRun.WorkItemID,
			Status:     "preparing",
			CreatedAt:  detail.AgentRun.CreatedAt,
			UpdatedAt:  now,
		},
		RunnerJob: RunnerJob{
			ID:         jobID,
			AgentRunID: agentRunID,
			Status:     "preparing",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		Workspace: Workspace{
			ID:          workspaceID,
			AgentRunID:  agentRunID,
			RunnerJobID: jobID,
			Status:      "allocated",
			Paths:       paths,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Events: []Event{event},
	}, nil
}

// MarkWorkspaceReady records successful repository preparation.
func (store *Store) MarkWorkspaceReady(agentRunID int64) (AgentRunPrepareResult, error) {
	return store.finishWorkspacePreparation(agentRunID, "ready", "preparing", "")
}

// MarkWorkspaceFailed records a retained failed Workspace for debugging.
func (store *Store) MarkWorkspaceFailed(agentRunID int64, failureMessage string) (AgentRunPrepareResult, error) {
	return store.finishWorkspacePreparation(agentRunID, "failed", "failed", failureMessage)
}

func (store *Store) finishWorkspacePreparation(agentRunID int64, workspaceStatus string, agentRunStatus string, failureMessage string) (AgentRunPrepareResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return AgentRunPrepareResult{}, err
	}
	if detail.Workspace == nil {
		return AgentRunPrepareResult{}, fmt.Errorf("Workspace not allocated for AgentRun %d", agentRunID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("begin Workspace preparation transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE agent_runs SET status = ?, updated_at = ? WHERE id = ?", agentRunStatus, now, agentRunID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark AgentRun %d %s: %w", agentRunID, agentRunStatus, err)
	}
	jobStatus := workspaceStatus
	if _, err := tx.Exec("UPDATE runner_jobs SET status = ?, updated_at = ? WHERE id = ?", jobStatus, now, detail.Workspace.RunnerJobID); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark RunnerJob %d %s: %w", detail.Workspace.RunnerJobID, jobStatus, err)
	}
	if _, err := tx.Exec(`
UPDATE workspaces
SET status = ?, failure_message = ?, updated_at = ?
WHERE id = ?`,
		workspaceStatus,
		failureMessage,
		now,
		detail.Workspace.ID,
	); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("mark Workspace %d %s: %w", detail.Workspace.ID, workspaceStatus, err)
	}

	eventType := "workspace.prepared"
	if workspaceStatus == "failed" {
		eventType = "workspace.prepare_failed"
	}
	event, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:           eventType,
		OccurredAt:     now,
		ForgeProjectID: detail.WorkItem.ForgeProjectID,
		SubjectType:    "workspace",
		SubjectRef:     fmt.Sprintf("workspace:%d", detail.Workspace.ID),
		WorkItemID:     detail.WorkItem.ID,
		WorkItemRef:    detail.WorkItem.ProviderRef,
		AgentRunID:     agentRunID,
		Payload: map[string]any{
			"agent_run_id":     agentRunID,
			"runner_job_id":    detail.Workspace.RunnerJobID,
			"workspace_id":     detail.Workspace.ID,
			"workspace_status": workspaceStatus,
			"agent_run_status": agentRunStatus,
			"failure_message":  failureMessage,
		},
	})
	if err != nil {
		return AgentRunPrepareResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return AgentRunPrepareResult{}, fmt.Errorf("commit Workspace preparation: %w", err)
	}

	workspace := *detail.Workspace
	workspace.Status = workspaceStatus
	workspace.FailureMessage = failureMessage
	workspace.UpdatedAt = now
	return AgentRunPrepareResult{
		AgentRun: AgentRun{
			ID:         detail.AgentRun.ID,
			WorkItemID: detail.AgentRun.WorkItemID,
			Status:     agentRunStatus,
			CreatedAt:  detail.AgentRun.CreatedAt,
			UpdatedAt:  now,
		},
		RunnerJob: RunnerJob{
			ID:         workspace.RunnerJobID,
			AgentRunID: agentRunID,
			Status:     jobStatus,
			CreatedAt:  workspace.CreatedAt,
			UpdatedAt:  now,
		},
		Workspace: workspace,
		Events:    []Event{event},
	}, nil
}

// CreatePlannedAgentRun writes one planned AgentRun, its immutable RunSpec, and matching Events.
func (store *Store) CreatePlannedAgentRun(plan workflow.PlannedAgentRunPlan) (workflow.AgentRunCreateResult, error) {
	return store.createAgentRun(plan, agentRunCreateOptions{})
}

// CreateRetryAgentRun writes a retry ControlAction, fresh AgentRun, fresh RunSpec, and optional active ChangeSet target in one transaction.
func (store *Store) CreateRetryAgentRun(priorAgentRunID int64, plan workflow.PlannedAgentRunPlan) (workflow.AgentRunCreateResult, error) {
	return store.createAgentRun(plan, agentRunCreateOptions{
		priorAgentRunID:               priorAgentRunID,
		targetPriorRunActiveChangeSet: true,
	})
}

type agentRunCreateOptions struct {
	priorAgentRunID               int64
	targetPriorRunActiveChangeSet bool
}

func (store *Store) createAgentRun(plan workflow.PlannedAgentRunPlan, options agentRunCreateOptions) (workflow.AgentRunCreateResult, error) {
	workItem := plan.WorkItem
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := store.db.Begin()
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("begin AgentRun create transaction: %w", err)
	}
	defer tx.Rollback()

	actionInput, err := json.Marshal(plan.ControlAction.Input)
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("encode ControlAction input: %w", err)
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
		plan.ControlAction.Type,
		plan.ControlAction.TargetType,
		plan.ControlAction.TargetRef,
		plan.ControlAction.RequestedBy,
		plan.ControlAction.Reason,
		string(actionInput),
		plan.ControlAction.Status,
		now,
		now,
		"[]",
	)
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("insert ControlAction: %w", err)
	}
	controlActionID, err := actionResult.LastInsertId()
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("read inserted ControlAction id: %w", err)
	}

	runResult, err := tx.Exec(`
INSERT INTO agent_runs (work_item_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?)`,
		workItem.ID,
		plan.Status,
		now,
		now,
	)
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("insert AgentRun: %w", err)
	}
	runID, err := runResult.LastInsertId()
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("read inserted AgentRun id: %w", err)
	}

	specJSON, err := plan.EncodeRunSpec(runID)
	if err != nil {
		return workflow.AgentRunCreateResult{}, err
	}

	specResult, err := tx.Exec(`
INSERT INTO run_specs (agent_run_id, spec_json, created_at)
VALUES (?, ?, ?)`,
		runID,
		specJSON,
		now,
	)
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("insert RunSpec for AgentRun %d: %w", runID, err)
	}
	specID, err := specResult.LastInsertId()
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("read inserted RunSpec id: %w", err)
	}

	var events []workflow.Event
	for _, eventPlan := range plan.EventPlans(workflow.PlannedAgentRunIDs{
		ControlActionID: controlActionID,
		AgentRunID:      runID,
		RunSpecID:       specID,
	}) {
		event, err := appendAgentRunEventTx(tx, agentRunEventInput{
			Type:            eventPlan.Type,
			OccurredAt:      now,
			ForgeProjectID:  workItem.ForgeProjectID,
			SubjectType:     eventPlan.SubjectType,
			SubjectRef:      eventPlan.SubjectRef,
			WorkItemID:      workItem.ID,
			WorkItemRef:     workItem.ProviderRef,
			AgentRunID:      runID,
			ControlActionID: controlActionID,
			Payload:         eventPlan.Payload,
		})
		if err != nil {
			return workflow.AgentRunCreateResult{}, err
		}
		events = append(events, event)
	}

	var changeSet *workflow.ChangeSet
	if options.targetPriorRunActiveChangeSet {
		activeChangeSet, found, err := scanActiveChangeSetForAgentRunTx(tx, options.priorAgentRunID)
		if err != nil {
			return workflow.AgentRunCreateResult{}, err
		}
		if found {
			if _, err := tx.Exec("UPDATE change_sets SET active_run_id = ?, updated_at = ? WHERE id = ?", runID, now, activeChangeSet.ID); err != nil {
				return workflow.AgentRunCreateResult{}, fmt.Errorf("target ChangeSet %d for AgentRun %d: %w", activeChangeSet.ID, runID, err)
			}
			activeChangeSet.ActiveRunID = runID
			activeChangeSet.UpdatedAt = now
			changeSet = &activeChangeSet
			event, err := appendAgentRunEventTx(tx, agentRunEventInput{
				Type:            "change_set.retry_targeted",
				OccurredAt:      now,
				ForgeProjectID:  workItem.ForgeProjectID,
				SubjectType:     "change_set",
				SubjectRef:      fmt.Sprintf("change_set:%d", activeChangeSet.ID),
				WorkItemID:      workItem.ID,
				WorkItemRef:     workItem.ProviderRef,
				AgentRunID:      runID,
				ControlActionID: controlActionID,
				ChangeSetID:     activeChangeSet.ID,
				Payload: map[string]any{
					"change_set_id":      activeChangeSet.ID,
					"prior_agent_run_id": options.priorAgentRunID,
					"agent_run_id":       runID,
					"work_item_ref":      workItem.ProviderRef,
					"branch_ref":         activeChangeSet.BranchRef,
					"change_ref":         activeChangeSet.ChangeRef,
				},
			})
			if err != nil {
				return workflow.AgentRunCreateResult{}, err
			}
			events = append(events, event)
		}
	}

	resultEventIDs := make([]int64, 0, len(events))
	for _, event := range events {
		resultEventIDs = append(resultEventIDs, event.ID)
	}
	resultEventRefs, err := json.Marshal(resultEventIDs)
	if err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("encode ControlAction result Event refs: %w", err)
	}
	if _, err := tx.Exec("UPDATE control_actions SET result_event_refs = ? WHERE id = ?", string(resultEventRefs), controlActionID); err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("update ControlAction result Event refs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return workflow.AgentRunCreateResult{}, fmt.Errorf("commit AgentRun create: %w", err)
	}

	return workflow.AgentRunCreateResult{
		ControlAction: workflow.ControlAction{
			ID:     controlActionID,
			Type:   plan.ControlAction.Type,
			Status: plan.ControlAction.Status,
		},
		AgentRun: workflow.AgentRun{
			ID:         runID,
			WorkItemID: workItem.ID,
			Status:     plan.Status,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		RunSpec: workflow.RunSpec{
			ID:         specID,
			AgentRunID: runID,
			SpecJSON:   specJSON,
			CreatedAt:  now,
		},
		Branch:    plan.Branch,
		ChangeSet: changeSet,
		Events:    events,
	}, nil
}

type agentRunEventInput struct {
	Type            string
	OccurredAt      string
	ForgeProjectID  int64
	SubjectType     string
	SubjectRef      string
	WorkItemID      int64
	WorkItemRef     string
	AgentRunID      int64
	ControlActionID int64
	ChangeSetID     int64
	Payload         map[string]any
}

func appendAgentRunEventTx(tx *sql.Tx, input agentRunEventInput) (Event, error) {
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s event payload: %w", input.Type, err)
	}
	var controlActionID any
	if input.ControlActionID != 0 {
		controlActionID = input.ControlActionID
	}
	var changeSetID any
	if input.ChangeSetID != 0 {
		changeSetID = input.ChangeSetID
	}

	result, err := tx.Exec(`
INSERT INTO events (
	type,
	occurred_at,
	actor,
	forge_project_id,
	subject_type,
	subject_ref,
	work_item_id,
	work_item_ref,
	agent_run_id,
	control_action_id,
	change_set_id,
	provider_ref,
	payload
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Type,
		input.OccurredAt,
		"forgelane",
		input.ForgeProjectID,
		input.SubjectType,
		input.SubjectRef,
		input.WorkItemID,
		input.WorkItemRef,
		input.AgentRunID,
		controlActionID,
		changeSetID,
		input.WorkItemRef,
		string(payload),
	)
	if err != nil {
		return Event{}, fmt.Errorf("append %s event: %w", input.Type, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Event{}, fmt.Errorf("read inserted %s Event id: %w", input.Type, err)
	}
	return Event{ID: id, Type: input.Type}, nil
}

func appendControlActionEventRefTx(tx *sql.Tx, controlActionID int64, eventID int64) error {
	var refsJSON string
	if err := tx.QueryRow("SELECT result_event_refs FROM control_actions WHERE id = ?", controlActionID).Scan(&refsJSON); err != nil {
		return fmt.Errorf("read ControlAction %d result Event refs: %w", controlActionID, err)
	}
	var refs []int64
	if refsJSON != "" {
		if err := json.Unmarshal([]byte(refsJSON), &refs); err != nil {
			return fmt.Errorf("decode ControlAction %d result Event refs: %w", controlActionID, err)
		}
	}
	refs = append(refs, eventID)
	updatedRefs, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("encode ControlAction %d result Event refs: %w", controlActionID, err)
	}
	if _, err := tx.Exec("UPDATE control_actions SET result_event_refs = ? WHERE id = ?", string(updatedRefs), controlActionID); err != nil {
		return fmt.Errorf("update ControlAction %d result Event refs: %w", controlActionID, err)
	}
	return nil
}

// RequestAgentRunStop records a human stop ControlAction and marks the AgentRun for cancellation.
func (store *Store) RequestAgentRunStop(agentRunID int64, plan workflow.ControlActionPlan) (workflow.AgentRunControlResult, error) {
	detail, err := store.GetAgentRunDetail(agentRunID)
	if err != nil {
		return workflow.AgentRunControlResult{}, err
	}
	if detail.AgentRun.Status != "running" {
		return workflow.AgentRunControlResult{}, fmt.Errorf("AgentRun %d is %s; expected running", agentRunID, detail.AgentRun.Status)
	}

	runnerJob, err := store.runnerJobForAgentRun(agentRunID)
	if err != nil {
		return workflow.AgentRunControlResult{}, err
	}
	if runnerJob.Status != "running" {
		return workflow.AgentRunControlResult{}, fmt.Errorf("RunnerJob %d for AgentRun %d is %s; expected running", runnerJob.ID, agentRunID, runnerJob.Status)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := store.db.Begin()
	if err != nil {
		return workflow.AgentRunControlResult{}, fmt.Errorf("begin AgentRun stop transaction: %w", err)
	}
	defer tx.Rollback()

	actionInput, err := json.Marshal(plan.Input)
	if err != nil {
		return workflow.AgentRunControlResult{}, fmt.Errorf("encode stop ControlAction input: %w", err)
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
		return workflow.AgentRunControlResult{}, fmt.Errorf("insert stop ControlAction: %w", err)
	}
	controlActionID, err := actionResult.LastInsertId()
	if err != nil {
		return workflow.AgentRunControlResult{}, fmt.Errorf("read inserted stop ControlAction id: %w", err)
	}

	if _, err := tx.Exec("UPDATE agent_runs SET status = ?, updated_at = ? WHERE id = ?", "cancel_requested", now, agentRunID); err != nil {
		return workflow.AgentRunControlResult{}, fmt.Errorf("mark AgentRun %d cancel_requested: %w", agentRunID, err)
	}

	actionEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "control_action.succeeded",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "control_action",
		SubjectRef:      fmt.Sprintf("control_action:%d", controlActionID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"type":              plan.Type,
			"status":            plan.Status,
			"agent_run_id":      agentRunID,
			"previous_status":   detail.AgentRun.Status,
		},
	})
	if err != nil {
		return workflow.AgentRunControlResult{}, err
	}
	if err := appendControlActionEventRefTx(tx, controlActionID, actionEvent.ID); err != nil {
		return workflow.AgentRunControlResult{}, err
	}

	cancelEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "agent_run.cancel_requested",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "agent_run",
		SubjectRef:      fmt.Sprintf("agent_run:%d", agentRunID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		Payload: map[string]any{
			"agent_run_id":      agentRunID,
			"previous_status":   detail.AgentRun.Status,
			"status":            "cancel_requested",
			"control_action_id": controlActionID,
		},
	})
	if err != nil {
		return workflow.AgentRunControlResult{}, err
	}
	if err := appendControlActionEventRefTx(tx, controlActionID, cancelEvent.ID); err != nil {
		return workflow.AgentRunControlResult{}, err
	}

	if _, err := tx.Exec("UPDATE agent_runs SET status = ?, updated_at = ? WHERE id = ?", "cancelled", now, agentRunID); err != nil {
		return workflow.AgentRunControlResult{}, fmt.Errorf("mark AgentRun %d cancelled: %w", agentRunID, err)
	}
	if _, err := tx.Exec("UPDATE runner_jobs SET status = ?, updated_at = ? WHERE id = ?", "cancelled", now, runnerJob.ID); err != nil {
		return workflow.AgentRunControlResult{}, fmt.Errorf("mark RunnerJob %d cancelled: %w", runnerJob.ID, err)
	}
	cancelledEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "agent_run.cancelled",
		OccurredAt:      now,
		ForgeProjectID:  detail.WorkItem.ForgeProjectID,
		SubjectType:     "agent_run",
		SubjectRef:      fmt.Sprintf("agent_run:%d", agentRunID),
		WorkItemID:      detail.WorkItem.ID,
		WorkItemRef:     detail.WorkItem.ProviderRef,
		AgentRunID:      agentRunID,
		ControlActionID: controlActionID,
		Payload: map[string]any{
			"agent_run_id":      agentRunID,
			"runner_job_id":     runnerJob.ID,
			"previous_status":   "cancel_requested",
			"status":            "cancelled",
			"control_action_id": controlActionID,
		},
	})
	if err != nil {
		return workflow.AgentRunControlResult{}, err
	}
	if err := appendControlActionEventRefTx(tx, controlActionID, cancelledEvent.ID); err != nil {
		return workflow.AgentRunControlResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return workflow.AgentRunControlResult{}, fmt.Errorf("commit AgentRun stop request: %w", err)
	}

	var workspace *workflow.Workspace
	if detail.Workspace != nil {
		workspace = detail.Workspace
	}
	return workflow.AgentRunControlResult{
		ControlAction: workflow.ControlAction{
			ID:     controlActionID,
			Type:   plan.Type,
			Status: plan.Status,
		},
		AgentRun: workflow.AgentRun{
			ID:         detail.AgentRun.ID,
			WorkItemID: detail.AgentRun.WorkItemID,
			Status:     "cancelled",
			CreatedAt:  detail.AgentRun.CreatedAt,
			UpdatedAt:  now,
		},
		RunnerJob: workflow.RunnerJob{
			ID:         runnerJob.ID,
			AgentRunID: runnerJob.AgentRunID,
			Status:     "cancelled",
			CreatedAt:  runnerJob.CreatedAt,
			UpdatedAt:  now,
		},
		Workspace: workspace,
		Events:    []workflow.Event{actionEvent, cancelEvent, cancelledEvent},
	}, nil
}

func isTerminalAgentRunStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "timed_out":
		return true
	default:
		return false
	}
}

func (store *Store) runnerJobForAgentRun(agentRunID int64) (workflow.RunnerJob, error) {
	var job workflow.RunnerJob
	err := store.db.QueryRow(`
SELECT id, agent_run_id, status, created_at, updated_at
FROM runner_jobs
WHERE agent_run_id = ?`, agentRunID).Scan(
		&job.ID,
		&job.AgentRunID,
		&job.Status,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err != nil {
		return workflow.RunnerJob{}, err
	}
	return job, nil
}

// GetForgeProjectByRef returns a persisted ForgeProject by canonical ref.
func (store *Store) GetForgeProjectByRef(providerRef string) (ForgeProject, error) {
	var forgeProject ForgeProject
	err := store.db.QueryRow(`
SELECT id, provider, provider_host, repository_path, provider_ref
FROM forge_projects
WHERE provider_ref = ? AND initialized_at IS NOT NULL`, providerRef).Scan(
		&forgeProject.ID,
		&forgeProject.Provider,
		&forgeProject.ProviderHost,
		&forgeProject.RepositoryPath,
		&forgeProject.ProviderRef,
	)
	if err == sql.ErrNoRows {
		return ForgeProject{}, fmt.Errorf("ForgeProject not initialized for %s; run forgelane init or pass a full ProviderRef", providerRef)
	}
	if err != nil {
		return ForgeProject{}, fmt.Errorf("query ForgeProject %s: %w", providerRef, err)
	}
	return forgeProject, nil
}

// UpsertForgeProject persists instance config idempotently.
// AgentRun and ControlAction state changes get audited by later workflow slices.
func (store *Store) UpsertForgeProject(forgeProject ForgeProject) error {
	_, err := upsertForgeProject(store.db, forgeProject)
	return err
}

func upsertForgeProject(db *sql.DB, forgeProject ForgeProject) (int64, error) {
	return upsertForgeProjectExecutor(db, forgeProject)
}

func upsertForgeProjectTx(tx *sql.Tx, forgeProject ForgeProject) (int64, error) {
	return upsertForgeProjectExecutor(tx, forgeProject)
}

type forgeProjectExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

func upsertForgeProjectExecutor(executor forgeProjectExecutor, forgeProject ForgeProject) (int64, error) {
	var initializedAt any
	if forgeProject.Initialized {
		initializedAt = time.Now().UTC().Format(time.RFC3339)
	}
	const statement = `
INSERT INTO forge_projects (provider, provider_host, repository_path, provider_ref, initialized_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(provider_ref) DO UPDATE SET
	provider = excluded.provider,
	provider_host = excluded.provider_host,
	repository_path = excluded.repository_path,
	initialized_at = COALESCE(excluded.initialized_at, forge_projects.initialized_at),
	updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');`

	_, err := executor.Exec(
		statement,
		forgeProject.Provider,
		forgeProject.ProviderHost,
		forgeProject.RepositoryPath,
		forgeProject.ProviderRef,
		initializedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("persist ForgeProject %s: %w", forgeProject.ProviderRef, err)
	}

	var id int64
	if err := executor.QueryRow(
		"SELECT id FROM forge_projects WHERE provider_ref = ?",
		forgeProject.ProviderRef,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("lookup ForgeProject %s: %w", forgeProject.ProviderRef, err)
	}
	return id, nil
}
