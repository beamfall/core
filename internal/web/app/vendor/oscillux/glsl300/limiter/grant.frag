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
// integer codes and match tools/shader/limiter-model.mjs bit for bit. The wrapper
// (tools/shader/limiter.mjs) declares the LimiterBlock `lu`, the pass's textures and outputs,
// LIM_F(tex, px) / LIM_U(tex, px) (texel fetch of a float or uint texture) and LIM_SIZE(tex) /
// LIM_USIZE(tex) (the size of a float or uint texture), and LIMITER_LUT in the passes that
// include luma-lut.glsl (rows, output), which measure pixels.
//   lu.limiter0 = (window columns, window rows, area limit in pixels, token refill)
//   lu.limiter1 = (tick in [0, 240), frame interval in ms, 0, 0)
// Tracker state, one RGBA16UI texel per pixel (uTrack, oTrack). Each measure's tracker has a
// direction (the way it last fired), its extreme since then (the peak when rising, the trough when
// falling) and its reference (the level at its last transition), stored as the extreme and the
// reference's distance from it, which stays under the step. x: luma extreme (15 bits), bit 15 the
// red memory's high bit; y: luma distance (12 bits), bit 12 rising, bit 13 no state yet (the
// reference model's start), bit 15 the red memory's low bit; z: red extreme (8 bits), red distance
// << 8 (4 bits), bit 12 rising; w four 4-bit recency stamps (luma up, luma down << 4, red up << 8,
// red down << 12), 0 absent, else the event's unit (tick >> 4) + 1. The red memory (x bit 15, y bit
// 15) is what the red tracker knows of saturation since its latest change: 0 0 unknown, 0 1 counted
// with no saturated state seen, 1 1 not counted (so none seen), 1 0 counted with a saturated state
// seen. The zero texel is tracked black: falling at 0, red memory unknown, so the next red change
// counts and, unless it ends saturated, counts again at the first saturated state, whatever the
// pixel showed before a reset (Codex rounds 18, 19). Hosts start the texture at zero (README.md,
// "Ping-pong and reset").

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
const uint UNTRACKED = 8192u;  // y: no state yet
const uint RED_MEM_LO = 32768u; // y: the red memory's low bit
const uint RED_UP = 4096u;     // z: the red tracker last fired rising
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

bool limiterUntracked(uvec4 t) {
    return (t.y & UNTRACKED) != 0u;
}

// The directions (luma up 1, luma down 2, red up 4, red down 8) tracker t fires in on the pixel
// with display values c; none for a pixel with no state yet.
uint limiterFires(uvec4 t, ivec3 c) {
    if (limiterUntracked(t)) return 0u;
    int fl = limiterLumaStep(t, limiterLuma(c)).w;
    int fr = limiterRedStep(t, limiterRed(c), limiterSat(c)).y;
    return (fl > 0 ? 1u : 0u) | (fl < 0 ? 2u : 0u) | (fr > 0 ? 4u : 0u) | (fr < 0 ? 8u : 0u);
}

