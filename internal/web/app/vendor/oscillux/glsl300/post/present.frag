#version 300 es
precision highp float;
precision highp int;
precision highp sampler2D;
layout(std140) uniform PresentBlock {
    vec4 present0;
    vec4 overlay0;
    vec4 overlay1;
    vec4 overlay2;
    vec4 overlay3;
    vec4 regionRect[8];
    vec4 regionShape[8];
    vec4 post0;
    vec4 post1;
} pu;
uniform sampler2D uA, uB, uH, uBloom;
in vec2 vUV;
layout(location = 0) out vec4 oColor;
layout(location = 1) out vec4 oHist;
#define PRESENT_A(uv) textureLod(uA, uv, 0.0)
#define PRESENT_B(uv) textureLod(uB, uv, 0.0)
#define PRESENT_HIST(px) texelFetch(uH, px, 0)
#define PRESENT_BLOOM(uv) textureLod(uBloom, uv, 0.0)
// SPDX-License-Identifier: AGPL-3.0-only
// The shared present pass (OSC-OVERLAY; v2, CORE-0108). One body for every host:
// tools/shader/present.mjs wraps it as GLSL ES 3.00 for WebGL2 and compiles it through glslang
// and SPIRV-Cross to Metal, and both reference hosts draw it in place of their own present code.
//
// The wrapper declares:
//   vUV                    viewport position, origin top-left, y down
//   PRESENT_A(uv)          the incoming frame (or a transition's blend), linear light
//   PRESENT_B(uv)          the outgoing frame during a switch, linear light
//   PRESENT_HIST(px)       the previous presented frame at integer pixel px (rgb display-linear,
//                          a 1 once written, 0 when there is no history)
//   PRESENT_BLOOM(uv)      the bloom chain's L0 (shaders/present/bloom.glsl), linear light; read
//                          only when post0.x > 0
//   pu                     the PresentBlock: present0 (saturation, eased switch progress or 1,
//                          tone 1 or 0 for the unmapped blend, vibrance), the OverlayUniforms
//                          block (conformance/packing/overlay-uniforms.schema.json) that
//                          oscillux_overlay_pack writes, then post0 (bloom strength, chromatic
//                          aberration, grain, dither amplitude in output codes) and post1 (frame
//                          seed, 0, 0, 0)
//   oColor, oHist          the presented colour and the history the next frame reads
//
// In order, the pass: dissolves a switch in linear light, with red and blue sampled along the
// radius from the centre when aberration is on; blurs blur regions; adds bloom; grades
// (saturation, vibrance); tone maps on luminance, keeping each colour's hue; desaturates
// desaturate regions; damps frame-to-frame change against the previous presented frame
// (everywhere under reduced motion, more inside damp regions); adds film grain (never under
// reduced motion, and never into the history); holds every text-region pixel at or below the
// light-text ceiling or at or above the dark-text floor; encodes with the exact piecewise sRGB
// curve; and puts the value on the output grid: text-region pixels round toward their bound,
// the rest take triangular dither. The contrast step comes after everything that changes
// luminance and before only the encode and that rounding, so the bound holds on the written
// pixel. Policy numbers all come from the packed block.
//
// The flash limiter is not here: it runs after this pass, on its output, and is the last stage
// before the display (present -> limiter -> output; docs/shader-toolchain.md, "Present pass").
//
// Optional host layer (OSCILLUX_PRESENT_LAYER): a host may composite one graphics layer, such
// as karaoke lyrics, over the visual. tools/shader/present.mjs inserts the layer's source at
// the OSCILLUX_PRESENT_LAYER_SOURCE marker below with OSCILLUX_PRESENT_LAYER defined, and
// declares for it:
//   pl.layer[8]            128 bytes of layer parameters, their meaning the layer's own
//                          (PresentLayerBlock: WebGL2 block binding 1, Metal buffer 1)
//   PRESENT_LAYER_TEX(uv)  the layer texture at uv (texture 3, sampler 1, chosen by the host)
//   PRESENT_LAYER_FETCH(px) the layer texture at integer texel px
// The layer defines vec3 oscillux_present_layer(vec2 uv, vec3 c). It runs after the tone map,
// desaturation, damping and grain, on c in display-linear light (the space the contrast step
// measures, before the sRGB encode), and returns display-linear colour in [0, 1]. Text-region
// contrast, the output rounding and alpha apply on top of it. The layer is not damped and does
// not enter the history, which holds the visual alone after the contrast step.

