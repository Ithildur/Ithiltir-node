package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Ithiltir-node/internal/metrics"
)

type testSource struct {
	snapshot *metrics.Snapshot
	static   *metrics.Static
}

type reportOnlySource struct {
	snapshot *metrics.Snapshot
}

func (s testSource) Snapshot() *metrics.Snapshot { return s.snapshot }
func (s testSource) Time() time.Time             { return time.Date(2026, 5, 1, 3, 4, 5, 0, time.UTC) }
func (s testSource) Version() string             { return "0.3.0" }
func (s testSource) Hostname() string            { return "node-a" }
func (s testSource) Static() *metrics.Static     { return s.static }

func (s reportOnlySource) Snapshot() *metrics.Snapshot { return s.snapshot }
func (s reportOnlySource) Time() time.Time             { return time.Date(2026, 5, 1, 3, 4, 5, 0, time.UTC) }
func (s reportOnlySource) Version() string             { return "0.3.0" }
func (s reportOnlySource) Hostname() string            { return "node-a" }

func TestLocalPageKeepsMetricsJSON(t *testing.T) {
	srv, _ := NewServer("127.0.0.1", "0", testSource{snapshot: &metrics.Snapshot{
		CPU: metrics.CPU{UsageRatio: 0.42},
		System: metrics.System{
			Alive:  true,
			Uptime: "1h",
		},
	}}, false)

	page := httptest.NewRecorder()
	srv.Handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", page.Code)
	}
	if ct := page.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("GET / Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(page.Body.String(), "<title>Ithiltir-node Local</title>") {
		t.Fatal("GET / did not return the local page")
	}

	report := httptest.NewRecorder()
	srv.Handler.ServeHTTP(report, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if report.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", report.Code)
	}
	if ct := report.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("GET /metrics Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(report.Body.String(), `"hostname":"node-a"`) {
		t.Fatal("GET /metrics did not return the NodeReport JSON")
	}
}

func TestLocalAliasReturnsPage(t *testing.T) {
	srv, _ := NewServer("127.0.0.1", "0", testSource{snapshot: &metrics.Snapshot{}}, false)

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /local status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<title>Ithiltir-node Local</title>") {
		t.Fatal("GET /local did not return the local page")
	}

	old := httptest.NewRecorder()
	srv.Handler.ServeHTTP(old, httptest.NewRequest(http.MethodGet, "/serve", nil))
	if old.Code != http.StatusNotFound {
		t.Fatalf("GET /serve status = %d, want 404", old.Code)
	}
}

func TestLocalStaticEndpoint(t *testing.T) {
	srv, _ := NewServer("127.0.0.1", "0", testSource{
		snapshot: &metrics.Snapshot{},
		static: &metrics.Static{
			Version: "0.3.0",
			CPU: metrics.StaticCPU{Info: metrics.StaticCPUInfo{
				ModelName:    "test cpu",
				CoresLogical: 8,
			}},
			Memory: metrics.StaticMemory{Total: 32 << 30},
		},
	}, false)

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("GET /static Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"model_name":"test cpu"`) {
		t.Fatal("GET /static did not return the Static JSON")
	}
}

func TestLocalStaticEndpointWithoutStaticSource(t *testing.T) {
	srv, _ := NewServer("127.0.0.1", "0", reportOnlySource{snapshot: &metrics.Snapshot{}}, false)

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /static status = %d, want 503", rec.Code)
	}
}

func TestLocalPageCanBeOverriddenAfterBuild(t *testing.T) {
	dir := t.TempDir()
	assetsDir := filepath.Join(dir, "assets")
	t.Setenv(localPageDirEnv, dir)
	if err := os.Mkdir(assetsDir, 0o755); err != nil {
		t.Fatalf("Mkdir(assets) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("<!doctype html><title>custom</title><p>custom local page</p>"), 0o644); err != nil {
		t.Fatalf("WriteFile(page.html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "custom.txt"), []byte("custom asset"), 0o644); err != nil {
		t.Fatalf("WriteFile(custom.txt) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("private"), 0o644); err != nil {
		t.Fatalf("WriteFile(private.txt) error = %v", err)
	}

	srv, _ := NewServer("127.0.0.1", "0", testSource{snapshot: &metrics.Snapshot{}}, false)

	page := httptest.NewRecorder()
	srv.Handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body.String(), "custom local page") {
		t.Fatal("GET / did not return the external local page")
	}

	asset := httptest.NewRecorder()
	srv.Handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/local-assets/custom.txt", nil))
	if asset.Code != http.StatusOK {
		t.Fatalf("GET /local-assets/custom.txt status = %d, want 200", asset.Code)
	}
	if strings.TrimSpace(asset.Body.String()) != "custom asset" {
		t.Fatal("GET /local-assets/custom.txt did not return the external asset")
	}

	oldAsset := httptest.NewRecorder()
	srv.Handler.ServeHTTP(oldAsset, httptest.NewRequest(http.MethodGet, "/serve-assets/custom.txt", nil))
	if oldAsset.Code != http.StatusNotFound {
		t.Fatalf("GET /serve-assets/custom.txt status = %d, want 404", oldAsset.Code)
	}

	private := httptest.NewRecorder()
	srv.Handler.ServeHTTP(private, httptest.NewRequest(http.MethodGet, "/local-assets/private.txt", nil))
	if private.Code != http.StatusNotFound {
		t.Fatalf("GET /local-assets/private.txt status = %d, want 404", private.Code)
	}

	dirList := httptest.NewRecorder()
	srv.Handler.ServeHTTP(dirList, httptest.NewRequest(http.MethodGet, "/local-assets/", nil))
	if dirList.Code != http.StatusNotFound {
		t.Fatalf("GET /local-assets/ status = %d, want 404", dirList.Code)
	}
}
