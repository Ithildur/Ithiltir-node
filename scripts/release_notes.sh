#!/usr/bin/env bash
set -euo pipefail

semver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'

usage() {
  cat >&2 <<EOF
Usage:
  scripts/release_notes.sh TAG OUT_FILE
EOF
  exit 2
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

tag="${1:-}"
out="${2:-}"
[[ -n "$tag" && -n "$out" ]] || usage

die() {
  echo "error: $*" >&2
  exit 1
}

validate_version() {
  [[ "$1" =~ $semver_re ]]
}

release_channel() {
  validate_version "$1" || return 1
  local main="${1%%+*}"
  if [[ "$main" == *-* ]]; then
    echo "prerelease"
  else
    echo "release"
  fi
}

is_numeric_identifier() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

semver_core() {
  local main="${1%%+*}"
  printf '%s\n' "${main%%-*}"
}

semver_prerelease() {
  local main="${1%%+*}"
  if [[ "$main" == *-* ]]; then
    printf '%s\n' "${main#*-}"
  fi
}

compare_number() {
  local a="$1" b="$2"
  if ((${#a} > ${#b})); then echo 1; return; fi
  if ((${#a} < ${#b})); then echo -1; return; fi
  if [[ "$a" > "$b" ]]; then echo 1; return; fi
  if [[ "$a" < "$b" ]]; then echo -1; return; fi
  echo 0
}

compare_prerelease_identifier() {
  local a="$1" b="$2" a_numeric="false" b_numeric="false"
  if is_numeric_identifier "$a"; then a_numeric="true"; fi
  if is_numeric_identifier "$b"; then b_numeric="true"; fi

  if [[ "$a_numeric" == "true" && "$b_numeric" == "true" ]]; then
    compare_number "$a" "$b"
    return
  fi
  if [[ "$a_numeric" == "true" ]]; then echo -1; return; fi
  if [[ "$b_numeric" == "true" ]]; then echo 1; return; fi
  if [[ "$a" > "$b" ]]; then echo 1; return; fi
  if [[ "$a" < "$b" ]]; then echo -1; return; fi
  echo 0
}

compare_prerelease() {
  local a="$1" b="$2" i limit cmp
  local -a a_parts b_parts
  IFS=. read -ra a_parts <<<"$a"
  IFS=. read -ra b_parts <<<"$b"
  limit="${#a_parts[@]}"
  if ((${#b_parts[@]} < limit)); then
    limit="${#b_parts[@]}"
  fi
  for ((i = 0; i < limit; i++)); do
    cmp="$(compare_prerelease_identifier "${a_parts[$i]}" "${b_parts[$i]}")"
    [[ "$cmp" == "0" ]] || { echo "$cmp"; return; }
  done
  if ((${#a_parts[@]} > ${#b_parts[@]})); then echo 1; return; fi
  if ((${#a_parts[@]} < ${#b_parts[@]})); then echo -1; return; fi
  echo 0
}

compare_versions() {
  local a="$1" b="$2" a_core b_core a_pre b_pre a1 a2 a3 b1 b2 b3 cmp pair
  validate_version "$a" || return 2
  validate_version "$b" || return 2

  a_core="$(semver_core "$a")"
  b_core="$(semver_core "$b")"
  IFS=. read -r a1 a2 a3 <<<"$a_core"
  IFS=. read -r b1 b2 b3 <<<"$b_core"
  for pair in "$a1:$b1" "$a2:$b2" "$a3:$b3"; do
    cmp="$(compare_number "${pair%%:*}" "${pair#*:}")"
    [[ "$cmp" == "0" ]] || { echo "$cmp"; return; }
  done

  a_pre="$(semver_prerelease "$a")"
  b_pre="$(semver_prerelease "$b")"
  if [[ -z "$a_pre" && -z "$b_pre" ]]; then echo 0; return; fi
  if [[ -z "$a_pre" ]]; then echo 1; return; fi
  if [[ -z "$b_pre" ]]; then echo -1; return; fi
  compare_prerelease "$a_pre" "$b_pre"
}

version_gt() {
  [[ "$(compare_versions "$1" "$2")" == "1" ]]
}

previous_release_tag() {
  local candidate channel cmp latest=""

  while IFS= read -r candidate; do
    [[ -n "$candidate" && "$candidate" != "$tag" ]] || continue
    channel="$(release_channel "$candidate" 2>/dev/null || true)"
    [[ "$channel" == "release" ]] || continue
    cmp="$(compare_versions "$candidate" "$tag" 2>/dev/null || true)"
    [[ "$cmp" == "-1" ]] || continue
    if [[ -z "$latest" ]] || version_gt "$candidate" "$latest"; then
      latest="$candidate"
    fi
  done < <(git -C "$repo_root" tag --merged "$tag" | sed '/^$/d')

  printf '%s\n' "$latest"
}

write_commits() {
  local previous="$1"
  local range="$tag"
  if [[ -n "$previous" ]]; then
    range="${previous}..${tag}"
  fi

  git -C "$repo_root" log --reverse --abbrev=7 --format='· %h %s' "$range"
}

validate_version "$tag" || die "invalid version tag: $tag"
previous="$(previous_release_tag)"

{
  printf '## Changelog\n\n'
  write_commits "$previous"
} > "$out"

if [[ -n "$previous" ]]; then
  echo "release notes base: $previous" >&2
else
  echo "release notes base: first release" >&2
fi
