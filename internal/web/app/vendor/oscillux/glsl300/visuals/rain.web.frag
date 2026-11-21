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
// Rain.
// A slow walk at ankle height down a rain-soaked street at night. Both walls
// carry rows of upright neon tubes whose height breathes with their band,
// so the street is a spectrum running low to high and back; neon gates span
// the street overhead and a strip line runs along each wall. The black floor
// is wet: puddles mirror everything in long vertical smears, every drop rings
// the puddles and throws a crown of droplets near the camera, and the falling
// rain is lit by whichever neon it passes, nearest drops dimmest. The camera
// keeps a slow steady pace with no roll. Audio drives the light, gently: the
// smoothed level with a trace of each band sets the tubes and the tower
// meter, a beat sends a faint pulse down the street, and nothing flashes or
// jumps with the kick, so the neon glows and fades instead of strobing.
// param0 = Density ; param1 = Fall Speed ; param2 = Reflection

const float RN_W = 4.2;      // street half-width
const float RN_BAR = 1.6;    // tube spacing along each wall
const float RN_GATE = 14.0;  // spacing of the gates over the street
const float RN_GY = 3.9;     // gate height
const float RN_STRIP = 3.3;  // wall strip height
const float RN_CELL = 0.6;   // ripple / splash cell
const float RN_FOG = 0.042;

float rnHash(vec2 c) { return hash21(mod(c, 1024.0) + 0.5); }

// Keeps every red just off saturated red once tonemapped: green and blue
// rise together until they make 0.27 of red, so no red area can strobe.
float rnTone(float x) { return x * (2.51 * x + 0.03) / (x * (2.43 * x + 0.59) + 0.14); }
float rnToneInv(float y) {
    y = clamp(y, 0.0, 0.999);
    float a = 2.51 - 2.43 * y, b = 0.03 - 0.59 * y;
    return (-b + sqrt(b * b + 0.56 * a * y)) / (2.0 * a);
}
vec3 rnOffRed(vec3 c) {
    float r = rnTone(c.r), g = rnTone(c.g), b = rnTone(c.b);
    float need = 0.27 * r - g - b;
    if (need <= 0.0) return c;
    c.g = rnToneInv(g + 0.5 * need);
    c.b = rnToneInv(b + 0.5 * need);
    return c;
}

// Spectrum position of a tube: along each wall the bars run low to high and back.
float rnBarF(float cz, float side) {
    float f = fract(cz * (1.0 / 24.0) + side * 0.25);
    return abs(f * 2.0 - 1.0);
}

float rnPulse(float z, float zb, VisualUniforms u) {
    float ph = saturate(u.beatPhase);
    return exp(-(z - zb) * (z - zb) * 0.03) * smoothstep(0.0, 0.04, ph) * (1.0 - ph);
}

