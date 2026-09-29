//go:build linux

package pve

import (
	"context"
	"errors"
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
	ips, err := decodeIPs([]byte(`{"result":[{"ip-addresses":[{"ip-address":"127.0.0.1"},{"ip-address":"::1"},{"ip-address":"192.168.1.2"},{"ip-address":"::ffff:192.168.1.2"},{"ip-address":"2001:0db8::2"},{"ip-address":"fd00::2"},{"ip-address":"fe80::2"},{"ip-address":"169.254.1.2"},{"ip-address":"ff02::1"},{"ip-address":"224.0.0.1"},{"ip-address":"0.0.0.0"},{"ip-address":"::"},{"ip-address":"invalid"}]}]}`))
	if err != nil || !slices.Equal(ips, []string{"192.168.1.2", "2001:db8::2", "fd00::2"}) {
		t.Fatalf("IPs=%v err=%v", ips, err)
	}
	if ips, err := decodeIPs([]byte(`{"result":[]}`)); err != nil || len(ips) != 0 {
		t.Fatalf("empty interfaces: %v %v", ips, err)
	}
	for _, raw := range []string{`null`, `{}`, `{"result":null}`, `{"result":"invalid"}`} {
		if _, err := decodeIPs([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid result: %s", raw)
		}
	}
}

func guestInventory() virt.Snapshot {
	now := time.Now().UTC()
	return virt.Snapshot{Schema: 1, Provider: "pve", Host: "pve", Status: "ok", CollectedAt: now, LastSuccessAt: &now, TTLSeconds: 90, VMs: []virt.VM{{ID: 101, Status: "running", Uptime: new(int64(3600))}}}
}

func TestGuestCooldownSurvivesCollectorRestart(t *testing.T) {
	dir := t.TempDir()
	snapshot := guestInventory()
	var calls int
	lookup := func(context.Context, string, int, *os.File) ([]string, error) {
		state, err := readGuest(filepath.Join(dir, "101.json"))
		if err != nil || !state.Next.After(time.Now()) {
			t.Error("query launched before reserving cooldown", err)
		}
		calls++
		return nil, errors.New("unavailable")
	}
	for attempt, minutes := range []int{5, 10, 20, 30, 30} {
		before := time.Now()
		if err := collectGuests(t.Context(), dir, snapshot, lookup); err != nil {
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
		if err := collectGuests(t.Context(), dir, snapshot, lookup); err != nil {
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
	if err := collectGuests(t.Context(), dir, snapshot, func(context.Context, string, int, *os.File) ([]string, error) { return []string{"192.0.2.1"}, nil }); err != nil {
		t.Fatal(err)
	}
	state, _ := readGuest(filepath.Join(dir, "101.json"))
	if state.Failures != 0 || state.Next.Sub(state.CollectedAt) != 5*time.Minute {
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
	AttachGuestIPs(dir, snapshot.Host, snapshot.VMs, time.Now())
	if snapshot.VMs[0].IPsCollectedAt != nil {
		t.Fatal("retained pre-boot IPs")
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
	started := make(chan struct{}, 4)
	done := make(chan error, 1)
	var active, calls atomic.Int32
	lookup := func(ctx context.Context, _ string, _ int, _ *os.File) ([]string, error) {
		calls.Add(1)
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() { done <- collectGuests(ctx, dir, snapshot, lookup) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	if err := collectGuests(t.Context(), dir, snapshot, lookup); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatal("overlapping collector")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ignored cancellation")
	}
	if active.Load() != 0 || calls.Load() != 4 {
		t.Fatal("started queries after cancellation")
	}
	lock, err := guestLock(filepath.Join(dir, "101.lock"))
	if err != nil || lock == nil {
		t.Fatalf("lock: %v", err)
	}
	defer lock.Close()
	state, err := readGuest(filepath.Join(dir, "101.json"))
	if err != nil {
		t.Fatal(err)
	}
	state.Next = time.Now().Add(-time.Second)
	if err := writeGuest(filepath.Join(dir, "101.json"), state); err != nil {
		t.Fatal(err)
	}
	if err := queryGuest(t.Context(), dir, "pve", snapshot.VMs[0], func(context.Context, string, int, *os.File) ([]string, error) {
		t.Fatal("overlapping VM query")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGuestProcessInheritsLockAndTimeoutReaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "101.lock")
	lock, err := guestLock(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ready := filepath.Join(dir, "ready")
	cmd := exec.CommandContext(ctx, "sh", "-c", "echo ready > \"$1\"; sleep 30", "sh", ready)
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
	lock.Close()
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
	}
	lookup := func(context.Context, string, int, *os.File) ([]string, error) {
		t.Error("queried ineligible inventory")
		return nil, nil
	}
	if err := collectGuests(t.Context(), dir, snapshot, lookup); err != nil {
		t.Fatal(err)
	}
	snapshot = guestInventory()
	snapshot.CollectedAt = time.Now().Add(-2 * time.Minute)
	if err := collectGuests(t.Context(), dir, snapshot, lookup); err == nil {
		t.Fatal("accepted stale inventory")
	}
}
