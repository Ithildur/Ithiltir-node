# Reporting API

This document is the wire contract between Ithiltir-node and a dashboard.

Code of record:

- runtime payload: [`internal/metrics/types.go`](../internal/metrics/types.go)
- static payload: [`internal/metrics/static_types.go`](../internal/metrics/static_types.go)
- local HTTP handlers: [`internal/server/server.go`](../internal/server/server.go)
- push client: [`internal/push/push.go`](../internal/push/push.go)
- report config: [`internal/reportcfg/config.go`](../internal/reportcfg/config.go)
- self-update staging: [`internal/selfupdate/update.go`](../internal/selfupdate/update.go)

## HTTP Surface

The `/api/node/*` endpoints are dashboard endpoints. Ithiltir-node calls them in Push mode; it does not serve them.

| Surface | Path | Method | Payload | Success | Notes |
| --- | --- | --- | --- | --- | --- |
| Local page | `/` | `GET` | HTML | `200` | Built-in single-node page. See [local_page_api.md](local_page_api.md). |
| Local page | `/local` | `GET` | HTML | `200` | Alias for `/`. |
| Local page | `/metrics` | `GET` | `NodeReport` | `200` | Returns `503` before the first snapshot. |
| Local page | `/static` | `GET` | `Static` | `200` | Returns `503` before static data is ready. |
| Push target | `/api/node/metrics` | `POST` | `NodeReport` | `200` | Requires `X-Node-Secret`. |
| Push target | `/api/node/static` | `POST` | `Static` | `200` | Requires `X-Node-Secret`. Derived from a `/metrics` target URL. |
| Push target | `/api/node/identity` | `POST` | `{}` | `200` | Requires `X-Node-Secret`. Returns `{ "install_id": "...", "created": true/false }`. |
| Push target | `/api/node/virt` | `POST` | VM snapshot | `204` | Requires `X-Node-Secret` and a configured PVE helper socket. No update manifest. |
| Push debug local | `/` | `GET` | `NodeReport` | `200` | Only enabled with `push --debug`; bound to `127.0.0.1:${NODE_PORT:-9101}`. Returns the last successfully pushed report when available, otherwise the current snapshot. |

Local `GET` routes also accept `HEAD`. Other methods return `405` with `Allow: GET, HEAD`.

## Wire Conventions

- JSON is UTF-8.
- Timestamps are UTC RFC3339.
- Byte and packet counters are raw numeric counters.
- `*Ratio` fields are `0..1`, not percentages.
- Host-report arrays are returned as `[]`, not `null`. VM reports use `vms: null` when no successful inventory is known; `vms: []` means a successful empty inventory.
- Optional fields with no value are omitted.
- Runtime disk and static disk are different payloads. Do not mix them; see [api_disk.md](api_disk.md).

## Push Targets

A report target URL is the dashboard metrics endpoint, usually:

```text
https://dashboard.example/api/node/metrics
```

The agent sends the same `NodeReport` to every configured target in a collection round. One failed target does not block the others.

Target URL rules:

- `POST <target URL>` receives runtime metrics.
- If the target path ends with `/metrics`, static metadata is posted to the sibling `/static` URL.
- `report install <url> <key>` requires a target URL ending in `/metrics`; it calls the sibling `/identity` URL before writing local config.
- `report update <id> <key>` only rotates the target key. URL changes go through `report install`.

Transport rules:

- `http` and `https` target URLs are valid.
- In `http` transport mode, HTTPS targets can fall back to HTTP under the client fallback rules. `grpc` and `auto` never authorize TLS-to-plaintext fallback.
- `--require-https` rejects non-HTTPS targets and disables HTTP fallback.

Response handling:

- Host metrics, static data, and identity HTTP requests require `200 OK`; VM reporting to `/api/node/virt` requires `204 No Content`. Metrics response body parsing only controls update staging.
- `/api/node/metrics` may return non-JSON content, an empty body, or JSON. Update manifests are parsed only when `Content-Type` is `application/json`; media type parameters such as `charset=utf-8` are accepted.
- JSON responses may include an optional `update` manifest:

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

