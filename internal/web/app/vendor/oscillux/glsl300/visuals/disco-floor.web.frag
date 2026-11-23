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
// Disco Floor.
// Saturday night under the mirror ball: a lit LED dance floor runs back to a
// crowd at the far wall, and eight dancers work it: six women, curvy in
// sequinned minis and catsuits, and two men, each a solid figure in the
// complement of the floor's colours, edged in neon. Every dancer sways a hip
// out on each beat and dips into it, the knees giving and the shoulders
// answering the hips, and every eight beats each one changes moves (hands up,
// a hand through the hair or his point, arms low and loose, hands on the hips)
// and may turn to profile. Hair swings behind the sway. The floor changes its
// pattern every eight beats too (a checker that flips on the beat, a ripple
// from the middle, a scatter, a diagonal chase). The ball throws its spots
// across the wall, the floor and the dancers, and four moving heads sweep
// coloured beams through the haze. The camera holds still; only the room moves.
//
// The kick moves colour, never light. A lit area that pumps on the beat, or an
// edge that snaps across the dark wall, is a large-area flash the limiter has
// to hold (ADR-0232), and a held frame shows as ghosts. So the tiles swap
// colour at nearly one luminance, crossfading over most of the beat, stepping
// their palette on the beat and fading from one pattern to the next; the kick tints the floor, beams,
// haze and crowd toward the rig's hot colour at a fixed luminance; the beams
// and halos shine steadily; the spots and sequins are coloured and add less
// than a flash step to whatever they cross; strong reds ease toward grey so the
// palette tour never spends the limiter's red budget; and the dancers move at a
// bounded speed, with no snap through the middle of a sway.
// param0 = Glow ; param1 = Sway ; param2 = Sparkle

const float DF_H = -0.12;      // horizon
const float DF_F = 1.85;       // focal length
const float DF_CAMH = 1.0;     // eye height above the floor
const vec2 DF_BALL = vec2(0.0, 0.80);
const float DF_BALL_R = 0.12;

// depth and screen x of the eight dancers, back to front
const float DF_Z[8] = float[8](9.5, 8.5, 7.5, 6.5, 5.5, 4.6, 3.8, 2.7);
// who dances where: mostly women, two men among them, a woman out front
const float DF_MALE[8] = float[8](0.0, 1.0, 0.0, 0.0, 0.0, 1.0, 0.0, 0.0);
const float DF_X[8] = float[8](0.05, -0.95, 1.0, -0.38, 0.52, -1.22, 1.24, -0.12);

// Tapered capsule from a (radius ra) to b (radius rb).
float dfCapsule(vec2 p, vec2 a, vec2 b, float ra, float rb) {
    vec2 pa = p - a, ba = b - a;
    float h = saturate(dot(pa, ba) / max(dot(ba, ba), 1e-6));
    return length(pa - ba * h) - mix(ra, rb, h);
}

float dfEllipse(vec2 p, vec2 c, vec2 r) {
    vec2 q = (p - c) / r;
    return (length(q) - 1.0) * min(r.x, r.y);
}

// The middle joint of a two-bone limb from a to c, bent toward side `bend`.
vec2 dfJoint(vec2 a, vec2 c, float l1, float l2, vec2 bend) {
    vec2 d = c - a;
    // never nearer than the bones can fold, or the joint flies off along the line
    float len = clamp(length(d), abs(l1 - l2) + 1e-4, l1 + l2 - 1e-4);
    vec2 dir = d / max(length(d), 1e-4);
    float x = (len * len + l1 * l1 - l2 * l2) / (2.0 * len);
    float h = sqrt(max(l1 * l1 - x * x, 0.0));
    vec2 n = vec2(-dir.y, dir.x);
    n *= sign(dot(n, bend) + 1e-4);
    return a + dir * x + n * h;
}

// The torso's outline is a half-width per side read through six heights (upper thigh, hip,
// waist, bust, shoulder, neck), each eased in with zero slope so the curve stays smooth.
const float DF_KY[6] = float[6](0.78, 0.92, 1.08, 1.24, 1.38, 1.47);

float dfOutline(float y, vec3 lo, vec3 hi) {
    float w = lo.x;
    w = mix(w, lo.y, smoothstep(DF_KY[0], DF_KY[1], y));
    w = mix(w, lo.z, smoothstep(DF_KY[1], DF_KY[2], y));
    w = mix(w, hi.x, smoothstep(DF_KY[2], DF_KY[3], y));
    w = mix(w, hi.y, smoothstep(DF_KY[3], DF_KY[4], y));
    return mix(w, hi.z, smoothstep(DF_KY[4], DF_KY[5], y));
}

// One dancer's figure for this frame: joints and outline, solved for each pixel near it.
struct DfPose {
    vec2 hip; vec2 chest; vec2 head; vec2 headR; float dy;
    vec3 rLo; vec3 rHi; vec3 lLo; vec3 lHi;   // outline half-widths, right and left
    vec2 hj0; vec2 knee0; vec2 ankle0; vec2 toe0;
    vec2 hj1; vec2 knee1; vec2 ankle1; vec2 toe1;
    vec2 sh0; vec2 el0; vec2 ha0; vec2 sh1; vec2 el1; vec2 ha1;
    vec2 hairA; vec2 hairB; float hairR; float style;
    float male; float dress; float turn; float facing; float lag; float sway;
};

