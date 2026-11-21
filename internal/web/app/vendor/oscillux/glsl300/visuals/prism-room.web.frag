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
// Prism room.
// A slow drift above a triangular mirror room. Its three walls, floor and
// ceiling are mirrors, so the room repeats as a kaleidoscope lattice that
// recedes into the dark in every direction. A tall glass prism stands in the
// middle, a ring of small ones around it; each bends the room behind it into
// red, green and blue fringes, and its edges burn as thin filaments. A white
// beam enters through a slot, strikes the central prism and leaves as a
// fanned spectrum sheet that lands on the walls and floor as rainbows, which
// the mirrors multiply into a star. The prism sways a little with the beat,
// so the spectrum drifts slowly round the room; the smoothed level warms the
// beam and the fan, and every beat sends a faint rainbow caustic ring across
// the floor. Spectrum bars rise along the foot of the walls, sparse LED points
// stud the ceiling and a few lamps hang in the air, all breathing with the
// level and a trace of their own band, so the mirrored lattice glows rather
// than flashes. The camera drifts slowly and level, never moved by the audio.
// Reds are kept just off saturated red.

// param0 = Facets (prism count) ; param1 = Sweep (drift pace, hue chase) ;
// param2 = Edge Glow

const float PR_RI = 3.6;   // inradius of the triangular room
const float PR_H = 4.4;    // room height
const float PR_BEAMY = 1.05;
const float PR_HS = 6.235; // half a wall's length: PR_RI * sqrt(3)

// The fitted filmic curve the post chain applies per channel, and its inverse.
float prTone(float x) { return x * (2.51 * x + 0.03) / (x * (2.43 * x + 0.59) + 0.14); }
float prToneInv(float y) {
    y = clamp(y, 0.0, 0.999);
    float a = 2.51 - 2.43 * y, b = 0.03 - 0.59 * y;
    return (-b + sqrt(b * b + 0.56 * a * y)) / (2.0 * a);
}

// Keeps every red just off saturated red once tonemapped: green and blue
// rise together until they make 0.27 of red, a slight desaturation that keeps
// the hue, so no red area can strobe.
vec3 prOffRed(vec3 c) {
    float r = prTone(c.r), g = prTone(c.g), b = prTone(c.b);
    float need = 0.27 * r - g - b;
    if (need <= 0.0) return c;
    c.g = prToneInv(g + 0.5 * need);
    c.b = prToneInv(b + 0.5 * need);
    return c;
}


// Smoothly saturated version of a palette colour: kills the pastel, keeps hue.

vec3 prDeep(vec3 c) {
    c = clamp(mix(vec3(luma(c)), c, 1.5), vec3(0.0), vec3(4.0));
    return c * c * 1.5;
}

// A visible-spectrum ramp for dispersed light, t in [0, 1] red to violet.
vec3 prSpectrum(float t) {
    vec3 c = vec3(1.0 - smoothstep(0.0, 0.45, abs(t - 0.12) * 1.6),
                  1.0 - smoothstep(0.0, 0.42, abs(t - 0.45) * 1.7),
                  1.0 - smoothstep(0.0, 0.45, abs(t - 0.8) * 1.6));
    c.r += 0.35 * (1.0 - smoothstep(0.0, 0.2, abs(t - 0.95)));
    return c;
}

struct PrScene {
    float count;     // satellite prisms
    float tw;
    vec3 bs;         // beam start (the slot)
    vec3 bd;         // beam direction
    float bl;        // beam length to the central prism
    vec2 fanDir;
    float fanE;
    float ringR;     // floor caustic ring radius and strength
    float ringA;
    float edgeGlow;
    float chase;     // hue chase along the LED strips
    float pix;
    vec3 cornerC;    // per-frame colours, hoisted out of the bounce loop
    vec3 tint0;
    vec3 tint1;
    vec3 tint2;
};

// Outward normal of wall k (in xz).
vec2 prWallN(int k) {
    float a = 1.5707963 + float(k) * 2.0943951;
    return vec2(cos(a), sin(a));
}

// Fan of dispersed light leaving the central prism: unit direction from the
// prism centre within the fan, mapped to spectrum position, or -1 outside.
float prFanT(vec2 d, vec2 dir0) {
    float c = dot(d, dir0);
    float sn = d.x * dir0.y - d.y * dir0.x;
    float a = atan2_(sn, c);
    float t = (a + 0.3) / 0.6;
    return (c > 0.0 && t > 0.0 && t < 1.0) ? t : -1.0;
}

