// Package app wires every component together and owns the process lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/wisnu/ylivemasadi/internal/api"
	"github.com/wisnu/ylivemasadi/internal/auth"
	"github.com/wisnu/ylivemasadi/internal/config"
	"github.com/wisnu/ylivemasadi/internal/jobs"
	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/monitoring"
	"github.com/wisnu/ylivemasadi/internal/repo"
	"github.com/wisnu/ylivemasadi/internal/scheduler"
	"github.com/wisnu/ylivemasadi/internal/secrets"
	"github.com/wisnu/ylivemasadi/internal/store"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// App holds the assembled service graph.
type App struct {
	cfg    *config.Config
	log    *logging.Logger
	server *http.Server

	store     *store.Store
	worker    *streaming.Worker
	processor *jobs.Processor
	uploader  *repo.UploadManager
	monitor   *monitoring.Monitor
	auth      *auth.Service
	hub       *api.Hub
	sched     *scheduler.Scheduler
	mediaRepo *repo.MediaRepo
	sessions  *repo.SessionRepo
}

// New assembles the application from a resolved configuration.
func New(cfg *config.Config, version string) (*App, error) {
	log, err := logging.New(logging.Options{Dir: cfg.LogDir})
	if err != nil {
		return nil, fmt.Errorf("inisialisasi log: %w", err)
	}
	log.Info(logging.SourceApp, "", "YouTube Live Server %s starting (%s/%s)", version, runtime.GOOS, runtime.GOARCH)

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}

	box, err := secrets.Load(cfg.KeyPath())
	if err != nil {
		return nil, err
	}

	authSvc := auth.New(db, cfg.SessionTTL, cfg.SecureCookies)
	hub := api.NewHub(log)
	// Log records are mirrored into the database so the Logs page can query
	// beyond the in-memory window.
	logStore := repo.NewLogRepo(db)
	log.AddSink(logSink{repo: logStore, hub: hub})

	mediaRepo := repo.NewMediaRepo(db)
	uploadRepo := repo.NewUploadRepo(db)
	playlistRepo := repo.NewPlaylistRepo(db)
	scheduleRepo := repo.NewScheduleRepo(db)
	sessionRepo := repo.NewSessionRepo(db)
	settingsRepo := repo.NewSettingsRepo(db)
	targetRepo := repo.NewTargetRepo(db)
	auditRepo := repo.NewAuditRepo(db)

	profile := media.SDRProfile()
	binaries := media.NewBinaries(cfg.FFmpegPath, cfg.FFprobePath)

	// The monitored interface defaults to the one carrying the default route.
	iface := cfg.NetInterface
	if iface == "" {
		iface = monitoring.DefaultInterface()
	}
	monitor := monitoring.New(cfg.MediaDir, iface)

	uploader := repo.NewUploadManager(uploadRepo, mediaRepo, log, repo.UploadManagerOptions{
		TempDir:     cfg.TempDir,
		MediaDir:    cfg.MediaDir,
		MaxFileSize: cfg.MaxFileSize,
		Quota:       cfg.MediaQuota,
		Reserve:     cfg.DiskReserveBytes,
		DiskPath:    cfg.MediaDir,
	})

	sessions := sessionRepo
	worker := streaming.NewWorker("", log, cfg.TempDir, sessions, hub)

	processor := jobs.NewProcessor(mediaRepo, uploadRepo, binaries, log, jobs.ProcessorOptions{
		MediaDir: cfg.MediaDir,
		ThumbDir: cfg.ThumbnailDir,
		Profile:  profile,
		Notify:   func(m repo.Media) { hub.PublishMedia(m) },
	})

	server := api.NewServer(api.Deps{
		Cfg: cfg, Store: db, Auth: authSvc, Secrets: box, Log: log, Hub: hub,
		Monitor: monitor, Profile: profile,
		Media: mediaRepo, Uploads: uploadRepo, Uploader: uploader,
		Playlists: playlistRepo, Schedules: scheduleRepo, Sessions: sessionRepo,
		Settings: settingsRepo, Targets: targetRepo, Logs: logStore, Audit: auditRepo,
		Worker: worker, Processor: processor, Binaries: binaries, Version: version,
	})

	sched := scheduler.New(scheduleRepo, log, scheduler.StarterFuncs{
		BuildConfig: func(scheduleID string, start, end time.Time) (streaming.Config, error) {
			return server.BuildScheduleConfig(scheduleID, start, end)
		},
		StartSession: func(scheduleID, name string, cfg streaming.Config) (string, error) {
			return server.StartScheduledSession(scheduleID, name, cfg)
		},
		FinishSession: func(sessionID, state, stopReason, errCode, errMsg string) error {
			return server.FinishSession(sessionID, state, stopReason, errCode, errMsg)
		},
		EndSchedule: func(scheduleID, status, message string) error {
			return scheduleRepo.SetStatus(scheduleID, status, message)
		},
		Active: worker.Active,
	})

	// The worker needs the resolved ffmpeg path, which is only known after the
	// tool check, so the check runs first and the result is fed back.
	deps := binaries.Check(context.Background())
	if !deps.FFmpegFound {
		log.Warn(logging.SourceApp, "",
			"FFmpeg tidak ditemukan. Pasang FFmpeg sebelum menyiarkan; tombol Start akan dinonaktifkan.")
	}
	worker.SetBinary(deps.FFmpegPath)

	a := &App{
		cfg: cfg, log: log, store: db, worker: worker, processor: processor,
		uploader: uploader, monitor: monitor, auth: authSvc, hub: hub,
		sched: sched, mediaRepo: mediaRepo, sessions: sessionRepo,
	}

	// No Referrer-Policy header is set on purpose: YouTube's embedded player
	// requires an HTTP Referer and shows error 153 when one is suppressed.
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: Server-Sent Events connections stay open by design.
		IdleTimeout: 120 * time.Second,
	}
	a.server = httpSrv
	return a, nil
}

