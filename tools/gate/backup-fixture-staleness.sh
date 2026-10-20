#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

schema_fingerprint() {
  find "$ROOT/internal/store/migrate" -type f -name '*.sql' -print |
    LC_ALL=C sort |
    while IFS= read -r path; do
      relative="${path#"$ROOT/"}"
      printf '%s  ' "$relative"
      shasum -a 256 "$path" | awk '{print $1}'
    done |
    shasum -a 256 |
    awk '{print $1}'
}

case "${1:-}" in
  fingerprint)
    schema_fingerprint
    ;;
  check)
    shift
    metadata=
    current=
    fingerprint=
    while test "$#" -gt 0; do
      case "$1" in
        --metadata) metadata="${2:-}"; shift 2 ;;
        --current-release-ordinal) current="${2:-}"; shift 2 ;;
        --fingerprint) fingerprint="${2:-}"; shift 2 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
      esac
    done
    test -f "$metadata" || { echo "backup fixture seed metadata missing: $metadata" >&2; exit 1; }
    case "$current" in *[!0-9]*|'') echo "current release ordinal must be a non-negative integer" >&2; exit 2 ;; esac
    test -n "$fingerprint" || fingerprint="$(schema_fingerprint)"
    recorded_metadata="$(
      python3 - "$metadata" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    value = json.load(handle)
print(value["schemaFingerprint"], value["releaseOrdinal"])
PY
    )"
    read -r recorded recorded_release <<<"$recorded_metadata"
    if test "$fingerprint" = "$recorded"; then
      exit 0
    fi
    drift=$((current - recorded_release))
    if test "$drift" -gt 1; then
      echo "backup fixture seed schema drift persisted for $drift releases (maximum 1); regenerate the seed fixture" >&2
      exit 1
    fi
    echo "backup fixture seed schema changed; regeneration is required by the next release" >&2
    ;;
  *)
    echo "usage: $0 fingerprint | check --metadata FILE --current-release-ordinal N [--fingerprint SHA256]" >&2
    exit 2
    ;;
esac
