#version 300 es
precision highp float;
precision highp int;
precision highp sampler2D;
uniform sampler2D uImage;
uniform sampler2D uPrev;
#define IMG(x) texture(uImage,(x))
#define IMGSIZE vec2(textureSize(uImage,0))
#define PREV(x) texture(uPrev,(x))
// SPDX-License-Identifier: AGPL-3.0-only
// Shared visual library: the prelude for the reference visual set
// (docs/shader-toolchain.md). Compiles as GLSL ES 3.00 and GLSL 450.
//
// Portable-subset rules for visual bodies:
//   - texture reads only through the IMG(uv) / PREV(uv) macros the wrapper defines
//   - two-argument arctangent is atan2_(y, x), never atan(y, x)
//   - no raw texture() calls and no derivatives beyond fwidth()
//   - visuals output linear HDR and never tonemap

const int NBANDS = 64;

struct VisualUniforms {
    vec2  resolution;
    float time;
    float dt;

    float amplitude;
    float bass;
    float mid;
    float treble;
    float beat;
    float beatPhase;
    float onset;

    float sensitivity;
    float param0;
    float param1;
    float param2;

    float level;
    float bassImpact;
    float spectralCentroid;
    float flux;
    float beatCount;
    float trailDecay;
    float barPhase;
    float downbeat;

    vec4 accent;
    vec4 motion;
    vec4 palette;
    vec4 presentation0;
    vec4 presentation1;
    vec4 safeRect;
    vec4 spectrum[16];
};

float atan2_(float y, float x) { return atan(y, x); }
float saturate(float x) { return clamp(x, 0.0, 1.0); }
vec3 saturate3(vec3 x) { return clamp(x, vec3(0.0), vec3(1.0)); }

// ---- spectrum reads ----
float band(VisualUniforms u, int i) {
    i = clamp(i, 0, NBANDS - 1);
    // Constant-index select: ANGLE lowers a dynamic index into a uniform array to a
    // per-call copy of the whole array, which cost 8x the frame time across the catalog.
    int k = i >> 2;
    vec4 q = k == 0 ? u.spectrum[0] : k == 1 ? u.spectrum[1] : k == 2 ? u.spectrum[2] : k == 3 ? u.spectrum[3] : k == 4 ? u.spectrum[4] : k == 5 ? u.spectrum[5] : k == 6 ? u.spectrum[6] : k == 7 ? u.spectrum[7] : k == 8 ? u.spectrum[8] : k == 9 ? u.spectrum[9] : k == 10 ? u.spectrum[10] : k == 11 ? u.spectrum[11] : k == 12 ? u.spectrum[12] : k == 13 ? u.spectrum[13] : k == 14 ? u.spectrum[14] : u.spectrum[15];
    return q[i & 3];
}

// Cover-fit a photo: screen uv -> image uv that fills the frame without stretching,
// cropping the overflow about the center. res and img are in pixels (IMGSIZE).
vec2 photoCover(vec2 uv, vec2 res, vec2 img) {
    vec2 s = res / max(img, vec2(1.0));
    return (uv - 0.5) * (s / max(s.x, s.y)) + 0.5;
}

float bandRange(VisualUniforms u, int lo, int hi) {
    lo = clamp(lo, 0, NBANDS); hi = clamp(hi, 0, NBANDS);
    float s = 0.0; int n = 0;
    for (int i = lo; i < hi; ++i) { s += band(u, i); n++; }
    return n > 0 ? s / float(n) : 0.0;
}

float bandAt(VisualUniforms u, float f) {
    float x = clamp(f, 0.0, 1.0) * float(NBANDS - 1);
    int i = int(x); float fr = x - float(i);
    return mix(band(u, i), band(u, i + 1), fr);
}

float bandAtWrapped(VisualUniforms u, float f) {
    float x = fract(f) * float(NBANDS);
    int i0 = int(floor(x)) % NBANDS;
    int i1 = (i0 + 1) % NBANDS;
    return mix(band(u, i0), band(u, i1), fract(x));
}

// ---- color: IQ cosine palettes ----
vec3 cosinePalette(float t, vec3 a, vec3 b, vec3 c, vec3 d) {
    return a + b * cos(6.2831853 * (c * t + d));
}

vec3 palNeon(float t) {
    return cosinePalette(t, vec3(0.50, 0.50, 0.50), vec3(0.50, 0.48, 0.44),
                            vec3(1.00, 1.00, 1.00), vec3(0.00, 0.18, 0.38));
}
vec3 palHeatIce(float t) {
    return cosinePalette(t, vec3(0.46, 0.36, 0.46), vec3(0.54, 0.48, 0.54),
                            vec3(1.00, 0.92, 1.18), vec3(0.02, 0.30, 0.58));
}
vec3 palNight(float t) {
    return cosinePalette(t, vec3(0.28, 0.34, 0.40), vec3(0.32, 0.40, 0.46),
                            vec3(1.10, 0.95, 1.25), vec3(0.55, 0.28, 0.05));
}

vec3 palCity(float t) {
    t = fract(t);
    vec3 hot = vec3(1.00, 0.18, 0.02);
    vec3 amber = vec3(1.00, 0.62, 0.05);
    vec3 mag = vec3(1.00, 0.04, 0.74);
    vec3 violet = vec3(0.42, 0.14, 1.00);
    vec3 cyan = vec3(0.00, 0.78, 1.00);
    vec3 blue = vec3(0.04, 0.22, 1.00);
    vec3 a = mix(hot, amber, smoothstep(0.00, 0.16, t));
    vec3 b = mix(a, mag, smoothstep(0.14, 0.42, t));
    vec3 c = mix(b, violet, smoothstep(0.38, 0.62, t));
    vec3 d = mix(c, cyan, smoothstep(0.58, 0.84, t));
    return mix(d, blue, smoothstep(0.82, 1.00, t));
}

