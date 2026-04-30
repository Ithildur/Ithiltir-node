package metrics

import (
	"slices"
	"time"
)

// 约定：所有 `*Ratio` 字段均为 0~1 的比例值（ratio），不是 0~100。

type CPUTimes struct {
	User   float64 `json:"user"`
	System float64 `json:"system"`
	Idle   float64 `json:"idle"`
	Iowait float64 `json:"iowait"`
	Steal  float64 `json:"steal"`
}

type CPU struct {
	UsageRatio float64  `json:"usage_ratio"`
	Load1      float64  `json:"load1"`
	Load5      float64  `json:"load5"`
	Load15     float64  `json:"load15"`
	Times      CPUTimes `json:"times"`
}

type Memory struct {
	Used          uint64  `json:"used"`
	Available     uint64  `json:"available"`
	Buffers       uint64  `json:"buffers"`
	Cached        uint64  `json:"cached"`
	UsedRatio     float64 `json:"used_ratio"`
	SwapUsed      uint64  `json:"swap_used"`
	SwapFree      uint64  `json:"swap_free"`
	SwapUsedRatio float64 `json:"swap_used_ratio"`
}

type DiskUsage struct {
	Path string `json:"path"`

	Device     string `json:"device,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`

	Total     uint64  `json:"total,omitempty"`
	Used      uint64  `json:"used"`
	Free      uint64  `json:"free"`
	UsedRatio float64 `json:"used_ratio"`
	FsType    string  `json:"fs_type,omitempty"`

	InodesTotal     uint64  `json:"inodes_total,omitempty"`
	InodesUsed      uint64  `json:"inodes_used"`
	InodesFree      uint64  `json:"inodes_free"`
	InodesUsedRatio float64 `json:"inodes_used_ratio"`
}

type DiskFilesystem struct {
	Path       string `json:"path"`
	Device     string `json:"device,omitempty"`
	Mountpoint string `json:"mountpoint,omitempty"`

	Used      uint64  `json:"used"`
	Free      uint64  `json:"free"`
	UsedRatio float64 `json:"used_ratio"`

	InodesUsed      uint64  `json:"inodes_used"`
	InodesFree      uint64  `json:"inodes_free"`
	InodesUsedRatio float64 `json:"inodes_used_ratio"`
}

type DiskIO struct {
	ReadBytes            uint64  `json:"read_bytes"`
	WriteBytes           uint64  `json:"write_bytes"`
	ReadRateBytesPerSec  float64 `json:"read_rate_bytes_per_sec"`
	WriteRateBytesPerSec float64 `json:"write_rate_bytes_per_sec"`
	IOPS                 float64 `json:"iops"`
	ReadIOPS             float64 `json:"read_iops"`
	WriteIOPS            float64 `json:"write_iops"`
	UtilRatio            float64 `json:"util_ratio"`
	QueueLength          float64 `json:"queue_length"`
	WaitMs               float64 `json:"wait_ms"`
	ServiceMs            float64 `json:"service_ms"`
}

type DiskPhysical struct {
	Name       string `json:"name"`
	DevicePath string `json:"device_path,omitempty"`
	Ref        string `json:"ref,omitempty"`
	DiskIO
}

