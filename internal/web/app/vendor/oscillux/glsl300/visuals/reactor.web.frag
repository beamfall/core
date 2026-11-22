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
// Reactor.
// A slow orbit around a fusion core held in a gimbal of containment rings. The
// core is a white-hot sphere of boiling plasma with a bright limb and corona;
// its light fills the haze of the chamber, glints off the machined rings and
// the wet floor, and the air around it shimmers with heat. Spectrum arcs run
// round the inner face of every ring (lows on the inner ring, highs outside),
// a steady drift of sparks rises gently with the level; the field tags, guide
// ring and beam glow a little brighter with the smoothed level, never the core
// light. Every second beat the core sheds a faint eased shell of plasma
// (alternating colours of matched brightness) that washes over the rings and
// runs on across the floor; a cool bounce light from the chamber answers the
// warm core. The arcs and plasma texture flow continuously, the orbit is a
// slow steady drift with a fixed roll, and audio drives soft light only, never
// the camera or sudden flashes.
// param0 = Core (size and heat) ; param1 = Orbit Count (two to four rings) ;
// param2 = Sparks

const float RC_WALL = 15.0;    // chamber wall radius
const float RC_FLOOR = -3.6;   // floor height
const float RC_BOUND = 4.6;    // bounding sphere of the rings

float rcNoise3(vec3 p) {
    vec3 i = floor(p), f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    float a = mix(hash31(i), hash31(i + vec3(1, 0, 0)), f.x);
    float b = mix(hash31(i + vec3(0, 1, 0)), hash31(i + vec3(1, 1, 0)), f.x);
    float c = mix(hash31(i + vec3(0, 0, 1)), hash31(i + vec3(1, 0, 1)), f.x);
    float d = mix(hash31(i + vec3(0, 1, 1)), hash31(i + vec3(1, 1, 1)), f.x);
    return mix(mix(a, b, f.y), mix(c, d, f.y), f.z);
}

// ring k: radius, half thickness (radial, axial)
vec3 rcRingSize(int k) {
    float fk = float(k);
    return vec3(1.85 + 0.68 * fk, 0.11 + 0.015 * fk, 0.21 - 0.025 * fk);
}

// Ring frames are rotations about two axes, eased, slow.
mat3 rcRingFrame(int k, float ang) {
    float fk = float(k);
    float a = ang * (0.55 + 0.25 * fk) * (mod(fk, 2.0) < 0.5 ? 1.0 : -1.0) + fk * 1.3;
    float b = 0.35 + 0.42 * fk + 0.25 * sin(ang * 0.3 + fk);
    float ca = cos(a), sa = sin(a), cb = cos(b), sb = sin(b);
    mat3 ry = mat3(ca, 0.0, -sa, 0.0, 1.0, 0.0, sa, 0.0, ca);
    mat3 rx = mat3(1.0, 0.0, 0.0, 0.0, cb, sb, 0.0, -sb, cb);
    return rx * ry;
}

float rcBox2(vec2 p, vec2 b) {
    vec2 q = abs(p) - b;
    return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0);
}

// distance to ring k in its own frame q; magnet blocks on the outer ring
float rcRing(vec3 q, int k, int nRing) {
    vec3 s = rcRingSize(k);
    float rad = length(q.xz);
    float d = rcBox2(vec2(rad - s.x, q.y), s.yz) - 0.02;
    // a groove down the inner face carries the spectrum arc
    d = max(d, -rcBox2(vec2(rad - s.x + s.y, q.y), vec2(0.04, 0.045)));
    if (k == nRing - 1) {
        float a = atan2_(q.z, q.x);
        float sec = 6.2831853 / 16.0;
        float ai = (floor(a / sec) + 0.5) * sec;
        vec2 cs = vec2(cos(ai), sin(ai));
        vec3 lq = vec3(dot(q.xz, cs) - s.x - 0.05, q.y, dot(q.xz, vec2(-cs.y, cs.x)));
        d = min(d, osBox(lq, vec3(0.16, s.z + 0.08, 0.14)) - 0.02);
    }
    return d;
}

