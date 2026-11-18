/* @ts-self-types="./oscillux.d.ts" */

/**
 * A host beat clock (`OscilluxBeatClock`): the beat position at one media time and its tempo.
 * The constructor writes 120 BPM in 4/4 at beat 0, media time 0.
 */
export class BeatClock {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        BeatClockFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_beatclock_free(ptr, 0);
    }
    /**
     * r" [0, 1); 0 is the downbeat.
     * @returns {number}
     */
    get barPhase() {
        const ret = wasm.beatclock_bar_phase(this.__wbg_ptr);
        return ret;
    }
    /**
     * Index of the current beat (an integer; fractions are truncated).
     * @returns {number}
     */
    get beatIndex() {
        const ret = wasm.beatclock_beat_index(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" [0, 1); 0 is the beat.
     * @returns {number}
     */
    get beatPhase() {
        const ret = wasm.beatclock_beat_phase(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" 1 ..= 16.
     * @returns {number}
     */
    get beatsPerBar() {
        const ret = wasm.beatclock_beats_per_bar(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Media time of this position, ms.
     * @returns {number}
     */
    get mediaTimeMs() {
        const ret = wasm.beatclock_media_time_ms(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.beatclock_new();
        this.__wbg_ptr = ret;
        BeatClockFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @param {number} value
     */
    set barPhase(value) {
        wasm.beatclock_set_bar_phase(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set beatIndex(value) {
        wasm.beatclock_set_beat_index(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set beatPhase(value) {
        wasm.beatclock_set_beat_phase(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set beatsPerBar(value) {
        wasm.beatclock_set_beats_per_bar(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set mediaTimeMs(value) {
        wasm.beatclock_set_media_time_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set tempoBpm(value) {
        wasm.beatclock_set_tempo_bpm(this.__wbg_ptr, value);
    }
    /**
     * r" Tempo, > 0.
     * @returns {number}
     */
    get tempoBpm() {
        const ret = wasm.beatclock_tempo_bpm(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) BeatClock.prototype[Symbol.dispose] = BeatClock.prototype.free;

/**
 * Offline beat and downbeat grid (`OscilluxBeatGrid`) of a whole PCM buffer. The constructor
 * runs the analysis and copies every beat into an owned array at `beatsPtr()`: `beatCount`
 * elements `beatBytes()` (24) apart, each little-endian Float64 at 0 (timeSeconds) and 8
 * (tempoBpm) and Uint32 at 16 (beatInBar) and 20 (barIndex). `clock` writes the position at a
 * media time to the getters below without allocating.
 */
export class BeatGrid {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        BeatGridFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_beatgrid_free(ptr, 0);
    }
    /**
     * Downbeats in the grid.
     * @returns {number}
     */
    get barCount() {
        const ret = wasm.beatgrid_bar_count(this.__wbg_ptr);
        return ret;
    }
    /**
     * Progress through the current bar, [0, 1).
     * @returns {number}
     */
    get barPhase() {
        const ret = wasm.beatgrid_bar_phase(this.__wbg_ptr);
        return ret;
    }
    /**
     * Bytes between beats, `sizeof(OscilluxBeatGridBeat)`.
     * @returns {number}
     */
    static beatBytes() {
        const ret = wasm.beatgrid_beatBytes();
        return ret >>> 0;
    }
    /**
     * Beats in the grid.
     * @returns {number}
     */
    get beatCount() {
        const ret = wasm.beatgrid_beat_count(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Position of the current beat in its bar, 0 at a downbeat.
     * @returns {number}
     */
    get beatInBar() {
        const ret = wasm.beatgrid_beat_in_bar(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Index of the current beat: negative before the first beat, at or above `beatCount` after
     * the last (the grid is extrapolated at its first and last interval), -1 without a
     * position.
     * @returns {number}
     */
    get beatIndex() {
        const ret = wasm.beatgrid_beat_index(this.__wbg_ptr);
        return ret;
    }
    /**
     * Progress from the current beat to the next, [0, 1).
     * @returns {number}
     */
    get beatPhase() {
        const ret = wasm.beatgrid_beat_phase(this.__wbg_ptr);
        return ret;
    }
    /**
     * Byte offset of the beat array.
     * @returns {number}
     */
    beatsPtr() {
        const ret = wasm.beatgrid_beatsPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Meter used: the hint, or 3 or 4 chosen from the audio; 0 for an empty grid.
     * @returns {number}
     */
    get beatsPerBar() {
        const ret = wasm.beatgrid_beats_per_bar(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Writes the position at `mediaTimeSeconds` from the start of the PCM to the clock getters;
     * returns a status. Without a position (fewer than two beats, or a non-finite time, which
     * is refused) `beatIndex` is -1 and the other clock getters 0. Does not allocate.
     * @param {number} mediaTimeSeconds
     * @returns {number}
     */
    clock(mediaTimeSeconds) {
        const ret = wasm.beatgrid_clock(this.__wbg_ptr, mediaTimeSeconds);
        return ret;
    }
    /**
     * Periodicity of the onset envelope at the global beat period, 0 ..= 1.
     * @returns {number}
     */
    get confidence() {
        const ret = wasm.beatgrid_confidence(this.__wbg_ptr);
        return ret;
    }
    /**
     * Length of the analysed PCM, seconds.
     * @returns {number}
     */
    get durationSeconds() {
        const ret = wasm.beatgrid_duration_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * Local tempo of the current beat, BPM.
     * @returns {number}
     */
    get localTempoBpm() {
        const ret = wasm.beatgrid_local_tempo_bpm(this.__wbg_ptr);
        return ret;
    }
    /**
     * Analyses interleaved Float32 PCM, `config.channels` samples per frame. Allocates and runs
     * the whole analysis. Throws naming the refused member, or when the length is not a whole
     * number of frames.
     * @param {BeatGridConfig} config
     * @param {Float32Array | number[]} samples
     */
    constructor(config, samples) {
        _assertClass(config, BeatGridConfig);
        const ptr0 = passArrayF32ToWasm0(samples, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.beatgrid_new(config.__wbg_ptr, ptr0, len0);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        BeatGridFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Global tempo, BPM; 0 for an empty grid.
     * @returns {number}
     */
    get tempoBpm() {
        const ret = wasm.beatgrid_tempo_bpm(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) BeatGrid.prototype[Symbol.dispose] = BeatGrid.prototype.free;

/**
 * Beat grid analysis parameters (`OscilluxBeatGridConfig`); the constructor writes the
 * defaults.
 */
export class BeatGridConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        BeatGridConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_beatgridconfig_free(ptr, 0);
    }
    /**
     * r" Meter hint: 2 ..= 16 beats per bar, or 0 to choose 3 or 4 from the audio.
     * @returns {number}
     */
    get beatsPerBar() {
        const ret = wasm.beatgridconfig_beats_per_bar(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Interleaved channels, averaged to mono; at least 1.
     * @returns {number}
     */
    get channels() {
        const ret = wasm.beatgridconfig_channels(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Fastest beat tempo considered, BPM; at most 400.
     * @returns {number}
     */
    get maxBpm() {
        const ret = wasm.beatgridconfig_max_bpm(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Slowest beat tempo considered, BPM; at least 20 and below maxBpm.
     * @returns {number}
     */
    get minBpm() {
        const ret = wasm.beatgridconfig_min_bpm(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.beatgridconfig_new();
        this.__wbg_ptr = ret;
        BeatGridConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" PCM sample rate, Hz; 8000 ..= 384000.
     * @returns {number}
     */
    get sampleRateHz() {
        const ret = wasm.beatgridconfig_sample_rate_hz(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} value
     */
    set beatsPerBar(value) {
        wasm.beatgridconfig_set_beats_per_bar(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set channels(value) {
        wasm.beatgridconfig_set_channels(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxBpm(value) {
        wasm.beatgridconfig_set_max_bpm(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set minBpm(value) {
        wasm.beatgridconfig_set_min_bpm(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set sampleRateHz(value) {
        wasm.beatgridconfig_set_sample_rate_hz(this.__wbg_ptr, value);
    }
    /**
     * The first member `new BeatGrid` would refuse, spelled as in oscillux.h (for example
     * "sample_rate_hz" or "max_bpm"), or undefined when the configuration is valid.
     * @returns {string | undefined}
     */
    validate() {
        const ret = wasm.beatgridconfig_validate(this.__wbg_ptr);
        let v1;
        if (ret[0] !== 0) {
            v1 = getStringFromWasm0(ret[0], ret[1]);
            wasm.__wbindgen_free(ret[0], ret[1] * 1, 1);
        }
        return v1;
    }
}
if (Symbol.dispose) BeatGridConfig.prototype[Symbol.dispose] = BeatGridConfig.prototype.free;

/**
 * CD+G lyric graphics decoder (`OscilluxCdg`) over a copied pack stream, with a preallocated
 * screen of colour indices (`screenPtr`, 300 x 216 bytes) and window (`rgbaPtr`, 288 x 192
 * straight-alpha RGBA8). The getters read the state of the last advance or `state` call;
 * `count` reads the last `indexCounts` call.
 */
export class Cdg {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        CdgFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_cdg_free(ptr, 0);
    }
    /**
     * Advances or seeks so the first `applied` packs (clamped to the stream) are decoded and
     * reads the state; returns a status. A negative or non-integer count is refused.
     * @param {number} applied
     * @returns {number}
     */
    advanceToPack(applied) {
        const ret = wasm.cdg_advanceToPack(this.__wbg_ptr, applied);
        return ret;
    }
    /**
     * Advances or seeks to media time `seconds` (pack `floor(seconds * 300)` is the last
     * decoded) and reads the state; returns a status. NaN is refused unchanged.
     * @param {number} seconds
     * @returns {number}
     */
    advanceToTime(seconds) {
        const ret = wasm.cdg_advanceToTime(this.__wbg_ptr, seconds);
        return ret;
    }
    /**
     * Packs decoded at the current position.
     * @returns {number}
     */
    get applied() {
        const ret = wasm.cdg_applied(this.__wbg_ptr);
        return ret;
    }
    /**
     * Most recent Border Preset colour index.
     * @returns {number}
     */
    get borderColour() {
        const ret = wasm.cdg_border_colour(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * `OSCILLUX_CDG_CHANGED_*` bits of the last advance.
     * @returns {number}
     */
    get changes() {
        const ret = wasm.cdg_changes(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Colour `index` (0..15) as `0xRGB`.
     * @param {number} index
     * @returns {number}
     */
    colour(index) {
        const ret = wasm.cdg_colour(this.__wbg_ptr, index);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        return ret[0] >>> 0;
    }
    /**
     * Pixels of colour `index` (0..15) counted by the last `indexCounts`.
     * @param {number} index
     * @returns {number}
     */
    count(index) {
        const ret = wasm.cdg_count(this.__wbg_ptr, index);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        return ret[0] >>> 0;
    }
    /**
     * Dirty rectangle `[dirtyX0, dirtyX1) x [dirtyY0, dirtyY1)` of the last advance, screen
     * pixels; empty when `dirtyX0 == dirtyX1`.
     * @returns {number}
     */
    get dirtyX0() {
        const ret = wasm.cdg_dirty_x0(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * See `dirtyX0`.
     * @returns {number}
     */
    get dirtyX1() {
        const ret = wasm.cdg_dirty_x1(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * See `dirtyX0`.
     * @returns {number}
     */
    get dirtyY0() {
        const ret = wasm.cdg_dirty_y0(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * See `dirtyX0`.
     * @returns {number}
     */
    get dirtyY1() {
        const ret = wasm.cdg_dirty_y1(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Horizontal scroll offset, 0..5.
     * @returns {number}
     */
    get hOffset() {
        const ret = wasm.cdg_h_offset(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Counts the pixels of each colour index in the window's `region`
     * (`OSCILLUX_CDG_REGION_*`: 0 the whole window, 1 the outer ring one tile deep), read with
     * `count`; returns a status. A refusal leaves the counts unchanged.
     * @param {number} region
     * @returns {number}
     */
    indexCounts(region) {
        const ret = wasm.cdg_indexCounts(this.__wbg_ptr, region);
        return ret;
    }
    /**
     * Copies `packs` (24-byte packs; a trailing partial pack is ignored) and makes the seek
     * checkpoints.
     * @param {Uint8Array | number[]} packs
     */
    constructor(packs) {
        const ptr0 = passArray8ToWasm0(packs, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.cdg_new(ptr0, len0);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        CdgFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Whole packs in the stream.
     * @returns {number}
     */
    get packCount() {
        const ret = wasm.cdg_pack_count(this.__wbg_ptr);
        return ret;
    }
    /**
     * Most recent Memory Preset colour index.
     * @returns {number}
     */
    get presetColour() {
        const ret = wasm.cdg_preset_colour(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the 288 x 192 RGBA8 window written by `windowRgba`.
     * @returns {number}
     */
    rgbaPtr() {
        const ret = wasm.cdg_rgbaPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the 300 x 216 colour indices written by `screen`.
     * @returns {number}
     */
    screenPtr() {
        const ret = wasm.cdg_screenPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Copies the screen's colour indices to `screenPtr`; returns a status.
     * @returns {number}
     */
    screen() {
        const ret = wasm.cdg_screen(this.__wbg_ptr);
        return ret;
    }
    /**
     * Re-reads the state at the current position; returns a status.
     * @returns {number}
     */
    state() {
        const ret = wasm.cdg_state(this.__wbg_ptr);
        return ret;
    }
    /**
     * The 6-bit transparent-colour table value of colour `index` (0..15).
     * @param {number} index
     * @returns {number}
     */
    transparency(index) {
        const ret = wasm.cdg_transparency(this.__wbg_ptr, index);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        return ret[0] >>> 0;
    }
    /**
     * Vertical scroll offset, 0..11.
     * @returns {number}
     */
    get vOffset() {
        const ret = wasm.cdg_v_offset(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Writes the window under transparency key `keyMode` (`OSCILLUX_CDG_KEY_*`: 0 none,
     * 1 transparent-colour table, 2 Memory Preset colour, 3 the index `keyIndex`, 4 every index
     * whose bit is set in the 16-bit mask `keyIndex`) to `rgbaPtr`; returns a status. A refusal
     * leaves the buffer unchanged.
     * @param {number} keyMode
     * @param {number} keyIndex
     * @returns {number}
     */
    windowRgba(keyMode, keyIndex) {
        const ret = wasm.cdg_windowRgba(this.__wbg_ptr, keyMode, keyIndex);
        return ret;
    }
    /**
     * Screen x of the window's left edge.
     * @returns {number}
     */
    get windowX() {
        const ret = wasm.cdg_window_x(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Screen y of the window's top edge.
     * @returns {number}
     */
    get windowY() {
        const ret = wasm.cdg_window_y(this.__wbg_ptr);
        return ret >>> 0;
    }
}
if (Symbol.dispose) Cdg.prototype[Symbol.dispose] = Cdg.prototype.free;

/**
 * A validated curve (`OscilluxCurve`).
 */
export class Curve {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        CurveFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_curve_free(ptr, 0);
    }
    /**
     * The value at `input`, or undefined when refused.
     * @param {CurveInput} input
     * @returns {number | undefined}
     */
    evaluate(input) {
        _assertClass(input, CurveInput);
        const ret = wasm.curve_evaluate(this.__wbg_ptr, input.__wbg_ptr);
        return ret[0] === 0 ? undefined : ret[1];
    }
    /**
     * @param {CurveConfig} config
     */
    constructor(config) {
        _assertClass(config, CurveConfig);
        const ret = wasm.curve_new(config.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        CurveFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
}
if (Symbol.dispose) Curve.prototype[Symbol.dispose] = Curve.prototype.free;

/**
 * A typed curve (`OscilluxCurveConfig`); the constructor writes a constant 0.
 */
export class CurveConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        CurveConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_curveconfig_free(ptr, 0);
    }
    /**
     * r" Envelope rise, curve units.
     * @returns {number}
     */
    get attack() {
        const ret = wasm.curveconfig_attack(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Easing length, curve units.
     * @returns {number}
     */
    get duration() {
        const ret = wasm.curveconfig_duration(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" A `CurveEasing`.
     * @returns {number}
     */
    get easing() {
        const ret = wasm.curveconfig_easing(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" `CurveFlag` bits marking which optional values are present.
     * @returns {number}
     */
    get flags() {
        const ret = wasm.curveconfig_flags(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Easing start value.
     * @returns {number}
     */
    get from() {
        const ret = wasm.curveconfig_from(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" A `CurveKind`.
     * @returns {number}
     */
    get kind() {
        const ret = wasm.curveconfig_kind(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Upper bound (Max flag); LFO high value.
     * @returns {number}
     */
    get max() {
        const ret = wasm.curveconfig_max(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Lower bound (Min flag); LFO low value.
     * @returns {number}
     */
    get min() {
        const ret = wasm.curveconfig_min(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.curveconfig_new();
        this.__wbg_ptr = ret;
        CurveConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" LFO cycle length, curve units.
     * @returns {number}
     */
    get period() {
        const ret = wasm.curveconfig_period(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" LFO phase offset, cycles.
     * @returns {number}
     */
    get phase() {
        const ret = wasm.curveconfig_phase(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Envelope release start, curve units; 0 = never.
     * @returns {number}
     */
    get releaseAt() {
        const ret = wasm.curveconfig_release_at(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Envelope fall, curve units.
     * @returns {number}
     */
    get release() {
        const ret = wasm.curveconfig_release(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {number} value
     */
    set attack(value) {
        wasm.curveconfig_set_attack(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set duration(value) {
        wasm.curveconfig_set_duration(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set easing(value) {
        wasm.curveconfig_set_easing(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set flags(value) {
        wasm.curveconfig_set_flags(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set from(value) {
        wasm.curveconfig_set_from(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set kind(value) {
        wasm.curveconfig_set_kind(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set max(value) {
        wasm.curveconfig_set_max(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set min(value) {
        wasm.curveconfig_set_min(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set period(value) {
        wasm.curveconfig_set_period(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set phase(value) {
        wasm.curveconfig_set_phase(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set releaseAt(value) {
        wasm.curveconfig_set_release_at(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set release(value) {
        wasm.curveconfig_set_release(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set shape(value) {
        wasm.curveconfig_set_shape(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set smoothingSeconds(value) {
        wasm.curveconfig_set_smoothing_seconds(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set to(value) {
        wasm.curveconfig_set_to(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set unit(value) {
        wasm.curveconfig_set_unit(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set value(value) {
        wasm.curveconfig_set_value(this.__wbg_ptr, value);
    }
    /**
     * r" A `CurveShape`.
     * @returns {number}
     */
    get shape() {
        const ret = wasm.curveconfig_shape(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Exponential smoothing time constant, s; 0 disables.
     * @returns {number}
     */
    get smoothingSeconds() {
        const ret = wasm.curveconfig_smoothing_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Easing end value.
     * @returns {number}
     */
    get to() {
        const ret = wasm.curveconfig_to(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" A `CurveUnit`.
     * @returns {number}
     */
    get unit() {
        const ret = wasm.curveconfig_unit(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Constant output; envelope peak.
     * @returns {number}
     */
    get value() {
        const ret = wasm.curveconfig_value(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) CurveConfig.prototype[Symbol.dispose] = CurveConfig.prototype.free;

/**
 * `CurveConfig.easing` for the Easing kind.
 * @enum {0 | 1 | 2 | 3}
 */
export const CurveEasing = Object.freeze({
    Linear: 0, "0": "Linear",
    In: 1, "1": "In",
    Out: 2, "2": "Out",
    InOut: 3, "3": "InOut",
});

/**
 * `CurveConfig.flags` bits; combine with `|`.
 * @enum {1 | 2 | 4 | 8 | 16 | 32}
 */
export const CurveFlag = Object.freeze({
    Value: 1, "1": "Value",
    From: 2, "2": "From",
    To: 4, "4": "To",
    Min: 8, "8": "Min",
    Max: 16, "16": "Max",
    Wrap: 32, "32": "Wrap",
});

/**
 * The clock and history for one evaluation (`OscilluxCurveInput`); starts at zero.
 */
export class CurveInput {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        CurveInputFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_curveinput_free(ptr, 0);
    }
    /**
     * r" Added to the LFO phase when the unit is beats, cycles.
     * @returns {number}
     */
    get beatPhase() {
        const ret = wasm.curveinput_beat_phase(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Time since the previous evaluation, s; smoothing needs > 0.
     * @returns {number}
     */
    get deltaSeconds() {
        const ret = wasm.curveinput_delta_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Time since the curve started, beats.
     * @returns {number}
     */
    get elapsedBeats() {
        const ret = wasm.curveinput_elapsed_beats(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Time since the curve started, s.
     * @returns {number}
     */
    get elapsedSeconds() {
        const ret = wasm.curveinput_elapsed_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * Whether `previousValue` is read.
     * @returns {boolean}
     */
    get hasPrevious() {
        const ret = wasm.curveinput_has_previous(this.__wbg_ptr);
        return ret !== 0;
    }
    constructor() {
        const ret = wasm.curveinput_new();
        this.__wbg_ptr = ret;
        CurveInputFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" The previous output, read when hasPrevious is true.
     * @returns {number}
     */
    get previousValue() {
        const ret = wasm.curveinput_previous_value(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {number} value
     */
    set beatPhase(value) {
        wasm.curveinput_set_beat_phase(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set deltaSeconds(value) {
        wasm.curveinput_set_delta_seconds(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set elapsedBeats(value) {
        wasm.curveinput_set_elapsed_beats(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set elapsedSeconds(value) {
        wasm.curveinput_set_elapsed_seconds(this.__wbg_ptr, value);
    }
    /**
     * @param {boolean} value
     */
    set hasPrevious(value) {
        wasm.curveinput_set_has_previous(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set previousValue(value) {
        wasm.curveinput_set_previous_value(this.__wbg_ptr, value);
    }
}
if (Symbol.dispose) CurveInput.prototype[Symbol.dispose] = CurveInput.prototype.free;

/**
 * `CurveConfig.kind`.
 * @enum {0 | 1 | 2 | 3}
 */
export const CurveKind = Object.freeze({
    Constant: 0, "0": "Constant",
    Easing: 1, "1": "Easing",
    Envelope: 2, "2": "Envelope",
    Lfo: 3, "3": "Lfo",
});

/**
 * `CurveConfig.shape` for the Lfo kind.
 * @enum {0 | 1 | 2}
 */
export const CurveShape = Object.freeze({
    Sine: 0, "0": "Sine",
    Triangle: 1, "1": "Triangle",
    Square: 2, "2": "Square",
});

/**
 * `CurveConfig.unit`: the clock for duration, attack, release, releaseAt and period.
 * @enum {0 | 1 | 2}
 */
export const CurveUnit = Object.freeze({
    Unspecified: 0, "0": "Unspecified",
    Seconds: 1, "1": "Seconds",
    Beats: 2, "2": "Beats",
});

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
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        DspFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_dsp_free(ptr, 0);
    }
    /**
     * Starts a segment whose first sample plays at `timeMs`; returns a status.
     * @param {number} timeMs
     * @param {boolean} stalled
     * @returns {number}
     */
    beginSegment(timeMs, stalled) {
        const ret = wasm.dsp_beginSegment(this.__wbg_ptr, timeMs, stalled);
        return ret;
    }
    /**
     * Sample frames (not scalars) consumed by the last `push` or `pushF64`.
     * @returns {number}
     */
    consumed() {
        const ret = wasm.dsp_consumed(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Size of one `OscilluxDspFrameExt`, bytes: its `struct_size`.
     * @returns {number}
     */
    extBytes() {
        const ret = wasm.dsp_extBytes(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the `OscilluxDspFrameExt` output buffer (`frameCapacity` elements) in
     * `wasmMemory().buffer`; `pushExt` and `pushExtF64` write its first `produced()` elements.
     * 0 until the first `pushExt` or `pushExtF64` allocates the buffer; fixed after that.
     * @returns {number}
     */
    extPtr() {
        const ret = wasm.dsp_extPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Size of one output frame, bytes.
     * @returns {number}
     */
    frameBytes() {
        const ret = wasm.dsp_frameBytes(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the Float64 PCM input buffer (`f64Capacity` samples) in
     * `wasmMemory().buffer`.
     * @returns {number}
     */
    inputF64Ptr() {
        const ret = wasm.dsp_inputF64Ptr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the Float32 PCM input buffer in `wasmMemory().buffer`.
     * @returns {number}
     */
    inputPtr() {
        const ret = wasm.dsp_inputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Validates `config` and allocates room for `sampleCapacity` interleaved Float32 PCM
     * samples, `frameCapacity` output frames and `f64Capacity` (default 0) interleaved Float64
     * samples. Throws naming the refused member.
     * @param {DspConfig} config
     * @param {number} sampleCapacity
     * @param {number} frameCapacity
     * @param {number | null} [f64Capacity]
     */
    constructor(config, sampleCapacity, frameCapacity, f64Capacity) {
        _assertClass(config, DspConfig);
        const ret = wasm.dsp_new(config.__wbg_ptr, sampleCapacity, frameCapacity, isLikeNone(f64Capacity) ? Number.MAX_SAFE_INTEGER : (f64Capacity) >>> 0);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        DspFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Byte offset of the frame output buffer in `wasmMemory().buffer`.
     * @returns {number}
     */
    outputPtr() {
        const ret = wasm.dsp_outputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Frames written to the output buffer by the last `push` or `pushF64`.
     * @returns {number}
     */
    produced() {
        const ret = wasm.dsp_produced(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * As `pushF64`, and also writes each frame's descriptors, beat and levels to the ext
     * buffer (`oscillux_dsp_push_ext_f64`).
     * @param {number} sampleCount
     * @param {number} channels
     * @returns {number}
     */
    pushExtF64(sampleCount, channels) {
        const ret = wasm.dsp_pushExtF64(this.__wbg_ptr, sampleCount, channels);
        return ret;
    }
    /**
     * As `push`, and also writes each frame's descriptors, beat and levels to the ext buffer
     * at `extPtr()` (`oscillux_dsp_push_ext_f32`). Frames equal those `push` would write.
     * @param {number} sampleCount
     * @param {number} channels
     * @returns {number}
     */
    pushExt(sampleCount, channels) {
        const ret = wasm.dsp_pushExt(this.__wbg_ptr, sampleCount, channels);
        return ret;
    }
    /**
     * Analyzes the first `sampleCount` interleaved scalars of the Float64 input buffer, exactly
     * as `oscillux_dsp_push_f64`; returns a status. Read `produced()` frames from `outputPtr()`.
     * @param {number} sampleCount
     * @param {number} channels
     * @returns {number}
     */
    pushF64(sampleCount, channels) {
        const ret = wasm.dsp_pushF64(this.__wbg_ptr, sampleCount, channels);
        return ret;
    }
    /**
     * Analyzes the first `sampleCount` interleaved scalars of the Float32 input buffer; returns
     * a status. Read `produced()` frames from `outputPtr()`.
     * @param {number} sampleCount
     * @param {number} channels
     * @returns {number}
     */
    push(sampleCount, channels) {
        const ret = wasm.dsp_push(this.__wbg_ptr, sampleCount, channels);
        return ret;
    }
}
if (Symbol.dispose) Dsp.prototype[Symbol.dispose] = Dsp.prototype.free;

/**
 * Analysis parameters (`OscilluxDspConfig`) and DSP behaviour version, passed to C as an
 * `OscilluxDspVersionedConfig`; the constructor writes the reference values and version 2.
 * Its `reserved` member is not exposed and is always passed as 0.
 */
export class DspConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        DspConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_dspconfig_free(ptr, 0);
    }
    /**
     * r" Band smoothing time constant while rising, ms.
     * @returns {number}
     */
    get ballisticsAttackMs() {
        const ret = wasm.dspconfig_ballistics_attack_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Band smoothing time constant while falling, ms.
     * @returns {number}
     */
    get ballisticsReleaseMs() {
        const ret = wasm.dspconfig_ballistics_release_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * DSP behaviour version: 1 (`OSCILLUX_DSP_BEHAVIOR_V1`, the ABI-1 analysis), 2 (the
     * default; real-input FFT, low-band bass analysis and normalised onsets), 3 (version 2
     * frames with the autocorrelation beat tracker) or 4 (version 2 frames with the
     * beat-pointer beat tracker).
     * @returns {number}
     */
    get behaviorVersion() {
        const ret = wasm.dspconfig_behavior_version(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Band level mapped to 0, dB; negative.
     * @returns {number}
     */
    get dbFloor() {
        const ret = wasm.dspconfig_db_floor(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Window and FFT length, samples; a power of two.
     * @returns {number}
     */
    get fftWindowSamples() {
        const ret = wasm.dspconfig_fft_window_samples(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Segment lateness tolerated before the Gap flag, hops.
     * @returns {number}
     */
    get gapToleranceHops() {
        const ret = wasm.dspconfig_gap_tolerance_hops(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Samples between frames.
     * @returns {number}
     */
    get hopSamples() {
        const ret = wasm.dspconfig_hop_samples(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Subtracted from the RMS level for loudnessDb, dB.
     * @returns {number}
     */
    get loudnessReferenceLufs() {
        const ret = wasm.dspconfig_loudness_reference_lufs(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.dspconfig_new();
        this.__wbg_ptr = ret;
        DspConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Onset band group `group` (0 low, 1 mid, 2 high) as `[start, end]`, inclusive.
     * @param {number} groupIndex
     * @returns {Uint32Array}
     */
    onsetBandGroup(groupIndex) {
        const ret = wasm.dspconfig_onsetBandGroup(this.__wbg_ptr, groupIndex);
        if (ret[3]) {
            throw takeFromExternrefTable0(ret[2]);
        }
        var v1 = getArrayU32FromWasm0(ret[0], ret[1]).slice();
        wasm.__wbindgen_free(ret[0], ret[1] * 4, 4);
        return v1;
    }
    /**
     * r" Per-frame decay factor of running onset peaks, 0 ..= 1.
     * @returns {number}
     */
    get onsetPeakDecay() {
        const ret = wasm.dspconfig_onset_peak_decay(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Lower bound of an onset peak used as a divisor.
     * @returns {number}
     */
    get onsetPeakFloor() {
        const ret = wasm.dspconfig_onset_peak_floor(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Linear RMS lower bound before the logarithm.
     * @returns {number}
     */
    get rmsFloor() {
        const ret = wasm.dspconfig_rms_floor(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" RMS window, ms.
     * @returns {number}
     */
    get rmsWindowMs() {
        const ret = wasm.dspconfig_rms_window_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Input sample rate, Hz.
     * @returns {number}
     */
    get sampleRateHz() {
        const ret = wasm.dspconfig_sample_rate_hz(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} group
     * @param {number} start
     * @param {number} end
     */
    setOnsetBandGroup(group, start, end) {
        const ret = wasm.dspconfig_setOnsetBandGroup(this.__wbg_ptr, group, start, end);
        if (ret[1]) {
            throw takeFromExternrefTable0(ret[0]);
        }
    }
    /**
     * @param {number} value
     */
    set ballisticsAttackMs(value) {
        wasm.dspconfig_set_ballistics_attack_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set ballisticsReleaseMs(value) {
        wasm.dspconfig_set_ballistics_release_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set behaviorVersion(value) {
        wasm.dspconfig_set_behavior_version(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set dbFloor(value) {
        wasm.dspconfig_set_db_floor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set fftWindowSamples(value) {
        wasm.dspconfig_set_fft_window_samples(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set gapToleranceHops(value) {
        wasm.dspconfig_set_gap_tolerance_hops(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set hopSamples(value) {
        wasm.dspconfig_set_hop_samples(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set loudnessReferenceLufs(value) {
        wasm.dspconfig_set_loudness_reference_lufs(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set onsetPeakDecay(value) {
        wasm.dspconfig_set_onset_peak_decay(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set onsetPeakFloor(value) {
        wasm.dspconfig_set_onset_peak_floor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set rmsFloor(value) {
        wasm.dspconfig_set_rms_floor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set rmsWindowMs(value) {
        wasm.dspconfig_set_rms_window_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set sampleRateHz(value) {
        wasm.dspconfig_set_sample_rate_hz(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set spectrumBands(value) {
        wasm.dspconfig_set_spectrum_bands(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set spectrumMaxHz(value) {
        wasm.dspconfig_set_spectrum_max_hz(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set spectrumMinHz(value) {
        wasm.dspconfig_set_spectrum_min_hz(this.__wbg_ptr, value);
    }
    /**
     * r" Log-spaced bands, 1 ..= 64.
     * @returns {number}
     */
    get spectrumBands() {
        const ret = wasm.dspconfig_spectrum_bands(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Upper band edge, Hz; at most half the sample rate.
     * @returns {number}
     */
    get spectrumMaxHz() {
        const ret = wasm.dspconfig_spectrum_max_hz(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Lower edge of the first band, Hz.
     * @returns {number}
     */
    get spectrumMinHz() {
        const ret = wasm.dspconfig_spectrum_min_hz(this.__wbg_ptr);
        return ret;
    }
    /**
     * The first member `new Dsp` would refuse, spelled as in oscillux.h (for example
     * "fft_window_samples" or "behavior_version"), or undefined when the configuration is
     * valid.
     * @returns {string | undefined}
     */
    validate() {
        const ret = wasm.dspconfig_validate(this.__wbg_ptr);
        let v1;
        if (ret[0] !== 0) {
            v1 = getStringFromWasm0(ret[0], ret[1]);
            wasm.__wbindgen_free(ret[0], ret[1] * 1, 1);
        }
        return v1;
    }
}
if (Symbol.dispose) DspConfig.prototype[Symbol.dispose] = DspConfig.prototype.free;

/**
 * `OscilluxDspFrame.flags` bits.
 * @enum {1 | 2 | 4}
 */
export const DspFlag = Object.freeze({
    Gap: 1, "1": "Gap",
    Stalled: 2, "2": "Stalled",
    InputReplaced: 4, "4": "InputReplaced",
});

/**
 * `UniformField.source`.
 * @enum {0 | 1 | 2}
 */
export const FieldSource = Object.freeze({
    /**
     * Mapped values, indexed by `MappedValue`.
     */
    Mapped: 0, "0": "Mapped",
    /**
     * The 64 mapped spectrum bins.
     */
    Spectrum: 1, "1": "Spectrum",
    /**
     * The packer's host values.
     */
    Host: 2, "2": "Host",
});

/**
 * Region flash analyzer (`OscilluxFlashAnalyzer`) with preallocated row-major region and color
 * arrays (`columns * rows` pairs of doubles: luminance, red; and triples: linear r, g, b) and
 * one report. `measure` / `admit` read the regions, `measureRgb` / `admitRgb` the colors; each
 * writes the report and none allocates.
 */
export class FlashAnalyzer {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        FlashAnalyzerFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_flashanalyzer_free(ptr, 0);
    }
    /**
     * As `admit`, from the color array.
     * @param {number} timeMs
     * @returns {number}
     */
    admitRgb(timeMs) {
        const ret = wasm.flashanalyzer_admitRgb(this.__wbg_ptr, timeMs);
        return ret;
    }
    /**
     * As `measure`, but commits the frame only if it keeps the window within the config.
     * When `admitted` is false the host keeps presenting its last admitted image.
     * @param {number} timeMs
     * @returns {number}
     */
    admit(timeMs) {
        const ret = wasm.flashanalyzer_admit(this.__wbg_ptr, timeMs);
        return ret;
    }
    /**
     * The frame was committed; false means show the last admitted one.
     * @returns {boolean}
     */
    get admitted() {
        const ret = wasm.flashanalyzer_admitted(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * Removes the overlay; the analyzer then measures exactly as without one.
     * @returns {number}
     */
    clearOverlay() {
        const ret = wasm.flashanalyzer_clearOverlay(this.__wbg_ptr);
        return ret;
    }
    /**
     * Byte offset of the color array: Float64 triples (r, g, b), linear light, row-major.
     * @returns {number}
     */
    colorsPtr() {
        const ret = wasm.flashanalyzer_colorsPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * The window holds more flashes than the config allows.
     * @returns {boolean}
     */
    get exceeds() {
        const ret = wasm.flashanalyzer_exceeds(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * General flashes (transitions / 2) in the last 1000 ms.
     * @returns {number}
     */
    get flashesInWindow() {
        const ret = wasm.flashanalyzer_flashes_in_window(this.__wbg_ptr);
        return ret;
    }
    /**
     * Regions completing a general change this frame.
     * @returns {number}
     */
    get generalRegions() {
        const ret = wasm.flashanalyzer_general_regions(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * The frame completes a general transition.
     * @returns {boolean}
     */
    get generalTransition() {
        const ret = wasm.flashanalyzer_general_transition(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * As `measure`, from the color array; the only path `FlashRedRule.Iso9241_391` accepts.
     * @param {number} timeMs
     * @returns {number}
     */
    measureRgb(timeMs) {
        const ret = wasm.flashanalyzer_measureRgb(this.__wbg_ptr, timeMs);
        return ret;
    }
    /**
     * Measures and commits the regions as the frame at `timeMs`; returns a status. On an
     * error the report is zero except `timeMs`.
     * @param {number} timeMs
     * @returns {number}
     */
    measure(timeMs) {
        const ret = wasm.flashanalyzer_measure(this.__wbg_ptr, timeMs);
        return ret;
    }
    /**
     * Throws when `config` is refused.
     * @param {FlashConfig} config
     */
    constructor(config) {
        _assertClass(config, FlashConfig);
        const ret = wasm.flashanalyzer_new(config.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        FlashAnalyzerFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Writes the overlay outcome of the last measured or admitted frame; returns a status.
     * @param {FlashOverlayReport} out
     * @returns {number}
     */
    overlayReport(out) {
        _assertClass(out, FlashOverlayReport);
        const ret = wasm.flashanalyzer_overlayReport(this.__wbg_ptr, out.__wbg_ptr);
        return ret;
    }
    /**
     * Red flashes (transitions / 2) in the last 1000 ms.
     * @returns {number}
     */
    get redFlashesInWindow() {
        const ret = wasm.flashanalyzer_red_flashes_in_window(this.__wbg_ptr);
        return ret;
    }
    /**
     * Regions completing a red change this frame.
     * @returns {number}
     */
    get redRegions() {
        const ret = wasm.flashanalyzer_red_regions(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * The frame completes a red transition.
     * @returns {boolean}
     */
    get redTransition() {
        const ret = wasm.flashanalyzer_red_transition(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * Regions in the array, `columns * rows`.
     * @returns {number}
     */
    regionCount() {
        const ret = wasm.flashanalyzer_regionCount(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the region array: Float64 pairs (luminance, red), row-major.
     * @returns {number}
     */
    regionsPtr() {
        const ret = wasm.flashanalyzer_regionsPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the `OscilluxFlashReport` the last measure or admit wrote.
     * @returns {number}
     */
    reportPtr() {
        const ret = wasm.flashanalyzer_reportPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Starts a new timeline (a seek or new stream); returns a status.
     * @returns {number}
     */
    reset() {
        const ret = wasm.flashanalyzer_reset(this.__wbg_ptr);
        return ret;
    }
    /**
     * Holds the grid cells `regions`' `DampFlash` regions cover to `overlay` from the next
     * frame; returns a status. A refusal keeps the previous overlay. Call on layout changes.
     * @param {FlashOverlay} overlay
     * @param {OverlayRegions} regions
     * @returns {number}
     */
    setOverlay(overlay, regions) {
        _assertClass(overlay, FlashOverlay);
        _assertClass(regions, OverlayRegions);
        const ret = wasm.flashanalyzer_setOverlay(this.__wbg_ptr, overlay.__wbg_ptr, regions.__wbg_ptr);
        return ret;
    }
    /**
     * Writes region `index` from a mean linear-light sRGB color, each channel in [0, 1];
     * returns a status and leaves the region unchanged on refusal.
     * @param {number} index
     * @param {number} r
     * @param {number} g
     * @param {number} b
     * @returns {number}
     */
    setRegionFromLinearRgb(index, r, g, b) {
        const ret = wasm.flashanalyzer_setRegionFromLinearRgb(this.__wbg_ptr, index, r, g, b);
        return ret;
    }
    /**
     * The last report's frame time, ms.
     * @returns {number}
     */
    get timeMs() {
        const ret = wasm.flashanalyzer_time_ms(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) FlashAnalyzer.prototype[Symbol.dispose] = FlashAnalyzer.prototype.free;

/**
 * Region flash analyzer configuration (`OscilluxFlashConfig`); the constructor writes the
 * defaults, the loosest configuration `new FlashAnalyzer` accepts.
 */
export class FlashConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        FlashConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_flashconfig_free(ptr, 0);
    }
    /**
     * r" Regions across the viewport; 1 ..= 16.
     * @returns {number}
     */
    get columns() {
        const ret = wasm.flashconfig_columns(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Field-window height as a viewport fraction; > 0, at most 1.
     * @returns {number}
     */
    get fieldHeight() {
        const ret = wasm.flashconfig_field_height(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Field-window width as a viewport fraction; > 0, at most 1.
     * @returns {number}
     */
    get fieldWidth() {
        const ret = wasm.flashconfig_field_width(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Field-window fraction at or below which a change is no flash; > 0, at most 0.25.
     * @returns {number}
     */
    get maxFieldArea() {
        const ret = wasm.flashconfig_max_field_area(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" General flashes per one-second window; > 0, at most 3.
     * @returns {number}
     */
    get maxFlashHz() {
        const ret = wasm.flashconfig_max_flash_hz(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Red flashes per one-second window; 0 ..= 3.
     * @returns {number}
     */
    get maxRedFlashes() {
        const ret = wasm.flashconfig_max_red_flashes(this.__wbg_ptr);
        return ret >>> 0;
    }
    constructor() {
        const ret = wasm.flashconfig_new();
        this.__wbg_ptr = ret;
        FlashConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" A `FlashRedRule`; any other value is refused by `new FlashAnalyzer`.
     * @returns {number}
     */
    get redRule() {
        const ret = wasm.flashconfig_red_rule(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Regions down the viewport; 1 ..= 16.
     * @returns {number}
     */
    get rows() {
        const ret = wasm.flashconfig_rows(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} value
     */
    set columns(value) {
        wasm.flashconfig_set_columns(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set fieldHeight(value) {
        wasm.flashconfig_set_field_height(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set fieldWidth(value) {
        wasm.flashconfig_set_field_width(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxFieldArea(value) {
        wasm.flashconfig_set_max_field_area(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxFlashHz(value) {
        wasm.flashconfig_set_max_flash_hz(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxRedFlashes(value) {
        wasm.flashconfig_set_max_red_flashes(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set redRule(value) {
        wasm.flashconfig_set_red_rule(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set rows(value) {
        wasm.flashconfig_set_rows(this.__wbg_ptr, value);
    }
}
if (Symbol.dispose) FlashConfig.prototype[Symbol.dispose] = FlashConfig.prototype.free;

/**
 * Stricter flash rules for `OverlayMode.DampFlash` regions (`OscilluxFlashOverlay`); the
 * constructor writes the defaults.
 */
export class FlashOverlay {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        FlashOverlayFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_flashoverlay_free(ptr, 0);
    }
    /**
     * r" A change counts once its darker end is below this, [0.8, 1]; default 1.
     * @returns {number}
     */
    get darkerLimit() {
        const ret = wasm.flashoverlay_darker_limit(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Smallest luminance change that counts inside, (0, 0.1]; default 0.05.
     * @returns {number}
     */
    get luminanceChange() {
        const ret = wasm.flashoverlay_luminance_change(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Fraction of overlay cells above which a frame is a transition; default 0 (one cell).
     * @returns {number}
     */
    get maxArea() {
        const ret = wasm.flashoverlay_max_area(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Overlay flashes a second, at most the analyzer's; default 1.
     * @returns {number}
     */
    get maxFlashHz() {
        const ret = wasm.flashoverlay_max_flash_hz(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.flashoverlay_new();
        this.__wbg_ptr = ret;
        FlashOverlayFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @param {number} value
     */
    set darkerLimit(value) {
        wasm.flashoverlay_set_darker_limit(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set luminanceChange(value) {
        wasm.flashoverlay_set_luminance_change(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxArea(value) {
        wasm.flashoverlay_set_max_area(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxFlashHz(value) {
        wasm.flashoverlay_set_max_flash_hz(this.__wbg_ptr, value);
    }
}
if (Symbol.dispose) FlashOverlay.prototype[Symbol.dispose] = FlashOverlay.prototype.free;

/**
 * The overlay outcome `FlashAnalyzer.overlayReport` writes (`OscilluxFlashOverlayReport`).
 */
export class FlashOverlayReport {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        FlashOverlayReportFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_flashoverlayreport_free(ptr, 0);
    }
    /**
     * An overlay is set.
     * @returns {boolean}
     */
    get active() {
        const ret = wasm.flashoverlayreport_active(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * Grid cells the overlay covers.
     * @returns {number}
     */
    get cells() {
        const ret = wasm.flashoverlayreport_cells(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * The overlay window holds more flashes than it allows.
     * @returns {boolean}
     */
    get exceeds() {
        const ret = wasm.flashoverlayreport_exceeds(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * Overlay flashes (transitions / 2) in the last 1000 ms.
     * @returns {number}
     */
    get flashesInWindow() {
        const ret = wasm.flashoverlayreport_flashes_in_window(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.flashoverlayreport_new();
        this.__wbg_ptr = ret;
        FlashOverlayReportFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Overlay cells completing a change this frame.
     * @returns {number}
     */
    get regions() {
        const ret = wasm.flashoverlayreport_regions(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * The frame holds an overlay transition.
     * @returns {boolean}
     */
    get transition() {
        const ret = wasm.flashoverlayreport_transition(this.__wbg_ptr);
        return ret !== 0;
    }
}
if (Symbol.dispose) FlashOverlayReport.prototype[Symbol.dispose] = FlashOverlayReport.prototype.free;

/**
 * `FlashConfig.redRule`: which red-flash rule the analyzer applies.
 * @enum {0 | 1}
 */
export const FlashRedRule = Object.freeze({
    /**
     * WCAG 2.0: (R - G - B) * 320 changes by more than 20; the default.
     */
    Wcag20: 0, "0": "Wcag20",
    /**
     * ISO 9241-391: a transition to or from saturated red (R / (R + G + B) >= 0.8) with a
     * CIE 1976 u'v' change above 0.2. Needs colors: use `measureRgb` / `admitRgb`.
     */
    Iso9241_391: 1, "1": "Iso9241_391",
});

/**
 * Frame-time governor (`OscilluxGovernor`). `level`, `emaMs` and `refreshMs` read the stats
 * of the last `record` or `reset`.
 */
export class Governor {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        GovernorFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_governor_free(ptr, 0);
    }
    /**
     * Moving average of frame duration, ms; undefined before the first accepted sample.
     * @returns {number | undefined}
     */
    get emaMs() {
        const ret = wasm.governor_ema_ms(this.__wbg_ptr);
        return ret[0] === 0 ? undefined : ret[1];
    }
    /**
     * Current quality level, 0 = lowest.
     * @returns {number}
     */
    get level() {
        const ret = wasm.governor_level(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {GovernorConfig} config
     */
    constructor(config) {
        _assertClass(config, GovernorConfig);
        const ret = wasm.governor_new(config.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        GovernorFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Records one frame duration, ms; returns a status. Pauses and invalid durations are
     * ignored.
     * @param {number} frameMs
     * @returns {number}
     */
    record(frameMs) {
        const ret = wasm.governor_record(this.__wbg_ptr, frameMs);
        return ret;
    }
    /**
     * Estimated display refresh interval, ms.
     * @returns {number}
     */
    get refreshMs() {
        const ret = wasm.governor_refresh_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * Clears the average and counters and sets the level, clamped to maxLevel.
     * @param {number} level
     * @returns {number}
     */
    reset(level) {
        const ret = wasm.governor_reset(this.__wbg_ptr, level);
        return ret;
    }
}
if (Symbol.dispose) Governor.prototype[Symbol.dispose] = Governor.prototype.free;

/**
 * Frame-time governor parameters (`OscilluxGovernorConfig`); the constructor writes the
 * reference values.
 */
export class GovernorConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        GovernorConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_governorconfig_free(ptr, 0);
    }
    /**
     * r" An average above targetMs times this is slow.
     * @returns {number}
     */
    get demoteFactor() {
        const ret = wasm.governorconfig_demote_factor(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Consecutive slow samples before a step down.
     * @returns {number}
     */
    get demoteFrames() {
        const ret = wasm.governorconfig_demote_frames(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Weight of each sample in the moving average; (0, 1].
     * @returns {number}
     */
    get emaAlpha() {
        const ret = wasm.governorconfig_ema_alpha(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Level after construction; <= maxLevel.
     * @returns {number}
     */
    get initialLevel() {
        const ret = wasm.governorconfig_initial_level(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Highest quality level.
     * @returns {number}
     */
    get maxLevel() {
        const ret = wasm.governorconfig_max_level(this.__wbg_ptr);
        return ret >>> 0;
    }
    constructor() {
        const ret = wasm.governorconfig_new();
        this.__wbg_ptr = ret;
        GovernorConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" Durations at or above this are pauses and ignored, ms.
     * @returns {number}
     */
    get pauseGapMs() {
        const ret = wasm.governorconfig_pause_gap_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Consecutive fast samples before a step up.
     * @returns {number}
     */
    get promoteFrames() {
        const ret = wasm.governorconfig_promote_frames(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" An average below refreshMs times this is fast ...
     * @returns {number}
     */
    get promoteRefreshFactor() {
        const ret = wasm.governorconfig_promote_refresh_factor(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" ... or below targetMs times this.
     * @returns {number}
     */
    get promoteTargetFactor() {
        const ret = wasm.governorconfig_promote_target_factor(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Per-sample growth factor of refreshMs toward the target; >= 1.
     * @returns {number}
     */
    get refreshDrift() {
        const ret = wasm.governorconfig_refresh_drift(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Lower bound of refreshMs, ms.
     * @returns {number}
     */
    get refreshFloorMs() {
        const ret = wasm.governorconfig_refresh_floor_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Samples are capped at targetMs times this.
     * @returns {number}
     */
    get sampleCapFactor() {
        const ret = wasm.governorconfig_sample_cap_factor(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {number} value
     */
    set demoteFactor(value) {
        wasm.governorconfig_set_demote_factor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set demoteFrames(value) {
        wasm.governorconfig_set_demote_frames(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set emaAlpha(value) {
        wasm.governorconfig_set_ema_alpha(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set initialLevel(value) {
        wasm.governorconfig_set_initial_level(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxLevel(value) {
        wasm.governorconfig_set_max_level(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set pauseGapMs(value) {
        wasm.governorconfig_set_pause_gap_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set promoteFrames(value) {
        wasm.governorconfig_set_promote_frames(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set promoteRefreshFactor(value) {
        wasm.governorconfig_set_promote_refresh_factor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set promoteTargetFactor(value) {
        wasm.governorconfig_set_promote_target_factor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set refreshDrift(value) {
        wasm.governorconfig_set_refresh_drift(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set refreshFloorMs(value) {
        wasm.governorconfig_set_refresh_floor_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set sampleCapFactor(value) {
        wasm.governorconfig_set_sample_cap_factor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set settleFrames(value) {
        wasm.governorconfig_set_settle_frames(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set targetMs(value) {
        wasm.governorconfig_set_target_ms(this.__wbg_ptr, value);
    }
    /**
     * r" Samples after a step that cannot cause another step.
     * @returns {number}
     */
    get settleFrames() {
        const ret = wasm.governorconfig_settle_frames(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Target frame duration, ms.
     * @returns {number}
     */
    get targetMs() {
        const ret = wasm.governorconfig_target_ms(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) GovernorConfig.prototype[Symbol.dispose] = GovernorConfig.prototype.free;

/**
 * `Mapper.setLevelSource` values (`OSCILLUX_MAPPING_LEVELS_*`).
 * @enum {0 | 1}
 */
export const LevelSource = Object.freeze({
    /**
     * The band energies; the default.
     */
    Absolute: 0, "0": "Absolute",
    /**
     * The ext frame's relative band levels.
     */
    Relative: 1, "1": "Relative",
});

/**
 * Flash limiter (`OscilluxLimiter`) with two preallocated seven-double frames in the order
 * time, broadband, low, mid, high, red, area.
 */
export class Limiter {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        LimiterFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_limiter_free(ptr, 0);
    }
    /**
     * @returns {number}
     */
    inputPtr() {
        const ret = wasm.limiter_inputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {LimiterPolicy} policy
     */
    constructor(policy) {
        _assertClass(policy, LimiterPolicy);
        const ret = wasm.limiter_new(policy.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        LimiterFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @returns {number}
     */
    outputPtr() {
        const ret = wasm.limiter_outputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Limits the input frame into the output frame; returns a status. A refused frame writes
     * the fail-safe frame, which the host renders.
     * @returns {number}
     */
    process() {
        const ret = wasm.limiter_process(this.__wbg_ptr);
        return ret;
    }
    /**
     * Starts a new timeline; returns a status.
     * @returns {number}
     */
    reset() {
        const ret = wasm.limiter_reset(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) Limiter.prototype[Symbol.dispose] = Limiter.prototype.free;

/**
 * Flash-limiter policy (`OscilluxLimiterPolicy`); the constructor writes the default, the
 * loosest policy accepted. Ceilings may only be tightened.
 */
export class LimiterPolicy {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        LimiterPolicyFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_limiterpolicy_free(ptr, 0);
    }
    /**
     * r" Screen fraction at or below which a change is no flash; at most 0.25.
     * @returns {number}
     */
    get maxFlashArea() {
        const ret = wasm.limiterpolicy_max_flash_area(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" General flashes per one-second window; at most 3.
     * @returns {number}
     */
    get maxFlashHz() {
        const ret = wasm.limiterpolicy_max_flash_hz(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Ceiling on any onset magnitude, normalized; at most 0.8.
     * @returns {number}
     */
    get maxLuminanceDelta() {
        const ret = wasm.limiterpolicy_max_luminance_delta(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Red flashes per one-second window; 0 ..= 3.
     * @returns {number}
     */
    get maxRedFlashes() {
        const ret = wasm.limiterpolicy_max_red_flashes(this.__wbg_ptr);
        return ret >>> 0;
    }
    constructor() {
        const ret = wasm.limiterpolicy_new();
        this.__wbg_ptr = ret;
        LimiterPolicyFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @param {number} value
     */
    set maxFlashArea(value) {
        wasm.limiterpolicy_set_max_flash_area(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxFlashHz(value) {
        wasm.limiterpolicy_set_max_flash_hz(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxLuminanceDelta(value) {
        wasm.limiterpolicy_set_max_luminance_delta(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set maxRedFlashes(value) {
        wasm.limiterpolicy_set_max_red_flashes(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set strobeBanned(value) {
        wasm.limiterpolicy_set_strobe_banned(this.__wbg_ptr, value);
    }
    /**
     * r" Must equal 1.
     * @returns {number}
     */
    get strobeBanned() {
        const ret = wasm.limiterpolicy_strobe_banned(this.__wbg_ptr);
        return ret >>> 0;
    }
}
if (Symbol.dispose) LimiterPolicy.prototype[Symbol.dispose] = LimiterPolicy.prototype.free;

/**
 * Indices into the mapped values (`OSCILLUX_MAPPED_*`); every value is normalized.
 * @enum {0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8}
 */
export const MappedValue = Object.freeze({
    Amplitude: 0, "0": "Amplitude",
    Level: 1, "1": "Level",
    Bass: 2, "2": "Bass",
    Mid: 3, "3": "Mid",
    Treble: 4, "4": "Treble",
    Onset: 5, "5": "Onset",
    BassImpact: 6, "6": "BassImpact",
    /**
     * Spectral flux with descriptors (`processExt`); otherwise the broadband onset, a
     * deprecated proxy.
     */
    Flux: 7, "7": "Flux",
    /**
     * Log-frequency centroid with descriptors (`processExt`); otherwise the band-index
     * fraction, a deprecated proxy.
     */
    Centroid: 8, "8": "Centroid",
});

/**
 * Frame mapper (`OscilluxMapper`) with a preallocated `OscilluxMapped` output (nine float64
 * values, then 64 float32 spectrum bins at byte 72) and a one-frame input buffer for frames
 * that do not come from a `Dsp`.
 */
export class Mapper {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        MapperFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_mapper_free(ptr, 0);
    }
    /**
     * Byte offset of the one `OscilluxDspFrameExt` input used by `processFrameExt`; its
     * `struct_size` is preset, so write only the descriptors and, for the relative level
     * source, the levels (layout as on `Dsp`).
     * @returns {number}
     */
    extInputPtr() {
        const ret = wasm.mapper_extInputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the one-frame `OscilluxDspFrame` input used by `processFrame`.
     * @returns {number}
     */
    frameInputPtr() {
        const ret = wasm.mapper_frameInputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {MappingConfig} config
     */
    constructor(config) {
        _assertClass(config, MappingConfig);
        const ret = wasm.mapper_new(config.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        MapperFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Byte offset of the `OscilluxMapped` output.
     * @returns {number}
     */
    outputPtr() {
        const ret = wasm.mapper_outputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * As `process`, with frame `index`'s descriptors from `dsp`'s last `pushExt` or
     * `pushExtF64` feeding FLUX and CENTROID (`oscillux_mapping_process_ext`), and its levels
     * feeding the relative level source; invalid after any other push.
     * @param {Dsp} dsp
     * @param {number} index
     * @param {boolean} unavailable
     * @returns {number}
     */
    processExt(dsp, index, unavailable) {
        _assertClass(dsp, Dsp);
        const ret = wasm.mapper_processExt(this.__wbg_ptr, dsp.__wbg_ptr, index, unavailable);
        return ret;
    }
    /**
     * Maps the frame at `frameInputPtr()` with the descriptors and levels at `extInputPtr()`;
     * returns a status.
     * @param {boolean} unavailable
     * @returns {number}
     */
    processFrameExt(unavailable) {
        const ret = wasm.mapper_processFrameExt(this.__wbg_ptr, unavailable);
        return ret;
    }
    /**
     * Maps the frame written at `frameInputPtr()`; returns a status.
     * @param {boolean} unavailable
     * @returns {number}
     */
    processFrame(unavailable) {
        const ret = wasm.mapper_processFrame(this.__wbg_ptr, unavailable);
        return ret;
    }
    /**
     * Maps frame `index` of `dsp`'s last `push` (index < produced()); returns a status.
     * `unavailable` writes all zeros.
     * @param {Dsp} dsp
     * @param {number} index
     * @param {boolean} unavailable
     * @returns {number}
     */
    process(dsp, index, unavailable) {
        _assertClass(dsp, Dsp);
        const ret = wasm.mapper_process(this.__wbg_ptr, dsp.__wbg_ptr, index, unavailable);
        return ret;
    }
    /**
     * Forgets the previous level; returns a status.
     * @returns {number}
     */
    reset() {
        const ret = wasm.mapper_reset(this.__wbg_ptr);
        return ret;
    }
    /**
     * Selects what BASS, MID, TREBLE and the spectrum read (a `LevelSource`): `Absolute`, the
     * default, the band energies; `Relative` the ext frame's relative band levels. The
     * relative source refuses `process` and `processFrame`, and `processExt` or
     * `processFrameExt` whose levels band count is not the frame's (0 when the analyzer
     * refused the AGC configuration) or whose relative bands are not finite. Returns a
     * status; an unknown source is refused and leaves the source unchanged. `reset` keeps it.
     * @param {number} source
     * @returns {number}
     */
    setLevelSource(source) {
        const ret = wasm.mapper_setLevelSource(this.__wbg_ptr, source);
        return ret;
    }
    /**
     * Spectrum bin `index` (0 ..= 63) from the last successful call, or undefined.
     * @param {number} index
     * @returns {number | undefined}
     */
    spectrum(index) {
        const ret = wasm.mapper_spectrum(this.__wbg_ptr, index);
        return ret === Number.MAX_SAFE_INTEGER ? undefined : ret;
    }
    /**
     * Mapped value `index` (a `MappedValue`) from the last successful call, or undefined for
     * an index out of range.
     * @param {number} index
     * @returns {number | undefined}
     */
    value(index) {
        const ret = wasm.mapper_value(this.__wbg_ptr, index);
        return ret[0] === 0 ? undefined : ret[1];
    }
}
if (Symbol.dispose) Mapper.prototype[Symbol.dispose] = Mapper.prototype.free;

/**
 * DSP-frame-to-value mapping (`OscilluxMappingConfig`); the constructor writes the values
 * matching the default `DspConfig`, `fromDsp` those matching any valid one.
 */
export class MappingConfig {
    static __wrap(ptr) {
        const obj = Object.create(MappingConfig.prototype);
        obj.__wbg_ptr = ptr;
        MappingConfigFinalization.register(obj, obj.__wbg_ptr, obj);
        return obj;
    }
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        MappingConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_mappingconfig_free(ptr, 0);
    }
    /**
     * r" Summed band energy at or below which the centroid is 0.
     * @returns {number}
     */
    get centroidFloor() {
        const ret = wasm.mappingconfig_centroid_floor(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Loudness mapped to amplitude 0, dB; negative.
     * @returns {number}
     */
    get dbFloor() {
        const ret = wasm.mappingconfig_db_floor(this.__wbg_ptr);
        return ret;
    }
    /**
     * The default mapping with hop and loudness reference taken from `dsp`; throws when `dsp`
     * is invalid.
     * @param {DspConfig} dsp
     * @returns {MappingConfig}
     */
    static fromDsp(dsp) {
        _assertClass(dsp, DspConfig);
        const ret = wasm.mappingconfig_fromDsp(dsp.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        return MappingConfig.__wrap(ret[0]);
    }
    /**
     * Band group `group` (0 bass, 1 mid, 2 treble) as `[start, end]`, inclusive.
     * @param {number} groupIndex
     * @returns {Uint32Array}
     */
    group(groupIndex) {
        const ret = wasm.mappingconfig_group(this.__wbg_ptr, groupIndex);
        if (ret[3]) {
            throw takeFromExternrefTable0(ret[2]);
        }
        var v1 = getArrayU32FromWasm0(ret[0], ret[1]).slice();
        wasm.__wbindgen_free(ret[0], ret[1] * 4, 4);
        return v1;
    }
    /**
     * r" Analyzer hop duration, s.
     * @returns {number}
     */
    get hopSeconds() {
        const ret = wasm.mappingconfig_hop_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Added back to a frame's loudnessDb, dB.
     * @returns {number}
     */
    get loudnessReference() {
        const ret = wasm.mappingconfig_loudness_reference(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.mappingconfig_new();
        this.__wbg_ptr = ret;
        MappingConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" Level decay time constant, s.
     * @returns {number}
     */
    get releaseSeconds() {
        const ret = wasm.mappingconfig_release_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {number} group
     * @param {number} start
     * @param {number} end
     */
    setGroup(group, start, end) {
        const ret = wasm.mappingconfig_setGroup(this.__wbg_ptr, group, start, end);
        if (ret[1]) {
            throw takeFromExternrefTable0(ret[0]);
        }
    }
    /**
     * @param {number} value
     */
    set centroidFloor(value) {
        wasm.mappingconfig_set_centroid_floor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set dbFloor(value) {
        wasm.mappingconfig_set_db_floor(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set hopSeconds(value) {
        wasm.mappingconfig_set_hop_seconds(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set loudnessReference(value) {
        wasm.mappingconfig_set_loudness_reference(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set releaseSeconds(value) {
        wasm.mappingconfig_set_release_seconds(this.__wbg_ptr, value);
    }
}
if (Symbol.dispose) MappingConfig.prototype[Symbol.dispose] = MappingConfig.prototype.free;

/**
 * Overlay presentation config (`OscilluxOverlayConfig`); the constructor writes the defaults.
 */
export class OverlayConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        OverlayConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_overlayconfig_free(ptr, 0);
    }
    /**
     * r" Output alpha outside text regions, [0, 1].
     * @returns {number}
     */
    get alpha() {
        const ret = wasm.overlayconfig_alpha(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" WCAG contrast ratio text regions guarantee, [1, 21]; default 4.5.
     * @returns {number}
     */
    get contrastRatio() {
        const ret = wasm.overlayconfig_contrast_ratio(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Milliseconds since the previous presented frame, (0, 1000].
     * @returns {number}
     */
    get frameIntervalMs() {
        const ret = wasm.overlayconfig_frame_interval_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Host presentation intensity before reduced motion, [0, 1].
     * @returns {number}
     */
    get intensity() {
        const ret = wasm.overlayconfig_intensity(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.overlayconfig_new();
        this.__wbg_ptr = ret;
        OverlayConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" Levels per channel: 0 for a float target or 2 ..= 65535; default 255.
     * @returns {number}
     */
    get outputLevels() {
        const ret = wasm.overlayconfig_output_levels(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * The canvas composites premultiplied alpha.
     * @returns {boolean}
     */
    get premultipliedAlpha() {
        const ret = wasm.overlayconfig_premultiplied_alpha(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * The intensity visuals present at: `intensity`, halved under reduced motion. Throws when
     * the config is refused.
     * @returns {number}
     */
    presentationIntensity() {
        const ret = wasm.overlayconfig_presentationIntensity(this.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        return ret[0];
    }
    /**
     * The user asked for reduced motion (`prefers-reduced-motion: reduce`).
     * @returns {boolean}
     */
    get reducedMotion() {
        const ret = wasm.overlayconfig_reduced_motion(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * @param {number} value
     */
    set alpha(value) {
        wasm.overlayconfig_set_alpha(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set contrastRatio(value) {
        wasm.overlayconfig_set_contrast_ratio(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set frameIntervalMs(value) {
        wasm.overlayconfig_set_frame_interval_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set intensity(value) {
        wasm.overlayconfig_set_intensity(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set outputLevels(value) {
        wasm.overlayconfig_set_output_levels(this.__wbg_ptr, value);
    }
    /**
     * @param {boolean} value
     */
    set premultipliedAlpha(value) {
        wasm.overlayconfig_set_premultiplied_alpha(this.__wbg_ptr, value);
    }
    /**
     * @param {boolean} value
     */
    set reducedMotion(value) {
        wasm.overlayconfig_set_reduced_motion(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set viewportHeight(value) {
        wasm.overlayconfig_set_viewport_height(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set viewportWidth(value) {
        wasm.overlayconfig_set_viewport_width(this.__wbg_ptr, value);
    }
    /**
     * Tightens `flash` for reduced motion (at most 1 general and 1 red flash a second over 0.1
     * of a field); never loosens it. Returns a status; a refusal leaves `flash` unchanged.
     * @param {FlashConfig} flash
     * @returns {number}
     */
    tightenFlashConfig(flash) {
        _assertClass(flash, FlashConfig);
        const ret = wasm.overlayconfig_tightenFlashConfig(this.__wbg_ptr, flash.__wbg_ptr);
        return ret;
    }
    /**
     * r" Drawing-buffer height in pixels.
     * @returns {number}
     */
    get viewportHeight() {
        const ret = wasm.overlayconfig_viewport_height(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Drawing-buffer width in pixels.
     * @returns {number}
     */
    get viewportWidth() {
        const ret = wasm.overlayconfig_viewport_width(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) OverlayConfig.prototype[Symbol.dispose] = OverlayConfig.prototype.free;

/**
 * `OverlayRegions.setRegion` mode bits; `LightText` and `DarkText` are exclusive.
 * @enum {1 | 2 | 4 | 8 | 16 | 32}
 */
export const OverlayMode = Object.freeze({
    /**
     * Hold the region's luminance at or below the ceiling, for light text.
     */
    LightText: 1, "1": "LightText",
    /**
     * Hold the region's luminance at or above the floor, for dark text.
     */
    DarkText: 2, "2": "DarkText",
    Desaturate: 4, "4": "Desaturate",
    Blur: 8, "8": "Blur",
    DampMotion: 16, "16": "DampMotion",
    /**
     * Hold the grid cells the region covers to the analyzer's `FlashOverlay`.
     */
    DampFlash: 32, "32": "DampFlash",
});

/**
 * Up to `OSCILLUX_OVERLAY_MAX_REGIONS` (8) overlay regions and the packed 320-byte
 * `OverlayUniforms` block. `pack` does not allocate.
 */
export class OverlayRegions {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        OverlayRegionsFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_overlayregions_free(ptr, 0);
    }
    /**
     * Regions in use, the first `count` of the array.
     * @returns {number}
     */
    get count() {
        const ret = wasm.overlayregions_count(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * No regions; the uniform block is zero.
     */
    constructor() {
        const ret = wasm.overlayregions_new();
        this.__wbg_ptr = ret;
        OverlayRegionsFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Packs the regions in use under `config` into the uniform block; returns a status. A
     * refusal leaves the block unchanged.
     * @param {OverlayConfig} config
     * @returns {number}
     */
    pack(config) {
        _assertClass(config, OverlayConfig);
        const ret = wasm.overlayregions_pack(this.__wbg_ptr, config.__wbg_ptr);
        return ret;
    }
    /**
     * Uses the first `count` regions (0 ..= 8); returns a status.
     * @param {number} count
     * @returns {number}
     */
    setCount(count) {
        const ret = wasm.overlayregions_setCount(this.__wbg_ptr, count);
        return ret;
    }
    /**
     * Writes region `index` (0 ..= 7) in viewport fractions, origin top-left; `mode` is
     * `OverlayMode` bits. Values are checked by `pack` and `FlashAnalyzer.setOverlay`.
     * @param {number} index
     * @param {number} x
     * @param {number} y
     * @param {number} width
     * @param {number} height
     * @param {number} cornerRadius
     * @param {number} feather
     * @param {number} strength
     * @param {number} mode
     * @returns {number}
     */
    setRegion(index, x, y, width, height, cornerRadius, feather, strength, mode) {
        const ret = wasm.overlayregions_setRegion(this.__wbg_ptr, index, x, y, width, height, cornerRadius, feather, strength, mode);
        return ret;
    }
    /**
     * Size of the block in bytes, 320.
     * @returns {number}
     */
    uniformsBytes() {
        const ret = wasm.overlayregions_uniformsBytes(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the packed `OverlayUniforms` block.
     * @returns {number}
     */
    uniformsPtr() {
        const ret = wasm.overlayregions_uniformsPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
}
if (Symbol.dispose) OverlayRegions.prototype[Symbol.dispose] = OverlayRegions.prototype.free;

/**
 * Explicit uniform block packer (`OscilluxPacker`) with preallocated float64 host values and
 * a `blockSize`-byte little-endian float32 output block.
 */
export class Packer {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        PackerFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_packer_free(ptr, 0);
    }
    /**
     * Size of the packed block, bytes.
     * @returns {number}
     */
    blockSize() {
        const ret = wasm.packer_blockSize(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Byte offset of the float64 host values.
     * @returns {number}
     */
    hostPtr() {
        const ret = wasm.packer_hostPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Validates the layout (at most 256 fields) for a `blockSize`-byte block and allocates
     * room for `hostCapacity` host values.
     * @param {readonly UniformField[]} fields
     * @param {number} blockSize
     * @param {number} hostCapacity
     */
    constructor(fields, blockSize, hostCapacity) {
        const ret = wasm.packer_new(fields, blockSize, hostCapacity);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        PackerFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Byte offset of the packed block.
     * @returns {number}
     */
    outputPtr() {
        const ret = wasm.packer_outputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Packs `mapper`'s last output and the first `hostCount` host values; returns a status.
     * A refusal leaves the block unchanged.
     * @param {Mapper} mapper
     * @param {number} hostCount
     * @returns {number}
     */
    pack(mapper, hostCount) {
        _assertClass(mapper, Mapper);
        const ret = wasm.packer_pack(this.__wbg_ptr, mapper.__wbg_ptr, hostCount);
        return ret;
    }
}
if (Symbol.dispose) Packer.prototype[Symbol.dispose] = Packer.prototype.free;

/**
 * Reaction sampler configuration (`OscilluxReactionConfig`); every time is in ms. The
 * constructor writes the defaults with the analysis latencies of DSP `behaviorVersion` at
 * `sampleRateHz` and throws for a version without measured latencies or a zero rate.
 */
export class ReactionConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        ReactionConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_reactionconfig_free(ptr, 0);
    }
    /**
     * r" Hop time to the audio its continuous values describe.
     * @returns {number}
     */
    get analysisLatencyMs() {
        const ret = wasm.reactionconfig_analysis_latency_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Release time constant of the bass impact.
     * @returns {number}
     */
    get bassImpactReleaseMs() {
        const ret = wasm.reactionconfig_bass_impact_release_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Hops queued, 1 ..= 65536.
     * @returns {number}
     */
    get capacity() {
        const ret = wasm.reactionconfig_capacity(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Frame presented to light emitted.
     * @returns {number}
     */
    get displayLatencyMs() {
        const ret = wasm.reactionconfig_display_latency_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Release time constant of the flux.
     * @returns {number}
     */
    get fluxReleaseMs() {
        const ret = wasm.reactionconfig_flux_release_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {number} behaviorVersion
     * @param {number} sampleRateHz
     */
    constructor(behaviorVersion, sampleRateHz) {
        const ret = wasm.reactionconfig_new(behaviorVersion, sampleRateHz);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        ReactionConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" Hop time to the audio its transients describe.
     * @returns {number}
     */
    get onsetLatencyMs() {
        const ret = wasm.reactionconfig_onset_latency_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Release time constant of the onset.
     * @returns {number}
     */
    get onsetReleaseMs() {
        const ret = wasm.reactionconfig_onset_release_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Audio written at the host time to audio heard.
     * @returns {number}
     */
    get outputLatencyMs() {
        const ret = wasm.reactionconfig_output_latency_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Share of an onset's phase error taken, 0 ..= 1.
     * @returns {number}
     */
    get phaseLockGain() {
        const ret = wasm.reactionconfig_phase_lock_gain(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Beat pulse decay time constant after the beat.
     * @returns {number}
     */
    get pulseDecayMs() {
        const ret = wasm.reactionconfig_pulse_decay_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Beat pulse Gaussian rise width before the beat.
     * @returns {number}
     */
    get pulseRiseMs() {
        const ret = wasm.reactionconfig_pulse_rise_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Sample time to frame presentation.
     * @returns {number}
     */
    get renderAheadMs() {
        const ret = wasm.reactionconfig_render_ahead_ms(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {number} value
     */
    set analysisLatencyMs(value) {
        wasm.reactionconfig_set_analysis_latency_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set bassImpactReleaseMs(value) {
        wasm.reactionconfig_set_bass_impact_release_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set capacity(value) {
        wasm.reactionconfig_set_capacity(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set displayLatencyMs(value) {
        wasm.reactionconfig_set_display_latency_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set fluxReleaseMs(value) {
        wasm.reactionconfig_set_flux_release_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set onsetLatencyMs(value) {
        wasm.reactionconfig_set_onset_latency_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set onsetReleaseMs(value) {
        wasm.reactionconfig_set_onset_release_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set outputLatencyMs(value) {
        wasm.reactionconfig_set_output_latency_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set phaseLockGain(value) {
        wasm.reactionconfig_set_phase_lock_gain(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set pulseDecayMs(value) {
        wasm.reactionconfig_set_pulse_decay_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set pulseRiseMs(value) {
        wasm.reactionconfig_set_pulse_rise_ms(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set renderAheadMs(value) {
        wasm.reactionconfig_set_render_ahead_ms(this.__wbg_ptr, value);
    }
}
if (Symbol.dispose) ReactionConfig.prototype[Symbol.dispose] = ReactionConfig.prototype.free;

/**
 * Reaction sampler (`OscilluxReactionSampler`) with a preallocated `OscilluxReaction` output:
 * the `OscilluxMapped` layout of `Mapper.outputPtr` (bytes 0 .. 328), then little-endian
 * Float64 at 328 (beat), 336 (downbeat), 344 (beatPhase), 352 (barPhase), 360 (tempoBpm), 368
 * (confidence), Int64 at 376 (beatCount) and Uint32 at 384 (beatInBar) and 388 (locked).
 * `push`, `pushWithBeat`, `sample` and the accessors do not allocate.
 */
export class ReactionSampler {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        ReactionSamplerFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_reactionsampler_free(ptr, 0);
    }
    /**
     * Output bar phase, [0, 1).
     * @returns {number}
     */
    barPhase() {
        const ret = wasm.reactionsampler_barPhase(this.__wbg_ptr);
        return ret;
    }
    /**
     * Beats counted at output wraps.
     * @returns {number}
     */
    beatCount() {
        const ret = wasm.reactionsampler_beatCount(this.__wbg_ptr);
        return ret;
    }
    /**
     * Index of the current beat within the bar; 0 is the downbeat.
     * @returns {number}
     */
    beatInBar() {
        const ret = wasm.reactionsampler_beatInBar(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Output beat phase, [0, 1).
     * @returns {number}
     */
    beatPhase() {
        const ret = wasm.reactionsampler_beatPhase(this.__wbg_ptr);
        return ret;
    }
    /**
     * Beat pulse, [0, 1]; 1 at the beat's photon time.
     * @returns {number}
     */
    beat() {
        const ret = wasm.reactionsampler_beat(this.__wbg_ptr);
        return ret;
    }
    /**
     * Returns the beat outputs to the tracker; returns a status.
     * @returns {number}
     */
    clearBeatClock() {
        const ret = wasm.reactionsampler_clearBeatClock(this.__wbg_ptr);
        return ret;
    }
    /**
     * Tracker confidence; 1 with a host clock; 0 without a reference.
     * @returns {number}
     */
    confidence() {
        const ret = wasm.reactionsampler_confidence(this.__wbg_ptr);
        return ret;
    }
    /**
     * Beat pulse of downbeats only, [0, 1].
     * @returns {number}
     */
    downbeat() {
        const ret = wasm.reactionsampler_downbeat(this.__wbg_ptr);
        return ret;
    }
    /**
     * Whether a reference drives the beat outputs.
     * @returns {boolean}
     */
    locked() {
        const ret = wasm.reactionsampler_locked(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * @param {ReactionConfig} config
     */
    constructor(config) {
        _assertClass(config, ReactionConfig);
        const ret = wasm.reactionsampler_new(config.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        ReactionSamplerFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Byte offset of the `OscilluxReaction` output.
     * @returns {number}
     */
    outputPtr() {
        const ret = wasm.reactionsampler_outputPtr(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * As `push`, with the tracker output of frame `index` of `dsp`'s last `pushExt` or
     * `pushExtF64`; invalid after any other push.
     * @param {number} mediaTimeMs
     * @param {Mapper} mapper
     * @param {Dsp} dsp
     * @param {number} index
     * @returns {number}
     */
    pushWithBeat(mediaTimeMs, mapper, dsp, index) {
        _assertClass(mapper, Mapper);
        _assertClass(dsp, Dsp);
        const ret = wasm.reactionsampler_pushWithBeat(this.__wbg_ptr, mediaTimeMs, mapper.__wbg_ptr, dsp.__wbg_ptr, index);
        return ret;
    }
    /**
     * Queues `mapper`'s last output as the hop whose analysis window ends at `mediaTimeMs`,
     * without tracker output; returns a status.
     * @param {number} mediaTimeMs
     * @param {Mapper} mapper
     * @returns {number}
     */
    push(mediaTimeMs, mapper) {
        _assertClass(mapper, Mapper);
        const ret = wasm.reactionsampler_push(this.__wbg_ptr, mediaTimeMs, mapper.__wbg_ptr);
        return ret;
    }
    /**
     * Hops queued and not yet consumed.
     * @returns {number}
     */
    queued() {
        const ret = wasm.reactionsampler_queued(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Forgets every hop, held peak, the beat clock and the beat state; returns a status.
     * @returns {number}
     */
    reset() {
        const ret = wasm.reactionsampler_reset(this.__wbg_ptr);
        return ret;
    }
    /**
     * Writes the values seen at the photon time of a frame sampled at host media time `timeMs`
     * to the output; returns a status.
     * @param {number} timeMs
     * @returns {number}
     */
    sample(timeMs) {
        const ret = wasm.reactionsampler_sample(this.__wbg_ptr, timeMs);
        return ret;
    }
    /**
     * Drives the beat outputs from `clock` instead of the tracker; returns a status.
     * @param {BeatClock} clock
     * @returns {number}
     */
    setBeatClock(clock) {
        _assertClass(clock, BeatClock);
        const ret = wasm.reactionsampler_setBeatClock(this.__wbg_ptr, clock.__wbg_ptr);
        return ret;
    }
    /**
     * Spectrum bin `index` (0 ..= 63) from the last sample, or undefined.
     * @param {number} index
     * @returns {number | undefined}
     */
    spectrum(index) {
        const ret = wasm.reactionsampler_spectrum(this.__wbg_ptr, index);
        return ret === Number.MAX_SAFE_INTEGER ? undefined : ret;
    }
    /**
     * Tempo of the reference clock; 0 without one.
     * @returns {number}
     */
    tempoBpm() {
        const ret = wasm.reactionsampler_tempoBpm(this.__wbg_ptr);
        return ret;
    }
    /**
     * Mapped value `index` (a `MappedValue`) from the last sample, or undefined.
     * @param {number} index
     * @returns {number | undefined}
     */
    value(index) {
        const ret = wasm.reactionsampler_value(this.__wbg_ptr, index);
        return ret[0] === 0 ? undefined : ret[1];
    }
}
if (Symbol.dispose) ReactionSampler.prototype[Symbol.dispose] = ReactionSampler.prototype.free;

/**
 * Transition scheduler (`OscilluxTransition`). `request` and `update` take the clock as
 * numbers; the getters read the status of the last successful call.
 */
export class Transition {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        TransitionFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_transition_free(ptr, 0);
    }
    /**
     * True when the last update issued an automatic request.
     * @returns {boolean}
     */
    get autoRequested() {
        const ret = wasm.transition_auto_requested(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * Time the blend ends, s.
     * @returns {number}
     */
    get endSeconds() {
        const ret = wasm.transition_end_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {TransitionConfig} config
     */
    constructor(config) {
        _assertClass(config, TransitionConfig);
        const ret = wasm.transition_new(config.__wbg_ptr);
        if (ret[2]) {
            throw takeFromExternrefTable0(ret[1]);
        }
        this.__wbg_ptr = ret[0];
        TransitionFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * Blend progress in [0, 1]; exactly 1 from the end of the blend.
     * @returns {number}
     */
    get progress() {
        const ret = wasm.transition_progress(this.__wbg_ptr);
        return ret;
    }
    /**
     * Quantisation applied to the start: 0 none, 1 beat, 2 bar.
     * @returns {number}
     */
    get quantise() {
        const ret = wasm.transition_quantise(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Schedules a switch requested at `timeSeconds`; quantise 0 none, 1 beat, 2 bar, waiting
     * at most `maxWaitSeconds` past the pre-roll. Returns a status; a refusal changes nothing.
     * @param {number} timeSeconds
     * @param {number} beatPhase
     * @param {number} barPhase
     * @param {number} tempoBpm
     * @param {number} confidence
     * @param {number} quantise
     * @param {number} maxWaitSeconds
     * @returns {number}
     */
    request(timeSeconds, beatPhase, barPhase, tempoBpm, confidence, quantise, maxWaitSeconds) {
        const ret = wasm.transition_request(this.__wbg_ptr, timeSeconds, beatPhase, barPhase, tempoBpm, confidence, quantise, maxWaitSeconds);
        return ret;
    }
    /**
     * Time of the request, s.
     * @returns {number}
     */
    get requestSeconds() {
        const ret = wasm.transition_request_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * Returns to idle and restarts the auto-advance count.
     * @returns {number}
     */
    reset() {
        const ret = wasm.transition_reset(this.__wbg_ptr);
        return ret;
    }
    /**
     * Time the blend starts, s.
     * @returns {number}
     */
    get startSeconds() {
        const ret = wasm.transition_start_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * `OSCILLUX_TRANSITION_STATE_*`: 0 idle, 1 pre-roll, 2 waiting, 3 blending, 4 done.
     * @returns {number}
     */
    get state() {
        const ret = wasm.transition_state(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * Evaluates the schedule at `timeSeconds` without changing it.
     * @param {number} timeSeconds
     * @returns {number}
     */
    status(timeSeconds) {
        const ret = wasm.transition_status(this.__wbg_ptr, timeSeconds);
        return ret;
    }
    /**
     * Per-frame evaluation at `timeSeconds`, issuing an automatic request when one is due.
     * @param {number} timeSeconds
     * @param {number} beatPhase
     * @param {number} barPhase
     * @param {number} tempoBpm
     * @param {number} confidence
     * @returns {number}
     */
    update(timeSeconds, beatPhase, barPhase, tempoBpm, confidence) {
        const ret = wasm.transition_update(this.__wbg_ptr, timeSeconds, beatPhase, barPhase, tempoBpm, confidence);
        return ret;
    }
}
if (Symbol.dispose) Transition.prototype[Symbol.dispose] = Transition.prototype.free;

/**
 * Transition scheduler parameters (`OscilluxTransitionConfig`); the constructor writes the
 * reference values.
 */
export class TransitionConfig {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        TransitionConfigFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_transitionconfig_free(ptr, 0);
    }
    /**
     * r" Bars between automatic switches; 0 disables auto-advance.
     * @returns {number}
     */
    get autoAdvanceBars() {
        const ret = wasm.transitionconfig_auto_advance_bars(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Beats per bar, 1 to 16.
     * @returns {number}
     */
    get beatsPerBar() {
        const ret = wasm.transitionconfig_beats_per_bar(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Blend length in beats on a confident clock, clamped to 0.8 to 2.5 s.
     * @returns {number}
     */
    get durationBeats() {
        const ret = wasm.transitionconfig_duration_beats(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Progress easing: 0 linear, 1 smoothstep.
     * @returns {number}
     */
    get easing() {
        const ret = wasm.transitionconfig_easing(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * r" Blend length without a confident beat, s; 0.8 to 2.5.
     * @returns {number}
     */
    get fallbackSeconds() {
        const ret = wasm.transitionconfig_fallback_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * r" Lowest clock confidence that counts as a beat.
     * @returns {number}
     */
    get minConfidence() {
        const ret = wasm.transitionconfig_min_confidence(this.__wbg_ptr);
        return ret;
    }
    constructor() {
        const ret = wasm.transitionconfig_new();
        this.__wbg_ptr = ret;
        TransitionConfigFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * r" Time the incoming visual runs offscreen before it may start, s.
     * @returns {number}
     */
    get preRollSeconds() {
        const ret = wasm.transitionconfig_pre_roll_seconds(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {number} value
     */
    set autoAdvanceBars(value) {
        wasm.transitionconfig_set_auto_advance_bars(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set beatsPerBar(value) {
        wasm.transitionconfig_set_beats_per_bar(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set durationBeats(value) {
        wasm.transitionconfig_set_duration_beats(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set easing(value) {
        wasm.transitionconfig_set_easing(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set fallbackSeconds(value) {
        wasm.transitionconfig_set_fallback_seconds(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set minConfidence(value) {
        wasm.transitionconfig_set_min_confidence(this.__wbg_ptr, value);
    }
    /**
     * @param {number} value
     */
    set preRollSeconds(value) {
        wasm.transitionconfig_set_pre_roll_seconds(this.__wbg_ptr, value);
    }
}
if (Symbol.dispose) TransitionConfig.prototype[Symbol.dispose] = TransitionConfig.prototype.free;

/**
 * Returns `OSCILLUX_ABI_VERSION` of the linked core.
 * @returns {number}
 */
export function abiVersion() {
    const ret = wasm.abiVersion();
    return ret >>> 0;
}

/**
 * The `z` component of the vivid palette tour at `timeSeconds` for a visual whose palette
 * starts at `offset`; the `palette` uniform is `[-1, 0, z, 0]` (`oscillux_palette_tour`).
 * Throws on a non-finite argument. Does not allocate, so hosts may call it every frame.
 * @param {number} time_seconds
 * @param {number} offset
 * @returns {number}
 */
export function paletteTour(time_seconds, offset) {
    const ret = wasm.paletteTour(time_seconds, offset);
    if (ret[2]) {
        throw takeFromExternrefTable0(ret[1]);
    }
    return ret[0];
}

/**
 * Short English description of a status code (0 ok, -1 null, -2 invalid argument, -3 panic).
 * @param {number} status
 * @returns {string}
 */
export function statusString(status) {
    let deferred1_0;
    let deferred1_1;
    try {
        const ret = wasm.statusString(status);
        deferred1_0 = ret[0];
        deferred1_1 = ret[1];
        return getStringFromWasm0(ret[0], ret[1]);
    } finally {
        wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
    }
}

/**
 * Release version of the linked core, packed as `major << 16 | minor << 8 | patch`.
 * @returns {number}
 */
export function version() {
    const ret = wasm.version();
    return ret >>> 0;
}

/**
 * The vivid present pass's `[saturation, vibrance]` (`oscillux_palette_vivid_present`).
 * Allocates the returned array; read it once, not per frame.
 * @returns {Float64Array}
 */
export function vividPresent() {
    const ret = wasm.vividPresent();
    var v1 = getArrayF64FromWasm0(ret[0], ret[1]).slice();
    wasm.__wbindgen_free(ret[0], ret[1] * 8, 8);
    return v1;
}

/**
 * The wasm memory every `*Ptr()` offset refers to. Obtain views after constructing adapters
 * and recreate them after any memory growth.
 * @returns {WebAssembly.Memory}
 */
export function wasmMemory() {
    const ret = wasm.wasmMemory();
    return ret;
}
function __wbg_get_imports() {
    const import0 = {
        __proto__: null,
        __wbg_Error_208446807d0d1e30: function(arg0, arg1) {
            const ret = Error(getStringFromWasm0(arg0, arg1));
            return ret;
        },
        __wbg___wbindgen_memory_17a1178bc3328ea3: function() {
            const ret = wasm.memory;
            return ret;
        },
        __wbg___wbindgen_throw_4c88d8bf264fd984: function(arg0, arg1) {
            throw new Error(getStringFromWasm0(arg0, arg1));
        },
        __wbg_components_66b0e13e4a4ab63f: function(arg0) {
            const ret = arg0.components;
            return ret;
        },
        __wbg_get_855af080df0afad5: function(arg0, arg1) {
            const ret = arg0[arg1 >>> 0];
            return ret;
        },
        __wbg_length_6057a25b69092db2: function(arg0) {
            const ret = arg0.length;
            return ret;
        },
        __wbg_offset_b97f54e1b13d5222: function(arg0) {
            const ret = arg0.offset;
            return ret;
        },
        __wbg_source_698a2c1f119ab383: function(arg0) {
            const ret = arg0.source;
            return ret;
        },
        __wbg_source_offset_7c7e2c2a27d0e1f5: function(arg0) {
            const ret = arg0.sourceOffset;
            return ret;
        },
        __wbg_stride_b51925357fdb14ef: function(arg0) {
            const ret = arg0.stride;
            return ret;
        },
        __wbindgen_init_externref_table: function() {
            const table = wasm.__wbindgen_externrefs;
            table.set(0, undefined);
            table.set(1024, undefined);
            table.set(1025, null);
            table.set(1026, true);
            table.set(1027, false);
        },
    };
    return {
        __proto__: null,
        "./oscillux_bg.js": import0,
    };
}

const BeatClockFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_beatclock_free(ptr, 1));
const BeatGridFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_beatgrid_free(ptr, 1));
const BeatGridConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_beatgridconfig_free(ptr, 1));
const CdgFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_cdg_free(ptr, 1));
const CurveFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_curve_free(ptr, 1));
const CurveConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_curveconfig_free(ptr, 1));
const CurveInputFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_curveinput_free(ptr, 1));
const DspFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_dsp_free(ptr, 1));
const DspConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_dspconfig_free(ptr, 1));
const FlashAnalyzerFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_flashanalyzer_free(ptr, 1));
const FlashConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_flashconfig_free(ptr, 1));
const FlashOverlayFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_flashoverlay_free(ptr, 1));
const FlashOverlayReportFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_flashoverlayreport_free(ptr, 1));
const GovernorFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_governor_free(ptr, 1));
const GovernorConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_governorconfig_free(ptr, 1));
const LimiterFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_limiter_free(ptr, 1));
const LimiterPolicyFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_limiterpolicy_free(ptr, 1));
const MapperFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_mapper_free(ptr, 1));
const MappingConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_mappingconfig_free(ptr, 1));
const OverlayConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_overlayconfig_free(ptr, 1));
const OverlayRegionsFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_overlayregions_free(ptr, 1));
const PackerFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_packer_free(ptr, 1));
const ReactionConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_reactionconfig_free(ptr, 1));
const ReactionSamplerFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_reactionsampler_free(ptr, 1));
const TransitionFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_transition_free(ptr, 1));
const TransitionConfigFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_transitionconfig_free(ptr, 1));

function _assertClass(instance, klass) {
    if (!(instance instanceof klass)) {
        throw new Error(`expected instance of ${klass.name}`);
    }
}

function getArrayF64FromWasm0(ptr, len) {
    ptr = ptr >>> 0;
    return getFloat64ArrayMemory0().subarray(ptr / 8, ptr / 8 + len);
}

function getArrayU32FromWasm0(ptr, len) {
    ptr = ptr >>> 0;
    return getUint32ArrayMemory0().subarray(ptr / 4, ptr / 4 + len);
}

let cachedFloat32ArrayMemory0 = null;
function getFloat32ArrayMemory0() {
    if (cachedFloat32ArrayMemory0 === null || cachedFloat32ArrayMemory0.byteLength === 0) {
        cachedFloat32ArrayMemory0 = new Float32Array(wasm.memory.buffer);
    }
    return cachedFloat32ArrayMemory0;
}

let cachedFloat64ArrayMemory0 = null;
function getFloat64ArrayMemory0() {
    if (cachedFloat64ArrayMemory0 === null || cachedFloat64ArrayMemory0.byteLength === 0) {
        cachedFloat64ArrayMemory0 = new Float64Array(wasm.memory.buffer);
    }
    return cachedFloat64ArrayMemory0;
}

function getStringFromWasm0(ptr, len) {
    ptr = ptr >>> 0;
    return decodeText(getUint8ArrayMemory0().subarray(ptr, ptr + len));
}

let cachedUint32ArrayMemory0 = null;
function getUint32ArrayMemory0() {
    if (cachedUint32ArrayMemory0 === null || cachedUint32ArrayMemory0.byteLength === 0) {
        cachedUint32ArrayMemory0 = new Uint32Array(wasm.memory.buffer);
    }
    return cachedUint32ArrayMemory0;
}

let cachedUint8ArrayMemory0 = null;
function getUint8ArrayMemory0() {
    if (cachedUint8ArrayMemory0 === null || cachedUint8ArrayMemory0.byteLength === 0) {
        cachedUint8ArrayMemory0 = new Uint8Array(wasm.memory.buffer);
    }
    return cachedUint8ArrayMemory0;
}

function isLikeNone(x) {
    return x === undefined || x === null;
}

function passArray8ToWasm0(arg, malloc) {
    const ptr = malloc(arg.length * 1, 1) >>> 0;
    getUint8ArrayMemory0().set(arg, ptr / 1);
    WASM_VECTOR_LEN = arg.length;
    return ptr;
}

function passArrayF32ToWasm0(arg, malloc) {
    const ptr = malloc(arg.length * 4, 4) >>> 0;
    getFloat32ArrayMemory0().set(arg, ptr / 4);
    WASM_VECTOR_LEN = arg.length;
    return ptr;
}

function takeFromExternrefTable0(idx) {
    const value = wasm.__wbindgen_externrefs.get(idx);
    wasm.__externref_table_dealloc(idx);
    return value;
}

let cachedTextDecoder = new TextDecoder('utf-8', { ignoreBOM: true });
cachedTextDecoder.decode();
const MAX_SAFARI_DECODE_BYTES = 2146435072;
let numBytesDecoded = 0;
function decodeText(bytes) {
    numBytesDecoded += bytes.length;
    if (numBytesDecoded >= MAX_SAFARI_DECODE_BYTES) {
        cachedTextDecoder = new TextDecoder('utf-8', { ignoreBOM: true });
        cachedTextDecoder.decode();
        numBytesDecoded = bytes.length;
    }
    return cachedTextDecoder.decode(bytes);
}

let WASM_VECTOR_LEN = 0;

let wasmModule, wasmInstance, wasm;
function __wbg_finalize_init(instance, module) {
    wasmInstance = instance;
    wasm = instance.exports;
    wasmModule = module;
    cachedFloat32ArrayMemory0 = null;
    cachedFloat64ArrayMemory0 = null;
    cachedUint32ArrayMemory0 = null;
    cachedUint8ArrayMemory0 = null;
    wasm.__wbindgen_start();
    return wasm;
}

async function __wbg_load(module, imports) {
    if (typeof Response === 'function' && module instanceof Response) {
        if (!module.ok) {
            throw new Error(`failed to fetch Wasm: ${module.status} ${module.statusText} fetching '${module.url}'`);
        }

        if (typeof WebAssembly.instantiateStreaming === 'function') {
            try {
                return await WebAssembly.instantiateStreaming(module, imports);
            } catch (e) {
                const validResponse = expectedResponseType(module.type);

                if (validResponse && module.headers.get('Content-Type') !== 'application/wasm') {
                    console.warn("`WebAssembly.instantiateStreaming` failed because your server does not serve Wasm with `application/wasm` MIME type. Falling back to `WebAssembly.instantiate` which is slower. Original error:\n", e);

                } else { throw e; }
            }
        }

        const bytes = await module.arrayBuffer();
        return await WebAssembly.instantiate(bytes, imports);
    } else {
        const instance = await WebAssembly.instantiate(module, imports);

        if (instance instanceof WebAssembly.Instance) {
            return { instance, module };
        } else {
            return instance;
        }
    }

    function expectedResponseType(type) {
        switch (type) {
            case 'basic': case 'cors': case 'default': return true;
        }
        return false;
    }
}

function initSync(module) {
    if (wasm !== undefined) return wasm;


    if (module !== undefined) {
        if (Object.getPrototypeOf(module) === Object.prototype) {
            ({module} = module)
        } else {
            console.warn('using deprecated parameters for `initSync()`; pass a single object instead')
        }
    }

    const imports = __wbg_get_imports();
    if (!(module instanceof WebAssembly.Module)) {
        module = new WebAssembly.Module(module);
    }
    const instance = new WebAssembly.Instance(module, imports);
    return __wbg_finalize_init(instance, module);
}

let __wbg_init_promise;
async function __wbg_init(module_or_path) {
    if (wasm !== undefined) return wasm;
    if (__wbg_init_promise === undefined) {
        __wbg_init_promise = __wbg_init_once(module_or_path);
        __wbg_init_promise.catch(() => { __wbg_init_promise = undefined; });
    }
    return __wbg_init_promise;
}

async function __wbg_init_once(module_or_path) {

    if (module_or_path !== undefined) {
        if (Object.getPrototypeOf(module_or_path) === Object.prototype) {
            ({module_or_path} = module_or_path)
        } else {
            console.warn('using deprecated parameters for the initialization function; pass a single object instead')
        }
    }

    if (module_or_path === undefined) {
        module_or_path = new URL('oscillux_bg.wasm', import.meta.url);
    }
    const imports = __wbg_get_imports();

    if (typeof module_or_path === 'string' || (typeof Request === 'function' && module_or_path instanceof Request) || (typeof URL === 'function' && module_or_path instanceof URL)) {
        module_or_path = fetch(module_or_path);
    }

    const { instance, module } = await __wbg_load(await module_or_path, imports);

    return __wbg_finalize_init(instance, module);
}

export { initSync, __wbg_init as default };
