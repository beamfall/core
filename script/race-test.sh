#!/usr/bin/env bash
# The `-race` test tiers (ADR-0121 §3 remediation, TEST-CORE-1).
#
# The race detector requires CGO_ENABLED=1 and a C toolchain, and inflates
# wall time ~20x. The two heaviest core packages (internal/graph, internal/store)
# time out under `-race` (~606s vs ~30s), so we split the work into two tiers:
#
#   short  — the in-gate subset. With GATE_DIFF_BASE, `-race -short` covers
#            changed Go packages plus their reverse dependencies. Without a
#            usable diff it covers every race-relevant package, excluding the
#            known-slow graph/store tier and generated web embed shim. Hot
#            packages run a concurrency-relevant slice unless directly changed,
#            in which case their full -race -short suite runs separately at
#            -p 1 with a measured 3600-second package ceiling.
#            Fast enough to run on every autonomous merge, and
#            it INCLUDES the deliberately-racy RED-proving fixture (internal/racefixture)
#            so the gate proves the detector is live.
#   full   — the nightly tier. `-race ./...` over everything, with a raised
#            per-package timeout and capped -p/-parallel so graph/store finish.
#
# Usage: script/race-test.sh {short|full}
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Packages excluded from the full-tree in-gate race tier. In diff scope, a
# directly changed excluded package is promoted to its full -race -short suite.
RACE_SHORT_EXCLUDED_PKGS=(
  github.com/beamfall/core/internal/graph
  github.com/beamfall/core/internal/store
  github.com/beamfall/core/internal/web
)

RACE_SHORT_HOT_PKGS=(
  github.com/beamfall/core/internal/app
  github.com/beamfall/core/internal/server
  github.com/beamfall/core/internal/mediahttp
  github.com/beamfall/core/internal/playback
  github.com/beamfall/core/internal/scan
  github.com/beamfall/core/internal/pairing
)

RACE_SHORT_HOT_TESTS='^(TestEventStreamTerminatesOnDeviceRevocation|TestHLSPrecomputedRenditionGatesSegmentAndRange|TestCanDirectPlayRichProfile|TestPrecomputeConcurrentAdmissionNeverExceedsBudget|TestPrecomputeCancellationCleansStagingAndReleasesReservation|TestPrecomputeDiskFullCleansStagingAndReleasesReservation|TestPrecomputeRejectsIncompleteArtifactBeforePublication|TestPrecomputeRejectsPlaylistWithMissingReferencedSegment|TestPrecomputeReplacementFencesStalePublisher|TestPrecomputeInputGenerationChangeCannotPublish|TestPrecomputeAtomicPublicationRejectsReplacedInput|TestSweepPrecomputeResidueRecoversCrashWithoutDeletingReadyGeneration|TestScanJobHandlerYieldsCheckpointAndResumes|TestBootstrapRejectsConcurrentDoubleSubmit|TestRunShutdownCancelsBlockingJobAtDrainBound|TestRunShutdownLetsFastJobDrainWithoutCancellation|TestRunShutdownRepeatedlyJoinsWorkers|TestShutdownSequenceStopsClaimsBeforeCancelJoinCheckpointAndStoreClose|TestDrainBackgroundJobsDrainsQueueInOneTickWindow|TestJobClaimerStartupBacklogRunsImmediatelyAndCancels|TestIntegrationManagerDeactivateCancelsInFlightWorkerAndReports|TestScanScheduleSpecAbortsWhenSchedulerContextCanceled)$'

