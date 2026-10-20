#!/usr/bin/env bash
# ADR-0024 / invariant #7 AGPL firewall gate.
#
# Commerce, account, and entitlement implementation belongs in proprietary
# cloud/client repos, never in the AGPL server. This scans production source
# and config for boundary-crossing symbols while allowing Core's existing
# non-commerce uses such as media-credit "billing" order metadata.
set -euo pipefail

ROOT="${BEAMFALL_GATE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$ROOT"

fail=0

emit() {
  # emit <file> <line> <text>
  echo "AGPL forbidden symbol: $1:$2 - $3" >&2
  fail=1
}

is_scanned_file() {
  case "$1" in
    cmd/*|internal/*|packaging/*|Dockerfile|Makefile) ;;
    *) return 1 ;;
  esac

  case "$1" in
    internal/store/migrate/*) return 1 ;;
    internal/web/app-dist/*) return 1 ;;
    internal/web/app/node_modules/*) return 1 ;;
    *_test.go|*.test.ts|*.test.tsx|*.test.mjs|*.spec.ts|*.spec.tsx) return 1 ;;
    */package-lock.json|*/package.json) return 1 ;;
  esac

  case "$1" in
    *.go|*.ts|*.tsx|*.js|*.mjs|*.sh|*.yml|*.yaml|*.toml|*.json|Dockerfile|Makefile) return 0 ;;
    *) return 1 ;;
  esac
}

strip_line_comments() {
  # Good enough for a grep gate: comments may mention the boundary, but live
  # identifiers and string literals should not.
  sed -E 's://.*$::; s:#.*$::'
}

allowed_match() {
  local file="$1" line="$2"

  # No active allow-exceptions. Plugin repos (beamfall-plugin-*) are separate
  # Go modules in sibling workspace dirs and are not tracked by this repo's
  # git ls-files, so they are out of scope for this gate. If they are ever
  # brought in-scope, re-add allow-exceptions for media-credit billing metadata
  # (Attributes: "billing" in plugin bundle.go files).

  return 1
}

pattern='(entitlement|stripe|premium|payment[-_ ]source|purchase[-_ ]state|beamfall[-_ ]account|account[-_ ](service|portal|deletion|identity|ledger|token|id)|Account(Service|Portal|Ledger|Token|ID|Id)|billing|billing[-_ ](portal|system|service|ledger|provider|integration|webhook)|checkout[-_ ](entry|flow|session|portal)|web[-_ ]checkout|license[-_ ]key|licensed[-_ ]feature|licensing[-_ ](code|logic)|license[-_ ]check)'

while IFS= read -r file; do
  is_scanned_file "$file" || continue
  [[ -f "$file" ]] || continue

  while IFS=: read -r line_no text; do
    [[ -n "${line_no:-}" ]] || continue
    if allowed_match "$file" "$text"; then
      continue
    fi
    emit "$file" "$line_no" "$text"
  done < <(strip_line_comments <"$file" | grep -inE "$pattern" || true)
done < <(git ls-files | LC_ALL=C sort)

# XCONTRACT-9 / ADR-0122 Amendment A §4: the AGPL<->proprietary firewall, machine
# enforced on the core side. The symbol scan above catches proprietary *concepts*
# written into this repo; it cannot catch the other direction — this repo taking a
# build-time dependency ON a proprietary sibling module. `github.com/beamfall/cloud`
# (beamfall-cloud) and `github.com/beamfall/relay` (beamfall-relay) are both
# all-rights-reserved modules. Core speaks the relay tunnel through its own
# re-implementation (internal/remoteaccess/relayproto.go) plus a copied golden-vector
# file precisely so that edge never exists; this pass keeps it that way.
#
# Scanned surfaces are the ones that can create the edge: the module manifests and
# every tracked Go source file. Comments are stripped first, so prose may name the
# repos (the ADRs do) while a live require/import may not.
proprietary_module_pattern='github\.com/beamfall/(cloud|relay)([/"[:space:]]|$)'

emit_module() {
  # emit_module <file> <line> <text>
  echo "AGPL proprietary module reference: $1:$2 - $3" >&2
  fail=1
}

scan_for_proprietary_modules() {
  # scan_for_proprietary_modules <file>
  local file="$1" line_no text
  [[ -f "$file" ]] || return 0

  while IFS=: read -r line_no text; do
    [[ -n "${line_no:-}" ]] || continue
    emit_module "$file" "$line_no" "$text"
  done < <(strip_line_comments <"$file" | grep -nE "$proprietary_module_pattern" || true)
}

for manifest in go.mod go.sum; do
  scan_for_proprietary_modules "$manifest"
done

while IFS= read -r file; do
  scan_for_proprietary_modules "$file"
done < <(git ls-files '*.go' | LC_ALL=C sort)

if [[ "$fail" -ne 0 ]]; then
  echo "==> AGPL forbidden-symbol gate FAILED (ADR-0024 / invariant #7)" >&2
  exit 1
fi

echo "agpl-forbidden-symbols gate: clean"