// Emission of the wall at w = (z, y). blur widens every edge (vertically
// stretched for the wet-floor reflection, and with distance against aliasing).
vec3 rnWall(vec2 w, float side, float blur, float zb, VisualUniforms u) {
    float bs = 1.0 + blur * 3.0;
    // upright spectrum tubes
    float qz = w.x / RN_BAR + side * 0.37;
    float cz = floor(qz);
    float lz = (qz - cz - 0.5) * RN_BAR;
    float f = rnBarF(cz, side);
    float lv = 0.45 * saturate(u.level) + 0.3 * bandAt(u, 0.03 + 0.9 * f);
    float top = 0.9 + 1.6 * lv;
    float dxb = max(abs(lz) - 0.04, 0.0);
    float dyb = max(max(0.15 - w.y, w.y - top), 0.0) / bs;
    float d = length(vec2(dxb, dyb));
    vec3 bc = palVisual(0.05 + 0.85 * f, u);
    bc = peakWhite(bc, 0.25 * smoothstep(top - 0.35, top, w.y) * lv);
    float core = 1.0 - smoothstep(0.0, 0.012 + 0.05 * blur, d);
    float halo = exp(-d * 5.0 / (1.0 + blur * 2.0));
    vec3 e = bc * (core * 3.2 + halo * 0.45) * (0.5 + 0.5 * lv);
    // storefront segments: a sign line at its own height, lifted by the bass and
    // swept by the beat pulse, over a softly lit shop window
    float qs = w.x / 7.0 + side * 0.31;
    float sg = floor(qs);
    float ls = (qs - sg) * 7.0;
    float h1 = rnHash(vec2(sg, side * 5.0 + 1.0));
    float h2 = rnHash(vec2(sg, side * 5.0 + 2.0));
    float hy = RN_STRIP - 0.9 + 1.3 * h1;
    float sl = 1.6 + 2.2 * h2;
    float dzs = max(abs(ls - 3.5) - sl, 0.0);
    float dys = length(vec2(dzs, (w.y - hy) / bs));
    vec3 sc = palVisual(fract(0.3 + 0.55 * h1 + 0.25 * side), u);
    float drive = 0.85 + 0.1 * u.level + 0.08 * rnPulse(w.x, zb, u);
    e += sc * ((1.0 - smoothstep(0.0, 0.02 + 0.06 * blur, dys)) * 2.2 + exp(-dys * 8.0) * 0.3) * drive;
    if (h2 > 0.35) {
        vec2 sd = vec2(max(abs(ls - 3.5) - 2.2, 0.0), max(abs(w.y - 1.05) - 0.8, 0.0) / bs);
        float shop = exp(-dot(sd, sd) * 60.0 / (1.0 + blur * 4.0));
        vec3 shc = mix(vec3(0.9, 0.55, 0.35), palVisual(fract(h1 * 3.0), u), 0.5);
        e += shc * shop * (0.03 + 0.05 * smoothstep(1.85, 0.25, w.y)) * (0.7 + 0.6 * h1);
    }
    // tall signboards high on the walls: a neon outline around rows of
    // glyph blocks that change slowly, lit by their own spectrum band
    if (w.y > 4.2) {
        float qg = w.x / 9.0 + side * 0.23;
        float gs = floor(qg);
        float lg = (qg - gs) * 9.0;
        float g1 = rnHash(vec2(gs, side * 7.0 + 3.0));
        float g2 = rnHash(vec2(gs, side * 7.0 + 4.0));
        if (g1 > 0.2) {
            float hwid = 0.5 + 0.45 * g1;
            float cx = 1.2 + hwid + (6.6 - 2.0 * hwid) * g2;
            float y0 = 4.6 + 1.4 * g2;
            float hh = 1.4 + 1.6 * g1;
            vec2 lp = vec2(lg - cx, (w.y - y0 - hh) / bs);
            vec2 hb = vec2(hwid, hh / bs);
            vec2 dq = abs(lp) - hb;
            float box = length(max(dq, 0.0)) + min(max(dq.x, dq.y), 0.0);
            float bd = abs(box);
            vec3 gcA = palVisual(fract(0.12 + 0.62 * g2 + 0.5 * side), u);
            vec3 gcB = palVisual(fract(0.62 + 0.62 * g2 + 0.5 * side), u);
            float glv = bandAt(u, 0.1 + 0.8 * g1);
            float gdrive = 0.7 + 0.3 * u.level + 0.2 * glv + 0.12 * rnPulse(w.x, zb, u);
            e += gcA * ((1.0 - smoothstep(0.0, 0.025 + 0.06 * blur, bd)) * 2.6 + exp(-bd * 7.0) * 0.25) * gdrive;
            if (box < 0.0) {
                // glyph grid: two columns of blocks, each a stack of strokes
                vec2 gq = (vec2(lg - cx, w.y - y0 - hh) + vec2(hwid, hh)) / vec2(hwid, 0.62);
                vec2 gc = floor(gq);
                vec2 gl = fract(gq) - 0.5;
                float gt = floor(mod(u.time, 600.0) * 0.35 + rnHash(gc + gs));
                float on = step(0.3, rnHash(gc + vec2(gs * 3.1, side + mod(gt, 97.0))));
                float st = rnHash(gc + vec2(gs, 9.0 + mod(gt, 97.0)));
                // a stroke pattern per block: bars or a box
                float bar = st > 0.5 ? abs(gl.y) - 0.08 : max(abs(gl.x), abs(gl.y)) - 0.24;
                float blk = max(max(abs(gl.x) - 0.3, abs(gl.y) - 0.34), st > 0.5 ? bar : abs(bar) - 0.06);
                float gd = max(blk, 0.0);
                float gly = (1.0 - smoothstep(0.0, 0.05 + 0.1 * blur, gd)) * on;
                e += gcB * gly * 1.6 * gdrive + gcA * 0.025;
            }
        }
    }
    // dim windows high above
    if (w.y > 4.4) {
        vec2 wq = vec2(w.x / 0.75 + side * 0.5, (w.y - 4.4) / 0.95);
        vec2 wc = floor(wq);
        vec2 wl = abs(fract(wq) - 0.5);
        float hw = rnHash(wc + side * 41.0);
        float lit = step(0.6, hw);
        float win = (1.0 - smoothstep(0.26, 0.3 + 0.2 * blur, wl.x)) * (1.0 - smoothstep(0.24, 0.28 + 0.2 * blur, wl.y));
        vec3 wcol = mix(vec3(0.9, 0.55, 0.3), vec3(0.35, 0.5, 0.9), rnHash(wc + 7.0));
        e += wcol * win * lit * (0.015 + 0.08 * rnHash(wc + 3.0) * rnHash(wc + 3.0));
    }
    return e;
}

