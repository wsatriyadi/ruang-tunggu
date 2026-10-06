package repo

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/store"
)

// Schedule statuses (FR-05).
const (
	ScheduleDisabled    = "disabled"
	ScheduleScheduled   = "scheduled"
	ScheduleRunning     = "running"
	ScheduleCompleted   = "completed"
	ScheduleStopped     = "stopped"
	ScheduleFailed      = "failed"
	ScheduleMissed      = "missed"
	ScheduleSkippedBusy = "skipped_busy"
)

// Schedule is a one-shot broadcast window pinned to a playlist revision.
type Schedule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// RevisionID pins the exact revision, so later playlist edits do not change
	// what this broadcast will play (FR-04).
	RevisionID     string    `json:"revision_id"`
	RevisionNumber int       `json:"revision_number"`
	PlaylistName   string    `json:"playlist_name"`
	TargetID       string    `json:"target_id"`
	StartAt        time.Time `json:"start_at"`
	EndAt          time.Time `json:"end_at"`
	Timezone       string    `json:"timezone"`
	PlaybackMode   string    `json:"playback_mode"`
	LoopEnabled    bool      `json:"loop_enabled"`
	AutoReconnect  bool      `json:"auto_reconnect"`
	Enabled        bool      `json:"enabled"`
	Status         string    `json:"status"`
	Message        string    `json:"message"`
	ExecutionKey   string    `json:"execution_key,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	IsRunning      bool      `json:"is_running"`
	// HasNewerRevision marks a schedule pinned to an older revision.
	HasNewerRevision bool      `json:"has_newer_revision"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ConflictError reports an overlapping schedule. It lists the blocking windows
// so the operator can pick a different time (AC-13).
type ConflictError struct {
	Message     string   `json:"message"`
	Conflicting []string `json:"conflicting"`
}

func (e *ConflictError) Error() string { return e.Message }

// RunningError blocks an edit or delete while a schedule owns the slot.
type RunningError struct{ Message string }

func (e *RunningError) Error() string { return e.Message }

// ScheduleRepo stores schedules and enforces the single-session window rules.
type ScheduleRepo struct{ st *store.Store }

// NewScheduleRepo builds a schedule repository.
func NewScheduleRepo(st *store.Store) *ScheduleRepo { return &ScheduleRepo{st: st} }

const scheduleColumns = `s.id, s.name, s.revision_id, r.revision_number, p.name, s.target_id,
	s.start_at_utc, s.end_at_utc, s.timezone, s.playback_mode, s.loop_enabled,
	s.auto_reconnect, s.enabled, s.status, s.message, s.execution_key, s.session_id,
	s.created_at, s.updated_at,
	(SELECT MAX(revision_number) FROM playlist_revisions WHERE playlist_id = r.playlist_id)`

func scanSchedule(sc interface{ Scan(...any) error }) (*Schedule, error) {
	var s Schedule
	var start, end, created, updated string
	var loop, reconnect, enabled int
	var latestRevision int
	err := sc.Scan(&s.ID, &s.Name, &s.RevisionID, &s.RevisionNumber, &s.PlaylistName, &s.TargetID,
		&start, &end, &s.Timezone, &s.PlaybackMode, &loop, &reconnect, &enabled,
		&s.Status, &s.Message, &s.ExecutionKey, &s.SessionID, &created, &updated, &latestRevision)
	if err != nil {
		return nil, err
	}
	s.LoopEnabled, s.AutoReconnect, s.Enabled = loop == 1, reconnect == 1, enabled == 1
	s.StartAt, _ = time.Parse(time.RFC3339Nano, start)
	s.EndAt, _ = time.Parse(time.RFC3339Nano, end)
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	s.IsRunning = s.Status == ScheduleRunning
	s.HasNewerRevision = latestRevision > s.RevisionNumber
	return &s, nil
}

const scheduleJoin = ` FROM schedules s
	JOIN playlist_revisions r ON r.id = s.revision_id
	JOIN playlists p ON p.id = r.playlist_id`

