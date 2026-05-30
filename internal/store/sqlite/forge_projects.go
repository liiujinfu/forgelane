package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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
CREATE INDEX IF NOT EXISTS idx_events_agent_run_id ON events(agent_run_id);`

	if _, err := store.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize ForgeLane database schema: %w", err)
	}
	if err := store.ensureColumn("forge_projects", "initialized_at", "TEXT"); err != nil {
		return err
	}
	if err := store.ensureColumn("events", "control_action_id", "INTEGER REFERENCES control_actions(id)"); err != nil {
		return err
	}
	if _, err := store.db.Exec("CREATE INDEX IF NOT EXISTS idx_events_control_action_id ON events(control_action_id)"); err != nil {
		return fmt.Errorf("initialize ControlAction event index: %w", err)
	}
	return nil
}

func (store *Store) ensureColumn(table string, column string, definition string) error {
	rows, err := store.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return fmt.Errorf("inspect %s schema: %w", table, err)
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
			return fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s schema: %w", table, err)
	}
	if _, err := store.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition); err != nil {
		return fmt.Errorf("add %s.%s column: %w", table, column, err)
	}
	return nil
}

// WorkItem is a persisted WorkItem snapshot.
type WorkItem struct {
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

// Event is a persisted audit event.
type Event struct {
	ID          int64
	Type        string
	OccurredAt  string
	Actor       string
	SubjectType string
	SubjectRef  string
}

// WorkItemImportResult is the outcome of an atomic WorkItem import.
type WorkItemImportResult struct {
	WorkItem WorkItem
	Event    Event
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

// AgentRunDetail is the read model for inspecting one AgentRun.
type AgentRunDetail struct {
	AgentRun AgentRun
	WorkItem WorkItem
	RunSpec  RunSpec
}

// ControlAction is a persisted operator request to change the delivery loop.
type ControlAction struct {
	ID     int64
	Type   string
	Status string
}

// ImportWorkItem persists a provider-owned issue snapshot and matching audit Event.
func (store *Store) ImportWorkItem(issue workitems.ProviderIssue) (WorkItemImportResult, error) {
	ref, err := workitems.ParseProviderRef(issue.ProviderRef)
	if err != nil {
		return WorkItemImportResult{}, err
	}
	issue = issue.Normalize(ref)

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

	eventType := "work_item.refreshed"
	var workItemID int64
	switch {
	case err == sql.ErrNoRows:
		eventType = "work_item.imported"
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

	payload, err := json.Marshal(map[string]any{
		"provider_ref":        issue.ProviderRef,
		"repository_ref":      issue.RepositoryRef,
		"provider_updated_at": providerUpdatedAt,
		"work_item_id":        workItemID,
		"forge_project_id":    forgeProjectID,
	})
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
		eventType,
		now,
		"forgelane",
		forgeProjectID,
		"work_item",
		issue.ProviderRef,
		workItemID,
		issue.ProviderRef,
		issue.ProviderRef,
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
			Type: eventType,
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
	return detail, nil
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

// CreateAgentRun creates one planned AgentRun and its immutable RunSpec.
func (store *Store) CreateAgentRun(workItem WorkItem) (AgentRunCreateResult, error) {
	ref, err := workitems.ParseProviderRef(workItem.ProviderRef)
	if err != nil {
		return AgentRunCreateResult{}, err
	}

	branch := fmt.Sprintf("forgelane/issue-%d", workItem.ProviderIssueNumber)
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := store.db.Begin()
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("begin AgentRun create transaction: %w", err)
	}
	defer tx.Rollback()

	actionInput, err := json.Marshal(map[string]any{
		"provider_ref": workItem.ProviderRef,
	})
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("encode ControlAction input: %w", err)
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
		"start",
		"work_item",
		workItem.ProviderRef,
		"local",
		"forgelane runs create",
		string(actionInput),
		"succeeded",
		now,
		now,
		"[]",
	)
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("insert ControlAction: %w", err)
	}
	controlActionID, err := actionResult.LastInsertId()
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("read inserted ControlAction id: %w", err)
	}

	runResult, err := tx.Exec(`
INSERT INTO agent_runs (work_item_id, status, created_at, updated_at)
VALUES (?, ?, ?, ?)`,
		workItem.ID,
		"planned",
		now,
		now,
	)
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("insert AgentRun: %w", err)
	}
	runID, err := runResult.LastInsertId()
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("read inserted AgentRun id: %w", err)
	}

	specJSON, err := encodeRunSpec(runID, workItem, ref, branch)
	if err != nil {
		return AgentRunCreateResult{}, err
	}

	specResult, err := tx.Exec(`
