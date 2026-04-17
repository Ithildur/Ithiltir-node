# internal/collect

采样与聚合。

- `config.go`：进程内采集配置
- `sampler.go`：生命周期、运行时快照、静态快照
- `io.go`：磁盘和网络速率
- `nic*.go`：网卡筛选
- `conn*.go`：TCP 和 UDP 连接数
- `diskfilter*.go`：磁盘排序和过滤
- `storage_linux.go`：Linux 慢路径入口
- `storage_fs_linux.go`：文件系统使用量
- `storage_lsblk_linux.go`：`lsblk` 解析
- `storage_disk_linux.go`：磁盘和 RAID 聚合
- `storage_lvm_linux.go`：LVM 元数据
- `storage_zfs_linux.go`：ZFS 元数据
- `storage_raid_linux.go`：md 和 ZFS RAID 解析
- `storage_other.go`：非 Linux 慢路径入口
- `storage_disk_fallback.go`：从文件系统回退聚合非 Linux 存储视图
- `utils.go`：辅助函数

说明：

- `Start()` 只启动一次后台采集
- `Stop()` 只停止采集，不负责退出进程
