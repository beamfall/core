#!/usr/bin/env bash
set -euo pipefail

mode="pr"
paths=()
while test "$#" -gt 0; do
  case "$1" in
    --mode) mode="${2:-}"; shift 2 ;;
    --changed-file) paths+=("${2:-}"); shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if test "$mode" = nightly; then
  echo "full crash-injection"
  exit 0
fi
test "$mode" = pr || { echo "mode must be pr or nightly" >&2; exit 2; }

if test "${#paths[@]}" -eq 0; then
  echo "none"
  exit 0
fi

for path in "${paths[@]}"; do
  case "$path" in
    internal/store/migrate/*|internal/backup/*|cmd/beamfall/backup.go|internal/store/*backup*)
      echo "oldest-supported latest-1 latest"
      exit 0
      ;;
  esac
done
echo "none"
