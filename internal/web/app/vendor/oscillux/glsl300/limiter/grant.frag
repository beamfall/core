#version 300 es
precision highp float;
precision highp int;
layout(std140) uniform LimiterBlock {
    vec4 limiter0;
    vec4 limiter1;
} lu;
uniform highp usampler2D uC;
uniform highp usampler2D uW;
uniform highp usampler2D uG;
layout(location = 0) out uvec4 oG;
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
// Pass 5, grant: one fragment (1 x 1). A direction is over when some window of cols x rows cells
// (the 10° field) counts more than the area limit (flagged by the windows pass). Luminance over
// the limit is rationed by one token budget; red has its own, refilled at an eighth of the luma
// rate, so at most one large-area red transition (either direction) falls in any 1 s window: no
// saturated-red flash (ITU-R BT.1702, ADR-0232 R5), and a held red change is admitted within
// 8 / (2 maxFlashHz) s instead of freezing (R7, CORE-2382). Red never takes or waits for the
// luminance token. Inputs uC, uW, uG (the previous grant); output oG = (admitted bits | over bits
// << 4, luma token deficit, red token deficit, 0).

void main() {
    uint refill = uint(lu.limiter0.w);
    uvec4 asks = uvec4(0u);
    uint overBits = 0u;
    for (int k = 0; k < GRID_X * GRID_Y; k++) {
        ivec2 cell = ivec2(k % GRID_X, k / GRID_X);
        asks += LIM_U(uC, cell) >> 5u;
        overBits |= LIM_U(uW, cell).x;
    }
    bvec4 over = notEqual(uvec4(overBits) & uvec4(1u, 2u, 4u, 8u), uvec4(0u));
    uint previous = LIM_U(uG, ivec2(0)).y;
    uint deficit = previous > refill ? previous - refill : 0u;
    bool grantUp = over.x && asks.x > 0u && deficit == 0u;
    if (grantUp) deficit = TOKEN;
    bool grantDown = over.y && asks.y > 0u && deficit == 0u;
    if (grantDown) deficit = TOKEN;
    uint redRefill = refill / 8u;
    uint redPrevious = LIM_U(uG, ivec2(0)).z;
    uint redDeficit = redPrevious > redRefill ? redPrevious - redRefill : 0u;
    bool grantRedUp = over.z && asks.z > 0u && redDeficit == 0u;
    if (grantRedUp) redDeficit = TOKEN;
    bool grantRedDown = over.w && asks.w > 0u && redDeficit == 0u;
    if (grantRedDown) redDeficit = TOKEN;
    uint admitted = (!over.x || grantUp ? 1u : 0u) | (!over.y || grantDown ? 2u : 0u)
        | (!over.z || grantRedUp ? 4u : 0u) | (!over.w || grantRedDown ? 8u : 0u);
    uvec4 o = uvec4(over);
    oG = uvec4(admitted | (o.x << 4u) | (o.y << 5u) | (o.z << 6u) | (o.w << 7u), deficit,
        redDeficit, 0u);
}
