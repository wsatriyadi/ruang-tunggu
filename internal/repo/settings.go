package repo

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/wisnu/ylivemasadi/internal/store"
)

// Kunci pengaturan yang disimpan di tabel settings (FR-10). Nilai disimpan
// sebagai teks; kunci ini adalah satu-satunya tempat nama kolomnya ditulis.
const (
	// Umum.
	SettingInstanceName = "instance_name"
	SettingLanguage     = "language"
	SettingTimezone     = "timezone"
	// Tujuan siaran.
	SettingIngestURL      = "ingest_url"
	SettingStreamKeySet   = "stream_key_set"
	SettingPreviewVideoID = "preview_video_id"
	SettingProfileID      = "profile_id"
	// Media.
	SettingMediaDir         = "media_dir"
	SettingQuotaBytes       = "quota_bytes"
	SettingMaxFileSize      = "max_file_size"
	SettingDiskReserveBytes = "disk_reserve_bytes"
	// Engine.
	SettingFFmpegPath          = "ffmpeg_path"
	SettingFFprobePath         = "ffprobe_path"
	SettingResumeAfterRestart  = "resume_after_restart"
	SettingAutoReconnect       = "auto_reconnect"
	SettingStallTimeoutSeconds = "stall_timeout_seconds"
	SettingMaxAttempts         = "max_attempts"
	// Monitoring.
	SettingNetInterface          = "net_interface"
	SettingSampleIntervalSeconds = "sample_interval_seconds"
	SettingWarnCPUPercent        = "warn_cpu_percent"
	SettingWarnDiskPercent       = "warn_disk_percent"
	// Log.
	SettingLogMaxBytes = "log_max_bytes"
	SettingLogMaxDays  = "log_max_days"
)

// Settings is the persisted application configuration, grouped the way the
// Settings page presents it (FR-10). StreamKeySet is the only thing exposed
// about the stream key: the key itself lives encrypted in stream_targets and
// never has a field here.
type Settings struct {
	// Umum.
	InstanceName string `json:"instance_name"`
	Language     string `json:"language"`
	Timezone     string `json:"timezone"`

	// Tujuan siaran.
	IngestURL      string `json:"ingest_url"`
	StreamKeySet   bool   `json:"stream_key_set"`
	PreviewVideoID string `json:"preview_video_id"`
	ProfileID      string `json:"profile_id"`

	// Media.
	MediaDir         string `json:"media_dir"`
	QuotaBytes       int64  `json:"quota_bytes"`
	MaxFileSize      int64  `json:"max_file_size"`
	DiskReserveBytes int64  `json:"disk_reserve_bytes"`

	// Engine.
	FFmpegPath          string `json:"ffmpeg_path"`
	FFprobePath         string `json:"ffprobe_path"`
	ResumeAfterRestart  bool   `json:"resume_after_restart"`
	AutoReconnect       bool   `json:"auto_reconnect"`
	StallTimeoutSeconds int    `json:"stall_timeout_seconds"`
	MaxAttempts         int    `json:"max_attempts"`

	// Monitoring.
	NetInterface          string `json:"net_interface"`
	SampleIntervalSeconds int    `json:"sample_interval_seconds"`
	WarnCPUPercent        int    `json:"warn_cpu_percent"`
	WarnDiskPercent       int    `json:"warn_disk_percent"`

	// Log.
	LogMaxBytes int64 `json:"log_max_bytes"`
	LogMaxDays  int   `json:"log_max_days"`
}

// DefaultSettings returns the configuration a fresh install starts with. Load
// starts from these values, so a key that was never written keeps working
// instead of reading back as an empty string.
func DefaultSettings() Settings {
	return Settings{
		InstanceName: "YouTube Live Server",
		Language:     "id",
		Timezone:     "Asia/Jakarta",
		ProfileID:    "yt-sdr-h264",

		// 8 GiB per file, matching the config default; 0 quota means unlimited.
		MaxFileSize:      8 << 30,
		DiskReserveBytes: 0,

		ResumeAfterRestart: true,
		AutoReconnect:      true,
		// 30 s mirrors the publisher stall threshold in the streaming engine.
		StallTimeoutSeconds: 30,
		MaxAttempts:         5,

		SampleIntervalSeconds: 5,
		WarnCPUPercent:        80,
		WarnDiskPercent:       85,

		// Retention from PRD §9: 100 MiB across rotated files, kept 7 days.
		LogMaxBytes: 100 << 20,
		LogMaxDays:  7,
	}
}

// SettingsRepo reads and writes the key/value settings table.
type SettingsRepo struct{ st *store.Store }

// NewSettingsRepo builds a settings repository.
func NewSettingsRepo(st *store.Store) *SettingsRepo { return &SettingsRepo{st: st} }

// setting is one key/value pair written by Save.
type setting struct {
	key   string
	value string
}

