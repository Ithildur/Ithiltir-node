//go:build linux

package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"Ithiltir-node/internal/virt"
)

type historyCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	raw     []byte
	err     error
	expires time.Time
}

type historyCache struct {
	ctx               context.Context
	host, output, dir string
	slots             chan struct{}
	mu                sync.Mutex
	calls             map[virt.HistoryQuery]*historyCall
	failures          map[int]time.Time
	active, bytes     int
	wg                sync.WaitGroup
	lookup            func(context.Context, string, virt.HistoryQuery, *os.File) ([]byte, error)
}

func newHistoryCache(ctx context.Context, host, output, dir string) *historyCache {
	return &historyCache{ctx: ctx, host: host, output: output, dir: dir, slots: make(chan struct{}, 4), calls: make(map[virt.HistoryQuery]*historyCall), failures: make(map[int]time.Time), lookup: history}
}

func (h *historyCache) inventory(q virt.HistoryQuery) error {
	snapshot, err := virt.Read(h.output)
	if err != nil || snapshot.Host != h.host || snapshot.Status != "ok" || time.Since(snapshot.CollectedAt) < 0 || time.Since(snapshot.CollectedAt) > time.Duration(snapshot.TTLSeconds)*time.Second {
		return virt.ErrSource
	}
	for _, vm := range snapshot.VMs {
		if vm.ID == q.VMID {
			return nil
		}
	}
	return virt.ErrVMNotFound
}

func (h *historyCache) get(ctx context.Context, q virt.HistoryQuery) ([]byte, error) {
	if err := h.inventory(q); err != nil {
		return nil, err
	}
	h.mu.Lock()
	now := time.Now()
	for id, until := range h.failures {
		if !now.Before(until) {
			delete(h.failures, id)
		}
	}
	for key, call := range h.calls {
		if !call.expires.IsZero() && !now.Before(call.expires) {
			h.bytes -= len(call.raw)
			delete(h.calls, key)
		}
	}
	call := h.calls[q]
	if call == nil {
		if h.active >= 32 {
			h.mu.Unlock()
			return nil, virt.ErrBusy
		}
		// Evict completed entries only. Active work stays owned until reaped.
		for len(h.calls) >= 128 {
			if !h.evictOldest() {
				break
			}
		}
		// Execution belongs to the cache; individual waiters own only their wait.
		callCtx, cancel := context.WithTimeout(h.ctx, 8*time.Second)
		call = &historyCall{done: make(chan struct{}), cancel: cancel}
		h.calls[q] = call
		h.active++
		h.wg.Go(func() { h.run(callCtx, q, call) })
	}
	call.waiters++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		call.waiters--
		if call.waiters == 0 && call.expires.IsZero() {
			call.cancel()
		}
		h.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return call.raw, call.err
	}
}

func (h *historyCache) run(ctx context.Context, q virt.HistoryQuery, call *historyCall) {
	defer call.cancel()
	raw, err := h.execute(ctx, q)
	h.mu.Lock()
	defer h.mu.Unlock()
	call.raw, call.err = raw, err
	ttl := 30 * time.Second
	if err != nil {
		ttl = 10 * time.Second
	}
	call.expires = time.Now().Add(ttl)
	h.active--
	h.bytes += len(raw)
	if errors.Is(err, context.Canceled) {
		delete(h.calls, q)
		h.bytes -= len(raw)
	}
	for h.bytes > 16<<20 {
		if !h.evictOldest() {
			break
		}
	}
	close(call.done)
}

// evictOldest removes a completed entry while h.mu is held.
func (h *historyCache) evictOldest() bool {
	var oldest *historyCall
	var key virt.HistoryQuery
	for candidateKey, candidate := range h.calls {
		if !candidate.expires.IsZero() && (oldest == nil || candidate.expires.Before(oldest.expires)) {
			oldest, key = candidate, candidateKey
		}
	}
	if oldest == nil {
		return false
	}
	h.bytes -= len(oldest.raw)
	delete(h.calls, key)
	return true
}

func (h *historyCache) execute(ctx context.Context, q virt.HistoryQuery) ([]byte, error) {
	select {
	case h.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.slots }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := h.inventory(q); err != nil {
		return nil, err
	}
	lock, err := guestLock(filepath.Join(h.dir, strconv.Itoa(q.VMID)+".lock"))
	if err != nil {
		return nil, err
	}
	if lock == nil {
		return nil, virt.ErrBusy
	}
	defer lock.Close()
	h.mu.Lock()
	cooling := time.Now().Before(h.failures[q.VMID])
	h.mu.Unlock()
	if cooling {
		return nil, virt.ErrSource
	}
	raw, err := h.lookup(ctx, h.host, q, lock)
	if len(raw) > virt.HistoryMaxBytes {
		raw, err = nil, errors.New("VM history exceeds limit")
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		h.mu.Lock()
		h.failures[q.VMID] = time.Now().Add(10 * time.Second)
		h.mu.Unlock()
	}
	return raw, err
}

func (h *historyCache) guest(ctx context.Context, host string, id int, lock *os.File) ([]string, error) {
	select {
	case h.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-h.slots }()
	return guestIPs(ctx, host, id, lock)
}

func history(ctx context.Context, host string, q virt.HistoryQuery, lock *os.File) ([]byte, error) {
	raw, err := queryLocked(ctx, lock, fmt.Sprintf("/nodes/%s/qemu/%d/rrddata", host, q.VMID), "--timeframe", q.Timeframe, "--cf", q.Consolidation)
	if err != nil {
		return nil, err
	}
	return decodeHistory(raw, q, time.Now().UTC())
}

func decodeHistory(raw []byte, q virt.HistoryQuery, now time.Time) ([]byte, error) {
	var samples []struct {
		Time        float64  `json:"time"`
		CPU         *float64 `json:"cpu"`
		Memory      *float64 `json:"mem"`
		MemoryLimit *float64 `json:"maxmem"`
		DiskRead    *float64 `json:"diskread"`
		DiskWrite   *float64 `json:"diskwrite"`
		NetIn       *float64 `json:"netin"`
		NetOut      *float64 `json:"netout"`
	}
	if err := json.Unmarshal(raw, &samples); err != nil {
		return nil, err
	}
	if samples == nil || len(samples) > 4096 {
		return nil, errors.New("invalid RRD sample count")
	}
	result := virt.History{Source: "pve_rrd", VMID: q.VMID, Timeframe: q.Timeframe, Consolidation: q.Consolidation, CollectedAt: now, Points: make([]virt.HistoryPoint, 0, len(samples))}
	for _, sample := range samples {
		if sample.Time <= 0 || sample.Time > float64(now.Add(5*time.Minute).Unix()) {
			return nil, errors.New("invalid RRD timestamp")
		}
		for _, value := range []*float64{sample.CPU, sample.Memory, sample.MemoryLimit, sample.DiskRead, sample.DiskWrite, sample.NetIn, sample.NetOut} {
			if value != nil && *value < 0 {
				return nil, errors.New("invalid RRD metric")
			}
		}
		result.Points = append(result.Points, virt.HistoryPoint{Timestamp: time.Unix(int64(sample.Time), 0).UTC(), CPU: sample.CPU, Memory: sample.Memory, MemoryLimit: sample.MemoryLimit, DiskRead: sample.DiskRead, DiskWrite: sample.DiskWrite, NetIn: sample.NetIn, NetOut: sample.NetOut})
	}
	return json.Marshal(result)
}
