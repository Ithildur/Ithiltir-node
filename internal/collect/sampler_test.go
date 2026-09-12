package collect

import (
	"encoding/json"
	"testing"
	"time"

	"Ithiltir-node/internal/metrics"
)

func TestSamplerInitialReportsKeepArrayFields(t *testing.T) {
	s := NewSampler(time.Second, 0, 0, Config{}, "1.2.3")
	static := s.Static()
	s.collectFast()
	snapshot := s.Snapshot()
	for name, value := range map[string]any{
		"static.physical":    static.Disk.Physical,
		"static.logical":     static.Disk.Logical,
		"static.filesystems": static.Disk.Filesystems,
		"static.base_io":     static.Disk.BaseIO,
		"disk.physical":      snapshot.Disk.Physical,
		"disk.logical":       snapshot.Disk.Logical,
		"disk.filesystems":   snapshot.Disk.Filesystems,
		"disk.base_io":       snapshot.Disk.BaseIO,
		"smart.devices":      snapshot.Disk.SMART.Devices,
		"network":            snapshot.Network,
		"raid.arrays":        snapshot.Raid.Arrays,
		"thermal.sensors":    snapshot.Thermal.Sensors,
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if len(data) == 0 || data[0] != '[' {
			t.Errorf("%s = %s, want JSON array", name, data)
		}
	}
	if snapshot.Disk.SMART.Status != metrics.StatusNoCache {
		t.Errorf("initial SMART status = %q, want no_cache", snapshot.Disk.SMART.Status)
	}
}

func TestSnapshotReturnsDeepCopy(t *testing.T) {
	s := &Sampler{}
	smartUpdatedAt := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	thermalUpdatedAt := time.Date(2026, 5, 14, 10, 1, 0, 0, time.UTC)
	health := "passed"
	criticalWarning := uint64(0x0e)
	mediaErrors := uint64(3)
	thermalTempC := 51.0
	cpuPressure := metrics.PressureStats{Avg10: 1.25, Avg60: 0.5, Avg300: 0.1, Total: 123}
	memoryPressure := metrics.PressureStats{Avg10: 2.5, Avg60: 1.5, Avg300: 0.5, Total: 456}
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
					MediaErrors:     &mediaErrors,
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
		Pressure: metrics.Pressure{
			CPU: metrics.PressureResource{
				Status: metrics.StatusOK,
				Some:   &cpuPressure,
			},
			Memory: metrics.PressureResource{
				Status: metrics.StatusOK,
				Full:   &memoryPressure,
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
	got := s.Snapshot()
	if got == nil {
		t.Fatal("Snapshot() = nil")
	}
	before, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	got.System.Uptime = "broken"
	got.Network[0].Name = "eth9"
	got.Disk.Physical[0].Name = "mutated"
	got.Disk.SMART.Devices[0].Name = "mutated-smart"
	*got.Disk.SMART.UpdatedAt = got.Disk.SMART.UpdatedAt.Add(time.Hour)
	*got.Disk.SMART.Devices[0].Health = "failed"
	*got.Disk.SMART.Devices[0].CriticalWarning = 0
	*got.Disk.SMART.Devices[0].MediaErrors = 0
	got.Disk.SMART.Devices[0].FailingAttrs[0].WhenFailed = ""
	got.Raid.Arrays[0].MemberStates[0].Name = "bad-member"
	*got.Pressure.CPU.Some = metrics.PressureStats{}
	*got.Pressure.Memory.Full = metrics.PressureStats{}
	got.Thermal.Sensors[0].Name = "mutated-thermal"
	*got.Thermal.UpdatedAt = got.Thermal.UpdatedAt.Add(time.Hour)
	*got.Thermal.Sensors[0].TempC = 99

	after, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("Snapshot() leaked mutations:\nbefore: %s\nafter:  %s", before, after)
	}
}
