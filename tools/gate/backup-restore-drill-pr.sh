#!/usr/bin/env bash
# backup-restore-drill-pr.sh — the merge-blocking, PR-scoped restore drill step.
#
# BKUP-CHAIN-1-RESIDUAL-1 r2 gap 3: the trigger selector could only ever be invoked by
# hand — nothing computed this lane's real changed-path list and fed it in, so the restore
# drill never actually blocked a merge. backup-restore-drill-scope.sh computes that list;
# backup-restore-drill.sh's own trigger selection (tools/gate/backup-restore-trigger.sh)
# decides pr-scope vs "none" from it, so an unrelated lane exits in well under a second and
# a matching lane pays the bounded 3-fixture PR drill, never the nightly full corpus (that
# stays release-gate-only via `make backup-restore-drill`).
#
# It is a script (not an inline `bash -c` block) so the gate registers it with a single-line
# command in BOTH the preflight lane and the full gate — gate scope selftest rule 2 matches
# the two registrations by that exact command.
set -euo pipefail
args=(--mode pr)
while IFS= read -r path; do
  [ -n "$path" ] && args+=(--changed-file "$path")
done < <(tools/gate/backup-restore-drill-scope.sh)
exec tools/gate/backup-restore-drill.sh "${args[@]}"
