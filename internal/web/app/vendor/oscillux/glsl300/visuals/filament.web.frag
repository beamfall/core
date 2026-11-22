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
// Filament.
// A macro product shot of a showcase bulb: one long coiled tungsten filament
// strung inside a clear glass tube, seen so close that every turn of the coil
// is a glowing wire with a lit front and a darker back. The coil's axis is a
// plucked string: band energies weight its standing-wave harmonics (bass the
// fundamental, treble the high harmonics) in two planes, and each beat softly
// plucks a small kink that runs out to the lead-in wires. The bands are smoothed
// toward the level, so the string sways rather than snaps. The coil burns at
// a steady orange heat that the level warms only slightly, with faint hot
// spots and a soft warm/cool rim accent that alternates each beat; the
// palette is held at one mean luma and the centroid only leans the hues. A shallow lens melts the far coil into a
// soft tube and the studio behind into palette bokeh; studio softboxes slide
// across the glass as long curved highlights. The camera glides along the
// tube at a steady pace with no surges; audio drives heat, light and the
// string's shape, never the camera.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Tension ; param1 = Glow ; param2 = Heat

// The palette read: the visual's own palette (2) with no beat or spectral
// nudge and no palette tour, so the host's vivid tour never swells the light;
// a host's manifest palette still replaces it. The fog tint is rebuilt on it.
vec3 flPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 2, u); }
vec3 flPalSteadyFog(float t, VisualUniforms u) {
    vec3 c = flPalSteady(t, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}
const float FL_L = 6.0;        // half length of the string
const float FL_FLOOR = -1.15;  // floor height
const float FL_RC = 1.15;      // radius that bounds the string's motion

// Band energies at the eight harmonic positions (bins 0, 9, ..., 63, the same
// points bandAt(n / 7) samples), read with constant indices.
void flBands(VisualUniforms u, out vec4 w0, out vec4 w1) {
    w0 = vec4(u.spectrum[0].x, u.spectrum[2].y, u.spectrum[4].z, u.spectrum[6].w);
    w1 = vec4(u.spectrum[9].x, u.spectrum[11].y, u.spectrum[13].z, u.spectrum[15].w);
}

// The palette read once per pixel at its five stops and blended locally; the
// library read walks the whole stop table on every call.
struct FlPalette { vec3 c0; vec3 c1; vec3 c2; vec3 c3; vec3 c4; };

FlPalette flPalLoad(VisualUniforms u) {
    FlPalette P;
    P.c0 = flPalSteady(0.0, u);
    P.c1 = flPalSteady(0.26, u);
    P.c2 = flPalSteady(0.52, u);
    P.c3 = flPalSteady(0.78, u);
    P.c4 = flPalSteady(0.999, u);
    // the palette tour's crossfades keep one overall brightness: the five
    // stops are scaled together to a fixed mean luma, so a tour step changes
    // the colours but never brightens or darkens the whole frame
    float gl = 0.2 * (luma(P.c0) + luma(P.c1) + luma(P.c2) + luma(P.c3) + luma(P.c4));
    float gk = clamp(0.32 / max(gl, 1e-3), 0.4, 3.0);
    P.c0 *= gk; P.c1 *= gk; P.c2 *= gk; P.c3 *= gk; P.c4 *= gk;
    return P;
}

vec3 flPal(FlPalette P, float t) {
    t = fract(t) * 4.0;
    vec3 c = mix(P.c0, P.c1, smoothstep(0.0, 1.0, t));
    c = mix(c, P.c2, smoothstep(1.0, 2.0, t));
    c = mix(c, P.c3, smoothstep(2.0, 3.0, t));
    return mix(c, P.c4, smoothstep(3.0, 4.0, t));
}

// Speed over the shared 32-beat profile, as osFlight, with the cycle's
// running distance read from a table instead of summed per pixel.
const float FL_RUN[33] = float[33](0.0, 1.0, 2.0, 3.0, 4.15, 5.6, 7.35, 9.4, 12.25, 15.75, 19.25, 22.75, 25.3, 26.325, 27.0, 27.95, 29.0, 30.15, 31.4, 32.75, 33.75, 34.35, 34.95, 35.55, 36.6, 38.475, 41.1, 44.475, 48.6, 53.1, 57.6, 61.05, 62.75);
vec2 flFlight(VisualUniforms u, float cruise, float surge) {
    float n = floor(u.beatCount);
    int k = int(mod(n, 32.0));
    float ph = saturate(u.beatPhase);
    float v0 = osBeatSpeed(n);
    float v1 = osBeatSpeed(n + 1.0);
    float beats = floor(n / 32.0) * FL_RUN[32] + FL_RUN[k] + v0 * ph + 0.5 * (v1 - v0) * ph * ph;
    return vec2(u.time * cruise * 0.2 + beats * (cruise * 0.4 + surge), mix(v0, v1, ph));
}

// Linear interpolation of those eight energies at span position s in [0, 1].
float flBand8(float s, vec4 w0, vec4 w1) {
    float x = saturate(s) * 7.0;
    vec4 h0 = max(1.0 - abs(x - vec4(0.0, 1.0, 2.0, 3.0)), 0.0);
    vec4 h1 = max(1.0 - abs(x - vec4(4.0, 5.0, 6.0, 7.0)), 0.0);
    return dot(w0, h0) + dot(w1, h1);
}

// Harmonic weights for one string: y and z coefficients of harmonics 1..8 in
// vec4 pairs, plus their magnitudes (the envelope). ph offsets the phase so
// the sister strings differ.
void flCoefs(VisualUniforms u, vec4 w0, vec4 w1, float tension, float pluck, float ph,
             out vec4 y0, out vec4 y1, out vec4 z0, out vec4 z1, out vec4 e0, out vec4 e1) {
    float t = mod(u.time, 600.0);
    vec4 fa = vec4(1.0, 2.0, 3.0, 4.0);
    vec4 fb = vec4(5.0, 6.0, 7.0, 8.0);
    float drive = pluck * (0.66 + 0.05 * u.level);
    // 0.80 / sqrt(n)
    vec4 aa = w0 * (1.25 - 0.09 * fa) * vec4(0.8, 0.5656854, 0.4618802, 0.4) * drive;
    vec4 ab = w1 * (1.25 - 0.09 * fb) * vec4(0.3577709, 0.3265986, 0.3023716, 0.2828427) * drive;
    // a slow sway: the harmonics ring at a sixth of their old rate
    vec4 pa = tension * (0.7 + 0.45 * fa) * 0.5 * t + fa * 1.7 + ph;
    vec4 pb = tension * (0.7 + 0.45 * fb) * 0.5 * t + fb * 1.7 + ph;
    float sum = dot(aa, vec4(1.0)) + dot(ab, vec4(1.0));
    // keep the whole motion inside the bounding cylinder
    float k = min(1.0, 0.75 / max(sum, 1e-4));
    y0 = aa * cos(pa) * k;
    y1 = ab * cos(pb) * k;
    z0 = aa * 0.75 * sin(pa + 0.9 * fa) * k;
    z1 = ab * 0.75 * sin(pb + 0.9 * fb) * k;
    e0 = aa * k;
    e1 = ab * k;
}

// sin(n th) for n = 1..8 by the Chebyshev recurrence
void flSines(float s, out vec4 a, out vec4 b) {
    float th = 3.14159265 * s;
    float s1 = sin(th);
    float c2 = 2.0 * cos(th);
    float s2 = c2 * s1;
    float s3 = c2 * s2 - s1;
    float s4 = c2 * s3 - s2;
    float s5 = c2 * s4 - s3;
    float s6 = c2 * s5 - s4;
    float s7 = c2 * s6 - s5;
    float s8 = c2 * s7 - s6;
    a = vec4(s1, s2, s3, s4);
    b = vec4(s5, s6, s7, s8);
}

// Point on a string at span position s in [0, 1].
vec3 flPoint(float s, vec4 y0, vec4 y1, vec4 z0, vec4 z1, float bow, vec3 kink) {
    vec4 a, b;
    flSines(s, a, b);
    float y = dot(y0, a) + dot(y1, b) + bow * a.x;
    float z = dot(z0, a) + dot(z1, b);
    // the pluck kinks: two packets running out from the pluck point
    float g = exp(-(s - kink.x) * (s - kink.x) * 600.0) + exp(-(s - kink.y) * (s - kink.y) * 600.0);
    y += g * kink.z;
    z += g * kink.z * 0.4;
    return vec3(-FL_L + 2.0 * FL_L * s, y, z);
}

// Closest approach of the ray to segment ab: (distance, ray t, segment h).
vec3 flRaySeg(vec3 ro, vec3 rd, vec3 a, vec3 b) {
    vec3 ba = b - a;
    vec3 oa = ro - a;
    float baba = dot(ba, ba);
    float bard = dot(ba, rd);
    float baoa = dot(ba, oa);
    float rdoa = dot(rd, oa);
    float h = saturate((baoa - bard * rdoa) / max(baba - bard * bard, 1e-6));
    float t = max(bard * h - rdoa, 0.0);
    return vec3(length(oa + rd * t - ba * h), t, h);
}

// Closest approach of the ray to a string around the x axis, in the string's
// frame: (distance, ray t, span s). Only the span the ray passes within FL_RC
// of the axis is walked; elsewhere the axis distance stands in (halo only).
vec3 flTrace(vec3 ro, vec3 rd, vec4 y0, vec4 y1, vec4 z0, vec4 z1, float bow, vec3 kink, int nseg) {
    float A = rd.y * rd.y + rd.z * rd.z;
    float B = ro.y * rd.y + ro.z * rd.z;
    float C = ro.y * ro.y + ro.z * ro.z;
    float Ai = 1.0 / max(A, 1e-6);
    float tAx = max(-B * Ai, 0.0);
    vec3 pa0 = ro + rd * tAx;
    float sAx = saturate((pa0.x + FL_L) / (2.0 * FL_L));
    float over = max(abs(pa0.x) - FL_L, 0.0);
    vec3 best = vec3(sqrt(max(C - B * B * Ai, 0.0) + over * over), tAx, sAx);
    float disc = B * B - A * (C - FL_RC * FL_RC);
    if (disc > 0.0 && A > 1e-6) {
        float sq = sqrt(disc);
        float t0 = max((-B - sq) * Ai, 0.0);
        float t1 = (-B + sq) * Ai;
        if (t1 > 0.0) {
            float xa = ro.x + rd.x * t0;
            float xb = ro.x + rd.x * t1;
            float fn = float(nseg);
            float sLo = (min(xa, xb) + FL_L) / (2.0 * FL_L);
            float sHi = (max(xa, xb) + FL_L) / (2.0 * FL_L);
            int i0 = int(clamp(floor(sLo * fn), 0.0, fn));
            int i1 = int(clamp(ceil(sHi * fn), 0.0, fn));
            vec3 a = flPoint(float(i0) / fn, y0, y1, z0, z1, bow, kink);
            for (int k = 0; k < 64; k++) {
                int i = i0 + k;
                if (i >= i1) break;
                float s = float(i + 1) / fn;
                vec3 b = flPoint(s, y0, y1, z0, z1, bow, kink);
                vec3 r = flRaySeg(ro, rd, a, b);
                if (r.x < best.x) best = vec3(r.x, r.y, s - (1.0 - r.z) / fn);
                a = b;
            }
        }
    }
    return best;
}

// Glow of a line of radius r at distance d, depth t, defocused around focus tf.
// The cross-section integral is conserved, so blurred parts get wide and dim.
float flLens(float d, float t, float r, float tf, float pix, float aperture) {
    float coc = aperture * abs(t - tf) / max(t, 0.2);
    float w = max(r + coc * t, t * pix);
    float peak = (r + 0.004) / (w + 0.004);
    float x = d / w;
    float gauss = exp(-x * x * 2.0);
    float disk = 1.0 - smoothstep(0.75, 1.0, x);
    return peak * mix(gauss, disk, smoothstep(0.02, 0.10, coc));
}

const float FL_R = 0.15;      // coil radius
const float FL_PITCH = 0.085; // coil pitch (one turn)
const float FL_WR = 0.022;    // wire radius
const float FL_RG = 1.3;      // radius of the glass tube round the string

// Tungsten glow for heat x (0 cold, 1 white-hot): the hue climbs from deep
// red through orange to warm white while the power climbs much faster.
// Below orange heat the glow leans toward the palette's own hot stop.
vec3 flHeat(float x, vec3 tint) {
    x = max(x, 0.0);
    vec3 c = vec3(1.0, 0.30 * x + 0.55 * x * x, 0.04 * x + 0.62 * x * x * x);
    c = mix(tint, c, 0.82 + 0.18 * smoothstep(0.30, 0.85, x));
    return c * (0.10 + 3.6 * x * x * sqrt(x));
}

// Coverage of the coil's wire at a point P on the coil cylinder, in the coil's
// local frame (x along the axis), for axis position s. soft widens the edge
// for pixel size and defocus; lim returns the wire's cross-section shading.
float flWire(vec3 P, float s, float soft, float blur, out float lim) {
    float ang = atan2_(P.z, P.y) * 0.15915494;
    float k = (s * 2.0 * FL_L + P.x) / FL_PITCH - ang;
    float d = abs(fract(k + 0.5) - 0.5) * FL_PITCH;
    lim = sqrt(saturate(1.0 - d * d / (FL_WR * FL_WR)));
    return smoothstep(-soft, soft, FL_WR - d) * FL_WR / (FL_WR + blur);
}

// A softbox: the reflected ray r from P hits the plane axis = at (n = 0, 1, 2
// for x, y, z) inside the rectangle lo..hi of the other two axes. Soft-edged,
// brighter in its middle.
float flBox(vec3 P, vec3 r, int n, float at, vec2 lo, vec2 hi) {
    float rn = n == 0 ? r.x : n == 1 ? r.y : r.z;
    float pn = n == 0 ? P.x : n == 1 ? P.y : P.z;
    float t = (at - pn) / (abs(rn) > 1e-4 ? rn : 1e-4);
    if (t <= 0.0) return 0.0;
    vec3 H = P + r * t;
    vec2 q = n == 0 ? H.yz : n == 1 ? H.xz : H.xy;
    vec2 c = 0.5 * (lo + hi);
    vec2 h = 0.5 * (hi - lo);
    vec2 e = abs(q - c) / h;
    float m = (1.0 - smoothstep(0.55, 1.0, e.x)) * (1.0 - smoothstep(0.25, 1.0, e.y));
    return m * (0.55 + 0.45 * (1.0 - e.x * e.x));
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float tension = mix(0.7, 2.0, u.param0);
    float glowAmt = mix(0.6, 1.6, u.param1);
    float heatAmt = mix(0.75, 1.25, u.param2);
    float t = mod(u.time, 600.0);
    FlPalette P = flPalLoad(u);
    float bp = saturate(u.beatPhase);
    // the centroid only leans the hues a little, so the wire never flickers
    float cen = 0.5 + 0.25 * (u.spectralCentroid - 0.5);

    float bn = mod(floor(u.beatCount), 64.0);
    float odd = mod(bn, 2.0);

    // ---- camera: a glide along the tube on the shared speed profile ----
    // a steady glide: the speed profile no longer surges the camera
    vec2 fl = flFlight(u, 1.4, -0.56);
    float surge = 0.0;
    float ph = fl.x * 0.22;
    float xc = 3.4 * sin(ph * 0.5);
    float rad = 1.95 + 0.35 * sin(ph * 0.53 + 0.4);
    float el = 0.10 + 0.30 * sin(ph * 0.77 + 1.3);
    vec3 ro = vec3(xc, rad * sin(el), -rad * cos(el));
    vec3 ta = vec3(xc + 1.7 * sin(ph * 0.61 + 2.0), -0.05, 0.0);
    float roll = osBank(u, 0.08);
    float fov = 1.85;
    vec3 rd = osCamera(ro, ta, roll, fov, p);
    float tf = length(ta - ro);
    float pix = 2.0 / (u.resolution.y * fov);
    float aperture = 0.055;

    // ---- the string's state: the coil's axis ----
    float pluck = 0.38 * 0.62;
    float bow = 0.015 + 0.01 * u.level;
    vec4 bw0, bw1;
    flBands(u, bw0, bw1);
    // the shape and heat read the bands smoothed toward the level, so no
    // single bin can jerk the bright wire from frame to frame
    bw0 = mix(vec4(0.4 + 0.08 * u.level), bw0, 0.06);
    bw1 = mix(vec4(0.4 + 0.08 * u.level), bw1, 0.06);
    vec4 y0, y1, z0, z1, e0, e1;
    flCoefs(u, bw0, bw1, tension, pluck, 0.0, y0, y1, z0, z1, e0, e1);
    float sp = 0.25 + 0.5 * hash11(bn + 3.0);
    float kinkA = sp + bp * 0.9;
    float kinkB = abs(sp - bp * 0.9);
    kinkA = kinkA > 1.0 ? 2.0 - kinkA : kinkA;
    float kinkAmp = 0.0045 * smoothstep
(0.0, 0.3, bp) * exp(-2.0 * bp) * (0.8 + 0.2 * u.level);
    vec3 kink = vec3(kinkA, kinkB, kinkAmp);

    // ---- colours ----
    vec3 tint = flPal(P, 0.06 + 0.04 * cen);
    tint *= 1.0 / max(max(tint.r, max(tint.g, tint.b)), 0.05);
    vec3 cCool = flPal(P, 0.80 + 0.06 * cen);
    vec3 cMid = flPal(P, 0.45);
    vec3 cHi = flPal(P, 0.999);
    vec3 amber = vec3(1.0, 0.52, 0.16);
    // the beat accent alternates cool and warm, easing out over the beat
    float acc = 0.05 * exp(-1.5 * bp);
    vec3 cAcc = mix(cCool * 1.4 + 0.1, amber * 1.3, odd);
    // a steady base heat that the level warms only slightly
    float heat0 = heatAmt * (0.43 + 0.04 * u.level);

    // ---- glass tube: where the ray meets it, for the reflections at the end ----
    float gA = rd.y * rd.y + rd.z * rd.z;
    float gB = ro.y * rd.y + ro.z * rd.z;
    float gC = ro.y * ro.y + ro.z * ro.z - FL_RG * FL_RG;
    float gD = gB * gB - gA * gC;

    // ---- the studio behind the bulb: dark sweep and palette bokeh ----
    // a cool seamless backdrop with two soft light pools that drift slowly,
    // so the hot coil always sits on its complement
    vec3 col = cCool * (0.006 + 0.010 * exp(-abs(rd.y + 0.10) * 2.5));
    {
        float az = atan2_(rd.x, rd.z);
        vec2 q0 = vec2(az - 0.35 * sin(t * 0.05 + ph * 0.3), rd.y + 0.05);
        vec2 q1 = vec2(az + 0.9 + 0.3 * sin(t * 0.04), rd.y - 0.25);
        float pool0 = exp(-dot(q0, q0) * 3.0);
        float pool1 = exp(-dot(q1, q1) * 5.0);
        col += cCool * pool0 * (0.060 + 0.015 * u.level);
        col += mix(cMid, cHi, 0.4) * pool1 * (0.040 + 0.010 * u.level);
    }
    int nlay = osSteps(1.0, 3.0, detail);
    float trebW = 0.0;
    for (int j = 0; j < 3; j++) {
        if (j >= nlay) break;
        float fj = float(j);
        vec2 q = p * (1.0 + 0.45 * fj) + vec2(-xc * (0.30 - 0.08 * fj) + t * 0.010, t * 0.006 + fj * 7.3);
        q *= 1.25;
        vec2 cell = floor(q);
        vec2 f = fract(q) - 0.5;
        vec2 h = hash22(cell + fj * 31.0);
        vec2 c = (h - 0.5) * 0.36;
        float rr = 0.10 + 0.16 * h.y * h.y;
        // six-bladed aperture: hexagonal bokeh, turned a little per layer
        vec2 g = rot2(0.3 + 0.7 * fj) * (f - c);
        vec2 ga = abs(g);
        float dd = max(ga.x * 0.8660254 + ga.y * 0.5, ga.y) / rr;
        float on = step(0.55, h.x);
        float edge = 1.0 - smoothstep(0.80, 1.0, dd);
        float rim = exp(-(dd - 0.88) * (dd - 0.88) * 90.0);
        float disk = edge * (0.55 + 0.30 * dd * dd) + rim * 0.35;
        float pick = fract(h.y * 7.3 + fj * 0.37);
        vec3 cm = pick < 0.45 ? cCool : pick < 0.75 ? cMid : pick < 0.90 ? cHi : amber;
        cm = peakWhite(cm, trebW);
        float twinkle = 0.75 + 0.25 * sin(t * (0.6 + h.x) + h.y * 40.0);
        float bandK = bandAt(u, pick);
        col += cm * disk * on * (0.040 + 0.025 * mix(u.level, bandK, 0.3)) * twinkle * (1.0 - 0.25 * fj);
    }

    int nseg = osSteps(24.0, 56.0, detail);

    // ---- sister filaments of the squirrel cage, behind and soft ----
    int nsis = osSteps(10.0, 20.0, detail);
    for (int j = 0; j < 2; j++) {
        float fj = float(j);
        vec3 off = vec3(0.0, j == 0 ? 0.55 : -0.50, 0.75 + 0.45 * fj);
        vec4 q0, q1, w0, w1, g0, g1;
        flCoefs(u, bw0, bw1, tension * (1.13 + 0.21 * fj), pluck * 0.8, 2.1 + 1.7 * fj, q0, q1, w0, w1, g0, g1);
        vec3 sb = flTrace(ro - off, rd, q0, q1, w0, w1, 0.0, vec3(0.0), nsis);
        float hs = heat0 * 0.85 + 0.06 * flBand8(0.2 + 0.3 * fj + 0.5 * sb.z, bw0, bw1);
        vec3 cs = flHeat(hs, tint);
        float ls = flLens(sb.x, sb.y, FL_R * 0.9, tf, pix, aperture);
        col += cs * (ls * 0.22 + exp(-sb.x * 4.0) * 0.003) * glowAmt;
    }

    // ---- the coil ----
    vec3 best = flTrace(ro, rd, y0, y1, z0, z1, bow, kink, nseg);
    float sC = best.z;
    float pph = sC * 3.0 - 2.0 * fract(fl.x * 0.175);
    float pk = 0.5 + 0.5 * sin(6.2831853 * pph);
    float packet = pk * pk * pk * pk;
    packet *= packet;
    float kf = exp(-(sC - kinkA) * (sC - kinkA) * 160.0) + exp(-(sC - kinkB) * (sC - kinkB) * 160.0);
    float eC = flBand8(sC, bw0, bw1);
    float heat = heat0 + 0.04 * eC + 0.03 * packet + kf * kinkAmp * 2.0;
    // the coil cools toward its anchors, where the lead-in wires sink the heat
    heat *= 0.55 + 0.45 * smoothstep(0.0, 0.06, sC) * smoothstep(1.0, 0.94, sC);
    vec3 E = flHeat(heat, tint) * glowAmt;
    float fogS = exp(-best.y * 0.03);

    // defocused coil: a soft glowing tube; sharp coil: the wire itself
    float blurC = aperture * abs(best.y - tf);
    float sharp = 1.0 - smoothstep(0.30 * FL_PITCH, 0.90 * FL_PITCH, blurC);
    col += E * flLens(best.x, best.y, FL_R, tf, pix, aperture) * 0.55 * (1.0 - sharp) * fogS;
    if (sharp > 0.001 && best.x < FL_R * 1.5) {
        float sA = clamp(sC, 0.0, 0.996);
        vec3 A = flPoint(sA, y0, y1, z0, z1, bow, kink);
        vec3 Tg = normalize(flPoint(sA + 0.004, y0, y1, z0, z1, bow, kink) - A);
        vec3 N1 = normalize(cross(Tg, vec3(0.0, 0.0, 1.0)));
        vec3 N2 = cross(Tg, N1);
        vec3 o = ro - A;
        vec3 ol = vec3(dot(o, Tg), dot(o, N1), dot(o, N2));
        vec3 dl = vec3(dot(rd, Tg), dot(rd, N1), dot(rd, N2));
        float qa = dl.y * dl.y + dl.z * dl.z;
        float qb = ol.y * dl.y + ol.z * dl.z;
        float qc = ol.y * ol.y + ol.z * ol.z - FL_R * FL_R;
        float disc = qb * qb - qa * qc;
        if (disc > 0.0 && qa > 1e-6) {
            float sq = sqrt(disc);
            float t0 = (-qb - sq) / qa;
            float t1 = (-qb + sq) / qa;
            if (t0 > 0.0) {
                float soft = t0 * pix * 0.8 + blurC;
                float l0, l1;
                float c0 = flWire(ol + dl * t0, sA, soft, blurC, l0);
                float c1 = flWire(ol + dl * t1, sA, soft, blurC, l1);
                float a = c0 + (1.0 - c0) * c1;
                // the near face is lit by the far turns behind it, the far face
                // is seen through the gaps; a dark core edge outlines each turn
                vec3 wire = E * c0 * (0.35 + 0.75 * l0) + E * 0.62 * c1 * (1.0 - c0) * (0.40 + 0.60 * l1);
                // hot vapour inside the coil
                vec3 inner = E * 0.05 * saturate((t1 - t0) / (2.0 * FL_R));
                col = mix(col, wire + inner, sharp * a * fogS) + inner * sharp * (1.0 - a) * fogS;
            }
        }
    }
    // the glow the hot wire throws into the gas
    col += E * (exp(-max(best.x - FL_R, 0.0) * 16.0) * 0.08 + exp(-best.x * 4.0) * 0.010) * fogS;

    // ---- lead-in wires and their clamps, lit by the coil ----
    for (int k = 0; k < 2; k++) {
        float sx = k == 0 ? -1.0 : 1.0;
        vec3 a0 = vec3(sx * FL_L, 0.0, 0.0);
        vec3 a1 = vec3(sx * (FL_L - 0.35), -1.55, 0.25);
        vec3 oa = a0 - ro;
        float tq = max(dot(oa, rd), 0.0);
        float da = length(oa - rd * tq);
        float wa = 0.035 + aperture * abs(tq - tf);
        col += flHeat(heat0 * 0.8, tint) * exp(-da * da / (wa * wa)) * (0.035 / wa) * 0.5 * glowAmt;
    }

    // ---- pluck sparks: tungsten glints thrown off the pluck point ----
    int nspk = osSteps(4.0, 10.0, detail);
    float spkLife = (1.0 - bp) * (1.0 - bp) * 0.05 * (0.5 + 0.5 * u.level);

    vec3 sp0 = flPoint(sp, y0, y1, z0, z1, bow, kink);
    vec3 cPkA = flHeat(1.05, tint) * 0.9;
    vec3 cPkB = cAcc * 2.0;
    for (int i = 0; i < 10; i++) {
        if (i >= nspk) break;
        float fi = float(i);
        vec2 h = hash22(vec2(fi * 1.37 + 0.5, bn + 11.0));
        float h3 = hash11(fi * 7.1 + bn * 3.3);
        float ang = 6.2831853 * h.x;
        vec3 v = vec3((h3 - 0.5) * 1.4, 0.5 + 1.0 * h.y * abs(sin(ang)), 0.9 * cos(ang)) * (0.7 + 0.7 * h.y);
        float tau = bp * 1.0;
        vec3 sa = sp0 + v * tau - vec3(0.0, 1.2, 0.0) * tau * tau;
        float tb = max(tau - 0.05, 0.0);
        vec3 sbb = sp0 + v * tb - vec3(0.0, 1.2, 0.0) * tb * tb;
        vec3 r = flRaySeg(ro, rd, sbb, sa);
        col += mix(cPkA, cPkB, step(0.6, h.y)) * flLens(r.x, r.y, 0.005, tf, pix, aperture) * spkLife * 3.0;
    }

    // ---- glass: softboxes sliding across the curved tube ----
    if (gD > 0.0 && gA > 1e-6 && gC > 0.0) {
        float sq = sqrt(gD);
        float tg = (-gB - sq) / gA;
        if (tg > 0.0) {
            vec3 Pg = ro + rd * tg;
            vec3 n = normalize(vec3(0.0, Pg.y, Pg.z));
            float cosI = saturate(-dot(rd, n));
            float F = 0.04 + 0.96 * pow(1.0 - cosI, 5.0);
            vec3 r = reflect(rd, n);
            // a long softbox overhead, a strip low behind the camera, a small kicker
            float sA = flBox(Pg, r, 1, 3.2, vec2(-5.0, -1.6), vec2(4.0, -0.9));
            float sB = flBox(Pg, r, 2, -4.0, vec2(-7.0, -1.35), vec2(7.0, -0.95)) * 0.5;
            float sK = flBox(Pg, r, 1, 3.2, vec2(4.6, -2.6), vec2(7.5, -2.1));
            vec3 env = (vec3(1.0, 0.97, 0.93) * 0.6 + cCool * 0.6) * sA * (5.0 + 3.0 * (1.0 - odd) * acc)
                     + mix(amber, cHi, 0.3) * sB * (3.5 + 3.0 * odd * acc)
                     + cAcc * sK * (2.0 + 4.0 * acc);
            // the glass carries a faint mirror of the hot coil and a cool rim
            env += E * 0.25 * exp(-best.x * 1.5);
            // the tube's edges, seen through the most glass, carry the coil's
            // light along them and a cool rim
            float edgeG = pow(1.0 - cosI, 6.0);
            env += (flHeat(heat0, tint) * 0.35 * glowAmt + cCool * 0.35) * edgeG;
            // the hot gas inside the tube glows with the heat
            float chord = 2.0 * sq / gA;
            col += flHeat(heat0, tint) * 0.0012 * chord * glowAmt;
            col = col * (1.0 - F) * vec3(0.98, 0.97, 0.95) + env * F;
        }
    }

    // ---- anamorphic streaks: each pluck kink throws a thin, faint horizontal flare ----
    {
        vec3 fw = normalize(ta - ro);
        vec3 rt = normalize(cross(fw, vec3(sin(roll), cos(roll), 0.0)));
        vec3 cu = cross(rt, fw);
        vec3 cS = mix(flHeat(1.0, tint), cAcc, 0.5);
        for (int k = 0; k < 2; k++) {
            vec3 X = flPoint(k == 0 ? kinkA : kinkB, y0, y1, z0, z1, bow, kink) - ro;
            float zz = dot(X, fw);
            if (zz > 0.3) {
                vec2 sp2 = fov * vec2(dot(X, rt), dot(X, cu)) / zz;
                vec2 dq = p - sp2;
                float st = (exp(-abs(dq.y) * 260.0) + 0.18 * exp(-abs(dq.y) * 55.0)) * exp(-abs(dq.x) * 1.6) + exp(-dot(dq, dq) * 900.0) * 0.6;
                col += cS * st * kinkAmp * 1.6 * glowAmt;

            }
        }
    }

    // interleaved-gradient dither keeps the dark gradients from banding
    float ign = fract(52.9829189 * fract(dot(uv * u.resolution, vec2(0.06711056, 0.00583715))));
    col = max(col * (1.0 + (ign - 0.5) * 0.05) + (ign - 0.5) * 0.0008, vec3(0.0));
    return col; // linear HDR; the host tone-maps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
