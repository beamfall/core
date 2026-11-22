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
// Kaleidoscope.
// Looking down a real mirror kaleidoscope. Radial mirrors fold the view into
// segments and a lattice of three mirrors repeats it to the edges of the
// frame. Inside the object chamber a stream of 3D pieces (glass marbles, cut
// cubes, dichroic rings and neon rods) drifts slowly and steadily toward the
// eyepiece, so the mirrored mandala blooms gently outward from every cell.
// The pieces are raymarched and lit:
// glass refracts the oil wheel behind it with a little dispersion and darkens
// at its grazing rim, rods burn, dichroic rings shift colour as they turn,
// and every mirror bounce costs a little light, so the pattern dims toward
// the edges as in a real tube. The end of the tube is a backlit oil wheel,
// coloured liquid slowly folding through dark ink, so no mirror cell is ever
// an empty hole. Audio drives soft light only: the smoothed level and a
// little spectrum lift the end light, rods, glints and edges, a faint warm
// front runs along the mirror joints each beat, and the reds are kept just
// off saturated red, so nothing large flashes.
// param0 = Segments (radial mirror count, 2..16) ; param1 = Spin ; param2 = Vortex

// Fold into the three-mirror lattice: hexagonal cells of inradius 0.5, each
// folded into twelve mirrored wedges. Returns the point in the base triangle.
// z is the cell centre's distance from the axis, in cells: roughly how many
// mirror bounces the light took to get there.
// The palette read without the library's per-beat and spectral-centroid hue
// nudge, so large lit areas never shimmer with the music; under the host's
// palette tour the cross-fade between palettes is spread over the whole tour
// step, so the colours change slowly.
vec3 kaPalStill(float t, VisualUniforms u) {
    if (u.palette.x >= 0.0) return palByIndex(t, int(floor(u.palette.x + 0.5)));
    int a = int(mod(floor(u.palette.z), OS_PAL_COUNT));
    int b = int(mod(float(a + 1), OS_PAL_COUNT));
    float blend = smoothstep(0.0, 1.0, fract(u.palette.z));
    return mix(palByIndex(t, a), palByIndex(t, b), blend);
}

vec3 kaFold(vec2 q) {
    const vec2 r = vec2(1.0, 1.7320508);
    vec2 a = mod(q, r) - 0.5 * r;
    vec2 b = mod(q - 0.5 * r, r) - 0.5 * r;
    vec2 g = dot(a, a) < dot(b, b) ? a : b;
    float ang = atan2_(g.y, g.x);
    ang = abs(mod(ang + 0.5235988, 1.0471976) - 0.5235988);
    return vec3(length(g) * vec2(cos(ang), sin(ang)), length(q - g));
}

// One chamber piece in its local frame. kind: 0 marble, 1 cut cube,
// 2 dichroic ring, 3 neon rod.
float kaPiece(vec3 q, float kind, float s) {
    if (kind < 0.5) return length(q) - s;
    if (kind < 1.5) {
        vec3 b = abs(q) - vec3(s * 0.62);
        float box = length(max(b, 0.0)) + min(max(b.x, max(b.y, b.z)), 0.0) - s * 0.06;
        // chamfered corners, like a cut stone
        return max(box, (abs(q.x) + abs(q.y) + abs(q.z)) * 0.57735 - s * 0.82);
    }
    if (kind < 2.5) {
        vec2 t = vec2(length(q.xz) - s * 0.7, q.y);
        return length(t) - s * 0.2;
    }
    vec3 c = q;
    c.y -= clamp(c.y, -s * 1.3, s * 1.3);
    return length(c) - s * 0.12;
}

const int KA_N = 12;

// Keeps every red just off saturated red once tonemapped: green (for warm
// reds) or blue (for pink ones) rises to at least 0.3 of red, so no red area
// can strobe, while the hue barely moves.
float kaTone(float x) { return x * (2.51 * x + 0.03) / (x * (2.43 * x + 0.59) + 0.14); }
float kaToneInv(float y) {
    y = clamp(y, 0.0, 0.999);
    float a = 2.51 - 2.43 * y, b = 0.03 - 0.59 * y;
    return (-b + sqrt(b * b + 0.56 * a * y)) / (2.0 * a);
}
vec3 kaOffRed(vec3 c) {
    float r = kaTone(c.r), g = kaTone(c.g), b = kaTone(c.b);
    float need = 0.3 * r - g - b;
    if (need <= 0.0) return c;
    if (c.g > c.b) c.g = max(c.g, kaToneInv(g + need));
    else c.b = max(c.b, kaToneInv(b + need));
    return c;
}