// Where a hand goes for move `pose` (side -1 left, +1 right; t in beats).
vec2 dfHand(int pose, float side, float t, float male, vec2 sh, vec2 hip, vec2 head) {
    if (pose == 0) {
        // arms up, wrists crossing and parting over the head
        float w = sin(3.14159265 * t + side * 0.7);
        return head + vec2(side * 0.05 + 0.09 * w, 0.31 - 0.03 * abs(w));
    }
    if (pose == 1) {
        if (male > 0.5) {
            // the point: up and out, then down and across, every two beats
            if (side > 0.0) {
                float up = smoothstep(0.0, 0.35, fract(t * 0.5)) * (1.0 - smoothstep(0.5, 0.85, fract(t * 0.5)));
                return sh + mix(vec2(-0.2, -0.42), vec2(0.34, 0.42), up);
            }
            return hip + vec2(-0.19, 0.03);
        }
        // one hand drawn up through the hair, the other resting on the hip
        if (side > 0.0) return head + vec2(0.07, 0.07 + 0.04 * sin(3.14159265 * t));
        return hip + vec2(-0.2, 0.05);
    }
    if (pose == 2) {
        // arms low and loose, swaying out from the hips
        float w = sin(3.14159265 * t + side);
        return hip + vec2(side * (0.33 + 0.05 * w), -0.04 + 0.07 * w);
    }
    // hands on the hips
    return hip + vec2(side * 0.205, 0.05);
}