- Other top-level JSON fields are ignored.
- `update.version`, `update.url`, `update.sha256`, and positive byte `update.size` are required when `update` is present. `update.id` is optional metadata.
- `update.url` must be an absolute `http` or `https` URL with a host. `update.version` must be a single release directory name, not `.` or `..`, and must not contain path separators. `update.sha256` is the expected SHA-256 hex digest, and `update.size` must equal the downloaded byte count.
- When downloading `update.url`, the node sends the current target key as `X-Node-Secret`. Do not put the key in the URL or query string.
- Windows self-updates require the Windows runner (`ITHILTIR_NODE_RUNNER=1`). Linux and macOS self-updates require the installed release layout under `/var/lib/ithiltir-node/releases`. Direct binaries outside the installed layout do not apply update manifests; the push loop reports self update as disabled and keeps running.
- Manifests whose `version` matches the node's current reported version are ignored before conflict checks. If multiple remaining targets return update manifests in the same round, all returned manifests must match by `id`, `version`, `url`, `sha256`, and `size`; conflicting manifests are skipped.
- A staged Windows update makes `node push` exit cleanly. The Windows runner verifies the staged file, replaces `%ProgramData%\Ithiltir-node\bin\ithiltir-node.exe`, and restarts the node. Linux and macOS switch `/var/lib/ithiltir-node/current` to the downloaded release, then exec the updated node with the same args and environment.
- Invalid JSON, invalid manifests, download failures, size mismatches, and checksum mismatches skip the update and keep reporting.
- Any non-`200` host metrics response fails that target for the current round. VM delivery uses its independent success and retry rules.
- `/api/node/identity` must return JSON with `install_id`; `created` is optional behavior metadata.

## Report Config

Default config path:

- Linux/macOS: `/var/lib/ithiltir-node/report.yaml`
- Windows: `%ProgramData%\Ithiltir-node\report.yaml`

Override with `ITHILTIR_NODE_REPORT_CONFIG`.

Missing config files and empty `targets` start normally and skip reporting. Malformed config fails startup.

```yaml
version: 1
targets:
  - id: 1
    url: https://dashboard.example/api/node/metrics
    key: node-secret
    server_install_id: dashboard-install-id
```

Writes are atomic and keep file mode `0600`.

## Runtime Payload

Top-level object: `NodeReport`

```json
{
  "version": "...",
  "hostname": "...",
  "timestamp": "...",
  "metrics": {}
}
```

- `timestamp`: UTC RFC3339
- `metrics`: `Snapshot`

`Snapshot` fields:

- `cpu`
  - `usage_ratio`, `load1`, `load5`, `load15`, `times`
  - `times`: `user`, `system`, `idle`, `iowait`, `steal`
- `memory`
  - `used`, `available`, `buffers`, `cached`, `used_ratio`
  - `swap_used`, `swap_free`, `swap_used_ratio`
- `disk`
  - see [api_disk.md](api_disk.md)
- `network[]`
  - `name`
  - `bytes_recv`, `bytes_sent`
  - `recv_rate_bytes_per_sec`, `sent_rate_bytes_per_sec`
  - `packets_recv`, `packets_sent`
  - `recv_rate_packets_per_sec`, `sent_rate_packets_per_sec`
  - `err_in`, `err_out`, `drop_in`, `drop_out`
- `system`
  - `alive`, `uptime_seconds`, `uptime`
- `processes`
  - `process_count`
- `connections`
  - `tcp_count`, `udp_count`
- `pressure`
  - `cpu`, `memory`, `io`
  - each resource has `status` and optional `some`, `full`
  - `some` / `full`: `avg10`, `avg60`, `avg300`, `total`
- `raid`
  - `supported`, `available`, `arrays[]`
  - `arrays[]`: `name`, `status`, `active`, `working`, `failed`, `health`, `members`, `sync_status?`, `sync_progress?`
  - `members[]`: `name`, `state`
- `thermal`
  - `status`, `sensors[]`
  - optional: `updated_at`
  - `sensors[]`: `kind`, `name`, `sensor_key`, `source`, `status`
  - optional: `temp_c`, `high_c`, `critical_c`
  - `kind`: `cpu`, `gpu`, `chipset`, `board`, `acpi`, or `unknown`
  - `temp_c`, `high_c`, and `critical_c` are omitted when unavailable

## Static Payload

Top-level object: `Static`

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

Static push behavior:

- Static metadata has no outer wrapper object.
- `report_interval_seconds` is required.
- Static metadata is posted on startup and posted again when the static snapshot changes.
- Partial static collection is retried until complete.
- Static metadata is sent again after a suppressed push failure recovers.

`Static` fields:

- `cpu.info`
  - `model_name`, `vendor_id`, `sockets`, `cores_physical`, `cores_logical`, `frequency_mhz`
- `memory`
  - `total`, `swap_total`