type DiskLogical struct {
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	DevicePath string  `json:"device_path,omitempty"`
	Ref        string  `json:"ref,omitempty"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	UsedRatio  float64 `json:"used_ratio"`
	Health     string  `json:"health,omitempty"`
}

type DiskMountpoint struct {
	FsType          string  `json:"fs_type,omitempty"`
	InodesTotal     uint64  `json:"inodes_total,omitempty"`
	InodesUsed      uint64  `json:"inodes_used,omitempty"`
	InodesFree      uint64  `json:"inodes_free,omitempty"`
	InodesUsedRatio float64 `json:"inodes_used_ratio,omitempty"`
}

type DiskBaseIO struct {
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	DevicePath string `json:"device_path,omitempty"`
	Ref        string `json:"ref,omitempty"`

	ReadBytes            uint64  `json:"read_bytes,omitempty"`
	WriteBytes           uint64  `json:"write_bytes,omitempty"`
	ReadRateBytesPerSec  float64 `json:"read_rate_bytes_per_sec"`
	WriteRateBytesPerSec float64 `json:"write_rate_bytes_per_sec"`
	ReadIOPS             float64 `json:"read_iops"`
	WriteIOPS            float64 `json:"write_iops"`
	IOPS                 float64 `json:"iops"`
	UtilRatio            float64 `json:"util_ratio,omitempty"`
	QueueLength          float64 `json:"queue_length,omitempty"`
	WaitMs               float64 `json:"wait_ms,omitempty"`
	ServiceMs            float64 `json:"service_ms,omitempty"`
}

type StorageUsage struct {
	Kind string `json:"kind"` // disk / raid / lvm_vg / lvm_thinpool / zfs_pool
	Name string `json:"name"`

	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Free      uint64  `json:"free"`
	UsedRatio float64 `json:"used_ratio"`

	Mountpoints []string `json:"mountpoints,omitempty"`

	Model      string `json:"model,omitempty"`
	Serial     string `json:"serial,omitempty"`
	Rotational *bool  `json:"rotational,omitempty"` // HDD=true, SSD=false

	Level  string `json:"level,omitempty"`
	Health string `json:"health,omitempty"`

	Devices []string `json:"devices,omitempty"`

	DataRatio float64 `json:"data_ratio,omitempty"`
	MetaRatio float64 `json:"meta_ratio,omitempty"`
}

type Disk struct {
	Physical    []DiskPhysical   `json:"physical"`
	Logical     []DiskLogical    `json:"logical"`
	Filesystems []DiskFilesystem `json:"filesystems"`
	BaseIO      []DiskBaseIO     `json:"base_io"`
}

type NetIO struct {
	Name                  string  `json:"name"`
	BytesRecv             uint64  `json:"bytes_recv"`
	BytesSent             uint64  `json:"bytes_sent"`
	RecvRateBytesPerSec   float64 `json:"recv_rate_bytes_per_sec"`
	SentRateBytesPerSec   float64 `json:"sent_rate_bytes_per_sec"`
	PacketsRecv           uint64  `json:"packets_recv"`
	PacketsSent           uint64  `json:"packets_sent"`
	RecvRatePacketsPerSec float64 `json:"recv_rate_packets_per_sec"`
	SentRatePacketsPerSec float64 `json:"sent_rate_packets_per_sec"`
	ErrIn                 uint64  `json:"err_in"`
	ErrOut                uint64  `json:"err_out"`
	DropIn                uint64  `json:"drop_in"`
	DropOut               uint64  `json:"drop_out"`
}

type System struct {
	Alive         bool   `json:"alive"`
	UptimeSeconds uint64 `json:"uptime_seconds"`
	Uptime        string `json:"uptime"`
}

type Processes struct {
	ProcessCount int `json:"process_count"`
}

type Connections struct {
	TCPCount int `json:"tcp_count"`
	UDPCount int `json:"udp_count"`
}

type RaidMember struct {
	Name  string `json:"name"`
	State string `json:"state"` // up/down/unknown
}

type RaidArray struct {
	Name         string       `json:"name"`
	Status       string       `json:"status"`
	Active       int          `json:"active"`
	Working      int          `json:"working"`
	Failed       int          `json:"failed"`
	Health       string       `json:"health"`
	MemberStates []RaidMember `json:"members"`
	SyncStatus   string       `json:"sync_status,omitempty"`
	SyncProgress string       `json:"sync_progress,omitempty"`
}

type Raid struct {
	Supported bool        `json:"supported"`
	Available bool        `json:"available"`
	Arrays    []RaidArray `json:"arrays"`
}

type Snapshot struct {
	CPU         CPU         `json:"cpu"`
	Memory      Memory      `json:"memory"`
	Disk        Disk        `json:"disk"`
	Network     []NetIO     `json:"network"`
	System      System      `json:"system"`
	Processes   Processes   `json:"processes"`
	Connections Connections `json:"connections"`
	Raid        Raid        `json:"raid"`
}

type NodeReport struct {
	Version   string    `json:"version"`
	Hostname  string    `json:"hostname"`
	Timestamp time.Time `json:"timestamp"`
	Metrics   *Snapshot `json:"metrics"`
}

func NewNodeReport(version, hostname string, timestamp time.Time, snapshot *Snapshot) NodeReport {
	return NodeReport{
		Version:   version,
		Hostname:  hostname,
		Timestamp: timestamp,
		Metrics:   snapshot,
	}
}

func (s *Snapshot) Clone() *Snapshot {
	if s == nil {
		return nil
	}

	cloned := *s
	cloned.Disk.Physical = slices.Clone(s.Disk.Physical)
	cloned.Disk.Logical = slices.Clone(s.Disk.Logical)
	cloned.Disk.Filesystems = slices.Clone(s.Disk.Filesystems)
	cloned.Disk.BaseIO = slices.Clone(s.Disk.BaseIO)
	cloned.Network = slices.Clone(s.Network)
	cloned.Raid.Arrays = cloneRaidArrays(s.Raid.Arrays)

	return &cloned
}

func cloneRaidArrays(in []RaidArray) []RaidArray {
	out := slices.Clone(in)
	for i := range out {
		out[i].MemberStates = slices.Clone(in[i].MemberStates)
	}
	return out
}

func (r *NodeReport) Clone() *NodeReport {
	if r == nil {
		return nil
	}

	cloned := *r
	cloned.Metrics = r.Metrics.Clone()
	return &cloned
}
