package conncache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const Schema = 1

type Cache struct {
	Schema     int       `json:"schema"`
	UpdatedAt  time.Time `json:"updated_at"`
	TTLSeconds int       `json:"ttl_seconds"`
	Status     string    `json:"status"`
	TCPCount   int       `json:"tcp_count"`
	UDPCount   int       `json:"udp_count"`
}

func DefaultPath() string {
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("ProgramData"))
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Ithiltir-node", "connections.json")
	}
	return "/run/ithiltir-node/connections.json"
}

func Read(path string, now time.Time) (tcp int, udp int, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return 0, 0, false
	}

	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		return 0, 0, false
	}
	if c.Schema != Schema || c.Status != "ok" || c.UpdatedAt.IsZero() {
		return 0, 0, false
	}
	if c.TTLSeconds <= 0 || c.TCPCount < 0 || c.UDPCount < 0 {
		return 0, 0, false
	}
	if c.UpdatedAt.UTC().Add(time.Duration(c.TTLSeconds) * time.Second).Before(now) {
		return 0, 0, false
	}
	return c.TCPCount, c.UDPCount, true
}