// List returns all schedules ordered by start time.
func (r *ScheduleRepo) List() ([]Schedule, error) {
	rows, err := r.st.DB().Query(`SELECT ` + scheduleColumns + scheduleJoin + ` ORDER BY s.start_at_utc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// Get returns one schedule.
func (r *ScheduleRepo) Get(id string) (*Schedule, error) {
	row := r.st.DB().QueryRow(`SELECT `+scheduleColumns+scheduleJoin+` WHERE s.id = ?`, id)
	s, err := scanSchedule(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return s, nil
}

// validate applies the field rules from FR-05 and checks for overlap.
func (r *ScheduleRepo) validate(s *Schedule, excludeID string) error {
	if strings.TrimSpace(s.Name) == "" {
		return &ValidationError{Message: "Nama jadwal wajib diisi."}
	}
	if s.RevisionID == "" {
		return &ValidationError{Message: "Pilih playlist untuk jadwal ini."}
	}
	if s.TargetID == "" {
		return &ValidationError{Message: "Tujuan siaran belum dikonfigurasi di Settings."}
	}
	if s.StartAt.IsZero() || s.EndAt.IsZero() {
		return &ValidationError{Message: "Waktu mulai dan selesai wajib diisi."}
	}
	if !s.EndAt.After(s.StartAt) {
		return &ValidationError{Message: "Waktu selesai harus lebih besar dari waktu mulai."}
	}
	if s.Timezone == "" {
		return &ValidationError{Message: "Zona waktu wajib diisi."}
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return &ValidationError{Message: fmt.Sprintf("Zona waktu %q tidak dikenali.", s.Timezone)}
	}
	if s.PlaybackMode != "sequential" && s.PlaybackMode != "random" && s.PlaybackMode != "random_no_repeat" {
		return &ValidationError{Message: "Mode pemutaran tidak dikenal."}
	}

	// Windows are half-open, so two schedules may meet exactly at the boundary.
	rows, err := r.st.DB().Query(`SELECT name, start_at_utc, end_at_utc FROM schedules
		WHERE id <> ? AND enabled = 1 AND status IN (?, ?)
		  AND start_at_utc < ? AND end_at_utc > ?`,
		excludeID, ScheduleScheduled, ScheduleRunning,
		s.EndAt.UTC().Format(time.RFC3339Nano), s.StartAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	defer rows.Close()
	var conflicts []string
	for rows.Next() {
		var name, start, end string
		if err := rows.Scan(&name, &start, &end); err != nil {
			return err
		}
		conflicts = append(conflicts, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return &ConflictError{
			Message:     "Jadwal bertumpang tindih dengan jadwal lain yang sudah aktif.",
			Conflicting: conflicts,
		}
	}
	return nil
}

// Create stores a new schedule after validating its window.
func (r *ScheduleRepo) Create(s Schedule) (*Schedule, error) {
	if !s.StartAt.After(time.Now()) {
		return nil, &ValidationError{Message: "Waktu mulai sudah terlewat. Pilih waktu di masa depan."}
	}
	if err := r.validate(&s, ""); err != nil {
		return nil, err
	}
	s.ID = newID()
	s.Status = ScheduleScheduled
	if !s.Enabled {
		s.Status = ScheduleDisabled
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.st.DB().Exec(`INSERT INTO schedules
		(id, name, revision_id, target_id, start_at_utc, end_at_utc, timezone, playback_mode,
		 loop_enabled, auto_reconnect, enabled, status, message, execution_key, session_id,
		 created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.Name, s.RevisionID, s.TargetID,
		s.StartAt.UTC().Format(time.RFC3339Nano), s.EndAt.UTC().Format(time.RFC3339Nano),
		s.Timezone, s.PlaybackMode, boolInt(s.LoopEnabled), boolInt(s.AutoReconnect),
		boolInt(s.Enabled), s.Status, s.Message, "", "", now, now)
	if err != nil {
		return nil, err
	}
	return r.Get(s.ID)
}

// Update changes a schedule's details. A running schedule keeps its window and
// mode but may still be disabled, so a stop always uses Stop Live (FR-05).
func (r *ScheduleRepo) Update(id string, s Schedule) (*Schedule, error) {
	current, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	if current.Status == ScheduleRunning {
		// Only the enabled toggle is permitted while the slot is occupied.
		return nil, &RunningError{Message: "Jadwal sedang berjalan. Matikan toggle aktif atau hentikan sesi dengan Stop Live."}
	}
	s.ID = id
	if err := r.validate(&s, id); err != nil {
		return nil, err
	}
	status := ScheduleScheduled
	if !s.Enabled {
		status = ScheduleDisabled
	}
	if current.Status == ScheduleCompleted || current.Status == ScheduleStopped ||
		current.Status == ScheduleFailed {
		// Editing a finished schedule re-arms it.
		status = ScheduleScheduled
		if !s.Enabled {
			status = ScheduleDisabled
		}
	}
	_, err = r.st.DB().Exec(`UPDATE schedules SET name=?, revision_id=?, target_id=?,
		start_at_utc=?, end_at_utc=?, timezone=?, playback_mode=?, loop_enabled=?,
		auto_reconnect=?, enabled=?, status=?, message='', updated_at=? WHERE id=?`,
		s.Name, s.RevisionID, s.TargetID,
		s.StartAt.UTC().Format(time.RFC3339Nano), s.EndAt.UTC().Format(time.RFC3339Nano),
		s.Timezone, s.PlaybackMode, boolInt(s.LoopEnabled), boolInt(s.AutoReconnect),
		boolInt(s.Enabled), status, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

// SetStatus records a schedule's lifecycle state.
func (r *ScheduleRepo) SetStatus(id, status, message string) error {
	_, err := r.st.DB().Exec(`UPDATE schedules SET status=?, message=?, updated_at=? WHERE id=?`,
		status, message, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// SetExecutionKey records the unique identity of a fired window.
func (r *ScheduleRepo) SetExecutionKey(id, key string) error {
	_, err := r.st.DB().Exec(`UPDATE schedules SET execution_key=?, updated_at=? WHERE id=?`,
		key, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// AttachSession links a schedule to the session it started.
func (r *ScheduleRepo) AttachSession(id, sessionID string) error {
	_, err := r.st.DB().Exec(`UPDATE schedules SET session_id=?, updated_at=? WHERE id=?`,
		sessionID, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// Delete removes a schedule that is not currently running.
func (r *ScheduleRepo) Delete(id string) error {
	current, err := r.Get(id)
	if err != nil {
		return err
	}
	if current.Status == ScheduleRunning {
		return &RunningError{Message: "Jadwal sedang berjalan. Hentikan sesi dengan Stop Live terlebih dahulu."}
	}
	_, err = r.st.DB().Exec(`DELETE FROM schedules WHERE id = ?`, id)
	return err
}

// Due returns enabled schedules whose window has opened and not yet closed.
func (r *ScheduleRepo) Due(now time.Time) ([]Schedule, error) {
	rows, err := r.st.DB().Query(`SELECT `+scheduleColumns+scheduleJoin+`
		WHERE s.enabled = 1 AND s.status = ? AND s.start_at_utc <= ? AND s.end_at_utc > ?`,
		ScheduleScheduled, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// MarkExpired closes out schedules whose window passed without running.
func (r *ScheduleRepo) MarkExpired(now time.Time) error {
	_, err := r.st.DB().Exec(`UPDATE schedules SET status=?, message=?, updated_at=?
		WHERE enabled = 1 AND status = ? AND end_at_utc <= ?`,
		ScheduleMissed, "Waktu mulai terlewat saat server tidak aktif.",
		time.Now().UTC().Format(time.RFC3339Nano), ScheduleScheduled,
		now.UTC().Format(time.RFC3339Nano))
	return err
}

// Running returns the schedule that currently owns the publisher slot.
func (r *ScheduleRepo) Running() (*Schedule, bool, error) {
	row := r.st.DB().QueryRow(`SELECT `+scheduleColumns+scheduleJoin+` WHERE s.status = ? LIMIT 1`,
		ScheduleRunning)
	s, err := scanSchedule(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return s, true, nil
}

// Upcoming returns the next enabled schedules, newest first for display.
func (r *ScheduleRepo) Upcoming(limit int) ([]Schedule, error) {
	all, err := r.List()
	if err != nil {
		return nil, err
	}
	out := []Schedule{}
	for _, s := range all {
		if s.Enabled && (s.Status == ScheduleScheduled || s.Status == ScheduleRunning) {
			out = append(out, s)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Session returns the session repository for reconciliation. It exists so the
// scheduler can reach session state through one dependency.
func (r *ScheduleRepo) Session() *SessionRepo { return NewSessionRepo(r.st) }
