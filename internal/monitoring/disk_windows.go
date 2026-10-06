//go:build windows

package monitoring

// readDiskUsage is unavailable on Windows without a syscall; the media volume
// figure is rendered as not available rather than approximated.
func readDiskUsage(string) (DiskSample, bool) { return DiskSample{}, false }