// Triangular prism at c (base on the floor), face normals at ang + k*120deg,
// inradius rin, height h. Returns entry/exit distances and their normals.
bool prPrism(vec3 ro, vec3 rd, vec3 c, float ang, float rin, float h,
             out float tN, out float tF, out vec3 nN, out vec3 nF) {
    vec3 o = ro - c;
    tN = -1e4; tF = 1e4;
    nN = vec3(0.0, 1.0, 0.0); nF = vec3(0.0, 1.0, 0.0);
    // cheap reject: the ray's ground track misses the prism's circumcircle
    vec2 rxz = rd.xz;
    float r2 = dot(rxz, rxz);
    if (r2 > 1e-8) {
        float tc = -dot(o.xz, rxz) / r2;
        vec2 q = o.xz + rxz * max(tc, 0.0);
        if (dot(q, q) > 4.0 * rin * rin) return false;
    }
    vec2 fn = vec2(cos(ang), sin(ang));
    for (int k = 0; k < 5; k++) {
        vec3 n;
        float off;
        if (k < 3) {
            n = vec3(fn.x, 0.0, fn.y);
            fn = vec2(-0.5 * fn.x - 0.8660254 * fn.y, 0.8660254 * fn.x - 0.5 * fn.y); // +120deg
            off = rin;
        } else if (k == 3) {
            n = vec3(0.0, 1.0, 0.0); off = h;
        } else {
            n = vec3(0.0, -1.0, 0.0); off = 0.0;
        }
        float dn = dot(rd, n);
        float on = dot(o, n);
        if (abs(dn) < 1e-6) {
            if (on > off) return false;
            continue;
        }
        float t = (off - on) / dn;
        if (dn < 0.0) { if (t > tN) { tN = t; nN = n; } }
        else { if (t < tF) { tF = t; nF = n; } }
    }
    return tN < tF && tF > 0.0;
}

void prPrismParams(int i, PrScene sc, out vec3 c, out float ang, out float rin, out float h) {
    if (i == 0) {
        c = vec3(0.0); ang = 0.4 + sc.tw * 0.02; rin = 0.33; h = 1.75;
        return;
    }
    float fi = float(i - 1);
    float a = sc.tw * 0.012 + fi * 6.2831853 / sc.count;
    float hr = hash11(fi * 3.7 + 1.3);
    float rr = 1.45 + 0.35 * hash11(fi * 1.9 + 0.4);
    c = vec3(cos(a) * rr, 0.0, sin(a) * rr);
    ang = a * 1.7 + hr * 6.28 + sc.tw * (0.03 + 0.02 * hr);
    rin = 0.12 + 0.05 * hr;
    h = 0.35 + 0.55 * hr;
}

// Distance from o (a point on the prism, relative to its base centre) to its
// vertical edges (x) and to the rims of its top and base triangles (y).
vec2 prEdgeDist(vec3 o, float ang, float rin, float h) {
    float dv = 1e4;
    float tb = 1e4;
    vec2 fn = vec2(cos(ang), sin(ang));
    for (int k = 0; k < 3; k++) {
        // the vertex between faces k and k+1 lies at +60deg from face k
        vec2 vn = vec2(0.5 * fn.x - 0.8660254 * fn.y, 0.8660254 * fn.x + 0.5 * fn.y);
        dv = min(dv, length(o.xz - 2.0 * rin * vn));
        tb = min(tb, rin - dot(o.xz, fn));
        fn = vec2(-0.5 * fn.x - 0.8660254 * fn.y, 0.8660254 * fn.x - 0.5 * fn.y);
    }
    tb = max(tb, 0.0);
    float dr = min(length(vec2(o.y - h, tb)), length(vec2(o.y, tb)));
    return vec2(dv, dr);
}

// Hanging lamp i: position (xyz) and its band (w).
vec4 prLamp(int i, float tw) {
    float fi = float(i);
    float a = fi * 2.3999632 + 0.7;
    float r = 0.7 + 1.7 * fract(fi * 0.618 + 0.21);
    float y = 2.25 + 0.8 * hash11(fi * 5.1 + 2.0) + 0.08 * sin(tw * 0.3 + fi);
    return vec4(cos(a) * r, y, sin(a) * r, fract(fi * 0.381966 + 0.05));
}

