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
// Lumen field.
// A low glide down an open aisle through a field of glowing light stems
// under an aurora. Each stem carries an HDR orb at its tip; the rows recede
// into coloured fog, the aisle is lined with floor lamps, and a wet, slowly
// rippled mirror floor doubles everything, the aurora included; the water
// lies stiller toward the horizon. The glide is
// slow and near-steady (the shared 32-beat profile barely moves it), level
// with no beat roll, and the aurora rays and the wind crawl gently. Audio
// drives the light, softly: stem brightness and
// height follow the music's smoothed level with a gentle tilt of the spectrum
// outward from the aisle, the aurora curtains follow it across the sky
// (cyan and blue hems over a magenta fringe, climbing into violet and a high
// red veil; the stems run cyan by the aisle to rose at the edges and warm
// with level and through the build),
// every beat sends a faint ring of light out across the field from a point
// ahead, lifting the stems a little as it passes, and a slow wind combs the
// field. No beat flashes the orbs or the floor.
// param0 = Stem Density ; param1 = Bloom ; param2 = Sway

// Spectrum cache: twelve stops read once per pixel, blended locally, so the
// many per-post reads skip the uniform-array select.
float lfB(mat3x4 B, float f) {
    float x = saturate(f) * 11.0;
    return dot(max(1.0 - abs(x - vec4(0.0, 1.0, 2.0, 3.0)), 0.0), B[0]) +
           dot(max(1.0 - abs(x - vec4(4.0, 5.0, 6.0, 7.0)), 0.0), B[1]) +
           dot(max(1.0 - abs(x - vec4(8.0, 9.0, 10.0, 11.0)), 0.0), B[2]);
}

// Post hue: cool cyan beside the aisle, through violet and magenta to rose at
// the edges of the field; slow warm drifts roll through the rows, loud posts
// and the build both push warmer. Kept clear of the palette's green stretch.
float lfHue(vec2 c, float ax, float e, float tw, float hs) {
    float f = saturate((ax - 1.0) / 4.0);
    return 0.58 - 0.4 * f + 0.06 * hash21(c + 4.4) + 0.12 * sin(c.y * 0.11 + tw * 0.08) - 0.22 * e * e - hs;
}

// Post for grid cell c (x = column, y = row) at spacing sp: base xz, then the
// tip (xyz) and the post's hue and band energy.
void lfPost(vec2 c, float sp, float sway, float tw, float hs, mat3x4 B,
            out vec2 base, out vec3 tip, out float hue, out float e) {
    vec2 j = hash22(c * vec2(1.37, 0.71) + 3.1) - 0.5;
    base = (c + j * vec2(0.4, 0.5)) * sp;
    float f = saturate((abs(c.x) - 1.0) / 8.0);       // 0 next to the aisle
    float hr = hash21(c * 1.91 + 2.3);
    e = lfB(B, 0.03 + f * 0.75 + 0.08 * (hr - 0.5));
    float h = (0.5 + 2.8 * hr * hr) * (0.75 + 0.55 * e);
    // wind: a slow gust front travelling across the field
    float g = sin(dot(base, vec2(0.23, 0.11)) - tw * 0.5) + 0.5 * sin(dot(base, vec2(-0.13, 0.31)) - tw * 0.35 + 1.7);
    vec2 lean = vec2(0.9, 0.35) * g * sway * h;
    tip = vec3(base.x + lean.x, h, base.y + lean.y);
    hue = lfHue(c, abs(c.x), e, tw, hs);
}

// The beat ring: a front of light racing out from wc to radius wr.
float lfRing(vec2 p, vec2 wc, float wr) {
    float d = length(p - wc) - wr;
    return exp(-d * d * 0.4) + 0.12 * exp(-d * d * 0.04) * step(d, 0.0);
}

