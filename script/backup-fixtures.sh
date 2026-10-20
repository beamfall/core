#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORPUS="$ROOT/testing/backup-fixtures"

if [[ "${BEAMFALL_BACKUP_FIXTURES_SUPERVISED:-0}" != 1 ]]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    BEAMFALL_BACKUP_FIXTURES_SUPERVISED "$0" "$@"
fi

usage() {
  echo "usage: $0 generate VERSION RELEASE_ORDINAL | drill [FIXTURE_DIR] | staleness CURRENT_RELEASE_ORDINAL" >&2
  exit 2
}

case "${1:-}" in
  generate)
    version="${2:-}"
    ordinal="${3:-}"
    test -n "$version" && test -n "$ordinal" || usage
    case "$version" in *[!A-Za-z0-9._-]*|'') echo "invalid fixture version: $version" >&2; exit 2 ;; esac
    case "$ordinal" in *[!0-9]*|'') echo "release ordinal must be a non-negative integer" >&2; exit 2 ;; esac
    final="$CORPUS/$version"
    test ! -e "$final" || { echo "fixture already exists: $final" >&2; exit 1; }
    mkdir -p "$CORPUS"
    staging="$(mktemp -d "$CORPUS/.${version}.XXXXXX")"
    trap 'rm -rf "$staging"' EXIT INT TERM
    (
      cd "$ROOT"
      BEAMFALL_BACKUP_FIXTURE_DIR="$staging" go test -count=1 ./internal/store \
        -run '^TestBackupFixtureSeedCoversEveryDurableTable$'
    )
    "$ROOT/tools/gate/backup-fixture-staleness.sh" fingerprint >"$staging/.schema-digest"
    printf '%s\n' "$ordinal" >"$staging/.release-ordinal"
    chmod -R go-rwx "$staging"
    mv "$staging" "$final"
    trap - EXIT INT TERM
    "$0" drill "$final"
    ;;
  drill)
    fixture_dir="${2:-}"
    if test -n "$fixture_dir"; then
      fixtures=("$(cd "$fixture_dir" && pwd)")
    else
      fixtures=()
      while IFS= read -r descriptor; do fixtures+=("$(dirname "$descriptor")"); done < <(
        find "$CORPUS" -mindepth 2 -maxdepth 2 -name fixture.json -type f | LC_ALL=C sort
      )
    fi
    test "${#fixtures[@]}" -gt 0 || { echo "backup fixture corpus is empty; run: make backup-fixture-generate VERSION=<version> RELEASE_ORDINAL=<n>" >&2; exit 1; }
    for fixture in "${fixtures[@]}"; do
      (
        cd "$ROOT"
        BEAMFALL_BACKUP_FIXTURE_DRILL_DIR="$fixture" go test -count=1 ./internal/store \
          -run '^TestBackupFixtureCorpusDrill$'
      )
    done
    ;;
  staleness)
    current="${2:-}"
    test -n "$current" || usage
    "$ROOT/tools/gate/backup-fixture-staleness.sh" check \
      --metadata "$CORPUS/seed-schema.json" \
      --current-release-ordinal "$current"
    ;;
  *)
    usage
    ;;
esac