// Light in the haze along one straight path segment: the white beam, the
// slot it enters by, the spectrum fan sheet and the hanging lamps.
vec3 prVolume(vec3 ro, vec3 rd, float seg, float dist, PrScene sc, int lamps, float bk, VisualUniforms u) {
    vec3 col = vec3(0.0);
    // the white beam: closest approach between the segment and the beam
    vec3 w0 = ro - sc.bs;
    float b = dot(rd, sc.bd);
    float dd = 1.0 - b * b;
    if (dd > 1e-5) {
        float tc = (b * dot(sc.bd, w0) - dot(rd, w0)) / dd;
        tc = clamp(tc, 0.0, seg);
        float sb = clamp(dot(ro + rd * tc - sc.bs, sc.bd), 0.0, sc.bl - 0.45);
        tc = clamp(dot(sc.bs + sc.bd * sb - ro, rd), 0.0, seg);
        float dist2 = length(ro + rd * tc - sc.bs - sc.bd * sb);
        float bw = max(0.008, (dist + tc) * sc.pix * 1.3);
        col += vec3(2.0, 1.9, 1.75) * exp(-dist2 * dist2 / (bw * bw)) * (0.014 / max(bw, 0.014)) * (0.9 + 0.1 * u.level) * bk *
               smoothstep(0.4, 1.4, dist + tc);
        col += vec3(0.6, 0.58, 0.55) * exp(-dist2 * 6.0) * 0.05 * (1.0 + 0.2 * u.level);
    }
    // the slot where the beam enters, a hot point on the wall
    vec3 ap = sc.bs - ro;
    float tap = clamp(dot(ap, rd), 0.0, seg);
    float da = length(ap - rd * tap);
    col += vec3(1.5, 1.42, 1.3) * (exp(-da * da * 60.0) * 0.7 + exp(-da * 2.5) * 0.03) * bk;
    // fan: a thin sheet at beam height, crossed by the segment
    if (abs(rd.y) > 1e-4) {
        float tp = (PR_BEAMY - ro.y) / rd.y;
        if (tp > 0.0 && tp < seg) {
            vec3 fp = ro + rd * tp;
            float r = length(fp.xz);
            float ft = prFanT(fp.xz / max(r, 1e-4), sc.fanDir);
            if (ft >= 0.0) {
                float sheet = min((0.04 + 0.03 * r) / abs(rd.y), 2.0) * 0.55;
                col += prSpectrum(ft) * sheet * sc.fanE * smoothstep(0.5, 1.0, r) * exp(-r * 0.1);
            }
        }
    }
    // hanging lamps
    for (int i = 0; i < 7; i++) {
        if (i >= lamps) break;
        vec4 lp = prLamp(i, sc.tw);
        vec3 to = lp.xyz - ro;
        float tt = clamp(dot(to, rd), 0.0, seg);
        vec3 ov = to - rd * tt;
        float o2 = dot(ov, ov);
        float e = 0.45 * saturate(u.level) + 0.15 * bandAt(u, 0.05 + 0.85 * lp.w);
        float w = max(0.05, (dist + tt) * sc.pix * 1.4);
        float k = 0.05 / w;
        vec3 lc = prSpectrum(fract(lp.w + sc.chase * 0.3)) * 1.2;
        col += (peakWhite(lc, 0.15) * k * k * exp(-o2 / (w * w)) * 1.4 + lc * exp(-sqrt(o2) * 9.0) * 0.05) *
               (0.4 + 0.6 * e);
    }
    return col;
}