- `disk`
  - see [api_disk.md](api_disk.md)
- `system`
  - `hostname`, `os`, `platform`, `platform_version`, `kernel_version`, `arch`
- `raid`
  - `supported`, `available`, `arrays[]`
  - `arrays[]`: `name`, `level`, `devices`, `members[]`
  - `members[]`: `name`

## Virtual machine snapshots

Set `ITHILTIR_NODE_PVE_SOCKET` for `node push` to enable independent VM delivery. Each configured target ending in `/metrics` also receives `POST` to the sibling `/virt`, using the same `X-Node-Secret`. No VM fields are added to the host report. The task sends new snapshots, retries failures with bounded backoff, and retains only the latest sample. It does not follow HTTP redirects. `--require-https` applies to this path too.

The helper keeps per-VM observations in memory. Eight workers rotate through due VMs; each VM is sampled at most once per three seconds, with no overlapping query for the same VM. Each observation has a two-second deadline, and a completed VM becomes readable immediately without waiting for other VMs or a whole-round deadline. Only QMP status `running` permits dynamic metrics and IPs; other VMs retain configuration and state, and templates are excluded. CPU uses process-counter deltas normalized to vCPUs and the host CPU count, independently of helper CPU affinity. Memory follows PVE cgroup/balloon semantics; disk and tap-network bytes are cumulative counters. Configuration is refreshed every three seconds. Disk capacity uses a separate PVE query every 60 seconds and expires after two minutes. Node reads the current snapshot from the helper Unix socket on its existing three-second VM delivery cadence. Unavailable metrics remain absent. PVE RRD history remains available through the helper; LXC and VM control are not included.

The helper's version 1 snapshot is the report body: `schema: 1`, `provider: "pve"`, `host`, `collected_at`, optional `last_success_at`, `ttl_seconds: 90`, `status` (`ok` or `error`), optional `error`, and `vms`. Successful samples require an array, including `[]` for an empty inventory. Failures preserve the previous successful inventory/time when available. An unavailable helper generates an error report; previous observation timestamps are preserved. Dash computes staleness at read time.

Each VM contains `id`, `name`, `status`, `template`; optional fields are `qmp_status`, `cpus`, `cpu_ratio`, `memory_bytes`, `memory_limit_bytes`, `disk_capacity_bytes`, `disk_read_bytes`, `disk_write_bytes`, `net_in_bytes`, `net_out_bytes`, `net_rate_limits_mbps`, `uptime_seconds`, `ips`. Missing metrics mean unknown. CPU uses consecutive `/proc` process counters normalized by elapsed time and the VM vCPU count; the first sample is unknown. Memory uses cgroup usage minus file cache, overridden by balloon total minus free memory when available; disk capacity follows low-frequency PVE `maxdisk`; network and disk bytes are cumulative counters. Only local QEMU VMs are included, including stopped VMs but excluding templates. Dynamic metrics and IPs require QMP status `running`.

Each VM independently carries optional `collected_at` and `ttl_seconds` (the helper uses 90 seconds). These describe the VM observation, not the time the enclosing snapshot was assembled. Missing observation metadata means live metrics are unknown. The helper and Dash omit expired CPU, memory usage, disk/network counters and uptime and expose the VM state as `unknown`, while retaining its original observation timestamp and configuration. IP freshness remains independent.

Optional `net_rate_limits_mbps` maps NIC names (such as `net0`) to PVE-configured rate limits as JSON numbers in Mbps (1 Mbps = 1,000,000 bit/s), rounded to two decimal places; JSON numbers do not retain trailing zeros. PVE `rate` is converted from MiB/s: `12.5` becomes `104.86`. The same limit applies separately to receive and transmit traffic, not to all VM NICs combined. Zero means no configured limit on that NIC; an absent field means NIC configuration was not collected. Each helper collection reads the current section of local `/etc/pve/qemu-server/<vmid>.conf`, ignoring pending changes and saved snapshots. Invalid NIC rate values are logged and omit this field without stopping basic metrics reporting.

Guest Agent IPs are returned in optional `ips` arrays, up to 1024 unique IPv4/IPv6 addresses per VM, including private addresses and excluding loopback, link-local, unspecified, multicast and invalid addresses. IP collection uses local QGA sockets independently of basic metrics. Only VMs with QMP status `running` and an enabled guest agent are queried. The scheduler checks due work every three seconds, with eight concurrent queries and a three-second total deadline per VM; each round has a ten-second budget and rounds never overlap. Successful queries are eligible again after 60 seconds; failures back off for 1, 2, then 5 minutes. Healthy refreshes have priority, and retries occupy at most two slots. These intervals are minimum delays; saturation can defer a query. Boot or agent configuration changes reset the VM's cached IPs and cooldown. A process lock keeps one helper running; its IP collection loop waits for each round to finish before starting the next. Root-only state in `/run/ithiltir-node/pve-guest` survives helper restarts but is rebuilt after host reboot. Basic collection reads the cached results without waiting for QGA.

