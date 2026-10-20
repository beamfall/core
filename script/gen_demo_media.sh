#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: script/gen_demo_media.sh --data-dir PATH

Generates tiny real demo media under PATH/seed-media/demo using ffmpeg and
ffprobe, then writes manifest.json for --seed=demo to consume. Media binaries
are generated artifacts and must not be committed.
USAGE
}

data_dir=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)
      if [[ $# -lt 2 ]]; then
        usage
        exit 2
      fi
      data_dir="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

if [[ -z "$data_dir" ]]; then
  usage
  exit 2
fi
if ! command -v ffmpeg >/dev/null 2>&1; then
  echo "ffmpeg is required to generate demo media" >&2
  exit 1
fi
if ! command -v ffprobe >/dev/null 2>&1; then
  echo "ffprobe is required to generate demo media metadata" >&2
  exit 1
fi

out_dir="$data_dir/seed-media/demo"
mkdir -p "$out_dir"

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

probe_int() {
  ffprobe -v error "$@" -of default=noprint_wrappers=1:nokey=1 | awk 'NR == 1 { printf("%d", $1 + 0.5) }'
}

probe_text() {
  ffprobe -v error "$@" -of default=noprint_wrappers=1:nokey=1 | awk 'NR == 1 { print $0 }'
}

generate_direct() {
  local media_id="$1"
  local frequency="$2"
  local duration="$3"
  local path="$out_dir/$media_id.mp4"
  ffmpeg -v error \
    -f lavfi -i "testsrc2=size=640x360:rate=24:duration=$duration" \
    -f lavfi -i "sine=frequency=$frequency:duration=$duration" \
    -shortest \
    -map_metadata -1 \
    -c:v libx264 -preset ultrafast -crf 30 -pix_fmt yuv420p \
    -c:a aac -b:a 64k \
    -movflags +faststart \
    -y "$path"
  write_manifest_entry "$media_id" "$path" "mp4"
}

generate_transcode_only() {
  local media_id="$1"
  local frequency="$2"
  local duration="$3"
  local path="$out_dir/$media_id.ts"
  ffmpeg -v error \
    -f lavfi -i "smptebars=size=640x360:rate=24:duration=$duration" \
    -f lavfi -i "sine=frequency=$frequency:duration=$duration" \
    -shortest \
    -map_metadata -1 \
    -c:v mpeg2video -q:v 8 \
    -c:a mp2 -b:a 64k \
    -f mpegts \
    -y "$path"
  write_manifest_entry "$media_id" "$path" "mpegts"
}

entries=()
write_manifest_entry() {
  local media_id="$1"
  local path="$2"
  local container="$3"
  local codec
  local duration
  local width
  local height
  codec="$(probe_text -select_streams v:0 -show_entries stream=codec_name "$path")"
  duration="$(probe_int -show_entries format=duration "$path")"
  width="$(probe_int -select_streams v:0 -show_entries stream=width "$path")"
  height="$(probe_int -select_streams v:0 -show_entries stream=height "$path")"
  entries+=("$(printf '{"mediaID":"%s","path":"%s","contentFingerprint":"sha256:%s","durationSeconds":%d,"container":"%s","codec":"%s","videoWidth":%d,"videoHeight":%d}' \
    "$(json_escape "$media_id")" \
    "$(json_escape "$path")" \
    "$(sha256_file "$path")" \
    "$duration" \
    "$(json_escape "$container")" \
    "$(json_escape "$codec")" \
    "$width" \
    "$height")")
}

generate_direct "media-demo-bunny" 330 12
generate_direct "media-demo-sintel" 392 14
generate_direct "media-demo-steel" 440 12
generate_direct "media-demo-spring" 494 12
generate_direct "media-demo-charge" 523 12
generate_direct "media-demo-caminandes" 587 10
generate_direct "media-demo-series-1" 659 12
generate_direct "media-demo-series-2" 698 12
generate_transcode_only "media-demo-orbit" 262 12
generate_direct "media-demo-aurora" 294 12
generate_direct "media-demo-guarded-1" 349 12
generate_direct "media-demo-guarded-2" 370 12

manifest_tmp="$out_dir/manifest.json.tmp"
{
  printf '{\n  "version": 1,\n  "files": [\n'
  for i in "${!entries[@]}"; do
    if [[ "$i" -gt 0 ]]; then
      printf ',\n'
    fi
    printf '    %s' "${entries[$i]}"
  done
  printf '\n  ]\n}\n'
} > "$manifest_tmp"
mv "$manifest_tmp" "$out_dir/manifest.json"

echo "generated demo media in $out_dir"
