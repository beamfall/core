/* tslint:disable */
/* eslint-disable */

/** Copies `components` consecutive source values, as float32, into the block (OscilluxUniformField). */
export interface UniformField {
    /** A FieldSource. */
    source: FieldSource;
    /** Index of the first source element (elements, not bytes). */
    sourceOffset: number;
    /** 1 ..= 64. */
    components: number;
    /** Byte offset of the first component; multiple of 4. */
    offset: number;
    /** Bytes between components; multiple of 4, >= 4. */
    stride: number;
}



/**
 * A host beat clock (`OscilluxBeatClock`): the beat position at one media time and its tempo.
 * The constructor writes 120 BPM in 4/4 at beat 0, media time 0.
 */
export class BeatClock {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" [0, 1); 0 is the downbeat.
     */
    barPhase: number;
    /**
     * Index of the current beat (an integer; fractions are truncated).
     */
    beatIndex: number;
    /**
     * r" [0, 1); 0 is the beat.
     */
    beatPhase: number;
    /**
     * r" 1 ..= 16.
     */
    beatsPerBar: number;
    /**
     * r" Media time of this position, ms.
     */
    mediaTimeMs: number;
    /**
     * r" Tempo, > 0.
     */
    tempoBpm: number;
}

/**
 * Offline beat and downbeat grid (`OscilluxBeatGrid`) of a whole PCM buffer. The constructor
 * runs the analysis and copies every beat into an owned array at `beatsPtr()`: `beatCount`
 * elements `beatBytes()` (24) apart, each little-endian Float64 at 0 (timeSeconds) and 8
 * (tempoBpm) and Uint32 at 16 (beatInBar) and 20 (barIndex). `clock` writes the position at a
 * media time to the getters below without allocating.
 */
export class BeatGrid {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Bytes between beats, `sizeof(OscilluxBeatGridBeat)`.
     */
    static beatBytes(): number;
    /**
     * Byte offset of the beat array.
     */
    beatsPtr(): number;
    /**
     * Writes the position at `mediaTimeSeconds` from the start of the PCM to the clock getters;
     * returns a status. Without a position (fewer than two beats, or a non-finite time, which
     * is refused) `beatIndex` is -1 and the other clock getters 0. Does not allocate.
     */
    clock(mediaTimeSeconds: number): number;
    /**
     * Analyses interleaved Float32 PCM, `config.channels` samples per frame. Allocates and runs
     * the whole analysis. Throws naming the refused member, or when the length is not a whole
     * number of frames.
     */
    constructor(config: BeatGridConfig, samples: Float32Array | number[]);
    /**
     * Downbeats in the grid.
     */
    readonly barCount: number;
    /**
     * Progress through the current bar, [0, 1).
     */
    readonly barPhase: number;
    /**
     * Beats in the grid.
     */
    readonly beatCount: number;
    /**
     * Position of the current beat in its bar, 0 at a downbeat.
     */
    readonly beatInBar: number;
    /**
     * Index of the current beat: negative before the first beat, at or above `beatCount` after
     * the last (the grid is extrapolated at its first and last interval), -1 without a
     * position.
     */
    readonly beatIndex: number;
    /**
     * Progress from the current beat to the next, [0, 1).
     */
    readonly beatPhase: number;
    /**
     * Meter used: the hint, or 3 or 4 chosen from the audio; 0 for an empty grid.
     */
    readonly beatsPerBar: number;
    /**
     * Periodicity of the onset envelope at the global beat period, 0 ..= 1.
     */
    readonly confidence: number;
    /**
     * Length of the analysed PCM, seconds.
     */
    readonly durationSeconds: number;
    /**
     * Local tempo of the current beat, BPM.
     */
    readonly localTempoBpm: number;
    /**
     * Global tempo, BPM; 0 for an empty grid.
     */
    readonly tempoBpm: number;
}

/**
 * Beat grid analysis parameters (`OscilluxBeatGridConfig`); the constructor writes the
 * defaults.
 */
export class BeatGridConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * The first member `new BeatGrid` would refuse, spelled as in oscillux.h (for example
     * "sample_rate_hz" or "max_bpm"), or undefined when the configuration is valid.
     */
    validate(): string | undefined;
    /**
     * r" Meter hint: 2 ..= 16 beats per bar, or 0 to choose 3 or 4 from the audio.
     */
    beatsPerBar: number;
    /**
     * r" Interleaved channels, averaged to mono; at least 1.
     */
    channels: number;
    /**
     * r" Fastest beat tempo considered, BPM; at most 400.
     */
    maxBpm: number;
    /**
     * r" Slowest beat tempo considered, BPM; at least 20 and below maxBpm.
     */
    minBpm: number;
    /**
     * r" PCM sample rate, Hz; 8000 ..= 384000.
     */
    sampleRateHz: number;
}

/**
 * CD+G lyric graphics decoder (`OscilluxCdg`) over a copied pack stream, with a preallocated
 * screen of colour indices (`screenPtr`, 300 x 216 bytes) and window (`rgbaPtr`, 288 x 192
 * straight-alpha RGBA8). The getters read the state of the last advance or `state` call;
 * `count` reads the last `indexCounts` call.
 */
export class Cdg {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Advances or seeks so the first `applied` packs (clamped to the stream) are decoded and
     * reads the state; returns a status. A negative or non-integer count is refused.
     */
    advanceToPack(applied: number): number;
    /**
     * Advances or seeks to media time `seconds` (pack `floor(seconds * 300)` is the last
     * decoded) and reads the state; returns a status. NaN is refused unchanged.
     */
    advanceToTime(seconds: number): number;
    /**
     * Colour `index` (0..15) as `0xRGB`.
     */
    colour(index: number): number;
    /**
     * Pixels of colour `index` (0..15) counted by the last `indexCounts`.
     */
    count(index: number): number;
    /**
     * Counts the pixels of each colour index in the window's `region`
     * (`OSCILLUX_CDG_REGION_*`: 0 the whole window, 1 the outer ring one tile deep), read with
     * `count`; returns a status. A refusal leaves the counts unchanged.
     */
    indexCounts(region: number): number;
    /**
     * Copies `packs` (24-byte packs; a trailing partial pack is ignored) and makes the seek
     * checkpoints.
     */
    constructor(packs: Uint8Array | number[]);
    /**
     * Byte offset of the 288 x 192 RGBA8 window written by `windowRgba`.
     */
    rgbaPtr(): number;
    /**
     * Byte offset of the 300 x 216 colour indices written by `screen`.
     */
    screenPtr(): number;
    /**
     * Copies the screen's colour indices to `screenPtr`; returns a status.
     */
    screen(): number;
    /**
     * Re-reads the state at the current position; returns a status.
     */
    state(): number;
    /**
     * The 6-bit transparent-colour table value of colour `index` (0..15).
     */
    transparency(index: number): number;
    /**
     * Writes the window under transparency key `keyMode` (`OSCILLUX_CDG_KEY_*`: 0 none,
     * 1 transparent-colour table, 2 Memory Preset colour, 3 the index `keyIndex`, 4 every index
     * whose bit is set in the 16-bit mask `keyIndex`) to `rgbaPtr`; returns a status. A refusal
     * leaves the buffer unchanged.
     */
    windowRgba(keyMode: number, keyIndex: number): number;
    /**
     * Packs decoded at the current position.
     */
    readonly applied: number;
    /**
     * Most recent Border Preset colour index.
     */
    readonly borderColour: number;
    /**
     * `OSCILLUX_CDG_CHANGED_*` bits of the last advance.
     */
    readonly changes: number;
    /**
     * Dirty rectangle `[dirtyX0, dirtyX1) x [dirtyY0, dirtyY1)` of the last advance, screen
     * pixels; empty when `dirtyX0 == dirtyX1`.
     */
    readonly dirtyX0: number;
    /**
     * See `dirtyX0`.
     */
    readonly dirtyX1: number;
    /**
     * See `dirtyX0`.
     */
    readonly dirtyY0: number;
    /**
     * See `dirtyX0`.
     */
    readonly dirtyY1: number;
    /**
     * Horizontal scroll offset, 0..5.
     */
    readonly hOffset: number;
    /**
     * Whole packs in the stream.
     */
    readonly packCount: number;
    /**
     * Most recent Memory Preset colour index.
     */
    readonly presetColour: number;
    /**
     * Vertical scroll offset, 0..11.
     */
    readonly vOffset: number;
    /**
     * Screen x of the window's left edge.
     */
    readonly windowX: number;
    /**
     * Screen y of the window's top edge.
     */
    readonly windowY: number;
}

/**
 * A validated curve (`OscilluxCurve`).
 */
export class Curve {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * The value at `input`, or undefined when refused.
     */
    evaluate(input: CurveInput): number | undefined;
    constructor(config: CurveConfig);
}

/**
 * A typed curve (`OscilluxCurveConfig`); the constructor writes a constant 0.
 */
export class CurveConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" Envelope rise, curve units.
     */
    attack: number;
    /**
     * r" Easing length, curve units.
     */
    duration: number;
    /**
     * r" A `CurveEasing`.
     */
    easing: number;
    /**
     * r" `CurveFlag` bits marking which optional values are present.
     */
    flags: number;
    /**
     * r" Easing start value.
     */
    from: number;
    /**
     * r" A `CurveKind`.
     */
    kind: number;
    /**
     * r" Upper bound (Max flag); LFO high value.
     */
    max: number;
    /**
     * r" Lower bound (Min flag); LFO low value.
     */
    min: number;
    /**
     * r" LFO cycle length, curve units.
     */
    period: number;
    /**
     * r" LFO phase offset, cycles.
     */
    phase: number;
    /**
     * r" Envelope release start, curve units; 0 = never.
     */
    releaseAt: number;
    /**
     * r" Envelope fall, curve units.
     */
    release: number;
    /**
     * r" A `CurveShape`.
     */
    shape: number;
    /**
     * r" Exponential smoothing time constant, s; 0 disables.
     */
    smoothingSeconds: number;
    /**
     * r" Easing end value.
     */
    to: number;
    /**
     * r" A `CurveUnit`.
     */
    unit: number;
    /**
     * r" Constant output; envelope peak.
     */
    value: number;
}

/**
 * `CurveConfig.easing` for the Easing kind.
 */
export enum CurveEasing {
    Linear = 0,
    In = 1,
    Out = 2,
    InOut = 3,
}

