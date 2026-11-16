#version 300 es
precision highp float;
precision highp int;
precision highp sampler2D;
layout(std140) uniform SwitchBlock {
    vec4 switch0;
    vec4 switch1;
    vec4 size;
} su;
uniform sampler2D uFrom, uTo, uPhoto;
layout(location = 0) out vec4 oColor;
vec3 oscTapFrom(vec2 uv) { return textureLod(uFrom, uv, 0.0).rgb; }
vec3 oscTapTo(vec2 uv) { return textureLod(uTo, uv, 0.0).rgb; }
vec3 oscTapPhoto(vec2 uv) { return textureLod(uPhoto, uv, 0.0).rgb; }
#define OSC_SWITCH_FROM(uv) oscTapFrom(uv)
// SPDX-License-Identifier: AGPL-3.0-only
// Host switch transitions: the four styles a host plays when it switches from one visual to
// another (README.md beside this file is the host-pass contract). Both frames, the photo and the
// result are linear HDR; the blend runs before bloom, tone mapping and the limiter.
//
//   vec3 transition(int style, vec2 uv, TransitionInputs t)
//
// The host defines OSC_SWITCH_FROM(uv), a vec3 linear sample of the outgoing frame with
// clamp-to-edge addressing, before including this file; Melt reads displaced outgoing texels
// through it. The struct carries the outgoing, incoming and photo colours already sampled at uv.
// uv is in [0, 1]^2 with v = 0 at the top row.
//
// Every style is a pure function of its arguments using only + - * /, min/max/clamp, a written-out
// smoothstep, sin and exp, so targets agree within the conformance tolerance
// (conformance/switch-transition/README.md, model tools/switch-transition-vectors.py).

const int OSC_SWITCH_LUMA_KEY = 0;
const int OSC_SWITCH_PHOTO_KEY = 1;
const int OSC_SWITCH_MELT = 2;
const int OSC_SWITCH_DOWNBEAT_CUT_LIGHT_LEAK = 3;
const int OSC_SWITCH_BEAT_IRIS = 4;

// Added light: per pixel, the luminance-weighted positive part of (style - reference) never
// exceeds this. The ACES fitted tone map's steepest slope is 1.80 (at x = 0.12), so after it a
// cell's added luminance stays at or below 1.80 * 0.04 = 0.072, under the 0.08 cell cap.
const float OSC_SWITCH_ADDED_LIGHT = 0.04;
const vec3 OSC_SWITCH_Y = vec3(0.2126, 0.7152, 0.0722);
const float OSC_SWITCH_PI = 3.14159265;

struct TransitionInputs {
    vec3 from;       // outgoing frame at uv, linear HDR
    vec3 to;         // incoming frame at uv, linear HDR
    vec3 photo;      // photo (or the neutral dark plate) at uv, linear; read by Photo Key only
    float p;         // progress in [0, 1], linear in time and already eased by the host
    float beatPhase; // [0, 1) position in the current beat; read by Beat Iris
    float energy;    // [0, 1] audio energy; scales emissive light and Melt's drip
    float focus;     // [0, 1] matte edge: 0 soft, 1 crisp
    float motion;    // [0, 1] spatial motion; 0 removes every displacement and pulse
    float aspect;    // frame width / height
};

float oscSwitchSmooth(float e0, float e1, float x) {
    float s = clamp((x - e0) / (e1 - e0), 0.0, 1.0);
    return s * s * (3.0 - 2.0 * s);
}

// Keeps a style's added light within OSC_SWITCH_ADDED_LIGHT of its plain reference by scaling the
// whole difference, so hue holds and the result stays between reference and style.
vec3 oscSwitchGuard(vec3 c, vec3 ref) {
    vec3 d = c - ref;
    float added = dot(max(d, vec3(0.0)), OSC_SWITCH_Y);
    return added > OSC_SWITCH_ADDED_LIGHT ? ref + d * (OSC_SWITCH_ADDED_LIGHT / added) : c;
}

// Key value in [0, 1): mid grey (0.18) keys at 0.5; focus stretches it towards a binary matte.
float oscSwitchKey(vec3 c, float focus) {
    float y = max(dot(c, OSC_SWITCH_Y), 0.0);
    float k = y / (y + 0.18);
    return clamp(0.5 + (k - 0.5) * (1.0 + 3.0 * focus), 0.0, 1.0);
}

// Luma Key: the key source's shadows cross first and its highlights hold longest. Each pixel's
// progress leads or lags p by at most 0.3 sin(pi p), so it stays monotone in p, meets both
// endpoints and, with motion 0, is p itself: the plain dissolve. Luma Key keys on the outgoing
// frame; Photo Key keys on the photo and lets it glow faintly along the moving threshold.
vec3 oscSwitchLumaKey(TransitionInputs t, bool photo) {
    float k = oscSwitchKey(photo ? t.photo : t.from, t.focus);
    float m = clamp(t.p + t.motion * 0.6 * (0.5 - k) * sin(OSC_SWITCH_PI * t.p), 0.0, 1.0);
    vec3 ref = mix(t.from, t.to, t.p);
    vec3 c = mix(t.from, t.to, m);
    if (photo) {
        vec3 ph = max(t.photo, vec3(0.0));
        c += ph / (vec3(1.0) + ph) * (4.0 * m * (1.0 - m)) * t.motion * (0.06 + 0.06 * t.energy);
    }
    return oscSwitchGuard(c, ref);
}

