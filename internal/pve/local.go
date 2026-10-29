//go:build linux

package pve

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"

	"Ithiltir-node/internal/virt"
)

type vmConfig struct {
	vm    virt.VM
	agent string
	nics  []string
}

func hostCPUCount(ctx context.Context) (int64, error) {
	count, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		return 0, fmt.Errorf("host CPU count: %w", err)
	}
	if count < 1 {
		return 0, errors.New("host CPU count is zero")
	}
	return int64(count), nil
}

func readConfig(id int, hostCPUs int64) (vmConfig, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/etc/pve/qemu-server/%d.conf", id))
	if err != nil {
		return vmConfig{}, err
	}
	return parseConfig(id, string(raw), hostCPUs)
}

func parseConfig(id int, raw string, hostCPUs int64) (vmConfig, error) {
	fields := make(map[string]string)
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	vm := virt.VM{ID: id, Name: cmp.Or(fields["name"], fmt.Sprintf("VM %d", id)), Status: "stopped", Template: fields["template"] == "1"}
	number := func(key string, fallback int64) (int64, error) {
		value := fields[key]
		if value == "" {
			return fallback, nil
		}
		if key == "memory" {
			value = strings.TrimPrefix(value, "current=")
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n <= 0 || n > math.MaxInt32 {
			return 0, fmt.Errorf("invalid VM %d %s: %q", id, key, value)
		}
		return n, nil
	}
	cores, err := number("cores", 1)
	if err != nil {
		return vmConfig{}, err
	}
	sockets, err := number("sockets", 1)
	if err != nil {
		return vmConfig{}, err
	}
	cpus, err := number("vcpus", min(cores*sockets, hostCPUs))
	if err != nil {
		return vmConfig{}, err
	}
	memory, err := number("memory", 512)
	if err != nil {
		return vmConfig{}, err
	}
	vm.CPUs, vm.MemoryLimit = &cpus, new(memory*1024*1024)
	vm.NetRateLimits, err = networkRateLimits(raw)
	if err != nil {
		log.Printf("PVE VM %d network rate limits: %v", id, err)
	}
	var nics []string
	for key := range fields {
		index, err := strconv.Atoi(strings.TrimPrefix(key, "net"))
		if err == nil && index >= 0 && key == fmt.Sprintf("net%d", index) {
			nics = append(nics, fmt.Sprintf("tap%di%d", id, index))
		}
	}
	agent := fields["agent"]
	enabled := false
	for property := range strings.SplitSeq(agent, ",") {
		key, value, ok := strings.Cut(property, "=")
		if !ok {
			key, value = "enabled", property
		}
		if key == "enabled" {
			enabled = value == "1" || value == "yes" || value == "on" || value == "true"
		}
	}
	if !enabled {
		agent = ""
	}
	return vmConfig{vm: vm, agent: agent, nics: nics}, nil
}

type processSample struct {
	pid          int
	start, ticks uint64
	at           time.Time
}

func readProcess(id int) (processSample, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/run/qemu-server/%d.pid", id))
	if err != nil {
		return processSample{}, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return processSample{}, fmt.Errorf("invalid VM %d PID", id)
	}
	// A stale pidfile must not turn an unrelated process into a running VM.
	raw, err = os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return processSample{}, err
	}
	args := bytes.Split(raw, []byte{0})
	found := false
	for i := 0; i+1 < len(args); i++ {
		if string(args[i]) == "-id" && string(args[i+1]) == strconv.Itoa(id) {
			found = true
			break
		}
	}
	if !found {
		return processSample{}, os.ErrNotExist
	}
	raw, err = os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return processSample{}, err
	}
	sample, err := parseProcess(raw)
	sample.pid, sample.at = pid, time.Now()
	return sample, err
}

func parseProcess(raw []byte) (processSample, error) {
	// comm may contain spaces and parentheses; fields after its final ')' are fixed.
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		return processSample{}, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return processSample{}, errors.New("short process stat")
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return processSample{}, os.ErrNotExist
	}
	user, e1 := strconv.ParseUint(fields[11], 10, 64)
	system, e2 := strconv.ParseUint(fields[12], 10, 64)
	start, e3 := strconv.ParseUint(fields[19], 10, 64)
	if err := errors.Join(e1, e2, e3); err != nil {
		return processSample{}, err
	}
	return processSample{start: start, ticks: user + system}, nil
}

// Linux exports /proc process times in USER_HZ (100 on amd64 and arm64),
// independently of the kernel scheduler's CONFIG_HZ.
const clockTicks = 100