/**
 * `CurveConfig.flags` bits; combine with `|`.
 */
export enum CurveFlag {
    Value = 1,
    From = 2,
    To = 4,
    Min = 8,
    Max = 16,
    Wrap = 32,
}

/**
 * The clock and history for one evaluation (`OscilluxCurveInput`); starts at zero.
 */
export class CurveInput {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" Added to the LFO phase when the unit is beats, cycles.
     */
    beatPhase: number;
    /**
     * r" Time since the previous evaluation, s; smoothing needs > 0.
     */
    deltaSeconds: number;
    /**
     * r" Time since the curve started, beats.
     */
    elapsedBeats: number;
    /**
     * r" Time since the curve started, s.
     */
    elapsedSeconds: number;
    /**
     * Whether `previousValue` is read.
     */
    hasPrevious: boolean;
    /**
     * r" The previous output, read when hasPrevious is true.
     */
    previousValue: number;
}

/**
 * `CurveConfig.kind`.
 */
export enum CurveKind {
    Constant = 0,
    Easing = 1,
    Envelope = 2,
    Lfo = 3,
}

/**
 * `CurveConfig.shape` for the Lfo kind.
 */
export enum CurveShape {
    Sine = 0,
    Triangle = 1,
    Square = 2,
}

/**
 * `CurveConfig.unit`: the clock for duration, attack, release, releaseAt and period.
 */
export enum CurveUnit {
    Unspecified = 0,
    Seconds = 1,
    Beats = 2,
}

/**
 * Streaming analyzer (`OscilluxDsp`) with preallocated PCM input and frame output buffers:
 * a Float32 input for `push` and, when constructed with an `f64Capacity`, a Float64 input for
 * `pushF64`.
 * Frames use the `OscilluxDspFrame` layout of the included oscillux.h, `frameBytes()` apart;
 * read `sequence` with `DataView.getBigUint64(offset, true)`. `pushExt` and `pushExtF64` also
 * write one `OscilluxDspFrameExt` per frame at `extPtr()`, `extBytes()` apart. The first of
 * them allocates that buffer (`frameCapacity` elements of 672 bytes), so refresh typed-array
 * views after it; a `Dsp` that only uses `push` or `pushF64` never allocates it. Its
 * descriptors are little-endian Float64 at byte 8 (momentaryLufs), 16 (shortTermLufs), 24
 * (width), 32 (balance), 40 (centroidHz) and 48 (spectralFlux). The beat follows as Float64
 * at 56 (tempoBpm), 64 (beatPhase), 72 (barPhase) and 80 (confidence) and Uint32 at 88
 * (beat), 92 (downbeat), 96 (beatInBar) and 100 (reserved). The levels follow: Uint32 at
 * 104 (bandCount) and 108 (reserved), then Float64 relative groups (low, mid, high) at 112,
 * 120 and 128, absolute groups at 136, 144 and 152, and 64 relative bands from 160.
 */
export class Dsp {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Starts a segment whose first sample plays at `timeMs`; returns a status.
     */
    beginSegment(timeMs: number, stalled: boolean): number;
    /**
     * Sample frames (not scalars) consumed by the last `push` or `pushF64`.
     */
    consumed(): number;
    /**
     * Size of one `OscilluxDspFrameExt`, bytes: its `struct_size`.
     */
    extBytes(): number;
    /**
     * Byte offset of the `OscilluxDspFrameExt` output buffer (`frameCapacity` elements) in
     * `wasmMemory().buffer`; `pushExt` and `pushExtF64` write its first `produced()` elements.
     * 0 until the first `pushExt` or `pushExtF64` allocates the buffer; fixed after that.
     */
    extPtr(): number;
    /**
     * Size of one output frame, bytes.
     */
    frameBytes(): number;
    /**
     * Byte offset of the Float64 PCM input buffer (`f64Capacity` samples) in
     * `wasmMemory().buffer`.
     */
    inputF64Ptr(): number;
    /**
     * Byte offset of the Float32 PCM input buffer in `wasmMemory().buffer`.
     */
    inputPtr(): number;
    /**
     * Validates `config` and allocates room for `sampleCapacity` interleaved Float32 PCM
     * samples, `frameCapacity` output frames and `f64Capacity` (default 0) interleaved Float64
     * samples. Throws naming the refused member.
     */
    constructor(config: DspConfig, sampleCapacity: number, frameCapacity: number, f64Capacity?: number | null);
    /**
     * Byte offset of the frame output buffer in `wasmMemory().buffer`.
     */
    outputPtr(): number;
    /**
     * Frames written to the output buffer by the last `push` or `pushF64`.
     */
    produced(): number;
    /**
     * As `pushF64`, and also writes each frame's descriptors, beat and levels to the ext
     * buffer (`oscillux_dsp_push_ext_f64`).
     */
    pushExtF64(sampleCount: number, channels: number): number;
    /**
     * As `push`, and also writes each frame's descriptors, beat and levels to the ext buffer
     * at `extPtr()` (`oscillux_dsp_push_ext_f32`). Frames equal those `push` would write.
     */
    pushExt(sampleCount: number, channels: number): number;
    /**
     * Analyzes the first `sampleCount` interleaved scalars of the Float64 input buffer, exactly
     * as `oscillux_dsp_push_f64`; returns a status. Read `produced()` frames from `outputPtr()`.
     */
    pushF64(sampleCount: number, channels: number): number;
    /**
     * Analyzes the first `sampleCount` interleaved scalars of the Float32 input buffer; returns
     * a status. Read `produced()` frames from `outputPtr()`.
     */
    push(sampleCount: number, channels: number): number;
}

/**
 * Analysis parameters (`OscilluxDspConfig`) and DSP behaviour version, passed to C as an
 * `OscilluxDspVersionedConfig`; the constructor writes the reference values and version 2.
 * Its `reserved` member is not exposed and is always passed as 0.
 */
export class DspConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * Onset band group `group` (0 low, 1 mid, 2 high) as `[start, end]`, inclusive.
     */
    onsetBandGroup(groupIndex: number): Uint32Array;
    setOnsetBandGroup(group: number, start: number, end: number): void;
    /**
     * The first member `new Dsp` would refuse, spelled as in oscillux.h (for example
     * "fft_window_samples" or "behavior_version"), or undefined when the configuration is
     * valid.
     */
    validate(): string | undefined;
    /**
     * r" Band smoothing time constant while rising, ms.
     */
    ballisticsAttackMs: number;
    /**
     * r" Band smoothing time constant while falling, ms.
     */
    ballisticsReleaseMs: number;
    /**
     * DSP behaviour version: 1 (`OSCILLUX_DSP_BEHAVIOR_V1`, the ABI-1 analysis), 2 (the
     * default; real-input FFT, low-band bass analysis and normalised onsets), 3 (version 2
     * frames with the autocorrelation beat tracker) or 4 (version 2 frames with the
     * beat-pointer beat tracker).
     */
    behaviorVersion: number;
    /**
     * r" Band level mapped to 0, dB; negative.
     */
    dbFloor: number;
    /**
     * r" Window and FFT length, samples; a power of two.
     */
    fftWindowSamples: number;
    /**
     * r" Segment lateness tolerated before the Gap flag, hops.
     */
    gapToleranceHops: number;
    /**
     * r" Samples between frames.
     */
    hopSamples: number;
    /**
     * r" Subtracted from the RMS level for loudnessDb, dB.
     */
    loudnessReferenceLufs: number;
    /**
     * r" Per-frame decay factor of running onset peaks, 0 ..= 1.
     */
    onsetPeakDecay: number;
    /**
     * r" Lower bound of an onset peak used as a divisor.
     */
    onsetPeakFloor: number;
    /**
     * r" Linear RMS lower bound before the logarithm.
     */
    rmsFloor: number;
    /**
     * r" RMS window, ms.
     */
    rmsWindowMs: number;
    /**
     * r" Input sample rate, Hz.
     */
    sampleRateHz: number;
    /**
     * r" Log-spaced bands, 1 ..= 64.
     */
    spectrumBands: number;
    /**
     * r" Upper band edge, Hz; at most half the sample rate.
     */
    spectrumMaxHz: number;
    /**
     * r" Lower edge of the first band, Hz.
     */
    spectrumMinHz: number;
}

/**
 * `OscilluxDspFrame.flags` bits.
 */
export enum DspFlag {
    Gap = 1,
    Stalled = 2,
    InputReplaced = 4,
}

/**
 * `UniformField.source`.
 */
export enum FieldSource {
    /**
     * Mapped values, indexed by `MappedValue`.
     */
    Mapped = 0,
    /**
     * The 64 mapped spectrum bins.
     */
    Spectrum = 1,
    /**
     * The packer's host values.
     */
    Host = 2,
}

/**
 * Region flash analyzer (`OscilluxFlashAnalyzer`) with preallocated row-major region and color
 * arrays (`columns * rows` pairs of doubles: luminance, red; and triples: linear r, g, b) and
 * one report. `measure` / `admit` read the regions, `measureRgb` / `admitRgb` the colors; each
 * writes the report and none allocates.
 */
