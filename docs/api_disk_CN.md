# 磁盘结构

以代码为准：

- 运行时磁盘：[`internal/metrics/types.go`](../internal/metrics/types.go)
- 静态磁盘：[`internal/metrics/static_types.go`](../internal/metrics/static_types.go)

## 运行时：`metrics.disk`

`metrics.disk` 有四个数组：

- `physical[]`
- `logical[]`
- `filesystems[]`
- `base_io[]`

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
