package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/repo"
	"github.com/wisnu/ylivemasadi/internal/store"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// handleStreamStatus returns the live publisher state.
func (s *Server) handleStreamStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.statusPayload())
}

// statusPayload enriches worker progress with the persisted session record.
func (s *Server) statusPayload() map[string]any {
	progress := s.worker.Status()
	payload := map[string]any{
		"stream":       progress,
		"busy":         s.worker.Active(),
		"dependencies": s.dependencyStatus(),
		"server_time":  time.Now().UTC(),
	}
	// Without YouTube API access the panel must not imply the broadcast is on
	// air; it reports the local publisher state and that fact separately (AC-18).
	payload["youtube"] = map[string]any{
		"verified": false,
		"state":    "unverified",
		"message":  "Status tayang di YouTube belum diverifikasi. Buka YouTube Studio untuk memastikan.",
	}
	if rec, err := s.sessions.Latest(); err == nil {
		payload["session"] = rec
	}
	if sch, running, err := s.schedules.Running(); err == nil && running {
		payload["running_schedule"] = sch
	}
	return payload
}

type startRequest struct {
	// RevisionID pins the playlist revision to play; empty means the newest.
	RevisionID string `json:"revision_id"`
	Mode       string `json:"mode"`
	Loop       bool   `json:"loop"`
	// EndAt bounds a manual session the same way a schedule does.
	EndAt *time.Time `json:"end_at,omitempty"`
	// IdempotencyKey makes a repeated click a single effective action (FR-06).
	IdempotencyKey string `json:"idempotency_key"`
}

// handlePreflight runs every start check and reports what would fail.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
	}
	result, _ := s.preflight(r.Context(), req.RevisionID, req.Mode, req.Loop, time.Time{})
	writeJSON(w, http.StatusOK, result)
}

// preflight checks credentials, tooling, the slot, files and compatibility
// before any process is started, so a failure names what to fix (FR-06).
func (s *Server) preflight(ctx context.Context, revisionID, mode string, loop bool, endAt time.Time) (streaming.PreflightResult, map[string]any) {
	result := streaming.PreflightResult{OK: true}
	details := map[string]any{}
	var issues []streaming.PreflightIssue

	// 1. Required executables.
	deps := s.binaries.Check(ctx)
	details["dependencies"] = deps
	if !deps.FFmpegFound || !deps.FFprobeFound {
		missing := []string{}
		if !deps.FFmpegFound {
			missing = append(missing, "ffmpeg")
		}
		if !deps.FFprobeFound {
			missing = append(missing, "ffprobe")
		}
		issues = append(issues, streaming.PreflightIssue{
			Field:   "dependencies",
			Message: fmt.Sprintf("%s belum terpasang. Pasang FFmpeg di server lalu periksa lagi.", joinNames(missing)),
		})
	}

	// 2. Destination credentials.
	target, err := s.targets.Default()
	if err != nil || target == nil || !target.StreamKeySet {
		issues = append(issues, streaming.PreflightIssue{
			Field:   "stream_key",
			Message: "Stream key belum diatur. Isi di Settings pada kelompok Tujuan siaran.",
		})
	}
	if target != nil {
		details["target"] = map[string]any{
			"name": target.Name, "ingest_url": target.IngestURL,
			"stream_key_set": target.StreamKeySet, "profile_id": target.ProfileID,
		}
		if target.IngestURL == "" {
			issues = append(issues, streaming.PreflightIssue{
				Field:   "ingest_url",
				Message: "URL ingest belum diisi di Settings.",
			})
		}
	}

	// 3. The single publisher slot.
	if s.worker.Active() {
		issues = append(issues, streaming.PreflightIssue{
			Field:   "slot",
			Message: "Sesi lain sedang berjalan. Kapasitas MVP adalah satu publisher aktif.",
		})
	}

	// 4. Deadline still in the future.
	if !endAt.IsZero() && !endAt.After(time.Now()) {
		issues = append(issues, streaming.PreflightIssue{
			Field:   "end_at",
			Message: "Waktu akhir sudah terlewat.",
		})
	}

	// 5. Playlist contents and cross-file compatibility.
	playlist, err := s.resolvePlaylist(revisionID)
	if err != nil {
		issues = append(issues, streaming.PreflightIssue{
			Field:   "playlist",
			Message: "Playlist tidak ditemukan atau tidak memiliki item.",
		})
	} else {
		details["playlist"] = map[string]any{
			"id": playlist.ID, "name": playlist.Name, "revision": playlist.RevisionNumber,
			"items": playlist.ItemCount, "cycle_duration": playlist.CycleDuration,
			"compatibility": playlist.Compatibility,
		}
		for _, item := range playlist.Items {
			switch {
			case item.FileStatus == "missing":
				issues = append(issues, streaming.PreflightIssue{
					Field: "media", MediaID: item.MediaID, Name: item.Name,
					Message: fmt.Sprintf("%q tidak ditemukan di penyimpanan.", item.Name),
				})
			case item.FileStatus != "ready":
				issues = append(issues, streaming.PreflightIssue{
					Field: "media", MediaID: item.MediaID, Name: item.Name,
					Message: fmt.Sprintf("%q belum siap diputar (status %s).", item.Name, item.FileStatus),
				})
			case item.Compatibility == media.StatusIncompatible:
				issues = append(issues, streaming.PreflightIssue{
					Field: "media", MediaID: item.MediaID, Name: item.Name,
					Message: fmt.Sprintf("%q tidak kompatibel dengan profile pengiriman.", item.Name),
				})
			}
		}
		// Cohesion: files sharing one connection must match exactly.
		for _, ci := range playlist.Issues {
			if ci.Severity != media.SeverityBlock {
				continue
			}
			issues = append(issues, streaming.PreflightIssue{
				Field: ci.Field, MediaID: ci.MediaID, Name: ci.Name, Message: ci.Message,
			})
		}
	}

	// 6. Storage access.
	if _, ok := s.diskUsage(); !ok {
		result.Warnings = append(result.Warnings,
			"Kapasitas disk tidak dapat dibaca pada platform ini.")
	}

	if len(issues) > 0 {
		result.OK = false
		result.Issues = issues
		result.Code = streaming.CodePreflightFailed
		result.Message = "Preflight gagal. Perbaiki masalah di bawah sebelum memulai."
		result.Details = details
	}
	return result, details
}

