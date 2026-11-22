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
// Vinyl.
// A macro product shot of a spinning record. A low, tilted camera drifts over
// the grooves; two lights rake slowly across them and the grooves throw long
// anisotropic sheen streaks that break into the spectrum along the radius
// (outer grooves low, inner high). The softbox swells a little every eight
// beats, a faint teal wave runs out through the grooves on each beat, the label
// turns, the stylus glows softly with the beat, and the studio behind melts
// into bokeh. Camera and lights move on a slow, steady clock; the level, not
// the kick, sets the light, gently, and audio never moves the camera.
// Its colours come from its own palette, unnudged by beat or centroid, so the host's vivid
// palette tour never swells the light across the frame.
// param0 = Groove Count ; param1 = Spin ; param2 = Needle

// The palette read: the visual's own palette with no beat or spectral nudge.
// The vivid tour swaps whole palettes of very different brightness every few
// seconds, which swells the light across the frame, so this visual keeps its
// own colours there (as the calm visuals do); a host's manifest palette still
// replaces them. The fog and peak roles read the same steady palette.
vec3 vyPalSteady(float t, VisualUniforms u) { return osPalCalm(t, 30, u); }
vec3 vyPalSteadyFog(float t, VisualUniforms u) {
    vec3 c = vyPalSteady(t, u);
    return mix(vec3(luma(c) * 0.62), c, 0.42);
}

const float VY_LABEL = 0.34;
const float VY_PLATTER = 1.035;
const float VY_PLINTH = -0.075;

// The palette read once at five fixed stops and blended locally: a palVisual
// call at a per-pixel position is expensive, and this one is shared by every
// surface below.
struct VyPal { vec3 a; vec3 b; vec3 c; vec3 d; vec3 e; };

vec3 vyPal(VyPal P, float t) {
    float x = clamp(t, 0.1, 0.9) * 5.0 - 0.5;
    vec3 c = mix(P.a, P.b, smoothstep(0.0, 1.0, x));
    c = mix(c, P.c, smoothstep(1.0, 2.0, x));
    c = mix(c, P.d, smoothstep(2.0, 3.0, x));
    return mix(c, P.e, smoothstep(3.0, 4.0, x));
}

// Closest approach of a ray to a segment: (distance, ray t).
vec2 vySegRay(vec3 ro, vec3 rd, vec3 a, vec3 b) {
    vec3 ba = b - a;
    vec3 w = ro - a;
    float bb = max(dot(ba, ba), 1e-6);
    float br = dot(ba, rd);
    float bo = dot(ba, w);
    float rw = dot(rd, w);
    float s = saturate((bo - rw * br) / max(bb - br * br, 1e-6));
    float t = max(s * br - rw, 0.0);
    return vec2(length(w + t * rd - s * ba), t);
}

// Per-channel complement: flips the hue, keeps lightness and saturation.
vec3 vyComp(vec3 c) {
    float hi = max(c.r, max(c.g, c.b)), lo = min(c.r, min(c.g, c.b));
    return vec3(hi + lo) - c;
}

float vyCapsule(vec3 ro, vec3 rd, vec3 pa, vec3 pb, float ra) {
    vec3 ba = pb - pa;
    vec3 oa = ro - pa;
    float baba = dot(ba, ba);
    float bard = dot(ba, rd);
    float baoa = dot(ba, oa);
    float rdoa = dot(rd, oa);
    float oaoa = dot(oa, oa);
    float a = max(baba - bard * bard, 1e-6);
    float b = baba * rdoa - baoa * bard;
    float c = baba * oaoa - baoa * baoa - ra * ra * baba;
    float h = b * b - a * c;
    if (h < 0.0) return -1.0;
    float t = (-b - sqrt(h)) / a;
    float y = baoa + t * bard;
    if (y > 0.0 && y < baba) return t;
    vec3 oc = y <= 0.0 ? oa : ro - pb;
    b = dot(rd, oc);
    c = dot(oc, oc) - ra * ra;
    h = b * b - c;
    return h > 0.0 ? -b - sqrt(h) : -1.0;
}