// Melt: columns let go in a slow staggered order (sums of low-frequency sines, so no hash), top
// first with motion; the outgoing frame sags downward as it goes, revealing the incoming one.
vec3 oscSwitchMelt(vec2 uv, TransitionInputs t) {
    float x = uv.x * t.aspect;
    float delay = 0.5 + 0.25 * sin(11.0 * x + 1.3) + 0.15 * sin(23.0 * x + 4.1) + 0.1 * sin(41.0 * x + 2.2);
    float order = mix(delay, 0.55 * delay + 0.45 * uv.y, t.motion);
    float spread = 0.8 + 1.2 * t.focus;
    float q = clamp(t.p * (1.0 + spread) - order * spread, 0.0, 1.0);
    float sag = t.motion * (0.08 + 0.06 * t.energy) * q * q;
    vec3 outgoing = OSC_SWITCH_FROM(vec2(uv.x, uv.y - sag));
    vec3 c = mix(outgoing, t.to, q * q * (3.0 - 2.0 * q));
    return oscSwitchGuard(c, mix(t.from, t.to, t.p));
}

// Downbeat Cut + Light Leak: the cut is on the boundary (p = 0 already shows the incoming frame);
// a warm leak flares within the first 6% of p and decays to nothing at p = 1, drifting across
// the frame with motion. Its luminance peaks below the added-light bound on its own.
vec3 oscSwitchLightLeak(vec2 uv, TransitionInputs t) {
    float env = oscSwitchSmooth(0.0, 0.06, t.p) * (1.0 - t.p) * (1.0 - t.p);
    vec2 s = vec2(uv.x * t.aspect, uv.y);
    vec2 c0 = vec2((0.15 + 0.5 * t.motion * t.p) * t.aspect, 0.2);
    vec2 d0 = s - c0;
    float glow = exp(-dot(d0, d0) / 0.18);
    float streak = exp(-(d0.y + 0.35 * d0.x) * (d0.y + 0.35 * d0.x) / 0.012);
    vec3 warm = vec3(1.0, 0.62, 0.34) / dot(vec3(1.0, 0.62, 0.34), OSC_SWITCH_Y);
    vec3 leak = warm * (0.7 * glow + 0.3 * streak) * env * (0.026 + 0.012 * t.energy);
    return oscSwitchGuard(t.to + leak, t.to);
}

// Beat Iris: a round aperture opens from the centre over the whole of p, from fully closed to
// past the corners; with motion its radius kicks on each beat and relaxes. A thin warm rim lights
// the aperture edge. The reference is the same aperture without the rim.
vec3 oscSwitchBeatIris(vec2 uv, TransitionInputs t) {
    float d = length((uv - 0.5) * vec2(t.aspect, 1.0));
    float rMax = 0.5 * sqrt(t.aspect * t.aspect + 1.0);
    float w = 0.04 - 0.034 * t.focus;
    float win = sin(OSC_SWITCH_PI * t.p);
    float kick = 1.0 - t.beatPhase;
    float r = mix(-w, rMax + w, t.p) + t.motion * (0.02 + 0.02 * t.energy) * kick * kick * win;
    float mask = 1.0 - oscSwitchSmooth(r - w, r + w, d);
    vec3 ref = mix(t.from, t.to, mask);
    float e = (d - r) / (2.0 * w + 0.01);
    vec3 rim = vec3(1.0, 0.9, 0.78) / dot(vec3(1.0, 0.9, 0.78), OSC_SWITCH_Y);
    return oscSwitchGuard(ref + rim * exp(-e * e) * win * (0.026 + 0.012 * t.energy), ref);
}

vec3 transition(int style, vec2 uv, TransitionInputs t) {
    if (style == OSC_SWITCH_PHOTO_KEY) return oscSwitchLumaKey(t, true);
    if (style == OSC_SWITCH_MELT) return oscSwitchMelt(uv, t);
    if (style == OSC_SWITCH_DOWNBEAT_CUT_LIGHT_LEAK) return oscSwitchLightLeak(uv, t);
    if (style == OSC_SWITCH_BEAT_IRIS) return oscSwitchBeatIris(uv, t);
    return oscSwitchLumaKey(t, false);
}

void main() {
    vec2 uv = gl_FragCoord.xy / su.size.xy;
    TransitionInputs t;
    t.from = oscTapFrom(uv);
    t.to = oscTapTo(uv);
    t.photo = oscTapPhoto(uv);
    t.p = su.switch0.x;
    t.beatPhase = su.switch0.y;
    t.energy = su.switch0.z;
    t.focus = su.switch0.w;
    t.motion = su.switch1.x;
    t.aspect = su.switch1.y;
    oColor = vec4(transition(int(su.switch1.z + 0.5), uv, t), 1.0);
}
