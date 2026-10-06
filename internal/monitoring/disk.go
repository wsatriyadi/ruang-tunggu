package monitoring

// ReadDiskUsage reports filesystem usage for the volume holding path.
// Exported so quota checks can run against the same figure the dashboard shows.
func ReadDiskUsage(path string) (DiskSample, bool) { return readDiskUsage(path) }
