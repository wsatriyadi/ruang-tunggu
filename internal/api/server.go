package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/auth"
	"github.com/wisnu/ylivemasadi/internal/config"
	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/monitoring"
	"github.com/wisnu/ylivemasadi/internal/repo"
	"github.com/wisnu/ylivemasadi/internal/secrets"
	"github.com/wisnu/ylivemasadi/internal/store"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// Server wires the repositories, worker and hub behind the HTTP API.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	auth    *auth.Service
	secrets *secrets.Box
	log     *logging.Logger
	hub     *Hub
	monitor *monitoring.Monitor
	profile media.Profile

	media     *repo.MediaRepo
	uploads   *repo.UploadRepo
	uploader  *repo.UploadManager
	playlists *repo.PlaylistRepo
	schedules *repo.ScheduleRepo
	sessions  *repo.SessionRepo
	settings  *repo.SettingsRepo
	targets   *repo.TargetRepo
	logs      *repo.LogRepo
	audit     *repo.AuditRepo

	worker    *streaming.Worker
	processor MediaProcessor
	binaries  *media.Binaries
	// version is stamped at build time and reported to the panel header.
	version string

	frontendDir string
}

// MediaProcessor is the media pipeline the API enqueues work into.
type MediaProcessor interface {
	Enqueue(mediaID, storagePath, uploadID string, skipThumbnails bool) error
	// Pending reports queued work so a media directory change can be refused.
	Pending() int
}

// Deps carries everything the API needs, assembled by main.
type Deps struct {
	Cfg       *config.Config
	Store     *store.Store
	Auth      *auth.Service
	Secrets   *secrets.Box
	Log       *logging.Logger
	Hub       *Hub
	Monitor   *monitoring.Monitor
	Profile   media.Profile
	Media     *repo.MediaRepo
	Uploads   *repo.UploadRepo
	Uploader  *repo.UploadManager
	Playlists *repo.PlaylistRepo
	Schedules *repo.ScheduleRepo
	Sessions  *repo.SessionRepo
	Settings  *repo.SettingsRepo
	Targets   *repo.TargetRepo
	Logs      *repo.LogRepo
	Audit     *repo.AuditRepo
	Worker    *streaming.Worker
	Processor MediaProcessor
	Binaries  *media.Binaries
	Version   string
}

// NewServer builds the API server.
func NewServer(d Deps) *Server {
	return &Server{
		cfg: d.Cfg, store: d.Store, auth: d.Auth, secrets: d.Secrets, log: d.Log,
		hub: d.Hub, monitor: d.Monitor, profile: d.Profile,
		media: d.Media, uploads: d.Uploads, uploader: d.Uploader,
		playlists: d.Playlists, schedules: d.Schedules, sessions: d.Sessions,
		settings: d.Settings, targets: d.Targets, logs: d.Logs, audit: d.Audit,
		worker: d.Worker, processor: d.Processor, binaries: d.Binaries,
		version: d.Version, frontendDir: d.Cfg.FrontendDir,
	}
}

// Handler builds the HTTP router with all middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- Unauthenticated: setup and login only (FR-01) ---
	mux.HandleFunc("GET /api/auth/state", s.handleAuthState)
	mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)

	// --- Authenticated ---
	mux.Handle("GET /api/events", s.requireAuth(s.handleEvents))
	mux.Handle("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	mux.Handle("POST /api/auth/password", s.requireCSRF(s.handleChangePassword))

	mux.Handle("GET /api/dashboard", s.requireAuth(s.handleDashboard))
	mux.Handle("GET /api/system", s.requireAuth(s.handleSystem))

	mux.Handle("POST /api/uploads", s.requireCSRF(s.handleCreateUpload))
	mux.Handle("GET /api/uploads/{id}", s.requireAuth(s.handleUploadStatus))
	mux.Handle("PUT /api/uploads/{id}/parts/{part}", s.requireCSRF(s.handleUploadPart))
	mux.Handle("POST /api/uploads/{id}/complete", s.requireCSRF(s.handleCompleteUpload))
	mux.Handle("DELETE /api/uploads/{id}", s.requireCSRF(s.handleCancelUpload))

	mux.Handle("GET /api/videos", s.requireAuth(s.handleListVideos))
	mux.Handle("GET /api/videos/{id}", s.requireAuth(s.handleGetVideo))
	mux.Handle("GET /api/videos/{id}/content", s.requireAuth(s.handleVideoContent))
	mux.Handle("GET /api/videos/{id}/thumbnail", s.requireAuth(s.handleThumbnail))
	mux.Handle("DELETE /api/videos/{id}", s.requireCSRF(s.handleDeleteVideo))
	mux.Handle("POST /api/videos/{id}/recheck", s.requireCSRF(s.handleRecheckVideo))

	mux.Handle("GET /api/playlists", s.requireAuth(s.handleListPlaylists))
	mux.Handle("POST /api/playlists", s.requireCSRF(s.handleCreatePlaylist))
	mux.Handle("PUT /api/playlists/{id}", s.requireCSRF(s.handleUpdatePlaylist))
	mux.Handle("DELETE /api/playlists/{id}", s.requireCSRF(s.handleDeletePlaylist))

	mux.Handle("GET /api/schedules", s.requireAuth(s.handleListSchedules))
	mux.Handle("POST /api/schedules", s.requireCSRF(s.handleCreateSchedule))
	mux.Handle("GET /api/schedules/{id}", s.requireAuth(s.handleGetSchedule))
	mux.Handle("PATCH /api/schedules/{id}", s.requireCSRF(s.handleUpdateSchedule))
	mux.Handle("DELETE /api/schedules/{id}", s.requireCSRF(s.handleDeleteSchedule))

	mux.Handle("POST /api/stream/start", s.requireCSRF(s.handleStreamStart))
	mux.Handle("POST /api/stream/stop", s.requireCSRF(s.handleStreamStop))
	mux.Handle("POST /api/stream/restart", s.requireCSRF(s.handleStreamRestart))
	mux.Handle("GET /api/stream/status", s.requireAuth(s.handleStreamStatus))
	mux.Handle("POST /api/stream/preflight", s.requireCSRF(s.handlePreflight))

	mux.Handle("GET /api/logs", s.requireAuth(s.handleLogs))
	mux.Handle("GET /api/logs/export", s.requireAuth(s.handleLogExport))
	mux.Handle("POST /api/logs/clear", s.requireCSRF(s.handleClearLogs))

	mux.Handle("GET /api/settings", s.requireAuth(s.handleGetSettings))
	mux.Handle("PUT /api/settings", s.requireCSRF(s.handleSaveSettings))
	mux.Handle("GET /api/settings/deps", s.requireAuth(s.handleDependencies))
	mux.Handle("POST /api/settings/password", s.requireCSRF(s.handleChangePassword))
	mux.Handle("GET /api/audit", s.requireAuth(s.handleAudit))

	// Frontend assets are served last so API paths always win.
	mux.Handle("/", s.staticHandler())

	return s.withRecovery(s.withLogging(mux))
}

