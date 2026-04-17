# Reporting API

Code of record:

- runtime payload: [`internal/metrics/types.go`](../internal/metrics/types.go)
- static payload: [`internal/metrics/static_types.go`](../internal/metrics/static_types.go)
- HTTP handlers: [`internal/server/server.go`](../internal/server/server.go)
- push client: [`internal/push/push.go`](../internal/push/push.go)

## Endpoints

| Path | Method | Body | Notes |
| --- | --- | --- | --- |
| `/metrics` | `GET` | `NodeReport` | Serve mode. Returns `503` before the first snapshot. |
| `/api/node/metrics` | `POST` | `NodeReport` | Push target. Header: `X-Node-Secret`. |
| `/api/node/static` | `POST` | `Static` | Push target for static metadata. |
| `/` | `GET` | `NodeReport` | Push-mode local endpoint. Returns the last pushed report if available, otherwise the current snapshot. |

Push starts with HTTPS. It falls back to HTTP unless `--require-https` is set.

## Runtime Payload

Top-level object: `NodeReport`

```json
{
  "version": "...",
  "hostname": "...",
  "timestamp": "...",
  "metrics": { ... }
}
```

- `timestamp`: UTC, RFC3339
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
- `raid`
  - `supported`, `available`, `arrays[]`
  - `arrays[]`: `name`, `status`, `active`, `working`, `failed`, `health`, `members`, `sync_status?`, `sync_progress?`
  - `members[]`: `name`, `state`

## Static Payload

Top-level object: `Static`

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

- No wrapper object
- `report_interval_seconds` is required for publish
- Static payload is posted on startup
- If static collection is still partial, the agent keeps retrying
- After a suppressed push failure recovers, the agent sends static data once more

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

## Rules

- Arrays are returned as `[]`, not `null`
- `*Ratio` fields use `0..1`
- Runtime disk and static disk are different payloads. Do not mix them