// Run starts background workers and serves HTTP until the process is signalled.
func (a *App) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Housekeeping: expired uploads, log rotation, session purge.
	go a.runHousekeeping(ctx)

	// Statistics sampling. Two seconds keeps figures fresh within the PRD's
	// five-second freshness budget without much CPU.
	go a.monitor.Start(ctx, 2*time.Second, func(m monitoring.Sample) { a.hub.PublishMetrics(m) })

	// Server-side scheduling runs regardless of any browser connection.
	go a.sched.Run(ctx)

	a.log.Info(logging.SourceApp, "", "Server berjalan di http://%s", a.cfg.Addr)
	a.log.Info(logging.SourceApp, "", "Antarmuka: %s", a.frontendURL())

	errCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	a.log.Info(logging.SourceApp, "", "Mematikan server")
	// Give in-flight requests a bounded window, then stop regardless.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		a.log.Warn(logging.SourceApp, "", "Shutdown tidak bersih: %v", err)
	}
	a.processor.Close()
	_ = a.store.Close()
	return nil
}

// runHousekeeping performs periodic maintenance.
func (a *App) runHousekeeping(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.uploader.Sweep(); err != nil {
				a.log.Warn(logging.SourceApp, "", "Pembersihan upload gagal: %v", err)
			}
			if err := a.log.Rotate(); err != nil {
				a.log.Warn(logging.SourceApp, "", "Rotasi log gagal: %v", err)
			}
			if err := a.auth.PurgeExpiredSessions(); err != nil {
				a.log.Warn(logging.SourceApp, "", "Pembersihan sesi gagal: %v", err)
			}
			// A file removed outside the app is marked missing and blocks new
			// starts until the operator re-uploads it (PRD §14).
			missing, err := a.mediaRepo.MarkMissingFlags(func(path string) bool {
				_, statErr := os.Stat(path)
				return statErr == nil
			})
			if err != nil {
				a.log.Warn(logging.SourceApp, "", "Pemeriksaan media gagal: %v", err)
			} else if missing > 0 {
				a.log.Warn(logging.SourceApp, "",
					"%d file video ditandai hilang karena tidak ditemukan di penyimpanan", missing)
				a.hub.PublishAlert(map[string]any{
					"kind":    "media_missing",
					"message": "Beberapa file video tidak ditemukan di penyimpanan.",
					"count":   missing,
				})
			}
		}
	}
}

func (a *App) frontendURL() string {
	if a.cfg.FrontendDir == "" {
		return "(frontend belum dibangun)"
	}
	abs, err := filepath.Abs(a.cfg.FrontendDir)
	if err != nil {
		return a.cfg.FrontendDir
	}
	return abs
}

// SignalContext returns a context cancelled on SIGINT or SIGTERM, which is how
// systemd stops the service cleanly.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// logSink mirrors log entries into the queryable index and the SSE hub.
type logSink struct {
	repo *repo.LogRepo
	hub  *api.Hub
}

// Publish stores the entry and forwards it to connected browsers.
func (s logSink) Publish(e logging.Entry) {
	if s.repo != nil {
		// A failed index write must never break logging, so the error is dropped.
		_ = s.repo.Insert(e)
	}
	if s.hub != nil {
		s.hub.PublishLog(e)
	}
}
