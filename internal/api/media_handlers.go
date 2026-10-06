package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/monitoring"
	"github.com/wisnu/ylivemasadi/internal/repo"
	"github.com/wisnu/ylivemasadi/internal/store"
)

// rContextTODO exists so dependency checks can run without a request context;
// the underlying tools take a bounded timeout internally.
func rContextTODO() context.Context { return context.Background() }

func listInterfaces() []string { return monitoring.ListInterfaces() }

func (s *Server) diskUsage() (any, bool) {
	settings, err := s.settings.Load()
	if err != nil {
		return nil, false
	}
	path := settings.MediaDir
	if path == "" {
		path = s.cfg.MediaDir
	}
	d, ok := monitoring.ReadDiskUsage(path)
	if !ok {
		return nil, false
	}
	return d, true
}

// handleListVideos returns the media library with search and pagination.
func (s *Server) handleListVideos(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50)
	if limit == 0 || limit > 200 {
		limit = 50
	}
	offset := queryInt(r, "offset", 0)
	list, total, err := s.media.List(r.URL.Query().Get("q"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat Video Library.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  list,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleGetVideo returns one media file with its dependencies listed.
func (s *Server) handleGetVideo(w http.ResponseWriter, r *http.Request) {
	m, err := s.media.Get(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Video tidak ditemukan.")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat detail video.")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleVideoContent streams a local file with byte-range support so the
// browser can seek during preview (FR-03).
func (s *Server) handleVideoContent(w http.ResponseWriter, r *http.Request) {
	m, err := s.media.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Video tidak ditemukan.")
		return
	}
	// A missing file is reported honestly instead of returning a broken stream.
	f, err := os.Open(m.StoragePath)
	if err != nil {
		writeError(w, http.StatusGone, "MEDIA_MISSING",
			"File video tidak ditemukan di penyimpanan. Tandai sebagai hilang dan unggah ulang.")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal membaca file.")
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentTypeFor(m.StoragePath))
	// The original name is user data and must reach the client escaped.
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`inline; filename="%s"`, escapeFilename(m.OriginalName)))
	http.ServeContent(w, r, m.OriginalName, stat.ModTime(), f)
}

// handleThumbnail serves the generated preview image.
func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	m, err := s.media.Get(r.PathValue("id"))
	if err != nil || m.ThumbnailPath == "" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Thumbnail belum tersedia.")
		return
	}
	f, err := os.Open(m.ThumbnailPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Thumbnail belum tersedia.")
		return
	}
	defer f.Close()
	stat, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	// Thumbnails change only when the file is reprocessed.
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, "thumb.jpg", stat.ModTime(), f)
}

// handleDeleteVideo removes a file once nothing references it (AC-20).
func (s *Server) handleDeleteVideo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := sessionFrom(r.Context())
	m, err := s.media.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Video tidak ditemukan.")
		return
	}

	// A live session holds the file open; refuse before anything else.
	if active, ok := s.media.ActiveSessionUsing(id); ok {
		writeErrorDetails(w, http.StatusConflict, "MEDIA_IN_USE",
			"Video sedang dipakai oleh sesi yang sedang berjalan.",
			map[string]any{"usage": []repo.MediaUsage{{Kind: "session", ID: active, Name: "Sesi aktif"}}})
		return
	}
	if len(m.Usage) > 0 {
		writeErrorDetails(w, http.StatusConflict, "MEDIA_IN_USE",
			"Video masih dipakai playlist atau jadwal. Hapus referensinya terlebih dahulu.",
			map[string]any{"usage": m.Usage})
		return
	}

	if err := s.media.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal menghapus data video.")
		return
	}
	// Metadata is gone, so the bytes can be removed too.
	if err := os.Remove(m.StoragePath); err != nil && !os.IsNotExist(err) {
		s.log.Warn(logging.SourceApp, "", "Gagal menghapus file %s: %v", m.OriginalName, err)
	}
	if m.ThumbnailPath != "" {
		_ = os.Remove(m.ThumbnailPath)
	}
	if sess != nil {
		_ = s.audit.Insert(sess.UserID, "delete_media", "media", id, map[string]any{"name": m.OriginalName})
	}
	s.log.Info(logging.SourceApp, "", "Video dihapus: %s", m.OriginalName)
	s.hub.PublishMedia(map[string]any{"action": "deleted", "id": id})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRecheckVideo re-queues a file for probing and validation.
func (s *Server) handleRecheckVideo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.media.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Video tidak ditemukan.")
		return
	}
	if err := s.processor.Enqueue(id, m.StoragePath, "", false); err != nil {
		writeError(w, http.StatusServiceUnavailable, "QUEUE_FULL", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": "processing"})
}

// --- uploads ---

type createUploadRequest struct {
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// handleCreateUpload reserves disk space and opens a resumable session.
func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	var req createUploadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	session, err := s.uploader.Create(r.Context(), sess.UserID, req.Filename, req.Size)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrFileTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", err.Error())
		case errors.Is(err, repo.ErrStorageFull):
			writeError(w, http.StatusInsufficientStorage, "INSUFFICIENT_STORAGE", err.Error())
		default:
			writeError(w, http.StatusBadRequest, "UPLOAD_REJECTED", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"upload_id":   session.ID,
		"part_size":   session.PartSize,
		"total_parts": totalParts(session.ExpectedSize, session.PartSize),
		"expires_at":  session.ExpiresAt,
	})
}

