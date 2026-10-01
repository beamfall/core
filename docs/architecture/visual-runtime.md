# Visual Runtime spec

Status: accepted
Date: 2026-06-23

Realizes: ADR-0047,
ADR-0139.

ADR-0047 owns the binding decisions: Visuals are compiled client shaders, gated by Visual Tier,
fed by a fixed input vocabulary, and safety-checked before release. This spec owns the mutable
manifest shape and first-party catalog inventory.

## Safety Contract manifest

Each Visual declares:

- **minimum Visual Tier**: Lite, Standard, or Flagship.
- **surfaces**: Overlay-capable or Visualiser-only.
- **input dependencies**: raw inputs, Generated Assets, and derived uniforms/textures consumed.
- **Director profile**: calm, balanced, kinetic, or photo-hero.
- **reduced-motion behavior**: motion-safe variant, static variant, or Dark Room fallback.
- **photosensitivity bounds**: flash-frequency ceiling, luminance-delta ceiling, and strobe ban.
- **comfort bounds**: camera velocity, zoom, rotation, and particle-density ceilings by tier.
- **performance budget**: target resolution scale, pass count, texture budget, and fallback tier.
- **privacy / asset policy**: Reveal-gated image use only, engagement-silent behavior, and no
  persistence of generated frame output.

Missing fallbacks, unknown inputs, unsupported surfaces, or budgets that cannot be met fail the
client build for that Visual.

## AudioFrame v1 contract

The Visual Runtime consumes host-produced `AudioFrame` values, pinned by the Core-owned
`beamfall.visualsync.audio-frame` artifact version `1.0.0` and frame contract version
`audio-frame.v1`. The artifact is additive-only: producers may add fields or enumerated flags in
future client releases, but may not remove or change the meaning of existing v1 fields. Consumers
must ignore unknown fields and flags.

Audible time is the sync reference:

```
audibleTimeMs = mediaTimeMs - outputLatencyMs
```

`mediaTimeMs` is the media timeline at the end of the DSP analysis window. `outputLatencyMs` is a
live estimate, not a startup constant; hosts update it when the route changes. Beat, bar, and
phrase phase or next-boundary predictions are expressed against audible time, never raw media time.

The reference DSP contract is fixed for v1:

- 48 kHz mono mixdown by channel average.
- FFT window 2048 samples, hop 512 samples, Hann window.
- 24 log-spaced spectrum bands from 40 Hz to 16 kHz, magnitude floor -90 dB.
- Per-band attack/release smoothing: 10 ms attack, 200 ms release.
- RMS over 300 ms, normalized against the fixed -18 LUFS reference.
- Onset strength is half-wave rectified log-spectral flux with running peak normalization, emitted
  broadband plus low/mid/high band groups.
- Cadence follows the hop and is variable/gap-tolerant. Producers flag stalls and gaps instead of
  inventing continuous frames.

Normative platform tap rules:

- Android taps the app-owned decoded PCM before `AudioTrack`; the system `Visualizer` API is not a
  conforming source.
- Web runs the reference analysis in an `AudioWorklet`; `AnalyserNode` is not a conforming source.
- Apple taps the playing item's decoded PCM (an `MTAudioProcessingTap` on the `AVPlayerItem` audio
  mix under AVPlayer; an AVAudioEngine tap only where playback runs through the engine) and applies
  output latency: live `AVAudioSession.outputLatency` route updates on iOS/tvOS, output-device
  latency on macOS (never 0).
- Any source the host cannot tap (for example AirPlay/cast routes; client DRM is excluded by
  ADR-0112) sets `live-analysis-unavailable` and degrades to generated assets only.

The producer-side conformance pack lives under
`internal/conformance/testdata/visualsync/`: it pins the contract artifact plus WAV-derived
`AudioFrame` sequences, including output-latency, gap, and stall cases.

## SmartPreset v1 contract

The SmartPreset artifact is `beamfall.visualsync.smart-preset` version `1.0.0`, contract version
`smart-preset.v1`. The portable schema and evaluator fixtures live under
`contracts/visualsync/smartpreset/v1/`; client kits vendor those files one way and generate local
typed models from them. Core never serves, downloads, or remotely updates SmartPreset manifests.

Each manifest declares:

- `param0`, `param1`, and `param2` defaults.
- cue responses for the closed cue set: `ambient`, `intro`, `groove`, `build`, `drop`, `break`,
  `phraseTurn`, and `finale`.
- curves for `param0..2` plus the `palette`, `motion`, `camera`, `overlay`, and `intensity` hint
  channels.
- required inputs and named fallbacks.
- intensity bounds in `[0,1]`.
- palette, motion, camera, overlay, reduced-motion, performance, and capability requirements.

