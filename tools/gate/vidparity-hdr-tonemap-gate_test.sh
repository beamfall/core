#!/usr/bin/env bash
# Planted-red coverage for the GA hardware HDR tone-mapping launch gate.
#
# A launch gate nobody has ever seen refuse is not a gate. Every case below
# starts from evidence the gate DOES accept and plants exactly one defect, so a
# green result proves the gate discriminates rather than that it is inert.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GATE="$ROOT/tools/gate/vidparity-hdr-tonemap-gate.py"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

failures=0

DIGEST="$(python3 "$GATE" --root "$ROOT" --print-digest)"
LOCKED_SHA="$(sed -n 's/^FFMPEG_LINUX_AMD64_SHA256=//p' "$ROOT/internal/ffmpeg/ffmpeg.lock")"
DARWIN_SHA="$(python3 -c 'import hashlib;print(hashlib.sha256(b"darwin-binary").hexdigest())')"

# green_evidence writes the smallest evidence packet the gate accepts: both
# halves of the 2026-07-25 floor, for both claimed transfer classes.
green_evidence() {
  python3 - "$1" "$DIGEST" "$LOCKED_SHA" "$DARWIN_SHA" <<'PY'
import json
import sys

path, digest, locked, darwin = sys.argv[1:5]


def row(host_os, host_arch, backend, transfer_class, identity):
    return {
        "host_os": host_os, "host_arch": host_arch,
        "backend": backend, "transfer_class": transfer_class,
        "status": "working", "failure": "",
        "ffmpeg_pin_identity": identity,
        "ffmpeg_version": "ffmpeg version 7.1.4-Jellyfin",
        "chain_builder_revision": "0" * 40,
        "driver_hint": "intel-gen12",
        "baseline_highlight": 173.125, "tonemapped_highlight": 122.859,
        "software_ref_highlight": None,
        "thresholds": {"min_highlight_drop": 32, "min_highlight": 64, "origin": "inherited"},
        "passthrough_control": "failed-as-required",
        "probed_at": "2026-07-26T00:00:00Z",
        "approver": "Test Approver", "approved_at": "2026-07-26T00:00:00Z",
    }


payload = {
    "schema_version": 1,
    "certification_scope_digest": digest,
    "rows": [
        row("darwin", "arm64", "videotoolbox", "pq", darwin),
        row("darwin", "arm64", "videotoolbox", "hlg", darwin),
        row("linux", "amd64", "vaapi", "pq", locked),
        row("linux", "amd64", "vaapi", "hlg", locked),
    ],
}
with open(path, "w", encoding="utf-8") as fh:
    json.dump(payload, fh, indent=2)
PY
}

# plant applies a Python mutation to the green packet and expects RED.
plant() {  # plant <name> <python-mutation>
  local name="$1" mutation="$2" file="$TMP/planted.json"
  green_evidence "$file"
  python3 - "$file" "$mutation" <<'PY'
import json
import sys

path, mutation = sys.argv[1:3]
with open(path, encoding="utf-8") as fh:
    payload = json.load(fh)
rows = payload["rows"]
exec(mutation)  # noqa: S102 -- fixture mutation, this file is the only caller
with open(path, "w", encoding="utf-8") as fh:
    json.dump(payload, fh, indent=2)
PY
  if python3 "$GATE" --root "$ROOT" --evidence "$file" >/dev/null 2>"$TMP/err"; then
    echo "FAIL [$name]: gate passed evidence it must refuse" >&2
    failures=$((failures + 1))
  else
    echo "ok   [$name] RED: $(head -1 "$TMP/err")"
  fi
}

expect_green() {  # expect_green <name> <file>
  if python3 "$GATE" --root "$ROOT" --evidence "$2" >/dev/null 2>"$TMP/err"; then
    echo "ok   [$1] GREEN"
  else
    echo "FAIL [$1]: gate refused evidence it must accept: $(cat "$TMP/err")" >&2
    failures=$((failures + 1))
  fi
}

