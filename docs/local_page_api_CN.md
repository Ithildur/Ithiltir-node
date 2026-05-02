# 本地页面 API

本文档说明 Ithiltir-node 通过 local 模式提供的内置单节点页面。

以代码为准：

- 本地 HTTP 处理器：[`internal/server/server.go`](../internal/server/server.go)
- 页面外壳：[`internal/server/localpage/page.html`](../internal/server/localpage/page.html)
- 页面运行时：[`internal/server/localpage/assets/page.js`](../internal/server/localpage/assets/page.js)
- 页面样式：[`internal/server/localpage/assets/page.css`](../internal/server/localpage/assets/page.css)
- JSON 结构：[reporting_apis_CN.md](reporting_apis_CN.md)

## 路由

| 路径 | 方法 | 响应 | 说明 |
| --- | --- | --- | --- |
| `/` | `GET` | HTML | 本地页面。返回 `Cache-Control: no-store`。 |
| `/local` | `GET` | HTML | `/` 的别名。 |
| `/local-assets/<name>` | `GET` | 文件 | 只服务选中 `localpage/assets/` 目录下的文件。 |
| `/metrics` | `GET` | `NodeReport` | 首次采样前返回 `503`。 |
| `/static` | `GET` | `Static` | 静态数据未就绪前返回 `503`。 |

本地 `GET` 路由也接受 `HEAD`。其他方法返回 `405`，并带 `Allow: GET, HEAD`。

资源规则：

- `/local-assets/` 不列目录。
- 非法资源路径、缺失文件和目录返回 `404`。
- 不服务 `localpage/assets/` 之外的文件。

## 页面解析

HTTP 服务启动时选择页面来源：

1. `ITHILTIR_NODE_LOCAL_PAGE_DIR` 已设置且目录内有 `page.html`：使用该目录。
2. `ITHILTIR_NODE_LOCAL_PAGE_DIR` 已设置但目录内没有 `page.html`：记录日志并使用内置页面。
3. 环境变量未设置，且二进制同级存在 `localpage/page.html`：使用该目录。
4. 其他情况使用内置页面。

外部目录：

```text
node
localpage/
  page.html
  assets/
    page.css
    page.js
```

示例：

```bash
ITHILTIR_NODE_LOCAL_PAGE_DIR=/opt/ithiltir-node/localpage ./node local
```

外部页面必须有 `page.html`。`assets/` 可选，但通过 `/local-assets/*` 引用的文件必须放在 `assets/` 下。

## 前端运行时

默认页面定义 `window.ITHILTIR_LOCAL`，`page.js` 会把它合并到自身默认配置上：

```js
window.ITHILTIR_LOCAL = {
  endpoint: "/metrics",
  staticEndpoint: "/static",
  mode: "node-report",
  pollMs: 5000
};
```

| 字段 | 默认值 | 含义 |
| --- | --- | --- |
| `endpoint` | `/metrics` | 运行时数据接口。 |
| `staticEndpoint` | `/static` | 静态硬件接口。空值会禁用静态请求。 |
| `mode` | `node-report` | `node-report` 适配 `NodeReport`；`local-view` 直接渲染视图模型。 |
| `pollMs` | `5000` | 轮询间隔。小于 `1000` 时按 `1000` 处理。 |

模式行为：

- `node-report`：`endpoint` 返回 `NodeReport`，`staticEndpoint` 返回 `Static`，`page.js` 通过 `toView(report, stat)` 适配。
- `local-view`：`endpoint` 返回下方视图模型，`staticEndpoint` 会被忽略。

## 默认映射

适配器：[`internal/server/localpage/assets/page.js`](../internal/server/localpage/assets/page.js) 的 `toView(report, stat)`。

| 页面项 | 来源 |
| --- | --- |
| hostname | `report.hostname` |
| cpu | `stat.cpu.info.model_name`，回退到 `vendor_id` |
| cores | `stat.cpu.info.cores_physical`、`cores_logical` |
| memory / swap | `stat.memory.total`、`swap_total`；运行时内存作为回退 |
| system / kernel | `stat.system` |
| uptime | `report.metrics.system.uptime` |
| load | `report.metrics.cpu.load1/load5/load15` |
| processes | `report.metrics.processes.process_count` |
| tcp / udp | `report.metrics.connections` |
| cpu% | `report.metrics.cpu.usage_ratio` |
| memory% | `report.metrics.memory.used_ratio` |
| disk% | `report.metrics.disk.filesystems[]` 和 `logical[]` 中最大的 `used_ratio` |
| memory detail | `report.metrics.memory` |
| df | 优先用 `filesystems[]`，没有时用 `logical[]` |
| iostat | 优先用 `base_io[]`，没有时用 `physical[]` |
| network | `report.metrics.network[]` |
| raid | `report.metrics.raid` |

显示规则：

- `*Ratio` 是 `0..1`。
- 网络不计算百分比。
- 磁盘行显示 `mount / used% / used / total`。
- 磁盘总量优先用运行时 `total`，再查静态磁盘元数据，最后用 `used + free`。
- 磁盘、IO、网络、RAID 列表最多显示 8 行。
- RAID 为空、不可用或不支持时隐藏 `cat /proc/mdstat` 区块。

## `local-view` 结构

`local-view` 用于复用页面外壳，但由调用方直接提供展示值。字符串字段会原样渲染。`cpu`、`memory`、`disk` 是 `0..1` 范围内的比例值。

```js
window.ITHILTIR_LOCAL = {
  endpoint: "/api/local/view",
  mode: "local-view",
  pollMs: 5000
};
```

```json
{
  "hostname": "node-a",
  "cpuModel": "AMD EPYC 7K62",
  "coreCount": "8 Cores 16 Threads",
  "memoryTotal": "32.0 GB",
  "swapTotal": "2.0 GB",
  "platform": "ubuntu / amd64",
  "kernel": "6.8.0",
  "uptime": "02d 14h 36m 21s",
  "processes": "182",
  "tcp": "86",
  "udp": "37",
  "cpu": 0.34,
  "memory": 0.58,
  "disk": 0.67,
  "load": "0.42 / 0.39 / 0.37",
  "memoryText": "18.6 GB used / 32.0 GB",
  "buffers": "412.0 MB",
  "cached": "8.4 GB",
  "swapText": "0 B used / 2.0 GB",
  "wait": "0.28 ms",
  "disks": [
    { "name": "/", "mid": "39%", "value": "198 GB / 512 GB" }
  ],
  "io": [
    { "name": "nvme0n1", "read": "8.2 MB/s", "write": "1.4 MB/s", "iops": "42.1", "util": "3%" }
  ],
  "nets": [
    {
      "name": "eth0",
      "rx": "142.7 KB/s",
      "tx": "46.2 KB/s",
      "rxTotal": "13.8 GB",
      "txTotal": "4.2 GB",
      "errors": "0"
    }
  ],
  "raid": [
    { "name": "md0", "mid": "clean", "value": "2/2" }
  ]
}
```

## 定制边界

- 视觉改动放在 `page.html` 和 `assets/page.css`。
- 数据映射改动放在 `window.ITHILTIR_LOCAL` 或 `assets/page.js`。
- `/metrics` 和 `/static` 必须保持已记录的 JSON 契约。
- 不要把 `X-Node-Secret` 或 dashboard 凭据放进浏览器代码。
