package repo

import (
	"database/sql"
	"errors"
	"time"

	"github.com/wisnu/ylivemasadi/internal/store"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// SessionRecord is the durable trace of one broadcast session (FR-06).
type SessionRecord struct {
	ID              string    `json:"id"`
	ScheduleID      string    `json:"schedule_id,omitempty"`
	Name            string    `json:"name"`
	DesiredState    string    `json:"desired_state"`
	ObservedState   string    `json:"observed_state"`
	StartAt         time.Time `json:"start_at,omitempty"`
	EndAt           time.Time `json:"end_at,omitempty"`
	StopReason      string    `json:"stop_reason,omitempty"`
	OwnerEpoch      string    `json:"owner_epoch,omitempty"`
	CurrentItem     int       `json:"current_item"`
	CurrentCycle    int       `json:"current_cycle"`
	PositionSeconds float64   `json:"position_seconds"`
	ErrorCode       string    `json:"error_code,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PlaybackRow records what actually played, so history can be inspected and a
// resumed session is not reshuffled blindly (FR-04).
type PlaybackRow struct {
	Cycle            int     `json:"cycle"`
	ItemIndex        int     `json:"item_index"`
	MediaID          string  `json:"media_id"`
	ExpectedDuration float64 `json:"expected_duration"`
	Status           string  `json:"status"`
	StartedAt        string  `json:"started_at,omitempty"`
	EndedAt          string  `json:"ended_at,omitempty"`
	Error            string  `json:"error,omitempty"`
}

// SessionRepo stores session state and playback history.
type SessionRepo struct{ st *store.Store }

// NewSessionRepo builds a session repository.
func NewSessionRepo(st *store.Store) *SessionRepo { return &SessionRepo{st: st} }

const sessionColumns = `id, schedule_id, name, desired_state, observed_state, start_at,
	end_at, stop_reason, owner_epoch, current_item, current_cycle, position_seconds,
	error_code, error_message, created_at, updated_at`

func scanSession(sc interface{ Scan(...any) error }) (*SessionRecord, error) {
	var r SessionRecord
	var start, end, created, updated string
	err := sc.Scan(&r.ID, &r.ScheduleID, &r.Name, &r.DesiredState, &r.ObservedState,
		&start, &end, &r.StopReason, &r.OwnerEpoch, &r.CurrentItem, &r.CurrentCycle,
		&r.PositionSeconds, &r.ErrorCode, &r.ErrorMessage, &created, &updated)
	if err != nil {
		return nil, err
	}
	if start != "" {
		r.StartAt, _ = time.Parse(time.RFC3339Nano, start)
	}
	if end != "" {
		r.EndAt, _ = time.Parse(time.RFC3339Nano, end)
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &r, nil
}

// Create persists a new session in the running state.
func (r *SessionRepo) Create(rec SessionRecord) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rec.CreatedAt = time.Now().UTC()
	rec.UpdatedAt = rec.CreatedAt
	if rec.DesiredState == "" {
		rec.DesiredState = "running"
	}
	if rec.ObservedState == "" {
		rec.ObservedState = streaming.StateStarting
	}
	_, err := r.st.DB().Exec(`INSERT INTO stream_sessions
		(`+sessionColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rec.ID, rec.ScheduleID, rec.Name, rec.DesiredState, rec.ObservedState,
		fmtTime(rec.StartAt), fmtTime(rec.EndAt), rec.StopReason, rec.OwnerEpoch,
		rec.CurrentItem, rec.CurrentCycle, rec.PositionSeconds,
		rec.ErrorCode, rec.ErrorMessage, now, now)
	return err
}

// Update writes the full session state.
func (r *SessionRepo) Update(id string, rec SessionRecord) error {
	_, err := r.st.DB().Exec(`UPDATE stream_sessions SET schedule_id=?, name=?,
		desired_state=?, observed_state=?, start_at=?, end_at=?, stop_reason=?,
		owner_epoch=?, current_item=?, current_cycle=?, position_seconds=?,
		error_code=?, error_message=?, updated_at=? WHERE id=?`,
		rec.ScheduleID, rec.Name, rec.DesiredState, rec.ObservedState,
		fmtTime(rec.StartAt), fmtTime(rec.EndAt), rec.StopReason, rec.OwnerEpoch,
		rec.CurrentItem, rec.CurrentCycle, rec.PositionSeconds,
		rec.ErrorCode, rec.ErrorMessage, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// SetObservedState records only the state, which is written frequently.
func (r *SessionRepo) SetObservedState(id, state, errorCode, errorMessage string) error {
	_, err := r.st.DB().Exec(`UPDATE stream_sessions SET observed_state=?, error_code=?,
		error_message=?, updated_at=? WHERE id=?`,
		state, errorCode, errorMessage, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// SetDesiredState records the operator's intent, which survives a restart and
// keeps a stopped session from being revived.
func (r *SessionRepo) SetDesiredState(id, desired string) error {
	_, err := r.st.DB().Exec(`UPDATE stream_sessions SET desired_state=?, updated_at=? WHERE id=?`,
		desired, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// Get returns one session.
func (r *SessionRepo) Get(id string) (*SessionRecord, error) {
	row := r.st.DB().QueryRow(`SELECT `+sessionColumns+` FROM stream_sessions WHERE id = ?`, id)
	rec, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return rec, nil
}

// Latest returns the most recently created session.
func (r *SessionRepo) Latest() (*SessionRecord, error) {
	row := r.st.DB().QueryRow(`SELECT ` + sessionColumns +
		` FROM stream_sessions ORDER BY created_at DESC LIMIT 1`)
	rec, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return rec, nil
}

// Recent returns the newest sessions first.
func (r *SessionRepo) Recent(limit int) ([]SessionRecord, error) {
	rows, err := r.st.DB().Query(`SELECT `+sessionColumns+
		` FROM stream_sessions ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionRecord{}
	for rows.Next() {
		rec, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// UnfinishedSession returns the session left running by a previous process.
func (r *SessionRepo) UnfinishedSession() (*SessionRecord, bool, error) {
	row := r.st.DB().QueryRow(`SELECT ` + sessionColumns + ` FROM stream_sessions
		WHERE desired_state = 'running' ORDER BY created_at DESC LIMIT 1`)
	rec, err := scanSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return rec, true, nil
}

// MarkItemPlaying upserts the row for the item currently on air.
func (r *SessionRepo) MarkItemPlaying(sessionID string, cycle, index int, mediaID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.st.DB().Exec(`INSERT INTO playback_items
		(session_id, cycle_number, item_index, media_id, expected_duration, playback_status,
		 started_at, ended_at, error)
		VALUES (?,?,?,?,0,'playing',?,'','')
		ON CONFLICT(session_id, cycle_number, item_index) DO UPDATE SET
			playback_status='playing', started_at=excluded.started_at`,
		sessionID, cycle, index, mediaID, now)
	return err
}

// MarkItemFinished closes a playback row. INSERT OR IGNORE keeps it working even
// when playing was never recorded, e.g. a session stopped before the first tick.
func (r *SessionRepo) MarkItemFinished(sessionID string, cycle, index int, status, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.st.DB().Exec(`INSERT OR IGNORE INTO playback_items
		(session_id, cycle_number, item_index, media_id, expected_duration, playback_status,
		 started_at, ended_at, error)
		VALUES (?,?,?,'',0,?,'',?,?)`,
		sessionID, cycle, index, status, now, errMsg)
	if err != nil {
		return err
	}
	_, err = r.st.DB().Exec(`UPDATE playback_items SET playback_status=?, ended_at=?, error=?
		WHERE session_id=? AND cycle_number=? AND item_index=?`,
		status, now, errMsg, sessionID, cycle, index)
	return err
}

// PlaybackHistory returns what played in a session, ordered for inspection.
func (r *SessionRepo) PlaybackHistory(sessionID string) ([]PlaybackRow, error) {
	rows, err := r.st.DB().Query(`SELECT cycle_number, item_index, media_id,
		expected_duration, playback_status, started_at, ended_at, error
		FROM playback_items WHERE session_id = ? ORDER BY cycle_number, item_index`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlaybackRow{}
	for rows.Next() {
		var p PlaybackRow
		if err := rows.Scan(&p.Cycle, &p.ItemIndex, &p.MediaID, &p.ExpectedDuration,
			&p.Status, &p.StartedAt, &p.EndedAt, &p.Error); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveCheckpoint implements streaming.Persister by mirroring session state.
func (r *SessionRepo) SaveCheckpoint(cp streaming.Checkpoint) error {
	_, err := r.st.DB().Exec(`UPDATE stream_sessions SET desired_state=?, observed_state=?,
		current_cycle=?, current_item=?, position_seconds=?, updated_at=? WHERE id=?`,
		cp.DesiredState, cp.ObservedState, cp.Cycle, cp.ItemIndex,
		cp.PositionSeconds, time.Now().UTC().Format(time.RFC3339Nano), cp.SessionID)
	return err
}

// LoadCheckpoint reconstructs a checkpoint from the session row.
func (r *SessionRepo) LoadCheckpoint(sessionID string) (streaming.Checkpoint, bool, error) {
	rec, err := r.Get(sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return streaming.Checkpoint{}, false, nil
		}
		return streaming.Checkpoint{}, false, err
	}
	return streaming.Checkpoint{
		SessionID:       rec.ID,
		DesiredState:    rec.DesiredState,
		ObservedState:   rec.ObservedState,
		Cycle:           rec.CurrentCycle,
		ItemIndex:       rec.CurrentItem,
		PositionSeconds: rec.PositionSeconds,
		EndAt:           rec.EndAt,
		UpdatedAt:       rec.UpdatedAt,
	}, true, nil
}

// Ensure SessionRepo satisfies the interfaces the worker and scheduler need.
var (
	_ streaming.Persister = (*SessionRepo)(nil)
	_ streaming.Store     = (*SessionRepo)(nil)
)

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
