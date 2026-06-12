//go:build linux

package collect

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"Ithiltir-node/internal/metrics"
)

const pressureRoot = "/proc/pressure"

func readPressure() metrics.Pressure {
	return metrics.Pressure{
		CPU:    readPressureResource(filepath.Join(pressureRoot, "cpu")),
		Memory: readPressureResource(filepath.Join(pressureRoot, "memory")),
		IO:     readPressureResource(filepath.Join(pressureRoot, "io")),
	}
}

func readPressureResource(path string) metrics.PressureResource {
	body, err := os.ReadFile(path)
	if err != nil {
		return pressureReadError(err)
	}

	pressure, err := parsePressureResource(string(body))
	if err != nil {
		return metrics.PressureResource{Status: metrics.StatusError}
	}
	return pressure
}

func pressureReadError(err error) metrics.PressureResource {
	status := metrics.StatusError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		status = metrics.StatusUnsupported
	case errors.Is(err, fs.ErrPermission):
		status = metrics.StatusNoPermission
	}
	return metrics.PressureResource{Status: status}
}

func parsePressureResource(text string) (metrics.PressureResource, error) {
	pressure := metrics.PressureResource{Status: metrics.StatusOK}

	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 5 {
			return metrics.PressureResource{}, fmt.Errorf("invalid pressure line %q", line)
		}

		stats, err := parsePressureStats(fields[1:])
		if err != nil {
			return metrics.PressureResource{}, err
		}
		switch fields[0] {
		case "some":
			pressure.Some = &stats
		case "full":
			pressure.Full = &stats
		}
	}

	if pressure.Some == nil && pressure.Full == nil {
		pressure.Status = metrics.StatusNotFound
	}
	return pressure, nil
}

func parsePressureStats(fields []string) (metrics.PressureStats, error) {
	stats := metrics.PressureStats{}
	seen := make(map[string]struct{}, 4)

	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return metrics.PressureStats{}, fmt.Errorf("invalid pressure field %q", field)
		}

		switch key {
		case "avg10":
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return metrics.PressureStats{}, fmt.Errorf("parse avg10: %w", err)
			}
			stats.Avg10 = v
		case "avg60":
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return metrics.PressureStats{}, fmt.Errorf("parse avg60: %w", err)
			}
			stats.Avg60 = v
		case "avg300":
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return metrics.PressureStats{}, fmt.Errorf("parse avg300: %w", err)
			}
			stats.Avg300 = v
		case "total":
			v, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return metrics.PressureStats{}, fmt.Errorf("parse total: %w", err)
			}
			stats.Total = v
		default:
			continue
		}
		seen[key] = struct{}{}
	}

	for _, key := range []string{"avg10", "avg60", "avg300", "total"} {
		if _, ok := seen[key]; !ok {
			return metrics.PressureStats{}, fmt.Errorf("missing pressure field %s", key)
		}
	}
	return stats, nil
}
