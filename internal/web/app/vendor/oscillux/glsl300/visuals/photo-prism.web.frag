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
    vec3 p = sanitizeColor(PREV(s).rgb) * inside;
    float k = pow(clamp(decay, 0.0, 0.90), n);
    return sanitizeColor(max(sanitizeColor(col), p * k));
}
// SPDX-License-Identifier: AGPL-3.0-only
// Photo prism.
// A glass iris hangs in front of the photo like a macro product shot. Twelve
// thick wedge prisms ring a crystal ball lens, each at its own depth and each
// turning on its own radial axis, so waves of turning glass run round the
// ring. Through every prism the photo is shifted, magnified and split into a
// spectral rainbow, so the frame fractures into displaced copies that slide as
// the glass turns; between the prisms the photo sits dark and deep behind.
// The ball lens shows the photo inverted and magnified with chromatic edges,
// ringed by a glowing circular waveform. Softbox reflections sweep across the
// faces and the bevels catch light. The turning wave and the slow ring spin
// run at a steady, unhurried pace with no surges; a soft light chase walks
// round the ring, a caustic band slides slowly out along the glass and a faint
// shock ring leaves the lens on each beat. The level lifts the light, the
// waveform ring and the dispersion only a little, so no large area pulses.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Shard Spin ; param1 = Refraction ; param2 = Glow

// The palette read: the visual's own palette with no beat or spectral nudge.
// The vivid tour swaps whole palettes of very different brightness every few
// seconds, which swells the light across the frame, so this visual keeps its
// own colours there (as the calm visuals do); a host's manifest palette still
// replaces them.
vec3 ppPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 31, u); }

const float PP_TAU = 6.2831853;
const float PP_K = 12.0;     // prisms in the ring
const float PP_ZP = 1.6;     // photo plane depth behind the ring
const float PP_RB = 0.80;    // crystal ball radius
const float PP_R0 = 1.30;    // inner end of the prisms
const float PP_R1 = 8.0;     // outer end of the prisms
const float PP_TH = 0.045;    // prism half thickness

// The palette, read at four of its stops per pixel and interpolated round a
// colour wheel (the music turns the wheel).
// Each palette read copies the palette table on some targets, so every colour
// below comes from these four samples instead of its own read.
vec3 ppPal(float t, vec3 c0, vec3 c1, vec3 c2, vec3 c3) {
    float x = fract(t) * 4.0;
    float i = floor(x);
    float f = smoothstep(0.0, 1.0, x - i);
    vec3 a = i < 1.0 ? c0 : (i < 2.0 ? c1 : (i < 3.0 ? c2 : c3));
    vec3 b = i < 1.0 ? c1 : (i < 2.0 ? c2 : (i < 3.0 ? c3 : c0));
    return mix(a, b, f);
}
#define PPAL(t) ppPal(t, pc0, pc1, pc2, pc3)
// the rim role (a palette hue lifted toward its peak on hits)
#define PPRIM(t) mix(PPAL(t), peakWhite(PPAL((t) + 0.18), saturate(u.onset * 0.03 + u.level * 0.05)), 0.45)

// mirrored repeat, so refracted samples never hit a hard image edge
vec2 ppMirror(vec2 x) { return 1.0 - abs(1.0 - mod(x, 2.0)); }

// The photo's own key colour: nine fixed taps averaged. The taps do not
// depend on the pixel, so every pixel agrees and the cost is tiny.
vec3 ppKey(VisualUniforms u) {
    vec3 s = vec3(0.0);
    for (int i = 0; i < 9; i++) {
        float fi = float(i);
        vec2 g = vec2(mod(fi, 3.0), floor(fi / 3.0)) * 0.3 + 0.2;
        s += max(IMG(photoCover(g, u.resolution, IMGSIZE)).rgb, vec3(0.0));
    }
    return s / 9.0;
}