float rcMap(vec3 p, mat3 f0, mat3 f1, mat3 f2, mat3 f3, int nRing, out int id) {
    float d = rcRing(f0 * p, 0, nRing);
    id = 0;
    float e = rcRing(f1 * p, 1, nRing);
    if (e < d) { d = e; id = 1; }
    if (nRing > 2) {
        e = rcRing(f2 * p, 2, nRing);
        if (e < d) { d = e; id = 2; }
    }
    if (nRing > 3) {
        e = rcRing(f3 * p, 3, nRing);
        if (e < d) { d = e; id = 3; }
    }
    return d;
}

// How much core light reaches pos past the rings: light leaves the centre
// radially, so ring k blocks pos when pos lies beyond it and within the band's
// angular height in the ring's frame.
float rcRingVis(vec3 q, int k) {
    vec3 s = rcRingSize(k);
    float rxz = length(q.xz);
    float slope = abs(q.y) / max(rxz, 1e-3);
    float edge = (s.z + 0.02) / s.x;
    float beyond = smoothstep(s.x - s.y, s.x + s.y, length(q));
    return 1.0 - beyond * (1.0 - smoothstep(edge * 0.85, edge * 1.15, slope));
}

float rcVis(vec3 p, mat3 f0, mat3 f1, mat3 f2, mat3 f3, int nRing) {
    float v = rcRingVis(f0 * p, 0) * rcRingVis(f1 * p, 1);
    if (nRing > 2) v *= rcRingVis(f2 * p, 2);
    if (nRing > 3) v *= rcRingVis(f3 * p, 3);
    return v;
}

// in-scattered light of a point source at the origin along ro + rd*t, t in [0, tm]
float rcScatter(vec3 ro, vec3 rd, float tm, float soft) {
    float tc = -dot(ro, rd);
    float h2 = max(dot(ro, ro) - tc * tc, 0.0) + soft * soft;
    float h = sqrt(h2);
    return (atan((tm - tc) / h) - atan(-tc / h)) / h;
}

// Chamber wall at pos: dark ribbed panels lit by the core, and vertical light
// strips that rise with the spectrum.
vec3 rcWall(vec3 pos, vec3 coreLight, vec3 coolFill, float vis, VisualUniforms u) {
    float a = atan2_(pos.z, pos.x);
    float rib = abs(fract(a * 48.0 / 6.2831853) - 0.5);
    float ribs = smoothstep(0.30, 0.5, rib);
    float bands = smoothstep(0.46, 0.5, abs(fract(pos.y * 0.35) - 0.5));
    float lit = 1.0 / (1.0 + pos.y * pos.y * 0.06);
    vec3 col = coreLight * vec3(0.0026, 0.0029, 0.0034) * vis * lit * (0.35 + 0.65 * ribs) * (1.0 - 0.6 * bands);
    float sid = floor(a * 12.0 / 6.2831853);
    float sx = abs(fract(a * 12.0 / 6.2831853) - 0.5);
    float strip = (1.0 - smoothstep(0.004, 0.012, sx)) * smoothstep(RC_FLOOR, RC_FLOOR + 0.6, pos.y);
    float sl = bandAt(u, fract(sid / 12.0 + 0.03));
    float rise = smoothstep(sl * 10.0 + 0.6, sl * 10.0, pos.y - RC_FLOOR);
    col += palRoleTrace(0.55 + sid / 24.0, u) * strip * (0.06 + 2.2 * rise);
    // cool bounce light off the panels, against the warm key of the core
    col += coolFill * 0.014 * lit * (0.3 + 0.7 * ribs) * (1.0 - 0.6 * bands);
    return col;
}

