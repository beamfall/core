#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Validation subcommands. The release gate uploads an evidence packet and the
# release workflow consumes it, so both need the same validator: a packet whose
# directories merely EXIST proves nothing about the tiers it claims to represent.
#   require-artifacts <dir>                 every expected artifact present and real
#   verify <dir> --expect-commit <sha>      that, plus the aggregate binds this SHA
case "${1:-}" in
  require-artifacts|verify)
    exec python3 - "$@" <<'CHECK'
import json
import sys
import xml.etree.ElementTree as ET
from pathlib import Path

# Each required artifact names what actually proves its tier ran. Presence is not
# evidence: an empty directory, a junk file, a testcase-free JUnit, or a record of
# a FAILED command would all satisfy a `test -d`.
REQUIRED = {
    "release-gate-io-evidence": {
        "junit": "io-journeys-junit.xml",
        "labels": ("io-smoke", "io-journeys-junit", "ioe2e-release-check"),
    },
    "release-gate-web-e2e-evidence": {
        "junit": None,
        "labels": ("web-e2e-full",),
    },
}


def die(message):
    print(f"release-gate-evidence: {message}", file=sys.stderr)
    raise SystemExit(1)


def junit_testcases(path):
    try:
        root = ET.parse(path).getroot()
    except ET.ParseError as err:
        die(f"{path} is not parseable JUnit: {err}")
    return root.iter("testcase")


def check_junit(artifact_dir, name):
    path = artifact_dir / name
    if not path.is_file():
        die(f"{artifact_dir.name} is missing {name}")
    if path.stat().st_size == 0:
        die(f"{artifact_dir.name}/{name} is empty")
    if next(junit_testcases(path), None) is None:
        die(f"{artifact_dir.name}/{name} records zero testcases")


def records(artifact_dir):
    for meta in sorted(artifact_dir.rglob("metadata.json")):
        try:
            payload = json.loads(meta.read_text(encoding="utf-8"))
        except (OSError, ValueError) as err:
            die(f"{meta} is not readable evidence: {err}")
        yield meta, payload


def check_label(artifact_dir, label):
    # run-with-evidence names each record directory <stamp>-<label>.XXXXXX.
    for meta, payload in records(artifact_dir):
        if f"-{label}." not in meta.parent.name:
            continue
        exit_code = payload.get("exit_code")
        if exit_code != 0:
            die(f"{artifact_dir.name} records {label} with exit_code={exit_code!r}; a failed tier is not evidence")
        return
    die(f"{artifact_dir.name} has no run-with-evidence record for {label}")


def require_artifacts(root):
    if not root.is_dir():
        die(f"evidence directory not found: {root}")
    for name, spec in REQUIRED.items():
        artifact_dir = root / name
        if not artifact_dir.is_dir():
            die(f"required evidence artifact missing: {name}")
        if spec["junit"]:
            check_junit(artifact_dir, spec["junit"])
        for label in spec["labels"]:
            check_label(artifact_dir, label)
    print(f"release-gate-evidence: OK — {len(REQUIRED)} required artifact(s) complete")


def verify(root, expect_commit):
    require_artifacts(root)
    aggregate = root / "aggregate-result.json"
    if not aggregate.is_file():
        die("aggregate-result.json missing; the packet claims no aggregate result")
    try:
        payload = json.loads(aggregate.read_text(encoding="utf-8"))
    except (OSError, ValueError) as err:
        die(f"aggregate-result.json is not readable: {err}")
    if payload.get("result") != "PASS":
        die(f"aggregate result is {payload.get('result')!r}, not PASS")
    if payload.get("commit") != expect_commit:
        die(f"aggregate evidence binds commit {payload.get('commit')!r}, not {expect_commit!r}")
    print(f"release-gate-evidence: OK — aggregate PASS bound to {expect_commit}")


def main(argv):
    if len(argv) < 2:
        die("usage: require-artifacts <dir> | verify <dir> --expect-commit <sha>")
    mode, root = argv[0], Path(argv[1])
    if mode == "require-artifacts":
        require_artifacts(root)
        return
    if len(argv) != 4 or argv[2] != "--expect-commit":
        die("usage: verify <dir> --expect-commit <sha>")
    if not argv[3]:
        die("--expect-commit requires a commit sha")
    verify(root, argv[3])


