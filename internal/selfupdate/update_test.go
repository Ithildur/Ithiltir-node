package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestApplySkipsWithoutRunnerEnv(t *testing.T) {
	t.Setenv(RunnerEnv, "")

	if err := Apply(context.Background(), Manifest{}); err != nil {
		t.Fatalf("Apply() error = %v, want nil", err)
	}
}

func TestStageWindowsWritesStagedFiles(t *testing.T) {
	body := []byte("node binary")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	home := t.TempDir()
	m := Manifest{
		ID:      "release-1",
		Version: "1.2.3",
		URL:     srv.URL,
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	}
	if err := stageWindows(context.Background(), home, m); err != nil {
		t.Fatalf("stageWindows() error = %v", err)
	}

	gotBody, err := os.ReadFile(stagedNodePath(home))
	if err != nil {
		t.Fatalf("read staged node: %v", err)
	}
	if string(gotBody) != string(body) {
		t.Fatalf("staged node = %q, want %q", gotBody, body)
	}
	gotManifest, err := os.ReadFile(stagedManifestPath(home))
	if err != nil {
		t.Fatalf("read staged manifest: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(gotManifest, &got); err != nil {
		t.Fatalf("decode staged manifest: %v", err)
	}
	if got != m {
		t.Fatalf("staged manifest = %+v, want %+v", got, m)
	}
}

func TestStageWindowsClearsOldStagingBeforeDownload(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(stagingDir(home), 0o700); err != nil {
		t.Fatalf("create staging dir: %v", err)
	}
	if err := os.WriteFile(stagedNodePath(home), []byte("old"), 0o644); err != nil {
		t.Fatalf("write old staged node: %v", err)
	}
	if err := os.WriteFile(stagedManifestPath(home), []byte(`{"version":"old"}`), 0o600); err != nil {
		t.Fatalf("write old staged manifest: %v", err)
	}

	body := []byte("short")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	err := stageWindows(context.Background(), home, Manifest{
		Version: "1.2.3",
		URL:     srv.URL,
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body) + 1),
	})
	if err == nil {
		t.Fatal("stageWindows() error = nil, want size mismatch")
	}
	if _, err := os.Stat(stagedNodePath(home)); !os.IsNotExist(err) {
		t.Fatalf("staged node still exists after failed stage: %v", err)
	}
	if _, err := os.Stat(stagedManifestPath(home)); !os.IsNotExist(err) {
		t.Fatalf("staged manifest still exists after failed stage: %v", err)
	}
}
