#version 300 es
precision highp float;
precision highp int;
precision highp sampler2D;
layout(std140) uniform SlideshowBlock {
    vec4 pass0;
    vec4 pass1;
    vec4 size;
    vec4 slideA;
    vec4 extraA;
    vec4 rectA0;
    vec4 rectA1;
    vec4 slideB;
    vec4 extraB;
    vec4 rectB0;
    vec4 rectB1;
} su;
uniform sampler2D uPhoto0, uPhoto1, uPhoto2, uPhoto3;
layout(location = 0) out vec4 oColor;
vec3 oscTap0(vec2 uv, float footprint) {
    float lod = max(log2(footprint * float(textureSize(uPhoto0, 0).y)), 0.0);
    return textureLod(uPhoto0, uv, lod).rgb;
}
vec3 oscSoft0(vec2 uv, float r) {
    float lod = max(log2(r * float(textureSize(uPhoto0, 0).y)) - 1.0, 0.0);
    return textureLod(uPhoto0, uv, lod).rgb;
}
vec3 oscTap1(vec2 uv, float footprint) {
    float lod = max(log2(footprint * float(textureSize(uPhoto1, 0).y)), 0.0);
    return textureLod(uPhoto1, uv, lod).rgb;
}
vec3 oscSoft1(vec2 uv, float r) {
    float lod = max(log2(r * float(textureSize(uPhoto1, 0).y)) - 1.0, 0.0);
    return textureLod(uPhoto1, uv, lod).rgb;
}
vec3 oscTap2(vec2 uv, float footprint) {
    float lod = max(log2(footprint * float(textureSize(uPhoto2, 0).y)), 0.0);
    return textureLod(uPhoto2, uv, lod).rgb;
}
vec3 oscSoft2(vec2 uv, float r) {
    float lod = max(log2(r * float(textureSize(uPhoto2, 0).y)) - 1.0, 0.0);
    return textureLod(uPhoto2, uv, lod).rgb;
}
vec3 oscTap3(vec2 uv, float footprint) {
    float lod = max(log2(footprint * float(textureSize(uPhoto3, 0).y)), 0.0);
    return textureLod(uPhoto3, uv, lod).rgb;
}
vec3 oscSoft3(vec2 uv, float r) {
    float lod = max(log2(r * float(textureSize(uPhoto3, 0).y)) - 1.0, 0.0);
    return textureLod(uPhoto3, uv, lod).rgb;
}
vec3 oscSlideTap(int i, vec2 uv, float footprint) {
    if (i == 0) return oscTap0(uv, footprint);
    if (i == 1) return oscTap1(uv, footprint);
    if (i == 2) return oscTap2(uv, footprint);
    return oscTap3(uv, footprint);
}
vec3 oscSlideSoft(int i, vec2 uv, float r) {
    if (i == 0) return oscSoft0(uv, r);
    if (i == 1) return oscSoft1(uv, r);
    if (i == 2) return oscSoft2(uv, r);
    return oscSoft3(uv, r);
}
#define OSC_SLIDE_TAP(i, uv, footprint) oscSlideTap(i, uv, footprint)
#define OSC_SLIDE_SOFT(i, uv, r) oscSlideSoft(i, uv, r)
// SPDX-License-Identifier: AGPL-3.0-only
// Calm photo slideshow (README.md beside this file is the host-pass contract): full-frame Ken
// Burns aimed at a supplied salient rect, portrait pairing or blurred fill, and three calm
// transitions (dissolve, light sweep, iris). Photos and the result are linear; the pass runs
// before bloom, tone mapping and the flash limiter.
//
//   vec3 slideshow(vec2 uv, SlideshowInputs s)
//
// The host defines, before including this file:
//   OSC_SLIDE_TAP(i, uv, f)  a vec3 linear sample of photo i at photo uv, clamp-to-edge, at the
//                            mip level for a footprint of f photo heights per pixel;
//   OSC_SLIDE_SOFT(i, uv, r) the same, prefiltered for taps r photo heights apart (a mip level),
//                            or the plain tap when the photo has no mips.
// Photos: 0 outgoing primary, 1 outgoing secondary, 2 incoming primary, 3 incoming secondary.
// uv is in [0, 1]^2 with v = 0 at the top row; photo uv likewise.
//
// Audio moves nothing unless amplitude > 0, and then only the pace of the Ken Burns move, which
// never reverses. No input adds light except the light sweep's band, held within 0.04 of its
// reference. Every function uses only + - * /, min/max/clamp, a written-out smoothstep, sin and
// exp, so targets agree within the conformance tolerance (conformance/slideshow/README.md, model
// tools/slideshow-vectors.py).

