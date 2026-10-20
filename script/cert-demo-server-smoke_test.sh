#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
bash -n script/cert-demo-server.sh script/cert-demo-server_test.sh script/cert-demo-server-smoke.sh script/cert-demo-server-smoke_test.sh

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fixture="$tmp/fixture"
spy_bin="$tmp/bin"
mkdir -p "$fixture" "$spy_bin"
key="$tmp/review.key"
printf '%s\n' 'review-key-negative-test-value' > "$key"
chmod 0600 "$key"

cat > "$spy_bin/curl" <<'SPY'
#!/usr/bin/env bash
set -euo pipefail
output=""
config=""
url=""
method=""
args="$*"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output="$2"; shift 2 ;;
    --config) config="$2"; shift 2 ;;
    --request) method="$2"; shift 2 ;;
    --connect-timeout|--max-time|--write-out) shift 2 ;;
    --data-binary) shift 2 ;;
    --silent|--show-error) shift ;;
    http://*|https://*) url="$1"; shift ;;
    *) echo "unexpected curl argument" >&2; exit 96 ;;
  esac
done
[[ -n "$output" && -n "$config" && -n "$url" && -n "$method" ]]
for secret in review-key-negative-test-value session-negative device-negative secret-negative; do
  [[ "$args" != *"$secret"* ]] || exit 97
  while IFS='=' read -r name value; do
    [[ "$name" == SPY_* ]] && continue
    [[ "$value" != *"$secret"* ]] || exit 97
  done < <(env)
done
body="$(cat)"
case "$url" in
  */health)
    stage=health
    [[ -z "$body" ]]
    ;;
  */api/enroll)
    stage=enrollment
    [[ "$body" == '{"code":"review-key-negative-test-value","deviceName":"cert-demo-preflight"}' ]] || exit 98
    grep -Fq 'Content-Type: application/json' "$config" || exit 98
    ;;
  */api/graphql)
    stage=browse
    [[ "$body" == '{"query":"query DemoBrowse($id:String!){media(id:$id){id title}}","variables":{"id":"media-demo-bunny"}}' ]] || exit 98
    for header in 'Content-Type: application/json' 'X-Beamfall-Session: session-negative' 'X-Beamfall-Device: device-negative' 'X-Beamfall-Device-Secret: secret-negative'; do
      grep -Fq "$header" "$config" || exit 98
    done
    ;;
  */api/playback-info)
    stage=playback
    [[ "$body" == '{"mediaID":"media-demo-bunny","capabilityProfile":{"client":"store-review","containers":["mp4"],"directCodecs":["h264"]}}' ]] || exit 98
    for header in 'Content-Type: application/json' 'X-Beamfall-Session: session-negative' 'X-Beamfall-Device: device-negative' 'X-Beamfall-Device-Secret: secret-negative'; do
      grep -Fq "$header" "$config" || exit 98
    done
    ;;
  *) exit 99 ;;
esac
cp "$SPY_FIXTURE/$stage.json" "$output"
printf '%s' "$(<"$SPY_FIXTURE/$stage.status")"
SPY
chmod +x "$spy_bin/curl"

reset_fixture() {
  printf '200' > "$fixture/health.status"
  printf '{}' > "$fixture/health.json"
  printf '201' > "$fixture/enrollment.status"
  printf '%s' '{"deviceID":"device-negative","deviceSecret":"secret-negative","sessionID":"session-negative","deviceRole":"viewer","profileID":"profile-demo-default"}' > "$fixture/enrollment.json"
  printf '200' > "$fixture/browse.status"
  printf '%s' '{"data":{"media":{"id":"media-demo-bunny","title":"Big Buck Bunny"}}}' > "$fixture/browse.json"
  printf '200' > "$fixture/playback.status"
  printf '%s' '{"decision":"direct-play","streams":[{"url":"/api/media/demo"}]}' > "$fixture/playback.json"
}

run_smoke() {
  PATH="$spy_bin:$PATH" SPY_FIXTURE="$fixture" script/cert-demo-server-smoke.sh --url "${1:-http://127.0.0.1:8800}" --key-file "${2:-$key}"
}

assert_stage_fails() {
  local name="$1" stage="$2" output
  output="$tmp/$name.output"
  if run_smoke > "$output" 2>&1; then
    echo "expected $name to fail" >&2
    exit 1
  fi
  grep -Fq "FAIL $stage:" "$output"
  ! grep -Fq 'review-key-negative-test-value' "$output"
  ! grep -Fq 'secret-negative' "$output"
}