DfPose dfPose(float seed, VisualUniforms u) {
    DfPose P;
    float t = u.beatCount + saturate(u.beatPhase);
    P.male = DF_MALE[int(seed) - 1];
    P.dress = (P.male < 0.5 && hash11(seed * 2.9) < 0.6) ? 1.0 : 0.0;
    P.facing = (hash11(seed * 6.7) < 0.5) ? -1.0 : 1.0;
    float amt = (0.55 + 0.9 * u.param1) * (0.8 + 0.4 * hash11(seed * 3.7)) * (1.0 - 0.1 * P.male);
    float mirror = (hash11(seed * 5.1) < 0.5) ? 0.0 : 1.0;

    // moves and turns change with each block of eight beats, eased in over its first beat
    float block = floor(t / 8.0);
    float w = smoothstep(0.0, 1.0, t - block * 8.0);
    P.turn = mix(step(0.62, hash11((block - 1.0) * 3.17 + seed * 7.9)), step(0.62, hash11(block * 3.17 + seed * 7.9)), w);

    // the hip sways to a side on every beat as a plain sine, and the knees dip over the first
    // part of each beat and rest; the chest answers late. Nothing snaps: a fast edge would jerk
    // the figure and sweep it across the dark wall in a frame, which the limiter must hold
    P.sway = cos(3.14159265 * (t + mirror));
    float cl = cos(3.14159265 * (t + mirror - 0.18));
    float hx = 0.07 * amt * P.sway * (1.0 - 0.5 * P.turn);
    float dip = sin(3.14159265 * min(fract(t) / 0.4, 1.0));
    P.dy = 0.04 * amt * dip * dip;
    P.hip = vec2(hx, 0.93 - P.dy);
    P.chest = vec2(-0.4 * 0.07 * amt * cl * (1.0 - 0.5 * P.turn), 1.30 - 0.85 * P.dy);
    P.head = P.chest + vec2(-0.25 * hx + 0.02 * P.facing * P.turn, 0.29 + 0.02 * P.male);
    P.headR = mix(vec2(0.07, 0.097), vec2(0.082, 0.103), P.male);
    P.lag = 0.1 * amt * cos(3.14159265 * (t + mirror - 0.35));

    // the outline: an hourglass from the front, bust and seat in profile; his is a V
    vec3 fLo = mix(vec3(0.165, 0.19, 0.122), vec3(0.15, 0.155, 0.145), P.male);
    vec3 fHi = mix(vec3(0.155, 0.178, 0.045), vec3(0.19, 0.225, 0.058), P.male);
    vec3 frontLo = mix(vec3(0.085, 0.085, 0.072), vec3(0.09, 0.09, 0.1), P.male);
    vec3 frontHi = mix(vec3(0.15, 0.085, 0.04), vec3(0.13, 0.1, 0.05), P.male);
    vec3 backLo = mix(vec3(0.095, 0.155, 0.072), vec3(0.09, 0.11, 0.08), P.male);
    vec3 backHi = mix(vec3(0.1, 0.09, 0.04), vec3(0.11, 0.1, 0.05), P.male);
    // the popped hip pushes out and the other side straightens
    vec3 pop = vec3(0.012, 0.03, 0.0) * P.sway * (1.0 - P.turn);
    bool right = P.facing > 0.0;
    P.rLo = mix(fLo, right ? frontLo : backLo, P.turn) + pop;
    P.rHi = mix(fHi, right ? frontHi : backHi, P.turn);
    P.lLo = mix(fLo, right ? backLo : frontLo, P.turn) - pop;
    P.lHi = mix(fHi, right ? backHi : frontHi, P.turn);

    // legs: the one under the popped hip straight, the other bent in and up on its toes
    for (int k = 0; k < 2; k++) {
        float s = (k == 0) ? -1.0 : 1.0;
        vec2 hj = P.hip + vec2(s * 0.085 * (1.0 - 0.75 * P.turn), -0.07);
        float lift = saturate(-s * P.sway) * (1.0 - 0.5 * P.male);
        vec2 front = vec2(s * mix(0.08 - 0.035 * lift, 0.0, P.turn) + P.facing * P.turn * s * 0.07 + 0.3 * P.hip.x, 0.0);
        vec2 ankle = vec2(front.x, mix(0.1, 0.035, P.male) + 0.06 * lift);
        vec2 bend = mix(vec2(-s, 0.0), vec2(P.facing, 0.0), P.turn);
        // from the front a bent knee comes toward us, so only part of its bend shows across
        vec2 knee = mix(mix(hj, ankle, 0.51), dfJoint(hj, ankle, mix(0.4, 0.43, P.male), mix(0.375, 0.41, P.male), bend), mix(0.3, 1.0, P.turn));
        // the foot: a pointed heel from the front, the instep and toe in profile
        vec2 toe = ankle + mix(vec2(0.0, -0.085), vec2(P.facing * 0.11, -0.095), P.turn);
        toe = mix(toe, ankle + mix(vec2(0.0, -0.03), vec2(P.facing * 0.12, -0.03), P.turn), P.male);
        if (k == 0) { P.hj0 = hj; P.knee0 = knee; P.ankle0 = ankle; P.toe0 = toe; }
        else { P.hj1 = hj; P.knee1 = knee; P.ankle1 = ankle; P.toe1 = toe; }
    }

    // arms
    int poseA = int(floor(hash11(block * 7.31 + seed * 13.7) * 4.0));
    int poseB = int(floor(hash11((block - 1.0) * 7.31 + seed * 13.7) * 4.0));
    float lu = mix(0.28, 0.31, P.male), ll = mix(0.25, 0.27, P.male);
    for (int k = 0; k < 2; k++) {
        float s = (k == 0) ? -1.0 : 1.0;
        float sw = (s > 0.0) ? P.rHi.y : P.lHi.y;
        vec2 sh = vec2(P.chest.x + s * (sw - 0.035), 1.375 - 0.85 * P.dy);
        vec2 hand = mix(dfHand(poseB, s, t, P.male, sh, P.hip, P.head), dfHand(poseA, s, t, P.male, sh, P.hip, P.head), w);
        vec2 elbow = dfJoint(sh, hand, lu, ll, vec2(s, -0.35));
        vec2 dir = normalize(hand - elbow + vec2(0.0, 1e-4));
        if (k == 0) { P.sh0 = sh; P.el0 = elbow; P.ha0 = hand + dir * 0.085; }
        else { P.sh1 = sh; P.el1 = elbow; P.ha1 = hand + dir * 0.085; }
    }

    // hair: long and loose, big curls, a high ponytail; his is short
    P.style = (P.male > 0.5) ? 3.0 : floor(hash11(seed * 9.3) * 3.0);
    P.hairA = P.head + vec2(0.0, 0.03);
    P.hairB = P.head + vec2(-P.lag - 0.05 * P.facing * P.turn, -0.30);
    P.hairR = 0.085;
    if (P.style > 1.5 && P.style < 2.5) {
        P.hairA = P.head + vec2(-0.02 * P.facing * P.turn, 0.1);
        P.hairB = P.hairA + vec2(-1.5 * P.lag - 0.1 * P.facing * P.turn, -0.24);
        P.hairR = 0.035;
    }
    return P;
}

// A limb from a to b whose radius follows a muscle profile, one per side: x at a, y at b,
// plus a swell of z centred at w along it. `outer` points to the outer side.
float dfLimb(vec2 p, vec2 a, vec2 b, vec4 rOut, vec4 rIn, vec2 outer) {
    vec2 pa = p - a, ba = b - a;
    float h = saturate(dot(pa, ba) / max(dot(ba, ba), 1e-6));
    vec2 q = pa - ba * h;
    vec2 n = vec2(-ba.y, ba.x);
    vec4 r = (dot(q, n) * dot(n, outer) >= 0.0) ? rOut : rIn;
    float g = saturate(1.0 - (h - r.w) * (h - r.w) * 8.0);
    return length(q) - (mix(r.x, r.y, h) + r.z * g * g);
}

