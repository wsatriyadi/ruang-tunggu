package repo

import (
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/store"
)

// ErrInvalidTarget reports a rejected broadcast target. The API layer maps it
// to the INVALID_TARGET error code so the user gets a field-level message on
// the Settings form (PRD §10).
var ErrInvalidTarget = errors.New("INVALID_TARGET")

// DefaultIngestURL is the primary RTMPS ingest endpoint YouTube documents.
// RTMPS is the default because YouTube recommends encrypted delivery (PRD §8).
const DefaultIngestURL = "rtmps://a.rtmps.youtube.com/live2"

// DefaultProfileID is the delivery profile shipped with P0: SDR H.264/AAC.
const DefaultProfileID = "yt-sdr-h264"

// YouTube ingest host suffixes. A target is refused unless it points at one of
// them, so the publisher can never be pointed at an arbitrary host (PRD §10).
var allowedIngestSuffixes = []string{".youtube.com", ".googlevideo.com"}

// StreamTarget is a configured broadcast destination. The encrypted stream key
// is deliberately absent: the settings API only ever reports StreamKeySet, and
// the key itself leaves this layer solely through DecryptedKey.
type StreamTarget struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	IngestURL      string    `json:"ingest_url"`
	PreviewVideoID string    `json:"preview_video_id"`
	ProfileID      string    `json:"profile_id"`
	Version        int       `json:"version"`
	StreamKeySet   bool      `json:"stream_key_set"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TargetRepo persists the broadcast target and its encrypted stream key.
type TargetRepo struct{ st *store.Store }

// NewTargetRepo builds a target repository.
func NewTargetRepo(st *store.Store) *TargetRepo { return &TargetRepo{st: st} }

const targetColumns = `id, name, ingest_url, preview_video_id, profile_id, version,
	encrypted_stream_key, updated_at`

// scanTarget reads one target row. The encrypted key is returned separately so
// only DecryptedKey can hand it back to a caller.
func scanTarget(sc interface{ Scan(...any) error }) (*StreamTarget, string, error) {
	var t StreamTarget
	var encrypted, updated string
	err := sc.Scan(&t.ID, &t.Name, &t.IngestURL, &t.PreviewVideoID, &t.ProfileID,
		&t.Version, &encrypted, &updated)
	if err != nil {
		return nil, "", err
	}
	t.StreamKeySet = encrypted != ""
	t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &t, encrypted, nil
}

// ValidateIngestURL rejects anything that is not a YouTube RTMP/RTMPS ingest
// endpoint. Both checks matter: the scheme keeps the publisher on a transport
// YouTube accepts, and the host allowlist stops a misconfigured URL from
// shipping the stream key somewhere else (PRD §10).
func ValidateIngestURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errInvalidTarget("URL ingest wajib diisi")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errInvalidTarget("URL ingest tidak valid")
	}
	switch strings.ToLower(u.Scheme) {
	case "rtmps", "rtmp":
	default:
		return errInvalidTarget("URL ingest harus memakai skema rtmps atau rtmp")
	}
	// Embedded credentials have no place in an ingest URL: the stream key
	// travels in its own field, so userinfo means a pasted URL is carrying a
	// secret that would then land in the log and the audit trail.
	if u.User != nil {
		return errInvalidTarget("URL ingest tidak boleh memuat kredensial")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return errInvalidTarget("URL ingest harus menyertakan host")
	}
	for _, suffix := range allowedIngestSuffixes {
		if strings.HasSuffix(host, suffix) {
			return nil
		}
	}
	return errInvalidTarget("Host URL ingest harus berakhiran .youtube.com atau .googlevideo.com")
}

func errInvalidTarget(msg string) error {
	return errors.Join(ErrInvalidTarget, errors.New(msg))
}

// Upsert stores the target, creating it when the id is new. encryptedKey is
// the ciphertext produced by secrets.Box.Encrypt; an empty value keeps the key
// already on file, which is what lets the Settings form save a URL without
// re-entering the key. Version is owned by this layer and increments on every
// write, so a client can tell an overwrite from a no-op.
func (r *TargetRepo) Upsert(t StreamTarget, encryptedKey string) (*StreamTarget, error) {
	t.IngestURL = strings.TrimSpace(t.IngestURL)
	if err := ValidateIngestURL(t.IngestURL); err != nil {
		return nil, err
	}
	if t.ID == "" {
		t.ID = newID()
	}
	if t.ProfileID == "" {
		t.ProfileID = DefaultProfileID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := r.st.Tx(func(tx *sql.Tx) error {
		var current int
		var existing string
		err := tx.QueryRow(`SELECT version, encrypted_stream_key FROM stream_targets WHERE id = ?`,
			t.ID).Scan(&current, &existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			current = 0
		case err != nil:
			return err
		}
		if encryptedKey == "" {
			encryptedKey = existing
		}
		t.Version = current + 1
		_, err = tx.Exec(`INSERT INTO stream_targets
			(id, name, ingest_url, encrypted_stream_key, preview_video_id, profile_id, version, updated_at)
			VALUES (?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
				name=excluded.name, ingest_url=excluded.ingest_url,
				encrypted_stream_key=excluded.encrypted_stream_key,
				preview_video_id=excluded.preview_video_id, profile_id=excluded.profile_id,
				version=excluded.version, updated_at=excluded.updated_at`,
			t.ID, t.Name, t.IngestURL, encryptedKey, t.PreviewVideoID, t.ProfileID,
			t.Version, now)
		return err
	}); err != nil {
		return nil, err
	}
	t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, now)
	t.StreamKeySet = encryptedKey != ""
	return &t, nil
}

// Get returns one target. The encrypted stream key is never included.
func (r *TargetRepo) Get(id string) (*StreamTarget, error) {
	t, _, err := scanTarget(r.st.DB().QueryRow(`SELECT `+targetColumns+
		` FROM stream_targets WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// DecryptedKey returns the stored ciphertext for id. Despite the name it does
// not decrypt: the caller passes the value to secrets.Box.Decrypt, which keeps
// the one place that turns ciphertext into a plaintext key (PRD §10). This is
// the only path by which key material leaves this layer, and no other method
// or struct ever returns it.
func (r *TargetRepo) DecryptedKey(id string) (string, error) {
	var encrypted string
	err := r.st.DB().QueryRow(`SELECT encrypted_stream_key FROM stream_targets WHERE id = ?`, id).
		Scan(&encrypted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", err
	}
	if encrypted == "" {
		return "", errors.New("stream key belum diatur")
	}
	return encrypted, nil
}

// Default returns the configured target, or an unset one describing that no
// target exists yet. P0 supports a single destination (PRD §8), so the oldest
// row is the effective one and the API never has to handle a list.
func (r *TargetRepo) Default() (*StreamTarget, error) {
	t, _, err := scanTarget(r.st.DB().QueryRow(`SELECT ` + targetColumns +
		` FROM stream_targets ORDER BY updated_at ASC LIMIT 1`))
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// No row yet: an empty target reads as "belum diatur" in the UI, which is
	// how the operator knows setup is unfinished. DefaultIngestURL and
	// DefaultProfileID are offered as the starting values by the API layer.
	return &StreamTarget{}, nil
}

// SetStreamKey stores the ciphertext for a target, creating the row when it does
// not exist yet. It is the write side paired with DecryptedKey, and it is the
// only path that accepts key material for storage.
func (r *TargetRepo) SetStreamKey(id, encryptedKey string) error {
	if encryptedKey == "" {
		return errors.New("stream key kosong")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return r.st.Tx(func(tx *sql.Tx) error {
		var exists int
		err := tx.QueryRow(`SELECT COUNT(*) FROM stream_targets WHERE id = ?`, id).Scan(&exists)
		if err != nil {
			return err
		}
		if exists == 0 {
			_, err = tx.Exec(`INSERT INTO stream_targets
				(id, name, ingest_url, encrypted_stream_key, preview_video_id, profile_id, version, updated_at)
				VALUES (?,?,?,?,?,?,?,?)`,
				id, "Default", DefaultIngestURL, encryptedKey, "", DefaultProfileID, 1, now)
			return err
		}
		_, err = tx.Exec(`UPDATE stream_targets SET encrypted_stream_key = ?,
			version = version + 1, updated_at = ? WHERE id = ?`, encryptedKey, now, id)
		return err
	})
}