// Whether tracker t fires on display values c in no direction outside `allowed`.
bool limiterSafe(uvec4 t, uint allowed, ivec3 c) {
    return (limiterFires(t, c) & ~allowed & 15u) == 0u;
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
// t is safe. a is safe (step 0), so the result always is.
ivec3 limiterToward(uvec4 t, uint allowed, ivec3 a, ivec3 b) {
    int at = 256;
    for (int n = 1; n <= NEAR_STEPS; n++) {
        int s = limiterNearStep(n);
        if (limiterSafe(t, allowed, limiterMix(a, b, 256 - s))) { at = s; break; }
    }
    int far = at;
    for (int n = NEAR_FILL; n >= 1; n--) {
        if (far > 16 && limiterSafe(t, allowed, limiterMix(a, b, 256 - (far - n)))) { at = far - n; break; }
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
    if (limiterSafe(t, allowed, d)) return d;
    int w = limiterLuma(d);
    int lo = 0;
    int hi = 256;
    for (int n = 0; n < 8; n++) {
        int mid = (lo + hi) >> 1;
        if (limiterLuma(ivec3(mid)) <= w) lo = mid; else hi = mid;
    }
    if (limiterLuma(ivec3(lo)) < lowest) lo++;
    if (limiterSafe(t, allowed, ivec3(lo))) return limiterToward(t, allowed, ivec3(lo), d);
    return limiterToward(t, allowed, h, c);
}
#endif


ivec3 limiterBytes(vec3 c) {
    return ivec3(round(c * 255.0));
}
// SPDX-License-Identifier: AGPL-3.0-only
// Pass 5, grant: one fragment (1 x 1). The frame is over when some window of cols x rows cells
// (covering the 10° field) counts more than the area limit pixels changing in any channel and
// direction (flagged by the windows pass); every direction is then over. Luminance over the limit
// is rationed by one token budget; red has its own, refilled at an eighth of the luma rate, so at
// most one large-area red transition (either direction) falls in any 1 s window: no saturated-red
// flash (ITU-R BT.1702, ADR-0232 R5), and a held red change is admitted instead of freezing (R7,
// CORE-2382): the red token is back by the first frame at which the frames after it was last
// spent, each counted up to one refill period of 1 / (2 maxFlashHz) s, have earned
// (65025 / 65024) · 8 periods. The bound is per grant and in periods the frames earn, not on the
// wall clock (README.md, Codex round 24), and it is the token's: a frame that asks luma too also
// waits for the luma token (below; Codex round 25). When both red directions are asked one is
// admitted, the other than last time, so neither starves. Both luma directions may be admitted in
// one frame (a pattern reversing: some pixels rise, others fall). That spends two luma tokens:
// the pair waits
// until the budget has also stood unspent for one refill period (credit, at most one token) and
// leaves two tokens of deficit, so it stands as two singles on both sides and no 1 s window holds
// more than singles allow (CORE-2515). A single grant forfeits the credit. A frame asking both
// channels is admitted only when both are ready: a pixel changing in both shows the change only if
// both are admitted, so a grant of one alone would spend its token on a held change (Codex round
// 13). Inputs uC, uW, uG (the previous grant); output oG = (admitted bits | held bits << 4 | second
// luma token << 8 | last red grant was down << 9 | red refill remainder << 10, luma token deficit, red token deficit, luma
// credit). The deficit can reach two tokens after a pair, more than a 16-bit channel holds, so bit
// 8 of x carries one token of it.

void main() {
    uint refill = uint(lu.limiter0.w);
    uint askBits = 0u;
    bool over = false;
    for (int k = 0; k < GRID_X * GRID_Y; k++) {
        ivec2 cell = ivec2(k % GRID_X, k / GRID_X);
        askBits |= LIM_U(uC, cell).y;
        over = over || LIM_U(uW, cell).x != 0u;
    }
    uint now = askBits & 15u;
    uint recent = askBits >> 4u;
    // When something fires now, a channel firing nowhere but changed recently is part of the over
    // area too: it is asked in one direction (up when it rose), so its own budget pays for the
    // frame and staggering red one frame behind luma cannot slip past the red budget (Codex
    // round 14).
    if (now != 0u) {
        if ((now & 3u) == 0u && (recent & 3u) != 0u) now |= (recent & 1u) != 0u ? 1u : 2u;
        if ((now & 12u) == 0u && (recent & 12u) != 0u) now |= (recent & 4u) != 0u ? 4u : 8u;
    }
    bvec4 asks = notEqual(uvec4(now) & uvec4(1u, 2u, 4u, 8u), uvec4(0u));
    uvec4 last = LIM_U(uG, ivec2(0));
    uint previous = last.y + ((last.x >> 8u) & 1u) * TOKEN;
    uint deficit = previous > refill ? previous - refill : 0u;
    uint credit = min(TOKEN, last.w + (refill > previous ? refill - previous : 0u));
    // An eighth of the luma refill, its remainder carried in bits 10 to 12 of x: exact at any frame
    // interval, so a tiny maxFlashHz does not freeze red (Codex round 14) and a very short one does
    // not refill it early (Codex round 16).
    uint redEarned = ((last.x >> 10u) & 7u) + refill;
    uint redRefill = redEarned / 8u;
    uint redDeficit = last.z > redRefill ? last.z - redRefill : 0u;
    bool lastRedDown = ((last.x >> 9u) & 1u) != 0u;
    bool askUp = over && asks.x, askDown = over && asks.y;
    bool askRedUp = over && asks.z, askRedDown = over && asks.w;
    bool askLuma = askUp || askDown, askRed = askRedUp || askRedDown;
    bool lumaReady = deficit == 0u && (!(askUp && askDown) || credit >= TOKEN);
    bool redReady = redDeficit == 0u;
    bool go = (!askLuma || lumaReady) && (!askRed || redReady);
    bool grantUp = askUp && go;
    bool grantDown = askDown && go;
    if (grantUp || grantDown) {
        deficit = grantUp && grantDown ? 2u * TOKEN : TOKEN;
        credit = 0u;
    }
    bool grantRedUp = askRedUp && go && (!askRedDown || lastRedDown);
    bool grantRedDown = askRedDown && go && !grantRedUp;
    if (grantRedUp || grantRedDown) {
        redDeficit = TOKEN;
        lastRedDown = grantRedDown;
    }
    uint admitted = (!over || grantUp ? 1u : 0u) | (!over || grantDown ? 2u : 0u)
        | (!over || grantRedUp ? 4u : 0u) | (!over || grantRedDown ? 8u : 0u);
    uint high = deficit > TOKEN ? 1u : 0u;
    // Bits 4 to 7: the directions some pixel fires in and the grant did not admit, which hold
    // pixels; a host may stop drawing a paused frame only when they are clear (Codex round 18).
    uint held = askBits & 15u & ~admitted;
    oG = uvec4(admitted | (held << 4u) | (high << 8u) | (lastRedDown ? 512u : 0u) | ((redEarned & 7u) << 10u),
        deficit - high * TOKEN, redDeficit, credit);
}