// Neon gates over the street, crossed by a ray up to tmax.
vec3 rnGates(vec3 ro, vec3 rd, float tmax, float blur, VisualUniforms u, float zb) {
    vec3 e = vec3(0.0);
    if (rd.z < 0.02) return e;
    float g0 = floor(ro.z / RN_GATE) + 1.0;
    float drive = 0.95 + 0.1 * u.level;
    vec3 acc = mod(floor(u.beatCount), 2.0) < 0.5 ? vec3(1.0, 0.5, 0.12) : vec3(0.1, 0.65, 1.0);
    for (int k = 0; k < 4; k++) {
        float gi = g0 + float(k);
        float t = (gi * RN_GATE - ro.z) / rd.z;
        if (t > tmax) break;
        vec3 q = ro + rd * t;
        float dy = abs(q.y - RN_GY) / (1.0 + blur * 3.0);
        float dx = max(abs(q.x) - RN_W, 0.0);
        float d = length(vec2(dx, dy)) / (1.0 + 0.004 * t);
        vec3 gc = palVisual(0.55 + 0.4 * rnHash(vec2(gi, 2.0)), u);
        float pls = rnPulse(gi * RN_GATE, zb, u);
        gc = mix(gc, acc, 0.15 * pls);
        float pz = 1.0 + 0.15 * pls;
        e += gc * ((1.0 - smoothstep(0.0, 0.035 + 0.08 * blur, d)) * 3.0 + exp(-d * 4.5) * 0.35)
             * drive * pz * exp(-t * RN_FOG);
    }
    return e;
}

vec3 rnHaze(vec3 rd, vec3 ha, vec3 hb) {
    return mix(ha, hb, 0.5 + 0.4 * rd.x);
}

// Roofline of the buildings along one wall.
float rnRoof(float z, float side) {
    return 12.6 + 8.0 * rnHash(vec2(floor(z / 11.0 + side * 0.4), side * 3.0 + 21.0));
}