const int OSC_SLIDE_DISSOLVE = 0;
const int OSC_SLIDE_LIGHT_SWEEP = 1;
const int OSC_SLIDE_IRIS = 2;

const int OSC_SLIDE_COVER = 0;
const int OSC_SLIDE_FILL = 1;
const int OSC_SLIDE_PAIR = 2;

const vec3 OSC_SLIDE_Y = vec3(0.2126, 0.7152, 0.0722);
const float OSC_SLIDE_PI = 3.14159265;
// Added light of the light sweep over its plain wipe; after the ACES fitted tone map (slope at
// most 1.80) a cell adds at most 0.072 luminance, under the limiter's 0.08 cell cap.
const float OSC_SLIDE_ADDED_LIGHT = 0.04;
// Ken Burns: at motion 1 the window shrinks to 1 / 1.18 of its full size over a slide.
const float OSC_SLIDE_ZOOM = 0.18;
// Salient rect margin: the rect is grown about its centre by this factor before it is framed.
const float OSC_SLIDE_MARGIN = 1.2;
// Tempo coupling: at amplitude 1 the move's pace swings between 0.1 and 1.9 times its plain pace.
const float OSC_SLIDE_PACE = 0.9;
// Pair: the gap between the two photos, as a fraction of the frame width.
const float OSC_SLIDE_GUTTER = 0.012;
const vec3 OSC_SLIDE_GUTTER_COLOR = vec3(0.003);
// Blurred fill: tap spacing in photo heights, dimming and saturation of the backdrop.
const float OSC_SLIDE_BLUR = 0.05;
const float OSC_SLIDE_FILL_DIM = 0.42;
const float OSC_SLIDE_FILL_SAT = 0.7;

struct SlideInputs {
    float k;         // Ken Burns progress in [0, 1], linear in the slide's time on screen
    int fit;         // OSC_SLIDE_COVER, OSC_SLIDE_FILL or OSC_SLIDE_PAIR
    float direction; // < 0.5 zooms in toward the salient rect; >= 0.5 zooms out from it
    float aspect0;   // primary photo width / height
    float aspect1;   // secondary photo width / height (pair only)
    float beats;     // beats in this slide's whole time on screen, at least 1 (tempo coupling only)
    vec4 rect0;      // primary salient rect (x0, y0, x1, y1) in photo uv; zero area = unknown
    vec4 rect1;      // secondary salient rect (pair only)
};

struct SlideshowInputs {
    float p;         // transition progress in [0, 1], already eased by the host
    int style;       // OSC_SLIDE_DISSOLVE, OSC_SLIDE_LIGHT_SWEEP or OSC_SLIDE_IRIS
    float aspect;    // frame width / height
    float motion;    // [0, 1] Ken Burns depth; 0 holds every photo still
    float amplitude; // [0, 1] tempo coupling; 0 (the default) ignores the audio inputs entirely
    float beatPhase; // [0, 1) position in the current beat
    vec2 size;       // target size in pixels
    SlideInputs a;   // outgoing slide (photos 0 and 1)
    SlideInputs b;   // incoming slide (photos 2 and 3)
};

float oscSlideSmooth(float e0, float e1, float x) {
    float s = clamp((x - e0) / (e1 - e0), 0.0, 1.0);
    return s * s * (3.0 - 2.0 * s);
}

