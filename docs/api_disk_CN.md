# 磁盘结构

以代码为准：

- 运行时磁盘：[`internal/metrics/types.go`](../internal/metrics/types.go)
- 静态磁盘：[`internal/metrics/static_types.go`](../internal/metrics/static_types.go)

## 运行时：`metrics.disk`

`metrics.disk` 有四个数组和一个 SMART 对象：

- `physical[]`
- `logical[]`
- `filesystems[]`
- `base_io[]`
- `smart`

### `physical[]`

每条对应一个块设备。

- 必填
  - `name`
  - `read_bytes`、`write_bytes`
  - `read_rate_bytes_per_sec`、`write_rate_bytes_per_sec`
  - `iops`、`read_iops`、`write_iops`
  - `util_ratio`、`queue_length`、`wait_ms`、`service_ms`
- 可选
  - `device_path`、`ref`

### `logical[]`

逻辑存储的容量视图。

- 必填
  - `kind`、`name`
  - `used`、`free`、`used_ratio`
- 可选
  - `device_path`、`ref`、`health`

常见 `kind`：

- `disk`
- `raid`
- `raid_md`
- `lvm_vg`
- `lvm_thinpool`
- `lvm_lv`
- `zfs_pool`

### `filesystems[]`

挂载点视图。

- 必填
  - `path`
  - `used`、`free`、`used_ratio`
  - `inodes_used`、`inodes_free`、`inodes_used_ratio`
- 可选
  - `device`、`mountpoint`

### `base_io[]`

用于展示和排序的 IO 视图。

- 必填
  - `kind`、`name`
  - `read_rate_bytes_per_sec`、`write_rate_bytes_per_sec`
  - `read_iops`、`write_iops`、`iops`
- 可选
  - `device_path`、`ref`
  - `read_bytes`、`write_bytes`
  - `util_ratio`、`queue_length`、`wait_ms`、`service_ms`

`logical` 项可能没有累计字节和底层延迟/利用率字段。

### `smart`

S.M.A.R.T. 数据来自 root 侧缓存文件。它是运行时状态，不是静态硬件元数据。

- 必填
  - `status`
  - `devices[]`
- 可选
  - `updated_at`、`ttl_seconds`
- `devices[]`
  - 必填：`name`、`source`、`status`
  - 可选：`ref`、`device_path`、`device_type`、`protocol`、`model`、`serial`、`wwn`、`exit_status`、`health`、`temp_c`、`power_on_hours`、`lifetime_used_percent`、`critical_warning`、`failing_attrs[]`

`devices[]` 永远返回 `[]`，不是 `null`。读不到的 SMART 值省略字段。

`critical_warning` 是 NVMe 的原始 critical warning bitset。`failing_attrs[]` 只包含当前 `FAILING_NOW` 的 ATA SMART 属性：

- `id`
- `name`
- `when_failed`

常见 `status`：

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

`status` 表示采集状态。`stale` 表示缓存已过期但仍会返回最后一次设备数据。`health` 表示磁盘健康结论。磁盘可以同时是 `status=ok` 和 `health=failed`。

`devices[].ref` 只有在能安全匹配时才指向 `physical[].ref` 或 `logical[].ref`。

## 运行时：`metrics.raid`

- 必填
  - `supported`、`available`、`arrays[]`
- `arrays[]`
  - `name`、`status`、`active`、`working`、`failed`、`health`、`members`
  - 可选：`sync_status`、`sync_progress`
- `members[]`
  - `name`、`state`

## 静态：`disk`

静态结构只保留稳定元数据。

### `physical[]`

- `name`
- 可选：`device_path`、`ref`

### `logical[]`

- `kind`、`name`
- 可选：`device_path`、`ref`
- 可选元数据：`total`、`mountpoint`、`fs_type`、`devices[]`

### `filesystems[]`

- `path`、`total`、`fs_type`、`inodes_total`
- 可选：`device`、`mountpoint`

### `base_io[]`

- `kind`、`name`
- 可选：`device_path`、`ref`、`role`

## 静态：`raid`

- `supported`、`available`、`arrays[]`
- `arrays[]`：`name`、`level`、`devices`、`members[]`
- `members[]`：`name`

## 平台说明

- Linux 采集文件系统、块设备、RAID、LVM、ZFS
- 非 Linux 仍会从 `gopsutil` 分区结果填充 `filesystems[]`
- 非 Linux 的 RAID 固定返回 `supported=false`
- 数组返回 `[]`，不是 `null`
