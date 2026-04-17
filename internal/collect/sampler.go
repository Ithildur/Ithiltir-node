package collect

import (
	"context"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"Ithiltir-node/internal/metrics"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

type slowCache struct {
	filesystems []metrics.DiskUsage
	storages    []metrics.StorageUsage
	raid        raidSnapshot
	aliases     mapperAliases
}

type sysInfo struct {
	hostname        string
	os              string
	platform        string
	platformVersion string
	kernelVersion   string
	arch            string
}

type Sampler struct {
	cfg Config

	fastInterval   time.Duration
	mediumInterval time.Duration
	slowInterval   time.Duration
	slowOffset     time.Duration
	pushDelay      time.Duration

	io    ioSampler
	zfsIO *zfsIOSampler
	usage *fsUsageReader

	mu       sync.RWMutex
	latest   *metrics.Snapshot
	latestTS time.Time
	slow     slowCache

	cpuInfo metrics.StaticCPUInfo
	sys     sysInfo
	version string

	procCount int

	runMu     sync.Mutex
	runState  samplerRunState
	runCancel context.CancelFunc
}

type samplerRunState uint8

const (
	samplerStateCreated samplerRunState = iota
	samplerStateRunning
	samplerStateStopped
)

func readSystem() sysInfo {
	sys := sysInfo{}
	if hi, err := host.Info(); err == nil && hi != nil {
		sys.hostname = strings.TrimSpace(hi.Hostname)
		sys.os = strings.TrimSpace(hi.OS)
		sys.platform = strings.TrimSpace(hi.Platform)
		sys.platformVersion = strings.TrimSpace(hi.PlatformVersion)
		sys.kernelVersion = strings.TrimSpace(hi.KernelVersion)
	}
	if sys.hostname == "" {
		if hostname, err := os.Hostname(); err == nil {
			sys.hostname = strings.TrimSpace(hostname)
		}
	}
	sys.arch = runtime.GOARCH
	return sys
}

func normalizeCPUInfo(info metrics.StaticCPUInfo) metrics.StaticCPUInfo {
	if info.Sockets <= 0 && (info.CoresLogical > 0 || info.CoresPhysical > 0) {
		info.Sockets = 1
	}
	return info
}

func readCPUInfo() metrics.StaticCPUInfo {
	info := metrics.StaticCPUInfo{}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		first := infos[0]
		info.ModelName = strings.TrimSpace(first.ModelName)
		info.VendorID = strings.TrimSpace(first.VendorID)
		info.FrequencyMhz = first.Mhz
		info.Sockets = countCPUSockets(infos)
	}
	if physical, err := cpu.Counts(false); err == nil && physical > 0 {
		info.CoresPhysical = physical
	}
	if logical, err := cpu.Counts(true); err == nil && logical > 0 {
		info.CoresLogical = logical
	}
	return normalizeCPUInfo(info)
}

func mergeSystem(dst *sysInfo, src sysInfo) {
	if src.hostname != "" {
		dst.hostname = src.hostname
	}
	if src.os != "" {
		dst.os = src.os
	}
	if src.platform != "" {
		dst.platform = src.platform
	}
	if src.platformVersion != "" {
		dst.platformVersion = src.platformVersion
	}
	if src.kernelVersion != "" {
		dst.kernelVersion = src.kernelVersion
	}
	if src.arch != "" {
		dst.arch = src.arch
	}
}

func mergeCPU(dst *metrics.StaticCPUInfo, src metrics.StaticCPUInfo) {
	if src.ModelName != "" {
		dst.ModelName = src.ModelName
	}
	if src.VendorID != "" {
		dst.VendorID = src.VendorID
	}
	if src.Sockets > 0 {
		dst.Sockets = src.Sockets
	}
	if src.CoresPhysical > 0 {
		dst.CoresPhysical = src.CoresPhysical
	}
	if src.CoresLogical > 0 {
		dst.CoresLogical = src.CoresLogical
	}
	if src.FrequencyMhz > 0 {
		dst.FrequencyMhz = src.FrequencyMhz
	}
	*dst = normalizeCPUInfo(*dst)
}

