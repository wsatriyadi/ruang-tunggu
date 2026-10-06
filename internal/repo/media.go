// Package repo holds the data access used by the API and the worker. Keeping
// queries here means the SQL lives in one place and the API layer stays thin.
package repo

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/store"
)

// Media is one stored video file.
type Media struct {
	ID           string  `json:"id"`
	OriginalName string  `json:"original_name"`
	Size         int64   `json:"size"`
	Duration     float64 `json:"duration"`
	Container    string  `json:"container"`
	VideoCodec   string  `json:"video_codec,omitempty"`
	AudioCodec   string  `json:"audio_codec,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	Resolution   string  `json:"resolution,omitempty"`
	FPS          float64 `json:"fps,omitempty"`
	Bitrate      int64   `json:"bitrate,omitempty"`
	SampleRate   int     `json:"sample_rate,omitempty"`
	Channels     int     `json:"channels,omitempty"`
	PixFmt       string  `json:"pix_fmt,omitempty"`
	HasVideo     bool    `json:"has_video"`
	HasAudio     bool    `json:"has_audio"`
	// FileStatus is uploading, processing, ready, invalid or missing (FR-03).
	FileStatus string `json:"file_status"`
	// Compatibility is unchecked, compatible, incompatible or needs_review.
	Compatibility string        `json:"compatibility"`
	Issues        []media.Issue `json:"issues,omitempty"`
	// ThumbnailURL is a path served by the API, never a filesystem path.
	// ThumbnailPath stays internal: exposing it would leak the server layout.
	ThumbnailURL  string    `json:"thumbnail_url,omitempty"`
	HasThumbnail  bool      `json:"has_thumbnail"`
	StoragePath   string    `json:"-"`
	ThumbnailPath string    `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// KeyframeMaxGap mirrors the probe result for the detail view.
	KeyframeMaxGap float64 `json:"keyframe_max_gap,omitempty"`
	// Usage lists what still references this file, blocking deletion (AC-20).
	Usage []MediaUsage `json:"usage,omitempty"`
}

// MediaUsage names one dependency that prevents deletion.
type MediaUsage struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MediaRepo reads and writes media metadata.
type MediaRepo struct{ st *store.Store }

// NewMediaRepo builds a media repository.
func NewMediaRepo(st *store.Store) *MediaRepo { return &MediaRepo{st: st} }

const mediaColumns = `id, original_name, storage_path, size, duration, container,
	video_codec, audio_codec, width, height, fps, bitrate, sample_rate, channels,
	channel_layout, pix_fmt, profile, level, time_base, has_video, has_audio,
	start_time, metadata, thumbnail_path, file_status, created_at, updated_at`

func scanMedia(sc interface{ Scan(...any) error }) (*Media, error) {
	var m Media
	var created, updated string
	var hasVideo, hasAudio int
	err := sc.Scan(&m.ID, &m.OriginalName, &m.StoragePath, &m.Size, &m.Duration, &m.Container,
		&m.VideoCodec, &m.AudioCodec, &m.Width, &m.Height, &m.FPS, &m.Bitrate, &m.SampleRate,
		&m.Channels, new(string), &m.PixFmt, new(string), new(int), new(string),
		&hasVideo, &hasAudio, new(float64), new(string), &m.ThumbnailPath, &m.FileStatus,
		&created, &updated)
	if err != nil {
		return nil, err
	}
	m.HasVideo, m.HasAudio = hasVideo == 1, hasAudio == 1
	m.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	m.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if m.Width > 0 && m.Height > 0 {
		m.Resolution = fmt.Sprintf("%dx%d", m.Width, m.Height)
	}
	m.HasThumbnail = m.ThumbnailPath != ""
	m.ThumbnailURL = thumbnailURL(m.ID)
	m.Compatibility = media.StatusUnchecked
	return &m, nil
}