// Signature palettes 1-4 and 8-31 share one shape: five stops blended across
// four smoothstep windows. Their data lives in one table, so palByIndex is a
// lookup instead of a 32-way branch inlined at every palette read. Rows 0 and
// 5-7 (palCity's six stops, the cosine palettes) are unused.
const vec3 PAL_STOPS[160] = vec3[160](
    vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), // 0 city (palCity)
    vec3(1.00, 0.16, 0.01), vec3(1.00, 0.74, 0.08), vec3(1.00, 0.06, 0.42), vec3(0.06, 0.86, 1.00), vec3(0.06, 0.10, 0.38), // 1 emberIce
    vec3(0.36, 0.08, 1.00), vec3(1.00, 0.02, 0.86), vec3(0.02, 0.20, 1.00), vec3(0.00, 0.78, 1.00), vec3(1.00, 0.08, 0.12), // 2 ultraviolet
    vec3(0.02, 0.16, 1.00), vec3(0.00, 0.78, 1.00), vec3(0.94, 0.04, 0.92), vec3(0.42, 0.08, 1.00), vec3(1.00, 0.48, 0.04), // 3 deepSea
    vec3(1.00, 0.07, 0.02), vec3(1.00, 0.38, 0.02), vec3(1.00, 0.86, 0.08), vec3(1.00, 0.04, 0.62), vec3(0.00, 0.70, 1.00), // 4 solarFlare
    vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), // 5 neon (cosine)
    vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), // 6 heatIce (cosine)
    vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), vec3(0.0), // 7 night (cosine)
    vec3(0.28, 0.04, 0.55), vec3(1.00, 0.05, 0.55), vec3(1.00, 0.35, 0.28), vec3(1.00, 0.78, 0.10), vec3(0.02, 0.72, 0.80), // 8 miami
    vec3(0.01, 0.02, 0.08), vec3(0.02, 0.08, 0.35), vec3(0.05, 0.25, 0.85), vec3(0.15, 0.55, 1.00), vec3(0.70, 0.90, 1.05), // 9 abyss
    vec3(0.30, 0.06, 0.28), vec3(0.85, 0.20, 0.45), vec3(1.00, 0.45, 0.60), vec3(1.00, 0.78, 0.82), vec3(1.00, 0.40, 0.32), // 10 sakura
    vec3(0.02, 0.35, 0.42), vec3(0.10, 0.75, 0.75), vec3(1.00, 0.90, 0.70), vec3(1.00, 0.55, 0.12), vec3(0.75, 0.20, 0.04), // 11 horizon
    vec3(0.30, 0.02, 0.04), vec3(0.85, 0.10, 0.05), vec3(1.00, 0.42, 0.03), vec3(1.00, 0.80, 0.10), vec3(1.00, 0.95, 0.55), // 12 moltenGold
    vec3(0.03, 0.07, 0.25), vec3(0.15, 0.40, 0.75), vec3(0.25, 0.80, 1.00), vec3(0.80, 0.95, 1.05), vec3(0.55, 0.55, 0.95), // 13 glacier
    vec3(0.06, 0.02, 0.06), vec3(0.35, 0.03, 0.12), vec3(0.65, 0.08, 0.22), vec3(0.85, 0.35, 0.45), vec3(0.55, 0.30, 0.50), // 14 velvet
    vec3(0.45, 0.22, 0.10), vec3(0.80, 0.40, 0.15), vec3(0.15, 0.60, 0.52), vec3(0.55, 0.85, 0.75), vec3(0.95, 0.92, 0.80), // 15 patina
    vec3(0.01, 0.10, 0.07), vec3(0.02, 0.38, 0.28), vec3(0.05, 0.62, 0.45), vec3(0.35, 0.85, 0.65), vec3(0.80, 1.00, 0.88), // 16 jade
    vec3(0.15, 0.06, 0.01), vec3(0.55, 0.25, 0.02), vec3(0.95, 0.55, 0.05), vec3(1.00, 0.80, 0.20), vec3(1.00, 0.95, 0.65), // 17 amber
    vec3(0.04, 0.04, 0.05), vec3(0.22, 0.24, 0.28), vec3(0.55, 0.58, 0.64), vec3(0.82, 0.86, 0.92), vec3(0.60, 0.66, 0.78), // 18 smoke
    vec3(1.00, 0.35, 0.70), vec3(0.75, 0.50, 0.95), vec3(0.35, 0.75, 1.00), vec3(0.45, 0.95, 0.80), vec3(1.00, 0.90, 0.45), // 19 candy
    vec3(0.12, 0.01, 0.03), vec3(0.60, 0.02, 0.06), vec3(1.00, 0.10, 0.05), vec3(1.00, 0.50, 0.05), vec3(1.60, 1.30, 1.00), // 20 infrared
    vec3(0.02, 0.20, 0.28), vec3(0.02, 0.55, 0.60), vec3(0.10, 0.85, 0.85), vec3(0.55, 0.95, 0.85), vec3(0.95, 0.80, 0.50), // 21 lagoon
    vec3(0.55, 0.35, 0.18), vec3(0.85, 0.45, 0.25), vec3(0.95, 0.65, 0.45), vec3(0.75, 0.70, 0.45), vec3(1.00, 0.90, 0.70), // 22 desert
    vec3(0.25, 0.05, 0.60), vec3(0.08, 0.30, 0.95), vec3(0.00, 0.80, 0.80), vec3(0.82, 0.95, 0.12), vec3(0.45, 0.08, 0.75), // 23 absinthe
    vec3(0.28, 0.08, 0.20), vec3(0.85, 0.22, 0.38), vec3(1.00, 0.45, 0.38), vec3(1.00, 0.78, 0.58), vec3(0.92, 0.62, 0.28), // 24 roseGold
    vec3(0.05, 0.02, 0.15), vec3(0.15, 0.08, 0.50), vec3(0.85, 0.25, 0.75), vec3(0.30, 0.55, 1.00), vec3(1.20, 1.15, 1.30), // 25 deepSpace
    vec3(0.50, 0.50, 0.12), vec3(0.75, 0.70, 0.15), vec3(0.45, 0.08, 0.20), vec3(0.70, 0.20, 0.40), vec3(0.80, 0.65, 0.25), // 26 tuscany
    vec3(0.03, 0.02, 0.06), vec3(0.12, 0.05, 0.25), vec3(1.00, 0.05, 0.60), vec3(0.10, 0.08, 0.30), vec3(0.05, 0.04, 0.10), // 27 neonNoir
    vec3(0.75, 0.82, 0.92), vec3(0.95, 0.97, 1.00), vec3(1.00, 0.92, 0.90), vec3(0.98, 1.00, 0.98), vec3(0.70, 0.75, 0.88), // 28 porcelain
    vec3(0.08, 0.04, 0.03), vec3(0.38, 0.12, 0.06), vec3(0.72, 0.28, 0.08), vec3(0.90, 0.55, 0.15), vec3(0.30, 0.15, 0.10), // 29 rust
    vec3(0.20, 0.04, 0.05), vec3(0.65, 0.05, 0.15), vec3(1.00, 0.15, 0.10), vec3(1.00, 0.85, 0.65), vec3(0.80, 0.45, 0.15), // 30 cherryCola
    vec3(1.00, 0.06, 0.10), vec3(1.00, 0.75, 0.05), vec3(0.00, 0.85, 0.75), vec3(0.10, 0.30, 1.00), vec3(0.75, 0.10, 0.95) // 31 prismatic
);
const vec2 PAL_EDGES[128] = vec2[128](
    vec2(0.0), vec2(0.0), vec2(0.0), vec2(0.0), // 0
    vec2(0.00, 0.24), vec2(0.20, 0.50), vec2(0.46, 0.78), vec2(0.76, 1.00), // 1
    vec2(0.00, 0.28), vec2(0.24, 0.56), vec2(0.52, 0.80), vec2(0.78, 1.00), // 2
    vec2(0.00, 0.28), vec2(0.24, 0.54), vec2(0.50, 0.76), vec2(0.74, 1.00), // 3
    vec2(0.00, 0.24), vec2(0.20, 0.44), vec2(0.40, 0.72), vec2(0.70, 1.00), // 4
    vec2(0.0), vec2(0.0), vec2(0.0), vec2(0.0), // 5
    vec2(0.0), vec2(0.0), vec2(0.0), vec2(0.0), // 6
    vec2(0.0), vec2(0.0), vec2(0.0), vec2(0.0), // 7
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 8
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 9
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 10
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 11
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 12
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 13
    vec2(0.00, 0.45), vec2(0.42, 0.68), vec2(0.64, 0.86), vec2(0.84, 1.00), // 14
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 15
    vec2(0.00, 0.40), vec2(0.36, 0.64), vec2(0.60, 0.84), vec2(0.82, 1.00), // 16
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 17
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 18
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 19
    vec2(0.00, 0.30), vec2(0.26, 0.55), vec2(0.50, 0.80), vec2(0.84, 1.00), // 20
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 21
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 22
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 23
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 24
    vec2(0.00, 0.35), vec2(0.32, 0.62), vec2(0.58, 0.80), vec2(0.82, 0.90), // 25
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 26
    vec2(0.28, 0.46), vec2(0.44, 0.52), vec2(0.50, 0.60), vec2(0.58, 0.95), // 27
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 28
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 29
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00), // 30
    vec2(0.00, 0.26), vec2(0.24, 0.52), vec2(0.48, 0.78), vec2(0.76, 1.00) // 31
);

vec3 palStops(float t, int idx) {
    t = fract(t);
    vec3 c = PAL_STOPS[idx * 5];
    for (int s = 1; s < 5; ++s) {
        vec2 e = PAL_EDGES[idx * 4 + s - 1];
        c = mix(c, PAL_STOPS[idx * 5 + s], smoothstep(e.x, e.y, t));
    }
    return c;
}

vec3 palEmberIce(float t) { return palStops(t, 1); }

vec3 palUltraviolet(float t) { return palStops(t, 2); }

vec3 palDeepSea(float t) { return palStops(t, 3); }

vec3 palSolarFlare(float t) { return palStops(t, 4); }

// Miami sunset: deep purple -> hot pink -> coral -> gold -> teal.
vec3 palMiami(float t) { return palStops(t, 8); }

// Abyss: monochrome blue depth — near-black -> navy -> cobalt -> azure -> pale cyan.
vec3 palAbyss(float t) { return palStops(t, 9); }

// Sakura: muted rose — deep plum -> rose -> blush -> petal white -> soft coral.
vec3 palSakura(float t) { return palStops(t, 10); }

// Horizon: film teal/orange duotone — deep teal -> cyan -> cream -> amber -> burnt orange.
vec3 palHorizon(float t) { return palStops(t, 11); }

// Molten gold: warm-only — maroon -> crimson -> orange -> gold -> pale gold.
vec3 palMoltenGold(float t) { return palStops(t, 12); }

// Glacier: cold pale — midnight navy -> steel blue -> ice cyan -> white-blue -> pale violet.
vec3 palGlacier(float t) { return palStops(t, 13); }

// Velvet: dark wine, dwells long in shadow before dusty rose -> mauve.
vec3 palVelvet(float t) { return palStops(t, 14); }

// Patina: earthy — copper -> rust -> verdigris teal -> seafoam -> bone.
vec3 palPatina(float t) { return palStops(t, 15); }

// Jade: deep pine -> jade -> seafoam -> pale mint; dwells dark.
vec3 palJade(float t) { return palStops(t, 16); }

// Amber: committed honey monochrome — umber -> amber -> honey gold -> pale straw.
vec3 palAmber(float t) { return palStops(t, 17); }

// Smoke: near-grayscale — charcoal -> slate -> silver -> white smoke -> blue-grey.
vec3 palSmoke(float t) { return palStops(t, 18); }

// Candy: bright pastel — bubblegum pink -> lilac -> sky cyan -> mint -> lemon.
vec3 palCandy(float t) { return palStops(t, 19); }

// Infrared: heat ramp, white-hot held to the very top.
vec3 palInfrared(float t) { return palStops(t, 20); }

// Lagoon: deep sea green -> turquoise -> aqua -> seafoam -> warm sand.
vec3 palLagoon(float t) { return palStops(t, 21); }

// Desert: muted earth — sand -> terracotta -> dusty rose -> sage -> cream.
vec3 palDesert(float t) { return palStops(t, 22); }

// Absinthe: violet -> electric blue -> cyan -> golden lime -> violet.
vec3 palAbsinthe(float t) { return palStops(t, 23); }

// Rose gold: muted metallic — deep mauve -> rose -> salmon -> champagne -> antique gold.
vec3 palRoseGold(float t) { return palStops(t, 24); }

// Deep space: dark-dominant nebula; narrow starlight-white burst near the top.
vec3 palDeepSpace(float t) { return palStops(t, 25); }

// Tuscany: olive & wine — olive gold -> chartreuse-olive -> deep wine -> plum -> brass.
vec3 palTuscany(float t) { return palStops(t, 26); }

// Neon noir: black with a single narrow hot-magenta spike.
vec3 palNeonNoir(float t) { return palStops(t, 27); }

// Porcelain: white-dominant — pale blue shadow -> white -> blush hint -> white -> grey-blue.
vec3 palPorcelain(float t) { return palStops(t, 28); }

// Rust: iron oxide — near-black -> oxide brown -> rust orange -> ochre -> dark umber.
vec3 palRust(float t) { return palStops(t, 29); }

// Cherry cola: dark cherry -> crimson -> red-orange -> cream -> caramel.
vec3 palCherryCola(float t) { return palStops(t, 30); }

// Prismatic: full rainbow sweep, high saturation.
vec3 palPrismatic(float t) { return palStops(t, 31); }

vec3 palByIndex(float t, int idx) {
    idx = clamp(idx, 0, 31);
    if (idx == 0) return palCity(t);
    if (idx == 5) return palNeon(t);
    if (idx == 6) return palHeatIce(t);
    if (idx == 7) return palNight(t);
    return palStops(t, idx);
}

