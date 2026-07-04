package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go sqlite driver (no cgo)
)

// Store provides SQLite persistence: encrypted provider keys, routing
// preferences, and the interaction log. Safe for concurrent use.
type Store struct {
	db     *sql.DB
	cipher *keyCipher
}

// Open creates or opens the database at path, running migrations and
// initializing the encryption key (see newKeyCipher). Parent directories
// are created as needed.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// modernc/sqlite serializes writes; a single connection avoids
	// SQLITE_BUSY on concurrent writers.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	cipher, err := newKeyCipher(filepath.Dir(path))
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, cipher: cipher}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// migrations are applied in order; PRAGMA user_version tracks progress.
// Every statement must be idempotent-safe within its version step.
var migrations = []string{
	// v1: initial schema
	`CREATE TABLE IF NOT EXISTS provider_keys (
		provider    TEXT PRIMARY KEY,
		api_key_enc TEXT NOT NULL,
		created_at  TEXT NOT NULL,
		updated_at  TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS prefs (
		id                   INTEGER PRIMARY KEY CHECK (id = 1),
		priority             TEXT    NOT NULL DEFAULT 'balanced',
		max_cost_cents       REAL    NOT NULL DEFAULT 0,
		latency_tolerance_ms INTEGER NOT NULL DEFAULT 0,
		only_free            INTEGER NOT NULL DEFAULT 0,
		only_local           INTEGER NOT NULL DEFAULT 0,
		max_response_tokens  INTEGER NOT NULL DEFAULT 0,
		default_model        TEXT    NOT NULL DEFAULT '',
		fallback_depth       INTEGER NOT NULL DEFAULT 2,
		disallowed_models    TEXT    NOT NULL DEFAULT '',
		updated_at           TEXT    NOT NULL
	);
	CREATE TABLE IF NOT EXISTS interactions (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		ts                TEXT    NOT NULL,
		model             TEXT    NOT NULL,
		provider          TEXT    NOT NULL,
		category          TEXT    NOT NULL DEFAULT '',
		complexity        REAL    NOT NULL DEFAULT 0,
		analyzer          TEXT    NOT NULL DEFAULT '',
		prompt_tokens     INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		cost_cents        REAL    NOT NULL DEFAULT 0,
		latency_ms        INTEGER NOT NULL DEFAULT 0,
		ttft_ms           INTEGER NOT NULL DEFAULT 0,
		status            TEXT    NOT NULL DEFAULT 'ok',
		fallback_attempts INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_interactions_ts ON interactions(ts);`,
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("bump schema version: %w", err)
		}
	}
	return nil
}
