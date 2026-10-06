// Package jobs runs the media pipeline: a bounded queue probes uploaded files,
// validates them against the delivery profile and generates thumbnails.
//
// The queue is deliberately shallow. A 2 GB device cannot probe and thumbnail
// several large files at once, so work is serialised and the results stream to
// the browser through the event hub.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wisnu/ylivemasadi/internal/logging"
	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/repo"
)

// Processor handles one media file end to end.
type Processor struct {
	repo     *repo.MediaRepo
	uploads  *repo.UploadRepo
	binaries *media.Binaries
	log      *logging.Logger
	mediaDir string
	thumbDir string
	profile  media.Profile

	// queue serialises processing work.
	queue chan task
	// notify publishes media state changes to the UI.
	notify func(repo.Media)

	mu      sync.Mutex
	pending int
	wg      sync.WaitGroup
	closed  bool
}

// task is one unit of queued work.
type task struct {
	mediaID     string
	storagePath string
	uploadID    string
	// skipThumbnails lets a bulk re-check skip the expensive step.
	skipThumbnails bool
}

// ProcessorOptions configures the processor.
type ProcessorOptions struct {
	MediaDir   string
	ThumbDir   string
	Profile    media.Profile
	QueueDepth int
	// Notify is called after each file reaches a terminal state.
	Notify func(repo.Media)
}

// NewProcessor builds a processor and starts its worker.
func NewProcessor(m *repo.MediaRepo, u *repo.UploadRepo, b *media.Binaries, log *logging.Logger, opts ProcessorOptions) *Processor {
	depth := opts.QueueDepth
	if depth <= 0 {
		depth = 4
	}
	p := &Processor{
		repo:     m,
		uploads:  u,
		binaries: b,
		log:      log,
		mediaDir: opts.MediaDir,
		thumbDir: opts.ThumbDir,
		profile:  opts.Profile,
		queue:    make(chan task, depth),
		notify:   opts.Notify,
	}
	p.wg.Add(1)
	go p.run()
	return p
}

// Enqueue schedules a file for probing, validation and thumbnailing.
func (p *Processor) Enqueue(mediaID, storagePath, uploadID string, skipThumbnails bool) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("processor stopped")
	}
	p.pending++
	p.mu.Unlock()

	select {
	case p.queue <- task{mediaID: mediaID, storagePath: storagePath, uploadID: uploadID, skipThumbnails: skipThumbnails}:
		return nil
	default:
		// Refusing is better than growing an unbounded backlog on a small device.
		p.mu.Lock()
		p.pending--
		p.mu.Unlock()
		return errors.New("antrean pemeriksaan media penuh, coba lagi nanti")
	}
}

// Pending reports how many files are queued.
func (p *Processor) Pending() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending
}

// Close stops the worker after the current task finishes.
func (p *Processor) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	close(p.queue)
	p.wg.Wait()
}

// run processes queued files one at a time.
func (p *Processor) run() {
	defer p.wg.Done()
	for t := range p.queue {
		p.process(t)
		p.mu.Lock()
		if p.pending > 0 {
			p.pending--
		}
		p.mu.Unlock()
	}
}

// process probes one file and records the outcome.
func (p *Processor) process(t task) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// A file deleted before processing is marked missing rather than failed.
	if _, err := os.Stat(t.storagePath); err != nil {
		p.fail(t, "missing", "File tidak ditemukan di penyimpanan.")
		return
	}

	p.log.Info(logging.SourceApp, "", "Memeriksa media %s", filepath.Base(t.storagePath))
	probe, err := media.ProbeFile(ctx, p.binaries, t.storagePath)
	if err != nil {
		p.fail(t, "invalid", err.Error())
		return
	}
	stat, err := os.Stat(t.storagePath)
	if err != nil {
		p.fail(t, "missing", "File hilang saat pemeriksaan.")
		return
	}
	probe.Size = stat.Size()

	// The keyframe scan tells the operator whether YouTube's 4-second ceiling is
	// met. It is bounded so one long file cannot monopolise the queue.
	if probe.HasVideo {
		gap, count, complete, kerr := media.ScanKeyframes(ctx, p.binaries, t.storagePath, 3*time.Minute)
		if kerr == nil && complete {
			probe.KeyframeMaxGap = gap
			probe.KeyframeCount = count
			probe.KeyframeScanComplete = true
		} else {
			probe.KeyframeScanComplete = false
		}
	}

	compat := media.Validate(probe, p.profile)
	status := "ready"
	if compat.Status == media.StatusIncompatible {
		// A file that cannot be sent is still stored so the operator can see why,
		// but it is never treated as usable media.
		status = "invalid"
	}
	if err := p.repo.UpdateProbe(t.mediaID, probe, compat, status); err != nil {
		p.log.Error(logging.SourceApp, "", "Gagal menyimpan hasil pemeriksaan: %v", err)
		return
	}

	// Thumbnail failure must not invalidate otherwise good media (FR-03).
	if !t.skipThumbnails && probe.HasVideo {
		p.makeThumbnail(ctx, t, probe)
	}

	if t.uploadID != "" {
		_ = p.uploads.SetStatus(t.uploadID, repo.UploadCompleted, "")
	}
	p.emit(t.mediaID)
	p.log.Info(logging.SourceApp, "", "Media %s diproses: %s (%s)", filepath.Base(t.storagePath), status, compat.Status)
}

// makeThumbnail renders one representative frame, seeking near the start where
// keyframe density makes the decode cheap.
func (p *Processor) makeThumbnail(ctx context.Context, t task, probe *media.Probe) {
	if err := os.MkdirAll(p.thumbDir, 0o750); err != nil {
		return
	}
	dst := filepath.Join(p.thumbDir, t.mediaID+".jpg")
	// Ten percent into the file avoids fade-ins while staying near a keyframe.
	at := time.Duration(probe.Duration * 0.1 * float64(time.Second))
	if err := media.Thumbnail(ctx, p.binaries, t.storagePath, dst, at); err != nil {
		p.log.Warn(logging.SourceApp, "", "Thumbnail gagal untuk %s: %v", t.mediaID, err)
		return
	}
	if err := p.repo.SetThumbnail(t.mediaID, dst); err != nil {
		p.log.Warn(logging.SourceApp, "", "Gagal menyimpan thumbnail: %v", err)
	}
}

// fail records a terminal media state and tells the upload session about it.
func (p *Processor) fail(t task, status, message string) {
	_ = p.repo.SetStatus(t.mediaID, status)
	if t.uploadID != "" {
		_ = p.uploads.SetStatus(t.uploadID, repo.UploadFailed, message)
	}
	p.log.Error(logging.SourceApp, "", "Media %s gagal: %s", t.mediaID, message)
	p.emit(t.mediaID)
}

// emit notifies the UI about the file's new state.
func (p *Processor) emit(mediaID string) {
	if p.notify == nil {
		return
	}
	m, err := p.repo.Get(mediaID)
	if err != nil {
		return
	}
	p.notify(*m)
}

// RecheckAll re-queues every stored file, used when the delivery profile changes.
func (p *Processor) RecheckAll(ctx context.Context) (int, error) {
	list, _, err := p.repo.List("", 1000, 0)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range list {
		if err := p.Enqueue(m.ID, m.StoragePath, "", true); err != nil {
			continue
		}
		n++
	}
	return n, nil
}

// DescribeIssue renders one validation issue for the UI.
func DescribeIssue(i media.Issue) string {
	if i.Message != "" {
		return i.Message
	}
	return fmt.Sprintf("%s: %s", i.Field, i.Severity)
}
