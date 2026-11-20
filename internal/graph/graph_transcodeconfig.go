package graph

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// Hardware transcoding config (ADR-0007 §§4/5, ADR-0013 §4). The graph half of
// the transcode-config feature: a manager-gated resolver that reads and updates
// the hardware-transcode policy — enable HW acceleration, codec/quality policy,
// and acceleration-device selection. Manager-gated in execute() (a Console
// operation). The vocabulary is domain-agnostic; codec and quality are closed
// value sets validated in the store.
//
// CORE-1970 adds the transcoder resource settings: the concurrent-transcode
// cap (applied live), the scratch temp directory (applied on restart, because
// the running sessions own their directory) and the HDR tone-map switch
// (applied live). An env var or flag (BEAMFALL_TRANSCODE_CAP / --transcode-cap,
// BEAMFALL_TRANSCODE_TMP / --transcode-tmp) overrides the stored value; the
// response says so, and reports the value in force next to the stored one.

// TranscodeCapSetter applies a concurrent-transcode cap to the running
// transcoder (the playback Sessions manager satisfies it).
type TranscodeCapSetter interface {
	SetTranscodeCap(limit int)
}

// EncodePolicySetter applies the Console encode policy (hardware switch, codec
// and quality) to new transcode sessions (the ffmpeg Runner satisfies it).
type EncodePolicySetter interface {
	SetEncodePolicy(policy ffmpeg.EncodePolicy)
}

// TranscodeEncodePolicy maps the stored transcode policy onto the encoder
// policy new sessions use (CORE-2452): the hardware switch off forces
// software encoding, codec "h264" withholds HEVC and AV1 output, and quality
// tunes the encoder.
func TranscodeEncodePolicy(cfg store.TranscodeConfig) ffmpeg.EncodePolicy {
	return ffmpeg.EncodePolicy{
		SoftwareOnly: !cfg.HardwareEnabled,
		H264Only:     cfg.Codec == "h264",
		Quality:      cfg.Quality,
	}
}

// TranscodeRuntime is the transcoder's startup state handed to the graph so the
// transcode-config resolver can report effective values and env overrides and
// apply live changes. The zero value reports no runtime: nothing is applied
// live and effective values are empty.
type TranscodeRuntime struct {
	// CapSetter applies a new cap live; nil means a cap change needs a restart.
	CapSetter TranscodeCapSetter
	// EncodePolicy applies the hardware, codec and quality settings to new
	// sessions live; nil means they are applied only at startup.
	EncodePolicy EncodePolicySetter
	// StartupCap is the cap the transcoder started with.
	StartupCap int
	// AutoCap is the probe-derived cap used when no cap is stored.
	AutoCap int
	// SoftwareAutoCap is the automatic cap for software encoding, used when
	// no cap is stored and the hardware switch is off; 0 falls back to AutoCap.
	SoftwareAutoCap int
	// EnvCap is the env/flag cap override in force; 0 means none.
	EnvCap int
	// ActiveTempDir is the scratch directory the running transcoder uses.
	ActiveTempDir string
	// DefaultTempDir is the resolved default scratch directory at startup, if present.
	DefaultTempDir string
	// StartupTempDir is the configured spelling selected at startup. An empty
	// value means the default under the data directory was selected.
	StartupTempDir string
	// EnvTempDir reports that an env var or flag set ActiveTempDir.
	EnvTempDir bool
}

// TranscodeConfigInfo is the wire shape for the hardware-transcode policy.
type TranscodeConfigInfo struct {
	// HardwareEnabled gates hardware-accelerated transcode sessions.
	HardwareEnabled bool `json:"hardwareEnabled"`
	// Codec is the output codec policy: empty = Automatic, "h264" = H.264
	// only. A written "hevc" is stored as Automatic.
	Codec string `json:"codec"`
	// Quality is the encoder quality policy ("low", "balanced", "high");
	// empty = server default (balanced).
	Quality string `json:"quality"`
	// Device is the operator-selected acceleration device hint; empty =
	// automatic selection.
	Device string `json:"device"`
	// ConfiguredPreference echoes Device only when it exactly names a known
	// accelerator. Raw device paths are intentionally not repeated here.
	ConfiguredPreference string `json:"configuredPreference,omitempty"`
	// HardwareAcceleration is the sanitized startup probe report. It names only
	// accelerator mechanisms and statuses, never media paths, device paths, or
	// FFmpeg stderr.
	HardwareAcceleration []HardwareAccelerationInfo `json:"hardwareAcceleration"`
	// MaxConcurrentTranscodes is the stored cap; 0 = automatic (probe-derived).
	MaxConcurrentTranscodes int `json:"maxConcurrentTranscodes"`
	// EffectiveMaxConcurrentTranscodes is the cap the transcoder enforces now.
	EffectiveMaxConcurrentTranscodes int `json:"effectiveMaxConcurrentTranscodes"`
	// MaxConcurrentTranscodesEnvOverride reports that BEAMFALL_TRANSCODE_CAP or
	// --transcode-cap overrides the stored cap.
	MaxConcurrentTranscodesEnvOverride bool `json:"maxConcurrentTranscodesEnvOverride"`
	// MaxConcurrentTranscodesRestartRequired reports that the cap in force
	// differs from the configured one until the server restarts.
	MaxConcurrentTranscodesRestartRequired bool `json:"maxConcurrentTranscodesRestartRequired"`
	// TempDir is the stored transcode temp directory; empty = server default.
	TempDir string `json:"tempDir"`
	// EffectiveTempDir is the directory the running transcoder uses.
	EffectiveTempDir string `json:"effectiveTempDir"`
	// TempDirEnvOverride reports that BEAMFALL_TRANSCODE_TMP or --transcode-tmp
	// overrides the stored temp directory.
	TempDirEnvOverride bool `json:"tempDirEnvOverride"`
	// TempDirRestartRequired reports that the configured temp directory takes
	// effect only after a restart.
	TempDirRestartRequired bool `json:"tempDirRestartRequired"`
	// ToneMapEnabled is the HDR-to-SDR tone-map switch (applied live).
	ToneMapEnabled bool `json:"toneMapEnabled"`
}