// handleStreamStart validates, records and starts a session.
func (s *Server) handleStreamStart(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req startRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	// A repeated click or a second tab must produce one effective action.
	if req.IdempotencyKey != "" {
		if prior, ok, err := s.operationResult(req.IdempotencyKey); err == nil && ok {
			writeJSON(w, http.StatusAccepted, prior)
			return
		}
	}

	var endAt time.Time
	if req.EndAt != nil {
		endAt = req.EndAt.UTC()
	}
	result, details := s.preflight(r.Context(), req.RevisionID, req.Mode, req.Loop, endAt)
	if !result.OK {
		// The refusal must name what to fix, so the issue list travels with the
		// error instead of only living in the preflight response.
		details["issues"] = result.Issues
		if result.Warnings != nil {
			details["warnings"] = result.Warnings
		}
		writeErrorDetails(w, http.StatusConflict, streaming.CodePreflightFailed,
			result.Message, details)
		return
	}

	cfg, err := s.buildConfig("", req.RevisionID, req.Mode, req.Loop, endAt)
	if err != nil {
		s.writeStreamError(w, err)
		return
	}
	cfg.SessionID = newSessionID()

	if err := s.sessions.Create(repo.SessionRecord{
		ID: cfg.SessionID, Name: cfg.PlaylistName, DesiredState: "running",
		ObservedState: streaming.StateStarting, StartAt: time.Now().UTC(), EndAt: endAt,
	}); err != nil {
		s.writeStreamError(w, err)
		return
	}
	if _, err := s.worker.Start(*cfg); err != nil {
		// The slot may have been taken between preflight and start.
		_ = s.sessions.SetObservedState(cfg.SessionID, streaming.StateFailed,
			streaming.CodeOf(err), err.Error())
		s.writeStreamError(w, err)
		return
	}
	if sess != nil {
		_ = s.audit.Insert(sess.UserID, "start_stream", "session", cfg.SessionID,
			map[string]any{"playlist": cfg.PlaylistName, "mode": cfg.Mode, "loop": cfg.Loop})
	}
	s.hub.PublishStreamState(s.worker.Status())
	response := map[string]any{
		"session_id": cfg.SessionID,
		"state":      streaming.StateStarting,
		"accepted":   true,
	}
	if req.IdempotencyKey != "" {
		_ = s.recordOperation(req.IdempotencyKey, "start", cfg.SessionID, response)
	}
	writeJSON(w, http.StatusAccepted, response)
}

