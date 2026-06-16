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

func TestDownloadSendsNodeSecret(t *testing.T) {
	body := []byte("node binary")
	sum := sha256.Sum256(body)
	gotSecret := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotSecret <- r.Header.Get(nodeSecretHeader):
		default:
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "node")
	err := download(context.Background(), Manifest{
		URL:    srv.URL,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(body)),
		Secret: "node-secret",
	}, path, 0o755)
	if err != nil {
		t.Fatalf("download() error = %v", err)
	}

	select {
	case got := <-gotSecret:
		if got != "node-secret" {
			t.Fatalf("%s = %q, want node-secret", nodeSecretHeader, got)
		}
	default:
		t.Fatalf("server did not receive %s", nodeSecretHeader)
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
	err := applyUnix(context.Background(), home, m)
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
