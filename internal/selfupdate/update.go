package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	ErrRestart  = errors.New("restart for update")
	ErrDisabled = errors.New("self update is disabled")
)

const (
	RunnerEnv        = "ITHILTIR_NODE_RUNNER"
	nodeName         = "ithiltir-node"
	nodeSecretHeader = "X-Node-Secret"
)

type Manifest struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	// Secret is the current target key used only for authenticated asset downloads.
	Secret string `json:"-"`
}

func Apply(ctx context.Context, m Manifest) error {
	if !Enabled() {
		return ErrDisabled
	}
	if err := validate(m); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "windows":
		if err := stageWindows(ctx, DataDir(), m); err != nil {
			return err
		}
		return ErrRestart
	case "linux", "darwin":
		return applyUnix(ctx, DataDir(), m)
	default:
		return fmt.Errorf("self update is not supported on %s", runtime.GOOS)
	}
}

func Enabled() bool {
	switch runtime.GOOS {
	case "windows":
		return os.Getenv(RunnerEnv) == "1"
	case "linux", "darwin":
		return installedUnix(DataDir())
	default:
		return false
	}
}

func DataDir() string {
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("ProgramData"))
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Ithiltir-node")
	}
	return "/var/lib/ithiltir-node"
}

func NodePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(DataDir(), "bin", "ithiltir-node.exe")
	}
	return currentNodePath(DataDir())
}

func StagedNodePath() string {
	return stagedNodePath(DataDir())
}

func StagedManifestPath() string {
	return stagedManifestPath(DataDir())
}

func stagingDir(home string) string {
	return filepath.Join(home, "staging")
}

func stagedNodePath(home string) string {
	return filepath.Join(stagingDir(home), "ithiltir-node.exe.new")
}

func stagedManifestPath(home string) string {
	return filepath.Join(stagingDir(home), "manifest.json")
}

func releasesDir(home string) string {
	return filepath.Join(home, "releases")
}

func currentDir(home string) string {
	return filepath.Join(home, "current")
}

func currentNodePath(home string) string {
	return filepath.Join(currentDir(home), nodeName)
}

func releaseDir(home, version string) string {
	return filepath.Join(releasesDir(home), version)
}

func releaseNodePath(home, version string) string {
	return filepath.Join(releaseDir(home, version), nodeName)
}

func validate(m Manifest) error {
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("update version is empty")
	}
	if m.Version == "." || m.Version == ".." {
		return fmt.Errorf("update version is invalid: %q", m.Version)
	}
	if strings.ContainsAny(m.Version, `/\`) {
		return fmt.Errorf("update version contains path separator: %q", m.Version)
	}
	if strings.TrimSpace(m.SHA256) == "" {
		return errors.New("update sha256 is empty")
	}
	if len(strings.TrimSpace(m.SHA256)) != sha256.Size*2 {
		return fmt.Errorf("update sha256 has invalid length")
	}
	if m.Size <= 0 {
		return errors.New("update size must be positive")
	}
	if m.Size+1 <= m.Size {
		return errors.New("update size is too large")
	}
	u, err := url.Parse(strings.TrimSpace(m.URL))
	if err != nil {
		return fmt.Errorf("parse update url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("update url scheme must be http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("update url host is empty")
	}
	return nil
}

func stageWindows(ctx context.Context, home string, m Manifest) error {
	staging := stagingDir(home)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	stagedExe := stagedNodePath(home)
	manifestPath := stagedManifestPath(home)
	_ = os.Remove(manifestPath)
	_ = os.Remove(stagedExe)

	tmp, err := os.CreateTemp(staging, "ithiltir-node-*.new")
	if err != nil {
		return fmt.Errorf("create update temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close update temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	if err := download(ctx, m, tmpPath, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, stagedExe); err != nil {
		return fmt.Errorf("stage update file: %w", err)
	}

	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode update manifest: %w", err)
	}
	if err := writeFileAtomic(staging, manifestPath, body, 0o600); err != nil {
		return fmt.Errorf("write staged manifest: %w", err)
	}
	return nil
}

func applyUnix(ctx context.Context, home string, m Manifest) error {
	if version, ok := activeUnixVersion(home); ok && version == m.Version {
		return nil
	}

	targetDir := releaseDir(home, m.Version)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create release dir: %w", err)
	}

	tmp, err := os.CreateTemp(targetDir, ".ithiltir-node-*.new")
	if err != nil {
		return fmt.Errorf("create update temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close update temp file: %w", err)
	}
	defer os.Remove(tmpPath)

	if err := download(ctx, m, tmpPath, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, releaseNodePath(home, m.Version)); err != nil {
		return fmt.Errorf("install release binary: %w", err)
	}
	if err := switchCurrent(home, targetDir); err != nil {
		return err
	}
	return ErrRestart
}

func installedUnix(home string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return unixManagedPath(home, exe)
}

func activeUnixVersion(home string) (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	cleanPath, err := filepath.Abs(exe)
	if err != nil {
		return "", false
	}
	if cleanPath == currentNodePath(home) {
		return currentUnixVersion(home)
	}
	return unixReleaseVersion(home, exe)
}

func currentUnixVersion(home string) (string, bool) {
	link, err := os.Readlink(currentDir(home))
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(home, link)
	}
	return unixReleaseVersion(home, filepath.Join(link, nodeName))
}

func unixManagedPath(home, path string) bool {
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if cleanPath == currentNodePath(home) {
		_, ok := currentUnixVersion(home)
		return ok
	}
	_, ok := unixReleaseVersion(home, cleanPath)
	return ok
}

func unixReleaseVersion(home, path string) (string, bool) {
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if filepath.Base(cleanPath) != nodeName {
		return "", false
	}
	rel, err := filepath.Rel(releasesDir(home), cleanPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	dir, file := filepath.Split(rel)
	if file != nodeName {
		return "", false
	}
	version := strings.TrimSuffix(dir, string(os.PathSeparator))
	if version == "" || strings.ContainsRune(version, os.PathSeparator) {
		return "", false
	}
	return version, true
}

func switchCurrent(home, targetDir string) error {
	tmp := filepath.Join(home, fmt.Sprintf(".current-%d.tmp", os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink(targetDir, tmp); err != nil {
		return fmt.Errorf("stage current symlink: %w", err)
	}
	if err := os.Rename(tmp, currentDir(home)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("switch current symlink: %w", err)
	}
	return nil
}

func writeFileAtomic(dir, path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func download(ctx context.Context, m Manifest, path string, mode os.FileMode) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(m.URL), nil)
	if err != nil {
		return fmt.Errorf("build update request: %w", err)
	}
	if secret := strings.TrimSpace(m.Secret); secret != "" {
		req.Header.Set(nodeSecretHeader, secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download update non-200 status: %s", resp.Status)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create update temp file: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("chmod update temp file: %w", err)
	}

	h := sha256.New()
	w := io.MultiWriter(f, h)
	n, copyErr := io.Copy(w, io.LimitReader(resp.Body, m.Size+1))
	if copyErr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write update temp file: %w", copyErr)
	}
	if n > m.Size {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("update size exceeds manifest: got more than %d", m.Size)
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("sync update temp file: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close update temp file: %w", closeErr)
	}
	if n != m.Size {
		_ = os.Remove(path)
		return fmt.Errorf("update size mismatch: got %d want %d", n, m.Size)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(m.SHA256)) {
		_ = os.Remove(path)
		return fmt.Errorf("update sha256 mismatch: got %s", got)
	}
	return nil
}
