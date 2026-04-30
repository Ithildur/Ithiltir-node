//go:build linux

package collect

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

func TestMountUsageFallbackTimesOut(t *testing.T) {
	usage := newUsageTestReader()

	release := make(chan struct{})
	var released atomic.Bool
	releaseProbe := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}
	t.Cleanup(releaseProbe)

	finished := make(chan struct{})
	var calls atomic.Int32
	usage.probe = func(path string) (*disk.UsageStat, error) {
		calls.Add(1)
		defer close(finished)
		<-release
		return &disk.UsageStat{Path: path, Total: 100, Used: 40, Free: 60}, nil
	}

	start := time.Now()
	mps, used, free, total := mountUsage(lsblkDev{
		Mountpoint: "/mnt/stuck",
		Fstype:     "xfs",
	}, nil, usage)
	if time.Since(start) > 5*usage.timeout {
		t.Fatalf("mountUsage() took too long with a timed-out mount")
	}
	if len(mps) != 1 || mps[0] != "/mnt/stuck" {
		t.Fatalf("mountUsage() mountpoints = %v, want [/mnt/stuck]", mps)
	}
	if used != 0 || free != 0 || total != 0 {
		t.Fatalf("mountUsage() usage = (%d, %d, %d), want zeros after timeout", used, free, total)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("usage.probe called %d times, want 1", got)
	}

	releaseProbe()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("timed-out fallback probe did not finish")
	}
}