# The positive control. Without it every RED below could be produced by a gate
# that simply always refuses.
green_evidence "$TMP/green.json"
expect_green "complete two-half evidence" "$TMP/green.json"

# The committed evidence is RED by design: GA_EVIDENCE_APPROVER is unassigned
# and the two-host physical floor has not been run.
if python3 "$GATE" --root "$ROOT" >/dev/null 2>"$TMP/err"; then
  echo "FAIL [committed evidence]: the GA floor is unproven but the gate passed" >&2
  failures=$((failures + 1))
else
  echo "ok   [committed evidence] RED: $(head -1 "$TMP/err")"
fi

if python3 "$GATE" --root "$ROOT" --evidence "$TMP/absent.json" >/dev/null 2>&1; then
  echo "FAIL [missing evidence]: absent evidence is not an implicit pass" >&2
  failures=$((failures + 1))
else
  echo "ok   [missing evidence] RED"
fi

printf 'not json at all' >"$TMP/junk.json"
if python3 "$GATE" --root "$ROOT" --evidence "$TMP/junk.json" >/dev/null 2>&1; then
  echo "FAIL [unparseable evidence]: junk passed" >&2
  failures=$((failures + 1))
else
  echo "ok   [unparseable evidence] RED"
fi

plant "schema version mismatch"      'payload["schema_version"] = 2'
plant "stale scope digest"           'payload["certification_scope_digest"] = "0" * 64'
plant "unapproved rows"              'all(r.update(approver="") for r in rows)'
plant "whitespace approver"          'all(r.update(approver="   ") for r in rows)'
plant "software-only evidence"       'all(r.update(backend="") for r in rows)'
plant "single half (no server)"      'payload["rows"] = [r for r in rows if r["backend"] == "videotoolbox"]'
plant "single half (no darwin)"      'payload["rows"] = [r for r in rows if r["backend"] == "vaapi"]'
plant "hlg class unclaimed"          'payload["rows"] = [r for r in rows if r["transfer_class"] == "pq"]'
plant "failing status recorded"      'rows[0].update(status="unsupported", failure="crushed")'
plant "missing required key"         'del rows[0]["driver_hint"]'
plant "failure outside vocabulary"   'rows[0]["failure"] = "gpu-sad"'
plant "passthrough control absent"   'all(r.update(passthrough_control="not-applicable") for r in rows)'
plant "passthrough control passed"   'rows[2]["passthrough_control"] = "passed"'
plant "drop floor below constant"    'rows[0]["thresholds"]["min_highlight_drop"] = 31'
plant "highlight floor below const"  'rows[0]["thresholds"]["min_highlight"] = 63'
plant "origin outside vocabulary"    'rows[0]["thresholds"]["origin"] = "assumed"'
plant "calibrated, no software ref"  'rows[0]["thresholds"]["origin"] = "calibrated"'
plant "calibrated, bad software ref" 'rows[0]["thresholds"]["origin"] = "calibrated"; rows[0]["software_ref_highlight"] = 40'
plant "inherited, stray software ref" 'rows[0]["software_ref_highlight"] = 122.859'
plant "linux row, unpinned ffmpeg"   'rows[2]["ffmpeg_pin_identity"] = "a" * 64'
plant "pin identity not a digest"    'rows[0]["ffmpeg_pin_identity"] = "not-a-sha"'
plant "calibration ratchet lowered"  '''
import copy
tight = copy.deepcopy(rows[2]); tight["thresholds"] = {"min_highlight_drop": 90, "min_highlight": 120, "origin": "calibrated"}; tight["software_ref_highlight"] = 122.859
loose = copy.deepcopy(rows[2]); loose["thresholds"] = {"min_highlight_drop": 40, "min_highlight": 70, "origin": "calibrated"}; loose["software_ref_highlight"] = 122.859
payload["rows"] = rows + [tight, loose]
'''

if [ "$failures" -ne 0 ]; then
  echo "vidparity-hdr-tonemap-gate_test: $failures case(s) failed" >&2
  exit 1
fi
echo "vidparity-hdr-tonemap-gate_test: OK"
