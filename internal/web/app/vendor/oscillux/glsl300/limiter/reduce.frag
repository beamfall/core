#version 300 es
precision highp float;
precision highp int;
layout(std140) uniform LimiterBlock {
    vec4 limiter0;
    vec4 limiter1;
} lu;
uniform highp usampler2D uRows;
uniform highp usampler2D uT0;
uniform highp usampler2D uT1;
layout(location = 0) out uvec4 oS;
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
// Pass 2, reduce: one fragment per sub-block (64 x 36). Finishes the box reduction of the
// sub-block's footprint (every pixel counts once) from the rows pass's partial sums to a luma and
// a red code, and tests the trackers for the transitions this frame would make. Inputs uRows
// (RGBA32UI, 64 x frame height), uT0, uT1; output oS = (luma code, red code, request bits: luma
// up 1, luma down 2, red up 4, red down 8, 0).

void main() {
    ivec2 b = ivec2(gl_FragCoord.xy);
    int height = LIM_USIZE(uRows).y;
    int y0 = b.y * height / BLOCKS_Y;
    int y1 = (b.y + 1) * height / BLOCKS_Y;
    uint sy = 0u;
    uint sr = 0u;
    uint n = 0u;
    for (int y = y0; y < y1; y++) {
        uvec4 r = LIM_U(uRows, ivec2(b.x, y));
        sy += r.x;
        sr += r.y;
        n += r.z;
    }
    int luma = int(min(65535u, (sy + n / 2u) / n));
    int red = int(min(65535u, (sr + n / 2u) / n));
    uvec4 t0 = LIM_U(uT0, b);
    uvec4 t1 = LIM_U(uT1, b);
    uint bits = 0u;
    if (t1.z != 0u) {
        int fl = limiterTrackStep(ivec3(t0.xy, int(t1.x) - 1), luma, LUMA_STEP, LUMA_DARK).w;
        int fr = limiterTrackStep(ivec3(t0.zw, int(t1.y) - 1), red, RED_STEP, NO_DARK).w;
        bits = (fl > 0 ? 1u : 0u) | (fl < 0 ? 2u : 0u) | (fr > 0 ? 4u : 0u) | (fr < 0 ? 8u : 0u);
    }
    oS = uvec4(uint(luma), uint(red), bits, 0u);
}