// The palette is read once per pixel at its five stops and blended here:
// each library read walks the whole stop table, and a pixel needs a dozen
// colours. kaComp is the hue opposite a colour at the same lightness, the
// warm counterweight to a cool palette (and the reverse).
struct KaPalette { vec3 c0; vec3 c1; vec3 c2; vec3 c3; vec3 c4; };

KaPalette kaPalLoad(VisualUniforms u) {
    KaPalette P;
    P.c0 = kaPalStill(0.0, u);
    P.c1 = kaPalStill(0.26, u);
    P.c2 = kaPalStill(0.52, u);
    P.c3 = kaPalStill(0.78, u);
    P.c4 = kaPalStill(0.999, u);
    return P;
}

vec3 kaPal(KaPalette P, float t) {
    t = fract(t) * 4.0;
    vec3 c = mix(P.c0, P.c1, smoothstep(0.0, 1.0, t));
    c = mix(c, P.c2, smoothstep(1.0, 2.0, t));
    c = mix(c, P.c3, smoothstep(2.0, 3.0, t));
    return mix(c, P.c4, smoothstep(3.0, 4.0, t));
}

vec3 kaComp(vec3 c) {
    float hi = max(c.r, max(c.g, c.b));
    float lo = min(c.r, min(c.g, c.b));
    return max(vec3(hi + lo) - c, vec3(0.0));
}

// The palette's warmest stop, pushed richer: amber glass and the light in
// the mirror joints. A palette with no warm stop uses the complement of its
// middle stop instead.
vec3 kaWarm(KaPalette P) {
    vec3 w = P.c0;
    if (P.c1.r - P.c1.b > w.r - w.b) w = P.c1;
    if (P.c2.r - P.c2.b > w.r - w.b) w = P.c2;
    if (P.c3.r - P.c3.b > w.r - w.b) w = P.c3;
    if (P.c4.r - P.c4.b > w.r - w.b) w = P.c4;
    w = w.r - w.b > 0.15 ? w : kaComp(P.c2);
    float m = (w.r + w.g + w.b) * 0.3333333;
    return max(vec3(m) + (w - vec3(m)) * 1.6, vec3(0.0));
}

vec3 kaRot(vec3 q, vec4 r) {
    q.xy = vec2(r.x * q.x - r.y * q.y, r.y * q.x + r.x * q.y);
    q.yz = vec2(r.z * q.y - r.w * q.z, r.w * q.y + r.z * q.z);
    return q;
}