// Where p sits on the dress: across from the line the dress hangs on (the torso's centre above
// the hips, the hem's swinging centre below) and up from the feet, both moving with the pose, so
// the sequins ride on the fabric instead of sliding under it as she sways and dips.
vec2 dfDressFrame(vec2 p, DfPose P) {
    float yy = p.y + P.dy;
    float xc = mix(P.hip.x, P.chest.x, smoothstep(0.9, 1.34, yy));
    float hem = 0.66 - 0.5 * P.dy;
    float k = saturate((P.hip.y - yy) / (P.hip.y - hem));
    vec2 km = 0.5 * (P.knee0 + P.knee1);
    float cx = mix(P.hip.x, km.x, k * (P.hip.y - hem) / max(P.hip.y - km.y, 0.1)) - 0.12 * P.lag * k;
    return vec2(p.x - mix(xc, cx, step(yy, P.hip.y)), yy);
}

// The figure's signed distance at p (local units, feet at y = 0); dressMask is 1 inside the dress.
float dfBody(vec2 p, DfPose P, out float dressMask) {
    // torso: the outline about a centre line that bends from the hips to the chest
    float xc = mix(P.hip.x, P.chest.x, smoothstep(0.9, 1.34, p.y + P.dy));
    float yy = p.y + P.dy;
    float dx = p.x - xc;
    float hw = (dx > 0.0) ? dfOutline(yy, P.rLo, P.rHi) : dfOutline(yy, P.lLo, P.lHi);
    float d = max(abs(dx) - hw, max(DF_KY[0] - yy, yy - DF_KY[5])) * 0.85;

    // neck and head
    vec2 neckBase = vec2(P.chest.x, 1.44 - 0.85 * P.dy);
    d = osSmin(d, dfCapsule(p, neckBase, P.head - vec2(0.0, 0.05), mix(0.036, 0.05, P.male), mix(0.032, 0.045, P.male)), 0.025);
    d = min(d, dfEllipse(p, P.head, P.headR));

    if (P.style < 0.5) {
        // long and loose: full at the crown, falling past the shoulders either side and swinging late
        d = osSmin(d, dfEllipse(p, P.head + vec2(0.0, 0.018), P.headR + vec2(0.028, 0.02)), 0.02);
        vec2 sway = vec2(-1.3 * P.lag, 0.0);
        float strands = min(dfCapsule(p, P.head + vec2(-0.07, -0.01), P.head + vec2(-0.105, -0.26) + sway, 0.05, 0.028),
                            dfCapsule(p, P.head + vec2(0.07, -0.01), P.head + vec2(0.105, -0.26) + sway, 0.05, 0.028));
        d = osSmin(d, mix(strands, dfCapsule(p, P.hairA, P.hairB, P.hairR, 0.06), P.turn), 0.04);
    } else if (P.style < 1.5) {
        vec2 q = p - P.head - vec2(-0.3 * P.lag, 0.025);
        float a = atan2_(q.y, q.x);
        d = min(d, length(q / vec2(1.0, 0.9)) - 0.15 - 0.012 * sin(a * 13.0));
    } else if (P.style < 2.5) {
        d = osSmin(d, dfEllipse(p, P.head + vec2(0.0, 0.01), P.headR + vec2(0.008, 0.01)), 0.02);
        d = osSmin(d, dfCapsule(p, P.hairA, P.hairB, P.hairR, 0.018), 0.02);
    } else {
        d = min(d, dfEllipse(p, P.head + vec2(0.0, 0.015), P.headR + vec2(0.006, 0.0)));
    }

    // legs: a full thigh into a soft knee, a calf that swells high and tapers to the ankle,
    // and a heeled shoe; his trousers run straight and flare below the knee
    float legs = 1.0;
    for (int k = 0; k < 2; k++) {
        vec2 hj = (k == 0) ? P.hj0 : P.hj1;
        vec2 kn = (k == 0) ? P.knee0 : P.knee1;
        vec2 an = (k == 0) ? P.ankle0 : P.ankle1;
        vec2 to = (k == 0) ? P.toe0 : P.toe1;
        vec2 out2 = vec2((k == 0) ? -1.0 : 1.0, 0.0);
        float thigh = dfLimb(p, hj + vec2(0.0, 0.03), kn,
            mix(vec4(0.098, 0.05, 0.014, 0.28), vec4(0.092, 0.06, 0.004, 0.3), P.male),
            mix(vec4(0.088, 0.045, 0.006, 0.2), vec4(0.085, 0.058, 0.0, 0.3), P.male), out2);
        float calf = dfLimb(p, kn, an,
            mix(vec4(0.047, 0.022, 0.017, 0.3), vec4(0.058, 0.07, 0.0, 0.5), P.male),
            mix(vec4(0.046, 0.022, 0.012, 0.26), vec4(0.056, 0.068, 0.0, 0.5), P.male), out2);
        float foot = dfCapsule(p, an, to, mix(0.022, 0.04, P.male), mix(0.01, 0.012, P.male));
        vec2 heel = vec2(-P.facing * 0.03, -0.1) * P.turn * (1.0 - P.male);
        foot = min(foot, dfCapsule(p, an, an + heel, 0.012, 0.005));
        legs = min(legs, osSmin(osSmin(thigh, calf, 0.03), foot, 0.012));
    }
    d = osSmin(d, legs, 0.035);

    // arms: a rounded shoulder into the upper arm, a forearm tapering to the wrist, and a
    // hand with the fingers together; his heavier through the shoulder
    float arms = 1.0;
    for (int k = 0; k < 2; k++) {
        vec2 sh = (k == 0) ? P.sh0 : P.sh1;
        vec2 el = (k == 0) ? P.el0 : P.el1;
        vec2 ha = (k == 0) ? P.ha0 : P.ha1;
        vec2 out2 = vec2((k == 0) ? -1.0 : 1.0, 0.4);
        vec2 dir = normalize(ha - el + vec2(0.0, 1e-4));
        vec2 wr = ha - dir * 0.085;
        float up = dfLimb(p, sh, el, mix(vec4(0.05, 0.03, 0.01, 0.12), vec4(0.06, 0.04, 0.014, 0.18), P.male),
            mix(vec4(0.038, 0.03, 0.004, 0.4), vec4(0.046, 0.038, 0.008, 0.4), P.male), out2);
        float fore = dfLimb(p, el, wr, mix(vec4(0.031, 0.018, 0.007, 0.25), vec4(0.04, 0.024, 0.009, 0.25), P.male),
            mix(vec4(0.03, 0.018, 0.005, 0.3), vec4(0.038, 0.024, 0.006, 0.3), P.male), out2);
        float hand = dfLimb(p, wr, ha, vec4(0.019, 0.008, 0.007, 0.35), vec4(0.019, 0.008, 0.005, 0.35), out2);
        arms = min(arms, osSmin(osSmin(up, fore, 0.02), hand, 0.01));
    }
    d = osSmin(d, arms, 0.035);

    // a short fitted dress: the hip line carried down to mid-thigh, its hem swinging after the hips
    dressMask = 0.0;
    if (P.dress > 0.5) {
        float hem = 0.66 - 0.5 * P.dy;
        float k = saturate((P.hip.y - yy) / (P.hip.y - hem));
        // the hem follows the thighs toward the knees
        vec2 km = 0.5 * (P.knee0 + P.knee1);
        float cx = mix(P.hip.x, km.x, k * (P.hip.y - hem) / max(P.hip.y - km.y, 0.1));
        float dxd = p.x - cx + 0.12 * P.lag * k;
        float hwd = ((dxd > 0.0) ? P.rLo.y : P.lLo.y) * (1.0 - 0.1 * k) + 0.008;
        float ds = max(abs(dxd) - hwd, max(hem - yy, yy - P.hip.y)) * 0.85;
        dressMask = saturate(-min(ds, max(abs(dx) - hw, max(DF_KY[0] - yy, yy - 1.28)) * 0.85) * 40.0);
        d = osSmin(d, ds, 0.015);
    }
    return d;
}

