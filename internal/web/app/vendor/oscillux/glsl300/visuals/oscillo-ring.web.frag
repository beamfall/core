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
// Oscillo ring.
// A long-exposure light-painting session in an empty concrete hall with a
// wet floor. A ring of LEDs swung on a cord paints an open lattice of cool
// glowing rings in the air, one ring after another, so the figure is always
// being drawn: the newest stroke glows softly brighter at its head and older
// strokes fade gently to a soft afterglow. Each ring is a closed oscilloscope trace
// whose shape barely breathes with the smoothed level and a trace of each band
// and whose wobble drifts slowly, one whole figure every twelve beats. Every 24 beats the painter changes
// the figure (a meridian sphere, a torus, a gyroscope rosette, a stacked
// spectrum vase) and paints it over the old one ring by ring. Two more
// painters swing pixel sticks round the orb on wide tilted orbits, a slow
// full turn every thirty-two beats, so slowly twisting ribbons of gold, amber and
// rose light sweep across the frame and fade softly behind their heads; the
// LED shimmer on rings and ribbons is faint and slow. Every
// fourth beat throws a burst of steel-wool sparks that arc down slowly
// and bounce off the floor; each repaint only tilts the rings a little; beat accents on the rings and the orb glow
// are small and swell softly rather than flashing. Calm under loud music:
// the camera circles slowly at a steady pace with no beat surges, and reds
// are kept just off saturated red. The orb and the moving stick heads are the
// only lights: they glow in the haze, pool on the floor and catch the wet
// sheen of the columns, and the puddles mirror it all.
// Rings and orbits are found analytically in screen space (a first guess
// refined by Gauss-Newton on the exact projection), so there is no ray march.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Orb Size ; param1 = Trace Swing ; param2 = Sparks

// The palette read: the visual's own palette with no beat or spectral nudge.
// The vivid tour swaps whole palettes of very different brightness every few
// seconds, which swells the light across the frame, so this visual keeps its
// own colours there (as the calm visuals do); a host's manifest palette still
// replaces them.
vec3 orPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 2, u); }

const float OR_TAU = 6.2831853;
const float OR_CYC = 12.0;   // beats to paint one whole figure

// The fitted filmic curve the post chain applies per channel, and its inverse.
float orTone(float x) { return x * (2.51 * x + 0.03) / (x * (2.43 * x + 0.59) + 0.14); }
float orToneInv(float y) {
    y = clamp(y, 0.0, 0.999);
    float a = 2.51 - 2.43 * y, b = 0.03 - 0.59 * y;
    return (-b + sqrt(b * b + 0.56 * a * y)) / (2.0 * a);
}

// Keeps every red just off saturated red once tonemapped: green and blue
// rise together until they make 0.27 of red, a slight desaturation that keeps
// the hue, so no red area can strobe.
vec3 orOffRed(vec3 c) {
    float r = orTone(c.r), g = orTone(c.g), b = orTone(c.b);
    float need = 0.27 * r - g - b;
    if (need <= 0.0) return c;
    c.g = orToneInv(g + 0.5 * need);
    c.b = orToneInv(b + 0.5 * need);
    return c;
}

struct OrPal { vec3 c0; vec3 c1; vec3 c2; vec3 c3; vec3 c4; };

// cyclic blend over the four cool stops; c4 is kept for the warm accent
vec3 orCool(OrPal P, float t) {
    t = fract(t) * 4.0;
    vec3 c = mix(P.c0, P.c2, smoothstep(0.0, 1.0, t));
    c = mix(c, P.c3, smoothstep(1.0, 2.0, t));
    c = mix(c, P.c1, smoothstep(2.0, 3.0, t));
    return mix(c, P.c0, smoothstep(3.0, 4.0, t));
}

// the warm family the pixel stick paints in: gold, amber, the palette's red, rose
vec3 orWarm(OrPal P, float t) {
    t = fract(t) * 4.0;
    vec3 g = vec3(1.0, 0.7, 0.2);
    vec3 am = vec3(1.0, 0.38, 0.06);
    vec3 re = vec3(1.0, 0.14, 0.06);
    vec3 ro = vec3(1.0, 0.2, 0.38);
    vec3 c = mix(g, am, smoothstep(0.0, 1.0, t));
    c = mix(c, re, smoothstep(1.0, 2.0, t));
    c = mix(c, ro, smoothstep(2.0, 3.0, t));
    return mix(c, g, smoothstep(3.0, 4.0, t));
}

struct OrCam { vec3 ro; vec3 fw; vec3 rt; vec3 cu; float f; };
struct OrRing { vec3 C; vec3 A; vec3 B; };

// x, y = screen position, z = depth along the view axis
vec3 orProj(OrCam c, vec3 X) {
    vec3 d = X - c.ro;
    float z = max(dot(d, c.fw), 0.05);
    return vec3(c.f * vec2(dot(d, c.rt), dot(d, c.cu)) / z, z);
}

vec2 orCm(vec2 a, vec2 b) { return vec2(a.x * b.x - a.y * b.y, a.x * b.y + a.y * b.x); }