// handleStreamStop ends the session; stop intent wins over any pending retry.
func (s *Server) handleStreamStop(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	reason := "Dihentikan operator"
	var req struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if r.ContentLength > 0 {
		_ = decodeJSON(r, &req)
	}
	if req.IdempotencyKey != "" {
		if prior, ok, err := s.operationResult(req.IdempotencyKey); err == nil && ok {
			writeJSON(w, http.StatusOK, prior)
			return
		}
	}

	active := s.worker.Active()
	if err := s.worker.Stop(streaming.StopReason{Code: "operator", Message: reason}); err != nil {
		s.writeStreamError(w, err)
		return
	}
	// The persisted intent must also say stopped, so a restart cannot revive it.
	if rec, err := s.sessions.Latest(); err == nil && rec.DesiredState == "running" {
		_ = s.sessions.SetDesiredState(rec.ID, "stopped")
		_ = s.sessions.SetObservedState(rec.ID, streaming.StateStopping, "", "")
	}
	if sess != nil {
		_ = s.audit.Insert(sess.UserID, "stop_stream", "session", "", nil)
	}
	s.log.Info(logging.SourcePublisher, "", "Sesi dihentikan oleh %s", sess.Username)
	response := map[string]any{"ok": true, "was_active": active, "state": streaming.StateStopping}
	if req.IdempotencyKey != "" {
		_ = s.recordOperation(req.IdempotencyKey, "stop", "", response)
	}
	writeJSON(w, http.StatusOK, response)
}

// handleStreamRestart replaces the publisher process on the same session.
func (s *Server) handleStreamRestart(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if r.ContentLength > 0 {
		_ = decodeJSON(r, &req)
	}
	if err := s.worker.Restart(); err != nil {
		s.writeStreamError(w, err)
		return
	}
	if sess != nil {
		_ = s.audit.Insert(sess.UserID, "restart_stream", "session", "", nil)
	}
	response := map[string]any{"ok": true, "state": streaming.StateStarting}
	if req.IdempotencyKey != "" {
		_ = s.recordOperation(req.IdempotencyKey, "restart", "", response)
	}
	writeJSON(w, http.StatusAccepted, response)
}

// buildConfig resolves a playlist into the immutable session snapshot.
func (s *Server) buildConfig(scheduleID, revisionID, mode string, loop bool, endAt time.Time) (*streaming.Config, error) {
	playlist, err := s.resolvePlaylist(revisionID)
	if err != nil {
		return nil, streaming.Errorf(streaming.CodePlaylistEmpty,
			"Playlist tidak memiliki video yang siap diputar.")
	}
	if mode == "" {
		mode = playlist.PlaybackMode
	}
	if mode == "" {
		mode = streaming.ModeSequential
	}
	items := make([]streaming.PlaylistItem, 0, len(playlist.Items))
	var cycle float64
	for _, row := range playlist.Items {
		m, err := s.media.Get(row.MediaID)
		if err != nil {
			return nil, streaming.Errorf(streaming.CodeMediaIncompatible,
				"Media %q tidak dapat dibaca.", row.Name)
		}
		if m.FileStatus != "ready" {
			return nil, streaming.Errorf(streaming.CodeMediaIncompatible,
				"Media %q belum siap diputar (status %s).", m.OriginalName, m.FileStatus)
		}
		// Storage access is part of preflight; a missing file fails here too.
		if _, err := os.Stat(m.StoragePath); err != nil {
			return nil, streaming.Errorf(streaming.CodeMediaIncompatible,
				"File %q tidak ditemukan di penyimpanan.", m.OriginalName)
		}
		items = append(items, streaming.PlaylistItem{
			MediaID:  m.ID,
			Path:     m.StoragePath,
			Name:     m.OriginalName,
			Duration: m.Duration,
		})
		cycle += m.Duration
	}
	if len(items) == 0 {
		return nil, streaming.Errorf(streaming.CodePlaylistEmpty, "Playlist tidak berisi video.")
	}

	settings, err := s.settings.Load()
	if err != nil {
		return nil, err
	}
	target, err := s.targets.Default()
	if err != nil {
		return nil, streaming.Errorf(streaming.CodeInvalidTarget,
			"Tujuan siaran belum dikonfigurasi di Settings.")
	}
	encrypted, err := s.targets.DecryptedKey(target.ID)
	if err != nil {
		return nil, streaming.Errorf(streaming.CodeInvalidTarget, "Stream key tidak dapat dibaca.")
	}
	streamKey, err := s.secrets.Decrypt(encrypted)
	if err != nil {
		return nil, streaming.Errorf(streaming.CodeInvalidTarget, "Stream key tidak dapat didekripsi.")
	}
	if streamKey == "" {
		return nil, streaming.Errorf(streaming.CodeInvalidTarget, "Stream key belum diatur di Settings.")
	}
	// Register the live key so it is masked in logs, events and exports.
	s.log.SetRedactor(redactorWith(s.log, streamKey))

	ingest := settings.IngestURL
	if ingest == "" {
		ingest = target.IngestURL
	}
	if ingest == "" {
		return nil, streaming.Errorf(streaming.CodeInvalidTarget, "URL ingest belum diisi.")
	}
	// The key travels in the URL, so validate the destination before use.
	buildURL, err := joinIngestURL(ingest, streamKey)
	if err != nil {
		return nil, err
	}

	return &streaming.Config{
		ScheduleID:    scheduleID,
		RevisionID:    playlist.RevisionID,
		PlaylistName:  playlist.Name,
		Mode:          mode,
		Loop:          loop,
		IngestURL:     buildURL,
		ProfileID:     s.profile.ID,
		EndAt:         endAt,
		AutoReconnect: settings.AutoReconnect,
		Items:         items,
		CycleDuration: cycle,
	}, nil
}

