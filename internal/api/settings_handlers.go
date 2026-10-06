package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/repo"
)

// --- logs ---

// handleLogs returns filtered log records. Only the active timezone is applied
// for display; stored timestamps stay in UTC.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := logging.LogFilter{
		Levels:    splitCSV(q.Get("levels")),
		Sources:   splitCSV(q.Get("sources")),
		SessionID: q.Get("session_id"),
		Search:    q.Get("q"),
		Limit:     queryInt(r, "limit", 200),
	}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Since = t
		}
	}
	entries, err := s.logs.Query(filter, filter.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal membaca log.")
		return
	}
	// Recent in-memory lines are merged in so a live publisher appears even
	// before its records are indexed.
	live := s.log.Recent(filter, 50)
	if len(live) > 0 {
		entries = mergeEntries(entries, live)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"count":   len(entries),
		"sources": []string{logging.SourceApp, logging.SourceScheduler, logging.SourcePublisher, logging.SourceAudit, logging.SourceUpload},
		"levels":  []string{"debug", "info", "warn", "error"},
	})
}

// handleLogExport streams sanitized plain text for download (FR-09).
func (s *Server) handleLogExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := logging.LogFilter{
		Levels:  splitCSV(q.Get("levels")),
		Sources: splitCSV(q.Get("sources")),
		Search:  q.Get("q"),
		Limit:   queryInt(r, "limit", 2000),
	}
	entries, err := s.logs.Query(filter, filter.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal mengekspor log.")
		return
	}
	loc := s.displayLocation()
	filename := fmt.Sprintf("ytlive-log-%s.txt", time.Now().Format("20060102-150405"))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	// Entries are already redacted at write time; masking again is cheap
	// insurance for anything written before a key rotation.
	redactor := logging.NewRedactor()
	if key, err := s.currentStreamKey(); err == nil && key != "" {
		redactor.Add(key)
	}
	for _, e := range entries {
		e.Message = redactor.String(e.Message)
		if err := logging.WriteExport(w, []logging.Entry{e}, loc); err != nil {
			return
		}
	}
}

// handleClearLogs empties the browser view only; server history is untouched
// and is removed through Settings retention rules (FR-09).
func (s *Server) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	s.hub.PublishAlert(map[string]any{
		"kind":    "logs_cleared",
		"message": "Tampilan log dibersihkan. Riwayat server tetap tersimpan.",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"note": "Clear Log hanya mengosongkan tampilan sesi ini.",
	})
}

// handleAudit returns the operator action trail.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	events, err := s.audit.List(queryInt(r, "limit", 100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat audit.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": events})
}

// --- settings ---

// handleGetSettings returns settings with the stream key masked (FR-10).
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.settings.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat pengaturan.")
		return
	}
	payload := map[string]any{"settings": settings}
	if target, err := s.targets.Default(); err == nil && target != nil {
		payload["target"] = target
		payload["stream_key_set"] = target.StreamKeySet
	}
	payload["defaults"] = map[string]any{
		"ingest_url": repo.DefaultIngestURL,
		"profile_id": repo.DefaultProfileID,
	}
	payload["profiles"] = s.profile
	writeJSON(w, http.StatusOK, payload)
}

type settingsRequest struct {
	Settings repo.Settings `json:"settings"`
	// StreamKey is write-only: the API never returns the stored value (FR-10).
	StreamKey string `json:"stream_key"`
}

// handleSaveSettings persists settings and optionally rotates the stream key.
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req settingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	// The ingest URL is validated before it is stored, so an unusable value is
	// rejected at configuration time rather than at broadcast time (PRD §10).
	if req.Settings.IngestURL != "" {
		if err := repo.ValidateIngestURL(req.Settings.IngestURL); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "INVALID_TARGET", err.Error())
			return
		}
	}
	if req.Settings.Timezone != "" {
		if _, err := time.LoadLocation(req.Settings.Timezone); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "VALIDATION",
				fmt.Sprintf("Zona waktu %q tidak dikenali.", req.Settings.Timezone))
			return
		}
	}

	// Moving the media directory is rejected while media work is in flight, so
	// an upload or a live send cannot end up writing to a moved volume (FR-10).
	if req.Settings.MediaDir != "" {
		current, err := s.settings.Load()
		if err == nil && current.MediaDir != "" && current.MediaDir != req.Settings.MediaDir {
			if s.processor.Pending() > 0 || s.worker.Active() {
				writeError(w, http.StatusConflict, "MEDIA_BUSY",
					"Lokasi media tidak dapat diubah saat ada upload atau siaran berjalan.")
				return
			}
			if err := checkDirWritable(req.Settings.MediaDir); err != nil {
				writeError(w, http.StatusUnprocessableEntity, "STORAGE_UNAVAILABLE", err.Error())
				return
			}
			s.uploader.SetMediaDir(req.Settings.MediaDir)
		}
	}

	if err := s.settings.Save(req.Settings); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal menyimpan pengaturan.")
		return
	}
	if req.StreamKey != "" {
		if err := s.setStreamKey(req.StreamKey); err != nil {
			s.writeTargetError(w, err)
			return
		}
	}
	_ = s.audit.Insert(sess.UserID, "update_settings", "settings", "", nil)
	s.log.Info(logging.SourceApp, "", "Pengaturan diperbarui oleh %s", sess.Username)

	// Report what the user asked for without echoing the secret.
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"stream_key_set": req.StreamKey != "" || s.streamKeyConfigured(),
	})
}

// handleDependencies reports installed tools so the UI can disable Start.
func (s *Server) handleDependencies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.dependencyStatus())
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mergeEntries combines indexed and in-memory records, newest last, dropping
// duplicates that appear in both.
func mergeEntries(indexed, live []logging.Entry) []logging.Entry {
	seen := make(map[string]struct{}, len(indexed))
	out := make([]logging.Entry, 0, len(indexed)+len(live))
	for _, e := range indexed {
		key := e.Time.UTC().Format(time.RFC3339Nano) + e.Level + e.Source + e.Message
		seen[key] = struct{}{}
		out = append(out, e)
	}
	for _, e := range live {
		key := e.Time.UTC().Format(time.RFC3339Nano) + e.Level + e.Source + e.Message
		if _, dup := seen[key]; dup {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (s *Server) displayLocation() *time.Location {
	settings, err := s.settings.Load()
	if err != nil || settings.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (s *Server) streamKeyConfigured() bool {
	target, err := s.targets.Default()
	return err == nil && target != nil && target.StreamKeySet
}

func (s *Server) currentStreamKey() (string, error) {
	target, err := s.targets.Default()
	if err != nil || target == nil {
		return "", err
	}
	encrypted, err := s.targets.DecryptedKey(target.ID)
	if err != nil {
		return "", err
	}
	return s.secrets.Decrypt(encrypted)
}

func (s *Server) writeTargetError(w http.ResponseWriter, err error) {
	if errors.Is(err, repo.ErrInvalidTarget) {
		writeError(w, http.StatusUnprocessableEntity, "INVALID_TARGET", err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
}

// parseIntDefault reads an integer query parameter with a fallback.
func parseIntDefault(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// checkDirWritable verifies a directory exists or can be created and accepts a
// write, so an unusable media path is rejected before it is saved.
func checkDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("direktori media tidak dapat dibuat: %w", err)
	}
	probe := filepath.Join(dir, ".ytlive-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("direktori media tidak dapat ditulis: %w", err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}
