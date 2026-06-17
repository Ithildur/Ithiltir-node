package conncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadReturnsFreshCounts(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	path := writeCache(t, Cache{
		Schema:     Schema,
		UpdatedAt:  now.Add(-time.Second),
		TTLSeconds: 10,
		Status:     "ok",
		TCPCount:   7,
		UDPCount:   3,
	})

	tcp, udp, ok := Read(path, now)
	if !ok || tcp != 7 || udp != 3 {
		t.Fatalf("Read() = tcp %d udp %d ok %t, want tcp 7 udp 3 ok true", tcp, udp, ok)
	}
}

func TestReadRejectsStaleCache(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	path := writeCache(t, Cache{
		Schema:     Schema,
		UpdatedAt:  now.Add(-11 * time.Second),
		TTLSeconds: 10,
		Status:     "ok",
		TCPCount:   7,
		UDPCount:   3,
	})

	_, _, ok := Read(path, now)
	if ok {
		t.Fatal("Read() ok = true, want false for stale cache")
	}
}

func TestReadRejectsInvalidCache(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		cache Cache
	}{
		{
			name: "schema",
			cache: Cache{
				Schema:     Schema + 1,
				UpdatedAt:  now,
				TTLSeconds: 10,
				Status:     "ok",
			},
		},
		{
			name: "status",
			cache: Cache{
				Schema:     Schema,
				UpdatedAt:  now,
				TTLSeconds: 10,
				Status:     "error",
			},
		},
		{
			name: "negative",
			cache: Cache{
				Schema:     Schema,
				UpdatedAt:  now,
				TTLSeconds: 10,
				Status:     "ok",
				TCPCount:   -1,
			},
		},
		{
			name: "ttl",
			cache: Cache{
				Schema:    Schema,
				UpdatedAt: now,
				Status:    "ok",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeCache(t, tt.cache)
			_, _, ok := Read(path, now)
			if ok {
				t.Fatal("Read() ok = true, want false")
			}
		})
	}
}

func writeCache(t *testing.T, cache Cache) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "connections.json")
	body, err := json.Marshal(cache)
	if err != nil {
		t.Fatalf("marshal cache: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	return path
}