// phases of ring k's four trace harmonics, as unit complex numbers; they
// turn slowly so the painted lattice drifts rather than shimmers
struct OrPh { vec4 q0; vec4 q1; };
OrPh orPhase(float k, float t) {
    t *= 0.2;
    vec4 a = vec4(t * 0.5 + k, -t * 0.65 + 2.0 * k, t * 0.85 + 0.5 * k, -t * 1.1 + 0.7 * k);
    OrPh h;
    h.q0 = vec4(cos(a.x), sin(a.x), cos(a.y), sin(a.y));
    h.q1 = vec4(cos(a.z), sin(a.z), cos(a.w), sin(a.w));
    return h;
}

// Point X and tangent dX of the ring at angle s. The radius wobbles with the
// spectrum on integer harmonics 3, 5, 9 and 14, so the trace always closes;
// the harmonics are powers of e^(is), so one sin/cos pair serves them all.
void orEval(OrRing r, float s, OrPh h, vec4 sw, out vec3 X, out vec3 dX) {
    vec2 z = vec2(cos(s), sin(s));
    vec2 z2 = orCm(z, z);
    vec2 z3 = orCm(z2, z);
    vec2 z5 = orCm(z3, z2);
    vec2 z9 = orCm(orCm(z5, z2), z2);
    vec2 z14 = orCm(z9, z5);
    vec2 w3 = orCm(z3, h.q0.xy);
    vec2 w5 = orCm(z5, h.q0.zw);
    vec2 w9 = orCm(z9, h.q1.xy);
    vec2 w14 = orCm(z14, h.q1.zw);
    vec4 amp = sw * vec4(0.10, 0.07, 0.045, 0.03);
    float tr = amp.x * w3.y + amp.y * w5.y + amp.z * w9.y + amp.w * w14.y;
    float dtr = 3.0 * amp.x * w3.x + 5.0 * amp.y * w5.x + 9.0 * amp.z * w9.x + 14.0 * amp.w * w14.x;
    vec3 rad = r.A * z.x + r.B * z.y;
    X = r.C + (1.0 + tr) * rad;
    dX = dtr * rad + (1.0 + tr) * (r.B * z.x - r.A * z.y);
}

// projected point (xy) and depth (z), with the screen tangent in de
vec3 orProjD(OrCam c, vec3 X, vec3 dX, out vec2 de) {
    vec3 d = X - c.ro;
    float z = max(dot(d, c.fw), 0.05);
    vec2 sxy = vec2(dot(d, c.rt), dot(d, c.cu));
    vec2 dxy = vec2(dot(dX, c.rt), dot(dX, c.cu));
    de = c.f * (dxy * z - sxy * dot(dX, c.fw)) / (z * z);
    return vec3(c.f * sxy / z, z);
}

// Closest point of ring r's projection to screen point p: x = distance,
// y = ring angle there, z = depth. A weak-perspective ellipse inverse gives
// the first guess (and culls pixels far from the curve); Gauss-Newton steps
// on the exact projection refine it. wob bounds the trace wobble.
vec3 orRingDist(vec2 p, vec2 stretch, OrCam cam, OrRing r, OrPh h, vec4 sw, float wob, int iters) {
    vec3 c3 = orProj(cam, r.C);
    vec2 a2 = cam.f * vec2(dot(r.A, cam.rt), dot(r.A, cam.cu)) / c3.z;
    vec2 b2 = cam.f * vec2(dot(r.B, cam.rt), dot(r.B, cam.cu)) / c3.z;
    vec2 q = p - c3.xy;
    float rmax = max(length(a2), length(b2));
    float reach = wob * rmax + 0.07;
    float ext = rmax + reach;
    if (dot(q, q) > ext * ext) return vec3(1e3, 0.0, c3.z);
    float det = a2.x * b2.y - a2.y * b2.x;
    det = det >= 0.0 ? max(det, 1e-4) : min(det, -1e-4);
    vec2 m = vec2(b2.y * q.x - b2.x * q.y, a2.x * q.y - a2.y * q.x) / det;
    float lm = length(m);
    vec2 mh = lm > 1e-6 ? m / lm : vec2(1.0, 0.0);
    vec2 gr = vec2(b2.y * mh.x - a2.y * mh.y, a2.x * mh.y - b2.x * mh.x) / det;
    if (abs(lm - 1.0) > 1.3 * reach * length(gr)) return vec3(1e3, 0.0, c3.z);
    float s = atan2_(mh.y, mh.x);
    vec3 X;
    vec3 dX;
    vec2 de;
    vec3 e = vec3(0.0);
    for (int i = 0; i < 3; i++) {
        orEval(r, s, h, sw, X, dX);
        e = orProjD(cam, X, dX, de);
        if (i >= iters) break;
        s += clamp(dot(p - e.xy, de) / max(dot(de, de), 1e-6), -0.5, 0.5);
    }
    return vec3(length((p - e.xy) * stretch), s, e.z);
}

