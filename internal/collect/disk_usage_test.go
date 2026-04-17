package collect

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

func newUsageTestReader() *fsUsageReader {
	usage := newFSUsageReader()
	usage.timeout = 20 * time.Millisecond
	return usage
}

func TestDiskUsageReusesCachedValueWhenRefreshTimesOut(t *testing.T) {
	usage := newUsageTestReader()

	first := &disk.UsageStat{Path: "/mnt/test", Total: 100, Used: 40, Free: 60}
	usage.probe = func(path string) (*disk.UsageStat, error) {
		return first, nil
	}

	got, err := usage.read("/mnt/test")
	if err != nil {
		t.Fatalf("usage.read() initial probe error = %v", err)
	}
	if got != first {
		t.Fatalf("usage.read() initial probe returned %+v, want cached %+v", got, first)
	}

	release := make(chan struct{})
	refreshed := make(chan struct{})
	usage.probe = func(path string) (*disk.UsageStat, error) {
		defer close(refreshed)
		<-release
		return &disk.UsageStat{Path: path, Total: 100, Used: 55, Free: 45}, nil
	}

	start := time.Now()
	got, err = usage.read("/mnt/test")
	if err != nil {
		t.Fatalf("usage.read() timeout refresh error = %v", err)
	}
	if got != first {
		t.Fatalf("usage.read() timeout refresh returned %+v, want stale cached %+v", got, first)
	}
	if time.Since(start) < usage.timeout/2 {
		t.Fatalf("usage.read() returned too early, timeout path not exercised")
	}

	close(release)
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("refresh probe did not finish")
	}
}

func TestDiskUsageMarksTimedOutProbeAsStuckUntilRecovery(t *testing.T) {
	usage := newUsageTestReader()

	var calls atomic.Int32
	release := make(chan struct{})
	finished := make(chan struct{})
	want := &disk.UsageStat{Path: "/mnt/stuck", Total: 300, Used: 90, Free: 210}
	usage.probe = func(path string) (*disk.UsageStat, error) {
		if calls.Add(1) == 1 {
			defer close(finished)
			<-release
		}
		return want, nil
	}

	_, err := usage.read("/mnt/stuck")
	if err != context.DeadlineExceeded {
		t.Fatalf("usage.read() first error = %v, want DeadlineExceeded", err)
	}

	start := time.Now()
	_, err = usage.read("/mnt/stuck")
	if err != context.DeadlineExceeded {
		t.Fatalf("usage.read() second error = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > usage.timeout/2 {
		t.Fatalf("usage.read() stuck path did not short-circuit")
	}
	if gotCalls := calls.Load(); gotCalls != 1 {
		t.Fatalf("usage.probe called %d times, want 1", gotCalls)
	}

	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("timed-out probe did not finish")
	}

	got, err := usage.read("/mnt/stuck")
	if err != nil {
		t.Fatalf("usage.read() recovered error = %v", err)
	}
	if got == nil || got.Path != want.Path || got.Total != want.Total || got.Used != want.Used {
		t.Fatalf("usage.read() recovered result = %+v, want %+v", got, want)
	}
	if gotCalls := calls.Load(); gotCalls != 2 {
		t.Fatalf("usage.probe called %d times after recovery, want 2", gotCalls)
	}
}

func TestCollectUsagesKeepsHealthyMountsAfterTimeout(t *testing.T) {
	usage := newUsageTestReader()
	usage.workers = 2

	release := make(chan struct{})
	stuckDone := make(chan struct{})
	usage.probe = func(path string) (*disk.UsageStat, error) {
		switch path {
		case "/mnt/stuck":
			defer close(stuckDone)
			<-release
			return nil, context.Canceled
		case "/mnt/ok":
			return &disk.UsageStat{
				Path:  path,
				Total: 200,
				Used:  80,
				Free:  120,
			}, nil
		default:
			return nil, nil
		}
	}

	parts := []disk.PartitionStat{
		{Device: "/dev/stuck", Mountpoint: "/mnt/stuck", Fstype: "xfs"},
		{Device: "/dev/ok", Mountpoint: "/mnt/ok", Fstype: "ext4"},
	}

	start := time.Now()
	got := usage.collect(parts, false)
	if time.Since(start) > 5*usage.timeout {
		t.Fatalf("usage.collect() took too long with a timed-out mount")
	}
	if len(got) != 1 {
		t.Fatalf("usage.collect() returned %d entries, want 1", len(got))
	}
	if got[0].Mountpoint != "/mnt/ok" {
		t.Fatalf("usage.collect() mountpoint = %q, want /mnt/ok", got[0].Mountpoint)
	}
	if got[0].Device != "/dev/ok" || got[0].Total != 200 || got[0].Used != 80 || got[0].Free != 120 {
		t.Fatalf("usage.collect() metric = %+v", got[0])
	}

	close(release)
	select {
	case <-stuckDone:
	case <-time.After(time.Second):
		t.Fatal("timed-out mount probe did not finish")
	}
}
