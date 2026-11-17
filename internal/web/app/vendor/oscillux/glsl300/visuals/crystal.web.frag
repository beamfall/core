#version 300 es
precision highp float;
precision highp int;
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
    float nudge = u.beatPhase * 0.015 + u.spectralCentroid * 0.08;
    return mix(palByIndex(t + nudge, a), palByIndex(t + nudge, b), blend);
}

vec3 peakWhite(vec3 col, float peak) {
    return mix(col, vec3(2.6, 2.35, 2.10), saturate(peak));
}

vec3 palRoleTrace(float t, VisualUniforms u) {
    return palVisual(t + 0.03 * u.beatPhase, u);
}

vec3 palRoleBass(float t, VisualUniforms u) {
    vec3 hot = palVisual(0.03 + t * 0.18 + 0.04 * u.beatPhase, u);
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

vec2 centered(vec2 uv, vec2 res) {
    vec2 p = uv * 2.0 - 1.0;
    p.x *= res.x / max(res.y, 1.0);
    return p;
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

    return p;
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

uniform VisualUniforms u;
vec3 feedbackTrail(vec3 col, vec2 uv, float decay, float zoom, float rotAmt) {
    vec2 q = uv - 0.5;
    q = rot2(rotAmt) * q;
    q *= (1.0 - zoom);
    vec3 p = suppressGreenCast(PREV(q + 0.5).rgb);
    float k = pow(clamp(decay, 0.0, 0.90), u.dt > 0.0 ? u.dt * 60.0 : 1.0);
    return suppressGreenCast(max(suppressGreenCast(col), p * k));
}
// SPDX-License-Identifier: AGPL-3.0-only
// Crystal / frequency + beat + onset.
// A faceted prism refracting a procedural neon event-horizon environment.
// param0 = Facet Count ; param1 = Dispersion ; param2 = Fracture

// ---------------------------------------------------------------------------
// SDF: faceted crystal
// ---------------------------------------------------------------------------
float sdOctahedron(vec3 p, float s) {
    vec3 q = abs(p);
    return (q.x + q.y + q.z - s) * 0.57735027;   // 1/sqrt(3)
}

float sdPlane(vec3 p, vec3 n, float d) {
    return dot(p, n) - d;
}

float crystalSDF(vec3 p, float facetSharp, float time) {
    float d = sdOctahedron(p, 0.80);
    float sphere = length(p) - 0.90;
    d = max(d, sphere);

    int N = int(clamp(facetSharp, 2.0, 12.0));
    float angleStep = 6.28318530 / float(N);

    for (int i = 0; i < 12; ++i) {
        if (i >= N) break;
        float a = float(i) * angleStep + time * 0.07;
        vec3 n1 = normalize(vec3(cos(a), 0.55, sin(a)));
        d = max(d, sdPlane(p, n1, 0.62));
    }
    for (int i = 0; i < 12; ++i) {
        if (i >= N) break;
        float a = float(i) * angleStep + angleStep * 0.5 + time * 0.07;
        vec3 n2 = normalize(vec3(cos(a), -0.55, sin(a)));
        d = max(d, sdPlane(p, n2, 0.62));
    }
    return d;
}

// ---------------------------------------------------------------------------
// Normal — tetrahedron method
// ---------------------------------------------------------------------------
vec3 crystalNormal(vec3 p, float facetSharp, float time) {
    const vec2 e = vec2(0.0015, -0.0015);
    vec3 n =
        e.xyy * crystalSDF(p + e.xyy, facetSharp, time) +
        e.yyx * crystalSDF(p + e.yyx, facetSharp, time) +
        e.yxy * crystalSDF(p + e.yxy, facetSharp, time) +
        e.xxx * crystalSDF(p + e.xxx, facetSharp, time);
    float len = length(n);
    return len > 1e-6 ? n / len : vec3(0.0, 1.0, 0.0);
}

// ---------------------------------------------------------------------------
// Procedural neon environment — polar spectral rings.
// No uImage dependency: gem looks premium even with no artwork loaded.
// ---------------------------------------------------------------------------
vec3 procEnv(vec3 dir, float time, float spectralCentroid) {
    // Spherical coords of the direction
    float phi   = atan2_(dir.z, dir.x);            // -pi..pi azimuth
    float theta = asin(clamp(dir.y, -1.0, 1.0));   // -pi/2..pi/2 elevation
    float su = phi / 6.28318530 + 0.5;             // 0..1
    float sv = theta / 3.14159265 + 0.5;           // 0..1

    vec3 env = vec3(0.0);

    // Several neon polar bands (ring layers at discrete elevation bands)
    for (int b = 0; b < 6; ++b) {
        float fb = float(b);
        float bandCenter = 0.15 + fb * 0.12;
        float bandV      = abs(sv - bandCenter);
        float ringMask   = 1.0 - smoothstep(0.012, 0.055, bandV);

        // Hue rotates around azimuth; animated slowly
        float t  = su + fb * 0.17 + time * (0.02 + fb * 0.007);
        vec3 c = palCity(t + 0.12 * spectralCentroid);
        c = c * c * 2.6; // deepen saturation — prismatic, never milky

        // Brightness varies by azimuth band for a spectrum effect
        float azBand = fract(su * 8.0 + fb * 0.3);
        float bright = 0.6 + 0.6 * sin(azBand * 6.28318530);

        env += c * ringMask * bright * 1.6;
    }

    // Dark baseline — the gem floats in black
    return env;
}

// ---------------------------------------------------------------------------
// Chromatic dispersion refraction (3 separate IOR channels → prism effect)
// ---------------------------------------------------------------------------
vec3 envRefractChromatic(vec3 rd, vec3 n, float iorBase, float dispersion,
                         float time, float spectralCentroid) {
    float dsp = dispersion * 0.30;
    float iorR = iorBase - dsp;
    float iorG = iorBase;
    float iorB = iorBase + dsp;

    vec3 rr = refract(rd, n, 1.0 / iorR);
    vec3 rg = refract(rd, n, 1.0 / iorG);
    vec3 rb = refract(rd, n, 1.0 / iorB);

    // Total internal reflection fallback
    if (length(rr) < 0.01) rr = reflect(rd, n);
    if (length(rg) < 0.01) rg = reflect(rd, n);
    if (length(rb) < 0.01) rb = reflect(rd, n);

    return vec3(
        procEnv(rr, time, spectralCentroid).r,
        procEnv(rg, time, spectralCentroid).g,
        procEnv(rb, time, spectralCentroid).b
    );
}

// ---------------------------------------------------------------------------
// Crack-flash pattern: high-freq sinusoidal thresholded to thin lines.
// Shard count driven by treble / onset — more treble = more fracture lines.
// ---------------------------------------------------------------------------
float crackPattern(vec3 p, float shardFreq) {
    float v = 0.0;
    v += abs(sin(p.x * 14.3 + p.y * 9.7));
    v += abs(sin(p.y * 11.1 + p.z * 17.3));
    v += abs(sin(p.z * 13.7 + p.x * 8.1));
    // Second harmonic for denser shards at high treble
    v += 0.4 * abs(sin(p.x * shardFreq + p.z * 21.1));
    v /= 3.4;
    return 1.0 - smoothstep(0.0, 0.20, v);
}

vec3 visual(vec2 uv, VisualUniforms u) {
    // ------------------------------------------------------------------
    // Params
    // ------------------------------------------------------------------
    float facetSharp  = mix(2.0, 12.0, u.param0);          // cutting planes
    float dispersion  = mix(0.05, 1.0, u.param1);          // IOR spread
    float fracturePow = u.param2;                          // crack intensity

    // Bass drives gem scale and camera push
    float gemScale    = 1.0 + 0.18 * u.bassImpact;
    float camPush     = 0.22 * u.bassImpact;

    // Mid drives internal wobble on the SDF cutplanes (via time offset)
    float wobbleTime  = u.time + u.mid * 0.8;

    // Treble + onset -> fracture brightness and shard count
    float shardFreq   = mix(14.0, 34.0, u.treble);         // more treble = denser cracks
    float transient   = saturate(u.onset * 3.0 + u.beat * 0.5);
    float fractureAmt = fracturePow * transient;

    // beatPhase -> slow base rotation on top of the normal yaw
    float beatRot     = u.beatPhase * 0.4;

    // spectralCentroid -> tint hue bias
    float tintT       = u.spectralCentroid;

    // ------------------------------------------------------------------
    // Ray setup — orbiting camera
    // ------------------------------------------------------------------
    vec2 sp = motionCentered(uv, u);
    vec3 ro = vec3(0.0, 0.0, 2.75 - camPush);
    vec3 rd = normalize(vec3(sp, -1.6));

    float yaw   = u.time * 0.18 + u.bass * 0.4 + beatRot;
    float pitch = u.time * 0.09 + u.mid  * 0.2;
    mat2 Ry = rot2(yaw);
    mat2 Rx = rot2(pitch);
    ro.xz = Ry * ro.xz;
    ro.yz = Rx * ro.yz;
    rd.xz = Ry * rd.xz;
    rd.yz = Rx * rd.yz;

    // ------------------------------------------------------------------
    // Raymarch (scale gem with bassImpact)
    // ------------------------------------------------------------------
    float t   = 0.0;
    float hit = 0.0;
    const float TMAX = 12.0;
    const float EPS  = 0.001;

    for (int i = 0; i < 72; ++i) {
        vec3 p = ro + rd * t;
        // Scale the SDF query point inversely -> gem grows on bassImpact
        float d = crystalSDF(p / gemScale, facetSharp, wobbleTime) * gemScale;
        if (d < EPS) { hit = 1.0; break; }
        t += d * 0.85;
        if (t > TMAX) break;
    }

    // ------------------------------------------------------------------
    // Background — deep black with faint accent vignette
    // ------------------------------------------------------------------
    float vig = 1.0 - smoothstep(0.5, 1.8, length(sp));
    vec3 col = vec3(0.0) + u.accent.rgb * 0.012 * vig;

    // ------------------------------------------------------------------
    // Gem shading
    // ------------------------------------------------------------------
    if (hit > 0.5) {
        vec3 p = ro + rd * t;
        vec3 pLocal = p / gemScale;
        vec3 n = crystalNormal(pLocal, facetSharp, wobbleTime);

        // Fresnel (Schlick)
        float cosI = max(-dot(rd, n), 0.0);
        float F0   = 0.05;   // crystal base reflectance
        float fres = F0 + (1.0 - F0) * pow(1.0 - cosI, 5.0);

        // --- Reflection: procedural env ---
        vec3 refl    = reflect(rd, n);
        vec3 reflCol = procEnv(refl, u.time, tintT);

        // --- Refraction: chromatic dispersion through procedural env ---
        float ior    = 1.52;   // crystal-ish
        vec3 refrCol = envRefractChromatic(rd, n, ior, dispersion, u.time, tintT);

        // --- Per-facet tint: each facet carries its own gem color, so the
        // interior refraction is prismatic rather than washed white ---
        vec3 fq = floor(n * 3.5 + 0.5);
        float facetHue = fract(dot(fq, vec3(0.157, 0.311, 0.427)));
        vec3 facetTint = palVisual(facetHue * 0.6 + tintT * 0.3 + 0.05, u);
        facetTint = facetTint * facetTint * 2.2;
        vec3 refrTinted = refrCol * mix(vec3(1.0), facetTint, 0.90) * 1.30;

        // --- Mix refract/reflect by Fresnel (grazing -> more reflect) ---
        vec3 gemCol = mix(refrTinted, reflCol * 0.85, fres);

        // spectralCentroid tint: bias hue toward neon palette
        vec3 tintCol = palVisual(tintT + 0.3, u);
        gemCol = mix(gemCol, gemCol * (0.7 + 0.5 * tintCol), 0.22);

        // Internal bass glow — colored ember in the heart of the gem, not a wash
        float heart = exp(-dot(pLocal, pLocal) * 2.2);
        float glow = (u.bassImpact * 0.55 + u.amplitude * 0.12) * heart;
        glow *= (1.0 - fres);   // strongest on the interior
        vec3 glowCol = palVisual(0.1 + tintT * 0.3, u);
        glowCol = glowCol * glowCol * 2.2;
        gemCol += glowCol * glow * 1.1;

        // Specular glitter: flat facets make dot(refl,L) constant across a
        // facet, so gate the flash with a positional sparkle mask — glitter,
        // never a facet-wide white sheet.
        float spec = pow(max(dot(refl, normalize(vec3(0.4, 0.8, 0.5))), 0.0), 160.0);
        spec += 0.6 * pow(max(dot(refl, normalize(vec3(-0.6, 0.5, 0.3))), 0.0), 120.0);
        float glitter = smoothstep(0.55, 0.92,
            vnoise(pLocal.xy * 26.0 + pLocal.z * 15.0 + u.time * 0.8));
        vec3 specCol = peakWhite(palVisual(facetHue + 0.2, u), 0.45);
        gemCol += specCol * spec * glitter * (2.2 + 2.5 * u.treble);

        // Rim/edge brightening from Fresnel: facet outlines pop
        float rim = pow(1.0 - cosI, 3.5);
        vec3 rimCol = palVisual(tintT + 0.5 + u.beatPhase * 0.2, u);
        gemCol += rimCol * rim * (0.45 + u.bassImpact * 1.1);

        // Beat brightening (overall pulse, kept moderate — bloom does the heavy lift)
        gemCol += u.accent.rgb * u.beat * 0.18;

        // --- Crack-flash on transients ---
        if (fractureAmt > 0.001) {
            float crack = crackPattern(pLocal * (4.0 + u.param0 * 6.0), shardFreq);
            // Crack lines: spectrum-colored shards, 5-8x HDR
            vec3 crackCol = palVisual(tintT + crack * 0.4 + u.onset * 0.2, u);
            crackCol = peakWhite(crackCol, 0.22 * u.onset);
            gemCol += crackCol * crack * fractureAmt * 4.5;
        }

        col = gemCol;
    }

    // ------------------------------------------------------------------
    // Feedback trails — light decay 0.72ish per spec
    // ------------------------------------------------------------------
    // Cap the trail: the gem rotates, and long max-blend trails accumulate
    // ghost facets into a pastel wash that kills the prismatic saturation.
    col = feedbackTrail(col, uv,
                        min(u.trailDecay, 0.55),
                        0.002 + 0.006 * u.bassImpact,     // subtle zoom
                        0.001 * sin(u.time * 0.5));       // very slow swirl

    return col; // LINEAR HDR — post chain tonemaps
}

in vec2 vUV;
layout(location=0) out vec4 fragColor;
void main() {
    fragColor = vec4(suppressGreenCast(visual(vUV, u)), 1.0);
}
