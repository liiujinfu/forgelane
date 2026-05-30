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

	"github.com/liiujinfu/forgelane/internal/workitems"
	_ "modernc.org/sqlite"
)

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
	change_set_id INTEGER,
	provider_ref TEXT,
	correlation_id TEXT,
	payload TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_work_item_id ON events(work_item_id);
CREATE INDEX IF NOT EXISTS idx_events_forge_project_id ON events(forge_project_id);`

	if _, err := store.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize ForgeLane database schema: %w", err)
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
	ID   int64
	Type string
}

// WorkItemImportResult is the outcome of an atomic WorkItem import.
type WorkItemImportResult struct {
	WorkItem WorkItem
	Event    Event
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
		return WorkItem{}, fmt.Errorf("WorkItem not found")
	}
	if err != nil {
		return WorkItem{}, fmt.Errorf("query WorkItem: %w", err)
	}
	return workItem, nil
}

// GetForgeProjectByRef returns a persisted ForgeProject by canonical ref.
func (store *Store) GetForgeProjectByRef(providerRef string) (ForgeProject, error) {
	var forgeProject ForgeProject
	err := store.db.QueryRow(`
SELECT id, provider, provider_host, repository_path, provider_ref
FROM forge_projects
WHERE provider_ref = ?`, providerRef).Scan(
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
	const statement = `
INSERT INTO forge_projects (provider, provider_host, repository_path, provider_ref)
VALUES (?, ?, ?, ?)
ON CONFLICT(provider_ref) DO UPDATE SET
	provider = excluded.provider,
	provider_host = excluded.provider_host,
	repository_path = excluded.repository_path,
	updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');`

	_, err := executor.Exec(
		statement,
		forgeProject.Provider,
		forgeProject.ProviderHost,
		forgeProject.RepositoryPath,
		forgeProject.ProviderRef,
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
