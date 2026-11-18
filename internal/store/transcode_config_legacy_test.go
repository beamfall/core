package store

import (
	"context"
	"testing"
	"time"
)

// TestTranscodeConfigIgnoresRetiredHardwareKey pins CORE-2452's migration
// safety: the retired "transcode.hardware_enabled" row, which every Console
// save wrote false while nothing read it, no longer turns hardware off, and a
// stored "hevc" codec reads back as Automatic.
func TestTranscodeConfigIgnoresRetiredHardwareKey(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1700, 0).UTC()
	if err := st.setServerConfigValues(ctx, map[string]string{
		"transcode.hardware_enabled": "0",
		configTranscodeCodec:         "hevc",
	}, now); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.TranscodeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HardwareEnabled || cfg.Codec != "" {
		t.Fatalf("TranscodeConfig with retired rows = %#v, want hardware on and Automatic codec", cfg)
	}

	off := false
	if _, err := st.SetTranscodeConfig(ctx, TranscodeConfigPatch{HardwareEnabled: &off}, now); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := st.TranscodeConfig(ctx); cfg.HardwareEnabled {
		t.Fatal("after an explicit hardware-off write HardwareEnabled = true")
	}
}