main(sys.argv[1:])
CHECK
    ;;
esac

OUT_ROOT="${AGENT_EVIDENCE_DIR:-$ROOT/.agent-evidence}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"

if [ -n "${RELEASE_EVIDENCE_ARCHIVE:-}" ]; then
  : "${VERSION:?VERSION is required for release evidence}"
  : "${CHANNEL:?CHANNEL is required for release evidence}"
  : "${COMMIT:?COMMIT is required for release evidence}"
  : "${INVOCATION_ID:?INVOCATION_ID is required for release evidence}"
fi

mkdir -p "$OUT_ROOT"
dir="$(mktemp -d "$OUT_ROOT/${stamp}-release-gate.XXXXXX")"
junit="$dir/io-journeys-junit.xml"
summary="$dir/summary.txt"
: > "$summary"

run_step() {
  local name="$1" start elapsed
  shift
  echo "==> [release-gate] $name" >&2
  start="$SECONDS"
  if AGENT_EVIDENCE_DIR="$dir" "$ROOT/script/run-with-evidence.sh" --label "$name" -- "$@"; then
    elapsed=$((SECONDS - start))
    printf 'PASS %s elapsed_s=%s\n' "$name" "$elapsed" | tee -a "$summary" >&2
  else
    elapsed=$((SECONDS - start))
    printf 'FAIL %s elapsed_s=%s\n' "$name" "$elapsed" | tee -a "$summary" >&2
    return 1
  fi
}

# The release contract: mandatory scoped lanes run even in a clean checkout with
# no diff range, and a skipped mandatory lane reds instead of being recorded as
# evidence (tools/gate/gate.sh gate_release_mode).
export GATE_MODE=release

run_step release-test-gates make -C "$ROOT" release-test-gates
run_step clean-checkout-canary "$ROOT/script/clean-checkout-canary.sh"
run_step nightly-freshness "$ROOT/script/nightly-freshness.sh" check
run_step scenario-release-check go run ./cmd/scenariocheck -release
run_step contract-registry python3 "$ROOT/script/check-contract-registry.py"
run_step vidparity-hdr-tonemap-gate "$ROOT/tools/gate/vidparity-hdr-tonemap-gate.sh"
run_step bench make -C "$ROOT" bench
run_step race-full make -C "$ROOT" race-full
run_step io-smoke "$ROOT/script/io-e2e.sh" smoke
run_step io-journeys-junit "$ROOT/script/io-e2e.sh" journeys-junit "$junit"
run_step ioe2e-release-check "$ROOT/script/io-e2e.sh" ledger release-check "$junit"
run_step web-e2e-full make -C "$ROOT" web-e2e-full

python3 - "$dir/release-identity.json" "$stamp" "${VERSION:-}" "${CHANNEL:-}" "${COMMIT:-}" "${INVOCATION_ID:-}" <<'PY'
import json
import sys

path, timestamp, version, channel, commit, invocation_id = sys.argv[1:]
with open(path, "w", encoding="utf-8") as f:
    json.dump({
        "channel": channel,
        "commit": commit,
        "invocation_id": invocation_id,
        "timestamp_utc": timestamp,
        "version": version,
    }, f, indent=2, sort_keys=True)
    f.write("\n")
PY

(
  cd "$dir"
  if command -v shasum >/dev/null 2>&1; then
    find . -type f ! -name sha256.txt -print0 | LC_ALL=C sort -z | xargs -0 shasum -a 256 >sha256.txt
  else
    find . -type f ! -name sha256.txt -print0 | LC_ALL=C sort -z | xargs -0 sha256sum >sha256.txt
  fi
)

if [ -n "${RELEASE_EVIDENCE_ARCHIVE:-}" ]; then
  mkdir -p "$(dirname "$RELEASE_EVIDENCE_ARCHIVE")"
  tar -C "$dir" -czf "$RELEASE_EVIDENCE_ARCHIVE" .
  echo "release-gate: archived evidence=$RELEASE_EVIDENCE_ARCHIVE" >&2
fi

echo "release-gate: PASS evidence=$dir" >&2
