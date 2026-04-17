package push

import (
	"testing"
	"time"

	"Ithiltir-node/internal/metrics"
)

func TestCacheOwnsCopiesOnSetAndGet(t *testing.T) {
	cache := NewCache()
	report := &metrics.NodeReport{
		Version:   "1.0.0",
		Hostname:  "node-1",
		Timestamp: time.Unix(1700000000, 0).UTC(),
		Metrics: &metrics.Snapshot{
			System:  metrics.System{Alive: true, Uptime: "1d 0h 0m"},
			Network: []metrics.NetIO{{Name: "eth0"}},
			Raid: metrics.Raid{
				Arrays: []metrics.RaidArray{
					{Name: "md0", MemberStates: []metrics.RaidMember{{Name: "sda", State: "up"}}},
				},
			},
		},
	}

	cache.Set(report)

	report.Version = "mutated"
	report.Metrics.System.Uptime = "broken"
	report.Metrics.Network[0].Name = "bad0"
	report.Metrics.Raid.Arrays[0].MemberStates[0].Name = "bad-member"

	got := cache.Get()
	if got == nil {
		t.Fatal("Get() = nil")
	}
	if got.Version != "1.0.0" {
		t.Fatalf("cached Version = %q, want original", got.Version)
	}
	if got.Metrics.System.Uptime != "1d 0h 0m" {
		t.Fatalf("cached Uptime = %q, want original", got.Metrics.System.Uptime)
	}
	if got.Metrics.Network[0].Name != "eth0" {
		t.Fatalf("cached Network[0].Name = %q, want original", got.Metrics.Network[0].Name)
	}
	if got.Metrics.Raid.Arrays[0].MemberStates[0].Name != "sda" {
		t.Fatalf("cached Raid member = %q, want original", got.Metrics.Raid.Arrays[0].MemberStates[0].Name)
	}

	got.Version = "changed-after-get"
	got.Metrics.Network[0].Name = "eth9"

	again := cache.Get()
	if again.Version != "1.0.0" {
		t.Fatalf("Get() after mutation returned Version = %q, want original", again.Version)
	}
	if again.Metrics.Network[0].Name != "eth0" {
		t.Fatalf("Get() after mutation returned Network[0].Name = %q, want original", again.Metrics.Network[0].Name)
	}
}
