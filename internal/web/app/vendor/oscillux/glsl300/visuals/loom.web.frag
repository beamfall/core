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
// Loom.
// A slow glide over an endless loom at night. Fibre-bright warp threads stretch
// to a fog-lit vanishing point and glow with the spectrum, bass in the middle,
// treble at the selvedges. Ahead of the camera a softly glowing spindle
// shuttle makes one unhurried pass across the cloth every four seconds at the
// loom's own steady tempo, laying fresh weft that glows warm and cools into a
// dark dyed twill as it passes beneath. Both sides are lined with tall loom
// frames strung with heddle wires; each bay glows steadily and lifts only a
// little with the music's level and its band. Ripples travel away through the
// satin sheen. The glide is slow and level so the fine wires never strobe;
// audio drives light gently, never the camera, and no large area brightens on
// the kick. Reds stay just off saturated red.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Weave Density ; param1 = Flight Speed ; param2 = Ripple

// The palette read: the visual's own palette (3) with no beat or spectral
// nudge and no palette tour, so the host's vivid tour never swells the light;
// a host's manifest palette still replaces it. The fog tint is rebuilt on it.
vec3 lmPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 3, u); }
vec3 lmPalSteadyFog(float t, VisualUniforms u) {
    vec3 c = lmPalSteady(t, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}

const float LM_HALF = 9.0;    // half cloth width
const float LM_CELL = 9.0;    // loom frame spacing along z
const float LM_POSTX = 11.5;  // post centre offset from the cloth centre
const float LM_H = 9.0;       // frame height
const float LM_ROW = 0.12;    // weft row pitch
const float LM_WRAP = 1260.0; // the whole scene repeats every LM_WRAP along z
const float LM_LEAD = 6.5;    // shuttle lead ahead of the camera
const float LM_SHED = 7.5;    // height of the raised warp sheet
const float LM_RISE = 12.0;   // length over which it rises from the fell

float lmMap(vec3 p) {
    float ax = abs(p.x);
    float cz = floor(p.z / LM_CELL);
    float lz = p.z - (cz + 0.5) * LM_CELL;
    float d = osBox(vec3(ax - LM_POSTX, p.y - LM_H * 0.5, lz), vec3(0.3, LM_H * 0.5, 0.3));
    // the top rails and the cloth-edge rails run the whole length
    d = min(d, length(vec2(ax - LM_POSTX, p.y - LM_H)) - 0.16);
    d = min(d, length(vec2(ax - LM_HALF - 0.5, p.y - 0.16)) - 0.11);
    // the cell boundary bounds the step so neighbours are never jumped over
    float zb = (cz + 1.0) * LM_CELL - p.z;
    float za = p.z - cz * LM_CELL;
    return min(d, max(min(za, zb), 0.0) + 0.3);
}

vec3 lmNormal(vec3 p) {
    vec2 e = vec2(0.003, -0.003);
    vec3 n = e.xyy * lmMap(p + e.xyy) + e.yyx * lmMap(p + e.yyx) +
             e.yxy * lmMap(p + e.yxy) + e.xxx * lmMap(p + e.xxx);
    return n / max(length(n), 1e-6);
}

float lmMarch(vec3 ro, vec3 rd, float t0, float tmax, int n) {
    float t = t0;
    for (int i = 0; i < 96; i++) {
        if (i >= n) break;
        float d = lmMap(ro + rd * t);
        if (d < 0.0015 * t) return t;
        t += d * 0.9;
        if (t > tmax) break;
    }
    return -1.0;
}

// Closest approach between a ray and the segment a0 + ba*h, h in [0, 1]:
// returns the distance, with the ray depth and segment parameter.
float lmSegDist(vec3 ro, vec3 rd, vec3 a0, vec3 ba, out float tr, out float hs) {
    vec3 oa = ro - a0;
    float bb = dot(ba, ba), rb = dot(rd, ba), orr = dot(oa, rd), ob = dot(oa, ba);
    hs = clamp((ob - rb * orr) / max(bb - rb * rb, 1e-4), 0.0, 1.0);
    tr = max(rb * hs - orr, 0.0);
    return length(oa + rd * tr - ba * hs);
}

// Keeps every red just off saturated red once tonemapped: green (for warm
// reds) or blue (for pink ones) rises to at least 0.3 of red, so no red area
// can strobe, while the hue barely moves.
float lmTone(float x) { return x * (2.51 * x + 0.03) / (x * (2.43 * x + 0.59) + 0.14); }
float lmToneInv(float y) {
    y = clamp(y, 0.0, 0.999);
    float a = 2.51 - 2.43 * y, b = 0.03 - 0.59 * y;
    return (-b + sqrt(b * b + 0.56 * a * y)) / (2.0 * a);
}
vec3 lmOffRed(vec3 c) {
    float r = lmTone(c.r), g = lmTone(c.g), b = lmTone(c.b);
    float need = 0.3 * r - g - b;
    if (need <= 0.0) return c;
    if (c.g > c.b) c.g = max(c.g, lmToneInv(g + need));
    else c.b = max(c.b, lmToneInv(b + need));
    return c;
}

// Peak and rim tints without the shared roles' onset/flux whitening, which
// would flash the vanishing-point glow and every post on the kick.
// The library nudges every palette read with the beat phase and the spectral
// centroid; that is undone here, so the whole frame never shifts colour with
// the music's timbre or snaps back on each beat (manifest mode has no nudge).
vec3 lmPal(float t, VisualUniforms u) {
    return lmPalSteady(t, u);
}

vec3 lmFog(float t, VisualUniforms u) {
    vec3 c = lmPal(t, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}
vec3 lmPeak(float t, VisualUniforms u) { return lmPal(t + 0.028, u); }
vec3 lmRim(float t, VisualUniforms u) { return mix(lmPal(t, u), lmPeak(t + 0.18, u), 0.5); }

// Band shown by the frame bay starting at cell cz.
float lmBayBand(VisualUniforms u, float cz) {
    return bandAt(u, fract(mod(cz, 12.0) / 12.0 + 0.04));
}

// Light emitted by a post or rail.
vec3 lmFrameGlow(vec3 pos, vec3 n, VisualUniforms u) {
    float ax = abs(pos.x);
    float cz = floor(pos.z / LM_CELL);
    float lz = pos.z - (cz + 0.5) * LM_CELL;
    float onPost = step(abs(ax - LM_POSTX), 0.38) * step(abs(lz), 0.38) * step(pos.y, LM_H - 0.2);
    // thin edges up the post corners and a hot cap
    float corner = 1.0 - smoothstep(0.0, 0.035, max(abs(abs(ax - LM_POSTX) - 0.3), abs(abs(lz) - 0.3)));
    vec3 col = onPost * corner * lmRim(0.62, u) * 1.6;
    float cap = exp(-abs(pos.y - LM_H + 0.25) * 30.0) * onPost;
    col += cap * lmPeak(0.3, u) * (1.0 + 0.1 * u.level);
    // rails: the top ones glow steadily, the cloth-edge ones carry dashes fixed
    // to the world so they stream past at the glide speed
    float railTop = step(LM_H - 0.2, pos.y) * (1.0 - onPost);
    col += railTop * lmPal(0.55, u) * 1.6;
    float railEdge = step(pos.y, 0.32) * step(abs(ax - LM_HALF - 0.5), 0.16);
    float dash = smoothstep(0.55, 0.8, abs(fract(pos.z * 0.2) - 0.5) * 2.0);
    col += railEdge * lmPal(0.2, u) * (0.25 + 1.6 * dash);
    return col;
}

// Heddle wires strung between the posts on the plane x = side * LM_POSTX: a
// level meter per bay, lit from the floor up to its band's level.
vec3 lmHeddles(vec3 ro, vec3 rd, float side, float tmax, VisualUniforms u, float detail) {
    // no early exits: fwidth below needs uniform control flow
    float t = (side * LM_POSTX - ro.x) / (abs(rd.x) > 1e-4 ? rd.x : 1e-4);
    t = clamp(t, -1.0, 1e3);
    vec3 q = ro + rd * t;
    float mask = step(0.0, t) * step(t, tmax) * step(0.0, q.y) * step(q.y, LM_H);
    float cz = floor(q.z / LM_CELL);
    float lz = q.z - cz * LM_CELL;
    float inBay = smoothstep(0.35, 0.6, lz) * (1.0 - smoothstep(LM_CELL - 0.6, LM_CELL - 0.35, lz));
    float wz = q.z / 0.3;
    float wire = 0.5 - 0.5 * cos(6.2831853 * wz);
    float fw = smoothstep(0.06, 0.3, fwidth(wz));
    wire = mix(pow(wire, 10.0), 0.176, fw);
    float lvl = saturate(0.45 + 0.1 * u.level + 0.02 * lmBayBand(u, cz));
    float h01 = q.y / LM_H;
    float lit = 1.0 - smoothstep(lvl - 0.03, lvl + 0.01, h01);
    float head = exp(-abs(h01 - lvl) * 60.0);
    vec3 c = lmPal(0.12 + 0.6 * h01 + 0.05 * 0.35, u);
    c = accentize(c, u.accent, 0.12);
    // the eyes of the heddles: a bright bead at mid height on every wire
    float eye = exp(-abs(h01 - 0.5) * 90.0) * wire * 1.5;
    vec3 col = c * inBay * (wire * (0.15 + 1.1 * lit) + head * (0.3 + 0.9 * wire)) + lmRim(0.6, u) * eye * inBay * 0.4;
    return col * mask * exp(-max(t, 0.0) * 0.02);
}

// Air glow: dark night with a bright vanishing point and faint shafts fanning
// out from it.
vec3 lmHaze(vec3 rd, VisualUniforms u, float withShafts) {
    float a = max(1.0 - rd.z, 0.0);           // about half the squared angle
    float vp = exp(-a * 900.0);
    float near = exp(-a * 60.0);
    float wide = exp(-a * 8.0);
    float band = exp(-abs(rd.y + 0.01) * 30.0);
    vec3 glow = lmPeak(0.34 + 0.06 * 0.35, u);
    vec3 col = lmFog(0.8, u) * (0.002 + 0.05 * wide * band);
    col += glow * (1.5 * vp + 0.32 * near + 0.18 * wide * band) * (0.95 + 0.05 * u.level);
    if (withShafts > 0.5 && rd.y > -0.01) {
        float ang = atan2_(rd.y + 0.02, rd.x);
        float shafts = vnoise(vec2(ang * 9.0, mod(u.time * 0.15, 400.0))) * vnoise(vec2(ang * 23.0 + 5.0, 1.0));
        col += lmPal(0.7, u) * shafts * near * 0.2;
    }
    return col;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);

    float density = mix(90.0, 200.0, u.param0);
    float speed = mix(0.04, 0.1, u.param1);
    float rippleAmt = mix(0.2, 1.6, u.param2);

    float z = mod(140.0 + u.time * speed, LM_WRAP);
    float w = 6.2831853 / LM_WRAP;
    float sway = 2.2 * sin(z * w * 7.0) + 0.8 * sin(z * w * 17.0 + 1.0);
    float alt = 2.3 + 0.5 * sin(z * w * 9.0 + 2.0);
    vec3 ro = vec3(sway, alt, z);
    vec3 ta = vec3(0.5 * sway, 1.1, z + 14.0);
    float roll = -0.025 * sway;
    vec3 rd = osCamera(ro, ta, roll, 1.15, p);

    // the cloth plane, computed in uniform control flow for fwidth
    float tp = rd.y < -1e-4 ? ro.y / -rd.y : 1e4;
    tp = min(tp, 1e4);
    vec3 hp = ro + rd * tp;
    float s = (hp.x + LM_HALF) / (2.0 * LM_HALF) * density;
    float r = hp.z / LM_ROW;
    float fs = smoothstep(0.06, 0.3, fwidth(s));
    float fr = smoothstep(0.12, 0.5, fwidth(r));

    // shuttle: one unhurried pass across the cloth every four seconds, easing at
    // the edges. It keeps the loom's own steady tempo (two picks a second): the
    // beat counter can jump when the beat tracker re-locks, which would throw
    // the shuttle and its light across the cloth in one frame.
    float sb = mod(u.time * 2.0, 64.0);
    float xS = LM_HALF * 0.94 * cos(0.39269908 * sb);
    float dirS = -sin(0.39269908 * sb);
    float zS = z + LM_LEAD;
    vec3 Ls = vec3(xS, 0.35, zS);
    // the shuttle burns in the palette's far hue (amber in deepSea) so it stands apart
    vec3 shCol = mix(lmPal(0.93, u), vec3(1.0), 0.2);
    float shPow = 0.85;
    // each reversal at a selvedge (every four seconds) beats up the pick: a soft glow
    // of light at that edge that dies away over the next beat
    float rev = floor(sb * 0.125 + 0.5) * 8.0;
    float since = sb - rev;
    float pick = since >= 0.0 ? smoothstep(0.0, 0.3, since) * exp(-since * 2.0) : 0.0;
    vec3 Lf = vec3(LM_HALF * 0.94 * (mod(rev, 16.0) < 1.0 ? 1.0 : -1.0), 0.3, zS);

    vec3 col;
    float tHit = tp;
    // structure lives only in the side slabs |x| in [HALF+0.35, POSTX+0.35]
    // below the top rail: march just the stretch of the ray inside them
    float sx = rd.x < 0.0 ? -1.0 : 1.0;
    float rdx = max(abs(rd.x), 1e-4);
    float tIn = max((LM_HALF + 0.35 - ro.x * sx) / rdx - 0.05, 0.05);
    float tOut = (LM_POSTX + 0.35 - ro.x * sx) / rdx;
    if (rd.y > 1e-4) tOut = min(tOut, (LM_H + 0.25 - ro.y) / rd.y);
    tOut = min(min(tOut, tp), 140.0);
    float t = tIn < tOut ? lmMarch(ro, rd, tIn, tOut, 44) : -1.0;
    if (t > 0.0) {
        tHit = t;
        vec3 pos = ro + rd * t;
        vec3 n = lmNormal(pos);
        vec3 l = Ls - pos;
        float ld = dot(l, l);
        float dif = saturate(dot(n, l * inversesqrt(max(ld, 1e-4))));
        col = lmFrameGlow(pos, n, u);
        col += shCol * shPow * dif * 0.9 / (1.0 + ld);
        col += lmHaze(reflect(rd, n), u, 0.0) * 0.3 * pow(1.0 - saturate(-dot(rd, n)), 3.0);
        col += vec3(0.004, 0.004, 0.007);
    } else if (tp < 1e3) {
        float ax = abs(hp.x);
        float behind = zS - hp.z;   // > 0 where the cloth is already woven
        float wovenM = smoothstep(-0.1, 0.1, behind);
        float onCloth = 1.0 - smoothstep(LM_HALF - 0.05, LM_HALF + 0.05, ax);

        float sid = floor(s);
        float rid = floor(r);
        float acr = abs((sid + 0.5) / density * 2.0 - 1.0);
        float bnd = bandAt(u, acr);

        // thread profiles fade to their mean where the weave is finer than a pixel
        float warpP = 0.5 - 0.5 * cos(6.2831853 * s);
        float weftP = 0.5 - 0.5 * cos(6.2831853 * r);
        float fibre = mix(pow(warpP, 16.0), 0.1399, fs);
        warpP = mix(warpP, 0.5, fs);
        weftP = mix(weftP, 0.5, fr);
        float fsr = max(fs, fr);
        float twill = mix(step(mod(sid + rid, 4.0), 1.5), 0.5, fsr);

        vec3 warpCol = lmPal(0.05 + 0.55 * acr + 0.06 * 0.35, u);
        warpCol = accentize(warpCol, u.accent, 0.12);
        float warpE = 0.7 + 0.12 * u.level + 0.04 * bnd;

        // weft: dark dyed stripes with a diamond motif, hot where freshly laid
        float grp = floor(mod(rid, 10500.0) / 14.0);
        vec3 dye = lmPal(0.15 + 0.7 * hash11(grp * 1.37 + 0.5), u) * 0.012;
        float dm = abs(mod(s, 20.0) - 10.0) + abs(mod(r, 20.0) - 10.0);
        float motif = mix(1.0 - smoothstep(0.5, 1.0, abs(dm - 7.0)), 0.1, fsr);
        dye += lmPal(0.6 + 0.25 * hash11(grp * 3.1), u) * motif * (0.12 + 0.04 * u.level);
        float fresh = exp(-max(behind, 0.0) / 0.9);
        vec3 hot = lmPal(0.62 + 0.3 * fract(rid * 0.137), u) * (1.5 + 0.12 * u.level);
        vec3 weftCol = mix(dye, hot, fresh) * (0.35 + 0.65 * weftP);

        // woven: the warp glows where it floats over the weft; bare: free fibres
        vec3 warpGlow = warpCol * warpE * fibre;
        vec3 woven = mix(weftCol, warpGlow * 1.4 + warpCol * 0.02 * warpP, twill);
        vec3 bare = warpGlow * 1.8;
        vec3 cloth = mix(bare, woven, wovenM);

        // the shuttle lights a pool on the cloth and trails a hot weft line
        vec3 l = Ls - hp;
        float ld = dot(l, l);
        cloth += shCol * shPow * (0.03 + 0.2 * wovenM * weftP) * 1.0 / (1.0 + 3.0 * ld);
        float line = exp(-abs(hp.z - zS) * 16.0) * exp(-max((xS - hp.x) * dirS, 0.0) * 0.35)
                   * step(0.0, (xS - hp.x) * dirS + 0.2);
        cloth += mix(shCol, vec3(1.0), 0.25) * line * 1.5;
        cloth += shCol * exp(-abs(hp.z - zS) * 3.5) * exp(-abs(xS - hp.x) * 0.6) * 0.12 * shPow;
        vec3 lf = hp - Lf;
        cloth += mix(shCol, vec3(1.0), 0.3) * pick * 0.25 / (1.0 + 2.5 * dot(lf, lf));

        // satin sheen of the vanishing-point glow, rippled by the bass
        float ph = mod(u.time * 0.8, 6.2831853);
        float rip = sin((hp.z - z) * 1.1 - ph) * (0.12 + 0.04 * u.level) * rippleAmt * exp(-(hp.z - z) * 0.03);
        float graze = pow(1.0 - saturate(-rd.y), 6.0);
        vec3 sheenC = lmHaze(vec3(rd.x, -rd.y, rd.z), u, 0.0);
        cloth += sheenC * graze * (0.22 + 0.5 * max(rip, -0.4)) * (0.5 + 0.5 * wovenM * weftP);

        // beyond the selvedges: black lacquer floor mirroring the air and rails
        vec3 floorC = sheenC * graze * 0.9 + vec3(0.002, 0.002, 0.004);
        floorC += lmPal(0.2, u) * exp(-abs(ax - LM_HALF - 0.5) * 4.0) * 0.18;
        col = mix(floorC, cloth, onCloth);
    } else {
        col = vec3(0.0);
        tHit = 400.0;
    }

    // the heddle meters mirrored in the satin and the lacquer floor
    // (outside the branches: lmHeddles takes derivatives)
    vec3 rr = vec3(rd.x, -rd.y, rd.z);
    // only the side the ray travels toward can be reached from inside the loom
    float side = rd.x < 0.0 ? -1.0 : 1.0;
    vec3 mir = lmHeddles(hp, rr, side, 60.0, u, detail);
    float onFloor = step(t, 0.0) * step(tp, 1e3);
    float clothM = 1.0 - smoothstep(LM_HALF - 0.05, LM_HALF + 0.05, abs(hp.x));
    col += mir * onFloor * mix(0.45, 0.16, clothM) * (0.3 + 0.7 * pow(1.0 - saturate(-rd.y), 6.0));

    // distance fog toward the bright vanishing point
    float fog = 1.0 - exp(-tHit * 0.02);
    col = mix(col, lmHaze(rd, u, 1.0), fog);

    // heddle curtains on both sides, in front of whatever the ray hit
    col += lmHeddles(ro, rd, side, tHit, u, detail);

    // the shed: beyond the fell the raised half of the warp climbs away from
    // the shuttle line, a sheet of fibres seen from below
    float k = LM_SHED / LM_RISE;
    float den = rd.y - k * rd.z;
    float t1 = (k * (ro.z - zS) - ro.y) / (abs(den) > 1e-4 ? den : 1e-4);
    float t2 = (LM_SHED - ro.y) / max(rd.y, 1e-4);
    float zt1 = ro.z + rd.z * t1;
    float ts = zt1 < zS + LM_RISE ? t1 : t2;
    ts = clamp(ts, -1.0, 1e3);
    vec3 sp = ro + rd * ts;
    float ss = (sp.x + LM_HALF) / (2.0 * LM_HALF) * density * 0.5;
    float sfw = smoothstep(0.06, 0.3, fwidth(ss));
    float sf = mix(pow(0.5 - 0.5 * cos(6.2831853 * ss), 16.0), 0.1399, sfw);
    float sacr = abs((floor(ss) + 0.5) / (density * 0.5) * 2.0 - 1.0);
    float sb2 = bandAt(u, sacr);
    float sheetM = step(0.0, ts) * step(ts, tHit) * step(zS, sp.z) * (1.0 - smoothstep(LM_HALF - 0.3, LM_HALF, abs(sp.x)));
    vec3 sheetC = lmPal(0.05 + 0.55 * sacr + 0.06 * 0.35 + 0.5, u);
    float fellGlow = exp(-(sp.z - zS) * 0.25);
    col += sheetM * sheetC * sf * (0.28 + 0.08 * u.level + 0.02 * sb2 + 0.15 * fellGlow) * (1.0 - 0.7 * sfw) * exp(-max(ts, 0.0) * 0.022);

    // the shuttle's light scattering in the air along the ray
    vec3 wv = Ls - ro;
    float t0 = dot(wv, rd);
    float h = sqrt(max(dot(wv, wv) - t0 * t0, 1e-4));
    float scat = (atan((min(tHit, 400.0) - t0) / h) - atan(-t0 / h)) / h;
    col += shCol * shPow * min(scat, 30.0) * 0.008 * exp(-h * 0.7);
    // a thin anamorphic streak through the shuttle
    vec3 fw = normalize(ta - ro);
    vec3 rt = normalize(cross(fw, vec3(sin(roll), cos(roll), 0.0)));
    vec3 cu = cross(rt, fw);
    vec3 dS = normalize(wv);
    float vis = step(0.0, dot(dS, fw)) * step(length(wv), tHit + 0.5);
    float dv = dot(rd - dS, cu), dh = dot(rd - dS, rt);
    col += shCol * vis * shPow * exp(-abs(dv) * 500.0) * exp(-abs(dh) * 2.5) * 0.35;

    // the shuttle body: a white-hot spindle pointing the way it flies
    // and a comet wake of light streaming behind it, longer the faster it flies
    float dsg = dirS < 0.0 ? -1.0 : 1.0;
    float tb, hs;
    float ds = lmSegDist(ro, rd, Ls - vec3(1.1 * dsg, 0.0, 0.0), vec3(1.75 * dsg, 0.0, 0.0), tb, hs);
    float rad = 0.15 * (1.0 - (2.0 * hs - 1.0) * (2.0 * hs - 1.0));
    float pix = max(tb * 2.0 / max(u.resolution.y, 1.0), 1e-3);
    float body = (1.0 - smoothstep(rad - pix, rad + pix, ds)) * step(tb, tHit);
    float halo = 0.006 / (ds * ds + 0.012) * step(tb, tHit + 0.5);
    // a dark lacquered hull with a neon rim, hot metal tips and the glowing
    // bobbin of weft along its back
    float x01 = ds / max(rad, 1e-3);
    float rim = smoothstep(0.6, 1.0, x01);
    float tips = exp(-min(hs, 1.0 - hs) * 10.0);
    float bob = exp(-x01 * x01 * 3.0) * smoothstep(0.18, 0.3, hs) * smoothstep(0.82, 0.7, hs);
    vec3 hull = vec3(0.01, 0.008, 0.02) + shCol * (0.3 * (1.0 - x01) + 1.6 * rim)
              + mix(shCol, vec3(1.3), 0.35) * (tips * 2.6 + bob * 2.2);
    col = mix(col, hull * shPow, body);
    col += shCol * halo * shPow * 0.6;
    float tw, hw;
    float wl = 1.5 + 6.0 * abs(dirS);
    float dw = lmSegDist(ro, rd, Ls - vec3((1.1 + wl) * dsg, 0.05, 0.0), vec3(wl * dsg, 0.05, 0.0), tw, hw);
    col += mix(shCol, lmPal(0.5, u), 1.0 - hw) * exp(-dw * mix(30.0, 9.0, hw)) * hw * hw * step(tw, tHit) * 1.4 * shPow;

    return lmOffRed(max(col, vec3(0.0))); // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
