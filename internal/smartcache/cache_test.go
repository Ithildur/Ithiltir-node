package smartcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Ithiltir-node/internal/metrics"
)

func TestReadCacheStatus(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()

	t.Run("missing", func(t *testing.T) {
		got := Read(filepath.Join(dir, "missing.json"), now)
		if got.Status != metrics.StatusNoCache || got.Devices == nil {
			t.Fatalf("Read(missing) = %+v, want no_cache with empty devices", got)
		}
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if !strings.Contains(string(b), `"devices":[]`) {
			t.Fatalf("devices encoded as %s, want []", b)
		}
	})

	t.Run("stale", func(t *testing.T) {
		path := filepath.Join(dir, "stale.json")
		criticalWarning := uint64(0x0e)
		writeCacheFile(t, path, Cache{
			Schema:     Schema,
			UpdatedAt:  now.Add(-10 * time.Minute),
			TTLSeconds: 300,
			Status:     metrics.StatusOK,
			Devices: []metrics.DiskSMARTDevice{{
				Name:            "sda",
				Source:          "smartctl",
				Status:          metrics.StatusOK,
				CriticalWarning: &criticalWarning,
				FailingAttrs: []metrics.DiskSMARTAttr{{
					ID:         184,
					Name:       "End-to-End_Error",
					WhenFailed: "FAILING_NOW",
				}},
			}},
		})
		got := Read(path, now)
		if got.Status != metrics.StatusStale || len(got.Devices) != 1 {
			t.Fatalf("Read(stale) = %+v, want stale with preserved devices", got)
		}
		if got.Devices[0].CriticalWarning == nil || *got.Devices[0].CriticalWarning != 0x0e {
			t.Fatalf("Read(stale) critical_warning = %v, want 0x0e", got.Devices[0].CriticalWarning)
		}
		if attrs := got.Devices[0].FailingAttrs; len(attrs) != 1 || attrs[0].WhenFailed != "FAILING_NOW" {
			t.Fatalf("Read(stale) failing_attrs = %+v", attrs)
		}
	})

	t.Run("unsupported schema", func(t *testing.T) {
		path := filepath.Join(dir, "schema.json")
		writeCacheFile(t, path, Cache{
			Schema:     Schema + 1,
			UpdatedAt:  now,
			TTLSeconds: 300,
			Status:     metrics.StatusOK,
		})
		got := Read(path, now)
		if got.Status != metrics.StatusUnsupported {
			t.Fatalf("Read(schema) = %+v, want unsupported", got)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		path := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		got := Read(path, now)
		if got.Status != metrics.StatusError {
			t.Fatalf("Read(bad json) = %+v, want error", got)
		}
	})
}

func writeCacheFile(t *testing.T, path string, c Cache) {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
