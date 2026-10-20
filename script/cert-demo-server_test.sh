#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
bash -n script/cert-demo-server.sh script/cert-demo-server_test.sh script/cert-demo-server-smoke.sh script/cert-demo-server-smoke_test.sh

root="$(mktemp -d)"
data_dir="$root/data"
bin="$root/bin/beamfall"
logs="$root/logs"
server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$root"
}
trap cleanup EXIT

[[ ! -e "$data_dir" ]]
install -d -m 0700 "$(dirname "$bin")" "$logs"
go build -o "$bin" ./cmd/beamfall
: > "$logs/prepare.log"
chmod 0600 "$logs/prepare.log"
script/cert-demo-server.sh --data-dir "$data_dir" --prepare-only > "$logs/prepare.log" 2>&1

key="$data_dir/secrets/review-demo-enrollment-key"
manifest="$data_dir/seed-media/demo/manifest.json"
[[ ! -L "$key" && -f "$key" && -s "$key" ]]
[[ "$(stat -c %a "$key" 2>/dev/null || stat -f %Lp "$key")" == 600 ]]
[[ -s "$manifest" ]]
top_entries="$(find "$data_dir" -mindepth 1 -maxdepth 1 -exec basename {} \; | sort | tr '\n' ' ')"
[[ "$top_entries" == "secrets seed-media " ]]
secret_entries="$(find "$data_dir/secrets" -mindepth 1 -print)"
[[ "$secret_entries" == "$key" ]]

port="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
: > "$logs/server.stdout"
: > "$logs/server.stderr"
chmod 0600 "$logs/server.stdout" "$logs/server.stderr"
BEAMFALL_BIN="$bin" script/cert-demo-server.sh --data-dir "$data_dir" --addr "127.0.0.1:$port" \
  > "$logs/server.stdout" 2> "$logs/server.stderr" &
server_pid=$!

deadline=$((SECONDS + 30))
until curl --silent --max-time 2 --output /dev/null "http://127.0.0.1:$port/health" 2>/dev/null; do
  kill -0 "$server_pid" 2>/dev/null || { echo "server exited before health" >&2; exit 1; }
  [[ "$SECONDS" -lt "$deadline" ]] || { echo "health timeout" >&2; exit 1; }
  sleep 1
done

smoke_output="$logs/smoke.output"
: > "$smoke_output"
chmod 0600 "$smoke_output"
script/cert-demo-server-smoke.sh --url "http://127.0.0.1:$port" --key-file "$key" > "$smoke_output" 2>&1
[[ "$(<"$smoke_output")" == PASS ]]

kill -TERM "$server_pid"
wait "$server_pid"
server_pid=""

deny_re='adult|performer(s)?|studio(s)?|scene(s)?|gallery|galleries|porn'
! grep -Eiq "$deny_re" "$manifest" "$logs/server.stdout" "$logs/server.stderr" "$smoke_output"
raw_key="$(tr -d '\r\n' < "$key")"
while IFS= read -r candidate; do
  [[ "$candidate" == "$key" ]] && continue
  if [[ -f "$candidate" ]] && grep -Fq -- "$raw_key" "$candidate" 2>/dev/null; then
    echo "raw key leaked outside key file" >&2
    exit 1
  fi
done < <(find "$data_dir" "$logs" -type f -print)

assert_prepare_fails() {
  local name="$1" stage="$2" dir="$3" output
  output="$root/$name.output"
  if script/cert-demo-server.sh --data-dir "$dir" --prepare-only > "$output" 2>&1; then
    echo "expected $name to fail" >&2
    exit 1
  fi
  grep -Fq "FAIL $stage:" "$output"
  ! grep -Fq -- "$raw_key" "$output"
}

bad="$root/bad-inventory"
mkdir -p "$bad"
touch "$bad/plugin-state"
assert_prepare_fails inventory inventory "$bad"

# Fresh preflight data is intentionally limited to the generated seed corpus
# and the sole review key: no Integration Worker state, artifacts, config, or
# contributed records may be carried into this shipping-Core path.
for external in integration-worker-state integration-worker-artifact integration-worker-config integration-worker-contributed-demo; do
  bad="$root/bad-$external"
  mkdir -p "$bad"
  touch "$bad/$external"
  assert_prepare_fails "$external" inventory "$bad"
done

bad="$root/bad-symlink-data"
ln -s "$data_dir" "$bad"
assert_prepare_fails symlink-data inventory "$bad"

make_fixture() {
  local dir="$1"
  mkdir -p "$dir/secrets"
  cp -R "$data_dir/seed-media" "$dir/seed-media"
}

bad="$root/bad-extra-secret"
make_fixture "$bad"
cp "$key" "$bad/secrets/review-demo-enrollment-key"
touch "$bad/secrets/integration-worker"
assert_prepare_fails extra-secret inventory "$bad"

bad="$root/bad-symlink-key"
make_fixture "$bad"
ln -s "$key" "$bad/secrets/review-demo-enrollment-key"
assert_prepare_fails symlink-key key "$bad"

bad="$root/bad-empty-key"
make_fixture "$bad"
install -m 0600 /dev/null "$bad/secrets/review-demo-enrollment-key"
assert_prepare_fails empty-key key "$bad"

bad="$root/bad-mode-key"
make_fixture "$bad"
install -m 0644 "$key" "$bad/secrets/review-demo-enrollment-key"
assert_prepare_fails mode-key key "$bad"

bad="$root/bad-manifest-vocab"
make_fixture "$bad"
install -m 0600 "$key" "$bad/secrets/review-demo-enrollment-key"
python3 - "$bad/seed-media/demo/manifest.json" <<'PY'
import json
import sys
path = sys.argv[1]
with open(path, encoding="utf-8") as handle:
    value = json.load(handle)
value["files"][0]["mediaID"] = "media-AdUlT-negative"
value["files"][0]["path"] = "/tmp/PerFormerS-negative.mp4"
with open(path, "w", encoding="utf-8") as handle:
    json.dump(value, handle)
PY
assert_prepare_fails manifest-vocabulary vocabulary "$bad"

echo "cert demo server tests: passed"
