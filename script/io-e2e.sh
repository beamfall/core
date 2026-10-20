#!/usr/bin/env bash
# io-e2e.sh — single agent-facing entry point for the Core Server I/O
# integration + E2E test track.
#
# Design goal: easy to USE, DEBUG, and RUN.
#   - one script, discoverable subcommands, `help` lists them all
#   - `doctor` proves the environment before anything else runs (fail-loud, never silent-skip)
#   - greppable status lines: every check prints `PASS`/`FAIL`/`SKIP` + a remediation hint
#   - `--verbose` turns on `set -x` for step-by-step debugging
#   - deterministic exit codes: 0 ok · 1 runtime/check failure · 2 usage error
#
# Subcommands:
#   doctor      verify the toolchain (go, ffmpeg, ffprobe, lavfi+libx264) — run this first
#   fixtures    (re)generate the gitignored lavfi bulk test fixtures + manifest
#   ledger ...  io-ops coverage ledger — delegates to `go run ./cmd/ioe2e`     [D2/F1/F3]
#               (e.g. `ledger list`, `ledger check`, `ledger release-check X.xml`)
#   journeys .. slow tier: real ffmpeg ops + lifecycle happy-paths over real    [D5/D8]
#               media (-tags io_e2e ./internal/ioe2e). Passes `go test` args through.
#   journeys-junit <out.xml>  same as journeys but emits JUnit XML to <out.xml>
#               (uses go-junit-report; required before ledger release-check).
#   fast        fast `io` execution tier: deterministic probe + loopback        [D8]
#               doubles (-tags io ./internal/ioe2e). Needs ffmpeg + fixtures
#               for the probe leg; excludes the slow io_e2e lifecycle journeys.
#   gate        fast inner-loop tier: the `fast` io tier + io-ops ledger        [D8]
#               completeness check (every op covered/ignored/documented-blocked).
#   smoke       built-binary boot smoke: build cmd/beamfall, boot it on a        [D1]
#               loopback port over the conformance seed, prove /health 200, tear
#               down. Fail-loud, no live network. In release-gate, not `gate`.
#   live        INTEGRATION_LIVE egress drift smoke — REAL outbound requests,    [D4]
#               opt-in twice (io_live tag + INTEGRATION_LIVE=1). Refuses (zero
#               egress) without the opt-in. NEVER in `gate`/`fast`/`journeys`;
#               weekly CI drift tier. Endpoints/creds via BEAMFALL_LIVE_* env.
#   help        print this help
set -euo pipefail

# ── locate repo root (independent of caller cwd) ────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FIXTURE_ROOT="$REPO_ROOT/testing/io-fixtures"
GENERATED_DIR="$FIXTURE_ROOT/generated"

# ── output helpers (greppable, agent-friendly) ──────────────────────────────
pass() { printf 'PASS  %s\n' "$*"; }
fail() { printf 'FAIL  %s\n' "$*" >&2; }
skip() { printf 'SKIP  %s\n' "$*"; }
info() { printf '      %s\n' "$*"; }
err()  { printf 'ERROR %s\n' "$*" >&2; }

usage() {
  sed -n '2,33p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

# ── doctor: prove the environment, fail loud ────────────────────────────────
cmd_doctor() {
  local ok=0
  if command -v go >/dev/null 2>&1; then
    pass "go            $(go version | awk '{print $3}')"
  else
    fail "go            not found — install Go 1.26+"; ok=1
  fi

  if command -v ffmpeg >/dev/null 2>&1; then
    pass "ffmpeg        $(ffmpeg -version 2>/dev/null | head -1 | awk '{print $3}')"
  else
    fail "ffmpeg        not found — the I/O suite REQUIRES it (brew install ffmpeg)"; ok=1
  fi

  if command -v ffprobe >/dev/null 2>&1; then
    pass "ffprobe       $(ffprobe -version 2>/dev/null | head -1 | awk '{print $3}')"
  else
    fail "ffprobe       not found — install ffmpeg (ships ffprobe)"; ok=1
  fi

  # Prove lavfi + libx264 + aac actually work by generating a 1-frame real clip.
  if command -v ffmpeg >/dev/null 2>&1; then
    local probe; probe="$(mktemp -d)"
    if ffmpeg -y -v error \
        -f lavfi -i "testsrc=duration=1:size=160x120:rate=24" \
        -f lavfi -i "sine=frequency=440:duration=1" \
        -shortest -map_metadata -1 \
        -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac \
        "$probe/smoke.mp4" >/dev/null 2>&1; then
      local streams; streams="$(ffprobe -v error -show_entries stream=codec_name -of csv=p=0 "$probe/smoke.mp4" | paste -sd, -)"
      pass "lavfi+libx264 real clip generated ($streams)"
    else
      fail "lavfi+libx264 generation failed — this ffmpeg build lacks libx264/aac"; ok=1
    fi
    rm -rf "$probe"
  fi

  if [[ $ok -eq 0 ]]; then
    pass "environment ready for the I/O E2E suite"
  else
    err "environment NOT ready — fix the FAIL lines above"
  fi
  return $ok
}

# ── fixtures: generate the gitignored lavfi bulk set + manifest ─────────────
# Tiny, real, deterministic-bytes fixtures (1–2s). The committed *quirk* samples
# (chapters/PGS/DV/HDR10+) are a separate build script per D3 — not generated here.
sha256_file() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else sha256sum "$1" | awk '{print $1}'; fi
}
probe1() { ffprobe -v error "$@" -of default=noprint_wrappers=1:nokey=1 | head -1; }