// handleUploadStatus reports progress so a client can resume (FR-03).
func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	session, err := s.uploads.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Sesi upload tidak ditemukan.")
		return
	}
	parts, err := s.uploads.ListParts(session.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal membaca bagian upload.")
		return
	}
	percent := 0.0
	if session.ExpectedSize > 0 {
		percent = float64(session.ReceivedBytes) / float64(session.ExpectedSize) * 100
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"upload_id":      session.ID,
		"filename":       session.OriginalName,
		"status":         session.Status,
		"expected_size":  session.ExpectedSize,
		"received_bytes": session.ReceivedBytes,
		"percent":        percent,
		"received_parts": parts,
		"media_id":       session.MediaID,
		"error":          session.Error,
	})
}

// handleUploadPart writes one chunk at its declared offset.
func (s *Server) handleUploadPart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	index, err := strconv.Atoi(r.PathValue("part"))
	if err != nil || index < 0 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Nomor bagian tidak valid.")
		return
	}
	offsetHeader := r.Header.Get("X-Part-Offset")
	var offset int64
	if offsetHeader != "" {
		offset, err = strconv.ParseInt(offsetHeader, 10, 64)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Offset bagian tidak valid.")
			return
		}
	} else {
		// Fall back to the session's part size when the header is absent.
		session, sErr := s.uploads.Get(id)
		if sErr != nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Sesi upload tidak ditemukan.")
			return
		}
		offset = int64(index) * session.PartSize
	}

	written, err := s.uploader.WritePart(r.Context(), id, index, offset, r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "UPLOAD_REJECTED", err.Error())
		return
	}
	session, _ := s.uploads.Get(id)
	percent := 0.0
	if session != nil && session.ExpectedSize > 0 {
		percent = float64(session.ReceivedBytes) / float64(session.ExpectedSize) * 100
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"part":           index,
		"bytes":          written,
		"received_bytes": session.ReceivedBytes,
		"percent":        percent,
	})
}

// handleCompleteUpload verifies completeness, promotes the temp file to its
// final name atomically, and queues it for checking (FR-03).
func (s *Server) handleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	id := r.PathValue("id")
	session, err := s.uploads.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Sesi upload tidak ditemukan.")
		return
	}
	if session.Status != repo.UploadActive {
		writeError(w, http.StatusConflict, "UPLOAD_NOT_ACTIVE",
			fmt.Sprintf("Upload sudah berstatus %s.", session.Status))
		return
	}
	if session.ReceivedBytes != session.ExpectedSize {
		writeErrorDetails(w, http.StatusBadRequest, "UPLOAD_INCOMPLETE",
			"Ukuran file belum lengkap.",
			map[string]any{"expected": session.ExpectedSize, "received": session.ReceivedBytes})
		return
	}

	// Space must still be available for the final copy plus its thumbnail.
	if err := s.uploader.VerifyFinalSpace(session.ExpectedSize); err != nil {
		writeError(w, http.StatusInsufficientStorage, "INSUFFICIENT_STORAGE", err.Error())
		return
	}

	mediaID, finalPath, err := s.uploader.Promote(session, s.cfg.ThumbnailDir)
	if err != nil {
		_ = s.uploads.SetStatus(id, repo.UploadFailed, err.Error())
		writeError(w, http.StatusInternalServerError, "UPLOAD_FAILED", err.Error())
		return
	}
	_ = s.uploads.SetMediaID(id, mediaID)
	_ = s.uploads.SetStatus(id, repo.UploadProcessing, "")

	if err := s.processor.Enqueue(mediaID, finalPath, id, false); err != nil {
		// The file is stored and visible; only the check is deferred.
		s.log.Warn(logging.SourceApp, "", "Antrean pemeriksaan penuh untuk %s: %v", session.OriginalName, err)
	}
	if sess != nil {
		_ = s.audit.Insert(sess.UserID, "upload_media", "media", mediaID,
			map[string]any{"name": session.OriginalName, "size": session.ExpectedSize})
	}
	s.hub.PublishMedia(map[string]any{"action": "processing", "id": mediaID})
	writeJSON(w, http.StatusAccepted, map[string]any{"media_id": mediaID, "status": "processing"})
}

// handleCancelUpload discards an in-flight upload and its temp bytes.
func (s *Server) handleCancelUpload(w http.ResponseWriter, r *http.Request) {
	if err := s.uploader.Cancel(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Sesi upload tidak ditemukan.")
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal membatalkan upload.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func totalParts(size, partSize int64) int64 {
	if size <= 0 || partSize <= 0 {
		return 0
	}
	n := size / partSize
	if size%partSize != 0 {
		n++
	}
	return n
}

// contentTypeFor maps a container extension to a MIME type. An unknown
// extension downloads rather than guessing a playable type.
func contentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	default:
		return "application/octet-stream"
	}
}

// escapeFilename makes a name safe for a Content-Disposition header.
func escapeFilename(name string) string {
	replacer := strings.NewReplacer(`"`, "", `\`, "", "\r", "", "\n", "")
	cleaned := replacer.Replace(name)
	if cleaned == "" {
		cleaned = "video"
	}
	return cleaned
}

// formatBytes renders a byte count for operator-facing messages.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit && exp < 4; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div),
		[]string{"KB", "MB", "GB", "TB", "PB"}[exp])
}

// readLimited discards at most n bytes, used to bound diagnostic reads.
func readLimited(r io.Reader, n int64) []byte {
	buf := make([]byte, n)
	read, _ := io.ReadFull(r, buf)
	return buf[:read]
}

// Compatibility re-exports so handlers do not import the media package.
type Compatibility = media.Compatibility

var _ = time.Second
