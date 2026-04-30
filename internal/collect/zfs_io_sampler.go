package collect

import (
	"context"
	"sync"
	"time"
)

type zfsIOSampler struct {
	readRates func() map[string]zfsIORates
	refreshCh chan struct{}

	mu    sync.RWMutex
	rates map[string]zfsIORates
}

func newZFSIOSampler() *zfsIOSampler {
	return &zfsIOSampler{
		readRates: readZFSIORates,
		refreshCh: make(chan struct{}, 1),
	}
}

func (z *zfsIOSampler) start(ctx context.Context, interval time.Duration, enabled func() bool) {
	if z == nil {
		return
	}
	go z.run(ctx, interval, enabled)
}

func (z *zfsIOSampler) trigger() {
	if z == nil {
		return
	}
	if z.hasRates() {
		return
	}
	select {
	case z.refreshCh <- struct{}{}:
	default:
	}
}

func (z *zfsIOSampler) snapshot() map[string]zfsIORates {
	if z == nil {
		return nil
	}
	z.mu.RLock()
	defer z.mu.RUnlock()
	return cloneZFSIORates(z.rates)
}

func (z *zfsIOSampler) hasRates() bool {
	z.mu.RLock()
	defer z.mu.RUnlock()
	return len(z.rates) > 0
}

func (z *zfsIOSampler) run(ctx context.Context, interval time.Duration, enabled func() bool) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-z.refreshCh:
		case <-ticker.C:
		case <-ctx.Done():
			return
		}

		if enabled != nil && !enabled() {
			z.setRates(nil)
			continue
		}
		z.setRates(z.readRates())
	}
}

func (z *zfsIOSampler) setRates(next map[string]zfsIORates) {
	z.mu.Lock()
	z.rates = cloneZFSIORates(next)
	z.mu.Unlock()
}

func cloneZFSIORates(in map[string]zfsIORates) map[string]zfsIORates {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]zfsIORates, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
