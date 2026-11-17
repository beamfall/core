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
// frame. Every other pixel blends toward the previous output frame by a weight that is continuous
// across sub-block boundaries and falls off round, so a hold has no visible edge: the hold field
// the state pass wrote (0..64 at each sub-block centre) is interpolated bilinearly between the
// four nearest centres (in 64ths per axis), scaled to t in 0..4096 and eased,
// w = ((t^2 >> 12) (12288 - 2 t)) >> 12. A pixel whose input, history or blend is red-dominant
// (r > g + b in 8-bit) shows the input, so red is never blended. Inputs uFrame (RGBA8), uHist (the
// previous output, RGBA8), uT1 (this frame's state); output oColor (RGBA8, alpha 1), which is next
// frame's uHist.

int fieldAt(ivec2 b) {
    if (b.x >= BLOCKS_X || b.y >= BLOCKS_Y) return 0;
    return int(LIM_U(uT1, b).w >> 1u);
}

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    ivec2 size = LIM_SIZE(uFrame);
    ivec2 n = ivec2(BLOCKS_X, BLOCKS_Y);
    ivec2 b = (n * (p + 1) - 1) / size;
    vec3 frame = LIM_F(uFrame, p).rgb;
    vec3 hist = LIM_F(uHist, p).rgb;
    if ((LIM_U(uT1, b).w & 1u) != 0u) {
        oColor = vec4(hist, 1.0);
        return;
    }
    oColor = vec4(frame, 1.0);
    ivec3 a = ivec3(frame * 255.0 + 0.5);
    ivec3 h = ivec3(hist * 255.0 + 0.5);
    if (a.r > a.g + a.b || h.r > h.g + h.b) return;
    // l: the first of the two sub-blocks, per axis, whose centres the pixel lies between; f: the
    // second's weight in 64ths. Centres are doubled so they stay integers. Before the first centre
    // and after the last the pixel takes its own sub-block alone.
    ivec2 at = 2 * p + 1;
    ivec2 l = b - ivec2(lessThan(at, b * size / n + (b + 1) * size / n));
    ivec2 cl = l * size / n + (l + 1) * size / n;
    ivec2 cr = (l + 1) * size / n + (l + 2) * size / n;
    ivec2 f = 64 * (at - cl) / (cr - cl);
    if (l.x < 0 || l.x + 1 >= n.x) { l.x = b.x; f.x = 0; }
    if (l.y < 0 || l.y + 1 >= n.y) { l.y = b.y; f.y = 0; }
    int t = ((fieldAt(l) * (64 - f.x) + fieldAt(l + ivec2(1, 0)) * f.x) * (64 - f.y)
        + (fieldAt(l + ivec2(0, 1)) * (64 - f.x) + fieldAt(l + ivec2(1, 1)) * f.x) * f.y) >> 6;
    if (t == 0) return;
    int w = (((t * t) >> 12) * (12288 - 2 * t)) >> 12;
    ivec3 o = (a * (4096 - w) + h * w + 2048) >> 12;
    if (o.r > o.g + o.b) return;
    oColor = vec4(vec3(o) / 255.0, 1.0);
}
