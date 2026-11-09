//go:build linux

package pve

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"Ithiltir-node/internal/virt"
)

const GuestDir = "/run/ithiltir-node/pve-guest"
const guestTTL = 15 * time.Minute

type guestState struct {
	Host        string    `json:"host"`
	Next        time.Time `json:"next"`
	Failures    int       `json:"failures"`
	Start       uint64    `json:"start"`
	Agent       string    `json:"agent,omitempty"`
	CollectedAt time.Time `json:"collected_at"`
	IPs         []string  `json:"ips"`
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
	return vm.Status == "running" && !vm.Template && vm.QMPStatus == "running"
}

type guestTarget struct {
	id    int
	start uint64
	agent string
}

// collectGuestIPs reads current configuration and boot identity so a newly
// enabled agent or restarted VM does not inherit an old retry delay.
func collectGuestIPs(ctx context.Context, dir string, snapshot virt.Snapshot) error {
	if snapshot.Status != "ok" {
		return errors.New("guest collection requires a successful inventory")
	}
	hostCPUs, err := hostCPUCount(ctx)
	if err != nil {
		return err
	}
	var targets []guestTarget
	for _, vm := range snapshot.VMs {
		if !guestEligible(vm) {
			continue
		}
		config, err := readConfig(vm.ID, hostCPUs)
		if err != nil {
			log.Printf("guest VM %d config: %v", vm.ID, err)
			continue
		}
		process, err := readProcess(vm.ID)
		if err != nil {
			log.Printf("guest VM %d process: %v", vm.ID, err)
			continue
		}
		targets = append(targets, guestTarget{id: vm.ID, start: process.start, agent: config.agent})
	}
	return collectGuests(ctx, dir, snapshot.Host, targets, guestIPs)
}

// The helper's guest collection loop runs one round at a time.
func collectGuests(ctx context.Context, dir, host string, targets []guestTarget, lookup func(context.Context, guestTarget) ([]string, error)) error {
	type candidate struct {
		target   guestTarget
		state    guestState
		priority int // healthy first, then first observations, then retries
	}
	now := time.Now().UTC()
	var candidates []candidate
	for _, target := range targets {
		path := filepath.Join(dir, strconv.Itoa(target.id)+".json")
		state, err := readGuest(path)
		if err != nil {
			log.Printf("guest VM %d state: %v", target.id, err)
			continue
		}
		if state.Host != host || state.Start != target.start || state.Agent != target.agent {
			state = guestState{Host: host, Start: target.start, Agent: target.agent}
			if err := writeGuest(path, state); err != nil {
				return err
			}
		}
		if target.agent == "" || now.Before(state.Next) {
			continue
		}
		priority := 0
		if state.CollectedAt.IsZero() {
			priority = 1
		}
		if state.Failures > 0 {
			priority = 2
		}
		candidates = append(candidates, candidate{target, state, priority})
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(a.priority, b.priority), a.state.Next.Compare(b.state.Next))
	})
	jobs := make(chan candidate)
	retries := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for job := range jobs {
				if ctx.Err() != nil {
					continue
				}
				retry := job.priority == 2
				if retry {
					select {
					case retries <- struct{}{}:
					default:
						continue
					}
				}
				err := queryGuest(ctx, dir, job.target, job.state, lookup)
				if retry {
					<-retries
				}
				if err != nil && ctx.Err() == nil {
					log.Printf("guest VM %d: %v", job.target.id, err)
				}
			}
		})
	}
send:
	for _, job := range candidates {
		select {
		case jobs <- job:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

func queryGuest(ctx context.Context, dir string, target guestTarget, state guestState, lookup func(context.Context, guestTarget) ([]string, error)) error {
	base := filepath.Join(dir, strconv.Itoa(target.id))
	now := time.Now().UTC()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	previous := state
	state.Failures = min(max(state.Failures, 0)+1, 3)
	state.Next = now.Add([]time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute}[state.Failures-1])
	// Reserve before launching, so a helper crash cannot cause a retry storm.
	if err := writeGuest(base+".json", state); err != nil {
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, err := lookup(queryCtx, target)
	if ctx.Err() != nil {
		// A service stop or round budget is not evidence of an unhealthy agent.
		previous.Next = time.Now().Add(3 * time.Second)
		return errors.Join(ctx.Err(), writeGuest(base+".json", previous))
	}
	if err != nil {
		return err
	}
	state.IPs, state.CollectedAt = ips, time.Now().UTC()
	state.Failures, state.Next = 0, state.CollectedAt.Add(time.Minute)
	return writeGuest(base+".json", state)
}

// attachGuestIPs only reads slow data. Its original timestamp is preserved.
func attachGuestIPs(dir, host string, vms []virt.VM, now time.Time) {
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
		if vm.Uptime == nil || vm.CollectedAt == nil || state.CollectedAt.Before(vm.CollectedAt.Add(-time.Duration(*vm.Uptime)*time.Second)) {
			continue
		}
		vm.IPs, vm.IPsCollectedAt, vm.IPsTTLSeconds = state.IPs, &state.CollectedAt, int(guestTTL/time.Second)
	}
}
