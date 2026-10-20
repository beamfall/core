#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$ROOT/script/client-drm-eme-check.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/web" "$TMP/apple" "$TMP/android"
printf 'const player = document.createElement("video");\n' > "$TMP/web/player.ts"
printf 'import AVFoundation\nlet player = AVPlayer()\n' > "$TMP/apple/Player.swift"
printf 'plugins { id("com.android.application") }\n' > "$TMP/android/build.gradle.kts"

"$CHECK" \
  --target "web:fixture-web:$TMP/web" \
  --target "apple:fixture-apple:$TMP/apple" \
  --target "android:fixture-android:$TMP/android" >/dev/null

if "$CHECK" --target "apple:missing-apple:$TMP/missing-apple" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: missing native repo did not fail" >&2
  exit 1
fi
"$CHECK" --allow-missing-repos --target "apple:missing-apple:$TMP/missing-apple" >/dev/null

printf 'navigator.requestMediaKeySystemAccess("com.widevine.alpha", []);\n' > "$TMP/web/player.ts"
if "$CHECK" --target "web:fixture-web:$TMP/web" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: web EME marker did not fail" >&2
  exit 1
fi
printf 'const player = document.createElement("video");\n' > "$TMP/web/player.ts"

printf 'let keySession = AVContentKeySession.self\n' > "$TMP/apple/Player.swift"
if "$CHECK" --target "apple:fixture-apple:$TMP/apple" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Apple CDM marker did not fail" >&2
  exit 1
fi
printf 'import AVFoundation\nlet player = AVPlayer()\n' > "$TMP/apple/Player.swift"

printf 'OTHER_LDFLAGS = "-framework AVContentKeySession";\n' > "$TMP/apple/project.pbxproj"
if "$CHECK" --target "apple:fixture-apple:$TMP/apple" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Apple build CDM marker did not fail" >&2
  exit 1
fi
rm "$TMP/apple/project.pbxproj"

printf 'OTHER_LDFLAGS = $(inherited) -framework AVContentKeySession\n' > "$TMP/apple/Playback.xcconfig"
if "$CHECK" --target "apple:fixture-apple:$TMP/apple" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Apple xcconfig CDM marker did not fail" >&2
  exit 1
fi
rm "$TMP/apple/Playback.xcconfig"

printf 'dependencies:\n  FairPlayKit: https://example.invalid/fairplay.git\n' > "$TMP/apple/project.yml"
if "$CHECK" --target "apple:fixture-apple:$TMP/apple" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Apple project generator CDM marker did not fail" >&2
  exit 1
fi
rm "$TMP/apple/project.yml"

printf 'val drm = MediaItem.DrmConfiguration.Builder(C.WIDEVINE_UUID)\n' > "$TMP/android/Player.kt"
if "$CHECK" --target "android:fixture-android:$TMP/android" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Android CDM marker did not fail" >&2
  exit 1
fi
rm "$TMP/android/Player.kt"

printf 'dependencies { implementation("androidx.media3:media3-exoplayer-drm:1.4.0") }\n' > "$TMP/android/build.gradle.kts"
if "$CHECK" --target "android:fixture-android:$TMP/android" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Android build CDM marker did not fail" >&2
  exit 1
fi
printf 'plugins { id("com.android.application") }\n' > "$TMP/android/build.gradle.kts"

# Dependency-only changes must exercise the same invariant. These are the
# lock/version-catalog formats used by the launch clients, not source files.
printf '{"dependencies":{"widevine-client":"1.0.0"}}\n' > "$TMP/web/package.json"
if "$CHECK" --target "web:fixture-web:$TMP/web" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: web package manifest marker did not fail" >&2
  exit 1
fi
rm "$TMP/web/package.json"

printf '{"packages":{"node_modules/eme-client":{"name":"widevine-client"}}}\n' > "$TMP/web/package-lock.json"
if "$CHECK" --target "web:fixture-web:$TMP/web" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: web dependency marker did not fail" >&2
  exit 1
fi
rm "$TMP/web/package-lock.json"

mkdir -p "$TMP/apple/App.xcodeproj/project.xcworkspace/xcshareddata/swiftpm"
apple_lock="$TMP/apple/App.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"
printf '{"pins":[{"identity":"fairplay-sdk"}]}\n' > "$apple_lock"
if "$CHECK" --target "apple:fixture-apple:$TMP/apple" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Apple dependency marker did not fail" >&2
  exit 1
fi
rm "$apple_lock"

printf '// swift-tools-version: 6.0\n.package(url: "https://example.invalid/FairPlay.git", from: "1.0.0")\n' > "$TMP/apple/Package.swift"
if "$CHECK" --target "apple:fixture-apple:$TMP/apple" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Apple package manifest marker did not fail" >&2
  exit 1
fi
rm "$TMP/apple/Package.swift"

mkdir -p "$TMP/android/gradle"
printf 'media3-drm = { module = "androidx.media3:media3-exoplayer-drm" }\n' > "$TMP/android/gradle/libs.versions.toml"
if "$CHECK" --target "android:fixture-android:$TMP/android" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Android dependency marker did not fail" >&2
  exit 1
fi
rm "$TMP/android/gradle/libs.versions.toml"

mkdir -p "$TMP/android/gradle/dependency-locks"
printf 'androidx.media3:media3-exoplayer-drm:1.4.0=runtimeClasspath\n' > "$TMP/android/gradle/dependency-locks/runtime.lockfile"
if "$CHECK" --target "android:fixture-android:$TMP/android" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Android dependency lock marker did not fail" >&2
  exit 1
fi
rm "$TMP/android/gradle/dependency-locks/runtime.lockfile"

printf 'playerArtifact=androidx.media3:media3-exoplayer-drm:1.4.0\n' > "$TMP/android/gradle.properties"
if "$CHECK" --target "android:fixture-android:$TMP/android" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Android dependency property marker did not fail" >&2
  exit 1
fi
rm "$TMP/android/gradle.properties"

printf "dependencies { implementation 'androidx.media3:media3-exoplayer-drm:1.4.0' }\n" > "$TMP/android/build.gradle"
if "$CHECK" --target "android:fixture-android:$TMP/android" >/dev/null 2>&1; then
  echo "client-drm-eme-check_test: Android Groovy dependency marker did not fail" >&2
  exit 1
fi
rm "$TMP/android/build.gradle"

# The default launch matrix reports each contractual surface independently,
# even where macOS and Apple TV share one tracked Apple source repository.
mkdir -p "$TMP/workspace/beamfall-web" "$TMP/workspace/beamfall-apple" "$TMP/workspace/beamfall-android"
printf 'const player = document.createElement("video");\n' > "$TMP/workspace/beamfall-web/player.ts"
printf 'import AVFoundation\nlet player = AVPlayer()\n' > "$TMP/workspace/beamfall-apple/Player.swift"
printf 'plugins { id("com.android.application") }\n' > "$TMP/workspace/beamfall-android/build.gradle.kts"
default_output="$(BEAMFALL_WORKSPACE_ROOT="$TMP/workspace" "$CHECK")"
for surface in core-web beamfall-web beamfall-macos beamfall-apple-tv beamfall-android-tv; do
  if ! grep -q "PASS $surface" <<<"$default_output"; then
    echo "client-drm-eme-check_test: default matrix omitted $surface" >&2
    exit 1
  fi
done

echo "client-drm-eme-check_test: all assertions passed"
