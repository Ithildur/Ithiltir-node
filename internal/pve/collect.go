//go:build linux

// Package pve collects local QEMU VM observations through the read-only PVE API.
package pve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"Ithiltir-node/internal/virt"
)

type machine struct {
	ID           int      `json:"vmid"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	QMPStatus    string   `json:"qmpstatus"`
	Template     int      `json:"template"`
	CPUs         *int64   `json:"cpus"`
	CPU          *float64 `json:"cpu"`
	Memory       *int64   `json:"mem"`
	MemoryLimit  *int64   `json:"maxmem"`
	DiskCapacity *int64   `json:"maxdisk"`
	DiskRead     *int64   `json:"diskread"`
	DiskWrite    *int64   `json:"diskwrite"`
	NetIn        *int64   `json:"netin"`
	NetOut       *int64   `json:"netout"`
	Uptime       *int64   `json:"uptime"`
}

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

func Collect(ctx context.Context, host string) ([]virt.VM, error) {
	if host == "" || strings.ContainsAny(host, "/\\ \t\r\n") {
		return nil, errors.New("invalid PVE node name")
	}
	raw, err := query(ctx, "/nodes/"+host+"/qemu", "--full", "1")
	if err != nil {
		return nil, err
	}
	vms, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if len(vms) == 0 {
		return vms, nil
	}
	// A one-shot pvesh process has no prior CPU counters. Use the PVE
	// statistics daemon's resource observations instead of reporting fake zeros.
	raw, err = query(ctx, "/cluster/resources", "--type", "vm")
	if err != nil {
		return nil, err
	}
	if err := applyCPU(vms, raw, host); err != nil {
		return nil, err
	}
	return vms, nil
}

func query(ctx context.Context, path string, options ...string) ([]byte, error) {
	return queryLocked(ctx, nil, path, options...)
}

func queryLocked(ctx context.Context, lock *os.File, path string, options ...string) ([]byte, error) {
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

func applyCPU(vms []virt.VM, raw []byte, host string) error {
	var resources []struct {
		ID     int      `json:"vmid"`
		Node   string   `json:"node"`
		Type   string   `json:"type"`
		Status string   `json:"status"`
		CPU    *float64 `json:"cpu"`
	}
	if err := json.Unmarshal(raw, &resources); err != nil {
		return fmt.Errorf("decode PVE resources: %w", err)
	}
	if resources == nil {
		return errors.New("PVE resources is not a list")
	}
	lookup := make(map[int]*float64)
	for _, resource := range resources {
		if resource.Node == host && resource.Type == "qemu" && resource.Status == "running" {
			lookup[resource.ID] = resource.CPU
		}
	}
	for i := range vms {
		if vms[i].Status == "running" {
			vms[i].CPU = lookup[vms[i].ID]
		}
	}
	return nil
}

func decode(raw []byte) ([]virt.VM, error) {
	var machines []machine
	if err := json.Unmarshal(raw, &machines); err != nil {
		return nil, fmt.Errorf("decode pvesh: %w", err)
	}
	if machines == nil {
		return nil, errors.New("pvesh did not return a VM list")
	}
	if len(machines) > 4096 {
		return nil, errors.New("too many VMs")
	}
	vms := make([]virt.VM, 0, len(machines))
	for _, vm := range machines {
		vms = append(vms, virt.VM{
			ID: vm.ID, Name: vm.Name, Status: vm.Status, QMPStatus: vm.QMPStatus,
			Template: vm.Template != 0, CPUs: vm.CPUs, CPU: vm.CPU,
			Memory: vm.Memory, MemoryLimit: vm.MemoryLimit, DiskCapacity: vm.DiskCapacity,
			DiskRead: vm.DiskRead, DiskWrite: vm.DiskWrite,
			NetIn: vm.NetIn, NetOut: vm.NetOut, Uptime: vm.Uptime,
		})
	}
	slices.SortFunc(vms, func(a, b virt.VM) int { return a.ID - b.ID })
	return vms, nil
}

// Write publishes only complete files in a root-owned directory. Old samples
// remain available to readers until rename completes.
func Write(path string, snapshot virt.Snapshot, gid int) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(raw) > virt.MaxBytes {
		return errors.New("virtualization cache exceeds limit")
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".pve-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chown(0, gid); err != nil {
		return err
	}
	if err := f.Chmod(0640); err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