vec3 sanitizeColor(vec3 col) {
    // NaN/Inf guard (isfinite/select port): NaN != NaN under IEEE
    bvec3 nan = notEqual(col, col);
    col = mix(col, vec3(0.0), vec3(nan));
    return clamp(col, vec3(0.0), vec3(16.0));
}

vec3 suppressGreenCast(vec3 col) {
    col = sanitizeColor(col);
    float rb = max(col.r, col.b);
    float lum = (col.r + col.g + col.b) * 0.3333333;
    float greenCap = max(rb * 1.08, lum * 0.78);
    float excess = max(0.0, col.g - greenCap);
    col.g -= excess * 0.78;
    col.b += excess * 0.86;
    col.r += excess * 0.34;
    return col;
}

// beatArc rises from 0 at the beat to 1 mid-beat and back to 0 by the next beat, so a hue
// shift driven by it has no step at the beat wrap (a raw beatPhase term jumps by its full
// amount on every beat, which reads as a flash over the whole frame; CORE-2659).
float beatArc(VisualUniforms u) {
    return 0.5 - 0.5 * cos(6.2831853 * u.beatPhase);
}

// Green-cast suppression is applied ONCE to the finished frame by the platform
// wrapper, not per palette read. Per-sample suppression was both expensive
// (~15 extra ops on every one of up to 120 palette reads per pixel, since most
// visuals call palVisual inside a loop) and wrong under accumulation:
// suppress(a) + suppress(b) != suppress(a + b) for a non-linear correction.
vec3 palVisual(float t, VisualUniforms u) {
    if (u.palette.x >= 0.0) {
        return palByIndex(t, int(floor(u.palette.x + 0.5)));
    }
    float phase = u.palette.z;
    // mod() not % : GLSL leaves integer % undefined when either operand is
    // negative, and a host may hand us a negative free-mode phase.
    int a = int(mod(floor(phase), 32.0));
    int b = int(mod(float(a + 1), 32.0));
    float blend = smoothstep(0.18, 0.82, fract(phase));
    float nudge = beatArc(u) * 0.015 + u.spectralCentroid * 0.08;
    return mix(palByIndex(t + nudge, a), palByIndex(t + nudge, b), blend);
}

vec3 peakWhite(vec3 col, float peak) {
    return mix(col, vec3(2.6, 2.35, 2.10), saturate(peak));
}

vec3 palRoleTrace(float t, VisualUniforms u) {
    return palVisual(t + 0.03 * beatArc(u), u);
}

vec3 palRoleBass(float t, VisualUniforms u) {
    vec3 hot = palVisual(0.03 + t * 0.18 + 0.04 * beatArc(u), u);
    vec3 body = palVisual(t + 0.10, u);
    return mix(body, hot, 0.62 + 0.25 * u.bassImpact);
}

vec3 palRoleTreble(float t, VisualUniforms u) {
    vec3 spark = palVisual(0.62 + t * 0.28 + 0.10 * u.spectralCentroid, u);
    return peakWhite(spark, u.treble * u.onset * 0.14);
}

vec3 palRolePeak(float t, VisualUniforms u) {
    return peakWhite(palVisual(t + 0.08 * u.spectralCentroid, u), saturate(u.onset * 0.18 + u.bassImpact * 0.10));
}

float luma(vec3 c) { return dot(c, vec3(0.2126, 0.7152, 0.0722)); } // Rec.709

vec3 palRoleFog(float t, VisualUniforms u) {
    vec3 c = palVisual(t + 0.04 * u.spectralCentroid, u);
    float grey = luma(c);
    return mix(vec3(grey * 0.62), c, 0.42);
}

vec3 palRoleRim(float t, VisualUniforms u) {
    return mix(palVisual(t, u), palRolePeak(t + 0.18, u), 0.35 + 0.35 * u.flux);
}

vec3 accentize(vec3 col, vec4 accent, float amt) {
    vec3 a = normalize(accent.rgb + 1e-3);
    return mix(col, col * (0.5 + a), clamp(amt, 0.0, 1.0));
}

float beatRadians(VisualUniforms u, float offset) {
    return (u.beatPhase + offset) * 6.2831853;
}

float beatWave(VisualUniforms u, float offset) {
    return 0.5 + 0.5 * sin(beatRadians(u, offset));
}

float phasePulse(VisualUniforms u, float offset, float width) {
    float d = abs(fract(u.beatPhase - offset + 0.5) - 0.5);
    return exp(-d * d / max(width * width, 1e-4));
}

// ---- frame fit ----
// Column layouts are authored for a 16:9 frame. columnFit is the share of the authored columns
// that fits this frame at their authored width measured against the frame's height: 1 at 16:9 or
// wider, about 0.26 on a portrait phone. fitColumns turns it into a count, at least `minimum` and
// never more than one column per 4 px. A visual draws its x widths divided by
// fitColumns / authored, so a narrow frame gets fewer, wider columns instead of the same count
// squeezed together.
float columnFit(VisualUniforms u) {
    return clamp(u.resolution.x / max(u.resolution.y, 1.0) * (9.0 / 16.0), 0.0, 1.0);
}
int fitColumns(VisualUniforms u, int authored, int minimum) {
    float n = min(float(authored) * columnFit(u), u.resolution.x / 4.0);
    return clamp(int(floor(n + 0.5)), min(minimum, authored), authored);
}

// ---- presentation hooks ----
float presentationMode(VisualUniforms u) { return u.presentation0.x; }
float presentationChrome(VisualUniforms u) { return u.presentation0.y; }
float presentationMetadata(VisualUniforms u) { return u.presentation0.z; }
float presentationIntensity(VisualUniforms u) { return clamp(u.presentation1.x, 0.0, 1.0); }

float safeRectMask(vec2 uv, vec4 rect, float feather) {
    vec2 lo = smoothstep(rect.xy - feather, rect.xy + feather, uv);
    vec2 hi = smoothstep(1.0 - rect.zw - feather, 1.0 - rect.zw + feather, 1.0 - uv);
    return lo.x * lo.y * hi.x * hi.y;
}

float overlaySafeMask(vec2 uv, VisualUniforms u) {
    return safeRectMask(uv, u.safeRect, 0.025);
}

// ---- geometry / line helpers ----
mat2 rot2(float a) {
    float c = cos(a), s = sin(a);
    return mat2(c, -s, s, c);
}

// Centered coordinates lean toward the frame's short side: y spans -1..1 on a
// landscape frame; on a portrait one the scene zooms out by aspect^-0.7, so x spans
// about -0.8..0.8 on a phone held upright (-0.46..0.46 without it) and a subject
// authored within radius 1 keeps most of its width without shrinking to a dot.
// Unchanged at aspect >= 1.
float shortSideFit(float aspect) {
    return pow(clamp(aspect, 0.05, 1.0), -0.7);
}

vec2 centered(vec2 uv, vec2 res) {
    vec2 p = uv * 2.0 - 1.0;
    float aspect = res.x / max(res.y, 1.0);
    p.x *= aspect;
    return p * shortSideFit(aspect);
}

vec2 motionCentered(vec2 uv, VisualUniforms u) {
    vec2 p = uv * 2.0 - 1.0;
    float aspect = u.resolution.x / max(u.resolution.y, 1.0);
    p.x *= aspect;

    float angle = u.motion.x;
    if (abs(angle) > 0.0001) {
        p = rot2(angle) * p;
    }

    float tx = u.motion.y;
    float ty = u.motion.z;
    float depth = u.motion.w;
    if (abs(tx) + abs(ty) + abs(depth) > 0.0001) {
        float y = p.y;
        float x = p.x / max(aspect, 1e-3);
        float z = 1.0 + depth + x * tx + y * ty;
        z = max(0.35, z);
        p /= z;
        p.y += ty * 0.18;
        p.x += tx * aspect * 0.10;
    }

    return p * shortSideFit(aspect);
}

float aaFill(float d) {
    float w = max(fwidth(d), 1e-5);
    return clamp(0.5 - d / w, 0.0, 1.0);
}
float aaLine(float d, float halfWidth) {
    float w = max(fwidth(d), 1e-5);
    return 1.0 - smoothstep(halfWidth, halfWidth + w, abs(d));
}
// Repeating line at fract(phase) == 0.5. Takes the CONTINUOUS phase so fwidth
// never sees the fract() 1->0 jump: aaLine(fract(p)-0.5, hw) reads fwidth ~= 1.0
// on the quad straddling the wrap and returns ~0.5 there, painting a ghost line
// at every cell boundary. Always use this for cyclic/repeating rulings.
float aaLineCyclic(float phase, float halfWidth) {
    float w = max(fwidth(phase), 1e-5);
    return 1.0 - smoothstep(halfWidth, halfWidth + w, abs(fract(phase) - 0.5));
}