// An accent complementary to the photo: the palette stop whose hue sits
// furthest from the photo's key, pushed toward the key's true complement
// (the more colourful the photo, the harder the push).
vec3 ppAccent(vec3 key, vec3 c0, vec3 c1, vec3 c2, vec3 c3) {
    vec3 kc = key - vec3(luma(key));
    float ks = length(kc);
    vec3 kn = kc / max(ks, 1e-4);
    vec3 best = c0;
    float bd = dot(c0 - vec3(luma(c0)), kn) / max(length(c0 - vec3(luma(c0))), 1e-4);
    float d1 = dot(c1 - vec3(luma(c1)), kn) / max(length(c1 - vec3(luma(c1))), 1e-4);
    float d2 = dot(c2 - vec3(luma(c2)), kn) / max(length(c2 - vec3(luma(c2))), 1e-4);
    float d3 = dot(c3 - vec3(luma(c3)), kn) / max(length(c3 - vec3(luma(c3))), 1e-4);
    if (d1 < bd) { best = c1; bd = d1; }
    if (d2 < bd) { best = c2; bd = d2; }
    if (d3 < bd) { best = c3; bd = d3; }
    vec3 comp = max(vec3(0.33) - kn * 0.75, vec3(0.0));
    vec3 a = mix(best, comp * 1.6, saturate(ks * 6.0) * 0.6);
    return a / max(max(a.r, max(a.g, a.b)), 0.05);
}

// The photo plane: q in world units, the frame spans about +-2.9 vertically.
// Graded for deep blacks: a contrast curve and a touch more saturation.
vec3 ppPhoto(vec2 q, float aspect, VisualUniforms u) {
    vec2 s = vec2(0.5 + 0.5 * q.x / (2.9 * aspect), 0.5 - 0.5 * q.y / 2.9);
    vec3 c = max(IMG(photoCover(ppMirror(s), u.resolution, IMGSIZE)).rgb, vec3(0.0));
    c = c * c * 1.25;
    return max(mix(vec3(luma(c)), c, 1.3), vec3(0.0));
}

// spectral weight of wavelength position w (0 red .. 1 violet)
vec3 ppSpectrum(float w) {
    return max(vec3(1.0 - abs(w - 0.10) * 2.4, 1.0 - abs(w - 0.5) * 2.6, 1.0 - abs(w - 0.90) * 2.4), vec3(0.0));
}

// Studio environment seen in reflections: a key softbox, a thin strip light
// and a coloured floor bounce, all crisp so they read as specular streaks.
vec3 ppEnv(vec3 r, float turn, vec3 top, vec3 floorA, vec3 floorB) {
    vec2 q = rot2(turn) * (r.xy / max(-r.z, 0.25));   // light cards, slowly orbiting
    float s1 = (q.x + 0.62) * 9.0;
    float s2 = (q.x - 0.70) * 12.0;
    float s3 = (q.y - 0.68) * 10.0;
    float tall = smoothstep(1.6, 0.6, abs(q.y));
    float wide = smoothstep(1.8, 0.8, abs(q.x));
    vec3 c = vec3(1.0, 0.95, 0.88) * exp(-s1 * s1) * tall * 3.2
           + vec3(0.82, 0.90, 1.0) * exp(-s2 * s2) * tall * 2.6
           + vec3(1.0, 0.97, 0.93) * exp(-s3 * s3 * 2.0) * wide * 1.4;
    c *= vec3(1.0, 1.0, 1.0) * 0.8;
    c += top * 0.05 * smoothstep(-0.2, 1.2, q.y);
    c += mix(floorA, floorB, saturate(0.5 + 0.5 * q.x)) * smoothstep(-0.2, -1.0, q.y) * 0.55;
    return c;
}

