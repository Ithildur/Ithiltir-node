package collect

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestZFSIOSamplerRefreshesInBackground(t *testing.T) {
	z := newZFSIOSampler()
	var calls atomic.Int32
	z.readRates = func() map[string]zfsIORates {
		calls.Add(1)
		return map[string]zfsIORates{
			"tank": {readBps: 10, writeBps: 20, readIOPS: 1, writeIOPS: 2},
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	z.start(ctx, time.Hour, func() bool { return true })
	z.trigger()

	deadline := time.After(time.Second)
	for {
		if got := z.snapshot(); got["tank"].readBps == 10 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("zfs IO sampler did not refresh")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	got := z.snapshot()
	got["tank"] = zfsIORates{readBps: 99}
	if again := z.snapshot(); again["tank"].readBps != 10 {
		t.Fatalf("snapshot() leaked cached map mutation, got %+v", again["tank"])
	}

	z.trigger()
	if gotCalls := calls.Load(); gotCalls != 1 {
		t.Fatalf("readRates called %d times, want 1", gotCalls)
	}
}