// Glow of the stems and orbs near a ray, row by row along +z. blur widens the
// glow with distance (rough reflections), tmax stops at the floor.
vec3 lfField(vec3 ro, vec3 rd, float tmax, float pix, float blur, int rows, float sp,
             float sway, float tw, float bloom, float fogK, vec4 wv4, float hs, mat3x4 B,
             VisualUniforms u) {
    vec3 acc = vec3(0.0);
    float rz = max(rd.z, 1e-3);
    float r0 = floor(ro.z / sp) + 1.0;
    float reach = float(rows) * sp;
    vec2 dxz = rd.xz / max(length(rd.xz), 1e-4);
    float reject = 0.45 * sp + sway * 1.6;
    for (int i = 0; i < 30; i++) {
        if (i >= rows) break;
        float row = r0 + float(i);
        float tz = (row * sp - ro.z) / rz;
        if (tz > tmax + 2.0 * sp) break;
        float xr = (ro.x + rd.x * tz) / sp;
        float cx = floor(xr);
        for (int k = 0; k < 2; k++) {
            vec2 c = vec2(cx + float(k), row);
            if (abs(c.x) < 0.5) continue;              // the open aisle
            // cheap reject: the post's base is far from the ray's ground track
            vec2 bj = (c + (hash22(c * vec2(1.37, 0.71) + 3.1) - 0.5) * vec2(0.4, 0.5)) * sp - ro.xz;
            if (abs(bj.x * dxz.y - bj.y * dxz.x) > reject) continue;
            vec2 b2; vec3 tip; float hue; float e;
            lfPost(c, sp, sway, tw, hs, B, b2, tip, hue, e);
            vec3 base = vec3(b2.x, 0.0, b2.y);
            // closest approach between the ray and the stem segment
            vec3 ab = tip - base;
            vec3 w0 = ro - base;
            float b = dot(rd, ab);
            float cc = dot(ab, ab);
            float d0 = dot(rd, w0);
            float e0 = dot(ab, w0);
            float s = saturate((e0 - b * d0) / max(cc - b * b, 1e-4));
            float t = max(b * s - d0, 0.0);
            vec3 dv = w0 + rd * t - ab * s;
            float d2 = dot(dv, dv);
            float fog = exp(-t * fogK) * (1.0 - smoothstep(reach - 3.0 * sp, reach, b2.y - ro.z));
            float wv = lfRing(b2, wv4.xy, wv4.z);
            float lit = (0.14 + 0.75 * e * e + 0.4 * e) * fog;

            // stem: thin hot core and a soft halo, never thinner than a pixel;
            // widening keeps the energy, so far stems do not shimmer
            float pw = t * (pix + blur);
            float wc = max(0.007, pw);
            float wh = max(0.045 * bloom, 2.0 * pw);
            float stem = (0.007 / wc) * exp(-d2 / (wc * wc)) * 1.8 +
                         (0.045 * bloom / wh) * exp(-d2 / (wh * wh)) * 0.35;
            // energy pulses climbing the stem after each beat
            float ps = fract(u.beatPhase * 1.15 - hash21(c) * 0.15);
            float pq = (s - ps) * 8.0;
            float pulse = exp(-pq * pq) * (1.0 - smoothstep(0.75, 1.0, ps));
            stem *= (0.15 + 0.85 * s * s) * (1.0 + 0.15 * pulse * (0.3 + e));
            stem *= step(t, tmax);

            // tip orb: a hot core in a coloured bloom
            vec3 to = tip - ro;
            float tt = max(dot(to, rd), 0.0);
            vec3 ov = to - rd * tt;
            float o2 = dot(ov, ov);
            // the ring whitens the orb (below) but never grows it: near bokeh
            // discs that swell on every beat flash the screen
            float r0o = 0.028 + 0.03 * e;
            // near orbs fall out of focus into soft bokeh discs
            float coc = 0.05 * abs(tt - 6.0) / 6.0;
            float ow = max(r0o + coc, tt * (pix * 1.2 + blur));
            float oh = max(0.13 * bloom, tt * (pix * 3.0 + 2.0 * blur));
            float k1 = r0o / ow;
            float k2 = 0.13 * bloom / oh;
            float disc = 1.0 - smoothstep(0.7 * ow * ow, ow * ow, o2);
            float orb = k1 * k1 * mix(exp(-o2 / (ow * ow)), disc * 0.8, smoothstep(r0o, 2.5 * r0o, ow)) * 4.5;
            float bl = k2 * k2 * exp(-o2 / (oh * oh)) * 0.45;
            // most posts near the ray only add a faint tail: skip their colour work
            if ((stem + orb + bl) * (lit + fog * wv) < 2e-4) continue;
            vec3 c0 = palVisual(hue, u);
            vec3 cw = peakWhite(c0, 0.55);
            // the orbs flare on the beat itself
            vec3 tipc = (peakWhite(c0, 0.15 + 0.3 * e + 0.1 * wv) * orb + c0 * bl) * step(tt, tmax);

            // the ring flares the whole post toward white as it passes
            acc += (mix(c0, cw, 0.3 * wv) * stem + tipc) * lit * (1.0 + 0.8 * wv * wv4.w) +
                   c0 * stem * fog * wv * wv4.w * 0.5;
        }
    }
    return acc;
}

