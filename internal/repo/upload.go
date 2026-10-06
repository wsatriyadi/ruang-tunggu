package repo

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/wisnu/ylivemasadi/internal/store"
)

// UploadSession is an in-progress resumable upload (FR-03).
type UploadSession struct {
	ID            string    `json:"id"`
	OriginalName  string    `json:"original_name"`
	ExpectedSize  int64     `json:"expected_size"`
	ReceivedBytes int64     `json:"received_bytes"`
	PartSize      int64     `json:"part_size"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	MediaID       string    `json:"media_id,omitempty"`
	TempPath      string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// Upload statuses.
const (
	UploadActive     = "active"
	UploadProcessing = "processing"
	UploadCompleted  = "completed"
	UploadFailed     = "failed"
	UploadCancelled  = "cancelled"
)

// UploadRepo persists upload sessions and their received parts.
type UploadRepo struct{ st *store.Store }

// NewUploadRepo builds an upload repository.
func NewUploadRepo(st *store.Store) *UploadRepo { return &UploadRepo{st: st} }

// DefaultPartSize is 8 MiB: large enough to keep request overhead low, small
// enough that a failed transfer loses little and RAM never scales with the file.
const DefaultPartSize = 8 << 20

// uploadTTL is how long an incomplete upload is retained for resumption.
const uploadTTL = 24 * time.Hour

// Create registers a new upload session and allocates its temp file.
func (r *UploadRepo) Create(userID int64, name string, expectedSize, partSize int64, tempPath string) (*UploadSession, error) {
	if partSize <= 0 {
		partSize = DefaultPartSize
	}
	now := time.Now().UTC()
	s := &UploadSession{
		ID:           newID(),
		OriginalName: name,
		ExpectedSize: expectedSize,
		PartSize:     partSize,
		Status:       UploadActive,
		TempPath:     tempPath,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(uploadTTL),
	}
	_, err := r.st.DB().Exec(`INSERT INTO upload_sessions
		(id, user_id, original_name, temp_path, expected_size, received_bytes, part_size,
		 status, error, media_id, created_at, updated_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, userID, s.OriginalName, s.TempPath, s.ExpectedSize, 0, s.PartSize,
		s.Status, "", "", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		s.ExpiresAt.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns an upload session by id.
func (r *UploadRepo) Get(id string) (*UploadSession, error) {
	var s UploadSession
	var created, updated, expires string
	err := r.st.DB().QueryRow(`SELECT id, original_name, expected_size, received_bytes,
		part_size, status, error, media_id, temp_path, created_at, updated_at, expires_at
		FROM upload_sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.OriginalName, &s.ExpectedSize, &s.ReceivedBytes, &s.PartSize,
			&s.Status, &s.Error, &s.MediaID, &s.TempPath, &created, &updated, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	s.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	return &s, nil
}

// ListParts returns the indexes already stored, so a client can resume.
func (r *UploadRepo) ListParts(id string) ([]int, error) {
	rows, err := r.st.DB().Query(`SELECT part_index FROM upload_parts WHERE upload_id = ? ORDER BY part_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var idx int
		if err := rows.Scan(&idx); err != nil {
			return nil, err
		}
		out = append(out, idx)
	}
	return out, rows.Err()
}

// RecordPart stores a received part. It is idempotent: re-sending the same part
// index replaces the record and recomputes the total, so a retried request
// never inflates the received byte count.
func (r *UploadRepo) RecordPart(id string, index int, size int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return r.st.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO upload_parts (upload_id, part_index, size, received_at)
			VALUES (?,?,?,?)
			ON CONFLICT(upload_id, part_index) DO UPDATE SET size=excluded.size, received_at=excluded.received_at`,
			id, index, size, now); err != nil {
			return err
		}
		// Recompute from the parts table rather than incrementing, so duplicate
		// delivery cannot drift the total.
		_, err := tx.Exec(`UPDATE upload_sessions SET
			received_bytes = (SELECT COALESCE(SUM(size), 0) FROM upload_parts WHERE upload_id = ?),
			updated_at = ? WHERE id = ?`, id, now, id)
		return err
	})
}

// SetStatus updates the session state and error text.
func (r *UploadRepo) SetStatus(id, status, errMsg string) error {
	_, err := r.st.DB().Exec(`UPDATE upload_sessions SET status=?, error=?, updated_at=? WHERE id=?`,
		status, errMsg, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// SetMediaID records the media row created from a completed upload.
func (r *UploadRepo) SetMediaID(id, mediaID string) error {
	_, err := r.st.DB().Exec(`UPDATE upload_sessions SET media_id=?, updated_at=? WHERE id=?`,
		mediaID, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// ListActive returns sessions still in flight, used by the cleanup sweep.
func (r *UploadRepo) ListActive() ([]UploadSession, error) {
	rows, err := r.st.DB().Query(`SELECT id FROM upload_sessions WHERE status = ?`, UploadActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]UploadSession, 0, len(ids))
	for _, id := range ids {
		s, err := r.Get(id)
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}

// Delete removes the session and its parts.
func (r *UploadRepo) Delete(id string) error {
	_, err := r.st.DB().Exec(`DELETE FROM upload_sessions WHERE id = ?`, id)
	return err
}

// Expired returns sessions past their retention window that are not active.
func (r *UploadRepo) Expired(now time.Time) ([]UploadSession, error) {
	rows, err := r.st.DB().Query(`SELECT id FROM upload_sessions
		WHERE expires_at < ? AND status != ?`, now.UTC().Format(time.RFC3339Nano), UploadActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]UploadSession, 0, len(ids))
	for _, id := range ids {
		s, err := r.Get(id)
		if err != nil {
			continue
		}
		out = append(out, *s)
	}
	return out, nil
}

// newID returns a random opaque identifier. Storage paths are derived from it,
// never from user input, which is what blocks path traversal (PRD §10).
// NewID returns a random opaque identifier, exported so other packages can
// mint identifiers without duplicating the scheme.
func NewID() string {
	return newID()
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is unrecoverable for identifier generation.
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b)
}
