#!/usr/bin/env bash
# CORE-0944: `make gate-affected` — the iteration tier between `go test` on one
# package and the full `make gate`. It runs `corvint test --base` (only the Go
# packages and declared language commands the change can reach; any fallback
# condition prints `corvint-test: FALLBACK <reason>` and runs the full suite),
# then `make gate-preflight` for the static/lint subset.
#
# A PASS here is NOT a merge signal: `make gate` on the exact head stays the
# merge gate. BASE defaults to the merge-base with main; uncommitted changes are
# part of the plan.
#
# The plan is computed once with --dry-run and the run is bound to it with
# --manifest, so what ran is what was planned. When that plan already ran
# `make gate-preflight` (a declared language in .github/cishards/
# test-commands.json maps to it), it is not run a second time.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

base_request="${1:-}"
if [ -z "$base_request" ]; then
  main_ref=main
  git rev-parse --verify --quiet "$main_ref^{commit}" > /dev/null || main_ref=origin/main
  base_request="$(git merge-base "$main_ref" HEAD)" || {
    echo "gate-affected: no merge-base with $main_ref; pass BASE=<commit>" >&2
    exit 2
  }
fi
case "$base_request" in
  -*) echo "gate-affected: BASE must be a commit, not an option: $base_request" >&2; exit 2 ;;
esac
base="$(git rev-parse --verify --quiet "$base_request^{commit}")" || {
  echo "gate-affected: BASE does not name a commit: $base_request" >&2
  exit 2
}
corvint="${CORVINT:-corvint}"
command -v "$corvint" > /dev/null || {
  echo "gate-affected: $corvint not found on PATH (set CORVINT=<path>)" >&2
  exit 2
}
echo "gate-affected: base $base ($base_request)"

work="$(mktemp -d "${TMPDIR:-/tmp}/gate-affected.XXXXXX")"
child=""
# A signal trap that returns lets the shell resume, so each signal exits with
# 128+N; EXIT cleans up. corvint kills its own process groups on the signal.
interrupt() {
  trap - "$1"
  if [ -n "$child" ]; then
    kill -s "$1" "$child" 2> /dev/null || true
    wait "$child" 2> /dev/null || true
  fi
  exit "$2"
}
trap 'rm -rf "$work"' EXIT
trap 'interrupt HUP 129' HUP
trap 'interrupt INT 130' INT
trap 'interrupt TERM 143' TERM

# Planning is quick and has no side effects; run it in the foreground.
"$corvint" test --base "$base" --dry-run --summary --manifest "$work/manifest.json"

status=0
"$corvint" test --base "$base" --manifest "$work/manifest.json" &
child=$!
wait "$child" || status=$?
child=""
if [ "$status" -ne 0 ]; then
  echo "gate-affected: corvint test exited $status; gate-preflight not run" >&2
  exit "$status"
fi

preflight_ran="$(python3 - "$work/manifest.json" .github/cishards/test-commands.json <<'PY'
import json, sys
manifest = json.load(open(sys.argv[1]))
languages = json.load(open(sys.argv[2])).get("languages", {})
preflight = {f"declared:{lang}" for lang, argv in languages.items() if argv == ["make", "gate-preflight"]}
ran = sorted(preflight & {unit.get("unit") for unit in manifest.get("units", [])})
print(ran[0] if ran else "")
PY
)"
if [ -n "$preflight_ran" ]; then
  echo "gate-affected: PASS (gate-preflight already ran as $preflight_ran; not a merge gate — run make gate)"
  exit 0
fi
echo "gate-affected: corvint test PASS; running gate-preflight (not a merge gate — run make gate)"
rm -rf "$work"
trap - EXIT HUP INT TERM
exec make gate-preflight
