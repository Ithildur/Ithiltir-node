package collect

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"

	"Ithiltir-node/internal/metrics"

	"github.com/shirou/gopsutil/v3/host"
)

var thermalKindRules = []struct {
	kind  string
	terms []string
}{
	{"cpu", []string{"coretemp", "k10temp", "cpu", "tctl", "tdie"}},
	{"gpu", []string{"gpu", "amdgpu", "radeon", "nouveau"}},
	{"chipset", []string{"pch", "chipset", "northbridge", "southbridge"}},
	{"acpi", []string{"acpi", "thermal_zone"}},
	{"board", []string{"nct", "it87", "asus", "gigabyte", "superio"}},
}

func defaultThermal() metrics.Thermal {
	status := metrics.StatusNotFound
	if runtime.GOOS == "darwin" {
		status = metrics.StatusUnsupported
	}
	return metrics.Thermal{
		Status:  status,
		Sensors: []metrics.ThermalSensor{},
	}
}

func readThermal(ctx context.Context) metrics.Thermal {
	if runtime.GOOS == "darwin" {
		return defaultThermal()
	}

	now := time.Now().UTC()
	stats, err := host.SensorsTemperaturesWithContext(ctx)
	if err != nil {
		status := metrics.StatusError
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = metrics.StatusTimeout
		} else if thermalPermission(err.Error()) {
			status = metrics.StatusNoPermission
		}
		return metrics.Thermal{
			Status:    status,
			UpdatedAt: &now,
			Sensors:   []metrics.ThermalSensor{},
		}
	}

	sensors := make([]metrics.ThermalSensor, 0, len(stats))
	for _, stat := range stats {
		key := strings.TrimSpace(stat.SensorKey)
		if key == "" {
			continue
		}
		temp := stat.Temperature
		sensors = append(sensors, metrics.ThermalSensor{
			Kind:      thermalKind(key),
			Name:      key,
			SensorKey: key,
			Source:    "gopsutil",
			Status:    metrics.StatusOK,
			TempC:     &temp,
			HighC:     nonZeroFloat(stat.High),
			CriticalC: nonZeroFloat(stat.Critical),
		})
	}
	if len(sensors) == 0 {
		return metrics.Thermal{
			Status:    metrics.StatusNotFound,
			UpdatedAt: &now,
			Sensors:   []metrics.ThermalSensor{},
		}
	}
	return metrics.Thermal{
		Status:    metrics.StatusOK,
		UpdatedAt: &now,
		Sensors:   sensors,
	}
}

func thermalKind(key string) string {
	k := strings.ToLower(key)
	for _, rule := range thermalKindRules {
		for _, term := range rule.terms {
			if strings.Contains(k, term) {
				return rule.kind
			}
		}
	}
	return "unknown"
}

func thermalPermission(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "permission denied") ||
		strings.Contains(text, "operation not permitted")
}

func nonZeroFloat(v float64) *float64 {
	if v == 0 {
		return nil
	}
	return &v
}