reset_fixture
[[ "$(run_smoke)" == PASS ]]
for valid in 'http://localhost' 'http://localhost:65535/' 'http://127.0.0.1' 'http://[::1]:8800' 'https://review.example'; do
  [[ "$(run_smoke "$valid")" == PASS ]]
done

url_case=0
for invalid in 'ftp://review.example' 'http://review.example' 'http://127.0.0.10' 'http://localhost.evil' 'http://user@localhost' 'http://localhost/path' 'http://localhost?query=1' 'http://localhost#fragment' 'http://localhost:0' 'http://localhost:65536' 'http://localhost:notaport'; do
  url_case=$((url_case + 1))
  output="$tmp/url-$url_case.output"
  if run_smoke "$invalid" > "$output" 2>&1; then exit 1; fi
  grep -Fq 'FAIL url:' "$output"
done

# The only request body transport is stdin from a 0600 file.  The curl spy
# above checks argv and environment for every secret on every wire stage.
rg -F -- '--data-binary @-' script/cert-demo-server-smoke.sh >/dev/null
! rg -e '(^|[^-])--data( |$)' -e 'CERT_DEMO.*=' script/cert-demo-server-smoke.sh

empty="$tmp/empty.key"; install -m 0600 /dev/null "$empty"
if run_smoke 'http://localhost' "$empty" > "$tmp/empty.output" 2>&1; then exit 1; fi
grep -Fq 'FAIL key:' "$tmp/empty.output"
wrong_mode="$tmp/mode.key"; install -m 0644 "$key" "$wrong_mode"
if run_smoke 'http://localhost' "$wrong_mode" > "$tmp/mode.output" 2>&1; then exit 1; fi
grep -Fq 'FAIL key:' "$tmp/mode.output"
symlink="$tmp/link.key"; ln -s "$key" "$symlink"
if run_smoke 'http://localhost' "$symlink" > "$tmp/link.output" 2>&1; then exit 1; fi
grep -Fq 'FAIL key:' "$tmp/link.output"
mkdir "$tmp/directory.key"
if run_smoke 'http://localhost' "$tmp/directory.key" > "$tmp/dir.output" 2>&1; then exit 1; fi
grep -Fq 'FAIL key:' "$tmp/dir.output"

reset_fixture; printf '503' > "$fixture/health.status"; assert_stage_fails health health
reset_fixture; printf '200' > "$fixture/enrollment.status"; assert_stage_fails enrollment-status enrollment
reset_fixture; printf '{' > "$fixture/enrollment.json"; assert_stage_fails enrollment-json enrollment
for field in deviceID deviceSecret sessionID; do
  reset_fixture
  python3 - "$fixture/enrollment.json" "$field" <<'PY'
import json, sys
path, field = sys.argv[1:]
with open(path, encoding="utf-8") as handle: value = json.load(handle)
value[field] = ""
with open(path, "w", encoding="utf-8") as handle: json.dump(value, handle, separators=(",", ":"))
PY
  assert_stage_fails "enrollment-$field" enrollment
done
reset_fixture; sed 's/"viewer"/"manager"/' "$fixture/enrollment.json" > "$fixture/enrollment.tmp"; mv "$fixture/enrollment.tmp" "$fixture/enrollment.json"; assert_stage_fails enrollment-role enrollment
reset_fixture; sed 's/profile-demo-default/profile-other/' "$fixture/enrollment.json" > "$fixture/enrollment.tmp"; mv "$fixture/enrollment.tmp" "$fixture/enrollment.json"; assert_stage_fails enrollment-profile enrollment
reset_fixture; sed 's/profile-demo-default/ScEnEs/' "$fixture/enrollment.json" > "$fixture/enrollment.tmp"; mv "$fixture/enrollment.tmp" "$fixture/enrollment.json"; assert_stage_fails enrollment-vocabulary vocabulary

reset_fixture; printf '500' > "$fixture/browse.status"; assert_stage_fails browse-status browse
reset_fixture; printf '%s' '{"errors":[{"message":"negative"}],"data":{"media":{"id":"media-demo-bunny","title":"Big Buck Bunny"}}}' > "$fixture/browse.json"; assert_stage_fails browse-errors browse
reset_fixture; printf '%s' '{"data":{"media":{"id":"wrong","title":"Big Buck Bunny"}}}' > "$fixture/browse.json"; assert_stage_fails browse-id browse
reset_fixture; printf '%s' '{"data":{"media":{"id":"media-demo-bunny","title":"wrong"}}}' > "$fixture/browse.json"; assert_stage_fails browse-title browse
reset_fixture; printf '%s' '{"data":{"media":{"id":"media-demo-bunny","title":"MixedCase PoRn"}}}' > "$fixture/browse.json"; assert_stage_fails browse-vocabulary vocabulary

