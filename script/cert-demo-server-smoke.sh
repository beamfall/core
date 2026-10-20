#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: script/cert-demo-server-smoke.sh --url URL --key-file PATH

Checks the certification demo's health, enrollment, browse, and playback wire
path. The key must be a non-empty, non-symlink regular file with mode 0600.
USAGE
}

fail() {
  printf 'FAIL %s: %s\n' "$1" "$2" >&2
  exit 1
}

url=""
key_file=""
seen_url=0
seen_key=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)
      [[ "$seen_url" -eq 0 && $# -ge 2 ]] || { usage; exit 2; }
      url="$2"; seen_url=1; shift 2
      ;;
    --key-file)
      [[ "$seen_key" -eq 0 && $# -ge 2 ]] || { usage; exit 2; }
      key_file="$2"; seen_key=1; shift 2
      ;;
    -h|--help)
      [[ $# -eq 1 ]] || { usage; exit 2; }
      usage
      exit 0
      ;;
    *) usage; exit 2 ;;
  esac
done
[[ "$seen_url" -eq 1 && "$seen_key" -eq 1 && -n "$url" && -n "$key_file" ]] || { usage; exit 2; }

base_url="$(python3 - "$url" <<'PY'
import sys
from urllib.parse import urlsplit

raw = sys.argv[1].strip()
try:
    parsed = urlsplit(raw)
    port = parsed.port
except ValueError:
    raise SystemExit(1)
if parsed.scheme not in {"http", "https"}:
    raise SystemExit(1)
if not parsed.hostname or parsed.username is not None or parsed.password is not None:
    raise SystemExit(1)
if parsed.query or parsed.fragment or parsed.path not in {"", "/"}:
    raise SystemExit(1)
if parsed.scheme == "http" and parsed.hostname not in {"127.0.0.1", "::1", "localhost"}:
    raise SystemExit(1)
if port is not None and not (1 <= port <= 65535):
    raise SystemExit(1)
print(raw[:-1] if raw.endswith("/") else raw)
PY
)" || fail url "invalid certification URL"

mode=""
[[ ! -L "$key_file" && -f "$key_file" ]] || fail key "key must be a non-symlink regular file"
[[ -s "$key_file" ]] || fail key "key must be non-empty"
mode="$(stat -c %a "$key_file" 2>/dev/null || stat -f %Lp "$key_file")"
[[ "$mode" == "600" ]] || fail key "key mode must be 0600"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
chmod 0700 "$tmp"
deny_re='adult|performer(s)?|studio(s)?|scene(s)?|gallery|galleries|porn'

request() {
  local stage="$1" method="$2" endpoint="$3" config="$4" body="$5" response="$6" status
  if [[ -n "$body" ]]; then
    status="$(curl --silent --show-error --connect-timeout 5 --max-time 10 --request "$method" \
      --config "$config" --data-binary @- --output "$response" --write-out '%{http_code}' \
      "$base_url$endpoint" < "$body")" || fail "$stage" "request failed"
  else
    status="$(curl --silent --show-error --connect-timeout 5 --max-time 10 --request "$method" \
      --config "$config" --output "$response" --write-out '%{http_code}' \
      "$base_url$endpoint")" || fail "$stage" "request failed"
  fi
  [[ "$status" == "$7" ]] || fail "$stage" "unexpected HTTP status"
}

empty_config="$tmp/empty.curl"
enroll_config="$tmp/enroll.curl"
auth_config="$tmp/auth.curl"
enroll_request="$tmp/enroll-request.json"
enroll_response="$tmp/enroll-response.json"
browse_request="$tmp/browse-request.json"
browse_response="$tmp/browse-response.json"
playback_request="$tmp/playback-request.json"
playback_response="$tmp/playback-response.json"
credentials="$tmp/credentials"
umask 077
: > "$empty_config"
printf 'header = "Content-Type: application/json"\n' > "$enroll_config"

request health GET /health "$empty_config" "" "$tmp/health-response" 200

python3 - "$key_file" "$enroll_request" <<'PY' || fail enrollment "could not prepare request"
import json
import sys
key_path, output = sys.argv[1:]
with open(key_path, encoding="utf-8") as handle:
    key = handle.read().strip()
if not key:
    raise SystemExit(1)
with open(output, "w", encoding="utf-8") as handle:
    handle.write(json.dumps({"code": key, "deviceName": "cert-demo-preflight"}, separators=(",", ":")))
PY
chmod 0600 "$enroll_request"
request enrollment POST /api/enroll "$enroll_config" "$enroll_request" "$enroll_response" 201

if grep -Eiq "$deny_re" "$enroll_response"; then fail vocabulary "enrollment response contains denied vocabulary"; fi
python3 - "$enroll_response" "$credentials" <<'PY' || fail enrollment "invalid credentials, role, or profile"
import json
import sys
source, output = sys.argv[1:]
try:
    with open(source, encoding="utf-8") as handle:
        value = json.load(handle)
except (OSError, ValueError):
    raise SystemExit(1)
fields = [value.get("sessionID"), value.get("deviceID"), value.get("deviceSecret")]
if not all(isinstance(item, str) and item for item in fields):
    raise SystemExit(1)
if value.get("deviceRole") != "viewer" or value.get("profileID") != "profile-demo-default":
    raise SystemExit(1)
with open(output, "w", encoding="utf-8") as handle:
    handle.write("\n".join(fields) + "\n")
PY
chmod 0600 "$credentials"
{
  IFS= read -r session_id
  IFS= read -r device_id
  IFS= read -r device_secret
} < "$credentials"
printf 'header = "Content-Type: application/json"\nheader = "X-Beamfall-Session: %s"\nheader = "X-Beamfall-Device: %s"\nheader = "X-Beamfall-Device-Secret: %s"\n' \
  "$session_id" "$device_id" "$device_secret" > "$auth_config"
chmod 0600 "$auth_config"

printf '%s' '{"query":"query DemoBrowse($id:String!){media(id:$id){id title}}","variables":{"id":"media-demo-bunny"}}' > "$browse_request"
chmod 0600 "$browse_request"
request browse POST /api/graphql "$auth_config" "$browse_request" "$browse_response" 200
if grep -Eiq "$deny_re" "$browse_response"; then fail vocabulary "browse response contains denied vocabulary"; fi
browse_error="$(python3 - "$browse_response" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        value = json.load(handle)
except (OSError, ValueError):
    print("invalid JSON")
    raise SystemExit(0)
if value.get("errors"):
    print("GraphQL errors present")
    raise SystemExit(0)
media = value.get("data", {}).get("media")
if not isinstance(media, dict):
    print("media missing")
elif media.get("id") != "media-demo-bunny":
    print("unexpected media ID")
elif media.get("title") != "Big Buck Bunny":
    print("unexpected media title")
PY
 )"
[[ -z "$browse_error" ]] || fail browse "$browse_error"

printf '%s' '{"mediaID":"media-demo-bunny","capabilityProfile":{"client":"store-review","containers":["mp4"],"directCodecs":["h264"]}}' > "$playback_request"
chmod 0600 "$playback_request"
request playback POST /api/playback-info "$auth_config" "$playback_request" "$playback_response" 200
if grep -Eiq "$deny_re" "$playback_response"; then fail vocabulary "playback response contains denied vocabulary"; fi
python3 - "$playback_response" <<'PY' || fail playback "unavailable or empty streams"
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        value = json.load(handle)
except (OSError, ValueError):
    raise SystemExit(1)
if value.get("decision") == "unavailable" or not isinstance(value.get("streams"), list) or not value["streams"]:
    raise SystemExit(1)
PY

printf 'PASS\n'