export class FlashAnalyzer {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * As `admit`, from the color array.
     */
    admitRgb(timeMs: number): number;
    /**
     * As `measure`, but commits the frame only if it keeps the window within the config.
     * When `admitted` is false the host keeps presenting its last admitted image.
     */
    admit(timeMs: number): number;
    /**
     * Removes the overlay; the analyzer then measures exactly as without one.
     */
    clearOverlay(): number;
    /**
     * Byte offset of the color array: Float64 triples (r, g, b), linear light, row-major.
     */
    colorsPtr(): number;
    /**
     * As `measure`, from the color array; the only path `FlashRedRule.Iso9241_391` accepts.
     */
    measureRgb(timeMs: number): number;
    /**
     * Measures and commits the regions as the frame at `timeMs`; returns a status. On an
     * error the report is zero except `timeMs`.
     */
    measure(timeMs: number): number;
    /**
     * Throws when `config` is refused.
     */
    constructor(config: FlashConfig);
    /**
     * Writes the overlay outcome of the last measured or admitted frame; returns a status.
     */
    overlayReport(out: FlashOverlayReport): number;
    /**
     * Regions in the array, `columns * rows`.
     */
    regionCount(): number;
    /**
     * Byte offset of the region array: Float64 pairs (luminance, red), row-major.
     */
    regionsPtr(): number;
    /**
     * Byte offset of the `OscilluxFlashReport` the last measure or admit wrote.
     */
    reportPtr(): number;
    /**
     * Starts a new timeline (a seek or new stream); returns a status.
     */
    reset(): number;
    /**
     * Holds the grid cells `regions`' `DampFlash` regions cover to `overlay` from the next
     * frame; returns a status. A refusal keeps the previous overlay. Call on layout changes.
     */
    setOverlay(overlay: FlashOverlay, regions: OverlayRegions): number;
    /**
     * Writes region `index` from a mean linear-light sRGB color, each channel in [0, 1];
     * returns a status and leaves the region unchanged on refusal.
     */
    setRegionFromLinearRgb(index: number, r: number, g: number, b: number): number;
    /**
     * The frame was committed; false means show the last admitted one.
     */
    readonly admitted: boolean;
    /**
     * The window holds more flashes than the config allows.
     */
    readonly exceeds: boolean;
    /**
     * General flashes (transitions / 2) in the last 1000 ms.
     */
    readonly flashesInWindow: number;
    /**
     * Regions completing a general change this frame.
     */
    readonly generalRegions: number;
    /**
     * The frame completes a general transition.
     */
    readonly generalTransition: boolean;
    /**
     * Red flashes (transitions / 2) in the last 1000 ms.
     */
    readonly redFlashesInWindow: number;
    /**
     * Regions completing a red change this frame.
     */
    readonly redRegions: number;
    /**
     * The frame completes a red transition.
     */
    readonly redTransition: boolean;
    /**
     * The last report's frame time, ms.
     */
    readonly timeMs: number;
}

/**
 * Region flash analyzer configuration (`OscilluxFlashConfig`); the constructor writes the
 * defaults, the loosest configuration `new FlashAnalyzer` accepts.
 */
export class FlashConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" Regions across the viewport; 1 ..= 16.
     */
    columns: number;
    /**
     * r" Field-window height as a viewport fraction; > 0, at most 1.
     */
    fieldHeight: number;
    /**
     * r" Field-window width as a viewport fraction; > 0, at most 1.
     */
    fieldWidth: number;
    /**
     * r" Field-window fraction at or below which a change is no flash; > 0, at most 0.25.
     */
    maxFieldArea: number;
    /**
     * r" General flashes per one-second window; > 0, at most 3.
     */
    maxFlashHz: number;
    /**
     * r" Red flashes per one-second window; 0 ..= 3.
     */
    maxRedFlashes: number;
    /**
     * r" A `FlashRedRule`; any other value is refused by `new FlashAnalyzer`.
     */
    redRule: number;
    /**
     * r" Regions down the viewport; 1 ..= 16.
     */
    rows: number;
}

/**
 * Stricter flash rules for `OverlayMode.DampFlash` regions (`OscilluxFlashOverlay`); the
 * constructor writes the defaults.
 */
export class FlashOverlay {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" A change counts once its darker end is below this, [0.8, 1]; default 1.
     */
    darkerLimit: number;
    /**
     * r" Smallest luminance change that counts inside, (0, 0.1]; default 0.05.
     */
    luminanceChange: number;
    /**
     * r" Fraction of overlay cells above which a frame is a transition; default 0 (one cell).
     */
    maxArea: number;
    /**
     * r" Overlay flashes a second, at most the analyzer's; default 1.
     */
    maxFlashHz: number;
}

/**
 * The overlay outcome `FlashAnalyzer.overlayReport` writes (`OscilluxFlashOverlayReport`).
 */
export class FlashOverlayReport {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * An overlay is set.
     */
    readonly active: boolean;
    /**
     * Grid cells the overlay covers.
     */
    readonly cells: number;
    /**
     * The overlay window holds more flashes than it allows.
     */
    readonly exceeds: boolean;
    /**
     * Overlay flashes (transitions / 2) in the last 1000 ms.
     */
    readonly flashesInWindow: number;
    /**
     * Overlay cells completing a change this frame.
     */
    readonly regions: number;
    /**
     * The frame holds an overlay transition.
     */
    readonly transition: boolean;
}

/**
 * `FlashConfig.redRule`: which red-flash rule the analyzer applies.
 */
export enum FlashRedRule {
    /**
     * WCAG 2.0: (R - G - B) * 320 changes by more than 20; the default.
     */
    Wcag20 = 0,
    /**
     * ISO 9241-391: a transition to or from saturated red (R / (R + G + B) >= 0.8) with a
     * CIE 1976 u'v' change above 0.2. Needs colors: use `measureRgb` / `admitRgb`.
     */
    Iso9241_391 = 1,
}

/**
 * Frame-time governor (`OscilluxGovernor`). `level`, `emaMs` and `refreshMs` read the stats
 * of the last `record` or `reset`.
 */
export class Governor {
    free(): void;
    [Symbol.dispose](): void;
    constructor(config: GovernorConfig);
    /**
     * Records one frame duration, ms; returns a status. Pauses and invalid durations are
     * ignored.
     */
    record(frameMs: number): number;
    /**
     * Clears the average and counters and sets the level, clamped to maxLevel.
     */
    reset(level: number): number;
    /**
     * Moving average of frame duration, ms; undefined before the first accepted sample.
     */
    readonly emaMs: number | undefined;
    /**
     * Current quality level, 0 = lowest.
     */
    readonly level: number;
    /**
     * Estimated display refresh interval, ms.
     */
    readonly refreshMs: number;
}

/**
 * Frame-time governor parameters (`OscilluxGovernorConfig`); the constructor writes the
 * reference values.
 */
export class GovernorConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" An average above targetMs times this is slow.
     */
    demoteFactor: number;
    /**
     * r" Consecutive slow samples before a step down.
     */
    demoteFrames: number;
    /**
     * r" Weight of each sample in the moving average; (0, 1].
     */
    emaAlpha: number;
    /**
     * r" Level after construction; <= maxLevel.
     */
    initialLevel: number;
    /**
     * r" Highest quality level.
     */
    maxLevel: number;
    /**
     * r" Durations at or above this are pauses and ignored, ms.
     */
    pauseGapMs: number;
    /**
     * r" Consecutive fast samples before a step up.
     */
    promoteFrames: number;
    /**
     * r" An average below refreshMs times this is fast ...
     */
    promoteRefreshFactor: number;
    /**
     * r" ... or below targetMs times this.
     */
    promoteTargetFactor: number;
    /**
     * r" Per-sample growth factor of refreshMs toward the target; >= 1.
     */
    refreshDrift: number;
    /**
     * r" Lower bound of refreshMs, ms.
     */
    refreshFloorMs: number;
    /**
     * r" Samples are capped at targetMs times this.
     */
    sampleCapFactor: number;
    /**
     * r" Samples after a step that cannot cause another step.
     */
    settleFrames: number;
    /**
     * r" Target frame duration, ms.
     */
    targetMs: number;
}

/**
 * `Mapper.setLevelSource` values (`OSCILLUX_MAPPING_LEVELS_*`).
 */
export enum LevelSource {
    /**
     * The band energies; the default.
     */
    Absolute = 0,
    /**
     * The ext frame's relative band levels.
     */
    Relative = 1,
}

/**
 * Flash limiter (`OscilluxLimiter`) with two preallocated seven-double frames in the order
 * time, broadband, low, mid, high, red, area.
 */
export class Limiter {
    free(): void;
    [Symbol.dispose](): void;
    inputPtr(): number;
    constructor(policy: LimiterPolicy);
    outputPtr(): number;
    /**
     * Limits the input frame into the output frame; returns a status. A refused frame writes
     * the fail-safe frame, which the host renders.
     */
    process(): number;
    /**
     * Starts a new timeline; returns a status.
     */
    reset(): number;
}

/**
 * Flash-limiter policy (`OscilluxLimiterPolicy`); the constructor writes the default, the
 * loosest policy accepted. Ceilings may only be tightened.
 */
export class LimiterPolicy {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" Screen fraction at or below which a change is no flash; at most 0.25.
     */
    maxFlashArea: number;
    /**
     * r" General flashes per one-second window; at most 3.
     */
    maxFlashHz: number;
    /**
     * r" Ceiling on any onset magnitude, normalized; at most 0.8.
     */
    maxLuminanceDelta: number;
    /**
     * r" Red flashes per one-second window; 0 ..= 3.
     */
    maxRedFlashes: number;
    /**
     * r" Must equal 1.
     */
    strobeBanned: number;
}

/**
 * Indices into the mapped values (`OSCILLUX_MAPPED_*`); every value is normalized.
 */
export enum MappedValue {
    Amplitude = 0,
    Level = 1,
    Bass = 2,
    Mid = 3,
    Treble = 4,
    Onset = 5,
    BassImpact = 6,
    /**
     * Spectral flux with descriptors (`processExt`); otherwise the broadband onset, a
     * deprecated proxy.
     */
    Flux = 7,
    /**
     * Log-frequency centroid with descriptors (`processExt`); otherwise the band-index
     * fraction, a deprecated proxy.
     */
    Centroid = 8,
}

/**
 * Frame mapper (`OscilluxMapper`) with a preallocated `OscilluxMapped` output (nine float64
 * values, then 64 float32 spectrum bins at byte 72) and a one-frame input buffer for frames
 * that do not come from a `Dsp`.
 */
