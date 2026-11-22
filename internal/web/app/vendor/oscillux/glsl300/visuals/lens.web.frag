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
// Lens.
// Sound as mass. Three dark bodies, weighed by the smoothed level, hang in
// front of a bright, deep field: a saturated emission nebula cut by black dust
// lanes, a dense star field and a scatter of small spiral galaxies. Each body
// bends that light with an exact thin-lens point-mass deflection, so stars and
// galaxies smear into arcs and Einstein rings, and farther layers bend more
// than nearer ones. Each body shows its black shadow and a thin photon ring.
// The field drifts past at a crawl behind the lenses and the bodies wander
// at a crawl with dimmer, slowly turning discs of soft streaks, so the arcs creep,
// and the view sways in a slow fixed roll. A slow cycle draws the bodies
// into an unhurried inspiral that merges them into one larger lens, then
// releases them; the level barely nudges it, so swells never jolt them. Each beat sends a faint gravitational ripple across the field; the
// level only breathes the masses, nebula, stars and rings a little, so the
// lensed field never lurches or flickers with the music.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Gravity ; param1 = Merge Eagerness ; param2 = Background Richness

// The palette read: the visual's own palette (1) with no beat or spectral
// nudge and no palette tour, so the host's vivid tour never swells the light;
// a host's manifest palette still replaces it. The fog tint is rebuilt on it.
vec3 lnPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 1, u); }
vec3 lnPalSteadyFog(float t, VisualUniforms u) {
    vec3 c = lnPalSteady(t, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}

float lnsFbm(vec2 p, int n) {
    float v = 0.0, a = 0.5, s = 0.0;
    for (int i = 0; i < 5; ++i) {
        if (i >= n) break;
        v += a * vnoise(p);
        s += a;
        p = FBM_OCTAVE * p;
        a *= 0.5;
    }
    return v / s;
}

// Gaussian stars, one per cell. foot is the pixel's footprint in source units:
// where the lens squeezes the field, each star widens and dims (same energy),
// so the demagnified zones never sparkle.
float lnsStars(vec2 s, float cells, float seed, float foot, float tw) {
    vec2 g = s * cells + seed;
    vec2 id = floor(g);
    vec2 d = fract(g) - 0.5 - (hash22(id + seed * 1.3) - 0.5) * 0.5;
    float h = hash21(id + 0.37 * seed);
    float sig0 = 0.055;
    float sig = max(sig0, foot * cells * 0.5);
    float amp = (sig0 * sig0) / (sig * sig);
    float h2 = h * h * h;
    float b = (0.006 + 5.0 * h2 * h2 * h2 * h2) * step(0.45, hash21(id + 4.3));
    b *= 1.0 + tw * (hash21(id + 11.0) - 0.35);
    return b * amp * exp(-dot(d, d) / (2.0 * sig * sig));
}

// Small spiral galaxies, about half the cells: an inclined disc with two arms
// and a hot core. The lens stretches them into the classic arcs.
vec3 lnsGalaxies(vec2 s, float foot, VisualUniforms u) {
    const float cells = 2.6;
    vec2 g = s * cells + 0.5;
    vec2 id = floor(g);
    vec2 h = hash22(id + 17.0);
    vec2 d = fract(g) - 0.5 - (hash22(id + 3.7) - 0.5) * 0.35;
    // Fade before the cell edge so a wide disc is never clipped square.
    float win = smoothstep(0.5, 0.3, max(abs(fract(g).x - 0.5), abs(fract(g).y - 0.5)));
    d = rot2(h.y * 6.2831853) * d;
    d.y *= 1.3 + 1.1 * h.x;
    float sz = 0.06 + 0.06 * hash21(id + 9.1);
    float sig = max(sz, foot * cells * 0.6);
    float keep = (sz * sz) / (sig * sig);
    float r2 = dot(d, d) / (sig * sig);
    float r = sqrt(r2);
    float arms = 0.55 + 0.45 * sin(2.0 * atan2_(d.y, d.x) - 3.0 * log(r + 0.25) * 1.6);
    float disc = exp(-r2 * 0.7) * mix(0.55, arms, smoothstep(0.3, 1.2, r));
    float core = exp(-r2 * 5.0);
    vec3 c = lnPalSteady(fract(h.y * 2.7 + 0.15), u);
    float on = smoothstep(0.58, 0.64, h.x);
    return on * keep * win * (c * disc * 2.2 + vec3(1.0, 0.93, 0.84) * core * 3.0);
}

// The emission nebula: warped fbm clouds over black space, two palette hues,
// a brighter ionisation front along each cloud's edge, hot knots, and broad
// dust lanes that cut it to black.
vec3 lnsNebula(vec2 s, VisualUniforms u, int oct, float rich, float glow, float foot) {
    vec2 w = vec2(lnsFbm(s * 0.55, 2), lnsFbm(s * 0.55 + vec2(4.1, 2.3), 2));
    vec2 q = s * 1.1 + 1.8 * w;
    float n = lnsFbm(q, oct);
    float cloud = saturate((n - 0.47) * 3.2);
    cloud *= cloud;
    // Hue walks along the palette (never an RGB mix of opposite hues, which
    // greys out), squared so the emission is deep and saturated.
    float th = mix(0.04 + 0.25 * w.x, 0.62 + 0.30 * w.y, smoothstep(0.42, 0.58, w.x + 0.5 * (n - 0.5)));
    vec3 c = lnPalSteady(th, u);
    c *= c;
    vec3 cF = lnPalSteady(th + 0.04, u);
    cF = mix(cF * cF, vec3(1.0, 0.92, 0.85), 0.12);
    float gain = (0.55 + 0.45 * rich) * (0.8 + 0.5 * glow);
    // Inner structure: a finer field gives each cloud pillars and knots.
    float fine = lnsFbm(q * 2.3 + w * 2.0 + vec2(3.0, 8.0), max(oct - 1, 2));
    vec3 col = c * (0.05 * cloud + 0.36 * cloud * cloud) * mix(0.06, 2.0, saturate((fine - 0.34) * 3.0)) * gain;
    // Filaments: thin bright ridges of the fine field run through each cloud.
    float fil = 1.0 - abs(2.0 * fine - 1.0);
    fil *= fil;
    fil *= fil;
    col += mix(c, cF, 0.5) * fil * fil * cloud * 0.55 * gain;
    // Ionisation front: a thin bright skin where each cloud meets the void.
    float front = cloud * (1.0 - cloud);
    col += cF * front * (0.08 + 1.1 * front) * gain;
    col += mix(c, vec3(1.0, 0.9, 0.8), 0.25) * pow(smoothstep(0.70, 0.95, n), 2.0) * 1.3 * rich;
    // Broad dust: large dark lanes with soft, wispy edges.
    float dust = lnsFbm(s * 1.4 + 1.3 * w + vec2(7.0, 1.0), max(oct - 1, 2));
    col *= 1.0 - 0.96 * smoothstep(0.50, 0.64, dust);
    // Where the lens squeezes the field (inside the Einstein rings) the fine
    // structure would alias; it sinks into shadow instead.
    col *= 1.0 - 0.85 * smoothstep(0.012, 0.05, foot);
    // Faint cold space so nothing reads as flat black.
    col += lnPalSteady(0.75, u) * 0.003;
    return col;
}

// A thin accretion disc, seen in its own plane. b is the deprojected position
// in units of the body's Einstein radius and fr the pixel footprint along the
// radius in the same units. The disc runs in lanes; each lane turns rigidly at
// a whole number of turns per 1200 s (so the time wrap is seamless), inner
// lanes faster, Kepler-like but unhurried, and the lane gaps hide the seams
// between them; the lane streaks are soft so the turning disc never shimmers.
// The side turning toward the viewer is beamed brighter and bluer.
// Sparse bright stars with six-point diffraction spikes (three axes, the
// spike width and energy kept under the lens footprint so they never alias).
vec3 lnsSpiked(vec2 s, float foot, float treble, VisualUniforms u) {
    const float cells = 6.0;
    vec2 g = s * cells + 0.37;
    vec2 id = floor(g);
    float h = hash21(id + 23.0);
    if (h < 0.86) return vec3(0.0);
    vec2 d = fract(g) - 0.5 - (hash22(id + 8.1) - 0.5) * 0.2;
    float edge = smoothstep(0.5, 0.36, max(abs(fract(g).x - 0.5), abs(fract(g).y - 0.5)));
    float w0 = 0.006;
    float w = max(w0, foot * cells * 0.6);
    float amp = w0 / w;
    float sp = 0.0;
    for (int k = 0; k < 3; ++k) {
        float an = 0.5236 + 1.0471976 * float(k);
        vec2 e = vec2(cos(an), sin(an));
        float al = dot(d, e);
        float pe = dot(d, vec2(-e.y, e.x));
        sp += exp(-pe * pe / (w * w)) * exp(-abs(al) * 14.0);
    }
    float core = exp(-dot(d, d) / (w * w * 4.0)) * 6.0 + exp(-dot(d, d) * 900.0) * 0.6;
    float b = (0.2 + (h - 0.86) * 4.5) * (0.9 + 0.3 * treble);
    vec3 tint = mix(vec3(1.0, 0.96, 0.92), lnPalSteady(fract(h * 7.3), u), 0.35);
    return tint * b * edge * (core + 0.9 * sp) * amp;
}

vec3 lnsDisc(vec2 b, float fr, float tt, float seed, vec3 hot, vec3 warm, float hi) {
    const float rIn = 0.50, rOut = 1.22, lanes = 13.0;
    float rd = length(b);
    float x = (rd - rIn) / (rOut - rIn);
    if (x <= 0.0 || x >= 1.0) return vec3(0.0);
    float frx = fr / (rOut - rIn);
    float prof = smoothstep(0.0, 0.04 + frx, x) * pow(1.0 - x, 1.4) / (0.22 + 1.6 * x);
    float ln = x * lanes;
    float k = floor(ln);
    float f = ln - k;
    float rk = rIn + (k + 0.5) / lanes * (rOut - rIn);
    float turns = floor(16.0 * pow(rIn / rk, 1.5) + 0.5);
    vec2 q = rot2(turns * 0.0052359878 * tt + seed) * b;
    float n = vnoise(q * 6.0 + vec2(seed * 3.1 + k * 1.7, k * 0.9));
    if (hi > 0.5) n = 0.62 * n + 0.38 * vnoise(q * 15.0 + vec2(k * 2.3, seed));
    float laneC = saturate(1.6 - 2.2 * frx * lanes);
    float gap = mix(1.0, 0.55 + 0.45 * smoothstep(0.0, 0.22, f) * smoothstep(1.0, 0.78, f), laneC);
    float streak = mix(0.75, 0.6 + 0.7 * n * n, mix(0.5, 1.0, laneC));
    float dop = 1.0 - 0.45 * b.x / max(rd, 1e-3);
    vec3 c = mix(warm, hot, saturate(1.15 - 1.6 * x));
    c = mix(c, vec3(0.80, 0.90, 1.15) * dot(c, vec3(0.4)), 0.7 * saturate(dop - 1.0));
    c = mix(c, warm * vec3(1.0, 0.45, 0.35), 0.8 * saturate(1.0 - dop));
    return c * prof * gap * streak * dop * dop * dop;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    float detail = osDetail(u);
    int oct = detail > 0.5 ? 5 : 3;
    float hi = step(0.5, detail);

    float grav = mix(0.55, 1.60, u.param0);
    float eager = mix(-0.06, 0.10, u.param1);
    float rich = mix(0.2, 1.0, u.param2);

    float mid = saturate(u.mid);
    float treble = saturate(u.treble);
    float level = saturate(u.level);
    float tt = mod(u.time, 1200.0);

    // The field drifts steadily behind the lenses; slow eased roll.
    float dist = u.time * 0.05;
    p = rot2(0.02 * sin(tt * 0.0418879)) * p;
    vec2 head = vec2(0.96, 0.28);

    // Sustained energy inspirals the bodies; a slow cycle (7 turns per 1200 s)
    // sets its pace; the level only nudges it, so a swell never throws the
    // bodies inward in a lurch.
    float sustain = (0.5 + 0.5 * sin(tt * 0.0366519)) * (0.97 + 0.03 * level);
    float merge = smoothstep(0.55 - eager, 0.95 - eager, sustain);
    float orbit = 1.0 - 0.93 * merge;
    float ph = tt * 0.0104720;
    vec2 c0 = orbit * vec2(0.78 * sin(ph * 0.9 + 0.4), 0.34 * sin(ph * 1.3 + 1.1));
    vec2 c1 = orbit * vec2(0.92 * sin(ph * 0.7 + 2.6), 0.42 * sin(ph * 1.1 + 4.0));
    vec2 c2 = orbit * vec2(0.70 * sin(ph * 1.2 + 4.4), 0.46 * sin(ph * 0.8 + 2.2));
    // Einstein radii: the level, mids and treble give each body its mass
    // (shape, not camera).
    float t0 = grav * 0.30 * (0.97 + 0.03 * level);
    float t1 = grav * 0.23 * (0.97 + 0.03 * level);
    float t2 = grav * 0.17 * (0.97 + 0.03 * level);

    // Deflection and its analytic Jacobian; shadows and photon rings.
    vec2 a = vec2(0.0);
    vec3 jac = vec3(0.0); // (dax/dx, day/dy, dax/dy)
    float sd = 10.0; // signed distance to the union of the shadows
    float bw = 0.0, bs = 0.0;
    vec2 cm = vec2(0.0);
    float m2 = 0.0;
    float bp = saturate(u.beatPhase);
    float bell = sin(3.14159265 * bp);
    bell *= bell;
    for (int i = 0; i < 3; ++i) {
        vec2 c = i == 0 ? c0 : i == 1 ? c1 : c2;
        float te = i == 0 ? t0 : i == 1 ? t1 : t2;
        vec2 d = p - c;
        float r2 = dot(d, d) + 1e-4;
        float k = te * te / r2;
        a += k * d;
        jac += k / r2 * vec3(r2 - 2.0 * d.x * d.x, r2 - 2.0 * d.y * d.y, -2.0 * d.x * d.y);
        float r = sqrt(r2);
        float sdi = r - 0.34 * te;
        // Close passes blend the shadows like liquid, under one outline.
        sd = osSmin(sd, sdi, 0.04);
        // Each ring is brighter on its approaching side, which turns slowly.
        float sp = tt * (0.0523599 + 0.0130900 * float(i)) + 2.1 * float(i);
        float wi = exp(-max(sdi, 0.0) * 30.0);
        bs += wi * (0.45 + 0.55 * dot(d / r, vec2(cos(sp), sin(sp))));
        bw += wi;
        cm += te * te * c;
        m2 += te * te;
    }
    // As the bodies merge their shadows fuse into one larger body.
    cm /= max(m2, 1e-4);
    float tm = sqrt(m2);
    float fuse = smoothstep(0.6, 0.98, merge);
    sd = mix(sd, osSmin(sd, length(p - cm) - 0.34 * tm, 0.04), fuse);
    float beam = bs / max(bw, 1e-4);
    float shadow = smoothstep(-0.003, 0.004, sd);
    // A thin photon ring hugging the shadow, plus a soft glow just outside.
    float xr = (sd - 0.006) / 0.005;
    float ring = (exp(-xr * xr) + 0.12 * exp(-abs(sd) / 0.03)) * (0.35 + beam * beam);
    // Source footprint of one pixel: pixel size times the RMS stretch.
    float px = 2.0 / max(u.resolution.y, 1.0);
    float j00 = 1.0 - jac.x, j11 = 1.0 - jac.y, j01 = -jac.z;
    float foot = px * sqrt(0.5 * (j00 * j00 + j11 * j11 + 2.0 * j01 * j01));
    foot = min(foot, 0.5);

    // Gravitational waves: every beat one body (the merged one, once fused,
    // and harder) sends a ripple across the field, easing in and out within
    // the beat. It shifts the background
    // radially and slightly magnifies and demagnifies it as it passes.
    float bsel = mod(floor(u.beatCount), 3.0);
    vec2 gc = mix(bsel < 0.5 ? c0 : bsel < 1.5 ? c1 : c2, cm, fuse);
    vec2 gd = p - gc;
    float gr = length(gd) + 1e-4;
    float gx = (gr - 0.12 - 1.9 * bp) / 0.07;
    float genv = bell * (0.02 + 0.01 * level) * (1.0 + 0.5 * fuse);
    float gw = genv * exp(-gx * gx);
    a += gd / gr * (0.006 * gw * sin(gx * 2.6));
    float gmag = 1.0 + 0.08 * gw * cos(gx * 2.6);

    float glow = 0.4 + 0.15 * level;
    // Nebula (nearest of the sources, bends least).
    vec2 sN = p - a * 0.78 + head * dist * 0.045;
    int octN = foot > 0.02 ? 2 : oct;
    vec3 col = lnsNebula(sN, u, octN, rich, glow, foot);

    // Stars and galaxies, farther, bend more. Where the bending is strong each
    // channel of the star field bends a touch differently, a chromatic fringe
    // on the arcs; far from the bodies the stars stay clean.
    float disp = 0.035 * hi * smoothstep(0.10, 0.45, length(a));
    vec3 kc = vec3(1.0 - disp, 1.0, 1.0 + disp);
    vec3 starTint = mix(vec3(1.0), lnPalSteady(0.55, u), 0.25);
    vec2 dS = head * dist * 0.075;
    vec2 dG = head * dist * 0.060 + vec2(3.1, 7.7);
    float twk = 0.1;
    vec3 st, gxy;
    st.g = lnsStars(p - a * 0.92 * kc.y + dS, 38.0, 1.0, foot, twk);
    gxy = lnsGalaxies(p - a + dG, foot, u);
    if (hi > 0.5) {
        st.r = lnsStars(p - a * 0.92 * kc.x + dS, 38.0, 1.0, foot, twk);
        st.b = lnsStars(p - a * 0.92 * kc.z + dS, 38.0, 1.0, foot, twk);
        st += lnsStars(p - a * 0.95 + dS * 0.8, 21.0, 5.0, foot, twk) * 0.9;
    } else {
        st.r = st.g;
        st.b = st.g;
    }
    col += st * starTint * (1.0 + 0.1 * level) * (0.5 + 0.5 * rich);
    col += gxy * (0.6 + 0.4 * rich);
    // A few bright foreground stars with six-point diffraction spikes; they
    // are lensed with the field, so near a body the spikes bend into arcs.
    col += lnsSpiked(p - a * 0.9 + dS * 1.15, foot, 0.4 + 0.1 * level, u) * (0.6 + 0.4 * rich);
    col *= gmag;

    // The bodies: black shadows rimmed by a thin photon ring.
    col *= shadow;
    vec3 ringCol = mix(lnPalSteady(0.30, u), vec3(1.0, 0.92, 0.8), 0.45);
    col += ringCol * ring * (0.7 + 0.2 * level + 0.05 * bell);

    // Accretion discs, nearly edge on. The near half crosses in front of the
    // shadow; the far half is hidden behind it, but its light, bent over the
    // top and under the bottom of the shadow, wraps it as a halo. As the bodies
    // fuse, their discs settle into one disc around the merged body, which
    // flares as it forms.
    vec3 hotC = mix(lnPalSteady(0.25, u), vec3(1.0, 0.95, 0.85), 0.45);
    vec3 warmC = lnPalSteady(0.06, u);
    float flare = 0.5 * fuse * (1.0 - fuse) + 0.2 * fuse;
    vec3 discs = vec3(0.0);
    for (int i = 0; i < 3; ++i) {
        float fi = float(i);
        vec2 c = mix(i == 0 ? c0 : i == 1 ? c1 : c2, cm, fuse);
        float te = mix(i == 0 ? t0 : i == 1 ? t1 : t2, tm, fuse);
        float ci = mix(0.17 + 0.05 * fi, 0.19, fuse);
        float psi = mix(0.22 - 0.31 * fi + 0.05 * sin(tt * 0.0157080 + fi), 0.08, fuse);
        float gain = 1.0 * (0.9 + 0.1 * level) * mix(1.0, 0.3333, fuse) * (1.0 + 1.6 * flare);
        vec2 dl = rot2(-psi) * (p - c) / te;
        dl.y = -dl.y; // the image's y runs down; the far side is up on screen
        float rho = length(dl) + 1e-4;
        float pxe = px / te;
        float seed = fi * 1.7;
        // Direct image.
        vec2 b = vec2(dl.x, dl.y / ci);
        float rb = length(b) + 1e-4;
        float fr = pxe * length(vec2(b.x, b.y / ci)) / rb;
        float behind = step(0.0, b.y) * smoothstep(0.34, 0.34 + 1.5 * pxe, rho);
        float front = 1.0 - step(0.0, b.y);
        vec3 dc = lnsDisc(b, fr, tt, seed, hotC, warmC, hi) * (front + behind);
        // Halo: the far side over the top, a thinner underside image below.
        vec2 dir = dl / rho;
        float wh = 0.30 * pow(max(dir.y, 0.0), 0.8) + 0.08 * max(-dir.y, 0.0) + 0.05;
        float sh = (rho - 0.352) / wh;
        vec3 hc = vec3(0.0);
        if (sh > 0.0 && sh < 1.0) {
            vec2 bh = dir * (0.50 + sh * 0.72);
            hc = lnsDisc(bh, pxe * 0.72 / wh, tt, seed, hotC, warmC, hi);
            hc *= mix(0.5, 1.15, saturate(0.5 + 0.5 * dir.y));
        }
        // Soft corona of scattered disc light around the body.
        float cor = exp(-dot(b, b) * 1.6) * smoothstep(0.34, 0.6, rho);
        vec3 corona = hotC * cor * 0.05 * (1.0 - 0.35 * dl.x / rho);
        discs += (dc + hc + corona) * gain;
    }
    col += discs;

    // Gentle lens falloff.
    col *= 1.0 - 0.25 * smoothstep(0.9, 2.1, length(p));
    return col; // LINEAR HDR
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
