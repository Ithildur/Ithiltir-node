# Ithiltir-node

[English](README.md)

节点指标采集器，只有两种模式：

- `serve`：运行本地节点页面
- `push`：向面板上报，同时保留本地缓存结果

## 模式

### Serve

```bash
./node
./node serve [listen_ip] [listen_port] [--net iface1,iface2] [--debug]
```

- 默认监听：`0.0.0.0:9100`
- 环境变量覆盖：`NODE_HOST`、`NODE_PORT`
- 页面：`GET /` 或 `GET /serve`
- 指标接口：`GET /metrics`
- 静态硬件接口：`GET /static`
- 页面覆盖：设置 `ITHILTIR_NODE_SERVE_PAGE_DIR`，或把 `servepage/` 放在二进制同级目录；公开资源放在 `servepage/assets/`

### Push

```bash
./node push [interval_seconds] [--net iface1,iface2] [--debug] [--require-https]
```

- Linux/macOS 默认读取 `/var/lib/ithiltir-node/report.yaml`，Windows 默认读取 `%ProgramData%\Ithiltir-node\report.yaml`
- 可用 `ITHILTIR_NODE_REPORT_CONFIG` 覆盖配置路径
- 每个 target URL 指向 dashboard 指标接口，并携带 `X-Node-Secret: <key>`
- target URL 以 `/metrics` 结尾时，静态元数据会发到同级 `/static` URL
- 本地接口：`GET http://127.0.0.1:${NODE_PORT:-9100}/`
- HTTPS target 默认可回落 HTTP；加 `--require-https` 后禁止回落

上报目标命令：

```bash
./node report install <url> <key>
./node report remove <id>
./node report update <id> <key>
./node report list
```

安装脚本使用 `report install`。URL 必须指向 dashboard 的 `/metrics` 接口。命令会先读取 dashboard 服务端身份，再写入 `report.yaml`；重复执行相同 install 不会改配置，相同 `server_install_id` 但目标不同才会提示选择保留哪一个。
`report update` 只用于轮换已有 target 的 key；URL 修改必须走 `report install`。
配置文件包含 `version` 和 `targets`；每个 target 有 `id`、`url`、`key`，以及可选 `server_install_id`。
写入使用原子 rename，并保持文件权限 `0600`。

### Version

```bash
./node --version
./node -v
```

## 构建

构建配置在 [`.goreleaser.yaml`](.goreleaser.yaml)。

版本格式：

```text
MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]
```

- 严格 SemVer，不使用 `v` 前缀。
- 只有 `x.x.x` 或 `x.x.x+build` 是普通发布。
- 任何带预发布段的版本，如 `x.x.x-rc.1` 或 `x.x.x-rc.1+build`，都是 GitHub pre-release。
- CI 会在发布前拒绝非法 SemVer tag。

Linux/macOS：

```bash
./scripts/build.sh --version 1.2.3-alpha.1
./scripts/build.sh --use-git-tag
./scripts/build.sh --use-git-tag --release
```

Windows：

```powershell
.\scripts\build.ps1 -Version 1.2.3-alpha.1
.\scripts\build.ps1 -UseGitTag
.\scripts\build.ps1 -UseGitTag -Release
```

- 输出目录：

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

- GitHub Release 标题是版本 tag。产物是裸二进制，命名为 `Ithiltir-node-<os>-<arch>` 和 `Ithiltir-runner-<os>-<arch>`；Windows 保留 `.exe`，checksums 单独上传
- 脚本会在缺失时自动安装 GoReleaser `v2.15.2`

## 文档

- 上报接口：[English](docs/reporting_apis.md)，[中文](docs/reporting_apis_CN.md)
- Serve 页面 API：[English](docs/serve_page_api.md)，[中文](docs/serve_page_api_CN.md)
- 磁盘结构：[English](docs/api_disk.md)，[中文](docs/api_disk_CN.md)

## 目录

```text
cmd/node         入口
internal/app     模式分发和生命周期
internal/cli     参数解析
internal/collect 采样器和平台采集逻辑
internal/metrics 运行时与静态 JSON 结构
internal/push    推送客户端
internal/reportcfg 上报目标配置
internal/server  HTTP 处理器
scripts/         构建脚本
build/           生成产物
```

## 许可证

Ithiltir-node 使用 GNU Affero General Public License v3.0 only 授权。详见 [LICENSE](LICENSE)。
