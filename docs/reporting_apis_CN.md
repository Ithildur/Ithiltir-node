# 上报接口

本文档定义 Ithiltir-node 和 dashboard 之间的线协议（wire contract）。

以代码为准：

- 运行时结构：[`internal/metrics/types.go`](../internal/metrics/types.go)
- 静态结构：[`internal/metrics/static_types.go`](../internal/metrics/static_types.go)
- 本地 HTTP 处理器：[`internal/server/server.go`](../internal/server/server.go)
- 推送客户端：[`internal/push/push.go`](../internal/push/push.go)
- 上报配置：[`internal/reportcfg/config.go`](../internal/reportcfg/config.go)
- 暂存更新：[`internal/selfupdate/update.go`](../internal/selfupdate/update.go)

## HTTP 接口

`/api/node/*` 是 dashboard 提供的接口。Ithiltir-node 在 Push 模式调用这些接口，本身不提供这些路径。

| 范围 | 路径 | 方法 | 数据 | 成功 | 说明 |
| --- | --- | --- | --- | --- | --- |
| 本地页面 | `/` | `GET` | HTML | `200` | 内置单节点页面。见 [local_page_api_CN.md](local_page_api_CN.md)。 |
| 本地页面 | `/local` | `GET` | HTML | `200` | `/` 的别名。 |
| 本地页面 | `/metrics` | `GET` | `NodeReport` | `200` | 首次采样前返回 `503`。 |
| 本地页面 | `/static` | `GET` | `Static` | `200` | 静态数据未就绪前返回 `503`。 |
| Push 目标 | `/api/node/metrics` | `POST` | `NodeReport` | `200` | 需要 `X-Node-Secret`。 |
| Push 目标 | `/api/node/static` | `POST` | `Static` | `200` | 需要 `X-Node-Secret`。由 `/metrics` target URL 推导。 |
| Push 目标 | `/api/node/identity` | `POST` | `{}` | `200` | 需要 `X-Node-Secret`。返回 `{ "install_id": "...", "created": true/false }`。 |
| Push debug 本地 | `/` | `GET` | `NodeReport` | `200` | 仅 `push --debug` 启用；绑定到 `127.0.0.1:${NODE_PORT:-9101}`。优先返回最近一次成功上报的结果，否则返回当前快照。 |

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

- `200 OK` 是 Push 目标请求的唯一成功响应。target 是否成功只看 HTTP 状态；响应 body 只影响更新暂存。
- `/api/node/metrics` 可以返回非 JSON 内容、空 body，或 JSON。只有 `Content-Type` 为 `application/json` 时才解析 update manifest；允许 `charset=utf-8` 这类 media type 参数。
- JSON 响应可包含可选的 `update` manifest：

```json
{
  "update": {
    "id": "release-id",
    "version": "1.2.3",
    "url": "https://dashboard.example/releases/Ithiltir-node-windows-amd64.exe",
    "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "size": 12345678
  }
}
```

- 其他顶层 JSON 字段会被忽略。
- `update` 存在时，`update.version`、`update.url`、`update.sha256` 和正数字节数 `update.size` 必填。`update.id` 是可选元数据。
- `update.url` 必须是带 host 的绝对 `http` 或 `https` URL。`update.version` 必须是单个 release 目录名，不能是 `.` 或 `..`，且不得包含路径分隔符。`update.sha256` 是期望的 SHA-256 十六进制摘要，`update.size` 必须等于下载字节数。
- 下载 `update.url` 时，node 会把当前 target key 作为 `X-Node-Secret` 发送。不要把 key 放进 URL 或 query string。
- Windows 自更新需要 Windows runner（`ITHILTIR_NODE_RUNNER=1`）。Linux 和 macOS 自更新需要 `/var/lib/ithiltir-node/releases` 安装布局。安装布局外的直接二进制不会应用 update manifest；push 循环会报告 self update disabled 并继续运行。
- `version` 与 node 当前上报版本相同的 manifest 会先被忽略，不参与冲突判断。如果同一轮里多个剩余 target 返回 update manifest，所有 manifest 的 `id`、`version`、`url`、`sha256` 和 `size` 必须一致；有冲突则跳过更新。
- Windows 成功暂存更新后，`node push` 会干净退出。Windows runner 校验暂存文件，替换 `%ProgramData%\Ithiltir-node\bin\ithiltir-node.exe`，然后重启 node。Linux 和 macOS 切换 `/var/lib/ithiltir-node/current` 到下载后的 release，然后用相同参数和环境 exec 更新后的 node。
- JSON 格式错误、manifest 非法、下载失败、大小不匹配或校验和不匹配时，跳过更新并继续上报。
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
- `pressure`
  - `cpu`、`memory`、`io`
  - 每个资源包含 `status`，以及可选的 `some`、`full`
  - `some` / `full`：`avg10`、`avg60`、`avg300`、`total`
