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
// integer codes and match tools/shader/limiter-model.mjs bit for bit. The wrapper
// (tools/shader/limiter.mjs) declares the LimiterBlock `lu`, the pass's textures and outputs,
// LIM_F(tex, px) / LIM_U(tex, px) (texel fetch of a float or uint texture) and LIM_SIZE(tex) /
// LIM_USIZE(tex) (the size of a float or uint texture), and LIMITER_LUT in the passes that
// include luma-lut.glsl (rows, output), which measure pixels.
//   lu.limiter0 = (window columns, window rows, area limit in pixels, token refill)
//   lu.limiter1 = (tick in [0, 240), frame interval in ms, window columns and rows in sub-blocks,
//                 or 0 and 0 for whole-cell windows)
// Tracker state, one RGBA32UI texel per pixel (uTrack, oTrack). The low 16 bits of each word hold
// the luma and red trackers; the high 16 bits the red chromaticity boxes (limiterUvStep). Each
// measure's tracker has a direction (the way it last fired), its extreme since then (the peak when
// rising, the trough when falling) and its reference (the level at its last transition), stored as
// the extreme and the reference's distance from it, which stays under the step. x: luma extreme (15
// bits), bit 15 the red memory's high bit; y: luma distance (12 bits), bit 12 rising, bit 15 the red
// memory's low bit; z: red extreme (8 bits), red distance << 8 (4 bits), bit 12 rising, bits 13 and
// 14 the saturated and far boxes known, bit 15 the saturated box uncertain; w four 4-bit recency stamps (luma up, luma down << 4, red up
// << 8, red down << 12), 0 absent, else the event's unit (tick >> 4) + 1. The red memory (x bit 15, y
// bit 15) is what the red tracker knows of saturation since its latest change: 0 0 unknown, 0 1
// counted with no saturated state seen, 1 1 not counted (so none seen), 1 0 counted with a saturated
// state seen. High halves: x the saturated box's u minimum | the far box's << 9, y the u maxima
// likewise, z the v minima (far << 8) | 32768, w the v maxima (far << 8) | the far box uncertain
// << 15. Bit 31 of z is set in every
// texel a pass writes: a texel without it that is not all zero was stored by a host at less than 32
// bits and fires every direction on a changed colour, so it is held (limiterValid). The zero texel
// is tracked black: falling at 0, red memory unknown, both boxes unknown, so the next red change
// counts and, unless it ends saturated, counts again at the first saturated state, whatever the
// pixel showed before a reset (Codex rounds 18, 19). (0, UNTRACKED, 0, 0) exactly is no state yet.
// Hosts start the texture at a seed (README.md, "Ping-pong and reset").

const int BLOCKS_X = 64;
const int BLOCKS_Y = 36;
const int GRID_X = 16;
const int GRID_Y = 9;
const int SUB = 4;
// Luma and red are measured in integer codes, and the codes are the measure: the auditor and the
// limiter both use them. A luma code is 0.74 above to 1.24 below 32767.5 Y and a red code 0.006
// above to 1.002 below WCAG's quantity in 256ths (every 8-bit colour, README.md, "Flash measure"),
// so the steps are the thresholds less those errors: every WCAG transition fires.
const int LUMA_STEP = 3275;  // 0.1 relative luminance (3276.75 of 32767.5, less 1.98)
const int LUMA_DARK = 26215; // 0.8 (26214, plus 0.74): the darker side is below this
// Luma codes are 15-bit so bit 15 of the luma words carries the red memory.
const int LUMA_MAX = 32767;
const uint LUMA_MASK = 32767u;
const uint RED_MEM_HI = 32768u; // x: the red memory's high bit
const uint LUMA_UP = 4096u;    // y: the luma tracker last fired rising
const uint UNTRACKED = 8192u;  // y: no state yet ((0, UNTRACKED, 0, 0) exactly)
const uint RED_MEM_LO = 32768u; // y: the red memory's low bit
const uint RED_UP = 4096u;     // z: the red tracker last fired rising
const uint UV_SBK = 8192u;     // z: the saturated box is known
const uint UV_FBK = 16384u;    // z: the far box is known
const uint UV_SBU = 32768u;    // z: the saturated box is uncertain
const uint UV_FBU = 32768u;    // w's high half (w bit 31): the far box is uncertain
const uint UV_MARKER = 2147483648u; // z bit 31: written at 32 bits
const int RED_STEP = 15;     // 20 on WCAG's 320 max(0, R - G - B) scale (16 of 256, less 1.01)
const int NO_DARK = 256;
const int RED_MAX = 255;
const uint TOKEN = 65025u;
const float COVERAGE_MS = 66.66666666666667; // 1000 / 15

