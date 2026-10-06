package media

import (
	"fmt"
	"sort"
	"strings"
)

// Profile describes the media shape the publisher accepts for stream copy.
// The MVP ships one conservative profile: SDR H.264 + AAC over RTMPS (PRD §11).
type Profile struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Version   int          `json:"version"`
	Transport string       `json:"transport"`
	Rules     ProfileRules `json:"rules"`
}

// ProfileRules holds the limits a file must satisfy to be stream-copied.
type ProfileRules struct {
	VideoCodec   string   `json:"video_codec"`
	AudioCodecs  []string `json:"audio_codecs"`
	PixelFormats []string `json:"pixel_formats"`
	// MaxResolution bounds the longest side; 0 means unbounded.
	MaxResolution int     `json:"max_resolution"`
	MaxFPS        float64 `json:"max_fps"`
	// SampleRates lists every accepted audio sample rate.
	SampleRates []int `json:"sample_rates"`
	// PreferredSampleRate is the rate YouTube recommends for stereo.
	PreferredSampleRate int `json:"preferred_sample_rate"`
	Channels            int `json:"channels"`
	// MaxKeyframeGap is the hard ceiling between keyframes; exceeding it warns.
	MaxKeyframeGap float64 `json:"max_keyframe_gap"`
	// TargetKeyframeGap is the recommended spacing.
	TargetKeyframeGap float64 `json:"target_keyframe_gap"`
}

// SDRProfile is the initial P0 delivery profile: H.264 + AAC over RTMPS.
//
// H.264 is the only codec every YouTube source agrees on for RTMPS, so AV1 and
// H.265 stay out of P0 (S3 and the protocol-comparison guide disagree there).
func SDRProfile() Profile {
	return Profile{
		ID:        "yt-sdr-h264",
		Name:      "YouTube SDR H.264/AAC (stream copy)",
		Version:   1,
		Transport: "rtmps",
		Rules: ProfileRules{
			VideoCodec:    "h264",
			AudioCodecs:   []string{"aac"},
			PixelFormats:  []string{"yuv420p"},
			MaxResolution: 1920,
			MaxFPS:        60,
			// YouTube's advanced-settings table pairs 44.1 kHz with stereo, and
			// the ingest error messages ask for 44.1 kHz outright.
			SampleRates:         []int{44100, 48000},
			PreferredSampleRate: 44100,
			Channels:            2,
			TargetKeyframeGap:   2,
			MaxKeyframeGap:      4,
		},
	}
}

// bitrateRow is one line of YouTube's recommended encoder bitrate table (S3).
// Only the H.264 column is kept, because that is the only codec P0 accepts.
type bitrateRow struct {
	height  int
	fps     int
	minKbps int64
	recKbps int64
}

// bitrateTableKbps is the H.264 column of the recommended encoder settings,
// transcribed from YouTube's documentation. Search summaries frequently quote
// the AV1/H.265 column instead, so these numbers come from the doc itself.
var bitrateTableKbps = []bitrateRow{
	{2160, 60, 14000, 50000},
	{2160, 30, 11000, 42000},
	{1440, 60, 8000, 34000},
	{1440, 30, 7000, 21000},
	{1080, 60, 6000, 17000},
	{1080, 30, 5000, 14000},
	{720, 60, 3000, 8000},
	{720, 30, 3000, 8000},
	{480, 30, 400, 4000},
	{360, 30, 400, 4000},
}

// BitrateRecommendation returns YouTube's documented minimum and recommended
// bitrate for a resolution and frame rate, in kbps. It reports false when the
// pair is outside the documented table.
func BitrateRecommendation(height int, fps float64) (minKbps, recKbps int64, ok bool) {
	frameRate := 30
	if fps >= 50 {
		frameRate = 60
	}
	for _, row := range bitrateTableKbps {
		if row.height == height && row.fps == frameRate {
			return row.minKbps, row.recKbps, true
		}
	}
	return 0, 0, false
}

// NearestDocumentedHeight maps an arbitrary height onto a resolution YouTube
// documents, so guidance still works for e.g. 900px sources.
func NearestDocumentedHeight(height int) int {
	best, bestDist := 0, 1<<30
	for _, row := range bitrateTableKbps {
		d := row.height - height
		if d < 0 {
			d = -d
		}
		if d < bestDist || (d == bestDist && row.height < best) {
			best, bestDist = row.height, d
		}
	}
	return best
}

// Compatibility statuses (FR-03).
const (
	StatusUnchecked    = "unchecked"
	StatusCompatible   = "compatible"
	StatusIncompatible = "incompatible"
	StatusNeedsReview  = "needs_review"
)

