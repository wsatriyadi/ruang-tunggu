// Package config resolves runtime paths and process-level flags from the
// environment, with defaults chosen for a small always-on Linux box.
package config

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config holds the resolved process configuration.
type Config struct {
	// Addr is the bind address for the HTTP server.
	Addr string
	// DataDir stores the database, encryption key, logs, media and thumbnails.
	DataDir string
	// MediaDir is where uploaded videos live (may be a mounted volume).
	MediaDir string
	// ThumbnailDir is where generated thumbnails live.
	ThumbnailDir string
	// TempDir holds in-flight upload parts.
	TempDir string
	// LogDir holds rotated log files.
	LogDir string
	// FrontendDir is the built static site; empty disables static serving.
	FrontendDir string
	// SessionTTL is how long a login stays valid.
	SessionTTL time.Duration
	// SecureCookies forces the Secure attribute on session cookies (HTTPS).
	SecureCookies bool
	// MaxFileSize is the per-file upload ceiling in bytes.
	MaxFileSize int64
	// MediaQuota caps total media bytes; 0 means unlimited.
	MediaQuota int64
	// DiskReserveBytes keeps headroom free. 0 means auto (max(1GiB, 5% of volume)).
	DiskReserveBytes int64
	// FFMpegPath and FFprobePath allow overriding binary discovery (FR-10).
	FFmpegPath  string
	FFprobePath string
	// NetInterface is the interface monitored for upload throughput.
	NetInterface string
	// Timezone is the default display timezone from setup (FR-05).
	Timezone string
	// ResumeAfterRestart restores interrupted sessions on boot (FR-07).
	ResumeAfterRestart bool
}

// Load parses flags and environment, then creates the data directory tree.
func Load(args []string) (*Config, error) {
	c := &Config{}
	fs := flag.NewFlagSet("ytliveserver", flag.ContinueOnError)
	fs.StringVar(&c.Addr, "addr", env("YTL_ADDR", "0.0.0.0:8080"), "bind address")
	fs.StringVar(&c.DataDir, "data", env("YTL_DATA_DIR", "./data"), "data directory")
	fs.StringVar(&c.MediaDir, "media", env("YTL_MEDIA_DIR", ""), "media directory (defaults to <data>/media)")
	fs.StringVar(&c.FrontendDir, "frontend", env("YTL_FRONTEND_DIR", "./web/dist"), "built frontend directory")
	fs.DurationVar(&c.SessionTTL, "session-ttl", envDuration("YTL_SESSION_TTL", 14*24*time.Hour), "session lifetime")
	fs.BoolVar(&c.SecureCookies, "secure-cookies", envBool("YTL_SECURE_COOKIES", false), "mark session cookies Secure (enable behind HTTPS)")
	fs.Int64Var(&c.MaxFileSize, "max-file-size", envInt64("YTL_MAX_FILE_SIZE", 8<<30), "max upload size in bytes")
	fs.Int64Var(&c.MediaQuota, "media-quota", envInt64("YTL_MEDIA_QUOTA", 0), "total media quota in bytes (0 = unlimited)")
	fs.Int64Var(&c.DiskReserveBytes, "disk-reserve", envInt64("YTL_DISK_RESERVE", 0), "free-disk reserve in bytes (0 = auto)")
	fs.StringVar(&c.FFmpegPath, "ffmpeg", env("YTL_FFMPEG", ""), "ffmpeg binary path (defaults to PATH lookup)")
	fs.StringVar(&c.FFprobePath, "ffprobe", env("YTL_FFPROBE", ""), "ffprobe binary path (defaults to PATH lookup)")
	fs.StringVar(&c.NetInterface, "iface", env("YTL_NET_IFACE", ""), "network interface to monitor")
	fs.StringVar(&c.Timezone, "timezone", env("YTL_TIMEZONE", "Asia/Makassar"), "display timezone (IANA)")
	fs.BoolVar(&c.ResumeAfterRestart, "resume", envBool("YTL_RESUME_AFTER_RESTART", true), "resume interrupted sessions after restart")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return nil, err
	}
	c.DataDir = abs
	if c.MediaDir == "" {
		c.MediaDir = filepath.Join(abs, "media")
	} else if !filepath.IsAbs(c.MediaDir) {
		if c.MediaDir, err = filepath.Abs(c.MediaDir); err != nil {
			return nil, err
		}
	}
	c.ThumbnailDir = filepath.Join(abs, "thumbnails")
	c.TempDir = filepath.Join(abs, "tmp")
	c.LogDir = filepath.Join(abs, "logs")

	for _, dir := range []string{c.DataDir, c.MediaDir, c.ThumbnailDir, c.TempDir, c.LogDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// DBPath is the SQLite database location.
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "app.db") }

// KeyPath is the stream-key encryption key, kept outside the database (PRD §10).
func (c *Config) KeyPath() string { return filepath.Join(c.DataDir, "streamkey.key") }

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