// Whether a 4-bit stamp is inside the coverage interval at `unit`: the frame interval is inside
// it and the stamp at most 5 units (of 15, modulo) old. A unit is 16 ticks of 1000/15/64 ms,
// 16.67 ms, so the 66.7 ms interval spans 4 units and reaches into a fifth: an event 5 units
// before the current one can be under 66.7 ms old.
bool limiterRecent(uint s, int unit, float deltaMs) {
    return s > 0u && deltaMs <= COVERAGE_MS && (unit - (int(s) - 1) + 15) % 15 <= 5;
}

// The audit counter's step on a tracker (extreme, distance, rising) for value v: returns
// (extreme', distance', rising', change), change 1 rising, -1 falling, 0 none. A change of at least
// stepSize whose darker side is below dark is a transition: against the direction from the extreme
// (a reversal), or onward from the reference: the level at the previous transition, or the furthest
// the value has since gone back short of a step. So a rise, a pause and a further rise of a step
// are two transitions: a large area can reach a step in one frame after its pixels started rising
// at different times (Codex round 16). A dip short of a step and a rise of a step from it is a rise
// (Codex round 17).
ivec4 limiterTrackStep(int ext, int dist, int up, int v, int stepSize, int dark) {
    if (up != 0) {
        if (ext - v >= stepSize && v < dark) return ivec4(v, 0, 0, -1);
        int ref = min(ext - dist, v);
        ext = max(ext, v);
        if (ext - ref < stepSize) return ivec4(ext, ext - ref, 1, 0);
        return ivec4(ext, 0, 1, ref < dark ? 1 : 0);
    }
    if (v - ext >= stepSize && ext < dark) return ivec4(v, 0, 1, 1);
    int ref = max(ext + dist, v);
    ext = min(ext, v);
    if (ref - ext < stepSize) return ivec4(ext, ref - ext, 0, 0);
    // A fall of a step that has not reached below dark keeps its high side, clamped a code under a
    // step: it becomes a transition when the trough goes on below dark (Codex round 18).
    if (ext >= dark) return ivec4(ext, stepSize - 1, 0, 0);
    return ivec4(ext, 0, 0, -1);
}

// The luma tracker's step on tracker t for luma code v.
ivec4 limiterLumaStep(uvec4 t, int v) {
    return limiterTrackStep(int(t.x & LUMA_MASK), int(t.y & 4095u), (t.y & LUMA_UP) != 0u ? 1 : 0, v, LUMA_STEP, LUMA_DARK);
}

// The red tracker's step on tracker t for red code rv and saturation sat: returns (z', count,
// memory high bit, memory low bit), count 1 rising, -1 falling, 0 none. A change counts as a red
// transition when the new state or one since the previous change is saturated; one that did not
// count then counts at the first saturated state before the next change, so a change through an
// unsaturated state to saturated red is not lost. With the memory unknown (a reset) a change counts
// at once and, unless it ends saturated, again at the first saturated state: either may be the
// auditor's (Codex round 19). Direction changes never wait for saturation: a gated direction let an
// unsaturated reversal hide the transition after it (Codex round 13).
ivec4 limiterRedStep(uvec4 t, int rv, bool sat) {
    ivec4 s = limiterTrackStep(int(t.z & 255u), int((t.z >> 8u) & 15u), (t.z & RED_UP) != 0u ? 1 : 0, rv, RED_STEP, NO_DARK);
    bool hi = (t.x & RED_MEM_HI) != 0u;
    bool lo = (t.y & RED_MEM_LO) != 0u;
    bool unknown = !hi && !lo;
    bool counted = !(hi && lo);
    bool seen = hi && !lo;
    int count = 0;
    if (s.w != 0) {
        count = sat || seen || unknown ? s.w : 0;
        counted = sat || seen;
        seen = sat;
        unknown = false;
    } else if (sat) {
        if (!counted || unknown) count = s.z != 0 ? 1 : -1;
        counted = true;
        seen = true;
        unknown = false;
    }
    int mhi = unknown ? 0 : !counted || seen ? 1 : 0;
    int mlo = unknown ? 0 : seen ? 0 : 1;
    return ivec4(s.x | (s.y << 8) | (s.z << 12), count, mhi, mlo);
}