race_short_diff_scope() { # <work-dir> <discovered-output> <changed-output>
  local work_dir="$1" discovered_file="$2" changed_file="$3"
  local requested_base="${GATE_DIFF_BASE:-}" requested_head="${GATE_DIFF_HEAD:-HEAD}"
  local base_sha head_sha path metadata_file changed_paths_file
  [ -n "$requested_base" ] || return 1

  base_sha="$(git rev-parse --verify "$requested_base^{commit}" 2>/dev/null)" || return 1
  head_sha="$(git rev-parse --verify "$requested_head^{commit}" 2>/dev/null)" || return 1
  git merge-base --is-ancestor "$base_sha" "$head_sha" 2>/dev/null || return 1
  RACE_DIFF_BASE_SHA="$base_sha"
  changed_paths_file="$work_dir/changed-paths.txt"
  git diff --name-only "$base_sha" "$head_sha" >"$changed_paths_file" 2>/dev/null || return 1
  [ -s "$changed_paths_file" ] || return 1

  while IFS= read -r path; do
    case "$path" in
      go.mod|go.sum|go.work|Makefile|tools/gate/*|script/race-test.sh) return 1 ;;
    esac
  done <"$changed_paths_file"

  # Keep the same fail-closed graph proof as ~/.agents/fleet/gate-delta.sh:
  # enumerate the test dependency graph before deriving reverse dependencies.
  go list -deps -test ./... >/dev/null 2>&1 || return 1
  metadata_file="$work_dir/package-metadata.json"
  go list -test -json ./... >"$metadata_file" 2>/dev/null || return 1

  python3 - "$PWD" "$metadata_file" "$changed_paths_file" \
    "$changed_file" "$discovered_file" <<'PY' || return 1
import json
import os
import sys

root, metadata_path, paths_path, changed_out, closure_out = sys.argv[1:]
decoder = json.JSONDecoder()
raw = open(metadata_path, encoding="utf-8").read()
records = []
offset = 0
while offset < len(raw):
    while offset < len(raw) and raw[offset].isspace():
        offset += 1
    if offset == len(raw):
        break
    record, offset = decoder.raw_decode(raw, offset)
    records.append(record)

normal = {}
test_deps = {}
embedded = set()
for record in records:
    import_path = record.get("ImportPath", "")
    for_test = record.get("ForTest", "")
    if for_test:
        test_deps.setdefault(for_test, set()).update(record.get("Deps", []))
        continue
    if not import_path or import_path.endswith(".test") or " [" in import_path:
        continue
    directory = record.get("Dir", "")
    if not directory:
        continue
    rel_dir = os.path.relpath(directory, root)
    normal[import_path] = (rel_dir, set(record.get("Deps", [])))
    for key in ("EmbedFiles", "TestEmbedFiles", "XTestEmbedFiles"):
        for name in record.get(key, []):
            embedded.add(os.path.normpath(os.path.join(rel_dir, name)))

paths = [line.strip() for line in open(paths_path, encoding="utf-8") if line.strip()]
if any(path.startswith("internal/") and not path.endswith(".go") and
       os.path.normpath(path) in embedded for path in paths):
    raise SystemExit(4)

go_dirs = {os.path.dirname(path) or "." for path in paths if path.endswith(".go")}
changed = sorted(
    import_path for import_path, (rel_dir, _) in normal.items() if rel_dir in go_dirs
)
if go_dirs and not changed:
    raise SystemExit(5)

changed_set = set(changed)
closure = []
for import_path, (_, deps) in normal.items():
    combined_deps = deps | test_deps.get(import_path, set())
    if import_path in changed_set or combined_deps & changed_set:
        closure.append(import_path)
closure.sort()

with open(changed_out, "w", encoding="utf-8") as output:
    output.writelines(f"{package}\n" for package in changed)
with open(closure_out, "w", encoding="utf-8") as output:
    output.writelines(f"{package}\n" for package in closure)
PY
}

# Confirm the C toolchain the race detector needs (CGO). Fail loud and early
# rather than emit a confusing linker error mid-build.
require_c_toolchain() {
  if ! go env CC >/dev/null 2>&1 || [ -z "$(go env CC)" ]; then
    echo "ERROR: race detector needs CGO; 'go env CC' is empty" >&2
    exit 1
  fi
  local cc
  cc="$(go env CC)"
  if ! command -v "$cc" >/dev/null 2>&1; then
    echo "ERROR: race detector needs a C toolchain; CC='$cc' not found on PATH" >&2
    echo "       install a C compiler (gcc/clang) on the CI image" >&2
    exit 1
  fi
}

mode="${1:-}"
case "$mode" in
  short)
    require_c_toolchain
    active_pid=""
    active_pgid=""
    work_dir="$(mktemp -d "${TMPDIR:-/tmp}/race-test.XXXXXX")"

    process_group_alive() { # <process-group-id>
      kill -0 -- "-$1" 2>/dev/null
    }

    wait_for_process_group_exit() { # <process-group-id>
      local pgid="$1" attempts=0
      while process_group_alive "$pgid" && [ "$attempts" -lt 40 ]; do
        sleep 0.05
        attempts=$((attempts + 1))
      done
      ! process_group_alive "$pgid"
    }

    retire_active_process_group() {
      local pid="${active_pid:-}" pgid="${active_pgid:-}"
      [ -n "$pgid" ] || return 0

      if process_group_alive "$pgid"; then
        kill -TERM -- "-$pgid" 2>/dev/null || true
        wait_for_process_group_exit "$pgid" || true
      fi
      if process_group_alive "$pgid"; then
        kill -KILL -- "-$pgid" 2>/dev/null || true
        wait_for_process_group_exit "$pgid" || true
      fi
      [ -z "$pid" ] || wait "$pid" 2>/dev/null || true
      if process_group_alive "$pgid"; then
        echo "ERROR: race-test could not retire owned process group $pgid" >&2
        return 1
      fi
      active_pid=""
      active_pgid=""
    }

    run_race_phase() { # <write-mode> <go test arguments...>
      local write_mode="$1" phase_status=0
      shift
      set -m
      if [ "$write_mode" = truncate ]; then
        CGO_ENABLED=1 go test "$@" >"$json_log" &
      else
        CGO_ENABLED=1 go test "$@" >>"$json_log" &
      fi
      active_pid=$!
      active_pgid=$active_pid
      wait "$active_pid" || phase_status=$?
      set +m
      if ! retire_active_process_group; then
        exit 70
      fi
      return "$phase_status"
    }

    cleanup() {
      local status=$?
      trap - EXIT
      trap '' INT TERM
      retire_active_process_group || true
      rm -rf "$work_dir"
      exit "$status"
    }

    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM

    evidence_dir="${AGENT_EVIDENCE_DIR:-.agent-evidence}"
    evidence_file="$evidence_dir/race-short-packages.jsonl"
    discovery_evidence_file="$evidence_dir/race-short-discovery.json"
    mkdir -p "$evidence_dir"
    parser="$work_dir/parser.py"
    cat > "$parser" <<'PY'
import json
import sys

package_order = []
records = {}
for raw in sys.stdin:
    try:
        event = json.loads(raw)
    except json.JSONDecodeError:
        sys.stderr.write(raw)
        continue
    output = event.get("Output")
    if output:
        sys.stderr.write(output)
    pkg = event.get("Package", "")
    action = event.get("Action")
    if not pkg or action not in {"pass", "fail", "skip"}:
        continue
    if event.get("Test") or event.get("Example"):
        continue
    if pkg not in records:
        package_order.append(pkg)
    records[pkg] = {
        "package": pkg,
        "status": action,
        "elapsed_seconds": event.get("Elapsed", 0),
        "cached": bool(event.get("Cached", False)),
    }

with open(sys.argv[1], "w", encoding="utf-8") as f:
    for pkg in package_order:
        rec = records[pkg]
        f.write(json.dumps(rec, sort_keys=True))
        f.write("\n")
PY

    write_discovery_evidence() {
      local list_status="$1" discovered_file="$2" runnable_file="$3"
      local changed_file="$4" scope="$5" base_sha="$6"
      python3 - "$discovery_evidence_file" "$list_status" "$discovered_file" \
        "$runnable_file" "$changed_file" "$scope" "$base_sha" <<'PY'
import json
import sys

output_path, status, discovered_path, runnable_path, changed_path, scope, base_sha = sys.argv[1:]

def packages(path):
    with open(path, encoding="utf-8") as f:
        return [line.strip() for line in f if line.strip()]

discovered = packages(discovered_path)
runnable = packages(runnable_path)
changed = packages(changed_path)
with open(output_path, "w", encoding="utf-8") as f:
    json.dump(
        {
            "base_sha": base_sha,
            "changed_packages": changed,
            "closure_packages": discovered,
            "closure_size": len(discovered),
            "go_list_status": int(status),
            "discovered_count": len(discovered),
            "discovered_packages": discovered,
            "runnable_count": len(runnable),
            "runnable_packages": runnable,
            "scope": scope,
        },
        f,
        sort_keys=True,
    )
    f.write("\n")
PY
    }

    discovered_log="$work_dir/discovered.txt"
    changed_log="$work_dir/changed.txt"
    runnable_log="$work_dir/runnable.txt"
    : >"$changed_log"
    : >"$runnable_log"
    scope=full
    base_sha=""
    RACE_DIFF_BASE_SHA=""
    if race_short_diff_scope "$work_dir" "$discovered_log" "$changed_log"; then
      scope=diff
      base_sha="$RACE_DIFF_BASE_SHA"
      list_status=0
    else
      base_sha="$RACE_DIFF_BASE_SHA"
      set +e
      go list ./... >"$discovered_log"
      list_status=$?
      set -e
    fi
    if [ "$list_status" -ne 0 ]; then
      write_discovery_evidence "$list_status" "$discovered_log" "$runnable_log" \
        "$changed_log" "$scope" "$base_sha"
      echo "ERROR: race-test short package discovery failed (go list status $list_status)" >&2
      exit "$list_status"
    fi

    # All packages except the slow and hot integration-heavy packages. In diff
    # scope, directly changed excluded/hot packages run their full short suite.
    pkgs=()
    hot_pkgs=()
    promoted_pkgs=()
    pkg_count=0
    hot_pkg_count=0
    discovered_count=0
    changed_packages="$(cat "$changed_log")"
    while IFS= read -r p; do
      if [ -z "${p//[[:space:]]/}" ]; then
        continue
      fi
      discovered_count=$((discovered_count + 1))
      skip=0
      for s in "${RACE_SHORT_EXCLUDED_PKGS[@]}"; do
        if [ "$p" = "$s" ]; then
          if [ "$scope" = diff ] && grep -Fxq "$p" <<<"$changed_packages"; then
            promoted_pkgs+=("$p")
          fi
          skip=1
          break
        fi
      done
      if [ "$skip" -eq 0 ]; then
        for s in "${RACE_SHORT_HOT_PKGS[@]}"; do
          if [ "$p" = "$s" ]; then
            if [ "$scope" = diff ] && grep -Fxq "$p" <<<"$changed_packages"; then
              promoted_pkgs+=("$p")
            else
              hot_pkgs+=("$p")
            fi
            skip=1
            break
          fi
        done
      fi
      if [ "$skip" -eq 0 ]; then
        pkgs+=("$p")
        pkg_count=$((pkg_count + 1))
      fi
    done < "$discovered_log"
    if [ "$discovered_count" -eq 0 ]; then
      write_discovery_evidence "$list_status" "$discovered_log" "$runnable_log" \
        "$changed_log" "$scope" "$base_sha"
      if [ "$scope" = diff ]; then
        echo "==> race-test short: diff scope has no changed Go packages"
        exit 0
      fi
      echo "ERROR: race-test short package discovery returned zero packages" >&2
      exit 1
    fi
    hot_pkg_count="${#hot_pkgs[@]}"
    promoted_pkg_count="${#promoted_pkgs[@]}"
    if [ "$pkg_count" -gt 0 ]; then
      printf '%s\n' "${pkgs[@]}" >>"$runnable_log"
    fi
    if [ "$hot_pkg_count" -gt 0 ]; then
      printf '%s\n' "${hot_pkgs[@]}" >>"$runnable_log"
    fi
    if [ "$promoted_pkg_count" -gt 0 ]; then
      printf '%s\n' "${promoted_pkgs[@]}" >>"$runnable_log"
    fi
    if [ "$((pkg_count + hot_pkg_count + promoted_pkg_count))" -eq 0 ]; then
      write_discovery_evidence "$list_status" "$discovered_log" "$runnable_log" \
        "$changed_log" "$scope" "$base_sha"
      if [ "$scope" = diff ]; then
        echo "==> race-test short: diff closure contains only nightly-excluded packages"
        exit 0
      fi
      echo "ERROR: race-test short package discovery returned zero runnable packages" >&2
      exit 1
    fi
    write_discovery_evidence "$list_status" "$discovered_log" "$runnable_log" \
      "$changed_log" "$scope" "$base_sha"
    if [ "$scope" = diff ]; then
      echo "==> race-test short: scope=diff -race -short over $pkg_count closure packages + $promoted_pkg_count full promoted packages + $hot_pkg_count focused hot dependencies"
    else
      echo "==> race-test short: -race -short over $pkg_count packages + $hot_pkg_count focused hot packages (excludes graph/store/web; capped parallelism)"
    fi
    if [ "$pkg_count" -gt 0 ]; then
      echo "resolved ordinary: go test -race -short -count=1 -p 4 -parallel 2 -test.timeout=600s ${pkgs[*]}"
    fi
    if [ "$promoted_pkg_count" -gt 0 ]; then
      echo "resolved promoted: go test -race -short -count=1 -p 1 -parallel 2 -test.timeout=3600s ${promoted_pkgs[*]}"
    fi
    if [ "$hot_pkg_count" -gt 0 ]; then
      echo "resolved focused: go test -race -short -count=1 -p 4 -parallel 2 -test.timeout=600s -run $RACE_SHORT_HOT_TESTS ${hot_pkgs[*]}"
    fi
    if [ "${EXPLAIN:-0}" = 1 ]; then
      exit 0
    fi
    set +e
    json_log="$work_dir/go-test.jsonl"
    : >"$json_log"
    ordinary_status=0
    if [ "$pkg_count" -gt 0 ]; then
      run_race_phase truncate -race -short -count=1 -p 4 -parallel 2 \
        -test.timeout=600s -json "${pkgs[@]}"
      ordinary_status=$?
    fi
    promoted_status=0
    if [ "$promoted_pkg_count" -gt 0 ]; then
      run_race_phase append -race -short -count=1 -p 1 -parallel 2 \
        -test.timeout=3600s -json "${promoted_pkgs[@]}"
      promoted_status=$?
    fi
    focused_status=0
    if [ "$hot_pkg_count" -gt 0 ]; then
      run_race_phase append -race -short -count=1 -p 4 -parallel 2 \
        -test.timeout=600s -run "$RACE_SHORT_HOT_TESTS" -json "${hot_pkgs[@]}"
      focused_status=$?
    fi
    python3 "$parser" "$evidence_file" <"$json_log"
    parser_status=$?
    set -e
    # Every phase executes so the evidence stream remains complete. The final
    # failure follows the documented phase order, not whichever command ended
    # last: ordinary closure, promoted full suites, focused hot slice, parser.
    if [ "$ordinary_status" -ne 0 ]; then
      exit "$ordinary_status"
    fi
    if [ "$promoted_status" -ne 0 ]; then
      exit "$promoted_status"
    fi
    if [ "$focused_status" -ne 0 ]; then
      exit "$focused_status"
    fi
    if [ "$parser_status" -ne 0 ]; then
      exit "$parser_status"
    fi
    ;;
  full)
    require_c_toolchain
    echo "==> race-test full: -race ./... (raised timeout, capped parallelism)"
    # Cap -p (packages built/tested in parallel) and -parallel (tests within a
    # package) so the race detector's memory/CPU overhead on graph/store does
    # not thrash the runner into the timeout. Measured under these flags:
    # graph ~564s, store ~817s on an M-series dev box; the 1800s per-test budget
    # leaves headroom for the slower shared CI runners.
    CGO_ENABLED=1 go test -race -count=1 -p 2 -parallel 2 -test.timeout=1800s ./...
    ;;
  *)
    echo "usage: $0 {short|full}" >&2
    exit 2
    ;;
esac