// Ring k of n in figure f around centre O with radius R.
OrRing orForm(float f, float k, float n, vec3 O, float R, float lvl, float spin) {
    float u = k / n;
    OrRing r;
    if (f < 0.5) {            // meridian sphere
        float th = 3.14159265 * u + spin;
        r.C = O;
        r.A = R * vec3(cos(th), 0.0, sin(th));
        r.B = vec3(0.0, R, 0.0);
    } else if (f < 1.5) {     // torus of vertical sections
        float ph = OR_TAU * u + spin;
        vec3 e = vec3(cos(ph), 0.0, sin(ph));
        float rr = R * (0.5 + 0.12 * lvl);
        r.C = O + 0.72 * R * e;
        r.A = rr * e;
        r.B = vec3(0.0, rr, 0.0);
    } else if (f < 2.5) {     // gyroscope rosette: great circles on a cone of normals
        float ph = OR_TAU * u + spin;
        float al = 1.05;
        vec3 nn = vec3(sin(al) * cos(ph), cos(al), sin(al) * sin(ph));
        vec3 a = vec3(-sin(ph), 0.0, cos(ph));
        r.C = O;
        r.A = R * a;
        r.B = R * cross(nn, a);
    } else {                  // spectrum vase: stacked latitude rings
        float uu = (k + 0.5) / n;
        float rr = R * (0.26 + 0.58 * pow(sin(3.14159265 * uu), 0.8) + 0.34 * lvl);
        float cs = cos(spin), sn = sin(spin);
        r.C = O + vec3(0.0, (2.0 * uu - 1.0) * 1.02 * R, 0.0);
        r.A = rr * vec3(cs, 0.0, sn);
        r.B = rr * vec3(-sn, 0.0, cs);
    }
    return r;
}

// The painter's swing axis wanders, so every pass over the figure lays its
// rings at a slightly new tilt and the lattice weaves as it is repainted.
OrRing orTilt(OrRing r, vec3 O, float drawn) {
    float ax = 0.16 * sin(drawn * 0.3);
    float az = 0.12 * sin(drawn * 0.2 + 1.0);
    float cx = cos(ax), sx = sin(ax), cz = cos(az), sz = sin(az);
    mat3 m = mat3(cz, sz, 0.0, -sz, cz, 0.0, 0.0, 0.0, 1.0) * mat3(1.0, 0.0, 0.0, 0.0, cx, sx, 0.0, -sx, cx);
    r.C = O + m * (r.C - O);
    r.A = m * r.A;
    r.B = m * r.B;
    return r;
}

struct OrSt {
    float bc; float head; float nR; int n; float R; vec3 O; float t; vec4 sw;
    float zc; float pix; float coolKick; float wob; float sb[8];
};

// The painted orb seen at screen point p (also used, mirrored, for the floor).
vec3 orOrb(vec2 p, vec2 stretch, float wAdd, float mirror, int iters, OrCam cam, OrSt S, OrPal P) {
    vec3 col = vec3(0.0);
    // the whole figure stays inside a sphere round the centre: skip the rest
    vec3 o3 = orProj(cam, vec3(S.O.x, S.O.y * mirror, S.O.z));
    float rb = cam.f * S.R * (1.3 + S.wob) / o3.z + 0.08;
    vec2 dq = (p - o3.xy) * stretch;
    if (dot(dq, dq) > rb * rb) return col;
    float headK = floor(S.head);
    float headS = fract(S.head) * OR_TAU;
    // harmonic phases advance by a fixed rotation from ring to ring
    OrPh ph = orPhase(0.0, S.t);
    const vec4 st0 = vec4(0.5403023, 0.8414710, -0.4161468, 0.9092974);
    const vec4 st1 = vec4(0.8775826, 0.4794255, 0.7648422, 0.6442177);
    for (int i = 0; i < 12; i++) {
        if (i >= S.n) break;
        if (i > 0) {
            ph.q0 = vec4(orCm(ph.q0.xy, st0.xy), orCm(ph.q0.zw, st0.zw));
            ph.q1 = vec4(orCm(ph.q1.xy, st1.xy), orCm(ph.q1.zw, st1.zw));
        }
        float k = float(i);
        float lvl = S.sb[i - 8 * (i / 8)];
        bool isHead = k == headK;
        for (int v = 0; v < 2; v++) {
            if (v == 1 && !isHead) break;
            // cycles since this version of the ring was painted
            float age = isHead ? (v == 0 ? 0.0 : (S.nR - 1.0) / S.nR) : fract((S.head - k - 1.0) / S.nR);
            // start time of that stroke, in beats
            float drawn = isHead ? S.bc - fract(S.head) * OR_CYC / S.nR - OR_CYC * float(v)
                                 : S.bc - OR_CYC * age - OR_CYC / S.nR;
            float sect = floor(drawn / 24.0);
            OrRing r = orForm(mod(sect, 4.0), k, S.nR, S.O, S.R, lvl, hash11(sect * 1.37 + 0.3) * OR_TAU + 0.04 * drawn);
            r = orTilt(r, S.O, drawn);
            r.C.y *= mirror; r.A.y *= mirror; r.B.y *= mirror;
            vec3 rd3 = orRingDist(p, stretch, cam, r, ph, S.sw, S.wob, iters);
            if (rd3.x > 0.4) continue;
            float sm = mod(rd3.y, OR_TAU);
            float mask = 1.0;
            float comet = 0.0;
            if (isHead) {
                float m = smoothstep(0.0, 0.06, sm) * smoothstep(0.0, 0.06, headS - sm);
                mask = v == 0 ? m : 1.0 - m;
                comet = v == 0 ? exp(-(headS - sm) * 1.4) : 0.0;
            }
            float bri = 0.25 + 0.4 * exp(-age * 1.5);
            float coc = abs(rd3.z - S.zc) * 0.008 + wAdd;
            float w = sqrt(S.pix * S.pix + coc * coc);
            float d = rd3.x;
            float core = exp(-d * d / (w * w)) * min(1.0, 1.4 * S.pix / w);
            // windowed, so the culls never show as edges against the haze
            float hwin = saturate(1.0 - d / (0.05 + 3.0 * coc));
            float halo = hwin * hwin / (1.0 + d * d / (0.00025 + 4.0 * coc * coc));
            float led = 0.93 + 0.07 * cos(sm * 36.0 + k * 2.1 + S.t * 0.3);
            vec3 c = orCool(P, sm / OR_TAU + k * 0.23 + sect * 0.27 + S.t * 0.03);
            c = mix(c, P.c3, 0.4 * exp(-age * 2.0));   // fresh strokes lean to the cool end
            float cf = S.coolKick * exp(-age * 5.0);
            c = mix(c, P.c3 + 0.3, cf * 0.3) * (1.0 + 0.3 * cf);
            c = mix(c, vec3(1.0), comet * 0.25) * (1.0 + 0.35 * comet);
            col += c * (bri * mask * led) * (1.25 * core + 0.10 * halo);
        }
    }
    return col;
}

