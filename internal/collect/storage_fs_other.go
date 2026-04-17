//go:build !linux

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
	return usage.collect(parts, false)
}