struct DfPalette { vec3 warm; vec3 hot; vec3 cool; };

// The colour c at relative luminance y: its hue kept, scaled to y, or at full
// strength with white mixed in where full strength is darker than y. A layer
// that covers much of the frame moves its colour at a fixed luminance, so it
// stays vivid without flashing.
vec3 dfAtLuma(vec3 c, float y) {
    // a palette can swing a channel below zero; the display would clip it and brighten the colour
    c = max(c, vec3(0.0));
    vec3 c1 = c / max(max(c.r, c.g), max(c.b, 1e-4));
    float l1 = luma(c1);
    if (l1 >= y) return c1 * (y / max(l1, 1e-4));
    return mix(c1, vec3(1.0), (y - l1) / (1.0 - l1));
}

// Lit and unlit tiles differ in colour only: a beat that changed the floor's luminance changed
// most of the screen at once, which the flash limiter has to hold (the ghosting)
const float DF_LED_ON = 0.13;   // a lit tile's luminance
const float DF_LED_OFF = 0.13;  // an unlit tile's: another colour, as bright
const float DF_BODY_L = 0.1;  // a dancer's figure, between the floor and the wall
// what a spot or a sequin adds: under the 0.1 luminance step that makes a flash, so the spots
// sweeping the room and the sequins riding the dresses never count as one
// before the present pass's grade and tone map, which lift a glint on the floor ~1.65 times
const float DF_GLINT_L = 0.045;

// How lit a tile is in one of the floor's four patterns.
float dfPattern(vec2 tile, float pattern, float t) {
    // a pattern that steps on the beat crossfades over most of the beat
    float on;
    float xf = smoothstep(0.0, 0.7, fract(t));
    if (pattern < 1.0) {
        on = mix(mod(tile.x + tile.y + floor(t) + 1.0, 2.0), mod(tile.x + tile.y + floor(t), 2.0), xf);
    } else if (pattern < 2.0) {
        float r = length(tile - vec2(0.0, 6.0));
        float e0 = r - fract(t) * 6.0, e1 = e0 - 3.0;
        on = exp(-e0 * e0 * 0.8) + 0.25 * exp(-e1 * e1 * 0.8);
    } else if (pattern < 3.0) {
        on = mix(step(0.62, hash21(tile + (floor(t) - 1.0) * 1.7)), step(0.62, hash21(tile + floor(t) * 1.7)), xf);
    } else {
        float ch = fract((tile.x - tile.y) * 0.125 - t * 0.25);
        on = smoothstep(0.0, 0.06, ch) * (1.0 - smoothstep(0.3, 0.36, ch));
    }
    return saturate(on);
}