export class Mapper {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Byte offset of the one `OscilluxDspFrameExt` input used by `processFrameExt`; its
     * `struct_size` is preset, so write only the descriptors and, for the relative level
     * source, the levels (layout as on `Dsp`).
     */
    extInputPtr(): number;
    /**
     * Byte offset of the one-frame `OscilluxDspFrame` input used by `processFrame`.
     */
    frameInputPtr(): number;
    constructor(config: MappingConfig);
    /**
     * Byte offset of the `OscilluxMapped` output.
     */
    outputPtr(): number;
    /**
     * As `process`, with frame `index`'s descriptors from `dsp`'s last `pushExt` or
     * `pushExtF64` feeding FLUX and CENTROID (`oscillux_mapping_process_ext`), and its levels
     * feeding the relative level source; invalid after any other push.
     */
    processExt(dsp: Dsp, index: number, unavailable: boolean): number;
    /**
     * Maps the frame at `frameInputPtr()` with the descriptors and levels at `extInputPtr()`;
     * returns a status.
     */
    processFrameExt(unavailable: boolean): number;
    /**
     * Maps the frame written at `frameInputPtr()`; returns a status.
     */
    processFrame(unavailable: boolean): number;
    /**
     * Maps frame `index` of `dsp`'s last `push` (index < produced()); returns a status.
     * `unavailable` writes all zeros.
     */
    process(dsp: Dsp, index: number, unavailable: boolean): number;
    /**
     * Forgets the previous level; returns a status.
     */
    reset(): number;
    /**
     * Selects what BASS, MID, TREBLE and the spectrum read (a `LevelSource`): `Absolute`, the
     * default, the band energies; `Relative` the ext frame's relative band levels. The
     * relative source refuses `process` and `processFrame`, and `processExt` or
     * `processFrameExt` whose levels band count is not the frame's (0 when the analyzer
     * refused the AGC configuration) or whose relative bands are not finite. Returns a
     * status; an unknown source is refused and leaves the source unchanged. `reset` keeps it.
     */
    setLevelSource(source: number): number;
    /**
     * Spectrum bin `index` (0 ..= 63) from the last successful call, or undefined.
     */
    spectrum(index: number): number | undefined;
    /**
     * Mapped value `index` (a `MappedValue`) from the last successful call, or undefined for
     * an index out of range.
     */
    value(index: number): number | undefined;
}

/**
 * DSP-frame-to-value mapping (`OscilluxMappingConfig`); the constructor writes the values
 * matching the default `DspConfig`, `fromDsp` those matching any valid one.
 */
export class MappingConfig {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * The default mapping with hop and loudness reference taken from `dsp`; throws when `dsp`
     * is invalid.
     */
    static fromDsp(dsp: DspConfig): MappingConfig;
    /**
     * Band group `group` (0 bass, 1 mid, 2 treble) as `[start, end]`, inclusive.
     */
    group(groupIndex: number): Uint32Array;
    constructor();
    setGroup(group: number, start: number, end: number): void;
    /**
     * r" Summed band energy at or below which the centroid is 0.
     */
    centroidFloor: number;
    /**
     * r" Loudness mapped to amplitude 0, dB; negative.
     */
    dbFloor: number;
    /**
     * r" Analyzer hop duration, s.
     */
    hopSeconds: number;
    /**
     * r" Added back to a frame's loudnessDb, dB.
     */
    loudnessReference: number;
    /**
     * r" Level decay time constant, s.
     */
    releaseSeconds: number;
}

/**
 * Overlay presentation config (`OscilluxOverlayConfig`); the constructor writes the defaults.
 */
export class OverlayConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * The intensity visuals present at: `intensity`, halved under reduced motion. Throws when
     * the config is refused.
     */
    presentationIntensity(): number;
    /**
     * Tightens `flash` for reduced motion (at most 1 general and 1 red flash a second over 0.1
     * of a field); never loosens it. Returns a status; a refusal leaves `flash` unchanged.
     */
    tightenFlashConfig(flash: FlashConfig): number;
    /**
     * r" Output alpha outside text regions, [0, 1].
     */
    alpha: number;
    /**
     * r" WCAG contrast ratio text regions guarantee, [1, 21]; default 4.5.
     */
    contrastRatio: number;
    /**
     * r" Milliseconds since the previous presented frame, (0, 1000].
     */
    frameIntervalMs: number;
    /**
     * r" Host presentation intensity before reduced motion, [0, 1].
     */
    intensity: number;
    /**
     * r" Levels per channel: 0 for a float target or 2 ..= 65535; default 255.
     */
    outputLevels: number;
    /**
     * The canvas composites premultiplied alpha.
     */
    premultipliedAlpha: boolean;
    /**
     * The user asked for reduced motion (`prefers-reduced-motion: reduce`).
     */
    reducedMotion: boolean;
    /**
     * r" Drawing-buffer height in pixels.
     */
    viewportHeight: number;
    /**
     * r" Drawing-buffer width in pixels.
     */
    viewportWidth: number;
}

/**
 * `OverlayRegions.setRegion` mode bits; `LightText` and `DarkText` are exclusive.
 */
export enum OverlayMode {
    /**
     * Hold the region's luminance at or below the ceiling, for light text.
     */
    LightText = 1,
    /**
     * Hold the region's luminance at or above the floor, for dark text.
     */
    DarkText = 2,
    Desaturate = 4,
    Blur = 8,
    DampMotion = 16,
    /**
     * Hold the grid cells the region covers to the analyzer's `FlashOverlay`.
     */
    DampFlash = 32,
}

/**
 * Up to `OSCILLUX_OVERLAY_MAX_REGIONS` (8) overlay regions and the packed 320-byte
 * `OverlayUniforms` block. `pack` does not allocate.
 */
export class OverlayRegions {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * No regions; the uniform block is zero.
     */
    constructor();
    /**
     * Packs the regions in use under `config` into the uniform block; returns a status. A
     * refusal leaves the block unchanged.
     */
    pack(config: OverlayConfig): number;
    /**
     * Uses the first `count` regions (0 ..= 8); returns a status.
     */
    setCount(count: number): number;
    /**
     * Writes region `index` (0 ..= 7) in viewport fractions, origin top-left; `mode` is
     * `OverlayMode` bits. Values are checked by `pack` and `FlashAnalyzer.setOverlay`.
     */
    setRegion(index: number, x: number, y: number, width: number, height: number, cornerRadius: number, feather: number, strength: number, mode: number): number;
    /**
     * Size of the block in bytes, 320.
     */
    uniformsBytes(): number;
    /**
     * Byte offset of the packed `OverlayUniforms` block.
     */
    uniformsPtr(): number;
    /**
     * Regions in use, the first `count` of the array.
     */
    readonly count: number;
}

/**
 * Explicit uniform block packer (`OscilluxPacker`) with preallocated float64 host values and
 * a `blockSize`-byte little-endian float32 output block.
 */
export class Packer {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Size of the packed block, bytes.
     */
    blockSize(): number;
    /**
     * Byte offset of the float64 host values.
     */
    hostPtr(): number;
    /**
     * Validates the layout (at most 256 fields) for a `blockSize`-byte block and allocates
     * room for `hostCapacity` host values.
     */
    constructor(fields: readonly UniformField[], blockSize: number, hostCapacity: number);
    /**
     * Byte offset of the packed block.
     */
    outputPtr(): number;
    /**
     * Packs `mapper`'s last output and the first `hostCount` host values; returns a status.
     * A refusal leaves the block unchanged.
     */
    pack(mapper: Mapper, hostCount: number): number;
}

/**
 * Reaction sampler configuration (`OscilluxReactionConfig`); every time is in ms. The
 * constructor writes the defaults with the analysis latencies of DSP `behaviorVersion` at
 * `sampleRateHz` and throws for a version without measured latencies or a zero rate.
 */
export class ReactionConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor(behaviorVersion: number, sampleRateHz: number);
    /**
     * r" Hop time to the audio its continuous values describe.
     */
    analysisLatencyMs: number;
    /**
     * r" Release time constant of the bass impact.
     */
    bassImpactReleaseMs: number;
    /**
     * r" Hops queued, 1 ..= 65536.
     */
    capacity: number;
    /**
     * r" Frame presented to light emitted.
     */
    displayLatencyMs: number;
    /**
     * r" Release time constant of the flux.
     */
    fluxReleaseMs: number;
    /**
     * r" Hop time to the audio its transients describe.
     */
    onsetLatencyMs: number;
    /**
     * r" Release time constant of the onset.
     */
    onsetReleaseMs: number;
    /**
     * r" Audio written at the host time to audio heard.
     */
    outputLatencyMs: number;
    /**
     * r" Share of an onset's phase error taken, 0 ..= 1.
     */
    phaseLockGain: number;
    /**
     * r" Beat pulse decay time constant after the beat.
     */
    pulseDecayMs: number;
    /**
     * r" Beat pulse Gaussian rise width before the beat.
     */
    pulseRiseMs: number;
    /**
     * r" Sample time to frame presentation.
     */
    renderAheadMs: number;
}

/**
 * Reaction sampler (`OscilluxReactionSampler`) with a preallocated `OscilluxReaction` output:
 * the `OscilluxMapped` layout of `Mapper.outputPtr` (bytes 0 .. 328), then little-endian
 * Float64 at 328 (beat), 336 (downbeat), 344 (beatPhase), 352 (barPhase), 360 (tempoBpm), 368
 * (confidence), Int64 at 376 (beatCount) and Uint32 at 384 (beatInBar) and 388 (locked).
 * `push`, `pushWithBeat`, `sample` and the accessors do not allocate.
 */
export class ReactionSampler {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Output bar phase, [0, 1).
     */
    barPhase(): number;
    /**
     * Beats counted at output wraps.
     */
    beatCount(): number;
    /**
     * Index of the current beat within the bar; 0 is the downbeat.
     */
    beatInBar(): number;
    /**
     * Output beat phase, [0, 1).
     */
    beatPhase(): number;
    /**
     * Beat pulse, [0, 1]; 1 at the beat's photon time.
     */
    beat(): number;
    /**
     * Returns the beat outputs to the tracker; returns a status.
     */
    clearBeatClock(): number;
    /**
     * Tracker confidence; 1 with a host clock; 0 without a reference.
     */
    confidence(): number;
    /**
     * Beat pulse of downbeats only, [0, 1].
     */
    downbeat(): number;
    /**
     * Whether a reference drives the beat outputs.
     */
    locked(): boolean;
    constructor(config: ReactionConfig);
    /**
     * Byte offset of the `OscilluxReaction` output.
     */
    outputPtr(): number;
    /**
     * As `push`, with the tracker output of frame `index` of `dsp`'s last `pushExt` or
     * `pushExtF64`; invalid after any other push.
     */
    pushWithBeat(mediaTimeMs: number, mapper: Mapper, dsp: Dsp, index: number): number;
    /**
     * Queues `mapper`'s last output as the hop whose analysis window ends at `mediaTimeMs`,
     * without tracker output; returns a status.
     */
    push(mediaTimeMs: number, mapper: Mapper): number;
    /**
     * Hops queued and not yet consumed.
     */
    queued(): number;
    /**
     * Forgets every hop, held peak, the beat clock and the beat state; returns a status.
     */
    reset(): number;
    /**
     * Writes the values seen at the photon time of a frame sampled at host media time `timeMs`
     * to the output; returns a status.
     */
    sample(timeMs: number): number;
    /**
     * Drives the beat outputs from `clock` instead of the tracker; returns a status.
     */
    setBeatClock(clock: BeatClock): number;
    /**
     * Spectrum bin `index` (0 ..= 63) from the last sample, or undefined.
     */
    spectrum(index: number): number | undefined;
    /**
     * Tempo of the reference clock; 0 without one.
     */
    tempoBpm(): number;
    /**
     * Mapped value `index` (a `MappedValue`) from the last sample, or undefined.
     */
    value(index: number): number | undefined;
}