// ---- noise toolbox ----
// Hashes are integer work, written for Oscillux: each float input's bit pattern
// is chained through an xorshift-multiply mixer (multipliers and seeds chosen
// for this library by an avalanche search), and the top 24 bits become a float
// in [0, 1). Every distinct input gives its own value, and the same value on
// every GPU and backend: no sin(), no reliance on float rounding.
uint hashBits(float x) {
    uint b = floatBitsToUint(x);
    return b == 0x80000000u ? 0u : b; // -0.0 hashes as 0.0
}
uint hashMix(uint h) {
    h ^= h >> 16u;
    h *= 0xa7d471dbu;
    h ^= h >> 14u;
    h *= 0x76a997d3u;
    h ^= h >> 16u;
    return h;
}
float hashUnit(uint h) { return float(h >> 8u) * (1.0 / 16777216.0); }
uint hashKey(vec2 p) { return hashMix(hashMix(hashBits(p.x) ^ 0xed81fd3au) ^ hashBits(p.y)); }
uint hashKey(vec3 p) { return hashMix(hashKey(p.xy) ^ hashBits(p.z)); }
float hash11(float p) { return hashUnit(hashMix(hashBits(p) ^ 0xed81fd3au)); }
float hash21(vec2 p) { return hashUnit(hashKey(p)); }
vec2 hash22(vec2 p) {
    uint h = hashKey(p);
    return vec2(hashUnit(h), hashUnit(hashMix(h ^ 0x6294a626u)));
}
float hash31(vec3 p) { return hashUnit(hashKey(p)); }
float vnoise(vec2 p) {
    vec2 i = floor(p), f = fract(p);
    vec2 w = f * f * (3.0 - 2.0 * f);
    float a = hash21(i + vec2(0, 0));
    float b = hash21(i + vec2(1, 0));
    float c = hash21(i + vec2(0, 1));
    float d = hash21(i + vec2(1, 1));
    return mix(mix(a, b, w.x), mix(c, d, w.x), w.y);
}
// Octave step: scale by 2 and rotate ~36.87 deg (3-4-5 triangle), so the
// value-noise lattice of successive octaves never lines up on the axes.
const mat2 FBM_OCTAVE = mat2(1.6, 1.2, -1.2, 1.6);
float fbm(vec2 p) {
    float v = 0.0, a = 0.5;
    for (int i = 0; i < 5; ++i) { v += a * vnoise(p); p = FBM_OCTAVE * p; a *= 0.5; }
    return v;
}
float fbmRidged(vec2 p) {
    float v = 0.0, a = 0.5;
    for (int i = 0; i < 5; ++i) {
        float n = 1.0 - abs(2.0 * vnoise(p) - 1.0);
        v += a * n * n; p = FBM_OCTAVE * p; a *= 0.5;
    }
    return v;
}

vec3 hsv2rgb(vec3 c) {
    vec3 p = abs(fract(c.xxx + vec3(1.0, 2.0/3.0, 1.0/3.0)) * 6.0 - 3.0);
    return c.z * mix(vec3(1.0), clamp(p - 1.0, 0.0, 1.0), c.y);
}

// POST CHAIN ONLY — visuals must not call this.
vec3 tonemap(vec3 x) {
    x = (x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14);
    return pow(clamp(x, vec3(0.0), vec3(1.0)), vec3(1.0 / 2.2));
}

// SPDX-License-Identifier: AGPL-3.0-only
// Shared helpers for the Oscillux originals, joined after library.glsl: the
// beat-locked flight speed, the eased roll, the detail level the host can
// afford and a few common shapes. Every function is a pure function of its
// arguments, so all targets agree.

// Speed profile, in distance per beat, over a 32-beat cycle: cruise, build,
// burst, a hard brake, a slow breakdown drift, then a build to a final burst.
float osBeatSpeed(float n) {
    float k = mod(n, 32.0);
    if (k < 4.0) return 1.0;
    if (k < 8.0) return 1.0 + 0.3 * (k - 3.0);
    if (k < 12.0) return 3.5;
    if (k < 13.0) return 1.6;
    if (k < 14.0) return 0.45;
    if (k < 20.0) return 0.9 + 0.1 * (k - 14.0);
    if (k < 24.0) return 0.6;
    if (k < 28.0) return 1.5 + 0.75 * (k - 24.0);
    if (k < 31.0) return 4.5;
    return 2.4;
}

// The flight state is a pure function of the uniforms, so every target and
// frame rate computes the same camera. Within a beat the speed ramps linearly
// from this beat's value to the next one's, so velocity never jumps and the
// distance is its exact integral. x = distance, y = speed in beats' units.
vec2 osFlight(VisualUniforms u, float cruise, float surge) {
    float n = floor(u.beatCount);
    float cycle = 0.0;
    float before = 0.0;
    float k0 = n - mod(n, 32.0);
    for (int i = 0; i < 32; i++) {
        float k = k0 + float(i);
        float seg = 0.5 * (osBeatSpeed(k) + osBeatSpeed(k + 1.0));
        cycle += seg;
        before += k < n ? seg : 0.0;
    }
    float ph = saturate(u.beatPhase);
    float v0 = osBeatSpeed(n);
    float v1 = osBeatSpeed(n + 1.0);
    float beats = floor(n / 32.0) * cycle + before + v0 * ph + 0.5 * (v1 - v0) * ph * ph;
    float v = mix(v0, v1, ph);
    // A fifth of the motion is steady; the rest follows the profile, so a burst
    // runs about four times cruise and a brake drops to about half.
    return vec2(u.time * cruise * 0.2 + beats * (cruise * 0.4 + surge), v);
}

// Detail the host can afford: 1, the default, down to 0. Hosts lower it with
// presentation1.y, a reduction in [0, 1], to hold their frame rate.
float osDetail(VisualUniforms u) { return 1.0 - saturate(u.presentation1.y); }

// Bank angle: every eight beats the roll picks a new side (a golden-angle
// walk) and eases toward it across the whole span, so it never stops and starts.
float osBank(VisualUniforms u, float amount) {
    float b = (u.beatCount + saturate(u.beatPhase)) / 8.0;
    float k = floor(b);
    return amount * mix(sin(k * 2.39996), sin((k + 1.0) * 2.39996), smoothstep(0.0, 1.0, b - k));
}

// Ray direction for a camera at ro looking at ta, rolled by roll, with
// focal length fov (larger is narrower). p is the centred pixel position.
vec3 osCamera(vec3 ro, vec3 ta, float roll, float fov, vec2 p) {
    vec3 fw = normalize(ta - ro);
    vec3 up = vec3(sin(roll), cos(roll), 0.0);
    vec3 rt = normalize(cross(fw, up));
    vec3 cu = cross(rt, fw);
    return normalize(p.x * rt + p.y * cu + fov * fw);
}

float osBox(vec3 p, vec3 b) {
    vec3 q = abs(p) - b;
    return length(max(q, 0.0)) + min(max(q.x, max(q.y, q.z)), 0.0);
}

// Smooth union with blend radius k.
float osSmin(float a, float b, float k) {
    float h = saturate(0.5 + 0.5 * (b - a) / k);
    return mix(b, a, h) - k * h * (1.0 - h);
}

// Round a march step count from a detail-scaled float, so every target
// agrees on the count.
int osSteps(float lo, float hi, float detail) { return int(mix(lo, hi, detail) + 0.5); }

// ---- vivid palettes ----
// In the hosts' free palette mode (palette.x < 0) the originals read this set
// instead of the library's: sixteen loops of five saturated stops in linear
// light, each a few hue families with a contrasting accent. palette.z tours
// them, cross-fading from one to the next, and also walks t by a quarter loop
// per palette (four whole loops per tour, so the wrap is seamless), so a visual
// that reads only a narrow range of t still meets every stop. Manifest mode
// (palette.x >= 0) keeps the library palettes, so reference renders do not
// change.
const vec3 OS_PAL[80] = vec3[80](
    vec3(1.000, 0.033, 0.133), vec3(1.000, 0.262, 0.010), vec3(1.000, 0.807, 0.010), vec3(0.033, 1.000, 0.262), vec3(0.000, 0.578, 1.000), // 0 tropicPunch
    vec3(0.000, 0.784, 1.000), vec3(0.027, 0.105, 1.000), vec3(0.479, 0.022, 1.000), vec3(1.000, 0.027, 0.309), vec3(1.000, 0.323, 0.014), // 1 synthSunset
    vec3(0.000, 1.000, 0.578), vec3(0.000, 0.392, 1.000), vec3(1.000, 0.078, 0.366), vec3(1.000, 0.434, 0.000), vec3(1.000, 0.102, 0.037), // 2 lagoonPop
    vec3(0.565, 1.000, 0.000), vec3(1.000, 0.791, 0.000), vec3(1.000, 0.195, 0.000), vec3(1.000, 0.000, 0.100), vec3(0.000, 1.000, 0.195), // 3 acidCitrus
    vec3(0.000, 0.871, 1.000), vec3(0.000, 0.133, 1.000), vec3(1.000, 0.047, 0.000), vec3(1.000, 0.552, 0.000), vec3(1.000, 0.000, 0.156), // 4 iceFire
    vec3(0.000, 1.000, 0.332), vec3(0.000, 0.658, 1.000), vec3(0.047, 0.102, 0.991), vec3(1.000, 0.823, 0.000), vec3(0.328, 1.000, 0.000), // 5 electricMint
    vec3(1.000, 0.347, 0.000), vec3(1.000, 0.074, 0.000), vec3(0.791, 0.000, 0.195), vec3(0.144, 0.000, 0.905), vec3(0.000, 0.539, 1.000), // 6 mangoBerry
    vec3(1.000, 0.050, 0.371), vec3(1.000, 0.347, 0.050), vec3(1.000, 0.784, 0.050), vec3(0.050, 1.000, 0.434), vec3(0.050, 0.479, 1.000), // 7 candyShop
    vec3(0.000, 1.000, 0.235), vec3(0.000, 0.745, 0.565), vec3(0.000, 0.296, 1.000), vec3(0.254, 0.024, 1.000), vec3(1.000, 0.028, 0.631), // 8 aurora
    vec3(1.000, 0.014, 0.014), vec3(1.000, 0.144, 0.000), vec3(1.000, 0.479, 0.000), vec3(1.000, 0.791, 0.000), vec3(1.000, 0.000, 0.212), // 9 lavaLamp
    vec3(0.784, 1.000, 0.000), vec3(0.000, 1.000, 0.133), vec3(0.000, 0.694, 1.000), vec3(0.000, 0.080, 1.000), vec3(1.000, 0.434, 0.000), // 10 cyberLime
    vec3(1.000, 0.102, 0.114), vec3(1.000, 0.026, 0.301), vec3(0.397, 0.045, 1.000), vec3(0.026, 0.198, 1.000), vec3(0.000, 0.807, 0.565), // 11 coralReef
    vec3(1.000, 0.631, 0.000), vec3(1.000, 0.254, 0.000), vec3(1.000, 0.044, 0.044), vec3(1.000, 0.195, 0.000), vec3(1.000, 0.871, 0.074), // 12 goldRush
    vec3(0.000, 0.010, 1.000), vec3(0.000, 0.283, 1.000), vec3(0.000, 0.913, 0.658), vec3(0.328, 1.000, 0.000), vec3(1.000, 0.791, 0.000), // 13 deepNeon
    vec3(1.000, 0.195, 0.565), vec3(0.571, 0.205, 1.000), vec3(0.195, 0.392, 1.000), vec3(0.195, 1.000, 0.745), vec3(1.000, 0.871, 0.195), // 14 bubbleTea
    vec3(1.000, 0.000, 0.091), vec3(0.133, 0.000, 1.000), vec3(0.000, 0.451, 1.000), vec3(0.000, 1.000, 0.665), vec3(0.468, 1.000, 0.000) // 15 neonTokyo
);
const float OS_PAL_COUNT = 16.0;

