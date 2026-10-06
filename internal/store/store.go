// Package store owns the SQLite database: schema, migrations, and all queries.
// Media bytes never live here — only metadata and indexes (PRD §12).
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open connects to the SQLite database at path and applies the schema.
func Open(path string) (*Store, error) {
	// WAL keeps readers (API) from blocking the single writer (worker).
	// Busy timeout absorbs the brief write overlap between HTTP and scheduler.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", url.PathEscape(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// One writer at a time keeps WAL contention predictable on low-power devices.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DB exposes the raw handle for the few packages that need custom queries.
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS auth_sessions (
    token_hash   TEXT PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_expiry ON auth_sessions(expires_at);

-- Single administrator account may be created once during setup (FR-01).
CREATE TABLE IF NOT EXISTS app_meta (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS stream_profiles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    version     INTEGER NOT NULL,
    definition  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS stream_targets (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL,
    ingest_url            TEXT NOT NULL,
    encrypted_stream_key  TEXT NOT NULL,
    preview_video_id      TEXT NOT NULL DEFAULT '',
    profile_id            TEXT NOT NULL DEFAULT 'yt-sdr-h264',
    version               INTEGER NOT NULL DEFAULT 1,
    updated_at            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS upload_sessions (
    id               TEXT PRIMARY KEY,
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    original_name    TEXT NOT NULL,
    temp_path        TEXT NOT NULL,
    expected_size    INTEGER NOT NULL,
    received_bytes   INTEGER NOT NULL DEFAULT 0,
    part_size        INTEGER NOT NULL,
    status           TEXT NOT NULL,
    error            TEXT NOT NULL DEFAULT '',
    media_id         TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    expires_at       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_user ON upload_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_upload_sessions_expiry ON upload_sessions(expires_at);

CREATE TABLE IF NOT EXISTS upload_parts (
    upload_id   TEXT NOT NULL REFERENCES upload_sessions(id) ON DELETE CASCADE,
    part_index  INTEGER NOT NULL,
    size        INTEGER NOT NULL,
    received_at TEXT NOT NULL,
    PRIMARY KEY (upload_id, part_index)
);

CREATE TABLE IF NOT EXISTS media_files (
    id             TEXT PRIMARY KEY,
    original_name  TEXT NOT NULL,
    storage_path   TEXT NOT NULL,
    size           INTEGER NOT NULL DEFAULT 0,
    duration       REAL NOT NULL DEFAULT 0,
    container      TEXT NOT NULL DEFAULT '',
    video_codec    TEXT NOT NULL DEFAULT '',
    audio_codec    TEXT NOT NULL DEFAULT '',
    width          INTEGER NOT NULL DEFAULT 0,
    height         INTEGER NOT NULL DEFAULT 0,
    fps            REAL NOT NULL DEFAULT 0,
    bitrate        INTEGER NOT NULL DEFAULT 0,
    sample_rate    INTEGER NOT NULL DEFAULT 0,
    channels       INTEGER NOT NULL DEFAULT 0,
    channel_layout TEXT NOT NULL DEFAULT '',
    pix_fmt        TEXT NOT NULL DEFAULT '',
    profile        TEXT NOT NULL DEFAULT '',
    level          INTEGER NOT NULL DEFAULT 0,
    time_base      TEXT NOT NULL DEFAULT '',
    has_video      INTEGER NOT NULL DEFAULT 0,
    has_audio      INTEGER NOT NULL DEFAULT 0,
    start_time     REAL NOT NULL DEFAULT 0,
    metadata       TEXT NOT NULL DEFAULT '{}',
    thumbnail_path TEXT NOT NULL DEFAULT '',
    file_status    TEXT NOT NULL DEFAULT 'processing',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_files_status ON media_files(file_status);
CREATE INDEX IF NOT EXISTS idx_media_files_created ON media_files(created_at DESC);

CREATE TABLE IF NOT EXISTS media_validations (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    media_id           TEXT NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    profile_id         TEXT NOT NULL,
    profile_version    INTEGER NOT NULL,
    compatibility      TEXT NOT NULL,
    issues             TEXT NOT NULL DEFAULT '[]',
    keyframe_max_gap   REAL NOT NULL DEFAULT 0,
    checked_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_validations_media ON media_validations(media_id);

CREATE TABLE IF NOT EXISTS playlists (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    latest_revision_id TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS playlist_revisions (
    id             TEXT PRIMARY KEY,
    playlist_id    TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    revision_number INTEGER NOT NULL,
    playback_mode  TEXT NOT NULL,
    loop_enabled   INTEGER NOT NULL DEFAULT 0,
    created_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at     TEXT NOT NULL,
    UNIQUE (playlist_id, revision_number)
);
CREATE INDEX IF NOT EXISTS idx_playlist_revisions_playlist ON playlist_revisions(playlist_id);

CREATE TABLE IF NOT EXISTS playlist_items (
    revision_id TEXT NOT NULL REFERENCES playlist_revisions(id) ON DELETE CASCADE,
    media_id    TEXT NOT NULL REFERENCES media_files(id) ON DELETE RESTRICT,
    position    INTEGER NOT NULL,
    PRIMARY KEY (revision_id, media_id)
);
CREATE INDEX IF NOT EXISTS idx_playlist_items_media ON playlist_items(media_id);

CREATE TABLE IF NOT EXISTS schedules (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    revision_id    TEXT NOT NULL REFERENCES playlist_revisions(id) ON DELETE RESTRICT,
    target_id      TEXT NOT NULL REFERENCES stream_targets(id) ON DELETE RESTRICT,
    start_at_utc   TEXT NOT NULL,
    end_at_utc     TEXT NOT NULL,
    timezone       TEXT NOT NULL,
    playback_mode  TEXT NOT NULL,
    loop_enabled   INTEGER NOT NULL DEFAULT 1,
    auto_reconnect INTEGER NOT NULL DEFAULT 1,
    enabled        INTEGER NOT NULL DEFAULT 1,
    status         TEXT NOT NULL DEFAULT 'scheduled',
    message        TEXT NOT NULL DEFAULT '',
    execution_key  TEXT NOT NULL DEFAULT '',
    session_id     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_schedules_window ON schedules(start_at_utc, end_at_utc);
CREATE INDEX IF NOT EXISTS idx_schedules_status ON schedules(status);

CREATE TABLE IF NOT EXISTS stream_sessions (
    id                  TEXT PRIMARY KEY,
    schedule_id         TEXT NOT NULL DEFAULT '',
    name                TEXT NOT NULL DEFAULT '',
    config_snapshot     TEXT NOT NULL DEFAULT '{}',
    desired_state       TEXT NOT NULL DEFAULT 'stopped',
    observed_state      TEXT NOT NULL DEFAULT 'IDLE',
    start_at            TEXT NOT NULL DEFAULT '',
    end_at              TEXT NOT NULL DEFAULT '',
    stop_reason         TEXT NOT NULL DEFAULT '',
    owner_epoch         TEXT NOT NULL DEFAULT '',
    connection_started_at TEXT NOT NULL DEFAULT '',
    current_item        INTEGER NOT NULL DEFAULT 0,
    current_cycle       INTEGER NOT NULL DEFAULT 0,
    position_seconds    REAL NOT NULL DEFAULT 0,
    error_code          TEXT NOT NULL DEFAULT '',
    error_message       TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_stream_sessions_created ON stream_sessions(created_at DESC);

CREATE TABLE IF NOT EXISTS playback_items (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id        TEXT NOT NULL REFERENCES stream_sessions(id) ON DELETE CASCADE,
    cycle_number      INTEGER NOT NULL,
    item_index        INTEGER NOT NULL,
    media_id          TEXT NOT NULL,
    expected_duration REAL NOT NULL DEFAULT 0,
    playback_status   TEXT NOT NULL DEFAULT 'pending',
    started_at        TEXT NOT NULL DEFAULT '',
    ended_at          TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    UNIQUE (session_id, cycle_number, item_index)
);
CREATE INDEX IF NOT EXISTS idx_playback_items_session ON playback_items(session_id);

CREATE TABLE IF NOT EXISTS stream_attempts (
    id               TEXT PRIMARY KEY,
    session_id       TEXT NOT NULL REFERENCES stream_sessions(id) ON DELETE CASCADE,
    attempt_number   INTEGER NOT NULL,
    process_identity TEXT NOT NULL DEFAULT '',
    pid              INTEGER NOT NULL DEFAULT 0,
    started_at       TEXT NOT NULL,
    ended_at         TEXT NOT NULL DEFAULT '',
    exit_code        INTEGER NOT NULL DEFAULT 0,
    error_code       TEXT NOT NULL DEFAULT '',
    error_detail     TEXT NOT NULL DEFAULT '',
    stall_detected   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_stream_attempts_session ON stream_attempts(session_id);

-- Single effective action per key: guards double-clicks and duplicate tabs (FR-06).
CREATE TABLE IF NOT EXISTS operation_requests (
    idempotency_key TEXT PRIMARY KEY,
    action          TEXT NOT NULL,
    session_id      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL,
    result          TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_operation_requests_created ON operation_requests(created_at);

CREATE TABLE IF NOT EXISTS audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    actor_id    INTEGER NOT NULL DEFAULT 0,
    action      TEXT NOT NULL,
    entity_type TEXT NOT NULL DEFAULT '',
    entity_id   TEXT NOT NULL DEFAULT '',
    metadata    TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at DESC);

-- Queryable copy of publisher/application log lines for the Logs page (FR-09).
CREATE TABLE IF NOT EXISTS log_records (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         TEXT NOT NULL,
    level      TEXT NOT NULL,
    source     TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_log_records_ts ON log_records(ts DESC);
CREATE INDEX IF NOT EXISTS idx_log_records_session ON log_records(session_id);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func nowUTC() time.Time { return time.Now().UTC() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTS(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, v)
}

// Tx runs fn inside a transaction, rolling back on error.
func (s *Store) Tx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