// What the chamber looks like in direction d. The end of the tube is a
// backlit oil wheel: coloured liquid folding slowly through dark ink, with
// bright filaments where two colours meet, bass lit and brightest on the
// axis. Glitter sand twinkles on it; coloured light bounces off the walls.
vec3 kaEnv(vec3 d, VisualUniforms u, float T, vec3 ec, KaPalette P, vec3 warm, float detail) {
    vec2 g = d.xy / max(d.z, 0.25);
    float ax = dot(g, g);
    vec2 w = g * 2.6 + vec2(0.035 * T, -0.025 * T);
    float n1 = vnoise(w * 1.2);
    // the second warp axis is a smooth wave at the lowest levels, where the
    // oil fold detail is below a pixel anyway
    float nw = detail > 0.4 ? vnoise(w * 1.1 + vec2(5.2, 1.3) - 0.02 * T)
                            : 0.5 + 0.5 * sin(w.x * 1.9 - w.y * 1.4 + 2.0 * n1 - 0.02 * T);
    vec2 wp = w * 2.0 + 1.5 * vec2(n1, nw);
    float n2 = vnoise(wp);
    float ink = smoothstep(0.5, 0.68, n2);
    float fil = 1.0 - abs(2.0 * n2 - 1.0);
    fil *= fil; fil *= fil; fil *= fil;
    vec3 hc = kaPal(P, 0.1 + 1.3 * n1 + 0.012 * T);
    float back = (0.0015 + 0.022 * exp(-ax * 2.0)) * (0.85 + 0.15 * u.level);
    // the ink in the cool palette, the filaments where two inks meet in the
    // warm complement, so the backdrop of every cell holds two hue families
    vec3 col = (hc * hc * ink * 1.2 + warm * fil * 0.5) * back;
    // the lamp behind the frosted end: a small white-hot heart in the warm
    // colour inside a tighter cool halo, so each cell centre reads as a
    // light source rather than a haze
    col += (peakWhite(warm, 0.5) * exp(-ax * 500.0) * 1.1 + ec * (exp(-ax * 60.0) * 0.16 + exp(-ax * 14.0) * 0.008)) * (0.8 + 0.15 * u.level);
    col *= smoothstep(-0.1, 0.4, d.z);
    // glitter sand lying on the frosted glass, twinkling with the treble
    vec2 gq = g * 14.0;
    vec2 gi = floor(gq);
    vec2 gh = hash22(gi + 17.0);
    vec2 go = gq - gi - 0.2 - 0.6 * gh;
    float gl = exp(-dot(go, go) * 600.0) * step(0.6, gh.y) * smoothstep(0.2, 0.6, d.z);
    if (gl > 0.002) {
        float tw = 0.5 + 0.5 * sin(T * (0.3 + 0.4 * gh.x) + gh.y * 6.2831853);
        col += mix(kaPal(P, gh.x), warm, step(0.8, gh.y)) * gl * (0.4 + 0.1 * u.level) * (0.7 + 0.3 * tw);
    }
    float wa = atan2_(d.y, d.x) * 0.4774648 + 0.02 * T;
    float wk = exp(-max(d.z, 0.0) * 6.0) * smoothstep(-1.0, 0.0, d.z);
    if (wk > 0.003 && detail > 0.3) {
        vec3 wall = kaPal(P, wa) * (0.5 + 0.5 * cos(wa * 18.8495559));
        col += wall * wall * 0.3 * wk;
    }
    return col;
}