// Issue severity levels.
const (
	SeverityBlock  = "blocking"
	SeverityWarn   = "warning"
	SeverityNotice = "notice"
)

// Issue is one human-readable problem with a media file.
type Issue struct {
	Field    string `json:"field"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// Compatibility is the verdict for one file against one profile.
type Compatibility struct {
	Status string  `json:"status"`
	Issues []Issue `json:"issues"`
}

// Validate checks one probe result against the profile. The verdict is about
// this file alone; playlist-level checks live in the streaming package.
func Validate(p *Probe, prof Profile) Compatibility {
	var issues []Issue
	r := prof.Rules

	if !p.HasVideo {
		issues = append(issues, Issue{"video", SeverityBlock,
			"File tidak memiliki track video. Siaran RTMPS memerlukan video H.264 dan audio AAC."})
	}
	if !p.HasAudio {
		issues = append(issues, Issue{"audio", SeverityBlock,
			"File tidak memiliki track audio. Siapkan file dengan audio AAC stereo."})
	}
	if p.HasVideo {
		if p.VideoCodec != r.VideoCodec {
			issues = append(issues, Issue{"video_codec", SeverityBlock, fmt.Sprintf(
				"Video codec %q, profile menerima %q. Konversi terlebih dahulu di luar aplikasi.",
				p.VideoCodec, r.VideoCodec)})
		}
		if !contains(r.PixelFormats, p.PixFmt) {
			issues = append(issues, Issue{"pix_fmt", SeverityBlock, fmt.Sprintf(
				"Pixel format %q, profile menerima %s. H.264 untuk YouTube memakai %s.",
				pixFmtLabel(p.PixFmt), strings.Join(r.PixelFormats, ", "), strings.Join(r.PixelFormats, ", "))})
		}
		longest := max(p.Width, p.Height)
		if r.MaxResolution > 0 && longest > r.MaxResolution {
			issues = append(issues, Issue{"resolution", SeverityBlock, fmt.Sprintf(
				"Resolusi %dx%d melebihi batas profile %dpx pada sisi terpanjang.",
				p.Width, p.Height, r.MaxResolution)})
		}
		if r.MaxFPS > 0 && p.FPS > r.MaxFPS+0.5 {
			issues = append(issues, Issue{"fps", SeverityWarn, fmt.Sprintf(
				"Frame rate %.3f fps melebihi %.0f fps yang didukung profile.", p.FPS, r.MaxFPS)})
		}
		if p.KeyframeScanComplete {
			switch {
			case p.KeyframeCount == 0:
				issues = append(issues, Issue{"keyframes", SeverityBlock,
					"Tidak ditemukan keyframe. Video tidak dapat dikirim dengan stream copy."})
			case p.KeyframeMaxGap > r.MaxKeyframeGap:
				issues = append(issues, Issue{"keyframes", SeverityWarn, fmt.Sprintf(
					"Jarak keyframe terbesar %.1f dtk melebihi batas %.0f dtk. Pertimbangkan GOP yang lebih pendek.",
					p.KeyframeMaxGap, r.MaxKeyframeGap)})
			}
		} else {
			issues = append(issues, Issue{"keyframes", SeverityNotice,
				"Pemeriksaan jarak keyframe tidak selesai. Verifikasi manual disarankan."})
		}
		if p.BitRate > 0 {
			height := p.Height
			if p.Height > p.Width {
				height = p.Width
			}
			minKbps, recKbps, documented := BitrateRecommendation(height, p.FPS)
			label := fmt.Sprintf("%dp", height)
			if !documented {
				// Guidance for a size outside the documented table.
				nearest := NearestDocumentedHeight(height)
				minKbps, recKbps, _ = BitrateRecommendation(nearest, p.FPS)
				label = fmt.Sprintf("%dp (paling dekat %dp di tabel)", height, nearest)
			}
			kbps := p.BitRate / 1000
			switch {
			case kbps < minKbps/2:
				issues = append(issues, Issue{"bitrate", SeverityWarn, fmt.Sprintf(
					"Bitrate %.0f kbps jauh di bawah minimum %.0f kbps untuk %s; transmisi memakai stream copy sehingga bitrate tidak diubah.",
					float64(kbps), float64(minKbps), label)})
			case kbps > recKbps:
				issues = append(issues, Issue{"bitrate", SeverityWarn, fmt.Sprintf(
					"Bitrate %.0f kbps melebihi rekomendasi %.0f kbps untuk %s; stream copy tidak mengubah bitrate file.",
					float64(kbps), float64(recKbps), label)})
			}
		}
	}
	if p.HasAudio {
		if !contains(r.AudioCodecs, p.AudioCodec) {
			issues = append(issues, Issue{"audio_codec", SeverityBlock, fmt.Sprintf(
				"Audio codec %q, profile menerima %s.", p.AudioCodec, strings.Join(r.AudioCodecs, ", "))})
		}
		if p.SampleRate != 0 && len(r.SampleRates) > 0 && !containsInt(r.SampleRates, p.SampleRate) {
			issues = append(issues, Issue{"sample_rate", SeverityBlock, fmt.Sprintf(
				"Sample rate %d Hz, profile menerima %s Hz.", p.SampleRate, joinInts(r.SampleRates))})
		} else if p.SampleRate != 0 && r.PreferredSampleRate > 0 && p.SampleRate != r.PreferredSampleRate {
			issues = append(issues, Issue{"sample_rate", SeverityWarn, fmt.Sprintf(
				"Sample rate %d Hz diterima, tetapi YouTube merekomendasikan %d Hz untuk stereo.",
				p.SampleRate, r.PreferredSampleRate)})
		}
		if p.Channels > 0 && r.Channels > 0 && p.Channels > r.Channels {
			issues = append(issues, Issue{"channels", SeverityBlock, fmt.Sprintf(
				"Jumlah channel audio %d, profile menerima %d.", p.Channels, r.Channels)})
		} else if p.Channels == 1 && r.Channels == 2 {
			issues = append(issues, Issue{"channels", SeverityWarn,
				"Audio mono. Profil stereo; mono tetap dapat dikirim, tetapi kualitas menurun."})
		}
	}
	if p.Duration <= 0 {
		issues = append(issues, Issue{"duration", SeverityBlock,
			"Durasi tidak dapat dibaca dari file."})
	}

	status := StatusCompatible
	if hasSeverity(issues, SeverityBlock) {
		status = StatusIncompatible
	} else if len(issues) > 0 {
		status = StatusNeedsReview
	}
	if issues == nil {
		issues = []Issue{}
	}
	return Compatibility{Status: status, Issues: issues}
}

// PlaylistIssue explains why a set of files cannot be concatenated and
// stream-copied as one continuous publisher input (FR-06).
type PlaylistIssue struct {
	MediaID   string `json:"media_id"`
	Name      string `json:"name"`
	Field     string `json:"field"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Reference string `json:"reference_media_id,omitempty"`
}