// resolvePlaylist loads a revision, or the newest one when none is pinned.
func (s *Server) resolvePlaylist(revisionID string) (*repo.Playlist, error) {
	if revisionID != "" {
		return s.playlists.GetRevision(revisionID)
	}
	// With no revision pinned, play the newest state of the first playlist.
	list, err := s.playlists.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].LatestRevisionID != "" {
			return s.playlists.GetRevision(list[i].LatestRevisionID)
		}
	}
	return nil, store.ErrNotFound
}

// writeStreamError maps a streaming error onto its stable API code.
func (s *Server) writeStreamError(w http.ResponseWriter, err error) {
	var se *streaming.Error
	if errors.As(err, &se) {
		status := http.StatusConflict
		switch se.Code {
		case streaming.CodeInvalidTarget, streaming.CodeDependencyMissing:
			status = http.StatusUnprocessableEntity
		case streaming.CodePlaylistEmpty:
			status = http.StatusUnprocessableEntity
		}
		writeErrorDetails(w, status, se.Code, se.Message, se.Details)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Data tidak ditemukan.")
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
}

// ensureMediaDir keeps the storage directory present before a session starts.
func (s *Server) ensureMediaDir() error {
	settings, err := s.settings.Load()
	if err != nil {
		return err
	}
	dir := settings.MediaDir
	if dir == "" {
		dir = s.cfg.MediaDir
	}
	return os.MkdirAll(dir, 0o750)
}

// safeMediaPath resolves a storage path and refuses anything outside the media
// directory, which is what blocks path traversal from a crafted id.
func safeMediaPath(mediaDir, path string) (string, bool) {
	absMedia, err := filepath.Abs(mediaDir)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absMedia, absPath)
	if err != nil || len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator) {
		return "", false
	}
	if rel == ".." || (len(rel) > 3 && rel[:3] == ".."+string(filepath.Separator)) {
		return "", false
	}
	return absPath, true
}

func joinNames(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " dan " + items[1]
	default:
		out := ""
		for i, item := range items {
			switch {
			case i == len(items)-1:
				out += " dan " + item
			case i > 0:
				out += ", " + item
			default:
				out = item
			}
		}
		return out
	}
}

// newSessionID returns a fresh session identifier.
func newSessionID() string { return repo.NewID() }

// redactorWith returns a redactor that masks the live stream key. The logger
// already holds one; this adds the key without exposing it.
func redactorWith(l *logging.Logger, key string) *logging.Redactor {
	r := logging.NewRedactor()
	r.Add(key)
	l.SetRedactor(r)
	return r
}

// joinIngestURL appends the stream key to the ingest URL and re-validates the
// result, so a key can never smuggle in a different destination (PRD §10).
func joinIngestURL(ingestURL, streamKey string) (string, error) {
	if err := repo.ValidateIngestURL(ingestURL); err != nil {
		return "", streaming.Errorf(streaming.CodeInvalidTarget, "%s", err.Error())
	}
	parsed, err := url.Parse(ingestURL)
	if err != nil {
		return "", streaming.Errorf(streaming.CodeInvalidTarget, "URL ingest tidak dapat dibaca.")
	}
	// A userinfo section in the base URL would be a credential leak; strip it.
	parsed.User = nil
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/" + url.PathEscape(streamKey)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

// operationResult returns a previously recorded response for an idempotency key.
func (s *Server) operationResult(key string) (map[string]any, bool, error) {
	var result string
	err := s.store.DB().QueryRow(`SELECT result FROM operation_requests WHERE idempotency_key = ?`, key).Scan(&result)
	if err != nil {
		return nil, false, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// recordOperation stores the outcome so a repeated request returns the same
// answer instead of acting twice.
func (s *Server) recordOperation(key, action, sessionID string, response map[string]any) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = s.store.DB().Exec(`INSERT OR REPLACE INTO operation_requests
		(idempotency_key, action, session_id, status, result, created_at)
		VALUES (?,?,?,?,?,?)`,
		key, action, sessionID, "done", string(encoded),
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
