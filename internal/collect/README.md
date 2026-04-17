# internal/collect

Sampling and aggregation.

- `config.go`: in-process collection config
- `sampler.go`: lifecycle, runtime snapshots, static snapshots
- `io.go`: disk and network rates
- `nic*.go`: NIC selection
- `conn*.go`: TCP and UDP counts
- `diskfilter*.go`: disk ranking and filtering
- `storage_linux.go`: Linux slow-path entry
- `storage_fs_linux.go`: filesystem usage
- `storage_lsblk_linux.go`: `lsblk` parsing
- `storage_disk_linux.go`: disk and RAID aggregation
- `storage_lvm_linux.go`: LVM metadata
- `storage_zfs_linux.go`: ZFS metadata
- `storage_raid_linux.go`: md and ZFS RAID parsing
- `storage_other.go`: non-Linux slow-path entry
- `storage_disk_fallback.go`: non-Linux storage fallback from filesystems
- `utils.go`: helpers

Notes:

- `Start()` starts background collectors once
- `Stop()` stops collectors. It does not exit the process
