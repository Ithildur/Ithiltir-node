//go:build linux

package pve

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"Ithiltir-node/internal/virt"
)

const GuestDir = "/run/ithiltir-node/pve-guest"
const guestTTL = 15 * time.Minute

type guestState struct {
	Host        string    `json:"host"`
	Next        time.Time `json:"next"`
	Failures    int       `json:"failures"`
	CollectedAt time.Time `json:"collected_at"`
	IPs         []string  `json:"ips"`
}

// Locks have stable inodes. Closing (not explicitly unlocking) lets inherited
// descriptors keep exclusion if the collector exits before its query process.
func guestLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

func readGuest(path string) (guestState, error) {
	var state guestState
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(raw, &state)
	return state, err
}

func writeGuest(path string, state guestState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".guest-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func guestEligible(vm virt.VM) bool {
	return vm.Status == "running" && !vm.Template && (vm.QMPStatus == "" || vm.QMPStatus == "running")
}

// CollectGuests uses a fresh inventory but never collects hot metrics itself.
func CollectGuests(ctx context.Context, dir string, snapshot virt.Snapshot) error {
	return collectGuests(ctx, dir, snapshot, guestIPs)
}

func collectGuests(ctx context.Context, dir string, snapshot virt.Snapshot, lookup func(context.Context, string, int, *os.File) ([]string, error)) error {
	now := time.Now().UTC()
	if snapshot.Status != "ok" || snapshot.CollectedAt.After(now) || now.Sub(snapshot.CollectedAt) > time.Duration(snapshot.TTLSeconds)*time.Second {
		return errors.New("guest collection requires a fresh successful inventory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := guestLock(filepath.Join(dir, "collector.lock"))
	if err != nil || lock == nil {
		return err
	}
	defer lock.Close()
	type candidate struct {
		vm   virt.VM
		next time.Time
	}
	var candidates []candidate
	for _, vm := range snapshot.VMs {
		if !guestEligible(vm) {
			continue
		}
		state, err := readGuest(filepath.Join(dir, strconv.Itoa(vm.ID)+".json"))
		if err != nil {
			log.Printf("guest VM %d state: %v", vm.ID, err)
			continue
		}
		if !now.Before(state.Next) {
			candidates = append(candidates, candidate{vm, state.Next})
		}
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int { return a.next.Compare(b.next) })
	jobs := make(chan virt.VM)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for vm := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := queryGuest(ctx, dir, snapshot.Host, vm, lookup); err != nil {
					log.Printf("guest VM %d: %v", vm.ID, err)
				}
			}
		})
	}
send:
	for _, candidate := range candidates {
		select {
		case jobs <- candidate.vm:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

func queryGuest(ctx context.Context, dir, host string, vm virt.VM, lookup func(context.Context, string, int, *os.File) ([]string, error)) error {
	base := filepath.Join(dir, strconv.Itoa(vm.ID))
	lock, err := guestLock(base + ".lock")
	if err != nil || lock == nil {
		return err
	}
	defer lock.Close()
	state, err := readGuest(base + ".json")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if now.Before(state.Next) {
		return nil
	}
	if state.Host != host {
		state = guestState{Host: host}
	}
	state.Failures = min(max(state.Failures, 0)+1, 4)
	delay := min(5*time.Minute*time.Duration(1<<(state.Failures-1)), 30*time.Minute)
	state.Next = now.Add(delay)
	// Reserve the cooldown before launching: crashes and service timeouts must
	// not cause an immediate retry when the next timer starts.
	if err := writeGuest(base+".json", state); err != nil {
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := lookup(queryCtx, host, vm.ID, lock)
	if err != nil {
		return err
	}
	state.IPs, state.CollectedAt = ips, time.Now().UTC()
	state.Failures, state.Next = 0, state.CollectedAt.Add(5*time.Minute)
	return writeGuest(base+".json", state)
}

// AttachGuestIPs only reads slow data. Its original timestamp is preserved.
func AttachGuestIPs(dir, host string, vms []virt.VM, now time.Time) {
	for i := range vms {
		vm := &vms[i]
		vm.IPs, vm.IPsCollectedAt, vm.IPsTTLSeconds = nil, nil, 0
		if !guestEligible(*vm) {
			continue
		}
		state, err := readGuest(filepath.Join(dir, strconv.Itoa(vm.ID)+".json"))
		if err != nil || state.Host != host || state.CollectedAt.IsZero() || state.CollectedAt.After(now) || now.Sub(state.CollectedAt) > guestTTL {
			continue
		}
		// A sample older than the current boot cannot describe this guest.
		if vm.Uptime == nil || state.CollectedAt.Before(now.Add(-time.Duration(*vm.Uptime)*time.Second)) {
			continue
		}
		vm.IPs, vm.IPsCollectedAt, vm.IPsTTLSeconds = state.IPs, &state.CollectedAt, int(guestTTL/time.Second)
	}
}
