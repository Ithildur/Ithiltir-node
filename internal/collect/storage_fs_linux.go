//go:build linux

package collect

import (
	"Ithiltir-node/internal/metrics"

	"github.com/shirou/gopsutil/v3/disk"
)

func collectFilesystems(usage *fsUsageReader) []metrics.DiskUsage {
	parts, err := disk.Partitions(true)
	if err != nil {
		return nil
	}
	return usage.collect(parts, true)
}

func indexByMount(fs []metrics.DiskUsage) map[string]metrics.DiskUsage {
	m := make(map[string]metrics.DiskUsage, len(fs))
	for _, u := range fs {
		key := u.Mountpoint
		if key == "" {
			key = u.Path
		}
		if key == "" {
			continue
		}
		m[key] = u
	}
	return m
}
