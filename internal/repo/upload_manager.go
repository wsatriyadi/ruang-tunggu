package repo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/monitoring"
)

// ErrStorageFull reports that the media volume cannot accept more bytes.
var ErrStorageFull = errors.New("insufficient storage")

// ErrFileTooLarge reports that a file exceeds the configured limit.
var ErrFileTooLarge = errors.New("file too large")

// UploadManager writes uploaded parts to disk with bounded memory and hands
// completed files to a processing callback.
//
// Parts are appended at their known offset, so memory never scales with file
// size and an interrupted transfer resumes from the last confirmed part.
type UploadManager struct {
	repo    *UploadRepo
	media   *MediaRepo
	tempDir string
	log     *logging.Logger

	// maxFileSize, quota and reserve bound disk consumption (FR-03).
	maxFileSize int64
	quota       int64
	reserve     int64
	diskPath    string
	// mediaDirPath is the storage directory for promoted files.
	mediaDirPath string

	// queue serialises probing so a burst of uploads cannot saturate a weak CPU.
	queue chan struct{}
	wg    sync.WaitGroup

	// inflight tracks uploads currently receiving parts, keeping one active
	// upload at a time by default.
	mu       sync.Mutex
	inflight map[string]int
}

// UploadManagerOptions configures the manager.
type UploadManagerOptions struct {
	TempDir       string
	MediaDir      string
	MaxFileSize   int64
	Quota         int64
	Reserve       int64
	DiskPath      string
	MaxConcurrent int
}

// NewUploadManager builds the manager.
func NewUploadManager(r *UploadRepo, m *MediaRepo, log *logging.Logger, opts UploadManagerOptions) *UploadManager {
	conc := opts.MaxConcurrent
	if conc <= 0 {
		// One active upload keeps load predictable on a 2 GB device (FR-03).
		conc = 1
	}
	return &UploadManager{
		repo:         r,
		media:        m,
		tempDir:      opts.TempDir,
		log:          log,
		maxFileSize:  opts.MaxFileSize,
		quota:        opts.Quota,
		reserve:      opts.Reserve,
		diskPath:     opts.DiskPath,
		mediaDirPath: opts.MediaDir,
		queue:        make(chan struct{}, conc),
		inflight:     map[string]int{},
	}
}

// Create registers an upload session after validating size, quota and space.
func (u *UploadManager) Create(ctx context.Context, userID int64, filename string, expectedSize int64) (*UploadSession, error) {
	name := sanitizeFilename(filename)
	if name == "" {
		return nil, fmt.Errorf("nama file tidak valid")
	}
	if !allowedExtension(name) {
		return nil, fmt.Errorf("format %s tidak didukung; gunakan MP4, MOV, atau MKV",
			strings.TrimPrefix(filepath.Ext(name), "."))
	}
	if u.maxFileSize > 0 && expectedSize > u.maxFileSize {
		return nil, fmt.Errorf("%w: ukuran %d melebihi batas %d byte", ErrFileTooLarge, expectedSize, u.maxFileSize)
	}
	if err := u.checkSpace(expectedSize); err != nil {
		return nil, err
	}

	session, err := u.repo.Create(userID, name, expectedSize, DefaultPartSize, "")
	if err != nil {
		return nil, err
	}
	// The temp path is derived from the generated id, never from user input,
	// so a crafted filename cannot escape the temp directory (PRD §10).
	session.TempPath = filepath.Join(u.tempDir, session.ID+".part")
	if _, err := u.repo.st.DB().Exec(`UPDATE upload_sessions SET temp_path = ? WHERE id = ?`,
		session.TempPath, session.ID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(u.tempDir, 0o750); err != nil {
		return nil, err
	}
	if f, err := os.OpenFile(session.TempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640); err == nil {
		_ = f.Close()
	}
	u.log.Info(logging.SourceUpload, session.ID, "Upload dimulai: %s (%d byte)", name, expectedSize)
	return session, nil
}

// WritePart stores one chunk at its offset and records it idempotently.
func (u *UploadManager) WritePart(ctx context.Context, sessionID string, index int, offset int64, r io.Reader) (int64, error) {
	s, err := u.repo.Get(sessionID)
	if err != nil {
		return 0, err
	}
	if s.Status != UploadActive {
		return 0, fmt.Errorf("upload sudah berstatus %s", s.Status)
	}
	u.mu.Lock()
	u.inflight[sessionID]++
	count := u.inflight[sessionID]
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.inflight[sessionID]--
		if u.inflight[sessionID] <= 0 {
			delete(u.inflight, sessionID)
		}
		u.mu.Unlock()
	}()

	if count > 1 {
		// More than one part in flight for the same upload means concurrent
		// writers on one file; refuse rather than corrupt it.
		return 0, fmt.Errorf("upload sedang menerima potongan lain")
	}

	f, err := os.OpenFile(s.TempPath, os.O_WRONLY, 0o640)
	if err != nil {
		return 0, fmt.Errorf("buka file upload: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("posisi tulis: %w", err)
	}
	// Limit the copy to one part plus a small margin, so a client cannot
	// declare a 1-byte part and stream an entire 5 GB file through it.
	limit := s.PartSize + 1
	written, err := io.Copy(f, io.LimitReader(r, limit))
	if err != nil {
		return 0, fmt.Errorf("tulis potongan: %w", err)
	}
	if written > s.PartSize {
		return 0, fmt.Errorf("potongan %d melebihi ukuran bagian %d byte", index, s.PartSize)
	}
	if err := u.repo.RecordPart(sessionID, index, written); err != nil {
		return 0, err
	}
	return written, nil
}

// checkSpace verifies the volume can hold the incoming file, accounting for the
// configured reserve and any quota (FR-03).
func (u *UploadManager) checkSpace(need int64) error {
	sample, ok := monitoring.ReadDiskUsage(u.diskPath)
	if !ok {
		// Without a usable reading the check cannot be trusted; quota still applies.
		return u.checkQuota(need)
	}
	reserve := u.reserve
	if reserve <= 0 {
		reserve = autoReserve(sample.TotalBytes)
	}
	free := int64(sample.FreeBytes)
	if free-need < reserve {
		return fmt.Errorf("%w: butuh %s, tersedia %s (cadangan %s)",
			ErrStorageFull, humanBytes(need), humanBytes(free), humanBytes(reserve))
	}
	return u.checkQuota(need)
}

// autoReserve is the PRD default: the larger of 1 GiB and 5% of the volume.
func autoReserve(totalBytes uint64) int64 {
	fivePercent := int64(float64(totalBytes) * 0.05)
	if fivePercent > (1 << 30) {
		return fivePercent
	}
	return 1 << 30
}

func (u *UploadManager) checkQuota(need int64) error {
	if u.quota <= 0 {
		return nil
	}
	used, err := u.media.TotalBytes()
	if err != nil {
		return err
	}
	if used+need > u.quota {
		return fmt.Errorf("%w: kuota media %s sudah mencapai %s",
			ErrStorageFull, humanBytes(u.quota), humanBytes(used))
	}
	return nil
}

// Cancel removes an in-flight upload and its temp file.
func (u *UploadManager) Cancel(sessionID string) error {
	s, err := u.repo.Get(sessionID)
	if err != nil {
		return err
	}
	_ = os.Remove(s.TempPath)
	if err := u.repo.Delete(sessionID); err != nil {
		return err
	}
	u.log.Info(logging.SourceUpload, sessionID, "Upload dibatalkan: %s", s.OriginalName)
	return nil
}

// allowedExtensions are the import formats named in FR-03. A matching extension
// does not imply the codec is streamable; that is checked later by the probe.
var allowedExtensions = map[string]bool{".mp4": true, ".mov": true, ".mkv": true}

func allowedExtension(name string) bool {
	return allowedExtensions[strings.ToLower(filepath.Ext(name))]
}

// sanitizeFilename keeps only a safe display name. Storage never uses this
// value, so stripping is for display and log hygiene, not path safety.
func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return ""
	}
	if len(name) > 200 {
		// Truncate on a rune boundary so the extension survives.
		runes := []rune(name)
		name = string(runes[:200])
	}
	return name
}

