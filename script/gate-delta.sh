#!/bin/zsh
# gate-delta.sh [--explain] — diff-classified gate for repair-round re-finishes.
#
# The full gate certifies a merge. Mid-lane stamps only need to certify the classes
# the lane diff can affect, so this command narrows repair-round work while relying
# on the canonical full gate that still runs at landing. A delta-gate receipt cannot
# satisfy that merge gate because it records a different command.
#
# Classification (fail-closed — one file outside every narrow class => full gate):
#   docs/evidence : docs/**, artifacts/**, .agent-evidence/**, backlog/**, *.md
#                   -> adr-lint only
#   web           : internal/web/app/** (+ any docs/evidence files)
#                   -> web build/lint/tests + build and test the Go packages that
#                      embed or serve the web app
#   anything else -> exec the canonical full gate (make gate)
#
# Run from anywhere inside the lane worktree. The diff base is merge-base(HEAD, main).
# --explain prints the class and plan without running anything.
set -u

EXPLAIN=0
[ "${1:-}" = "--explain" ] && EXPLAIN=1

SELF="${0:A}"
ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
  echo "gate-delta: not in a git repo"
  exit 2
}
cd "$ROOT"
BASE=$(git merge-base HEAD main 2>/dev/null) || {
  echo "gate-delta: no merge-base with main"
  exit 2
}

typeset -a FILES
FILES=("${(f)$(git diff --name-only "$BASE"..HEAD)}")
[ -n "${FILES[1]:-}" ] || {
  echo "gate-delta: empty diff vs main — nothing to certify"
  exit 0
}

class=docs
for f in "${FILES[@]}"; do
  case "$f" in
    docs/*|artifacts/*|.agent-evidence/*|backlog/*|*.md) ;;
    internal/web/app/*) [ "$class" = docs ] && class=web ;;
    *) class=full; break ;;
  esac
done

echo "gate-delta: class=$class files=${#FILES[@]} base=${BASE:0:9}"
if [ "$EXPLAIN" -eq 1 ]; then
  case "$class" in
    docs) echo "plan: make adr-lint" ;;
    web) echo "plan: web suite (biome/build/tests) + go build ./... + go test -count=1 ./internal/web/... ./internal/server/..." ;;
    full) echo "plan: exec canonical full gate (make gate)" ;;
  esac
  exit 0
fi

case "$class" in
  full)
    exec make gate
    ;;
  docs)
    make adr-lint || exit 1
    echo "gate-delta OK (docs/evidence class; full gate deferred to merge)"
    ;;
  web)
    # Same flock-backed admission as the canonical gate: one gate-class job per repo.
    if [ "${BEAMFALL_GATE_CAPACITY_HELD:-0}" != 1 ]; then
      export BEAMFALL_GATE_CAPACITY_HELD=1
      exec python3 "$ROOT/script/gate-capacity.py" -- "$SELF" "$@"
    fi
    make web-build || exit 1
    (cd internal/web/app && npm run check) || exit 1
    (cd internal/web/app && npm test) || exit 1
    go build ./... || exit 1
    go test -count=1 ./internal/web/... ./internal/server/... || exit 1
    echo "gate-delta OK (web class; full gate deferred to merge)"
    ;;
esac