The v1 curve vocabulary is closed: `constant`, `easing`, `envelope`, and `lfo`. Easing curves use
`linear`, `easeIn`, `easeOut`, or `easeInOut`; envelopes use attack/release durations in `seconds`
or `beats`; LFOs use `sine`, `triangle`, or `square` with beat-phase anchoring when unit is
`beats`. Curve parameters are numeric only. Unknown fields, unknown cues, unknown targets, unknown
curve kinds, or expression-shaped values fail build-time validation. Clamp, wrap, and exponential
smoothing semantics are pinned by the reference evaluator and `curve-fixtures.json`.

## Visual Director v1 contract

The Visual Director contract is `visual-director.v1`. Core owns the portable reference
implementation in `tools/visualsync/contract`; clients port it and compare semantic traces, not
pixels. The Director evaluates on a fixed logical media-time tick, defaulting to 250 ms, so host
refresh rate and render cadence do not change cue traces.

The cue vocabulary is closed for v1:

| Cue | Derivation contract |
|---|---|
| `ambient` | Degrade path for unavailable live analysis, untrusted beat state, low onset, or low-energy non-rhythmic material. |
| `intro` | Early track position cue from media time and duration. |
| `groove` | Trusted steady-state fallback when no stronger cue lands. |
| `build` | Trusted beat state plus elevated onset or high-band activity. |
| `drop` | Trusted beat state plus strong broadband onset. |
| `break` | Trusted low-energy passage. |
| `phraseTurn` | Trusted phrase boundary from sync-lane / phase confidence. |
| `finale` | Late track position cue from media time and duration. |

Beat trust uses hysteresis: confidence at or above 0.60 enters trusted state; confidence below
0.40 leaves it. Missing or low-confidence beat data never triggers client-side BPM or beat
inference; the Director degrades to `ambient` or position-only cues.

Each emitted trace record carries:

- `contractVersion`, tick index, effective media time, cue, and landing tick;
- selected Visual and SmartPreset;
- transition kind and boundary alignment;
- fallback reason, including safety-driven fallback;
- composed intensity, intensity clamp, clamp ceilings, and degradation-ladder steps.

Selection is deterministic from contract inputs: seed, cue, current selection, compatible Visual
set, surface profile, and safety flags. Default rotation lands on phrase boundaries when available,
then bar, beat, or tick. A Visual+SmartPreset pair is not immediately repeated. "Hold this look"
pins selection only; cue motion, intensity, and clamps continue. If a held selection becomes
incompatible with an active clamp, hold releases and the fallback reason is
`hold-released-by-clamp`.

Per-surface default profiles are contract input:

| Profile | Default intensity | Transition bias | Clamp bias |
|---|---:|---|---|
| `tvAmbient` | 0.55 | slower phrase-led rotation | OLED idle protection. |
| `phoneNowPlaying` | 0.45 | conservative cuts | low performance budget. |
| `web` | 0.50 | balanced cuts / phrase crossfades | neutral default. |

Hard clamps are independent per-axis ceilings; composition is most-restrictive-wins, not a
priority chain. The v1 clamp inventory is:

| Clamp | Axes |
|---|---|
| Reduce Motion | intensity, camera velocity, zoom, rotation, reduced-motion-compatible selection. |
| Photosensitive safe mode | intensity, flash frequency, luminance delta, flash area, pre-vetted calm selection. |
| Vestibular / motion comfort | camera velocity, zoom, rotation. |
| OLED idle protection | intensity and luminance delta. |
| Overlay legibility | intensity, luminance delta, overlay-safe selection. |
| Dark Room | near-static intensity, flash, luminance, camera, zoom, and rotation ceilings. |
| Platform performance budget | resolution scale, derived-texture quality, particle count, intensity cap, lower-tier Visual, Dark Room floor. |

Intensity composition is fixed: user intensity and Director intensity take the lower value, then
clamp into the SmartPreset bounds and the active safety ceiling.

The Director-layer flash/luminance guard is always on. It tracks live onset-driven flash events
across cue transitions and Visual switches; when the active flash-rate or luminance-delta ceiling
would be exceeded, it holds the transition, clamps intensity to the runtime guard ceiling, records
`photosensitivity-runtime-guard`, and appends `runtime-flash-guard` to the ladder steps.

## Visual switch transitions

The following numbered rules are the host contract for switching between Visuals. `LandingTick`
stays the decision tick: it selects the boundary and transition plan, while rendering may prepare
ahead of that tick and overlap after it.

1. **VS-1 — State machine.** A switch moves through `Idle`, `Preparing`, `Armed`, and `Overlap`.
   `Preparing` builds or warms the incoming Visual without displaying it. `Armed` means the plan is
   ready and waiting for its chosen boundary. `Overlap` renders the outgoing and incoming Visuals
   until the transition completes, then returns to `Idle` with only the incoming Visual active.
2. **VS-2 — Landing.** For a crossfade, the transition midpoint lands on the chosen boundary. The
   host derives that boundary from `DirectorPhase.timeToNextMS` in audible time, including the
   output path's effective latency. A cut or iris starts on the boundary. If preparation is not
   complete in time, the host moves the switch to the next boundary of the same grain instead of
   landing late. A user-picked Visual always targets the next beat.