_manifest_entries=()
_emit() { # logical-name path
  local name="$1" path="$2" codec dur
  codec="$(probe1 -select_streams v:0 -show_entries stream=codec_name "$path")"
  [[ -z "$codec" ]] && codec="$(probe1 -select_streams a:0 -show_entries stream=codec_name "$path")"
  dur="$(probe1 -show_entries format=duration "$path")"
  _manifest_entries+=("$(printf '{"name":"%s","file":"%s","sha256":"%s","codec":"%s","durationSec":"%s"}' \
    "$name" "$(basename "$path")" "$(sha256_file "$path")" "$codec" "$dur")")
  info "generated $(basename "$path")  ($codec, ${dur}s)"
}

cmd_fixtures() {
  command -v ffmpeg  >/dev/null 2>&1 || { err "ffmpeg required — run 'io-e2e.sh doctor'"; return 1; }
  command -v ffprobe >/dev/null 2>&1 || { err "ffprobe required — run 'io-e2e.sh doctor'"; return 1; }
  mkdir -p "$GENERATED_DIR"

  local F="$GENERATED_DIR"
  # av_basic: h264 + aac mp4 — the workhorse (probe, FrameGrab, ladder source)
  ffmpeg -y -v error -f lavfi -i "testsrc2=size=640x360:rate=24:duration=2" \
    -f lavfi -i "sine=frequency=440:duration=2" -shortest -map_metadata -1 \
    -c:v libx264 -preset ultrafast -crf 30 -pix_fmt yuv420p -c:a aac -b:a 64k \
    -movflags +faststart "$F/av_basic.mp4"
  _emit av_basic "$F/av_basic.mp4"

  # av_71: 7.1 audio (8 sine channels via amerge) in mkv — multi-track probe
  ffmpeg -y -v error -f lavfi -i "testsrc2=size=320x180:rate=24:duration=2" \
    -f lavfi -i "sine=frequency=200:duration=2" -f lavfi -i "sine=frequency=250:duration=2" \
    -f lavfi -i "sine=frequency=300:duration=2" -f lavfi -i "sine=frequency=350:duration=2" \
    -f lavfi -i "sine=frequency=400:duration=2" -f lavfi -i "sine=frequency=450:duration=2" \
    -f lavfi -i "sine=frequency=500:duration=2" -f lavfi -i "sine=frequency=550:duration=2" \
    -filter_complex "[1:a][2:a][3:a][4:a][5:a][6:a][7:a][8:a]amerge=inputs=8[a]" \
    -map 0:v -map "[a]" -shortest -map_metadata -1 \
    -c:v libx264 -preset ultrafast -crf 32 -pix_fmt yuv420p -c:a aac -b:a 96k \
    -channel_layout 7.1 "$F/av_71.mkv"
  _emit av_71 "$F/av_71.mkv"

  # audio_sine: audio-only flac — LoudnessMeasure, WaveformAmplitudes, AudioFingerprint
  ffmpeg -y -v error -f lavfi -i "sine=frequency=440:duration=2" \
    -map_metadata -1 -c:a flac "$F/audio_sine.flac"
  _emit audio_sine "$F/audio_sine.flac"

  # silence_gaps: tone-silence-tone wav — SilenceSpans. The quiet region runs
  # 1.0s–3.5s (a 2.5s gap), deliberately above the detector's 2.0s floor
  # (silenceMinDurationSec, ADR-0052 §2.4(a)) so the journey asserts a REAL
  # detected span, not just shape.
  ffmpeg -y -v error \
    -f lavfi -i "aevalsrc='sin(440*2*PI*t)*lt(t\,1)+sin(440*2*PI*t)*gt(t\,3.5)':d=4.5" \
    -map_metadata -1 -c:a pcm_s16le "$F/silence_gaps.wav"
  _emit silence_gaps "$F/silence_gaps.wav"

  # bars_mpegts: mpeg2 in mpegts — transcode-only source path
  ffmpeg -y -v error -f lavfi -i "smptebars=size=640x360:rate=24:duration=2" \
    -f lavfi -i "sine=frequency=330:duration=2" -shortest -map_metadata -1 \
    -c:v mpeg2video -q:v 8 -c:a mp2 -b:a 64k -f mpegts "$F/bars_mpegts.ts"
  _emit bars_mpegts "$F/bars_mpegts.ts"

  # manifest.json — logical name → file + external sha256 digest (feeds D6 #10 cross-check)
  {
    printf '{\n  "schemaVersion": 1,\n  "note": "Generated by script/io-e2e.sh fixtures — gitignored, do not commit. Regenerate with: script/io-e2e.sh fixtures",\n  "fixtures": [\n'
    local i
    for i in "${!_manifest_entries[@]}"; do
      [[ $i -gt 0 ]] && printf ',\n'
      printf '    %s' "${_manifest_entries[$i]}"
    done
    printf '\n  ]\n}\n'
  } > "$GENERATED_DIR/manifest.json"
  pass "fixtures generated in $GENERATED_DIR (${#_manifest_entries[@]} files + manifest.json)"
}

