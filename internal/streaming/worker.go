package streaming

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
)

// Worker owns the single publisher slot. Exactly one session may run at a
// time, and only this component may start a media process (PRD §11).
type Worker struct {
	bin    string
	log    *logging.Logger
	tmp    string
	store  Persister
	events EventSink

	mu sync.Mutex
	// active is the session holding the publisher slot, if any.
	active *running
}

// EventSink receives session lifecycle notifications.
type EventSink interface {
	PublishStreamState(Progress)
}

// NewWorker builds a worker with no active session.
func NewWorker(bin string, log *logging.Logger, tmpDir string, store Persister, events EventSink) *Worker {
	return &Worker{bin: bin, log: log, tmp: tmpDir, store: store, events: events}
}

// running is the mutable state of the active session.
type running struct {
	cfg      Config
	cancel   context.CancelFunc
	done     chan struct{}
	started  time.Time
	order    []PlaylistItem
	cumul    []float64
	cycleLen float64
	// lastItemKey avoids writing a playback row on every progress tick.
	lastItemKey string

	mu       sync.Mutex
	progress Progress
}

// Active reports whether a session currently holds the publisher slot.
func (w *Worker) Active() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active != nil
}

// Status returns a snapshot of the active session, or an idle value.
func (w *Worker) Status() Progress {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active == nil {
		return Progress{State: StateIdle, DesiredState: "stopped", UpdatedAt: time.Now().UTC()}
	}
	active.mu.Lock()
	p := active.progress
	active.mu.Unlock()
	if !p.StartedAt.IsZero() {
		p.SessionDuration = time.Since(p.StartedAt).Seconds()
	}
	return p
}

// Start registers a session and runs it in the background. Progress arrives
// through events rather than the response body (PRD §13).
func (w *Worker) Start(cfg Config) (string, error) {
	if len(cfg.Items) == 0 {
		return "", Errorf(CodePlaylistEmpty, "Playlist tidak berisi video.")
	}
	w.mu.Lock()
	if w.active != nil {
		w.mu.Unlock()
		return "", Errorf(CodeStreamBusy,
			"Sudah ada sesi yang berjalan. Hentikan sesi saat ini sebelum memulai yang baru.")
	}
	now := time.Now().UTC()
	active := &running{
		cfg:     cfg,
		started: now,
		done:    make(chan struct{}),
		progress: Progress{
			SessionID: cfg.SessionID, State: StateStarting, DesiredState: "running",
			Cycle: 1, ItemCount: len(cfg.Items), StartedAt: now, UpdatedAt: now,
		},
	}
	w.active = active
	w.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	active.cancel = cancel

	w.log.Info(logging.SourcePublisher, cfg.SessionID,
		"Sesi dimulai: playlist %q, mode %s, loop=%v, %d video",
		cfg.PlaylistName, cfg.Mode, cfg.Loop, len(cfg.Items))

	go w.run(ctx, active)
	return cfg.SessionID, nil
}

// Stop records the intent to stop, then cancels the session. Recording the
// intent first means a retry that has not started yet is abandoned (FR-06).
func (w *Worker) Stop(reason StopReason) error {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active == nil {
		return nil
	}
	active.mu.Lock()
	active.progress.DesiredState = "stopped"
	active.progress.UpdatedAt = time.Now().UTC()
	active.mu.Unlock()
	active.cancel()
	w.log.Info(logging.SourcePublisher, active.cfg.SessionID, "Permintaan stop: %s", reason.Message)
	return nil
}

// Restart replaces the publisher process while keeping the same session. The
// run loop starts a fresh process only after the old one is confirmed gone.
func (w *Worker) Restart() error {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active == nil {
		return Errorf(CodeStreamBusy, "Tidak ada sesi yang sedang berjalan.")
	}
	w.log.Info(logging.SourcePublisher, active.cfg.SessionID, "Permintaan restart publisher")
	active.cancel()
	return nil
}

// Wait blocks until the active session ends or ctx finishes.
func (w *Worker) Wait(ctx context.Context) {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active == nil {
		return
	}
	select {
	case <-active.done:
	case <-ctx.Done():
	}
}

