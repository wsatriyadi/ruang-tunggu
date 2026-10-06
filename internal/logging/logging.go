// Package logging provides structured, redacted logging with rotation, a
// database index for the Logs page, and a fan-out to SSE subscribers (FR-09).
package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level is a log severity.
type Level int

// Severity levels, ordered by increasing importance.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel maps a wire value to a Level.
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// String renders the level as it appears in logs and API responses.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// Sources of log records, so the Logs page can filter by origin.
const (
	SourceApp       = "app"
	SourceScheduler = "scheduler"
	SourcePublisher = "publisher"
	SourceAudit     = "audit"
	SourceUpload    = "upload"
)

// Entry is one log record.
type Entry struct {
	Time      time.Time `json:"ts"`
	Level     string    `json:"level"`
	Source    string    `json:"source"`
	SessionID string    `json:"session_id,omitempty"`
	Message   string    `json:"message"`
}

// Sink receives log entries in real time, typically an SSE hub.
type Sink interface {
	Publish(Entry)
}

// Logger writes entries to a rotating file and a queryable index.
type Logger struct {
	mu         sync.Mutex
	dir        string
	file       *os.File
	maxBytes   int64
	maxDays    int
	maxRecords int
	sinks      []Sink
	redact     *Redactor

	// index is retained in memory for fast log queries; the database copy is
	// the durable one.
	index []Entry
}

// Options configures a Logger.
type Options struct {
	Dir          string
	MaxBytes     int64
	MaxDays      int
	MaxFileBytes int64
}

// New creates a logger writing into dir.
func New(opts Options) (*Logger, error) {
	if opts.MaxBytes <= 0 {
		// Default retention from PRD §9: 100 MiB total across rotated files.
		opts.MaxBytes = 100 << 20
	}
	if opts.MaxDays <= 0 {
		opts.MaxDays = 7
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = 8 << 20
	}
	if err := os.MkdirAll(opts.Dir, 0o750); err != nil {
		return nil, err
	}
	l := &Logger{
		dir:      opts.Dir,
		maxBytes: opts.MaxBytes,
		maxDays:  opts.MaxDays,
		redact:   NewRedactor(),
	}
	if err := l.rotate(); err != nil {
		return nil, err
	}
	return l, nil
}

// AddSink registers a real-time destination.
func (l *Logger) AddSink(s Sink) {
	l.mu.Lock()
	l.sinks = append(l.sinks, s)
	l.mu.Unlock()
}