// Linear mix that keeps the endpoints' channel range, so a blend between two
// saturated colours stays saturated instead of passing through grey.
vec3 osVividMix(vec3 a, vec3 b, float w) {
    vec3 m = mix(a, b, w);
    float lo = min(m.r, min(m.g, m.b)), hi = max(m.r, max(m.g, m.b));
    float tlo = mix(min(a.r, min(a.g, a.b)), min(b.r, min(b.g, b.b)), w);
    float thi = mix(max(a.r, max(a.g, a.b)), max(b.r, max(b.g, b.b)), w);
    return tlo + (m - lo) * ((thi - tlo) / max(hi - lo, 1e-3));
}

vec3 osPalLoop(float t, int idx) {
    float f = fract(t) * 5.0;
    int i = int(floor(f));
    i = clamp(i, 0, 4);
    int j = i == 4 ? 0 : i + 1;
    return osVividMix(OS_PAL[idx * 5 + i], OS_PAL[idx * 5 + j], smoothstep(0.0, 1.0, f - float(i)));
}

vec3 osPalVisual(float t, VisualUniforms u) {
    if (u.palette.x >= 0.0) return palVisual(t, u);
    float phase = u.palette.z;
    int a = int(mod(floor(phase), OS_PAL_COUNT));
    int b = int(mod(float(a + 1), OS_PAL_COUNT));
    float blend = smoothstep(0.18, 0.82, fract(phase));
    float nudge = phase * 0.25 + u.beatPhase * 0.015 + u.spectralCentroid * 0.08;
    return osVividMix(osPalLoop(t + nudge, a), osPalLoop(t + nudge, b), blend);
}

// The calm visuals' audio response, 0..0.25: the music's level, which rises
// and falls with the song, scaled by the sensitivity. Not the kick: a calm
// visual's light never jumps on the beat.
float osCalmBreath(VisualUniforms u) {
    return 0.25 * saturate(u.level) * (0.5 + 0.5 * u.sensitivity);
}

// The calm visuals' palette read: the visual's own library palette `own`
// (its manifest palette) at t, with no beat or spectral nudge. Under the vivid
// palette tour (palette.x < 0) a calm visual keeps `own`: the tour moves
// through sixteen neon palettes of very different brightness every few
// seconds, which turns a moonlit sea orange and swells the light, so vivid
// leaves a calm visual only its present-pass saturation. A host's manifest
// palette (palette.x >= 0) still replaces it.
vec3 osPalCalm(float t, int own, VisualUniforms u) {
    return palByIndex(t, u.palette.x >= 0.0 ? int(floor(u.palette.x + 0.5)) : own);
}