const vec3 PRESENT_LUMA = vec3(0.2126, 0.7152, 0.0722);

float presentLum(vec3 c) { return dot(c, PRESENT_LUMA); }

vec2 presentZoom(vec2 p, float z) { return 0.5 + (p - 0.5) / z; }

// The frame before grading: the incoming frame, or during a switch the dissolve from the
// outgoing one, the incoming visual's bright features and the centre first, with a slow drift
// towards the viewer.
vec3 presentScene(vec2 uv) {
    float e = pu.present0.y;
    if (e >= 1.0) return PRESENT_A(uv).rgb;
    vec3 a = PRESENT_A(presentZoom(uv, 1.0 + 0.05 * (1.0 - e) * (1.0 - e))).rgb;
    vec3 b = PRESENT_B(presentZoom(uv, 1.0 + 0.08 * e)).rgb;
    float la = presentLum(a);
    la /= 1.0 + la;
    return mix(b, a, smoothstep(0.0, 1.0, e * 1.7 - 0.3 + 0.4 * (la - 0.5) - 0.3 * length(uv - 0.5)));
}

// Radial chromatic aberration: red sampled farther out along the radius and blue farther in, by
// k times the squared distance from the centre (1 at the corners), so the split is zero at the
// centre and grows toward the edges.
vec3 presentAberrated(vec2 uv, float aspect) {
    float k = pu.post0.y;
    if (k <= 0.0) return presentScene(uv);
    vec2 d = uv - 0.5;
    vec2 q = d * vec2(aspect, 1.0);
    float r2 = dot(q, q) / (0.25 * (aspect * aspect + 1.0));
    vec2 o = d * (k * r2);
    return vec3(presentScene(uv + o).r, presentScene(uv).g, presentScene(uv - o).b);
}

// The ACES fit (Narkowicz) on one value.
float presentAces(float v) { return clamp((v * (2.51 * v + 0.03)) / (v * (2.43 * v + 0.59) + 0.14), 0.0, 1.0); }

// Tone map on luminance: the colour scaled to the mapped luminance, then, where a channel would
// pass 1, moved toward the grey of that luminance until it fits. Both steps keep the HSV hue of
// the linear colour (v1 mapped each channel, which turned saturated oranges yellow); greys map
// as before.
vec3 presentTonemap(vec3 x) {
    float l = presentLum(x);
    if (l <= 1e-6) return x * (0.03 / 0.14);
    float t = presentAces(l);
    vec3 c = x * (t / l);
    float m = max(c.r, max(c.g, c.b));
    if (m > 1.0) c = vec3(t) + (c - vec3(t)) * ((1.0 - t) / (m - t));
    return clamp(c, 0.0, 1.0);
}

// Integer hash written for Oscillux, the same on every GPU: the library's xorshift-multiply
// mixer (hashMix in shaders/library.glsl, same multipliers and seeds). The three inputs are
// chained through it into one key, and each output word is the key remixed with its own seed.
uint presentMix(uint h) {
    h ^= h >> 16u;
    h *= 0xa7d471dbu;
    h ^= h >> 14u;
    h *= 0x76a997d3u;
    h ^= h >> 16u;
    return h;
}
uvec3 presentHash(uvec3 v) {
    uint k = presentMix(presentMix(presentMix(v.x ^ 0xed81fd3au) ^ v.y) ^ v.z);
    return uvec3(presentMix(k ^ 0x6294a626u), presentMix(k ^ 0xbe633942u), presentMix(k ^ 0xa97a7369u));
}

// The top 24 bits as a float in [0, 1).
float presentUnit(uint h) { return float(h >> 8u) * (1.0 / 16777216.0); }