#ifdef LIMITER_LUT
// A pixel's luma code from its 8-bit display values.
int limiterLuma(ivec3 c) {
    return min(65535, LUMA_LUT[c.r].x + LUMA_LUT[c.g].y + LUMA_LUT[c.b].z) >> 1;
}

// Whether a pixel is saturated red, R / (R + G + B) >= 0.8 of the linear channel codes, widened by
// their rounding so every saturated colour passes.
bool limiterSat(ivec3 c) {
    int r = LUMA_LUT[c.r].w;
    int g = LUMA_LUT[c.g].w;
    int b = LUMA_LUT[c.b].w;
    return r > g + b && r + 4 >= 4 * (g + b);
}

// A pixel's red code: (R - G - B) >> 8 of the linear channel codes when R > G + B, else 0. A red
// change counts only when a saturated state is seen with it (limiterRedStep), so every WCAG
// transition between saturated red and any other state counts and orange to orange does not.
int limiterRed(ivec3 c) {
    int r = LUMA_LUT[c.r].w;
    int g = LUMA_LUT[c.g].w;
    int b = LUMA_LUT[c.b].w;
    return r > g + b ? (r - g - b) >> 8 : 0;
}

// Red chromaticity (WCAG 2.1 and 2.2: a transition to or from saturated red is a change of more
// than 0.2 in CIE 1976 u'v'). Codes are u' and v' in 2^-14 units from LIN24_LUT, every intermediate
// under 2^32, and match limiter-model.mjs uvCode bit for bit; limiter-uv-check.mjs verifies them,
// the thresholds and the box cells over every 8-bit colour (openspec change
// limiter-red-uv-chromaticity, design.md, sections 2 to 4).
const ivec2 UV_V0 = ivec2(7386, 8567); // (1, 0, 0)
const ivec2 UV_V1 = ivec2(5044, 8852); // (0.8, 0.2, 0)
const ivec2 UV_V2 = ivec2(6396, 7255); // (0.8, 0, 0.2)
const int UV_T = 10683798;  // a step: codes over it may be more than 0.2 apart
const int UV_TH = 10791225; // a definite step: codes over it are
const int UV_TF = 10634475; // far: some saturated code may be more than a step away
const int UV_TVF = 12233465; // very far: every saturated code is more than 0.2 away
const ivec2 UV_S0 = ivec2(5044, 7254); // the saturated box: 8-code cells, 511 x 255
const ivec2 UV_SN = ivec2(511, 255);
const ivec2 UV_F0 = ivec2(2048, 2587); // the far box: 64-code cells, 127 x 127
const int UV_FN = 127;
const int UV_R = 3285; // the clip radius on each axis: UV_R * UV_R == UV_TH

// A non-black pixel's u'v' code.
ivec2 limiterUvCode(ivec3 c) {
    int s = LIN24_LUT[c.r] + LIN24_LUT[c.g] + LIN24_LUT[c.b];
    int k = 0;
    for (int n = 0; n < 8; n++) if ((s >> k) >= 262144) k++;
    uvec3 l = uvec3(ivec3(LIN24_LUT[c.r], LIN24_LUT[c.g], LIN24_LUT[c.b]) >> k);
    uvec3 q = l * 16384u / (l.x + l.y + l.z);
    uint x = 6757u * q.x + 5859u * q.y + 2957u * q.z;
    uint y = 3483u * q.x + 11718u * q.y + 1183u * q.z;
    uint d = (59954u * q.x + 187485u * q.y + 67420u * q.z) / 16384u;
    return ivec2(int(4u * x / d), int(9u * y / d));
}

// Saturated red for the chromaticity rule: linear R / (R + G + B) >= 0.8, exact over every 8-bit
// colour.
bool limiterUvSat(ivec3 c) {
    return c != ivec3(0) && LIN24_LUT[c.r] + 1 >= 4 * (LIN24_LUT[c.g] + LIN24_LUT[c.b]);
}

int limiterUvD2(ivec2 a, ivec2 b) {
    ivec2 d = a - b;
    return d.x * d.x + d.y * d.y;
}

