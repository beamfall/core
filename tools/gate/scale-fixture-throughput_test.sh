#!/usr/bin/env bash
set -euo pipefail

root=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/beamfall-scale-throughput.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/corpus/library"

cat >"$tmp/corpus/manifest.json" <<'JSON'
{"generator_version":"1","seed":171,"tier":"10k","profile":"perf-realistic","file_count":10000}
JSON
cat >"$tmp/green.json" <<'JSON'
{"generator_version":"1","seed":171,"tier":"10k","profile":"perf-realistic","file_count":10000,"scan_nanoseconds":1000000000,"search_nanoseconds":1000000000,"thumbnail_nanoseconds":1000000000,"search_matches":10000,"thumbnail_probes":2500}
JSON

python3 "$root/tools/gate/scale-fixture-throughput.py" \
  --recipe "$root/testing/scale-benchmark/recipe.json" \
  --corpus "$tmp/corpus" \
  --timings "$tmp/green.json" >/dev/null

cat >"$tmp/regression.json" <<'JSON'
{"generator_version":"1","seed":171,"tier":"10k","profile":"perf-realistic","file_count":10000,"scan_nanoseconds":3000000000,"search_nanoseconds":1000000000,"thumbnail_nanoseconds":1000000000,"search_matches":10000,"thumbnail_probes":2500}
JSON
if python3 "$root/tools/gate/scale-fixture-throughput.py" \
  --recipe "$root/testing/scale-benchmark/recipe.json" \
  --corpus "$tmp/corpus" \
  --timings "$tmp/regression.json" >/dev/null 2>&1; then
  echo "scale-fixture-throughput selftest: regression fixture unexpectedly passed" >&2
  exit 1
fi

echo "scale-fixture-throughput selftest: ok"
