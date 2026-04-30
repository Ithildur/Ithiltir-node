package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"Ithiltir-node/internal/reportcfg"
)

func TestReportInstallIsIdempotentForSameTarget(t *testing.T) {
	const (
		key       = "secret"
		installID = "server_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("identity endpoint was called for an already installed target")
	}))
	defer server.Close()

	endpoint := server.URL + "/api/node/metrics"
	cfg := reportcfg.Config{
		Version: reportcfg.Version,
		Targets: []reportcfg.Target{{
			ID:              1,
			URL:             endpoint,
			Key:             key,
			ServerInstallID: installID,
		}},
	}

	path := filepath.Join(t.TempDir(), "report.yaml")
	if got := saveReportInstall(path, cfg, endpoint, key, false); got != 0 {
		t.Fatalf("saveReportInstall() = %d, want 0", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config file was written for no-op install: %v", err)
	}
}

func TestReportInstallBackfillsLegacyTarget(t *testing.T) {
	const (
		key       = "secret"
		installID = "server_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node/identity" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("X-Node-Secret"); got != key {
			t.Errorf("X-Node-Secret = %q, want %q", got, key)
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"install_id":"` + installID + `","created":false}`))
	}))
	defer server.Close()

	endpoint := server.URL + "/api/node/metrics"
	cfg := reportcfg.Config{
		Version: reportcfg.Version,
		Targets: []reportcfg.Target{{
			ID:  1,
			URL: endpoint,
			Key: key,
		}},
	}

	path := filepath.Join(t.TempDir(), "report.yaml")
	if got := saveReportInstall(path, cfg, endpoint, key, false); got != 0 {
		t.Fatalf("saveReportInstall() = %d, want 0", got)
	}
	next, err := reportcfg.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(next.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(next.Targets))
	}
	got := next.Targets[0]
	if got.ID != 1 || got.URL != endpoint || got.Key != key || got.ServerInstallID != installID {
		t.Fatalf("target = %+v, want same target with install id", got)
	}
}
