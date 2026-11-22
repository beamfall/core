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
// Phosphor Scope — a macro shot of a vector CRT with a two-colour phosphor.
// The beam writes a slowly tumbling 3D Lissajous figure; where it lands the
// core of the stroke flashes cool, then settles into a long, even afterglow
// (amber and violet on alternate figures, matched in brightness), so every
// stroke carries its age as a hue behind a white-hot write head.
// The beam writes at a steady, unhurried speed with no audio surges; each
// kick rings the deflection very slightly, so the figure records the beat as
// a faint ripple that trails round the loop, and every sixteen beats the beam
// takes a new frequency ratio: the new figure is written in while the last
// one dissolves along its trace, never dimming all at once. The phosphor
// colours are held at one luma (no centroid flicker, no palette-tour pumping)
// and the write head and halo are soft, so the scope glows without flashing.
// Curved glass carries softbox reflections, an etched graticule and
// halation; a graphite bezel catches the glow. The far side of the tube
// falls out of focus.
//
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 Persistence → how long the afterglow lasts along the trace
// param1 Figure Complexity → frequency ratios of the figures
// param2 Beam Fuzz → beam softness and treble jitter

// The palette read: the visual's own palette (3) with no beat or spectral
// nudge and no palette tour, so the host's vivid tour never swells the light;
// a host's manifest palette still replaces it. The fog tint is rebuilt on it.
vec3 psPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 3, u); }
vec3 psPalSteadyFog(float t, VisualUniforms u) {
    vec3 c = psPalSteady(t, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}

const float PS_W = 1.45;   // tube face half width
const float PS_H = 1.0;    // tube face half height
const float PS_R = 0.16;   // corner radius
const float PS_BUL = 0.07; // glass bulge
const float PS_LIP = 0.22; // bezel width

float psBox(vec2 q, vec2 b, float r) {
    vec2 d = abs(q) - b + r;
    return length(max(d, 0.0)) + min(max(d.x, d.y), 0.0) - r;
}

// Studio environment seen in the glass: a cool overhead softbox, a warm
// vertical strip light to one side, and a faint room.
vec3 psEnv(vec3 r) {
    vec2 bq = (r.xy - vec2(-0.42, 0.33)) / vec2(0.28, 0.09);
    float box = smoothstep(1.0, 0.55, max(abs(bq.x), abs(bq.y))) * (0.45 + 0.55 * exp(-dot(bq, bq) * 1.5));
    float strip = smoothstep(0.007, 0.002, abs(r.x - 0.40)) * smoothstep(0.55, 0.30, abs(r.y + 0.02));
    float room = 0.02 * smoothstep(-0.6, 0.6, r.y);
    return vec3(0.70, 0.85, 1.0) * box * 2.2 + vec3(1.0, 0.82, 0.62) * strip * 4.0 + vec3(room);
}

// Frequencies of the figure shown in bar k: x and y are always coprime, so
// each figure is a single closed loop; complexity c raises both.
vec3 psRatio(float k, float c) {
    float h1 = hash11(k * 3.7 + 1.3), h2 = hash11(k * 7.1 + 4.1), h3 = hash11(k * 1.3 + 9.7);
    float a = 1.0 + floor(h1 * 2.99) + c;
    float b = a + 1.0 + ((mod(a, 2.0) > 0.5 && h2 > 0.5) ? 1.0 : 0.0);
    vec2 xy = h3 > 0.5 ? vec2(a, b) : vec2(b, a);
    return vec3(xy, 1.0 + floor(hash11(k * 5.3 + 2.2) * 2.99));
}

// The palette without the per-frame centroid nudge: the raw centroid jumps
// from hop to hop, and on the phosphor's saturated colours that nudge flickers
// the whole figure.
vec3 psPal(float t, VisualUniforms u) {
    return psPalSteady(t, u);
}

vec2 psRotStep(vec2 v, vec2 s) { return vec2(v.x * s.x - v.y * s.y, v.y * s.x + v.x * s.y); }

const int PS_SUB = 4; // fine steps per coarse chord near the trace

// Beam deflection for raw oscillator values q: the kick ring scales it, then
// the figure is sized and tumbled; returns the screen position and the depth.
vec3 psDeflect(vec3 q, float ring, vec3 amp, vec2 cy, vec2 cx) {
    q *= (1.0 + ring) * amp;
    q.xz = vec2(q.x * cy.x - q.z * cy.y, q.x * cy.y + q.z * cy.x);
    q.yz = vec2(q.y * cx.x - q.z * cx.y, q.y * cx.y + q.z * cx.x);
    return vec3(q.xy * (1.0 + 0.12 * q.z), q.z);
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float tm = mod(u.time, 600.0);

    // ---- camera: a slow, eased drift across the tube face ----
    float fd = mod(osFlight(u, 0.05, 0.02).x, 600.0);
    float sw = sin(fd * 0.27 + 0.8);
    float yaw = (0.14 + 0.10 * abs(sw)) * sign(sw) * smoothstep(0.0, 0.35, abs(sw)) + 0.05 * sin(fd * 0.13);
    float pitch = 0.09 * sin(fd * 0.23 + 2.0);
    float dist = 1.86 + 0.12 * sin(fd * 0.17);
    vec3 ro = vec3(sin(yaw) * cos(pitch), sin(pitch), cos(yaw) * cos(pitch)) * dist;
    vec3 ta = vec3(-0.22 * sin(yaw), -0.15 * sin(pitch), 0.0);
    vec3 rd = osCamera(ro, ta, osBank(u, 0.01), 2.45, p);

    // ---- hit the phosphor plane (z = 0) and the glass above it ----
    float tz = rd.z < -1e-3 ? -ro.z / rd.z : 1e3;
    vec2 sp = (ro + rd * tz).xy;
    float sd = psBox(sp, vec2(PS_W, PS_H), PS_R);
    float inScreen = 1.0 - smoothstep(-0.004, 0.004, sd);
    vec2 nq = sp / vec2(PS_W, PS_H);
    float bul = max(1.0 - 0.5 * dot(nq, nq), 0.0);
    float tg = rd.z < -1e-3 ? (0.045 * bul - ro.z) / rd.z : 1e3;
    vec2 gp = (ro + rd * tg).xy;
    float focusD = dist - 0.05 + 0.12 * sin(fd * 0.19);
    float blur = abs(tz - focusD) * 0.045;

    // ---- the beam's state ----
    float pers = saturate(u.param0);
    float cpx = floor(saturate(u.param1) * 2.99);
    float fuzz = saturate(u.param2);
    // a steady, unhurried write speed (an eighth of a loop a second): the
    // music never speeds the beam, so the bright head never races the face
    vec2 wr = osFlight(u, 0.3, -0.12);

    float build = 0.0;

    // beats of music written per loop of trace, for the kick ripples
    float kB = 6.0;
    // Every sixteen beats the beam starts on the next frequency ratio: the
    // new figure builds over that span while the last one dissolves along its
    // trace, so the strokes never all dim at once. Both are traced
    // while it does, so no stroke slides or snaps. The ratios are integers,
    // so only the fraction of a loop the beam has written matters.
    float bar = (u.beatCount + saturate(u.beatPhase)) / 16.0;

    float kb = mod(floor(bar), 64.0);
    float xf = smoothstep(0.0, 1.0, fract(bar));
    float wOld = 1.0 - xf;

    float th = 6.2831853 * fract(wr.x);
    float ringAmp = 0.0012 * (0.6 + 0.4 * u.sensitivity);
    // a fixed figure size: the level never breathes the strokes across the face
    vec3 amp = vec3(0.94, 0.93, 0.7) * vec3(PS_W * 0.50, PS_H * 0.46, 0.70);
    // slow tumble of the figure
    float ry = tm * 0.012, rx = 0.35 + 0.25 * sin(tm * 0.013);
    vec2 cy = vec2(cos(ry), sin(ry)), cx = vec2(cos(rx), sin(rx));

    float bw = 0.0034 + 0.004 * fuzz + blur * 0.4;
    float hw = 0.05 + 0.03 * fuzz + blur * 1.5;
    float bw2 = bw * bw, hw2 = hw * hw, bc2 = bw2 * 9.0;
    float jit = 0.0005 * fuzz;

    // the phosphor's two emissions and the kick accent, read at fixed stops
    vec3 cFlash = psPal(0.20, u);
    cFlash *= 0.4 / max(luma(cFlash), 0.05);

    // alternate figures settle into amber or violet, matched in brightness so
    // the change is one of hue; the kick accent takes another stop at the same
    // brightness, and both cross over with the dissolve
    float par = mod(floor(bar), 2.0);
    float alt = mix(1.0 - par, par, xf);
    vec3 cAmber = psPal(0.97, u), cViolet = psPal(0.74, u);
    // a fixed afterglow brightness, so the palette tour's crossfades change
    // only the hue of the strokes, never how bright they are
    float lAV = 0.3;

    cAmber *= lAV / max(luma(cAmber), 0.05);
    cViolet *= lAV / max(luma(cViolet), 0.05);
    vec3 cGlow = mix(cAmber, cViolet, alt);
    vec3 cKick = mix(psPal(0.52, u), cAmber, alt);

    cKick *= lAV / max(luma(cKick), 0.05);
    // the flash trails the head at the afterglow's own brightness, half way to
    // its hue, so the passing head tints the strokes without pulsing them
    vec3 cTrail = cFlash * (lAV / max(luma(cFlash), 0.05));
    // the cool flash runs further down the trace when the music builds
    float iFlash = 1.0 / (0.07 + 0.10 * build + 0.03 * u.level);
    float gK = 3.0 / mix(0.25, 1.4, pers);

    // The trace is walked back from the write head in coarse chords (about
    // six per period of the fastest axis). Far from a chord its glow is summed
    // from the chord; near one, the chord is refined into PS_SUB fine steps,
    // walked forward again from its older end, for the sharp core; the two
    // are blended so no seam shows. Every per-step quantity is a recurrence.
    float phx = tm * 0.006, phz = tm * 0.005 + 1.3;
    float bp0 = saturate(u.beatPhase);
    float best = 1e4;
    float hAcc = 0.0, hT = 0.0, hK = 0.0, hZ = 0.0, wAcc = 0.0;
    float cW = 1e-6, cM = 0.0, cT = 0.0, cK = 0.0, cZ = 0.0;
    vec2 headNew = vec2(0.0), headOld = vec2(0.0);
    // the new figure, and the last one while it dissolves
    for (int fig = 0; fig < 2; fig++) {
        if (sd >= 0.15 || (fig == 1 && xf >= 1.0)) break;
        vec3 fq3 = psRatio(kb - float(fig), cpx);
        // The dissolve runs along the trace, not over the whole figure at
        // once: the new figure lights from the head back, the old one goes
        // dark from its oldest end, so no two strokes dim in step.
        float fwA = 2.0 * (fig == 0 ? fract(bar) : 1.0 - fract(bar));
        float fMax = max(fq3.x, fq3.y);
        int m = int(clamp(ceil(fMax * mix(6.0, 8.0, detail)), 14.0, 48.0));
        float fm = float(m);
        float dc = 6.2831853 / fm, df = dc / float(PS_SUB);
        vec2 ax = vec2(cos(fq3.x * th + phx), sin(fq3.x * th + phx)), ay = vec2(cos(fq3.y * th), sin(fq3.y * th));
        vec2 az = vec2(cos(fq3.z * th + phz), sin(fq3.z * th + phz));
        vec2 vj = vec2(cos(tm * 1.3), sin(tm * 1.3));
        vec2 cax = vec2(cos(fq3.x * dc), -sin(fq3.x * dc)), cay = vec2(cos(fq3.y * dc), -sin(fq3.y * dc));
        vec2 caz = vec2(cos(fq3.z * dc), -sin(fq3.z * dc)), cj = vec2(cos(37.0 * dc), sin(37.0 * dc));
        // fine steps run toward the head (younger), the opposite way to the coarse walk
        vec2 fax = vec2(cos(fq3.x * df), sin(fq3.x * df)), fay = vec2(cos(fq3.y * df), sin(fq3.y * df));
        vec2 faz = vec2(cos(fq3.z * df), sin(fq3.z * df)), fj = vec2(cos(37.0 * df), -sin(37.0 * df));
        float dbp = kB / fm;
        float cbp = bp0, cenv = exp2(-bp0 * 9.0), envUp = exp2(9.0 * dbp);
        vec2 rg = vec2(cos(bp0 * 25.13), sin(bp0 * 25.13)), rgs = vec2(cos(dbp * 25.13), -sin(dbp * 25.13));
        float glow = 1.0, glowStep = exp2(-gK / fm), envPrev = cenv;
        float dbpF = dbp / float(PS_SUB), envDnF = exp2(-9.0 * dbpF), glowUpF = exp2(gK / (fm * float(PS_SUB)));
        vec2 rgsF = vec2(cos(dbpF * 25.13), sin(dbpF * 25.13));
        float dsF = 1.0 / (fm * float(PS_SUB));
        vec3 prevC = vec3(0.0);
        for (int j = 0; j <= 48; j++) {
            if (j > m) break;
            float s = float(j) / fm;
            vec3 pt = psDeflect(vec3(ax.y, ay.y, az.y), cenv * rg.y * ringAmp, amp, cy, cx);
            if (j == 0) {
                if (fig == 0) headNew = pt.xy; else headOld = pt.xy;
            } else {
                vec2 ab = pt.xy - prevC.xy;
                float ab2 = max(dot(ab, ab), 1e-8);
                vec2 dv = sp - prevC.xy - ab * saturate(dot(sp - prevC.xy, ab) / ab2);
                float d2 = dot(dv, dv);
                float sm = s - 0.5 / fm;
                float mw = (0.85 + 0.15 * glow * inversesqrt(glowStep)) * saturate(fwA - sm) * sqrt(ab2);
                float kw = 0.05 / (d2 + 0.05);
                wAcc += kw * kw * mw;
                float rf = smoothstep(0.05, 0.022, d2);
                if (rf < 1.0) {
                    float hk = hw2 / (d2 + hw2);
                    float w = hk * hk * mw * (1.0 - rf);
                    hAcc += w;
                    hT += w * saturate(sm * iFlash);
                    hK += w * cenv * envPrev * (1.0 - 0.6 * sm);
                    hZ += w * 0.5 * (prevC.z + pt.z);
                }
                if (rf > 0.0) {
                    // walk this chord from its older end (sample j) toward the head
                    vec2 rax = ax, ray = ay, raz = az, rj = vj, frg = rg;
                    float fbp = cbp, fenv = cenv, fgl = glow, fs = s;
                    float segLen = sqrt(ab2) / float(PS_SUB);
                    vec3 fp = vec3(0.0);
                    for (int k = 0; k <= PS_SUB; k++) {
                        vec3 qf = vec3(rax.y, ray.y, raz.y);
                        qf.xy += vec2(rj.y, rj.x) * jit;
                        vec3 fq = psDeflect(qf, fenv * frg.y * ringAmp, amp, cy, cx);
                        if (k > 0) {
                            vec2 fb = fq.xy - fp.xy;
                            float fb2 = max(dot(fb, fb), 1e-8);
                            vec2 fv = sp - fp.xy - fb * saturate(dot(sp - fp.xy, fb) / fb2);
                            float e2 = dot(fv, fv);
                            best = min(best, e2);
                            float fmw = (0.85 + 0.15 * fgl) * saturate(fwA - fs);

                            float tcf = saturate(fs * iFlash);
                            float kcf = fenv * fenv * (1.0 - 0.6 * fs);
                            float hk = hw2 / (e2 + hw2);
                            float w = hk * hk * fmw * segLen * rf;
                            hAcc += w;
                            hT += w * tcf;
                            hK += w * kcf;
                            hZ += w * fq.z;
                            float kc = bc2 / (e2 + bc2);
                            kc *= kc;
                            kc *= kc;
                            cW += kc;
                            cM += kc * fmw;
                            cT += kc * tcf;
                            cK += kc * kcf;
                            cZ += kc * fq.z;
                        }
                        fp = fq;
                        rax = psRotStep(rax, fax); ray = psRotStep(ray, fay); raz = psRotStep(raz, faz);
                        rj = psRotStep(rj, fj);
                        frg = psRotStep(frg, rgsF);
                        fbp += dbpF;
                        fenv *= envDnF;
                        if (fbp >= 1.0) { fbp -= 1.0; fenv *= 512.0; }
                        fgl *= glowUpF;
                        fs -= dsF;
                    }
                }
            }
            prevC = pt;
            ax = psRotStep(ax, cax); ay = psRotStep(ay, cay); az = psRotStep(az, caz);
            vj = psRotStep(vj, cj);
            // step the ring back one chord; it restarts at each earlier beat
            envPrev = cenv;
            cbp -= dbp;
            cenv *= envUp;
            if (cbp < 0.0) { cbp += 1.0; cenv *= 0.001953125; }
            rg = psRotStep(rg, rgs);
            glow *= glowStep;
        }
    }
    // resolve the nearest trace's age, kick and depth into colour
    float ic = 1.0 / cW;
    float depth = saturate(0.5 - 0.6 * cZ * ic);
    vec3 tc = mix(mix(cTrail, cGlow, cT * ic), cKick, saturate(cK * ic * 0.35)) * (0.40 + 0.60 * depth) * (cM * ic);
    vec3 core = tc * (bw2 / (best + bw2) + 0.25 * (bw2 * 6.0) / (best + bw2 * 6.0));
    float ih = 1.0 / max(hAcc, 1e-6);
    float hDepth = saturate(0.5 - 0.6 * hZ * ih);
    vec3 halo = mix(cGlow, cKick, saturate(hK * ih * 0.35)) * (0.40 + 0.60 * hDepth);
    // overlapping strokes saturate the phosphor instead of summing to white
    float hSum = hAcc / hw;
    halo *= hSum / (1.0 + 0.12 * hSum);
    // phosphor emission is purer than the palette's display mix
    core = max(mix(vec3(luma(core)), core, 1.3), 0.0);
    halo = max(mix(vec3(luma(halo)), halo, 1.3), 0.0);
    float energy = 1.15 + 0.08 * u.level;

    // a faint aperture grille that only shows where the phosphor is lit
    vec2 mg = sp * vec2(260.0, 150.0);
    float mask = 0.86 + 0.14 * (0.5 + 0.5 * sin(mg.x)) * (0.6 + 0.4 * sin(mg.y + 1.57 * step(0.0, sin(mg.x * 0.5))));
    // the core of the beam runs whiter than its glow
    float coreHot = saturate(1.5 * bw2 / (best + bw2));
    core = mix(core, vec3(luma(core)) * vec3(1.9, 2.0, 2.1), 0.18 * coreHot);
    vec3 phos = (core * 0.85 * mask + halo * 0.16 + cGlow * wAcc * 0.03) * energy;

    // the write head: a white-hot spot with a thin anamorphic flare
    // (one on each figure while they dissolve)
    float hotHead = 0.6 + 0.06 * u.level;


    for (int fig = 0; fig < 2; fig++) {
        vec2 dh = sp - (fig == 0 ? headNew : headOld);
        float hw8 = fig == 0 ? xf : wOld;
        float hs = (bw2 * 4.0) / (dot(dh, dh) + bw2 * 4.0);
        phos += peakWhite(cFlash, 0.8) * hs * hs * hotHead * hw8;
        float hg = 0.0015 / (dot(dh, dh) + 0.0015);
        phos += cFlash * 0.5 * hg * hg * (0.4 + 0.3 * build) * hw8;
        phos += peakWhite(cFlash, 0.4) * exp(-abs(dh.y) * 260.0) * exp(-abs(dh.x) * 24.0) * 0.06 * hotHead * hw8;

    }
    float tubeShade = 1.0 - 0.6 * smoothstep(0.4, 1.15, length(nq));
    vec3 tube = (psPalSteadyFog(0.35, u) * 0.003 + phos) * tubeShade;

    // graticule: fine etched lines lit edge-on by the trace
    vec2 gq = gp / 0.2;
    vec2 gl = abs(fract(gq + 0.5) - 0.5) * 0.2;
    float grid = max(aaLine(gl.x, 0.0012), aaLine(gl.y, 0.0012));
    vec2 tk = abs(fract(gp / 0.04 + 0.5) - 0.5) * 0.04;
    float ticks = max(aaLine(tk.y, 0.0013) * step(abs(gp.x), 0.025),
                      aaLine(tk.x, 0.0013) * step(abs(gp.y), 0.025));
    float gBox = 1.0 - smoothstep(-0.02, 0.0, psBox(gp, vec2(PS_W - 0.12, PS_H - 0.1), 0.05));
    vec3 glowAvg = (cGlow * 0.6 + cFlash * 0.4) * energy;
    tube += (grid * 0.5 + ticks) * gBox * (glowAvg * 0.003 + halo * 0.25);

    // glass: fresnel softbox reflections on the bulged face
    vec3 gn = normalize(vec3(PS_BUL * nq.x / PS_W, PS_BUL * nq.y / PS_H, 1.0));
    vec3 rr = reflect(rd, gn);
    float cosv = saturate(-dot(rd, gn));
    float fres = 0.035 + 0.965 * pow(1.0 - cosv, 5.0);
    float smudge = 0.75 + 0.5 * vnoise(sp * 3.1 + 7.0);
    vec3 col = (tube + psEnv(rr) * fres * smudge) * inScreen;

    // ---- bezel: graphite with a polished chamfer round the glass ----
    float outer = psBox(sp, vec2(PS_W + PS_LIP, PS_H + PS_LIP), PS_R + PS_LIP);
    if (sd > -0.004 && outer < 0.0) {
        float lip = saturate(sd / 0.06);
        vec2 gdir = sp / max(length(sp), 1e-4);
        float bevel = 1.0 - smoothstep(0.0, 0.22, lip);
        vec3 bn = normalize(vec3(-gdir * bevel * 0.9, 1.0));
        vec3 br = reflect(rd, bn);
        float bf = 0.08 + 0.92 * pow(1.0 - saturate(-dot(rd, bn)), 5.0);
        float brush = 0.85 + 0.15 * vnoise(vec2(dot(sp, vec2(0.7, 0.7)) * 60.0, sd * 4.0));
        vec3 metal = vec3(0.010, 0.011, 0.012) * brush + psEnv(br) * bf * mix(0.30, 0.05, saturate(lip * 4.0)) * brush;
        // the chamfer and the lip pick up the trace's own glow
        metal += (halo * 0.08 + glowAvg * 0.02) * (bevel * 0.8 + 0.2) * exp(-sd * 14.0);
        col += metal * (1.0 - inScreen) * (1.0 - smoothstep(-0.01, 0.0, outer));
    }
    float outside = smoothstep(-0.004, 0.004, outer);
    col += outside * (vec3(0.004, 0.005, 0.006) + glowAvg * 0.004 * exp(-outer * 2.0));

    col = accentize(col, u.accent, 0.08);
    return col; // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