vec3 vyCapNormal(vec3 pos, vec3 a, vec3 b, float r) {
    vec3 ba = b - a;
    vec3 pa = pos - a;
    float h = saturate(dot(pa, ba) / max(dot(ba, ba), 1e-6));
    return (pa - h * ba) / r;
}

// Studio behind the record: a dark room melted into bokeh, plus the softbox.
vec3 vyStudio(vec3 rd, VisualUniforms u, float sweep, vec3 teal, VyPal P, vec3 fog, bool soft) {
    vec3 col = fog * 0.012 * (1.0 - abs(rd.y));
    // a warm back glow low behind the record, breathing with the mids
    col += vyPal(P, 0.35) * exp(-abs(rd.y - 0.1) * 5.0) * (0.014 + 0.01 * u.level);
    // softbox overhead: a large soft rectangle the record mirrors
    if (rd.y > 0.05) {
        vec2 q = rd.xz / rd.y;
        vec2 b = abs(q - vec2(-0.6, 0.9)) - vec2(0.7, 0.18);
        float box = 1.0 - smoothstep(-0.05, 0.12, max(b.x, b.y));
        col += vec3(1.0, 0.92, 0.85) * box * (0.3 + 0.12 * sweep);
    }
    float az = atan2_(rd.x, rd.z) / 6.2831853 + 0.5;
    float el = rd.y;
    float band0 = smoothstep(-0.25, 0.05, el) * (1.0 - smoothstep(0.4, 0.7, el));
    if (soft) {
        // curved chrome would alias the discs: reflect their blurred average
        float w = 0.5 + 0.5 * sin(az * 37.699112);
        return col + mix(vyPal(P, 0.3), teal * 0.6, w) * band0 * (0.05 + 0.04 * u.level);
    }
    // bokeh: hashed discs on a direction grid, each lit by one band
    vec2 g = vec2(az * 30.0, el * 9.5);
    vec2 cell = floor(g);
    vec2 f = fract(g);
    for (int j = 0; j < 4; j++) {
        vec2 o = vec2(float(j - (j / 2) * 2), float(j / 2));
        vec2 id = cell + step(0.5, f) - 1.0 + o;
        vec2 cid = vec2(mod(id.x, 30.0), id.y);
        vec2 hh = hash22(cid + 17.0);
        vec2 centre = id + vec2(0.25, 0.3) + vec2(0.5, 0.4) * hh;
        vec2 d = (g - centre) * vec2(1.0, 0.5);
        float rad = 0.1 + 0.1 * hash21(cid + 3.0);
        float dd = length(d);
        float disc = 1.0 - smoothstep(rad - 0.025, rad, dd);
        float ring = exp(-(dd - rad + 0.015) * (dd - rad + 0.015) * 3000.0);
        float lvl = mix(u.level, bandAt(u, hash21(cid + 9.0)), 0.1);
        float on = step(0.25, hash21(cid + 5.0)) * band0;
        vec3 c = hh.y > 0.62 ? teal : vyPal(P, 0.2 + 0.7 * hh.x);
        // a lens-like disc: brighter rim, a faint cool fringe outside it
        float fringe = exp(-(dd - rad - 0.01) * (dd - rad - 0.01) * 2500.0);
        col += (c * (disc * 0.22 + ring * 0.4) + teal * fringe * 0.08) * on * (0.5 + 0.6 * lvl);
    }
    return col;
}