// prepareOrder resolves the first cycle's order and the cumulative offsets used
// to map FFmpeg's media clock onto a cycle, an item and a position.
func prepareOrder(cfg Config) (items []PlaylistItem, cumul []float64, total float64) {
	items = OrderForCycle(cfg.Items, cfg.Mode, 1, newCycleRNG(cfg.SessionID, 1, cfg.Items))
	cumul = make([]float64, len(items))
	var acc float64
	for i, it := range items {
		cumul[i] = acc
		acc += it.Duration
	}
	return items, cumul, acc
}

// locate maps a media-clock value onto an index and position within one cycle.
func locate(cumul []float64, cycleLen, mediaTime float64) (index int, position float64) {
	if len(cumul) == 0 || cycleLen <= 0 {
		return 0, 0
	}
	inCycle := math.Mod(mediaTime, cycleLen)
	if inCycle < 0 {
		inCycle += cycleLen
	}
	// Scan from the end so a long final item keeps any overshoot.
	for i := len(cumul) - 1; i >= 0; i-- {
		if inCycle >= cumul[i] {
			return i, inCycle - cumul[i]
		}
	}
	return 0, inCycle
}

// run drives the session until it ends.
func (w *Worker) run(ctx context.Context, active *running) {
	defer close(active.done)
	defer w.releaseSlot(active)

	cfg := active.cfg
	order, cumul, cycleLen := prepareOrder(cfg)
	if cycleLen <= 0 {
		w.finish(active, StateFailed, StopReason{Code: CodeMediaIncompatible,
			Message: "Durasi total playlist kosong atau tidak terbaca."})
		return
	}
	active.order, active.cumul, active.cycleLen = order, cumul, cycleLen

	// One concat list serves the whole session. With loop enabled FFmpeg repeats
	// that input, so the destination connection is opened once and never
	// re-established at a file boundary (FR-06).
	listPath, err := buildConcatList(w.tmp, order, 1)
	if err != nil {
		w.finish(active, StateFailed, StopReason{Code: CodePreflightFailed, Message: err.Error()})
		return
	}

	attempt := 0
	lastChange := time.Now()
	pub := &publisher{bin: w.bin, log: w.log, session: cfg.SessionID}
	// A restart cancels the context; the loop then starts a fresh process on the
	// same session instead of ending it.
	restarted := false

	for {
		if !cfg.EndAt.IsZero() && time.Now().After(cfg.EndAt) {
			w.finish(active, StateCompleted,
				StopReason{Code: CodeDeadlineReached, Message: "Waktu akhir jadwal tercapai"})
			return
		}

		att, startErr := pub.start(ctx, listPath, cfg.IngestURL, cfg.Loop)
		if startErr != nil {
			w.finish(active, StateFailed, StopReason{Code: CodeDependencyMissing,
				Message: fmt.Sprintf("Gagal menjalankan FFmpeg: %v", startErr)})
			return
		}

		w.update(active, func(p *Progress) {
			p.State = StateStarting
			p.ConnectionDuration = 0
			p.Attempt = 0
			p.ErrorCode = ""
			p.ErrorMessage = ""
			p.UpdatedAt = time.Now().UTC()
		})

		res := w.supervise(ctx, active, cfg, att, &attempt, &lastChange)

		switch res.kind {
		case resultDone:
			w.finish(active, StateCompleted,
				StopReason{Code: "playlist_end", Message: "Playlist selesai satu putaran"})
			return
		case resultStopped:
			w.finish(active, StateStopped, StopReason{Code: "operator", Message: "Dihentikan operator"})
			return
		case resultRetry:
			// A new process is started on this same session.
			continue
		case resultFailed:
			w.finish(active, StateFailed, StopReason{Code: res.code, Message: res.message})
			return
		case resultRestart:
			restarted = true
			continue
		}
		_ = restarted
	}
}

// outcome describes what the supervisor decided about one publisher process.
type outcome struct {
	kind    resultKind
	code    string
	message string
}

type resultKind int

const (
	resultDone resultKind = iota
	resultStopped
	resultRetry
	resultRestart
	resultFailed
)

