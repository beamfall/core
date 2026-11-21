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
// Crystal.
// A macro product shot: a large brilliant-cut gem floats over black lacquer
// in a dark studio. An overhead softbox, a ring light and two equaliser strip
// lights (the spectrum) surround it; the gem bends them through several
// internal bounces with dispersion at every exit, so its fire changes as it
// turns. Below, the light it focuses lands as spectral caustic streaks, and
// defocused studio lights hang in the dark behind. The camera circles the
// stone in a slow, steady orbit with a gentle dolly, and a laser's spectrum
// fan drifts round it about once a minute. Audio drives light only: the
// level, shaped by the spectrum, lights the strips and the fan rays; the
// level with only a faint swell on each beat lights the ring, the gem's heart
// and the caustics, and the level softly lifts the glints and inner fractures. The gem is
// an exact convex polytope (no marching), so facets stay crisp at every
// detail level.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Facet Count ; param1 = Dispersion ; param2 = Fracture

// The palette read: the visual's own palette (13) with no beat or spectral
// nudge and no palette tour, so the host's vivid tour never swells the light;
// a host's manifest palette still replaces it. The fog tint is rebuilt on it.
vec3 crPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 13, u); }
vec3 crPalSteadyFog(float t, VisualUniforms u) {
    vec3 c = crPalSteady(t, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}

const float CR_FLOOR = -1.25;

// Ray against the convex gem: returns (tEnter, tExit), the entry normal and
// the exit normal. Rings of planes: table, crown mains, crown stars, girdle,
// pavilion mains and pavilion halves, each built by rotating its first normal
// around y.
vec2 crHit(vec3 ro, vec3 rd, int N, vec2 rot, vec2 half_, out vec3 nEnter, out vec3 nExit) {
    float tin = -1e4;
    float tout = 1e4;
    nEnter = vec3(0.0, 1.0, 0.0);
    nExit = vec3(0.0, 1.0, 0.0);
    for (int k = 0; k < 6; k++) {
        // ring: (sin tilt, cos tilt with sign, plane offset, half-step flag)
        vec4 rg = k == 0 ? vec4(0.0, 1.0, 0.36, 0.0)          // table
                : k == 1 ? vec4(0.566, 0.824, 0.607, 0.0)      // crown mains, 34.5 deg
                : k == 2 ? vec4(0.669, 0.743, 0.690, 1.0)      // crown stars, 42 deg
                : k == 3 ? vec4(1.0, 0.0, 0.985, 1.0)          // girdle
                : k == 4 ? vec4(0.653, -0.757, 0.676, 0.0)     // pavilion mains, 40.75 deg
                :          vec4(0.690, -0.724, 0.695, 1.0);    // pavilion halves
        vec2 az = rg.w > 0.5 ? half_ : vec2(1.0, 0.0);
        int cnt = k == 0 ? 1 : N;
        for (int i = 0; i < 12; i++) {
            if (i >= cnt) break;
            vec3 n = vec3(rg.x * az.x, rg.y, rg.x * az.y);
            float den = dot(rd, n);
            float num = rg.z - dot(ro, n);
            float t = num / (abs(den) < 1e-6 ? (den < 0.0 ? -1e-6 : 1e-6) : den);
            if (den < 0.0) { if (t > tin) { tin = t; nEnter = n; } }
            else { if (t < tout) { tout = t; nExit = n; } }
            az = vec2(az.x * rot.x - az.y * rot.y, az.x * rot.y + az.y * rot.x);
        }
    }
    return vec2(tin, tout);
}

// Cheap bounding-sphere test (radius 1.25 around the gem's centre), so only
// rays near the stone pay for the polytope.
bool crNear(vec3 ro, vec3 rd) {
    float b = dot(ro, rd);
    float c = dot(ro, ro) - 1.5625;
    return b < 0.0 && b * b - c > 0.0;
}

// Rectangle mask in a light's projected plane, edge softness s.
float crRect(vec2 q, vec2 b, float s) {
    vec2 e = abs(q) - b;
    return 1.0 - smoothstep(-0.006 - s, 0.006 + s, max(e.x, e.y));
}

// The palette colour's complement at the same lightness: a warm accent for a
// cool palette (and the reverse), so the studio never sits in one hue.
vec3 crCompl(vec3 c) {
    float mx = max(c.r, max(c.g, c.b));
    float mn = min(c.r, min(c.g, c.b));
    return vec3(mx + mn) - c;
}

// The palette read once per pixel at fixed stops: the gem samples the studio
// up to eight times per pixel, and each palette read at a call site costs.
struct CrPal { vec3 ring; vec3 spot; vec3 key; vec3 cool; vec3 heart; vec3 warm; vec3 w05; vec3 s0; vec3 s1; vec3 b0; vec3 b1; vec3 sweep; float beam; };

// The light pulse: the level, with a beat that swells in and out within the
// beat (zero at both ends), so the studio breathes with the music instead of
// jumping on each kick.
float crPulse(VisualUniforms u) {
    float b = sin(3.14159265 * saturate(u.beatPhase));
    return saturate(u.level) * (0.25 + 0.03 * b * b);
}

// A band of the spectrum, mostly the steady level: the spectrum still shapes
// the strips, streaks and rays, but a kick only nudges them.
float crBand(VisualUniforms u, float x) {
    return mix(saturate(u.level), bandAt(u, x), 0.15);
}

// The laser that feeds the stone, from high on one side of the studio.
const vec3 CR_BEAM_FROM = vec3(-0.7365, 0.6088, 0.2946);

// Studio lights shared by every view: the key softbox, hard spots that become
// the fire, and the ring light. soft widens the edges (defocus, gloss).
vec3 crLights(vec3 d, VisualUniforms u, float soft, vec3 ringL, CrPal P, float spots, vec3 sweepL) {
    vec3 col = vec3(0.0);
    vec3 warm = P.warm;
    // overhead key softbox, with a darker cross bar like a real light box
    {
        vec3 L = vec3(0.234, 0.936, -0.262);
        float dl = dot(d, L);
        if (dl > 0.2) {
            vec2 q = vec2(dot(d, vec3(0.97, -0.243, 0.0)), dot(d, vec3(0.063, 0.254, 0.965))) / dl;
            float m = crRect(q, vec2(0.5, 0.11), soft);
            m *= 1.0 - 0.7 * (1.0 - smoothstep(0.0, 0.015 + soft, abs(q.x)));
            col += vec3(2.4, 2.35, 2.3) * m;
        }
    }
    // small hard spots, white and warm/cool: these become the fire
    for (int k = 0; k < 6; k++) {
        if (spots < 0.5) break;
        float fk = float(k);
        float az = fk * 1.047 + 0.4;
        float el = 0.15 + 0.5 * fract(fk * 0.618);
        vec3 L = vec3(sin(az) * cos(el), sin(el), cos(az) * cos(el));
        float x = max(1.0 - dot(d, L), 0.0);
        vec3 sc = k == 1 || k == 4 ? warm * 1.4 : k == 2 ? P.spot : vec3(1.0);
        col += sc * exp(-x * (900.0 - 700.0 * saturate(soft * 4.0))) * 4.0;
    }
    // the ring light: a crisp, hot tube with a tight halo that swells with the beat
    {
        float dl = dot(d, ringL);
        vec3 rc = P.ring;
        float pump = 0.8 + 0.9 * crPulse(u);
        if (dl > 0.3) {
            vec3 ax = normalize(vec3(ringL.z, 0.0, -ringL.x));
            vec2 q = vec2(dot(d, ax), dot(d, cross(ringL, ax))) / dl;
            float e = abs(length(q) - 0.42);
            float tube = 1.0 - smoothstep(0.004, 0.009 + soft * 0.4, e);
            col += rc * tube * 4.0 * pump;
            col += rc * (exp(-e * 40.0) * 0.25 + exp(-e * 8.0) * 0.05) * pump;
        }
    }
    // the sweep light: a tall strip that laps the stone slowly and steadily,
    // alternating warm and cool, so the fire rolls through the facets;
    // and the laser's source, a hard white point. The stone alone sees both.
    if (spots > 0.5) {
        float xb = max(1.0 - dot(d, CR_BEAM_FROM), 0.0);
        col += vec3(1.0, 0.97, 0.94) * exp(-xb * 1400.0) * 9.0 * P.beam;
        float dl = dot(d, sweepL);
        if (dl > 0.5) {
            vec3 ax = normalize(vec3(sweepL.z, 0.0, -sweepL.x));
            vec2 q = vec2(dot(d, ax), d.y) / dl;
            float m = crRect(q - vec2(0.0, 0.25), vec2(0.035, 0.7), soft);
            col += P.sweep * m * 3.2;
        }
    }
    return col;
}

// The full studio, for the background and the lacquer's reflection: a dark
// cyclorama split warm and cool, the ring light's wash, two equaliser strips
// (the spectrum) and the lights above.
vec3 crEnv(vec3 d, VisualUniforms u, float soft, vec4 lvA, vec4 lvB, vec3 ringL, CrPal P, vec3 sweepL) {
    vec3 cool = P.cool;
    vec3 warm = P.warm;
    float sideW = dot(d.xz, vec2(ringL.z, -ringL.x));          // left/right of the ring
    vec3 wall = mix(cool * 0.012, warm * 0.016, smoothstep(-0.6, 0.6, sideW));
    vec3 col = wall * smoothstep(-0.2, 0.5, d.y) + vec3(0.0008, 0.0008, 0.0012);
    // the ring's backdrop wash and the horizon sweep of the cyclorama
    float dl = dot(d, ringL);
    col += mix(P.ring, vec3(0.6), 0.2) * 0.05 * exp(-max(1.0 - dl, 0.0) * 5.0) * (0.8 + 0.6 * crPulse(u));
    col += mix(cool, warm, smoothstep(-0.5, 0.5, sideW)) * 0.025 * exp(-abs(d.y - 0.02) * 9.0);
    col += crLights(d, u, soft, ringL, P, 0.0, sweepL);
    // two vertical equaliser strips in 16 segments: warm on the left, cool on the right
    for (int s = 0; s < 2; s++) {
        float az = s == 0 ? 1.75 : -2.25;
        vec3 L = vec3(sin(az), 0.0, cos(az));
        float sl = dot(d, L);
        if (sl > 0.2) {
            vec2 q = vec2(dot(d, vec3(L.z, 0.0, -L.x)), d.y) / sl;
            float m = crRect(q - vec2(0.0, 0.05), vec2(0.022, 0.95), soft);
            if (m > 0.0) {
                float hgt = saturate(q.y * 0.5 + 0.475) * 16.0;
                float gap = mix(smoothstep(0.06, 0.16, abs(fract(hgt) - 0.5)), 1.0, saturate(soft * 6.0));
                vec4 lv = s == 0 ? lvA : lvB;
                float f = hgt / 4.0;
                float lvl = f < 1.0 ? lv.x : f < 2.0 ? lv.y : f < 3.0 ? lv.z : lv.w;
                float lit = smoothstep(0.0, 0.08, lvl * 1.3 - fract(f));
                vec3 sc = s == 0 ? mix(warm, P.w05 * 0.6 + warm * 0.6, fract(hgt / 16.0))
                                 : mix(P.s0, P.s1, floor(hgt) / 15.0);
                col += sc * m * gap * (0.08 + 2.2 * lit * lvl + 0.3 * lvl);
            }
        }
    }
    // the floor seen from inside the gem: dark with a coloured bounce
    col = d.y < -0.05 ? mix(col, cool * 0.04 * (0.5 + crPulse(u)), smoothstep(-0.05, -0.4, d.y)) : col;
    return col;
}

// What the stone sees: the studio's lights and the light tent around it,
// near-white louvred panels with black gaps, which gives the facets their
// black-and-white brilliance; dispersion turns every edge into fire. It skips
// the strips' segments and the wall detail, which the facets would scramble.
vec3 crGemEnv(vec3 d, VisualUniforms u, vec3 ringL, CrPal P, vec4 lvA, vec3 sweepL) {
    vec3 warm = P.warm;
    vec3 col = vec3(0.001) + crLights(d, u, 0.0, ringL, P, 1.0, sweepL);
    float az = atan2_(d.z, d.x) * 1.9098593;               // 12 panels around
    float fa = fract(az);
    float pan = smoothstep(0.30, 0.24, abs(fa - 0.5));
    float band = smoothstep(0.12, 0.2, d.y) * (1.0 - smoothstep(0.75, 0.8, d.y));
    float hi = smoothstep(0.92, 0.95, d.y);                 // the open top
    float pk = floor(az);
    // one panel in six carries the cool key colour; the rest are clean white
    vec3 pc = mod(pk, 6.0) < 0.5 ? P.key : vec3(1.0);
    col += pc * (pan * band * 1.5 + hi * 1.1) * (0.7 + 0.5 * u.level);
    // the warm strip low around the stone
    col += warm * smoothstep(0.08, 0.02, abs(d.y + 0.02)) * (0.05 + 2.5 * lvA.x * lvA.x);
    return col;
}

// Fractures: three flat sheets inside the stone, each a straight-edged shard
// (a triangle in its plane). The ray meets each sheet analytically, so edges
// stay crisp: an iridescent thin-film sheen across the sheet and a hot line
// along its edge that flares on onsets.
vec3 crCracks(vec3 pos, vec3 dir, float seg, VisualUniforms u, float fracture) {
    vec3 col = vec3(0.0);
    for (int k = 0; k < 3; k++) {
        vec3 n = k == 0 ? vec3(0.6, 0.48, -0.64) : k == 1 ? vec3(-0.3, 0.906, 0.3) : vec3(0.813, -0.2, 0.547);
        vec3 c = k == 0 ? vec3(0.12, 0.05, -0.1) : k == 1 ? vec3(-0.15, -0.05, 0.12) : vec3(0.18, -0.15, 0.2);
        float dn = dot(dir, n);
        if (abs(dn) < 0.02) continue;
        float t = dot(c - pos, n) / dn;
        if (t <= 0.0 || t >= seg) continue;
        vec3 q = pos + dir * t - c;
        vec3 e1 = normalize(cross(n, vec3(0.0, 0.0, 1.0) + 0.3 * float(k)));
        vec3 e2 = cross(n, e1);
        vec2 w = vec2(dot(q, e1), dot(q, e2));
        // triangle distance: max of three edge half-planes
        float R = (0.22 + 0.2 * fracture) * (1.0 + 0.3 * float(k));
        float tri = max(max(dot(w, vec2(0.0, 1.0)), dot(w, vec2(0.866, -0.5))), dot(w, vec2(-0.866, -0.5))) - R * 0.5;
        if (tri > 0.03) continue;
        float inside = 1.0 - smoothstep(-0.004, 0.0, tri);
        float edge = exp(-abs(tri) * 260.0);
        // a straight inner hairline from the shard's centre
        float hair = exp(-abs(dot(w, vec2(0.6, 0.8))) * 400.0) * inside * smoothstep(0.0, -0.08, tri);
        vec3 film = hsv2rgb(vec3(fract(length(w) * 2.5 + float(k) * 0.3 + 0.1 * u.spectralCentroid), 0.7, 1.0));
        float view = 1.0 / (0.3 + abs(dn));
        col += film * inside * 0.05 * view + peakWhite(film, 0.5) * (edge + hair) * (0.08 + 0.1 * saturate(u.level));
    }
    return col * fracture;
}

// Full gem shading for a ray in gem space that hits at t with normal n.
vec3 crShadeGem(vec3 ro, vec3 rd, float t, vec3 n, mat3 toWorld, VisualUniforms u,
                int N, vec2 rot, vec2 half_, int bounces, float disp, vec3 tint,
                float fracture, vec3 ringL, CrPal P, vec4 lvA, vec3 sweepL) {
    vec3 pos = ro + rd * t;
    float c = saturate(-dot(rd, n));
    float F = 0.17 + 0.83 * pow(1.0 - c, 5.0);
    vec3 col = crGemEnv(toWorld * reflect(rd, n), u, ringL, P, lvA, sweepL) * F;
    vec3 dir = refract(rd, n, 1.0 / 2.42);
    vec3 thr = vec3(1.0 - F);
    for (int b = 0; b < 4; b++) {
        if (b >= bounces) break;
        vec3 nE, nX;
        vec2 h = crHit(pos, dir, N, rot, half_, nE, nX);
        float seg = clamp(h.y, 1e-3, 3.0);
        // inner glow: the level, swelling on the beat, lights the heart
        vec3 mid = pos + dir * (0.5 * seg);
        vec3 em = P.heart * exp(-dot(mid, mid) * 9.0) * (0.004 + 0.16 * crPulse(u)) * seg;
        if (b == 0 && fracture > 0.001) em += crCracks(pos, dir, seg, u, fracture);
        col += thr * em;
        pos += dir * seg;
        thr *= exp(-seg * (vec3(1.0) - tint) * 0.8);
        // leave through the face, split into wavelengths: red, green and blue
        // for the first exit, the two ends for the second, one ray after
        vec3 r0 = refract(dir, -nX, 2.42 - disp);
        if (dot(r0, r0) > 0.5) {
            float fr = 0.17 + 0.83 * pow(1.0 - saturate(dot(dir, nX)), 5.0);
            vec3 outc;
            if (b < 2) {
                vec3 r2 = refract(dir, -nX, 2.42 + disp);
                r2 = dot(r2, r2) < 0.5 ? r0 : r2;
                vec3 e0 = crGemEnv(toWorld * r0, u, ringL, P, lvA, sweepL);
                vec3 e2 = crGemEnv(toWorld * r2, u, ringL, P, lvA, sweepL);
                float g = 0.5 * (e0.g + e2.g);
                if (b == 0) {
                    vec3 r1 = refract(dir, -nX, 2.42);
                    r1 = dot(r1, r1) < 0.5 ? r0 : r1;
                    g = crGemEnv(toWorld * r1, u, ringL, P, lvA, sweepL).g;
                }
                outc = vec3(e0.r, g, e2.b);
            } else {
                outc = crGemEnv(toWorld * r0, u, ringL, P, lvA, sweepL);
            }
            col += thr * (1.0 - fr) * outc;
            thr *= fr;
        }
        dir = reflect(dir, nX);
        pos += dir * 1e-3;
    }
    // what is left after the last bounce leaves as a soft tinted glow
    col += thr * tint * crGemEnv(toWorld * dir, u, ringL, P, lvA, sweepL) * 0.6;
    return col;
}

// Spectral caustic streaks the gem throws on the floor: each facet throws a
// dashed streak whose width fans out into a rainbow with distance, as
// dispersed light does; under the stone, a focused star of light.
vec3 crCaustic(vec2 q, float yaw, float N, float disp, vec3 tint, float T, VisualUniforms u) {
    float r = length(q);
    float a = atan2_(q.y, q.x) + yaw;
    vec3 c = vec3(0.0);
    for (int l = 0; l < 2; l++) {
        float fl = float(l);
        float N2 = N * (2.0 + fl);
        float A = a * N2 / 6.2831853 + fl * 0.37;
        float id = floor(A);
        float hs = hash11(mod(id, N2) * 3.7 + 1.0 + fl * 9.0);
        float x = fract(A) - 0.5 + 0.2 * (hs - 0.5);
        float wdt = (0.025 + 0.45 * disp * r) * (0.6 + 0.6 * hs);
        float prof = exp(-x * x / (wdt * wdt));
        vec3 spec = hsv2rgb(vec3(fract(0.62 + 0.45 * x / max(wdt, 1e-3)), 0.9, 1.0));
        float dash = smoothstep(0.35, 0.75, vnoise(vec2(id * 7.3 + fl * 31.0, r * 2.4 - T * 0.12)));
        // each streak is one band of an equaliser fanning out from the stone
        float lv = crBand(u, fract(id / N2 + fl * 0.5));
        float reach = smoothstep(0.25, 0.55, r) * exp(-r * (0.45 + 0.7 * hs)) * smoothstep(0.0, 0.4, 1.3 + 4.5 * lv - r);
        c += spec * prof * dash * reach * (0.5 + 1.1 * hs + 1.2 * lv) / (1.0 + fl);
    }
    // the focused star right under the stone: N sharp spikes and a hot ring
    float spike = pow(abs(cos(a * N * 0.5)), 24.0) * exp(-r * 4.0) * smoothstep(0.02, 0.15, r);
    float e = (r - 0.42) / 0.025;
    vec3 ringC = hsv2rgb(vec3(fract(0.55 + (r - 0.42) * 3.0), 0.6, 1.0));
    c += tint * spike * 0.8 + ringC * exp(-e * e) * 0.35 + tint * exp(-r * r * 30.0) * 0.4;
    return c * mix(vec3(1.0), tint, 0.3) * (0.6 + 1.6 * crPulse(u));
}

// Defocused studio lights hanging in the dark behind the gem: lens bokeh
// with a bright rim and a soft core, alternating warm and cool.
vec3 crBokeh(vec2 p, float orbit, VisualUniforms u, CrPal P) {
    vec3 warm = P.warm;
    vec3 col = vec3(0.0);
    for (int l = 0; l < 2; l++) {
        float fl = float(l);
        float sc = 2.0 + 1.8 * fl;
        vec2 q = p * sc + vec2(orbit * (3.0 + 2.0 * fl), 0.3 * fl);
        vec2 g = floor(q);
        vec2 f = fract(q) - 0.5;
        vec2 h = hash22(g + fl * 17.0);
        float on = step(0.5, hash21(g * 1.7 + fl * 3.0));
        float rad = 0.12 + 0.18 * h.x;
        float d = length(f - (h - 0.5) * 0.35) - rad;
        float disc = 1.0 - smoothstep(-0.012, 0.012, d);
        float rim = exp(-abs(d + 0.012) * 60.0) * disc;
        float lvl = crBand(u, h.y);
        vec3 bc = h.y < 0.5 ? warm : mix(P.b0, P.b1, h.x);
        col += bc * (disc * 0.5 + rim * 0.8) * on * (0.05 + 0.3 * lvl) / (1.0 + 1.5 * fl);
    }
    return col;
}

// A beam through the studio haze: the view ray's closest approach to the beam
// c + s b (s in [s0, L], b unit), seen only in front of tMax. A hot core and a
// wide scattering halo, fading along the beam.
vec3 crBeam(vec3 ro, vec3 rd, vec3 c, vec3 b, float s0, float L, float tMax, vec3 colr) {
    vec3 w0 = ro - c;
    float bb = dot(rd, b);
    float dd = dot(rd, w0), e = dot(b, w0);
    float den = max(1.0 - bb * bb, 1e-4);
    float s = clamp((e - bb * dd) / den, s0, L);
    vec3 cp = c + b * s;
    float t = max(dot(cp - ro, rd), 0.0);
    if (t > tMax) return vec3(0.0);
    vec3 dv = ro + rd * t - cp;
    float d2 = dot(dv, dv);
    float fade = exp(-(s - s0) * 0.16) * smoothstep(s0, s0 + 0.15, s);
    return colr * fade * (exp(-d2 * 9000.0) * 1.6 + exp(-d2 * 300.0) * 0.18 + exp(-d2 * 14.0) * 0.02);
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float T = mod(u.time, 600.0);
    int N = int(floor(mix(6.0, 12.0, u.param0) + 0.5));
    float fN = float(N);
    vec2 rot = vec2(cos(6.2831853 / fN), sin(6.2831853 / fN));
    vec2 half_ = vec2(cos(3.14159265 / fN), sin(3.14159265 / fN));
    float disp = mix(0.03, 0.22, u.param1);
    float fracture = u.param2;

    // the strip lights' bands: left strip low end, right strip high end
    vec4 lvA = vec4(crBand(u, 0.03), crBand(u, 0.10), crBand(u, 0.18), crBand(u, 0.28));
    vec4 lvB = vec4(crBand(u, 0.40), crBand(u, 0.55), crBand(u, 0.70), crBand(u, 0.85));
    // the warm accent: the complement of the palette's key colour
    vec3 warm = crCompl(crPalSteady(0.42, u));
    warm = warm / max(max(warm.r, max(warm.g, warm.b)), 0.05);
    CrPal P;
    P.warm = warm;
    P.ring = mix(crPalSteady(0.4, u), vec3(1.0), 0.35);
    P.spot = crPalSteady(0.6, u);
    P.key = crPalSteady(0.55, u);
    P.cool = crPalSteady(0.45, u);
    P.heart = crPalSteady(0.48, u);
    P.w05 = crPalSteady(0.05, u);
    P.s0 = crPalSteady(0.36, u); P.s1 = crPalSteady(0.54, u);
    P.b0 = crPalSteady(0.35, u); P.b1 = crPalSteady(0.85, u);
    // the sweep light: a slow, steady lap of about 30 s (the facets multiply
    // its motion, so a faster lap would strobe the stone)
    float sweepA = u.time * 0.21;
    // warm and cool alternate lap by lap, crossfading while the strip is behind the stone
    float coolMix = 0.5 - 0.5 * cos(0.5 * sweepA);
    P.sweep = mix(warm, P.key * 1.3, coolMix) * (0.6 + 1.4 * crPulse(u));
    P.beam = 0.45 + 0.8 * crPulse(u);

    // camera: a slow, steady orbit and dolly, level (a roll would sweep the
    // fan's long rays across the frame)
    float orbit = u.time * 0.045;
    float elev = 0.17 + 0.06 * sin(T * 0.071);
    float dist = 2.35 + 0.2 * sin(T * 0.053);
    vec3 ro = dist * vec3(sin(orbit) * cos(elev), sin(elev), -cos(orbit) * cos(elev));
    vec3 ta = vec3(0.0, -0.12, 0.0);
    // the ring light hangs behind the stone wherever the camera is
    vec3 ringL = vec3(-sin(orbit) * 0.987, 0.16, cos(orbit) * 0.987);
    vec3 sweepL = vec3(sin(sweepA) * 0.94, 0.34, cos(sweepA) * 0.94);
    vec3 rd = osCamera(ro, ta, 0.0, 1.45, p);

    // gem pose: slow spin and a gentle eased tilt toward the camera
    float yaw = 0.06 * T;
    float tilt = 0.28 + 0.1 * sin(T * 0.09);
    float cy = cos(yaw), sy = sin(yaw), ct = cos(tilt), st = sin(tilt);
    mat3 ry = mat3(cy, 0.0, sy, 0.0, 1.0, 0.0, -sy, 0.0, cy);
    mat3 rx = mat3(1.0, 0.0, 0.0, 0.0, ct, st, 0.0, -st, ct);
    mat3 toWorld = ry * rx;
    mat3 toGem = transpose(toWorld);
    vec3 gc = vec3(0.0, 0.05 * sin(T * 0.4), 0.0);

    vec3 tint = mix(vec3(1.0), crPalSteady(0.3, u), 0.25);
    int bounces = osSteps(2.0, 3.0, detail);

    vec3 lo = toGem * (ro - gc);
    vec3 ld = toGem * rd;
    vec3 nE = vec3(0.0, 1.0, 0.0), nX = nE;
    vec2 h = vec2(1.0, -1.0);
    if (crNear(lo, ld)) h = crHit(lo, ld, N, rot, half_, nE, nX);
    vec3 col;
    if (h.x < h.y && h.x > 0.0) {
        col = crShadeGem(lo, ld, h.x, nE, toWorld, u, N, rot, half_, bounces, disp, tint, fracture, ringL, P, lvA, sweepL);
        // facet glints toward the key light, sparked by treble
        float g = pow(saturate(dot(reflect(ld, nE), toGem * vec3(0.234, 0.936, -0.262))), 160.0);
        col += peakWhite(tint, 0.8) * g * (1.0 + 0.4 * saturate(u.level));
    } else if (rd.y < 0.0) {
        // black lacquer floor: glossy reflections, caustics
        float tf = (CR_FLOOR - ro.y) / rd.y;
        vec3 fp = ro + rd * tf;
        vec3 rr = vec3(rd.x, -rd.y, rd.z);
        float fres = 0.05 + 0.6 * pow(1.0 - saturate(-rd.y), 5.0);
        vec3 refl = crEnv(rr, u, 0.06, lvA, lvB, ringL, P, sweepL);
        vec3 nE2 = vec3(0.0, 1.0, 0.0), nX2 = nE2;
        vec3 flo = toGem * (fp - gc);
        vec3 fld = toGem * rr;
        vec2 h2 = vec2(1.0, -1.0);
        if (crNear(flo, fld)) h2 = crHit(flo, fld, N, rot, half_, nE2, nX2);
        if (h2.x < h2.y && h2.x > 0.0) {
            refl = crShadeGem(flo, fld, h2.x, nE2, toWorld, u, N, rot, half_, 1, disp, tint, 0.0, ringL, P, lvA, sweepL) * 2.0;
        }
        float fade = exp(-length(fp.xz) * 0.15);
        col = refl * fres * fade;
        vec2 kq = (fp.xz - vec2(-0.3, 0.35)) * 0.8;
        col += crCaustic(kq, yaw, fN, disp, tint, T, u) * 0.45;
        float far = smoothstep(4.0, 10.0, length(fp.xz));
        col = mix(col, refl * 0.5, far);
        // the floor curves up into the cyclorama: no hard horizon
        if (rd.y > -0.1) {
            vec3 sky = crEnv(rd, u, 0.0, lvA, lvB, ringL, P, sweepL) * 0.5 + crBokeh(p, orbit, u, P);
            col = mix(sky, col, smoothstep(0.0, -0.1, rd.y));
        }
    } else {
        col = crEnv(rd, u, 0.0, lvA, lvB, ringL, P, sweepL) * 0.5 + crBokeh(p, orbit, u, P);
    }
    // the laser into the stone and the spectrum fan it throws out: the rays
    // spread in a vertical fan, red on top, that drifts slowly round the studio,
    // and each ray rides a band of the spectrum
    float tMax = h.x < h.y && h.x > 0.0 ? h.x : rd.y < 0.0 ? (CR_FLOOR - ro.y) / rd.y : 1e4;
    col += crBeam(ro, rd, gc, CR_BEAM_FROM, 0.55, 12.0, tMax, vec3(1.0, 0.96, 0.92) * P.beam);
    int rays = osSteps(3.0, 6.0, detail);
    // a slow drift of about a minute: out to one side, behind, out to the other
    float sw = sin(u.time * 0.1 + 1.2);
    float fanAz = orbit + 3.1415927 + 1.9 * sw;
    vec2 fanH = vec2(sin(fanAz), -cos(fanAz));
    vec3 fanC = gc + vec3(0.0, -0.2, 0.0) + vec3(fanH.x, 0.0, fanH.y) * 0.2;
    for (int i = 0; i < 6; i++) {
        if (i >= rays) break;
        float fi = (float(i) + 0.5) / float(rays);
        float el = 0.14 - 0.5 * fi;
        vec3 bd = vec3(fanH.x * cos(el), sin(el), fanH.y * cos(el));
        vec3 rc = hsv2rgb(vec3(fi * 0.78, 0.92, 1.0));
        col += crBeam(ro, rd, fanC, bd, 0.75, 12.0, tMax, rc * P.beam * (0.25 + 1.6 * crBand(u, fi)) * 0.8);
    }
    col *= 1.0 - 0.3 * smoothstep(0.8, 1.9, length(p));
    return col; // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