// Far field: beyond the stem rows, orbs on two height layers stand in for
// the stems, one nearest-cell lookup each, fading in where the rows fade out.
vec3 lfFar(vec3 ro, vec3 rd, float tmax, float sp, float pix, float reach, float fogK,
           vec4 wv4, float tw, float hs, mat3x4 B, VisualUniforms u) {
    vec3 acc = vec3(0.0);
    for (int l = 0; l < 2; l++) {
        float hy = l == 0 ? 0.8 : 2.2;
        if (abs(rd.y) < 1e-4) continue;
        float t = (hy - ro.y) / rd.y;
        if (t <= 0.0 || t > tmax) continue;
        vec2 xz = ro.xz + rd.xz * t;
        vec2 c = floor(xz / sp + 0.5);
        if (abs(c.x) < 0.5) continue;
        vec2 cl = c + float(l) * 31.0;
        vec2 j = hash22(cl * vec2(1.37, 0.71) + 3.1) - 0.5;
        vec2 oxz = (c + j * vec2(0.4, 0.5)) * sp;
        vec3 to = vec3(oxz.x, hy, oxz.y) - ro;
        float tt = max(dot(to, rd), 0.0);
        float o2 = max(dot(to, to) - tt * tt, 0.0);
        float f = saturate((abs(c.x) - 1.0) / 8.0);
        float hr = hash21(cl * 1.91 + 2.3);
        float e = lfB(B, 0.03 + f * 0.75 + 0.08 * (hr - 0.5));
        float w = max(0.04, tt * pix * 1.3);
        float k = 0.04 / w;
        // a cell seen edge-on is thinner than the orb: fade rather than clip it
        float orb = k * k * exp(-o2 / (w * w)) * saturate(0.3 * sp * abs(rd.y) / w - 0.5);
        float hue = lfHue(cl, abs(c.x), e, tw, hs);
        float fade = smoothstep(reach - 3.0 * sp, reach, xz.y - ro.z) * exp(-t * fogK);
        float wv = lfRing(oxz, wv4.xy, wv4.z);
        acc += peakWhite(palVisual(hue, u), 0.3 + 0.1 * wv) * orb * 3.0 * fade *
               (0.14 + 1.1 * e * e + 0.5 * e) * (1.0 + 0.8 * wv * wv4.w) * step(0.35, hr);
    }
    return acc;
}

// Deepens a palette colour's saturation, keeping its brightest channel.
vec3 lfSat(vec3 c) {
    float m = max(max(c.r, c.g), max(c.b, 1e-4));
    vec3 n = c / m;
    return n * n * m;
}

// Sky colours, read once per pixel: cyan, blue, violet, magenta, red.
struct LfSkyPal { vec3 c; vec3 b; vec3 v; vec3 m; vec3 r; vec3 fog; };

