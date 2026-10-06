package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/repo"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// handleListPlaylists returns every playlist with its rolled-up summary.
func (s *Server) handleListPlaylists(w http.ResponseWriter, r *http.Request) {
	list, err := s.playlists.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat playlist.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

type playlistRequest struct {
	Name         string   `json:"name"`
	PlaybackMode string   `json:"playback_mode"`
	LoopEnabled  bool     `json:"loop_enabled"`
	MediaIDs     []string `json:"media_ids"`
}

// handleCreatePlaylist creates a playlist and its first revision.
func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req playlistRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION", "Nama playlist wajib diisi.")
		return
	}
	playlist, err := s.playlists.Create(req.Name, sess.UserID)
	if err != nil {
		s.writePlaylistError(w, err)
		return
	}
	// A playlist created with items in one call saves its first real revision.
	if len(req.MediaIDs) > 0 {
		playlist, err = s.playlists.Update(playlist.ID, sess.UserID, req.Name,
			req.PlaybackMode, req.LoopEnabled, req.MediaIDs)
		if err != nil {
			s.writePlaylistError(w, err)
			return
		}
	}
	_ = s.audit.Insert(sess.UserID, "create_playlist", "playlist", playlist.ID,
		map[string]any{"name": playlist.Name})
	writeJSON(w, http.StatusCreated, playlist)
}

// handleUpdatePlaylist saves a new revision, leaving old ones intact (FR-04).
func (s *Server) handleUpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req playlistRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	playlist, err := s.playlists.Update(r.PathValue("id"), sess.UserID, req.Name,
		req.PlaybackMode, req.LoopEnabled, req.MediaIDs)
	if err != nil {
		s.writePlaylistError(w, err)
		return
	}
	_ = s.audit.Insert(sess.UserID, "update_playlist", "playlist", playlist.ID,
		map[string]any{"revision": playlist.RevisionNumber, "items": playlist.ItemCount})
	s.hub.PublishMedia(map[string]any{"action": "playlist_updated", "id": playlist.ID})
	writeJSON(w, http.StatusOK, playlist)
}

// handleDeletePlaylist removes a playlist that nothing depends on.
func (s *Server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id := r.PathValue("id")
	if err := s.playlists.Delete(id); err != nil {
		var inUse *repo.ScheduleInUseError
		if errors.As(err, &inUse) {
			writeErrorDetails(w, http.StatusConflict, "MEDIA_IN_USE",
				"Playlist masih dipakai jadwal berikut.", map[string]any{"schedules": inUse.Schedules})
			return
		}
		s.writePlaylistError(w, err)
		return
	}
	_ = s.audit.Insert(sess.UserID, "delete_playlist", "playlist", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// writePlaylistError maps repository errors onto stable API codes.
func (s *Server) writePlaylistError(w http.ResponseWriter, err error) {
	var vErr *repo.ValidationError
	if errors.As(err, &vErr) {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION", vErr.Message)
		return
	}
	var sErr *repo.ScheduleInUseError
	if errors.As(err, &sErr) {
		writeErrorDetails(w, http.StatusConflict, "MEDIA_IN_USE",
			"Playlist masih dipakai jadwal berikut.", map[string]any{"schedules": sErr.Schedules})
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
}

// --- schedules ---

// handleListSchedules returns every schedule with its status.
func (s *Server) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.schedules.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat jadwal.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// handleGetSchedule returns one schedule with its playlist revision.
func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	sch, err := s.schedules.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Jadwal tidak ditemukan.")
		return
	}
	payload := map[string]any{"schedule": sch}
	if playlist, err := s.playlists.GetRevision(sch.RevisionID); err == nil {
		payload["playlist"] = playlist
	}
	writeJSON(w, http.StatusOK, payload)
}

type scheduleRequest struct {
	Name          string    `json:"name"`
	RevisionID    string    `json:"revision_id"`
	StartAt       time.Time `json:"start_at"`
	EndAt         time.Time `json:"end_at"`
	Timezone      string    `json:"timezone"`
	PlaybackMode  string    `json:"playback_mode"`
	LoopEnabled   bool      `json:"loop_enabled"`
	AutoReconnect bool      `json:"auto_reconnect"`
	Enabled       bool      `json:"enabled"`
	TargetID      string    `json:"target_id"`
}

// handleCreateSchedule stores a new one-shot schedule.
func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req scheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	sch, err := s.schedules.Create(repo.Schedule{
		Name: req.Name, RevisionID: req.RevisionID, TargetID: s.resolveTargetID(req.TargetID),
		StartAt: req.StartAt, EndAt: req.EndAt, Timezone: req.Timezone,
		PlaybackMode: req.PlaybackMode, LoopEnabled: req.LoopEnabled,
		AutoReconnect: req.AutoReconnect, Enabled: req.Enabled,
	})
	if err != nil {
		s.writeScheduleError(w, err)
		return
	}
	_ = s.audit.Insert(sess.UserID, "create_schedule", "schedule", sch.ID,
		map[string]any{"name": sch.Name, "start_at": sch.StartAt})
	s.hub.PublishSchedule(map[string]any{"action": "created", "id": sch.ID})
	writeJSON(w, http.StatusCreated, sch)
}

