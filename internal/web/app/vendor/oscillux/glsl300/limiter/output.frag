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
// frame. A pixel of a sub-block that is not held blends toward the previous output frame by the
// largest weight (0..15, in sixteenths) of its sub-block's eight neighbours that are held, so a
// hold has no hard edge; a pixel whose input, history or blend is red-dominant (r > g + b in
// 8-bit) shows the input, so red is never blended. Inputs uFrame (RGBA8), uHist (the previous
// output, RGBA8), uT1 (this frame's state); output oColor (RGBA8, alpha 1), which is next frame's
// uHist.

int heldAt(ivec2 b) {
    if (b.x < 0 || b.y < 0 || b.x >= BLOCKS_X || b.y >= BLOCKS_Y) return 0;
    return LIM_U(uT1, b).w != 0u ? 1 : 0;
}

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    ivec2 size = LIM_SIZE(uFrame);
    ivec2 n = ivec2(BLOCKS_X, BLOCKS_Y);
    ivec2 b = (n * (p + 1) - 1) / size;
    vec3 frame = LIM_F(uFrame, p).rgb;
    vec3 hist = LIM_F(uHist, p).rgb;
    if (heldAt(b) != 0) {
        oColor = vec4(hist, 1.0);
        return;
    }
    oColor = vec4(frame, 1.0);
    ivec3 a = ivec3(frame * 255.0 + 0.5);
    ivec3 h = ivec3(hist * 255.0 + 0.5);
    if (a.r > a.g + a.b || h.r > h.g + h.b) return;
    ivec2 lo = b * size / n;
    ivec2 hi = (b + 1) * size / n;
    ivec2 f = 16 * (p - lo) / (hi - lo);
    int wl = 15 - f.x, wr = f.x, wu = 15 - f.y, wd = f.y;
    int w = max(max(heldAt(b + ivec2(-1, 0)) * wl, heldAt(b + ivec2(1, 0)) * wr),
                max(heldAt(b + ivec2(0, -1)) * wu, heldAt(b + ivec2(0, 1)) * wd));
    w = max(w, max(max(heldAt(b + ivec2(-1, -1)) * ((wl * wu) >> 4), heldAt(b + ivec2(1, -1)) * ((wr * wu) >> 4)),
                   max(heldAt(b + ivec2(-1, 1)) * ((wl * wd) >> 4), heldAt(b + ivec2(1, 1)) * ((wr * wd) >> 4))));
    if (w == 0) return;
    ivec3 o = (a * (16 - w) + h * w + 8) >> 4;
    if (o.r > o.g + o.b) return;
    oColor = vec4(vec3(o) / 255.0, 1.0);
}