INSERT INTO run_specs (agent_run_id, spec_json, created_at)
VALUES (?, ?, ?)`,
		runID,
		specJSON,
		now,
	)
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("insert RunSpec for AgentRun %d: %w", runID, err)
	}
	specID, err := specResult.LastInsertId()
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("read inserted RunSpec id: %w", err)
	}

	controlActionEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "control_action.succeeded",
		OccurredAt:      now,
		ForgeProjectID:  workItem.ForgeProjectID,
		SubjectType:     "control_action",
		SubjectRef:      fmt.Sprintf("control_action:%d", controlActionID),
		WorkItemID:      workItem.ID,
		WorkItemRef:     workItem.ProviderRef,
		AgentRunID:      runID,
		ControlActionID: controlActionID,
		Payload: map[string]any{
			"control_action_id": controlActionID,
			"type":              "start",
			"status":            "succeeded",
			"agent_run_id":      runID,
			"work_item_id":      workItem.ID,
			"provider_ref":      workItem.ProviderRef,
		},
	})
	if err != nil {
		return AgentRunCreateResult{}, err
	}

	runEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "agent_run.created",
		OccurredAt:      now,
		ForgeProjectID:  workItem.ForgeProjectID,
		SubjectType:     "agent_run",
		SubjectRef:      fmt.Sprintf("agent_run:%d", runID),
		WorkItemID:      workItem.ID,
		WorkItemRef:     workItem.ProviderRef,
		AgentRunID:      runID,
		ControlActionID: controlActionID,
		Payload: map[string]any{
			"agent_run_id": runID,
			"work_item_id": workItem.ID,
			"provider_ref": workItem.ProviderRef,
			"status":       "planned",
		},
	})
	if err != nil {
		return AgentRunCreateResult{}, err
	}

	specEvent, err := appendAgentRunEventTx(tx, agentRunEventInput{
		Type:            "run_spec.created",
		OccurredAt:      now,
		ForgeProjectID:  workItem.ForgeProjectID,
		SubjectType:     "run_spec",
		SubjectRef:      fmt.Sprintf("run_spec:%d", specID),
		WorkItemID:      workItem.ID,
		WorkItemRef:     workItem.ProviderRef,
		AgentRunID:      runID,
		ControlActionID: controlActionID,
		Payload: map[string]any{
			"agent_run_id": runID,
			"run_spec_id":  specID,
			"branch":       branch,
		},
	})
	if err != nil {
		return AgentRunCreateResult{}, err
	}

	resultEventRefs, err := json.Marshal([]int64{controlActionEvent.ID, runEvent.ID, specEvent.ID})
	if err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("encode ControlAction result Event refs: %w", err)
	}
	if _, err := tx.Exec("UPDATE control_actions SET result_event_refs = ? WHERE id = ?", string(resultEventRefs), controlActionID); err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("update ControlAction result Event refs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AgentRunCreateResult{}, fmt.Errorf("commit AgentRun create: %w", err)
	}

	return AgentRunCreateResult{
		ControlAction: ControlAction{
			ID:     controlActionID,
			Type:   "start",
			Status: "succeeded",
		},
		AgentRun: AgentRun{
			ID:         runID,
			WorkItemID: workItem.ID,
			Status:     "planned",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		RunSpec: RunSpec{
			ID:         specID,
			AgentRunID: runID,
			SpecJSON:   specJSON,
			CreatedAt:  now,
		},
		Branch: branch,
		Events: []Event{controlActionEvent, runEvent, specEvent},
	}, nil
}

func encodeRunSpec(runID int64, workItem WorkItem, ref workitems.ProviderRef, branch string) (string, error) {
	owner, name, err := splitRepositoryPath(ref.RepositoryPath)
	if err != nil {
		return "", err
	}
	spec := map[string]any{
		"run_id": fmt.Sprintf("run_%d", runID),
		"work_item": map[string]any{
			"id":                  workItem.ID,
			"provider":            workItem.Provider,
			"provider_ref":        workItem.ProviderRef,
			"repository_ref":      workItem.RepositoryRef,
			"provider_issue":      workItem.ProviderIssueNumber,
			"title":               workItem.Title,
			"body_snapshot":       workItem.Body,
			"status":              workItem.Status,
			"provider_status_raw": workItem.ProviderStatusRaw,
			"url":                 workItem.URL,
			"provider_updated_at": workItem.ProviderUpdatedAt,
			"imported_at":         workItem.ImportedAt,
			"refreshed_at":        workItem.RefreshedAt,
		},
		"repo": map[string]any{
			"provider":    ref.Provider,
			"host":        ref.ProviderHost,
			"owner":       owner,
			"name":        name,
			"ref":         ref.RepositoryRef(),
			"base_branch": "main",
		},
		"branch": branch,
		"agent_adapter": map[string]any{
			"kind":       "command",
			"preset":     "codex",
			"env_policy": "scrubbed",
		},
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("encode RunSpec JSON: %w", err)
	}
	return string(encoded), nil
}

func splitRepositoryPath(repositoryPath string) (string, string, error) {
	parts := strings.Split(repositoryPath, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repository path %q", repositoryPath)
	}
	return parts[0], parts[1], nil
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
	Payload         map[string]any
}

func appendAgentRunEventTx(tx *sql.Tx, input agentRunEventInput) (Event, error) {
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode %s event payload: %w", input.Type, err)
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
	provider_ref,
	payload
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Type,
		input.OccurredAt,
		"forgelane",
		input.ForgeProjectID,
		input.SubjectType,
		input.SubjectRef,
		input.WorkItemID,
		input.WorkItemRef,
		input.AgentRunID,
		input.ControlActionID,
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
