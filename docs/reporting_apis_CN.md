# 上报接口

以代码为准：

- 运行时结构：[`internal/metrics/types.go`](../internal/metrics/types.go)
- 静态结构：[`internal/metrics/static_types.go`](../internal/metrics/static_types.go)
- HTTP 处理器：[`internal/server/server.go`](../internal/server/server.go)
- 推送客户端：[`internal/push/push.go`](../internal/push/push.go)

## 接口

| 路径 | 方法 | 请求/响应体 | 说明 |
| --- | --- | --- | --- |
| `/metrics` | `GET` | `NodeReport` | Serve 模式。首次采样前返回 `503`。 |
| `/api/node/metrics` | `POST` | `NodeReport` | Push 目标。请求头：`X-Node-Secret`。 |
| `/api/node/static` | `POST` | `Static` | Push 静态元数据。 |
| `/api/node/identity` | `POST` | `{}` / `{install_id, created}` | 安装期 dashboard 身份检查。请求头：`X-Node-Secret`。 |
| `/` | `GET` | `NodeReport` | Push 模式本地接口。优先返回最近一次成功上报的结果，否则返回当前快照。 |

Push 默认先走 HTTPS。除非加 `--require-https`，否则允许回落 HTTP。
Push 目标通过 `report.yaml` 配置；没有配置文件或 `targets` 为空时正常启动并跳过上报。
配置文件格式错误时启动失败。
安装脚本会先调用 `report install <url> <key>`。该命令读取 `/api/node/identity`，把返回的 `server_install_id` 写入本地配置；重复执行相同 install 直接成功，相同服务端身份指向不同本地配置时才提示选择。
`report update <id> <key>` 只轮换 target key；URL 修改必须走 `report install`。

## 运行时结构

顶层对象：`NodeReport`

```json
{
  "version": "...",
  "hostname": "...",
  "timestamp": "...",
  "metrics": { ... }
}
```

- `timestamp`：UTC，RFC3339
- `metrics`：`Snapshot`

Push 每轮采集只生成一份 `NodeReport`，并发发送到所有配置的 target。
单个 target 失败不影响其他 target。

`Snapshot` 字段：

- `cpu`
  - `usage_ratio`、`load1`、`load5`、`load15`、`times`
  - `times`：`user`、`system`、`idle`、`iowait`、`steal`
- `memory`
  - `used`、`available`、`buffers`、`cached`、`used_ratio`
  - `swap_used`、`swap_free`、`swap_used_ratio`
- `disk`
  - 见 [api_disk_CN.md](api_disk_CN.md)
- `network[]`
  - `name`
  - `bytes_recv`、`bytes_sent`
  - `recv_rate_bytes_per_sec`、`sent_rate_bytes_per_sec`
  - `packets_recv`、`packets_sent`
  - `recv_rate_packets_per_sec`、`sent_rate_packets_per_sec`
  - `err_in`、`err_out`、`drop_in`、`drop_out`
- `system`
  - `alive`、`uptime_seconds`、`uptime`
- `processes`
  - `process_count`
- `connections`
  - `tcp_count`、`udp_count`
- `raid`
  - `supported`、`available`、`arrays[]`
  - `arrays[]`：`name`、`status`、`active`、`working`、`failed`、`health`、`members`、`sync_status?`、`sync_progress?`
  - `members[]`：`name`、`state`

## 静态结构

顶层对象：`Static`

```json
{
  "version": "...",
  "timestamp": "...",
  "report_interval_seconds": 3,
  "cpu": { ... },
  "memory": { ... },
  "disk": { ... },
  "system": { ... },
  "raid": { ... }
}
```

- 没有外层包装
- `report_interval_seconds` 是发布时的必填字段
- 静态数据会在启动时上报一次
- 如果静态采集还不完整，agent 会继续重试
- 被抑制的 push 错误恢复后，静态数据会再补发一次

`Static` 字段：

- `cpu.info`
  - `model_name`、`vendor_id`、`sockets`、`cores_physical`、`cores_logical`、`frequency_mhz`
- `memory`
  - `total`、`swap_total`
- `disk`
  - 见 [api_disk_CN.md](api_disk_CN.md)
- `system`
  - `hostname`、`os`、`platform`、`platform_version`、`kernel_version`、`arch`
- `raid`
  - `supported`、`available`、`arrays[]`
  - `arrays[]`：`name`、`level`、`devices`、`members[]`
  - `members[]`：`name`

## 约定

- 数组返回 `[]`，不是 `null`
- `*Ratio` 字段范围是 `0..1`
- 运行时磁盘结构和静态磁盘结构不是一回事，别混用
