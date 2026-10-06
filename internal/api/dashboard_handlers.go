package api

import (
	"net/http"
	"time"

	"github.com/wisnu/ylivemasadi/internal/media"
	"github.com/wisnu/ylivemasadi/internal/monitoring"
	"github.com/wisnu/ylivemasadi/internal/repo"
	"github.com/wisnu/ylivemasadi/internal/streaming"
)

// dashboardResponse is the aggregated first-screen payload (PRD §13).
type dashboardResponse struct {
	Instance struct {
		Name     string `json:"name"`
		Timezone string `json:"timezone"`
		Version  string `json:"version"`
	} `json:"instance"`
	System monitoringPayload  `json:"system"`
	Stream streaming.Progress `json:"stream"`
	// YouTubeStatus stays unknown without API integration. The panel must never
	// claim a broadcast is on air based on local process state alone (FR-06).
	YouTubeStatus youtubeStatus   `json:"youtube"`
	Media         mediaSummary    `json:"media"`
	Playlists     []playlistBrief `json:"playlists"`
	Schedules     []scheduleBrief `json:"schedules"`
	// Dependencies reports installed tools so Start can be disabled (PRD §14).
	Dependencies map[string]any `json:"dependencies"`
	// DiskSpace mirrors the volume figure the upload limit is checked against.
	DiskSpace any `json:"disk_space,omitempty"`
}

type monitoringPayload struct {
	CPUPercent        *float64      `json:"cpu_percent"`
	MemTotalBytes     int64         `json:"mem_total_bytes"`
	MemAvailableBytes int64         `json:"mem_available_bytes"`
	MemUsedPercent    *float64      `json:"mem_used_percent"`
	TemperatureC      *float64      `json:"temperature_celsius"`
	UptimeSeconds     float64       `json:"uptime_seconds"`
	Network           any           `json:"network,omitempty"`
	History           []samplePoint `json:"history,omitempty"`
	ProcessRSSBytes   int64         `json:"process_rss_bytes"`
	Stale             bool          `json:"stale"`
	// StaleAfterSeconds is the age after which figures are marked stale (§5.3).
	StaleAfterSeconds int `json:"stale_after_seconds"`
}

type samplePoint struct {
	T   int64    `json:"t"`
	CPU *float64 `json:"cpu,omitempty"`
	Mem *float64 `json:"mem,omitempty"`
	Up  *float64 `json:"up,omitempty"`
}

type youtubeStatus struct {
	// Verified is always false in this release: no YouTube API is connected.
	Verified bool   `json:"verified"`
	State    string `json:"state"`
	Message  string `json:"message"`
	VideoID  string `json:"preview_video_id,omitempty"`
}

type mediaSummary struct {
	Total         int     `json:"total"`
	Ready         int     `json:"ready"`
	Processing    int     `json:"processing"`
	Invalid       int     `json:"invalid"`
	Missing       int     `json:"missing"`
	NeedsReview   int     `json:"needs_review"`
	TotalBytes    int64   `json:"total_bytes"`
	TotalDuration float64 `json:"total_duration_seconds"`
}

type playlistBrief struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	ItemCount      int     `json:"item_count"`
	CycleDuration  float64 `json:"cycle_duration_seconds"`
	PlaybackMode   string  `json:"playback_mode"`
	LoopEnabled    bool    `json:"loop_enabled"`
	Compatibility  string  `json:"compatibility"`
	RevisionNumber int     `json:"revision_number"`
}

type scheduleBrief struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	StartAt   time.Time `json:"start_at"`
	EndAt     time.Time `json:"end_at"`
	Timezone  string    `json:"timezone"`
	Status    string    `json:"status"`
	Enabled   bool      `json:"enabled"`
	IsRunning bool      `json:"is_running"`
}