// Emission on wall k at p, seen with footprint w: corner filaments, a hue
// wash along the foot, spectrum bars and the fan band.
vec3 prWallLight(vec3 p, int k, float w, PrScene sc, VisualUniforms u) {
    vec2 n = prWallN(k);
    float s = dot(p.xz, vec2(-n.y, n.x));
    vec3 col = vec3(0.0);
    // vertical corner filaments
    float dv = PR_HS - abs(s);
    col += sc.cornerC * (exp(-dv * dv / (w * w)) * 0.45 + exp(-dv * 6.0) * 0.03) * sc.edgeGlow *
           (0.2 + 0.8 * smoothstep(3.4, 1.0, p.y));
    // a soft wash of chasing hue along the foot of the wall
    float hs = s * 0.08 + float(k) * 0.33 + sc.chase;
    col += prDeep(palVisual(fract(hs), u)) * exp(-p.y * 4.0) * 0.035;
    // spectrum bars rising from the floor
    float bw = 0.95;
    float bi = floor(s / bw);
    float ls = s - (bi + 0.5) * bw;
    float f = abs(bi + 0.5) * bw / PR_HS;
    float e = 0.45 * saturate(u.level) + 0.15 * bandAt(u, saturate(0.03 + 0.9 * f));
    float hgt = 0.3 + 1.2 * e;
    if (p.y < hgt + 0.3) {
        float cw = max(0.022, w);
        float core = (1.0 - smoothstep(cw, cw + max(w, 0.02), abs(ls))) * (0.03 / cw);
        float seg = 0.4 + 0.6 * smoothstep(0.02, 0.045, fract(p.y / 0.13) * 0.13);
        float lit = (1.0 - smoothstep(hgt - 0.04, hgt, p.y)) * smoothstep(0.08, 0.14, p.y);
        vec3 bc = prDeep(palVisual(fract(0.06 + 0.1 * p.y + 0.4 * f + 0.27 * float(k)), u));
        col += bc * core * lit * seg * 2.2;
        float cap = exp(-(p.y - hgt - 0.07) * (p.y - hgt - 0.07) / 0.0012) * core;
        col += peakWhite(bc, 0.5) * cap * 1.4;
        col += bc * exp(-abs(ls) * 12.0) * lit * 0.05;
    }
    // the dispersed fan lands here as a rainbow band
    float ft = prFanT(normalize(p.xz + 1e-5), sc.fanDir);
    if (ft >= 0.0) {
        // a tall rainbow patch with a hot centre line, so the mirrors
        // multiply it into a kaleidoscope of spectra
        float dy = (p.y - PR_BEAMY) / (0.2 + 0.04 * length(p.xz));
        float edgeT = smoothstep(0.0, 0.08, ft) * smoothstep(1.0, 0.92, ft);
        col += prSpectrum(ft) * (exp(-dy * dy) * 0.9 + exp(-dy * dy * 30.0) * 1.2) * edgeT * sc.fanE;
    }
    return col;
}

// Floor emission: the beat caustic ring, a caustic pool and the fan's trace.
vec3 prFloorLight(vec3 p, float w, PrScene sc, VisualUniforms u) {
    vec3 col = vec3(0.0);
    float r = length(p.xz);
    float a = atan2_(p.z, p.x + 1e-5);
    // the caustic ring: rainbow by angle, a soft net of light inside it
    float rq = (r - sc.ringR) / max(0.08, w);
    vec3 rc = prSpectrum(fract(a * 0.477 + sc.chase * 2.0));
    col += rc * exp(-rq * rq) * sc.ringA * 1.4;
    float net = sin(p.x * 9.0 + sc.tw * 0.7) * sin(p.z * 9.0 - sc.tw * 0.9) +
                sin((p.x + p.z) * 6.0 - sc.tw * 0.5);
    net = pow(saturate(0.5 + 0.35 * net), 6.0);
    col += rc * net * sc.ringA * 0.25 * smoothstep(sc.ringR, sc.ringR - 1.2, r) * smoothstep(0.4, 0.8, r);
    // a steady caustic pool around the central prism's foot
    col += prSpectrum(fract(a * 0.159 + sc.tw * 0.02)) * net * 0.08 * exp(-r * 1.6) * smoothstep(0.45, 0.6, r);
    // the fan's trace across the floor
    float ft = prFanT(p.xz / max(r, 1e-4), sc.fanDir);
    // the fan's trace across the floor: a rainbow wedge that the mirrors
    // repeat into a star
    if (ft >= 0.0) {
        float eT = smoothstep(0.0, 0.1, ft) * smoothstep(1.0, 0.9, ft);
        col += prSpectrum(ft) * sc.fanE * eT * 0.16 * smoothstep(0.4, 1.2, r) * exp(-r * 0.25);
    }
    return col;
}