// Steel-wool sparks thrown off the orb on the warm beats, in screen space
// round the orb's image: ballistic arcs whose long-exposure trails cool from
// gold to red and end in a bounce on the floor. All sparks of a burst share
// one launch speed, so a pixel can undo gravity once, find its direction
// sector and test only the three sparks that can reach it.
vec3 orSparks(vec2 p, vec2 o, float Rs, float yFloor, float bc, float beats, float nS, float pix, OrPal P, float power) {
    vec3 col = vec3(0.0);
    vec3 hotC = vec3(1.0, 0.62, 0.2);
    vec3 coolC = mix(vec3(1.0, 0.22, 0.02), P.c4, 0.3);
    float g = 1.1;
    vec2 q = p - o;
    float w = pix * 0.9 + 0.0005;
    for (int bb = 0; bb < 4; bb++) {
        // two bursts, each with a fast and a slow layer of sparks
        float b = floor(beats * 0.25) - float(bb / 2);
        float layer = float(bb - 2 * (bb / 2));
        float age = (bc - 4.0 * b) * 0.4;      // sparks fly slower than the beat
        if (age > 3.4) continue;
        float v = (0.8 + 0.35 * hash11(b * 5.3 + 1.0)) * (layer > 0.5 ? 0.55 : 1.0);
        float nL = layer > 0.5 ? floor(nS * 0.6) : nS;
        // undo gravity: the launch time at which a spark passes this pixel
        float tau = max(length(q) - Rs, 0.0) / v;
        for (int it = 0; it < 2; it++) tau = max(length(q + vec2(0.0, 0.5 * g * tau * tau)) - Rs, 0.0) / v;
        // nothing of this layer is near this pixel's flight time
        if (tau > age + 0.05 || tau < age - 1.6) continue;
        vec2 qu = q + vec2(0.0, 0.5 * g * tau * tau);
        float sec = (atan2_(qu.y, qu.x) / OR_TAU + 0.5) * nL;
        float c0 = floor(sec);
        for (int j = -1; j < 2; j++) {
            float ci = mod(c0 + float(j), nL);
            float id = b * 57.0 + ci * 1.618 + layer * 311.0;
            float h1 = hash11(id), h2 = hash11(id + 3.7), h3 = hash11(id + 9.1), h4 = hash11(id + 15.3);
            if (h3 > 0.85) continue;
            float th = ((ci + 0.1 + 0.8 * h1) / nL - 0.5) * OR_TAU;
            vec2 dir = vec2(cos(th), sin(th));
            float delay = 0.8 * h2;
            float life = 0.8 + 1.3 * h4;
            float sa = age - delay;
            if (sa <= 0.0) continue;
            // floor contact: a little lower for sparks that land nearer the camera
            float yf = yFloor - 0.38 * hash11(id + 21.9) * hash11(id + 27.1);
            float cc = max(o.y + Rs * dir.y - yf, 0.0);
            float tf = (v * dir.y + sqrt(v * v * dir.y * dir.y + 2.0 * g * cc)) / g;
            float tb = min(sa, min(life, tf));
            float ta = max(0.0, tb - 0.2 - 0.35 * fract(h1 * 7.3));
            float tc = clamp(tau, ta, tb);
            vec2 pt = (Rs + v * tc) * dir - vec2(0.0, 0.5 * g * tc * tc);
            float d = length(q - pt);
            float fade = exp(-(sa - tc) * 3.0) * sqrt(saturate(1.0 - tc / life));
            float glow = exp(-d * d / (w * w)) * 1.8 + 0.07 / (1.0 + d * d / 0.00015);
            glow *= 0.85 + 0.15 * sin(tc * 47.0 + h2 * 40.0);
            col += mix(coolC, hotC, exp(-(sa - tc) * 2.2)) * glow * fade * (tb > ta ? 1.0 : 0.0);
            // bounce on the floor
            if (tf < life && sa > tf) {
                vec2 hp = (Rs + v * tf) * dir - vec2(0.0, 0.5 * g * tf * tf);
                vec2 dv = q - hp;
                col += hotC * exp(-dot(dv, dv) / (w * w * 9.0)) * exp(-(sa - tf) * 3.0) * 1.5;
            }
        }
    }
    return col * power;
}