// Whether code p may be more than a step from some saturated code.
bool limiterUvFar(ivec2 p) {
    return limiterUvD2(p, UV_V0) > UV_TF || limiterUvD2(p, UV_V1) > UV_TF || limiterUvD2(p, UV_V2) > UV_TF;
}

// Whether code p is more than 0.2 from every saturated code.
bool limiterUvVeryFar(ivec2 p) {
    return limiterUvD2(p, UV_V0) > UV_TVF && limiterUvD2(p, UV_V1) > UV_TVF && limiterUvD2(p, UV_V2) > UV_TVF;
}

// Whether code p is more than a step from some point of the box [lo, hi]: from its furthest corner.
bool limiterUvBoxFar(ivec2 p, ivec2 lo, ivec2 hi) {
    ivec2 m = max(p - lo, hi - p);
    return m.x * m.x + m.y * m.y > UV_T;
}

// Whether code p is more than a definite step from every point of the box [lo, hi].
bool limiterUvOver(ivec2 p, ivec2 lo, ivec2 hi) {
    ivec2 d = max(ivec2(0), max(lo - p, p - hi));
    return d.x * d.x + d.y * d.y > UV_TH;
}

// Whether code p is more than a definite step from every point of one of the box's edge strips, k
// codes wide (its cell): each strip holds a code the box was built from.
bool limiterUvBoxDefinite(ivec2 p, ivec2 lo, ivec2 hi, int k) {
    return limiterUvOver(p, lo, ivec2(lo.x + k - 1, hi.y)) || limiterUvOver(p, ivec2(hi.x - k + 1, lo.y), hi) ||
        limiterUvOver(p, lo, ivec2(hi.x, lo.y + k - 1)) || limiterUvOver(p, ivec2(lo.x, hi.y - k + 1), hi);
}

// Clips one axis of a box, cells [lo, hi] from base and 2^shift codes wide, to the cells that
// overlap [q - UV_R, q + UV_R]: (lo', hi'), hi' < lo' when nothing is left.
ivec2 limiterUvClip(int lo, int hi, int q, int base, int shift) {
    int a = q - UV_R - base;
    int b = q + UV_R - base;
    return ivec2(max(lo, max(a, 0) >> shift), b < 0 ? -1 : min(hi, b >> shift));
}

