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
// Petals.
// A huge flower held against a lamp in a dark room: three rings of cupped
// petals, each one its own shape (broad or narrow, round, pointed or notched,
// with a ruffled rim), lit from behind so the light passes through the
// tissue and is filtered by its pigment. Thin rims and tips glow, the veins
// read as darker, denser lines in the glow, the base of each petal carries a
// deep blotch and dew drops on the near rings lens the glow and catch a
// glint. The lamp drifts very slowly behind the bloom, so the glow creeps
// across the petals. Within a ring each petal overlaps the one before it, like
// the blades of an iris. The rings sit at different depths: the camera's slow,
// level drift (no roll) slides them gently against each other, the bloom turns
// at a crawl and the far ring is out of focus; every sixteen beats the shot
// glides slowly to a slightly different framing. On every beat a faint ring of light
// runs out from the heart and the petals lift a touch as it passes, their
// rims stirring gently with the treble; waves of loose petals tear off at a
// slow steady pace and drift past at a distance, fading before they come
// close, with their own parallax. The light follows the music's smoothed
// level; the spectrum only barely stretches and tints each petal, so no
// petal or the bloom as a whole pulses with the beat.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Petal Count ; param1 = Bloom ; param2 = Detail

// The palette read: the visual's own palette with no beat or spectral nudge.
// The vivid tour swaps whole palettes of very different brightness every few
// seconds, which swells the light across the frame, so this visual keeps its
// own colours there (as the calm visuals do); a host's manifest palette still
// replaces them.
vec3 ptPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 8, u); }

const float PT_Z0 = 3.0;    // depth of the flower plane (screen units = world units there)
const int PT_WAVES = 6;      // waves of flying petals alive at once

// Petal in its own frame: x from base (0) to tip (1), y across. shp holds the
// petal's shape: x width, y tip taper (above 1 pointed), z tip notch depth,
// w the phase of the ripple running along its rim; ruf is the ripple depth.
// Returns (coverage, signed side -1..1 across the width, x, distance outside).
vec4 ptPetal(vec2 q, float soft, vec4 shp, float ruf) {
    float x = q.x;
    float xc = saturate(x);
    float w = 0.50 * shp.x * sqrt(xc) * pow(max(1.0 - xc * xc, 0.0), 0.5 * shp.y);
    w *= 1.0 + ruf * sin(xc * 17.0 + shp.w) * xc;
    w *= 1.0 - shp.z * exp(-q.y * q.y * 260.0) * smoothstep(0.7, 1.0, xc);
    float d = max(abs(q.y) - w, max(-x, x - 1.0) * 1.3);
    float cov = smoothstep(soft, -soft, d);
    float side = clamp(q.y / max(w, 1e-3), -1.0, 1.0);
    return vec4(cov, side, xc, d);
}

// The palette read once at five fixed stops; every colour in the frame is a
// cheap cyclic blend of these locals instead of a palette read per sample.
struct PtPal { vec3 c0; vec3 c1; vec3 c2; vec3 c3; vec3 c4; };
vec3 ptPal(PtPal P, float t) {
    float x = fract(t) * 5.0;
    float i = floor(x);
    float f = smoothstep(0.0, 1.0, x - i);
    vec3 a = i < 1.0 ? P.c0 : i < 2.0 ? P.c1 : i < 3.0 ? P.c2 : i < 4.0 ? P.c3 : P.c4;
    vec3 b = i < 1.0 ? P.c1 : i < 2.0 ? P.c2 : i < 3.0 ? P.c3 : i < 4.0 ? P.c4 : P.c0;
    return mix(a, b, f);
}

// Speed over the shared 32-beat profile, as osFlight, with the cycle's
// running distance read from a table instead of summed per pixel.
const float PT_RUN[33] = float[33](0.0, 1.0, 2.0, 3.0, 4.15, 5.6, 7.35, 9.4, 12.25, 15.75, 19.25, 22.75, 25.3, 26.325, 27.0, 27.95, 29.0, 30.15, 31.4, 32.75, 33.75, 34.35, 34.95, 35.55, 36.6, 38.475, 41.1, 44.475, 48.6, 53.1, 57.6, 61.05, 62.75);
vec2 ptFlight(VisualUniforms u, float cruise, float surge) {
    float n = floor(u.beatCount);
    int k = int(mod(n, 32.0));
    float ph = saturate(u.beatPhase);
    float v0 = osBeatSpeed(n);
    float v1 = osBeatSpeed(n + 1.0);
    float beats = floor(n / 32.0) * PT_RUN[32] + PT_RUN[k] + v0 * ph + 0.5 * (v1 - v0) * ph * ph;
    return vec2(u.time * cruise * 0.2 + beats * (cruise * 0.4 + surge), mix(v0, v1, ph));
}