/**
 * Transition scheduler (`OscilluxTransition`). `request` and `update` take the clock as
 * numbers; the getters read the status of the last successful call.
 */
export class Transition {
    free(): void;
    [Symbol.dispose](): void;
    constructor(config: TransitionConfig);
    /**
     * Schedules a switch requested at `timeSeconds`; quantise 0 none, 1 beat, 2 bar, waiting
     * at most `maxWaitSeconds` past the pre-roll. Returns a status; a refusal changes nothing.
     */
    request(timeSeconds: number, beatPhase: number, barPhase: number, tempoBpm: number, confidence: number, quantise: number, maxWaitSeconds: number): number;
    /**
     * Returns to idle and restarts the auto-advance count.
     */
    reset(): number;
    /**
     * Evaluates the schedule at `timeSeconds` without changing it.
     */
    status(timeSeconds: number): number;
    /**
     * Per-frame evaluation at `timeSeconds`, issuing an automatic request when one is due.
     */
    update(timeSeconds: number, beatPhase: number, barPhase: number, tempoBpm: number, confidence: number): number;
    /**
     * True when the last update issued an automatic request.
     */
    readonly autoRequested: boolean;
    /**
     * Time the blend ends, s.
     */
    readonly endSeconds: number;
    /**
     * Blend progress in [0, 1]; exactly 1 from the end of the blend.
     */
    readonly progress: number;
    /**
     * Quantisation applied to the start: 0 none, 1 beat, 2 bar.
     */
    readonly quantise: number;
    /**
     * Time of the request, s.
     */
    readonly requestSeconds: number;
    /**
     * Time the blend starts, s.
     */
    readonly startSeconds: number;
    /**
     * `OSCILLUX_TRANSITION_STATE_*`: 0 idle, 1 pre-roll, 2 waiting, 3 blending, 4 done.
     */
    readonly state: number;
}

/**
 * Transition scheduler parameters (`OscilluxTransitionConfig`); the constructor writes the
 * reference values.
 */
export class TransitionConfig {
    free(): void;
    [Symbol.dispose](): void;
    constructor();
    /**
     * r" Bars between automatic switches; 0 disables auto-advance.
     */
    autoAdvanceBars: number;
    /**
     * r" Beats per bar, 1 to 16.
     */
    beatsPerBar: number;
    /**
     * r" Blend length in beats on a confident clock, clamped to 0.8 to 2.5 s.
     */
    durationBeats: number;
    /**
     * r" Progress easing: 0 linear, 1 smoothstep.
     */
    easing: number;
    /**
     * r" Blend length without a confident beat, s; 0.8 to 2.5.
     */
    fallbackSeconds: number;
    /**
     * r" Lowest clock confidence that counts as a beat.
     */
    minConfidence: number;
    /**
     * r" Time the incoming visual runs offscreen before it may start, s.
     */
    preRollSeconds: number;
}

/**
 * Returns `OSCILLUX_ABI_VERSION` of the linked core.
 */
export function abiVersion(): number;

/**
 * The `z` component of the vivid palette tour at `timeSeconds` for a visual whose palette
 * starts at `offset`; the `palette` uniform is `[-1, 0, z, 0]` (`oscillux_palette_tour`).
 * Throws on a non-finite argument. Does not allocate, so hosts may call it every frame.
 */
export function paletteTour(time_seconds: number, offset: number): number;

/**
 * Short English description of a status code (0 ok, -1 null, -2 invalid argument, -3 panic).
 */
export function statusString(status: number): string;

/**
 * Release version of the linked core, packed as `major << 16 | minor << 8 | patch`.
 */
export function version(): number;

/**
 * The vivid present pass's `[saturation, vibrance]` (`oscillux_palette_vivid_present`).
 * Allocates the returned array; read it once, not per frame.
 */
export function vividPresent(): Float64Array;

/**
 * The wasm memory every `*Ptr()` offset refers to. Obtain views after constructing adapters
 * and recreate them after any memory growth.
 */
export function wasmMemory(): WebAssembly.Memory;

export type InitInput = RequestInfo | URL | Response | BufferSource | WebAssembly.Module;