- `raid`
  - `supported`、`available`、`arrays[]`
  - `arrays[]`：`name`、`status`、`active`、`working`、`failed`、`health`、`members`、`sync_status?`、`sync_progress?`
  - `members[]`：`name`、`state`
- `thermal`
  - `status`、`sensors[]`
  - 可选：`updated_at`
  - `sensors[]`：`kind`、`name`、`sensor_key`、`source`、`status`
  - 可选：`temp_c`、`high_c`、`critical_c`
  - `kind`：`cpu`、`gpu`、`chipset`、`board`、`acpi` 或 `unknown`
  - `temp_c`、`high_c`、`critical_c` 读不到时省略字段

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
- 静态元数据会在启动时上报，并在静态快照变化时再次上报。
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

## 虚拟机快照

为 `node push` 设置 `ITHILTIR_NODE_VIRT_CACHE` 后启用独立 VM 推送。每个以 `/metrics` 结尾的上报目标派生同级 `/virt`，使用同一 `X-Node-Secret` 发送 POST；宿主报告不增加 VM 字段。任务仅发送新快照，失败时有界退避并保留最新样本，不积累队列。该路径不跟随 HTTP 重定向，遵守 `--require-https`。

版本 1 缓存就是上报体：`schema: 1`、`provider: "pve"`、`host`、`collected_at`、可选 `last_success_at`、`ttl_seconds: 90`、`status`（`ok` 或 `error`）、可选 `error`、`vms`。成功样本必须是数组，`[]` 表示成功枚举且没有 VM；失败可保留上次成功的列表和时间。缓存缺失或非法时上报错误，不刷新旧样本时间。Dash 在读取时计算数据是否过期。

VM 包含 `id`、`name`、`status`、`template`；可选字段为 `qmp_status`、`cpus`、`cpu_ratio`、`memory_bytes`、`memory_limit_bytes`、`disk_capacity_bytes`、`disk_read_bytes`、`disk_write_bytes`、`net_in_bytes`、`net_out_bytes`、`uptime_seconds`、`ips`。缺失表示未知。CPU 沿用 PVE 资源统计中相对于 VM CPU 配额的比例；内存沿用 PVE `mem`；磁盘容量沿用 `maxdisk`；磁盘和网络字节数为累计计数。只上报本机 QEMU VM，包括关机 VM 和模板。

Guest Agent IP 通过可选 `ips` 数组返回，每台 VM 最多 128 个不重复的 IPv4/IPv6 地址，包含私网地址，排除回环、链路本地、未指定、多播及非法地址。需要在 PVE 启用 Guest Agent，并在来宾内运行代理。IP 缺失不使基础采集失败。 IP 由 `pve-cache --serve` 内的独立慢任务采集（手动单次采集使用 `--guest`）：调度器在任务结束 60 秒后唤醒，成功的 VM 间隔 5 分钟再查，失败按 5、10、20、30 分钟退避（上限 30 分钟）。只查询新鲜清单中运行、非模板、未暂停的 VM。最多四路并发，单次查询超时 5 秒，慢任务总预算 50 秒。每台 VM 使用由查询进程继承的文件锁；超时终止进程组并等待退出，仍存活的查询会阻止该 VM 再次启动查询。启动前将退避状态写入仅 root 可读的 `/run/ithiltir-node/pve-guest`，该状态为易失数据，重启后重建。热采集只读此缓存。

