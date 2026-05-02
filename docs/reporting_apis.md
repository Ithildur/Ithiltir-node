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
| Push debug local | `/` | `GET` | `NodeReport` | `200` | Only enabled with `push --debug`; bound to `127.0.0.1:${NODE_PORT:-9101}`. Returns the last successfully pushed report when available, otherwise the current snapshot. |

Local `GET` routes also accept `HEAD`. Other methods return `405` with `Allow: GET, HEAD`.

## Wire Conventions

- JSON is UTF-8.
- Timestamps are UTC RFC3339.
- Byte and packet counters are raw numeric counters.
- `*Ratio` fields are `0..1`, not percentages.
- Arrays are returned as `[]`, not `null`.
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
- HTTPS targets can fall back to HTTP under the client fallback rules.
- `--require-https` rejects non-HTTPS targets and disables HTTP fallback.

Response handling:

- `200 OK` is the only successful response for push target requests. The HTTP status decides target success; response body parsing only controls update staging.
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

- Other top-level JSON fields are ignored; `ok` is not required.
- `update.version`, `update.url`, `update.sha256`, and positive byte `update.size` are required when `update` is present. `update.id` is optional metadata.
- `update.url` must be an absolute `http` or `https` URL with a host. `update.version` must not contain path separators. `update.sha256` is the expected SHA-256 hex digest, and `update.size` must equal the downloaded byte count.
- A node stages self-updates only when launched by the Windows runner (`ITHILTIR_NODE_RUNNER=1`). Direct `node push` runs ignore update manifests. Non-Windows self-update is not supported.
- If multiple targets return update manifests in the same round, all returned manifests must match by `id`, `version`, `url`, `sha256`, and `size`; conflicting manifests are skipped.
- A staged update makes `node push` exit cleanly. The Windows runner verifies the staged file, replaces `%ProgramData%\Ithiltir-node\bin\ithiltir-node.exe`, and restarts the node.
- Invalid JSON, invalid manifests, download failures, size mismatches, and checksum mismatches skip the update and keep reporting.
- Any non-`200` response fails that target for the current round.
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
- Static metadata is posted once on startup.
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
