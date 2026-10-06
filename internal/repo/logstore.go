package repo

import (
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/store"
)

// LogRepo is the durable copy of log lines that backs the Logs page (FR-09).
// The rotating files stay the primary record; this index exists so a query can
// filter by level, source and session without reading them.
type LogRepo struct{ st *store.Store }

// NewLogRepo builds a log repository.
func NewLogRepo(st *store.Store) *LogRepo { return &LogRepo{st: st} }

// defaultLogLimit bounds an unbounded query so a browser cannot ask the small
// device for the whole table.
const defaultLogLimit = 500

// Insert stores one entry. The message is expected to be redacted already —
// logging.Logger redacts before an entry exists — and is stored as given so the
// index matches the file.
func (r *LogRepo) Insert(entry logging.Entry) error {
	ts := entry.Time
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	level := entry.Level
	if level == "" {
		level = logging.LevelInfo.String()
	}
	source := entry.Source
	if source == "" {
		source = logging.SourceApp
	}
	_, err := r.st.DB().Exec(`INSERT INTO log_records (ts, level, source, session_id, message)
		VALUES (?,?,?,?,?)`,
		ts.UTC().Format(time.RFC3339Nano), level, source, entry.SessionID, entry.Message)
	return err
}

// Query returns matching entries newest first, capped by limit.
func (r *LogRepo) Query(f logging.LogFilter, limit int) ([]logging.Entry, error) {
	if limit <= 0 {
		limit = f.Limit
	}
	if limit <= 0 {
		limit = defaultLogLimit
	}
	where := []string{}
	var args []any
	if len(f.Levels) > 0 {
		where, args = appendFilter(where, args, "level", f.Levels)
	}
	if len(f.Sources) > 0 {
		where, args = appendFilter(where, args, "source", f.Sources)
	}
	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if !f.Since.IsZero() {
		where = append(where, "ts >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		where = append(where, "message LIKE ?")
		args = append(args, "%"+s+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	rows, err := r.st.DB().Query(`SELECT ts, level, source, session_id, message
		FROM log_records`+clause+` ORDER BY ts DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]logging.Entry, 0, limit)
	for rows.Next() {
		var e logging.Entry
		var ts string
		if err := rows.Scan(&ts, &e.Level, &e.Source, &e.SessionID, &e.Message); err != nil {
			return nil, err
		}
		e.Time, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteOlderThan drops records older than cutoff and reports how many went.
func (r *LogRepo) DeleteOlderThan(cutoff time.Time) (int64, error) {
	res, err := r.st.DB().Exec(`DELETE FROM log_records WHERE ts < ?`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// TrimTo keeps only the newest maxRows records, so the index cannot grow without
// bound on a device with limited storage.
func (r *LogRepo) TrimTo(maxRows int) (int64, error) {
	if maxRows <= 0 {
		return 0, nil
	}
	res, err := r.st.DB().Exec(`DELETE FROM log_records WHERE id NOT IN
		(SELECT id FROM log_records ORDER BY ts DESC, id DESC LIMIT ?)`, maxRows)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Count reports how many records the index holds.
func (r *LogRepo) Count() (int64, error) {
	var n int64
	err := r.st.DB().QueryRow(`SELECT COUNT(*) FROM log_records`).Scan(&n)
	return n, err
}

// appendFilter turns a set of accepted values into one parameterised clause,
// keeping every caller off string interpolation of user input.
func appendFilter(where []string, args []any, column string, values []string) ([]string, []any) {
	ph := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		ph = append(ph, "?")
		args = append(args, v)
	}
	if len(ph) == 0 {
		return where, args
	}
	return append(where, column+" IN ("+strings.Join(ph, ",")+")"), args
}
