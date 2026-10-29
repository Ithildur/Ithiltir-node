//go:build linux

// Package pve collects local QEMU observations from PVE configuration and monitors.
package pve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"Ithiltir-node/internal/virt"
)

// limitedBuffer also bounds child output when pvesh fails or behaves unexpectedly.
type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		return 0, errors.New("pvesh output exceeds limit")
	}
	return b.buffer.Write(p)
}

func (c *collector) metadata(ctx context.Context, host string) error {
	if host == "" || strings.ContainsAny(host, "/\\ \t\r\n") {
		return errors.New("invalid PVE node name")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Boot-disk capacity depends on PVE storage plugins. Keep this low-frequency
	// query separate from live metrics, without QMP or cluster CPU queries.
	raw, err := query(ctx, nil, "/nodes/"+host+"/qemu")
	if err != nil {
		return err
	}
	var machines []struct {
		ID       int    `json:"vmid"`
		Capacity *int64 `json:"maxdisk"`
	}
	if err := json.Unmarshal(raw, &machines); err != nil {
		return err
	}
	if machines == nil {
		return errors.New("PVE metadata is not a list")
	}
	capacities := make(map[int]*int64, len(machines))
	for _, vm := range machines {
		if vm.Capacity != nil && *vm.Capacity >= 0 {
			capacities[vm.ID] = vm.Capacity
		}
	}
	c.mu.Lock()
	c.capacities, c.metadataAt = capacities, time.Now()
	c.mu.Unlock()
	return nil
}

func networkRateLimits(config string) (map[string]float64, error) {
	limits := make(map[string]float64)
	for line := range strings.SplitSeq(config, "\n") {
		line = strings.TrimSpace(line)
		// Pending changes and saved snapshots do not describe the current NICs.
		if strings.HasPrefix(line, "[") {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.HasPrefix(name, "net") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(name, "net"))
		if err != nil || index < 0 || name != fmt.Sprintf("net%d", index) {
			continue
		}
		rate := 0.0
		for property := range strings.SplitSeq(value, ",") {
			key, value, ok := strings.Cut(strings.TrimSpace(property), "=")
			if !ok || key != "rate" {
				continue
			}
			rate, err = strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
				return nil, fmt.Errorf("invalid %s rate %q", name, value)
			}
		}
		// PVE applies rate in MiB/s; Mbps uses decimal megabits per second.
		rate = math.Round(rate*1024*1024*8/1_000_000*100) / 100
		if math.IsInf(rate, 0) {
			return nil, fmt.Errorf("%s rate exceeds numeric range", name)
		}
		limits[name] = rate
	}
	return limits, nil
}

func query(ctx context.Context, lock *os.File, path string, options ...string) ([]byte, error) {
	args := append([]string{"get", path, "--output-format", "json"}, options...)
	cmd := exec.CommandContext(ctx, "/usr/bin/pvesh", args...)
	return runCommand(ctx, cmd, lock)
}

// Wait always reaps the query before its caller releases the VM lock. The
// inherited descriptor also keeps the lock held if the collector is killed.
func runCommand(ctx context.Context, cmd *exec.Cmd, lock *os.File) ([]byte, error) {
	// Linux binds Pdeathsig to the spawning thread, not just the process.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	stdout := &limitedBuffer{limit: virt.MaxBytes}
	stderr := &limitedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("pvesh: %w: %.512s", err, strings.TrimSpace(stderr.buffer.String()))
	}
	return stdout.buffer.Bytes(), nil
}
