//go:build windows

package monitoring

import (
	"os"
	"runtime"
	"time"
)

// readBootTime falls back to process start: Windows exposes no portable
// uptime source without a syscall, and the dashboard shows what it can.
func readBootTime() time.Time { return time.Now() }

// readProcessRSS returns 0; Windows reports memory through a different API and
// the field is simply unavailable in development builds.
func readProcessRSS() int64 { return 0 }

// readInfo reports what is knowable without Windows-specific syscalls.
func readInfo() Info {
	hostname, _ := os.Hostname()
	return Info{
		Hostname:   hostname,
		Arch:       runtime.GOARCH,
		CPUCores:   runtime.NumCPU(),
		OS:         runtime.GOOS,
		ServerBoot: time.Now(),
	}
}