type HardwareAccelerationInfo struct {
	Accelerator string `json:"accelerator"`
	Status      string `json:"status"`
}

func transcodeConfigInfo(cfg store.TranscodeConfig, report config.HardwareAccelReport, runtime TranscodeRuntime, liveCap int) TranscodeConfigInfo {
	hw := make([]HardwareAccelerationInfo, len(report.Results))
	for i, result := range report.Results {
		hw[i] = HardwareAccelerationInfo{
			Accelerator: result.Accelerator,
			Status:      result.Status,
		}
	}
	preference := ""
	for _, result := range report.Results {
		if result.Accelerator == cfg.Device {
			preference = cfg.Device
			break
		}
	}
	return TranscodeConfigInfo{
		HardwareEnabled:      cfg.HardwareEnabled,
		Codec:                cfg.Codec,
		Quality:              cfg.Quality,
		Device:               cfg.Device,
		ConfiguredPreference: preference,
		HardwareAcceleration: hw,

		MaxConcurrentTranscodes:                cfg.MaxConcurrent,
		EffectiveMaxConcurrentTranscodes:       liveCap,
		MaxConcurrentTranscodesEnvOverride:     runtime.EnvCap > 0,
		MaxConcurrentTranscodesRestartRequired: desiredTranscodeCap(cfg, runtime) != liveCap,
		TempDir:                                cfg.TempDir,
		EffectiveTempDir:                       runtime.ActiveTempDir,
		TempDirEnvOverride:                     runtime.EnvTempDir,
		TempDirRestartRequired:                 tempDirRestartRequired(cfg.TempDir, runtime),
		ToneMapEnabled:                         cfg.ToneMapEnabled,
	}
}

// desiredTranscodeCap resolves the cap the configuration asks for: the env/flag
// override, else the stored cap, else the automatic default for the encoder in
// use — the software cap when the hardware switch is off.
func desiredTranscodeCap(cfg store.TranscodeConfig, runtime TranscodeRuntime) int {
	switch {
	case runtime.EnvCap > 0:
		return runtime.EnvCap
	case cfg.MaxConcurrent > 0:
		return cfg.MaxConcurrent
	case !cfg.HardwareEnabled && runtime.SoftwareAutoCap > 0:
		return runtime.SoftwareAutoCap
	default:
		return runtime.AutoCap
	}
}

func tempDirRestartRequired(stored string, runtime TranscodeRuntime) bool {
	if runtime.EnvTempDir || stored == runtime.StartupTempDir {
		return false
	}
	if stored == "" {
		return runtime.DefaultTempDir != runtime.ActiveTempDir
	}
	if stored == runtime.ActiveTempDir {
		return false
	}
	resolved, err := filepath.EvalSymlinks(stored)
	return err != nil || resolved != runtime.ActiveTempDir
}

// ResolveTranscodeCap is the startup counterpart of desiredTranscodeCap, so the
// server boots with the same cap the Console reports as configured.
func ResolveTranscodeCap(cfg store.TranscodeConfig, envCap, autoCap, softwareAutoCap int) int {
	return desiredTranscodeCap(cfg, TranscodeRuntime{EnvCap: envCap, AutoCap: autoCap, SoftwareAutoCap: softwareAutoCap})
}

func (h *Handler) transcodeConfigInfo(cfg store.TranscodeConfig) TranscodeConfigInfo {
	return transcodeConfigInfo(cfg, h.hardwareAccel, h.transcodeRuntime, int(h.liveTranscodeCap.Load()))
}

