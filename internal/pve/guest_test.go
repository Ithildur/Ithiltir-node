//go:build linux

package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"Ithiltir-node/internal/virt"
)

func TestGuestIPsNormalizeAndFilter(t *testing.T) {
	ips, err := decodeIPs([]byte(`[{"ip-addresses":[{"ip-address":"127.0.0.1"},{"ip-address":"::1"},{"ip-address":"192.168.1.2"},{"ip-address":"::ffff:192.168.1.2"},{"ip-address":"2001:0db8::2"},{"ip-address":"fd00::2"},{"ip-address":"fe80::2"},{"ip-address":"169.254.1.2"},{"ip-address":"ff02::1"},{"ip-address":"224.0.0.1"},{"ip-address":"0.0.0.0"},{"ip-address":"::"},{"ip-address":"invalid"}]}]`))
	if err != nil || !slices.Equal(ips, []string{"192.168.1.2", "2001:db8::2", "fd00::2"}) {
		t.Fatalf("IPs=%v err=%v", ips, err)
	}
	if ips, err := decodeIPs([]byte(`[]`)); err != nil || len(ips) != 0 {
		t.Fatalf("empty interfaces: %v %v", ips, err)
	}
	for _, raw := range []string{`null`, `{}`, `"invalid"`} {
		if _, err := decodeIPs([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid result: %s", raw)
		}
	}
	addresses := make([]map[string]string, 1025)
	snapshot := guestInventory()
	snapshot.VMs[0].IPs = make([]string, len(addresses))
	for i := range addresses {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		addresses[i] = map[string]string{"ip-address": ip}
		snapshot.VMs[0].IPs[i] = ip
	}
	for _, count := range []int{1024, 1025} {
		raw, err := json.Marshal([]any{map[string]any{"ip-addresses": addresses[:count]}})
		if err != nil {
			t.Fatal(err)
		}
		ips, err := decodeIPs(raw)
		if (err == nil) != (count == 1024) || err == nil && len(ips) != count {
			t.Fatalf("IP count %d: decoded=%d err=%v", count, len(ips), err)
		}
		sample := snapshot
		sample.VMs = []virt.VM{snapshot.VMs[0]}
		sample.VMs[0].IPs = snapshot.VMs[0].IPs[:count]
		if err := sample.Validate(); (err == nil) != (count == 1024) {
			t.Fatalf("snapshot IP count %d: %v", count, err)
		}
	}
}

func guestInventory() virt.Snapshot {
	now := time.Now().UTC()
	return virt.Snapshot{Schema: 1, Provider: "pve", Host: "pve", Status: "ok", CollectedAt: now, LastSuccessAt: &now, TTLSeconds: 90, VMs: []virt.VM{{ID: 101, Status: "running", QMPStatus: "running", CollectedAt: &now, TTLSeconds: 90, Uptime: new(int64(3600))}}}
}

func guestTargets(snapshot virt.Snapshot) []guestTarget {
	targets := make([]guestTarget, len(snapshot.VMs))
	for i, vm := range snapshot.VMs {
		targets[i] = guestTarget{id: vm.ID, start: 1, agent: "1"}
	}
	return targets
}

func TestGuestCooldownSurvivesCollectorRestart(t *testing.T) {
	dir := t.TempDir()
	snapshot := guestInventory()
	var calls int
	lookup := func(context.Context, guestTarget) ([]string, error) {
		state, err := readGuest(filepath.Join(dir, "101.json"))
		if err != nil || !state.Next.After(time.Now()) {
			t.Error("query launched before reserving cooldown", err)
		}
		calls++
		return nil, errors.New("unavailable")
	}
	for attempt, minutes := range []int{1, 2, 5, 5, 5} {
		before := time.Now()
		if err := collectGuests(t.Context(), dir, snapshot.Host, guestTargets(snapshot), lookup); err != nil {
			t.Fatal(err)
		}
		if calls != attempt+1 {
			t.Fatalf("calls=%d", calls)
		}
		state, err := readGuest(filepath.Join(dir, "101.json"))
		if err != nil {
			t.Fatal(err)
		}
		if state.Next.Before(before.Add(time.Duration(minutes)*time.Minute)) || state.Next.After(time.Now().Add(time.Duration(minutes)*time.Minute)) {
			t.Fatalf("bad cooldown: %+v", state)
		}
		if err := collectGuests(t.Context(), dir, snapshot.Host, guestTargets(snapshot), lookup); err != nil {
			t.Fatal(err)
		}
		if calls != attempt+1 {
			t.Fatal("retried during persisted cooldown")
		}
		state.Next = time.Now().Add(-time.Second)
		if err := writeGuest(filepath.Join(dir, "101.json"), state); err != nil {
			t.Fatal(err)
		}
	}
	if err := collectGuests(t.Context(), dir, snapshot.Host, guestTargets(snapshot), func(context.Context, guestTarget) ([]string, error) { return []string{"192.0.2.1"}, nil }); err != nil {
		t.Fatal(err)
	}
	state, _ := readGuest(filepath.Join(dir, "101.json"))
	if state.Failures != 0 || state.Next.Sub(state.CollectedAt) != time.Minute {
		t.Fatalf("success: %+v", state)
	}
	AttachGuestIPs(dir, snapshot.Host, snapshot.VMs, time.Now())
	vm := snapshot.VMs[0]
	if !slices.Equal(vm.IPs, state.IPs) || vm.IPsCollectedAt == nil || !vm.IPsCollectedAt.Equal(state.CollectedAt) || vm.IPsTTLSeconds != 900 {
		t.Fatalf("sample: %+v", vm)
	}
	AttachGuestIPs(dir, snapshot.Host, snapshot.VMs, time.Now().Add(16*time.Minute))
	if snapshot.VMs[0].IPsCollectedAt != nil {
		t.Fatal("retained expired IPs")
	}
	snapshot.VMs[0].Uptime = new(int64(0))
	snapshot.VMs[0].CollectedAt = new(time.Now().UTC())
	AttachGuestIPs(dir, snapshot.Host, snapshot.VMs, time.Now())
	if snapshot.VMs[0].IPsCollectedAt != nil {
		t.Fatal("retained pre-boot IPs")
	}
	targets := guestTargets(snapshot)
	targets[0].start++
	var restarted int
	lookupRestarted := func(context.Context, guestTarget) ([]string, error) {
		restarted++
		return []string{}, nil
	}
	if err := collectGuests(t.Context(), dir, snapshot.Host, targets, lookupRestarted); err != nil {
		t.Fatal(err)
	}
	state, _ = readGuest(filepath.Join(dir, "101.json"))
	if restarted != 1 || len(state.IPs) != 0 || state.Start != targets[0].start {
		t.Fatalf("restart retained cooldown or old IPs: %+v calls=%d", state, restarted)
	}
	targets[0].agent = ""
	if err := collectGuests(t.Context(), dir, snapshot.Host, targets, lookupRestarted); err != nil {
		t.Fatal(err)
	}
	state, _ = readGuest(filepath.Join(dir, "101.json"))
	if restarted != 1 || !state.CollectedAt.IsZero() {
		t.Fatal("queried disabled agent or retained its IPs")
	}
}

func TestGuestConcurrentCollectorsAndCancellation(t *testing.T) {
	dir := t.TempDir()
	snapshot := guestInventory()
	for id := 102; id < 120; id++ {
		snapshot.VMs = append(snapshot.VMs, virt.VM{ID: id, Status: "running"})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, 8)
	done := make(chan error, 1)
	var active, calls atomic.Int32
	lookup := func(ctx context.Context, _ guestTarget) ([]string, error) {
		calls.Add(1)
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() { done <- collectGuests(ctx, dir, snapshot.Host, guestTargets(snapshot), lookup) }()
	for range 8 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	if err := collectGuests(t.Context(), dir, snapshot.Host, guestTargets(snapshot), lookup); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 8 {
		t.Fatal("overlapping collector")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ignored cancellation")
	}
	if active.Load() != 0 || calls.Load() != 8 {
		t.Fatal("started queries after cancellation")
	}
	state, err := readGuest(filepath.Join(dir, "101.json"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Failures != 0 {
		t.Fatal("collector cancellation penalized the guest")
	}
}

func TestQueryProcessInheritsLockAndTimeoutReaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "101.lock")
	lock, err := guestLock(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ready := filepath.Join(dir, "ready")
	cmd := exec.CommandContext(ctx, "sh", "-c", "test /proc/$$/fd/3 -ef \"$2\" || exit 1; echo ready > \"$1\"; sleep 30", "sh", ready, path)
	done := make(chan error, 1)
	go func() { _, err := runCommand(ctx, cmd, lock); done <- err }()
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	other, err := guestLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if other != nil {
		other.Close()
		t.Fatal("query did not retain lock")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not reap query")
	}
	lock.Close()
	other, err = guestLock(path)
	if err != nil || other == nil {
		t.Fatalf("query descendants retained lock after timeout: %v", err)
	}
	other.Close()
}

func TestGuestSkipsIneligibleAndStaleInventory(t *testing.T) {
	dir := t.TempDir()
	snapshot := guestInventory()
	snapshot.VMs = []virt.VM{
		{ID: 1, Status: "stopped"},
		{ID: 2, Status: "running", Template: true},
		{ID: 3, Status: "running", QMPStatus: "paused"},
		{ID: 4, Status: "running"},
		{ID: 5, Status: "running", QMPStatus: "shutdown"},
	}
	if err := CollectGuests(t.Context(), dir, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot = guestInventory()
	snapshot.CollectedAt = time.Now().Add(-2 * time.Minute)
	if err := CollectGuests(t.Context(), dir, snapshot); err == nil {
		t.Fatal("accepted stale inventory")
	}
}
