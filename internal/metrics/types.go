package metrics

import (
	"slices"
	"time"
)

// 约定：所有 `*Ratio` 字段均为 0~1 的比例值（ratio），不是 0~100。

const (
	StatusOK           = "ok"
	StatusPartial      = "partial"
	StatusUnsupported  = "unsupported"
	StatusNotFound     = "not_found"
	StatusNoPermission = "no_permission"
	StatusTimeout      = "timeout"
	StatusError        = "error"
	StatusNoCache      = "no_cache"
	StatusStale        = "stale"
	StatusNoTool       = "no_tool"
	StatusStandby      = "standby"
)

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
	SMART       DiskSMART        `json:"smart"`
}

type DiskSMART struct {
	Status     string            `json:"status"`
	UpdatedAt  *time.Time        `json:"updated_at,omitempty"`
	TTLSeconds int               `json:"ttl_seconds,omitempty"`
	Devices    []DiskSMARTDevice `json:"devices"`
}

type DiskSMARTDevice struct {
	Ref             string          `json:"ref,omitempty"`
	Name            string          `json:"name"`
	DevicePath      string          `json:"device_path,omitempty"`
	DeviceType      string          `json:"device_type,omitempty"`
	Protocol        string          `json:"protocol,omitempty"`
	Model           string          `json:"model,omitempty"`
	Serial          string          `json:"serial,omitempty"`
	WWN             string          `json:"wwn,omitempty"`
	Source          string          `json:"source"`
	Status          string          `json:"status"`
	ExitStatus      *int            `json:"exit_status,omitempty"`
	Health          *string         `json:"health,omitempty"`
	TempC           *float64        `json:"temp_c,omitempty"`
	PowerOnHours    *uint64         `json:"power_on_hours,omitempty"`
	LifetimeUsedPct *float64        `json:"lifetime_used_percent,omitempty"`
	CriticalWarning *uint64         `json:"critical_warning,omitempty"`
	FailingAttrs    []DiskSMARTAttr `json:"failing_attrs,omitempty"`
}

type DiskSMARTAttr struct {
	ID         int    `json:"id,omitempty"`
	Name       string `json:"name"`
	WhenFailed string `json:"when_failed"`
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

type PressureStats struct {
	Avg10  float64 `json:"avg10"`
	Avg60  float64 `json:"avg60"`
	Avg300 float64 `json:"avg300"`
	Total  uint64  `json:"total"`
}

type PressureResource struct {
	Status string         `json:"status"`
	Some   *PressureStats `json:"some,omitempty"`
	Full   *PressureStats `json:"full,omitempty"`
}

type Pressure struct {
	CPU    PressureResource `json:"cpu"`
	Memory PressureResource `json:"memory"`
	IO     PressureResource `json:"io"`
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

type Thermal struct {
	Status    string          `json:"status"`
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
	Sensors   []ThermalSensor `json:"sensors"`
}

type ThermalSensor struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	SensorKey string   `json:"sensor_key"`
	Source    string   `json:"source"`
	Status    string   `json:"status"`
	TempC     *float64 `json:"temp_c,omitempty"`
	HighC     *float64 `json:"high_c,omitempty"`
	CriticalC *float64 `json:"critical_c,omitempty"`
}

type Snapshot struct {
	CPU         CPU         `json:"cpu"`
	Memory      Memory      `json:"memory"`
	Disk        Disk        `json:"disk"`
	Network     []NetIO     `json:"network"`
	System      System      `json:"system"`
	Processes   Processes   `json:"processes"`
	Connections Connections `json:"connections"`
	Pressure    Pressure    `json:"pressure"`
	Raid        Raid        `json:"raid"`
	Thermal     Thermal     `json:"thermal"`
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
	cloned.Disk.SMART = cloneDiskSMART(s.Disk.SMART)
	cloned.Network = slices.Clone(s.Network)
	cloned.Raid.Arrays = cloneRaidArrays(s.Raid.Arrays)
	cloned.Thermal = cloneThermal(s.Thermal)
	cloned.Pressure = clonePressure(s.Pressure)

	return &cloned
}

func cloneDiskSMART(in DiskSMART) DiskSMART {
	out := in
	out.UpdatedAt = clonePtr(in.UpdatedAt)
	out.Devices = slices.Clone(in.Devices)
	for i := range out.Devices {
		out.Devices[i].ExitStatus = clonePtr(in.Devices[i].ExitStatus)
		out.Devices[i].Health = clonePtr(in.Devices[i].Health)
		out.Devices[i].TempC = clonePtr(in.Devices[i].TempC)
		out.Devices[i].PowerOnHours = clonePtr(in.Devices[i].PowerOnHours)
		out.Devices[i].LifetimeUsedPct = clonePtr(in.Devices[i].LifetimeUsedPct)
		out.Devices[i].CriticalWarning = clonePtr(in.Devices[i].CriticalWarning)
		out.Devices[i].FailingAttrs = slices.Clone(in.Devices[i].FailingAttrs)
	}
	return out
}

func cloneRaidArrays(in []RaidArray) []RaidArray {
	out := slices.Clone(in)
	for i := range out {
		out[i].MemberStates = slices.Clone(in[i].MemberStates)
	}
	return out
}

func cloneThermal(in Thermal) Thermal {
	out := in
	out.UpdatedAt = clonePtr(in.UpdatedAt)
	out.Sensors = slices.Clone(in.Sensors)
	for i := range out.Sensors {
		out.Sensors[i].TempC = clonePtr(in.Sensors[i].TempC)
		out.Sensors[i].HighC = clonePtr(in.Sensors[i].HighC)
		out.Sensors[i].CriticalC = clonePtr(in.Sensors[i].CriticalC)
	}
	return out
}

func clonePressure(in Pressure) Pressure {
	out := in
	out.CPU = clonePressureResource(in.CPU)
	out.Memory = clonePressureResource(in.Memory)
	out.IO = clonePressureResource(in.IO)
	return out
}

func clonePressureResource(in PressureResource) PressureResource {
	out := in
	out.Some = clonePtr(in.Some)
	out.Full = clonePtr(in.Full)
	return out
}

func clonePtr[T any](in *T) *T {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func (r *NodeReport) Clone() *NodeReport {
	if r == nil {
		return nil
	}

	cloned := *r
	cloned.Metrics = r.Metrics.Clone()
	return &cloned
}
