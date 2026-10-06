package repo

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/store"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// Playlist is one playlist with one revision resolved and rolled up (FR-04).
//
// Every save produces a new revision, so this struct always describes the
// revision it was read from: Get and List use the newest one, GetRevision the
// pinned one a schedule refers to.
type Playlist struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	ItemCount     int     `json:"item_count"`
	CycleDuration float64 `json:"cycle_duration"`
	LoopEnabled   bool    `json:"loop_enabled"`
	PlaybackMode  string  `json:"playback_mode"`
	// RevisionID is the revision this view was loaded from. A schedule pins it,
	// so it must survive independently of LatestRevisionID.
	RevisionID string `json:"revision_id"`
	// LatestRevisionID is always the newest revision of the playlist, even on a
	// pinned view, so the UI can tell a stale schedule from a current one.
	LatestRevisionID string `json:"latest_revision_id"`
	RevisionNumber   int    `json:"revision_number"`
	// Compatibility is the worst per-file verdict inside the revision.
	Compatibility string                `json:"compatibility"`
	Issues        []media.PlaylistIssue `json:"issues,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
	// Items is the resolved queue of the revision, ordered by position.
	Items []PlaylistItemRow `json:"items,omitempty"`
}

// PlaylistItemRow is one entry of a revision joined with its media file.
type PlaylistItemRow struct {
	Position      int     `json:"position"`
	MediaID       string  `json:"media_id"`
	Name          string  `json:"name"`
	Duration      float64 `json:"duration"`
	FileStatus    string  `json:"file_status"`
	Compatibility string  `json:"compatibility"`
	ThumbnailURL  string  `json:"thumbnail_url,omitempty"`
	HasThumbnail  bool    `json:"has_thumbnail"`
	// thumbnailPath is the stored location; it never leaves the server.
	thumbnailPath string
}

// ValidationError is rejected input. The message is written for the operator,
// so the API can return it unchanged.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// ScheduleInUseError blocks a playlist delete because schedules still pin one
// of its revisions. Schedules names what has to be deleted first (FR-05).
type ScheduleInUseError struct {
	PlaylistName string
	Schedules    []string
}

func (e *ScheduleInUseError) Error() string {
	return fmt.Sprintf("Playlist %q masih dipakai oleh jadwal: %s. Hapus jadwal tersebut terlebih dahulu.",
		e.PlaylistName, strings.Join(e.Schedules, ", "))
}

// PlaylistRepo reads and writes playlists and their immutable revisions.
type PlaylistRepo struct{ st *store.Store }

// NewPlaylistRepo builds a playlist repository.
func NewPlaylistRepo(st *store.Store) *PlaylistRepo { return &PlaylistRepo{st: st} }

// playlistColumns is the playlist row plus the revision it resolves. Both
// readers alias the revision as r, so one column list and one scanner serve the
// latest read and the pinned one alike.
const playlistColumns = `p.id, p.name, p.latest_revision_id, r.id, r.revision_number,
	r.playback_mode, r.loop_enabled, p.created_at, p.updated_at`

func scanPlaylist(sc interface{ Scan(...any) error }) (*Playlist, error) {
	var p Playlist
	var created, updated string
	var loop int
	err := sc.Scan(&p.ID, &p.Name, &p.LatestRevisionID, &p.RevisionID, &p.RevisionNumber,
		&p.PlaybackMode, &loop, &created, &updated)
	if err != nil {
		return nil, err
	}
	p.LoopEnabled = loop == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if p.PlaybackMode == "" {
		p.PlaybackMode = streaming.ModeSequential
	}
	p.Compatibility = media.StatusUnchecked
	return &p, nil
}

// itemColumns carries the owning revision, everything the row itself shows, and
// the probe fields media.ValidateCohesion compares. One query therefore answers
// the list, the cycle duration and the cross-file cohesion check at once.
const itemColumns = `i.revision_id, i.position, i.media_id, m.original_name, m.duration,
	m.file_status, m.thumbnail_path,
	COALESCE((SELECT v.compatibility FROM media_validations v
		WHERE v.media_id = m.id ORDER BY v.id DESC LIMIT 1), ''),
	m.container, m.video_codec, m.audio_codec, m.width, m.height, m.fps, m.sample_rate,
	m.channels, m.channel_layout, m.pix_fmt, m.profile, m.level, m.time_base,
	m.has_video, m.has_audio`

func scanItem(sc interface{ Scan(...any) error }) (string, PlaylistItemRow, media.NamedProbe, error) {
	var revisionID string
	var it PlaylistItemRow
	var probe media.Probe
	var hasVideo, hasAudio int
	err := sc.Scan(&revisionID, &it.Position, &it.MediaID, &it.Name, &it.Duration,
		&it.FileStatus, &it.thumbnailPath, &it.Compatibility, &probe.Container,
		&probe.VideoCodec, &probe.AudioCodec, &probe.Width, &probe.Height, &probe.FPS,
		&probe.SampleRate, &probe.Channels, &probe.ChannelLayout, &probe.PixFmt,
		&probe.Profile, &probe.Level, &probe.TimeBase, &hasVideo, &hasAudio)
	if err != nil {
		return "", it, media.NamedProbe{}, err
	}
	it.HasThumbnail = it.thumbnailPath != ""
	if it.HasThumbnail {
		it.ThumbnailURL = "/api/videos/" + it.MediaID + "/thumbnail"
	}
	if it.Compatibility == "" {
		it.Compatibility = media.StatusUnchecked
	}
	probe.Duration = it.Duration
	probe.HasVideo, probe.HasAudio = hasVideo == 1, hasAudio == 1
	return revisionID, it, media.NamedProbe{ID: it.MediaID, OriginalName: it.Name, Probe: &probe}, nil
}

// revisionView is one revision's items plus everything derived from them.
type revisionView struct {
	items    []PlaylistItemRow
	duration float64
	compat   string
	issues   []media.PlaylistIssue
}

// applyView copies a resolved revision onto the playlist that asked for it.
func (p *Playlist) applyView(v *revisionView) {
	if v == nil {
		return
	}
	p.ItemCount = len(v.items)
	p.CycleDuration = v.duration
	p.Compatibility = v.compat
	p.Issues = v.issues
	p.Items = v.items
}

// loadViews resolves many revisions in one query. A playlist list would
// otherwise issue one query per row, which is exactly the fan-out that hurts on
// a low-power device. Revisions without items keep an entry, so an empty
// playlist reports zero items instead of a missing summary.
func (r *PlaylistRepo) loadViews(revisionIDs []string) (map[string]*revisionView, error) {
	views := make(map[string]*revisionView, len(revisionIDs))
	args := make([]any, 0, len(revisionIDs))
	for _, id := range revisionIDs {
		if id == "" {
			continue
		}
		if _, ok := views[id]; !ok {
			views[id] = &revisionView{compat: media.StatusUnchecked}
			args = append(args, id)
		}
	}
	if len(args) == 0 {
		return views, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")

	rows, err := r.st.DB().Query(`SELECT `+itemColumns+`
		FROM playlist_items i
		JOIN media_files m ON m.id = i.media_id
		WHERE i.revision_id IN (`+placeholders+`)
		ORDER BY i.revision_id, i.position`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	probes := make(map[string][]media.NamedProbe, len(args))
	for rows.Next() {
		revisionID, item, probe, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		view := views[revisionID]
		if view == nil {
			view = &revisionView{compat: media.StatusUnchecked}
			views[revisionID] = view
		}
		view.items = append(view.items, item)
		probes[revisionID] = append(probes[revisionID], probe)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for id, view := range views {
		var duration float64
		// Start at compatible so a revision of passing files reports
		// compatible, then let each item drag the verdict down.
		view.compat = media.StatusCompatible
		for i := range view.items {
			duration += view.items[i].Duration
			view.compat = worstCompat(view.compat, view.items[i].Compatibility)
		}
		if len(view.items) == 0 {
			view.compat = media.StatusUnchecked
		}
		view.duration = duration
		view.issues = media.ValidateCohesion(probes[id], media.SDRProfile())
	}
	return views, nil
}

// loadView resolves one revision, used by the single-playlist readers.
func (r *PlaylistRepo) loadView(revisionID string) (*revisionView, error) {
	views, err := r.loadViews([]string{revisionID})
	if err != nil {
		return nil, err
	}
	return views[revisionID], nil
}

// compatRank orders verdicts so a playlist reports the one that matters: a
// single incompatible file blocks the start no matter how many good files
// surround it.
func compatRank(status string) int {
	switch status {
	case media.StatusIncompatible:
		return 3
	case media.StatusNeedsReview:
		return 2
	case media.StatusUnchecked:
		return 1
	default:
		return 0
	}
}

// worstCompat picks the least favourable of two verdicts.
func worstCompat(a, b string) string {
	if compatRank(a) >= compatRank(b) {
		return a
	}
	return b
}

// List returns every playlist newest first, each with its latest revision
// resolved and rolled up.
func (r *PlaylistRepo) List() ([]Playlist, error) {
	list := make([]Playlist, 0, 16)
	revisions := make([]string, 0, 16)
	rows, err := r.st.DB().Query(`SELECT ` + playlistColumns + `
		FROM playlists p
		JOIN playlist_revisions r ON r.id = p.latest_revision_id
		ORDER BY p.updated_at DESC, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPlaylist(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *p)
		revisions = append(revisions, p.LatestRevisionID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	views, err := r.loadViews(revisions)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].applyView(views[revisions[i]])
	}
	return list, nil
}

