//go:build linux

package pve

import (
	"context"
	"errors"
	"sync"
	"time"

	"Ithiltir-node/internal/virt"
)

const sampleInterval = 3 * time.Second

type observation struct {
	config   vmConfig
	vm       virt.VM
	previous processSample
	next     time.Time
}

type collector struct {
	mu           sync.Mutex
	entries      map[int]*observation
	order        []int
	active       map[int]bool
	cursor       int
	changed      chan struct{}
	inventoryAt  time.Time
	inventoryErr error
	capacities   map[int]*int64
	metadataAt   time.Time
}

func newCollector() *collector {
	return &collector{
		entries: make(map[int]*observation),
		active:  make(map[int]bool),
		changed: make(chan struct{}),
	}
}

// Callers hold mu. Readers wait for changes without adding pending work.
func (c *collector) notify() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *collector) refreshInventory(ctx context.Context) error {
	configs, err := inventory(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inventoryErr = err
	if err != nil {
		return err
	}
	entries := make(map[int]*observation, len(configs))
	order := make([]int, 0, len(configs))
	for _, config := range configs {
		id := config.vm.ID
		entry := c.entries[id]
		if entry == nil {
			vm := config.vm
			vm.Status = "unknown"
			entry = &observation{vm: vm}
		}
		entry.config = config
		entries[id] = entry
		order = append(order, id)
	}
	c.entries, c.order, c.inventoryAt = entries, order, time.Now().UTC()
	c.notify()
	return nil
}

func (c *collector) next(now time.Time) (*observation, vmConfig, processSample, time.Duration, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	wait := sampleInterval
	for offset := range c.order {
		index := (c.cursor + offset) % len(c.order)
		id := c.order[index]
		entry := c.entries[id]
		if c.active[id] {
			continue
		}
		if entry.next.After(now) {
			wait = min(wait, entry.next.Sub(now))
			continue
		}
		c.active[id] = true
		c.cursor = (index + 1) % len(c.order)
		return entry, entry.config, entry.previous, 0, nil
	}
	return nil, vmConfig{}, processSample{}, wait, c.changed
}

func (c *collector) worker(ctx context.Context) {
	for ctx.Err() == nil {
		started := time.Now()
		entry, config, previous, wait, changed := c.next(started)
		if entry == nil {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-changed:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}
		vm, process := sampleVM(ctx, config, previous)
		c.mu.Lock()
		delete(c.active, config.vm.ID)
		if c.entries[config.vm.ID] == entry && ctx.Err() == nil {
			entry.vm, entry.previous = vm, process
			entry.next = started.Add(sampleInterval)
		}
		c.notify()
		c.mu.Unlock()
	}
}

// Snapshot copies completed observations; it never waits for monitor queries.
func (c *collector) snapshot(host, dir string) virt.Snapshot {
	c.mu.Lock()
	now := time.Now().UTC()
	result := virt.Snapshot{Schema: 1, Provider: "pve", Host: host, CollectedAt: now, TTLSeconds: 90, Status: "ok"}
	if !c.inventoryAt.IsZero() {
		result.LastSuccessAt = new(c.inventoryAt)
	}
	err := c.inventoryErr
	if c.inventoryAt.IsZero() || now.Sub(c.inventoryAt) > 90*time.Second {
		err = errors.New("PVE inventory unavailable or expired")
	}
	if err != nil {
		result.Status, result.Error = "error", virt.ErrorText(err)
	}
	if c.order != nil {
		result.VMs = make([]virt.VM, 0, len(c.order))
		for _, id := range c.order {
			entry := c.entries[id]
			vm := entry.vm
			vm.Name, vm.NetRateLimits = entry.config.vm.Name, entry.config.vm.NetRateLimits
			vm.DiskCapacity = nil
			if now.Sub(c.metadataAt) < 2*time.Minute {
				vm.DiskCapacity = c.capacities[id]
			}
			vm.Expire(now)
			result.VMs = append(result.VMs, vm)
		}
	}
	c.mu.Unlock()
	attachGuestIPs(dir, host, result.VMs, now)
	return result
}

func (c *collector) contains(id int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inventoryErr != nil || c.inventoryAt.IsZero() || time.Since(c.inventoryAt) > 90*time.Second {
		return virt.ErrSource
	}
	if c.entries[id] == nil {
		return virt.ErrVMNotFound
	}
	return nil
}