// Ray against a thick wedge prism, in the prism's local frame (x radial,
// y across, z through). Returns the entry distance or -1, and the local
// entry normal.
float ppWedge(vec3 o, vec3 d, float a, float r0, out vec3 nl) {
    float tn = -1e9, tf = 1e9;
    nl = vec3(0.0, 0.0, -1.0);
    float na = inversesqrt(1.0 + a * a);
    for (int i = 0; i < 6; i++) {
        vec3 N; float c;
        if (i == 0) { N = vec3(0.0, 0.0, 1.0); c = -PP_TH; }
        else if (i == 1) { N = vec3(0.0, 0.0, -1.0); c = -PP_TH; }
        else if (i == 2) { N = vec3(-a, 1.0, 0.0) * na; c = 0.0; }
        else if (i == 3) { N = vec3(-a, -1.0, 0.0) * na; c = 0.0; }
        else if (i == 4) { N = vec3(-1.0, 0.0, 0.0); c = r0; }
        else { N = vec3(1.0, 0.0, 0.0); c = -PP_R1; }
        float f = dot(N, o) + c;
        float g = dot(N, d);
        if (abs(g) < 1e-6) {
            if (f > 0.0) return -1.0;
            continue;
        }
        float t = -f / g;
        if (g < 0.0) {
            if (t > tn) { tn = t; nl = N; }
        } else {
            tf = min(tf, t);
        }
    }
    if (tn > tf || tf <= 0.0 || tn <= 0.0) return -1.0;
    return tn;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    float detail = osDetail(u);
    float aspect = u.resolution.x / max(u.resolution.y, 1.0);
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;                                  // y up
    float t = mod(u.time, 600.0);

    float spinAmt = mix(0.35, 1.5, u.param0);
    float refr = mix(0.4, 1.5, u.param1);
    float glow = mix(0.5, 1.6, u.param2);

    // turning wave and ring spin follow the speed profile; wrapped so every
    // use below is periodic over the wrap
    vec2 fl = osFlight(u, 0.15 * spinAmt, -0.06 * spinAmt);
    float dw = mod(fl.x, PP_TAU * 20.0);
    float rush = saturate((fl.y - 1.0) / 3.5);
    float spin = dw * 0.07;
    // the iris opens on builds and breathes with the bass
    float irisR = PP_R0 + 0.02 * rush + 0.03 * u.level;
    // the palette walks round the wheel with the music and jumps ahead on builds
    float bc = u.beatCount + saturate(u.beatPhase);
    float hueOff = mod(bc, 256.0) * (0.137 / 16.0) + 0.02 * rush;
    // a light chase runs round the ring, a prism and a half per beat
    float cpos = mod(bc * 0.25, PP_K);
    float kick = 0.0;
    vec3 pc0 = ppPalSteady(0.02, u);
    vec3 pc1 = ppPalSteady(0.34, u);
    vec3 pc2 = ppPalSteady(0.62, u);
    vec3 pc3 = ppPalSteady(0.97, u);
    // the photo's key and an accent complementary to it
    vec3 key = ppKey(u);
    vec3 accent = ppAccent(key, pc0, pc1, pc2, pc3);
    vec3 envTop = PPAL(hueOff + 0.6);
    vec3 envA = accent;
    vec3 envB = PPRIM(0.50 + hueOff);
    float lightTurn = -dw * 0.1;

    // camera: slow drift for parallax between glass and photo, never audio
    float w1 = PP_TAU * 9.0 / 600.0;
    float w2 = PP_TAU * 7.0 / 600.0;
    float w3 = PP_TAU * 5.0 / 600.0;
    vec3 ro = vec3(0.75 * sin(t * w1), 0.45 * sin(t * w2 + 1.3), -3.3 + 0.35 * sin(t * w3 + 0.4));
    vec3 rd = osCamera(ro, vec3(0.0), 0.0, 1.65, p);

    // the ring plane z = 0 picks the candidate prisms
    float tz = -ro.z / max(rd.z, 1e-3);
    vec3 h0 = ro + rd * tz;
    float r0 = length(h0.xy);
    float ang = atan2_(h0.y, h0.x);
    float sector = floor((ang - spin) / PP_TAU * PP_K);
    float wa = tan(3.14159265 / PP_K) * 0.97;

    float bestT = 1e9;
    vec3 bN = vec3(0.0), bW = vec3(0.0), bH = vec3(0.0), bNL = vec3(0.0);
    float bS = 0.0, bR = 0.0, bTh = 0.0, bId = 0.0;
    for (int j = -1; j <= 1; j++) {
        float idx = mod(sector + float(j), PP_K);
        float phi0 = (idx + 0.5) * PP_TAU / PP_K;
        float phi = phi0 + spin;
        float hb = hash11(idx * 7.13 + 1.7);
        // a two-lobed wave runs round the ring; each prism turns on its radial axis
        float th = 0.5 * sin(dw - phi0 * 2.0 + hb * 0.6)
                 + 0.22 * sin(dw * 0.35 + idx * 2.1);
        vec3 er = vec3(cos(phi), sin(phi), 0.0);
        vec3 et = vec3(-sin(phi), cos(phi), 0.0);
        vec3 wv = cos(th) * et + vec3(0.0, 0.0, sin(th));
        vec3 n = cos(th) * vec3(0.0, 0.0, 1.0) - sin(th) * et;
        vec3 c = vec3(0.0, 0.0, 0.9 * (hb - 0.5));
        vec3 o = ro - c;
        vec3 ol = vec3(dot(o, er), dot(o, wv), dot(o, n));
        vec3 dl = vec3(dot(rd, er), dot(rd, wv), dot(rd, n));
        vec3 nl;
        float tt = ppWedge(ol, dl, wa, irisR, nl);
        if (tt > 0.0 && tt < bestT) {
            vec3 hl = ol + dl * tt;
            bestT = tt;
            bNL = nl;
            bN = nl.x * er + nl.y * wv + nl.z * n;
            bW = wv; bH = ro + rd * tt; bS = hl.y / max(hl.x * wa, 1e-3);
            bR = hl.x; bTh = th; bId = idx;
        }
    }

    int ns = osSteps(3.0, 7.0, detail);
    float fns = float(ns);
    vec3 col;
    float tp = (PP_ZP - ro.z) / max(rd.z, 1e-3);
    vec2 qDirect = (ro + rd * tp).xy;

    // crystal ball
    float bb = dot(ro, rd);
    float bh = bb * bb - (dot(ro, ro) - PP_RB * PP_RB);
    float tBall = bh > 0.0 ? -bb - sqrt(bh) : -1.0;

    if (bestT < 1e8 && (tBall < 0.0 || bestT < tBall)) {
        // ---- through a prism: shifted, magnified, dispersed ----
        float hb = hash11(bId * 3.71 + 0.3);
        float sgn = mod(bId, 2.0) < 0.5 ? 1.0 : -1.0;
        float bnd = bandAt(u, bId / (PP_K - 1.0));
        bool side = abs(bNL.z) < 0.5;            // entered through a bevel face
        vec2 off = bW.xy * refr * (sgn * (0.35 + 0.30 * hb) + 0.30 * bS)
                 + normalize(bH.xy + vec2(1e-4)) * refr * 0.20 * sin(bTh);
        float disp = refr * (0.14 + 0.02 * u.level) * (side ? 4.0 : 1.0);
        float tq = (PP_ZP - bH.z) / max(rd.z, 1e-3);
        vec2 qb = (bH + rd * tq).xy;
        vec3 acc = vec3(0.0), ws = vec3(0.0);
        for (int i = 0; i < 7; i++) {
            if (i >= ns) break;
            float wl = (float(i) + 0.5) / fns;
            vec3 sw = ppSpectrum(wl);
            vec2 q = qb + off * (1.0 + disp * (wl - 0.5) * 2.0);
            acc += ppPhoto(q, aspect, u) * sw;
            ws += sw;
        }
        vec3 trans = acc / max(ws, vec3(1e-4));
        // each prism grades the photo into its own duotone: shadows in one
        // palette hue, highlights in the opposite one, so the ring reads as a
        // colour wheel of glass and every copy of the photo has its own light
        float hk = bId / PP_K + hueOff;
        float L = luma(trans);
        // a light split-tone keeps the photo itself readable: shadows sink
        // to black, highlights take the prism's palette hue
        vec3 duo = L * (0.2 + 1.7 * L) * mix(PPAL(hk), PPAL(hk + 0.17) * 1.15, smoothstep(0.04, 0.5, L));
        float dc = abs(mod(bId - cpos + 0.5 * PP_K, PP_K) - 0.5 * PP_K);
        float chase = exp(-dc * dc * 0.9);
        // the chased prism clears to the true photo
        trans = mix(trans, duo, 0.45 * (1.0 - 0.3 * chase));
        trans = max(mix(vec3(luma(trans)), trans, 1.45), vec3(0.0)) * smoothstep(0.0, 0.16, L);
        trans /= 1.0 + 0.45 * luma(trans);     // soft shoulder keeps highlights coloured
        trans *= 0.70 + chase * (0.05 + 0.03 * u.level);

        vec3 n = bN * (dot(rd, bN) > 0.0 ? -1.0 : 1.0);
        float ci = saturate(-dot(rd, n));
        float fres = 0.04 + 0.96 * pow(1.0 - ci, 5.0);
        vec3 refl = ppEnv(reflect(rd, n), lightTurn, envTop, envA, envB);
        col = trans * (1.0 - fres) * (0.70 + 0.30 * abs(cos(bTh))) + refl * fres * 0.55;

        vec3 rim = mix(PPAL(bId / PP_K + hueOff + 0.5 + 0.2 * bS), accent * 1.4, 0.45 + 0.45 * chase);
        float lit = 0.3 + 0.15 * u.level + 0.1 * bnd + 0.1 * chase;
        if (side) {
            // the slab's thickness: a lit, rainbow-split glass edge
            col = col * 0.35 + rim * glow * min(lit, 1.4);
        } else {
            // light from the iris piped down the glass: an inner glow that
            // fades along the prism and a bright caustic band that slides
            // outward with every beat
            float rr = bR - irisR;
            float band = exp(-pow((rr - 6.5 * fract(t * 0.05)) * 1.6, 2.0)) * 0.08;
            vec3 pipe = PPAL(hk + 0.5);
            col += pipe * pipe * glow * (exp(-rr * 1.1) * (0.12 + 0.08 * u.level + 0.04 * bnd + 0.05 * chase)
                                       + band * (0.2 + 0.2 * chase) * (1.0 - 0.5 * abs(bS)));
            // a fine lit line where the face meets the bevel
            float e = min((1.0 - abs(bS)) * bR * wa, (bR - irisR));
            float line = 1.0 - smoothstep(0.0, 0.010 + 0.004 * bR, e);
            col += mix(rim, vec3(1.0), 0.3) * line * glow * (0.3 + lit);
            // spectral fringe just inside the edge
            float fr = exp(-e * 34.0) * (1.0 - line);
            col += ppSpectrum(fract(e * 6.0 + bId * 0.13)) * fr * glow * (0.2 + 0.15 * u.level + 0.1 * bnd) * refr;
        }
    } else if (tBall > 0.0) {
        // ---- the crystal ball: the photo inverted and magnified ----
        vec3 p1 = ro + rd * tBall;
        vec3 n1 = p1 / PP_RB;
        int nl = osSteps(3.0, 5.0, detail);
        vec3 acc = vec3(0.0), ws = vec3(0.0);
        for (int i = 0; i < 5; i++) {
            if (i >= nl) break;
            float wl = (float(i) + 0.5) / float(nl);
            vec3 sw = ppSpectrum(wl);
            float eta = 1.52 + (0.5 - wl) * 0.05 * refr;
            vec3 d1 = refract(rd, n1, 1.0 / eta);
            float t2 = -2.0 * dot(p1, d1);
            vec3 p2 = p1 + d1 * max(t2, 0.0);
            vec3 d2 = refract(d1, -p2 / PP_RB, eta);
            vec3 c = vec3(0.0);
            if (d2.z > 0.05) {
                // the ball looks at a deeper virtual plane, so it shows a
                // wide inverted view of the photo rather than a blur
                vec3 p3 = p2 + d2 * ((5.0 - p2.z) / d2.z);
                c = ppPhoto(p3.xy * 0.62, aspect, u);
            }
            acc += c * sw;
            ws += sw;
        }
        vec3 ball = acc / max(ws, vec3(1e-4));
        ball = ball * 0.85 / (1.0 + 0.6 * ball);
        float Lb = luma(ball);
        ball = mix(ball, Lb * (0.2 + 1.6 * Lb) * mix(PPAL(hueOff + 0.62), PPAL(hueOff + 0.80) * 1.8, smoothstep(0.05, 0.5, Lb)), 0.3);
        ball = max(mix(vec3(luma(ball)), ball, 1.25), vec3(0.0)) * smoothstep(0.0, 0.14, Lb);
        float ci = saturate(-dot(rd, n1));
        float fres = 0.04 + 0.96 * pow(1.0 - ci, 5.0);
        ball = ball * (1.0 - fres) + ppEnv(reflect(rd, n1), lightTurn, envTop, envA, envB) * fres * 1.4;
        ball += accent * pow(1.0 - ci, 3.0) * glow * (0.4 + 0.2 * u.level);
        col = ball;
    } else {
        // ---- the photo deep behind, seen between the prisms ----
        col = luma(ppPhoto(qDirect, aspect, u)) * mix(PPAL(hueOff + 0.62), accent, 0.5) * 0.03;
        // light split by the prisms fans across the dark gaps as rainbow rays
        float g = fract((ang - spin) / PP_TAU * PP_K + 0.5) - 0.5;
        float gi = mod(floor((ang - spin) / PP_TAU * PP_K + 0.5), PP_K);
        float gb = bandAt(u, gi / (PP_K - 1.0));
        vec2 dirn = h0.xy / max(r0, 1e-3);
        float rays = 0.35 + 0.65 * vnoise(dirn * 13.0 + 3.0 * vec2(sin(dw * 0.05), cos(dw * 0.05)));
        float fan = smoothstep(irisR, irisR + 0.6, r0) * exp(-max(r0 - irisR, 0.0) * 0.35);
        vec3 spec = mix(hsv2rgb(vec3(0.78 * saturate(g * 5.0 + 0.5), 0.80, 1.0)), accent, 0.45);
        col += spec * rays * rays * fan * exp(-g * g * 70.0) * glow * refr
             * (0.12 + 0.08 * u.level + 0.04 * gb);
    }

    // ---- the circular waveform ring round the ball ----
    float tw = abs(fract((ang - spin * 0.5) / PP_TAU + 0.25) * 2.0 - 1.0);
    float b = (bandAt(u, tw) * 2.0 + bandAt(u, tw + 0.03) + bandAt(u, tw - 0.03)) * 0.25;
    float ringR = PP_RB + 0.10 + 0.22 * b;
    float occl = (bestT < 1e8 || tBall > 0.0) ? 0.0 : 1.0;
    float rd0 = r0 - ringR;
    vec3 ringCol = peakWhite(mix(PPAL(tw * 0.8 + 0.1 + hueOff), accent, 0.6) * 1.6, 0.05 * u.onset);
    col += ringCol * occl * glow * (aaLine(rd0, 0.005 + 0.004 * b) * (1.0 + 0.8 * b)
                                  + exp(-abs(rd0) * 24.0) * (0.10 + 0.15 * b));

    // ---- on each beat a dispersed shock ring leaves the lens and runs out
    // across the glass: red, accent and violet edges slightly apart ----
    float bp = saturate(u.beatPhase);
    float rs = PP_RB + 0.2 + 6.5 * bp;
    float sw = smoothstep(0.0, 0.2, bp) * (1.0 - bp) * (1.0 - bp) * glow * 0.07;
    float sx = (r0 - rs) * (16.0 - 9.0 * bp);
    vec3 shock = vec3(1.0, 0.2, 0.05) * 0.45 * exp(-(sx - 0.9) * (sx - 0.9) * 4.0)
               + accent * 1.5 * exp(-sx * sx * 3.0)
               + vec3(0.4, 0.1, 1.0) * 0.45 * exp(-(sx + 0.9) * (sx + 0.9) * 4.0);
    col += shock * sw * (tBall > 0.0 && (bestT > 1e8 || tBall < bestT) ? 0.0 : 0.9);

    col *= 0.30 + 0.70 * smoothstep(2.3, 0.5, length(p));
    return col; // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(sanitizeColor(visual(vUV, u)), 1.0);
}
