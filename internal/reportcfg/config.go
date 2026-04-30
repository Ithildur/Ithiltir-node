package reportcfg

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	Version  = 1
	fileMode = 0o600
	dirMode  = 0o700
)

var ErrNotFound = errors.New("target not found")

type Config struct {
	Version int      `yaml:"version"`
	Targets []Target `yaml:"targets"`
}

type Target struct {
	ID              int    `yaml:"id"`
	URL             string `yaml:"url"`
	Key             string `yaml:"key"`
	ServerInstallID string `yaml:"server_install_id,omitempty"`
}

func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv("ITHILTIR_NODE_REPORT_CONFIG")); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		base := strings.TrimSpace(os.Getenv("ProgramData"))
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "Ithiltir-node", "report.yaml")
	}
	return "/var/lib/ithiltir-node/report.yaml"
}

func Empty() Config {
	return Config{Version: Version}
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Empty(), nil
		}
		return Config{}, fmt.Errorf("open report config: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("stat report config: %w", err)
	}
	if err := checkMode(info); err != nil {
		return Config{}, err
	}

	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse report config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create report config dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".report-*.tmp")
	if err != nil {
		return fmt.Errorf("create report config temp: %w", err)
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod report config temp: %w", err)
	}
	enc := yaml.NewEncoder(tmp)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		_ = enc.Close()
		_ = tmp.Close()
		return fmt.Errorf("encode report config: %w", err)
	}
	if err := enc.Close(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("close report config encoder: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync report config temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close report config temp: %w", err)
	}
	if err := chownLike(path, tmpName); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename report config: %w", err)
	}
	keep = true
	if err := os.Chmod(path, fileMode); err != nil {
		return fmt.Errorf("chmod report config: %w", err)
	}
	return nil
}

func Add(cfg Config, endpoint, key, serverInstallID string) (Config, Target, error) {
	target := Target{
		ID:              nextID(cfg.Targets),
		URL:             strings.TrimSpace(endpoint),
		Key:             strings.TrimSpace(key),
		ServerInstallID: strings.TrimSpace(serverInstallID),
	}
	next := Config{
		Version: cfg.Version,
		Targets: append(append([]Target(nil), cfg.Targets...), target),
	}
	if next.Version == 0 {
		next.Version = Version
	}
	if err := next.Validate(); err != nil {
		return Config{}, Target{}, err
	}
	return next, target, nil
}

func Remove(cfg Config, id int) (Config, error) {
	next := Config{Version: cfg.Version, Targets: make([]Target, 0, len(cfg.Targets))}
	found := false
	for _, target := range cfg.Targets {
		if target.ID == id {
			found = true
			continue
		}
		next.Targets = append(next.Targets, target)
	}
	if !found {
		return Config{}, ErrNotFound
	}
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	return next, nil
}

func Replace(cfg Config, id int, endpoint, key, serverInstallID string) (Config, error) {
	next := Config{Version: cfg.Version, Targets: append([]Target(nil), cfg.Targets...)}
	found := false
	for i := range next.Targets {
		if next.Targets[i].ID != id {
			continue
		}
		next.Targets[i].URL = strings.TrimSpace(endpoint)
		next.Targets[i].Key = strings.TrimSpace(key)
		next.Targets[i].ServerInstallID = strings.TrimSpace(serverInstallID)
		found = true
		break
	}
	if !found {
		return Config{}, ErrNotFound
	}
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	return next, nil
}

func UpdateKey(cfg Config, id int, key string) (Config, error) {
	next := Config{Version: cfg.Version, Targets: append([]Target(nil), cfg.Targets...)}
	found := false
	for i := range next.Targets {
		if next.Targets[i].ID != id {
			continue
		}
		next.Targets[i].Key = strings.TrimSpace(key)
		found = true
		break
	}
	if !found {
		return Config{}, ErrNotFound
	}
	if err := next.Validate(); err != nil {
		return Config{}, err
	}
	return next, nil
}

func (cfg Config) Validate() error {
	if cfg.Version != Version {
		return fmt.Errorf("report config version must be %d", Version)
	}
	seenIDs := make(map[int]struct{}, len(cfg.Targets))
	seenInstallIDs := make(map[string]int, len(cfg.Targets))
	for _, target := range cfg.Targets {
		if target.ID <= 0 {
			return fmt.Errorf("report target id must be positive")
		}
		if _, ok := seenIDs[target.ID]; ok {
			return fmt.Errorf("report target id %d is duplicated", target.ID)
		}
		seenIDs[target.ID] = struct{}{}
		if err := validateURL(target.URL); err != nil {
			return fmt.Errorf("report target %d url: %w", target.ID, err)
		}
		if strings.TrimSpace(target.Key) == "" {
			return fmt.Errorf("report target %d key cannot be empty", target.ID)
		}
		if err := validateServerInstallID(target.ServerInstallID); err != nil {
			return fmt.Errorf("report target %d server_install_id: %w", target.ID, err)
		}
		installID := strings.TrimSpace(target.ServerInstallID)
		if installID == "" {
			continue
		}
		if existingID, ok := seenInstallIDs[installID]; ok {
			return fmt.Errorf("report target server_install_id %s is duplicated by ids %d and %d", installID, existingID, target.ID)
		}
		seenInstallIDs[installID] = target.ID
	}
	return nil
}

func nextID(targets []Target) int {
	maxID := 0
	for _, target := range targets {
		if target.ID > maxID {
			maxID = target.ID
		}
	}
	return maxID + 1
}

func validateURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("cannot be empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("host is required")
	}
	return nil
}

func validateServerInstallID(id string) error {
	if id == "" {
		return nil
	}
	_, err := NormalizeServerInstallID(id)
	return err
}

func NormalizeServerInstallID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("cannot be empty")
	}
	return id, nil
}