// Ceiling emission: a lattice of LED points, each on a band.
vec3 prCeilLight(vec3 p, float w, PrScene sc, VisualUniforms u) {
    vec2 g = p.xz / 0.55;
    vec2 ci = floor(g);
    vec2 cf = fract(g) - 0.5;
    float hsh = hash21(ci + 7.0);
    float d = length(cf) * 0.55;
    float ww = max(0.015, w);
    float k = 0.015 / ww;
    float e = 0.45 * saturate(u.level) + 0.15 * bandAt(u, hsh);
    vec3 c = prDeep(palVisual(fract(hsh * 0.7 + sc.chase), u));
    return peakWhite(c, 0.3) * k * k * exp(-d * d / (ww * ww)) * (0.3 + 0.5 * e) * step(0.8, hsh);
}

// Trace through the mirror room: up to maxB straight segments. Prisms
// refract the path with one index; walls, floor and ceiling reflect it.
// Reports the first surface hit so the caller can add dispersion and the
// glass reflection.
vec3 prTrace(vec3 ro, vec3 rd, vec3 thr0, float dist0, float ior, int maxB, int lamps,
             PrScene sc, VisualUniforms u, out int firstId, out vec3 fHit, out vec3 fN, out float fT) {
    vec3 col = vec3(0.0);
    vec3 thr = thr0;
    float dist = dist0;
    firstId = -1; fHit = ro; fN = vec3(0.0, 1.0, 0.0); fT = 0.0;
    for (int bnc = 0; bnc < 12; bnc++) {
        if (bnc >= maxB) break;
        // room: the three walls, floor and ceiling
        float tR = 1e4;
        int kind = 0;
        for (int k = 0; k < 3; k++) {
            vec2 n = prWallN(k);
            float dn = dot(rd.xz, n);
            if (dn > 1e-5) {
                float t = (PR_RI - dot(ro.xz, n)) / dn;
                if (t < tR) { tR = t; kind = k; }
            }
        }
        if (rd.y < -1e-5) { float t = -ro.y / rd.y; if (t < tR) { tR = t; kind = 3; } }
        if (rd.y > 1e-5) { float t = (PR_H - ro.y) / rd.y; if (t < tR) { tR = t; kind = 4; } }
        tR = max(tR, 0.0);
        // prisms
        int pid = -1;
        float tP = tR;
        vec3 nP = vec3(0.0, 1.0, 0.0);
        // satellites only near the eye; deeper in the mirror lattice only the
        // central prism repeats, which keeps the kaleidoscope legible
        float np = bnc < 3 ? sc.count : 0.0;
        for (int i = 0; i < 7; i++) {
            if (float(i) > np) break;
            vec3 c; float ang, rin, h;
            prPrismParams(i, sc, c, ang, rin, h);
            float tN, tF; vec3 nN, nF;
            if (prPrism(ro, rd, c, ang, rin, h, tN, tF, nN, nF) && tN > 1e-3 && tN < tP) {
                tP = tN; nP = nN; pid = i;
            }
        }
        float seg = min(tP, tR);
        col += thr * prVolume(ro, rd, seg, dist, sc, bnc < 2 ? lamps : 0, bnc == 0 ? 1.0 : 0.45, u);
        dist += seg;
        // haze: everything fades with path length into the deep lattice
        thr *= exp(-seg * 0.07);
        vec3 p = ro + rd * seg;
        float w = max(0.004, dist * sc.pix * 1.2);
        if (bnc == 0) { firstId = pid; fHit = p; fT = seg; }
        if (pid >= 0) {
            vec3 c; float ang, rin, h;
            prPrismParams(pid, sc, c, ang, rin, h);
            if (bnc == 0) fN = nP;
            // edge filaments and their spectral fringes
            vec2 de = prEdgeDist(p - c, ang, rin, h);
            float edge = exp(-de.x * de.x / (w * w)) + exp(-de.y * de.y / (w * w));
            float dm = min(de.x, de.y);
            vec3 fringe = prSpectrum(saturate(dm / (6.0 * w))) * exp(-dm / (4.0 * w)) * 0.3;
            vec3 rim = prDeep(palVisual(0.55 + 0.11 * float(pid), u));
            float eg = pid == 0 ? 1.0 : 0.45;
            col += thr * (peakWhite(rim, 0.3) * edge + fringe) * sc.edgeGlow * eg * (0.9 + 0.1 * u.level);
            float cosi = saturate(-dot(rd, nP));
            float fr = 0.04 + 0.96 * pow(1.0 - cosi, 5.0);
            vec3 d1 = refract(rd, nP, 1.0 / ior);
            if (dot(d1, d1) < 1e-6) d1 = reflect(rd, nP);
            vec3 p1 = p + d1 * 1e-3;
            float tN2, tF2; vec3 nN2, nF2;
            bool ok = prPrism(p1, d1, c, ang, rin, h, tN2, tF2, nN2, nF2);
            float tx = ok ? max(tF2, 0.0) : 0.0;
            vec3 p2 = p1 + d1 * tx;
            vec3 d2 = refract(d1, -nF2, ior);
            if (dot(d2, d2) < 1e-6) d2 = reflect(d1, -nF2);
            vec3 tint = mix(vec3(1.0), prDeep(palVisual(0.15 + 0.13 * float(pid), u)), 0.22);
            // a faint glow held inside the glass, rising towards the top
            col += thr * tint * (1.0 - fr) * 0.015 * (0.4 + 0.6 * saturate(p.y / h));
            if (pid == 0) {
                // the struck prism holds the beam: a white core where it
                // enters, opening into a spectrum wedge through the glass
                vec3 m = p + d1 * tx * 0.5;
                float sy = m.y - PR_BEAMY;
                float acr = dot(m.xz, vec2(-sc.fanDir.y, sc.fanDir.x)) / rin;
                float along = saturate(0.5 + 0.5 * dot(m.xz, sc.fanDir) / rin);
                vec3 wedge = mix(vec3(1.0), prSpectrum(saturate(0.5 + 0.7 * acr)) * 1.3, smoothstep(0.0, 0.4, along));
                float vy = exp(-sy * sy / (0.012 + 0.16 * along * along));
                col += thr * (1.0 - fr) * wedge * vy * tx * sc.fanE * 0.75;
            }
            thr *= (1.0 - fr) * tint * 0.95;
            dist += tx;
            ro = p2 + d2 * 1e-3;
            rd = d2;
            continue;
        }
        vec3 n;
        vec3 refl;
        if (kind < 3) {
            vec2 wn = prWallN(kind);
            n = vec3(-wn.x, 0.0, -wn.y);
            col += thr * prWallLight(p, kind, w, sc, u);
            // each wall's silvering is faintly tinted, so the lattice drifts
            // in colour as it recedes
            refl = kind == 0 ? sc.tint0 : (kind == 1 ? sc.tint1 : sc.tint2);
        } else if (kind == 3) {
            n = vec3(0.0, 1.0, 0.0);
            col += thr * prFloorLight(p, w, sc, u);
            refl = vec3(0.4);
        } else {
            n = vec3(0.0, -1.0, 0.0);
            col += thr * prCeilLight(p, w, sc, u);
            refl = vec3(0.14);
        }
        if (bnc == 0) fN = n;
        thr *= refl;
        if (max(thr.r, max(thr.g, thr.b)) < 0.02) break;
        rd = reflect(rd, n);
        ro = p + n * 1e-3;
    }
    return col;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float tw = mod(u.time, 600.0);

    PrScene sc;
    sc.count = floor(mix(1.0, 5.0, u.param0) + 0.5);
    sc.tw = tw;
    // the shared speed profile: drift pace, and the rush that brightens the fan
    vec2 fl = osFlight(u, mix(0.1, 0.32, u.param1), mix(0.01, 0.04, u.param1));

    float rush = saturate((fl.y - 1.0) / 3.5);
    sc.edgeGlow = mix(0.5, 2.0, u.param2);
    sc.chase = tw * mix(0.01, 0.05, u.param1);
    // the beam enters through a slot high on wall 0 and strikes the central
    // prism; the fan leaves turned away by the glass
    vec2 n0 = prWallN(0);
    vec2 sxz = n0 * (PR_RI - 0.01) + vec2(-n0.y, n0.x) * 1.4;
    sc.bs = vec3(sxz.x, 2.6, sxz.y);
    vec3 be = vec3(0.0, PR_BEAMY, 0.0);
    sc.bd = be - sc.bs;
    sc.bl = length(sc.bd);
    sc.bd /= sc.bl;
    vec2 inc = normalize(sc.bd.xz);
    // the central prism turns a step on every beat, so the spectrum sweeps
    // across the walls in time
    float kb = u.beatCount + smoothstep(0.0, 1.0, saturate(u.beatPhase));
    float dev = 0.7 + 0.12 * sin(tw * 0.023) + 0.01 * sin(kb * 2.39996);
    sc.fanDir = vec2(inc.x * cos(dev) - inc.y * sin(dev), inc.x * sin(dev) + inc.y * cos(dev));
    sc.fanE = (0.8 + 0.1 * u.level) * (1.0 + 0.15 * rush);
    float bp = saturate(u.beatPhase);
    sc.ringR = 0.5 + 3.2 * pow(bp, 0.6);
    sc.ringA = smoothstep(0.0, 0.3, bp) * (1.0 - bp) * 0.05;



    // ---- camera: a slow drift around the prisms, gazing in and out ----
    float oa = fl.x * 0.07;
    float orad = 2.75 + 0.2 * sin(oa * 0.7 + 1.0);
    vec3 ro = vec3(cos(oa) * orad, 1.8 + 0.3 * sin(tw * 0.043) + 0.12 * sin(oa * 1.3), sin(oa) * orad);
    // the gaze swings between the central prism and the lattice beyond it
    float gz = 0.5 + 0.5 * sin(tw * 0.021);
    vec3 side = vec3(-sin(oa), 0.0, cos(oa));
    vec3 ta = mix(vec3(0.0, 0.95, 0.0), ro + side * 3.0 + vec3(0.0, 0.4 * sin(tw * 0.017), 0.0), gz * 0.4);
    float roll = 0.0;
    float fov = 1.2;
    vec3 rd = osCamera(ro, ta, roll, fov, p);
    sc.pix = 2.0 / (u.resolution.y * fov);

    sc.cornerC = peakWhite(prDeep(palVisual(0.55, u)), 0.4);
    sc.tint0 = mix(vec3(0.44), prDeep(palVisual(0.5, u)) * 0.44, 0.16);
    sc.tint1 = mix(vec3(0.44), prDeep(palVisual(0.7, u)) * 0.44, 0.16);
    sc.tint2 = mix(vec3(0.44), prDeep(palVisual(0.9, u)) * 0.44, 0.16);

    int maxB = osSteps(3.0, 5.0, detail);
    int lamps = osSteps(2.0, 4.0, detail);
    // passes: the eye's path; then, where it first meets glass, one path per
    // colour channel's index (dispersion) and the reflection off the face.
    // One loop keeps a single copy of the trace in the compiled shader.
    vec3 col = vec3(0.0);
    int fid = -1; vec3 fh = ro; vec3 fn = vec3(0.0, 1.0, 0.0); float ft = 0.0;
    int passes = 1;
    bool disperse = false;
    for (int ps = 0; ps < 4; ps++) {
        if (ps >= passes) break;
        int role = (ps == 1 && !disperse) ? 3 : ps;
        vec3 pro = ro;
        vec3 prd = rd;
        vec3 thr = vec3(1.0);
        float d0 = 0.0;
        int mb = role == 0 ? maxB : maxB - 2;
        float ior = role == 1 ? 1.46 : (role == 2 ? 1.54 : 1.5);
        if (role == 3) {
            prd = reflect(rd, fn);
            pro = fh + prd * 1e-3;
            thr = vec3(0.04 + 0.96 * pow(1.0 - saturate(-dot(rd, fn)), 5.0));
            d0 = ft;
            mb = 1;
        }
        int qid; vec3 qh; vec3 qn; float qt;
        vec3 c = prTrace(pro, prd, thr, d0, ior, mb, role == 3 ? 0 : lamps, sc, u, qid, qh, qn, qt);
        if (role == 0) {
            col = c;
            fid = qid; fh = qh; fn = qn; ft = qt;
            if (fid >= 0) passes = disperse ? 4 : 2;
        } else if (role == 1) {
            col.r = c.r;
        } else if (role == 2) {
            col.b = c.b;
        } else {
            col += c;
        }
    }
    return prOffRed(max(col, vec3(0.0))); // LINEAR HDR; the post chain tonemaps

}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