# ── ledger: io-ops coverage, delegated to the Go tool (F1: Go owns codebase
# reasoning; shell is just the discoverable facade). Passes args through:
#   ledger list | ledger check | ledger release-check <junit.xml>
cmd_ledger() {
  command -v go >/dev/null 2>&1 || { err "go required — run 'io-e2e.sh doctor'"; return 1; }
  ( cd "$REPO_ROOT" && go run ./cmd/ioe2e "${@:-list}" )
}

# ── journeys: the slow tier — real ffmpeg ops + lifecycle happy-paths over real
# media (//go:build io_e2e, internal/ioe2e). Fail-loud on missing ffmpeg via the
# per-test ffmpegtest.Require guard. Pass extra `go test` args through, e.g.
#   journeys -run TestProbeJourney -v
# Regenerate fixtures first if they're stale: `io-e2e.sh fixtures`.
cmd_journeys() {
  command -v go     >/dev/null 2>&1 || { err "go required — run 'io-e2e.sh doctor'"; return 1; }
  command -v ffmpeg >/dev/null 2>&1 || { err "ffmpeg required for journeys — run 'io-e2e.sh doctor'"; return 3; }
  if [[ ! -f "$GENERATED_DIR/manifest.json" ]]; then
    err "fixtures missing — run 'io-e2e.sh fixtures' first"; return 2
  fi
  ( cd "$REPO_ROOT" && go test -tags io_e2e ./internal/ioe2e/... "$@" )
}