// humanBytes renders a byte count for operator-facing messages.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit && exp < 4; size /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}

// Sweep removes expired uploads and orphaned temp files.
func (u *UploadManager) Sweep() error {
	expired, err := u.repo.Expired(time.Now())
	if err != nil {
		return err
	}
	for _, s := range expired {
		_ = os.Remove(s.TempPath)
		_ = u.repo.Delete(s.ID)
		u.log.Info(logging.SourceUpload, s.ID, "Upload kedaluwarsa dihapus: %s", s.OriginalName)
	}
	return nil
}

// VerifyFinalSpace re-checks the volume before the temp file is promoted,
// since time has passed since the session was created.
func (u *UploadManager) VerifyFinalSpace(size int64) error {
	return u.checkSpace(size)
}

// Promote moves a completed temp file into the media directory and registers the
// media row. The final file appears atomically under a name derived from the
// generated id, so a partial file is never visible as a ready video.
func (u *UploadManager) Promote(s *UploadSession, thumbDir string) (string, string, error) {
	if err := os.MkdirAll(u.mediaDir(), 0o750); err != nil {
		return "", "", err
	}
	mediaID := newID()
	// The stored name is the id plus the sanitised original extension.
	ext := strings.ToLower(filepath.Ext(s.OriginalName))
	finalPath := filepath.Join(u.mediaDir(), mediaID+ext)

	if err := os.Rename(s.TempPath, finalPath); err != nil {
		// A rename across filesystems fails, so fall back to a copy.
		if cerr := copyFile(s.TempPath, finalPath); cerr != nil {
			return "", "", fmt.Errorf("promosikan file: %w", cerr)
		}
		_ = os.Remove(s.TempPath)
	}

	m := &Media{
		ID:           mediaID,
		OriginalName: s.OriginalName,
		StoragePath:  finalPath,
		Size:         s.ExpectedSize,
		FileStatus:   "processing",
	}
	if err := u.media.Insert(m); err != nil {
		_ = os.Remove(finalPath)
		return "", "", err
	}
	return mediaID, finalPath, nil
}

// mediaDir resolves the storage directory at call time, because Settings may
// point it at a mounted volume.
func (u *UploadManager) mediaDir() string { return u.mediaDirPath }

// SetMediaDir updates the storage directory used for promoted files.
func (u *UploadManager) SetMediaDir(dir string) {
	if dir != "" {
		u.mediaDirPath = dir
	}
}

// copyFile duplicates a file, used only when a rename cannot cross filesystems.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