// List returns media ordered newest first, optionally filtered by a search term.
func (r *MediaRepo) List(search string, limit, offset int) ([]Media, int, error) {
	where := ""
	var args []any
	if s := strings.TrimSpace(search); s != "" {
		where = `WHERE original_name LIKE ?`
		args = append(args, "%"+s+"%")
	}
	var total int
	if err := r.st.DB().QueryRow(`SELECT COUNT(*) FROM media_files `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := r.st.DB().Query(`SELECT `+mediaColumns+` FROM media_files `+where+
		` ORDER BY created_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]Media, 0, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *m)
		ids = append(ids, m.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := r.attachValidations(out, ids); err != nil {
		return nil, 0, err
	}
	if err := r.attachUsage(out, ids); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Get returns one media file with its latest validation result.
func (r *MediaRepo) Get(id string) (*Media, error) {
	row := r.st.DB().QueryRow(`SELECT `+mediaColumns+` FROM media_files WHERE id = ?`, id)
	m, err := scanMedia(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	list := []Media{*m}
	if err := r.attachValidations(list, []string{m.ID}); err != nil {
		return nil, err
	}
	list[0].KeyframeMaxGap = m.KeyframeMaxGap
	list[0].Usage, _ = r.Usage(m.ID)
	if len(list) == 0 {
		return nil, store.ErrNotFound
	}
	return &list[0], nil
}

// attachValidations loads the newest validation for each media id in one query.
func (r *MediaRepo) attachValidations(list []Media, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.st.DB().Query(`SELECT media_id, compatibility, issues, keyframe_max_gap
		FROM media_validations v
		WHERE v.id = (SELECT id FROM media_validations m2 WHERE m2.media_id = v.media_id ORDER BY m2.id DESC LIMIT 1)
		AND v.media_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := map[string]*Media{}
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	for rows.Next() {
		var mediaID, status, issues string
		var gap float64
		if err := rows.Scan(&mediaID, &status, &issues, &gap); err != nil {
			return err
		}
		m := byID[mediaID]
		if m == nil {
			continue
		}
		m.Compatibility = status
		m.KeyframeMaxGap = gap
		if issues != "" {
			var parsed []media.Issue
			if err := json.Unmarshal([]byte(issues), &parsed); err == nil {
				m.Issues = parsed
			}
		}
	}
	return rows.Err()
}

// Usage lists every dependency that still references a media file. Deletion is
// refused while this is non-empty (AC-20).
func (r *MediaRepo) Usage(mediaID string) ([]MediaUsage, error) {
	rows, err := r.st.DB().Query(`
		SELECT p.name, r.id FROM playlist_items i
		  JOIN playlist_revisions r ON r.id = i.revision_id
		  JOIN playlists p ON p.id = r.playlist_id
		WHERE i.media_id = ?`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaUsage
	for rows.Next() {
		var u MediaUsage
		if err := rows.Scan(&u.Name, &u.ID); err != nil {
			return nil, err
		}
		u.Kind = "playlist"
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	schedRows, err := r.st.DB().Query(`
		SELECT s.name, s.id FROM schedules s
		WHERE s.revision_id IN (
		  SELECT revision_id FROM playlist_items WHERE media_id = ?
		)`, mediaID)
	if err != nil {
		return nil, err
	}
	defer schedRows.Close()
	for schedRows.Next() {
		var u MediaUsage
		if err := schedRows.Scan(&u.Name, &u.ID); err != nil {
			return nil, err
		}
		u.Kind = "schedule"
		out = append(out, u)
	}
	return out, schedRows.Err()
}

// ActiveSessionUsing reports the id of a running session that is playing a file.
func (r *MediaRepo) ActiveSessionUsing(mediaID string) (string, bool) {
	var id string
	err := r.st.DB().QueryRow(`
		SELECT s.id FROM stream_sessions s
		JOIN playback_items p ON p.session_id = s.id
		WHERE p.media_id = ? AND s.desired_state = 'running'
		ORDER BY s.created_at DESC LIMIT 1`, mediaID).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, true
}

// Insert stores a newly uploaded file in the processing state.
func (r *MediaRepo) Insert(m *Media) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.st.DB().Exec(`INSERT INTO media_files
		(id, original_name, storage_path, size, duration, container, video_codec, audio_codec,
		 width, height, fps, bitrate, sample_rate, channels, channel_layout, pix_fmt, profile,
		 level, time_base, has_video, has_audio, start_time, metadata, thumbnail_path,
		 file_status, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.OriginalName, m.StoragePath, m.Size, m.Duration, m.Container, m.VideoCodec, m.AudioCodec,
		m.Width, m.Height, m.FPS, m.Bitrate, m.SampleRate, m.Channels, "", m.PixFmt, "",
		0, "", boolInt(m.HasVideo), boolInt(m.HasAudio), 0.0, "{}", m.ThumbnailPath,
		m.FileStatus, now, now)
	return err
}

// UpdateProbe writes the result of a successful probe and validation.
func (r *MediaRepo) UpdateProbe(id string, p *media.Probe, compat media.Compatibility, fileStatus string) error {
	issues, err := json.Marshal(compat.Issues)
	if err != nil {
		return err
	}
	return r.st.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE media_files SET
			size=?, duration=?, container=?, video_codec=?, audio_codec=?, width=?, height=?,
			fps=?, bitrate=?, sample_rate=?, channels=?, pix_fmt=?, has_video=?, has_audio=?,
			metadata=?, file_status=?, updated_at=? WHERE id=?`,
			p.Size, p.Duration, p.Container, p.VideoCodec, p.AudioCodec, p.Width, p.Height,
			p.FPS, p.BitRate, p.SampleRate, p.Channels, p.PixFmt,
			boolInt(p.HasVideo), boolInt(p.HasAudio), "{}", fileStatus,
			time.Now().UTC().Format(time.RFC3339Nano), id); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO media_validations
			(media_id, profile_id, profile_version, compatibility, issues, keyframe_max_gap, checked_at)
			VALUES (?,?,?,?,?,?,?)`,
			id, "yt-sdr-h264", 1, compat.Status, string(issues), p.KeyframeMaxGap,
			time.Now().UTC().Format(time.RFC3339Nano))
		return err
	})
}

// SetStatus updates only the storage status, used when a file goes missing.
func (r *MediaRepo) SetStatus(id, status string) error {
	_, err := r.st.DB().Exec(`UPDATE media_files SET file_status=?, updated_at=? WHERE id=?`,
		status, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// SetThumbnail records the generated thumbnail path.
func (r *MediaRepo) SetThumbnail(id, path string) error {
	_, err := r.st.DB().Exec(`UPDATE media_files SET thumbnail_path=?, updated_at=? WHERE id=?`,
		path, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// Delete removes the database row; the caller removes the file from disk after
// checking references.
func (r *MediaRepo) Delete(id string) error {
	res, err := r.st.DB().Exec(`DELETE FROM media_files WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// TotalBytes reports the space used by stored media, for quota checks.
func (r *MediaRepo) TotalBytes() (int64, error) {
	var total sql.NullInt64
	err := r.st.DB().QueryRow(`SELECT SUM(size) FROM media_files WHERE file_status != 'missing'`).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

// MarkMissingFlags finds stored files that no longer exist on disk. A file
// removed outside the app is marked missing and blocks new starts (PRD §14).
func (r *MediaRepo) MarkMissingFlags(exists func(path string) bool) (int, error) {
	rows, err := r.st.DB().Query(`SELECT id, storage_path FROM media_files WHERE file_status != 'missing'`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type ref struct{ id, path string }
	var refs []ref
	for rows.Next() {
		var item ref
		if err := rows.Scan(&item.id, &item.path); err != nil {
			return 0, err
		}
		refs = append(refs, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, ref := range refs {
		if exists(ref.path) {
			continue
		}
		if err := r.SetStatus(ref.id, "missing"); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// thumbnailURL builds the authenticated endpoint that serves a thumbnail.
// The browser cannot read the stored path directly, and the endpoint keeps
// access control in one place (PRD §10).
func thumbnailURL(mediaID string) string {
	return "/api/videos/" + mediaID + "/thumbnail"
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// attachUsage fills the dependency list for a page of media in one pass.
//
// The library needs this so the table can tell the operator that a file is
// referenced before they reach for delete, and so the delete dialog can name
// the blockers instead of reporting a bare conflict (AC-20).
func (r *MediaRepo) attachUsage(items []Media, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	byID := make(map[string]*Media, len(items))
	for i := range items {
		args[i] = ids[i]
		byID[ids[i]] = &items[i]
	}

	rows, err := r.st.DB().Query(`
		SELECT i.media_id, 'playlist', p.name, r.id
		FROM playlist_items i
		  JOIN playlist_revisions r ON r.id = i.revision_id
		  JOIN playlists p ON p.id = r.playlist_id
		WHERE i.media_id IN (`+placeholders+`)
		UNION ALL
		SELECT i.media_id, 'schedule', s.name, s.id
		FROM playlist_items i
		  JOIN schedules s ON s.revision_id = i.revision_id
		WHERE i.media_id IN (`+placeholders+`)`, append(args, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var mediaID, kind, name, id string
		if err := rows.Scan(&mediaID, &kind, &name, &id); err != nil {
			return err
		}
		if m := byID[mediaID]; m != nil {
			m.Usage = append(m.Usage, MediaUsage{Kind: kind, ID: id, Name: name})
		}
	}
	return rows.Err()
}
