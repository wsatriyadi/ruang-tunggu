package streaming

import (
	"context"
	"strings"
	"time"
)

// backoffDelays is the retry schedule from FR-07, capped by the last entry.
var backoffDelays = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	30 * time.Second,
}

// maxAttempts bounds consecutive retries so a broken configuration cannot
// loop forever.
const maxAttempts = 10

// backoffFor returns the delay before retry number `attempt` (1-based),
// including a small jitter so retries from several causes do not align.
func backoffFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	idx := attempt - 1
	if idx >= len(backoffDelays) {
		idx = len(backoffDelays) - 1
	}
	base := backoffDelays[idx]
	// ±10% jitter, deterministic per attempt so a retry keeps its identity.
	jitter := time.Duration(float64(base) * 0.1)
	return base - jitter + time.Duration(attempt%3)*jitter
}

// classifyExit decides whether a publisher exit is worth retrying.
//
// Authentication rejection, bad configuration and unreadable media are
// terminal: retrying them just burns attempts and hides the real problem.
func classifyExitCode(exitCode int, output string) (retryable bool, code string, message string) {
	lower := lowerASCII(output)

	switch {
	case containsAny(lower, "error opening url", "unauthorized", "authentication",
		"stream key", "invalid url", "access denied", "forbidden",
		"403 forbidden", "connection refused", "server returned 4",
		// FFmpeg reports a refused handshake or a rejected publish target with
		// this generic message, so it must not be treated as a transient fault.
		"error opening output files", "could not write header",
		"handshake failed"):
		// A rejected key or a blocked host is refused identically on every
		// attempt, so retrying only burns the attempt budget (FR-07).
		return false, CodeInvalidTarget,
			"YouTube menolak koneksi. Periksa stream key dan URL ingest di Settings."
	case containsAny(lower, "unknown encoder", "invalid data found",
		"could not find codec parameters", "permission denied",
		"no such file or directory", "unrecognized option",
		"error splitting the argument list", "option not found",
		"invalid argument"):
		// Bad input, a missing file, or a command line this build rejects: the
		// same failure repeats on every attempt, so retrying is pointless.
		return false, CodePreflightFailed,
			"FFmpeg tidak dapat memproses input. Periksa media dan lokasi executable."
	case exitCode == 0:
		// The playlist ran to its end.
		return false, "", ""
	case containsAny(lower, "non-monotonous", "non-monotonic"):
		// FFmpeg reports a timestamp hiccup as a warning it clamps and recovers
		// from, not as a fatal error, so the process can still exit 0 with this
		// in its log. Treating it as terminal would kill otherwise healthy
		// sessions. It is logged as a warning for the operator instead.
		return true, CodePublisherFailed,
			"FFmpeg melaporkan timestamp tidak monoton dan menyesuaikan otomatis."
	case exitCode == 255:
		// FFmpeg's generic failure code; usually transient.
		return true, CodePublisherFailed, "Proses publisher berhenti dengan kode 255."
	default:
		return true, CodePublisherFailed,
			"Proses publisher berhenti dengan kode " + itoa(exitCode) + "."
	}
}

// stallThreshold is how long media time may stop advancing before the worker
// treats the connection as wedged and restarts the publisher.
const stallThreshold = 30 * time.Second

// Checkpoint is the durable record of a session, written periodically so a
// restart can resume without replaying the whole queue (FR-07).
type Checkpoint struct {
	SessionID string `json:"session_id"`
	// DesiredState is the operator's intent: run or stop. A stopped session
	// must never be revived by recovery.
	DesiredState  string `json:"desired_state"`
	ObservedState string `json:"observed_state"`
	Cycle         int    `json:"cycle"`
	ItemIndex     int    `json:"item_index"`
	// OrderedMediaIDs records the realised order of the current cycle.
	OrderedMediaIDs []string `json:"ordered_media_ids"`
	PositionSeconds float64  `json:"position_seconds"`
	// EndAt is the schedule deadline; recovery respects it.
	EndAt time.Time `json:"end_at"`
	// Attempt counts consecutive retries for the current connection.
	Attempt   int       `json:"attempt"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Persister stores checkpoints and records playback rows.
type Persister interface {
	SaveCheckpoint(cp Checkpoint) error
	MarkItemPlaying(sessionID string, cycle, index int, mediaID string) error
	MarkItemFinished(sessionID string, cycle, index int, status, errMsg string) error
}

// Store is the service surface the API and scheduler use.
type Store interface {
	Persister
	LoadCheckpoint(sessionID string) (Checkpoint, bool, error)
}

// contextSleep waits for d unless ctx ends first, reporting false if cancelled.
func contextSleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func lowerASCII(s string) string { return strings.ToLower(s) }

// containsAny reports whether haystack contains any needle.
func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