// ValidateCohesion compares files that will share one concat input. Fields
// here must match exactly: FFmpeg stream copy moves packets without converting
// them, so mismatched parameters cannot be reconciled downstream (S1, S2).
func ValidateCohesion(probes []NamedProbe, prof Profile) []PlaylistIssue {
	var issues []PlaylistIssue
	if len(probes) < 2 {
		return nil
	}
	ref := probes[0]
	for _, cur := range probes[1:] {
		compare := func(field, refVal, curVal, message string) {
			if refVal != curVal && refVal != "" && curVal != "" {
				issues = append(issues, PlaylistIssue{
					MediaID: cur.ID, Name: cur.Name(),
					Field: field, Severity: SeverityBlock, Reference: ref.ID,
					Message: fmt.Sprintf("%s. %s: %q, sedangkan %q memakai %q.",
						message, ref.Name(), refVal, cur.Name(), curVal),
				})
			}
		}
		if ref.Probe.HasVideo && cur.Probe.HasVideo {
			compare("video_codec", ref.Probe.VideoCodec, cur.Probe.VideoCodec,
				"Codec video berbeda antar file")
			compare("pix_fmt", ref.Probe.PixFmt, cur.Probe.PixFmt,
				"Pixel format berbeda antar file")
			compare("width", fmt.Sprint(ref.Probe.Width), fmt.Sprint(cur.Probe.Width),
				"Lebar video berbeda antar file")
			compare("height", fmt.Sprint(ref.Probe.Height), fmt.Sprint(cur.Probe.Height),
				"Tinggi video berbeda antar file")
			compare("profile", ref.Probe.Profile, cur.Probe.Profile,
				"Profile H.264 berbeda antar file")
			compare("level", fmt.Sprint(ref.Probe.Level), fmt.Sprint(cur.Probe.Level),
				"Level H.264 berbeda antar file")
			compare("time_base", ref.Probe.TimeBase, cur.Probe.TimeBase,
				"Time base berbeda antar file")
			// The FLV muxer writes parameter sets from the first file only, so
			// differing SPS/PPS silently corrupts every later file.
			if ref.Probe.ExtradataHash != "" && cur.Probe.ExtradataHash != "" &&
				ref.Probe.ExtradataHash != cur.Probe.ExtradataHash {
				issues = append(issues, PlaylistIssue{
					MediaID: cur.ID, Name: cur.Name(),
					Field: "extradata", Severity: SeverityBlock, Reference: ref.ID,
					Message: fmt.Sprintf(
						"Parameter set video (SPS/PPS) berbeda antar file. %s dan %q tidak dapat dikirim pada satu koneksi dengan stream copy; konsversi salah satunya terlebih dahulu.",
						ref.Name(), cur.Name()),
				})
			}
			// B-frame count changes DTS/PTS relationships across the boundary,
			// which is where non-monotonic DTS appears.
			if ref.Probe.HasBFrames != cur.Probe.HasBFrames {
				issues = append(issues, PlaylistIssue{
					MediaID: cur.ID, Name: cur.Name(),
					Field: "has_b_frames", Severity: SeverityBlock, Reference: ref.ID,
					Message: fmt.Sprintf(
						"Jumlah B-frame berbeda antar file (%d vs %d). Nilai ini memengaruhi timestamp di batas file.",
						ref.Probe.HasBFrames, cur.Probe.HasBFrames),
				})
			}
			if ref.Probe.FieldOrder != "" && cur.Probe.FieldOrder != "" &&
				ref.Probe.FieldOrder != cur.Probe.FieldOrder {
				issues = append(issues, PlaylistIssue{
					MediaID: cur.ID, Name: cur.Name(),
					Field: "field_order", Severity: SeverityBlock, Reference: ref.ID,
					Message: fmt.Sprintf("Urutan field berbeda antar file (%q vs %q).",
						ref.Probe.FieldOrder, cur.Probe.FieldOrder),
				})
			}
			// A/V length mismatch inside one file shifts every later file.
			if skew := ref.Probe.AVDurationSkew(); skew > 0.05 {
				issues = append(issues, PlaylistIssue{
					MediaID: cur.ID, Name: cur.Name(),
					Field: "av_duration", Severity: SeverityWarn, Reference: ref.ID,
					Message: fmt.Sprintf(
						"Panjang track video dan audio berbeda %.0f ms pada %q; batas file dapat menimbulkan jeda atau desinkronisasi A/V.",
						skew*1000, ref.Name()),
				})
			}
			// Frame rates within ~0.5 fps are treated as equal after rounding;
			// exact 30000/1001 vs 30/1 pairs are common and concat-safe.
			if absDiff(ref.Probe.FPS, cur.Probe.FPS) > 0.5 {
				issues = append(issues, PlaylistIssue{
					MediaID: cur.ID, Name: cur.Name(),
					Field: "fps", Severity: SeverityBlock, Reference: ref.ID,
					Message: fmt.Sprintf("Frame rate berbeda antar file. %s: %.3f fps, sedangkan %q: %.3f fps.",
						ref.Name(), ref.Probe.FPS, cur.Name(), cur.Probe.FPS),
				})
			}
		}
		if ref.Probe.HasAudio && cur.Probe.HasAudio {
			compare("audio_codec", ref.Probe.AudioCodec, cur.Probe.AudioCodec,
				"Codec audio berbeda antar file")
			compare("sample_rate", fmt.Sprint(ref.Probe.SampleRate), fmt.Sprint(cur.Probe.SampleRate),
				"Sample rate audio berbeda antar file")
			compare("channel_layout", ref.Probe.ChannelLayout, cur.Probe.ChannelLayout,
				"Tata letak channel audio berbeda antar file")
		}
		if ref.Probe.HasAudio != cur.Probe.HasAudio || ref.Probe.HasVideo != cur.Probe.HasVideo {
			issues = append(issues, PlaylistIssue{
				MediaID: cur.ID, Name: cur.Name(),
				Field: "tracks", Severity: SeverityBlock, Reference: ref.ID,
				Message: fmt.Sprintf("Jumlah track berbeda antar file. %s: video=%v audio=%v, sedangkan %q: video=%v audio=%v.",
					ref.Name(), ref.Probe.HasVideo, ref.Probe.HasAudio,
					cur.Name(), cur.Probe.HasVideo, cur.Probe.HasAudio),
			})
		}
	}
	if issues == nil {
		return nil
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].MediaID < issues[j].MediaID })
	return issues
}

// NamedProbe pairs a probe with its media identity for issue reporting.
type NamedProbe struct {
	ID           string
	OriginalName string
	Probe        *Probe
}

// Name returns the display name for issue messages.
func (n NamedProbe) Name() string {
	if n.OriginalName != "" {
		return n.OriginalName
	}
	return n.ID
}

func hasSeverity(issues []Issue, sev string) bool {
	for _, i := range issues {
		if i.Severity == sev {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func containsInt(list []int, v int) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func joinInts(list []int) string {
	parts := make([]string, len(list))
	for i, v := range list {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ", ")
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func pixFmtLabel(v string) string {
	if v == "" {
		return "tidak diketahui"
	}
	return v
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
