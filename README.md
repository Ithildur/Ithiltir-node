# Ithiltir-node

[中文](README_CN.md)

Node metrics agent with two modes:

- `serve`: run the local node page
- `push`: post reports to a dashboard and keep a local cached report

## Modes

### Serve

```bash
./node
./node serve [listen_ip] [listen_port] [--net iface1,iface2] [--debug]
```

- Default listen: `0.0.0.0:9100`
- Env override: `NODE_HOST`, `NODE_PORT`
- Page: `GET /` or `GET /serve`
- Metrics endpoint: `GET /metrics`
- Static hardware endpoint: `GET /static`
- Page override: set `ITHILTIR_NODE_SERVE_PAGE_DIR`, or place `servepage/` next to the binary; public assets live under `servepage/assets/`

### Push

```bash
./node push [interval_seconds] [--net iface1,iface2] [--debug] [--require-https]
```

- Report targets are read from `/var/lib/ithiltir-node/report.yaml` on Linux/macOS and `%ProgramData%\Ithiltir-node\report.yaml` on Windows
- Override the config path with `ITHILTIR_NODE_REPORT_CONFIG`
- Each target URL is the dashboard metrics endpoint and receives `X-Node-Secret: <key>`
- If a target URL ends with `/metrics`, static metadata is posted to the sibling `/static` URL
- Local endpoint: `GET http://127.0.0.1:${NODE_PORT:-9100}/`
- HTTPS targets can fall back to HTTP unless `--require-https` is set

Report target commands:

```bash
./node report install <url> <key>
./node report remove <id>
./node report update <id> <key>
./node report list
```

Use `report install` from install scripts. The URL must point at the dashboard `/metrics` endpoint. The command reads the dashboard server identity before writing `report.yaml`; rerunning the same install is a no-op, and a different target with the same `server_install_id` prompts which one to keep.
Use `report update` only to rotate an existing target key; URL changes go through `report install`.
The config file keeps `version` and `targets`; each target has `id`, `url`, `key`, and optional `server_install_id`.
Writes are atomic and keep file mode `0600`.

### Version

```bash
./node --version
./node -v
```

## Build

Build config lives in [`.goreleaser.yaml`](.goreleaser.yaml).

Version format:

```text
MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]
```

- Strict SemVer, without a `v` prefix.
- Normal releases are only `x.x.x` or `x.x.x+build`.
- Any version with a pre-release part such as `x.x.x-rc.1` or `x.x.x-rc.1+build` is a GitHub pre-release.
- CI rejects invalid SemVer tags before publishing.

Linux/macOS:

```bash
./scripts/build.sh --version 1.2.3-alpha.1
./scripts/build.sh --use-git-tag
./scripts/build.sh --use-git-tag --release
```

Windows:

```powershell
.\scripts\build.ps1 -Version 1.2.3-alpha.1
.\scripts\build.ps1 -UseGitTag
.\scripts\build.ps1 -UseGitTag -Release
```

- Output directory:

```text
build/
  linux/
    node_linux_amd64
    node_linux_arm64
  macos/
    node_macos_arm64
  windows/
    node_windows_amd64.exe
    node_windows_arm64.exe
    runner_windows_amd64.exe
    runner_windows_arm64.exe
```

- GitHub Release title is the version tag. Assets are plain binaries named `Ithiltir-node-<os>-<arch>` and `Ithiltir-runner-<os>-<arch>`; Windows keeps `.exe`, and checksums are uploaded separately
- The scripts install GoReleaser `v2.15.2` if it is missing

## Docs

- Reporting API: [English](docs/reporting_apis.md), [中文](docs/reporting_apis_CN.md)
- Serve page API: [English](docs/serve_page_api.md), [中文](docs/serve_page_api_CN.md)
- Disk schema: [English](docs/api_disk.md), [中文](docs/api_disk_CN.md)

## Layout

```text
cmd/node         entry point
internal/app     mode dispatch and lifecycle
internal/cli     flag parsing
internal/collect samplers and platform collectors
internal/metrics runtime and static JSON types
internal/push    push client
internal/reportcfg report target config
internal/server  HTTP handlers
scripts/         build scripts
build/           generated artifacts
```

## License

Ithiltir-node is licensed under the GNU Affero General Public License v3.0 only. See [LICENSE](LICENSE).