// The far city beyond the street: low cloud lit from below by the neon, and
// at the end of the street a tower whose facade is a spectrum meter, its
// spine flaring warm or cool on alternate beats.
vec3 rnSky(vec3 rd, vec3 ha, vec3 hb, float blur, float open, VisualUniforms u) {
    float tm = mod(u.time, 600.0);
    float hz = max(length(rd.xz), 1e-3);
    float e = rd.y / hz;
    float az = rd.x / max(rd.z, 0.05);
    vec3 base = rnHaze(rd, ha, hb);
    float glow = exp(-max(e, 0.0) * 4.0);
    vec2 cq = rd.xz / max(rd.y, 0.06) * 0.5 + vec2(0.0, tm * 0.35);
    float cn = vnoise(cq * 0.6) + 0.5 * vnoise(cq * 1.7 + 3.0);
    vec3 under = mix(palVisual(0.62, u), vec3(0.55, 0.1, 0.9), 0.4);
    float hi = smoothstep(0.06, 0.35, e) * open;
    vec3 c = base * (1.0 - 0.5 * hi)
           + under * smoothstep(0.6, 1.3, cn) * hi * (0.035 + 0.05 * glow);
    // side towers: dark, with sparse warm windows and a thin neon crown
    for (int k = 0; k < 2; k++) {
        float cx = k == 0 ? -0.15 : 0.13;
        float hw = k == 0 ? 0.045 : 0.035;
        float top = k == 0 ? 0.26 : 0.33;
        float dx = abs(az - cx) - hw;
        if (dx < 0.0 && e < top) {
            vec2 wc = floor(vec2(az * 260.0, e * 150.0));
            float wl = step(0.82, rnHash(wc + float(k) * 17.0));
            c = base * 0.45 + vec3(0.9, 0.55, 0.3) * wl * 0.05;
            c += palVisual(k == 0 ? 0.3 : 0.8, u) * exp(-abs(e - top) * 400.0) * 1.2;
        }
    }
    // the spectrum tower
    float tdx = abs(az) - 0.075;
    float ttop = 0.5;
    if (tdx < 0.0 && e < ttop) {
        float f = saturate((az + 0.075) / 0.15);
        float colI = floor(f * 12.0);
        float fb = (colI + 0.5) / 12.0;
        float lv = 0.45 * saturate(u.level) + 0.3 * bandAt(u, 0.03 + 0.9 * abs(fb * 2.0 - 1.0));
        float fill = ttop * (0.12 + 0.82 * lv);
        vec2 wq = vec2(f * 12.0, e / 0.011);
        vec2 wl = abs(fract(wq) - 0.5);
        float win = (1.0 - smoothstep(0.3, 0.36 + blur * 0.2, wl.x)) * (1.0 - smoothstep(0.28, 0.34 + blur * 0.2, wl.y));
        vec3 wcol = palVisual(0.05 + 0.85 * abs(fb * 2.0 - 1.0), u);
        float on = smoothstep(fill + 0.004, fill - 0.004, e);
        float topRow = exp(-abs(e - fill) * 300.0);
        c = base * 0.3 + wcol * win * (on * (0.9 + 0.5 * lv) + topRow * 1.2 + 0.03);
        // the spine
        float par = mod(floor(u.beatCount), 2.0);
        vec3 acc = par < 0.5 ? vec3(1.0, 0.5, 0.12) : vec3(0.1, 0.65, 1.0);
        float sp = exp(-az * az * 90000.0 / (1.0 + blur * 6.0));
        float pulse = exp(-abs(e - ttop * saturate(u.beatPhase)) * 40.0) * (1.0 - saturate(u.beatPhase));
        c += acc * sp * (0.6 + 0.2 * pulse);
    }
    // the crown of the tower and its slow warning beacon
    float bd = length(vec2(az, (e - ttop - 0.012) * 0.6));
    float blink = 0.5 - 0.5 * cos(tm * 3.14159265);
    c += vec3(1.0, 0.15, 0.08) * exp(-bd * bd * 4e5 / (1.0 + blur * 8.0)) * (0.3 + 1.0 * blink);
    c += mix(palVisual(0.9, u), vec3(1.0), 0.3) * exp(-abs(e - ttop) * 500.0) * step(abs(az), 0.075) * 0.8;
    return c;
}