// The chromaticity step on tracker t for display values c after the previous output h: writes the
// new high halves (z with 32768, the marker; w with the far box's uncertain bit) to `halves` and
// z's box bits (UV_SBK, UV_FBK, UV_SBU) to `known`, and returns 1 up, -1 down, 0 none. SB (the
// saturated box) holds the saturated codes shown since the last red fall, FB (the far box) the far
// codes since the last red rise; each is known (it holds every such code) or unknown (a zero seed),
// and certain (every code it was built from is still a reference) or uncertain (a fire that may not
// have been a transition kept codes that may be spent). Only a colour other than h fires. A
// saturated colour fires up when FB is unknown or has a point more than a step from it; another
// non-black colour fires down when SB is known and has such a point, or is unknown and the colour
// far. A fire empties the box and makes it known and certain when it is surely a transition from
// every code the box may hold: the box is certain and an edge strip is more than 0.2 away, or, for
// SB, the colour is very far. Any other fire keeps only the box's cells within UV_R of the colour
// on each axis and makes the box uncertain unless that is nothing. Then the colour joins SB if
// saturated and FB if far. Black changes nothing. A min is stored as N - cell and a max as
// cell + 1, so the empty box is 0. Matches limiter-model.mjs uvStep (openspec change
// limiter-red-uv-chromaticity, design.md, sections 4 and 13).
int limiterUvStep(uvec4 t, ivec3 c, ivec3 h, out uvec4 halves, out uint known) {
    uvec4 hv = t >> 16u;
    int su0 = int(hv.x & 511u);
    int su1 = int(hv.y & 511u);
    int sv0 = int(hv.z & 255u);
    int sv1 = int(hv.w & 255u);
    int fu0 = int(hv.x >> 9u);
    int fu1 = int(hv.y >> 9u);
    int fv0 = int((hv.z >> 8u) & 127u);
    int fv1 = int((hv.w >> 8u) & 127u);
    bool sbk = (t.z & UV_SBK) != 0u;
    bool fbk = (t.z & UV_FBK) != 0u;
    bool sbu = (t.z & UV_SBU) != 0u;
    bool fbu = (hv.w & UV_FBU) != 0u;
    int dir = 0;
    if (c != ivec3(0)) {
        ivec2 p = limiterUvCode(c);
        bool sat = limiterUvSat(c);
        if (c != h) {
            if (sat) {
                ivec2 lo = UV_F0 + 64 * (ivec2(UV_FN) - ivec2(fu0, fv0));
                ivec2 hi = UV_F0 + 64 * ivec2(fu1, fv1) - 1;
                if (!fbk || (fu1 > 0 && limiterUvBoxFar(p, lo, hi))) {
                    dir = 1;
                    if (fu1 > 0 && !fbu && limiterUvBoxDefinite(p, lo, hi, 64)) {
                        fu0 = 0; fu1 = 0; fv0 = 0; fv1 = 0;
                        fbk = true;
                    } else if (fu1 > 0) {
                        ivec2 cu = limiterUvClip(UV_FN - fu0, fu1 - 1, p.x, UV_F0.x, 6);
                        ivec2 cv = limiterUvClip(UV_FN - fv0, fv1 - 1, p.y, UV_F0.y, 6);
                        if (cu.y < cu.x || cv.y < cv.x) {
                            fu0 = 0; fu1 = 0; fv0 = 0; fv1 = 0;
                        } else {
                            fu0 = UV_FN - cu.x; fu1 = cu.y + 1; fv0 = UV_FN - cv.x; fv1 = cv.y + 1;
                        }
                    }
                    fbu = fu1 > 0;
                }
            } else {
                ivec2 lo = UV_S0 + 8 * (UV_SN - ivec2(su0, sv0));
                ivec2 hi = UV_S0 + 8 * ivec2(su1, sv1) - 1;
                if (sbk ? su1 > 0 && limiterUvBoxFar(p, lo, hi) : limiterUvFar(p)) {
                    dir = -1;
                    if (limiterUvVeryFar(p) || (su1 > 0 && !sbu && limiterUvBoxDefinite(p, lo, hi, 8))) {
                        su0 = 0; su1 = 0; sv0 = 0; sv1 = 0;
                        sbk = true;
                    } else if (su1 > 0) {
                        ivec2 cu = limiterUvClip(UV_SN.x - su0, su1 - 1, p.x, UV_S0.x, 3);
                        ivec2 cv = limiterUvClip(UV_SN.y - sv0, sv1 - 1, p.y, UV_S0.y, 3);
                        if (cu.y < cu.x || cv.y < cv.x) {
                            su0 = 0; su1 = 0; sv0 = 0; sv1 = 0;
                        } else {
                            su0 = UV_SN.x - cu.x; su1 = cu.y + 1; sv0 = UV_SN.y - cv.x; sv1 = cv.y + 1;
                        }
                    }
                    sbu = su1 > 0;
                }
            }
        }
        if (sat) {
            ivec2 i = (p - UV_S0) >> 3;
            su0 = max(su0, UV_SN.x - i.x);
            su1 = max(su1, i.x + 1);
            sv0 = max(sv0, UV_SN.y - i.y);
            sv1 = max(sv1, i.y + 1);
        }
        if (limiterUvFar(p)) {
            ivec2 i = (p - UV_F0) >> 6;
            fu0 = max(fu0, UV_FN - i.x);
            fu1 = max(fu1, i.x + 1);
            fv0 = max(fv0, UV_FN - i.y);
            fv1 = max(fv1, i.y + 1);
        }
    }
    halves = uvec4(uint(su0 | (fu0 << 9)), uint(su1 | (fu1 << 9)), uint(sv0 | (fv0 << 8)) | 32768u,
        uint(sv1 | (fv1 << 8)) | (fbu ? UV_FBU : 0u));
    known = (sbk ? UV_SBK : 0u) | (fbk ? UV_FBK : 0u) | (sbu ? UV_SBU : 0u);
    return dir;
}

bool limiterUntracked(uvec4 t) {
    return t == uvec4(0u, UNTRACKED, 0u, 0u);
}

// Whether a texel was written at 32 bits (its marker) or is the zero texel.
bool limiterValid(uvec4 t) {
    return (t.z & UV_MARKER) != 0u || (t.x | t.y | t.z | t.w) == 0u;
}

