#!/usr/bin/env bash
# GA hardware HDR tone-mapping launch gate (ADR-0075 Amendment 2026-07-26 D6).
#
# RED unless the committed evidence carries, for every transfer class the
# release claims, an approved passing VideoToolbox row AND an approved passing
# row from VAAPI / QSV / CUDA-NVENC, all bound to this release's
# certification-scope digest and pinned FFmpeg identity.
#
# It never mutates or regenerates the evidence it validates.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

exec python3 tools/gate/vidparity-hdr-tonemap-gate.py --root . "$@"
