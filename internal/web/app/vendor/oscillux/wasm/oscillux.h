/* SPDX-License-Identifier: AGPL-3.0-only */
/*
 * C interface to the Oscillux core.
 *
 * Handles are opaque; every buffer is owned by the caller. Functions returning int32_t return
 * OSCILLUX_OK or a negative OSCILLUX_ERR_* status; oscillux_status_string() describes one. A
 * panic inside the library is caught at the boundary and reported as OSCILLUX_ERR_PANIC; it
 * never unwinds into the caller.
 *
 * After any call on a handle returns OSCILLUX_ERR_PANIC, the handle's state is unspecified,
 * because partial changes are not rolled back. This applies to every handle type (OscilluxDsp,
 * OscilluxLimiter, OscilluxFlashAnalyzer, OscilluxCurve, OscilluxGovernor, OscilluxMapper,
 * OscilluxPacker, OscilluxReactionSampler, OscilluxCdg). The caller must make no further calls on
 * that handle other than its destroy function, which stays safe, and must create a new handle to
 * continue. Output buffers and
 * results from the failed call are not valid results.
 *
 * Units. Times are milliseconds where a name ends in _ms and seconds where it ends in _seconds;
 * frequencies are hertz (_hz); levels ending in _db or _lufs are decibels. Sizes, offsets and
 * strides of byte buffers are bytes; counts of samples, frames, bands, fields and values are
 * element counts. "Normalized" means a value in [0, 1]. Every public flag is a uint32_t that
 * must be 0 or 1 unless it is documented as a bitmask.
 *
 * Threading. The library keeps no global or thread-local mutable state and creates no threads.
 *  - oscillux_abi_version, oscillux_version, oscillux_status_string, the *_config_default and
 *    *_policy_default functions, oscillux_mapping_config_from_dsp and
 *    oscillux_dsp_config_validate touch only their arguments and may be called from any number
 *    of threads at once.
 *  - Handles are not internally synchronized. Different handles are independent and may be used
 *    concurrently from different threads.
 *  - Calls on one handle must not overlap. A handle may move between threads (created on one,
 *    used or destroyed on another) provided the caller orders the calls, for example with a
 *    mutex or a queue. Exception: oscillux_curve_evaluate and oscillux_packer_pack take a const
 *    handle, do not modify it, and may run concurrently with each other on the same handle.
 *  - Destroy must not overlap any other call on the same handle, and the handle must not be
 *    used after destroy.
 * Bindings may be stricter: the Java binding confines each handle to the thread that created it.
 *
 * Linking. On Windows the library ships as oscillux.dll with import library oscillux.dll.lib,
 * and OSCILLUX_API expands to __declspec(dllimport). Define OSCILLUX_STATIC before including
 * this header to link a static build instead, and OSCILLUX_BUILD when compiling code that
 * exports these functions from a DLL. Elsewhere OSCILLUX_API expands to nothing.
 *
 * ABI. OSCILLUX_ABI_VERSION changes only on an incompatible change: a changed signature, struct
 * layout or status value, or a removed function. Adding a function keeps the version, so a
 * library reporting the same version may predate a function declared here; linking or symbol
 * lookup then fails instead of misbehaving. oscillux_mapping_config_from_dsp,
 * oscillux_status_string, oscillux_dsp_config_validate and oscillux_version were added within
 * ABI version 1.
 *
 * Versions. OSCILLUX_VERSION_MAJOR, _MINOR and _PATCH are the release version of this header,
 * and OSCILLUX_VERSION packs them as major << 16 | minor << 8 | patch. oscillux_version()
 * returns the linked library's version packed the same way. A release that adds a function
 * increments the version, and a change of OSCILLUX_ABI_VERSION comes with a new release; builds
 * between releases report the last release's version. Check once at startup, before any other
 * call; when this holds, every function and type declared here is available as declared:
 *
 *     oscillux_abi_version() == OSCILLUX_ABI_VERSION && oscillux_version() >= OSCILLUX_VERSION
 *
 * A different ABI version means signatures, layouts or statuses differ: do not use that
 * library. The same ABI version with an older library version means functions added since may
 * be missing; use only those its release declares (CHANGELOG.md). docs/api-stability.md gives
 * the full versioning and deprecation policy.
 *
 * Extensible structs. A configuration or policy struct added from now on begins with
 * `uint32_t struct_size`, which the caller sets to sizeof the struct as this header declares
 * it before passing it to any function, including its *_default function. Members are only
 * appended, never reordered, retyped or removed, and each documents its default. A function
 * reads and writes only the members within struct_size and uses the documented default for any
 * member beyond it. It returns OSCILLUX_ERR_INVALID_ARGUMENT when struct_size is not the size
 * of that struct in some released header. The configuration and policy structs declared below
 * predate this rule: their layouts stay fixed for ABI version 1, and the next ABI version adds
 * struct_size to each of them. Per-frame and array-element structs never carry struct_size; a
 * change to one takes a new struct and function, or a new ABI version. The one exception is
 * OscilluxDspFrameExt, the opt-in per-frame output array: its sections are only appended, every
 * element carries struct_size, which may be the end of any section, and that size is also the
 * array stride (see its declaration).
 *
 * `cargo test -p oscillux-ffi` fails when a prototype here and an exported function disagree,
 * when a #define here and the Rust value it mirrors disagree, when the version here and the
 * crate version differ, or when a function, constant or struct layout here differs from the
 * committed snapshot ffi/abi-snapshot.txt (`make abi-snapshot` regenerates it).
 */
#ifndef OSCILLUX_H
#define OSCILLUX_H

#include <stddef.h>
#include <stdint.h>

#if defined(_WIN32) && !defined(OSCILLUX_STATIC)
#if defined(OSCILLUX_BUILD)
#define OSCILLUX_API __declspec(dllexport)
#else
#define OSCILLUX_API __declspec(dllimport)
#endif
#else
#define OSCILLUX_API
#endif