// Get returns one playlist with its latest revision and that revision's items.
// A missing playlist yields store.ErrNotFound.
func (r *PlaylistRepo) Get(id string) (*Playlist, error) {
	row := r.st.DB().QueryRow(`SELECT `+playlistColumns+`
		FROM playlists p
		JOIN playlist_revisions r ON r.id = p.latest_revision_id
		WHERE p.id = ?`, id)
	p, err := scanPlaylist(row)
	if err != nil {
		return nil, notFound(err)
	}
	view, err := r.loadView(p.LatestRevisionID)
	if err != nil {
		return nil, err
	}
	p.applyView(view)
	return p, nil
}

// GetRevision returns the playlist as pinned by one revision. Schedules store a
// revision id, so this is how a pending or running schedule reads its content
// even after the playlist has moved on. LatestRevisionID still points at the
// newest revision, which is how a caller notices the pin is stale.
func (r *PlaylistRepo) GetRevision(revisionID string) (*Playlist, error) {
	row := r.st.DB().QueryRow(`SELECT `+playlistColumns+`
		FROM playlists p
		JOIN playlist_revisions r ON r.playlist_id = p.id
		WHERE r.id = ?`, revisionID)
	p, err := scanPlaylist(row)
	if err != nil {
		return nil, notFound(err)
	}
	view, err := r.loadView(revisionID)
	if err != nil {
		return nil, err
	}
	p.applyView(view)
	return p, nil
}