// pairs flattens the struct into stored rows. Numbers and booleans always
// render to a non-empty string; only genuine strings can be skipped by Save.
func (s Settings) pairs() []setting {
	return []setting{
		{SettingInstanceName, strings.TrimSpace(s.InstanceName)},
		{SettingLanguage, strings.TrimSpace(s.Language)},
		{SettingTimezone, strings.TrimSpace(s.Timezone)},

		{SettingIngestURL, strings.TrimSpace(s.IngestURL)},
		{SettingStreamKeySet, boolText(s.StreamKeySet)},
		{SettingPreviewVideoID, strings.TrimSpace(s.PreviewVideoID)},
		{SettingProfileID, strings.TrimSpace(s.ProfileID)},

		{SettingMediaDir, strings.TrimSpace(s.MediaDir)},
		{SettingQuotaBytes, strconv.FormatInt(s.QuotaBytes, 10)},
		{SettingMaxFileSize, strconv.FormatInt(s.MaxFileSize, 10)},
		{SettingDiskReserveBytes, strconv.FormatInt(s.DiskReserveBytes, 10)},

		{SettingFFmpegPath, strings.TrimSpace(s.FFmpegPath)},
		{SettingFFprobePath, strings.TrimSpace(s.FFprobePath)},
		{SettingResumeAfterRestart, boolText(s.ResumeAfterRestart)},
		{SettingAutoReconnect, boolText(s.AutoReconnect)},
		{SettingStallTimeoutSeconds, strconv.Itoa(s.StallTimeoutSeconds)},
		{SettingMaxAttempts, strconv.Itoa(s.MaxAttempts)},

		{SettingNetInterface, strings.TrimSpace(s.NetInterface)},
		{SettingSampleIntervalSeconds, strconv.Itoa(s.SampleIntervalSeconds)},
		{SettingWarnCPUPercent, strconv.Itoa(s.WarnCPUPercent)},
		{SettingWarnDiskPercent, strconv.Itoa(s.WarnDiskPercent)},

		{SettingLogMaxBytes, strconv.FormatInt(s.LogMaxBytes, 10)},
		{SettingLogMaxDays, strconv.Itoa(s.LogMaxDays)},
	}
}

// Load returns the stored settings, filling every absent or unparsable key
// with its default.
func (r *SettingsRepo) Load() (*Settings, error) {
	rows, err := r.st.DB().Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s := DefaultSettings()
	settingString(raw, SettingInstanceName, &s.InstanceName)
	settingString(raw, SettingLanguage, &s.Language)
	settingString(raw, SettingTimezone, &s.Timezone)

	settingString(raw, SettingIngestURL, &s.IngestURL)
	settingBool(raw, SettingStreamKeySet, &s.StreamKeySet)
	settingString(raw, SettingPreviewVideoID, &s.PreviewVideoID)
	settingString(raw, SettingProfileID, &s.ProfileID)

	settingString(raw, SettingMediaDir, &s.MediaDir)
	settingInt64(raw, SettingQuotaBytes, &s.QuotaBytes)
	settingInt64(raw, SettingMaxFileSize, &s.MaxFileSize)
	settingInt64(raw, SettingDiskReserveBytes, &s.DiskReserveBytes)

	settingString(raw, SettingFFmpegPath, &s.FFmpegPath)
	settingString(raw, SettingFFprobePath, &s.FFprobePath)
	settingBool(raw, SettingResumeAfterRestart, &s.ResumeAfterRestart)
	settingBool(raw, SettingAutoReconnect, &s.AutoReconnect)
	settingInt(raw, SettingStallTimeoutSeconds, &s.StallTimeoutSeconds)
	settingInt(raw, SettingMaxAttempts, &s.MaxAttempts)

	settingString(raw, SettingNetInterface, &s.NetInterface)
	settingInt(raw, SettingSampleIntervalSeconds, &s.SampleIntervalSeconds)
	settingInt(raw, SettingWarnCPUPercent, &s.WarnCPUPercent)
	settingInt(raw, SettingWarnDiskPercent, &s.WarnDiskPercent)

	settingInt64(raw, SettingLogMaxBytes, &s.LogMaxBytes)
	settingInt(raw, SettingLogMaxDays, &s.LogMaxDays)
	return &s, nil
}

// Save writes every non-empty key in one transaction, so a settings update is
// never observed half-applied. Empty values are left untouched: they mean
// "use the default", not "reset to empty".
func (r *SettingsRepo) Save(s Settings) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	pairs := s.pairs()
	return r.st.Tx(func(tx *sql.Tx) error {
		for _, p := range pairs {
			if p.value == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?,?,?)
				ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
				p.key, p.value, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Get returns one raw setting value and whether the key exists.
func (r *SettingsRepo) Get(key string) (string, bool, error) {
	var value string
	err := r.st.DB().QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// Set writes one raw setting value.
func (r *SettingsRepo) Set(key, value string) error {
	_, err := r.st.DB().Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func settingString(raw map[string]string, key string, dst *string) {
	if v, ok := raw[key]; ok && v != "" {
		*dst = v
	}
}

func settingBool(raw map[string]string, key string, dst *bool) {
	v, ok := raw[key]
	if !ok || v == "" {
		return
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return
	}
	*dst = b
}

func settingInt(raw map[string]string, key string, dst *int) {
	n, err := strconv.Atoi(raw[key])
	if err != nil {
		return
	}
	*dst = n
}

func settingInt64(raw map[string]string, key string, dst *int64) {
	n, err := strconv.ParseInt(raw[key], 10, 64)
	if err != nil {
		return
	}
	*dst = n
}

// boolText renders a boolean the way it is stored and parsed back.
func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
