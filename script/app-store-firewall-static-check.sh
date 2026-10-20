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
# Ordinary Core/workspace gates scan every present client repo but remain
# runnable in partial checkouts. Store certification passes --require-repos
# explicitly and therefore stays fail-closed for every release input.
allow_missing_repos=1
targets=()
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-app-store-firewall.XXXXXX")"
trap 'rm -rf "$TMP_ROOT"' EXIT

add_default_targets() {
  targets+=("apple:beamfall-iphone:$WORKSPACE_ROOT/beamfall-apple/Apps/BeamfallMobile")
  targets+=("apple:beamfall-ipad:$WORKSPACE_ROOT/beamfall-apple/Apps/BeamfallPad")
  targets+=("apple:beamfall-tvos:$WORKSPACE_ROOT/beamfall-apple/Apps/BeamfallTV")
  targets+=("apple:beamfall-apple-policy:$WORKSPACE_ROOT/beamfall-apple/Sources/BeamfallAppStorePolicy")
  targets+=("apple:beamfall-mobile:$WORKSPACE_ROOT/beamfall-apple/Sources/BeamfallMobile")
  targets+=("apple:beamfall-tv:$WORKSPACE_ROOT/beamfall-apple/Sources/BeamfallTV")
  targets+=("android:beamfall-android-mobile:$WORKSPACE_ROOT/beamfall-android/app-mobile/src/main")
  targets+=("android:beamfall-android-tv:$WORKSPACE_ROOT/beamfall-android/app-tv/src/main")
  targets+=("roku:beamfall-roku:$WORKSPACE_ROOT/beamfall-roku")
  targets+=("wrapper:beamfall-webos:$WORKSPACE_ROOT/beamfall-webos")
  targets+=("wrapper:beamfall-tizen:$WORKSPACE_ROOT/beamfall-tizen")
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --allow-missing-repos)
      allow_missing_repos=1
      shift
      ;;
    --require-repos)
      allow_missing_repos=0
      shift
      ;;
    --target)
      if [[ "$#" -lt 2 ]]; then
        echo "app-store-firewall-static-check: --target requires kind:label:path" >&2
        exit 2
      fi
      targets+=("$2")
      shift 2
      ;;
    -h|--help)
      echo "usage: script/app-store-firewall-static-check.sh [--allow-missing-repos|--require-repos] [--target kind:label:path ...]" >&2
      exit 0
      ;;
    *)
      echo "app-store-firewall-static-check: unknown option: $1" >&2
      exit 2
      ;;
  esac
done

if [[ "${#targets[@]}" -eq 0 ]]; then
  add_default_targets
fi

# Require a leading token boundary so platform APIs such as `optJSONArray`
# cannot synthesize a false `Sonarr` match across `JSON` + `Array`. Service
# identifiers beginning with a forbidden name (for example SonarrClient) still
# fail, as do all user-facing string occurrences.
acquisition_re='(^|[^[:alnum:]_])(qBittorrent|Radarr|Sonarr|Overseerr|torrent|usenet)'

# Canonical client boundary identities are structural assertions, not vocabulary.
acquisition_surface_identities=(
  BeamfallDirectDistributionKit
)

tracked_files() {
  local root="$1"
  if git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git -C "$root" ls-files -z
  else
    find "$root" -type f -print0
  fi
}