func reportSecs(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	secs := int(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}
	if secs <= 0 {
		return 1
	}
	return secs
}

func (s *Sampler) refreshStatic() {
	sys := readSystem()
	cpuInfo := readCPUInfo()

	s.mu.Lock()
	mergeSystem(&s.sys, sys)
	mergeCPU(&s.cpuInfo, cpuInfo)
	s.mu.Unlock()
}

func countCPUSockets(infos []cpu.InfoStat) int {
	ids := make(map[string]struct{})
	for _, info := range infos {
		if id := strings.TrimSpace(info.PhysicalID); id != "" {
			ids[id] = struct{}{}
		}
	}
	if len(ids) > 0 {
		return len(ids)
	}
	if len(infos) > 0 {
		return 1
	}
	return 0
}

func procCountSample() int {
	pids, err := process.Pids()
	if err != nil {
		return 0
	}
	return len(pids)
}

func NewSampler(fastInterval time.Duration, slowOffset time.Duration, pushDelay time.Duration, cfg Config, version string) *Sampler {
	if fastInterval <= 0 {
		fastInterval = time.Second
	}
	mediumInterval := fastInterval * 8
	slowInterval := minDuration(60*time.Second, fastInterval*20)
	s := &Sampler{
		cfg:            cfg,
		fastInterval:   fastInterval,
		mediumInterval: mediumInterval,
		slowInterval:   slowInterval,
		slowOffset:     slowOffset,
		pushDelay:      pushDelay,
		zfsIO:          newZFSIOSampler(),
		usage:          newFSUsageReader(),
		procCount:      procCountSample(),
		version:        version,
	}

	s.refreshStatic()

	return s
}

