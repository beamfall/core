#!/usr/bin/env bash
# ADR-0115 / ADR-0024 backup firewall.
#
# Backup in AGPL Core is BYO, zero-knowledge, and accountless. Hosted Store,
# entitlement, billing, and Premium ownership decisions stay in proprietary
# cloud/client repos.
set -euo pipefail

ROOT="${BEAMFALL_GATE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$ROOT"

fail=0

emit() {
  echo "backup AGPL firewall leak: $1:$2 - $3" >&2
  fail=1
}

is_scanned_file() {
  case "$1" in
    internal/backup/*|internal/graph/*|cmd/beamfall/backup.go|internal/config/*) ;;
    *) return 1 ;;
  esac

  case "$1" in
    *_test.go|*.test.ts|*.test.tsx|*.test.mjs|*.spec.ts|*.spec.tsx) return 1 ;;
    internal/web/app-dist/*|internal/web/app/node_modules/*|*/package-lock.json|*/package.json) return 1 ;;
  esac

  case "$1" in
    *.go|*.ts|*.tsx|*.js|*.mjs|*.sh|*.yml|*.yaml|*.toml|*.json) return 0 ;;
    *) return 1 ;;
  esac
}

strip_line_comments() {
  sed -E 's://.*$::; s:#.*$::'
}

backup_context_pattern='backup|restore|bbk1|zero[-_ ]?knowledge|replication'

backup_scope_match() {
  local file="$1" line_no="$2" start end
  case "$file" in
    internal/backup/*|cmd/beamfall/backup.go) return 0 ;;
    internal/graph/*|internal/config/*)
      grep -Eiq "$backup_context_pattern" <<<"$file" && return 0
      start=$((line_no > 3 ? line_no - 3 : 1))
      end=$((line_no + 3))
      sed -n "${start},${end}p" "$file" | grep -Eiq "$backup_context_pattern"
      return $?
      ;;
  esac
  return 1
}

# Forbidden-vocabulary base (no `subscription` token — that word is scope-
# dependent, see below).
pattern_base='entitlement|premium|stripe|billing|checkout|purchase[-_ ]state|payment[-_ ]source|beamfall[-_ ]account|account[-_ ](service|portal|deletion|identity|ledger|token|id)|Account(Service|Portal|Ledger|Token|ID|Id)|hosted[-_ ](store|ciphertext|backup)|hosted(Store|Ciphertext|Backup)|license[-_ ]key|licensed[-_ ]feature|licensing[-_ ](code|logic)|license[-_ ]check'

# `subscription` is scope-dependent. In the DEDICATED backup packages
# (internal/backup/*, cmd/beamfall/backup.go — every line is unconditionally in
# backup scope) the bare word stays a hard leak signal: those files must be
# accountless, so any subscription reference is suspicious regardless of shape,
# and a strict match catches forms a finite qualifier list would miss
# (subscriptionID, SubscriptionExpiresAt, TokenShapeSubscription, …).
#
# In the PROXIMITY-scanned dirs (internal/graph/*, internal/config/*) the bare
# word is Beamfall Core's legitimate, non-monetary Library feed-subscription
# feature (RSS/podcast following — addSubscription, associateSubscriptionEntry,
# SubscriptionInfo with FeedURL/GUID-to-Media association). Matching it there
# false-positives on content code, and because backup_scope_match uses a ±3-line
# window an alphabetically-sorted dispatch registry catches a content entry
# adjacent to an unrelated `backup*` entry (the CORE-ARCH-1A graph_dispatch.go
# regression). Genuine billing/entitlement subscription vocabulary is always
# compound/qualified, so those dirs match only qualified shapes. Both scopes are
# pinned by a positive/negative pair in backup-agpl-firewall_test.sh.
subscription_qualified='(billing|premium|entitlement|paid|expired|active|trial|hosted[-_ ]?store|beamfall)[-_ ]?subscription|subscription[-_ ]?(tier|plan|status|renewal|renewed|grace|price|trial|grant|notification|billing|entitlement|receipt|id|provider|expires|cancel[a-z]*)'
pattern_strict="($pattern_base|subscription)"
pattern_qualified="($pattern_base|$subscription_qualified)"

unconditional_backup_file() {
  case "$1" in
    internal/backup/*|cmd/beamfall/backup.go) return 0 ;;
  esac
  return 1
}

while IFS= read -r file; do
  is_scanned_file "$file" || continue
  [[ -f "$file" ]] || continue

  if unconditional_backup_file "$file"; then
    file_pattern="$pattern_strict"
  else
    file_pattern="$pattern_qualified"
  fi

  while IFS=: read -r line_no text; do
    [[ -n "${line_no:-}" ]] || continue
    backup_scope_match "$file" "$line_no" || continue
    emit "$file" "$line_no" "$text"
  done < <(strip_line_comments <"$file" | grep -inE "$file_pattern" || true)
done < <(git ls-files | LC_ALL=C sort)

if [[ "$fail" -ne 0 ]]; then
  echo "==> backup AGPL firewall gate FAILED (ADR-0115 / ADR-0024 / invariant #7)" >&2
  exit 1
fi

echo "backup-agpl-firewall gate: clean"
