//go:build unix

package monitoring

import "golang.org/x/sys/unix"

// readDiskUsage reports filesystem usage for the volume holding path.
// Reserved blocks are excluded from the free figure the operator sees, because
// those bytes are not available to a non-root service.
func readDiskUsage(path string) (DiskSample, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return DiskSample{}, false
	}
	blockSize := uint64(st.Bsize)
	total := st.Blocks * blockSize
	// Bavail is what an unprivileged process may actually use.
	avail := st.Bavail * blockSize
	used := total - (st.Bfree * blockSize)

	sample := DiskSample{
		Path:       path,
		TotalBytes: total,
		FreeBytes:  avail,
		UsedBytes:  used,
	}
	if total > 0 {
		sample.UsedPercent = float64(used) / float64(total) * 100
	}
	return sample, total > 0
}