// The orbit of pixel stick j: a big tilted ring round the orb whose tilt and
// heading precess slowly, so each sweep lays its ribbon on a new path.
OrRing orStickRing(OrSt S, float j) {
    float ph = S.t * 0.02 + j * 2.4;
    float al = 0.42 + 0.22 * sin(S.t * 0.015 + j * 1.7);
    vec3 nn = vec3(sin(al) * cos(ph), cos(al), sin(al) * sin(ph));
    vec3 a = normalize(cross(nn, vec3(0.0, 0.0, 1.0)));
    float Rr = S.R * (1.32 + 0.26 * j);
    OrRing r;
    r.C = S.O + vec3(0.0, 0.12 - 0.26 * j, 0.0);
    r.A = Rr * a;
    r.B = Rr * cross(nn, a);
    return r;
}

// angle of the stick head on orbit j: a slow full turn every thirty-two beats
float orStickHead(OrSt S, float j) { return fract(S.bc / 32.0 + j * 0.37) * OR_TAU; }

// The pixel stick: a second painter swings a bar of five LEDs round the orb,
// so a broad twisting ribbon of warm light is drawn slowly across the frame
// and fades gently behind its head. orStickAt refines one angle guess
// by Gauss-Newton on the exact projection and shades the ribbon there.
vec3 orStickAt(vec2 p, vec2 stretch, float wAdd, float mirror, OrCam cam, OrSt S, OrPal P, float j, OrRing r, OrPh h, float s) {
    vec4 sw = S.sw * 0.45;
    vec3 X;
    vec3 dX;
    vec2 de;
    vec3 e = vec3(0.0);
    for (int i = 0; i < 3; i++) {
        orEval(r, s, h, sw, X, dX);
        e = orProjD(cam, X, dX, de);
        if (i == 2) break;
        s += clamp(dot(p - e.xy, de) / max(dot(de, de), 1e-6), -0.6, 0.6);
    }
    vec2 dv = (p - e.xy) * stretch;
    float dist = length(dv);
    float hwMax = cam.f * 0.085 * S.R / e.z;
    float reach = hwMax * 3.5 + 0.012;
    if (dist > reach) return vec3(0.0);
    vec2 tn = de / max(length(de), 1e-6);
    float across = dot(dv, vec2(-tn.y, tn.x));
    // a guess that has not settled onto the stroke shows nothing
    float settled = smoothstep(4.0, 2.5, abs(dot(dv, tn)) / (hwMax + 3.0 * S.pix));
    // the trail behind the head, one turn long
    float sm = mod(s, OR_TAU);
    float dth = mod(orStickHead(S, j) - sm, OR_TAU);
    float trail = (0.45 + 0.55 * exp(-dth * 0.2)) * smoothstep(0.0, 0.6, dth) * smoothstep(OR_TAU, OR_TAU - 1.2, dth) ;
    // the stick turns in the hand, so the ribbon twists
    float tw = abs(cos(sm * 2.0 + S.t * 0.05 + j * 1.3));
    float hw = hwMax * (0.18 + 0.82 * tw);
    float coc = abs(e.z - S.zc) * 0.008 + wAdd;
    float aa = sqrt(S.pix * S.pix + coc * coc);
    float band = smoothstep(hw + aa, hw - aa, dist);
    float x = clamp(across / max(hw, 1e-5), -1.0, 1.0);
    float lf = fract((x * 0.5 + 0.5) * 4.999) - 0.5;
    float res = saturate(hw / (7.0 * aa) - 0.4);
    float leds = mix(0.55, 0.25 + 1.6 * exp(-lf * lf * 40.0), res);
    // pixel-stick pattern: the LEDs chase colour along the stroke
    float chase = 0.93 + 0.07 * cos(sm * 22.0 - S.t * 0.3 + x * 2.0);
    vec3 c = orWarm(P, x * 0.2 + sm * 0.16 + j * 0.45 + S.t * 0.02);
    float hot = exp(-dth * 9.0);
    c = mix(c, vec3(1.0, 0.92, 0.8), 0.15 * hot) * (1.0 + 0.2 * hot);
    float fall = saturate(1.0 - dist / reach);
    float halo = 0.06 * fall * fall / (1.0 + dist * dist / (hw * hw * 2.0 + 0.0001));
    // the stick never passes through the floor
    float above = smoothstep(0.0, 0.06, X.y * mirror);
    return c * (band * leds * chase * 1.3 + halo) * trail * above * settled;
}

