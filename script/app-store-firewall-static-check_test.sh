#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/apple" "$TMP/android" "$TMP/roku/source" "$TMP/webos/src" "$TMP/tizen/src" "$TMP/archive/Payload/Beamfall.app"

cat >"$TMP/apple/App.swift" <<'SWIFT'
let policyLabel = "watch folder ingest"
SWIFT

cat >"$TMP/apple/Info.plist" <<'PLIST'
<plist><dict><key>CFBundleName</key><string>Beamfall</string></dict></plist>
PLIST

cat >"$TMP/android/Main.kt" <<'KT'
const val policyLabel = "watch folder ingest"
val sheets = obj.optJSONArray("sheets")
KT

cat >"$TMP/android/strings.xml" <<'XML'
<resources><string name="app_name">Beamfall</string></resources>
XML

cat >"$TMP/roku/source/main.brs" <<'BRS'
sub Main()
  print "Beamfall"
end sub
BRS

cat >"$TMP/webos/src/index.ts" <<'TS'
export const clientClass = "webos-store";
TS

cat >"$TMP/tizen/src/index.js" <<'JS'
export const clientClass = "tizen-store";
JS

"$ROOT/script/app-store-firewall-static-check.sh" \
  --require-repos \
  --target "apple:apple-fixture:$TMP/apple" \
  --target "android:android-fixture:$TMP/android" \
  --target "roku:roku-fixture:$TMP/roku" \
  --target "wrapper:webos-fixture:$TMP/webos" \
  --target "wrapper:tizen-fixture:$TMP/tizen" >/dev/null

assert_fails() {
  local message="$1"
  shift
  if "$ROOT/script/app-store-firewall-static-check.sh" "$@" >"$TMP/check.out" 2>"$TMP/check.err"; then
    echo "$message" >&2
    exit 1
  fi
}

"$ROOT/script/app-store-firewall-static-check.sh" \
  --target "apple:missing-fixture:$TMP/missing" >"$TMP/check.out"
grep -q 'SKIP missing-fixture' "$TMP/check.out"

assert_fails "expected --require-repos to reject a missing repo" \
  --require-repos --target "apple:missing-fixture:$TMP/missing"
grep -q 'missing-fixture missing required repo' "$TMP/check.err"

mkdir "$TMP/empty"
assert_fails "expected empty target to fail" \
  --target "android:empty-fixture:$TMP/empty"
grep -q 'empty-fixture contains no scannable files' "$TMP/check.err"

assert_fails "expected malformed target to fail" --target "apple:missing-path"
grep -q 'malformed target' "$TMP/check.err"

cat >"$TMP/apple/Leak.swift" <<'SWIFT'
let forbidden = "RADARR"
SWIFT
assert_fails "expected Swift acquisition term to fail" \
  --target "apple:apple-fixture:$TMP/apple"
grep -q 'apple-fixture:.*Leak.swift.*RADARR' "$TMP/check.err"
rm "$TMP/apple/Leak.swift"

cat >"$TMP/apple/NeutralSurface.swift" <<'SWIFT'
import BeamfallDirectDistributionKit

struct TransferSourceSettings {}
SWIFT
assert_fails "expected neutral-vocabulary acquisition surface identity to fail" \
  --target "apple:apple-fixture:$TMP/apple"
grep -q 'apple-fixture:.*NeutralSurface.swift.*BeamfallDirectDistributionKit' "$TMP/check.err"
grep -q 'apple-fixture references an acquisition-surface identity' "$TMP/check.err"
if grep -q 'firewalled acquisition vocabulary' "$TMP/check.err"; then
  echo "expected seeded surface failure to be independent of acquisition vocabulary" >&2
  exit 1
fi
rm "$TMP/apple/NeutralSurface.swift"

cat >"$TMP/android/Leak.kt" <<'KT'
const val forbidden = "Usenet"
KT
assert_fails "expected Kotlin acquisition term to fail" \
  --target "android:android-fixture:$TMP/android"
grep -q 'android-fixture:.*Leak.kt.*Usenet' "$TMP/check.err"
rm "$TMP/android/Leak.kt"

cat >"$TMP/roku/source/leak.brs" <<'BRS'
sub Leaked()
  print "torrent"
end sub
BRS
assert_fails "expected BrightScript acquisition term to fail" \
  --target "roku:roku-fixture:$TMP/roku"
grep -q 'roku-fixture:.*leak.brs.*torrent' "$TMP/check.err"
rm "$TMP/roku/source/leak.brs"

cat >"$TMP/webos/src/leak.ts" <<'TS'
export const forbidden = "qBittorrent";
TS
assert_fails "expected wrapper acquisition term to fail" \
  --target "wrapper:webos-fixture:$TMP/webos"
grep -q 'webos-fixture:.*leak.ts.*qBittorrent' "$TMP/check.err"
rm "$TMP/webos/src/leak.ts"

printf 'Beamfall clean binary\n' >"$TMP/archive/Payload/Beamfall.app/Beamfall"
for extension in ipa apk aab; do
  archive="$TMP/beamfall.$extension"
  (cd "$TMP/archive" && zip -qr "$archive" .)
  "$ROOT/script/app-store-firewall-static-check.sh" \
    --target "archive:$extension-fixture:$archive" >/dev/null
done

printf 'embedded SoNaRr surface\n' >"$TMP/archive/Payload/Beamfall.app/Beamfall"
(cd "$TMP/archive" && zip -qr "$TMP/leak.ipa" .)
assert_fails "expected archive acquisition term to fail" \
  --target "archive:ipa-fixture:$TMP/leak.ipa"
grep -q 'ipa-fixture:.*Beamfall.*SoNaRr' "$TMP/check.err"

printf 'BeamfallDirectDistributionKit\n' >"$TMP/archive/Payload/Beamfall.app/Beamfall"
(cd "$TMP/archive" && zip -qr "$TMP/neutral-surface.ipa" .)
assert_fails "expected neutral-vocabulary acquisition surface identity in archive to fail" \
  --target "archive:ipa-fixture:$TMP/neutral-surface.ipa"
grep -q 'ipa-fixture:.*Beamfall.*BeamfallDirectDistributionKit' "$TMP/check.err"
grep -q 'ipa-fixture references an acquisition-surface identity' "$TMP/check.err"
if grep -q 'firewalled acquisition vocabulary' "$TMP/check.err"; then
  echo "expected seeded archive failure to be independent of acquisition vocabulary" >&2
  exit 1
fi

assert_fails "expected missing archive to fail" \
  --target "archive:missing-archive:$TMP/missing.apk"
grep -q 'missing-archive missing required archive' "$TMP/check.err"

: >"$TMP/corrupt.aab"
assert_fails "expected corrupt archive to fail" \
  --target "archive:corrupt-archive:$TMP/corrupt.aab"
grep -q 'corrupt-archive could not inspect' "$TMP/check.err"

: >"$TMP/unsupported.zip"
assert_fails "expected unsupported archive to fail" \
  --target "archive:unsupported-archive:$TMP/unsupported.zip"
grep -q 'unsupported archive type' "$TMP/check.err"

python3 - "$TMP/unsafe.apk" <<'PY'
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1], "w") as archive:
    archive.writestr("../escape.txt", "Beamfall")
PY
assert_fails "expected unsafe archive path to fail" \
  --target "archive:unsafe-archive:$TMP/unsafe.apk"
grep -q 'unsafe-archive archive contains unsafe path' "$TMP/check.err"

echo "app-store-firewall-static-check_test: OK"
