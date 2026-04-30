param(
  [string]$Version,
  [switch]$UseGitTag,
  [switch]$Release
)

$ErrorActionPreference = "Stop"

function Get-GitTag {
  if ($env:GITHUB_REF_TYPE -eq "tag" -and $env:GITHUB_REF_NAME) {
    return $env:GITHUB_REF_NAME.Trim()
  }

  $tags = @(& git tag --points-at HEAD 2>$null | Where-Object { $_ })
  if ($tags.Count -ne 1) {
    if ($tags.Count -gt 0) {
      Write-Error ($tags -join [Environment]::NewLine)
    }
    throw "当前提交必须且只能有一个git tag"
  }

  return $tags[0].Trim()
}

function Test-SemVer {
  param([string]$Value)

  $pattern = '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-((0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
  return $Value -match $pattern
}

function Package-LocalBuild {
  New-Item -ItemType Directory -Force -Path "build\linux", "build\macos", "build\windows" | Out-Null

  Move-Item -Force "build\node_linux_amd64_v1\node" "build\linux\node_linux_amd64"
  Move-Item -Force "build\node_linux_arm64_v8.0\node" "build\linux\node_linux_arm64"
  Move-Item -Force "build\node_darwin_arm64_v8.0\node" "build\macos\node_macos_arm64"
  Move-Item -Force "build\node_windows_amd64_v1\node.exe" "build\windows\node_windows_amd64.exe"
  Move-Item -Force "build\node_windows_arm64_v8.0\node.exe" "build\windows\node_windows_arm64.exe"

  Remove-Item -Recurse -Force `
    "build\node_linux_amd64_v1", `
    "build\node_linux_arm64_v8.0", `
    "build\node_darwin_arm64_v8.0", `
    "build\node_windows_amd64_v1", `
    "build\node_windows_arm64_v8.0"
  Remove-Item -Force "build\artifacts.json", "build\config.yaml", "build\metadata.json"
}

if ($UseGitTag) {
  $Version = Get-GitTag
}

if (-not $Version) {
  throw "缺少版本号。使用 --version 或 --use-git-tag"
}

if (-not (Test-SemVer $Version)) {
  throw "版本号必须是严格 SemVer，且不能带 v 前缀: $Version。正式发布: x.x.x 或 x.x.x+build；预发布: x.x.x-prerelease 或 x.x.x-prerelease+build"
}

if ($Release -and -not $UseGitTag) {
  throw "发布模式必须使用 -UseGitTag"
}

if ($Release) {
  $dirty = @(& git status --porcelain)
  if ($dirty.Count -ne 0) {
    Write-Error ($dirty -join [Environment]::NewLine)
    throw "发布模式要求干净工作区"
  }
}

$env:GOFLAGS = "-trimpath"
$env:VERSION = $Version
$goBin = (Join-Path ((& go env GOPATH).Trim()) "bin")
$env:PATH = "$goBin$([System.IO.Path]::PathSeparator)$env:PATH"

if (-not (Get-Command goreleaser -ErrorAction SilentlyContinue)) {
  Write-Host "GoReleaser not found, installing v2.15.2..."
  cd .\tools
  go install github.com/goreleaser/goreleaser/v2@v2.15.2
  cd ..
}

if ($Release) {
  & goreleaser release --clean
} else {
  & goreleaser build --snapshot --clean
  if ($LASTEXITCODE -eq 0 -and $env:GITHUB_ACTIONS -ne "true") {
    Package-LocalBuild
  }
}

if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}