// Push a colour away from its grey (the rose-gold stops are soft on their own).
vec3 ptSat(vec3 c, float k) {
    return max(mix(vec3(dot(c, vec3(0.2126, 0.7152, 0.0722))), c, k), vec3(0.0));
}

// Keep the bloom off pure saturated red after the post chain's tonemap: lift
// green and blue together just enough that no deep rose reads as red.
float ptTone(float x) { return x * (2.51 * x + 0.03) / (x * (2.43 * x + 0.59) + 0.14); }
float ptToneInv(float y) {
    y = clamp(y, 0.0, 0.999);
    float a = 2.51 - 2.43 * y, b = 0.03 - 0.59 * y;
    return (-b + sqrt(b * b + 0.56 * a * y)) / (2.0 * a);
}
vec3 ptOffRed(vec3 c) {
    float r = ptTone(c.r), g = ptTone(c.g), b = ptTone(c.b);
    float need = 0.27 * r - g - b;
    if (need <= 0.0) return c;
    c.g = ptToneInv(g + 0.5 * need);
    c.b = ptToneInv(b + 0.5 * need);
    return c;
}

// The light on a petal: sun is the lamp behind the bloom at this pixel, rimC
// the cool rim light, dew (density, time) the drops, glint their sparkle,
// blot the depth of the dark base and thk the tissue thickness.
struct PtLight { vec3 sun; vec3 rimC; vec2 dew; float glint; float blot; float thk; };

