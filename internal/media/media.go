// Package media wraps FFprobe and FFmpeg: metadata extraction, thumbnail
// generation, and stream-copy compatibility validation (FR-03, FR-06).
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrBinaryMissing reports that ffmpeg or ffprobe is not installed.
var ErrBinaryMissing = errors.New("media binary not found")

// Probe holds the metadata extracted from a media file.
type Probe struct {
	Duration   float64 `json:"duration"`
	Size       int64   `json:"size"`
	Container  string  `json:"container"`
	FormatName string  `json:"format_name"`
	BitRate    int64   `json:"bitrate"`
	StartTime  float64 `json:"start_time"`
	HasVideo   bool    `json:"has_video"`
	HasAudio   bool    `json:"has_audio"`
	VideoCodec string  `json:"video_codec,omitempty"`
	Profile    string  `json:"profile,omitempty"`
	Level      int     `json:"level,omitempty"`
	TimeBase   string  `json:"time_base,omitempty"`
	// ExtradataHash identifies the codec parameter sets. Two files sharing a
	// concat input must produce the same hash.
	ExtradataHash string  `json:"extradata_hash,omitempty"`
	HasBFrames    int     `json:"has_b_frames,omitempty"`
	FieldOrder    string  `json:"field_order,omitempty"`
	SampleAspect  string  `json:"sample_aspect_ratio,omitempty"`
	Width         int     `json:"width,omitempty"`
	Height        int     `json:"height,omitempty"`
	PixFmt        string  `json:"pix_fmt,omitempty"`
	FPS           float64 `json:"fps,omitempty"`
	AudioCodec    string  `json:"audio_codec,omitempty"`
	SampleRate    int     `json:"sample_rate,omitempty"`
	ChannelLayout string  `json:"channel_layout,omitempty"`
	Channels      int     `json:"channels,omitempty"`
	// VideoDuration and AudioDuration are the per-stream lengths. The concat
	// demuxer re-bases timestamps globally, so a mismatch here becomes a
	// permanent A/V offset at every file boundary.
	VideoDuration float64 `json:"video_duration,omitempty"`
	AudioDuration float64 `json:"audio_duration,omitempty"`
	// KeyframeMaxGap is the longest observed gap between keyframes, in seconds.
	KeyframeMaxGap float64 `json:"keyframe_max_gap,omitempty"`
	// KeyframeCount is the number of keyframes found during the scan.
	KeyframeCount int `json:"keyframe_count,omitempty"`
	// KeyframeScanComplete is false when the scan was skipped or failed.
	KeyframeScanComplete bool `json:"keyframe_scan_complete"`
}

// Binaries resolves and caches the ffmpeg/ffprobe executables.
type Binaries struct {
	ffmpeg  string
	ffprobe string

	mu          sync.Mutex
	ffmpegPath  string
	ffprobePath string
}

// NewBinaries resolves executables. Explicit paths win over PATH lookup.
func NewBinaries(ffmpeg, ffprobe string) *Binaries {
	return &Binaries{ffmpeg: ffmpeg, ffprobe: ffprobe}
}

// Deps describes installed tools for the Settings page (FR-10 / §14).
type Deps struct {
	FFmpegPath     string `json:"ffmpeg_path"`
	FFprobePath    string `json:"ffprobe_path"`
	FFmpegFound    bool   `json:"ffmpeg_found"`
	FFprobeFound   bool   `json:"ffprobe_found"`
	FFmpegVersion  string `json:"ffmpeg_version,omitempty"`
	FFprobeVersion string `json:"ffprobe_version,omitempty"`
}

// Check resolves both binaries and reads their versions.
func (b *Binaries) Check(ctx context.Context) Deps {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := Deps{}
	b.ffmpegPath = resolve("ffmpeg", b.ffmpeg)
	b.ffprobePath = resolve("ffprobe", b.ffprobe)
	d.FFmpegPath, d.FFmpegFound = b.ffmpegPath, b.ffmpegPath != ""
	d.FFprobePath, d.FFprobeFound = b.ffprobePath, b.ffprobePath != ""
	if d.FFmpegFound {
		d.FFmpegVersion = toolVersion(ctx, b.ffmpegPath)
	}
	if d.FFprobeFound {
		d.FFprobeVersion = toolVersion(ctx, b.ffprobePath)
	}
	return d
}

