//go:build linux

package pve

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Ithiltir-node/internal/virt"
)

func testHistoryCache(t *testing.T) *historyCache {
	t.Helper()
	dir := t.TempDir()
	inventory := guestInventory()
	raw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "virt.json")
	if err := os.WriteFile(output, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return newHistoryCache(t.Context(), "pve", output, dir)
}

func TestHistoryCoalescesAndKeepsOtherWaiters(t *testing.T) {
	h := testHistoryCache(t)
	q := virt.HistoryQuery{VMID: 101, Timeframe: "hour", Consolidation: "AVERAGE"}
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); h.wg.Wait() }()
	h.lookup = func(ctx context.Context, _ string, _ virt.HistoryQuery, _ *os.File) ([]byte, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return []byte("{}"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	firstCtx, cancelFirst := context.WithCancel(t.Context())
	defer cancelFirst()
	first := make(chan error, 1)
	go func() { _, err := h.get(firstCtx, q); first <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	second := make(chan error, 1)
	go func() { _, err := h.get(t.Context(), q); second <- err }()
	until := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		waiters := h.calls[q].waiters
		h.mu.Unlock()
		if waiters == 2 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("second query did not join")
		}
		time.Sleep(time.Millisecond)
	}
	cancelFirst()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if _, err := h.get(t.Context(), q); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate query or cache miss")
	}
}

func TestHistoryWaiterDeadlineDoesNotCancelSharedQuery(t *testing.T) {
	h := testHistoryCache(t)
	q := virt.HistoryQuery{VMID: 101, Timeframe: "hour", Consolidation: "AVERAGE"}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); h.wg.Wait() }()
	h.lookup = func(ctx context.Context, _ string, _ virt.HistoryQuery, _ *os.File) ([]byte, error) {
		close(entered)
		<-release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []byte("{}"), nil
	}
	firstCtx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := h.get(firstCtx, q); first <- err }()
	select {
	case <-entered:
	case <-firstCtx.Done():
		t.Fatal("query did not start")
	}
	secondCtx, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	second := make(chan error, 1)
	go func() { _, err := h.get(secondCtx, q); second <- err }()
	for {
		h.mu.Lock()
		waiters := h.calls[q].waiters
		h.mu.Unlock()
		if waiters == 2 {
			break
		}
		select {
		case <-firstCtx.Done():
			t.Fatal("second waiter did not join before deadline")
		case <-time.After(time.Millisecond):
		}
	}
	if err := <-first; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first waiter: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-second; err != nil {
		t.Fatalf("first deadline canceled second waiter: %v", err)
	}
	if _, err := h.get(t.Context(), q); err != nil {
		t.Fatalf("waiter deadline poisoned cached result: %v", err)
	}
}

func TestHistoryCancellationKeepsVMLockUntilQueryReturns(t *testing.T) {
	h := testHistoryCache(t)
	q := virt.HistoryQuery{VMID: 101, Timeframe: "hour", Consolidation: "AVERAGE"}
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); h.wg.Wait() }()
	var calls atomic.Int32
	h.lookup = func(ctx context.Context, _ string, _ virt.HistoryQuery, _ *os.File) ([]byte, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.get(ctx, q); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-canceled
	other := q
	other.Timeframe = "day"
	if _, err := h.get(t.Context(), other); !errors.Is(err, virt.ErrBusy) {
		t.Fatalf("overlapping VM query: %v", err)
	}
	lock, err := guestLock(filepath.Join(h.dir, "101.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		lock.Close()
		t.Fatal("lock released before process completion")
	}
	if calls.Load() != 1 {
		t.Fatal("launched a replacement query")
	}
	once.Do(func() { close(release) })
	h.wg.Wait()
	lock, err = guestLock(filepath.Join(h.dir, "101.lock"))
	if err != nil || lock == nil {
		t.Fatal("query did not release lock", err)
	}
	lock.Close()
}

func TestHistoryFailureBackoffAndMissingVM(t *testing.T) {
	h := testHistoryCache(t)
	q := virt.HistoryQuery{VMID: 101, Timeframe: "hour", Consolidation: "MAX"}
	var calls atomic.Int32
	h.lookup = func(context.Context, string, virt.HistoryQuery, *os.File) ([]byte, error) {
		calls.Add(1)
		return nil, virt.ErrSource
	}
	for range 3 {
		if _, err := h.get(t.Context(), q); !errors.Is(err, virt.ErrSource) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("retried a failed cached query")
	}
	q.Timeframe = "day"
	if _, err := h.get(t.Context(), q); !errors.Is(err, virt.ErrSource) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("changed timeframe bypassed VM failure cooldown")
	}
	q.VMID = 999
	if _, err := h.get(t.Context(), q); !errors.Is(err, virt.ErrVMNotFound) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("queried a VM not in the local inventory")
	}
}

func TestHistoryPreservesMissingAndZeroMetrics(t *testing.T) {
	now := time.Now().UTC()
	sample := []map[string]any{{"time": now.Unix(), "cpu": nil, "mem": 0, "netin": 12.5}}
	raw, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeHistory(raw, virt.HistoryQuery{VMID: 101, Timeframe: "hour", Consolidation: "AVERAGE"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var history virt.History
	if err := json.Unmarshal(result, &history); err != nil {
		t.Fatal(err)
	}
	point := history.Points[0]
	if point.CPU != nil || point.Memory == nil || *point.Memory != 0 || point.NetIn == nil || *point.NetIn != 12.5 || !point.Timestamp.Equal(time.Unix(now.Unix(), 0)) {
		t.Fatalf("wrong history: %+v", point)
	}
}
