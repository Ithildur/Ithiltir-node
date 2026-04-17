//go:build !linux

package collect

import "Ithiltir-node/internal/metrics"

func collectSlowPlatform(_ string, _ bool, usage *fsUsageReader) (filesystems []metrics.DiskUsage, storages []metrics.StorageUsage, raid raidSnapshot) {
	filesystems = collectFilesystems(usage)
	storages = fallbackStorages(filesystems)
	raid = raidSnapshot{Supported: false, Available: false, Arrays: []raidArraySnapshot{}}
	return filesystems, storages, raid
}
