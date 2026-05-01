# Serve Page API

This document covers the built-in single-node page served by Ithiltir-node in Serve mode.

Code of record:

- local HTTP handlers: [`internal/server/server.go`](../internal/server/server.go)
- page shell: [`internal/server/servepage/page.html`](../internal/server/servepage/page.html)
- page runtime: [`internal/server/servepage/assets/page.js`](../internal/server/servepage/assets/page.js)
- page styles: [`internal/server/servepage/assets/page.css`](../internal/server/servepage/assets/page.css)
- JSON payloads: [reporting_apis.md](reporting_apis.md)

## Routes

| Path | Method | Response | Notes |
| --- | --- | --- | --- |
| `/` | `GET` | HTML | Serve page. Sends `Cache-Control: no-store`. |
| `/serve` | `GET` | HTML | Alias for `/`. |
| `/serve-assets/<name>` | `GET` | file | Serves files under the selected `servepage/assets/` directory. |
| `/metrics` | `GET` | `NodeReport` | Returns `503` before the first sample. |
| `/static` | `GET` | `Static` | Returns `503` before static data is ready. |

Local `GET` routes also accept `HEAD`. Other methods return `405` with `Allow: GET, HEAD`.

Asset rules:

- `/serve-assets/` does not list directories.
- Invalid asset paths, missing files, and directories return `404`.
- Assets outside `servepage/assets/` are not served.

## Page Resolution

The page source is selected when the HTTP server starts:

1. If `ITHILTIR_NODE_SERVE_PAGE_DIR` is set and contains `page.html`, use that directory.
2. If `ITHILTIR_NODE_SERVE_PAGE_DIR` is set but does not contain `page.html`, log the problem and use the embedded page.
3. If the environment variable is not set and `servepage/page.html` exists next to the binary, use that directory.
4. Otherwise use the embedded page.

External layout:

```text
node
servepage/
  page.html
  assets/
    page.css
    page.js
```

Example:

```bash
ITHILTIR_NODE_SERVE_PAGE_DIR=/opt/ithiltir-node/servepage ./node serve
```

`page.html` is required for an external page. `assets/` is optional, but any file referenced through `/serve-assets/*` must live under `assets/`.

## Frontend Runtime

The default page defines `window.ITHILTIR_SERVE`, and `page.js` merges that object over its own defaults:

```js
window.ITHILTIR_SERVE = {
  endpoint: "/metrics",
  staticEndpoint: "/static",
  mode: "node-report",
  pollMs: 5000
};
```

| Field | Default | Meaning |
| --- | --- | --- |
| `endpoint` | `/metrics` | Runtime data endpoint. |
| `staticEndpoint` | `/static` | Static hardware endpoint. Empty value disables static fetches. |
| `mode` | `node-report` | `node-report` adapts `NodeReport`; `serve-view` renders a view model directly. |
| `pollMs` | `5000` | Poll interval. Values below `1000` are clamped to `1000`. |

Mode behavior:

- `node-report`: `endpoint` returns `NodeReport`; `staticEndpoint` returns `Static`; `page.js` adapts both through `toView(report, stat)`.
- `serve-view`: `endpoint` returns the view model shown below; `staticEndpoint` is ignored.

## Default Mapping

Adapter: [`internal/server/servepage/assets/page.js`](../internal/server/servepage/assets/page.js), `toView(report, stat)`.

| UI item | Source |
| --- | --- |
| hostname | `report.hostname` |
| cpu | `stat.cpu.info.model_name`, fallback `vendor_id` |
| cores | `stat.cpu.info.cores_physical`, `cores_logical` |
| memory / swap | `stat.memory.total`, `swap_total`; runtime memory is used as fallback |
| system / kernel | `stat.system` |
| uptime | `report.metrics.system.uptime` |
| load | `report.metrics.cpu.load1/load5/load15` |
| processes | `report.metrics.processes.process_count` |
| tcp / udp | `report.metrics.connections` |
| cpu% | `report.metrics.cpu.usage_ratio` |
| memory% | `report.metrics.memory.used_ratio` |
| disk% | max `used_ratio` from `report.metrics.disk.filesystems[]` and `logical[]` |
| memory detail | `report.metrics.memory` |
| df | `filesystems[]` if present, otherwise `logical[]` |
| iostat | `base_io[]` if present, otherwise `physical[]` |
| network | `report.metrics.network[]` |
| raid | `report.metrics.raid` |

Display rules:

- `*Ratio` is `0..1`.
- Network has no percentage.
- Disk rows show `mount / used% / used / total`.
- Disk total uses runtime `total` when present, then static disk metadata, then `used + free`.
- Disk, IO, network, and RAID lists show at most eight rows.
- Empty or unavailable RAID hides the `cat /proc/mdstat` section.

## `serve-view` Payload

`serve-view` is for callers that want to reuse the page shell but supply display-ready values directly. String fields are rendered as-is. `cpu`, `memory`, and `disk` are ratios in `0..1`.

```js
window.ITHILTIR_SERVE = {
  endpoint: "/api/serve/view",
  mode: "serve-view",
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

## Customization Boundaries

- Visual changes belong in `page.html` and `assets/page.css`.
- Data mapping changes belong in `window.ITHILTIR_SERVE` or `assets/page.js`.
- Keep `/metrics` and `/static` as their documented JSON contracts.
- Do not put `X-Node-Secret` or dashboard credentials in browser code.