// The same chamber reduced to its light: the end glow and the walls, for
// the faint mirror images in the glass where the oil detail is lost anyway.
vec3 kaEnvLite(vec3 d, vec3 ec, VisualUniforms u) {
    vec2 g = d.xy / max(d.z, 0.25);
    float ax = dot(g, g);
    vec3 col = ec * (exp(-ax * 80.0) * 0.4 + exp(-ax * 12.0) * 0.015) * (0.8 + 0.15 * u.level);
    col *= smoothstep(-0.1, 0.4, d.z);
    float wa = atan2_(d.y, d.x) * 0.4774648;
    float wk = 0.5 + 0.5 * cos(wa * 18.8495559);
    col += ec * ec * wk * wk * 0.3 * exp(-max(d.z, 0.0) * 6.0) * smoothstep(-1.0, 0.0, d.z);
    return col;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float T = mod(u.time, 600.0);
    float nSeg = max(floor(mix(2.0, 16.0, u.param0) + 0.5), 2.0);
    float spin = mix(0.2, 1.8, u.param1);
    float vortex = u.param2;
    float pix = 2.0 / u.resolution.y;
    KaPalette P = kaPalLoad(u);
    vec3 ec = P.c2;
    // the warm counterweight drifts between the complements of two stops
    vec3 warm = kaWarm(P);


    // slow eased turn of the whole tube, never driven by audio
    p = rot2(0.006 * spin * T) * p;
    float r = length(p);
    p *= 1.0 + 0.04 * r * r;                            // a touch of lens barrel
    p = rot2(vortex * 0.5 * log(1.0 + 1.6 * r)) * p;    // the vortex twist

    // radial mirrors, then the three-mirror lattice
    float wedge = 3.14159265 / nSeg;
    float pa = atan2_(p.y, p.x);
    pa = abs(mod(pa + wedge, 2.0 * wedge) - wedge);
    vec2 pm = length(p) * vec2(cos(pa), sin(pa));
    vec3 fz = kaFold(pm * 0.8);
    vec2 f = fz.xy;

    // a ray into the object chamber through this point of the base triangle
    // the cell centre looks straight down the tube axis, at the end light
    vec3 rd = normalize(vec3(f * 1.05, 1.0));

    // the stream of pieces: each slot flies from the far end to the lens,
    // then returns far away in a new place
    float flow = 0.03 * u.time;
    vec4 pc[KA_N];     // centre, size
    vec4 pr[KA_N];     // two rotations as cos/sin pairs
    vec4 pk[KA_N];     // kind, hue, spectrum position, grow
    int hl[4];          // the pieces in this ray's way, at most four
    vec2 hs[4];         // and where along the ray each one's bounds lie
    hl[0] = 0; hl[1] = 0; hl[2] = 0; hl[3] = 0;
    hs[0] = vec2(0.0); hs[1] = vec2(0.0); hs[2] = vec2(0.0); hs[3] = vec2(0.0);
    int nh = 0;
    float t0 = 9.0, t1 = 0.0;
    vec3 glow = vec3(0.0);
    float rodLvl = 0.6 + 0.3 * u.level;
    float cr = spin * 0.03 * T;
    for (int i = 0; i < KA_N; i++) {
        float fi = float(i);
        float cyc = flow + fi / float(KA_N);
        float ph = fract(cyc);
        float pass = mod(floor(cyc), 61.0);
        vec2 h = hash22(vec2(fi * 7.13 + pass * 1.37, fi * 3.1 + pass * 0.71));
        float z = mix(6.0, 0.9, ph);
        // turning about the axis, folded into the mirrors' 30-degree wedge
        // (only what lies inside it is seen, mirrored into the whole cell);
        // a piece crossing a mirror meets its own image there; the vortex
        // makes them spiral in
        float ang = h.x * 6.2831853 + cr + vortex * 0.3 * z;
        ang = abs(mod(ang + 0.5235988, 1.0471976) - 0.5235988);
        float rad = z * 0.6 * sqrt(0.02 + 0.98 * h.y);
        float s = (0.12 + 0.12 * hash11(fi * 1.7 + pass)) * (0.55 + 0.12 * z);
        pc[i] = vec4(rad * cos(ang), rad * sin(ang), z, s);
        float kind = mod(fi + pass, 4.0);
        float hue = fract(h.x * 0.5 + fi * 0.137 + 0.01 * T);
        // grow in out of the dark, and slip away before reaching the lens
        pk[i] = vec4(kind, hue, fract(0.06 + fi * 0.103 + 0.3 * h.y), smoothstep(0.0, 0.12, ph) * (1.0 - smoothstep(0.8, 1.0, ph)));
        pr[i] = vec4(1.0, 0.0, 1.0, 0.0);

        // can this ray meet the piece at all? Bounding spheres keep the
        // march to the one or two pieces in its way; the neon rods' halos
        // are taken from the ray's closest approach
        float b = dot(pc[i].xyz, rd);
        float d2 = max(dot(pc[i].xyz, pc[i].xyz) - b * b, 0.0);
        float R = pc[i].w * 1.45;
        if (d2 < R * R && b > 0.0) {
            float hc = sqrt(R * R - d2);
            t0 = min(t0, b - hc);
            t1 = max(t1, b + hc);
            float a1 = T * (0.05 + 0.03 * h.y) + h.x * 6.0;
            float a2 = T * (0.035 + 0.025 * h.x) + h.y * 6.0;
            pr[i] = vec4(cos(a1), sin(a1), cos(a2), sin(a2));
            if (nh < 4) { hl[nh] = i; hs[nh] = vec2(b - hc, b + hc); nh++; }
        }
        float gk = exp(-d2 / (pc[i].w * pc[i].w) * 6.0) * pk[i].w * step(0.0, b);
        if (pk[i].x > 2.5 && gk > 0.002) {
            glow += kaPal(P, pk[i].y) * (0.1 + 0.12 * rodLvl) * gk;
        }
    }

    // march only the pieces whose bounds hold the current point, and jump
    // the gaps between them, so no step is spent on empty chamber
    int steps = osSteps(12.0, 24.0, detail);
    float t = max(t0, 0.3);
    float dm = 1e3;
    int hid = -1;
    if (t1 > t) {
        for (int sI = 0; sI < 24; sI++) {
            if (sI >= steps) break;
            vec3 pos = rd * t;
            dm = 1e3;
            float nxt = 1e3;
            for (int j = 0; j < 4; j++) {
                if (j >= nh) break;
                vec2 iv = hs[j];
                if (t < iv.x) { nxt = min(nxt, iv.x); continue; }
                if (t > iv.y) continue;
                int i = hl[j];
                float d = kaPiece(kaRot(pos - pc[i].xyz, pr[i]), pk[i].x, pc[i].w * pk[i].w);
                if (d < dm) { dm = d; hid = i; }
            }
            if (dm > 1e2) {
                if (nxt > 1e2) break;
                t = nxt;
                continue;
            }
            if (dm < 0.0015 * t) break;
            t = min(t + dm * 0.9, max(nxt, t + 1e-4));
            if (t > t1) break;
        }
    }

    float la = 0.09 * T;
    vec3 L = normalize(vec3(cos(la), sin(la), -0.7));
    vec3 col = vec3(0.0);
    vec3 fogCol = mix(P.c1, P.c2, 0.7) * 0.01;

    bool onPiece = dm < 0.0015 * t + 0.003 && hid >= 0;
    if (!onPiece) col = kaEnv(rd, u, T, ec, P, warm, detail);
    if (onPiece) {
        vec3 pos = rd * t;
        vec4 c0 = vec4(0.0, 0.0, 1.0, 0.3), r0 = vec4(1.0, 0.0, 1.0, 0.0), m0 = vec4(0.0);
        for (int i = 0; i < KA_N; i++) {
            if (i == hid) { c0 = pc[i]; r0 = pr[i]; m0 = pk[i]; }
        }
        // normal of the hit piece from a four-tap tetrahedron in its local frame
        vec3 q = kaRot(pos - c0.xyz, r0);
        float sz = c0.w * m0.w;
        const vec2 e = vec2(0.002, -0.002);
        vec3 nl = e.xyy * kaPiece(q + e.xyy, m0.x, sz) + e.yyx * kaPiece(q + e.yyx, m0.x, sz)
                + e.yxy * kaPiece(q + e.yxy, m0.x, sz) + e.xxx * kaPiece(q + e.xxx, m0.x, sz);
        nl /= max(length(nl), 1e-6);
        // back to the chamber frame (inverse rotations, in reverse order)
        nl.yz = vec2(r0.z * nl.y + r0.w * nl.z, -r0.w * nl.y + r0.z * nl.z);
        vec3 n = vec3(r0.x * nl.x + r0.y * nl.y, -r0.y * nl.x + r0.x * nl.y, nl.z);

        // every fourth slot turns warm: amber glass among the cool pieces
        vec3 gc = mod(float(hid), 4.0) > 2.5 ? warm : kaPal(P, m0.y);
        float lvl = bandAt(u, m0.z);
        float light = 0.88 + 0.08 * u.level + 0.06 * lvl;
        float ndv = saturate(-dot(n, rd));
        float fres = 0.04 + 0.96 * pow(1.0 - ndv, 5.0);
        vec3 rf = reflect(rd, n);
        float spec = pow(saturate(dot(rf, L)), 120.0);
        // the mirror walls fold reflections back toward the end light
        vec3 env = kaEnvLite(vec3(rf.xy, abs(rf.z)), ec, u);
        vec3 sc;
        if (m0.x < 1.5) {
            // glass: the oil wheel seen through it, bent, split a little by
            // dispersion and tinted by the glass, deeper where it is thick;
            // the grazing rim turns dark as it mirrors the tube wall
            // Beer absorption: the longer the path, the deeper the colour
            vec3 tint = exp(-(1.0 - min(gc, vec3(0.97))) * (1.6 + 1.4 / max(ndv, 0.25))) * 1.7;
            // the scene behind sits close, so it shifts across the face
            vec2 par = (pos.xy - c0.xy) / max(c0.w, 1e-3) * 0.4;
            vec3 rr = refract(rd, n, 0.66);
            rr = dot(rr, rr) > 0.01 ? rr : rf;
            vec3 rrd = normalize(vec3(rr.xy * 1.3 + par, rr.z));
            vec3 back = kaEnv(rrd, u, T, ec, P, warm, detail) * 7.0;
            float rim = smoothstep(0.0, 0.35, ndv);
            // the frosted end glows behind every piece of glass, focused
            // to a bright heart where the face looks down the axis; blue
            // bends more than red, so the heart splits into a fringe
            float bl = exp(-dot(rrd.xy, rrd.xy) / max(rrd.z * rrd.z, 0.05) * 3.5);
            vec3 blc = vec3(bl);
            if (detail > 0.5) {
                vec3 rb = refract(rd, n, 0.62);
                rb = dot(rb, rb) > 0.01 ? rb : rf;
                vec3 rbd = normalize(vec3(rb.xy * 1.3 + par * 1.06, rb.z));
                float bb = exp(-dot(rbd.xy, rbd.xy) / max(rbd.z * rbd.z, 0.05) * 3.5);
                blc = vec3(bl, 0.5 * (bl + bb), bb);
            }
            back += 0.11 * blc * blc + 0.006;
            sc = tint * (back * 3.2 + 0.03) * light * (1.0 - fres) * (0.25 + 0.75 * rim);
            sc += env * fres * 1.2 + peakWhite(gc, 0.6) * spec * (1.0 + 0.1 * u.level);
            // the backlight focused through the glass: a soft caustic heart
            sc += tint * pow(ndv, 6.0) * 0.1 * light;
            vec3 aq = abs(q) / max(sz * 0.62, 1e-3);
            if (m0.x > 0.5) {
                // cut stone: light gathers in the edges and the chamfers
                float mid = max(min(aq.x, aq.y), min(max(aq.x, aq.y), aq.z));
                float edge = smoothstep(0.8, 0.97, mid);
                sc += peakWhite(gc, 0.4) * edge * (0.42 + 0.08 * lvl) * (0.3 + 0.7 * fres + 0.4 * ndv);
            } else {
                // marble: a twisted ribbon of colour glows inside
                float w = q.x * 0.8 + q.z * 0.6 + 0.35 * sin(q.y / max(sz, 1e-3) * 3.0);
                float rib = exp(-w * w / max(sz * sz, 1e-4) * 22.0);
                vec3 rc = kaPal(P, m0.y + 0.5);
                sc += rc * rc * rib * (0.6 + 0.1 * lvl) * (0.3 + 0.7 * ndv) * rim;
            }
        } else if (m0.x < 2.5) {
            // dichroic glass: its colour shifts with the viewing angle
            vec3 dc = kaPal(P, m0.y + 0.35 * ndv + 0.4 * fres);

            dc *= dc * 1.3;
            sc = dc * (0.06 + 0.3 * ndv * ndv + 1.4 * fres) * light + env * dc * 1.5
               + peakWhite(dc, 0.6) * spec * (1.0 + 0.1 * u.level);
        } else {
            // neon rod: emissive, hotter in its core
            sc = gc * gc * (1.55 + 0.15 * lvl) * (0.4 + 0.6 * ndv) + peakWhite(gc, 0.3) * ndv * ndv * 0.6;
        }
        float fogA = 1.0 - exp(-t * 0.18);
        col = mix(sc, fogCol, fogA);
    }
    col += glow;
    // every mirror bounce loses a little light; the joints between the
    // mirrors show as faint dark seams
    col *= pow(0.9, fz.z);
    float seam = min(f.y, 0.5 - f.x);
    seam = min(seam, abs(f.y - f.x * 0.5773503) * 0.8660254);
    col *= 0.82 + 0.18 * smoothstep(0.0, pix * 1.6 + 0.002, seam);
    // the mirror joints catch light: each beat sends a warm front out from
    // the axis across the cells, lighting the joints as it passes, and the
    // bass keeps a faint glow in them between beats. Thin lines only, so
    // the frame as a whole never flashes.
    float bp = saturate(u.beatPhase);
    float front = 0.1 + 2.0 * sqrt(bp);
    float wave = exp(-(r - front) * (r - front) * 40.0) * (1.0 - bp) * (1.0 - bp) * (0.3 + 0.05 * u.level);
    float line = exp(-seam / (pix * 1.2 + 1e-4)) + 0.15 * exp(-seam * 60.0);
    col += warm * line * (0.008 + 0.006 * u.level + 0.06 * wave);


    col *= 1.0 - 0.4 * smoothstep(0.7, 1.8, r);
    return kaOffRed(col); // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
