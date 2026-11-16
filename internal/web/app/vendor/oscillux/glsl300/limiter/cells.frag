#version 300 es
precision highp float;
precision highp int;
layout(std140) uniform LimiterBlock {
    vec4 limiter0;
    vec4 limiter1;
} lu;
uniform highp usampler2D uS;
uniform highp usampler2D uT2;
layout(location = 0) out uvec4 oC;
#define LIM_F(t, px) texelFetch(t, px, 0)
#define LIM_U(t, px) texelFetch(t, px, 0)
#define LIM_SIZE(t) textureSize(t, 0)
#define LIM_USIZE(t) textureSize(t, 0)
// SPDX-License-Identifier: AGPL-3.0-only
// Shared by the frame flash limiter passes (README.md). The decisions are integer arithmetic on
// 16-bit codes and match tools/shader/limiter-model.mjs bit for bit. The wrapper
// (tools/shader/limiter.mjs) declares the LimiterBlock `lu`, the pass's textures and outputs,
// LIM_F(tex, px) / LIM_U(tex, px) (texel fetch of a float or uint texture) and LIM_SIZE(tex) /
// LIM_USIZE(tex) (the size of a float or uint texture).
//   lu.limiter0 = (window columns, window rows, area limit in sub-blocks, token refill)
//   lu.limiter1 = (tick in [0, 255), frame interval in ms, 0, 0)

const int BLOCKS_X = 64;
const int BLOCKS_Y = 36;
const int GRID_X = 16;
const int GRID_Y = 9;
const int SUB = 4;
const int LUMA_STEP = 6554;  // 0.1 relative luminance
const int LUMA_DARK = 52428; // 0.8: the darker side of a luminance transition is below this
const int RED_STEP = 512;    // 2.5 on the 320 * (R - G - B) scale: an eighth of the auditor's step (CORE-1616)
const int NO_DARK = 65536;
const uint TOKEN = 65025u;
const float COVERAGE_MS = 66.66666666666667; // 1000 / 15

// The audit counter's step on a tracker (lo, hi, dir): returns (lo', hi', dir', fired), fired
// 1 rising, -1 falling, 0 none.
ivec4 limiterTrackStep(ivec3 t, int v, int stepSize, int dark) {
    int lo = min(t.x, v);
    int hi = max(t.y, v);
    if (t.z <= 0 && v - lo >= stepSize && lo < dark) return ivec4(v, v, 1, 1);
    if (t.z >= 0 && hi - v >= stepSize && v < dark) return ivec4(v, v, -1, -1);
    return ivec4(lo, hi, t.z, 0);
}

// A recency stamp (event tick + 1, 0 absent) if it is still inside the coverage interval, else 0.
uint limiterRecent(uint r, int tick, float deltaMs) {
    int elapsed = (tick - (int(r) - 1) + 255) % 255;
    return r > 0u && deltaMs <= COVERAGE_MS && elapsed <= 64 ? r : 0u;
}
// SPDX-License-Identifier: AGPL-3.0-only
// Pass 3, cells: one fragment per cell (16 x 9). Per direction (luma up, luma down, red up, red
// down), counts the cell's 16 sub-blocks that request the transition now or made it within the
// coverage interval, and the requests alone. Inputs uS, uT2; output oC = count | requests << 5.

void main() {
    ivec2 cell = ivec2(gl_FragCoord.xy);
    int tick = int(lu.limiter1.x);
    float deltaMs = lu.limiter1.y;
    uvec4 count = uvec4(0u);
    uvec4 asks = uvec4(0u);
    for (int j = 0; j < SUB; j++) {
        for (int i = 0; i < SUB; i++) {
            ivec2 b = cell * SUB + ivec2(i, j);
            uint bits = LIM_U(uS, b).z;
            uvec4 rec = LIM_U(uT2, b);
            for (int d = 0; d < 4; d++) {
                uint q = (bits >> uint(d)) & 1u;
                asks[d] += q;
                if (q == 1u || limiterRecent(rec[d], tick, deltaMs) != 0u) count[d] += 1u;
            }
        }
    }
    oC = count | (asks << 5u);
}
