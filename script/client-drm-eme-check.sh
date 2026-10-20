#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

workspace_root() {
  local common core
  common="$(git -C "$REPO_ROOT" rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
  if [[ -n "$common" && "$(basename "$common")" == ".git" ]]; then
    core="$(dirname "$common")"
    dirname "$core"
    return
  fi
  dirname "$REPO_ROOT"
}

WORKSPACE_ROOT="${BEAMFALL_WORKSPACE_ROOT:-$(workspace_root)}"

targets=()
allow_missing_repos="${ALLOW_MISSING_NATIVE_REPOS:-0}"
add_default_targets() {
  # Root the embedded web target above src so dependency manifests and locks
  # participate in the same unconditional scan as authored source.
  targets+=("web:core-web:$REPO_ROOT/internal/web/app")
  targets+=("web:beamfall-web:$WORKSPACE_ROOT/beamfall-web")
  # Scan the full Apple repository for both launch surfaces. Shared Sources,
  # Package.resolved, and the Xcode project can enter either binary, so a
  # path-only split would create false negatives at the shared-code boundary.
  targets+=("apple:beamfall-macos:$WORKSPACE_ROOT/beamfall-apple")
  targets+=("apple:beamfall-apple-tv:$WORKSPACE_ROOT/beamfall-apple")
  targets+=("android:beamfall-android-tv:$WORKSPACE_ROOT/beamfall-android")
}
if [[ "$#" -gt 0 ]]; then
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --allow-missing-repos)
        allow_missing_repos=1
        shift
        ;;
      --target)
        targets+=("$2")
        shift 2
        ;;
      -h|--help)
        echo "usage: script/client-drm-eme-check.sh [--allow-missing-repos] [--target kind:label:path ...]" >&2
        exit 0
        ;;
      *)
        echo "client-drm-eme-check: unknown option: $1" >&2
        exit 2
        ;;
    esac
  done
  if [[ "${#targets[@]}" -eq 0 ]]; then
    add_default_targets
  fi
else
  add_default_targets
fi

web_re='requestMediaKeySystemAccess|MediaKeySystemAccess|MediaKeys|MediaKeySession|encryptedmedia|encrypted-media|keySystems|emeEnabled|emeController|com[.]widevine|\bwidevine\b|\bplayready\b|\bfairplay\b|com[.]apple[.]fps'
apple_re='AVContentKey|\bFairPlay\b|com[.]apple[.]fps|skd://|\bwidevine\b|\bplayready\b'
android_re='MediaDrm|DrmSession|DrmConfiguration|setDrm|androidx[.]media3[.]exoplayer[.]drm|com[.]google[.]android[.]exoplayer2[.]drm|exoplayer-drm|\bwidevine\b|\bplayready\b|\bclearkey\b|licenseUri|licenseServer'

tracked_files() {
  local root="$1"
  if git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git -C "$root" ls-files -z
  else
    find "$root" -type f -print0
  fi
}

scan_target() {
  local kind="$1" label="$2" root="$3" re
  if [[ ! -d "$root" ]]; then
    if [[ "$allow_missing_repos" == "1" ]]; then
      echo "client-drm-eme-check: SKIP $label (missing $root)"
      return 0
    fi
    echo "client-drm-eme-check: FAIL $label missing required client repo at $root" >&2
    return 1
  fi
  case "$kind" in
    web) re="$web_re" ;;
    apple) re="$apple_re" ;;
    android) re="$android_re" ;;
    *) echo "client-drm-eme-check: unknown target kind: $kind" >&2; return 2 ;;
  esac

  local matches
  matches="$(
    tracked_files "$root" |
      while IFS= read -r -d '' file; do
        if [[ "$file" = /* ]]; then
          abs="$file"
          rel="${file#$root/}"
        else
          abs="$root/$file"
          rel="$file"
        fi
        [[ -f "$abs" ]] || continue
        if [[ "$rel" == .git/* || "$rel" == .agent-evidence/* || "$rel" == .claude/* || "$rel" == .dev/* || "$rel" == artifacts/* || "$rel" == docs/* || "$rel" == node_modules/* || "$rel" == dist/* || "$rel" == build/* || "$rel" == .build/* || "$rel" == DerivedData/* || "$rel" == ui-catalog/* || "$rel" == test-results/* || "$rel" == RESULT.md || "$rel" == CONTEXT.md || "$rel" == AGENTS.md || "$rel" == README.md || "$rel" == LICENSE* || "$rel" == hls.min.js ]]; then
          continue
        fi
        if ! { [[ "$kind" == web && ( "$rel" == *.js || "$rel" == *.jsx || "$rel" == *.mjs || "$rel" == *.ts || "$rel" == *.tsx || "$rel" == *.html || "$rel" == *.json || "$rel" == package.json || "$rel" == package-lock.json || "$rel" == vite.config.* || "$rel" == tsconfig*.json ) ]] ||
          [[ "$kind" == apple && ( "$rel" == *.swift || "${rel##*/}" == Package.swift || "${rel##*/}" == Package.resolved || "${rel##*/}" == project.yml || "$rel" == *.pbxproj || "$rel" == *.xcconfig || "$rel" == *.plist || "$rel" == *.entitlements ) ]] ||
          [[ "$kind" == android && ( "$rel" == *.kt || "$rel" == *.kts || "$rel" == *.gradle || "$rel" == *.xml || "$rel" == *.pro || "$rel" == *.toml || "$rel" == *.lockfile || "$rel" == gradle.lockfile || "$rel" == *.properties ) ]]; }; then
          continue
        fi
        rg -n -i --color never "$re" "$abs" | sed "s#^#$label:#" || true
      done
  )"
  if [[ -n "$matches" ]]; then
    echo "$matches" >&2
    echo "client-drm-eme-check: FAIL $label contains media DRM/EME/CDM markers forbidden by ADR-0112" >&2
    return 1
  fi
  echo "client-drm-eme-check: PASS $label"
}

status=0
for target in "${targets[@]}"; do
  IFS=: read -r kind label root <<<"$target"
  scan_target "$kind" "$label" "$root" || status=1
done
exit "$status"
