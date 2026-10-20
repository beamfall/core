package graph

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
)

// Hardware transcoding config (ADR-0007 §§4/5, ADR-0013 §4). The graph half of
// the transcode-config feature: a manager-gated resolver that reads and updates
// the hardware-transcode policy — enable HW acceleration, codec/quality policy,
// and acceleration-device selection. Manager-gated in execute() (a Console
// operation). The vocabulary is domain-agnostic; codec and quality are closed
// value sets validated in the store.

// TranscodeConfigInfo is the wire shape for the hardware-transcode policy.
type TranscodeConfigInfo struct {
	// HardwareEnabled gates hardware-accelerated transcode sessions.
	HardwareEnabled bool `json:"hardwareEnabled"`
	// Codec is the output codec policy ("h264", "hevc"); empty = server default.
	Codec string `json:"codec"`
	// Quality is the quality policy ("low", "balanced", "high"); empty =
	// server default.
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
}

type HardwareAccelerationInfo struct {
	Accelerator string `json:"accelerator"`
	Status      string `json:"status"`
}

func transcodeConfigInfo(cfg store.TranscodeConfig, report config.HardwareAccelReport) TranscodeConfigInfo {
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
	}
}

// transcodeConfig reads the current hardware-transcode policy.
func (h *Handler) transcodeConfig(ctx context.Context, _ auth.Scope, _ request) (TranscodeConfigInfo, error) {
	cfg, err := h.store.TranscodeConfig(ctx)
	if err != nil {
		return TranscodeConfigInfo{}, err
	}
	return transcodeConfigInfo(cfg, h.hardwareAccel), nil
}

// setTranscodeConfig applies a partial transcode-config write and returns the
// resolved policy (manager-gated in execute). Absent variables leave the stored
// value unchanged; an explicit empty string for codec/quality/device clears the
// override back to the server default. An out-of-vocabulary codec or quality is
// a client error.
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
	if err := v.Err(); err != nil {
		return TranscodeConfigInfo{}, err
	}
	if !has {
		return TranscodeConfigInfo{}, clientError("no transcode config to update")
	}
	cfg, err := h.store.SetTranscodeConfig(ctx, patch, time.Now().UTC())
	if err != nil {
		return TranscodeConfigInfo{}, err
	}
	if err := h.logActivity(ctx, scope, store.ActivityConfigUpdate, "transcode", "", ""); err != nil {
		return TranscodeConfigInfo{}, err
	}
	return transcodeConfigInfo(cfg, h.hardwareAccel), nil
}