Optional `ips_collected_at` and `ips_ttl_seconds` describe the IP sample independently of hot metrics. The helper emits a 900-second IP TTL, preserves the original timestamp on reuse, and omits expired samples and samples known to predate the VM's current boot. An empty successful query may have freshness metadata without `ips`. On query failure, the previous IP sample remains usable only until its own expiry. Clients must check IP freshness separately from snapshot `stale`. Reports without these fields remain accepted, with unknown IP freshness; when provided, the timestamp must be nonzero and no later than the enclosing snapshot's `collected_at`, and TTL must be 1–3600. The IP timestamp may be later than the VM's own `collected_at`.

Limits are 4 MiB, 4096 unique VM IDs, 255-byte names/host, 64-byte states, 1024-byte errors, nonnegative signed 64-bit counters and TTL 1–3600 seconds. Successful reports retain the latest successful inventory time in `last_success_at`; it can precede snapshot assembly time `collected_at`. Reports more than five minutes ahead of Dash are rejected. Dash returns `204` for accepted, duplicate or older samples; only newer samples replace current state. Error responses are `400 invalid_virt`, `401 unauthorized`, `413 body_too_large`, and `503 virt_unavailable`. This endpoint does not deliver updates or refresh host uptime. Older Dash versions may lack it; failures do not stop host reporting.


## Node gRPC transport

The existing HTTP endpoints remain supported. The `ithiltir.node.v1.Node` service shares Dash's existing listener using HTTP/2 over TCP; `app.grpc_port` remains unused. A TLS proxy must forward `/ithiltir.node.v1.Node/` to Dash with gRPC support. The schema source is `protocol/node.proto` in the Dash repository; `bash scripts/generate-node-protocol.sh [node-repository]` synchronizes both repositories with pinned Go generator versions.

Authenticate with `x-node-secret` metadata. `Identify(Empty)` returns `install_id`, `created`, and `protocol_version: 1`. `Metrics(Report)` returns the existing JSON response in `Reply.json`, including the update manifest; `Static(Report)` and `Virt(Report)` return `Empty` after successful processing. `Report.json` carries the existing UTF-8 JSON payload, preserving missing fields and 64-bit integers. Metrics/static remain limited to 1 MiB and VM snapshots to 4 MiB. HTTP and gRPC share validation, receipt deadlines, lifecycle locks, persistence and response construction.

Authentication failures return `UNAUTHENTICATED` and are rate limited by IP; exhausted limits and oversized messages return `RESOURCE_EXHAUSTED`. Invalid JSON/report fields return `INVALID_ARGUMENT`. Acceptance failures map to `UNAVAILABLE` or `INTERNAL` according to the existing error boundary. HTTP status codes and bodies are unchanged. VM snapshot storage failures return `UNAVAILABLE` with `virt_unavailable`; HTTP retains `503 virt_unavailable`.

`Connect` is a separate node-initiated bidirectional query stream. Capabilities must arrive within five seconds. Reconnection replaces the previous session for that node. Credentials are checked before queries and every second while connected. Heartbeats run every 20 seconds with a 60-second peer timeout; they do not refresh metrics or uptime. Queries have a ten-second total budget, at most 32 pending per node and results of at most 2 MiB. Disconnect fails pending work rather than replaying it.

Set `ITHILTIR_NODE_TRANSPORT=http|grpc|auto` on Node; the default is `http` for installation compatibility. `grpc` uses only RPC. `auto` probes `Identify` before reports, fixes successful gRPC negotiation for that target's process lifetime, and can select HTTP on the same URL/scheme when the probe is unavailable or incompatible. Authentication/authorization failure does not select HTTP. Auto/grpc never authorize TLS-to-plaintext fallback; explicit/default HTTP retains its existing fallback behavior and `--require-https` restriction. Node disables gRPC policy retries (gRPC may transparently retry calls known not to have been processed) and never replays an uncertain metrics response over another transport; subsequent rounds read the latest metrics. Existing VM timestamp deduplication remains effective.