func (l *Logger) rotate() error {
	name := filepath.Join(l.dir, "app.log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	old := l.file
	l.file = f
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// SetRedactor replaces the redactor, letting the app register the live stream
// key once it is known so it is masked everywhere.
func (l *Logger) SetRedactor(r *Redactor) {
	l.mu.Lock()
	l.redact = r
	l.mu.Unlock()
}

// Log writes an entry.
func (l *Logger) Log(level Level, source, sessionID, message string) {
	l.mu.Lock()
	entry := Entry{
		Time:      time.Now().UTC(),
		Level:     level.String(),
		Source:    source,
		SessionID: sessionID,
		Message:   l.redact.String(message),
	}
	l.mu.Unlock()

	l.writeFile(entry)
	l.retain(entry)
	for _, s := range l.sinksSnapshot() {
		s.Publish(entry)
	}
}

// Debug logs at debug level.
func (l *Logger) Debug(source, sessionID, format string, args ...any) {
	l.Log(LevelDebug, source, sessionID, fmt.Sprintf(format, args...))
}

// Info logs at info level.
func (l *Logger) Info(source, sessionID, format string, args ...any) {
	l.Log(LevelInfo, source, sessionID, fmt.Sprintf(format, args...))
}

// Warn logs at warning level.
func (l *Logger) Warn(source, sessionID, format string, args ...any) {
	l.Log(LevelWarn, source, sessionID, fmt.Sprintf(format, args...))
}

// Error logs at error level.
func (l *Logger) Error(source, sessionID, format string, args ...any) {
	l.Log(LevelError, source, sessionID, fmt.Sprintf(format, args...))
}

func (l *Logger) sinksSnapshot() []Sink {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Sink, len(l.sinks))
	copy(out, l.sinks)
	return out
}

func (l *Logger) writeFile(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = l.file.Write(append(line, '\n'))
}

// retain keeps a bounded in-memory window for fast recent-log queries.
func (l *Logger) retain(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	const window = 5000
	l.index = append(l.index, e)
	if len(l.index) > window {
		l.index = append(l.index[:0], l.index[len(l.index)-window:]...)
	}
}

// Recent returns buffered entries matching the filter, newest last.
func (l *Logger) Recent(filter LogFilter, limit int) []Entry {
	l.mu.Lock()
	snapshot := make([]Entry, len(l.index))
	copy(snapshot, l.index)
	l.mu.Unlock()

	out := make([]Entry, 0, len(snapshot))
	for _, e := range snapshot {
		if filter.Matches(e) {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// LogFilter narrows log queries.
type LogFilter struct {
	Levels    []string
	Sources   []string
	SessionID string
	Search    string
	Limit     int
	Since     time.Time
}

// Matches reports whether an entry satisfies the filter.
func (f LogFilter) Matches(e Entry) bool {
	if len(f.Levels) > 0 && !containsFold(f.Levels, e.Level) {
		return false
	}
	if len(f.Sources) > 0 && !containsFold(f.Sources, e.Source) {
		return false
	}
	if f.SessionID != "" && e.SessionID != f.SessionID {
		return false
	}
	if !f.Since.IsZero() && e.Time.Before(f.Since) {
		return false
	}
	if f.Search != "" && !strings.Contains(strings.ToLower(e.Message), strings.ToLower(f.Search)) {
		return false
	}
	return true
}

func containsFold(list []string, v string) bool {
	for _, item := range list {
		if strings.EqualFold(item, v) {
			return true
		}
	}
	return false
}

// Rotate trims rotated files beyond the retention limits.
func (l *Logger) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return err
	}
	type fileInfo struct {
		path string
		size int64
		mod  time.Time
	}
	var files []fileInfo
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(l.dir, e.Name())
		files = append(files, fileInfo{path: p, size: info.Size(), mod: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })

	cutoff := time.Now().AddDate(0, 0, -l.maxDays)
	for _, f := range files {
		if f.path == l.file.Name() {
			continue
		}
		if f.mod.Before(cutoff) {
			_ = os.Remove(f.path)
			total -= f.size
		}
	}
	// Drop oldest first until the total retention budget is met.
	for _, f := range files {
		if total <= l.maxBytes {
			break
		}
		if f.path == l.file.Name() {
			continue
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
		}
	}
	return nil
}

// Close flushes and closes the log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

// WriteExport writes sanitized log lines as plain text. The caller supplies the
// entries; this keeps redaction and formatting in one place.
func WriteExport(w io.Writer, entries []Entry, tz *time.Location) error {
	for _, e := range entries {
		ts := e.Time
		if tz != nil {
			ts = ts.In(tz)
		}
		if _, err := fmt.Fprintf(w, "%s  %-5s %-9s %s %s\n",
			ts.Format("2006-01-02 15:04:05.000"), strings.ToUpper(e.Level), e.Source, e.SessionID, e.Message); err != nil {
			return err
		}
	}
	return nil
}

// Redactor masks secrets before they reach a log, event, or export.
type Redactor struct {
	mu       sync.RWMutex
	literals map[string]struct{}
}

// NewRedactor builds a redactor with the built-in credential patterns.
func NewRedactor() *Redactor {
	return &Redactor{literals: map[string]struct{}{}}
}

// Add registers a literal secret, such as the active stream key.
func (r *Redactor) Add(secret string) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 4 {
		return
	}
	r.mu.Lock()
	r.literals[secret] = struct{}{}
	r.mu.Unlock()
}

// Forget removes a previously registered literal.
func (r *Redactor) Forget(secret string) {
	r.mu.Lock()
	delete(r.literals, secret)
	r.mu.Unlock()
}

// sensitiveArgs matches credential-shaped substrings inside a log message.
// Patterns target the shapes FFmpeg and our own code actually produce.
var sensitiveArgs = []*regexp.Regexp{
	// Stream keys appear as /live2/<key> or as the last path segment of a URL.
	regexp.MustCompile(`(?i)(/live2/)[A-Za-z0-9_\-]{6,}`),
	regexp.MustCompile(`(?i)([?&](?:key|stream_key|token|sig|password)=)[^\s&]+`),
	regexp.MustCompile(`(?i)\b(stream[_ -]?key|password|passwd|secret|authorization|bearer)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)\b(ytls_session|xsrf|csrf)[=:]\s*[A-Za-z0-9_\-]+`),
	// Credentials embedded in an ingest URL.
	regexp.MustCompile(`(?i)(rtmps?://)[^/\s:@]+:[^/\s@]+@`),
}

// Mask is the replacement written in place of a secret.
const Mask = "***REDACTED***"

// String redacts secrets in a message.
func (r *Redactor) String(s string) string {
	if s == "" {
		return s
	}
	r.mu.RLock()
	literals := make([]string, 0, len(r.literals))
	for k := range r.literals {
		literals = append(literals, k)
	}
	r.mu.RUnlock()
	// Longest first so overlapping secrets do not leave fragments behind.
	sort.Slice(literals, func(i, j int) bool { return len(literals[i]) > len(literals[j]) })
	for _, secret := range literals {
		s = strings.ReplaceAll(s, secret, Mask)
	}
	for _, re := range sensitiveArgs {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			// Keep the structural prefix, mask only the value.
			if idx := strings.Index(match, "="); idx >= 0 && len(match) > idx+1 {
				return match[:idx+1] + Mask
			}
			if idx := strings.Index(match, ":"); idx >= 0 && len(match) > idx+1 {
				return match[:idx+1] + " " + Mask
			}
			if idx := strings.LastIndex(match, "/"); idx >= 0 {
				return match[:idx+1] + Mask
			}
			return Mask
		})
	}
	return s
}

// Context keys for passing the logger and session id through call chains.
type ctxKey int

const (
	ctxKeyLogger ctxKey = iota
	ctxKeySession
)

// WithLogger attaches a logger to a context.
func WithLogger(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, ctxKeyLogger, l)
}

// FromContext returns the logger stored in ctx, or nil.
func FromContext(ctx context.Context) *Logger {
	l, _ := ctx.Value(ctxKeyLogger).(*Logger)
	return l
}

// WithSession attaches a session id for log correlation.
func WithSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeySession, id)
}

// SessionFromContext returns the session id stored in ctx.
func SessionFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeySession).(string)
	return id
}

// Ctx returns a logger bound to a context, falling back to a given logger.
func Ctx(ctx context.Context, fallback *Logger) *Logger {
	if l := FromContext(ctx); l != nil {
		return l
	}
	return fallback
}