// The library's palette roles, reading osPalVisual.
vec3 osPalRoleTrace(float t, VisualUniforms u) { return osPalVisual(t + 0.03 * u.beatPhase, u); }
vec3 osPalRoleBass(float t, VisualUniforms u) {
    vec3 hot = osPalVisual(0.03 + t * 0.18 + 0.04 * u.beatPhase, u);
    vec3 body = osPalVisual(t + 0.10, u);
    return mix(body, hot, 0.62 + 0.25 * u.bassImpact);
}
vec3 osPalRoleTreble(float t, VisualUniforms u) {
    vec3 spark = osPalVisual(0.62 + t * 0.28 + 0.10 * u.spectralCentroid, u);
    return peakWhite(spark, u.treble * u.onset * 0.14);
}
vec3 osPalRolePeak(float t, VisualUniforms u) {
    return peakWhite(osPalVisual(t + 0.08 * u.spectralCentroid, u), saturate(u.onset * 0.18 + u.bassImpact * 0.10));
}
vec3 osPalRoleFog(float t, VisualUniforms u) {
    vec3 c = osPalVisual(t + 0.04 * u.spectralCentroid, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}
vec3 osPalRoleRim(float t, VisualUniforms u) {
    return mix(osPalVisual(t, u), osPalRolePeak(t + 0.18, u), 0.35 + 0.35 * u.flux);
}

// Every original finishes through osOffRed. The present pass grades with the vivid saturation
// and vibrance (palette.rs), which pushes a nearly pure red past the 0.8 red share at which the
// flash limiter counts saturated red and holds it. Where green plus blue is under 0.55 of red
// (linear light), both rise by half the difference, so the graded red presents under a 0.77
// share. Near black the present dither can still split a dim red into a saturated-red byte
// triple such as (1, 0, 0), so a dim colour becomes exactly grey and a dim red loses its excess
// over green and blue, which then ramps back in (limiter-red-expire-black, design.md section 3;
// tools/shader/test-originals-red.mjs). A quality measure: the frame limiter carries the safety
// claim. NaN becomes 0 by a boolean select, exact on targets honouring IEEE NaN.
const float OS_GREY_BELOW = 0.006;  // below it the colour becomes exactly grey
const float OS_RED_FROM = 0.012;    // below it red is at most the larger of green and blue
const float OS_RED_FULL = 0.05;     // red's excess is whole from here, and red returned as it stands
vec3 osOffRed(vec3 c) {
    c = mix(c, vec3(0.0), notEqual(c, c));      // NaN -> 0
    c = clamp(c, vec3(0.0), vec3(16.0));
    float s = c.g + c.b, t = 0.55 * c.r;        // gated on the rounded sum and product, so a
    if (s < t) c += vec3(0.0, 0.5 * (t - s), 0.5 * (t - s));  // colour that meets the share is kept
    float m = max(c.r, max(c.g, c.b));
    if (m < OS_GREY_BELOW) return vec3(luma(c));  // three identical channels
    float p = max(c.g, c.b);
    if (c.r > p && m < OS_RED_FULL) {           // no saturated red byte from the present dither
        float w = clamp((m - OS_RED_FROM) / (OS_RED_FULL - OS_RED_FROM), 0.0, 1.0);
        c.r = w <= 0.0 ? p : p + w * (c.r - p);  // red equal to a partner below OS_RED_FROM
    }
    return c;
}

// Everything joined after this file reads the vivid set through the usual names.
#define palVisual osPalVisual
#define palRoleTrace osPalRoleTrace
#define palRoleBass osPalRoleBass
#define palRoleTreble osPalRoleTreble
#define palRolePeak osPalRolePeak
#define palRoleFog osPalRoleFog
#define palRoleRim osPalRoleRim

uniform VisualUniforms u;
vec3 feedbackTrail(vec3 col, vec2 uv, float decay, float zoom, float rotAmt) {
    float n = u.dt > 0.0 ? min(u.dt * 60.0, 4.0) : 1.0;
    vec2 q = uv - 0.5;
    q = rot2(rotAmt * n) * q;
    q *= pow(max(1.0 - zoom, 1e-4), n);
    vec2 s = q + 0.5;
    vec2 out2 = max(-s, s - 1.0);
    float inside = clamp(1.0 - 32.0 * max(max(out2.x, out2.y), 0.0), 0.0, 1.0);
    vec3 p = suppressGreenCast(PREV(s).rgb) * inside;
    float k = pow(clamp(decay, 0.0, 0.90), n);
    return suppressGreenCast(max(suppressGreenCast(col), p * k));
}
// SPDX-License-Identifier: AGPL-3.0-only
// Circuit.
// A low fly-over of a circuit board seen as a night city. Chips, capacitor
// clusters and finned heat-sink towers stand in blocks between avenues of
// neon and bare-copper bus traces. Data packets stream along the traces, the
// chip tops twinkle like lit office windows, and uplink beams rise from the
// skyline. Every beat sends a faint copper or ice-blue diamond ripple across
// the board that warms the traces it passes. The glossy solder mask mirrors
// the towers. The camera glides at a slow steady pace with a gentle sway, and
// packets, windows and uplink bursts move unhurriedly, so no bright structure
// sweeps fast over the dark board. The light follows the music's smoothed
// level with only a small spectrum term and no beat flashes; reds are kept
// just off saturated red. Its colours come from its own palette, unnudged by
// beat or centroid, so the host's vivid palette tour never swells the light.
// param0 = Scale (board size under the camera) ; param1 = Pulse Speed ; param2 = Trace Brightness

const float CI_C = 4.0;    // block pitch
const float CI_TOP = 2.7;  // tallest component

// avenue (x) and street (z) centre-lines run along the block boundaries
float ciPath(float z) { return 2.0 * sin(z * 0.023) + 1.2 * sin(z * 0.051 + 0.7); }

// Distance to the component standing in a block. l is block-local, board at y = 0.
float ciComp(vec3 l, float h, float h2) {
    if (h < 0.40) {
        // flat chip package
        vec3 b = vec3(0.8 + 0.5 * h2, 0.09 + 0.12 * fract(h2 * 7.0), 0.8 + 0.5 * fract(h2 * 3.3));
        return osBox(l - vec3(0.0, b.y, 0.0), b - 0.02) - 0.02;
    }
    if (h < 0.60) {
        // capacitor cluster: four cylinders
        vec2 s = vec2(l.x < 0.0 ? -1.0 : 1.0, l.z < 0.0 ? -1.0 : 1.0);
        vec2 c = l.xz - s * 0.78;
        float ht = 0.6 + 1.3 * hash21(s + h2 * 11.0);
        float dr = length(c) - 0.58;
        float dy = abs(l.y - ht * 0.5) - ht * 0.5;
        float d = min(max(dr, dy), 0.0) + length(max(vec2(dr, dy), 0.0)) - 0.03;
        // the quadrant split bounds the step so a neighbour is never jumped
        return min(d, min(abs(l.x), abs(l.z)) + 0.2);
    }
    if (h < 0.80) {
        // finned heat-sink tower
        float ht = 1.1 + 1.5 * h2;
        float base = osBox(l - vec3(0.0, 0.12, 0.0), vec3(1.35, 0.12, 1.35));
        float fx = l.x - (floor(l.x / 0.27) + 0.5) * 0.27;
        float fins = max(osBox(l - vec3(0.0, ht * 0.5, 0.0), vec3(1.2, ht * 0.5, 1.2)), abs(fx) - 0.05);
        return min(base, fins);
    }
    // field of small surface-mount parts
    vec2 sc = floor(l.xz / 0.6);
    vec2 sq = l.xz - (sc + 0.5) * 0.6;
    float sh = hash21(sc + h * 17.0);
    if (max(abs(l.x), abs(l.z)) > 1.5 || sh < 0.3) return 0.3 - max(abs(sq.x), abs(sq.y)) + 0.12;
    vec3 b = sh > 0.65 ? vec3(0.18, 0.06, 0.09) : vec3(0.09, 0.05 + 0.08 * sh, 0.18);
    float d = osBox(vec3(sq.x, l.y - b.y, sq.y), b);
    return min(d, 0.3 - max(abs(sq.x), abs(sq.y)) + 0.12);
}

float ciMap(vec3 p) {
    vec2 cell = floor(p.xz / CI_C);
    vec2 l2 = p.xz - (cell + 0.5) * CI_C;
    float h = hash21(cell + 0.37);
    float h2 = hash21(cell * 1.7 + 5.1);
    float d = ciComp(vec3(l2.x, p.y, l2.y), h, h2);
    // components keep 0.5 from the block edge, so the edge bounds the step
    vec2 e = 0.5 * CI_C - abs(l2);
    return min(d, min(e.x, e.y) + 0.5);
}

vec3 ciNormal(vec3 p) {
    vec2 e = vec2(0.002, 0.0);
    return normalize(vec3(ciMap(p + e.xyy) - ciMap(p - e.xyy),
                          ciMap(p + e.yxy) - ciMap(p - e.yxy),
                          ciMap(p + e.yyx) - ciMap(p - e.yyx)) + 1e-6);
}

// March the components inside their slab; -1 means the ray left the slab or
// reached tmax (the board plane) first.
float ciMarch(vec3 ro, vec3 rd, float tmax, int n) {
    float t = 0.0;
    if (ro.y > CI_TOP) {
        if (rd.y > -1e-3) return -1.0;
        t = (ro.y - CI_TOP) / -rd.y;
    }
    for (int i = 0; i < 140; i++) {
        if (i >= n) break;
        vec3 p = ro + rd * t;
        float d = ciMap(p);
        if (d < 0.0015 * (1.0 + t)) return t;
        t += d * 0.9;
        if (t > tmax || p.y < 0.0 || (p.y > CI_TOP + 0.01 && rd.y > 0.0)) break;
    }
    return -1.0;
}

// The palette read: the visual's own palette with no beat or spectral nudge.
vec3 ciPal(float t, VisualUniforms u) { return osPalCalm(t, 23, u); }

// Keeps every red just off saturated red once tonemapped.
float ciTone(float x) { return x * (2.51 * x + 0.03) / (x * (2.43 * x + 0.59) + 0.14); }
float ciToneInv(float y) {
    y = clamp(y, 0.0, 0.999);
    float a = 2.51 - 2.43 * y, b = 0.03 - 0.59 * y;
    return (-b + sqrt(b * b + 0.56 * a * y)) / (2.0 * a);
}
vec3 ciOffRed(vec3 c) {
    float r = ciTone(c.r), g = ciTone(c.g), b = ciTone(c.b);
    float need = 0.3 * r - g - b;
    if (need <= 0.0) return c;
    if (c.g > c.b) c.g = max(c.g, ciToneInv(g + need));
    else c.b = max(c.b, ciToneInv(b + need));
    return c;
}

// Palette colours, band levels and beat terms, computed once per pixel.
struct CiK {
    vec3 tA, tB, tre, pA, pB, key, glow, bass, mask, albA, albB, top, hot, cool;
    vec4 bA, bB;
    float wPh, wAmp, pulse, tm, bassE, beat, par;
};

CiK ciKMake(VisualUniforms u) {
    CiK k;
    k.tA = ciPal(0.55, u);
    k.tB = ciPal(0.95, u);
    k.tre = ciPal(0.3, u);
    k.pA = ciPal(0.6, u);
    k.pB = ciPal(0.95, u);
    k.glow = ciPal(0.35, u);
    // warm key from the right, cool palette rim from the left
    k.key = mix(vec3(1.0, 0.72, 0.48), ciPal(0.75, u), 0.3) * 0.1;
    k.bass = ciPal(0.4, u);
    k.mask = ciPal(0.2, u) * 0.006;
    k.albA = ciPal(0.5, u);
    k.albB = ciPal(0.9, u);
    k.top = ciPal(0.02, u);
    // bare copper: the board's own material, the warm counterpart to the neon
    k.hot = vec3(1.0, 0.45, 0.13);
    // its complement: an ice-blue charge tinted by the palette; beat waves
    // alternate between the two
    k.cool = mix(vec3(0.16, 0.55, 1.0), k.pB, 0.3);
    k.par = mod(floor(u.beatCount), 2.0);
    // band levels ride on the smoothed level; the spectrum only tints them
    float lv = 0.22 * saturate(u.level);
    k.bA = lv + 0.05 * vec4(band(u, 2), band(u, 6), band(u, 10), band(u, 16));
    k.bB = lv + 0.05 * vec4(band(u, 24), band(u, 32), band(u, 42), band(u, 54));
    k.wPh = saturate(u.beatPhase);
    k.wAmp = 0.005;
    k.pulse = mix(0.4, 1.2, u.param1);
    k.tm = mod(u.time, 600.0);
    k.bassE = 0.0;
    k.beat = 0.0;
    return k;
}

// A band level picked by a hash: linear blend across eight anchor bands.
float ciLv(CiK k, float h) {
    float x = h * 7.0;
    return dot(max(1.0 - abs(x - vec4(0.0, 1.0, 2.0, 3.0)), 0.0), k.bA)
         + dot(max(1.0 - abs(x - vec4(4.0, 5.0, 6.0, 7.0)), 0.0), k.bB);
}

// Two diamond waves per beat, the current and the fading previous one, so
// nothing pops when the beat rolls over. x = molten front, y = neon wake.
// Even beats send molten copper, odd beats an ice-blue charge: z is the
// copper share of the front, so ciWaveCol never pops at the rollover.
vec3 ciWave(vec2 xz, vec2 src, CiK k) {
    float m = abs(xz.x - src.x) + abs(xz.y - src.y);
    vec3 w = vec3(0.0);
    for (int i = 0; i < 2; i++) {
        float age = k.wPh + float(i);
        float x = m - (1.5 + 18.0 * age);
        // white-hot as it leaves the source, then a steady glow; a function of
        // age alone, so the rollover stays seamless
        float fade = exp(-age * 1.25) * (0.45 + 0.25 * exp(-age * 3.0));
        float f = (x < 0.0 ? exp(x * 1.8) : exp(-x * x * 1.6)) * fade;
        w.x += f;
        w.y += (x < 0.0 ? exp(x * 0.22) * 0.55 : 0.0) * fade;
        w.z += (f + 1e-4) * (i == 0 ? 1.0 - k.par : k.par);
    }
    return vec3(w.xy * k.wAmp, w.z / (w.x + 2e-4));
}

vec3 ciWaveCol(vec3 w, CiK k) { return mix(k.cool * 1.3, k.hot, w.z); }

// Anti-aliased line with an analytic footprint fp in the units of d, so no
// derivatives are taken inside branches.
float ciLine(float d, float hw, float fp) {
    return 1.0 - smoothstep(hw - fp, hw + fp, abs(d));
}

// Repeating line of period 1 centred at phase 0.5; it fades to its mean
// coverage as the period shrinks toward a pixel, so distant buses never shimmer.
float ciLines(float ph, float hw, float fp) {
    float l = ciLine(fract(ph) - 0.5, hw, fp);
    return mix(l, 2.0 * hw, smoothstep(0.2, 0.55, fp));
}

// Emission of the board: bus traces, fan-outs, vias, pads, silkscreen.
// fp is the pixel footprint (x across, y along the view). copper receives
// the copper coverage for the gloss.
vec3 ciBoard(vec2 xz, vec2 src, vec2 fp, CiK k, float detail, out float copper) {
    vec2 cell = floor(xz / CI_C);
    vec2 l = xz - (cell + 0.5) * CI_C;
    float h = hash21(cell + 0.37);
    float h2 = hash21(cell * 1.7 + 5.1);
    vec3 wv = ciWave(xz, src, k);
    vec3 wcl = ciWaveCol(wv, k);
    float tm = k.tm;

    vec3 col = vec3(0.0);
    copper = 0.0;

    // avenue bus: ten traces along z on each block boundary
    float s = xz.x - floor(xz.x / CI_C + 0.5) * CI_C;
    if (abs(s) < 0.55) {
        float li = floor(s / 0.1);
        float lid = li + 40.0 * floor(xz.x / CI_C + 0.5);
        float on = step(0.18, hash11(lid * 1.37 + 3.0));
        float seg = step(0.12, hash21(vec2(lid, floor(xz.y / 9.0 + hash11(lid)))));
        float dir = hash11(lid * 3.1) < 0.5 ? -1.0 : 1.0;
        float spd = k.pulse * (0.6 + 0.8 * hash11(lid * 5.7));
        float ph = fract((xz.y * dir - tm * spd) / 11.0 + hash11(lid * 9.3));
        float head = exp(-ph * 8.0) * 0.7 + exp(-ph * 3.0) * 0.3 + exp(-(1.0 - ph) * 30.0) * 0.3;
        // a packet swells its trace, so it reads at a distance
        float line = ciLines(s / 0.1, 0.17 + 0.12 * saturate(head * 0.3), fp.x / 0.1) * on * seg * step(abs(s), 0.5);
        float hc = hash11(lid * 2.3);
        float bi = ciLv(k, hash11(lid * 7.9));
        // one trace in four is bare copper, the rest carry the palette's neon
        vec3 tc = hc < 0.25 ? k.hot * 0.8 : mix(k.tA, k.tB, (hc - 0.25) * 1.33);
        col += line * (tc * (0.12 + 0.6 * bi + head * (0.4 + bi) + 3.0 * wv.y) + wcl * wv.x * 7.0);
        // soft halo the lit bus throws on the solder mask
        float halo = exp(-abs(s) * 4.0);
        col += halo * (tc * (0.04 + 0.2 * head * on * seg + 0.3 * wv.y) + wcl * wv.x * 0.5);
        copper += line;
    }
    // street bus: six traces along x
    float sz = xz.y - floor(xz.y / CI_C + 0.5) * CI_C;
    if (abs(sz) < 0.32 && abs(s) > 0.6) {
        float li = floor(sz / 0.1);
        float lid = li + 23.0 * floor(xz.y / CI_C + 0.5) + 7.0 * cell.x;
        float line = ciLines(sz / 0.1, 0.15, fp.y / 0.1) * step(0.25, hash11(lid * 1.9 + 1.0));
        float ph = fract((xz.x - tm * k.pulse * 0.7) / 6.0 + hash11(lid * 4.1));
        float head = exp(-ph * 8.0) * 0.5;
        col += line * (k.tre * (0.08 + head * 0.6 + 4.0 * wv.y) + wcl * wv.x * 6.0);
        copper += line;
    }
    // block interior: pads, fan-out traces, vias and silkscreen
    float inner = 0.5 * CI_C - 0.6;
    if (max(abs(l.x), abs(l.y)) < inner) {
        vec3 accent = mix(k.pA, k.pB, h);
        float wsum = wv.x * 2.0 + wv.y;
        if (h < 0.40) {
            vec2 b = vec2(0.8 + 0.5 * h2, 0.8 + 0.5 * fract(h2 * 3.3));
            // fan-out along x from the chip's x-facing pins to the avenue bus
            float ex = abs(l.x) - b.x;
            if (ex > 0.0) {
                float t = saturate(ex / (inner - b.x));
                float squeeze = mix(1.0, 0.45, smoothstep(0.15, 0.7, t));
                float zz = l.y / squeeze;
                float fz = fp.y / (0.14 * squeeze);
                float inside = step(abs(zz), b.y - 0.08);
                float line = ciLines(zz / 0.14 + 0.5, 0.16, fz) * inside;
                float pad = step(ex, 0.18) * ciLines(zz / 0.14 + 0.5, 0.3, fz) * inside;
                float lane = floor(zz / 0.14);
                float flow = exp(-fract(t * 1.5 - tm * k.pulse * 0.1 + hash21(vec2(lane, cell.x + cell.y * 7.0))) * 7.0);
                col += accent * (line * (0.08 + 1.4 * wsum + 0.9 * flow * (0.3 + ciLv(k, fract(lane * 0.37 + 0.5)))) + pad * 0.15);
                col += wcl * pad * wv.x * 2.0;
                copper += max(line, pad);
            }
            float ez = abs(l.y) - b.y;
            if (ez > 0.0 && ez < 0.18) {
                float pad = ciLines(l.x / 0.14 + 0.5, 0.3, fp.x / 0.14) * step(abs(l.x), b.x - 0.08);
                col += (accent * 0.12 + wcl * wv.x * 2.0) * pad;
                copper += pad;
            }
            // silkscreen outline
            vec2 o = abs(l) - b - 0.32;
            col += vec3(0.06) * ciLine(max(o.x, o.y), 0.012, fp.y);
        } else {
            // via field
            vec2 vc = floor(l / 0.25);
            vec2 vq = l - (vc + 0.5) * 0.25;
            float vh = hash21(vc + cell * 3.3);
            float ring = ciLine(length(vq) - 0.055, 0.018, fp.y) * step(0.82, vh) * (1.0 - smoothstep(0.03, 0.08, fp.y));
            col += ring * (accent * (0.2 + wsum * 1.8 + 0.8 * ciLv(k, vh)) + wcl * wv.x * 2.0);
            copper += ring;
        }
    }
    // the molten front itself sweeps across the solder mask
    col += wcl * wv.x * 0.22;
    // faint copper-pour hatch fills the gaps
    float hatch = ciLines((xz.x + xz.y) / 0.18, 0.08, (fp.x + fp.y) / 0.18);
    col += k.glow * hatch * 0.01 * detail;
    return col;
}

// Night sky over the board: a near-black zenith, a palette haze on the
// horizon over a thin seam of the current wave colour.
vec3 ciSky(vec3 rd, CiK k) {
    float h = rd.y;
    float ah = abs(h);
    vec3 col = vec3(0.0006, 0.0008, 0.002) + k.glow * 0.03 * exp(-ah * 6.0);
    col += k.top * 0.025 * smoothstep(0.0, 0.5, h) * exp(-h * 2.0);
    col += k.bass * exp(-ah * 26.0) * (0.22 + 0.25 * k.bassE);
    col += mix(k.cool, k.hot, 0.5) * exp(-ah * 110.0) * (0.08 + 0.25 * k.bassE);
    return col;
}

// Uplink pillars: in some 4x4 super-blocks one block sends a column of light
// straight up. Each pillar sits on a block centre clear of the flight line.
const float CI_P = 16.0;
vec4 ciPil(vec2 c) {
    float hp = hash21(c * 0.71 + 2.3);
    vec2 bi = floor(vec2(hash21(c + 9.1), hash21(c + 4.7)) * 3.999);
    vec2 pp = c * CI_P + (bi + 0.5) * CI_C;
    float on = step(hp, 0.55) * step(3.0, abs(pp.x - ciPath(pp.y)));
    return vec4(pp, on, hp);
}

// Colour and strength of a pillar: copper or ice by its hash, metered by a
// band, with the beat's accent colour on top.
vec4 ciPilLight(float hp, CiK k) {
    float lv = ciLv(k, fract(hp * 5.3));
    vec3 c = fract(hp * 7.7) < 0.4 ? k.hot : mix(k.tA, k.cool, fract(hp * 3.1));
    vec3 acc = mix(k.hot, k.cool, k.par);
    return vec4(c * (0.35 + 1.2 * lv) + acc * k.beat * 0.0, lv);
}

// Glow of the pillars along the ray up to tEnd, walking the super-block grid.
// The ray-to-column distance is horizontal, and the core widens to the
// pixel footprint so far pillars stay thin without shimmering.
vec3 ciPillars(vec3 ro, vec3 rd, float tEnd, CiK k, float pixk, int n) {
    vec2 d = rd.xz;
    float a = dot(d, d);
    if (a < 1e-6) return vec3(0.0);
    vec2 s = vec2(d.x >= 0.0 ? 1.0 : -1.0, d.y >= 0.0 ? 1.0 : -1.0);
    vec2 c = floor(ro.xz / CI_P);
    vec2 ad = max(abs(d), vec2(1e-4));
    vec2 tN = ((c + max(s, 0.0)) * CI_P - ro.xz) * s / ad;
    vec2 tD = CI_P / ad;
    vec3 col = vec3(0.0);
    for (int i = 0; i < 12; i++) {
        if (i >= n) break;
        vec4 pl = ciPil(c);
        if (pl.z > 0.5) {
            vec2 q = ro.xz - pl.xy;
            float tc = clamp(-dot(q, d) / a, 0.0, tEnd);
            vec2 w = q + d * tc;
            float r2 = dot(w, w);
            float y = ro.y + rd.y * tc;
            float fw = 0.012 + (tc * pixk) * (tc * pixk);
            float core = 0.012 / (r2 + fw) + 0.25 * exp(-r2 * 1.2);
            // data bursts climb the column
            float ph = fract(y * 0.05 - k.tm * (0.08 + 0.06 * pl.w) + pl.w * 7.0);
            float pk = exp(-ph * 4.0) * 0.4;
            float hf = exp(-max(y - 0.3, 0.0) * 0.1) * smoothstep(0.1, 0.5, y) * smoothstep(2.0, 1.2, sqrt(r2));
            vec4 lt = ciPilLight(pl.w, k);
            col += lt.rgb * core * hf * (1.0 + pk * (0.4 + lt.w)) * exp(-tc * 0.014) * smoothstep(2.0, 8.0, tc);
        }
        float tn = min(tN.x, tN.y);
        if (tn > tEnd) break;
        if (tN.x < tN.y) { tN.x += tD.x; c.x += s.x; } else { tN.y += tD.y; c.y += s.y; }
    }
    return col;
}

// Light the nearest pillar throws on the board and the parts around it.
vec3 ciPool(vec3 pos, CiK k) {
    vec4 pl = ciPil(floor(pos.xz / CI_P));
    vec2 q = pos.xz - pl.xy;
    return ciPilLight(pl.w, k).rgb * pl.z * exp(-dot(q, q) * 0.3) * 0.5 * exp(-max(pos.y, 0.0) * 0.4);
}

// Light a component surface: key light, underglow from the traces, emissive
// crowns and windows. fpw is the pixel footprint in world units.
vec3 ciShade(vec3 pos, vec3 n, vec3 rd, vec2 src, CiK k, float bright, float fpw) {
    vec2 cell = floor(pos.xz / CI_C);
    vec2 l = pos.xz - (cell + 0.5) * CI_C;
    float h = hash21(cell + 0.37);
    float h2 = hash21(cell * 1.7 + 5.1);
    vec3 L = vec3(-0.44667, 0.49629, 0.74444);
    float dif = saturate(dot(n, L));
    vec3 R = reflect(rd, n);
    float spec = pow(saturate(dot(R, L)), 48.0);
    float fres = 0.04 + 0.96 * pow(1.0 - saturate(dot(n, -rd)), 5.0);
    float top = step(0.5, n.y);

    // black epoxy by default; metal tints its highlights
    vec3 alb = vec3(0.008);
    vec3 tint = vec3(1.0);
    float gloss = 0.9;
    vec3 em = vec3(0.0);
    vec3 accent = mix(k.pA, k.pB, h);
    float lvl = ciLv(k, fract(h * 3.7 + h2));
    if (h < 0.40) {
        // chip top: glowing inset groove, a status light, a grid of lit windows
        vec2 b = vec2(0.8 + 0.5 * h2, 0.8 + 0.5 * fract(h2 * 3.3));
        vec2 o = abs(l) - b + 0.16;
        float groove = ciLine(max(o.x, o.y), 0.025, max(0.01, fpw)) * top;
        em += accent * groove * (0.8 + 4.0 * lvl) * bright;
        float dot0 = length(l - (b - 0.3) * vec2(-1.0, 1.0));
        em += k.tre * top * exp(-dot0 * dot0 * 600.0) * (2.0 + 6.0 * k.beat);
        if (top > 0.5 && max(o.x, o.y) < -0.12) {
            vec2 g = l / 0.13;
            vec2 wc = floor(g);
            vec2 wq = g - wc - 0.5;
            float wh = hash21(wc + cell * 7.3);
            // each window cross-fades to a new state, so none of them pops
            float st = k.tm * (0.05 + 0.08 * wh) + wh * 9.0;
            float a0 = hash21(wc + floor(st) * 1.31 + cell);
            float a1 = hash21(wc + (floor(st) + 1.0) * 1.31 + cell);
            float on = smoothstep(0.5, 0.8, mix(a0, a1, smoothstep(0.0, 1.0, fract(st))));
            float fq = fpw / 0.13;
            float cov = 1.0 - smoothstep(0.3 - fq, 0.3 + fq, max(abs(wq.x), abs(wq.y)));
            cov = mix(cov, 0.36, smoothstep(0.25, 0.6, fq));
            vec3 wcol = fract(wh * 11.0) < 0.3 ? k.hot : mix(k.tre, accent, fract(wh * 5.0));
            em += wcol * on * cov * (0.2 + 1.8 * ciLv(k, fract(wh * 3.3))) * bright;
        }
        // pin row on the sides
        float pins = (1.0 - top) * step(pos.y, 0.07) * ciLines((abs(n.x) > 0.5 ? l.y : l.x) / 0.14 + 0.5, 0.2, max(0.05, fpw / 0.14));
        alb = mix(alb, vec3(0.6), pins);
    } else if (h < 0.60) {
        // capacitor: tinted sleeve, metal top with a lit rim that meters its band
        vec2 s = vec2(l.x < 0.0 ? -1.0 : 1.0, l.y < 0.0 ? -1.0 : 1.0);
        float ht = 0.6 + 1.3 * hash21(s + h2 * 11.0);
        float q = hash21(s + cell);
        // deep lacquered sleeve in the palette, brushed aluminium top
        alb = mix(k.albA, k.albB, q) * 0.11;
        float r = length(l - s * 0.78);
        // the top's pressure-vent cross is scored into the aluminium
        vec2 cq = abs(l - s * 0.78);
        float score = ciLine(min(cq.x, cq.y), 0.014, max(0.01, fpw)) * step(r, 0.4);
        alb = mix(alb, vec3(0.07, 0.075, 0.085) * (1.0 - 0.8 * score), top);
        tint = mix(vec3(1.0), vec3(0.8, 0.88, 1.0), top);
        float lv = ciLv(k, q);
        em += accent * top * ciLine(r - 0.47, 0.035, max(0.01, fpw)) * (1.0 + 5.0 * lv) * bright;
        em += accent * (1.0 - top) * exp(-abs(pos.y - ht + 0.1) * 40.0) * (0.3 + 2.5 * lv) * bright;
        gloss = 1.0;
    } else if (h < 0.80) {
        // heat sink: bare-copper or gunmetal fins with a lit crown that
        // brightens on the beat
        float ht = 1.1 + 1.5 * h2;
        bool cu = fract(h2 * 5.3) < 0.5;
        alb = cu ? k.hot * vec3(0.05, 0.035, 0.03) : vec3(0.012, 0.013, 0.016);
        tint = cu ? vec3(1.0, 0.55, 0.3) : vec3(0.75, 0.8, 1.0);
        // distant fins fold into one face, so their facets do not shimmer
        float far = saturate(fpw * 12.0);
        gloss = 0.8 * (1.0 - 0.6 * far);
        float crown = exp(-max(ht - pos.y, 0.0) * 28.0) * step(0.3, pos.y) * (1.0 - top * 0.93);
        em += (cu ? k.hot : accent) * crown * (0.4 + 1.2 * k.bassE + 2.0 * lvl) * bright;
    } else {
        alb = vec3(0.03);
    }
    // the beat wave flares a block's lights as it passes
    vec3 wc = ciWave((cell + 0.5) * CI_C, src, k);
    em *= 1.0 + 1.2 * wc.x + 1.5 * wc.y;
    // and the wave front rakes the towers' flanks from below
    vec3 wp = ciWave(pos.xz, src, k);
    vec3 wcol = ciWaveCol(wp, k);
    float side = 1.0 - abs(n.y);
    em += wcol * wp.x * side * exp(-pos.y * 1.2) * 0.9 * bright;
    // glossy reflection: the horizon haze above, the lit board below
    vec3 env = R.y > 0.0 ? k.glow * 0.22 * exp(-R.y * 5.0) + k.bass * 0.3 * exp(-R.y * 22.0)
                         : (k.tA * (0.05 + 0.12 * k.bA.y) + wcol * wp.x * 0.5) * exp(R.y * 2.0);
    vec3 under = mix(k.tA, k.hot, 0.35) * exp(-pos.y * 3.0) * side * 0.06 * bright;
    vec3 pool = ciPool(pos, k) * (0.4 + side);
    vec3 col = alb * (k.key * dif * 4.0 + 0.02) + k.key * tint * spec * gloss * 2.5
             + env * tint * fres * gloss + under + pool * (alb * 20.0 + 0.1) + em;
    return col;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float bright = mix(0.5, 1.7, u.param2);
    CiK k = ciKMake(u);

    float speed = mix(3.5, 9.5, u.param1);
    // a slow steady glide: no beat-timed surge or brake
    float z = u.time * speed * 0.05;
    float rush = 0.0;
    float lull = 0.0;
    float alt = mix(3.4, 6.0, u.param0) + 0.4 * sin(u.time * 0.21);
    vec3 ro = vec3(ciPath(z), alt, z);
    vec3 ta = vec3(ciPath(z + 8.0), -1.4, z + 8.0 + alt * 0.6);
    float curve = ciPath(z + 12.0) - 2.0 * ciPath(z + 6.0) + ciPath(z);
    float roll = -0.06 * curve + 0.05 * sin(u.time * 0.05);
    float fov = 1.3 - 0.16 * rush + 0.08 * lull;
    vec3 rd = osCamera(ro, ta, roll, fov, p);

    vec2 src = vec2(ciPath(z + 6.0), z + 6.0);
    vec3 fogCol = k.glow * 0.025;
    vec3 sky = ciSky(rd, k);
    float tmax = 110.0;
    float pixk = 2.0 / (max(u.resolution.y, 1.0) * fov);
    vec3 col;
    float t;
    // the board plane is the floor: march the component slab, else hit y = 0
    float tb = rd.y < -1e-3 ? ro.y / -rd.y : 1e4;
    float tc = ciMarch(ro, rd, min(tb, tmax), osSteps(32.0, 90.0, detail));
    t = tc > 0.0 ? tc : tb;
    if (t > tmax) {
        col = sky;
        t = tmax;
    } else {
        vec3 pos = ro + rd * t;
        if (tc < 0.0) {
            float copper;
            float pix = t * pixk;
            vec2 fp = vec2(pix, pix / max(-rd.y, 0.05));
            vec3 em = ciBoard(pos.xz, src, fp, k, detail, copper) * bright;
            // glossy solder mask: mirror the towers and the sky
            vec3 rr = vec3(rd.x, -rd.y, rd.z);
            float fres = 0.06 + 0.94 * pow(1.0 - saturate(-rd.y), 5.0);
            float t2 = ciMarch(pos + vec3(0.0, 0.003, 0.0), rr, 14.0, osSteps(8.0, 28.0, detail));
            vec3 refl;
            if (t2 < 0.0) refl = ciSky(rr, k);
            else {
                vec3 p2 = pos + rr * t2;
                refl = ciShade(p2, ciNormal(p2), rr, src, k, bright, (t + t2) * pixk);
            }
            col = k.mask + em + refl * mix(0.08 + fres * 0.6, 0.4 + 0.5 * fres, saturate(copper))
                + ciPool(pos, k) * (0.25 + 1.5 * saturate(copper));
        } else {
            col = ciShade(pos, ciNormal(pos), rd, src, k, bright, t * pixk);
        }
    }
    float fog = 1.0 - exp(-t * 0.016);
    col = mix(col, fogCol + sky, fog);
    col += k.bass * fogCol * 2.0 * k.bassE * fog;
    col += ciPillars(ro, rd, t, k, pixk, osSteps(5.0, 12.0, detail)) * bright;
    return ciOffRed(max(col, vec3(0.0))); // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