// The directions (luma up 1, luma down 2, red up 4, red down 8) tracker t fires in on the pixel
// with display values c after the previous output h: none for a pixel with no state yet, and every
// direction on a colour other than h for a texel not written at 32 bits. The red direction is the
// chromaticity step's when it fires, else the 320 rule's.
uint limiterFires(uvec4 t, ivec3 c, ivec3 h) {
    if (limiterUntracked(t)) return 0u;
    if (!limiterValid(t)) return c != h ? 15u : 0u;
    int fl = limiterLumaStep(t, limiterLuma(c)).w;
    uvec4 hv;
    uint kb;
    int fr = limiterUvStep(t, c, h, hv, kb);
    if (fr == 0) fr = limiterRedStep(t, limiterRed(c), limiterSat(c)).y;
    return (fl > 0 ? 1u : 0u) | (fl < 0 ? 2u : 0u) | (fr > 0 ? 4u : 0u) | (fr < 0 ? 8u : 0u);
}

// Whether tracker t fires on display values c after the previous output h in no direction outside
// `allowed`.
bool limiterSafe(uvec4 t, uint allowed, ivec3 c, ivec3 h) {
    return (limiterFires(t, c, h) & ~allowed & 15u) == 0u;
}

// Display values a mixed k / 256 of the way to b.
ivec3 limiterMix(ivec3 a, ivec3 b, int k) {
    return (a * (256 - k) + b * k + 128) >> 8;
}

// The steps a held pixel's searches try along a mix, counted from the input (1 is one step from
// it, 256 the other end): the 16 nearest, then every 16th (n from 1 to NEAR_STEPS), then the
// NEAR_FILL between the first of those that qualifies and the one tried before it. No order is
// assumed along the mix: the luma and the trackers of rounded mixes cross a threshold more than
// once (Codex review of limiter-dim-hold).
const int NEAR_STEPS = 30;
const int NEAR_FILL = 15;

int limiterNearStep(int n) {
    return n <= 16 ? n : 16 * (n - 15);
}

// The step nearest b, of those searched (limiterNearStep) along the mix from a, at which tracker
// t is safe after the previous output h. a is safe (step 0), so the result always is.
ivec3 limiterToward(uvec4 t, uint allowed, ivec3 a, ivec3 b, ivec3 h) {
    int at = 256;
    for (int n = 1; n <= NEAR_STEPS; n++) {
        int s = limiterNearStep(n);
        if (limiterSafe(t, allowed, limiterMix(a, b, 256 - s), h)) { at = s; break; }
    }
    int far = at;
    for (int n = NEAR_FILL; n >= 1; n--) {
        if (far > 16 && limiterSafe(t, allowed, limiterMix(a, b, 256 - (far - n)), h)) { at = far - n; break; }
    }
    return limiterMix(a, b, 256 - at);
}

// Whether luma code v is at most lim (atMost) or at least lim.
bool limiterLumaOk(int v, int lim, bool atMost) {
    return atMost ? v <= lim : v >= lim;
}

// How many steps from b the nearest mix from a is, of those searched (limiterNearStep), whose
// luma is at most lim (atMost) or at least lim; 256, a itself, when none is.
int limiterLumaToward(ivec3 a, ivec3 b, int lim, bool atMost) {
    int at = 256;
    for (int n = 1; n <= NEAR_STEPS; n++) {
        int s = limiterNearStep(n);
        if (limiterLumaOk(limiterLuma(limiterMix(a, b, 256 - s)), lim, atMost)) { at = s; break; }
    }
    int far = at;
    for (int n = NEAR_FILL; n >= 1; n--) {
        if (far > 16 && limiterLumaOk(limiterLuma(limiterMix(a, b, 256 - (far - n))), lim, atMost)) { at = far - n; break; }
    }
    return at;
}

// How far (of 256) a pixel denied a rise moves each frame from the previous output's hue toward
// the input's (limiterDim).
const int CHROMA_K = 32;