// Two guesses: where the eye ray meets the orbit's plane, and the nearest of
// eight coarse samples of the projected orbit; the brighter result wins, so
// a fold of the orbit seen near edge-on never drops a stroke.
vec3 orRibbon(vec2 p, vec2 stretch, float wAdd, float mirror, OrCam cam, OrSt S, OrPal P, float j) {
    OrRing r = orStickRing(S, j);
    r.C.y *= mirror; r.A.y *= mirror; r.B.y *= mirror;
    vec3 c3 = orProj(cam, r.C);
    float ext = cam.f * length(r.A) * 1.6 / max(c3.z - length(r.A), 0.6) + 0.2;
    vec2 qc = p - c3.xy;
    if (dot(qc, qc) > ext * ext) return vec3(0.0);
    vec3 rd = normalize(p.x * cam.rt + p.y * cam.cu + cam.f * cam.fw);
    vec3 nP = cross(r.A, r.B);
    float dn = dot(rd, nP);
    dn = abs(dn) > 1e-5 ? dn : 1e-5;
    vec3 q = cam.ro + rd * (dot(r.C - cam.ro, nP) / dn) - r.C;
    float s0 = atan2_(dot(q, r.B), dot(q, r.A));
    float s1 = 0.0;
    float best = 1e9;
    for (int k = 0; k < 8; k++) {
        float sk = float(k) * (OR_TAU / 8.0);
        vec2 d2 = p - orProj(cam, r.C + r.A * cos(sk) + r.B * sin(sk)).xy;
        float dd = dot(d2, d2);
        if (dd < best) { best = dd; s1 = sk; }
    }
    OrPh h = orPhase(j * 3.0 + 5.0, S.t * 0.6);
    vec3 a = orStickAt(p, stretch, wAdd, mirror, cam, S, P, j, r, h, s0);
    vec3 b = orStickAt(p, stretch, wAdd, mirror, cam, S, P, j, r, h, s1);
    return max(a, b);
}

// light scattered toward the eye by the hall's haze from a point light at L
float orHaze(vec3 ro, vec3 rd, vec3 L, float tMax) {
    vec3 d = L - ro;
    float b = dot(d, rd);
    float hh = sqrt(max(dot(d, d) - b * b, 1e-4));
    return (atan((tMax - b) / hh) + atan(b / hh)) / hh;
}

// diffuse plus a broad wet sheen, times the light distance (l unnormalised)
float orLamb(vec3 n, vec3 v, vec3 l) {
    vec3 ln = normalize(l);
    float sp = pow(max(dot(normalize(ln + v), n), 0.0), 24.0);
    return (max(dot(n, ln), 0.0) + 6.0 * sp) * length(l);
}

// Concrete columns of the empty hall, lit only by the painting: the orb's
// cool light, the two warm stick heads and the spark bursts. x = distance
// along the ray to the nearest column hit (1e4 if none), yzw = its colour.
vec4 orColumns(vec3 ro, vec3 rd, vec3 O, vec3 cO, vec3 h0, vec3 c0, vec3 h1, vec3 c1) {
    float tb = 1e4;
    vec3 nb = vec3(0.0);
    for (int i = 0; i < 8; i++) {
        float fi = float(i);
        vec2 cc = vec2((mod(fi, 4.0) < 2.0 ? -1.0 : 1.0) * (3.1 + 3.0 * mod(fi, 2.0)), i < 4 ? 1.9 : 5.6);
        vec2 oc = ro.xz - cc;
        vec2 dz = rd.xz;
        float a = dot(dz, dz);
        float b = dot(oc, dz);
        float c = dot(oc, oc) - 0.34 * 0.34;
        float disc = b * b - a * c;
        if (disc < 0.0 || a < 1e-6) continue;
        float th = (-b - sqrt(disc)) / a;
        if (th > 0.0 && th < tb) {
            tb = th;
            nb = vec3((oc + dz * th) / 0.34, 0.0).xzy;
        }
    }
    if (tb > 9e3) return vec4(1e4, vec3(0.0));
    vec3 X = ro + rd * tb;
    float alb = 0.04 * (0.7 + 0.6 * vnoise(vec2(atan2_(nb.z, nb.x) * 3.0, X.y * 4.0)));
    // wet concrete: a soft sheen of each light along the column
    vec3 v = -rd;
    vec3 l = O - X;
    float dl = dot(l, l);
    vec3 col = cO * orLamb(nb, v, l) / (dl * sqrt(dl));
    l = h0 - X; dl = dot(l, l);
    col += c0 * orLamb(nb, v, l) / (dl * sqrt(dl) + 0.05);
    l = h1 - X; dl = dot(l, l);
    col += c1 * orLamb(nb, v, l) / (dl * sqrt(dl) + 0.05);
    return vec4(tb, col * alb);
}