// ray from ro along rd to the chamber wall (ro inside it)
float rcWallT(vec3 ro, vec3 rd) {
    float bw = dot(ro.xz, rd.xz);
    float aw = max(dot(rd.xz, rd.xz), 1e-4);
    float cw = dot(ro.xz, ro.xz) - RC_WALL * RC_WALL;
    return (-bw + sqrt(max(bw * bw - aw * cw, 0.0))) / aw;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);
    float tm = mod(u.time, 600.0);

    // ---- camera: slow orbit on the speed profile, eased roll ----
    vec2 fl = osFlight(u, 0.06, 0.0);
    float orb = 0.06 * u.time + 0.1 * fl.x;
    float rush = saturate((fl.y - 1.0) / 3.5);
    float camR = 5.9 + 0.45 * sin(tm * 0.031);
    float elev = 0.13 + 0.10 * sin(tm * 0.047 + 1.0);
    vec3 ro = camR * vec3(cos(elev) * cos(orb), sin(elev), cos(elev) * sin(orb));
    vec2 ph = p;

    // ---- heat shimmer: the air near the core bends the view ----
    float coreR = mix(0.85, 1.3, u.param0) * (1.0 + 0.0 * u.bassImpact);
    float heatAmt = 0.006 + 0.002 * u.level;
    vec2 hw = vec2(vnoise(p * 9.0 + vec2(0.0, -tm * 0.9)), vnoise(p * 9.0 + vec2(5.2, -tm * 1.1))) - 0.5;
    float nearCore = exp(-dot(p, p) * 2.2);
    ph += hw * heatAmt * nearCore;
    float fov = 1.35 - 0.02 * rush;
    vec3 rd = osCamera(ro, vec3(0.0), 0.05, fov, ph);

    int nRing = int(mix(2.0, 4.99, saturate(u.param1)));
    mat3 f0 = rcRingFrame(0, orb * 0.9 + tm * 0.05);
    mat3 f1 = rcRingFrame(1, orb * 0.9 + tm * 0.05);
    mat3 f2 = rcRingFrame(2, orb * 0.9 + tm * 0.05);
    mat3 f3 = rcRingFrame(3, orb * 0.9 + tm * 0.05);

    float heat = (0.75 + 0.5 * u.param0) * (1.1 + 0.15 * u.level);
    // core colours: a saturated body and a hotter, paler crest
    vec3 cb0 = palRoleBass(0.15, u);
    // blackbody orange steered by the palette, so the core always reads as fire
    vec3 cb1 = max(mix(vec3(luma(cb0)), cb0, 1.5), vec3(0.0));
    vec3 coreTint = cb1 / max(max(cb1.r, max(cb1.g, cb1.b)), 0.05);
    vec3 coreBody = mix(vec3(1.0, 0.36, 0.07), vec3(1.0, 0.36, 0.07) * coreTint, 0.3);
    vec3 coreHot = peakWhite(palVisual(0.32 + 0.06 * u.spectralCentroid, u), 0.4);
    vec3 coreLight = mix(coreBody, coreHot, 0.35) * heat;
    // cool fill from the chamber, the complement of the core's fire
    vec3 coolFill = palVisual(0.88, u);

    // ---- beat shockwave: every beat the core throws off a shell of plasma
    // that sweeps out through the rings and across the floor; alternate beats
    // alternate its colour ----
    float bp = saturate(u.beatPhase);
    // a swell that rises and falls within the beat: zero on the kick
    float swell = sin(3.14159265 * bp);
    swell *= swell;
    float sp = saturate((mod(floor(u.beatCount), 2.0) + bp) * 0.5);
    float shR = coreR + sp * (3.4 + 0.6 * u.bass);
    float shW = 0.07 + 0.20 * sp;
    float shA = (0.2 + 0.1 * u.level) * pow(1.0 - sp, 1.3) * smoothstep(0.0, 0.25, sp);
    // where the shell lands on the floor it runs on as a ground wave
    float gwR = sp * (8.0 + 1.5 * u.bass);
    vec3 shA0 = palVisual(0.74, u), shB0 = palVisual(0.52, u);
    float shL = min(luma(shA0), luma(shB0));
    vec3 shCol = mix(shA0 * shL / max(luma(shA0), 1e-3), shB0 * shL / max(luma(shB0), 1e-3),
                     step(0.5, mod(floor(u.beatCount * 0.5), 2.0)));

    // ---- core sphere (analytic) ----
    float b = dot(ro, rd);
    float c = dot(ro, ro) - coreR * coreR;
    float disc = b * b - c;
    float tCore = disc > 0.0 ? -b - sqrt(disc) : 1e4;

    // ---- rings (bounded march) ----
    float tHit = 1e4;
    int hid = 0;
    float dMin = 1e3;
    int nid = 0;
    float cb = dot(ro, ro) - RC_BOUND * RC_BOUND;
    float db = b * b - cb;
    if (db > 0.0) {
        float t = max(-b - sqrt(db), 0.0);
        float tEnd = min(-b + sqrt(db), tCore);
        int n = osSteps(32.0, 64.0, detail);
        for (int i = 0; i < 64; i++) {
            if (i >= n || t > tEnd) break;
            int id;
            float d = rcMap(ro + rd * t, f0, f1, f2, f3, nRing, id);
            if (d < dMin) { dMin = d; nid = id; }
            if (d < 0.0015 * t) { tHit = t; hid = id; break; }
            t += d * 0.9;
        }
    }

    vec3 col = vec3(0.0);
    float tSurf;
    if (tHit < tCore && tHit < 1e3) {
        tSurf = tHit;
        vec3 pos = ro + rd * tHit;
        vec2 e = vec2(0.0015, 0.0);
        int dummy;
        vec3 n = normalize(vec3(
            rcMap(pos + e.xyy, f0, f1, f2, f3, nRing, dummy) - rcMap(pos - e.xyy, f0, f1, f2, f3, nRing, dummy),
            rcMap(pos + e.yxy, f0, f1, f2, f3, nRing, dummy) - rcMap(pos - e.yxy, f0, f1, f2, f3, nRing, dummy),
            rcMap(pos + e.yyx, f0, f1, f2, f3, nRing, dummy) - rcMap(pos - e.yyx, f0, f1, f2, f3, nRing, dummy)) + 1e-6);
        // light from the core
        float r = max(length(pos), 1e-3);
        vec3 l = -pos / r;
        float dif = saturate(dot(n, l));
        float fall = 1.0 / (0.35 + r * r * 0.22);
        vec3 refl = reflect(rd, n);
        // glossy reflection of the core sphere
        float rb = dot(pos, refl);
        float rc2 = dot(pos, pos) - rb * rb;
        float glint = rb < 0.0 ? exp(-max(rc2 - coreR * coreR, 0.0) * 1.3) : 0.0;
        float fres = 0.15 + 0.85 * pow(1.0 - saturate(-dot(rd, n)), 4.0);
        mat3 fr = hid == 0 ? f0 : hid == 1 ? f1 : hid == 2 ? f2 : f3;
        vec3 q = fr * pos;
        float ang = atan2_(q.z, q.x);
        float turn = ang / 6.2831853 + 0.5;
        // machined panels: fine radial grooves
        float groove = 0.7 + 0.3 * smoothstep(0.0, 0.25, abs(fract(turn * 64.0) - 0.5));
        vec3 metal = vec3(0.045, 0.047, 0.055) * groove;
        float rvis = rcVis(pos * 0.995, f0, f1, f2, f3, nRing);
        col = metal * coreLight * (0.25 + dif * 1.6 * rvis) * fall;
        col += coreLight * glint * (0.3 + 1.6 * fres) * 1.4;
        // broad brushed-metal lobe toward the core
        col += coreLight * pow(saturate(dot(refl, l)), 10.0) * 0.35 * rvis * fall * 4.0;
        col += palRoleFog(0.3, u) * 0.03 * fres * saturate(refl.y * 0.5 + 0.6);
        // the chamber strips slide across the polished metal
        vec3 rw = pos + refl * rcWallT(pos, refl);
        col += rcWall(rw, coreLight, coolFill, 1.0, u) * (0.25 + 0.75 * fres) * 0.8;
        // cool rim from the chamber
        col += palRoleRim(0.18, u) * pow(1.0 - saturate(-dot(rd, n)), 3.0) * 0.05;
        // spectrum arc in the groove on the inner face
        vec3 s = rcRingSize(hid);
        float rq = length(q.xz);
        float inGroove = (1.0 - smoothstep(0.035, 0.05, abs(q.y))) *
                         (1.0 - smoothstep(0.02, 0.06, rq - (s.x - s.y + 0.04)));
        float fb = (float(hid) + fract(turn * 2.0)) / 4.0;
        float lvl = bandAt(u, fb);
        float flow = 0.55 + 0.45 * sin(turn * 6.2831853 * 6.0 - tm * (0.6 + 0.2 * float(hid)));
        vec3 arcCol = palRoleTrace(0.12 + 0.18 * float(hid) + 0.25 * turn + 0.1 * u.spectralCentroid, u);
        col += arcCol * inGroove * (2.5 + 2.5 * lvl) * flow;
        // the shockwave washes over the metal as it passes
        col += shCol * shA * exp(-pow((r - shR) / (shW * 0.6), 2.0)) * (0.1 + 1.2 * fres) * 0.7;
        // light strips on the magnet blocks
        if (hid == nRing - 1) {
            float tag = (1.0 - smoothstep(0.02, 0.035, abs(q.y - s.z - 0.04))) * step(s.x + 0.1, rq);
            col += palRoleBass(0.3, u) * tag * (0.9 + 0.15 * u.level + 0.1 * swell * u.level);
        }
    } else if (tCore < 1e3) {
        tSurf = tCore;
        vec3 pos = ro + rd * tCore;
        vec3 n = pos / coreR;
        // plasma: field-line filaments wound round a tilted axis, warped by
        // slow 3D noise, over a white-hot centre and a saturated limb
        float sp = tm * 0.08;
        vec3 q = vec3(n.x * cos(sp) - n.z * sin(sp), n.y, n.x * sin(sp) + n.z * cos(sp));
        float w = rcNoise3(q * 2.2 + vec3(0.0, tm * 0.12, 0.0));
        float g = detail > 0.5 ? rcNoise3(q * 5.0 + w * 2.0 - vec3(tm * 0.14)) : 0.5;
        vec3 ax = normalize(vec3(0.35, 1.0, 0.2));
        float lat = dot(n, ax);
        vec3 tg = normalize(cross(ax, n) + 1e-4);
        float lon = atan2_(dot(tg, vec3(0.0, 0.0, 1.0)), dot(tg, vec3(1.0, 0.0, 0.0)));
        float wind = lat * 16.0 + lon * 1.0 + (w - 0.5) * 6.0 + (g - 0.5) * 4.0 - tm * 0.5;
        float fil = pow(0.5 + 0.5 * sin(wind), 7.0);
        float fil2 = pow(0.5 + 0.5 * sin(wind * 2.3 + 1.7 + tm * 0.25), 9.0);
        float mu = saturate(-dot(rd, n));
        float limb = pow(1.0 - mu, 2.5);
        // temperature: deep red body, orange-gold filaments, white-hot centre
        float temp = 0.2 * w * w + 0.5 * fil + 0.35 * fil2 + 0.75 * pow(mu, 6.0) * (0.6 + 0.8 * g) + 0.45 * limb;
        vec3 fire = mix(vec3(0.62, 0.2, 0.03), vec3(1.0, 0.38, 0.06), smoothstep(0.0, 0.45, temp));
        fire = mix(fire, vec3(1.0, 0.82, 0.55), smoothstep(0.5, 1.2, temp));
        fire = mix(fire, fire * coreTint, 0.3);
        col = fire * (0.25 + 2.6 * temp) * heat;
    } else {
        // ---- chamber: wet floor and ribbed wall ----
        float tf = rd.y < -1e-3 ? (RC_FLOOR - ro.y) / rd.y : 1e4;
        float tw = rcWallT(ro, rd);
        if (tf < tw) {
            tSurf = tf;
            vec3 pos = ro + rd * tf;
            float r = length(pos);
            // wet, slightly rippled black floor: mirror of the core and the wall strips
            vec2 rip = vec2(vnoise(pos.xz * 2.1 + vec2(0.0, tm * 0.2)), vnoise(pos.zx * 2.1)) - 0.5;
            vec3 rr = normalize(vec3(rd.x + rip.x * 0.03, -rd.y, rd.z + rip.y * 0.03));
            float rb = dot(pos, rr);
            float rc2 = max(dot(pos, pos) - rb * rb, 0.0);
            vec2 tile = abs(fract(pos.xz * 0.5) - 0.5);
            float seam = 1.0 - smoothstep(0.0, 0.02, min(tile.x, tile.y));
            float wet = smoothstep(0.25, 0.75, vnoise(pos.xz * 0.9 + 3.0));
            float fres = 0.2 + 0.8 * pow(1.0 - saturate(-rd.y), 5.0);
            float mirror = rb > 0.0 ? exp(-max(rc2 - coreR * coreR, 0.0) * (0.5 + 2.5 * (1.0 - wet))) : 0.0;
            float fvis = rcVis(pos, f0, f1, f2, f3, nRing);
            col = coreLight * 0.04 * fvis / (1.0 + r * r * 0.12) * (1.0 - 0.7 * seam);
            col += coreLight * mirror * (0.15 + 0.6 * wet) * (1.0 - 0.8 * seam);
            vec3 rw = pos + rr * rcWallT(pos, rr);
            col += rcWall(rw, coreLight, coolFill, 1.0, u) * fres * (0.3 + 0.7 * wet) * (1.0 - 0.8 * seam);
            // a glowing guide ring set in the floor under the core
            float fr = length(pos.xz);
            float guide = exp(-pow((fr - 5.2) * 10.0, 2.0)) + 0.5 * exp(-pow((fr - 6.0) * 16.0, 2.0));
            col += palRoleTrace(0.4 + 0.05 * sin(atan2_(pos.z, pos.x) * 3.0), u) * guide * (0.6 + 0.1 * u.level + 0.05 * swell);
            // the shockwave's footprint racing out over the wet floor, and its mirror
            float fq = length(pos.xz) - gwR;
            float shF = exp(-fq * fq * 18.0) + 0.18 * exp(-max(-fq, 0.0) * 1.2) * step(fq, 0.0);
            col += shCol * shA * shF * (0.25 + 0.9 * wet) * (1.0 - 0.6 * seam) * 0.5;
            col += coolFill * 0.010 * (0.4 + 0.6 * wet) * (1.0 - 0.7 * seam);
        } else {
            tSurf = tw;
            vec3 pw = ro + rd * tw;
            col = rcWall(pw, coreLight, coolFill, rcVis(pw, f0, f1, f2, f3, nRing), u);
        }
    }

    // ---- arcs bloom past the ring edges ----
    if (tHit > 1e3) {
        vec3 hc = palRoleTrace(0.12 + 0.18 * float(nid) + 0.1 * u.spectralCentroid, u);
        col += hc * exp(-dMin * 22.0) * (0.06 + 0.06 * bandAt(u, float(nid) / 4.0 + 0.1));
    }

    // ---- injector beam: a thin column of light feeding the core ----
    {
        vec2 rxz = rd.xz;
        float l2 = max(dot(rxz, rxz), 1e-5);
        float tb = -dot(ro.xz, rxz) / l2;
        float db = abs(ro.x * rxz.y - ro.z * rxz.x) / sqrt(l2);
        float yb = ro.y + rd.y * tb;
        if (tb > 0.0 && tb < tSurf && abs(yb) > coreR * 0.9) {
            float pulse = 0.55 + 0.45 * sin(abs(yb) * 3.0 + tm * 2.0);
            float beam = exp(-db * db * 900.0) * 1.6 + exp(-db * db * 40.0) * 0.12;
            col += palRoleTrace(0.62 + 0.1 * u.spectralCentroid, u) * beam * pulse * (1.0 + 0.15 * u.level + 0.05 * swell)
                   * smoothstep(9.0, 2.0, abs(yb));
        }
    }

    // ---- volumetric glow of the core in the chamber haze ----
    float tv = min(tSurf, 40.0);
    float scat = rcScatter(ro, rd, tv, coreR * 0.9);
    // light shafts: weight the rings' shadow along the ray by the light density
    {
        float tc0 = -b;
        float hd2 = max(dot(ro, ro) - tc0 * tc0, 0.0);
        int ns = osSteps(5.0, 14.0, detail);
        float span = 7.0;
        float t0 = max(tc0 - span, 0.0);
        float t1 = min(tc0 + span, tv);
        float dt = (t1 - t0) / float(ns);
        float jit = hash21(uv * u.resolution);
        float sw = 0.0, sv = 0.0;
        for (int i = 0; i < 14; i++) {
            if (i >= ns) break;
            float ts = t0 + (float(i) + jit) * dt;
            vec3 ps = ro + rd * ts;
            float w = 1.0 / (dot(ps, ps) + 0.6);
            sv += w * rcVis(ps, f0, f1, f2, f3, nRing);
            sw += w;
        }
        scat *= t1 > t0 ? mix(0.12, 1.0, sv / max(sw, 1e-5)) : 1.0;
    }
    vec3 hazeCol = mix(coolFill * 1.2, coreBody, 0.5);
    col += hazeCol * scat * 0.032 * heat * 1.05;
    // tight corona just outside the limb
    float tc = -b;
    float dperp = sqrt(max(dot(ro, ro) - tc * tc, 0.0));
    if (tSurf > tc) {
        float cor = exp(-max(dperp - coreR, 0.0) * 5.5) * step(coreR, dperp);
        float crn = rcNoise3(vec3(normalize(ro + rd * tc + 1e-4) * 3.0 + tm * 0.3));
        col += mix(coreBody, coreHot, 0.4) * cor * (0.4 + 0.9 * crn) * 0.9 * heat;
    }

    // ---- the shockwave shell in the haze: brightest where seen edge-on ----
    {
        float h = shR * shR - dperp * dperp;
        float cap = sqrt(2.0 * shR * shW);
        float sOut = sqrt(max(h, 0.0));
        float g = h > 0.0 ? min(shR * shW / max(sOut, 1e-3), cap) : cap * exp(-pow((dperp - shR) / shW, 2.0));
        float tn = tc - sOut, tf = tc + sOut;
        float vis = (tn > 0.0 && tn < tSurf ? 1.0 : 0.0) + (tf > 0.0 && tf < tSurf ? 0.7 : 0.0);
        vec3 dn = normalize(ro + rd * max(tn, 0.0) + 1e-4);
        float tex = 0.45 + 0.9 * rcNoise3(dn * 3.5 + vec3(tm * 0.2, tm * 0.15, 0.0));
        col += shCol * shA * g * vis * tex * 0.22;
    }

    // ---- sparks: streaks flung out of the core ----
    // The camera looks at the core, so sparks fly outward from the screen
    // centre. Each layer splits the circle into sectors holding one spark;
    // a pixel checks its own sector and the nearer neighbour.
    float sparkAmt = mix(0.3, 1.6, u.param2);
    float burst = 0.6 + 0.0 * u.onset + 0.0 * u.flux + 0.1 * u.level;
    float coreScr = coreR * fov / camR;
    float pr = length(p);
    float pa = atan2_(p.y, p.x) / 6.2831853 + 0.5;
    vec3 spkA = palRoleTreble(0.1 + 0.15 * u.spectralCentroid, u);
    vec3 spkB = peakWhite(palRolePeak(0.85, u), 0.4);
    vec3 spk = vec3(0.0);
    int nLayer = osSteps(1.0, 3.0, detail);
    for (int L = 0; L < 3; L++) {
        if (L >= nLayer) break;
        float fL = float(L);
        float ns = 28.0 + 12.0 * fL;
        float x = pa * ns + fL * 0.37;
        float k0 = floor(x);
        float k1 = k0 + (fract(x) < 0.5 ? -1.0 : 1.0);
        for (int j = 0; j < 2; j++) {
            float k = mod(j == 0 ? k0 : k1, ns);
            float h = hash21(vec2(k, fL * 17.0 + 3.0));
            float rate = 0.16 + 0.24 * h;
            float lt = tm * rate + hash21(vec2(k * 1.7, fL + 9.0));
            float life = fract(lt);
            float gen = mod(floor(lt), 97.0);
            float hg = hash21(vec2(k + gen * 3.1, fL * 5.0 + gen));
            // angle within the sector, with a slight magnetic swirl
            float ang = (k + 0.2 + 0.6 * hg - fL * 0.37 + 0.35 * life * (hg - 0.5)) / ns;
            ang = (ang - 0.5) * 6.2831853;
            vec2 dir = vec2(cos(ang), sin(ang));
            float reach = 0.35 + 1.3 * h * h;
            float rr = coreScr * (1.0 + 0.2 * hg) + life * reach;
            float len = (0.03 + 0.10 * h) * (1.0 - 0.5 * life) * (0.7 + 0.6 * burst) ;
            vec2 q = p - dir * rr;
            float al = dot(q, dir);
            float pe = dot(q, vec2(-dir.y, dir.x));
            float along = saturate(-al / max(len, 1e-3));
            float body = step(al, 0.004) * (1.0 - along);
            float w = 0.0016 + 0.0018 * (1.0 - life);
            float s1 = exp(-pe * pe / (w * w)) * body * body;
            float head = 1.2e-5 / (dot(q, q) + 1.2e-5);
            float fade = (1.0 - life) * smoothstep(0.0, 0.04, life) * step(hg, 0.2 + 0.4 * saturate(burst));
            spk += mix(spkA, spkB, hg) * (s1 * 1.4 + head) * fade;
        }
    }
    // sparks behind a ring in front of the core are hidden
    float occl = tHit < camR - 0.5 ? 0.25 : 1.0;
    col += spk * burst * sparkAmt * 1.6 * occl;

    col = accentize(col, u.accent, 0.10);
    return col; // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
