//go:build !linux

package collect

import "github.com/shirou/gopsutil/v3/disk"

func filterDiskIOCounters(curr map[string]disk.IOCountersStat) map[string]disk.IOCountersStat {
	return curr
}

func readZFSIORates() map[string]zfsIORates {
	return nil
}
