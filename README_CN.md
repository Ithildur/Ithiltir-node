# Ithiltir-node

[English](README.md)

节点指标采集器，只有两种模式：

- `serve`：暴露 `GET /metrics`
- `push`：向面板上报，同时保留本地缓存结果

## 模式

### Serve

```bash
./node
./node serve [listen_ip] [listen_port] [--net iface1,iface2] [--debug]
```

- 默认监听：`0.0.0.0:9100`
- 环境变量覆盖：`NODE_HOST`、`NODE_PORT`
- 接口：`GET /metrics`

### Push

```bash
./node push <dash_host> <dash_port> <secret> [interval_seconds] [--net iface1,iface2] [--debug] [--require-https]
```

- 指标目标：`https://<dash_host>:<dash_port>/api/node/metrics`
- 静态目标：`https://<dash_host>:<dash_port>/api/node/static`
- 请求头：`X-Node-Secret: <secret>`
- 本地接口：`GET http://127.0.0.1:${NODE_PORT:-9100}/`
- 默认先走 HTTPS。除非加 `--require-https`，否则允许回落 HTTP

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
- 普通发布 Git tag 示例：`1.2.3`
- 预发布 Git tag 示例：`1.2.3-rc.1`
- CI 会把普通 SemVer tag 发布为普通 GitHub Release，把 `1.2.3-alpha.1` 这类预发布 tag 发布为 GitHub pre-release。

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

- 输出目录：`build/`
- GitHub Release 标题是版本 tag。产物是裸二进制，命名为 `Ithiltir-node-<os>-<arch>`；Windows 保留 `.exe`，checksums 单独上传
- 脚本会在缺失时自动安装 GoReleaser `v2.15.2`

## 文档

- 上报接口：[English](docs/reporting_apis.md)，[中文](docs/reporting_apis_CN.md)
- 磁盘结构：[English](docs/api_disk.md)，[中文](docs/api_disk_CN.md)

## 目录

```text
cmd/node         入口
internal/app     模式分发和生命周期
internal/cli     参数解析
internal/collect 采样器和平台采集逻辑
internal/metrics 运行时与静态 JSON 结构
internal/push    推送客户端
internal/server  HTTP 处理器
scripts/         构建脚本
build/           生成产物
```

## 许可证

Ithiltir-node 使用 GNU Affero General Public License v3.0 only 授权。详见 [LICENSE](LICENSE)。