// defocused lamps across the far end of the hall
vec3 orBokeh(vec3 rd, float yScale, OrPal P, float t, float lift) {
    float az = atan2_(rd.x, rd.z);
    vec2 b = vec2(az * 7.0, (rd.y - 0.015) * 7.0 * yScale);
    vec2 id = floor(b);
    if (id.y < 0.0 || id.y > 2.0) return vec3(0.0);
    vec2 f = fract(b) - 0.5;
    float h = hash21(id);
    if (h > 0.32) return vec3(0.0);
    vec2 j = (vec2(hash21(id + 7.0), hash21(id + 13.0)) - 0.5) * 0.3;
    float r = 0.1 + 0.2 * hash21(id + 21.0);
    float dist = length(f - j);
    float disc = smoothstep(r, r - 0.05, dist) * (0.7 + 0.3 * smoothstep(r - 0.12, r - 0.02, dist));
    vec3 warm = mix(vec3(1.0, 0.55, 0.18), P.c4, 0.3);
    vec3 cool = mix(P.c2, P.c3, hash21(id + 3.0));
    vec3 c = hash21(id + 5.0) < 0.6 ? warm : cool;
    float row = 1.0 - 0.3 * id.y;
    return c * disc * row * (0.025 + 0.07 * h) * (0.8 + 0.5 * lift);
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float t = mod(u.time, 600.0);
    float ign = fract(52.9829189 * fract(dot(uv * u.resolution, vec2(0.06711056, 0.00583715))));

    OrPal P = OrPal(orPalSteady(0.0, u), orPalSteady(0.2, u), orPalSteady(0.4, u), orPalSteady(0.6, u), orPalSteady(0.8, u));

    OrSt S;
    // the trace shapes follow the smoothed level and only a little of each
    // band, so painted rings breathe with the music instead of twitching
    float lv = saturate(u.level);
    for (int i = 0; i < 8; i++) S.sb[i] = 0.24 + 0.12 * lv + 0.02 * bandAt(u, (float(i) + 0.5) / 8.0);

    // ---- camera: a slow circle round the orb, low over the floor ----
    vec2 fl = osFlight(u, 0.35, -0.14);
    float s = fl.x;
    float yaw = 0.6 * sin(s * 0.041) + 0.25 * sin(s * 0.017 + 1.0);
    float camR = 3.0 + 0.3 * sin(s * 0.057 + 0.7);
    vec3 ro = vec3(camR * sin(yaw), 0.48 + 0.12 * sin(s * 0.049 + 2.0), -camR * cos(yaw));
    float R = mix(0.95, 1.35, u.param0);
    vec3 O = vec3(0.0, 1.45, 0.0);
    vec3 ta = vec3(0.0, 0.95, 0.0);
    float roll = osBank(u, 0.01);
    OrCam cam;
    cam.ro = ro;
    cam.fw = normalize(ta - ro);
    vec3 up = vec3(sin(roll), cos(roll), 0.0);
    cam.rt = normalize(cross(cam.fw, up));
    cam.cu = cross(cam.rt, cam.fw);
    cam.f = 1.45;
    vec3 rd = normalize(p.x * cam.rt + p.y * cam.cu + cam.f * cam.fw);

    // ---- beat clock and the painting state ----
    float bc = u.beatCount + saturate(u.beatPhase);
    float bph = saturate(u.beatPhase);
    float kick = sin(3.14159265 * bph) * sin(3.14159265 * bph);
    float warmBeat = mod(floor(u.beatCount), 2.0) < 0.5 ? 1.0 : 0.0;
    S.n = osSteps(7.0, 12.0, detail);
    S.nR = float(S.n);
    S.bc = bc;
    S.head = fract(bc / OR_CYC) * S.nR;   // one full figure every twelve beats
    S.R = R;
    S.O = O;
    S.t = t;
    float swing = mix(0.4, 1.7, u.param1);
    S.sw = swing * vec4(S.sb[1] + 0.25, S.sb[3] + 0.15, S.sb[5] + 0.1, S.sb[7] + 0.1 * lv) * 0.7;
    S.wob = dot(S.sw, vec4(0.10, 0.07, 0.045, 0.03));
    S.zc = orProj(cam, O).z;
    S.pix = 2.0 / u.resolution.y;
    S.coolKick = (1.0 - warmBeat) * kick * (0.02 + 0.01 * u.level);
    int iters = detail > 0.5 ? 2 : 1;
    float lift = 0.55 + 0.1 * saturate(u.level);

    // the two pixel-stick heads, warm moving lights
    vec3 hPos[2];
    vec3 hCol[2];
    for (int j = 0; j < 2; j++) {
        vec3 hX;
        vec3 hdX;
        float hs = orStickHead(S, float(j));
        orEval(orStickRing(S, float(j)), hs, orPhase(float(j) * 3.0 + 5.0, t * 0.6), S.sw * 0.45, hX, hdX);
        hPos[j] = hX;
        hCol[j] = orWarm(P, hs * 0.16 + float(j) * 0.45 + t * 0.02) * 0.8;
    }

    // ---- the hall: near-black air, distant defocused lamps ----
    vec3 col = vec3(0.002, 0.0025, 0.005) * (1.0 + 1.5 * exp(-abs(rd.y) * 12.0));
    if (rd.y > 0.0) col += orBokeh(rd, 1.0, P, t, lift);
    col += mix(P.c0, P.c2, 0.5) * 0.01 * exp(-abs(rd.y) * 14.0);

    // the orb's own glow in the air, cool, with a warm surge on spark beats
    vec2 oc = orProj(cam, O).xy;
    vec3 coolAvg = (P.c0 + P.c2 + P.c3) / 3.0;
    float sparkPow = mix(0.0, 1.4, u.param2) * (0.18 + 0.06 * u.level);
    vec3 warmLit = mix(vec3(1.0, 0.45, 0.1), P.c4, 0.3);
    float wAge = bc - 2.0 * floor(u.beatCount * 0.5);
    float warmGlow = (0.2 + 0.08 * (0.5 - 0.5 * cos(3.14159265 * wAge))) * sparkPow;
    vec3 cLight = coolAvg * (2.2 + 1.5 * lift + 1.0 * S.coolKick) + warmLit * 3.0 * warmGlow;
    vec4 colm = orColumns(ro, rd, O, cLight, hPos[0], hCol[0] * 2.0, hPos[1], hCol[1] * 2.0);
    float tFl = rd.y < -0.002 ? -ro.y / rd.y : 1e4;
    if (colm.x < tFl) col = colm.yzw * exp(-colm.x * 0.035);
    vec2 dq = p - oc;
    col += (coolAvg * (0.018 + 0.018 * lift) + warmLit * 0.03 * warmGlow) * exp(-dot(dq, dq) * 3.5);

    // ---- the wet floor ----
    if (rd.y < -0.002 && colm.x > tFl) {
        float tg = -ro.y / rd.y;
        vec3 gp = ro + rd * tg;
        float puddle = smoothstep(0.42, 0.6, vnoise(gp.xz * 0.55 + 3.0) * 0.65 + vnoise(gp.xz * 1.7) * 0.35);
        float fres = mix(0.22, 0.72, puddle);
        float rip = vnoise(gp.xz * vec2(2.5, 7.0) + vec2(t * 0.35, 0.0)) - 0.5;
        vec2 pr = p + vec2(0.0, rip * (0.003 + 0.01 * (1.0 - puddle)));
        vec2 str = vec2(1.0, mix(0.3, 1.0, puddle));
        vec3 refl = orOrb(pr, str, 0.002 + 0.006 * (1.0 - puddle), -1.0, 1, cam, S, P);
        vec4 cr = orColumns(gp, vec3(rd.x, -rd.y, rd.z), O, cLight, hPos[0], hCol[0] * 2.0, hPos[1], hCol[1] * 2.0);
        refl += cr.x < 1e3 ? cr.yzw : orBokeh(vec3(rd.x, -rd.y, rd.z), mix(0.4, 1.0, puddle), P, t, lift) * 1.5;
        for (int j = 0; j < 2; j++) refl += orRibbon(pr, str, 0.002 + 0.006 * (1.0 - puddle), -1.0, cam, S, P, float(j));
        // the orb and the sparks light the floor
        float r2 = dot(gp.xz, gp.xz);
        vec3 lit = (coolAvg * (0.5 + 0.4 * lift) + warmLit * 0.8 * warmGlow) * 0.05 / (1.0 + 0.8 * r2);
        for (int j = 0; j < 2; j++) {
            vec3 dl = hPos[j] - gp;
            float dl2 = dot(dl, dl);
            lit += hCol[j] * 0.05 * hPos[j].y / (dl2 * sqrt(dl2) + 0.02);
        }
        float rough = 0.8 + 0.4 * vnoise(gp.xz * 9.0);
        vec3 fl0 = refl * fres + lit * rough * (1.0 - 0.6 * puddle);
        col = mix(col, fl0, smoothstep(-0.002, -0.03, rd.y)) * exp(-tg * 0.035);
    }

    // ---- the painted orb and the sparks ----
    col += orOrb(p, vec2(1.0), 0.0, 1.0, iters, cam, S, P);
    for (int j = 0; j < 2; j++) col += orRibbon(p, vec2(1.0), 0.0, 1.0, cam, S, P, float(j));
    // haze lit by the orb and the two stick heads
    float tAir = rd.y < -0.002 ? -ro.y / rd.y : 30.0;
    vec3 hz = coolAvg * (0.5 + 0.5 * lift + 0.6 * S.coolKick) * orHaze(ro, rd, O, tAir);
    for (int j = 0; j < 2; j++) hz += hCol[j] * orHaze(ro, rd, hPos[j], tAir);
    col += hz * 0.006;
    // the LED head itself, a hot point
    {
        float headK = floor(S.head);
        float sect = floor((bc - fract(S.head) * OR_CYC / S.nR) / 24.0);
        OrRing hr = orForm(mod(sect, 4.0), headK, S.nR, O, R, S.sb[int(headK) - 8 * (int(headK) / 8)], hash11(sect * 1.37 + 0.3) * OR_TAU + 0.04 * (bc - fract(S.head) * OR_CYC / S.nR));
        hr = orTilt(hr, O, bc - fract(S.head) * OR_CYC / S.nR);
        vec3 hX;
        vec3 hdX;
        orEval(hr, fract(S.head) * OR_TAU, orPhase(headK, t), S.sw, hX, hdX);
        vec2 hp = orProj(cam, hX).xy;
        vec2 dv = p - hp;
        float dd = dot(dv, dv);
        col += mix(orCool(P, headK * 0.23 + sect * 0.27), vec3(1.0), 0.6) * (exp(-dd / (S.pix * S.pix * 6.0)) * 1.4 + 0.0003 / (dd + 0.0004));
    }
    float Rs = cam.f * R / S.zc;
    float yFloor = orProj(cam, vec3(0.0)).y;
    col += orSparks(p, oc, Rs * 0.92, yFloor, bc, u.beatCount, floor(mix(14.0, 26.0, detail)), S.pix, P, sparkPow);

    // mild vignette, IGN dither against banding
    col *= 1.0 - 0.3 * dot(p * 0.5, p * 0.5);
    col += (ign - 0.5) * 0.004;
    return orOffRed(max(col, vec3(0.0)));
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