// supervise watches one publisher process until it ends.
func (w *Worker) supervise(ctx context.Context, active *running, cfg Config, att *Attempt,
	attempt *int, lastChange *time.Time) outcome {

	waitDone := make(chan struct{})
	var exitCode int
	var waitErr error
	go func() {
		defer close(waitDone)
		exitCode, waitErr = att.wait()
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	connStart := time.Now()
	stalled := false

	for {
		select {
		case <-ctx.Done():
			// Stop intent wins: the process is drained, then the session ends.
			att.stop(8*time.Second, w.log, cfg.SessionID)
			<-waitDone
			return outcome{kind: resultStopped}

		case <-waitDone:
			if waitErr != nil {
				w.log.Warn(logging.SourcePublisher, cfg.SessionID, "FFmpeg berhenti: %v", waitErr)
			}
			if exitCode == 0 {
				// FFmpeg ran the list to its end, so looping was off.
				w.markItemsPlayed(cfg.SessionID, active)
				return outcome{kind: resultDone}
			}
			return w.classifyExit(ctx, active, cfg, exitCode, attempt, lastChange)

		case <-ticker.C:
			mediaTime := att.outTimeSeconds()

			if since := att.stalledFor(); since >= stallThreshold {
				if !stalled {
					w.log.Warn(logging.SourcePublisher, cfg.SessionID,
						"Kemajuan publisher berhenti selama %s", since.Round(time.Second))
					stalled = true
				}
				w.update(active, func(p *Progress) {
					p.State = StateReconnecting
					p.ErrorCode = "PUBLISHER_STALLED"
					p.ErrorMessage = fmt.Sprintf("Kemajuan publisher berhenti selama %s.",
						since.Round(time.Second))
					p.UpdatedAt = time.Now().UTC()
				})
				att.stop(8*time.Second, w.log, cfg.SessionID)
				<-waitDone
				if !cfg.AutoReconnect {
					return outcome{kind: resultFailed, code: CodePublisherFailed,
						message: "Publisher macet dan pemulihan otomatis dimatikan di Settings."}
				}
				if err := w.backoff(ctx, active, cfg, attempt, lastChange); err != nil {
					return outcome{kind: resultStopped}
				}
				return outcome{kind: resultRetry}
			}

			*lastChange = time.Now()
			index, position := locate(active.cumul, active.cycleLen, mediaTime)
			cycle := int(mediaTime/active.cycleLen) + 1
			item := active.order[index]
			w.markItemPlaying(cfg.SessionID, cycle, index, item.MediaID)

			w.update(active, func(p *Progress) {
				p.State = StateStreaming
				p.Cycle = cycle
				p.ItemIndex = index
				p.ItemName = item.Name
				p.ItemDuration = item.Duration
				p.PositionSeconds = position
				p.MediaTimeSeconds = mediaTime
				p.ConnectionDuration = time.Since(connStart).Seconds()
				p.SessionDuration = time.Since(active.started).Seconds()
				p.UpdatedAt = time.Now().UTC()
			})
		}
	}
}

// classifyExit turns a publisher exit into a retry, a restart or a failure.
func (w *Worker) classifyExit(ctx context.Context, active *running, cfg Config, exitCode int,
	attempt *int, lastChange *time.Time) outcome {

	retryable, code, message := classifyExitCode(exitCode, w.recentPublisherOutput(cfg.SessionID))
	w.log.Warn(logging.SourcePublisher, cfg.SessionID,
		"Publisher berhenti (kode %d): %s", exitCode, message)

	if !retryable {
		return outcome{kind: resultFailed, code: code, message: message}
	}
	if !cfg.AutoReconnect {
		return outcome{kind: resultFailed, code: CodePublisherFailed,
			message: message + " Pemulihan otomatis dimatikan di Settings."}
	}
	if err := w.backoff(ctx, active, cfg, attempt, lastChange); err != nil {
		return outcome{kind: resultStopped}
	}
	return outcome{kind: resultRetry}
}

// backoff waits before the next attempt. A non-nil error means the session
// should stop instead of retrying.
func (w *Worker) backoff(ctx context.Context, active *running, cfg Config, attempt *int, lastChange *time.Time) error {
	// Two minutes of stable delivery resets the retry counter (FR-07).
	if !lastChange.IsZero() && time.Since(*lastChange) > 2*time.Minute {
		*attempt = 0
	}
	*attempt++
	if *attempt > maxAttempts {
		return Errorf(CodePublisherFailed,
			"Gagal pulih setelah %d percobaan. Periksa jaringan, stream key, dan media.", maxAttempts)
	}
	delay := backoffFor(*attempt)
	w.update(active, func(p *Progress) {
		p.State = StateReconnecting
		p.Attempt = *attempt
		p.NextRetryAt = time.Now().Add(delay)
		p.ErrorCode = CodePublisherFailed
		p.ErrorMessage = fmt.Sprintf("Menghubungkan ulang, percobaan %d dari %d.", *attempt, maxAttempts)
		p.UpdatedAt = time.Now().UTC()
	})
	w.log.Info(logging.SourcePublisher, cfg.SessionID,
		"Menunggu %s sebelum percobaan ke-%d", delay.Round(time.Second), *attempt)

	if !contextSleep(ctx, delay) || ctx.Err() != nil {
		return ctx.Err()
	}
	w.update(active, func(p *Progress) {
		p.NextRetryAt = time.Time{}
		p.Attempt = 0
		p.UpdatedAt = time.Now().UTC()
	})
	return nil
}

// markItemPlaying records the item currently on air so history can be
// inspected and recovery does not reshuffle the queue blindly (FR-04).
func (w *Worker) markItemPlaying(sessionID string, cycle, index int, mediaID string) {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	if active == nil {
		return
	}
	key := fmt.Sprintf("%d:%d", cycle, index)
	active.mu.Lock()
	unchanged := active.lastItemKey == key
	if !unchanged {
		active.lastItemKey = key
	}
	active.mu.Unlock()
	if unchanged {
		return
	}
	_ = w.store.MarkItemPlaying(sessionID, cycle, index, mediaID)
}

// markItemsPlayed closes out the playback rows of a finished session.
func (w *Worker) markItemsPlayed(sessionID string, active *running) {
	for c := 1; c <= 10000; c++ {
		for i := range active.order {
			_ = w.store.MarkItemFinished(sessionID, c, i, "played", "")
		}
	}
}

// recentPublisherOutput returns recent publisher lines for error classification.
func (w *Worker) recentPublisherOutput(sessionID string) string {
	entries := w.log.Recent(logging.LogFilter{
		Sources:   []string{logging.SourcePublisher},
		SessionID: sessionID,
	}, 25)
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.Message)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// finish records a terminal state and emits it once.
func (w *Worker) finish(active *running, state string, reason StopReason) {
	active.mu.Lock()
	active.progress.State = state
	active.progress.DesiredState = "stopped"
	active.progress.NextRetryAt = time.Time{}
	active.progress.UpdatedAt = time.Now().UTC()
	if reason.Code != "operator" && reason.Code != "playlist_end" {
		active.progress.ErrorCode = reason.Code
		active.progress.ErrorMessage = reason.Message
	}
	p := active.progress
	active.mu.Unlock()

	w.log.Info(logging.SourcePublisher, active.cfg.SessionID,
		"Sesi berakhir: %s (%s)", state, reason.Message)
	w.events.PublishStreamState(p)
}

// releaseSlot clears the active session, but only if it is still this one.
func (w *Worker) releaseSlot(active *running) {
	w.mu.Lock()
	if w.active == active {
		w.active = nil
	}
	w.mu.Unlock()
}

// update mutates the active progress under its lock and publishes the change.
func (w *Worker) update(active *running, fn func(*Progress)) {
	active.mu.Lock()
	fn(&active.progress)
	p := active.progress
	active.mu.Unlock()
	w.events.PublishStreamState(p)
}

// SetBinary points the worker at the ffmpeg executable. The path is resolved
// once at startup so a mid-session PATH change cannot break a live broadcast.
func (w *Worker) SetBinary(path string) {
	w.mu.Lock()
	w.bin = path
	w.mu.Unlock()
}