// handleUpdateSchedule edits a schedule, refusing while it holds the slot.
func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req scheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	sch, err := s.schedules.Update(r.PathValue("id"), repo.Schedule{
		Name: req.Name, RevisionID: req.RevisionID, TargetID: s.resolveTargetID(req.TargetID),
		StartAt: req.StartAt, EndAt: req.EndAt, Timezone: req.Timezone,
		PlaybackMode: req.PlaybackMode, LoopEnabled: req.LoopEnabled,
		AutoReconnect: req.AutoReconnect, Enabled: req.Enabled,
	})
	if err != nil {
		s.writeScheduleError(w, err)
		return
	}
	_ = s.audit.Insert(sess.UserID, "update_schedule", "schedule", sch.ID, nil)
	s.hub.PublishSchedule(map[string]any{"action": "updated", "id": sch.ID})
	writeJSON(w, http.StatusOK, sch)
}

// handleDeleteSchedule removes a schedule that is not running.
func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := s.schedules.Delete(r.PathValue("id")); err != nil {
		s.writeScheduleError(w, err)
		return
	}
	_ = s.audit.Insert(sess.UserID, "delete_schedule", "schedule", r.PathValue("id"), nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// writeScheduleError maps schedule errors onto stable API codes.
func (s *Server) writeScheduleError(w http.ResponseWriter, err error) {
	var cErr *repo.ConflictError
	if errors.As(err, &cErr) {
		writeErrorDetails(w, http.StatusConflict, "SCHEDULE_CONFLICT",
			cErr.Message, map[string]any{"conflicting": cErr.Conflicting})
		return
	}
	var rErr *repo.RunningError
	if errors.As(err, &rErr) {
		writeError(w, http.StatusConflict, "SCHEDULE_RUNNING", rErr.Message)
		return
	}
	var vErr *repo.ValidationError
	if errors.As(err, &vErr) {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION", vErr.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
}

// resolveTargetID falls back to the configured default target.
func (s *Server) resolveTargetID(id string) string {
	if id != "" {
		return id
	}
	if target, err := s.targets.Default(); err == nil && target != nil {
		return target.ID
	}
	return "default"
}

// buildScheduleConfig is used by the scheduler to prepare a scheduled session.
func (s *Server) buildScheduleConfig(scheduleID string, start, end time.Time) (streaming.Config, error) {
	sch, err := s.schedules.Get(scheduleID)
	if err != nil {
		return streaming.Config{}, err
	}
	cfg, err := s.buildConfig(sch.ID, sch.RevisionID, sch.PlaybackMode, sch.LoopEnabled, end)
	if err != nil {
		return streaming.Config{}, err
	}
	cfg.ScheduleID = sch.ID
	cfg.PlaylistName = sch.Name
	return *cfg, nil
}

// logScheduleStart records the scheduler's decision.
func (s *Server) logScheduleStart(name, sessionID string) {
	s.log.Info(logging.SourceScheduler, sessionID, "Jadwal %q dimulai sesi %s", name, sessionID)
}

// BuildScheduleConfig prepares the immutable snapshot for a scheduled session.
// It is called by the scheduler, which owns the wall clock but not the worker.
func (s *Server) BuildScheduleConfig(scheduleID string, start, end time.Time) (streaming.Config, error) {
	return s.buildScheduleConfig(scheduleID, start, end)
}

// StartScheduledSession records and starts a session on the scheduler's behalf.
func (s *Server) StartScheduledSession(scheduleID, name string, cfg streaming.Config) (string, error) {
	cfg.SessionID = newSessionID()
	if err := s.sessions.Create(repo.SessionRecord{
		ID: cfg.SessionID, ScheduleID: scheduleID, Name: name,
		DesiredState: "running", ObservedState: streaming.StateStarting,
		StartAt: time.Now().UTC(), EndAt: cfg.EndAt,
	}); err != nil {
		return "", err
	}
	if _, err := s.worker.Start(cfg); err != nil {
		_ = s.sessions.SetObservedState(cfg.SessionID, streaming.StateFailed,
			streaming.CodeOf(err), err.Error())
		return "", err
	}
	s.logScheduleStart(name, cfg.SessionID)
	return cfg.SessionID, nil
}

// FinishSession writes the terminal state of a session.
func (s *Server) FinishSession(sessionID, state, stopReason, errCode, errMsg string) error {
	rec, err := s.sessions.Get(sessionID)
	if err != nil {
		return err
	}
	rec.ObservedState = state
	rec.StopReason = stopReason
	rec.ErrorCode = errCode
	rec.ErrorMessage = errMsg
	if state != streaming.StateStreaming && state != streaming.StateStarting &&
		state != streaming.StateReconnecting && state != streaming.StateStopping {
		// A terminal state must not be revived by recovery (FR-07).
		rec.DesiredState = "stopped"
	}
	rec.EndAt = time.Now().UTC()
	if err := s.sessions.Update(sessionID, *rec); err != nil {
		return err
	}
	// A finished scheduled session releases its schedule slot.
	if rec.ScheduleID != "" {
		status := repo.ScheduleCompleted
		switch state {
		case streaming.StateStopped:
			status = repo.ScheduleStopped
		case streaming.StateFailed:
			status = repo.ScheduleFailed
		}
		_ = s.schedules.SetStatus(rec.ScheduleID, status, errMsg)
		s.hub.PublishSchedule(map[string]any{"action": "finished", "id": rec.ScheduleID})
	}
	return nil
}
