package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestApplyReportsWhenDisabled(t *testing.T) {
	t.Setenv(RunnerEnv, "")
	if Enabled() {
		t.Skip("self update is enabled for this test binary")
	}

	if err := Apply(context.Background(), Manifest{}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Apply() error = %v, want ErrDisabled", err)
	}
}

func TestValidateRejectsReleasePathMeta(t *testing.T) {
	for _, version := range []string{".", ".."} {
		t.Run(version, func(t *testing.T) {
			m := Manifest{
				Version: version,
				URL:     "https://example.test/node",
				SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
				Size:    1,
			}
			if err := validate(m); err == nil {
				t.Fatal("validate() error = nil, want invalid version")
			}
		})
	}
}

func TestStageWindowsWritesStagedFiles(t *testing.T) {
	body := []byte("node binary")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(nodeSecretHeader); got != "node-secret" {
			t.Errorf("%s = %q, want node-secret", nodeSecretHeader, got)
		}
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
		Secret:  "node-secret",
	}
	if err := stageWindows(t.Context(), home, m); err != nil {
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
	m.Secret = ""
	if got != m {
		t.Fatalf("staged manifest = %+v, want %+v", got, m)
	}
	if strings.Contains(string(gotManifest), "node-secret") {
		t.Fatal("staged manifest contains download secret")
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
	entries, err := os.ReadDir(stagingDir(home))
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging directory after failed download = %v, %v, want empty", entries, err)
	}
}

func TestApplyUnixSwitchesCurrentRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix symlink update")
	}

	body := []byte("new node binary")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	home := t.TempDir()
	oldDir := releaseDir(home, "1.0.0")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("create old release dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, nodeName), []byte("old"), 0o755); err != nil {
		t.Fatalf("write old release: %v", err)
	}
	if err := os.Symlink(oldDir, currentDir(home)); err != nil {
		t.Fatalf("create current symlink: %v", err)
	}

	m := Manifest{
		ID:      "release-2",
		Version: "1.2.3",
		URL:     srv.URL,
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(body)),
	}
	for _, failure := range []struct {
		name      string
		size      int64
		hash      string
		wantError string
	}{
		{"oversized", m.Size - 1, m.SHA256, "update size exceeds manifest"},
		{"truncated", m.Size + 1, m.SHA256, "update size mismatch"},
		{"checksum", m.Size, strings.Repeat("0", 64), "update sha256 mismatch"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			invalid := m
			invalid.Size, invalid.SHA256 = failure.size, failure.hash
			if err := applyUnix(t.Context(), home, invalid); err == nil || !strings.Contains(err.Error(), failure.wantError) {
				t.Fatalf("applyUnix() error = %v, want %s", err, failure.wantError)
			}
			current, err := os.Readlink(currentDir(home))
			if err != nil || current != oldDir {
				t.Fatalf("current after failed update = %q, %v, want %q", current, err, oldDir)
			}
			entries, err := os.ReadDir(releaseDir(home, m.Version))
			if err != nil || len(entries) != 0 {
				t.Fatalf("release directory after failed update = %v, %v, want empty", entries, err)
			}
		})
	}
	err := applyUnix(t.Context(), home, m)
	if !errors.Is(err, ErrRestart) {
		t.Fatalf("applyUnix() error = %v, want ErrRestart", err)
	}

	current, err := os.Readlink(currentDir(home))
	if err != nil {
		t.Fatalf("read current symlink: %v", err)
	}
	if current != releaseDir(home, m.Version) {
		t.Fatalf("current = %q, want %q", current, releaseDir(home, m.Version))
	}

	got, err := os.ReadFile(releaseNodePath(home, m.Version))
	if err != nil {
		t.Fatalf("read release binary: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("release binary = %q, want %q", got, body)
	}
	info, err := os.Stat(releaseNodePath(home, m.Version))
	if err != nil {
		t.Fatalf("stat release binary: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("release binary mode = %v, want executable", info.Mode())
	}
}