3. **VS-3 — Duration and easing.** Luma Key (including photo-key) and Melt last 1.2 s and apply
   `smoothstep` exactly once. Downbeat Cut + Light Leak cuts on the boundary and runs a 0.6 s leak
   with linear progress. Beat Iris lasts 1.0 s and applies `smoothstep` exactly once. No host or
   shader may apply a second easing curve.
4. **VS-4 — Blend placement.** Both Visuals and their blend are evaluated in linear HDR. The blend
   occurs before bloom, tonemapping, and the final luminance limiter, so those stages process one
   composited signal.
5. **VS-5 — Overlap budget.** A host chooses one recorded overlap mode: `full`, `halfResolution`,
   `frozen`, or `incomingDownOneRung`. `full` renders both Visuals at their normal setting;
   `halfResolution` renders the overlap at half resolution; `frozen` holds a prepared frame for one
   side; `incomingDownOneRung` lowers only the incoming Visual by one degradation rung. The
   performance governor ignores overlap frames so a planned switch cannot permanently lower the
   steady-state quality tier.
6. **VS-6 — Luminance and limiter continuity.** A switch never resets limiter state. Across every
   transition style, the transition itself may add no more than 0.08 relative luminance to any
   cell; the always-on flash/luminance guard may hold or further clamp the switch.
7. **VS-7 — Reduce Motion.** With Reduce Motion active, every animated style becomes a 0.8 s plain
   dissolve with linear progress. Boundary selection and the safety clamps remain unchanged.
8. **VS-8 — Photo handoff.** A photo-key transition receives a prepared, authorized photo input
   before it becomes `Armed` and keeps that input stable through `Overlap`. If the photo is
   unavailable or restricted, the host substitutes the neutral dark plate before shader input; no
   artwork bytes, filename, identifier, or token from the restricted photo may enter the shader or
   trace.
9. **VS-9 — Named styles.** The portable style names are `lumaKey` (with the `photoKey` input
   treatment), `melt`, `downbeatCutLightLeak`, and `beatIris`.
10. **VS-10 — Host trace.** Each attempted switch records `boundaryMs`, `landedMs`, `style`,
    `overlapMode`, `overlapFrames`, `framesOver16_7`, `criticalPathCompiles`, and `minCellLuma`.
    Times use the same audible-time basis as VS-2; counters cover the complete preparation and
    overlap interval, including a deferred landing.

`VISXFADE0927-SHADERS-1`, `VISXFADE0927-WEB-1`, `VISXFADE0927-APPLE-UI-1`, and
`VISXFADE0927-ANDROID-UI-1` implement these rules on their owned shader and host surfaces.

## Music Visuals v1 launch catalog

Dark Room is the motion-free display-mode floor, not a Visual.

| Visual | Tier | Input | Overlay | Shape |
|---|---|---|---|---|
| Liquid Optics | Lite | amplitude + current image | yes | Artwork-through-glass caustics; procedural fallback when image detail is weak. |
| Volumetric Light | Lite | amplitude | yes | Projector haze, sparse light leaks, calm beam fields. |
| Spectral City | Standard | frequency + beat | yes | Glass skyline plus molten waveform highway. |
| Cymatics/Prism | Standard | frequency + beat | yes | Tilted contour ring, prism folds, beat ripples. |
| Particles | Standard | frequency + beat | yes | Support-layer ember swarm and overlay-capable transient field. |
| Event Horizon | Flagship | frequency + beat | no | Opaque tunnel/vortex showpiece. |
| Prism Room | Flagship | frequency + beat | no | Optional faceted caustic room if it clears the authored bar. |

## Deferred first-party candidates

These are not binding release commitments. They become catalog entries only when a client release
adds the compiled Visual and parity-ledger evidence.

| Candidate | Likely fold |
|---|---|
| Pulse | Director impact-core shot. |
| Waveform | Spectral City unless a pure oscilloscope earns a distinct role. |
| Ripple, Lattice, Kaleidoscope | Cymatics/Prism or Event Horizon transition grammar. |
| Bloom, Nebula, Aurora | Volumetric Light if they stay sparse and authored. |
| Spectrum, Terrain | Spectral City. |
| Starfield, Tunnel | Event Horizon. |
| Crystal, Chrome | Prism Room or Liquid Optics. |

## Photo Visuals later increment

Image-capable Visuals are Ambient-Display-only and ship after image-Media plus the Photos Plugin.
Enrichment-produced inputs are optional and must degrade to shader-only fallbacks.

| Visual | Tier | Inputs | Overlay | Shape |
|---|---|---|---|---|
| Parallax | Flagship | image + depth + saliency + bass | no | Photo as 2.5D stage. |
| Shatter | Flagship | image + beat + palette | no | Photo fractures and reforms on beat. |
| Liquid | Flagship | image + frequency | yes | Photo on a fluid bass-reactive surface. |
| Mosaic | Standard | image + frequency + beat | yes | Photo rebuilt from pulsing tiles/facets. |
