#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

usage() {
  cat >&2 <<'USAGE'
usage: script/repo-map.sh [--limit N] [PATH ...]

Emits a capped symbol map for source orientation. Paths may be files or
directories. This is a navigation aid; verify cited files before editing.
USAGE
}

limit=200
paths=()
while [ $# -gt 0 ]; do
  case "$1" in
    --limit) limit="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) paths+=("$1"); shift ;;
  esac
done

case "$limit" in
  ''|*[!0-9]*) echo "repo-map: --limit must be a positive integer" >&2; exit 2 ;;
esac
[ "$limit" -gt 0 ] || { echo "repo-map: --limit must be a positive integer" >&2; exit 2; }

source_globs=(
  '*.go' '*.ts' '*.tsx' '*.js' '*.jsx' '*.mjs'
  ':!:vendor/**' ':!:node_modules/**' ':!:dist/**' ':!:build/**'
  ':!:.next/**' ':!:coverage/**' ':!:internal/web/app-dist/**'
  ':!:internal/web/static/**'
)

files_tmp="$(mktemp "${TMPDIR:-/tmp}/beamfall-repo-map.XXXXXX")"
trap 'rm -f "$files_tmp"' EXIT

if [ "${#paths[@]}" -eq 0 ]; then
  git ls-files -- "${source_globs[@]}" > "$files_tmp"
else
  git ls-files -- "${paths[@]}" |
    rg '\.(go|tsx?|jsx?|mjs)$' |
    rg -v '^(vendor|node_modules|dist|build|\.next|coverage|internal/web/app-dist|internal/web/static)/' > "$files_tmp" ||
    true
fi

emitted=0
current=""

emit() {
  [ "$emitted" -lt "$limit" ] || exit 0
  printf '%s\n' "$1"
  emitted=$((emitted + 1))
}

while IFS= read -r file; do
  case "$file" in
    *.go)
      matches="$(rg -n --no-heading '^(func|type|const|var)[[:space:]]' "$file" || true)"
      ;;
    *.ts|*.tsx|*.js|*.jsx|*.mjs)
      matches="$(rg -n --no-heading '^[[:space:]]*(export[[:space:]]+)?(default[[:space:]]+)?(async[[:space:]]+)?(function|class|interface|type|const|let|var|enum)[[:space:]]+[A-Za-z0-9_$]+' "$file" || true)"
      ;;
    *)
      matches=""
      ;;
  esac
  [ -n "$matches" ] || continue

  while IFS= read -r line; do
    [ -n "$line" ] || continue
    if [ "$current" != "$file" ]; then
      [ -z "$current" ] || emit ""
      emit "$file:"
      current="$file"
    fi
    emit "  $line"
  done <<< "$matches"
done < "$files_tmp"
