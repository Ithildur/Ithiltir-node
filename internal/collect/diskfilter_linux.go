//go:build linux

package collect

import (
	"context"
	"os/exec"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

func wholeBlock(name string) bool {
	return pathExists("/sys/block/" + name)
}

func deviceBacked(name string) bool {
	return pathExists("/sys/block/" + name + "/device")
}

func filterDiskIOCounters(curr map[string]disk.IOCountersStat) map[string]disk.IOCountersStat {
	if curr == nil {
		return curr
	}

	tmp := make(map[string]disk.IOCountersStat)
	hasPhysical := false

	for name, st := range curr {
		if !wholeBlock(name) {
			continue
		}
		if ignoredBlock(name) {
			continue
		}
		if deviceBacked(name) {
			hasPhysical = true
		}
		tmp[name] = st
	}

	if !hasPhysical {
		return tmp
	}

	out := make(map[string]disk.IOCountersStat)
	for name, st := range tmp {
		if deviceBacked(name) || mdDevice(name) {
			out[name] = st
		}
	}
	return out
}

func readZFSIORates() map[string]zfsIORates {
	ctx, cancel := context.WithTimeout(context.Background(), 2800*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "zpool", "iostat", "-H", "-p", "0.5", "4").Output()
	if err != nil {
		return nil
	}
	return parseZFSIORates(string(out))
}