// Path position: k, with the pace of the move swinging with the beat when tempo coupling is on.
// k advances at 1 / T while the beat phase advances at beats / T, so with
// c = 0.9 amplitude / (2 pi b + pi) the derivative of the offset c sin(2 pi beatPhase) sin(pi k)
// stays under 0.9 / T: the move speeds up and slows down on the beat but never reverses, and it
// still starts and ends where it would without audio.
float oscSlidePath(float k, float beats, SlideshowInputs s) {
    float e = clamp(k, 0.0, 1.0);
    if (s.amplitude > 0.0) {
        float c = OSC_SLIDE_PACE * clamp(s.amplitude, 0.0, 1.0) / (2.0 * OSC_SLIDE_PI * max(beats, 1.0) + OSC_SLIDE_PI);
        e += c * sin(2.0 * OSC_SLIDE_PI * s.beatPhase) * sin(OSC_SLIDE_PI * e);
    }
    return e;
}

// The centre nearest `target` at which a window of half size hs keeps [s0, s1] in view (the
// rect's centre when it cannot), then kept inside the photo (centred when the window is wider).
vec2 oscSlideCentre(vec2 target, vec2 hs, vec2 s0, vec2 s1) {
    vec2 lo = s1 - hs;
    vec2 hi = s0 + hs;
    vec2 c = mix(0.5 * (s0 + s1), clamp(target, min(lo, hi), max(lo, hi)), step(lo, hi));
    return mix(clamp(c, min(hs, vec2(0.5)), max(1.0 - hs, vec2(0.5))), vec2(0.5), step(vec2(0.5), hs));
}

// The photo window (centre, half size) in photo uv at path position e. Cover fills the region
// and crops; contain shows the whole photo. Zoom in starts at the full window nearest the photo
// centre that frames the rect and ends zoomed toward the rect's centre; zoom out reverses it.
// Centre and half size move linearly, so every window on the path keeps the rect in view.
vec4 oscSlideWindow(float photoAspect, float regionAspect, vec4 rect, bool contain, float e,
                    float direction, float motion) {
    float r = max(regionAspect, 1e-4) / max(photoAspect, 1e-4);
    vec2 full = contain ? vec2(max(1.0, r), max(1.0, 1.0 / r)) : vec2(min(1.0, r), min(1.0, 1.0 / r));
    vec2 h0 = 0.5 * full;
    vec2 a = min(rect.xy, rect.zw);
    vec2 b = max(rect.xy, rect.zw);
    if ((b.x - a.x) * (b.y - a.y) <= 0.0) { a = vec2(0.3); b = vec2(0.7); }
    vec2 sc = clamp(0.5 * (a + b), 0.0, 1.0);
    vec2 sh = 0.5 * OSC_SLIDE_MARGIN * (b - a);
    vec2 s0 = clamp(sc - sh, 0.0, 1.0);
    vec2 s1 = clamp(sc + sh, 0.0, 1.0);
    vec2 half1 = max(0.5 * (s1 - s0), vec2(1e-4));
    float m = clamp(motion, 0.0, 1.0);
    float z = clamp(min(h0.x / half1.x, h0.y / half1.y), 1.0, 1.0 + OSC_SLIDE_ZOOM * m);
    vec2 h1 = h0 / z;
    vec2 c0 = oscSlideCentre(vec2(0.5), h0, s0, s1);
    vec2 c1 = oscSlideCentre(mix(c0, 0.5 * (s0 + s1), m), h1, s0, s1);
    float f = direction < 0.5 ? e : 1.0 - e;
    return vec4(mix(c0, c1, f), mix(h0, h1, f));
}

// The salient centre's position in the region at path position e (cover only).
vec2 oscSlideFocus(float photoAspect, float regionAspect, vec4 rect, float e, float direction, float motion) {
    vec4 w = oscSlideWindow(photoAspect, regionAspect, rect, false, e, direction, motion);
    vec2 a = min(rect.xy, rect.zw);
    vec2 b = max(rect.xy, rect.zw);
    vec2 sc = (b.x - a.x) * (b.y - a.y) <= 0.0 ? vec2(0.5) : clamp(0.5 * (a + b), 0.0, 1.0);
    return clamp(0.5 + (sc - w.xy) / (2.0 * w.zw), 0.0, 1.0);
}

