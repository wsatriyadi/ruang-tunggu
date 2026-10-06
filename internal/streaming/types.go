// Package streaming owns the publisher: preflight, playlist playback, the
// FFmpeg process, progress tracking, retry with backoff and shutdown (FR-06,
// FR-07). It is the only component allowed to start a media process.
package streaming

import (
	"errors"
	"fmt"
	"time"
)

// Session states (FR-06). These describe the LOCAL publisher; they never
// assert that a broadcast is on air on YouTube.
const (
	StateIdle         = "IDLE"
	StateValidating   = "VALIDATING"
	StateStarting     = "STARTING"
	StateStreaming    = "STREAMING"
	StateReconnecting = "RECONNECTING"
	StateStopping     = "STOPPING"
	StateStopped      = "STOPPED"
	StateCompleted    = "COMPLETED"
	StateFailed       = "FAILED"
)

// Playback modes (FR-04).
const (
	ModeSequential     = "sequential"
	ModeRandom         = "random"
	ModeRandomNoRepeat = "random_no_repeat"
)

// Stable error codes returned to the API (PRD §13).
const (
	CodeStreamBusy          = "STREAM_BUSY"
	CodeScheduleConflict    = "SCHEDULE_CONFLICT"
	CodeMediaIncompatible   = "MEDIA_INCOMPATIBLE"
	CodeMediaInUse          = "MEDIA_IN_USE"
	CodeInsufficientStorage = "INSUFFICIENT_STORAGE"
	CodeInvalidTarget       = "INVALID_TARGET"
	CodeDependencyMissing   = "DEPENDENCY_MISSING"
	CodePlaylistEmpty       = "PLAYLIST_EMPTY"
	CodePublisherFailed     = "PUBLISHER_FAILED"
	CodeDeadlineReached     = "DEADLINE_REACHED"
	CodePreflightFailed     = "PREFLIGHT_FAILED"
	CodeRestartSuperseded   = "RESTART_SUPERSEDED"
)

// Error carries a stable code alongside a message safe to show an operator.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds an Error with a formatted message.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithDetail attaches a context key to the error.
func (e *Error) WithDetail(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// CodeOf extracts a stable code from an error, defaulting to INTERNAL.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "INTERNAL"
}

// PlaylistItem is one resolved media entry in a session snapshot.
type PlaylistItem struct {
	MediaID  string  `json:"media_id"`
	Path     string  `json:"-"`
	Name     string  `json:"name"`
	Duration float64 `json:"duration"`
}

// Config is the immutable snapshot a running session uses. Settings changed
// mid-session must not affect it (FR-10).
type Config struct {
	SessionID    string `json:"session_id"`
	ScheduleID   string `json:"schedule_id,omitempty"`
	RevisionID   string `json:"revision_id"`
	PlaylistName string `json:"playlist_name"`
	Mode         string `json:"mode"`
	Loop         bool   `json:"loop"`
	IngestURL    string `json:"-"`
	ProfileID    string `json:"profile_id"`
	// EndAt bounds the session; zero means no deadline.
	EndAt time.Time `json:"end_at"`
	// AutoReconnect enables bounded retry on transient failures.
	AutoReconnect bool `json:"auto_reconnect"`
	// Items is the flattened, already-ordered queue for one cycle. The worker
	// reorders per cycle according to Mode.
	Items []PlaylistItem `json:"items"`
	// CycleDuration is the expected length of one pass, used for the UI.
	CycleDuration float64 `json:"cycle_duration"`
}

// Progress is the live view of a running session (FR-02, FR-08, AC-17).
type Progress struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
	// DesiredState is the operator's intent, distinct from what the process
	// has achieved so far.
	DesiredState string `json:"desired_state"`
	// Cycle is the 1-based pass number.
	Cycle int `json:"cycle"`
	// ItemIndex is the 0-based position within the cycle.
	ItemIndex int `json:"item_index"`
	ItemCount int `json:"item_count"`
	// ItemName describes the file currently being sent. The UI resolves the
	// thumbnail from the playlist, so no image path travels in this payload.
	ItemName string `json:"item_name,omitempty"`
	// PositionSeconds is media time within the active file.
	PositionSeconds float64 `json:"position_seconds"`
	// ItemDuration is the length of the active file.
	ItemDuration float64 `json:"item_duration"`
	// SessionDuration is wall time since the session started.
	SessionDuration float64 `json:"session_duration"`
	// ConnectionDuration is wall time since the current publisher attempt began.
	ConnectionDuration float64 `json:"connection_duration"`
	// Attempt counts reconnect attempts for the current connection.
	Attempt int `json:"attempt"`
	// NextRetryAt is set while reconnecting.
	NextRetryAt time.Time `json:"next_retry_at,omitempty"`
	// ErrorCode and ErrorMessage explain a failure or the current disruption.
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	// PublisherBitrateKbps is measured publisher throughput, kept separate from
	// host upload traffic (FR-02).
	PublisherBitrateKbps float64 `json:"publisher_bitrate_kbps"`
	// MediaTimeSeconds is total media time sent this session.
	MediaTimeSeconds float64   `json:"media_time_seconds"`
	StartedAt        time.Time `json:"started_at,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// StopReason explains why a session ended.
type StopReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PreflightResult reports whether a session may start and why not (FR-06).
type PreflightResult struct {
	OK       bool             `json:"ok"`
	Code     string           `json:"code,omitempty"`
	Message  string           `json:"message,omitempty"`
	Details  map[string]any   `json:"details,omitempty"`
	Issues   []PreflightIssue `json:"issues,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
}

// PreflightIssue is one blocking problem found before starting.
type PreflightIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	// MediaID attributes the issue to a playlist entry.
	MediaID string `json:"media_id,omitempty"`
	Name    string `json:"name,omitempty"`
}