// transcodeTempDirVariable validates an operator-entered temp directory: empty
// clears back to the default; anything else must be an absolute path to an
// existing, writable directory (the same check the env/flag override gets).
func transcodeTempDirVariable(value string) (string, error) {
	dir := strings.TrimSpace(value)
	if dir == "" {
		return "", nil
	}
	if !filepath.IsAbs(dir) {
		return "", clientError("tempDir must be an absolute path")
	}
	if err := config.ValidateTranscodeScratchDir(dir); err != nil {
		return "", clientError("tempDir must be an existing, writable directory")
	}
	return dir, nil
}

// transcodeConfig reads the current hardware-transcode policy.
func (h *Handler) transcodeConfig(ctx context.Context, _ auth.Scope, _ request) (TranscodeConfigInfo, error) {
	cfg, err := h.store.TranscodeConfig(ctx)
	if err != nil {
		return TranscodeConfigInfo{}, err
	}
	return h.transcodeConfigInfo(cfg), nil
}

// setTranscodeConfig applies a partial transcode-config write and returns the
// resolved policy (manager-gated in execute). Absent variables leave the stored
// value unchanged; an explicit empty string for codec/quality/device clears the
// override back to the server default. An out-of-vocabulary codec or quality is
// a client error. maxConcurrentTranscodes must be a whole number of at least 1
// (clearMaxConcurrentTranscodes returns it to automatic); tempDir must name an
// existing, writable directory or be empty for the default; toneMapEnabled
// flips the HDR tone-map switch. The cap and tone-map switch apply live.
func (h *Handler) setTranscodeConfig(ctx context.Context, scope auth.Scope, req request) (TranscodeConfigInfo, error) {
	v := newVars(req)
	var patch store.TranscodeConfigPatch
	has := false
	if enabled, ok := v.Bool("hardwareEnabled"); ok {
		patch.HardwareEnabled = &enabled
		has = true
	}
	for name, target := range map[string]**string{
		"codec":   &patch.Codec,
		"quality": &patch.Quality,
		"device":  &patch.Device,
	} {
		if value, ok := v.String(name); ok {
			value := value
			*target = &value
			has = true
		}
	}
	if number, ok := v.Number("maxConcurrentTranscodes"); ok {
		if number != math.Trunc(number) || number < math.MinInt32 || number > math.MaxInt32 {
			return TranscodeConfigInfo{}, clientError("maxConcurrentTranscodes must be a whole number")
		}
		limit := int(number)
		patch.MaxConcurrent = &limit
		has = true
	}
	if clear, ok := v.Bool("clearMaxConcurrentTranscodes"); ok && clear {
		patch.ClearMaxConcurrent = true
		has = true
	}
	if enabled, ok := v.Bool("toneMapEnabled"); ok {
		patch.ToneMapEnabled = &enabled
		has = true
	}
	tempDir, setTempDir := v.String("tempDir")
	if err := v.Err(); err != nil {
		return TranscodeConfigInfo{}, err
	}
	if setTempDir {
		dir, err := transcodeTempDirVariable(tempDir)
		if err != nil {
			return TranscodeConfigInfo{}, err
		}
		patch.TempDir = &dir
		has = true
	}
	if !has {
		return TranscodeConfigInfo{}, clientError("no transcode config to update")
	}
	cfg, err := h.saveAndApplyTranscodeConfig(ctx, patch)
	if err != nil {
		return TranscodeConfigInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "transcode", "", ""); err != nil {
		return TranscodeConfigInfo{}, err
	}
	return h.transcodeConfigInfo(cfg), nil
}

// saveAndApplyTranscodeConfig stores the patch and applies the result to the
// running transcoder under one lock, so overlapping saves apply in the order
// they were stored.
func (h *Handler) saveAndApplyTranscodeConfig(ctx context.Context, patch store.TranscodeConfigPatch) (store.TranscodeConfig, error) {
	h.transcodeConfigMu.Lock()
	defer h.transcodeConfigMu.Unlock()
	cfg, err := h.store.SetTranscodeConfig(ctx, patch, time.Now().UTC())
	if err != nil {
		return store.TranscodeConfig{}, err
	}
	h.applyTranscodeRuntime(cfg)
	return cfg, nil
}

// applyTranscodeRuntime pushes the live-applicable settings into the running
// transcoder: the tone-map switch always, the encode policy when a setter is
// wired (new sessions only; running sessions keep their encoder), and the cap
// when a setter is wired and no env/flag override pins it. The temp directory
// is never applied live.
func (h *Handler) applyTranscodeRuntime(cfg store.TranscodeConfig) {
	ffmpeg.SetToneMapEnabled(cfg.ToneMapEnabled)
	runtime := h.transcodeRuntime
	if runtime.EncodePolicy != nil {
		runtime.EncodePolicy.SetEncodePolicy(TranscodeEncodePolicy(cfg))
	}
	if runtime.CapSetter == nil || runtime.EnvCap > 0 {
		return
	}
	limit := desiredTranscodeCap(cfg, runtime)
	if limit < 1 {
		return
	}
	runtime.CapSetter.SetTranscodeCap(limit)
	h.liveTranscodeCap.Store(int64(limit))
}
