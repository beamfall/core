#version 300 es
precision highp float;
precision highp int;
layout(std140) uniform LimiterBlock {
    vec4 limiter0;
    vec4 limiter1;
} lu;
uniform highp usampler2D uC;
layout(location = 0) out uvec4 oW;
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
// Pass 4, windows: one fragment per window origin (16 x 9). Sums the cols x rows cells from this
// origin per direction and flags the directions over the area limit (more than limit
// sub-blocks); an origin whose window would leave the grid flags nothing. Running the windows
// in parallel keeps the single-fragment grant pass short. Input uC; output oW = (over bits: luma
// up 1, luma down 2, red up 4, red down 8, 0, 0, 0).

void main() {
    ivec2 o = ivec2(gl_FragCoord.xy);
    int cols = int(lu.limiter0.x);
    int rows = int(lu.limiter0.y);
    float limit = lu.limiter0.z;
    uint bits = 0u;
    if (o.x + cols <= GRID_X && o.y + rows <= GRID_Y) {
        uvec4 s = uvec4(0u);
        for (int y = o.y; y < o.y + rows; y++) {
            for (int x = o.x; x < o.x + cols; x++) s += LIM_U(uC, ivec2(x, y)) & 31u;
        }
        uvec4 over = uvec4(greaterThan(vec4(s), vec4(limit)));
        bits = over.x | (over.y << 1u) | (over.z << 2u) | (over.w << 3u);
    }
    oW = uvec4(bits, 0u, 0u, 0u);
}