vec3 oscSlideBlur(int i, vec2 uv, float photoAspect) {
    vec2 step2 = OSC_SLIDE_BLUR * vec2(1.0 / max(photoAspect, 1e-4), 1.0);
    vec3 c = vec3(0.0);
    for (int y = -1; y <= 1; y++) {
        for (int x = -1; x <= 1; x++) {
            float w = (2.0 - abs(float(x))) * (2.0 - abs(float(y)));
            c += w * OSC_SLIDE_SOFT(i, uv + step2 * vec2(float(x), float(y)), OSC_SLIDE_BLUR);
        }
    }
    return c / 16.0;
}

// Cover: the photo fills the region, cropped about the salient rect. heightPx is the region's
// height in pixels: the tap's footprint (photo uv per pixel) selects its mip level.
vec3 oscSlideCover(int i, vec2 q, float regionAspect, float photoAspect, vec4 rect, float e,
                   float direction, float motion, float heightPx) {
    vec4 w = oscSlideWindow(photoAspect, regionAspect, rect, false, e, direction, motion);
    return OSC_SLIDE_TAP(i, w.xy + (q - 0.5) * 2.0 * w.zw, 2.0 * w.w / max(heightPx, 1.0));
}

// Blurred fill: the whole photo over a still, blurred, dimmed cover crop of itself. Never bars.
vec3 oscSlideFill(int i, vec2 q, float regionAspect, float photoAspect, vec4 rect, float e,
                  float direction, float motion, vec2 size) {
    vec4 w = oscSlideWindow(photoAspect, regionAspect, rect, true, e, direction, motion);
    vec2 puv = w.xy + (q - 0.5) * 2.0 * w.zw;
    vec2 px = 2.0 * w.zw / max(size, vec2(1.0));
    vec2 inside = clamp(min(puv, 1.0 - puv) / px + 0.5, 0.0, 1.0);
    vec4 bw = oscSlideWindow(photoAspect, regionAspect, rect, false, 0.0, 0.0, 0.0);
    vec3 back = oscSlideBlur(i, bw.xy + (q - 0.5) * 2.0 * bw.zw, photoAspect);
    back = mix(vec3(dot(back, OSC_SLIDE_Y)), back, OSC_SLIDE_FILL_SAT) * OSC_SLIDE_FILL_DIM;
    return mix(back, OSC_SLIDE_TAP(i, puv, px.y), inside.x * inside.y);
}

// One slide at uv: photo `first` (and first + 1 for a pair) through its layout.
vec3 oscSlideLayer(vec2 uv, SlideInputs sl, int first, SlideshowInputs s) {
    float e = oscSlidePath(sl.k, sl.beats, s);
    if (sl.fit == OSC_SLIDE_PAIR) {
        float hw = 0.5 - 0.5 * OSC_SLIDE_GUTTER;
        float ra = s.aspect * hw;
        if (uv.x < hw) return oscSlideCover(first, vec2(uv.x / hw, uv.y), ra, sl.aspect0, sl.rect0, e, sl.direction, s.motion, s.size.y);
        if (uv.x > 1.0 - hw) return oscSlideCover(first + 1, vec2((uv.x - (1.0 - hw)) / hw, uv.y), ra, sl.aspect1, sl.rect1, e, sl.direction, s.motion, s.size.y);
        return OSC_SLIDE_GUTTER_COLOR;
    }
    if (sl.fit == OSC_SLIDE_FILL) return oscSlideFill(first, uv, s.aspect, sl.aspect0, sl.rect0, e, sl.direction, s.motion, s.size);
    return oscSlideCover(first, uv, s.aspect, sl.aspect0, sl.rect0, e, sl.direction, s.motion, s.size.y);
}

