#version 300 es
precision highp float;
precision highp int;
layout(std140) uniform LimiterBlock {
    vec4 limiter0;
    vec4 limiter1;
} lu;
uniform highp sampler2D uFrame;
uniform highp sampler2D uHist;
uniform highp usampler2D uT1;
layout(location = 0) out vec4 oColor;
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
// Pass 7, output: one fragment per pixel. A pixel of a held sub-block shows the previous output
// frame, every other pixel the input. Inputs uFrame (RGBA8), uHist (the previous output, RGBA8),
// uT1 (this frame's state); output oColor (RGBA8, alpha 1), which is next frame's uHist.

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    ivec2 b = (ivec2(BLOCKS_X, BLOCKS_Y) * (p + 1) - 1) / LIM_SIZE(uFrame);
    bool held = LIM_U(uT1, b).w != 0u;
    oColor = vec4(held ? LIM_F(uHist, p).rgb : LIM_F(uFrame, p).rgb, 1.0);
}