// Aurora: three curtains hanging over the field, each a folded ribbon in
// azimuth with a hot lower hem over a magenta fringe, fine vertical rays,
// and a glow that climbs from cyan or blue into violet, magenta and red; the
// mix slides along each curtain so the sky never sits on one hue. Each
// curtain's light follows the spectrum band under it, folds seen edge-on burn
// brighter, every beat sends a kink running along the hems and flares them,
// and the curtains churn faster through the build. Above them a high red veil
// with long rays fills the zenith.
vec3 lfAurora(vec3 rd, float zc, float tw, float detail, float rush, float bass, float bph,
              LfSkyPal sk, mat3x4 B) {
    float el = rd.y;
    if (el <= -0.04) return vec3(0.0);
    float az = atan2_(rd.x, max(rd.z, 1e-3) + abs(rd.x) * 0.2);
    vec3 acc = vec3(0.0);
    float chn = 1.0 + 0.3 * rush;
    for (int i = 0; i < 3; i++) {
        float fi = float(i);
        // curtains drift sideways as the glide carries the camera on
        float x = az * (2.6 + 0.9 * fi) + fi * 2.3 + zc * 0.003 * (1.0 + 0.5 * fi) + tw * 0.03;
        float w1 = x * 1.3 + tw * (0.13 + 0.04 * fi) * chn;
        float w2 = x * 3.7 - tw * (0.21 + 0.05 * fi) * chn;
        float w3 = x * 9.0 + tw * 0.45 * chn;
        float kink = 0.02 * bass * sin(x * 6.0 - bph * 12.0 + fi * 2.0);
        // hems hang in festoons: rounded bottoms between sharp folds, so the
        // dark sky under them never reads as a line of hills
        // the arcs run across the sky at a slant, each its own way
        float s1 = sin(w1), s2 = sin(w2);
        float slant = (0.09 - 0.07 * fi) * az + 0.03 * sin(tw * 0.05 + fi * 2.0) * az;
        float bse = 0.1 + 0.08 * fi + slant + 0.018 * s1 - 0.022 * abs(s2) + 0.006 * sin(w3) + kink;
        float db = abs(0.0234 * cos(w1) + 0.054 * cos(w3)) + 0.081 * abs(cos(w2));
        // each curtain comes and goes along the sky, leaving dark gaps
        float env = smoothstep(0.05, 0.8, sin(x * 0.9 + fi * 2.1 + tw * 0.02));
        env *= 0.35 + 0.65 * vnoise(vec2(x * 2.3 - tw * 0.08, fi * 5.0));
        float fold = 0.6 + 2.2 * smoothstep(0.02, 0.12, abs(db));
        float h = 0.06 + 0.035 * fi;
        float y = el - bse;
        float hem = smoothstep(-0.012, 0.004, y);
        float yp = max(y, 0.0);
        float body = hem * exp(-yp / h) * (1.0 + 0.9 * exp(-yp * 22.0));
        float tall = hem * exp(-yp / (4.0 * h)) * 0.5;
        // fine rays: a stretched noise along the curtain, crawling
        float rays = vnoise(vec2(x * 26.0 + tw * 0.4 * (1.0 + fi) * chn, fi * 7.0));
        if (detail > 0.4) rays = 0.6 * rays + 0.4 * vnoise(vec2(x * 71.0 - tw * 0.6, fi * 3.0 + 1.0));
        rays = 0.15 + 0.85 * rays * rays;
        body += tall * rays * rays;
        float e = lfB(B, 0.1 + 0.28 * fi + 0.18 * sin(x * 0.7));
        float sh = 0.5 + 0.5 * sin(x * 0.45 + fi * 1.9 + tw * 0.04);
        vec3 lo = i == 0 ? mix(sk.c, sk.b, sh) : i == 1 ? mix(sk.b, sk.v, sh) : mix(sk.c, sk.v, sh);
        vec3 hi = i == 0 ? mix(sk.v, sk.m, sh) : i == 1 ? mix(sk.m, sk.r, sh) : mix(sk.r, sk.m, sh);
        hi = mix(hi, sk.r, rush * 0.5);
        vec3 c = mix(lo, hi, smoothstep(0.0, 2.5 * h, yp));
        // a hot line at the hem
        c = mix(c, peakWhite(lo, 0.4), exp(-yp * 70.0) * hem * 0.7);
        float gain = env * (0.5 + 2.0 * e * e + 0.6 * e) * (0.85 - 0.2 * fi) * (1.0 + 1.6 * bass * exp(-yp * 12.0));
        acc += c * body * rays * fold * gain;
        // magenta fringe hanging just under the hem
        float fr = smoothstep(-0.035, -0.004, y) * (1.0 - hem);
        acc += sk.m * fr * fr * rays * fold * gain * 0.9;
    }
    // high veil: a diffuse red and magenta glow with long rays toward the zenith
    float vx = az * 1.6 + tw * 0.015;
    float vn = vnoise(vec2(vx * 2.0 - tw * 0.03, el * 3.0 - tw * 0.04));
    float vr = vnoise(vec2(vx * 17.0 + tw * 0.12, 0.5));
    float veil = smoothstep(0.14, 0.45, el) * vn * vn * vn * (0.3 + 0.7 * vr);
    acc += mix(sk.v, mix(sk.m, sk.r, 0.4), smoothstep(0.15, 0.45, el + 0.1 * vn)) * veil * (0.08 + 0.25 * lfB(B, 0.6) + 0.2 * rush);
    // stars where the sky is clear, a few tinted
    vec2 sg = vec2(az * 0.55, el) * 70.0;
    vec2 sc = floor(sg);
    float sh = hash21(mod(sc, 509.0));
    vec2 sf = fract(sg) - 0.5 - 0.3 * (hash22(sc + 1.7) - 0.5);
    float star = step(0.9, sh) * exp(-dot(sf, sf) * 90.0) * smoothstep(0.0, 0.05, el);
    vec3 stc = mix(vec3(0.75, 0.8, 1.0), sk.m + 0.3, step(0.975, sh));
    acc += stc * star * 0.4 * (0.6 + 0.4 * sin(tw * 1.7 + sh * 40.0));
    return acc;
}

