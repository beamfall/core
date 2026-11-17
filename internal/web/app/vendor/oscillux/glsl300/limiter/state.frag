#version 300 es
precision highp float;
precision highp int;
layout(std140) uniform LimiterBlock {
    vec4 limiter0;
    vec4 limiter1;
} lu;
uniform highp usampler2D uS;
uniform highp usampler2D uG;
uniform highp usampler2D uT0;
uniform highp usampler2D uT1;
uniform highp usampler2D uT2;
layout(location = 0) out uvec4 oT0;
layout(location = 1) out uvec4 oT1;
layout(location = 2) out uvec4 oT2;
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
// Pass 6, state: one fragment per sub-block (64 x 36). A sub-block that requests a transition
// the grant did not admit is held: it shows the previous output frame and its trackers do not
// advance (recency stamps only age). Every other sub-block advances its trackers on the frame.
// The pass also writes the hold field the output pass blends by: at each sub-block, 0..64, the
// largest over the held sub-blocks within four of it on each axis of clamp(72 - 4 d2, 0, 64), d2
// the squared distance in sub-blocks. It is 64 at a held sub-block and its eight neighbours and
// reaches 0 a little past four sub-blocks away, so a hold fades out round, not square.
// Inputs uS, uG (this frame's grant), uT0, uT1, uT2; outputs the next state:
//   oT0 = (luma lo, luma hi, red lo, red hi)
//   oT1 = (luma dir + 1, red dir + 1, live, hold + 2 * hold field)
//   oT2 = recency stamps (luma up, luma down, red up, red down)

const int FIELD_REACH = 4;

void main() {
    ivec2 b = ivec2(gl_FragCoord.xy);
    int tick = int(lu.limiter1.x);
    float deltaMs = lu.limiter1.y;
    uvec4 s = LIM_U(uS, b);
    uvec4 t0 = LIM_U(uT0, b);
    uvec4 t1 = LIM_U(uT1, b);
    uvec4 t2 = LIM_U(uT2, b);
    uint grant = LIM_U(uG, ivec2(0)).x;
    int field = 0;
    for (int j = -FIELD_REACH; j <= FIELD_REACH; j++) {
        for (int i = -FIELD_REACH; i <= FIELD_REACH; i++) {
            ivec2 q = b + ivec2(i, j);
            if (q.x < 0 || q.y < 0 || q.x >= BLOCKS_X || q.y >= BLOCKS_Y) continue;
            if ((LIM_U(uS, q).z & ~grant & 15u) == 0u) continue;
            field = max(field, clamp(72 - 4 * (i * i + j * j), 0, 64));
        }
    }
    uint fw = uint(2 * field);
    if (t1.z == 0u) {
        oT0 = uvec4(s.x, s.x, s.y, s.y);
        oT1 = uvec4(1u, 1u, 1u, fw);
        oT2 = uvec4(0u);
        return;
    }
    uvec4 rec = uvec4(limiterRecent(t2.x, tick, deltaMs), limiterRecent(t2.y, tick, deltaMs),
        limiterRecent(t2.z, tick, deltaMs), limiterRecent(t2.w, tick, deltaMs));
    if ((s.z & ~grant & 15u) != 0u) {
        oT0 = t0;
        oT1 = uvec4(t1.xyz, 1u + fw);
        oT2 = rec;
        return;
    }
    ivec4 fl = limiterTrackStep(ivec3(t0.xy, int(t1.x) - 1), int(s.x), LUMA_STEP, LUMA_DARK);
    ivec4 fr = limiterTrackStep(ivec3(t0.zw, int(t1.y) - 1), int(s.y), RED_STEP, NO_DARK);
    bvec4 fired = bvec4(fl.w > 0, fl.w < 0, fr.w > 0, fr.w < 0);
    oT0 = uvec4(fl.xy, fr.xy);
    oT1 = uvec4(uint(fl.z + 1), uint(fr.z + 1), 1u, fw);
    oT2 = uvec4(fired.x ? uint(tick + 1) : rec.x, fired.y ? uint(tick + 1) : rec.y,
        fired.z ? uint(tick + 1) : rec.z, fired.w ? uint(tick + 1) : rec.w);
}