func (s *Sampler) Start() {
	s.runMu.Lock()
	if s.runState != samplerStateCreated {
		s.runMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runCancel = cancel
	s.runState = samplerStateRunning
	s.runMu.Unlock()

	s.collectFast()
	s.zfsIO.start(ctx, s.fastInterval, s.hasZFS)

	go func() {
		t := time.NewTicker(s.fastInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.collectFast()
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		if s.slowOffset > 0 {
			select {
			case <-time.After(s.slowOffset):
			case <-ctx.Done():
				return
			}
		}
		s.collectSlow()

		t := time.NewTicker(s.slowInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.collectSlow()
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		t := time.NewTicker(s.mediumInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				s.mu.Lock()
				s.procCount = procCountSample()
				s.mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (s *Sampler) Stop() {
	s.runMu.Lock()
	if s.runState != samplerStateRunning {
		s.runMu.Unlock()
		return
	}
	cancel := s.runCancel
	s.runCancel = nil
	s.runState = samplerStateStopped
	s.runMu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (s *Sampler) Snapshot() *metrics.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest.Clone()
}

func (s *Sampler) Time() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latestTS
}

func (s *Sampler) Hostname() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sys.hostname
}

func (s *Sampler) Static() *metrics.Static {
	s.refreshStatic()

	vm, err := mem.VirtualMemory()
	if err != nil {
		vm = &mem.VirtualMemoryStat{}
	}
	swap, err := mem.SwapMemory()
	if err != nil {
		swap = &mem.SwapMemoryStat{}
	}

	s.mu.RLock()
	cpuInfo := s.cpuInfo
	sys := s.sys
	slowRaid := s.slow.raid
	slowFS := s.slow.filesystems
	slowStor := s.slow.storages
	aliases := s.slow.aliases
	latest := s.latest
	s.mu.RUnlock()

	staticArrays := make([]metrics.StaticRaidArray, 0, len(slowRaid.Arrays))
	for _, a := range slowRaid.Arrays {
		members := make([]metrics.StaticRaidMember, 0, len(a.MemberStates))
		for _, m := range a.MemberStates {
			members = append(members, metrics.StaticRaidMember{Name: m.Name})
		}
		staticArrays = append(staticArrays, metrics.StaticRaidArray{
			Name:    a.Name,
			Level:   a.Level,
			Devices: a.Devices,
			Members: members,
		})
	}

	staticDisk := buildStaticDisk(slowFS, slowStor, slowRaid, latest, aliases)

	return &metrics.Static{
		Version:               s.version,
		Timestamp:             time.Now().UTC(),
		ReportIntervalSeconds: reportSecs(s.fastInterval),
		CPU: metrics.StaticCPU{
			Info: cpuInfo,
		},
		Memory: metrics.StaticMemory{
			Total:     vm.Total,
			SwapTotal: swap.Total,
		},
		Disk: staticDisk,
		System: metrics.StaticSystem{
			Hostname:        sys.hostname,
			OS:              sys.os,
			Platform:        sys.platform,
			PlatformVersion: sys.platformVersion,
			KernelVersion:   sys.kernelVersion,
			Arch:            sys.arch,
		},
		Raid: metrics.StaticRaid{
			Supported: slowRaid.Supported,
			Available: slowRaid.Available,
			Arrays:    staticArrays,
		},
	}
}

func buildStaticDisk(fs []metrics.DiskUsage, storages []metrics.StorageUsage, raid raidSnapshot, latest *metrics.Snapshot, aliases mapperAliases) metrics.StaticDisk {
	logical := buildLogical(storages, raid, fs, aliases)
	filesystems := filterFilesystems(fs, aliases)

	staticLogical := make([]metrics.StaticDiskLogical, 0, len(logical))
	for _, l := range logical {
		staticLogical = append(staticLogical, metrics.StaticDiskLogical{
			Kind:       l.Kind,
			Name:       l.Name,
			DevicePath: l.DevicePath,
			Ref:        l.Ref,
			Total:      l.Total,
			Mountpoint: l.Mountpoint,
			FsType:     pickFsType(l.Mountpoint, l.Mountpoints),
			Devices:    l.Devices,
		})
	}

	staticFS := make([]metrics.StaticDiskFilesystem, 0, len(filesystems))
	for _, u := range filesystems {
		staticFS = append(staticFS, metrics.StaticDiskFilesystem{
			Path:        u.Path,
			Device:      u.Device,
			Mountpoint:  u.Mountpoint,
			Total:       u.Total,
			FsType:      u.FsType,
			InodesTotal: u.InodesTotal,
		})
	}

	staticBaseIO := buildStaticBaseIO(logical, filesystems)
	if staticBaseIO == nil {
		staticBaseIO = []metrics.StaticDiskBaseIO{}
	}

	staticPhysical := []metrics.StaticDiskPhysical(nil)
	if latest != nil {
		staticPhysical = make([]metrics.StaticDiskPhysical, 0, len(latest.Disk.Physical))
		for _, p := range latest.Disk.Physical {
			staticPhysical = append(staticPhysical, metrics.StaticDiskPhysical{
				Name:       p.Name,
				DevicePath: p.DevicePath,
				Ref:        p.Ref,
			})
		}
	}
	if staticPhysical == nil {
		staticPhysical = []metrics.StaticDiskPhysical{}
	}

	return metrics.StaticDisk{
		Physical:    staticPhysical,
		Logical:     staticLogical,
		Filesystems: staticFS,
		BaseIO:      staticBaseIO,
	}
}

func pickFsType(mountpoint string, mountpoints map[string]metrics.DiskMountpoint) string {
	if len(mountpoints) == 0 {
		return ""
	}
	if mountpoint != "" {
		if meta, ok := mountpoints[mountpoint]; ok {
			return meta.FsType
		}
	}
	keys := make([]string, 0, len(mountpoints))
	for k := range mountpoints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return mountpoints[keys[0]].FsType
}

func buildStaticBaseIO(logical []logicalSnapshot, filesystems []metrics.DiskUsage) []metrics.StaticDiskBaseIO {
	candidates := buildBaseIOCandidates(logical)

	if len(candidates) == 0 {
		return nil
	}

	sortBaseIO(candidates, logical, filesystems)

	out := make([]metrics.StaticDiskBaseIO, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, metrics.StaticDiskBaseIO{
			Kind:       c.Kind,
			Name:       c.Name,
			DevicePath: c.DevicePath,
			Ref:        c.Ref,
			Role:       c.Role,
		})
	}
	return out
}

func buildFilesystems(filesystems []metrics.DiskUsage) []metrics.DiskFilesystem {
	if len(filesystems) == 0 {
		return []metrics.DiskFilesystem{}
	}
	out := make([]metrics.DiskFilesystem, 0, len(filesystems))
	for _, u := range filesystems {
		out = append(out, metrics.DiskFilesystem{
			Path:            u.Path,
			Device:          u.Device,
			Mountpoint:      u.Mountpoint,
			Used:            u.Used,
			Free:            u.Free,
			UsedRatio:       u.UsedRatio,
			InodesUsed:      u.InodesUsed,
			InodesFree:      u.InodesFree,
			InodesUsedRatio: u.InodesUsedRatio,
		})
	}
	return out
}

func buildRaid(raid raidSnapshot) metrics.Raid {
	arrays := make([]metrics.RaidArray, 0, len(raid.Arrays))
	for _, a := range raid.Arrays {
		members := make([]metrics.RaidMember, 0, len(a.MemberStates))
		for _, m := range a.MemberStates {
			members = append(members, metrics.RaidMember{Name: m.Name, State: m.State})
		}
		arrays = append(arrays, metrics.RaidArray{
			Name:         a.Name,
			Status:       a.Status,
			Active:       a.Active,
			Working:      a.Working,
			Failed:       a.Failed,
			Health:       a.Health,
			MemberStates: members,
			SyncStatus:   a.SyncStatus,
			SyncProgress: a.SyncProgress,
		})
	}
	return metrics.Raid{
		Supported: raid.Supported,
		Available: raid.Available,
		Arrays:    arrays,
	}
}

func (s *Sampler) collectSlow() {
	filesystems, storages, raid := collectSlowPlatform(thinpoolCachePath, s.cfg.Debug, s.usage)

	sort.Slice(storages, func(i, j int) bool {
		kindRank := func(k string) int {
			switch k {
			case "zfs_pool":
				return 0
			case "lvm_thinpool":
				return 1
			case "raid":
				return 2
			case "disk":
				return 3
			default:
				return 9
			}
		}
		ri, rj := kindRank(storages[i].Kind), kindRank(storages[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if storages[i].Kind == storages[j].Kind {
			return storages[i].Name < storages[j].Name
		}
		return storages[i].Kind < storages[j].Kind
	})

	aliases := buildMapperAliases()

	s.mu.Lock()
	s.slow.filesystems = filesystems
	s.slow.storages = storages
	s.slow.raid = raid
	s.slow.aliases = aliases
	s.mu.Unlock()

	if hasZFS(storages) {
		s.zfsIO.trigger()
	}
}

func (s *Sampler) hasZFS() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return hasZFS(s.slow.storages)
}

func hasZFS(storages []metrics.StorageUsage) bool {
	for _, storage := range storages {
		if storage.Kind == "zfs_pool" {
			return true
		}
	}
	return false
}

func (s *Sampler) collectFast() {
	ts := time.Now().UTC()

	cpuPercentSlice, err := cpu.Percent(0, false)
	var cpuUsageRatio float64
	if err == nil && len(cpuPercentSlice) > 0 {
		cpuUsageRatio = percentToRatio(cpuPercentSlice[0])
	}

	load1, load5, load15 := -1.0, -1.0, -1.0
	if runtime.GOOS != "windows" {
		if loadStat, err := load.Avg(); err == nil && loadStat != nil {
			load1, load5, load15 = loadStat.Load1, loadStat.Load5, loadStat.Load15
		}
	}

	cpuTimesSlice, err := cpu.Times(false)
	cpuTimes := metrics.CPUTimes{}
	if err == nil && len(cpuTimesSlice) > 0 {
		t := cpuTimesSlice[0]
		cpuTimes = metrics.CPUTimes{
			User:   t.User,
			System: t.System,
			Idle:   t.Idle,
			Iowait: t.Iowait,
			Steal:  t.Steal,
		}
	}

	vm, err := mem.VirtualMemory()
	if err != nil {
		vm = &mem.VirtualMemoryStat{}
	}
	swap, err := mem.SwapMemory()
	if err != nil {
		swap = &mem.SwapMemoryStat{}
	}

	diskIO, netIO := s.io.sampleIO(s.cfg.PreferredNICs)

	uptimeSec, err := host.Uptime()
	if err != nil {
		uptimeSec = 0
	}

	s.mu.RLock()
	procCount := s.procCount
	slowFS := s.slow.filesystems
	slowStor := s.slow.storages
	slowRaid := s.slow.raid
	aliases := s.slow.aliases
	s.mu.RUnlock()

	tcpCount, udpCount := countTCPUDP()

	physical := buildPhysical(diskIO)
	filesystems := filterFilesystems(slowFS, aliases)
	logicals := buildLogical(slowStor, slowRaid, filesystems, aliases)
	logical := buildLogicalMetrics(logicals)
	var zfsRates map[string]zfsIORates
	for _, logicalEntry := range logicals {
		if logicalEntry.Kind != "zfs_pool" {
			continue
		}
		zfsRates = s.zfsIO.snapshot()
		break
	}
	baseIO := buildBaseIO(logicals, physical, filesystems, zfsRates)
	if physical == nil {
		physical = []metrics.DiskPhysical{}
	}
	if filesystems == nil {
		filesystems = []metrics.DiskUsage{}
	}
	if logical == nil {
		logical = []metrics.DiskLogical{}
	}
	if baseIO == nil {
		baseIO = []metrics.DiskBaseIO{}
	}
	if netIO == nil {
		netIO = []metrics.NetIO{}
	}
	if slowRaid.Arrays == nil {
		slowRaid.Arrays = []raidArraySnapshot{}
	}
	fsMetrics := buildFilesystems(filesystems)

	m := &metrics.Snapshot{
		CPU: metrics.CPU{
			UsageRatio: cpuUsageRatio,
			Load1:      load1,
			Load5:      load5,
			Load15:     load15,
			Times:      cpuTimes,
		},
		Memory: metrics.Memory{
			Used:          vm.Used,
			Available:     vm.Available,
			Buffers:       vm.Buffers,
			Cached:        vm.Cached,
			UsedRatio:     percentToRatio(vm.UsedPercent),
			SwapUsed:      swap.Used,
			SwapFree:      swap.Free,
			SwapUsedRatio: percentToRatio(swap.UsedPercent),
		},
		Disk: metrics.Disk{
			Physical:    physical,
			Logical:     logical,
			Filesystems: fsMetrics,
			BaseIO:      baseIO,
		},
		Network: netIO,
		System: metrics.System{
			Alive:         true,
			UptimeSeconds: uptimeSec,
			Uptime:        formatUptime(uptimeSec),
		},
		Processes: metrics.Processes{
			ProcessCount: procCount,
		},
		Connections: metrics.Connections{
			TCPCount: tcpCount,
			UDPCount: udpCount,
		},
		Raid: buildRaid(slowRaid),
	}

	s.mu.Lock()
	s.latest = m
	s.latestTS = ts
	s.mu.Unlock()
}

func (s *Sampler) Version() string {
	return s.version
}

func (s *Sampler) PushDelay() time.Duration {
	return s.pushDelay
}
