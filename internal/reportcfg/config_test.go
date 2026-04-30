package reportcfg

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEditTargetsKeepsStableIDs(t *testing.T) {
	cfg := Empty()

	next, first, err := Add(cfg, "https://a.example.com/api/node/metrics", "first", "server_11111111111111111111111111111111")
	if err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	if first.ID != 1 {
		t.Fatalf("first id = %d, want 1", first.ID)
	}

	next, second, err := Add(next, "https://b.example.com/api/node/metrics", "second", "server_22222222222222222222222222222222")
	if err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}
	if second.ID != 2 {
		t.Fatalf("second id = %d, want 2", second.ID)
	}

	next, err = Remove(next, 1)
	if err != nil {
		t.Fatalf("Remove(1) error = %v", err)
	}
	next, third, err := Add(next, "https://c.example.com/api/node/metrics", "third", "server_33333333333333333333333333333333")
	if err != nil {
		t.Fatalf("Add(third) error = %v", err)
	}
	if third.ID != 3 {
		t.Fatalf("third id = %d, want 3 after removing id 1", third.ID)
	}
}

func TestSaveLoadAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.yaml")
	cfg := Config{
		Version: Version,
		Targets: []Target{{
			ID:              1,
			URL:             "https://example.com/api/node/metrics",
			Key:             "secret",
			ServerInstallID: "server_11111111111111111111111111111111",
		}},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != fileMode {
		t.Fatalf("mode = %o, want %o", got, fileMode)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.Targets) != 1 || got.Targets[0].ID != 1 || got.Targets[0].Key != "secret" || got.Targets[0].ServerInstallID == "" {
		t.Fatalf("loaded config = %+v", got)
	}
}

func TestValidateRejectsDuplicateServerInstallID(t *testing.T) {
	cfg := Config{
		Version: Version,
		Targets: []Target{
			{
				ID:              1,
				URL:             "https://a.example.com/api/node/metrics",
				Key:             "first",
				ServerInstallID: "server_11111111111111111111111111111111",
			},
			{
				ID:              2,
				URL:             "https://b.example.com/api/node/metrics",
				Key:             "second",
				ServerInstallID: "server_11111111111111111111111111111111",
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate server_install_id error")
	}
}

func TestUpdateKeyKeepsTargetIdentity(t *testing.T) {
	cfg := Config{
		Version: Version,
		Targets: []Target{{
			ID:              1,
			URL:             "https://example.com/api/node/metrics",
			Key:             "old",
			ServerInstallID: "server_11111111111111111111111111111111",
		}},
	}

	next, err := UpdateKey(cfg, 1, "new")
	if err != nil {
		t.Fatalf("UpdateKey() error = %v", err)
	}
	got := next.Targets[0]
	if got.URL != cfg.Targets[0].URL || got.ServerInstallID != cfg.Targets[0].ServerInstallID {
		t.Fatalf("UpdateKey() changed target identity: %+v", got)
	}
	if got.Key != "new" {
		t.Fatalf("key = %q, want new", got.Key)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := os.WriteFile(path, []byte("version: 1\ntargets: []\nlegacy: true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want unknown field error")
	}
}

func TestLoadRejectsBadMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no unix file mode")
	}

	path := filepath.Join(t.TempDir(), "report.yaml")
	if err := os.WriteFile(path, []byte("version: 1\ntargets: []\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want bad mode error")
	}
}

func TestRemoveMissingTarget(t *testing.T) {
	_, err := Remove(Empty(), 42)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove() error = %v, want ErrNotFound", err)
	}
}