// handleDashboard assembles the first screen in one request.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	var resp dashboardResponse
	settings, err := s.settings.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat pengaturan.")
		return
	}
	resp.Instance.Name = settings.InstanceName
	if resp.Instance.Name == "" {
		resp.Instance.Name = "YouTube Live Server"
	}
	resp.Instance.Timezone = settings.Timezone
	resp.Instance.Version = s.version

	if sample, ok := s.monitor.Latest(); ok {
		resp.System = buildMonitoringPayload(sample, s.monitor.History(time.Minute))
	}
	resp.Stream = s.worker.Status()

	target, _ := s.targets.Default()
	resp.YouTubeStatus = youtubeStatus{
		Verified: false,
		State:    "unverified",
		Message:  "Mengirim ke YouTube — status tayang belum diverifikasi. Periksa YouTube Studio.",
	}
	if target != nil {
		resp.YouTubeStatus.VideoID = target.PreviewVideoID
	}

	list, _, err := s.media.List("", 1000, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat media.")
		return
	}
	for _, m := range list {
		resp.Media.Total++
		resp.Media.TotalBytes += m.Size
		resp.Media.TotalDuration += m.Duration
		switch m.FileStatus {
		case "ready":
			resp.Media.Ready++
		case "processing", "uploading":
			resp.Media.Processing++
		case "invalid":
			resp.Media.Invalid++
		case "missing":
			resp.Media.Missing++
		}
		if m.Compatibility == media.StatusNeedsReview {
			resp.Media.NeedsReview++
		}
	}

	playlists, err := s.playlists.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat playlist.")
		return
	}
	for _, p := range playlists {
		resp.Playlists = append(resp.Playlists, playlistBrief{
			ID: p.ID, Name: p.Name, ItemCount: p.ItemCount,
			CycleDuration: p.CycleDuration, PlaybackMode: p.PlaybackMode,
			LoopEnabled: p.LoopEnabled, Compatibility: p.Compatibility,
			RevisionNumber: p.RevisionNumber,
		})
	}

	schedules, err := s.schedules.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "Gagal memuat jadwal.")
		return
	}
	for _, sc := range schedules {
		resp.Schedules = append(resp.Schedules, scheduleBrief{
			ID: sc.ID, Name: sc.Name, StartAt: sc.StartAt, EndAt: sc.EndAt,
			Timezone: sc.Timezone, Status: sc.Status, Enabled: sc.Enabled,
			IsRunning: sc.IsRunning,
		})
	}

	resp.Dependencies = s.dependencyStatus()
	if disk, ok := s.diskUsage(); ok {
		resp.DiskSpace = disk
	}
	writeJSON(w, http.StatusOK, resp)
}

// buildMonitoringPayload converts a sample plus history into the wire shape.
func buildMonitoringPayload(sample monitoring.Sample, history []monitoring.Sample) monitoringPayload {
	p := monitoringPayload{
		CPUPercent:        sample.CPULoadPercent,
		MemTotalBytes:     sample.MemTotalBytes,
		MemAvailableBytes: sample.MemAvailableBytes,
		MemUsedPercent:    sample.MemUsedPercent,
		TemperatureC:      sample.TemperatureCelsius,
		UptimeSeconds:     sample.UptimeSeconds,
		Network:           sample.Network,
		ProcessRSSBytes:   sample.ProcessRSSBytes,
		// Three missed ticks means the collector is not keeping up; the UI
		// then marks figures as stale rather than showing frozen values as live.
		StaleAfterSeconds: 10,
	}
	if time.Since(sample.Timestamp) > 10*time.Second {
		p.Stale = true
	}
	base := history[0].Timestamp
	for _, h := range history {
		pt := samplePoint{T: h.Timestamp.Sub(base).Milliseconds()}
		if h.CPULoadPercent != nil {
			pt.CPU = h.CPULoadPercent
		}
		if h.MemUsedPercent != nil {
			pt.Mem = h.MemUsedPercent
		}
		if h.Network != nil {
			v := h.Network.UploadMbps
			pt.Up = &v
		}
		p.History = append(p.History, pt)
	}
	return p
}

// handleSystem returns the static device inventory for the System card.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	info := s.monitor.Info()
	iface := ""
	if settings, err := s.settings.Load(); err == nil {
		iface = settings.NetInterface
	}
	interfaces := listInterfaces()
	writeJSON(w, http.StatusOK, map[string]any{
		"info":       info,
		"interfaces": interfaces,
		"selected":   iface,
	})
}

// handleEvents streams metrics, session state and logs to the browser.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.hub.Serve(w, r)
}

// dependencyStatus reports which external tools are installed (PRD §14).
func (s *Server) dependencyStatus() map[string]any {
	deps := s.binaries.Check(rContextTODO())
	return map[string]any{
		"ffmpeg":          deps.FFmpegFound,
		"ffprobe":         deps.FFprobeFound,
		"ffmpeg_path":     deps.FFmpegPath,
		"ffprobe_path":    deps.FFprobePath,
		"ffmpeg_version":  deps.FFmpegVersion,
		"ffprobe_version": deps.FFprobeVersion,
		"ready":           deps.FFmpegFound && deps.FFprobeFound,
	}
}

// mediaBrief is the compact media shape used by the dashboard and library grid.
type mediaBrief struct {
	repo.Media
}