#ifdef __cplusplus
extern "C" {
#endif

/* ==== Common ============================================================================== */

#define OSCILLUX_ABI_VERSION 1u

/* Release version of this header; equals the crate version in Cargo.toml. */
#define OSCILLUX_VERSION_MAJOR 0u
#define OSCILLUX_VERSION_MINOR 2u
#define OSCILLUX_VERSION_PATCH 7u
/* OSCILLUX_VERSION_MAJOR << 16 | OSCILLUX_VERSION_MINOR << 8 | OSCILLUX_VERSION_PATCH */
#define OSCILLUX_VERSION 519u

/* Status: success. */
#define OSCILLUX_OK 0
/* Status: a required pointer argument was null. */
#define OSCILLUX_ERR_NULL (-1)
/* Status: an argument, configuration or input value was out of range. */
#define OSCILLUX_ERR_INVALID_ARGUMENT (-2)
/* Status: an internal panic was caught; destroy the handle (see above). */
#define OSCILLUX_ERR_PANIC (-3)

/* Returns OSCILLUX_ABI_VERSION of the linked library. */
OSCILLUX_API uint32_t oscillux_abi_version(void);

/* Returns the linked library's release version, packed as OSCILLUX_VERSION (see "Versions"). */
OSCILLUX_API uint32_t oscillux_version(void);

/*
 * Returns a short English description of status, such as "invalid argument", for logs. The
 * string is static, NUL-terminated and never null; do not free or modify it. An unknown status
 * returns "unknown status". Does not allocate, lock or perform I/O.
 */
OSCILLUX_API const char *oscillux_status_string(int32_t status);

/* ==== DSP ================================================================================= */
/*
 * Streaming audio analysis: interleaved PCM in, one frame per hop out, carrying RMS, loudness,
 * log-spaced band energies and onsets. Only oscillux_dsp_create allocates; the other calls do
 * not allocate, lock or perform I/O.
 */

/* Capacity of OscilluxDspFrame.spectrum and the largest spectrum_bands. */
#define OSCILLUX_DSP_MAX_BANDS 64
/* Number of onset band groups: low, mid, high. */
#define OSCILLUX_DSP_ONSET_GROUPS 3

/* Frame flag: the segment started more than gap_tolerance_hops after the previous frame. */
#define OSCILLUX_DSP_FLAG_GAP 1u
/* Frame flag: the segment was begun with stalled != 0. */
#define OSCILLUX_DSP_FLAG_STALLED 2u
/* Frame flag: an input sample since the previous frame was replaced with 0 (see push_f32). */
#define OSCILLUX_DSP_FLAG_INPUT_REPLACED 4u

/* DSP behaviour version 1: the original analysis, frozen bit for bit by conformance/dsp. Every
 * analyzer made by oscillux_dsp_create runs it. */
#define OSCILLUX_DSP_BEHAVIOR_V1 1u
/* DSP behaviour version 2 (conformance/dsp/v2): disjoint triangular band weights with a
 * decimated bass analysis, energy-flux onsets with an absolute floor, and a cheaper hop. */
#define OSCILLUX_DSP_BEHAVIOR_V2 2u
/* DSP behaviour version 3: version 2 analysis frames with a beat tracker that finds tempo by
 * autocorrelating the summed onset groups into a histogram that decays over 4 s, from 60 to
 * 200 BPM (conformance/dsp/music/README.md, "Version 3"). */
#define OSCILLUX_DSP_BEHAVIOR_V3 3u
/* DSP behaviour version 4: version 2 analysis frames with a causal beat-pointer beat tracker
 * over every integer beat period from 60 to 200 BPM, reported at the frame's media time, with
 * a bar-position downbeat stage (conformance/dsp/music/README.md, "Beat tracker, version 4"). */
#define OSCILLUX_DSP_BEHAVIOR_V4 4u

/* Beat analysis latency per behaviour version, ms: the mean time from an onset to the media time
 * of the frame whose beat output reports it, measured on click and kick trains from 60 to
 * 200 BPM at 44.1 and 48 kHz (conformance/dsp/music/README.md, "Beat timing"). A host that times
 * a reaction to the beat subtracts the constant of the version it runs. */
#define OSCILLUX_DSP_BEAT_LATENCY_V1_MS 16u
#define OSCILLUX_DSP_BEAT_LATENCY_V2_MS 14u
#define OSCILLUX_DSP_BEAT_LATENCY_V3_MS 16u
#define OSCILLUX_DSP_BEAT_LATENCY_V4_MS 3u

/* Analysis parameters; oscillux_dsp_config_default writes the reference values. */
typedef struct OscilluxDspConfig {
    uint32_t sample_rate_hz;        /* input sample rate, Hz; > 0 */
    uint32_t fft_window_samples;    /* window and FFT length, samples; power of two, 2 ..= 65536 */
    uint32_t hop_samples;           /* samples between frames; > 0 */
    uint32_t spectrum_bands;        /* log-spaced bands, 1 ..= OSCILLUX_DSP_MAX_BANDS */
    double spectrum_min_hz;         /* lower edge of the first band, Hz; > 0 */
    double spectrum_max_hz;         /* upper band edge, Hz; > min, <= sample_rate_hz / 2 */
    double db_floor;                /* band level mapped to 0, dB; < 0 (0 dB maps to 1) */
    double ballistics_attack_ms;    /* band smoothing time constant while rising, ms; > 0 */
    double ballistics_release_ms;   /* band smoothing time constant while falling, ms; > 0 */
    double rms_window_ms;           /* RMS window, ms; 1 ..= 2^24 samples long */
    double loudness_reference_lufs; /* subtracted from the RMS level for loudness_db, dB */
    double rms_floor;               /* linear RMS lower bound before the logarithm; > 0 */
    double onset_peak_decay;        /* per-frame decay factor of running onset peaks; 0 ..= 1 */
    double onset_peak_floor;        /* lower bound of an onset peak used as a divisor; > 0 */
    /* Inclusive band index ranges [start, end], start <= end < spectrum_bands. */
    uint32_t onset_band_groups[OSCILLUX_DSP_ONSET_GROUPS][2];
    uint32_t gap_tolerance_hops;    /* segment lateness tolerated before FLAG_GAP, hops */
} OscilluxDspConfig;

/* Onset strengths, normalized: broadband and the three onset band groups. */
typedef struct OscilluxDspOnset {
    double broadband;
    double low;
    double mid;
    double high;
} OscilluxDspOnset;

/* One analysis frame, emitted every hop once a full window is available. */
typedef struct OscilluxDspFrame {
    uint64_t sequence;    /* frame index since the analyzer was created */
    uint32_t flags;       /* OSCILLUX_DSP_FLAG_*; GAP and STALLED only on a segment's first frame */
    uint32_t band_count;  /* valid entries in spectrum */
    double media_time_ms; /* media time of the analysis-window end, ms */
    double rms;           /* linear RMS over the RMS window; full scale = 1 */
    double loudness_db;   /* RMS level in dB minus loudness_reference_lufs, dB */
    OscilluxDspOnset onset;
    double spectrum[OSCILLUX_DSP_MAX_BANDS]; /* normalized band energies; zero past band_count */
} OscilluxDspFrame;

typedef struct OscilluxDspPushResult {
    size_t consumed; /* sample frames consumed from the input */
    size_t produced; /* frames written to out */
} OscilluxDspPushResult;

typedef struct OscilluxDsp OscilluxDsp;

/* Writes the reference configuration (48000 Hz, 2048-sample window, 512-sample hop, 24 bands)
 * to *out. */
OSCILLUX_API int32_t oscillux_dsp_config_default(OscilluxDspConfig *out);

/*
 * Checks *config exactly as oscillux_dsp_create does, without allocating. Returns OSCILLUX_OK,
 * OSCILLUX_ERR_INVALID_ARGUMENT, or OSCILLUX_ERR_NULL when config is null. out_field may be null.
 * Otherwise it receives the name of the first offending OscilluxDspConfig member, spelled as in
 * this header (for example "fft_window_samples" or "onset_band_groups"), or "config" when no
 * single member is named; it receives null on OSCILLUX_OK and OSCILLUX_ERR_NULL. The name is
 * static and NUL-terminated; do not free it.
 */
OSCILLUX_API int32_t oscillux_dsp_config_validate(const OscilluxDspConfig *config,
                                                  const char **out_field);

/* Validates *config and creates an analyzer; *out_dsp is null on failure. Call
 * oscillux_dsp_config_validate to learn which member was refused. */
OSCILLUX_API int32_t oscillux_dsp_create(const OscilluxDspConfig *config, OscilluxDsp **out_dsp);

/*
 * A configuration and the DSP behaviour version that interprets it. OscilluxDspConfig predates
 * struct_size, so the functions above keep behaviour version 1; this struct selects a version.
 * Set struct_size to sizeof(OscilluxDspVersionedConfig) before every call, including the
 * default call. Members are only appended after reserved, which fills the tail so that the
 * next appended member changes sizeof.
 */
typedef struct OscilluxDspVersionedConfig {
    uint32_t struct_size;      /* sizeof(OscilluxDspVersionedConfig), the only released size */
    OscilluxDspConfig config;  /* analysis parameters; default as oscillux_dsp_config_default */
    uint32_t behavior_version; /* OSCILLUX_DSP_BEHAVIOR_V1, _V2, _V3 or _V4; default _V2 */
    uint32_t reserved;         /* must be 0 */
} OscilluxDspVersionedConfig;

/* Writes the reference configuration at OSCILLUX_DSP_BEHAVIOR_V2, with reserved 0, to *out.
 * Returns OSCILLUX_ERR_INVALID_ARGUMENT, writing nothing, when out->struct_size is not the
 * released size. */
OSCILLUX_API int32_t oscillux_dsp_versioned_config_default(OscilluxDspVersionedConfig *out);

/*
 * Checks *config exactly as oscillux_dsp_create_versioned does, without allocating; statuses and
 * out_field as for oscillux_dsp_config_validate. On OSCILLUX_ERR_INVALID_ARGUMENT out_field
 * receives "struct_size" when that is not the released size, else the first offending config
 * member, else "behavior_version" when it is not a released behaviour version, else
 * "reserved" when it is not 0.
 */
OSCILLUX_API int32_t oscillux_dsp_versioned_config_validate(
    const OscilluxDspVersionedConfig *config, const char **out_field);

/* Validates *config and creates an analyzer running config->behavior_version; *out_dsp is null
 * on failure. Every other oscillux_dsp_* function accepts the handle. */
OSCILLUX_API int32_t oscillux_dsp_create_versioned(const OscilluxDspVersionedConfig *config,
                                                   OscilluxDsp **out_dsp);

/* Frees an analyzer from either create function; null is ignored. */
OSCILLUX_API void oscillux_dsp_destroy(OscilluxDsp *dsp);

/* Starts a new input segment whose first sample plays at media_start_ms (ms); stalled is nonzero
 * when capture stalled before it. A non-finite media_start_ms returns
 * OSCILLUX_ERR_INVALID_ARGUMENT and leaves the current segment unchanged. */
OSCILLUX_API int32_t oscillux_dsp_begin_segment(OscilluxDsp *dsp,
                                                double media_start_ms,
                                                uint32_t stalled);

/*
 * Consumes up to sample_frames interleaved frames of `channels` (nonzero) samples, full scale
 * 1.0, and writes at most out_capacity analysis frames to out; *result reports both counts.
 * Once out is full it stops before the sample that would complete another frame; call again
 * with the unconsumed rest. samples and out may be null when their length is zero. A sample
 * that is NaN, infinite or larger in magnitude than FLT_MAX is replaced with 0 and the next
 * frame carries OSCILLUX_DSP_FLAG_INPUT_REPLACED; every output stays finite.
 */
OSCILLUX_API int32_t oscillux_dsp_push_f32(OscilluxDsp *dsp,
                                           const float *samples,
                                           size_t sample_frames,
                                           uint32_t channels,
                                           OscilluxDspFrame *out,
                                           size_t out_capacity,
                                           OscilluxDspPushResult *result);

/* Double-precision input; same state, status and backpressure rules as push_f32.
 * This additive entry point preserves samples already mixed/resampled in double precision.
 * All nonempty buffers must be aligned and live for the call. Input, output and result must
 * not overlap; the live handle, output and result require exclusive access (no concurrent calls).
 * Buffers remain caller-owned and are neither retained nor freed. Only read result/output
 * as successful results after OSCILLUX_OK. After OSCILLUX_ERR_PANIC, discard/recreate the
 * analyzer: partial state changes are not rolled back. Pointer validity is a caller obligation;
 * null/count checks and panic catching do not contain invalid pointers or aliasing.
 * Qualified PCM is finite normalized input, including bounded gain-scaled values in [-2, 2].
 * NaN, infinity and magnitudes above FLT_MAX are replaced as for push_f32. */
OSCILLUX_API int32_t oscillux_dsp_push_f64(OscilluxDsp *dsp,
                                           const double *samples,
                                           size_t sample_frames,
                                           uint32_t channels,
                                           OscilluxDspFrame *out,
                                           size_t out_capacity,
                                           OscilluxDspPushResult *result);

/*
 * Opt-in descriptors of one frame, computed on every hop whichever push function is used and
 * written only by the push_ext functions. Loudness, width and balance read the input channels
 * before mixdown, K-weighted as in ITU-R BS.1770-4 (high shelf, then the RLB high-pass, which
 * also removes DC) with coefficients derived for sample_rate_hz; below 8000 Hz they are not
 * measured (loudness -120, width and balance 0). Loudness weights channels 1 for L, R, C, 0 for
 * LFE and 1.41 for Ls, Rs in 5-channel (L R C Ls Rs), 6-channel (L R C LFE Ls Rs) and 8-channel
 * (L R C LFE Ls Rs Lrs Rrs, ITU-R BS.2051 order; rear surrounds 1) input, 1 otherwise; a 7.1
 * stream in WAVEFORMATEXTENSIBLE order (back pair before side pair) weighs its back pair 1.41.
 * Channels past the eighth are ignored. Every window ends at the analysis-window end
 * and is clipped to the current segment, like rms. OscilluxDspFrame.loudness_db is unchanged:
 * it remains the unweighted RMS level minus loudness_reference_lufs, not LUFS.
 */
typedef struct OscilluxDspDescriptors {
    double momentary_lufs;  /* BS.1770-4 momentary loudness, 400 ms window, LUFS; >= -120 */
    double short_term_lufs; /* EBU R 128 short-term loudness, 3 s window, LUFS; >= -120 */
    /* Side-energy fraction E_S / (E_M + E_S) of channels 0 and 1 over the 400 ms window, with
     * M = (L + R) / 2 and S = (L - R) / 2; 0 ..= 1: 0 identical channels, 0.5 one silent channel
     * or uncorrelated equal channels, 1 anti-phase. 0 for mono input and silence. */
    double width;
    /* (E_R - E_L) / (E_R + E_L) of channels 0 and 1 over the 400 ms window; -1 ..= 1: -1 left
     * only, 1 right only. 0 for mono input and silence. */
    double balance;
    /* Spectral centroid of the analysis window, sum(f_k |X_k|) / sum(|X_k|) over FFT bins
     * 0 ..= fft_window_samples / 2 with f_k = k * sample_rate_hz / fft_window_samples, Hz;
     * 0 for a zero spectrum. */
    double centroid_hz;
    /* Half-wave rectified flux, sum(max(0, |X_k| - |P_k|)) / sum(|X_k|) against the previous
     * frame's spectrum P; 0 ..= 1. 0 on the analyzer's first frame and for a zero spectrum. */
    double spectral_flux;
} OscilluxDspDescriptors;

/*
 * Beat tracker and adaptive level outputs of one frame (conformance/dsp/music/README.md),
 * computed from the analysis frames on every hop whichever push function is used and written
 * only by the push_ext functions. The analyzer runs the tracker for 80 to 160 BPM in 4/4, and the
 * normaliser with a 4 s time constant, a 12 dB minimum range and a noise gate 36 dB below each
 * level's reference, a maximum that relaxes over 600 s. The gate is relative, so a quiet master
 * reads like a loud one down to db_floor; levels below db_floor read as db_floor, so a host
 * expecting quiet input lowers db_floor. A section stays all zero when the configuration cannot
 * run those settings: a frame rate (sample_rate_hz / hop_samples) below 64/3 or above 4096/3
 * frames per second for the tracker, or a db_floor above -12 dB for the normaliser.
 */

/* Beat tracker output for one analysis frame. Every member is 0 until the first tempo lock. */
typedef struct OscilluxDspBeat {
    double tempo_bpm;     /* current tempo, beats per minute */
    double beat_phase;    /* position within the current beat, [0, 1); 0 is the beat */
    double bar_phase;     /* position within the current bar, [0, 1); 0 is the downbeat */
    double confidence;    /* [0, 1]; 0 from sixteen beats after the last onset */
    uint32_t beat;        /* 1 on the frame a beat is emitted, else 0; none from sixteen beats
                             after the last onset until the next onset */
    uint32_t downbeat;    /* 1 when this frame's beat is the first of a bar, else 0 */
    uint32_t beat_in_bar; /* index of the current beat within the bar; 0 is the downbeat */
    uint32_t reserved;    /* always 0 */
} OscilluxDspBeat;

/* Adaptive levels for one analysis frame: each level relative to its own recent range in dB,
 * and the onset band groups' absolute levels. Every value is in [0, 1]. A relative level reads
 * in full while its recent maximum is within 24 dB of the loudest it has been over the last
 * minutes, is scaled down from 24 dB below, and is gated to 0 from 36 dB below. */
typedef struct OscilluxDspLevels {
    uint32_t band_count; /* valid entries in relative_bands */
    uint32_t reserved;   /* always 0 */
    double relative_groups[OSCILLUX_DSP_ONSET_GROUPS]; /* onset band group levels, relative */
    double absolute_groups[OSCILLUX_DSP_ONSET_GROUPS]; /* mean band level of each onset group */
    double relative_bands[OSCILLUX_DSP_MAX_BANDS];     /* band levels, relative; zero past
                                                          band_count */
} OscilluxDspLevels;

/*
 * Opt-in outputs written beside one OscilluxDspFrame. Set struct_size in every element to
 * sizeof(OscilluxDspFrameExt) as this header declares it and reserved to 0; struct_size is also
 * the stride of the array. struct_size may be any section end: 56 (through descriptors, the size
 * in the first header that declared this struct), 104 (through beat) or 672 (through levels,
 * this header). No header has declared 104; it is accepted so a caller can take the beat
 * without the 568-byte levels section. Sections are only appended after the last one, never
 * moved, retyped or given a new meaning, so every section end stays a valid layout and a caller
 * built against an older header keeps working: a push writes only the sections that end within
 * struct_size and leaves struct_size and reserved as set.
 */
typedef struct OscilluxDspFrameExt {
    uint32_t struct_size;               /* a section end; the same in every element */
    uint32_t reserved;                  /* must be 0 */
    OscilluxDspDescriptors descriptors; /* first section; ends at 56 */
    OscilluxDspBeat beat;               /* ends at 104 */
    OscilluxDspLevels levels;           /* ends at 672 */
} OscilluxDspFrameExt;

/*
 * As oscillux_dsp_push_f32, and also writes ext[i] for every frame written to out[i]. Frames and
 * analyzer state equal those of oscillux_dsp_push_f32 bit for bit. ext holds out_capacity
 * elements of ext->struct_size bytes each, aligned as OscilluxDspFrameExt, disjoint from the
 * other buffers; it may be null when out_capacity is 0. Before any input is consumed, a null
 * ext with nonzero out_capacity returns OSCILLUX_ERR_NULL, and OSCILLUX_ERR_INVALID_ARGUMENT is
 * returned when ext is misaligned, its struct_size is not a section end, or any element has a
 * different struct_size or a nonzero reserved.
 */
OSCILLUX_API int32_t oscillux_dsp_push_ext_f32(OscilluxDsp *dsp,
                                               const float *samples,
                                               size_t sample_frames,
                                               uint32_t channels,
                                               OscilluxDspFrame *out,
                                               OscilluxDspFrameExt *ext,
                                               size_t out_capacity,
                                               OscilluxDspPushResult *result);

/* oscillux_dsp_push_f64 with the ext output of oscillux_dsp_push_ext_f32. */
OSCILLUX_API int32_t oscillux_dsp_push_ext_f64(OscilluxDsp *dsp,
                                               const double *samples,
                                               size_t sample_frames,
                                               uint32_t channels,
                                               OscilluxDspFrame *out,
                                               OscilluxDspFrameExt *ext,
                                               size_t out_capacity,
                                               OscilluxDspPushResult *result);

/* ==== Limiter ============================================================================= */
/* Proxy policy: rendered-pixel compliance requires host measurement. See conformance/limiter.
 * Each ceiling may be tightened below its default; none may be loosened past it. */
typedef struct OscilluxLimiterPolicy {
    double max_flash_hz;        /* general flashes per one-second window; > 0, at most 3 */
    double max_luminance_delta; /* ceiling on any onset magnitude, normalized; > 0, at most 0.8 */
    double max_flash_area;      /* screen fraction at or below which a change is no flash;
                                   > 0, at most 0.25 */
    uint32_t max_red_flashes;   /* red flashes per one-second window; 0 .. 3 */
    uint32_t strobe_banned;     /* must equal 1; never a C/Rust bool */
} OscilluxLimiterPolicy;

/* One frame of host-supplied proxies; the limiter does not measure rendered pixels. */
typedef struct OscilluxLimiterFrame {
    double time_ms;   /* presentation time, ms */
    double broadband; /* broadband onset, the luminance-change proxy; normalized */
    double low;       /* low-band onset; normalized */
    double mid;       /* mid-band onset; normalized */
    double high;      /* high-band onset; normalized */
    double red;       /* saturated-red transition magnitude; normalized */
    double area;      /* screen fraction the change covers; normalized */
} OscilluxLimiterFrame;

typedef struct OscilluxLimiter OscilluxLimiter;

/* Writes the default policy, the loosest one create accepts, to *out. */
OSCILLUX_API int32_t oscillux_limiter_policy_default(OscilluxLimiterPolicy *out);
/* Validates *policy and creates a limiter; *out_limiter is null on failure. */
OSCILLUX_API int32_t oscillux_limiter_create(const OscilluxLimiterPolicy *policy,
                                             OscilluxLimiter **out_limiter);
/* Frees a limiter; null is ignored. */
OSCILLUX_API void oscillux_limiter_destroy(OscilluxLimiter *limiter);
/*
 * Reset starts a new timeline (a seek or new stream): the next frame may carry any finite
 * timestamp. Admitted flashes keep their age; create a new limiter to forget them.
 */
OSCILLUX_API int32_t oscillux_limiter_reset(OscilluxLimiter *limiter);
/*
 * Each scalar must be finite in [0,1]; timestamps must be finite and nondecreasing.
 * Invalid input returns OSCILLUX_ERR_INVALID_ARGUMENT and leaves state unchanged. On any error
 * with a non-null out, process writes the fail-safe frame (every scalar 0, time_ms from the
 * input, or NaN without one); hosts must render it. A forward gap above 250 ms credits no
 * elapsed time. Input/output may alias each other.
 * Only create allocates. Reset and process perform no allocation, locking or I/O.
 */
OSCILLUX_API int32_t oscillux_limiter_process(OscilluxLimiter *limiter,
                                              const OscilluxLimiterFrame *frame,
                                              OscilluxLimiterFrame *out);

/*
 * Region flash analyzer. Measures flashes from host-measured per-region relative luminance and
 * saturated red of the rendered (or about-to-render) image, instead of proxies, with the WCAG
 * 2.3.1 rules (conformance/limiter/README.md, "Region flash analyzer"):
 *  - general flash: a pair of opposing relative-luminance changes of at least 0.1 where the
 *    darker image is below 0.8;
 *  - red flash, by default (OSCILLUX_FLASH_RED_RULE_WCAG20): a pair of opposing changes of
 *    more than 20/320 in (R - G - B) of saturated red, a color whose R / (R + G + B) is at least
 *    0.8 (WCAG 2.0 "general flash and red flash thresholds" note 3, after Harding and Binnie);
 *  - red flash, selectable (OSCILLUX_FLASH_RED_RULE_ISO_9241_391): a pair of opposing
 *    transitions where one is to or from a state with R / (R + G + B) of at least 0.8 and the
 *    states differ by more than 0.2 in the CIE 1976 UCS (u', v') diagram (WCAG 2.2 note 3,
 *    after ISO 9241-391), with linear R, G, B taken to XYZ by the WCAG 2.2 Understanding
 *    matrix. Needs whole colors: frames go through the _rgb functions. Black has no
 *    chromaticity and counts against a saturated red; any two transitions in a row make a
 *    pair (conformance/limiter/README.md gives the model and its conservative choices);
 *  - area: a frame is a transition when the regions completing a change inside any field window
 *    (default 1/3 x 1/3 of the viewport, about a 10-degree field, 0.024 sr) cover more than
 *    max_field_area (default 0.25, the 0.006 sr limit) of that window.
 * The host divides the viewport into columns x rows regions (row-major, at most
 * OSCILLUX_FLASH_MAX_GRID per axis) and supplies their mean levels each frame, for example via
 * oscillux_flash_region_from_linear_rgb, or their mean linear colors (either rule). Pattern
 * (spatial) detection is not supported; neither is HDR luminance above the display's normalized
 * white.
 */
#define OSCILLUX_FLASH_MAX_GRID 16u
/* OscilluxFlashConfig.red_rule values. */
#define OSCILLUX_FLASH_RED_RULE_WCAG20 0u
#define OSCILLUX_FLASH_RED_RULE_ISO_9241_391 1u

/* Analyzer configuration. Members are append-only; struct_size selects the released layout. */
typedef struct OscilluxFlashConfig {
    uint32_t struct_size;     /* sizeof(OscilluxFlashConfig); 48, the first released size
                                 (through max_flash_hz), uses the default red_rule */
    uint32_t columns;         /* regions across the viewport; 1 .. OSCILLUX_FLASH_MAX_GRID */
    uint32_t rows;            /* regions down the viewport; 1 .. OSCILLUX_FLASH_MAX_GRID */
    uint32_t max_red_flashes; /* red flashes per one-second window; 0 .. 3 */
    double field_width;       /* field-window width as a viewport fraction; > 0, at most 1;
                                 default 1/3 (a 10-degree field at the WCAG viewing distance) */
    double field_height;      /* field-window height as a viewport fraction; > 0, at most 1;
                                 default 1/3 */
    double max_field_area;    /* field-window fraction at or below which a change is no flash;
                                 > 0, at most 0.25 */
    double max_flash_hz;      /* general flashes per one-second window; > 0, at most 3 */
    uint32_t red_rule;        /* OSCILLUX_FLASH_RED_RULE_*; default WCAG20 */
    uint32_t reserved;        /* must be 0 */
} OscilluxFlashConfig;

/* One region's levels for one frame; an array element. */
typedef struct OscilluxFlashRegion {
    double luminance; /* relative luminance, normalized */
    double red;       /* saturated red: (R - G - B) when saturated, else 0; normalized */
} OscilluxFlashRegion;

/* One region's mean linear-light sRGB color for one frame, each channel in [0,1]; an array
 * element of the _rgb functions. */
typedef struct OscilluxFlashColor {
    double r;
    double g;
    double b;
} OscilluxFlashColor;

/* Result of one measured frame. Flags are 0 or 1. */
typedef struct OscilluxFlashReport {
    double time_ms;               /* the frame's presentation time, ms */
    double flashes_in_window;     /* general flashes (transitions / 2) in the last 1000 ms */
    double red_flashes_in_window; /* red flashes (transitions / 2) in the last 1000 ms */
    uint32_t admitted;            /* the frame was committed; 0 means show the last admitted one */
    uint32_t general_transition;  /* the frame completes a general transition */
    uint32_t red_transition;      /* the frame completes a red transition */
    uint32_t general_regions;     /* regions completing a general change this frame */
    uint32_t red_regions;         /* regions completing a red change this frame */
    uint32_t exceeds;             /* the window holds more flashes than the config allows */
} OscilluxFlashReport;

typedef struct OscilluxFlashAnalyzer OscilluxFlashAnalyzer;

/*
 * Writes the default config, the loosest one create accepts, to the first out->struct_size
 * bytes of *out. out->struct_size must already equal sizeof(OscilluxFlashConfig) or 48 (the
 * first released size); any other size returns OSCILLUX_ERR_INVALID_ARGUMENT and leaves *out
 * unchanged.
 */
OSCILLUX_API int32_t oscillux_flash_config_default(OscilluxFlashConfig *out);
/*
 * Validates the first config->struct_size bytes of *config, with defaults beyond them, and
 * creates an analyzer; *out_analyzer is null on failure. An unknown red_rule or a nonzero
 * reserved returns OSCILLUX_ERR_INVALID_ARGUMENT.
 */
OSCILLUX_API int32_t oscillux_flash_analyzer_create(const OscilluxFlashConfig *config,
                                                    OscilluxFlashAnalyzer **out_analyzer);
/* Frees an analyzer; null is ignored. */
OSCILLUX_API void oscillux_flash_analyzer_destroy(OscilluxFlashAnalyzer *analyzer);
/*
 * Reset starts a new timeline (a seek or new stream): the next frame may carry any finite
 * timestamp. Counted transitions keep their age and regions keep their last image; create a new
 * analyzer to forget them.
 */
OSCILLUX_API int32_t oscillux_flash_analyzer_reset(OscilluxFlashAnalyzer *analyzer);
/*
 * Measures one frame of region_count (columns * rows) regions and always commits it.
 * Each level must be finite in [0,1]; timestamps must be finite and nondecreasing. The first
 * frame is the baseline. Invalid input returns OSCILLUX_ERR_INVALID_ARGUMENT and leaves state
 * unchanged. On any error with a non-null out, writes a report with every other member 0 and
 * time_ms from the input. A forward gap above 250 ms credits no elapsed time. Under
 * OSCILLUX_FLASH_RED_RULE_ISO_9241_391, which needs colors, every frame returns
 * OSCILLUX_ERR_INVALID_ARGUMENT with that report and leaves state unchanged; use measure_rgb.
 * Only create allocates. Reset, measure, admit and their _rgb forms perform no allocation,
 * locking or I/O.
 */
OSCILLUX_API int32_t oscillux_flash_analyzer_measure(OscilluxFlashAnalyzer *analyzer,
                                                     double time_ms,
                                                     const OscilluxFlashRegion *regions,
                                                     size_t region_count,
                                                     OscilluxFlashReport *out);
/*
 * As measure, but commits the frame only if it keeps the window within the config. On
 * out->admitted == 0 every region keeps its tracker state and the windows keep every unexpired
 * transition, and the host must keep presenting its last admitted image; admitted images,
 * measured again, never exceed. The refused frame's time still counts: it becomes the previous
 * frame time for the gap rule, it ends a pending discontinuity (first frame after reset), and
 * transitions 1000 ms old at that time leave the windows.
 */
OSCILLUX_API int32_t oscillux_flash_analyzer_admit(OscilluxFlashAnalyzer *analyzer,
                                                   double time_ms,
                                                   const OscilluxFlashRegion *regions,
                                                   size_t region_count,
                                                   OscilluxFlashReport *out);
/*
 * As measure, from region_count mean linear colors (each channel finite in [0,1]), under
 * either red rule. Under the WCAG 2.0 rule the reports equal measuring
 * oscillux_flash_region_from_linear_rgb of each color.
 */
OSCILLUX_API int32_t oscillux_flash_analyzer_measure_rgb(OscilluxFlashAnalyzer *analyzer,
                                                         double time_ms,
                                                         const OscilluxFlashColor *colors,
                                                         size_t region_count,
                                                         OscilluxFlashReport *out);
/* As admit, from mean linear colors, under either red rule. */
OSCILLUX_API int32_t oscillux_flash_analyzer_admit_rgb(OscilluxFlashAnalyzer *analyzer,
                                                       double time_ms,
                                                       const OscilluxFlashColor *colors,
                                                       size_t region_count,
                                                       OscilluxFlashReport *out);
/*
 * Writes the region levels of a mean linear-light sRGB color, each channel finite in [0,1]:
 * luminance = 0.2126 r + 0.7152 g + 0.0722 b; red = r - g - b when r / (r + g + b) >= 0.8,
 * else 0. Invalid channels return OSCILLUX_ERR_INVALID_ARGUMENT and leave *out unchanged. Does
 * not allocate, lock or perform I/O.
 */
OSCILLUX_API int32_t oscillux_flash_region_from_linear_rgb(double r, double g, double b,
                                                           OscilluxFlashRegion *out);

/* ==== Overlay regions: UI over visuals with guaranteed contrast ============================ */
/*
 * Host UI (menus, captions, cards) sits over the visuals in up to OSCILLUX_OVERLAY_MAX_REGIONS
 * rectangles. oscillux_overlay_pack writes the std140 OverlayUniforms block
 * (conformance/packing/overlay-uniforms.schema.json) the shared present pass reads: in a
 * LIGHT_TEXT region every output pixel's relative luminance is held at or below
 * luminance_ceiling = (1.05 / contrast_ratio - 0.05) - 1e-4, so white text keeps that ratio;
 * in a DARK_TEXT region at or above luminance_floor = (0.05 contrast_ratio - 0.05) + 1e-4, so
 * black text does. DESATURATE, BLUR and DAMP_MOTION calm the region; DAMP_FLASH marks it for
 * the flash analyzer's stricter overlay (oscillux_flash_analyzer_set_overlay). Regions are
 * rounded rectangles in viewport fractions, origin top-left, y down. Every call here is pure and performs no
 * allocation, locking or I/O. Policy (which UI, when) stays with the host.
 */
#define OSCILLUX_OVERLAY_MAX_REGIONS 8u
#define OSCILLUX_OVERLAY_UNIFORMS_SIZE 320u
/* OscilluxOverlayRegion.mode bits; LIGHT_TEXT and DARK_TEXT are exclusive. */
#define OSCILLUX_OVERLAY_MODE_LIGHT_TEXT 1u
#define OSCILLUX_OVERLAY_MODE_DARK_TEXT 2u
#define OSCILLUX_OVERLAY_MODE_DESATURATE 4u
#define OSCILLUX_OVERLAY_MODE_BLUR 8u
#define OSCILLUX_OVERLAY_MODE_DAMP_MOTION 16u
#define OSCILLUX_OVERLAY_MODE_DAMP_FLASH 32u

/* One overlay region; an array element. */
typedef struct OscilluxOverlayRegion {
    double x;             /* left edge, viewport-width fraction; finite */
    double y;             /* top edge, viewport-height fraction; finite */
    double width;         /* viewport-width fraction, finite and >= 0 */
    double height;        /* viewport-height fraction, finite and >= 0 */
    double corner_radius; /* viewport-height fraction in [0,1]; clamped to half the shorter
                             side */
    double feather;       /* soft edge outside the rect, viewport-height fraction in [0,1]; the
                             contrast bound holds in full inside the rect */
    double strength;      /* desaturate, blur and damping strength in [0,1]; the contrast
                             bound does not depend on it */
    uint32_t mode;        /* OSCILLUX_OVERLAY_MODE_* bits */
    uint32_t reserved;    /* must be 0 */
} OscilluxOverlayRegion;

/* Overlay presentation config. Flags are 0 or 1; other values are refused. */
typedef struct OscilluxOverlayConfig {
    uint32_t struct_size;         /* sizeof(OscilluxOverlayConfig) */
    uint32_t reduced_motion;      /* the platform's reduce-motion preference */
    uint32_t premultiplied_alpha; /* the output surface composites premultiplied alpha */
    uint32_t output_levels;       /* levels per channel: 0 for a float target or 2 .. 65535;
                                     default 255; text-region pixels round toward the bound */
    double viewport_width;        /* pixels, finite and > 0 */
    double viewport_height;       /* pixels, finite and > 0 */
    double contrast_ratio;        /* WCAG ratio against white (light) or black (dark) text,
                                     [1, 21]; default 4.5 */
    double intensity;             /* host presentation intensity before reduced motion, [0,1] */
    double frame_interval_ms;     /* ms since the previous presented frame, (0, 1000];
                                     scales damping */
    double alpha;                 /* output alpha outside text regions, [0,1]; text regions are
                                     opaque */
} OscilluxOverlayConfig;

/*
 * Writes the default config. out->struct_size must already equal
 * sizeof(OscilluxOverlayConfig); any other size returns OSCILLUX_ERR_INVALID_ARGUMENT and
 * leaves *out unchanged.
 */
OSCILLUX_API int32_t oscillux_overlay_config_default(OscilluxOverlayConfig *out);
/*
 * Packs the OverlayUniforms block into the first OSCILLUX_OVERLAY_UNIFORMS_SIZE bytes of out
 * (little-endian f32, unused regions zero); bytes beyond are not touched. regions may be null
 * when count is 0. Invalid config or regions, more than OSCILLUX_OVERLAY_MAX_REGIONS, or
 * capacity below OSCILLUX_OVERLAY_UNIFORMS_SIZE return OSCILLUX_ERR_INVALID_ARGUMENT with out
 * unchanged.
 */
OSCILLUX_API int32_t oscillux_overlay_pack(const OscilluxOverlayConfig *config,
                                           const OscilluxOverlayRegion *regions,
                                           size_t count,
                                           uint8_t *out,
                                           size_t capacity);
/*
 * Writes the intensity visuals should present at: config->intensity, halved when
 * reduced_motion is set.
 */
OSCILLUX_API int32_t oscillux_overlay_presentation_intensity(const OscilluxOverlayConfig *config,
                                                             double *out);
/*
 * Tightens *inout, a flash config of any released struct_size, for config->reduced_motion: at
 * most 1 general and 1 red flash a second over at most 0.1 of a field window. Never loosens a
 * value; without reduced_motion *inout is unchanged. An invalid config or *inout returns
 * OSCILLUX_ERR_INVALID_ARGUMENT with *inout unchanged.
 */
OSCILLUX_API int32_t oscillux_overlay_flash_config(const OscilluxOverlayConfig *config,
                                                   OscilluxFlashConfig *inout);

/* Stricter flash rules for the grid cells DAMP_FLASH overlay regions cover. */
typedef struct OscilluxFlashOverlay {
    uint32_t struct_size;    /* sizeof(OscilluxFlashOverlay) */
    uint32_t reserved;       /* must be 0 */
    double luminance_change; /* smallest change that counts inside, (0, 0.1]; default 0.05 */
    double darker_limit;     /* a change counts once its darker end is below this, [0.8, 1];
                                default 1 */
    double max_flash_hz;     /* overlay flashes a second, [0, config max_flash_hz]; default 1 */
    double max_area;         /* a frame is an overlay transition when more than this fraction
                                of overlay cells change, [0, config max_field_area]; default 0
                                (one cell) */
} OscilluxFlashOverlay;

/* Overlay outcome of the frame last measured or admitted. Flags are 0 or 1. */
typedef struct OscilluxFlashOverlayReport {
    uint32_t struct_size;     /* sizeof(OscilluxFlashOverlayReport); set by the caller */
    uint32_t active;          /* an overlay is set */
    uint32_t transition;      /* the frame holds an overlay transition */
    uint32_t regions;         /* overlay cells completing a change this frame */
    uint32_t cells;           /* grid cells the overlay covers */
    uint32_t exceeds;         /* the overlay window holds more flashes than it allows */
    double flashes_in_window; /* overlay flashes (transitions / 2) in the last 1000 ms */
} OscilluxFlashOverlayReport;

/*
 * Writes the default flash overlay. out->struct_size must already equal
 * sizeof(OscilluxFlashOverlay); any other size returns OSCILLUX_ERR_INVALID_ARGUMENT.
 */
OSCILLUX_API int32_t oscillux_flash_overlay_default(OscilluxFlashOverlay *out);
/*
 * Holds the grid cells the DAMP_FLASH regions (with strength > 0, expanded by their feather)
 * cover to *overlay from the next frame; a null overlay clears it and ignores regions. Opt-in:
 * without an overlay the analyzer is unchanged, and measure reports never depend on it; admit
 * also refuses a frame that would put the overlay window over its rate. Values looser than the
 * analyzer's config, or invalid regions, return OSCILLUX_ERR_INVALID_ARGUMENT and keep the
 * previous overlay. Call when the UI layout changes, not per frame.
 */
OSCILLUX_API int32_t oscillux_flash_analyzer_set_overlay(OscilluxFlashAnalyzer *analyzer,
                                                         const OscilluxFlashOverlay *overlay,
                                                         const OscilluxOverlayRegion *regions,
                                                         size_t count);
/*
 * Writes the overlay report. out->struct_size must already equal
 * sizeof(OscilluxFlashOverlayReport). Does not allocate, lock or perform I/O.
 */
OSCILLUX_API int32_t oscillux_flash_analyzer_overlay_report(const OscilluxFlashAnalyzer *analyzer,
                                                            OscilluxFlashOverlayReport *out);

/* ==== Curves, mapping, governor and explicit uniform blocks ================================ */
/*
 * Pointers must be null or valid/aligned; output and live handles must be exclusive and
 * disjoint from input storage. Empty host arrays accept null. Create copies configuration.
 * Only create/destroy allocate/free. All other calls perform no allocation, locking or I/O.
 * Failure leaves output/state unchanged. Invalid governor durations are ignored with OK.
 */

/* OscilluxCurveConfig.kind */
#define OSCILLUX_CURVE_KIND_CONSTANT 0u /* value */
#define OSCILLUX_CURVE_KIND_EASING 1u   /* from -> to over duration */
#define OSCILLUX_CURVE_KIND_ENVELOPE 2u /* linear attack, hold, linear release */
#define OSCILLUX_CURVE_KIND_LFO 3u      /* periodic shape between min and max */
/* OscilluxCurveConfig.unit: the clock for duration, attack, release, release_at and period. */
#define OSCILLUX_CURVE_UNIT_UNSPECIFIED 0u /* only for OSCILLUX_CURVE_KIND_CONSTANT */
#define OSCILLUX_CURVE_UNIT_SECONDS 1u     /* reads OscilluxCurveInput.elapsed_seconds */
#define OSCILLUX_CURVE_UNIT_BEATS 2u       /* reads OscilluxCurveInput.elapsed_beats */
/* OscilluxCurveConfig.easing (EASING kind) */
#define OSCILLUX_CURVE_EASING_LINEAR 0u
#define OSCILLUX_CURVE_EASING_IN 1u     /* quadratic t^2 */
#define OSCILLUX_CURVE_EASING_OUT 2u    /* quadratic 1-(1-t)^2 */
#define OSCILLUX_CURVE_EASING_IN_OUT 3u /* quadratic, symmetric about t = 0.5 */
/* OscilluxCurveConfig.shape (LFO kind) */
#define OSCILLUX_CURVE_SHAPE_SINE 0u
#define OSCILLUX_CURVE_SHAPE_TRIANGLE 1u
#define OSCILLUX_CURVE_SHAPE_SQUARE 2u /* max for the first half cycle, then min */
/* OscilluxCurveConfig.flags bitmask; other bits must be clear. */
#define OSCILLUX_CURVE_FLAG_VALUE 1u /* value is present */
#define OSCILLUX_CURVE_FLAG_FROM 2u  /* from is present */
#define OSCILLUX_CURVE_FLAG_TO 4u    /* to is present */
#define OSCILLUX_CURVE_FLAG_MIN 8u   /* min is present */
#define OSCILLUX_CURVE_FLAG_MAX 16u  /* max is present */
#define OSCILLUX_CURVE_FLAG_WRAP 32u /* with MIN and MAX: wrap into [min, max) instead of clamp */

/*
 * A typed curve. "Curve units" are seconds or beats per unit. Every double must be finite.
 * CONSTANT needs FLAG_VALUE. EASING needs FLAG_FROM, FLAG_TO, duration > 0 and a unit. ENVELOPE
 * needs attack >= 0, release > 0 and a unit. LFO needs FLAG_MIN, FLAG_MAX, period > 0 and a
 * unit. With FLAG_MIN and FLAG_MAX set, min <= max and the output is clamped (or wrapped).
 */
typedef struct OscilluxCurveConfig {
    uint32_t kind;     /* OSCILLUX_CURVE_KIND_* */
    uint32_t unit;     /* OSCILLUX_CURVE_UNIT_* */
    uint32_t easing;   /* OSCILLUX_CURVE_EASING_* */
    uint32_t shape;    /* OSCILLUX_CURVE_SHAPE_* */
    uint32_t flags;    /* OSCILLUX_CURVE_FLAG_* bitmask */
    double value;      /* CONSTANT output; ENVELOPE peak (1 without FLAG_VALUE) */
    double from;       /* EASING start value */
    double to;         /* EASING end value */
    double duration;   /* EASING length, curve units; > 0 */
    double attack;     /* ENVELOPE rise from 0 to the peak, curve units; >= 0 */
    double release;    /* ENVELOPE fall from the peak to 0, curve units; > 0 */
    double release_at; /* ENVELOPE time the release starts, curve units; 0 = never */
    double period;     /* LFO cycle length, curve units; > 0 */
    double phase;      /* LFO phase offset, cycles */
    double min;        /* lower bound (FLAG_MIN); LFO low value */
    double max;        /* upper bound (FLAG_MAX); LFO high value */
    double smoothing_seconds; /* exponential smoothing time constant toward the raw value, s;
                                 >= 0, 0 disables */
} OscilluxCurveConfig;

/* The clock and history for one evaluation; every double must be finite. */
typedef struct OscilluxCurveInput {
    double elapsed_seconds; /* time since the curve started, s */
    double elapsed_beats;   /* time since the curve started, beats */
    double beat_phase;      /* added to the LFO phase when the unit is beats, cycles */
    double delta_seconds;   /* time since the previous evaluation, s; smoothing needs > 0 */
    double previous_value;  /* the previous output, read when has_previous is 1 */
    uint32_t has_previous;  /* 0 or 1 */
} OscilluxCurveInput;

/*
 * Frame-time governor: a moving average of frame durations steps an integer quality level
 * (0 = lowest) down while frames run long and up while they run short. The host decides what
 * each level means. Create refuses (INVALID_ARGUMENT) a config without hysteresis: target_ms
 * times each promote factor must be strictly below target_ms times demote_factor.
 */
typedef struct OscilluxGovernorConfig {
    double target_ms;              /* target frame duration, ms; > 0 */
    double ema_alpha;              /* weight of each sample in the moving average; (0, 1] */
    double pause_gap_ms;           /* durations at or above this are pauses, ignored, ms; > 0 */
    double sample_cap_factor;      /* samples are capped at target_ms times this; > 0 */
    double refresh_drift;          /* per-sample growth factor of refresh_ms toward target; >= 1 */
    double refresh_floor_ms;       /* lower bound of refresh_ms, ms; (0, target_ms] */
    double demote_factor;          /* average above target_ms times this is slow; > 0 */
    double promote_refresh_factor; /* average below refresh_ms times this ... ; > 0 */
    double promote_target_factor;  /* ... or below target_ms times this is fast; > 0 */
    uint32_t demote_frames;        /* consecutive slow samples before a step down; >= 1 */
    uint32_t promote_frames;       /* consecutive fast samples before a step up; >= 1 */
    uint32_t settle_frames;        /* samples after a step that cannot cause another step */
    uint32_t max_level;            /* highest quality level */
    uint32_t initial_level;        /* level after create; <= max_level */
} OscilluxGovernorConfig;

typedef struct OscilluxGovernorStats {
    double ema_ms;     /* moving average of frame duration, ms; valid when has_ema is 1 */
    double refresh_ms; /* estimated display refresh interval (shortest recent frame), ms */
    uint32_t level;    /* current quality level, 0 ..= max_level */
    uint32_t has_ema;  /* 0 until the first accepted sample, then 1 */
} OscilluxGovernorStats;

/* Number of entries in OscilluxMapped.values, indexed by OSCILLUX_MAPPED_*. */
#define OSCILLUX_MAPPED_VALUE_COUNT 9
/* Number of entries in OscilluxMapped.spectrum. */
#define OSCILLUX_MAPPED_SPECTRUM_BINS 64
/* Number of band groups in OscilluxMappingConfig.groups: bass, mid, treble. */
#define OSCILLUX_MAPPING_GROUPS 3
/* Indices into OscilluxMapped.values; every value is normalized. */
#define OSCILLUX_MAPPED_AMPLITUDE 0   /* loudness between db_floor and 0 dB */
#define OSCILLUX_MAPPED_LEVEL 1       /* amplitude, instant attack, release_seconds decay */
#define OSCILLUX_MAPPED_BASS 2        /* mean band energy of groups[0] */
#define OSCILLUX_MAPPED_MID 3         /* mean band energy of groups[1] */
#define OSCILLUX_MAPPED_TREBLE 4      /* mean band energy of groups[2] */
#define OSCILLUX_MAPPED_ONSET 5       /* broadband onset */
#define OSCILLUX_MAPPED_BASS_IMPACT 6 /* low-band onset */
#define OSCILLUX_MAPPED_FLUX 7        /* _process_ext: descriptors.spectral_flux; _process: the
                                       * broadband onset (deprecated proxy) */
#define OSCILLUX_MAPPED_CENTROID 8    /* _process_ext: log10(centroid_hz / 20) / 3; _process: the
                                       * band-index fraction of the band range (deprecated proxy) */

/* oscillux_mapping_set_level_source: what OSCILLUX_MAPPED_BASS, _MID, _TREBLE and the spectrum
 * are computed from. */
#define OSCILLUX_MAPPING_LEVELS_ABSOLUTE 0u /* frame band energies; the default */
#define OSCILLUX_MAPPING_LEVELS_RELATIVE 1u /* OscilluxDspFrameExt.levels.relative_bands */

/* Converts DSP frames to normalized values; oscillux_mapping_config_from_dsp writes the
 * reference values matched to an analyzer, oscillux_mapping_config_default those for the
 * default DSP config. */
typedef struct OscilluxMappingConfig {
    double loudness_reference; /* added back to OscilluxDspFrame.loudness_db, dB; finite */
    double db_floor;           /* loudness mapped to amplitude 0, dB; < 0 */
    double hop_seconds;        /* analyzer hop_samples / sample_rate_hz, s; > 0 */
    double release_seconds;    /* level decay time constant, s; > 0 */
    double centroid_floor;     /* summed band energy at or below which centroid is 0; >= 0 */
    /* Inclusive band index ranges [start, end], start <= end < OSCILLUX_DSP_MAX_BANDS. */
    uint32_t groups[OSCILLUX_MAPPING_GROUPS][2];
} OscilluxMappingConfig;

typedef struct OscilluxMapped {
    double values[OSCILLUX_MAPPED_VALUE_COUNT];    /* indexed by OSCILLUX_MAPPED_* */
    float spectrum[OSCILLUX_MAPPED_SPECTRUM_BINS]; /* bands resampled linearly; normalized */
} OscilluxMapped;

/* OscilluxUniformField.source */
#define OSCILLUX_FIELD_SOURCE_MAPPED 0u   /* OscilluxMapped.values */
#define OSCILLUX_FIELD_SOURCE_SPECTRUM 1u /* OscilluxMapped.spectrum */
#define OSCILLUX_FIELD_SOURCE_HOST 2u     /* the host array passed to oscillux_packer_pack */
/* Most fields one packer accepts. */
#define OSCILLUX_PACKER_MAX_FIELDS 256
/* Most components one field accepts. */
#define OSCILLUX_PACKER_MAX_COMPONENTS 64u

/* Copies `components` consecutive source values, as float32, into the block. */
typedef struct OscilluxUniformField {
    uint32_t source;        /* OSCILLUX_FIELD_SOURCE_* */
    uint32_t source_offset; /* index of the first source element (elements, not bytes) */
    uint32_t components;    /* 1 ..= OSCILLUX_PACKER_MAX_COMPONENTS */
    uint32_t offset;        /* byte offset of the first component; multiple of 4 */
    uint32_t stride;        /* bytes between components; multiple of 4, >= 4 */
} OscilluxUniformField;

typedef struct OscilluxCurve OscilluxCurve;
typedef struct OscilluxGovernor OscilluxGovernor;
typedef struct OscilluxMapper OscilluxMapper;
typedef struct OscilluxPacker OscilluxPacker;

/* Writes a constant-0 curve (kind CONSTANT, FLAG_VALUE set) to *out. */
OSCILLUX_API int32_t oscillux_curve_config_default(OscilluxCurveConfig *out);
/* Validates *config and creates a curve; *out is null on failure. */
OSCILLUX_API int32_t oscillux_curve_create(const OscilluxCurveConfig *config, OscilluxCurve **out);
/* Frees a curve; null is ignored. */
OSCILLUX_API void oscillux_curve_destroy(OscilluxCurve *handle);
/* Writes the curve's value at *input to *out, rounded half-up to 6 decimal places. Refuses a
 * non-finite input or result and has_previous other than 0 or 1. The handle is not modified. */
OSCILLUX_API int32_t oscillux_curve_evaluate(const OscilluxCurve *handle,
                                             const OscilluxCurveInput *input,
                                             double *out);
/* Writes the reference governor (16.67 ms target, levels 0 ..= 3, starting at 3) to *out. */
OSCILLUX_API int32_t oscillux_governor_config_default(OscilluxGovernorConfig *out);
/* Validates *config and creates a governor; *out is null on failure. */
OSCILLUX_API int32_t oscillux_governor_create(const OscilluxGovernorConfig *config,
                                              OscilluxGovernor **out);
/* Frees a governor; null is ignored. */
OSCILLUX_API void oscillux_governor_destroy(OscilluxGovernor *handle);
/* Records one frame duration frame_ms (ms) and writes the resulting stats to *out. A
 * non-finite, nonpositive or pause-length (>= pause_gap_ms) duration is ignored: OK with the
 * unchanged stats. */
OSCILLUX_API int32_t oscillux_governor_record(OscilluxGovernor *handle,
                                              double frame_ms,
                                              OscilluxGovernorStats *out);
/* Clears the average and counters and sets the level, clamped to max_level. */
OSCILLUX_API int32_t oscillux_governor_reset(OscilluxGovernor *handle, uint32_t level);

/* ---- Transition scheduler -------------------------------------------------------------------
 * Decides when a visual switch starts and how far its blend has progressed, as a function of
 * presentation time. The host passes its beat state as plain numbers and owns which visuals and
 * transition are shown. A request first pre-rolls the incoming visual for pre_roll_seconds,
 * then (with a confident beat) waits for the next beat or bar boundary, then blends for
 * duration_beats beats clamped to 0.8 ..= 2.5 s. Without a confident beat the blend starts when
 * the pre-roll ends and lasts fallback_seconds. Results are exact across targets;
 * conformance/transition/README.md specifies every step and reference.json holds the vectors.
 * Every call but create is allocation-free. */

/* quantise: start when the pre-roll ends. */
#define OSCILLUX_TRANSITION_QUANTISE_NONE 0u
/* quantise: start on the first beat at or after the end of the pre-roll. */
#define OSCILLUX_TRANSITION_QUANTISE_BEAT 1u
/* quantise: start on the first bar at or after the end of the pre-roll. */
#define OSCILLUX_TRANSITION_QUANTISE_BAR 2u
/* state: no request since create or reset; progress 0. */
#define OSCILLUX_TRANSITION_STATE_IDLE 0u
/* state: the incoming visual warms up offscreen; nothing is blended; progress 0. */
#define OSCILLUX_TRANSITION_STATE_PRE_ROLL 1u
/* state: pre-roll done, waiting for the quantised start; progress 0. */
#define OSCILLUX_TRANSITION_STATE_WAITING 2u
/* state: start <= t < end; progress rises from exactly 0 toward 1. */
#define OSCILLUX_TRANSITION_STATE_BLENDING 3u
/* state: t >= end; progress is exactly 1. Hosts draw this frame blended at p = 1, then show the
 * incoming visual alone. */
#define OSCILLUX_TRANSITION_STATE_DONE 4u
/* easing: progress linear in time (the default; transition bodies ease internally). */
#define OSCILLUX_TRANSITION_EASING_LINEAR 0u
/* easing: progress u * u * (3 - 2 * u) of the linear fraction u. */
#define OSCILLUX_TRANSITION_EASING_SMOOTHSTEP 1u

/* Scheduler configuration. Set struct_size to sizeof(OscilluxTransitionConfig) first. */
typedef struct OscilluxTransitionConfig {
    uint32_t struct_size;
    /* OSCILLUX_TRANSITION_EASING_*; default LINEAR. */
    uint32_t easing;
    /* Beats in a bar, 1 ..= 16; default 4. */
    uint32_t beats_per_bar;
    /* Bars between automatic bar-quantised requests, counted from the first update or the last
     * blend end at the clock's tempo (120 BPM without a confident beat); 0 (default) is off,
     * at most 1024. */
    uint32_t auto_advance_bars;
    /* Blend length in beats with a confident beat, > 0 and <= 64; default 4. */
    double duration_beats;
    /* Blend length in seconds without a confident beat, 0.8 ..= 2.5; default 1.6. */
    double fallback_seconds;
    /* Seconds the incoming visual runs before it can be seen, 0 ..= 10; default 0.5. */
    double pre_roll_seconds;
    /* Beat confidence needed to use the beat clock, 0 ..= 1; default 0.5. */
    double min_confidence;
} OscilluxTransitionConfig;

/* The host's beat state at one presentation time. The clock is a confident beat when
 * confidence >= min_confidence, 30 <= tempo_bpm <= 300 and both phases are in 0 ..= 1; any
 * other value (NaN included) just means no beat. */
typedef struct OscilluxTransitionClock {
    /* Presentation time in seconds on the host's monotonic clock; must be finite. */
    double time_seconds;
    /* Fraction of the current beat elapsed. */
    double beat_phase;
    /* Fraction of the current bar elapsed. */
    double bar_phase;
    double tempo_bpm;
    double confidence;
} OscilluxTransitionClock;

/* The schedule evaluated at one time. Times are 0 while idle. */
typedef struct OscilluxTransitionStatus {
    /* Eased progress: exactly 0 up to start_seconds, exactly 1 from end_seconds on. */
    double progress;
    double request_seconds;
    double start_seconds;
    double end_seconds;
    /* OSCILLUX_TRANSITION_STATE_*. */
    uint32_t state;
    /* The OSCILLUX_TRANSITION_QUANTISE_* actually applied, after any fallback. */
    uint32_t quantise;
    /* 1 when this update issued an automatic request, else 0. */
    uint32_t auto_requested;
    /* Always 0. */
    uint32_t reserved;
} OscilluxTransitionStatus;

typedef struct OscilluxTransition OscilluxTransition;

/* Writes the defaults; out->struct_size must be sizeof(OscilluxTransitionConfig). */
OSCILLUX_API int32_t oscillux_transition_config_default(OscilluxTransitionConfig *out);
/* Validates *config and creates an idle scheduler; *out is null on failure. */
OSCILLUX_API int32_t oscillux_transition_create(const OscilluxTransitionConfig *config,
                                                OscilluxTransition **out);
/* Frees a scheduler; null is ignored. */
OSCILLUX_API void oscillux_transition_destroy(OscilluxTransition *handle);
/* Schedules a switch requested at clock->time_seconds, replacing any current one, and writes
 * the schedule. With a confident beat, start is the first beat (QUANTISE_BEAT) or bar
 * (QUANTISE_BAR) at or after the end of the pre-roll; when that is more than max_wait_seconds
 * (>= 0, may be +inf) after the pre-roll ends, bar falls back to beat and beat to none.
 * Refuses a non-finite time, quantise above 2 and a negative or NaN wait with
 * OSCILLUX_ERR_INVALID_ARGUMENT, changing nothing and leaving *out unwritten. */
OSCILLUX_API int32_t oscillux_transition_request(OscilluxTransition *handle,
                                                 const OscilluxTransitionClock *clock,
                                                 uint32_t quantise,
                                                 double max_wait_seconds,
                                                 OscilluxTransitionStatus *out);
/* Per frame: writes the schedule at clock->time_seconds, first issuing an automatic
 * QUANTISE_BAR request (waiting at most one bar) when auto-advance is due. Refuses a non-finite
 * time as oscillux_transition_request does. */
OSCILLUX_API int32_t oscillux_transition_update(OscilluxTransition *handle,
                                                const OscilluxTransitionClock *clock,
                                                OscilluxTransitionStatus *out);
/* Writes the schedule at time_seconds without changing anything; a NaN time reads as
 * before the start. */
OSCILLUX_API int32_t oscillux_transition_status(const OscilluxTransition *handle,
                                                double time_seconds,
                                                OscilluxTransitionStatus *out);
/* Returns to idle and restarts the auto-advance count. */
OSCILLUX_API int32_t oscillux_transition_reset(OscilluxTransition *handle);
/* ---- End of transition scheduler ---------------------------------------------------------- */
/* Writes the reference mapping, matching oscillux_dsp_config_default, to *out. */
OSCILLUX_API int32_t oscillux_mapping_config_default(OscilluxMappingConfig *out);
/* Writes the default mapping with hop_seconds and loudness_reference taken from a valid DSP
 * config (hop_samples / sample_rate_hz, loudness_reference_lufs); an invalid one returns
 * OSCILLUX_ERR_INVALID_ARGUMENT and leaves *out unchanged. */
OSCILLUX_API int32_t oscillux_mapping_config_from_dsp(const OscilluxDspConfig *dsp,
                                                      OscilluxMappingConfig *out);
/* Validates *config and creates a mapper; *out is null on failure. */
OSCILLUX_API int32_t oscillux_mapping_create(const OscilluxMappingConfig *config,
                                             OscilluxMapper **out);
/* Frees a mapper; null is ignored. */
OSCILLUX_API void oscillux_mapping_destroy(OscilluxMapper *handle);
/* Forgets the previous level so the next frame starts the level envelope afresh. */
OSCILLUX_API int32_t oscillux_mapping_reset(OscilluxMapper *handle);
/* Selects the level source, OSCILLUX_MAPPING_LEVELS_ABSOLUTE (the default) or _RELATIVE; any
 * other value returns OSCILLUX_ERR_INVALID_ARGUMENT and leaves it unchanged. Reset keeps it.
 * The relative source maps OSCILLUX_MAPPED_BASS, _MID, _TREBLE and the spectrum from
 * ext->levels.relative_bands exactly as the absolute source maps the band energies;
 * OSCILLUX_MAPPED_AMPLITUDE, _LEVEL, _ONSET, _BASS_IMPACT, _FLUX and _CENTROID still read *frame
 * and the descriptors. A host can check that ext->levels.band_count is nonzero before opting
 * in: it is 0 when the analyzer refused the AGC configuration, and every frame is refused. */
OSCILLUX_API int32_t oscillux_mapping_set_level_source(OscilluxMapper *handle, uint32_t source);
/* Maps *frame to *out. unavailable is 0 or 1; 1 writes all zeros (frame is still required).
 * Refuses band_count above OSCILLUX_DSP_MAX_BANDS and non-finite frame values.
 * OSCILLUX_MAPPED_FLUX and OSCILLUX_MAPPED_CENTROID keep their deprecated proxies, as in
 * earlier releases; _process_ext maps them from the descriptors. With the relative level
 * source it refuses available input (OSCILLUX_ERR_INVALID_ARGUMENT); use _process_ext. */
OSCILLUX_API int32_t oscillux_mapping_process(OscilluxMapper *handle,
                                              const OscilluxDspFrame *frame,
                                              uint32_t unavailable,
                                              OscilluxMapped *out);
/* As oscillux_mapping_process, with the descriptors in *ext (the element a push_ext call wrote
 * beside *frame) mapped to OSCILLUX_MAPPED_FLUX, clamped to 0 ..= 1, and
 * OSCILLUX_MAPPED_CENTROID, log10(centroid_hz / 20) / 3 clamped to 0 ..= 1 (20 Hz -> 0,
 * 20 kHz -> 1), or 0 when the summed band energy is at most centroid_floor. A null ext maps as
 * oscillux_mapping_process. An ext whose struct_size is not a section end, whose reserved is
 * not 0 or whose descriptors are not finite returns OSCILLUX_ERR_INVALID_ARGUMENT. With the
 * relative level source, available input is also refused when ext is null, struct_size ends
 * before the levels section, levels.band_count differs from frame->band_count (as for the
 * all-zero levels of an analyzer whose db_floor is too shallow for the AGC), or a valid
 * relative band is not finite. */
OSCILLUX_API int32_t oscillux_mapping_process_ext(OscilluxMapper *handle,
                                                  const OscilluxDspFrame *frame,
                                                  const OscilluxDspFrameExt *ext,
                                                  uint32_t unavailable,
                                                  OscilluxMapped *out);
/* Creates a packer for a block_size-byte block (> 0, multiple of 4) from field_count
 * (<= OSCILLUX_PACKER_MAX_FIELDS) fields. Every component must lie inside the block, no two
 * components may share an offset, and mapped/spectrum sources must stay within
 * OSCILLUX_MAPPED_VALUE_COUNT / OSCILLUX_MAPPED_SPECTRUM_BINS elements. *out is null on failure. */
OSCILLUX_API int32_t oscillux_packer_create(const OscilluxUniformField *fields,
                                            size_t field_count,
                                            size_t block_size,
                                            OscilluxPacker **out);
/* Frees a packer; null is ignored. */
OSCILLUX_API void oscillux_packer_destroy(OscilluxPacker *handle);
/* Packs little-endian float32 components; zeroes padding; leaves bytes beyond block_size alone.
 * host holds host_count values; out holds out_capacity bytes, at least block_size. Rejects
 * referenced non-finite values, f32 overflow, absent host values and short output. The handle
 * is not modified.
 */
OSCILLUX_API int32_t oscillux_packer_pack(const OscilluxPacker *handle,
                                          const OscilluxMapped *mapped,
                                          const double *host,
                                          size_t host_count,
                                          uint8_t *out,
                                          size_t out_capacity);

/* ==== Reaction sampler ===================================================================== */
/*
 * Turns mapped analysis hops into the audio values of one display frame at the moment its light
 * reaches the viewer (conformance/reaction/README.md). Push every hop with its media time and
 * tracker output (oscillux_reaction_push), and sample once per display frame
 * (oscillux_reaction_sample). Times are milliseconds on the media timeline. A sample at host
 * time t targets the media heard when the frame is seen:
 *
 *     target = t + render_ahead_ms + display_latency_ms - output_latency_ms
 *
 * A hop whose analysis window ends at media time m describes the audio at
 * m - analysis_latency_ms (continuous values and the beat clock) and m - onset_latency_ms
 * (OSCILLUX_MAPPED_ONSET, _BASS_IMPACT and _FLUX). Transients are held at full strength from
 * the first sample at or after m - onset_latency_ms through the first at or after m, then
 * released, so no hop between two samples is lost. Continuous values and the spectrum are
 * interpolated between the hops around the target. The beat phase follows the tracker output of
 * the latest hop, or a host clock (oscillux_reaction_set_beat_clock), extrapolated to the target;
 * it moves forward at 0.5 to 2 times the nominal rate and steps only at the wrap, where the beat
 * pulse peaks. It resynchronises on the first sample, after reset, and when the target moves
 * back or more than 500 ms forward. Creation allocates; no other call allocates, locks or does
 * I/O. Invalid input and a full queue return OSCILLUX_ERR_INVALID_ARGUMENT and change nothing;
 * oscillux_reaction_queued tells a full queue (capacity hops) apart.
 */

/* Most hops one sampler queues. */
#define OSCILLUX_REACTION_MAX_CAPACITY 65536u
/* Most beats per bar a host beat clock accepts. */
#define OSCILLUX_REACTION_MAX_BEATS_PER_BAR 16u

/* Sampler configuration; every time is in ms. oscillux_reaction_config_default writes it. */
typedef struct OscilluxReactionConfig {
    uint32_t struct_size;          /* sizeof(OscilluxReactionConfig), 96 */
    uint32_t capacity;             /* hops queued, 1 ..= OSCILLUX_REACTION_MAX_CAPACITY */
    double output_latency_ms;      /* audio written at the host time to audio heard; >= 0 */
    double display_latency_ms;     /* frame presented to light emitted; >= 0 */
    double render_ahead_ms;        /* sample time to frame presentation; >= 0 */
    double analysis_latency_ms;    /* hop time to the audio its continuous values describe; >= 0 */
    double onset_latency_ms;       /* hop time to the audio its transients describe; >= 0 */
    double onset_release_ms;       /* release time constant of OSCILLUX_MAPPED_ONSET; > 0 */
    double bass_impact_release_ms; /* of OSCILLUX_MAPPED_BASS_IMPACT; > 0 */
    double flux_release_ms;        /* of OSCILLUX_MAPPED_FLUX; > 0 */
    double pulse_decay_ms;         /* beat pulse decay time constant after the beat; > 0 */
    double pulse_rise_ms;          /* beat pulse Gaussian rise width before the beat; > 0 */
    double phase_lock_gain;        /* share of an onset's phase error taken, 0 ..= 1 */
} OscilluxReactionConfig;

/* A host beat clock: the beat position at one media time and the tempo it runs at. */
typedef struct OscilluxBeatClock {
    double media_time_ms;   /* media time of this position; finite */
    double tempo_bpm;       /* > 0 */
    double beat_phase;      /* [0, 1); 0 is the beat */
    double bar_phase;       /* [0, 1); 0 is the downbeat */
    int64_t beat_index;     /* index of the current beat; beat_count counts from it */
    uint32_t beats_per_bar; /* 1 ..= OSCILLUX_REACTION_MAX_BEATS_PER_BAR */
    uint32_t reserved;      /* must be 0 */
} OscilluxBeatClock;

/* Values of one display frame. */
typedef struct OscilluxReaction {
    OscilluxMapped mapped; /* mapped values and spectrum at the target */
    double beat;           /* beat pulse, [0, 1]; 1 at the beat's photon time */
    double downbeat;       /* beat pulse of downbeats only, [0, 1] */
    double beat_phase;     /* output beat phase, [0, 1); 0 at the beat */
    double bar_phase;      /* output bar phase, [0, 1); 0 at the downbeat */
    double tempo_bpm;      /* tempo of the reference clock; 0 without one */
    double confidence;     /* tracker confidence; 1 with a host clock; 0 without a reference */
    int64_t beat_count;    /* beats counted at output wraps; with a host clock, its beat index */
    uint32_t beat_in_bar;  /* index of the current beat within the bar; 0 is the downbeat */
    uint32_t locked;       /* 1 while a reference drives the beat outputs, else 0 (all zero) */
} OscilluxReaction;

typedef struct OscilluxReactionSampler OscilluxReactionSampler;

/* Writes the default configuration (capacity 256, no output, display or render-ahead latency)
 * with the measured analysis latencies of DSP behavior_version at sample_rate_hz (the 48 kHz
 * table scaled by 48000 / sample_rate_hz). out->struct_size must already be
 * sizeof(OscilluxReactionConfig); another size, a version without a measured entry or a zero
 * rate returns OSCILLUX_ERR_INVALID_ARGUMENT and leaves *out unchanged. */
OSCILLUX_API int32_t oscillux_reaction_config_default(uint32_t behavior_version,
                                                      uint32_t sample_rate_hz,
                                                      OscilluxReactionConfig *out);
/* Validates *config, whose struct_size must be sizeof(OscilluxReactionConfig), and creates a
 * sampler; *out is null on failure. */
OSCILLUX_API int32_t oscillux_reaction_create(const OscilluxReactionConfig *config,
                                              OscilluxReactionSampler **out);
/* Frees a sampler; null is ignored. */
OSCILLUX_API void oscillux_reaction_destroy(OscilluxReactionSampler *handle);
/* Forgets every hop, held peak, the beat clock and the beat state; keeps the configuration. */
OSCILLUX_API int32_t oscillux_reaction_reset(OscilluxReactionSampler *handle);
/* Queues one mapped hop whose analysis window ends at media_time_ms with the tracker output
 * beside it (the beat section of its OscilluxDspFrameExt), or null without one. Refuses a
 * non-finite time or value, a time earlier than the previous hop's, a beat with a non-finite or
 * negative value, a phase outside [0, 1) or beat_in_bar >= OSCILLUX_REACTION_MAX_BEATS_PER_BAR,
 * and a full queue. */
OSCILLUX_API int32_t oscillux_reaction_push(OscilluxReactionSampler *handle,
                                            double media_time_ms,
                                            const OscilluxMapped *mapped,
                                            const OscilluxDspBeat *beat);
/* Drives the beat outputs from *clock instead of the tracker; null returns to the tracker. A
 * clock outside the ranges above is refused. */
OSCILLUX_API int32_t oscillux_reaction_set_beat_clock(OscilluxReactionSampler *handle,
                                                      const OscilluxBeatClock *clock);
/* Writes to *out the values seen at the photon time of a frame sampled at host media time
 * time_ms (the audio output's write position), consuming every hop due by then. A non-finite
 * time is refused. */
OSCILLUX_API int32_t oscillux_reaction_sample(OscilluxReactionSampler *handle,
                                              double time_ms,
                                              OscilluxReaction *out);
/* Writes the number of hops queued and not yet consumed to *out. */
OSCILLUX_API int32_t oscillux_reaction_queued(const OscilluxReactionSampler *handle,
                                              uint32_t *out);

/* ==== Beat grid =========================================================================== */
/*
 * Offline beat and downbeat grid of a whole PCM buffer (conformance/beatgrid/README.md).
 * oscillux_beatgrid_create analyses the buffer non-causally: it allocates, runs in time roughly
 * linear in its length and belongs off the frame path. The grid is deterministic: the same
 * configuration and samples give bit-identical beats on every supported target.
 *
 * Every query takes a const handle, does not modify it, performs no allocation, locking or I/O,
 * and may run concurrently with the other queries on the same handle. oscillux_beatgrid_clock
 * is the per-frame one. The OSCILLUX_ERR_PANIC rule above applies to OscilluxBeatGrid.
 */

/* Analysis configuration. Members are append-only; struct_size selects the released layout. */
typedef struct OscilluxBeatGridConfig {
    uint32_t struct_size;    /* sizeof(OscilluxBeatGridConfig); 32 is the only released size */
    uint32_t sample_rate_hz; /* PCM sample rate, Hz; 8000 .. 384000; default 44100 */
    uint32_t channels;       /* interleaved channels, averaged to mono; >= 1; default 2 */
    uint32_t beats_per_bar;  /* meter hint: 2 .. 16, or 0 to choose 3 or 4 from the audio;
                                default 0 */
    double min_bpm;          /* slowest beat tempo considered, BPM; >= 20 and below max_bpm;
                                default 40 */
    double max_bpm;          /* fastest beat tempo considered, BPM; at most 400; default 240 */
} OscilluxBeatGridConfig;

/* Grid summary, from oscillux_beatgrid_info. */
typedef struct OscilluxBeatGridInfo {
    uint64_t beat_count;     /* beats in the grid */
    uint64_t bar_count;      /* downbeats in the grid */
    double tempo_bpm;        /* global tempo, BPM; 0 for an empty grid */
    double confidence;       /* periodicity of the onset envelope at the global beat period;
                                normalized */
    double duration_seconds; /* length of the analysed PCM, seconds */
    uint32_t beats_per_bar;  /* meter used (the hint, or 3 or 4); 0 for an empty grid */
    uint32_t reserved;       /* always 0 */
} OscilluxBeatGridInfo;

/* One beat, the element oscillux_beatgrid_copy_beats writes. */
typedef struct OscilluxBeatGridBeat {
    double time_seconds;  /* from the start of the PCM, seconds; strictly increasing */
    double tempo_bpm;     /* local tempo from the neighbouring beat intervals, BPM */
    uint32_t beat_in_bar; /* position in the bar; 0 at a downbeat */
    uint32_t bar_index;   /* 0 for the first (possibly partial) bar; +1 at each later downbeat */
} OscilluxBeatGridBeat;

/* Musical position at one media time, from oscillux_beatgrid_clock. Without a position (fewer
 * than two beats, or a non-finite time) beat_index is -1 and every other member is 0. */
typedef struct OscilluxBeatGridClock {
    double beat_phase;    /* progress from the current beat to the next; [0, 1) */
    double bar_phase;     /* progress through the current bar; [0, 1) */
    double tempo_bpm;     /* local tempo of the current beat, BPM */
    int64_t beat_index;   /* current beat; negative before the first beat and at or beyond the
                             beat count after the last (see oscillux_beatgrid_clock) */
    uint32_t beat_in_bar; /* position of the current beat in its bar; 0 at a downbeat */
    uint32_t reserved;    /* always 0 */
} OscilluxBeatGridClock;

typedef struct OscilluxBeatGrid OscilluxBeatGrid;

/* Writes the default configuration to *out; out->struct_size must already hold
 * sizeof(OscilluxBeatGridConfig). */
OSCILLUX_API int32_t oscillux_beatgrid_config_default(OscilluxBeatGridConfig *out);
/* Checks *config as oscillux_beatgrid_create does. On OSCILLUX_ERR_INVALID_ARGUMENT a non-null
 * out_field receives a static string naming the first offending member ("struct_size",
 * "sample_rate_hz", "channels", "beats_per_bar", "min_bpm" or "max_bpm"); otherwise null. */
OSCILLUX_API int32_t oscillux_beatgrid_config_validate(const OscilluxBeatGridConfig *config,
                                                       const char **out_field);
/* Analyses sample_frames frames of interleaved float PCM (sample_frames * channels values;
 * samples may be null when that is 0) and writes a new grid to *out_grid, null on failure.
 * Non-finite samples are read as silence; audio without a detectable pulse gives an empty
 * grid. Allocates. */
OSCILLUX_API int32_t oscillux_beatgrid_create(const OscilluxBeatGridConfig *config,
                                              const float *samples,
                                              size_t sample_frames,
                                              OscilluxBeatGrid **out_grid);
/* Frees a grid; null is ignored. */
OSCILLUX_API void oscillux_beatgrid_destroy(OscilluxBeatGrid *grid);
/* Writes the number of beats to *out_count. */
OSCILLUX_API int32_t oscillux_beatgrid_beat_count(const OscilluxBeatGrid *grid,
                                                  size_t *out_count);
/* Writes the grid summary to *out. */
OSCILLUX_API int32_t oscillux_beatgrid_info(const OscilluxBeatGrid *grid,
                                            OscilluxBeatGridInfo *out);
/* Copies beats first .. first + capacity - 1, clipped to the beat count, into out (which may
 * be null when capacity is 0) and writes how many to *out_written. first equal to the beat
 * count copies none; a larger first returns OSCILLUX_ERR_INVALID_ARGUMENT with 0 written. */
OSCILLUX_API int32_t oscillux_beatgrid_copy_beats(const OscilluxBeatGrid *grid,
                                                  size_t first,
                                                  OscilluxBeatGridBeat *out,
                                                  size_t capacity,
                                                  size_t *out_written);
/* Writes the position at media_time_seconds (from the start of the analysed PCM) to *out.
 * Before the first beat and after the last the grid is extrapolated at the first and last beat
 * interval, so beat_in_bar keeps cycling. A grid with fewer than two beats writes the
 * no-position clock and returns OSCILLUX_OK; a non-finite time writes it and returns
 * OSCILLUX_ERR_INVALID_ARGUMENT. */
OSCILLUX_API int32_t oscillux_beatgrid_clock(const OscilluxBeatGrid *grid,
                                             double media_time_seconds,
                                             OscilluxBeatGridClock *out);

/* ==== CD+G lyric graphics (OSC-CDG) ==== */

/* A CD+G stream is 24-byte packs at 300 per second; only the TV-graphics command (0x09) is
 * decoded and parity is never read. conformance/cdg/README.md specifies every instruction.
 * The screen is 300 x 216 colour indices (0..15) with a 16-entry 12-bit colour table; the
 * visible window is 288 x 192 at screen (6 + h_offset, 12 + v_offset). The panic rule above
 * applies to OscilluxCdg handles. Only oscillux_cdg_create allocates; advancing, seeking and
 * reading never allocate, lock or do I/O. Distinct handles are independent across threads. */
typedef struct OscilluxCdg OscilluxCdg;

#define OSCILLUX_CDG_PACK_SIZE 24u
#define OSCILLUX_CDG_PACKS_PER_SECOND 300u
#define OSCILLUX_CDG_SCREEN_WIDTH 300u
#define OSCILLUX_CDG_SCREEN_HEIGHT 216u
#define OSCILLUX_CDG_SCREEN_PIXELS 64800u
#define OSCILLUX_CDG_WINDOW_WIDTH 288u
#define OSCILLUX_CDG_WINDOW_HEIGHT 192u
#define OSCILLUX_CDG_WINDOW_RGBA_BYTES 221184u

/* Transparency keys for oscillux_cdg_window_rgba: which pixels show the visual beneath. */
#define OSCILLUX_CDG_KEY_NONE 0u              /* every pixel opaque */
#define OSCILLUX_CDG_KEY_TRANSPARENT_TABLE 1u /* alpha ((63 - t[i]) * 255 + 31) / 63 from the
                                                 stream's transparent-colour table */
#define OSCILLUX_CDG_KEY_PRESET_COLOUR 2u     /* the last Memory Preset colour is transparent */
#define OSCILLUX_CDG_KEY_INDEX 3u             /* colour index key_index is transparent */
#define OSCILLUX_CDG_KEY_MASK 4u              /* every index i with bit 1 << i of key_index set
                                                 is transparent */

/* Regions for oscillux_cdg_index_counts. */
#define OSCILLUX_CDG_REGION_WINDOW 0u /* the whole visible window */
#define OSCILLUX_CDG_REGION_EDGE 1u   /* the window's outer ring one tile deep: columns x < 6 or
                                         x >= 282 and rows y < 12 or y >= 180 */
#define OSCILLUX_CDG_EDGE_PIXELS 8928u
#define OSCILLUX_CDG_INDEX_COUNT 16u

/* OscilluxCdgState.changes bits: what the last advance changed. */
#define OSCILLUX_CDG_CHANGED_SCREEN 1u       /* indices inside the dirty rectangle */
#define OSCILLUX_CDG_CHANGED_PALETTE 2u      /* the colour table */
#define OSCILLUX_CDG_CHANGED_TRANSPARENCY 4u /* the transparent-colour table */
#define OSCILLUX_CDG_CHANGED_SCROLL 8u       /* a scroll offset, and so the window */
#define OSCILLUX_CDG_CHANGED_PRESETS 16u     /* the Memory or Border Preset colour */

/* The decoder state at the current position. Set struct_size to sizeof(OscilluxCdgState)
 * before oscillux_cdg_state; it writes every other member. The dirty rectangle
 * [dirty_x0, dirty_x1) x [dirty_y0, dirty_y1) in screen pixels covers every index the last
 * advance may have changed (the whole screen after a checkpoint restore); it is empty when
 * dirty_x0 == dirty_x1. palette entries are 0xRGB, 4 bits per channel; transparent entries
 * are the 6-bit values of Define Transparent Colour. */
typedef struct OscilluxCdgState {
    uint32_t struct_size;
    uint32_t h_offset;      /* 0..5 */
    uint32_t v_offset;      /* 0..11 */
    uint32_t window_x;      /* 6 + h_offset */
    uint32_t window_y;      /* 12 + v_offset */
    uint32_t preset_colour; /* last Memory Preset colour index */
    uint32_t border_colour; /* last Border Preset colour index */
    uint32_t changes;       /* OSCILLUX_CDG_CHANGED_* */
    uint32_t dirty_x0;
    uint32_t dirty_y0;
    uint32_t dirty_x1;
    uint32_t dirty_y1;
    uint32_t palette[16];
    uint32_t transparent[16];
    uint64_t applied;    /* packs decoded at the current position */
    uint64_t pack_count; /* whole packs in the stream */
} OscilluxCdgState;

/* Copies len bytes of packs (len may be 0; a trailing partial pack is ignored) and decodes them
 * once, keeping checkpoints so any seek restores one checkpoint and replays bounded work. The
 * decoder starts at position 0, before the first pack. *out is null on failure. */
OSCILLUX_API int32_t oscillux_cdg_create(const uint8_t *packs, size_t len, OscilluxCdg **out);
/* Frees a decoder; null is ignored. */
OSCILLUX_API void oscillux_cdg_destroy(OscilluxCdg *handle);
/* Advances or seeks to media time seconds: pack floor(seconds * 300) and every pack before it
 * are decoded (none for negative times; clamped to the stream). NaN returns
 * OSCILLUX_ERR_INVALID_ARGUMENT and changes nothing. Call once per frame. */
OSCILLUX_API int32_t oscillux_cdg_advance_to_time(OscilluxCdg *handle, double seconds);
/* Advances or seeks so the first applied packs (clamped to the stream) are decoded. */
OSCILLUX_API int32_t oscillux_cdg_advance_to_pack(OscilluxCdg *handle, uint64_t applied);
/* Writes the state at the current position; a struct_size other than sizeof(OscilluxCdgState)
 * returns OSCILLUX_ERR_INVALID_ARGUMENT and writes nothing. */
OSCILLUX_API int32_t oscillux_cdg_state(const OscilluxCdg *handle, OscilluxCdgState *out);
/* Copies the screen's colour indices, rows top to bottom; len must be
 * OSCILLUX_CDG_SCREEN_PIXELS. A refusal writes nothing. */
OSCILLUX_API int32_t oscillux_cdg_screen(const OscilluxCdg *handle, uint8_t *out, size_t len);
/* Writes the visible window as straight-alpha RGBA8, rows top to bottom, each 4-bit channel c
 * as c * 17; len must be OSCILLUX_CDG_WINDOW_RGBA_BYTES. key_mode is an OSCILLUX_CDG_KEY_*
 * value; key_index is read only for OSCILLUX_CDG_KEY_INDEX (an index, 0..15) and
 * OSCILLUX_CDG_KEY_MASK (a mask, 0..0xFFFF). An unknown key_mode or an out-of-range key_index
 * returns OSCILLUX_ERR_INVALID_ARGUMENT and writes nothing. */
OSCILLUX_API int32_t oscillux_cdg_window_rgba(const OscilluxCdg *handle,
                                              uint32_t key_mode,
                                              uint32_t key_index,
                                              uint8_t *out,
                                              size_t len);
/* Writes into out[i] how many pixels of the visible window's region (an OSCILLUX_CDG_REGION_*
 * value) hold colour index i; len must be OSCILLUX_CDG_INDEX_COUNT. A host chooses an automatic
 * key from it: the dominant edge index is usually the disc's background. An unknown region or
 * another len returns OSCILLUX_ERR_INVALID_ARGUMENT and writes nothing. */
OSCILLUX_API int32_t oscillux_cdg_index_counts(const OscilluxCdg *handle,
                                               uint32_t region,
                                               uint32_t *out,
                                               size_t len);

/*
 * Palette tour (vivid colour mode). Hosts that tour the palette set the visual's palette
 * uniform from oscillux_palette_tour each frame and the present pass's saturation and vibrance
 * from oscillux_palette_vivid_present. Neither call allocates.
 */
#define OSCILLUX_PALETTE_TOUR_COUNT 16u  /* palettes the tour visits */
#define OSCILLUX_PALETTE_TOUR_SECONDS 6u /* seconds the tour spends on each palette */
/*
 * Writes the palette uniform at time_seconds to out[0..4]: {-1, 0, z, 0}, where
 * z = (time_seconds / OSCILLUX_PALETTE_TOUR_SECONDS + offset) wrapped into
 * [0, OSCILLUX_PALETTE_TOUR_COUNT). offset is the visual's own palette start. A non-finite
 * argument returns OSCILLUX_ERR_INVALID_ARGUMENT with out unchanged.
 */
OSCILLUX_API int32_t oscillux_palette_tour(double time_seconds, double offset, double *out);
/* Writes the vivid present pass's saturation to out[0] and vibrance to out[1]. */
OSCILLUX_API int32_t oscillux_palette_vivid_present(double *out);

#ifdef __cplusplus
}
#endif

#endif /* OSCILLUX_H */