// Everything a ray that leaves the floor sees: a wall, the gates, the haze,
// and the city above the rooftops.
vec3 rnTrace(vec3 ro, vec3 rd, float blur, float zb, vec3 ha, vec3 hb, VisualUniforms u) {
    float side = rd.x >= 0.0 ? 1.0 : -1.0;
    float tw = abs(rd.x) > 1e-4 ? (side * RN_W - ro.x) / rd.x : 400.0;
    tw = clamp(tw, 0.0, 400.0);
    vec3 pw = ro + rd * tw;
    float fog = exp(-tw * RN_FOG);
    vec3 c = vec3(0.0);
    float roof = rnRoof(pw.z, side);
    if (pw.y > roof) {
        c = rnSky(rd, ha, hb, blur, 1.0, u);
    } else {
        // the fog in front of a wall hides the clouds, not the far towers
        vec3 far = fog < 0.99 ? rnSky(rd, ha, hb, blur, 0.0, u) : vec3(0.0);
        if (fog > 0.01) c = rnWall(pw.zy, side, blur + tw * 0.004, zb, u) * fog;
        // a neon line along the parapet
        float pe = exp(-abs(pw.y - roof) * 14.0 / (1.0 + blur * 3.0 + tw * 0.01));
        c += palVisual(side > 0.0 ? 0.82 : 0.28, u) * pe * 0.6 * fog * (0.8 + 0.2 * u.level);
        c += far * (1.0 - fog);
    }
    c += rnGates(ro, rd, tw, blur, u, zb);
    return c;
}

// Cheap neon light falling on a point in the street (rain, splashes, floor).
vec3 rnLight(vec3 q, VisualUniforms u) {
    vec3 l = vec3(0.0);
    for (int s = 0; s < 2; s++) {
        float side = s == 0 ? -1.0 : 1.0;
        float dw = max(RN_W - side * q.x, 0.05);
        float qz = q.z / RN_BAR + side * 0.37;
        float f = rnBarF(floor(qz), side);
        l += palVisual(0.05 + 0.85 * f, u) * (0.5 + 0.5 * u.level) / (1.0 + dw * dw * 0.35);
    }
    float gz = q.z - RN_GATE * floor(q.z / RN_GATE + 0.5);
    float gy = q.y - RN_GY;
    l += palVisual(0.75, u) * 0.6 / (1.0 + (gz * gz + gy * gy) * 0.25);
    return l;
}