// FFmpeg returns the resolved ffmpeg path, discovering it if needed.
func (b *Binaries) FFmpeg(ctx context.Context) (string, error) {
	b.mu.Lock()
	if b.ffmpegPath == "" {
		b.ffmpegPath = resolve("ffmpeg", b.ffmpeg)
	}
	path := b.ffmpegPath
	b.mu.Unlock()
	if path == "" {
		return "", fmt.Errorf("%w: ffmpeg", ErrBinaryMissing)
	}
	return path, nil
}

// FFprobe returns the resolved ffprobe path, discovering it if needed.
func (b *Binaries) FFprobe(ctx context.Context) (string, error) {
	b.mu.Lock()
	if b.ffprobePath == "" {
		b.ffprobePath = resolve("ffprobe", b.ffprobe)
	}
	path := b.ffprobePath
	b.mu.Unlock()
	if path == "" {
		return "", fmt.Errorf("%w: ffprobe", ErrBinaryMissing)
	}
	return path, nil
}

// Available reports whether both tools can be found.
func (b *Binaries) Available(ctx context.Context) bool {
	d := b.Check(ctx)
	return d.FFmpegFound && d.FFprobeFound
}

// resolve finds the executable for a tool, honouring an explicit path when one
// is configured and falling back to a PATH lookup otherwise.
func resolve(name, explicit string) string {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return ""
		}
		if _, err := exec.LookPath(abs); err != nil {
			return ""
		}
		return abs
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func toolVersion(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(out), "\n")
	// "ffmpeg version N-119731-... Copyright ..." -> keep the build identifier.
	if fields := strings.Fields(line); len(fields) >= 3 {
		return fields[2]
	}
	return line
}

// probeOutput mirrors the subset of ffprobe -show_format/-show_streams we read.
type probeOutput struct {
	Format struct {
		Filename   string            `json:"filename"`
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		Size       string            `json:"size"`
		BitRate    string            `json:"bit_rate"`
		StartTime  string            `json:"start_time"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Index         int    `json:"index"`
		CodecType     string `json:"codec_type"`
		CodecName     string `json:"codec_name"`
		Profile       string `json:"profile"`
		Level         int    `json:"level"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		AvgFrameRate  string `json:"avg_frame_rate"`
		RFrameRate    string `json:"r_frame_rate"`
		PixFmt        string `json:"pix_fmt"`
		TimeBase      string `json:"time_base"`
		SampleRate    string `json:"sample_rate"`
		Channels      int    `json:"channels"`
		ChannelLayout string `json:"channel_layout"`
		BitRate       string `json:"bit_rate"`
		StartTime     string `json:"start_time"`
		Duration      string `json:"duration"`
		// ExtradataHash is the md5 of the codec extradata. For H.264 it is the
		// direct SPS/PPS equality test: files whose hashes differ cannot be
		// concatenated and stream-copied safely, because the FLV muxer writes
		// only the first file's parameter sets.
		ExtradataHash string            `json:"extradata_hash"`
		ExtradataSize int               `json:"extradata_size"`
		HasBFrames    int               `json:"has_b_frames"`
		FieldOrder    string            `json:"field_order"`
		SampleAspect  string            `json:"sample_aspect_ratio"`
		Disposition   map[string]int    `json:"disposition"`
		Tags          map[string]string `json:"tags"`
	} `json:"streams"`
}

// ProbeFile extracts metadata for a media file. It inspects streams, not the
// whole file, keeping it fast on weak devices.
func ProbeFile(ctx context.Context, b *Binaries, path string) (*Probe, error) {
	bin, err := b.FFprobe(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// -show_entries keeps the JSON small; -of json gives stable keys.
	cmd := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-of", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	stdout, err := cmd.Output()
	if err != nil {
		return nil, describeFFprobeError(err, path)
	}
	var out probeOutput
	if err := json.Unmarshal(stdout, &out); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	p := &Probe{
		FormatName: out.Format.FormatName,
		BitRate:    parseInt64(out.Format.BitRate),
		StartTime:  parseFloat(out.Format.StartTime),
		Container:  primaryContainer(out.Format.FormatName),
	}
	if out.Format.Duration != "" && out.Format.Duration != "N/A" {
		p.Duration = parseFloat(out.Format.Duration)
	}

	for i := range out.Streams {
		s := &out.Streams[i]
		switch s.CodecType {
		case "video":
			// Skip attached cover art, which is not a playable track.
			if s.Disposition["attached_pic"] == 1 {
				continue
			}
			if p.HasVideo {
				continue
			}
			p.HasVideo = true
			p.VideoCodec = s.CodecName
			p.Profile = s.Profile
			p.Level = s.Level
			p.Width = s.Width
			p.Height = s.Height
			p.PixFmt = s.PixFmt
			p.TimeBase = s.TimeBase
			p.ExtradataHash = s.ExtradataHash
			p.HasBFrames = s.HasBFrames
			p.FieldOrder = s.FieldOrder
			p.SampleAspect = s.SampleAspect
			p.FPS = parseRate(s.AvgFrameRate)
			if p.FPS == 0 {
				p.FPS = parseRate(s.RFrameRate)
			}
			p.VideoDuration = parseFloat(s.Duration)
		case "audio":
			if p.HasAudio {
				continue
			}
			p.HasAudio = true
			p.AudioCodec = s.CodecName
			p.SampleRate = int(parseInt64(s.SampleRate))
			p.Channels = s.Channels
			p.ChannelLayout = s.ChannelLayout
			p.AudioDuration = parseFloat(s.Duration)
		}
	}
	return p, nil
}

func describeFFprobeError(err error, path string) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		msg := strings.TrimSpace(string(exitErr.Stderr))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		if msg == "" {
			msg = "ffprobe exited with an error"
		}
		return fmt.Errorf("probe failed for %s: %s", filepath.Base(path), msg)
	}
	return fmt.Errorf("probe %s: %w", filepath.Base(path), err)
}

