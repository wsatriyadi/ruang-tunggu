package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/wisnu/ylivemasadi/internal/logging"
)

// handleAuthState tells the frontend whether setup, login or the panel is next.
// It never reveals whether a specific username exists.
func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.auth.HasAdmin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memeriksa status instalasi.")
		return
	}
	state := "login"
	switch {
	case !hasAdmin:
		state = "setup"
	default:
		if _, err := s.auth.Lookup(r); err == nil {
			state = "authenticated"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state":   state,
		"version": s.version,
		"app":     "YouTube Live Server",
	})
}

type setupRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirm_password"`
	InstanceName    string `json:"instance_name"`
	Timezone        string `json:"timezone"`
	MediaDir        string `json:"media_dir"`
	NetInterface    string `json:"net_interface"`
	IngestURL       string `json:"ingest_url"`
	StreamKey       string `json:"stream_key"`
	PreviewVideoID  string `json:"preview_video_id"`
}

// handleSetup creates the single administrator. It succeeds only while no
// account exists, so the flow cannot be replayed (FR-01).
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Password != req.ConfirmPassword {
		writeError(w, http.StatusBadRequest, "PASSWORD_MISMATCH", "Konfirmasi kata sandi tidak sama.")
		return
	}
	if err := s.auth.CreateAdmin(req.Username, req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "SETUP_FAILED", err.Error())
		return
	}
	s.log.Info(logging.SourceApp, "", "Instalasi selesai, akun administrator dibuat")

	// Apply the initial settings so the operator does not have to re-enter them.
	if err := s.applySetupSettings(req); err != nil {
		s.log.Warn(logging.SourceApp, "", "Sebagian pengaturan awal gagal disimpan: %v", err)
	}
	s.issueSession(w, r, req.Username)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin authenticates and issues a session cookie plus a CSRF token.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	userID, err := s.auth.Authenticate(strings.TrimSpace(req.Username), req.Password)
	if err != nil {
		s.log.Warn(logging.SourceApp, "", "Login gagal untuk %q", req.Username)
		writeError(w, http.StatusUnauthorized, "LOGIN_FAILED",
			"Nama pengguna atau kata sandi salah, atau percobaan terlalu sering.")
		return
	}
	sess, err := s.auth.CreateSession(userID, r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal membuat sesi.")
		return
	}
	s.auth.AttachSession(w, sess)
	s.log.Info(logging.SourceApp, "", "Login berhasil: %s", req.Username)
	_ = s.audit.Insert(int64(userID), "login", "user", "", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   sess.Username,
		"csrf_token": sess.CSRFToken,
	})
}

// issueSession creates and attaches a session for the given user.
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, username string) {
	var userID int64
	if err := s.store.DB().QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&userID); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Akun tidak ditemukan.")
		return
	}
	sess, err := s.auth.CreateSession(userID, r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal membuat sesi.")
		return
	}
	s.auth.AttachSession(w, sess)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   sess.Username,
		"csrf_token": sess.CSRFToken,
	})
}

// handleLogout ends the browser session. It never touches the worker, so an
// active broadcast keeps running (FR-01).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess := sessionFrom(r.Context()); sess != nil {
		if err := s.auth.Logout(r); err != nil {
			s.log.Warn(logging.SourceApp, "", "Gagal menghapus sesi: %v", err)
		}
		_ = s.audit.Insert(sess.UserID, "logout", "user", "", nil)
	}
	s.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type passwordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

// handleChangePassword rotates the password and invalidates other sessions.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if sess == nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Sesi tidak valid.")
		return
	}
	var req passwordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		writeError(w, http.StatusBadRequest, "PASSWORD_MISMATCH", "Konfirmasi kata sandi tidak sama.")
		return
	}
	if err := s.auth.ChangePassword(sess.UserID, req.CurrentPassword, req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, "PASSWORD_CHANGE_FAILED", err.Error())
		return
	}
	// Every other browser is logged out; the current one keeps working.
	if err := s.auth.DestroyAllSessions(sess.Token); err != nil {
		s.log.Warn(logging.SourceApp, "", "Gagal membersihkan sesi lain: %v", err)
	}
	_ = s.audit.Insert(sess.UserID, "change_password", "user", "", nil)
	s.log.Info(logging.SourceApp, "", "Kata sandi diubah oleh %s", sess.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// applySetupSettings stores the values collected during first-run setup.
func (s *Server) applySetupSettings(req setupRequest) error {
	settings, err := s.settings.Load()
	if err != nil {
		return err
	}
	if req.InstanceName != "" {
		settings.InstanceName = req.InstanceName
	}
	if req.Timezone != "" {
		settings.Timezone = req.Timezone
	}
	if req.NetInterface != "" {
		settings.NetInterface = req.NetInterface
	}
	if req.IngestURL != "" {
		settings.IngestURL = req.IngestURL
	}
	if req.PreviewVideoID != "" {
		settings.PreviewVideoID = req.PreviewVideoID
	}
	if err := s.settings.Save(*settings); err != nil {
		return err
	}
	if req.StreamKey != "" {
		// The key is encrypted with a key held outside the database.
		return s.setStreamKey(req.StreamKey)
	}
	return nil
}

// errNoSession reports a missing authenticated session.
var errNoSession = errors.New("no session")

// setStreamKey encrypts the key with the application key and stores it, so the
// plaintext never touches the database.
func (s *Server) setStreamKey(plaintext string) error {
	encrypted, err := s.secrets.Encrypt(plaintext)
	if err != nil {
		return err
	}
	if err := s.targets.SetStreamKey("default", encrypted); err != nil {
		return err
	}
	s.log.SetRedactor(redactorWith(s.log, plaintext))
	return nil
}
