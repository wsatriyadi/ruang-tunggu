//go:build unix

package monitoring

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// readBootTime derives uptime from /proc/uptime, which is present on Linux and
// on the BSD-derived systems this build targets.
func readBootTime() time.Time {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Now()
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return time.Now()
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || seconds <= 0 {
		return time.Now()
	}
	return time.Now().Add(-time.Duration(seconds * float64(time.Second)))
}

// readProcessRSS reads this process's resident memory in bytes.
func readProcessRSS() int64 {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * int64(os.Getpagesize())
}

// readInfo collects the device inventory shown in the System card.
func readInfo() Info {
	boot := readBootTime()
	hostname, _ := os.Hostname()
	totalRAM, _ := readMemInfo()
	return Info{
		Hostname:   hostname,
		Arch:       runtime.GOARCH,
		CPUCores:   runtime.NumCPU(),
		TotalRAM:   totalRAM,
		Kernel:     readKernelRelease(),
		OS:         readOSRelease(),
		CPUModel:   readCPUModel(),
		SoC:        readSoC(),
		ServerBoot: boot,
	}
}

// readKernelRelease parses the kernel version exposed by /proc/sys.
func readKernelRelease() string {
	rel, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(rel))
}

// readOSRelease prefers PRETTY_NAME from /etc/os-release and falls back to the
// kernel type, so the field is populated whenever the OS can be identified.
func readOSRelease() string {
	raw, err := os.ReadFile("/etc/os-release")
	if err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && key == "PRETTY_NAME" {
				return strings.Trim(value, `"`)
			}
		}
	}
	kind, err := os.ReadFile("/proc/sys/kernel/ostype")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(kind))
}

// readCPUModel reads the processor name from /proc/cpuinfo.
func readCPUModel() string {
	raw, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name", "Model", "Hardware":
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// readSoC reports the SoC family when the kernel exposes it. A generic x86 or
// desktop ARM box returns empty, which the UI renders as unavailable.
func readSoC() string {
	for _, path := range []string{
		"/proc/device-tree/compatible",
		"/sys/firmware/devicetree/base/compatible",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Device-tree strings are NUL separated.
		var keep []string
		for _, p := range strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00") {
			if p != "" {
				keep = append(keep, p)
			}
		}
		if len(keep) > 0 {
			return strings.Join(keep, " ")
		}
	}
	return ""
}
