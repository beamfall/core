#!/usr/bin/env bash
# audit-npm.sh — run `npm audit` for the embedded web app and FAIL CLOSED.
#
# The baseline (internal/web/app/.audit-baseline) is no longer a permanent
# allow-list of advisory IDs. It is a set of *bounded waivers*, each an object:
#
#   { "id": 1234567, "rationale": "why accepted", "added": "YYYY-MM-DD",
#     "expires": "YYYY-MM-DD" }
#
# The gate fails when ANY of the following holds:
#   * npm is missing or `npm audit` errors / produces no JSON (no silent skip);
#   * a high/critical advisory is present that is NOT covered by a waiver (NEW);
#   * a covered advisory has a fix available (`fixAvailable`) — apply the fix,
#     a waiver is not allowed to hide a fixable advisory;
#   * a waiver has passed its `expires` date (EXPIRED);
#   * a waiver's advisory no longer appears in the audit (STALE — remove it).
#
# Test seams (used by script/audit_npm_test.sh; unset in real runs):
#   AUDIT_NPM_BASELINE   override baseline path
#   AUDIT_NPM_JSON       read audit JSON from this file instead of running npm
#   AUDIT_NPM_NO_NPM=1   simulate npm being unavailable
#   AUDIT_NPM_TODAY      override "today" (YYYY-MM-DD) for expiry checks
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WEB_PREFIX="${REPO_ROOT}/internal/web/app"
BASELINE="${AUDIT_NPM_BASELINE:-${WEB_PREFIX}/.audit-baseline}"

# --- obtain the audit JSON -------------------------------------------------
audit_json=""
if [[ -n "${AUDIT_NPM_JSON:-}" ]]; then
  [[ -f "$AUDIT_NPM_JSON" ]] || { echo "audit-npm: fixture not found: $AUDIT_NPM_JSON" >&2; exit 1; }
  audit_json="$(cat "$AUDIT_NPM_JSON")"
else
  if [[ "${AUDIT_NPM_NO_NPM:-0}" == "1" ]] || ! command -v npm >/dev/null 2>&1; then
    echo "audit-npm: FAIL — npm is not available; cannot run the required audit" >&2
    exit 1
  fi
  # `npm audit` exits non-zero when advisories exist; capture output regardless
  # and let the analyzer below decide pass/fail.
  audit_json="$(npm --prefix "$WEB_PREFIX" audit --audit-level=high --json 2>/dev/null || true)"
fi

if [[ -z "${audit_json//[[:space:]]/}" ]]; then
  echo "audit-npm: FAIL — npm audit produced no output" >&2
  exit 1
fi

# --- analyze ----------------------------------------------------------------
tmp_json="$(mktemp)"
trap 'rm -f "$tmp_json"' EXIT
printf '%s' "$audit_json" > "$tmp_json"

AUDIT_NPM_TODAY="${AUDIT_NPM_TODAY:-$(date -u +%Y-%m-%d)}" \
python3 - "$BASELINE" "$tmp_json" <<'PY'
import datetime as dt
import json
import os
import sys

baseline_path, audit_path = sys.argv[1], sys.argv[2]
today = dt.date.fromisoformat(os.environ["AUDIT_NPM_TODAY"])

try:
    with open(audit_path, encoding="utf-8") as fh:
        audit = json.load(fh)
except Exception as exc:  # malformed audit output is a hard fail
    print(f"audit-npm: FAIL — could not parse npm audit JSON: {exc}", file=sys.stderr)
    raise SystemExit(1)

if isinstance(audit, dict) and "error" in audit:
    print(f"audit-npm: FAIL — npm audit reported an error: {audit['error']}", file=sys.stderr)
    raise SystemExit(1)

# Collect high/critical advisories: id -> fix_available(bool).
current = {}
for vuln in audit.get("vulnerabilities", {}).values():
    if vuln.get("severity") not in ("high", "critical"):
        continue
    fix = bool(vuln.get("fixAvailable"))
    for via in vuln.get("via", []):
        if isinstance(via, dict) and via.get("source") is not None:
            src = int(via["source"])
            current[src] = current.get(src, False) or fix

# Read + validate the bounded-waiver baseline.
try:
    with open(baseline_path, encoding="utf-8") as fh:
        raw = json.load(fh)
except Exception as exc:
    print(f"audit-npm: FAIL — could not parse baseline {baseline_path}: {exc}", file=sys.stderr)
    raise SystemExit(1)

waivers = raw.get("waivers", []) if isinstance(raw, dict) else raw
if not isinstance(waivers, list):
    print("audit-npm: FAIL — baseline `waivers` must be a list", file=sys.stderr)
    raise SystemExit(1)

failures = []
waived_ids = set()
required = ("id", "rationale", "added", "expires")
for i, w in enumerate(waivers):
    if not isinstance(w, dict) or any(k not in w for k in required):
        failures.append(f"waiver[{i}] must have keys {required}; got {w!r}")
        continue
    wid = int(w["id"])
    waived_ids.add(wid)
    try:
        expires = dt.date.fromisoformat(str(w["expires"]))
    except ValueError:
        failures.append(f"waiver id={wid} has an invalid `expires` date: {w['expires']!r}")
        continue
    if expires < today:
        failures.append(f"waiver id={wid} EXPIRED on {expires} (today {today}) — re-triage or fix")
    if wid not in current:
        failures.append(f"waiver id={wid} STALE — advisory no longer present; remove it")

# NEW (unwaived) and fixable advisories.
for src, fix in sorted(current.items()):
    if fix:
        failures.append(f"advisory id={src} has a fix available — apply it (waivers may not hide fixable advisories)")
    elif src not in waived_ids:
        failures.append(f"advisory id={src} is NEW high/critical and unwaived — fix it or add a bounded waiver")

if failures:
    print("audit-npm: FAIL", file=sys.stderr)
    for f in failures:
        print(f"  - {f}", file=sys.stderr)
    raise SystemExit(1)

print(f"audit-npm: ok — {len(current)} high/critical advisories, {len(waived_ids)} bounded waivers, 0 unresolved")
PY
