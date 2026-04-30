package collect

import (
	"testing"

	"Ithiltir-node/internal/metrics"
)

func TestSnapshotReturnsDeepCopy(t *testing.T) {
	s := &Sampler{}
	s.mu.Lock()
	s.latest = &metrics.Snapshot{
		System:  metrics.System{Alive: true, Uptime: "1d 0h 0m"},
		Network: []metrics.NetIO{{Name: "eth0"}},
		Disk: metrics.Disk{
			Physical: []metrics.DiskPhysical{{Name: "nvme0n1"}},
		},
		Raid: metrics.Raid{
			Arrays: []metrics.RaidArray{
				{Name: "md0", MemberStates: []metrics.RaidMember{{Name: "sda", State: "up"}}},
			},
		},
	}
	s.mu.Unlock()

	got := s.Snapshot()
	if got == nil {
		t.Fatal("Snapshot() = nil")
	}

	got.System.Uptime = "broken"
	got.Network[0].Name = "eth9"
	got.Disk.Physical[0].Name = "mutated"
	got.Raid.Arrays[0].MemberStates[0].Name = "bad-member"

	again := s.Snapshot()
	if again.System.Uptime != "1d 0h 0m" {
		t.Fatalf("Snapshot() leaked scalar mutation, got %q", again.System.Uptime)
	}
	if again.Network[0].Name != "eth0" {
		t.Fatalf("Snapshot() leaked network mutation, got %q", again.Network[0].Name)
	}
	if again.Disk.Physical[0].Name != "nvme0n1" {
		t.Fatalf("Snapshot() leaked disk mutation, got %q", again.Disk.Physical[0].Name)
	}
	if again.Raid.Arrays[0].MemberStates[0].Name != "sda" {
		t.Fatalf("Snapshot() leaked raid member mutation, got %q", again.Raid.Arrays[0].MemberStates[0].Name)
	}
}
