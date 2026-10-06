// Package monitoring samples host metrics for the dashboard: CPU, memory,
// temperature, network throughput, disk usage and uptime (FR-02).
//
// Values that cannot be read on a given device are reported as unavailable
// rather than guessed, and every sample carries the moment it was taken so a
// disconnected panel can mark its figures as stale.
package monitoring

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sample is one monitoring reading.
type Sample struct {
	Timestamp time.Time `json:"ts"`
	// CPULoadPercent is host CPU utilisation across all cores.
	CPULoadPercent *float64 `json:"cpu_percent"`
	// MemTotalBytes and MemAvailableBytes describe RAM.
	MemTotalBytes     int64    `json:"mem_total_bytes"`
	MemAvailableBytes int64    `json:"mem_available_bytes"`
	MemUsedPercent    *float64 `json:"mem_used_percent"`
	// TemperatureCelsius is nil when no sensor can be read.
	TemperatureCelsius *float64 `json:"temperature_celsius"`
	// Network is upload/download throughput on the monitored interface, in
	// bits per second. This is host traffic, not publisher bitrate.
	Network *NetworkSample `json:"network,omitempty"`
	// PublisherBitrateKbps is measured separately by the streaming worker.
	PublisherBitrateKbps float64     `json:"publisher_bitrate_kbps"`
	Disk                 *DiskSample `json:"disk,omitempty"`
	UptimeSeconds        float64     `json:"uptime_seconds"`
	// ProcessRSSBytes is this backend's own memory use.
	ProcessRSSBytes int64 `json:"process_rss_bytes"`
	Info            Info  `json:"info"`
}

// NetworkSample is one interface's throughput.
type NetworkSample struct {
	Interface   string  `json:"interface"`
	UploadBps   float64 `json:"upload_bps"`
	DownloadBps float64 `json:"download_bps"`
	UploadMbps  float64 `json:"upload_mbps"`
}