// Sky: deep indigo overhead, a violet band over the horizon with a rose glow
// at its foot, then the aurora.
vec3 lfSky(vec3 rd, float zc, float tw, float detail, float rush, float bass, float bph,
           LfSkyPal sk, mat3x4 B) {
    float h = max(rd.y, 0.0);
    vec3 col = sk.fog * (exp(-h * 16.0) * 1.2 + exp(-h * 3.0) * 0.4) +
               mix(sk.m, sk.r, 0.3) * 0.045 * exp(-h * 26.0) + sk.v * 0.01 * exp(-h * 6.0) + vec3(0.001, 0.0008, 0.004);
    return col + 0.55 * lfAurora(rd, zc, 0.5 * tw, detail, rush, bass, bph, sk, B);
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);

    float sp = mix(1.7, 1.05, u.param0);
    float bloom = mix(0.5, 1.6, u.param1);
    float sway = mix(0.03, 0.22, u.param2);
    float tw = mod(u.time, 600.0) * 0.9;

    vec2 fl = osFlight(u, 0.32, -0.12);
    float z = fl.x;
    float rush = 0.4 * saturate((fl.y - 1.0) / 3.5);
    float x = 0.2 * sp * sin(z * 0.045) + 0.1 * sp * sin(u.time * 0.07);
    float alt = 1.0 + 0.06 * sin(u.time * 0.11);
    vec3 ro = vec3(x, alt, z);
    vec3 ta = vec3(0.25 * sp * sin((z + 8.0) * 0.045), 1.25, z + 8.0);
    float roll = -0.25 * (ta.x - ro.x) / 8.0;
    float fov = 1.25;
    vec3 rd = osCamera(ro, ta, roll, fov, p);
    float pix = 2.0 / (u.resolution.y * fov);

    int rows = osSteps(4.0, 8.0, detail);
    float reach = float(rows) * sp;
    float fogK = 0.07;
    // the spectrum, read once
    mat3x4 B;
    for (int i = 0; i < 3; i++) {
        float f0 = float(i * 4) / 11.0;
        B[i] = 0.5 * u.level + 0.15 * vec4(bandAt(u, f0), bandAt(u, f0 + 1.0 / 11.0), bandAt(u, f0 + 2.0 / 11.0), bandAt(u, f0 + 3.0 / 11.0));
    }
    // sky colours, five palette reads drifting slowly
    LfSkyPal sk;
    float sd = 0.03 * sin(u.time * 0.03);
    sk.c = lfSat(palVisual(0.61 + sd, u));
    sk.b = lfSat(palVisual(0.5 - sd, u));
    sk.v = lfSat(palVisual(0.38 + sd, u));
    sk.m = lfSat(palVisual(0.28 - sd, u));
    sk.r = lfSat(palVisual(0.17 + sd, u));
    sk.fog = mix(palVisual(0.42, u), sk.m, 0.25 + 0.15 * sin(u.time * 0.05)) * 0.04;
    vec3 fogCol = sk.fog;
    float hs = 0.04 * rush;
    float bass = 0.15 * u.level;
    float bph = saturate(u.beatPhase);
    // the beat ring: born on the beat a little ahead of the camera and racing
    // outward across the field; centre, radius and strength
    float bp = saturate(u.beatPhase);
    vec2 wc = vec2(0.0, ro.z + 12.0);
    float wAmp = smoothstep(0.0, 0.25, bp) * (1.0 - bp) * (1.0 - bp) * (0.045 + 0.02 * u.level);
    vec4 wv = vec4(wc, 1.0 + 28.0 * pow(bp, 0.7), wAmp);
    vec3 ringC = lfSat(palVisual(0.27 + 0.05 * sin(u.time * 0.07), u));

    // aisle lamps: two lines of low beads along the open aisle
    float lx = 0.42 * sp;
    float ls = 0.5 * sp;
    float lampE = lfB(B, 0.04);
    vec3 lampC = lfSat(palVisual(0.12 + 0.03 * sin(u.time * 0.04), u));

    float tf = rd.y < -1e-3 ? -ro.y / rd.y : 1e4;
    vec3 col;
    float tEnd;
    if (tf < 1e3) {
        vec3 fp = ro + rd * tf;
        // wet floor: a rippled mirror; ripples run mostly across the view, so
        // reflections stretch into streaks toward the viewer
        vec2 n2 = vec2(vnoise(fp.xz * vec2(3.0, 1.2) + vec2(0.0, tw * 0.06)) - 0.5,
                       vnoise(fp.zx * vec2(2.2, 4.0) + 3.0 - tw * 0.04) - 0.5);
        // the ring rolls a swell through the water as it passes
        float rq = length(fp.xz - wc) - wv.z;
        float swell = exp(-rq * rq * 0.5) * wAmp;
        vec2 rdir = normalize(fp.xz - wc + 1e-4);
        // far water calms toward the horizon so distant reflections do not shimmer
        float calm = 1.0 / (1.0 + 0.08 * tf);
        vec3 rn = normalize(vec3(n2.x * 0.03 * calm + rdir.x * swell * 0.12, 1.0,
                                 n2.y * 0.12 * calm + rdir.y * swell * 0.12));
        vec3 rr = reflect(rd, rn);
        rr.y = abs(rr.y);
        float fres = 0.3 + 0.7 * pow(1.0 - saturate(-rd.y), 5.0);
        int rrows = osSteps(2.0, 4.0, detail);
        vec3 refl = lfField(fp, rr, 60.0, pix, 0.004, rrows, sp, sway, tw, bloom * 1.3, fogK, wv, hs, B, u);
        refl += lfSky(rr, ro.z, tw, 0.0, rush, bass, bph, sk, B) * 0.6;
        col = refl * fres * 0.85;
        // light pools around the nearest stems' feet
        vec2 cc = floor(fp.xz / sp);
        for (int i = 0; i < 4; i++) {
            vec2 c = cc + vec2(float(i & 1), float(i >> 1));
            if (abs(c.x) < 0.5) continue;
            vec2 b2; vec3 tip; float hue; float e;
            lfPost(c, sp, sway, tw, hs, B, b2, tip, hue, e);
            float pd = length(fp.xz - b2) / (0.5 * sp);
            float pool = saturate(1.0 - pd);
            col += palVisual(hue, u) * pool * pool * (0.05 + 0.5 * e * e) * bloom;
        }
        // lamp pools along the aisle, a warm runway in the dark water
        float lz = (floor(fp.z / ls) + 0.5) * ls;
        vec2 ld = vec2(abs(fp.x) - lx, (fp.z - lz) * 0.5) / (0.1 * sp);
        col += lampC * exp(-dot(ld, ld) * 2.0) * (0.05 + 0.2 * lampE) * exp(-tf * 0.06);
        // the ring of light itself, skimming the water
        float rq2 = length(fp.xz - wc) - wv.z;
        col += ringC * exp(-rq2 * rq2 * 3.0) * wAmp * 0.3 * exp(-tf * 0.04);
        tEnd = tf;
    } else {
        col = lfSky(rd, ro.z, tw, detail, rush, bass, bph, sk, B);
        tEnd = 80.0;
    }
    col += lfField(ro, rd, tEnd, pix, 0.0, rows, sp, sway, tw, bloom, fogK, wv, hs, B, u);
    col += lfFar(ro, rd, tEnd, sp, pix, reach, fogK, wv, tw, hs, B, u);

    // aisle lamp beads, seen directly: the closest bead on each line
    for (int sd = 0; sd < 2; sd++) {
        vec2 lp = vec2(sd == 0 ? -lx : lx, 0.07);
        vec2 dxy = ro.xy - lp;
        float a = dot(rd.xy, rd.xy);
        float tl = a > 1e-6 ? -dot(dxy, rd.xy) / a : 0.0;
        tl = clamp(tl, 0.0, tEnd);
        float zc = ro.z + rd.z * tl;
        for (int k = -1; k <= 1; k++) {
            float bz = (floor(zc / ls) + 0.5 + float(k)) * ls;
            vec3 to = vec3(lp.x, lp.y, bz) - ro;
            float tt = dot(to, rd);
            if (tt <= 0.0 || tt > tEnd + 0.2) continue;
            float o2 = max(dot(to, to) - tt * tt, 0.0);
            float w = max(0.018, tt * pix * 1.2);
            float kk = 0.018 / w;
            col += peakWhite(lampC, 0.4) * kk * kk * exp(-o2 / (w * w)) * 2.5 * exp(-tt * 0.05) * (0.5 + lampE);
        }
    }

    // fog over the floor and a glowing band at the horizon
    // the sky keeps its own gradient: only a low haze over the horizon
    float fog = 1.0 - exp(-min(tEnd, 80.0) * fogK);
    if (tf >= 1e3) fog *= exp(-max(rd.y, 0.0) * 8.0);
    col = mix(col, fogCol, fog * 0.8);
    // the haze over the field glows with the spectrum: bass dead ahead,
    // treble out toward the sides
    float ha = abs(atan2_(rd.x, max(rd.z, 1e-3)));
    float hb = lfB(B, ha * 0.9);
    vec3 hc = palVisual(0.3 + 0.35 * saturate(ha * 0.9) + 0.05 * u.spectralCentroid, u);
    col += hc * 0.05 * (exp(-abs(rd.y) * 14.0) + 0.25 * exp(-abs(rd.y) * 4.0)) * (0.35 + 1.6 * hb * hb);

    return col; // LINEAR HDR; the post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
