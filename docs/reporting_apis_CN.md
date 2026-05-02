# 上报接口

本文档定义 Ithiltir-node 和 dashboard 之间的线协议（wire contract）。

以代码为准：

- 运行时结构：[`internal/metrics/types.go`](../internal/metrics/types.go)
- 静态结构：[`internal/metrics/static_types.go`](../internal/metrics/static_types.go)
- 本地 HTTP 处理器：[`internal/server/server.go`](../internal/server/server.go)
- 推送客户端：[`internal/push/push.go`](../internal/push/push.go)
- 上报配置：[`internal/reportcfg/config.go`](../internal/reportcfg/config.go)

## HTTP 接口

`/api/node/*` 是 dashboard 提供的接口。Ithiltir-node 在 Push 模式调用这些接口，本身不提供这些路径。

| 范围 | 路径 | 方法 | 数据 | 成功 | 说明 |
| --- | --- | --- | --- | --- | --- |
| Serve 本地 | `/` | `GET` | HTML | `200` | 内置单节点页面。见 [serve_page_api_CN.md](serve_page_api_CN.md)。 |
| Serve 本地 | `/serve` | `GET` | HTML | `200` | `/` 的别名。 |
| Serve 本地 | `/metrics` | `GET` | `NodeReport` | `200` | 首次采样前返回 `503`。 |
| Serve 本地 | `/static` | `GET` | `Static` | `200` | 静态数据未就绪前返回 `503`。 |
| Push 目标 | `/api/node/metrics` | `POST` | `NodeReport` | `200` | 需要 `X-Node-Secret`。 |
| Push 目标 | `/api/node/static` | `POST` | `Static` | `200` | 需要 `X-Node-Secret`。由 `/metrics` target URL 推导。 |
| Push 目标 | `/api/node/identity` | `POST` | `{}` | `200` | 需要 `X-Node-Secret`。返回 `{ "install_id": "...", "created": true/false }`。 |
| Push 本地 | `/` | `GET` | `NodeReport` | `200` | Push 模式绑定到 `127.0.0.1:${NODE_PORT:-9100}`。优先返回最近一次成功上报的结果，否则返回当前快照。 |

本地 `GET` 路由也接受 `HEAD`。其他方法返回 `405`，并带 `Allow: GET, HEAD`。

## 线协议约定

- JSON 使用 UTF-8。
- 时间戳为 UTC RFC3339。
- 字节和包计数是原始数字计数器。
- `*Ratio` 字段范围是 `0..1`，不是百分比。
- 数组返回 `[]`，不是 `null`。
- 没有值的可选字段会省略。
- 运行时磁盘结构和静态磁盘结构不是一回事，别混用；见 [api_disk_CN.md](api_disk_CN.md)。

## Push 目标

上报 target URL 是 dashboard 的指标接口，通常是：

```text
https://dashboard.example/api/node/metrics
```

agent 每轮把同一份 `NodeReport` 发给所有配置的 target。单个 target 失败不阻塞其他 target。

target URL 规则：

- `POST <target URL>` 接收运行时指标。
- target 路径以 `/metrics` 结尾时，静态元数据发到同级 `/static` URL。
- `report install <url> <key>` 要求 target URL 以 `/metrics` 结尾；写入本地配置前会调用同级 `/identity` URL。
- `report update <id> <key>` 只轮换 target key。URL 修改必须走 `report install`。

传输规则：

- `http` 和 `https` target URL 都是合法配置。
- HTTPS target 可按客户端回落规则降级到 HTTP。
- `--require-https` 会拒绝非 HTTPS target，并禁止 HTTP 回落。

响应处理：

- `200 OK` 是 Push 目标请求的唯一成功响应。
- `/api/node/metrics` 可以返回空 body、纯文本 `ok`，或 JSON。JSON 响应可包含可选的 `update` manifest：

```json
{
  "ok": true,
  "update": {
    "id": "release-id",
    "version": "1.2.3",
    "url": "https://dashboard.example/releases/Ithiltir-node-windows-amd64.exe",
    "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "size": 12345678
  }
}
```

- `update` 存在时，`update.version`、`update.url`、`update.sha256` 和正数字节数 `update.size` 必填。当前只有由 Windows runner 启动的 node 支持自更新暂存；直接启动的 node 会忽略该 manifest。
- 任何非 `200` 响应都会让该 target 在当前轮失败。
- `/api/node/identity` 必须返回带 `install_id` 的 JSON；`created` 只是行为元数据。

## 上报配置

默认配置路径：

- Linux/macOS：`/var/lib/ithiltir-node/report.yaml`
- Windows：`%ProgramData%\Ithiltir-node\report.yaml`

可用 `ITHILTIR_NODE_REPORT_CONFIG` 覆盖。

配置文件缺失或 `targets` 为空时正常启动并跳过上报。配置格式错误时启动失败。

```yaml
version: 1
targets:
  - id: 1
    url: https://dashboard.example/api/node/metrics
    key: node-secret
    server_install_id: dashboard-install-id
```

写入使用原子 rename，并保持文件权限 `0600`。

## 运行时结构

顶层对象：`NodeReport`

```json
{
  "version": "...",
  "hostname": "...",
  "timestamp": "...",
  "metrics": {}
}
```

- `timestamp`：UTC RFC3339
- `metrics`：`Snapshot`

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
  "cpu": {},
  "memory": {},
  "disk": {},
  "system": {},
  "raid": {}
}
```

静态上报行为：

- 静态元数据没有外层包装对象。
- `report_interval_seconds` 是必填字段。
- 静态元数据会在启动时上报一次。
- 静态采集不完整时继续重试，直到完整。
- 被抑制的 push 失败恢复后，静态元数据会再补发一次。

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
