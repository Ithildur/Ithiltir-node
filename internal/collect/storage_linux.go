//go:build linux

package collect

import "Ithiltir-node/internal/metrics"

func collectSlowPlatform(thinpoolCachePath string, debug bool, usage *fsUsageReader) (filesystems []metrics.DiskUsage, storages []metrics.StorageUsage, raid raidSnapshot) {
	filesystems = collectFilesystems(usage)
	byMount := indexByMount(filesystems)

	raid = collectRaid(debug)

	var (
		tree          *lsblkOutput
		zfs           []metrics.StorageUsage
		lvm           []metrics.StorageUsage
		blockStorages []metrics.StorageUsage
	)

	zfs = append(zfs, collectZFSPools(debug)...)
	lvm = append(lvm, readLVMCache(thinpoolCachePath)...)
	if t, err := readLsblkTree(); err == nil {
		tree = t
		blockStorages = append(blockStorages, collectBlockStorages(tree, byMount, raid, usage)...)
	} else {
		blockStorages = append(blockStorages, fallbackStorages(filesystems)...)
	}

	storages = make([]metrics.StorageUsage, 0, len(zfs)+len(lvm)+len(blockStorages))
	storages = append(storages, zfs...)
	storages = append(storages, lvm...)
	storages = append(storages, blockStorages...)

	return filesystems, storages, raid
}