// Backlit petal shading. pe from ptPetal, q the petal-frame position, R the
// petal-to-screen rotation (screen = q * R), face how squarely it faces us
// (1 flat, 0 edge on). base/mid/tip are its pigment along its length, lit its
// light level. The front is lit only by a dim raking key; most of the colour
// is the lamp behind shining through the tissue, filtered by the pigment
// more deeply where the petal is thick: at the base, along the spine and in
// the veins, which therefore read as darker, richer lines.
vec3 ptShade(vec4 pe, vec2 q, mat2 R, float face, vec3 base, vec3 mid, vec3 tip,
             PtLight Li, float lit, float detail, float glowT, float seed) {
    float x = pe.z;
    float e = pe.y;
    // surface slope in the petal frame: cup across, arch along
    vec2 sl = vec2(0.9 * (x - 0.45), 1.1 * e * (0.4 + 0.6 * x));
    float vein = 0.0;
    float crepe = 0.5;
    if (detail > 0.0) {
        // main veins fanning out from the base, branching toward the tip
        float vc = q.y / (q.x + 0.12) * 7.0 + 0.5 + 0.15 * sin(x * 5.0 + seed * 6.0) * x;
        float v1 = 1.0 - smoothstep(0.03, 0.11, abs(fract(vc) - 0.5));
        float v2 = 1.0 - smoothstep(0.02, 0.07, abs(fract(vc * 2.0 + 0.5 + 0.3 * x) - 0.5));
        vein = (v1 + 0.5 * v2 * smoothstep(0.4, 0.75, x)) * smoothstep(0.05, 0.3, x)
             * smoothstep(1.0, 0.75, abs(e)) * detail;
        sl.y += 0.08 * detail * sin(fract(vc) * 6.2831853) * x;
        crepe = mix(0.5, vnoise(vec2(e * 18.0 + seed * 7.0, x * 3.0)), detail);
    }
    vec2 ss = sl * R;
    vec3 n = normalize(vec3(-ss, 0.55 + 0.6 * face));
    // dim warm key from the upper left, in front
    vec3 L = vec3(-0.45, 0.55, 0.70);
    float dl = dot(n, L);
    float dif = max(dl, 0.0);
    vec3 H = normalize(L + vec3(0.0, 0.0, 1.0));
    float spec = pow(max(dot(n, H), 0.0), 48.0);
    vec3 alb = mix(base, mid, smoothstep(0.0, 0.55, x));
    alb = mix(alb, tip, smoothstep(0.55, 1.0, x) * 0.7);
    // a deep blotch at the base of every petal
    float blot = 1.0 - smoothstep(0.08, 0.26, x + 0.04 * sin(e * 11.0 + seed * 9.0));
    alb = mix(alb, base * base * 0.7, blot * Li.blot);
    // the lamp through the tissue, filtered by the pigment
    float thick = (0.15 + 0.95 * (1.0 - x * x) * (1.0 - 0.7 * e * e)) * Li.thk + 0.4 * vein;
    vec3 filt = pow(max(alb, vec3(0.002)), vec3(0.5 + 1.8 * thick)) * exp(-1.3 * thick) * (1.0 - 0.45 * vein);
    float thin = 1.0 + 0.9 * smoothstep(0.72, 1.0, abs(e)) * x + 0.6 * smoothstep(0.8, 1.0, x);
    float ao = 0.2 + 0.8 * smoothstep(0.0, 0.4, x);
    vec3 c = alb * (0.004 + 0.12 * dif * dif + 0.012 * (dl * 0.5 + 0.5)) * ao;
    c += Li.sun * filt * thin * (0.8 + 0.4 * crepe) * (0.5 + 0.5 * face + glowT);
    // the cool rim light wraps the thin edge: complementary to the warm glow
    float rim = smoothstep(0.78, 1.0, abs(e)) * (0.3 + 0.7 * x);
    c += mix(tip, Li.rimC, 0.7) * rim * rim * (0.22 + glowT);
    c += vec3(1.0, 0.93, 0.85) * spec * 0.18 * (0.4 + 0.6 * detail);

    // dew drops: little lenses that magnify the glow, with a dark meniscus, a
    // caustic away from the key and a glint toward it; they creep to the base
    if (Li.dew.x > 0.0 && x > 0.3 && x < 0.92) {
        vec2 dq = q / 0.13;
        float lane = floor(dq.y);
        dq.x += Li.dew.y * (0.1 + 0.25 * hash11(lane * 3.1 + seed * 11.0));
        vec2 cid = floor(dq);
        float hd = hash21(cid + seed * 37.0);
        if (hd < Li.dew.x) {
            float rad = 0.22 + 0.18 * fract(hd * 13.7);
            vec2 cc = cid + 0.5 + (hash22(cid + 3.1) - 0.5) * (1.0 - 2.0 * rad);
            vec2 dv = (dq - cc) / rad;
            float dd = dot(dv, dv);
            float m = 1.0 - smoothstep(0.75, 1.0, dd);
            vec2 lq = R * vec2(-0.63, 0.77);
            vec2 qc = dv + 0.45 * lq;
            vec2 qh = dv - 0.42 * lq;
            vec3 drop = c * (1.35 - 0.95 * smoothstep(0.45, 1.0, dd));
            drop += Li.sun * filt * 1.4 * exp(-dot(qc, qc) * 9.0);
            drop += vec3(1.0, 0.97, 0.92) * Li.glint * exp(-dot(qh, qh) * 80.0);
            c = mix(c, drop, m);
        }
    }
    return c * lit;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p0 = motionCentered(uv, u);
    p0.y = -p0.y;
    // slow camera: a steady drift, push-in and roll.
    // Each layer of the scene sees it at its own depth k (1 = the middle
    // ring): nearer layers slide and swell more, so the drift reveals depth.
    vec2 flc = ptFlight(u, 0.0, 0.0);
    flc.x += 0.06 * u.time;
    float camT = mod(flc.x, 600.0);
    vec2 camOff = 0.06 * vec2(sin(camT * 0.13), 0.7 * sin(camT * 0.09 + 1.3));
    float camZ = 0.06 * sin(camT * 0.07);
    // every ten seconds the shot is reframed, gliding slowly over the span
    // to a new pose: the whole bloom, or a little closer on one side of it.
    // It runs on the clock, so a pause or a skipped beat never jumps the frame.
    float sb8 = u.time / 10.0;
    float sk = floor(sb8);
    float se = smoothstep(0.0, 1.0, sb8 - sk);
    float hk0 = hash11(mod(sk, 251.0) * 1.37 + 0.4);
    float hk1 = hash11(mod(sk + 1.0, 251.0) * 1.37 + 0.4);
    vec3 pose0 = vec3(vec2(cos(sk * 2.39996), sin(sk * 3.1)) * 0.03 * step(0.3, hk0), 1.0 + 0.04 * hk0 * step(0.3, hk0));
    vec3 pose1 = vec3(vec2(cos((sk + 1.0) * 2.39996), sin((sk + 1.0) * 3.1)) * 0.03 * step(0.3, hk1), 1.0 + 0.04 * hk1 * step(0.3, hk1));
    vec3 pose = mix(pose0, pose1, se);
    camOff += pose.xy;
    p0 = p0 / pose.z;
    float detail = osDetail(u);
    float t = mod(u.time, 600.0);

    float nPet = floor(mix(5.0, 12.0, u.param0) + 0.5);
    float bloom = mix(0.85, 1.30, u.param1);
    float detailAmt = mix(0.3, 1.0, u.param2) * detail;
    float drift = 0.05 * u.spectralCentroid;
    // the spectrum, read once at eight stops
    float sb[9];
    for (int i = 0; i < 8; i++) sb[i] = bandAt(u, (float(i) + 0.5) / 8.0);
    sb[8] = sb[0];

    // the palette at five fixed stops: deep, rose, coral, gold, and the cool
    // far end that rims the bloom
    PtPal P = PtPal(ptPalSteady(0.02, u), ptPalSteady(0.24, u), ptPalSteady(0.48, u),
                    ptPalSteady(0.72, u), ptPalSteady(0.97, u));
    float bp = saturate(u.beatPhase);
    float kick = (1.0 - bp) * (1.0 - bp);
    vec3 cool = ptSat(P.c4, 1.35);
    vec3 gold = ptSat(ptPal(P, 0.60 + drift), 1.3);
    // the accent eases between a warm gold and an icy cool light, beat by beat
    float odd = 0.5 - 0.5 * cos(3.14159265 * (u.beatCount + bp));
    vec3 accent = mix(gold * vec3(1.15, 1.0, 0.8), mix(cool, vec3(0.75, 0.9, 1.0), 0.35), odd);
    // a ring of light runs out from the heart through the rings on each beat
    float front = (0.10 + 1.35 * bp) * bloom;
    float waveAmp = smoothstep(0.0, 0.25, bp) * (1.0 - bp) * (0.02 + 0.01 * u.level);

    // the lamp behind the bloom: warm white, its hot spot drifting slowly
    vec2 sunPos = 0.5 * bloom * vec2(cos(t * 0.07), sin(t * 0.05 + 1.0));
    vec2 sd = p0 - sunPos;
    float spot = exp(-dot(sd, sd) * 1.4);
    PtLight Li;
    Li.sun = mix(vec3(1.0, 0.86, 0.95), gold, 0.15) * (0.22 + 0.85 * spot) * (0.95 + 0.04 * u.level);
    Li.rimC = cool;
    Li.dew = vec2(0.0, 0.06 * t);
    Li.glint = 0.45 + 0.15 * u.level;
    Li.blot = 0.8;
    Li.thk = 1.0;

    // ---- backdrop: near black, the lamp just showing through the gaps ----
    vec2 pm = p0 / (1.0 + camZ) - camOff;
    float r = length(pm);
    float edgeR = 1.05 * bloom;
    vec3 col = ptSat(P.c0, 1.2) * 0.002 * exp(-r * 1.5);
    col += Li.sun * 0.03 * spot * smoothstep(edgeR * 1.1, 0.2, r);
    // far defocused lights, deep in the room behind
    vec2 pb = p0 / (1.0 + camZ * 0.3) - camOff * 0.3;
    for (int L = 0; L < 2; L++) {
        float fl = float(L);
        float sc = 2.2 + 1.6 * fl;
        vec2 bq = pb * sc + vec2(t * 0.03 * (1.0 + fl), -t * 0.02);
        vec2 cell = floor(bq);
        vec2 f = fract(bq) - 0.5;
        vec2 h = hash22(mod(cell, 113.0) + fl * 7.1);
        vec2 dv = f - (h - 0.5) * 0.3;
        float rad = 0.08 + 0.12 * h.y;
        float dl = length(dv);
        float disc = smoothstep(rad, rad - 0.05, dl) * (0.5 + 0.5 * smoothstep(rad - 0.07, rad - 0.01, dl));
        disc *= step(0.74, h.x);
        vec3 bc = h.y > 0.66 ? cool : h.y > 0.33 ? ptSat(ptPal(P, 0.22), 1.3) : gold;
        col += bc * disc * (0.010 + 0.010 * u.level) / (1.0 + fl);
    }

    // ---- the bloom: three rings of petals, back to front ----
    // the bloom turns slowly and the rings breathe open and shut, each ring
    // out of step with the next; the music never jolts the bloom's size
    float spin = 0.025 * flc.x;
    float breath = bloom;
    float shiver = 0.003 + 0.002 * u.level;
    for (int L = 0; L < 3; L++) {
        float fl = float(L);
        float kp = 0.82 + 0.18 * fl;
        vec2 pl = p0 / (1.0 + camZ * kp) - camOff * kp;
        float rl = length(pl);
        float al = atan2_(pl.y, pl.x + 1e-6);
        float n = nPet + fl;
        float sector = 6.2831853 / n;
        float phi = spin * (1.0 - 0.35 * fl) + fl * sector * 0.5 + 0.06 * sin(camT * 0.3 + fl);
        float lenL = breath * (1.18 - 0.34 * fl) * (1.0 + 0.02 * sin(camT * 0.9 - fl * 1.4));
        // focus on the middle ring: the far ring is soft and loses its veins
        float soft = L == 0 ? 0.034 : L == 1 ? 0.008 : 0.013;
        float dAmt = L == 0 ? 0.0 : detailAmt;
        float lit = 0.42 + 0.3 * fl;
        Li.dew.x = L == 0 ? 0.0 : 0.13 * step(0.5, detailAmt);
        // outer petals deep rose, inner ones coral running to gold
        float hb = 0.00 + 0.05 * fl + drift;
        // the beat's lift reaches this ring as the wave of light passes
        float puff = waveAmp * exp(-pow((0.6 * lenL - front) * 4.0, 2.0));
        // this sector's petal and both neighbours can cover the pixel; each
        // petal overlaps the one before it, like the blades of an iris, so the
        // stacking never changes across a sector boundary
        float idx0 = floor((al - phi) / sector + 0.5);
        for (int k = 0; k < 3; k++) {
            float idx = idx0 + float(k) - 1.0;
            float ia = phi + idx * sector;
            float iw = mod(idx, n);
            float hv = hash11(iw + fl * 13.0);
            float hv2 = hash11(iw * 1.7 + fl * 5.3 + 2.0);
            float hv3 = hash11(iw * 2.9 + fl * 3.1 + 7.0);
            float bf = fract(iw / n + 0.07 * fl) * 8.0;
            int bi = int(bf);
            float lvl = 0.6 * u.level + 0.15 * mix(sb[bi], sb[bi + 1], bf - float(bi));
            float len = lenL * (0.88 + 0.02 * lvl + 0.02 * sin(t * 0.15 + iw * 1.7))
                      * (0.9 + 0.2 * hv3) * (1.0 + 0.06 * puff);
            // beyond the tip and its shadow the petal adds nothing
            if (rl > len * 1.3) continue;
            // each petal its own shape: broad or narrow, round or pointed,
            // some notched, with a ripple running along the rim
            vec4 shp = vec4(mix(0.85, 1.25, hv2) * (1.0 - 0.08 * fl), mix(0.6, 1.7, hv3),
                            hv > 0.55 && hv3 < 0.45 ? 0.16 : 0.0, hv * 40.0 + t * 0.1 * (0.7 + 0.6 * hv2));
            float ruf = (0.015 + 0.045 * hv3) * (1.0 + 1.5 * puff);
            mat2 R = rot2(ia);
            vec2 q = R * pl / len;
            // the tip curls to one side, lifts with the beat, shivers with treble
            q.y += (0.05 * sin(iw * 2.1 + fl) + 0.08 * puff * (hv2 - 0.5)
                   + shiver * sin(t * 0.4 + iw * 1.3 + fl * 2.0)) * q.x * q.x;
            vec4 pe = ptPetal(q, soft / len, shp, ruf);
            float wv = waveAmp * exp(-pow((rl - front) * 7.0, 2.0));
            // soft shadow cast on what lies behind, and the cool rim light
            // bleeding round the silhouette
            float outD = max(pe.w, 0.0) * len;
            col *= 1.0 - 0.75 * exp(-outD * 14.0) * (1.0 - pe.x) * smoothstep(0.0, 0.3, pe.z);
            col += mix(cool, accent, wv) * exp(-outD * 30.0) * (1.0 - pe.x) * (0.025 + 0.08 * wv) * lit * (0.9 + 0.1 * lvl);
            if (pe.x > 0.001) {
                // each petal its own shade, and louder ones warmer and brighter
                float hp = hb + 0.1 * (hv - 0.5) + 0.02 * lvl;
                vec3 cBase = ptSat(ptPal(P, hp), 1.35) * 0.8;
                vec3 cMid = ptSat(ptPal(P, hp + 0.18), 1.35);
                vec3 cTip = ptSat(ptPal(P, hp + 0.30 + 0.03 * fl), 1.35);
                PtLight Lw = Li;
                Lw.rimC = mix(cool, accent, wv);
                vec3 pc = ptShade(pe, q, R, 1.0, cBase, cMid, cTip, Lw,
                                  lit * (0.85 + 0.2 * lvl), dAmt, 0.2 * wv, hv + fl);
                col = mix(col, ptSat(pc, 1.3), pe.x);
            }
        }
    }
    // glowing heart with a crown of anthers that sparkle with the treble
    vec2 ph = p0 / (1.0 + camZ * 1.1) - camOff * 1.1;
    float rh = length(ph);
    float ah = atan2_(ph.y, ph.x + 1e-6);
    vec3 coral = ptSat(P.c2, 1.3);
    float core = exp(-rh * rh / (0.010 * breath));
    col += mix(gold, coral, 0.35) * core * (0.26 + 0.12 * u.level);
    float sa = ah / 6.2831853 * 21.0;
    float si = floor(sa);
    float hA = hash11(si + 3.7);
    float stamR = 0.20 * breath * (0.82 + 0.30 * hA);
    float sf = (fract(sa) - 0.5) * 6.2831853 / 21.0 * rh;   // across the filament, in screen units
    col += gold * exp(-sf * sf * 9.0e4) * smoothstep(stamR, 0.03, rh) * step(0.03, rh) * 0.10;
    float ba = (si + 0.5) * 6.2831853 / 21.0;
    float db = length(ph - vec2(cos(ba), sin(ba)) * stamR);
    float rb = 0.011 * breath * (0.8 + 0.4 * hA);
    float tw = 0.5 + 0.5 * sin(t * (0.6 + 0.6 * hA) + hA * 40.0);
    float bead = smoothstep(rb, rb * 0.35, db);
    col = mix(col, gold * 0.15, bead * 0.6);
    col += mix(gold, vec3(1.0, 0.95, 0.85), 0.4) * smoothstep(rb * 0.8, 0.0, length(ph - vec2(cos(ba), sin(ba)) * stamR + vec2(0.3, -0.35) * rb))
         * (0.5 + 0.4 * u.level * tw);
    col += mix(gold, accent, 0.5) * exp(-db * db / (rb * rb * 2.5)) * (0.05 + 0.06 * u.level * tw + 0.03 * kick);

    // ---- loose petals tearing off and tumbling toward the camera ----
    // a slow steady clock: a new wave tears off every few seconds
    float T = 0.04 * u.time;
    float nw = floor(T);
    int perWave = osSteps(6.0, 12.0, detail);
    float pw = float(perWave);
    float sector = 6.2831853 / pw;
    float flare = 1.0;
    Li.dew.x = 0.0;
    Li.blot = 0.3;
    Li.thk = 0.6;
    for (int w = PT_WAVES - 1; w >= 0; w--) {
        float fw = float(w);
        float wave = nw - fw;
        float age = T - wave;                    // 0 .. PT_WAVES
        float wid = mod(wave, 997.0);
        float z = PT_Z0 - age * (PT_Z0 - 0.25) / float(PT_WAVES);
        float fadeIn = smoothstep(0.0, 0.5, age);
        float fadeOut = smoothstep(1.1, 2.0, z);
        float defocus = smoothstep(1.5, 0.45, z);   // near petals blur out
        float glowW = (w == 0 ? flare : 1.0) * (0.94 + 0.05 * u.level);
        float sc = PT_Z0 / z;
        // nearer petals slide further with the camera's drift
        float kp = min(1.0 + 0.3 * (sc - 1.0), 2.2);
        vec2 pw2 = p0 / (1.0 + camZ * kp) - camOff * kp;
        float r2 = length(pw2);
        // the wave's petals all lie in one annulus; skip it when the pixel is outside
        if (r2 < (0.35 + age * 0.20) * sc - 0.36 * sc || r2 > (0.80 + age * 0.45) * sc + 0.36 * sc) continue;
        float a2 = atan2_(pw2.y, pw2.x + 1e-6);
        // each wave tears from its own part of the bloom, so its colour varies
        float hw = 0.05 + 0.12 * hash11(wid * 1.31 + 0.5) + drift;
        vec3 fBase = ptSat(ptPal(P, hw), 1.35) * 0.8;
        vec3 fMid = ptSat(ptPal(P, hw + 0.18), 1.35);
        vec3 fTip = ptSat(ptPal(P, hw + 0.34), 1.35);
        PtLight Lw = Li;
        Lw.sun *= glowW * 0.55;
        Lw.rimC = cool;
        // each petal of a wave owns one angular sector, so only the pixel's
        // sector and its two neighbours can cover it
        float base = mod(wid * 2.39996, 6.2831853);
        float j0 = floor(mod((a2 - base) / sector, pw));
        for (int k = 0; k < 3; k++) {
            float fj = mod(j0 + float(k) - 1.0, pw);
            vec4 h = vec4(hash22(vec2(wid * 0.37 + fj * 3.1, fj + 1.7)),
                          hash22(vec2(fj * 7.7 + 2.0, wid * 0.53 + 9.0)));
            float th = sector * (fj + h.x) + base + age * 0.10 * (h.w - 0.5);
            float rr = 0.35 + 0.45 * h.y + age * (0.20 + 0.25 * h.z);
            vec2 c = vec2(cos(th), sin(th)) * rr * sc;
            float sz = (0.30 + 0.12 * h.z) * sc;
            vec2 dv = pw2 - c;
            if (dot(dv, dv) > sz * sz * 0.7) continue;
            // orientation: along the flight direction, tumbling and fluttering
            float tumble = h.w * 6.28 + age * (0.5 + 0.4 * h.x) + 0.08 * sin(t * 0.6 + h.y * 20.0);
            float ori = th + 1.2 * (h.z - 0.5) + age * 0.5 * (h.y - 0.5);
            mat2 R = rot2(ori);
            vec2 q = R * dv / sz;
            float flip = cos(tumble);
            q.y /= max(abs(flip), 0.18);
            q.x += 0.5;
            vec4 shp = vec4(mix(0.8, 1.2, h.x), mix(0.6, 1.6, h.y), h.z > 0.6 && h.y < 0.4 ? 0.16 : 0.0, h.w * 30.0 + t * 0.4);
            vec4 pe = ptPetal(q, 0.006 * z + defocus * 0.20, shp, 0.03 * (1.0 - defocus));
            float alpha = pe.x * fadeIn * fadeOut * (1.0 - 0.45 * defocus);
            // the petal's own shadow falls on the bloom behind it
            float outD = max(pe.w, 0.0) * sz;
            col *= 1.0 - 0.4 * exp(-outD * 10.0) * (1.0 - pe.x) * fadeIn * fadeOut * (1.0 - defocus);
            if (alpha > 0.001) {
                // a tumbling petal turns its cup through the key light, and the
                // edge-on moment lets a little more of the lamp through it
                vec2 qs = vec2(q.x, q.y * sign(flip));
                vec3 pc = ptShade(pe, qs, R, abs(flip), fBase, fMid, fTip, Lw,
                                  0.8 + 0.4 * h.y, defocus > 0.4 ? 0.0 : detailAmt * (1.0 - defocus),
                                  0.15 * (1.0 - abs(flip)), h.z * 3.0);
                pc = mix(pc, (fMid + fTip) * 0.10 * glowW, defocus * 0.5);   // soft bokeh body
                col = mix(col, ptSat(pc, 1.3), alpha);
            }
        }
    }

    // gentle vignette keeps the edges deep
    col *= 1.0 - 0.55 * smoothstep(0.9, 2.0, length(p0));
    col = ptSat(col, 1.15);
    return ptOffRed(max(col, vec3(0.0))); // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