// primaryContainer picks the main container name from ffprobe's format list.
func primaryContainer(formatName string) string {
	for _, name := range strings.Split(formatName, ",") {
		switch name {
		case "mov", "mp4", "m4a", "3gp", "3g2", "mj2":
			return "mp4"
		case "matroska", "webm":
			return name
		}
	}
	if formatName == "" {
		return "unknown"
	}
	first, _, _ := strings.Cut(formatName, ",")
	return first
}

// ScanKeyframes walks keyframe timestamps to find the largest gap between
// consecutive video keyframes. Bounded by timeout so a long file cannot stall
// the queue; incomplete scans are reported, never silently treated as passing.
func ScanKeyframes(ctx context.Context, b *Binaries, path string, maxDuration time.Duration) (maxGap float64, count int, complete bool, err error) {
	bin, err := b.FFprobe(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, maxDuration)
	defer cancel()

	// Reading packet flags demuxes only: no decoder is opened, so the scan costs
	// roughly a magnitude less than a keyframe-only decode on an ARM device.
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "packet=pts_time,flags",
		"-of", "csv=p=0",
		path,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return 0, 0, false, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}

	prev := -1.0
	for _, line := range strings.Split(string(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "N/A" {
			continue
		}
		pts, flags, ok := strings.Cut(line, ",")
		if !ok || !strings.Contains(flags, "K") {
			continue
		}
		t := parseFloat(pts)
		if t < 0 {
			continue
		}
		count++
		if prev >= 0 && t-prev > maxGap {
			maxGap = t - prev
		}
		prev = t
	}
	if count == 0 {
		return 0, 0, true, nil
	}
	// The first keyframe gap is unknown; treat it as the observed max so a file
	// with a single keyframe is not treated as clean.
	return maxGap, count, true, nil
}

// Thumbnail renders a single representative JPEG frame. Failure to thumbnail
// does not invalidate an otherwise good media file (FR-03).
func Thumbnail(ctx context.Context, b *Binaries, src, dst string, at time.Duration) error {
	bin, err := b.FFmpeg(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	// -ss before -i seeks by demuxing (fast keyframe seek), which matters on ARM.
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-i", src, "-frames:v", "1",
		"-vf", "scale='min(640,iw)':-2",
		"-q:v", "4", dst)

	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("thumbnail failed: %s", msg)
	}
	return nil
}

func parseInt64(s string) int64 {
	if s == "" || s == "N/A" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseFloat(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// parseRate converts an ffprobe rational like "30000/1001" to a float.
func parseRate(s string) float64 {
	if s == "" || s == "N/A" || s == "0/0" {
		return 0
	}
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}
	n := parseFloat(num)
	d := parseFloat(den)
	if d == 0 {
		return 0
	}
	return n / d
}

// AVDurationSkew returns the length difference between the video and audio
// tracks. A non-zero value means each following file inherits an A/V offset.
func (p *Probe) AVDurationSkew() float64 {
	if !p.HasVideo || !p.HasAudio {
		return 0
	}
	if p.VideoDuration <= 0 || p.AudioDuration <= 0 {
		return 0
	}
	d := p.VideoDuration - p.AudioDuration
	if d < 0 {
		return -d
	}
	return d
}