可选字段 `ips_collected_at`、`ips_ttl_seconds` 独立描述 IP 样本新鲜度。helper 使用 900 秒 IP TTL；复用时保留原采样时间，省略过期样本以及已知早于 VM 本次启动的样本。成功查询无地址时可以只有新鲜度字段而没有 `ips`。查询失败仅在原 IP 样本未过期时继续使用；读取方必须单独判断 IP 新鲜度，不能只看快照 `stale`。兼容不带这两个字段的报告，此时 IP 新鲜度未知；提供时，时间必须非零且不晚于 `collected_at`，TTL 为 1–3600。

限制：4 MiB、4096 个不重复的 VM ID、255 字节名称/宿主名、64 字节状态、1024 字节错误、非负有符号 64 位计数器，以及 1–3600 秒 TTL。成功时采样时间等于最近成功时间，超前 Dash 超过 5 分钟的报告会被拒绝。Dash 对接收、重复及较旧样本返回 `204`，只有较新样本更新当前态；错误为 `400 invalid_virt`、`401 unauthorized`、`413 body_too_large`、`503 virt_unavailable`。该接口不下发更新、不刷新宿主在线率。旧 Dash 可以不支持该接口，失败不影响宿主上报。


## Node gRPC 传输

原 HTTP 接口继续可用。`ithiltir.node.v1.Node` 服务复用 Dash 现有监听地址，通过 TCP 上的 HTTP/2 通信，不启用 `app.grpc_port`。TLS 代理须以 gRPC 转发 `/ithiltir.node.v1.Node/`。契约单源位于 Dash 仓库的 `protocol/node.proto`；`bash scripts/generate-node-protocol.sh [node仓库目录]` 使用固定 Go 生成器版本同步两个仓库。

通过 metadata `x-node-secret` 鉴权。`Identify(Empty)` 返回 `install_id`、`created`、`protocol_version: 1`；`Metrics(Report)` 在 `Reply.json` 中返回原 JSON 响应及更新 manifest；`Static(Report)`、`Virt(Report)` 处理成功后返回 `Empty`。`Report.json` 使用原 UTF-8 JSON 载荷，保留缺失值和 64 位整数。指标/静态载荷仍限 1 MiB，VM 快照仍限 4 MiB。HTTP 和 gRPC 共享校验、接收截止时间、节点锁、持久化和响应构造。

鉴权失败返回 `UNAUTHENTICATED`，按 IP 限流；限流或消息超限返回 `RESOURCE_EXHAUSTED`，非法 JSON/报告字段返回 `INVALID_ARGUMENT`。受理失败按原错误边界映射为 `UNAVAILABLE` 或 `INTERNAL`。HTTP 状态码及响应体不变。 VM 快照存储失败返回 `UNAVAILABLE`，错误标识为 `virt_unavailable`；HTTP 保持 `503 virt_unavailable`。

`Connect` 是 Node 主动建立的独立双向查询流，首条能力声明须在 5 秒内到达。重连替换该节点的旧会话。派发前及连接期间每秒复查凭据；双方每 20 秒发送心跳，60 秒无消息关闭连接，心跳不刷新指标和在线率。查询总预算 10 秒，每节点最多 32 个待完成查询，结果上限 2 MiB。断线使待完成查询失败，不自动重放。

Node 设置 `ITHILTIR_NODE_TRANSPORT=http|grpc|auto` 选择传输，默认 `http` 以兼容已有安装。`grpc` 只使用 RPC；`auto` 在上报前探测 `Identify`，成功后该目标在进程生命周期内固定使用 gRPC，探测不可达或不兼容时可选择同 URL、同协议的 HTTP。鉴权/授权失败不切换到 HTTP。auto/grpc 不允许因 TLS 失败降级到明文；显式或默认 HTTP 保留原回退行为和 `--require-https` 限制。Node 禁用策略重试（gRPC 仍可透明重试确定尚未处理的调用），指标应答不确定时不跨传输重发，按原周期采集下一份指标；VM 快照继续按采样时间去重。
