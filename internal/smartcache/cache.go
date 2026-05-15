package smartcache

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"Ithiltir-node/internal/metrics"
)

const Schema = 1

type Cache struct {
	Schema     int                       `json:"schema"`
	UpdatedAt  time.Time                 `json:"updated_at"`
	TTLSeconds int                       `json:"ttl_seconds"`
	Status     string                    `json:"status"`
	Devices    []metrics.DiskSMARTDevice `json:"devices"`
}

func DefaultPath() string {
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("ProgramData"))
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Ithiltir-node", "smart.json")
	}
	return "/run/ithiltir-node/smart.json"
}

func Default() metrics.DiskSMART {
	return smartStatus(metrics.StatusNoCache)
}

func Read(path string, now time.Time) metrics.DiskSMART {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return smartStatus(metrics.StatusNoCache)
		}
		if errors.Is(err, fs.ErrPermission) {
			return smartStatus(metrics.StatusNoPermission)
		}
		return smartStatus(metrics.StatusError)
	}
	if len(b) == 0 {
		return smartStatus(metrics.StatusError)
	}

	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		return smartStatus(metrics.StatusError)
	}
	if c.Schema != Schema {
		return smartStatus(metrics.StatusUnsupported)
	}
	if c.UpdatedAt.IsZero() || c.Status == "" {
		return smartStatus(metrics.StatusError)
	}
	return fromCache(c, now)
}

func fromCache(c Cache, now time.Time) metrics.DiskSMART {
	devices := c.Devices
	if devices == nil {
		devices = []metrics.DiskSMARTDevice{}
	}

	updatedAt := c.UpdatedAt.UTC()
	s := metrics.DiskSMART{
		Status:     c.Status,
		UpdatedAt:  &updatedAt,
		TTLSeconds: c.TTLSeconds,
		Devices:    devices,
	}
	if c.TTLSeconds > 0 && updatedAt.Add(time.Duration(c.TTLSeconds)*time.Second).Before(now) {
		s.Status = metrics.StatusStale
		return s
	}
	if s.Status == metrics.StatusOK && len(devices) == 0 {
		s.Status = metrics.StatusNotFound
	}
	return s
}

func smartStatus(status string) metrics.DiskSMART {
	return metrics.DiskSMART{
		Status:  status,
		Devices: []metrics.DiskSMARTDevice{},
	}
}
