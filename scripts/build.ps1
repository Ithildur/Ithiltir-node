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

if ($UseGitTag) {
  $Version = Get-GitTag
}

if (-not $Version) {
  throw "缺少版本号。使用 --version 或 --use-git-tag"
}

if ($Release -and -not $UseGitTag) {
  throw "发布模式必须使用 -UseGitTag"
}

$validation = (& go run .\cmd\versioncheck --version $Version 2>&1)
if ($LASTEXITCODE -ne 0) {
  throw (($validation | Out-String).Trim())
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
}

if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}