reset_fixture; printf '500' > "$fixture/playback.status"; assert_stage_fails playback-status playback
reset_fixture; printf '%s' '{"decision":"unavailable","streams":[{}]}' > "$fixture/playback.json"; assert_stage_fails playback-unavailable playback
reset_fixture; printf '%s' '{"decision":"direct-play","streams":[]}' > "$fixture/playback.json"; assert_stage_fails playback-streams playback
reset_fixture; printf '%s' '{"decision":"direct-play","streams":[{"url":"/GaLlErIeS"}]}' > "$fixture/playback.json"; assert_stage_fails playback-vocabulary vocabulary

for header in X-Beamfall-Session X-Beamfall-Device X-Beamfall-Device-Secret; do
  reset_fixture
  mutated="$tmp/mutated-$header.sh"
  sed "s/$header/X-Negative-Removed/" script/cert-demo-server-smoke.sh > "$mutated"
  chmod +x "$mutated"
  if PATH="$spy_bin:$PATH" SPY_FIXTURE="$fixture" "$mutated" --url http://localhost --key-file "$key" > "$tmp/header.output" 2>&1; then exit 1; fi
  grep -Fq 'FAIL browse:' "$tmp/header.output"
done

reset_fixture
leaky="$tmp/leaky.sh"
sed '/} < "$credentials"/a\
export CERT_DEMO_NEGATIVE_LEAK="$session_id"' script/cert-demo-server-smoke.sh > "$leaky"
chmod +x "$leaky"
if PATH="$spy_bin:$PATH" SPY_FIXTURE="$fixture" "$leaky" --url http://localhost --key-file "$key" > "$tmp/leak.output" 2>&1; then exit 1; fi
grep -Fq 'FAIL browse:' "$tmp/leak.output"
! grep -Fq 'session-negative' "$tmp/leak.output"

for args in '' '--url http://localhost' '--key-file x' '--url http://localhost --url http://localhost --key-file x' '--url http://localhost --key-file x --unknown'; do
  if eval "script/cert-demo-server-smoke.sh $args" > "$tmp/args.output" 2>&1; then exit 1; fi
done
script/cert-demo-server-smoke.sh --help > /dev/null 2>&1

# Deterministic gate mutation-proof.  The probe copies only gate.sh and the two
# stubbed focused tests, so the process-group supervisor gate.sh re-execs is
# absent from the probe root; run the probe with GATE_PROCESS_SUPERVISED=1 to
# skip that orthogonal wrapper and exercise the cert-demo blocking steps against
# the probe-root stubs (script/<name> resolves relative to GATE_ROOT).
gate_probe="$tmp/gate-probe"
mkdir -p "$gate_probe/tools/gate" "$gate_probe/script"
cp tools/gate/gate.sh "$gate_probe/tools/gate/gate.sh"
run_gate_probe() {
  GATE_PROCESS_SUPERVISED=1 GATE_CERT_DEMO_ONLY=1 bash "$gate_probe/tools/gate/gate.sh"
}
for failed in cert-demo-server_test.sh cert-demo-server-smoke_test.sh; do
  for focused in cert-demo-server_test.sh cert-demo-server-smoke_test.sh; do
    if [[ "$focused" == "$failed" ]]; then result=1; else result=0; fi
    printf '#!/usr/bin/env bash\nexit %s\n' "$result" > "$gate_probe/script/$focused"
    chmod +x "$gate_probe/script/$focused"
  done
  if run_gate_probe > "$tmp/gate-$failed.output" 2>&1; then
    echo "gate accepted failing $failed" >&2
    exit 1
  fi
  grep -Fq "FAIL certification demo server" "$tmp/gate-$failed.output"
done
for focused in cert-demo-server_test.sh cert-demo-server-smoke_test.sh; do
  printf '#!/usr/bin/env bash\nexit 0\n' > "$gate_probe/script/$focused"
  chmod +x "$gate_probe/script/$focused"
done
run_gate_probe > "$tmp/gate-pass.output" 2>&1
grep -Fq 'PASS certification demo server test' "$tmp/gate-pass.output"
grep -Fq 'PASS certification demo server smoke test' "$tmp/gate-pass.output"

echo "cert demo server smoke tests: passed"
