//go:build linux

package collect

import "Ithiltir-node/internal/metrics"

func collectSlowPlatform(thinpoolCachePath string, debug bool, usage *fsUsageReader) (filesystems []metrics.DiskUsage, storages []metrics.StorageUsage, raid raidSnapshot) {
	filesystems = collectFilesystems(usage)
	byMount := indexByMount(filesystems)

	raid = collectRaid(debug)

	storages = make([]metrics.StorageUsage, 0)
	storages = append(storages, collectZFSPools(debug)...)
	storages = append(storages, readLVMCache(thinpoolCachePath)...)
	if tree, err := readLsblkTree(); err == nil {
		storages = append(storages, collectBlockStorages(tree, byMount, raid, usage)...)
	} else {
		storages = append(storages, fallbackStorages(filesystems)...)
	}

	return filesystems, storages, raid
}
