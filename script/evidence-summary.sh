#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: script/evidence-summary.sh EVIDENCE_DIR

Prints command, exit code, SHA-256, and a capped summary/failure tail for a
script/run-with-evidence.sh artifact directory.
USAGE
}

[ $# -eq 1 ] || { usage; exit 2; }
dir="$1"
[ -d "$dir" ] || { echo "evidence-summary: not a directory: $dir" >&2; exit 1; }
[ -f "$dir/metadata.json" ] || { echo "evidence-summary: missing metadata.json in $dir" >&2; exit 1; }

python3 - "$dir" <<'PY'
import json
import sys
from pathlib import Path

root = Path(sys.argv[1])
meta = json.loads((root / "metadata.json").read_text(encoding="utf-8"))
summary = root / str(meta.get("summary", "summary.txt"))
output = root / str(meta.get("output", "output.log"))

print(f"artifact: {root}")
print("command: " + " ".join(meta.get("command") or []))
print(f"exit: {meta.get('exit_code')}")
print(f"sha256: {meta.get('sha256')}")
print(f"summary_sha256: {meta.get('summary_sha256')}")
if summary.exists():
    print("summary:")
    for line in summary.read_text(encoding="utf-8", errors="replace").splitlines()[:12]:
        print(line)
if meta.get("exit_code") and output.exists():
    print("tail:")
    for line in output.read_text(encoding="utf-8", errors="replace").splitlines()[-12:]:
        print(line)
PY
