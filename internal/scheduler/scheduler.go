// Package scheduler starts and stops sessions on a wall-clock schedule. It runs
// entirely inside the server, so closing the browser does not affect it (FR-05).
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/repo"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// Schedule statuses (FR-05).
const (
	StatusDisabled    = "disabled"
	StatusScheduled   = "scheduled"
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusStopped     = "stopped"
	StatusFailed      = "failed"
	StatusMissed      = "missed"
	StatusSkippedBusy = "skipped_busy"
)

// Starter is the slice of the worker the scheduler needs.
type Starter interface {
	Active() bool
	Start(cfg streaming.Config) (string, error)
	Stop(reason streaming.StopReason) error
}

// StarterFuncs adapts the API package's start/stop calls.
type StarterFuncs struct {
	// BuildConfig produces the immutable snapshot for a scheduled session.
	BuildConfig func(scheduleID string, start, end time.Time) (streaming.Config, error)
	// StartSession records a new session in the database and returns its id.
	StartSession func(scheduleID, name string, cfg streaming.Config) (string, error)
	// FinishSession writes the terminal state of a session.
	FinishSession func(sessionID, state, stopReason, errCode, errMsg string) error
	// EndSchedule marks the schedule's terminal status.
	EndSchedule func(scheduleID, status, message string) error
	// Active reports whether the publisher slot is occupied.
	Active func() bool
}

// Scheduler polls for due schedules and owns their lifecycle.
type Scheduler struct {
	repo  *repo.ScheduleRepo
	log   *logging.Logger
	funcs StarterFuncs

	// tick is how often the scheduler looks for work. Five seconds keeps the
	// PRD's 5-second tolerance with margin for a busy device.
	tick time.Duration

	mu sync.Mutex
	// fired records execution keys already claimed, so a clock change or a
	// second tick cannot start the same window twice (FR-07).
	fired map[string]bool
	// running tracks schedule ids this process is currently executing.
	running map[string]string
}

// New builds a scheduler.
func New(r *repo.ScheduleRepo, log *logging.Logger, funcs StarterFuncs) *Scheduler {
	return &Scheduler{
		repo:    r,
		log:     log,
		funcs:   funcs,
		tick:    5 * time.Second,
		fired:   map[string]bool{},
		running: map[string]string{},
	}
}

// Run polls until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	// Reconcile once at startup so an interrupted service does not replay old
	// windows or double-publish (AC-16).
	s.reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickOnce(ctx)
		}
	}
}

// executionKey is the unique identity of one schedule window. Deriving it from
// the schedule id and start time means a re-trigger attempt is recognisable as
// a duplicate even across restarts.
func executionKey(scheduleID string, start time.Time) string {
	return scheduleID + "@" + start.UTC().Format(time.RFC3339)
}

// claim marks a key as taken. It returns false when another actor already owns
// it, which is how a second scheduler instance or a restarted service is kept
// from starting the same window twice.
func (s *Scheduler) claim(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired[key] {
		return false
	}
	s.fired[key] = true
	// Keep the map bounded; windows far in the past can never fire again.
	if len(s.fired) > 512 {
		for k := range s.fired {
			if k != key {
				delete(s.fired, k)
				break
			}
		}
	}
	return true
}

// tickOnce evaluates every enabled schedule against the current time.
func (s *Scheduler) tickOnce(ctx context.Context) {
	now := time.Now().UTC()
	schedules, err := s.repo.List()
	if err != nil {
		s.log.Error(logging.SourceScheduler, "", "Gagal membaca jadwal: %v", err)
		return
	}
	for _, sch := range schedules {
		if !sch.Enabled || sch.Status == StatusRunning {
			continue
		}
		switch {
		case now.After(sch.EndAt):
			// The window closed without ever running.
			if sch.Status == StatusScheduled {
				_ = s.repo.SetStatus(sch.ID, StatusMissed, "Waktu mulai terlewat saat server tidak aktif.")
				s.log.Warn(logging.SourceScheduler, "",
					"Jadwal %q terlewat: berakhir %s tanpa dijalankan", sch.Name, sch.EndAt.Local())
			}
		case !now.Before(sch.StartAt):
			s.claimAndRun(ctx, sch, now)
		}
	}
}