func cpuRatio(before, after processSample, cpus int64) *float64 {
	if before.pid != after.pid || before.start != after.start || before.at.IsZero() || !after.at.After(before.at) || after.ticks < before.ticks {
		return nil
	}
	return new(float64(after.ticks-before.ticks) / clockTicks / after.at.Sub(before.at).Seconds() / float64(cpus))
}

func memoryUsage(id int) (int64, error) {
	dir := fmt.Sprintf("/sys/fs/cgroup/qemu.slice/%d.scope", id)
	usage, cache := "memory.current", "file"
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); errors.Is(err, os.ErrNotExist) {
		dir = fmt.Sprintf("/sys/fs/cgroup/memory/qemu.slice/%d.scope", id)
		usage, cache = "memory.usage_in_bytes", "total_cache"
	}
	raw, err := os.ReadFile(filepath.Join(dir, usage))
	if err != nil {
		return 0, err
	}
	total, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, err
	}
	raw, err = os.ReadFile(filepath.Join(dir, "memory.stat"))
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != cache {
			continue
		}
		cached, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		// Match PVE: exclude file cache, rather than using process RSS.
		return max(0, total-cached), nil
	}
	return 0, errors.New("missing cgroup memory cache counter")
}

func inventory(ctx context.Context) ([]vmConfig, error) {
	hostCPUs, err := hostCPUCount(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/etc/pve/qemu-server")
	if err != nil {
		return nil, err
	}
	configs := make([]vmConfig, 0, len(entries))
	for _, entry := range entries {
		idText, ok := strings.CutSuffix(entry.Name(), ".conf")
		id, err := strconv.Atoi(idText)
		if !ok || err != nil || id <= 0 {
			continue
		}
		config, err := readConfig(id, hostCPUs)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("VM %d config: %w", id, err)
		}
		if !config.vm.Template {
			configs = append(configs, config)
		}
	}
	slices.SortFunc(configs, func(a, b vmConfig) int { return cmp.Compare(a.vm.ID, b.vm.ID) })
	return configs, nil
}

func sampleVM(ctx context.Context, config vmConfig, previous processSample) (virt.VM, processSample) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	vm := config.vm
	vm.Status = "unknown"
	vm.TTLSeconds = 90
	before, err := readProcess(vm.ID)
	if errors.Is(err, os.ErrNotExist) {
		vm.Status, vm.QMPStatus = "stopped", "stopped"
		vm.CollectedAt = new(time.Now().UTC())
		vm.Uptime = new(int64(0))
		return vm, processSample{}
	}
	if err != nil {
		log.Printf("PVE VM %d process: %v", vm.ID, err)
		vm.CollectedAt = new(time.Now().UTC())
		return vm, processSample{}
	}
	vm.Status = "running"
	if err := collectMonitor(ctx, &vm); err != nil {
		log.Printf("PVE VM %d monitor: %v", vm.ID, err)
	}
	after, err := readProcess(vm.ID)
	if err != nil || before.pid != after.pid || before.start != after.start {
		vm = config.vm
		vm.Status = "unknown"
		vm.CollectedAt, vm.TTLSeconds = new(time.Now().UTC()), 90
		return vm, processSample{}
	}
	if vm.QMPStatus == "running" {
		vm.CPU = cpuRatio(previous, after, *vm.CPUs)
		raw, err := os.ReadFile("/proc/uptime")
		if err == nil {
			value, _, _ := strings.Cut(string(raw), " ")
			uptime, parseErr := strconv.ParseFloat(value, 64)
			if parseErr == nil {
				vm.Uptime = new(max(int64(0), int64(uptime-float64(after.start)/clockTicks)))
			} else {
				err = parseErr
			}
		}
		if err != nil {
			log.Printf("PVE VM %d uptime: %v", vm.ID, err)
		}
		var received, transmitted int64
		err = nil
		for _, nic := range config.nics {
			var rx, tx int64
			rx, err = interfaceBytes(nic, "rx")
			if err != nil {
				break
			}
			tx, err = interfaceBytes(nic, "tx")
			if err != nil {
				break
			}
			// tap receive/transmit are reversed from the guest's perspective.
			received += tx
			transmitted += rx
		}
		if err == nil {
			vm.NetIn, vm.NetOut = &received, &transmitted
		} else {
			log.Printf("PVE VM %d network: %v", vm.ID, err)
		}
	}
	vm.CollectedAt = new(time.Now().UTC())
	return vm, after
}

func interfaceBytes(name, direction string) (int64, error) {
	raw, err := os.ReadFile(filepath.Join("/sys/class/net", name, "statistics", direction+"_bytes"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}