// One channel on the output grid with dither of post0.w codes: triangular noise tri in the
// interior, rectangular noise rect within one code of either end. A value within 1/1024 code of
// an end is that end: the encode of 1.0 can land a hair under it on a GPU, which rect noise would
// otherwise round down to the code below white.
float presentDither(float v, float levels, float tri, float rect) {
    float q = v * levels;
    if (q < 1.0 / 1024.0 || q > levels - 1.0 / 1024.0) return q < 1.0 ? 0.0 : 1.0;
    float r = q < 1.0 || q > levels - 1.0 ? rect : tri;
    return clamp(floor(q + 0.5 + pu.post0.w * r), 0.0, levels) / levels;
}

// Signed distance from p to region i, in viewport heights (negative inside).
float presentRegionDistance(int i, vec2 p, float aspect) {
    vec4 r = pu.regionRect[i];
    vec2 half_ = 0.5 * vec2(r.z * aspect, r.w);
    vec2 centre = vec2(r.x * aspect, r.y) + half_;
    float radius = min(pu.regionShape[i].x, min(half_.x, half_.y));
    vec2 q = abs(p - centre) - half_ + radius;
    return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0) - radius;
}

bool presentHasMode(float mode, float bit) { return mod(floor(mode / bit), 2.0) >= 1.0; }

// Contrast: the floor first, then the ceiling, which wins where regions of both kinds meet.
vec3 presentContrast(vec3 c, float flo, float cap) {
    float y = presentLum(c);
    if (y < flo && y < 1.0) c += (flo - y) / (1.0 - y) * (1.0 - c);
    y = presentLum(c);
    if (y > cap) c *= cap / y;
    return clamp(c, 0.0, 1.0);
}

// OSCILLUX_PRESENT_LAYER_SOURCE

