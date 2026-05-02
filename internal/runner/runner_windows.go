//go:build windows

package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"Ithiltir-node/internal/selfupdate"

	"golang.org/x/sys/windows/svc"
)

const serviceName = "ithiltir-node"

type manifest struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type service struct {
	args []string
}

func Run(args []string) int {
	if len(args) == 0 {
		args = []string{"push"}
	}

	inService, err := svc.IsWindowsService()
	if err == nil && inService {
		if err := svc.Run(serviceName, service{args: args}); err != nil {
			log.Printf("runner service failed: %v", err)
			return 1
		}
		return 0
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if err := loop(ctx, args); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("runner failed: %v", err)
		return 1
	}
	return 0
}

func (s service) Execute(_ []string, changes <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	errCh := make(chan error, 1)
	status <- svc.Status{State: svc.StartPending}
	go func() {
		errCh <- loop(ctx, s.args)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case change := <-changes:
			switch change.Cmd {
			case svc.Interrogate:
				status <- change.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				stop()
				<-errCh
				return false, 0
			default:
			}
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("runner loop stopped: %v", err)
				return false, 1
			}
			return false, 0
		}
	}
}

func loop(ctx context.Context, args []string) error {
	backoff := time.Second
	for {
		if err := applyStaged(); err != nil {
			log.Printf("apply staged update failed: %v", err)
		}

		err := runNode(ctx, args)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			log.Printf("node exited: %v", err)
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func runNode(ctx context.Context, args []string) error {
	bin := selfupdate.NodePath()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = selfupdate.DataDir()
	cmd.Env = append(os.Environ(), selfupdate.RunnerEnv+"=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func applyStaged() error {
	manifestPath := selfupdate.StagedManifestPath()
	stagedExe := selfupdate.StagedNodePath()

	m, err := readManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		cleanupStaged()
		return err
	}
	if _, err := os.Stat(stagedExe); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cleanupStaged()
		}
		return err
	}
	if err := verify(stagedExe, m.SHA256); err != nil {
		cleanupStaged()
		return err
	}

	bin := selfupdate.NodePath()
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return fmt.Errorf("create node bin dir: %w", err)
	}
	backup := bin + ".old"
	_ = os.Remove(backup)
	if _, err := os.Stat(bin); err == nil {
		if err := os.Rename(bin, backup); err != nil {
			return fmt.Errorf("backup old node: %w", err)
		}
	}
	if err := os.Rename(stagedExe, bin); err != nil {
		_ = os.Rename(backup, bin)
		return fmt.Errorf("replace node: %w", err)
	}
	_ = os.Remove(backup)
	_ = os.Remove(manifestPath)
	log.Printf("node update applied: version=%s", m.Version)
	return nil
}

func cleanupStaged() {
	_ = os.Remove(selfupdate.StagedManifestPath())
	_ = os.Remove(selfupdate.StagedNodePath())
}

func readManifest(path string) (manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()

	var m manifest
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return manifest{}, fmt.Errorf("read staged manifest: %w", err)
	}
	if strings.TrimSpace(m.SHA256) == "" {
		return manifest{}, errors.New("staged manifest sha256 is empty")
	}
	return m, nil
}

func verify(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash staged node: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(want)) {
		return fmt.Errorf("staged node sha256 mismatch: got %s", got)
	}
	return nil
}
