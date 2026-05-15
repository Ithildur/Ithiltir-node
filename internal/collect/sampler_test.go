package collect

import (
	"testing"
	"time"

	"Ithiltir-node/internal/metrics"
)

func TestSnapshotReturnsDeepCopy(t *testing.T) {
	s := &Sampler{}
	smartUpdatedAt := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	thermalUpdatedAt := time.Date(2026, 5, 14, 10, 1, 0, 0, time.UTC)
	health := "passed"
	criticalWarning := uint64(0x0e)
	thermalTempC := 51.0
	s.mu.Lock()
	s.latest = &metrics.Snapshot{
		System:  metrics.System{Alive: true, Uptime: "1d 0h 0m"},
		Network: []metrics.NetIO{{Name: "eth0"}},
		Disk: metrics.Disk{
			Physical: []metrics.DiskPhysical{{Name: "nvme0n1"}},
			SMART: metrics.DiskSMART{
				UpdatedAt: &smartUpdatedAt,
				Devices: []metrics.DiskSMARTDevice{{
					Name:            "nvme0n1",
					Source:          "smartctl",
					Status:          metrics.StatusOK,
					Health:          &health,
					CriticalWarning: &criticalWarning,
					FailingAttrs: []metrics.DiskSMARTAttr{{
						ID:         184,
						Name:       "End-to-End_Error",
						WhenFailed: "FAILING_NOW",
					}},
				}},
			},
		},
		Raid: metrics.Raid{
			Arrays: []metrics.RaidArray{
				{Name: "md0", MemberStates: []metrics.RaidMember{{Name: "sda", State: "up"}}},
			},
		},
		Thermal: metrics.Thermal{
			UpdatedAt: &thermalUpdatedAt,
			Sensors: []metrics.ThermalSensor{{
				Name:      "coretemp",
				SensorKey: "coretemp",
				Status:    metrics.StatusOK,
				TempC:     &thermalTempC,
			}},
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
	got.Disk.SMART.Devices[0].Name = "mutated-smart"
	*got.Disk.SMART.UpdatedAt = got.Disk.SMART.UpdatedAt.Add(time.Hour)
	*got.Disk.SMART.Devices[0].Health = "failed"
	*got.Disk.SMART.Devices[0].CriticalWarning = 0
	got.Disk.SMART.Devices[0].FailingAttrs[0].WhenFailed = ""
	got.Raid.Arrays[0].MemberStates[0].Name = "bad-member"
	got.Thermal.Sensors[0].Name = "mutated-thermal"
	*got.Thermal.UpdatedAt = got.Thermal.UpdatedAt.Add(time.Hour)
	*got.Thermal.Sensors[0].TempC = 99

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
	if again.Disk.SMART.Devices[0].Name != "nvme0n1" {
		t.Fatalf("Snapshot() leaked smart mutation, got %q", again.Disk.SMART.Devices[0].Name)
	}
	if !again.Disk.SMART.UpdatedAt.Equal(smartUpdatedAt) {
		t.Fatalf("Snapshot() leaked smart updated_at mutation, got %v", again.Disk.SMART.UpdatedAt)
	}
	if again.Disk.SMART.Devices[0].Health == nil || *again.Disk.SMART.Devices[0].Health != "passed" {
		t.Fatalf("Snapshot() leaked smart health mutation, got %v", again.Disk.SMART.Devices[0].Health)
	}
	if again.Disk.SMART.Devices[0].CriticalWarning == nil || *again.Disk.SMART.Devices[0].CriticalWarning != 0x0e {
		t.Fatalf("Snapshot() leaked smart critical warning mutation, got %v", again.Disk.SMART.Devices[0].CriticalWarning)
	}
	if got := again.Disk.SMART.Devices[0].FailingAttrs; len(got) != 1 || got[0].WhenFailed != "FAILING_NOW" {
		t.Fatalf("Snapshot() leaked smart failing_attrs mutation, got %+v", got)
	}
	if again.Raid.Arrays[0].MemberStates[0].Name != "sda" {
		t.Fatalf("Snapshot() leaked raid member mutation, got %q", again.Raid.Arrays[0].MemberStates[0].Name)
	}
	if again.Thermal.Sensors[0].Name != "coretemp" {
		t.Fatalf("Snapshot() leaked thermal mutation, got %q", again.Thermal.Sensors[0].Name)
	}
	if !again.Thermal.UpdatedAt.Equal(thermalUpdatedAt) {
		t.Fatalf("Snapshot() leaked thermal updated_at mutation, got %v", again.Thermal.UpdatedAt)
	}
	if again.Thermal.Sensors[0].TempC == nil || *again.Thermal.Sensors[0].TempC != 51 {
		t.Fatalf("Snapshot() leaked thermal temp mutation, got %v", again.Thermal.Sensors[0].TempC)
	}
}