# ── journeys-junit: slow tier, JUnit XML output (for release-check). Requires
# go-junit-report (fetched via `go run` at module version v2.1.0 — no go.mod entry
# needed). Usage: journeys-junit <output.xml> [extra go test flags...]
# The ffmpeg requirement documentation: release CI must install ffmpeg before
# invoking this target (e.g. `apt-get install -y ffmpeg` on ubuntu-latest).
cmd_journeys_junit() {
  command -v go     >/dev/null 2>&1 || { err "go required — run 'io-e2e.sh doctor'"; return 1; }
  command -v ffmpeg >/dev/null 2>&1 || { err "ffmpeg required — release CI: apt-get install -y ffmpeg"; return 3; }
  if [[ ! -f "$GENERATED_DIR/manifest.json" ]]; then
    err "fixtures missing — run 'io-e2e.sh fixtures' first"; return 2
  fi
  if [[ $# -lt 1 ]]; then
    err "usage: journeys-junit <output.xml> [go test flags...]"; return 2
  fi
  local out="$1"; shift
  local pkgs
  if ! pkgs="$(release_marker_packages)"; then
    err "could not resolve release marker packages — run 'ioe2e release-packages'"; return 1
  fi
  local -a targets=("./internal/ioe2e/...")
  local pkg
  while IFS= read -r pkg; do
    # internal/ioe2e is already covered by the recursive pattern above; listing
    # it again would run the package twice.
    [[ -n "$pkg" && "$pkg" != ./internal/ioe2e* ]] || continue
    targets+=("$pkg")
  done <<<"$pkgs"
  info "running io_e2e journeys → JUnit XML: $out (packages: ${targets[*]})"
  # go-junit-report v2 reads go test -v -json on stdin — but ONLY with `-parser
  # gojson`. Its default `gotest` parser silently misreads JSON lines as plain
  # test log and drops every package name, so the report cannot say which
  # package proved an id. release-check binds each marker to its package, so the
  # parser must be named explicitly.
  ( cd "$REPO_ROOT" && go test -tags io_e2e -v -json "${targets[@]}" "$@" ) \
    | go run github.com/jstemmer/go-junit-report/v2@v2.1.0 -parser gojson -set-exit-code \
    > "$out"
}

# release_marker_packages prints the go test package arguments that declare
# release io markers. Derived from cmd/ioe2e so producer scope and the release
# obligation cannot drift apart: an id whose marker lives outside the journey
# tree would otherwise be required to pass in a report that never ran it.
release_marker_packages() {
  local pkgs
  pkgs="$( cd "$REPO_ROOT" && go run ./cmd/ioe2e release-packages )" || return 1
  [[ -n "$pkgs" ]] || return 1
  printf '%s\n' "$pkgs"
}

# ── fast: the fast `io` execution tier (E2E-IO-FAST-TIER). Deterministic
# probe + loopback-double tests (//go:build io): real ffmpeg *probe* over the
# generated fixtures plus the egress emitters driven against httptest doubles.
# Fast (sub-second) and deterministic — it deliberately excludes the slow
# `io_e2e` lifecycle journeys (D5 depth: real transcodes, apptest boot). Needs
# ffmpeg + generated fixtures for the probe leg (same prereqs as `journeys`).
cmd_fast() {
  command -v go     >/dev/null 2>&1 || { err "go required — run 'io-e2e.sh doctor'"; return 1; }
  command -v ffmpeg >/dev/null 2>&1 || { err "ffmpeg required for the probe leg — run 'io-e2e.sh doctor'"; return 3; }
  if [[ ! -f "$GENERATED_DIR/manifest.json" ]]; then
    err "fixtures missing — run 'io-e2e.sh fixtures' first"; return 2
  fi
  ( cd "$REPO_ROOT" && go test -tags io ./internal/ioe2e/... "$@" )
}

# ── gate: the fast inner-loop tier (D8 + E2E-IO-FAST-TIER). Runs the fast `io`
# execution tier (probe + loopback doubles, see `fast`) AND the io-ops ledger
# completeness check — every io-op must be covered by a marker, //io:ignore'd,
# or documented-blocked in testing/io-ops.yaml. The slow `io_e2e` lifecycle
# journeys (D5 depth) stay OUT of this gate; run them via `journeys`. The
# executed-not-just-marked cross-check is `ledger release-check <junit>`.
cmd_gate() {
  command -v go >/dev/null 2>&1 || { err "go required — run 'io-e2e.sh doctor'"; return 1; }
  cmd_fast || return $?
  ( cd "$REPO_ROOT" && go run ./cmd/ioe2e check )
}

# ── smoke: the D1 built-binary boot smoke. Builds cmd/beamfall as a release
# artifact, boots it on a loopback ephemeral port over the deterministic
# conformance seed (no live network: discovery off, no ingest/ACME/public
# domain), and proves it serves /health 200 (ready) — then tears it down.
# Fail-loud: non-zero on build failure, an early process exit during boot, or
# /health not ready within the deadline (the server's logs are dumped on FAIL).
# This is the release-artifact counterpart to the in-process apptest journeys;
# it is intentionally NOT in the fast `gate` (it builds + boots), it runs in the
# release-gate and standalone via `make io-smoke`.
cmd_smoke() {
  command -v go >/dev/null 2>&1 || { err "go required — run 'io-e2e.sh doctor'"; return 1; }
  command -v curl >/dev/null 2>&1 || { err "curl required for the /health readiness probe"; return 1; }
  command -v python3 >/dev/null 2>&1 || { err "python3 required to pick a free loopback port"; return 1; }

  local tmp; tmp="$(mktemp -d)"
  local bin="$tmp/beamfall" datadir="$tmp/data" logf="$tmp/serve.log" pid="" rc=0
  mkdir -p "$datadir"
  # shellcheck disable=SC2329
  _smoke_cleanup() {
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true   # server exits 143 on SIGTERM (no signal handler) — expected
    fi
    rm -rf "$tmp"
  }

  info "building embedded web app"
  if ! ( cd "$REPO_ROOT" && npm --prefix internal/web/app run build ); then
    err "npm --prefix internal/web/app run build failed — embedded web app does not compile"; _smoke_cleanup; return 1
  fi
  info "building release artifact: go build ./cmd/beamfall"
  if ! ( cd "$REPO_ROOT" && go build -o "$bin" ./cmd/beamfall ); then
    err "go build ./cmd/beamfall failed — release artifact does not compile"; _smoke_cleanup; return 1
  fi

  local port; port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
  info "booting: beamfall serve --addr 127.0.0.1:$port --seed=conformance (loopback, no live network)"
  "$bin" serve --addr "127.0.0.1:$port" --data-dir "$datadir" \
    --seed=conformance --allow-conformance-seed --discovery-enabled=false >"$logf" 2>&1 &
  pid=$!

  local deadline=$(( SECONDS + 30 )) code=""
  while [[ $SECONDS -lt $deadline ]]; do
    if ! kill -0 "$pid" 2>/dev/null; then
      err "server process exited during boot (fail-loud) — last log:"; tail -n "${BEAMFALL_LOG_TAIL:-120}" "$logf" | sed 's/^/  serve| /' >&2
      pid=""; _smoke_cleanup; return 1
    fi
    code="$(curl -s -o /dev/null -m 2 -w '%{http_code}' "http://127.0.0.1:$port/health" 2>/dev/null || true)"
    [[ "$code" == "200" ]] && break
    sleep 0.3
  done
  if [[ "$code" != "200" ]]; then
    err "server did not report /health ready (got '${code:-no-response}') within 30s — last log:"
    tail -n "${BEAMFALL_LOG_TAIL:-120}" "$logf" | sed 's/^/  serve| /' >&2; _smoke_cleanup; return 1
  fi

  pass "release binary booted + /health 200 ready on 127.0.0.1:$port (conformance seed, no live network)"
  _smoke_cleanup
  pass "binary smoke OK — boot + serve proven, no live egress"
  return 0
}

# ── live: the D4 live-egress drift tier (E2E-IO-LIVE-EGRESS). Opt-in TWICE:
# the `io_live` build tag (compiled into no other tier) AND INTEGRATION_LIVE=1.
# This is the ONLY entry point that performs real outbound egress; it is never
# in `gate`, `fast`, or `journeys`. Without INTEGRATION_LIVE=1 it refuses with a
# clear message and performs ZERO egress (so a stray `live` in a local loop is a
# loud no-op, never a silent live request). Each egress leg points the SAME
# production client used by the loopback tier at a real endpoint via env
# (BEAMFALL_LIVE_*); a leg with no endpoint/creds SKIPs. The updatecheck canary
# needs only INTEGRATION_LIVE=1 (public, signature-verified). Intended cadence:
# weekly drift smoke in CI, not the inner loop. Pass extra `go test` args
# through, e.g. `live -run UpdateCheck -v`.
cmd_live() {
  command -v go >/dev/null 2>&1 || { err "go required — run 'io-e2e.sh doctor'"; return 1; }
  if [[ "${INTEGRATION_LIVE:-}" != "1" ]]; then
    err "live egress is opt-in — set INTEGRATION_LIVE=1 to run the drift tier"
    info "this tier performs REAL outbound requests; it is never part of 'gate'"
    info "configure endpoints/creds via BEAMFALL_LIVE_* env (see internal/ioe2e/live_test.go)"
    return 2
  fi
  info "INTEGRATION_LIVE=1 — running the live-egress drift tier (real outbound requests)"
  ( cd "$REPO_ROOT" && INTEGRATION_LIVE=1 go test -tags io_live ./internal/ioe2e/... "$@" )
}

# ── not-yet-built subcommands: honest, never a silent no-op ──────────────────
not_yet() {
  err "subcommand '$1' is planned but not built yet"
  info "tracked as decision $2"
  return 2
}

main() {
  local verbose=0
  while [[ $# -gt 0 && "$1" == --* ]]; do
    case "$1" in
      --verbose) verbose=1; shift ;;
      --help) usage; exit 0 ;;
      *) err "unknown flag: $1"; usage; exit 2 ;;
    esac
  done
  [[ $verbose -eq 1 ]] && set -x
  local sub="${1:-help}"; shift || true
  case "$sub" in
    doctor)   cmd_doctor "$@" ;;
    fixtures) cmd_fixtures "$@" ;;
    ledger)   cmd_ledger "$@" ;;
    journeys)       cmd_journeys "$@" ;;
    journeys-junit) cmd_journeys_junit "$@" ;;
    fast)           cmd_fast "$@" ;;
    gate)           cmd_gate "$@" ;;
    smoke)          cmd_smoke "$@" ;;
    live)           cmd_live "$@" ;;
    help|-h|--help) usage ;;
    *) err "unknown subcommand: $sub"; usage; exit 2 ;;
  esac
}
main "$@"
