package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store owns access to ForgeLane's instance-global SQLite database.
type Store struct {
	db *sql.DB
}

// ForgeProject is the persisted provider-backed project identity.
type ForgeProject struct {
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

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open ForgeLane database: %w", err)
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
);`

	if _, err := store.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize ForgeLane database schema: %w", err)
	}
	return nil
}

// UpsertForgeProject persists instance config idempotently.
// AgentRun and ControlAction state changes get audited by later workflow slices.
func (store *Store) UpsertForgeProject(forgeProject ForgeProject) error {
	const statement = `
INSERT INTO forge_projects (provider, provider_host, repository_path, provider_ref)
VALUES (?, ?, ?, ?)
ON CONFLICT(provider_ref) DO UPDATE SET
	provider = excluded.provider,
	provider_host = excluded.provider_host,
	repository_path = excluded.repository_path,
	updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now');`

	_, err := store.db.Exec(
		statement,
		forgeProject.Provider,
		forgeProject.ProviderHost,
		forgeProject.RepositoryPath,
		forgeProject.ProviderRef,
	)
	if err != nil {
		return fmt.Errorf("persist ForgeProject %s: %w", forgeProject.ProviderRef, err)
	}
	return nil
}