// The colour a held pixel shows (output.glsl): the input c with its luma moved into the band the
// luma tracker does not fire in; then, if a tracker would still fire outside `allowed` (the
// directions the input fires and the grant admitted), the furthest safe step from the grey of that
// luma toward it, or else from the previous output h, which fires nothing, toward the input. To
// raise the luma the input is mixed with the least of the previous output that reaches the band
// (the light a pixel may not yet lose is the light it showed). To lower it the previous output is
// mixed toward the input as far as the band allows, then CHROMA_K / 256 of the way toward the
// input scaled toward black to the band's top (that scaled input alone if the mix is over the
// band): over black this is the input dimmed, and over lit content the pixel keeps its hue on the
// first frame and takes the input's over the next few, so a white line crossing a coloured glow
// does not show as a grey one beside it. Both stop at `top`, the band's ceiling less the room
// above what the pixel showed times (input - ceiling) / input and three times the shown luma over
// the ceiling (at most one): a lit pixel whose input is far over the band stays near what it
// showed, so a bright ring crossing a lit fill leaves no second ring where it now is, and a pixel
// that was black still goes to the ceiling. The band, from limiterTrackStep: a fall fires from a
// step below the tracker's high (its peak when rising, its reference when falling) if that is
// below the dark threshold, and a rise from a step above its low unless the low is at or above
// the dark threshold. Every step is integer, as in the model.
ivec3 limiterDim(uvec4 t, uint allowed, ivec3 c, ivec3 h) {
    int ext = int(t.x & LUMA_MASK);
    int dist = int(t.y & 4095u);
    bool up = (t.y & LUMA_UP) != 0u;
    int high = up ? ext : ext + dist;
    int low = up ? ext - dist : ext;
    int lowest = (allowed & 2u) != 0u ? 0 : min(high - LUMA_STEP + 1, LUMA_DARK);
    int highest = (allowed & 1u) != 0u || low >= LUMA_DARK ? LUMA_MAX : low + LUMA_STEP - 1;
    int v = limiterLuma(c);
    ivec3 d = c;
    if (v > highest) {
        int was = limiterLuma(h);
        int top = highest;
        if (was < highest) top = highest - (((highest - was) * (min(256, 768 * was / highest) * (v - highest) / v)) >> 8);
        int lo = 0;
        int hi = 255;
        for (int n = 0; n < 8; n++) {
            int mid = (lo + hi) >> 1;
            if (limiterLuma((c * mid + 127) / 255) <= top) lo = mid; else hi = mid;
        }
        d = (c * lo + 127) / 255;
        ivec3 e = limiterMix(limiterMix(h, c, 256 - limiterLumaToward(h, c, top, true)), d, CHROMA_K);
        if (limiterLuma(e) <= top) d = e;
    } else if (v < lowest) {
        d = limiterMix(h, c, 256 - limiterLumaToward(h, c, lowest, false));
    }
    if (limiterSafe(t, allowed, d, h)) return d;
    int w = limiterLuma(d);
    int lo = 0;
    int hi = 256;
    for (int n = 0; n < 8; n++) {
        int mid = (lo + hi) >> 1;
        if (limiterLuma(ivec3(mid)) <= w) lo = mid; else hi = mid;
    }
    if (limiterLuma(ivec3(lo)) < lowest) lo++;
    if (limiterSafe(t, allowed, ivec3(lo), h)) return limiterToward(t, allowed, ivec3(lo), d, h);
    return limiterToward(t, allowed, h, c, h);
}
#endif


ivec3 limiterBytes(vec3 c) {
    return ivec3(round(c * 255.0));
}
// SPDX-License-Identifier: AGPL-3.0-only
// Pass 4, windows: one fragment per cell origin (16 x 9). With a sub-block window (lu.limiter1.zw
// nonzero) it passes on the cells pass's flag for the window origins at this cell's sub-blocks.
// Otherwise it sums the cells pass's counts over the cols x rows cells from this origin and flags
// a sum over the area limit (more than limit pixels); an origin whose window would leave the grid
// flags nothing. Running the windows in parallel keeps the single-fragment grant pass short. Input
// uC; output oW = (over, 0, 0, 0). The limit is an integer below 2^24, exact in a float; a window
// holds under 2^27 pixels.

void main() {
    ivec2 o = ivec2(gl_FragCoord.xy);
    int cols = int(lu.limiter0.x);
    int rows = int(lu.limiter0.y);
    uint limit = uint(lu.limiter0.z);
    uint over = 0u;
    if (lu.limiter1.z > 0.0 && lu.limiter1.w > 0.0) {
        over = LIM_U(uC, o).z;
    } else if (o.x + cols <= GRID_X && o.y + rows <= GRID_Y) {
        uint sum = 0u;
        for (int y = o.y; y < o.y + rows; y++) {
            for (int x = o.x; x < o.x + cols; x++) sum += LIM_U(uC, ivec2(x, y)).x;
        }
        over = sum > limit ? 1u : 0u;
    }
    oW = uvec4(over, 0u, 0u, 0u);
}
