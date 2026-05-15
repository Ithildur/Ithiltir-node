# Disk Schema

Code of record:

- runtime disk: [`internal/metrics/types.go`](../internal/metrics/types.go)
- static disk: [`internal/metrics/static_types.go`](../internal/metrics/static_types.go)

## Runtime: `metrics.disk`

`metrics.disk` has four arrays and one SMART object:

- `physical[]`
- `logical[]`
- `filesystems[]`
- `base_io[]`
- `smart`

### `physical[]`

Per block device.

- required
  - `name`
  - `read_bytes`, `write_bytes`
  - `read_rate_bytes_per_sec`, `write_rate_bytes_per_sec`
  - `iops`, `read_iops`, `write_iops`
  - `util_ratio`, `queue_length`, `wait_ms`, `service_ms`
- optional
  - `device_path`, `ref`

### `logical[]`

Capacity view for logical storage.

- required
  - `kind`, `name`
  - `used`, `free`, `used_ratio`
- optional
  - `device_path`, `ref`, `health`

Typical `kind` values:

- `disk`
- `raid`
- `raid_md`
- `lvm_vg`
- `lvm_thinpool`
- `lvm_lv`
- `zfs_pool`

### `filesystems[]`

Mountpoint view.

- required
  - `path`
  - `used`, `free`, `used_ratio`
  - `inodes_used`, `inodes_free`, `inodes_used_ratio`
- optional
  - `device`, `mountpoint`

### `base_io[]`

IO view used for display and ranking.

- required
  - `kind`, `name`
  - `read_rate_bytes_per_sec`, `write_rate_bytes_per_sec`
  - `read_iops`, `write_iops`, `iops`
- optional
  - `device_path`, `ref`
  - `read_bytes`, `write_bytes`
  - `util_ratio`, `queue_length`, `wait_ms`, `service_ms`

`logical` entries may omit cumulative bytes and low-level latency/utilization fields.

### `smart`

S.M.A.R.T. data is read from the root-side cache file. It is runtime state, not static hardware metadata.

- required
  - `status`
  - `devices[]`
- optional
  - `updated_at`, `ttl_seconds`
- `devices[]`
  - required: `name`, `source`, `status`
  - optional: `ref`, `device_path`, `device_type`, `protocol`, `model`, `serial`, `wwn`, `exit_status`, `health`, `temp_c`, `power_on_hours`, `lifetime_used_percent`, `critical_warning`, `failing_attrs[]`

`devices[]` is always `[]`, not `null`. Unavailable SMART values are omitted.

`critical_warning` is the raw NVMe critical warning bitset when available. `failing_attrs[]` contains ATA SMART attributes currently reported as failed:

- `id`
- `name`
- `when_failed`

Common `status` values:

- `ok`
- `partial`
- `unsupported`
- `not_found`
- `no_permission`
- `timeout`
- `error`
- `no_cache`
- `stale`
- `no_tool`
- `standby`

`status` is the collection state. `stale` means the last cache entry is expired but still returned. `health` is the disk health result. A disk can have `status=ok` and `health=failed`.

`devices[].ref` points to `physical[].ref` or `logical[].ref` only when the device can be matched safely.

## Runtime: `metrics.raid`

- required
  - `supported`, `available`, `arrays[]`
- `arrays[]`
  - `name`, `status`, `active`, `working`, `failed`, `health`, `members`
  - optional: `sync_status`, `sync_progress`
- `members[]`
  - `name`, `state`

## Static: `disk`

Static payload keeps stable metadata only.

### `physical[]`

- `name`
- optional: `device_path`, `ref`

### `logical[]`

- `kind`, `name`
- optional: `device_path`, `ref`
- optional metadata: `total`, `mountpoint`, `fs_type`, `devices[]`

### `filesystems[]`

- `path`, `total`, `fs_type`, `inodes_total`
- optional: `device`, `mountpoint`

### `base_io[]`

- `kind`, `name`
- optional: `device_path`, `ref`, `role`

## Static: `raid`

- `supported`, `available`, `arrays[]`
- `arrays[]`: `name`, `level`, `devices`, `members[]`
- `members[]`: `name`

## Platform Notes

- Linux collects filesystem, block, RAID, LVM, and ZFS data
- Non-Linux still fills `filesystems[]` from `gopsutil` partitions
- Non-Linux RAID reports `supported=false`
- Arrays are returned as `[]`, not `null`
