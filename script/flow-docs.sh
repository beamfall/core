#!/usr/bin/env bash
# Corvint flow documentation for the web app (internal/web/app). See
# docs/web/flows/README.md for what the record covers and its limits.
#
#   script/flow-docs.sh generate   # `make flow-docs`: regenerate at HEAD
#   script/flow-docs.sh check      # `make flow-docs-check`: drift vs the record
#
# generate rewrites docs/web/flows/{generated/,generation.json,map.json} from the
# COMMITTED inventory.json and appmap.json at HEAD (Corvint reads its inputs
# from Git, so commit inventory or manifest edits first). check is read-only:
# `corvint docs flows check` compares HEAD against the committed
# generation.json (exit 1 = drift, 2 = invalid input), then the application map
# is rebuilt at HEAD and compared with map.json ignoring only its revision and
# digest. Neither mode is wired into `make gate`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DIR=docs/web/flows
SCOPE=internal/web/app
mode="${1:-}"

command -v corvint >/dev/null || { echo "flow-docs: corvint is not on PATH" >&2; exit 2; }
if ! git diff --quiet HEAD -- "$DIR/inventory.json" "$DIR/appmap.json"; then
  echo "flow-docs: commit $DIR/inventory.json and $DIR/appmap.json first (Corvint reads them from Git at HEAD)" >&2
  exit 2
fi

rev="$(git rev-parse HEAD)"
# Corvint refuses an output path with a symlinked component (macOS /var -> /private/var).
tmp="$(cd "$(mktemp -d)" && pwd -P)"
trap 'rm -rf "$tmp"' EXIT INT TERM

build_map() {
  corvint flows appmap build --manifest "$DIR/appmap.json" --revision "$rev" --no-workspace
}

case "$mode" in
  generate)
    corvint docs flows generate --revision "$rev" --scope "$SCOPE" \
      --flows "$DIR/inventory.json" --output-dir "$tmp/out" >"$tmp/generation.json"
    # The output directory carries its own copy of generation.json; keep one.
    rm -f "$tmp/out/generation.json"
    build_map >"$tmp/map.json"
    rm -rf "$DIR/generated"
    mv "$tmp/out" "$DIR/generated"
    mv "$tmp/generation.json" "$DIR/generation.json"
    mv "$tmp/map.json" "$DIR/map.json"
    python3 -I - "$DIR/generation.json" "$DIR/map.json" <<'PY'
import json, sys
g = json.load(open(sys.argv[1]))
m = json.load(open(sys.argv[2]))
flows = g["inventory"]["flows"]
res = sum(len(f["resolved"]) for f in flows)
unres = sum(len(f["unresolved"]) for f in flows)
gen = sum(1 for f in flows if f["generated"])
print(f"flow-docs: {len(flows)} flows, {gen} generated; members {res} resolved, {unres} unresolved; "
      f"{len(g['inventory']['unassigned'])} unassigned declarations")
print(f"flow-docs: appmap {len(m['screens'])} screens, {len(m['edges'])} edges, "
      f"{len(m['files'])} test files, {len(m['unknowns'])} unknowns")
for f in flows:
    if not f["generated"]:
        print(f"flow-docs: not generated (no resolved member): {f['id']}")
PY
    ;;
  check)
    status=0
    corvint docs flows check --revision "$rev" --previous "$DIR/generation.json" \
      --output-dir "$tmp/check" >"$tmp/check.json" || status=$?
    if [ "$status" -ne 0 ]; then
      echo "flow-docs-check: docs flows check exited $status (1 = drift, 2 = invalid); report: " >&2
      head -c 2000 "$tmp/check.json" >&2
      echo >&2
      echo "flow-docs-check: run \`make flow-docs\` and commit the result" >&2
      exit "$status"
    fi
    build_map >"$tmp/map.json"
    if ! python3 -I - "$DIR/map.json" "$tmp/map.json" <<'PY'
import json, sys
old, new = (json.load(open(p)) for p in sys.argv[1:3])
for m in (old, new):
    m.pop("revision", None)
    m.pop("digest", None)
sys.exit(0 if old == new else 1)
PY
    then
      echo "flow-docs-check: application map drifted from $DIR/map.json; run \`make flow-docs\`" >&2
      exit 1
    fi
    echo "flow-docs-check: no drift at $rev"
    ;;
  *)
    echo "usage: script/flow-docs.sh generate|check" >&2
    exit 2
    ;;
esac