scan_target() {
  local kind="$1" label="$2" root="$3" abs rel file matches surface_matches files scanned output rg_status identity
  if [[ ! -d "$root" ]]; then
    if [[ "$allow_missing_repos" == "1" ]]; then
      echo "app-store-firewall-static-check: SKIP $label (missing $root)"
      return 0
    fi
    echo "app-store-firewall-static-check: FAIL $label missing required repo at $root" >&2
    return 1
  fi

  matches="$(mktemp "$TMP_ROOT/matches.XXXXXX")"
  surface_matches="$(mktemp "$TMP_ROOT/surface-matches.XXXXXX")"
  files="$(mktemp "$TMP_ROOT/files.XXXXXX")"
  tracked_files "$root" >"$files"
  scanned=0
  while IFS= read -r -d '' file; do
    if [[ "$file" = /* ]]; then
      abs="$file"
      rel="${file#$root/}"
    else
      abs="$root/$file"
      rel="$file"
    fi
    [[ -f "$abs" ]] || continue
    case "$rel" in
      .git/*|.agent-evidence/*|artifacts/*|docs/*|node_modules/*|dist/*|build/*|.build/*|coverage/*|test-results/*|RESULT.md|README.md|LICENSE*) continue ;;
    esac
    case "$kind:$rel" in
      apple:*.swift|apple:*.plist|apple:*.xcprivacy|apple:*.entitlements|apple:*.storekit|android:*.kt|android:*.kts|android:*.xml|android:*.json|android:*.properties|roku:*.brs|roku:*.bs|roku:*.xml|roku:*.json|roku:*.pkg|roku:manifest|wrapper:*.js|wrapper:*.jsx|wrapper:*.mjs|wrapper:*.ts|wrapper:*.tsx|wrapper:*.html|wrapper:*.json|wrapper:*.xml|wrapper:*.config|wrapper:package.json|wrapper:package-lock.json|archive:*) ;;
      *) continue ;;
    esac
    scanned=$((scanned + 1))
    rg_status=0
    output="$(rg -a -i -n --with-filename --color never "$acquisition_re" "$abs")" || rg_status=$?
    case "$rg_status" in
      0) printf '%s\n' "$output" | sed "s#^#$label:#" >>"$matches" ;;
      1) ;;
      *)
        echo "app-store-firewall-static-check: FAIL $label could not scan $rel" >&2
        return 1
        ;;
    esac
    for identity in "${acquisition_surface_identities[@]}"; do
      rg_status=0
      output="$(rg -a -n -F --with-filename --color never "$identity" "$abs")" || rg_status=$?
      case "$rg_status" in
        0) printf '%s\n' "$output" | sed "s#^#$label:#" >>"$surface_matches" ;;
        1) ;;
        *)
          echo "app-store-firewall-static-check: FAIL $label could not assert acquisition-surface identities in $rel" >&2
          return 1
          ;;
      esac
    done
  done <"$files"
  if [[ "$scanned" -eq 0 ]]; then
    echo "app-store-firewall-static-check: FAIL $label contains no scannable files" >&2
    return 1
  fi
  if [[ -s "$matches" ]]; then
    cat "$matches" >&2
    echo "app-store-firewall-static-check: FAIL $label contains App Store firewalled acquisition vocabulary" >&2
    return 1
  fi
  if [[ -s "$surface_matches" ]]; then
    cat "$surface_matches" >&2
    echo "app-store-firewall-static-check: FAIL $label references an acquisition-surface identity" >&2
    return 1
  fi
  echo "app-store-firewall-static-check: PASS $label"
}

scan_archive() {
  local label="$1" archive="$2" extension extract entry entries
  if [[ ! -f "$archive" ]]; then
    echo "app-store-firewall-static-check: FAIL $label missing required archive at $archive" >&2
    return 1
  fi
  extension="${archive##*.}"
  case "$extension" in
    ipa|IPA|apk|APK|aab|AAB) ;;
    *)
      echo "app-store-firewall-static-check: FAIL $label unsupported archive type: $archive" >&2
      return 1
      ;;
  esac
  if ! entries="$(unzip -Z1 "$archive")"; then
    echo "app-store-firewall-static-check: FAIL $label could not inspect $archive" >&2
    return 1
  fi
  while IFS= read -r entry; do
    case "$entry" in
      /*|../*|*/../*|*/..)
        echo "app-store-firewall-static-check: FAIL $label archive contains unsafe path: $entry" >&2
        return 1
        ;;
    esac
  done <<<"$entries"
  extract="$(mktemp -d "$TMP_ROOT/archive.XXXXXX")"
  if ! unzip -qq "$archive" -d "$extract"; then
    echo "app-store-firewall-static-check: FAIL $label could not unpack $archive" >&2
    return 1
  fi
  scan_target archive "$label" "$extract"
}

status=0
for target in "${targets[@]}"; do
  IFS=: read -r kind label root extra <<<"$target"
  if [[ -z "$kind" || -z "$label" || -z "$root" || -n "${extra:-}" ]]; then
    echo "app-store-firewall-static-check: malformed target: $target" >&2
    status=1
    continue
  fi
  case "$kind" in
    apple|android|roku|wrapper) scan_target "$kind" "$label" "$root" || status=1 ;;
    archive) scan_archive "$label" "$root" || status=1 ;;
    *) echo "app-store-firewall-static-check: unknown target kind: $kind" >&2; status=1 ;;
  esac
done
exit "$status"