// DiskSample is filesystem usage for the media volume.
type DiskSample struct {
	Path        string  `json:"path"`
	TotalBytes  uint64  `json:"total_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// Info is the static device inventory shown in the System card.
type Info struct {
	Hostname   string    `json:"hostname"`
	OS         string    `json:"os"`
	Arch       string    `json:"arch"`
	Kernel     string    `json:"kernel"`
	SoC        string    `json:"soc"`
	CPUModel   string    `json:"cpu_model"`
	CPUCores   int       `json:"cpu_cores"`
	TotalRAM   int64     `json:"total_ram_bytes"`
	ServerBoot time.Time `json:"boot_time"`
}

// Monitor samples the host on an interval and retains a short history.
type Monitor struct {
	mu       sync.RWMutex
	latest   Sample
	history  []Sample
	diskPath string
	iface    string
	bootTime time.Time
	info     Info

	cpuTimes  cpuSnapshot
	lastCPU   time.Time
	lastNetRx uint64
	lastNetTx uint64
	lastNetAt time.Time
}

// maxHistory matches the PRD's 15-minute window at a 2-second interval, with a
// hard cap so a long-running process cannot grow memory without bound.
const (
	historyWindow = 15 * time.Minute
	maxHistory    = 512
)

// New builds a monitor for the given disk path and network interface.
func New(diskPath, iface string) *Monitor {
	m := &Monitor{
		diskPath: diskPath,
		iface:    iface,
		bootTime: readBootTime(),
		info:     readInfo(),
	}
	m.cpuTimes = readCPUTimes()
	m.lastCPU = time.Now()
	m.lastNetAt = time.Now()
	if m.iface != "" {
		m.lastNetRx, m.lastNetTx = readNetCounters(m.iface)
	}
	return m
}

// Start samples until ctx ends, calling onSample for each new reading.
func (m *Monitor) Start(ctx context.Context, interval time.Duration, onSample func(Sample)) {
	// Take one reading immediately so the panel has data before the first tick.
	m.sample()
	if s, ok := m.Latest(); ok {
		onSample(s)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sample()
			if s, ok := m.Latest(); ok {
				onSample(s)
			}
		}
	}
}

// Latest returns the most recent sample.
func (m *Monitor) Latest() (Sample, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.latest.Timestamp.IsZero() {
		return Sample{}, false
	}
	return m.latest, true
}

// History returns retained samples inside the display window.
func (m *Monitor) History(window time.Duration) []Sample {
	if window <= 0 || window > historyWindow {
		window = historyWindow
	}
	cutoff := time.Now().Add(-window)
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Sample, 0, len(m.history))
	for _, s := range m.history {
		if s.Timestamp.After(cutoff) {
			out = append(out, s)
		}
	}
	return out
}

// Info returns the static device inventory.
func (m *Monitor) Info() Info { return m.info }

// sample takes one reading and stores it.
func (m *Monitor) sample() {
	now := time.Now()
	s := Sample{
		Timestamp:     now.UTC(),
		UptimeSeconds: time.Since(m.bootTime).Seconds(),
		Info:          m.info,
	}

	// CPU utilisation is the delta between two /proc/stat readings.
	if cur := readCPUTimes(); cur.total > 0 {
		prev := m.cpuTimes
		if prev.total > 0 {
			deltaTotal := float64(cur.total - prev.total)
			deltaIdle := float64((cur.idle + cur.iowait) - (prev.idle + prev.iowait))
			if deltaTotal > 0 {
				pct := (1 - deltaIdle/deltaTotal) * 100
				s.CPULoadPercent = &pct
			}
		}
		m.cpuTimes = cur
	}

	s.MemTotalBytes, s.MemAvailableBytes = readMemInfo()
	if s.MemTotalBytes > 0 && s.MemAvailableBytes <= s.MemTotalBytes {
		pct := float64(s.MemTotalBytes-s.MemAvailableBytes) / float64(s.MemTotalBytes) * 100
		s.MemUsedPercent = &pct
	}

	if t, ok := readTemperature(); ok {
		s.TemperatureCelsius = &t
	}

	if n := m.sampleNetwork(now); n != nil {
		s.Network = n
	}

	if d, ok := readDiskUsage(m.diskPath); ok {
		s.Disk = &d
	}

	s.ProcessRSSBytes = readProcessRSS()

	m.mu.Lock()
	m.latest = s
	m.history = append(m.history, s)
	if len(m.history) > maxHistory {
		m.history = append(m.history[:0], m.history[len(m.history)-maxHistory:]...)
	}
	m.mu.Unlock()
}

// sampleNetwork computes throughput between the previous and current counter
// readings, which is why the result is a rate rather than a total.
func (m *Monitor) sampleNetwork(now time.Time) *NetworkSample {
	if m.iface == "" {
		return nil
	}
	rx, tx := readNetCounters(m.iface)
	elapsed := now.Sub(m.lastNetAt).Seconds()
	if elapsed <= 0 {
		return nil
	}
	upBps, downBps := 0.0, 0.0
	if rx >= m.lastNetRx {
		downBps = float64(rx-m.lastNetRx) * 8 / elapsed
	}
	if tx >= m.lastNetTx {
		upBps = float64(tx-m.lastNetTx) * 8 / elapsed
	}
	m.lastNetRx, m.lastNetTx, m.lastNetAt = rx, tx, now
	return &NetworkSample{
		Interface:   m.iface,
		UploadBps:   upBps,
		DownloadBps: downBps,
		UploadMbps:  upBps / 1e6,
	}
}

// cpuSnapshot holds aggregate jiffies from /proc/stat.
type cpuSnapshot struct {
	total  uint64
	idle   uint64
	iowait uint64
}

func readCPUTimes() cpuSnapshot {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSnapshot{}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		var vals []uint64
		for _, f := range fields {
			v, _ := strconv.ParseUint(f, 10, 64)
			vals = append(vals, v)
		}
		if len(vals) < 5 {
			return cpuSnapshot{}
		}
		var s cpuSnapshot
		for _, v := range vals {
			s.total += v
		}
		s.idle = vals[3]
		s.iowait = vals[4]
		return s
	}
	return cpuSnapshot{}
}

// readMemInfo parses MemTotal and MemAvailable from /proc/meminfo, falling
// back to MemFree plus reclaimable buffers on older kernels.
func readMemInfo() (total, available int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return runtimeMemStats()
	}
	defer f.Close()
	values := map[string]int64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[key] = v * 1024 // reported in kB
	}
	total = values["MemTotal"]
	if a, ok := values["MemAvailable"]; ok {
		return total, a
	}
	return total, values["MemFree"] + values["Buffers"] + values["Cached"]
}

// runtimeMemStats is the portable fallback for non-Linux hosts.
func runtimeMemStats() (total, available int64) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.Sys), int64(ms.Sys - ms.HeapAlloc)
}

// readTemperature finds the first readable thermal zone. Devices without a
// sensor simply report unavailable rather than failing the sample.
func readTemperature() (float64, bool) {
	matches, err := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	if err != nil || len(matches) == 0 {
		return 0, false
	}
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			continue
		}
		v, err := strconv.ParseFloat(text, 64)
		if err != nil {
			continue
		}
		// The kernel reports millidegrees on virtually every device.
		if v > 1000 {
			v /= 1000
		}
		if v > 0 && v < 120 {
			return v, true
		}
	}
	return 0, false
}

// readNetCounters returns cumulative bytes for an interface.
func readNetCounters(iface string) (rx, tx uint64) {
	raw, err := os.ReadFile(filepath.Join("/sys/class/net", iface, "statistics", "rx_bytes"))
	if err != nil {
		return 0, 0
	}
	rx, _ = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	raw, err = os.ReadFile(filepath.Join("/sys/class/net", iface, "statistics", "tx_bytes"))
	if err != nil {
		return 0, 0
	}
	tx, _ = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return rx, tx
}

// DefaultInterface picks the interface carrying the default route, which is the
// one whose traffic is worth showing on a server.
func DefaultInterface() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Scan() // header
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		// Destination 00000000 is the default route.
		if fields[1] == "00000000" {
			return fields[0]
		}
	}
	return ""
}

// ListInterfaces returns every non-loopback interface name.
func ListInterfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Name() == "lo" {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}