// Create stores an empty playlist with revision 1: sequential, no loop.
func (r *PlaylistRepo) Create(name string, userID int64) (*Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &ValidationError{Message: "Nama playlist tidak boleh kosong."}
	}
	var revisionID string
	err := r.st.Tx(func(tx *sql.Tx) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		playlistID, revID := newID(), newID()
		revisionID = revID
		if _, err := tx.Exec(`INSERT INTO playlists
			(id, name, latest_revision_id, created_at, updated_at) VALUES (?,?,?,?,?)`,
			playlistID, name, revID, now, now); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO playlist_revisions
			(id, playlist_id, revision_number, playback_mode, loop_enabled, created_by, created_at)
			VALUES (?,?,?,?,?,?,?)`,
			revID, playlistID, 1, streaming.ModeSequential, 0, creator(userID), now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return r.GetRevision(revisionID)
}

// Update saves the playlist as a NEW revision (FR-04). Revisions are immutable,
// so a schedule keeps streaming exactly the content it was created for while the
// playlist keeps changing underneath it. The revision number, the item order and
// the latest-revision pointer move together or not at all.
func (r *PlaylistRepo) Update(id string, userID int64, name string, mode string, loop bool, mediaIDs []string) (*Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &ValidationError{Message: "Nama playlist tidak boleh kosong."}
	}
	if !validMode(mode) {
		return nil, &ValidationError{Message: fmt.Sprintf(
			"Mode pemutaran %q tidak dikenal. Pilihan yang tersedia: %s, %s, %s.",
			mode, streaming.ModeSequential, streaming.ModeRandom, streaming.ModeRandomNoRepeat)}
	}
	if err := checkUniqueItems(mediaIDs); err != nil {
		return nil, err
	}

	var revisionID string
	err := r.st.Tx(func(tx *sql.Tx) error {
		// Numbered inside the transaction: SQLite serialises writers, so max+1
		// cannot collide with a save running at the same moment.
		var number int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(revision_number), 0) + 1
			FROM playlist_revisions WHERE playlist_id = ?`, id).Scan(&number); err != nil {
			return notFound(err)
		}
		missing, err := missingMedia(tx, mediaIDs)
		if err != nil {
			return err
		}
		if missing != "" {
			return &ValidationError{Message: fmt.Sprintf("Video %q tidak ditemukan.", missing)}
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		revisionID = newID()
		if _, err := tx.Exec(`INSERT INTO playlist_revisions
			(id, playlist_id, revision_number, playback_mode, loop_enabled, created_by, created_at)
			VALUES (?,?,?,?,?,?,?)`,
			revisionID, id, number, mode, boolInt(loop), creator(userID), now); err != nil {
			return err
		}
		for position, mediaID := range mediaIDs {
			if _, err := tx.Exec(`INSERT INTO playlist_items (revision_id, media_id, position)
				VALUES (?,?,?)`, revisionID, mediaID, position); err != nil {
				return err
			}
		}
		res, err := tx.Exec(`UPDATE playlists SET name = ?, latest_revision_id = ?, updated_at = ?
			WHERE id = ?`, name, revisionID, now, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return store.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.GetRevision(revisionID)
}

// Rename changes only the display name. It writes no revision, because the
// content a schedule pins stays identical.
func (r *PlaylistRepo) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ValidationError{Message: "Nama playlist tidak boleh kosong."}
	}
	res, err := r.st.DB().Exec(`UPDATE playlists SET name = ?, updated_at = ? WHERE id = ?`,
		name, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// Delete removes the playlist and, by cascade, all of its revisions. It is
// refused while a schedule still pins any of those revisions, because such a
// schedule would point at content that no longer exists; the returned
// *ScheduleInUseError names the schedules to delete first.
func (r *PlaylistRepo) Delete(id string) error {
	return r.st.Tx(func(tx *sql.Tx) error {
		var name string
		if err := tx.QueryRow(`SELECT name FROM playlists WHERE id = ?`, id).Scan(&name); err != nil {
			return notFound(err)
		}
		blocking, err := blockingSchedules(tx, id)
		if err != nil {
			return err
		}
		if len(blocking) > 0 {
			return &ScheduleInUseError{PlaylistName: name, Schedules: blocking}
		}
		_, err = tx.Exec(`DELETE FROM playlists WHERE id = ?`, id)
		return err
	})
}

// blockingSchedules names every schedule pinned to a revision of the playlist.
func blockingSchedules(tx *sql.Tx, playlistID string) ([]string, error) {
	rows, err := tx.Query(`SELECT s.name FROM schedules s
		JOIN playlist_revisions r ON r.id = s.revision_id
		WHERE r.playlist_id = ? ORDER BY s.name`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// missingMedia reports the first id that is not a stored file, so the save is
// rejected with a readable message instead of failing on the item foreign key.
func missingMedia(tx *sql.Tx, mediaIDs []string) (string, error) {
	if len(mediaIDs) == 0 {
		return "", nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(mediaIDs)), ",")
	args := make([]any, len(mediaIDs))
	for i, id := range mediaIDs {
		args[i] = id
	}
	rows, err := tx.Query(`SELECT id FROM media_files WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	found := make(map[string]struct{}, len(mediaIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		found[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	for _, id := range mediaIDs {
		if _, ok := found[id]; !ok {
			return id, nil
		}
	}
	return "", nil
}

func validMode(mode string) bool {
	switch mode {
	case streaming.ModeSequential, streaming.ModeRandom, streaming.ModeRandomNoRepeat:
		return true
	}
	return false
}

// checkUniqueItems rejects a queue that names the same file twice: the item key
// is (revision, media), so a duplicate would be dropped by the database and the
// saved order would not match what the operator arranged.
func checkUniqueItems(mediaIDs []string) error {
	seen := make(map[string]struct{}, len(mediaIDs))
	for _, id := range mediaIDs {
		if id == "" {
			return &ValidationError{Message: "Daftar video tidak boleh kosong."}
		}
		if _, dup := seen[id]; dup {
			return &ValidationError{
				Message: "Video yang sama hanya boleh muncul sekali dalam satu revisi."}
		}
		seen[id] = struct{}{}
	}
	return nil
}

// creator maps the acting user onto the revision author. Setup runs before any
// user exists, which the schema records as NULL.
func creator(userID int64) any {
	if userID <= 0 {
		return nil
	}
	return userID
}

// notFound maps the driver's missing-row error onto the store sentinel.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}