void main() {
    if (pu.present0.z < 0.5) {
        oColor = vec4(presentScene(vUV), 1.0);
        oHist = vec4(0.0);
        return;
    }
    float aspect = pu.overlay2.w;
    vec2 p = vec2(vUV.x * aspect, vUV.y);
    // A pixel counts as inside a region when any part of it may be: its centre lies within
    // three quarters of a pixel (more than half the diagonal) of the rectangle.
    float dilate = 0.75 / pu.overlay3.y;
    float cap = 1.0, flo = 0.0;
    bool light = false, dark = false;
    float wDesat = 0.0, wBlur = 0.0, blurStrength = 0.0, wMotion = 0.0, wFlash = 0.0;
    int count = int(pu.overlay0.x);
    for (int i = 0; i < 8; i++) {
        if (i >= count) break;
        vec4 shape = pu.regionShape[i];
        float d = presentRegionDistance(i, p, aspect) - dilate;
        if (d > shape.y) continue;
        // 0 inside the rectangle (and its dilation), rising to 1 across the feather.
        float t = shape.y > 0.0 ? clamp(d / shape.y, 0.0, 1.0) : (d > 0.0 ? 1.0 : 0.0);
        float edge = t * t * (3.0 - 2.0 * t);
        float w = shape.z * (1.0 - edge);
        float mode = shape.w;
        if (presentHasMode(mode, 1.0)) {
            cap = min(cap, mix(pu.overlay0.y, 1.0, edge));
            light = light || d <= 0.0;
        }
        if (presentHasMode(mode, 2.0)) {
            flo = max(flo, mix(pu.overlay0.z, 0.0, edge));
            dark = dark || d <= 0.0;
        }
        if (presentHasMode(mode, 4.0)) wDesat = max(wDesat, w);
        if (presentHasMode(mode, 8.0) && w > wBlur) { wBlur = w; blurStrength = shape.z; }
        if (presentHasMode(mode, 16.0)) wMotion = max(wMotion, w);
        if (presentHasMode(mode, 32.0)) wFlash = max(wFlash, w);
    }

    vec3 x = presentAberrated(vUV, aspect);
    if (wBlur > 0.0) {
        // A 3 by 3 box of scene taps, its radius the region's strength times the blur radius.
        float r = pu.overlay2.z * blurStrength;
        vec2 o = vec2(r / aspect, r);
        vec3 sum = vec3(0.0);
        for (int j = -1; j <= 1; j++) {
            for (int k = -1; k <= 1; k++) sum += presentScene(vUV + o * vec2(float(j), float(k)));
        }
        x = mix(x, sum / 9.0, wBlur);
    }
    if (pu.post0.x > 0.0) x += pu.post0.x * PRESENT_BLOOM(vUV).rgb;

    // Grade: saturation, plus vibrance for dull colours, (1 - chroma)^2 with chroma
    // (max - min) / max; then the tone map to display-linear.
    float l = presentLum(x);
    float mx = max(x.r, max(x.g, x.b));
    float chroma = mx > 1e-4 ? clamp((mx - min(x.r, min(x.g, x.b))) / mx, 0.0, 1.0) : 1.0;
    x = max(vec3(l) + (x - vec3(l)) * (pu.present0.x + pu.present0.w * (1.0 - chroma) * (1.0 - chroma)), 0.0);
    vec3 c = presentTonemap(x);
    c = mix(c, vec3(presentLum(c)), wDesat);

    // Temporal damping against the previous presented frame: keep a fraction of it, then
    // limit the luminance step.
    vec4 h = PRESENT_HIST(ivec2(gl_FragCoord.xy));
    if (h.a > 0.5) {
        float keep = max(pu.overlay1.y, pu.overlay2.x * wMotion);
        float stepMax = min(pu.overlay1.z, mix(1.0, pu.overlay2.y, wFlash));
        c = mix(c, h.rgb, keep);
        float y = presentLum(c), yh = presentLum(h.rgb);
        float hi = yh + stepMax, lo = yh - stepMax;
        if (y > hi) c *= hi / y;
        else if (y < lo && y < 1.0) c += (lo - y) / (1.0 - y) * (1.0 - c);
    }
    vec3 visual = presentContrast(c, flo, cap);

    // Noise for the grain and the dither, from the pixel (top-left origin on every host) and the
    // frame seed.
    float height = pu.overlay3.y;
    uvec2 px = uvec2(max(vUV * vec2(height * aspect, height), 0.0));
    uint frame = uint(max(pu.post1.x, 0.0));
    uvec3 hd = presentHash(uvec3(px, frame));
    // Film grain: multiplicative, monochrome, triangular; zero under reduced motion.
    float grain = pu.post0.z * (1.0 - pu.overlay3.z);
    if (grain > 0.0) {
        uvec3 hg = presentHash(uvec3(px, frame ^ 0x9e3779b9u));
        c = clamp(c * (1.0 + grain * (presentUnit(hg.x) + presentUnit(hg.y) - 1.0)), 0.0, 1.0);
    }

#ifdef OSCILLUX_PRESENT_LAYER
    vec3 s = presentContrast(clamp(oscillux_present_layer(vUV, c), 0.0, 1.0), flo, cap);
#else
    vec3 s = grain > 0.0 ? presentContrast(c, flo, cap) : visual;
#endif

    // Exact piecewise sRGB encode.
    vec3 lo3 = s * 12.92;
    vec3 hi3 = 1.055 * pow(s, vec3(1.0 / 2.4)) - 0.055;
    vec3 e = mix(hi3, lo3, vec3(lessThanEqual(s, vec3(0.0031308))));
    // On a fixed-point output, light-text pixels round down and dark-text pixels up, so the
    // stored value never crosses the bound. Other pixels take triangular dither of post0.w codes
    // (0 leaves the target's rounding): within one code of either end the noise is rectangular,
    // which never leaves the range, keeps black and white exact and stays unbiased.
    float levels = pu.overlay0.w;
    if (levels > 0.0) {
        if (light) e = floor(e * levels) / levels;
        else if (dark) e = ceil(e * levels) / levels;
        else if (pu.post0.w > 0.0) {
            float tri = presentUnit(hd.x) + presentUnit(hd.y) - 1.0, rect = presentUnit(hd.x) - 0.5;
            e = vec3(presentDither(e.r, levels, tri, rect), presentDither(e.g, levels, tri, rect),
                presentDither(e.b, levels, tri, rect));
        }
    }
    float alpha = light || dark ? 1.0 : pu.overlay3.x;
    if (pu.overlay1.w > 0.5) e *= alpha;
    oColor = vec4(e, alpha);
    oHist = vec4(visual, 1.0);
}