// A tile's colour. Every eight beats the floor moves to its next pattern, fading the outgoing
// one out over the first beat of the incoming one rather than cutting between them.
vec3 dfLed(vec2 tile, float t, float kick, DfPalette P) {
    float block = floor(t / 8.0);
    float pattern = mod(block, 4.0);
    float on = dfPattern(tile, pattern, t);
    float fade = smoothstep(0.0, 1.0, t - block * 8.0);
    if (fade < 1.0) on = mix(dfPattern(tile, mod(block + 3.0, 4.0), t), on, fade);
    float h = hash21(tile * 1.31 + 7.0);
    vec3 c = (h < 0.4) ? P.hot : ((h < 0.75) ? P.warm : P.cool);
    vec3 c2 = (h < 0.4) ? P.cool : ((h < 0.75) ? P.hot : P.warm);
    // lit and unlit differ in colour; the kick blushes every tile toward the rig's hot colour
    vec3 hue = mix(c2, c, on);
    hue = mix(hue, P.hot, 0.55 * kick);
    return dfAtLuma(hue, mix(DF_LED_OFF, DF_LED_ON, on));
}

// Spots thrown by the mirror ball: cells in angle and log distance around it, turning as it does.
float dfSpots(vec2 sp, VisualUniforms u) {
    vec2 q = sp - DF_BALL;
    float r = length(q);
    float a = atan2_(q.y, q.x) / 6.2831853 * 48.0 + u.time * 0.35;
    float lr = log(max(r, 1e-3)) * 7.0;
    vec2 cell = floor(vec2(a, lr));
    vec2 f = fract(vec2(a, lr)) - 0.5;
    vec2 o = (hash22(cell) - 0.5) * 0.6;
    float on = step(0.6, hash21(cell + 19.0));
    float s = exp(-dot(f - o, f - o) * 60.0) * on;
    return s * smoothstep(DF_BALL_R * 1.4, DF_BALL_R * 3.0, r);
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 sp = centered(uv, u.resolution);
    sp.y = -sp.y;
    float t = u.beatCount + saturate(u.beatPhase);
    float ph = saturate(u.beatPhase);
    float kick = exp(-ph * 3.0) * (0.45 + 0.55 * saturate(u.bassImpact + u.bass * 0.5));
    float glow = 0.5 + u.param0;
    float sparkle = 0.4 + 1.2 * u.param2;
    float detail = osDetail(u);

    // the colours move on a step every beat, an even quarter of their bar's move each time: the
    // floor's answer to the beat, at a fixed luminance, so it is a change of colour, not a flash
    float bar = u.beatCount / 4.0;
    DfPalette Pal;
    Pal.warm = palVisual(fract(0.12 + bar * 0.17), u);
    Pal.hot = palVisual(fract(0.45 + bar * 0.17), u);
    Pal.cool = palVisual(fract(0.80 + bar * 0.17), u);

    vec3 col;

    // the floor's tile coordinates and their pixel footprint, taken here while every pixel
    // runs together: fwidth is undefined inside the branch below
    float floorZ = DF_CAMH * DF_F / max(DF_H - sp.y, 1e-4);
    vec2 floorP = vec2(sp.x * floorZ / DF_F, floorZ);
    vec2 floorW = fwidth(floorP);
    if (sp.y > DF_H) {
        // the far wall: dark, with coloured haze rising off the floor
        float hgt = sp.y - DF_H;
        col = mix(Pal.hot * 0.05, Pal.cool * 0.015, saturate(hgt * 0.9));
        col += dfAtLuma(mix(Pal.warm, Pal.hot, kick), 0.03) * exp(-hgt * 6.0);
    } else {
        // the LED floor in perspective
        float z = floorZ;
        vec2 fp = floorP;
        vec2 tile = floor(fp);
        vec2 f = fract(fp) - 0.5;
        vec3 led = dfLed(tile, t, kick, Pal);
        vec2 fw = floorW;
        float seamW = max(fw.x, fw.y);
        float edge = max(abs(f.x), abs(f.y));
        float glass = 1.0 - smoothstep(0.44 - seamW, 0.46 + seamW, edge);
        float centre = 1.0 - 0.45 * smoothstep(0.0, 0.5, edge);
        // far tiles fall below a pixel: show their mean instead of aliasing
        float farMix = saturate(seamW * 2.0 - 0.3);
        vec3 mean = dfAtLuma(Pal.hot * 0.4 + Pal.warm * 0.35 + Pal.cool * 0.25, 0.07);
        vec3 floorCol = mix(led * glass * centre, mean, farMix);
        // Glow lifts the floor only a little: its light stays near the dancers'
        col = floorCol * (0.85 + 0.3 * u.param0);
        // a mist where the floor meets the wall
        col = mix(col, Pal.warm * 0.08, exp(-(DF_H - sp.y) * 14.0) * 0.8);
        // the ball, mirrored in the glass
        vec2 mb = vec2(sp.x, 2.0 * DF_H - sp.y) - DF_BALL;
        col += Pal.cool * 0.05 * exp(-length(mb) * 5.0);
    }

    // the moving heads: four beams from the rig, sweeping through the haze
    float haze = 0.55 + 0.45 * fbm(sp * 2.2 + vec2(u.time * 0.05, -u.time * 0.03));
    for (int i = 0; i < 4; i++) {
        float fi = float(i);
        vec2 src = vec2(-1.35 + fi * 0.9, 1.08);
        float ang = -1.5708 + (fi - 1.5) * 0.22 + 0.42 * sin(t * 0.39 + fi * 1.7) * (1.0 - 2.0 * mod(fi, 2.0));
        vec2 dir = vec2(cos(ang), sin(ang));
        vec2 q = sp - src;
        float along = dot(q, dir);
        float across = abs(dot(q, vec2(-dir.y, dir.x)));
        float width = 0.012 + max(along, 0.0) * 0.075;
        float aw = across / width;
        float beam = exp(-aw * aw) * step(0.0, along) * exp(-along * 0.55);
        vec3 bc = (mod(fi + bar, 3.0) < 1.0) ? Pal.cool : ((mod(fi + bar, 3.0) < 2.0) ? Pal.hot : Pal.warm);
        bc = dfAtLuma(mix(bc, Pal.hot, 0.65 * kick), 0.3);
        col += bc * beam * haze * 0.42 * glow;
    }

    // the crowd along the far wall: heads and shoulders, bouncing on the beat
    {
        float cx = sp.x * 15.0;
        float cell = floor(cx);
        float fx = fract(cx) - 0.5;
        float hh = hash11(cell * 1.13);
        float bob = 0.012 * (0.5 + 0.5 * cos(6.2831853 * (t + hh * 0.3)));
        float top = DF_H + 0.035 + hh * 0.02 - bob;
        vec2 hq = vec2(fx / 15.0, sp.y - top);
        float dh = length(hq) - 0.022;
        float shoulders = sp.y - (DF_H + 0.012 + 0.012 * (1.0 - fx * fx * 4.0) - bob);
        float dc = min(dh, shoulders);
        // some hands are up
        if (hash11(cell * 3.7) < 0.3) {
            float hx0 = (cell + 0.5) / 15.0;
            float reach = 0.055 + 0.02 * hh;
            dc = min(dc, dfCapsule(sp, vec2(hx0 - 0.02, top - 0.012), vec2(hx0 - 0.034, top + reach), 0.0045, 0.0035));
            dc = min(dc, dfCapsule(sp, vec2(hx0 + 0.02, top - 0.012), vec2(hx0 + 0.034, top + reach), 0.0045, 0.0035));
        }
        float a = aaFill(dc) * step(DF_H - 0.02, sp.y);
        col = mix(col, Pal.hot * 0.01, a);
        col += dfAtLuma(mix(Pal.warm, Pal.hot, kick), 0.12) * aaLine(dc, 0.0015) * step(DF_H, sp.y);
    }

    // the ball's spots on the wall and the floor: coloured, and less than a flash brighter than
    // what they cross, since a spot sweeping a pixel lights and darkens it in under a second
    col += dfAtLuma(mix(vec3(1.0), Pal.cool, 0.5), DF_GLINT_L) * dfSpots(sp, u) * min(sparkle, 1.0);

    // the dancers, back to front, each with its reflection in the floor
    for (int i = 0; i < 8; i++) {
        float z = DF_Z[i];
        float seed = float(i) + 1.0;
        float sc = DF_F / z * (0.95 + 0.1 * hash11(seed * 8.1));
        vec2 foot = vec2(DF_X[i], DF_H - DF_CAMH * DF_F / z);
        vec2 p = (sp - foot) / sc;
        bool inFig = p.y > -0.1 && p.y < 2.2 && abs(p.x) < 0.75;
        bool inRefl = p.y < 0.0 && p.y > -2.2 && abs(p.x) < 0.75 && detail > 0.25;
        if (!inFig && !inRefl) continue;
        DfPose P = dfPose(seed, u);
        float dressMask;
        // one pixel in the figure's units; edges are filled from it, not from fwidth, which
        // this loop's branches leave undefined
        float px = 2.0 / (u.resolution.y * sc);
        // the reflection: the dancer mirrored about the feet, faint in the glass
        if (inRefl) {
            float dr = dfBody(vec2(p.x, -p.y), P, dressMask);
            col *= 1.0 - 0.3 * saturate(0.5 - dr / px) * exp(p.y * 1.5);
        }
        if (!inFig) continue;
        float d = dfBody(p, P, dressMask);
        // a soft halo of the lights behind them
        col += mix(Pal.warm, Pal.hot, saturate(p.y * 0.5)) * exp(-max(d, 0.0) * 40.0) * 0.07 * glow * 0.8;
        float fill = saturate(0.5 - d / px);
        if (fill <= 0.0) continue;
        // rim light from the outline's normal: the rig's two colours from either side above,
        // the floor's from below
        float e = 0.006, dm;
        vec2 n = normalize(vec2(dfBody(p + vec2(e, 0.0), P, dm) - d, dfBody(p + vec2(0.0, e), P, dm) - d) + vec2(0.0, 1e-5));
        // a thin band, a few pixels wide however far away the dancer is
        float rim = pow(saturate(1.0 + d / (2.5 * px)), 2.0);
        float lightL = pow(saturate(dot(n, vec2(-0.85, 0.53))), 1.5);
        float lightR = pow(saturate(dot(n, vec2(0.85, 0.53))), 1.5);
        float bounce = saturate(-n.y);
        // the figure is a solid colour of its own, lit by the rig from either side, at a
        // luminance near the floor's: dark on the bright floor, its every move would step the
        // light over the area it sweeps, a large-area flash
        // its hue is the complement of the floor's, so it stands apart by colour, not light
        vec3 fm = Pal.hot * 0.4 + Pal.warm * 0.35 + Pal.cool * 0.25;
        vec3 comp = max(vec3(max(max(fm.r, fm.g), fm.b)) - fm, vec3(0.02));
        vec3 skin = dfAtLuma(comp, DF_BODY_L * (1.0 + 0.4 * (lightL + lightR - 0.5)));
        // the rim is a neon edge in the rig's colours at about the figure's luminance: a bright
        // edge sweeping with the dance would flash over all the area it crosses
        float rimA = rim * saturate((lightL + lightR + 0.35 * bounce) * 1.6) * (0.6 + 0.4 * glow);
        vec3 rimHue = mix(Pal.hot * lightL + Pal.cool * lightR + Pal.warm * bounce * 0.35, Pal.hot, 0.6 * kick);
        vec3 body = mix(skin, dfAtLuma(rimHue, 0.14), rimA);
        // sequins on the dresses catch the light
        if (dressMask > 0.0) {
            vec2 dp = dfDressFrame(p, P) * 140.0;
            vec2 sq = floor(dp);
            float dot_ = 1.0 - smoothstep(0.25, 0.45, length(fract(dp) - 0.5));
            float tw = pow(0.5 + 0.5 * sin(u.time * (1.5 + 2.0 * hash21(sq + 5.0)) + 6.2831853 * hash21(sq)), 6.0) * dot_;
            body += dfAtLuma(mix(vec3(1.0), Pal.warm, 0.5), DF_GLINT_L) * tw * dressMask * min(sparkle, 1.0);
            body += Pal.hot * 0.03 * dressMask;
        }
        // the spots fade out over the dress, so a spot and a sequin together stay within one glint
        body += dfAtLuma(mix(vec3(1.0), Pal.cool, 0.5), 0.7 * DF_GLINT_L) * dfSpots(sp, u) * min(sparkle, 1.0) * (1.0 - dressMask);
        col = mix(col, body, fill);
    }
    // the mirror ball, hanging from the rig
    {
        vec2 q = sp - DF_BALL;
        float wire = aaLine(q.x, 0.0015) * step(DF_BALL_R, q.y);
        col = mix(col, vec3(0.05), wire * 0.8);
        float r = length(q);
        float disc = aaFill(r - DF_BALL_R);
        if (disc > 0.0) {
            vec2 nq = q / DF_BALL_R;
            vec3 n = vec3(nq, sqrt(max(1.0 - dot(nq, nq), 0.0)));
            float lon = atan2_(n.x, n.z) + u.time * 0.3;
            float lat = asin(clamp(n.y, -1.0, 1.0));
            vec2 fc = vec2(lon, lat) * (14.0 / 3.14159265);
            vec2 cell = floor(fc);
            vec2 ff = fract(fc) - 0.5;
            float seam = smoothstep(0.3, 0.5, max(abs(ff.x), abs(ff.y)));
            float glint = pow(0.5 + 0.5 * sin(u.time * (1.0 + 1.0 * hash21(cell + 3.0)) + 6.2831853 * hash21(cell)), 4.0);
            vec3 silver = vec3(0.10, 0.10, 0.12) * (0.4 + 0.6 * n.z) + Pal.cool * 0.05;
            vec3 ball = silver * (1.0 - 0.8 * seam) + mix(vec3(1.0), mix(Pal.warm, Pal.hot, kick), 0.35) * glint * (1.0 - seam) * 1.4 * sparkle;
            col = mix(col, ball, disc);
        }
        col += mix(vec3(1.0), Pal.cool, 0.4) * exp(-r * 9.0) * 0.25 * glow;
    }

    col *= 1.0 - 0.35 * smoothstep(0.9, 2.0, length(sp));
    // Strong reds ease toward grey at their own luminance. As the palette tours through red the
    // floor, beams and figures turn a red the vivid present grades into saturated red, and the
    // limiter's slower red budget then held whole windows of the room (the ghosting).
    float redShare = col.r / (col.r + col.g + col.b + 1e-4);
    col = mix(col, vec3(luma(col)), saturate((redShare - 0.7) * 5.0) * 0.5);
    float ign = fract(52.9829189 * fract(dot(uv * u.resolution, vec2(0.06711056, 0.00583715))));
    col += (ign - 0.5) * 0.0015;
    return max(col, 0.0); // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