// --- middleware ---

type ctxKey int

const ctxSessionKey ctxKey = iota

// sessionFrom returns the authenticated session attached by requireAuth.
func sessionFrom(ctx context.Context) *auth.Session {
	s, _ := ctx.Value(ctxSessionKey).(*auth.Session)
	return s
}

// requireAuth rejects unauthenticated requests before they reach any handler,
// so media, logs and credentials are unreachable without a session (AC-01).
func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.auth.Lookup(r)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Sesi tidak valid atau sudah berakhir.")
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memverifikasi sesi.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxSessionKey, sess)))
	})
}

// requireCSRF authenticates and then validates the X-CSRF-Token header on
// mutations. It performs the session check itself rather than relying on route
// registration order, so a mutation can never reach a handler unauthenticated.
//
// The cookie is SameSite=Strict; this header additionally guards against a
// same-origin script or a cross-site form post that rides the session.
func (s *Server) requireCSRF(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.auth.Lookup(r)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED",
					"Sesi tidak valid atau sudah berakhir.")
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memverifikasi sesi.")
			return
		}
		if !sess.ValidCSRF(r.Header.Get("X-CSRF-Token")) {
			writeError(w, http.StatusForbidden, "CSRF_INVALID", "Token CSRF tidak valid. Muat ulang halaman.")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxSessionKey, sess))
		next(w, r)
	})
}

// withLogging records each request and its outcome.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Media content can be large; keep routine asset hits out of the log.
		if strings.HasPrefix(r.URL.Path, "/api/") && !isContentPath(r.URL.Path) {
			s.log.Debug(logging.SourceApp, sessionID(r), "%s %s -> %d (%s)",
				r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so SSE streaming still works.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// withRecovery converts a panic into a 500 instead of killing the process, so
// one bad request cannot take down a running broadcast.
func (s *Server) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error(logging.SourceApp, "", "Panic pada %s %s: %v", r.Method, r.URL.Path, rec)
				writeError(w, http.StatusInternalServerError, "INTERNAL",
					"Terjadi kesalahan pada server.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func isContentPath(p string) bool {
	return strings.HasSuffix(p, "/content") || strings.HasSuffix(p, "/thumbnail")
}

func sessionID(r *http.Request) string {
	if sess := sessionFrom(r.Context()); sess != nil {
		return sess.Username
	}
	return ""
}

// --- helpers ---

// apiError is the stable error envelope (PRD §13).
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeErrorDetails(w, status, code, message, nil)
}

func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Error: apiErrorBody{
		Code: code, Message: message, Details: details,
	}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The status line is already sent at this point, so an encoding failure
	// can only be logged; the client sees a truncated body.
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads a JSON body with a size limit.
func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("body JSON tidak valid: %w", err)
	}
	return nil
}

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// staticHandler serves the built frontend, falling back to index.html so a
// client-side route survives a page reload.
func (s *Server) staticHandler() http.Handler {
	root := s.frontendDir
	if root == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Frontend belum dibangun.")
		})
	}
	// Resolve the root once. Comparing an absolute candidate path against a
	// relative root makes the containment check below unreliable, which is how
	// assets ended up being answered with the SPA fallback.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	fileServer := http.FileServer(http.Dir(absRoot))
	index := filepath.Join(absRoot, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "." || clean == string(filepath.Separator) {
			clean = "index.html"
		}
		full := filepath.Join(absRoot, clean)

		// Confine every response to the build directory so a crafted path cannot
		// escape it and read arbitrary files (PRD 10).
		rel, relErr := filepath.Rel(absRoot, full)
		contained := relErr == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator))
		if contained {
			if info, statErr := os.Stat(full); statErr == nil && !info.IsDir() {
				if strings.HasPrefix(filepath.ToSlash(clean), "assets/") {
					// Vite fingerprints these filenames, so they are immutable.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Anything else is a client-side route.
		http.ServeFile(w, r, index)
	})
}