vec3 osBody(vec2 uv, VisualUniforms u) {
    vec2 p = centered(uv, u.resolution);
    p.y = -p.y;
    float detail = osDetail(u);

    float grooves = mix(80.0, 220.0, u.param0);   // visible groove rings per unit radius
    float spinRate = mix(0.25, 1.1, u.param1);    // radians per second
    float needle = mix(0.3, 1.4, u.param2);
    float tm = mod(u.time, 600.0);
    float spin = mod(u.time * spinRate, 6.2831853);

    VyPal P = VyPal(vyPalSteady(0.1, u), vyPalSteady(0.3, u), vyPalSteady(0.5, u), vyPalSteady(0.7, u), vyPalSteady(0.9, u));
    vec3 fog = vyPalSteadyFog(0.15, u);
    vec3 peak = vyPalSteady(0.2, u);
    vec3 trace = vyPalSteady(0.25, u);
    // small highlights (stylus, strobe dots, label ring) pulse on a smooth beat swell
    float bp = saturate(u.beatPhase);
    float bSw = smoothstep(0.0, 0.3, bp) * (1.0 - bp) * (1.0 - bp) * 2.0;
    float pulse = 0.4 * u.level + 0.15 * bSw;

    // ---- camera: a low macro drift on a slow, steady clock ----
    float fl = mod(u.time, 36000.0) * 0.8;
    float s1 = sin(fl * 0.021);
    float s2 = sin(fl * 0.013 + 1.3);
    float phi = -2.35 + 0.3 * s1;
    float dist = 1.08 + 0.14 * s2;
    float camH = 0.2 + 0.05 * sin(u.time * 0.053);
    vec3 ro = vec3(sin(phi) * dist, camH, cos(phi) * dist);
    vec3 ta = vec3(sin(phi) * 0.22, -0.02, cos(phi) * 0.22) + vec3(0.08 * s2, 0.0, 0.06 * s1);
    float roll = 0.14 + 0.04 * sin(mod(u.time, 628.31853) * 0.05);
    vec3 rd = osCamera(ro, ta, roll, 2.3, p);
    float focus = length(ta - ro);
    float pixA = 2.0 / (max(u.resolution.y, 1.0) * 2.3);   // radians per pixel

    // ---- lights: a key that drifts, a coloured rim, the orbiting spot and a front fill ----
    float ka = 0.4 + 0.9 * sin(tm * 0.0104720);
    vec3 teal = vyComp(P.b) * 1.5;
    // a teal spot orbits the deck so its streak sweeps round the disc
    float oa = mod(tm * 0.0209440, 6.2831853);
    vec3 Lo = normalize(vec3(cos(oa), 0.4, sin(oa)));
    vec3 Lk = normalize(vec3(cos(ka), 0.55, sin(ka)));
    vec3 Lr = normalize(vec3(-sin(phi) * 0.9 + 0.3, 0.22, -cos(phi) * 0.9));
    // a fill from behind the camera's left shoulder lays a streak over the near grooves
    vec3 fw = normalize(vec3(ta.x - ro.x, 0.0, ta.z - ro.z));
    vec3 lf = vec3(fw.z, 0.0, -fw.x);
    float fa = 0.35 * sin(tm * 0.07 + 1.0);
    vec3 Lf = normalize(-fw * 0.55 + lf * (0.9 + fa) + vec3(0.0, 0.5, 0.0));
    vec3 fillC = mix(P.c, P.e, 0.5 + 0.5 * sin(tm * 0.05)) * 1.1;
    float sb = (u.beatCount + saturate(u.beatPhase)) / 8.0;
    float sweepT = fract(sb);
    float sweepOn = smoothstep(0.0, 0.15, sweepT) * (1.0 - smoothstep(0.75, 1.0, sweepT));
    float level = 0.8 + 0.3 * u.level;

    // ---- tonearm geometry ----
    vec3 sty = vec3(0.5805, 0.012, 0.2176);
    vec3 piv = vec3(0.6505, 0.13, 1.0748);
    vec3 postA = vec3(piv.x, VY_PLINTH, piv.z);
    vec3 armDir = normalize(piv - sty);
    vec3 headA = sty + vec3(0.0, 0.025, 0.0);
    vec3 headB = headA + armDir * 0.16 + vec3(0.0, 0.012, 0.0);
    vec3 cwA = piv + armDir * 0.08;
    vec3 cwB = piv + armDir * 0.26;

    // exact hits for shading, closest approach for an antialiased silhouette
    float tArm = 1e3;
    vec3 armN = vec3(0.0, 1.0, 0.0);
    float armA = 0.0;
    {
        vec2 c1 = vySegRay(ro, rd, headB, piv);
        vec2 c2 = vySegRay(ro, rd, headA, headB);
        vec2 c3 = vySegRay(ro, rd, cwA, cwB);
        vec2 c4 = vySegRay(ro, rd, postA, piv);
        float e1 = (c1.x - 0.021) / max(c1.y * pixA, 1e-5);
        float e2 = (c2.x - 0.03) / max(c2.y * pixA, 1e-5);
        float e3 = (c3.x - 0.075) / max(c3.y * pixA, 1e-5);
        float e4 = (c4.x - 0.045) / max(c4.y * pixA, 1e-5);
        float em = min(min(e1, e2), min(e3, e4));
        armA = saturate(0.5 - em);
        if (armA > 0.0) {
            float t1 = vyCapsule(ro, rd, headB, piv, 0.021);
            if (t1 > 0.0) { tArm = t1; armN = vyCapNormal(ro + rd * t1, headB, piv, 0.021); }
            float t2 = vyCapsule(ro, rd, headA, headB, 0.03);
            if (t2 > 0.0 && t2 < tArm) { tArm = t2; armN = vyCapNormal(ro + rd * t2, headA, headB, 0.03); }
            float t3 = vyCapsule(ro, rd, cwA, cwB, 0.075);
            if (t3 > 0.0 && t3 < tArm) { tArm = t3; armN = vyCapNormal(ro + rd * t3, cwA, cwB, 0.075); }
            float t4 = vyCapsule(ro, rd, postA, piv, 0.045);
            if (t4 > 0.0 && t4 < tArm) { tArm = t4; armN = vyCapNormal(ro + rd * t4, postA, piv, 0.045); }
            if (tArm > 1e2) {
                // a silhouette pixel the ray just misses: shade it with the nearest rim normal
                vec2 cb = em == e1 ? c1 : em == e2 ? c2 : em == e3 ? c3 : c4;
                vec3 sa = em == e1 ? headB : em == e2 ? headA : em == e3 ? cwA : postA;
                vec3 sbb = em == e1 ? piv : em == e2 ? headB : em == e3 ? cwB : piv;
                vec3 rp = ro + rd * cb.y;
                vec3 ba = sbb - sa;
                float hh = saturate(dot(rp - sa, ba) / max(dot(ba, ba), 1e-6));
                vec3 nn = rp - (sa + ba * hh);
                armN = dot(nn, nn) > 1e-10 ? nn : -rd;
                tArm = cb.y;
            }
        }
    }

    vec3 col;
    float tRec = rd.y < -1e-4 ? -ro.y / rd.y : -1.0;
    vec3 hp = ro + rd * max(tRec, 0.0);
    float rr = length(hp.xz);
    bool onRecord = tRec > 0.0 && rr < 1.0;
    float tHit = 1e3;

    if (onRecord) {
        tHit = tRec;
        vec2 q = hp.xz;
        float r = max(rr, 1e-3);
        vec3 radial = vec3(q.x, 0.0, q.y) / r;
        vec3 T = vec3(-radial.z, 0.0, radial.x);
        vec3 V = -rd;
        float ang = atan2_(q.y, q.x) - spin;
        vec2 cs = vec2(cos(ang), sin(ang));
        // defocus away from the focal plane softens the groove detail
        float blur = saturate(abs(tRec - focus) * 1.2);
        float pix = tRec * 2.5 / max(u.resolution.y, 1.0) / max(-rd.y, 0.08);

        if (r < VY_LABEL) {
            // ---- label: matte printed paper, turning ----
            vec3 lc = mix(P.a, P.b, 0.1 + 0.25 * sin(tm * 0.11));
            float printRing = smoothstep(0.004 + pix, 0.0, abs(r - 0.29)) + smoothstep(0.003 + pix, 0.0, abs(r - 0.2));
            float arcs = step(0.5, fract(ang * 1.2732395)) * smoothstep(0.004, 0.0, abs(r - 0.245) - 0.018);
            float blocks = step(0.62, vnoise(vec2(cs.x * 9.0 + cs.y * 3.0, r * 60.0)))
                         * smoothstep(0.004, 0.0, abs(r - 0.12) - 0.035);
            float diff = 0.25 + 0.75 * saturate(dot(Lk, vec3(0.0, 1.0, 0.0)));
            col = lc * diff * 0.35 * level;
            col += peakWhite(lc, 0.5) * (printRing + arcs * 0.6 + blocks * 0.5) * 0.25 * level;
            // the printed ring lights up with the bass, a teal accent on the label
            col += teal * smoothstep(0.004 + pix, 0.0, abs(r - 0.29)) * (0.1 + 0.9 * pulse) * 0.5;
            float sheenL = pow(saturate(dot(reflect(-Lk, vec3(0.0, 1.0, 0.0)), V)), 12.0);
            col += vec3(0.9, 0.85, 0.8) * sheenL * 0.15;
            // spindle
            float sp = smoothstep(0.03 + pix, 0.022, r);
            col = mix(col, vec3(0.05) + vec3(1.6, 1.5, 1.4) * pow(saturate(1.0 - r / 0.03), 3.0) * (0.4 + 0.5 * bSw), sp);
        } else {
            // ---- grooved vinyl ----
            float fBin = saturate((1.0 - r) / (1.0 - VY_LABEL));     // outer = low
            float lvl = saturate(mix(u.level * (1.0 - 0.2 * fBin), bandAt(u, fBin * 0.9), 0.1));
            // tracks separated by polish gaps; loudness bands give each track its sheen
            float tr = (r - VY_LABEL) / (0.985 - VY_LABEL) * 6.0;
            float gapD = abs(fract(tr) - 0.5) * 2.0;
            float gap = smoothstep(0.985, 0.995, gapD) * step(r, 0.985);
            float leadIn = smoothstep(0.985, 0.99, r);
            float polish = max(gap, leadIn);
            float loud = 0.88 + 0.12 * vnoise(vec2(r * 120.0 + cs.x * 4.0, cs.y * 4.0 + 3.0));
            // groove rings, filtered so they fade before aliasing
            float gp = r * grooves;
            float gw = grooves * pix;
            float gl = 0.5 + 0.5 * cos(gp * 6.2831853);
            float ringC = (1.0 - saturate(gw * 1.5)) * (1.0 - 0.7 * blur);
            float micro = mix(0.5, gl, ringC);

            // anisotropic sheen along the groove tangent
            float expo = mix(900.0, 260.0, blur);
            float warp = 0.15 * (0.035 * cs.y + 0.015 * (cs.x * cs.x - cs.y * cs.y));
            T = normalize(T + vec3(0.0, warp, 0.0));
            vec3 Hk = normalize(Lk + V);
            vec3 Ho = normalize(Lo + V);
            vec3 Hf = normalize(Lf + V);
            // Divide by the half vector's horizontal length: when a light sits straight
            // behind the record its streak would otherwise widen over the whole disc.
            float to = dot(T, Ho) * 0.7 / max(length(Ho.xz), 0.4);
            float ao = exp(-to * to * expo * 0.4);
            vec3 Hr = normalize(Lr + V);
            float tk = dot(T, Hk) * 0.7 / max(length(Hk.xz), 0.4);
            float trr = dot(T, Hr) * 0.7 / max(length(Hr.xz), 0.4);
            float tf = dot(T, Hf) * 0.7 / max(length(Hf.xz), 0.4);
            float ak = exp(-tk * tk * expo * 0.5);
            float ar = exp(-trr * trr * expo * 0.3);
            float af = exp(-tf * tf * expo * 0.8);
            float akw = exp(-tk * tk * 40.0);   // wide faint halo around the key streak
            float specMul = (1.0 - polish) * loud * (0.78 + 0.22 * micro);
            // grating colour: hue walks with the offset from the streak's core
            vec3 kc = vyPal(P, 0.62 + 0.3 * clamp(tk * 8.0, -1.0, 1.0));
            kc = mix(kc, vec3(1.0, 0.94, 0.88), ak * ak * 0.6);
            vec3 rc = vyPal(P, 0.2 + 0.12 * fBin);
            // the key light swells softly on each beat
            float kick = 1.2;
            col = kc * (ak * 1.2 + akw * 0.06) * specMul * (1.2 + 1.0 * lvl) * level * kick;
            col += rc * ar * specMul * (0.8 + 0.8 * lvl) * level;
            col += teal * ao * specMul * (0.8 + 0.3 * u.level + 0.5 * lvl) * level;
            // the fill streak, with first-order diffraction fringes either side of it
            col += fillC * af * specMul * (0.35 + 0.45 * lvl + 0.2 * u.level) * level;
            float d1 = abs(tf) - 0.075;
            float lobe = exp(-d1 * d1 * mix(5200.0, 1400.0, blur));
            vec3 fringe = vyPal(P, 0.5 + 0.4 * clamp(tf * 9.0, -1.0, 1.0) + 0.15 * fBin);
            col += fringe * lobe * specMul * (0.15 + 0.9 * lvl) * level * kick;

            // mirror reflection of the studio: crisp in the gaps, smeared on the grooves
            vec3 nr = normalize(vec3(0.0, 1.0, 0.0) + radial * (micro - 0.5) * 0.06 * (1.0 - polish)
                              - radial * warp * 0.5);
            vec3 refl = vyStudio(reflect(rd, nr), u, sweepOn, teal, P, fog, detail < 0.5);
            float fres = 0.045 + 0.955 * pow(1.0 - saturate(-rd.y), 5.0);
            col += refl * fres * mix(0.12, 0.6, polish);
            col += vec3(0.0025, 0.002, 0.0025);

            // luminous grooves: the spectrum glows in the dark between streaks
            float glowRing = pow(gl, 8.0) * ringC + 0.08;
            col += vyPal(P, 0.08 + 0.3 * fBin + 0.1 * u.spectralCentroid)
                 * glowRing * lvl * lvl * 0.43 * (1.0 - polish);

            // each beat a faint teal wave runs out through the grooves from the label,
            // brightest as it leaves
            float wr = VY_LABEL + (1.0 - VY_LABEL) * (1.0 - (1.0 - bp) * (1.0 - bp));
            float wv = exp(-(r - wr) * (r - wr) * 900.0) * (1.0 - bp) * 0.12;
            col += teal * wv * (0.35 + 0.65 * micro) * (0.3 + 0.35 * u.level) * (1.0 - polish);

            // playing groove: the stylus line glows on the beat
            float playR = length(sty.xz);
            float hot = exp(-abs(r - playR) * 400.0);
            // brightest just behind the stylus, fading round the turn
            float behind = fract((atan2_(q.y, q.x) - atan2_(sty.z, sty.x)) / 6.2831853 + 1.0);
            hot *= 0.15 + 0.85 * exp(-behind * 5.0);
            col += peak * hot * (0.25 + 0.8 * pulse) * needle;
        }
    } else {
        // ---- platter lip, strobe dots, plinth and the studio ----
        vec3 bg = vyStudio(rd, u, sweepOn, teal, P, fog, false);
        col = bg;
        float tP = rd.y < -1e-4 ? (VY_PLINTH - ro.y) / rd.y : -1.0;
        // platter side: a cylinder of strobe dots under the record
        float a2 = max(dot(rd.xz, rd.xz), 1e-6);
        float b2 = dot(ro.xz, rd.xz);
        float c2 = dot(ro.xz, ro.xz) - VY_PLATTER * VY_PLATTER;
        float h2 = b2 * b2 - a2 * c2;
        bool side = false;
        if (h2 > 0.0) {
            float tc = (-b2 - sqrt(h2)) / a2;
            vec3 cp = ro + rd * tc;
            if (tc > 0.0 && cp.y < 0.0 && cp.y > VY_PLINTH + 0.01) {
                side = true;
                tHit = tc;
                float ca = atan2_(cp.z, cp.x) - spin;
                float dotsA = fract(ca / 6.2831853 * 90.0) - 0.5;
                float row = abs(cp.y + 0.03) - 0.008;
                float dots = smoothstep(0.25, 0.1, abs(dotsA)) * smoothstep(0.004, 0.0, row);
                vec3 cn = vec3(cp.x, 0.0, cp.z) / VY_PLATTER;
                float lit = 0.1 + 0.9 * saturate(dot(cn, Lk));
                col = vec3(0.03, 0.03, 0.035) * lit + trace * dots * (1.2 + 1.0 * pulse);
                col += vyStudio(reflect(rd, cn), u, sweepOn, teal, P, fog, true) * 0.3;
            }
        }
        if (!side && tRec > 0.0 && rr < VY_PLATTER) {
            // platter lip around the record edge
            tHit = tRec;
            col = vec3(0.04) + vec3(0.9, 0.85, 0.8) * pow(saturate(dot(reflect(-Lk, vec3(0.0, 1.0, 0.0)), -rd)), 8.0) * 0.3;
        } else if (!side && tP > 0.0) {
            // glossy black plinth
            tHit = tP;
            vec3 pp = ro + rd * tP;
            vec3 pr = reflect(rd, vec3(0.0, 1.0, 0.0));
            float fres = 0.04 + 0.96 * pow(1.0 - saturate(-rd.y), 5.0);
            float pd = length(pp.xz);
            col = vyStudio(pr, u, sweepOn, teal, P, fog, detail < 0.5) * fres * 0.35 * exp(-pd * 0.6) + vec3(0.003);
            col = mix(col, bg, smoothstep(1.5, 5.0, pd));
            // the platter's glow spills onto the plinth
            float pl = length(pp.xz) - VY_PLATTER;
            col += trace * exp(-max(pl, 0.0) * 12.0) * 0.05 * (1.4 + 0.6 * u.level);
        }
    }

    // ---- chrome tonearm, composited over with its antialiased coverage ----
    if (armA > 0.0) {
        vec3 n = normalize(armN);
        vec3 refl = vyStudio(reflect(rd, n), u, sweepOn, teal, P, fog, true);
        float fres = 0.5 + 0.5 * pow(1.0 - saturate(-dot(rd, n)), 3.0);
        float spk = pow(saturate(dot(reflect(-Lk, n), -rd)), 60.0);
        float spr = pow(saturate(dot(reflect(-Lr, n), -rd)), 30.0);
        float spf = pow(saturate(dot(reflect(-Lf, n), -rd)), 40.0);
        vec3 ac = refl * fres * 0.9 + vec3(1.4, 1.35, 1.3) * spk * 2.0 + P.b * spr * 1.5 + fillC * spf * 1.2;
        ac += vec3(0.01);
        col = mix(col, ac, armA);
        tHit = min(tHit, tArm);
    }

    // a soft shaft of the softbox's light through the haze, dust lit inside it
    vec2 sh = vySegRay(ro, rd, vec3(-0.9, 1.7, 1.25), vec3(0.15, 0.0, 0.1));
    float shW = 0.16 + 0.1 * (1.0 - saturate(sh.y));
    float shaft = exp(-sh.x * sh.x / (shW * shW)) * smoothstep(0.0, 0.3, tHit - sh.y + 0.3);
    col += mix(vec3(1.0, 0.9, 0.8), P.c, 0.35) * shaft * (0.03 + 0.015 * u.level + 0.015 * sweepOn);

    // stylus spark on the beat, a small hot glint at the contact point
    vec3 so = sty - ro;
    float sa = dot(so, rd);
    if (sa > 0.0) {
        float sd2 = max(dot(so, so) - sa * sa, 0.0) / (sa * sa);
        float spark = 3e-6 / (sd2 + 3e-6) + 0.02 * exp(-sd2 * 4000.0);
        col += vyPalSteady(0.1, u) * spark * needle * (0.3 + 0.8 * pulse);
    }

    // dust motes drifting through the key light
    for (int i = 0; i < 10; i++) {
        float fi = float(i);
        vec2 h = hash22(vec2(fi, 2.7));
        vec3 mp = vec3((h.x - 0.5) * 1.6, 0.05 + 0.35 * fract(h.y + tm * 0.01 * (1.0 + h.x)), (h.y - 0.5) * 1.6);
        mp.xz += 0.05 * vec2(sin(tm * 0.3 + fi), cos(tm * 0.23 + fi * 1.7));
        vec3 mo = mp - ro;
        float ma = dot(mo, rd);
        if (ma <= 0.0 || ma > tHit) continue;
        float md2 = max(dot(mo, mo) - ma * ma, 0.0) / (ma * ma);
        float m = 1.5e-6 / (md2 + 1.5e-6);
        col += vec3(1.0, 0.9, 0.8) * m * (0.2 + 0.2 * u.level);
    }

    return col; // LINEAR HDR — post chain tonemaps
}

// Reds stay off saturated red through the vivid present (common.glsl).
vec3 visual(vec2 uv, VisualUniforms u) { return osOffRed(osBody(uv, u)); }

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
