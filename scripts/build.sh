#!/usr/bin/env bash
set -euo pipefail

version=""
use_git_tag="false"
release_mode="false"
goreleaser_version="v2.15.2"

get_git_tag() {
  if [[ "${GITHUB_REF_TYPE:-}" == "tag" && -n "${GITHUB_REF_NAME:-}" ]]; then
    printf '%s\n' "$GITHUB_REF_NAME"
    return
  fi

  local tags
  tags="$(git tag --points-at HEAD 2>/dev/null | sed '/^$/d' || true)"
  local count
  count="$(printf '%s\n' "$tags" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [[ "$count" != "1" ]]; then
    echo "当前提交必须且只能有一个git tag" >&2
    if [[ -n "$tags" ]]; then
      printf '%s\n' "$tags" >&2
    fi
    return 1
  fi

  printf '%s\n' "$tags"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      version="${2:-}"
      shift 2
      ;;
    --use-git-tag)
      use_git_tag="true"
      shift
      ;;
    --release)
      release_mode="true"
      shift
      ;;
    *)
      echo "unknown arg: $1" >&2
      exit 1
      ;;
  esac
done

gopath_bin="$(go env GOPATH)/bin"
export PATH="$gopath_bin:$PATH"

if [[ "$use_git_tag" == "true" ]]; then
  if ! version="$(get_git_tag)"; then
    exit 1
  fi
fi

if [[ -z "$version" ]]; then
  echo "缺少版本号。使用 --version 或 --use-git-tag" >&2
  exit 1
fi

if [[ "$release_mode" == "true" && "$use_git_tag" != "true" ]]; then
  echo "发布模式必须使用 --use-git-tag" >&2
  exit 1
fi

if [[ "$release_mode" == "true" ]]; then
  dirty="$(git status --porcelain)"
  if [[ -n "$dirty" ]]; then
    echo "发布模式要求干净工作区" >&2
    printf '%s\n' "$dirty" >&2
    exit 1
  fi
fi

export GOFLAGS="-trimpath"
export VERSION="$version"

if ! command -v goreleaser >/dev/null 2>&1; then
  echo "GoReleaser not found, installing ${goreleaser_version}..." >&2
  cd ./tools
  go install github.com/goreleaser/goreleaser/v2@"${goreleaser_version}"
  cd ..
fi

if [[ "$release_mode" == "true" ]]; then
  goreleaser release --clean
else
  goreleaser build --snapshot --clean
fi