// Light sweep: a soft diagonal wipe from the left that carries a faint warm band of light on its
// edge. The reference is the same wipe without the band.
vec3 oscSlideSweep(vec2 uv, vec3 a, vec3 b, float p, float aspect) {
    float x = (uv.x * aspect + 0.4 * uv.y) / (aspect + 0.4);
    float w = 0.14;
    float s = mix(-w, 1.0 + w, p);
    vec3 ref = mix(a, b, 1.0 - oscSlideSmooth(s - w, s + w, x));
    float e = (x - s) / (0.7 * w);
    vec3 tint = vec3(1.0, 0.95, 0.86) / dot(vec3(1.0, 0.95, 0.86), OSC_SLIDE_Y);
    vec3 c = ref + tint * (0.035 * exp(-e * e) * 4.0 * p * (1.0 - p));
    vec3 d = c - ref;
    float added = dot(max(d, vec3(0.0)), OSC_SLIDE_Y);
    return added > OSC_SLIDE_ADDED_LIGHT ? ref + d * (OSC_SLIDE_ADDED_LIGHT / added) : c;
}

// Iris: a soft round aperture opens on the incoming photo's salient centre (the frame centre for
// fill and pair) until it passes the farthest corner. No rim: it adds no light. The centre follows
// the uncoupled move, so tempo coupling never moves the aperture's edge with the beat.
vec3 oscSlideIris(vec2 uv, vec3 a, vec3 b, float p, SlideshowInputs s) {
    vec2 f = vec2(0.5);
    if (s.b.fit == OSC_SLIDE_COVER) f = oscSlideFocus(s.b.aspect0, s.aspect, s.b.rect0, clamp(s.b.k, 0.0, 1.0), s.b.direction, s.motion);
    vec2 ar = vec2(s.aspect, 1.0);
    vec2 far = max(f, 1.0 - f) * ar;
    float rMax = length(far);
    float w = 0.08;
    float r = mix(-w, rMax + w, p);
    float d = length((uv - f) * ar);
    return mix(a, b, 1.0 - oscSlideSmooth(r - w, r + w, d));
}

vec3 slideshow(vec2 uv, SlideshowInputs s) {
    float p = clamp(s.p, 0.0, 1.0);
    if (p <= 0.0) return oscSlideLayer(uv, s.a, 0, s);
    if (p >= 1.0) return oscSlideLayer(uv, s.b, 2, s);
    vec3 a = oscSlideLayer(uv, s.a, 0, s);
    vec3 b = oscSlideLayer(uv, s.b, 2, s);
    if (s.style == OSC_SLIDE_LIGHT_SWEEP) return oscSlideSweep(uv, a, b, p, s.aspect);
    if (s.style == OSC_SLIDE_IRIS) return oscSlideIris(uv, a, b, p, s);
    return mix(a, b, p);
}

SlideInputs oscSlideUnpack(vec4 slide, vec4 extra, vec4 r0, vec4 r1) {
    SlideInputs sl;
    sl.k = slide.x;
    sl.fit = int(slide.y + 0.5);
    sl.direction = slide.z;
    sl.aspect0 = slide.w;
    sl.aspect1 = extra.x;
    sl.beats = extra.y;
    sl.rect0 = r0;
    sl.rect1 = r1;
    return sl;
}
void main() {
    vec2 uv = gl_FragCoord.xy / su.size.xy;
    SlideshowInputs s;
    s.p = su.pass0.x;
    s.style = int(su.pass0.y + 0.5);
    s.aspect = su.pass0.z;
    s.motion = su.pass0.w;
    s.amplitude = su.pass1.x;
    s.beatPhase = su.pass1.y;
    s.size = su.size.xy;
    s.a = oscSlideUnpack(su.slideA, su.extraA, su.rectA0, su.rectA1);
    s.b = oscSlideUnpack(su.slideB, su.extraB, su.rectB0, su.rectB1);
    oColor = vec4(slideshow(uv, s), 1.0);
}