// One rain cycle of a floor cell: x = age (s), y = 1 when the cell drops this cycle,
// z = the impact's x offset in the cell.
vec3 rnDrop(vec2 c, float tm, float dens) {
    float per = 0.9 + 0.8 * rnHash(c + 17.3);
    float k = tm / per + rnHash(c);
    float ci = floor(k);
    float h = rnHash(c + vec2(mod(ci, 509.0) * 1.37, 5.0));
    return vec3((k - ci) * per, step(h, dens), (rnHash(c + vec2(3.1, mod(ci, 509.0))) - 0.5) * 0.16);
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float tm = mod(u.time, 600.0);
    float dens = mix(0.25, 0.85, u.param0);
    float fall = mix(2.5, 6.5, u.param1);
    float wet = u.param2;

    // camera: ankle height, a slow walk with a gentle weave; the audio never moves it
    vec2 fl = osFlight(u, 0.0, 0.0);
    fl.x += 0.22 * u.time;
    float z = fl.x;
    vec3 ro = vec3(1.1 * sin(z * 0.045) + 0.4 * sin(z * 0.11 + 1.0), 0.5 + 0.08 * sin(z * 0.07), z);
    vec3 ta = vec3(0.6 * sin((z + 7.0) * 0.045) + 0.3 * sin(z * 0.03 + 2.0), 1.35, z + 7.0);
    float roll = 0.0;
    vec3 rd = osCamera(ro, ta, roll, 1.2, p);
    float zb = z + 3.0 + 34.0 * saturate(u.beatPhase);
    float hl = 0.03 + 0.05 * u.level;
    vec3 ha = palVisual(0.2, u) * hl + vec3(0.004, 0.005, 0.012);
    vec3 hb = palVisual(0.62, u) * hl + vec3(0.004, 0.005, 0.012);

    vec3 col;
    float tHit;
    float tf = rd.y < -1e-4 ? -ro.y / rd.y : 1e4;
    float side = rd.x >= 0.0 ? 1.0 : -1.0;
    float tw = abs(rd.x) > 1e-4 ? (side * RN_W - ro.x) / rd.x : 1e4;
    if (tf < tw) {
        // the wet floor
        tHit = tf;
        vec3 q = ro + rd * tf;
        float pn = vnoise(q.xz * 0.32) + 0.3 * vnoise(q.xz * 1.4 + 9.0);
        float pm = smoothstep(0.62 - 0.45 * wet, 0.72 - 0.45 * wet, pn);
        vec3 n = vec3(0.0, 1.0, 0.0);
        // asphalt grain where it is not pooled
        float gr = vnoise(q.xz * vec2(16.0, 7.0)) - 0.5;
        n.xz += vec2(gr, vnoise(q.xz * vec2(7.0, 16.0) + 4.0) - 0.5) * 0.035 * (1.0 - pm);
        // ripple rings from the drops, near the camera
        float near = saturate(1.0 - tf / 16.0);
        if (near > 0.0) {
            vec2 c = floor(q.xz / RN_CELL);
            vec3 dr = rnDrop(c, tm, dens);
            vec2 l = q.xz - (c + 0.5) * RN_CELL - vec2(dr.z, 0.0);
            float r = length(l);
            float ph = dr.x / 1.7;
            float rr = 0.01 + 0.15 * sqrt(saturate(ph));
            float x = r - rr;
            float wave = sin(x * 90.0) * exp(-x * x * 2500.0) * (1.0 - ph) * (1.0 - ph) * dr.y;
            n.xz += l / max(r, 1e-3) * wave * (0.05 + 0.2 * pm) * near;
        }
        n = normalize(n);
        vec3 rr = reflect(rd, n);
        rr.y = max(rr.y, 0.015);
        rr = normalize(rr);
        float fres = 0.06 + 0.94 * pow(1.0 - saturate(dot(n, -rd)), 3.0);
        float blur = mix(1.3, 0.05, pm) + tf * 0.01;
        vec3 refl = rnTrace(q, rr, blur, zb, ha, hb, u);
        float strength = mix(0.25, 1.0, pm) * mix(0.5, 1.2, wet);
        // neon pooling on the floor beside each wall
        vec3 pool = rnLight(q + vec3(0.0, 0.5, 0.0), u) * 0.03;
        col = pool + refl * fres * strength;
        float fog = exp(-tf * RN_FOG);
        col = col * fog + rnHaze(rd, ha, hb) * (1.0 - fog);
    } else {
        tHit = tw;
        col = rnTrace(ro, rd, 0.0, zb, ha, hb, u);
    }

    // light columns under the lamps on each gate, with the rain glittering in them
    vec2 rxz = rd.xz;
    float rl2 = max(dot(rxz, rxz), 1e-4);
    float lg0 = floor(ro.z / RN_GATE);
    for (int k = 0; k < 6; k++) {
        float gi = lg0 + 1.0 + float(k / 2);
        float lx = (k - 2 * (k / 2)) == 0 ? -1.9 : 1.9;
        vec2 lp = vec2(lx, gi * RN_GATE + 0.6);
        float sc = dot(lp - ro.xz, rxz) / rl2;
        if (sc < 0.0 || sc > tHit) continue;
        vec2 dd = ro.xz + rxz * sc - lp;
        float y = ro.y + rd.y * sc;
        float below = RN_GY - 0.1 - y;
        vec3 lc = palVisual(0.55 + 0.4 * rnHash(vec2(gi, 2.0)), u);
        // the lamp head itself
        float hd = dot(dd, dd) + below * below;
        col += (lc + 0.6) * exp(-hd * 260.0 / (1.0 + sc * 0.08)) * 2.5 * exp(-sc * RN_FOG);
        if (below < 0.0) continue;
        float r = 0.12 + 0.3 * below;
        float cone = exp(-dot(dd, dd) / (r * r)) * smoothstep(0.0, 0.4, below);
        float grain = 0.7 + 0.6 * vnoise(vec2(p.x * 60.0, p.y * 6.0 + tm * fall * 0.6));
        col += lc * cone * grain * (0.7 / (1.0 + 0.5 * below)) * (0.85 + 0.15 * u.level)
               * exp(-sc * RN_FOG) * inversesqrt(rl2);
    }

    // neon-tinted mist in the air
    col += rnLight(ro + rd * min(tHit, 5.0), u) * 0.03 * (1.0 - exp(-min(tHit, 30.0) * 0.08));

    // crowns of droplets where drops hit the floor near the camera
    if (rd.z > 0.05) {
        vec3 sp = vec3(0.0);
        float cz0 = floor((ro.z + 0.8) / RN_CELL);
        int ns = osSteps(4.0, 14.0, detail);
        for (int k = 0; k < 14; k++) {
            if (k >= ns) break;
            float cz = cz0 + float(k);
            float t = ((cz + 0.5) * RN_CELL - ro.z) / rd.z;
            if (t > tHit) break;
            vec3 q = ro + rd * t;
            if (q.y > 0.6) continue;
            float cx = floor(q.x / RN_CELL);
            vec2 c = vec2(cx, cz);
            vec3 dr = rnDrop(c, tm, dens);
            float age = dr.x;
            if (dr.y < 0.5 || age > 0.34) continue;
            vec2 l = vec2(q.x - (cx + 0.5) * RN_CELL - dr.z, q.y);
            float fade = 1.0 - age / 0.34;
            float a = 0.0;
            for (int j = 0; j < 6; j++) {
                float hj = rnHash(c + vec2(float(j) * 3.3, 11.0));
                float ang = (float(j) + 0.5) / 6.0 * 3.14159;
                vec2 v = vec2(cos(ang) * (0.25 + 0.35 * hj), 0.8 + 0.8 * hj);
                vec2 p1 = v * age - vec2(0.0, 4.9 * age * age);
                vec2 p0 = v * max(age - 0.025, 0.0) - vec2(0.0, 4.9 * max(age - 0.025, 0.0) * max(age - 0.025, 0.0));
                vec2 sg = p1 - p0;
                float hseg = saturate(dot(l - p0, sg) / max(dot(sg, sg), 1e-6));
                vec2 dd = l - p0 - sg * hseg;
                float w = 0.0042 + 0.0018 * t;
                a += exp(-dot(dd, dd) / (w * w)) * step(0.0, p1.y) * 1.3;
            }
            // the crown's base flash right at impact
            vec2 b = vec2(l.x * 0.5, l.y - 0.01);
            a += exp(-dot(b, b) * 9000.0) * saturate(1.0 - age * 8.0) * 1.5;
            if (a > 0.002) sp += (rnLight(vec3(q.x, 0.3, q.z), u) + vec3(0.05, 0.06, 0.09)) * a * fade * exp(-t * RN_FOG);
        }
        col += sp * (0.9 + 0.2 * u.level);
    }

    // falling rain in parallax layers, each streak lit by the neon it passes
    vec3 rain = vec3(0.0);
    float dl = 0.45;
    int nl = osSteps(3.0, 7.0, detail);
    for (int k = 0; k < 7; k++) {
        if (k >= nl) break;
        float fk = float(k);
        float t = dl / max(rd.z, 0.25);
        dl *= 2.0;
        if (t > tHit) break;
        vec3 q = ro + rd * t;
        vec2 sq = vec2(q.x + q.y * 0.1 + fk * 0.37, q.y + fall * (1.0 + 0.08 * fk) * tm + fk * 3.7);
        vec2 cs = vec2(0.13, 0.9);
        vec2 cl = floor(sq / cs);
        vec2 lq = sq - cl * cs;
        vec2 cid = cl + vec2(fk * 131.0, 0.0);
        float h = rnHash(cid);
        if (h > dens) continue;
        float ox = (0.15 + 0.7 * rnHash(cid + 7.7)) * cs.x;
        float len = 0.22 + 0.3 * rnHash(cid + 1.9);
        float oy = rnHash(cid + 3.3) * (cs.y - len);
        float w = 0.0035 + t * 0.0016 + 0.012 * exp(-t * 2.5);
        float sx = 1.0 - smoothstep(0.0, w, abs(lq.x - ox));
        float sy = smoothstep(oy, oy + len * 0.6, lq.y) * (1.0 - smoothstep(oy + len * 0.85, oy + len, lq.y));
        float a = sx * sy * (0.0035 / w) * (0.35 + 0.65 * smoothstep(0.5, 2.5, t));
        if (a > 0.001) rain += rnLight(q, u) * a * exp(-t * RN_FOG);
    }
    col += rain * (0.35 + 0.15 * u.level);
    return rnOffRed(max(col, vec3(0.0))); // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
