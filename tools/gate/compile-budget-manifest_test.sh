#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-compile-budget-test.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin"
cat >"$TMP/bin/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

if [[ "$*" == "run ./tools/fuzzmanifest -mode matrix" ]]; then
  printf 'matrix\t-\t-\t-\n' >>"$FUZZ_LOG"
  python3 -c 'import json, sys; print(json.dumps({"include": json.load(open(sys.argv[1]))["targets"]}))' "$FUZZ_MANIFEST"
  exit
fi

if [[ "$*" == "env GOHOSTOS GOHOSTARCH" ]]; then
  printf 'env\t-\t-\t-\n' >>"$FUZZ_LOG"
  printf 'darwin\narm64\n'
  exit
fi

if [[ "$1" == "test" ]]; then
  regex=""
  pkg=""
  count=0
  tags=""
  prev=""
  for arg in "$@"; do
    case "$arg" in
      -count=1) count=$((count + 1)) ;;
      -run=*) regex="${arg#-run=}" ;;
      ./internal/*/) pkg="$arg" ;;
    esac
    [[ "$prev" == "-tags" ]] && tags="$arg"
    prev="$arg"
  done
  printf '%s\t%s\t%s\t%s\n' "$regex" "$pkg" "$count" "$tags" >>"$FUZZ_LOG"
  [[ -z "${FAIL_FUZZ_TARGET:-}" || "$regex" != *"$FAIL_FUZZ_TARGET"* ]]
  exit
fi

echo "unexpected fuzz go invocation: $*" >&2
exit 2
SH
chmod +x "$TMP/bin/go"

export PATH="$TMP/bin:$PATH"
export FUZZ_LOG="$TMP/fuzz.log"
export FUZZ_MANIFEST="$ROOT/testing/fuzz-targets.json"
FUZZ_JOBS=3 "$ROOT/script/fuzz-seed.sh" >"$TMP/fuzz.out"

python3 - "$FUZZ_LOG" <<'PY'
from pathlib import Path
import sys

rows = {tuple(line.split("\t")) for line in Path(sys.argv[1]).read_text().splitlines()}
expected = {
    ("matrix", "-", "-", "-"),
    ("env", "-", "-", "-"),
    ("^FuzzParseProbe$", "./internal/ffmpeg/", "1", ""),
    ("^FuzzPairingInputNormalization$", "./internal/pairing/", "1", ""),
    ("^FuzzRequestDecodeAndFieldScan$", "./internal/graph/", "1", ""),
    ("^FuzzLocaleStringJSON$", "./internal/plugin/", "1", ""),
    ("^FuzzValidateBundleJSON$", "./internal/plugin/", "1", ""),
    ("^FuzzUnverifiedGrant$", "./internal/streamtoken/", "1", ""),
    ("^FuzzResolveJSONPath$", "./internal/scrape/", "1", ""),
    ("^FuzzResolveXMLPath$", "./internal/scrape/", "1", ""),
    ("^FuzzParseFeedEntries$", "./internal/scrape/", "1", ""),
    ("^FuzzScrapeTransforms$", "./internal/scrape/", "1", ""),
}
if rows != expected:
    raise SystemExit(f"fuzz invocation manifest mismatch: {rows!r}")
if len(Path(sys.argv[1]).read_text().splitlines()) != len(expected):
    raise SystemExit("each fuzz go invocation must occur exactly once")
PY

fuzz_targets=(
  FuzzParseProbe
  FuzzPairingInputNormalization
  FuzzRequestDecodeAndFieldScan
  FuzzLocaleStringJSON
  FuzzValidateBundleJSON
  FuzzUnverifiedGrant
  FuzzResolveJSONPath
  FuzzResolveXMLPath
  FuzzParseFeedEntries
  FuzzScrapeTransforms
)
for target in "${fuzz_targets[@]}"; do
  : >"$FUZZ_LOG"
  if FAIL_FUZZ_TARGET="$target" FUZZ_JOBS=3 "$ROOT/script/fuzz-seed.sh" >"$TMP/fuzz-fail.out" 2>&1; then
    echo "fuzz target failure was hidden: $target" >&2
    exit 1
  fi
done

export CAP_ROOT="$TMP/capability-root"
mkdir -p "$CAP_ROOT/bin" "$CAP_ROOT/script"
cp "$ROOT/script/capability-manifest-check.sh" "$CAP_ROOT/script/"

cat >"$CAP_ROOT/script/cla-docs-check.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
echo cla-docs >>"$OP_LOG"
if [[ "${FAIL_OP:-}" == "cla-docs" ]]; then exit 97; fi
SH
cat >"$CAP_ROOT/script/workspace-contracts-gate.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
echo workspace-contracts >>"$OP_LOG"
if [[ "${FAIL_OP:-}" == "workspace-contracts" ]]; then exit 97; fi
SH
chmod +x "$CAP_ROOT/script/"*.sh

cat >"$CAP_ROOT/fixture.py" <<'PY'
import json
import os
import sys

mode, *args = sys.argv[1:]
failure = os.environ.get("FAIL_CHECK", "")
version = "--version" in args

if mode == "default":
    payload = {
        "capabilities": ["conversion", "rename"],
        "capabilityManifest": {
            "schemaVersion": 1,
            "namespace": "flat",
            "buildTags": [],
            "capabilities": [
                {"id": "conversion", "version": 1},
                {"id": "rename", "version": 1},
            ],
        },
        "capabilityStates": [{"id": "conversion"}, {"id": "rename"}],
    }
    if failure == "default-capabilities":
        payload["capabilities"] = []
    elif failure == "schema-version":
        payload["capabilityManifest"]["schemaVersion"] = 2
    elif failure == "namespace":
        payload["capabilityManifest"]["namespace"] = "nested"
    elif failure == "duplicate-ids":
        payload["capabilityManifest"]["capabilities"][1]["id"] = "conversion"
    elif failure == "state-ids":
        payload["capabilityStates"] = [{"id": "rename"}]
    elif failure == "version-positive":
        payload["capabilityManifest"]["capabilities"][0]["version"] = 0
    elif failure == "entries-empty":
        payload["capabilityManifest"]["capabilities"] = []
        payload["capabilityStates"] = []
    elif failure == "build-tags-type":
        payload["capabilityManifest"]["buildTags"] = "bad"
else:
    payload = {"capabilities": []}
    if version:
        payload.update({"commit": "fixture", "buildTags": ["beamfall_no_powertools"]})
    if failure == "disabled-capabilities" and not version:
        payload["capabilities"] = ["conversion"]
    elif failure == "version-disabled-capabilities" and version:
        payload["capabilities"] = ["conversion"]
    elif failure == "version-commit-metadata" and version:
        payload.pop("commit")
    elif failure == "version-build-tags-metadata" and version:
        payload.pop("buildTags")

json.dump(payload, sys.stdout)
PY

cat >"$CAP_ROOT/bin/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -eq 4 && "$1" == "build" && "$2" == "-tags" && "$3" == "beamfall_appstore" && "$4" == "./..." ]]; then
  echo appstore-build >>"$OP_LOG"
  if [[ "${FAIL_OP:-}" == "appstore-build" ]]; then exit 97; fi
  exit
fi
if [[ "$#" -eq 7 && "$1" == "test" && "$2" == "-tags" && "$3" == "beamfall_appstore" &&
      "$4" == "-count=1" && "$5" == "./internal/conformance/" && "$6" == "-run" &&
      "$7" == "TestAppStoreSuppressesCapabilityControlAPI" ]]; then
  echo appstore-test >>"$OP_LOG"
  if [[ "${FAIL_OP:-}" == "appstore-test" ]]; then exit 97; fi
  exit
fi
if [[ "$1" != "build" ]]; then
  echo "unexpected capability go invocation: $*" >&2
  exit 2
fi

if [[ "$#" -eq 4 && "$2" == "-o" && "$4" == "./cmd/beamfall" ]]; then
  mode=default
  operation=cli-default-build
  output="$3"
elif [[ "$#" -eq 6 && "$2" == "-tags" && "$3" == "beamfall_no_powertools" &&
        "$4" == "-o" && "$6" == "./cmd/beamfall" ]]; then
  mode=disabled
  operation=cli-disabled-build
  output="$5"
else
  echo "unexpected capability build invocation: $*" >&2
  exit 2
fi
echo "$operation" >>"$OP_LOG"
if [[ "${FAIL_OP:-}" == "$operation" ]]; then exit 97; fi
cat >"$output" <<SH2
#!/usr/bin/env bash
set -euo pipefail
if [[ "$mode" == "default" && "\$#" -eq 1 && "\$1" == "capabilities" ]]; then
  operation=cli-default-run
elif [[ "$mode" == "disabled" && "\$#" -eq 1 && "\$1" == "capabilities" ]]; then
  operation=cli-disabled-run
elif [[ "$mode" == "disabled" && "\$#" -eq 2 && "\$1" == "capabilities" && "\$2" == "--version" ]]; then
  operation=cli-version-run
else
  echo "unexpected capability binary invocation ($mode): \$*" >&2
  exit 2
fi
echo "\$operation" >>"\$OP_LOG"
if [[ "\${FAIL_OP:-}" == "\$operation" ]]; then exit 97; fi
exec python3 "$CAP_ROOT/fixture.py" "$mode" "\$@"
SH2
chmod +x "$output"
SH
chmod +x "$CAP_ROOT/bin/go"

export PATH="$CAP_ROOT/bin:$PATH"
export OP_LOG="$TMP/capability-operations.log"
run_capability() {
  : >"$OP_LOG"
  set +e
  "$CAP_ROOT/script/capability-manifest-check.sh" >"$TMP/capability.out" 2>"$TMP/capability.err"
  CAPABILITY_RC=$?
  set -e
}

run_capability
[[ "$CAPABILITY_RC" -eq 0 ]]
cat >"$TMP/expected-operations" <<'EOF'
cla-docs
cli-default-build
cli-disabled-build
cli-default-run
cli-disabled-run
cli-version-run
appstore-build
appstore-test
workspace-contracts
EOF
cmp "$TMP/expected-operations" "$OP_LOG"

operations=(
  cla-docs
  cli-default-build
  cli-disabled-build
  cli-default-run
  cli-disabled-run
  cli-version-run
  appstore-build
  appstore-test
  workspace-contracts
)
for operation in "${operations[@]}"; do
  export FAIL_OP="$operation"
  run_capability
  if [[ "$CAPABILITY_RC" -eq 0 ]]; then
    echo "capability operation failure was hidden: $operation" >&2
    cat "$OP_LOG" >&2
    exit 1
  fi
  [[ "$(tail -n 1 "$OP_LOG")" == "$operation" ]]
  [[ "$(grep -c "^${operation}$" "$OP_LOG")" -eq 1 ]]
  unset FAIL_OP
done

checks=(
  "default-capabilities|default capabilities diverged"
  "schema-version|capability manifest schema diverged"
  "namespace|capability manifest schema diverged"
  "duplicate-ids|capability manifest has duplicate IDs"
  "state-ids|runtime states do not key on manifest IDs"
  "entries-empty|capability manifest versions are not positive integers"
  "version-positive|capability manifest versions are not positive integers"
  "build-tags-type|capability manifest buildTags missing"
  "disabled-capabilities|disabled capabilities diverged"
  "version-disabled-capabilities|version disabled capabilities diverged"
  "version-commit-metadata|version metadata missing"
  "version-build-tags-metadata|version metadata missing"
)
for check_case in "${checks[@]}"; do
  check="${check_case%%|*}"
  diagnostic="${check_case#*|}"
  export FAIL_CHECK="$check"
  run_capability
  if [[ "$CAPABILITY_RC" -eq 0 ]]; then
    echo "capability assertion failure was hidden: $check" >&2
    cat "$OP_LOG" >&2
    exit 1
  fi
  grep -Fq "$diagnostic" "$TMP/capability.err"
  [[ "$(tail -n 1 "$OP_LOG")" == "cli-version-run" ]]
  ! grep -q '^appstore-' "$OP_LOG"
  unset FAIL_CHECK
done

python3 - "$ROOT/tools/gate/gate.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
batch = text.split("# GATE-COMPILE-BUDGET-1:", 1)[1].split("# ADR-0121 §4:", 1)[0]
active_batch = [
    line.strip()
    for line in batch.splitlines()[1:]
    if line.strip() and not line.lstrip().startswith("#")
]
expected_batch = [
    'start_parallel_step pid_golangci_lint "golangci-lint (staticcheck + gosec)" make lint',
    'start_parallel_step pid_go_build "go build" go build ./...',
    'start_parallel_step pid_fuzz "fuzz" make fuzz',
    'start_parallel_step pid_capability_manifest "capability manifest" script/capability-manifest-check.sh',
    'wait_parallel "$pid_fuzz"',
    'start_parallel_step pid_go_vet "go vet" go vet ./...',
    'wait_parallel "$pid_go_build"',
    'start_parallel_step pid_client_drm "client DRM/EME invariant" script/client-drm-eme-check.sh',
    'wait_parallel "$pid_golangci_lint"',
    'start_parallel_step pid_feature_coverage "feature-coverage ratchet" go run ./cmd/featurecheck -ratchet testing/feature-coverage-ratchet.json -strict-areas api,ingest,legal,onboarding',
    'wait_parallel "$pid_capability_manifest"',
    'start_parallel_step pid_scenario_ledger "scenario ledger" go run ./cmd/scenariocheck -strict',
    'wait_parallel "$pid_go_vet" "$pid_client_drm" "$pid_feature_coverage" "$pid_scenario_ledger"',
]
if active_batch != expected_batch:
    raise SystemExit(f"compile budget schedule mismatch: {active_batch!r}")

tagged = text.split("# ADR-0121 §4:", 1)[1].split("# Real-encoder wall-clock SLOs", 1)[0]
active_tagged = [
    line.strip()
    for line in tagged.splitlines()[1:]
    if line.strip() and not line.lstrip().startswith("#")
]
expected_tagged = [
    'run_step "go test -tags beamfall_appstore conformance capabilities graph" go test -tags beamfall_appstore -count=1 ./internal/conformance ./internal/capabilities ./internal/graph',
    'run_scoped_step "ADR-0122 cross-repo wire conformance" contract make crossrepo-conformance',
    'run_step "go test + line-coverage ratchet" go run ./cmd/covratchet -floor testing/coverage-floor.json',
]
if active_tagged != expected_tagged:
    raise SystemExit(f"App Store/cache sequencing mismatch: {active_tagged!r}")

registration = 'run_step "compile budget manifest selftest" tools/gate/compile-budget-manifest_test.sh'
preflight_registration = 'preflight_step "compile budget manifest selftest" tools/gate/compile-budget-manifest_test.sh'
active_lines = [line.strip() for line in text.splitlines() if not line.lstrip().startswith("#")]
if active_lines.count(registration) != 1:
    raise SystemExit("compile budget manifest selftest must be registered exactly once")
if active_lines.count(preflight_registration) != 1:
    raise SystemExit("compile budget manifest selftest must be registered in gate-preflight exactly once")
PY

echo "compile budget manifest test: passed"