// claimAndRun starts a due schedule unless the publisher slot is busy.
func (s *Scheduler) claimAndRun(ctx context.Context, sch repo.Schedule, now time.Time) {
	key := executionKey(sch.ID, sch.StartAt)
	if !s.claim(key) {
		return
	}

	// A manual session occupies the single publisher slot. The schedule is
	// marked skipped rather than stopping the operator's session (AC-14).
	if s.funcs.Active != nil && s.funcs.Active() {
		_ = s.repo.SetStatus(sch.ID, StatusSkippedBusy,
			"Sesi lain sedang berjalan saat jadwal ini tiba.")
		_ = s.repo.SetExecutionKey(sch.ID, key)
		s.log.Warn(logging.SourceScheduler, "",
			"Jadwal %q dilewati: sesi lain sedang berjalan", sch.Name)
		return
	}

	cfg, err := s.funcs.BuildConfig(sch.ID, sch.StartAt, sch.EndAt)
	if err != nil {
		_ = s.repo.SetStatus(sch.ID, StatusFailed, err.Error())
		_ = s.repo.SetExecutionKey(sch.ID, key)
		s.log.Error(logging.SourceScheduler, "",
			"Persiapan jadwal %q gagal: %v", sch.Name, err)
		return
	}

	sessionID, err := s.funcs.StartSession(sch.ID, sch.Name, cfg)
	if err != nil {
		// Losing the race against a manual start is a busy slot, not a failure.
		if streaming.CodeOf(err) == streaming.CodeStreamBusy {
			_ = s.repo.SetStatus(sch.ID, StatusSkippedBusy, "Sesi lain memegang slot publisher.")
			_ = s.repo.SetExecutionKey(sch.ID, key)
			return
		}
		_ = s.repo.SetStatus(sch.ID, StatusFailed, err.Error())
		_ = s.repo.SetExecutionKey(sch.ID, key)
		s.log.Error(logging.SourceScheduler, "", "Sesi jadwal %q gagal dimulai: %v", sch.Name, err)
		return
	}

	_ = s.repo.SetStatus(sch.ID, StatusRunning, "Siaran berjalan.")
	_ = s.repo.SetExecutionKey(sch.ID, key)
	_ = s.repo.AttachSession(sch.ID, sessionID)

	s.mu.Lock()
	s.running[sch.ID] = sessionID
	s.mu.Unlock()

	s.log.Info(logging.SourceScheduler, "",
		"Jadwal %q dimulai sesi %s pada %s", sch.Name, sessionID, sch.StartAt.Local())
}

// reconcile at startup decides the fate of sessions and schedules that were
// interrupted by a restart (FR-07, AC-16).
func (s *Scheduler) reconcile(ctx context.Context) {
	now := time.Now().UTC()
	s.log.Info(logging.SourceScheduler, "", "Rekonsiliasi startup")

	unfinished, found, err := s.repo.Session().UnfinishedSession()
	if err != nil {
		s.log.Error(logging.SourceScheduler, "", "Rekonsiliasi sesi gagal: %v", err)
		return
	}
	if found {
		// A session the operator stopped must never come back to life.
		if unfinished.DesiredState == "stopped" {
			_ = s.funcs.FinishSession(unfinished.ID, streaming.StateStopped,
				"recovered", "", "Sesi dihentikan operator sebelum restart.")
			s.log.Info(logging.SourceScheduler, "",
				"Sesi %s tidak dipulihkan: dihentikan operator", unfinished.ID)
		} else if !unfinished.EndAt.IsZero() && now.After(unfinished.EndAt) {
			// The window already closed; resume would run an expired schedule.
			_ = s.funcs.FinishSession(unfinished.ID, streaming.StateCompleted,
				"recovered", streaming.CodeDeadlineReached,
				"Batas waktu sesi sudah terlewat saat server kembali.")
			s.log.Info(logging.SourceScheduler, "",
				"Sesi %s tidak dipulihkan: batas waktu terlewat", unfinished.ID)
		}
		// A resumable session is handled by the caller, which owns the worker.
	}

	schedules, err := s.repo.List()
	if err != nil {
		return
	}
	for _, sch := range schedules {
		switch sch.Status {
		case StatusRunning:
			// The process is gone; no publisher can still be running.
			if s.funcs.Active == nil || !s.funcs.Active() {
				_ = s.repo.SetStatus(sch.ID, StatusFailed,
					"Server dimulai ulang saat jadwal berjalan. Periksa media lalu jalankan ulang.")
			}
		case StatusScheduled:
			if !sch.EndAt.IsZero() && now.After(sch.EndAt) {
				_ = s.repo.SetStatus(sch.ID, StatusMissed,
					"Waktu mulai terlewat saat server tidak aktif.")
			} else if s.claim(executionKey(sch.ID, sch.StartAt)) {
				// Still inside its window: run it now.
				s.claimAndRun(ctx, sch, now)
			}
		}
	}
}

// StopSchedule ends a running schedule's session, used when an operator stops
// a session that a schedule had started.
func (s *Scheduler) StopSchedule(scheduleID, reason string) error {
	return s.funcs.EndSchedule(scheduleID, StatusStopped, reason)
}

// SessionIDFor returns the session a schedule started.
func (s *Scheduler) SessionIDFor(scheduleID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.running[scheduleID]
	return id, ok
}

// Forget removes a schedule's runtime tracking once it ends.
func (s *Scheduler) Forget(scheduleID string) {
	s.mu.Lock()
	delete(s.running, scheduleID)
	s.mu.Unlock()
}

// newID returns a random identifier for execution bookkeeping.
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("exec-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// errNoSchedule reports that an operation referenced an unknown schedule.
var errNoSchedule = errors.New("schedule not found")