export interface InitOutput {
    readonly memory: WebAssembly.Memory;
    readonly oscillux_reaction_destroy: (a: number) => void;
    readonly oscillux_cdg_destroy: (a: number) => void;
    readonly oscillux_dsp_destroy: (a: number) => void;
    readonly oscillux_packer_destroy: (a: number) => void;
    readonly oscillux_beatgrid_destroy: (a: number) => void;
    readonly oscillux_beatgrid_config_validate: (a: number, b: number) => number;
    readonly oscillux_dsp_versioned_config_validate: (a: number, b: number) => number;
    readonly __wbg_beatclock_free: (a: number, b: number) => void;
    readonly __wbg_beatgrid_free: (a: number, b: number) => void;
    readonly __wbg_beatgridconfig_free: (a: number, b: number) => void;
    readonly __wbg_cdg_free: (a: number, b: number) => void;
    readonly __wbg_curve_free: (a: number, b: number) => void;
    readonly __wbg_curveconfig_free: (a: number, b: number) => void;
    readonly __wbg_curveinput_free: (a: number, b: number) => void;
    readonly __wbg_dsp_free: (a: number, b: number) => void;
    readonly __wbg_dspconfig_free: (a: number, b: number) => void;
    readonly __wbg_flashanalyzer_free: (a: number, b: number) => void;
    readonly __wbg_flashconfig_free: (a: number, b: number) => void;
    readonly __wbg_flashoverlay_free: (a: number, b: number) => void;
    readonly __wbg_flashoverlayreport_free: (a: number, b: number) => void;
    readonly __wbg_governor_free: (a: number, b: number) => void;
    readonly __wbg_governorconfig_free: (a: number, b: number) => void;
    readonly __wbg_limiter_free: (a: number, b: number) => void;
    readonly __wbg_limiterpolicy_free: (a: number, b: number) => void;
    readonly __wbg_mapper_free: (a: number, b: number) => void;
    readonly __wbg_mappingconfig_free: (a: number, b: number) => void;
    readonly __wbg_overlayconfig_free: (a: number, b: number) => void;
    readonly __wbg_overlayregions_free: (a: number, b: number) => void;
    readonly __wbg_packer_free: (a: number, b: number) => void;
    readonly __wbg_reactionconfig_free: (a: number, b: number) => void;
    readonly __wbg_reactionsampler_free: (a: number, b: number) => void;
    readonly __wbg_transition_free: (a: number, b: number) => void;
    readonly __wbg_transitionconfig_free: (a: number, b: number) => void;
    readonly abiVersion: () => number;
    readonly beatclock_bar_phase: (a: number) => number;
    readonly beatclock_beat_index: (a: number) => number;
    readonly beatclock_beat_phase: (a: number) => number;
    readonly beatclock_beats_per_bar: (a: number) => number;
    readonly beatclock_media_time_ms: (a: number) => number;
    readonly beatclock_new: () => number;
    readonly beatclock_set_bar_phase: (a: number, b: number) => void;
    readonly beatclock_set_beat_index: (a: number, b: number) => void;
    readonly beatclock_set_beat_phase: (a: number, b: number) => void;
    readonly beatclock_set_beats_per_bar: (a: number, b: number) => void;
    readonly beatclock_set_media_time_ms: (a: number, b: number) => void;
    readonly beatclock_set_tempo_bpm: (a: number, b: number) => void;
    readonly beatclock_tempo_bpm: (a: number) => number;
    readonly beatgrid_bar_count: (a: number) => number;
    readonly beatgrid_bar_phase: (a: number) => number;
    readonly beatgrid_beatBytes: () => number;
    readonly beatgrid_beat_count: (a: number) => number;
    readonly oscillux_beatgrid_clock: (a: number, b: number, c: number) => number;
    readonly beatgrid_beat_in_bar: (a: number) => number;
    readonly beatgrid_beat_index: (a: number) => number;
    readonly beatgrid_beat_phase: (a: number) => number;
    readonly beatgrid_beatsPtr: (a: number) => number;
    readonly oscillux_beatgrid_create: (a: number, b: number, c: number, d: number) => number;
    readonly oscillux_beatgrid_copy_beats: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly beatgrid_beats_per_bar: (a: number) => number;
    readonly beatgrid_clock: (a: number, b: number) => number;
    readonly beatgrid_confidence: (a: number) => number;
    readonly beatgrid_duration_seconds: (a: number) => number;
    readonly beatgrid_local_tempo_bpm: (a: number) => number;
    readonly beatgrid_new: (a: number, b: number, c: number) => [number, number, number];
    readonly beatgrid_tempo_bpm: (a: number) => number;
    readonly beatgridconfig_beats_per_bar: (a: number) => number;
    readonly oscillux_cdg_state: (a: number, b: number) => number;
    readonly beatgridconfig_channels: (a: number) => number;
    readonly beatgridconfig_max_bpm: (a: number) => number;
    readonly beatgridconfig_min_bpm: (a: number) => number;
    readonly beatgridconfig_new: () => number;
    readonly beatgridconfig_sample_rate_hz: (a: number) => number;
    readonly beatgridconfig_set_beats_per_bar: (a: number, b: number) => void;
    readonly beatgridconfig_set_channels: (a: number, b: number) => void;
    readonly beatgridconfig_set_max_bpm: (a: number, b: number) => void;
    readonly beatgridconfig_set_min_bpm: (a: number, b: number) => void;
    readonly beatgridconfig_set_sample_rate_hz: (a: number, b: number) => void;
    readonly oscillux_cdg_index_counts: (a: number, b: number, c: number, d: number) => number;
    readonly beatgridconfig_validate: (a: number) => [number, number];
    readonly cdg_advanceToPack: (a: number, b: number) => number;
    readonly cdg_advanceToTime: (a: number, b: number) => number;
    readonly cdg_applied: (a: number) => number;
    readonly cdg_border_colour: (a: number) => number;
    readonly cdg_changes: (a: number) => number;
    readonly cdg_colour: (a: number, b: number) => [number, number, number];
    readonly cdg_count: (a: number, b: number) => [number, number, number];
    readonly cdg_dirty_x0: (a: number) => number;
    readonly oscillux_cdg_window_rgba: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly cdg_dirty_x1: (a: number) => number;
    readonly cdg_dirty_y0: (a: number) => number;
    readonly cdg_dirty_y1: (a: number) => number;
    readonly oscillux_curve_evaluate: (a: number, b: number, c: number) => number;
    readonly cdg_h_offset: (a: number) => number;
    readonly cdg_indexCounts: (a: number, b: number) => number;
    readonly cdg_new: (a: number, b: number) => [number, number, number];
    readonly cdg_pack_count: (a: number) => number;
    readonly cdg_preset_colour: (a: number) => number;
    readonly cdg_rgbaPtr: (a: number) => number;
    readonly cdg_screen: (a: number) => number;
    readonly cdg_screenPtr: (a: number) => number;
    readonly cdg_state: (a: number) => number;
    readonly cdg_transparency: (a: number, b: number) => [number, number, number];
    readonly cdg_v_offset: (a: number) => number;
    readonly cdg_windowRgba: (a: number, b: number, c: number) => number;
    readonly cdg_window_x: (a: number) => number;
    readonly cdg_window_y: (a: number) => number;
    readonly curve_evaluate: (a: number, b: number) => [number, number];
    readonly curve_new: (a: number) => [number, number, number];
    readonly curveconfig_attack: (a: number) => number;
    readonly curveconfig_duration: (a: number) => number;
    readonly curveconfig_easing: (a: number) => number;
    readonly curveconfig_flags: (a: number) => number;
    readonly curveconfig_from: (a: number) => number;
    readonly curveconfig_kind: (a: number) => number;
    readonly curveconfig_max: (a: number) => number;
    readonly curveconfig_min: (a: number) => number;
    readonly curveconfig_new: () => number;
    readonly curveconfig_period: (a: number) => number;
    readonly curveconfig_phase: (a: number) => number;
    readonly oscillux_dsp_begin_segment: (a: number, b: number, c: number) => number;
    readonly curveconfig_release: (a: number) => number;
    readonly curveconfig_release_at: (a: number) => number;
    readonly curveconfig_set_attack: (a: number, b: number) => void;
    readonly curveconfig_set_duration: (a: number, b: number) => void;
    readonly curveconfig_set_easing: (a: number, b: number) => void;
    readonly curveconfig_set_flags: (a: number, b: number) => void;
    readonly curveconfig_set_from: (a: number, b: number) => void;
    readonly oscillux_dsp_create_versioned: (a: number, b: number) => number;
    readonly curveconfig_set_kind: (a: number, b: number) => void;
    readonly curveconfig_set_max: (a: number, b: number) => void;
    readonly oscillux_dsp_push_ext_f64: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number) => number;
    readonly curveconfig_set_min: (a: number, b: number) => void;
    readonly oscillux_dsp_push_ext_f32: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number) => number;
    readonly curveconfig_set_period: (a: number, b: number) => void;
    readonly oscillux_dsp_push_f64: (a: number, b: number, c: number, d: number, e: number, f: number, g: number) => number;
    readonly curveconfig_set_phase: (a: number, b: number) => void;
    readonly oscillux_dsp_push_f32: (a: number, b: number, c: number, d: number, e: number, f: number, g: number) => number;
    readonly curveconfig_set_release: (a: number, b: number) => void;
    readonly curveconfig_set_release_at: (a: number, b: number) => void;
    readonly curveconfig_set_shape: (a: number, b: number) => void;
    readonly curveconfig_set_smoothing_seconds: (a: number, b: number) => void;
    readonly curveconfig_set_to: (a: number, b: number) => void;
    readonly curveconfig_set_unit: (a: number, b: number) => void;
    readonly curveconfig_set_value: (a: number, b: number) => void;
    readonly curveconfig_shape: (a: number) => number;
    readonly curveconfig_smoothing_seconds: (a: number) => number;
    readonly curveconfig_to: (a: number) => number;
    readonly curveconfig_unit: (a: number) => number;
    readonly curveconfig_value: (a: number) => number;
    readonly curveinput_beat_phase: (a: number) => number;
    readonly curveinput_delta_seconds: (a: number) => number;
    readonly curveinput_elapsed_beats: (a: number) => number;
    readonly curveinput_elapsed_seconds: (a: number) => number;
    readonly curveinput_has_previous: (a: number) => number;
    readonly curveinput_new: () => number;
    readonly curveinput_previous_value: (a: number) => number;
    readonly oscillux_flash_analyzer_create: (a: number, b: number) => number;
    readonly curveinput_set_beat_phase: (a: number, b: number) => void;
    readonly curveinput_set_delta_seconds: (a: number, b: number) => void;
    readonly curveinput_set_elapsed_beats: (a: number, b: number) => void;
    readonly curveinput_set_elapsed_seconds: (a: number, b: number) => void;
    readonly curveinput_set_has_previous: (a: number, b: number) => void;
    readonly curveinput_set_previous_value: (a: number, b: number) => void;
    readonly dsp_beginSegment: (a: number, b: number, c: number) => number;
    readonly dsp_consumed: (a: number) => number;
    readonly dsp_extBytes: (a: number) => number;
    readonly dsp_extPtr: (a: number) => number;
    readonly dsp_frameBytes: (a: number) => number;
    readonly dsp_inputF64Ptr: (a: number) => number;
    readonly dsp_inputPtr: (a: number) => number;
    readonly dsp_new: (a: number, b: number, c: number, d: number) => [number, number, number];
    readonly dsp_outputPtr: (a: number) => number;
    readonly dsp_produced: (a: number) => number;
    readonly dsp_push: (a: number, b: number, c: number) => number;
    readonly dsp_pushExt: (a: number, b: number, c: number) => number;
    readonly oscillux_governor_create: (a: number, b: number) => number;
    readonly dsp_pushExtF64: (a: number, b: number, c: number) => number;
    readonly oscillux_governor_record: (a: number, b: number, c: number) => number;
    readonly dsp_pushF64: (a: number, b: number, c: number) => number;
    readonly dspconfig_ballistics_attack_ms: (a: number) => number;
    readonly dspconfig_ballistics_release_ms: (a: number) => number;
    readonly dspconfig_behavior_version: (a: number) => number;
    readonly dspconfig_db_floor: (a: number) => number;
    readonly dspconfig_fft_window_samples: (a: number) => number;
    readonly dspconfig_gap_tolerance_hops: (a: number) => number;
    readonly dspconfig_hop_samples: (a: number) => number;
    readonly dspconfig_loudness_reference_lufs: (a: number) => number;
    readonly dspconfig_new: () => number;
    readonly dspconfig_onsetBandGroup: (a: number, b: number) => [number, number, number, number];
    readonly dspconfig_onset_peak_decay: (a: number) => number;
    readonly oscillux_limiter_create: (a: number, b: number) => number;
    readonly dspconfig_onset_peak_floor: (a: number) => number;
    readonly dspconfig_rms_floor: (a: number) => number;
    readonly oscillux_limiter_process: (a: number, b: number, c: number) => number;
    readonly dspconfig_rms_window_ms: (a: number) => number;
    readonly dspconfig_sample_rate_hz: (a: number) => number;
    readonly dspconfig_setOnsetBandGroup: (a: number, b: number, c: number, d: number) => [number, number];
    readonly dspconfig_set_ballistics_attack_ms: (a: number, b: number) => void;
    readonly dspconfig_set_ballistics_release_ms: (a: number, b: number) => void;
    readonly oscillux_mapping_create: (a: number, b: number) => number;
    readonly dspconfig_set_behavior_version: (a: number, b: number) => void;
    readonly dspconfig_set_db_floor: (a: number, b: number) => void;
    readonly dspconfig_set_fft_window_samples: (a: number, b: number) => void;
    readonly dspconfig_set_gap_tolerance_hops: (a: number, b: number) => void;
    readonly dspconfig_set_hop_samples: (a: number, b: number) => void;
    readonly dspconfig_set_loudness_reference_lufs: (a: number, b: number) => void;
    readonly dspconfig_set_onset_peak_decay: (a: number, b: number) => void;
    readonly dspconfig_set_onset_peak_floor: (a: number, b: number) => void;
    readonly dspconfig_set_rms_floor: (a: number, b: number) => void;
    readonly dspconfig_set_rms_window_ms: (a: number, b: number) => void;
    readonly dspconfig_set_sample_rate_hz: (a: number, b: number) => void;
    readonly dspconfig_set_spectrum_bands: (a: number, b: number) => void;
    readonly dspconfig_set_spectrum_max_hz: (a: number, b: number) => void;
    readonly dspconfig_set_spectrum_min_hz: (a: number, b: number) => void;
    readonly oscillux_overlay_presentation_intensity: (a: number, b: number) => number;
    readonly dspconfig_spectrum_bands: (a: number) => number;
    readonly dspconfig_spectrum_max_hz: (a: number) => number;
    readonly dspconfig_spectrum_min_hz: (a: number) => number;
    readonly oscillux_overlay_flash_config: (a: number, b: number) => number;
    readonly dspconfig_validate: (a: number) => [number, number];
    readonly flashanalyzer_admit: (a: number, b: number) => number;
    readonly oscillux_overlay_pack: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly flashanalyzer_admitRgb: (a: number, b: number) => number;
    readonly flashanalyzer_admitted: (a: number) => number;
    readonly flashanalyzer_clearOverlay: (a: number) => number;
    readonly flashanalyzer_colorsPtr: (a: number) => number;
    readonly flashanalyzer_exceeds: (a: number) => number;
    readonly oscillux_packer_create: (a: number, b: number, c: number, d: number) => number;
    readonly flashanalyzer_flashes_in_window: (a: number) => number;
    readonly flashanalyzer_general_regions: (a: number) => number;
    readonly flashanalyzer_general_transition: (a: number) => number;
    readonly flashanalyzer_measure: (a: number, b: number) => number;
    readonly flashanalyzer_measureRgb: (a: number, b: number) => number;
    readonly flashanalyzer_new: (a: number) => [number, number, number];
    readonly flashanalyzer_overlayReport: (a: number, b: number) => number;
    readonly flashanalyzer_red_flashes_in_window: (a: number) => number;
    readonly flashanalyzer_red_regions: (a: number) => number;
    readonly flashanalyzer_red_transition: (a: number) => number;
    readonly flashanalyzer_regionCount: (a: number) => number;
    readonly flashanalyzer_regionsPtr: (a: number) => number;
    readonly flashanalyzer_reportPtr: (a: number) => number;
    readonly oscillux_reaction_create: (a: number, b: number) => number;
    readonly flashanalyzer_reset: (a: number) => number;
    readonly oscillux_reaction_push: (a: number, b: number, c: number, d: number) => number;
    readonly flashanalyzer_setOverlay: (a: number, b: number, c: number) => number;
    readonly flashanalyzer_setRegionFromLinearRgb: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly flashanalyzer_time_ms: (a: number) => number;
    readonly flashconfig_columns: (a: number) => number;
    readonly oscillux_reaction_sample: (a: number, b: number, c: number) => number;
    readonly flashconfig_field_height: (a: number) => number;
    readonly flashconfig_field_width: (a: number) => number;
    readonly flashconfig_max_field_area: (a: number) => number;
    readonly flashconfig_max_flash_hz: (a: number) => number;
    readonly flashconfig_max_red_flashes: (a: number) => number;
    readonly flashconfig_new: () => number;
    readonly oscillux_transition_create: (a: number, b: number) => number;
    readonly flashconfig_red_rule: (a: number) => number;
    readonly flashconfig_rows: (a: number) => number;
    readonly flashconfig_set_columns: (a: number, b: number) => void;
    readonly flashconfig_set_field_height: (a: number, b: number) => void;
    readonly oscillux_transition_update: (a: number, b: number, c: number) => number;
    readonly flashconfig_set_field_width: (a: number, b: number) => void;
    readonly flashconfig_set_max_field_area: (a: number, b: number) => void;
    readonly flashconfig_set_max_flash_hz: (a: number, b: number) => void;
    readonly oscillux_beatgrid_beat_count: (a: number, b: number) => number;
    readonly oscillux_beatgrid_config_default: (a: number) => number;
    readonly oscillux_beatgrid_info: (a: number, b: number) => number;
    readonly oscillux_cdg_advance_to_pack: (a: number, b: bigint) => number;
    readonly oscillux_cdg_advance_to_time: (a: number, b: number) => number;
    readonly oscillux_cdg_create: (a: number, b: number, c: number) => number;
    readonly oscillux_cdg_screen: (a: number, b: number, c: number) => number;
    readonly oscillux_curve_config_default: (a: number) => number;
    readonly oscillux_curve_create: (a: number, b: number) => number;
    readonly oscillux_curve_destroy: (a: number) => void;
    readonly oscillux_dsp_config_default: (a: number) => number;
    readonly oscillux_dsp_config_validate: (a: number, b: number) => number;
    readonly oscillux_dsp_create: (a: number, b: number) => number;
    readonly oscillux_dsp_versioned_config_default: (a: number) => number;
    readonly oscillux_flash_analyzer_admit: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly oscillux_flash_analyzer_admit_rgb: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly oscillux_flash_analyzer_destroy: (a: number) => void;
    readonly oscillux_flash_analyzer_measure: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly oscillux_flash_analyzer_measure_rgb: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly oscillux_flash_analyzer_overlay_report: (a: number, b: number) => number;
    readonly oscillux_flash_analyzer_reset: (a: number) => number;
    readonly oscillux_flash_analyzer_set_overlay: (a: number, b: number, c: number, d: number) => number;
    readonly oscillux_flash_config_default: (a: number) => number;
    readonly oscillux_flash_overlay_default: (a: number) => number;
    readonly oscillux_flash_region_from_linear_rgb: (a: number, b: number, c: number, d: number) => number;
    readonly oscillux_governor_config_default: (a: number) => number;
    readonly oscillux_governor_destroy: (a: number) => void;
    readonly oscillux_governor_reset: (a: number, b: number) => number;
    readonly oscillux_limiter_policy_default: (a: number) => number;
    readonly oscillux_limiter_reset: (a: number) => number;
    readonly oscillux_mapping_config_default: (a: number) => number;
    readonly oscillux_mapping_config_from_dsp: (a: number, b: number) => number;
    readonly oscillux_mapping_destroy: (a: number) => void;
    readonly oscillux_mapping_process: (a: number, b: number, c: number, d: number) => number;
    readonly oscillux_mapping_process_ext: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly oscillux_mapping_reset: (a: number) => number;
    readonly oscillux_mapping_set_level_source: (a: number, b: number) => number;
    readonly oscillux_overlay_config_default: (a: number) => number;
    readonly oscillux_packer_pack: (a: number, b: number, c: number, d: number, e: number, f: number) => number;
    readonly oscillux_palette_tour: (a: number, b: number, c: number) => number;
    readonly oscillux_palette_vivid_present: (a: number) => number;
    readonly oscillux_reaction_config_default: (a: number, b: number, c: number) => number;
    readonly oscillux_reaction_queued: (a: number, b: number) => number;
    readonly oscillux_reaction_reset: (a: number) => number;
    readonly oscillux_reaction_set_beat_clock: (a: number, b: number) => number;
    readonly oscillux_status_string: (a: number) => number;
    readonly oscillux_transition_config_default: (a: number) => number;
    readonly oscillux_transition_destroy: (a: number) => void;
    readonly oscillux_transition_request: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly oscillux_transition_reset: (a: number) => number;
    readonly oscillux_transition_status: (a: number, b: number, c: number) => number;
    readonly oscillux_version: () => number;
    readonly oscillux_window_governor_config_default: (a: number) => number;
    readonly oscillux_window_governor_create: (a: number, b: number, c: number) => number;
    readonly oscillux_window_governor_destroy: (a: number) => void;
    readonly oscillux_window_governor_record: (a: number, b: number, c: number) => number;
    readonly oscillux_window_governor_reset: (a: number, b: number) => number;
    readonly flashconfig_set_max_red_flashes: (a: number, b: number) => void;
    readonly flashconfig_set_red_rule: (a: number, b: number) => void;
    readonly flashconfig_set_rows: (a: number, b: number) => void;
    readonly flashoverlay_darker_limit: (a: number) => number;
    readonly flashoverlay_luminance_change: (a: number) => number;
    readonly flashoverlay_max_area: (a: number) => number;
    readonly flashoverlay_max_flash_hz: (a: number) => number;
    readonly flashoverlay_new: () => number;
    readonly flashoverlay_set_darker_limit: (a: number, b: number) => void;
    readonly flashoverlay_set_luminance_change: (a: number, b: number) => void;
    readonly flashoverlay_set_max_area: (a: number, b: number) => void;
    readonly flashoverlay_set_max_flash_hz: (a: number, b: number) => void;
    readonly flashoverlayreport_active: (a: number) => number;
    readonly flashoverlayreport_cells: (a: number) => number;
    readonly flashoverlayreport_exceeds: (a: number) => number;
    readonly flashoverlayreport_flashes_in_window: (a: number) => number;
    readonly flashoverlayreport_new: () => number;
    readonly flashoverlayreport_regions: (a: number) => number;
    readonly flashoverlayreport_transition: (a: number) => number;
    readonly governor_ema_ms: (a: number) => [number, number];
    readonly governor_level: (a: number) => number;
    readonly governor_new: (a: number) => [number, number, number];
    readonly governor_record: (a: number, b: number) => number;
    readonly governor_refresh_ms: (a: number) => number;
    readonly governor_reset: (a: number, b: number) => number;
    readonly governorconfig_demote_factor: (a: number) => number;
    readonly governorconfig_demote_frames: (a: number) => number;
    readonly governorconfig_ema_alpha: (a: number) => number;
    readonly governorconfig_initial_level: (a: number) => number;
    readonly governorconfig_max_level: (a: number) => number;
    readonly governorconfig_new: () => number;
    readonly governorconfig_pause_gap_ms: (a: number) => number;
    readonly governorconfig_promote_frames: (a: number) => number;
    readonly governorconfig_promote_refresh_factor: (a: number) => number;
    readonly governorconfig_promote_target_factor: (a: number) => number;
    readonly governorconfig_refresh_drift: (a: number) => number;
    readonly governorconfig_refresh_floor_ms: (a: number) => number;
    readonly governorconfig_sample_cap_factor: (a: number) => number;
    readonly governorconfig_set_demote_factor: (a: number, b: number) => void;
    readonly governorconfig_set_demote_frames: (a: number, b: number) => void;
    readonly governorconfig_set_ema_alpha: (a: number, b: number) => void;
    readonly governorconfig_set_initial_level: (a: number, b: number) => void;
    readonly governorconfig_set_max_level: (a: number, b: number) => void;
    readonly governorconfig_set_pause_gap_ms: (a: number, b: number) => void;
    readonly governorconfig_set_promote_frames: (a: number, b: number) => void;
    readonly governorconfig_set_promote_refresh_factor: (a: number, b: number) => void;
    readonly governorconfig_set_promote_target_factor: (a: number, b: number) => void;
    readonly governorconfig_set_refresh_drift: (a: number, b: number) => void;
    readonly governorconfig_set_refresh_floor_ms: (a: number, b: number) => void;
    readonly governorconfig_set_sample_cap_factor: (a: number, b: number) => void;
    readonly governorconfig_set_settle_frames: (a: number, b: number) => void;
    readonly governorconfig_set_target_ms: (a: number, b: number) => void;
    readonly governorconfig_settle_frames: (a: number) => number;
    readonly governorconfig_target_ms: (a: number) => number;
    readonly limiter_inputPtr: (a: number) => number;
    readonly limiter_new: (a: number) => [number, number, number];
    readonly limiter_outputPtr: (a: number) => number;
    readonly limiter_process: (a: number) => number;
    readonly limiter_reset: (a: number) => number;
    readonly limiterpolicy_max_flash_area: (a: number) => number;
    readonly limiterpolicy_max_flash_hz: (a: number) => number;
    readonly limiterpolicy_max_luminance_delta: (a: number) => number;
    readonly limiterpolicy_max_red_flashes: (a: number) => number;
    readonly limiterpolicy_new: () => number;
    readonly limiterpolicy_set_max_flash_area: (a: number, b: number) => void;
    readonly limiterpolicy_set_max_flash_hz: (a: number, b: number) => void;
    readonly limiterpolicy_set_max_luminance_delta: (a: number, b: number) => void;
    readonly limiterpolicy_set_max_red_flashes: (a: number, b: number) => void;
    readonly limiterpolicy_set_strobe_banned: (a: number, b: number) => void;
    readonly limiterpolicy_strobe_banned: (a: number) => number;
    readonly mapper_extInputPtr: (a: number) => number;
    readonly mapper_frameInputPtr: (a: number) => number;
    readonly mapper_new: (a: number) => [number, number, number];
    readonly mapper_outputPtr: (a: number) => number;
    readonly mapper_process: (a: number, b: number, c: number, d: number) => number;
    readonly mapper_processExt: (a: number, b: number, c: number, d: number) => number;
    readonly mapper_processFrame: (a: number, b: number) => number;
    readonly mapper_processFrameExt: (a: number, b: number) => number;
    readonly mapper_reset: (a: number) => number;
    readonly oscillux_limiter_destroy: (a: number) => void;
    readonly mapper_setLevelSource: (a: number, b: number) => number;
    readonly mapper_spectrum: (a: number, b: number) => number;
    readonly mapper_value: (a: number, b: number) => [number, number];
    readonly oscillux_abi_version: () => number;
    readonly mappingconfig_centroid_floor: (a: number) => number;
    readonly mappingconfig_db_floor: (a: number) => number;
    readonly mappingconfig_fromDsp: (a: number) => [number, number, number];
    readonly mappingconfig_group: (a: number, b: number) => [number, number, number, number];
    readonly mappingconfig_hop_seconds: (a: number) => number;
    readonly mappingconfig_loudness_reference: (a: number) => number;
    readonly mappingconfig_new: () => number;
    readonly mappingconfig_release_seconds: (a: number) => number;
    readonly mappingconfig_setGroup: (a: number, b: number, c: number, d: number) => [number, number];
    readonly mappingconfig_set_centroid_floor: (a: number, b: number) => void;
    readonly mappingconfig_set_db_floor: (a: number, b: number) => void;
    readonly mappingconfig_set_hop_seconds: (a: number, b: number) => void;
    readonly mappingconfig_set_loudness_reference: (a: number, b: number) => void;
    readonly mappingconfig_set_release_seconds: (a: number, b: number) => void;
    readonly overlayconfig_alpha: (a: number) => number;
    readonly overlayconfig_contrast_ratio: (a: number) => number;
    readonly overlayconfig_frame_interval_ms: (a: number) => number;
    readonly overlayconfig_intensity: (a: number) => number;
    readonly overlayconfig_new: () => number;
    readonly overlayconfig_output_levels: (a: number) => number;
    readonly overlayconfig_premultiplied_alpha: (a: number) => number;
    readonly overlayconfig_presentationIntensity: (a: number) => [number, number, number];
    readonly overlayconfig_reduced_motion: (a: number) => number;
    readonly overlayconfig_set_alpha: (a: number, b: number) => void;
    readonly overlayconfig_set_contrast_ratio: (a: number, b: number) => void;
    readonly overlayconfig_set_frame_interval_ms: (a: number, b: number) => void;
    readonly overlayconfig_set_intensity: (a: number, b: number) => void;
    readonly overlayconfig_set_output_levels: (a: number, b: number) => void;
    readonly overlayconfig_set_premultiplied_alpha: (a: number, b: number) => void;
    readonly overlayconfig_set_reduced_motion: (a: number, b: number) => void;
    readonly overlayconfig_set_viewport_height: (a: number, b: number) => void;
    readonly overlayconfig_set_viewport_width: (a: number, b: number) => void;
    readonly overlayconfig_tightenFlashConfig: (a: number, b: number) => number;
    readonly overlayconfig_viewport_height: (a: number) => number;
    readonly overlayconfig_viewport_width: (a: number) => number;
    readonly overlayregions_count: (a: number) => number;
    readonly overlayregions_new: () => number;
    readonly overlayregions_pack: (a: number, b: number) => number;
    readonly overlayregions_setCount: (a: number, b: number) => number;
    readonly overlayregions_setRegion: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number, i: number, j: number) => number;
    readonly overlayregions_uniformsBytes: (a: number) => number;
    readonly overlayregions_uniformsPtr: (a: number) => number;
    readonly packer_blockSize: (a: number) => number;
    readonly packer_hostPtr: (a: number) => number;
    readonly packer_new: (a: any, b: number, c: number) => [number, number, number];
    readonly packer_outputPtr: (a: number) => number;
    readonly packer_pack: (a: number, b: number, c: number) => number;
    readonly paletteTour: (a: number, b: number) => [number, number, number];
    readonly reactionconfig_analysis_latency_ms: (a: number) => number;
    readonly reactionconfig_bass_impact_release_ms: (a: number) => number;
    readonly reactionconfig_capacity: (a: number) => number;
    readonly reactionconfig_display_latency_ms: (a: number) => number;
    readonly reactionconfig_flux_release_ms: (a: number) => number;
    readonly reactionconfig_new: (a: number, b: number) => [number, number, number];
    readonly reactionconfig_onset_latency_ms: (a: number) => number;
    readonly reactionconfig_onset_release_ms: (a: number) => number;
    readonly reactionconfig_output_latency_ms: (a: number) => number;
    readonly reactionconfig_phase_lock_gain: (a: number) => number;
    readonly reactionconfig_pulse_decay_ms: (a: number) => number;
    readonly reactionconfig_pulse_rise_ms: (a: number) => number;
    readonly reactionconfig_render_ahead_ms: (a: number) => number;
    readonly reactionconfig_set_analysis_latency_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_bass_impact_release_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_capacity: (a: number, b: number) => void;
    readonly reactionconfig_set_display_latency_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_flux_release_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_onset_latency_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_onset_release_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_output_latency_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_phase_lock_gain: (a: number, b: number) => void;
    readonly reactionconfig_set_pulse_decay_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_pulse_rise_ms: (a: number, b: number) => void;
    readonly reactionconfig_set_render_ahead_ms: (a: number, b: number) => void;
    readonly reactionsampler_barPhase: (a: number) => number;
    readonly reactionsampler_beat: (a: number) => number;
    readonly reactionsampler_beatCount: (a: number) => number;
    readonly reactionsampler_beatInBar: (a: number) => number;
    readonly reactionsampler_beatPhase: (a: number) => number;
    readonly reactionsampler_clearBeatClock: (a: number) => number;
    readonly reactionsampler_confidence: (a: number) => number;
    readonly reactionsampler_downbeat: (a: number) => number;
    readonly reactionsampler_locked: (a: number) => number;
    readonly reactionsampler_new: (a: number) => [number, number, number];
    readonly reactionsampler_outputPtr: (a: number) => number;
    readonly reactionsampler_push: (a: number, b: number, c: number) => number;
    readonly reactionsampler_pushWithBeat: (a: number, b: number, c: number, d: number, e: number) => number;
    readonly reactionsampler_queued: (a: number) => number;
    readonly reactionsampler_reset: (a: number) => number;
    readonly reactionsampler_sample: (a: number, b: number) => number;
    readonly reactionsampler_setBeatClock: (a: number, b: number) => number;
    readonly reactionsampler_spectrum: (a: number, b: number) => number;
    readonly reactionsampler_tempoBpm: (a: number) => number;
    readonly reactionsampler_value: (a: number, b: number) => [number, number];
    readonly statusString: (a: number) => [number, number];
    readonly transition_auto_requested: (a: number) => number;
    readonly transition_end_seconds: (a: number) => number;
    readonly transition_new: (a: number) => [number, number, number];
    readonly transition_progress: (a: number) => number;
    readonly transition_quantise: (a: number) => number;
    readonly transition_request: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number) => number;
    readonly transition_request_seconds: (a: number) => number;
    readonly transition_reset: (a: number) => number;
    readonly transition_start_seconds: (a: number) => number;
    readonly transition_state: (a: number) => number;
    readonly transition_status: (a: number, b: number) => number;
    readonly transition_update: (a: number, b: number, c: number, d: number, e: number, f: number) => number;
    readonly transitionconfig_auto_advance_bars: (a: number) => number;
    readonly transitionconfig_beats_per_bar: (a: number) => number;
    readonly transitionconfig_duration_beats: (a: number) => number;
    readonly transitionconfig_easing: (a: number) => number;
    readonly transitionconfig_fallback_seconds: (a: number) => number;
    readonly transitionconfig_min_confidence: (a: number) => number;
    readonly transitionconfig_new: () => number;
    readonly transitionconfig_pre_roll_seconds: (a: number) => number;
    readonly transitionconfig_set_auto_advance_bars: (a: number, b: number) => void;
    readonly transitionconfig_set_beats_per_bar: (a: number, b: number) => void;
    readonly transitionconfig_set_duration_beats: (a: number, b: number) => void;
    readonly transitionconfig_set_easing: (a: number, b: number) => void;
    readonly transitionconfig_set_fallback_seconds: (a: number, b: number) => void;
    readonly transitionconfig_set_min_confidence: (a: number, b: number) => void;
    readonly transitionconfig_set_pre_roll_seconds: (a: number, b: number) => void;
    readonly version: () => number;
    readonly vividPresent: () => [number, number];
    readonly wasmMemory: () => any;
    readonly __wbindgen_externrefs: WebAssembly.Table;
    readonly __wbindgen_malloc: (a: number, b: number) => number;
    readonly __wbindgen_free: (a: number, b: number, c: number) => void;
    readonly __externref_table_dealloc: (a: number) => void;
    readonly __wbindgen_start: () => void;
}

export type SyncInitInput = BufferSource | WebAssembly.Module;

/**
 * Instantiates the given `module`, which can either be bytes or
 * a precompiled `WebAssembly.Module`.
 *
 * @param {{ module: SyncInitInput }} module - Passing `SyncInitInput` directly is deprecated.
 *
 * @returns {InitOutput}
 */
export function initSync(module: { module: SyncInitInput } | SyncInitInput): InitOutput;

/**
 * If `module_or_path` is {RequestInfo} or {URL}, makes a request and
 * for everything else, calls `WebAssembly.instantiate` directly.
 *
 * @param {{ module_or_path: InitInput | Promise<InitInput> }} module_or_path - Passing `InitInput` directly is deprecated.
 *
 * @returns {Promise<InitOutput>}
 */
export default function __wbg_init (module_or_path?: { module_or_path: InitInput | Promise<InitInput> } | InitInput | Promise<InitInput>): Promise<InitOutput>;
