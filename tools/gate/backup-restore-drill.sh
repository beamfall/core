#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

if [[ "${BEAMFALL_BACKUP_RESTORE_DRILL_SUPERVISED:-0}" != 1 ]]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    BEAMFALL_BACKUP_RESTORE_DRILL_SUPERVISED "$0" "$@"
fi

mode="pr"
changed=()
while test "$#" -gt 0; do
  case "$1" in
    --mode) mode="${2:-}"; shift 2 ;;
    --changed-file) changed+=("--changed-file" "${2:-}"); shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if test "${#changed[@]}" -eq 0; then
  selection="$("$ROOT/tools/gate/backup-restore-trigger.sh" --mode "$mode")"
else
  selection="$("$ROOT/tools/gate/backup-restore-trigger.sh" --mode "$mode" "${changed[@]}")"
fi
if test "$selection" = none; then
  echo "backup restore drill not selected"
  exit 0
fi

fixtures=()
while IFS= read -r descriptor; do fixtures+=("$(dirname "$descriptor")"); done < <(
  find "$ROOT/testing/backup-fixtures" -mindepth 2 -maxdepth 2 -name fixture.json -type f | LC_ALL=C sort
)
test "${#fixtures[@]}" -gt 0 || { echo "backup fixture corpus is empty" >&2; exit 1; }

if test "$selection" = "oldest-supported latest-1 latest" && test "${#fixtures[@]}" -gt 3; then
  fixtures=("${fixtures[0]}" "${fixtures[${#fixtures[@]}-2]}" "${fixtures[${#fixtures[@]}-1]}")
fi
for fixture in "${fixtures[@]}"; do
  BEAMFALL_BACKUP_FIXTURES_SUPERVISED=1 \
    "$ROOT/script/backup-fixtures.sh" drill "$fixture"
done

(
  cd "$ROOT"
  go test ./internal/store -run '^TestBackupRestoreDrillDetectsRowChecksumCorruption$' -count=1
)
if test "$selection" = "full crash-injection"; then
  (
    cd "$ROOT"
    go test ./internal/store -run '^TestMigrationSessionSnapshotResumesAfterCrashBetweenDatabases$' -count=1
  )
fi
