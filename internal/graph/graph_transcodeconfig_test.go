package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// recordingCapSetter records the caps the resolver applies live.
type recordingCapSetter struct{ caps []int }

func (r *recordingCapSetter) SetTranscodeCap(limit int) { r.caps = append(r.caps, limit) }

const transcodeResourceSelection = `maxConcurrentTranscodes effectiveMaxConcurrentTranscodes maxConcurrentTranscodesEnvOverride maxConcurrentTranscodesRestartRequired tempDir effectiveTempDir tempDirEnvOverride tempDirRestartRequired toneMapEnabled`

func newTranscodeRuntimeHandler(t *testing.T, st *store.Store, runtime TranscodeRuntime) *Handler {
	t.Helper()
	handler := newConsoleHandler(t, st)
	handler.transcodeRuntime = runtime
	handler.liveTranscodeCap.Store(int64(runtime.StartupCap))
	t.Cleanup(func() { ffmpeg.SetToneMapEnabled(true) })
	openManagerAdminSession(t, st)
	return handler
}

func setTranscodeResources(t *testing.T, handler *Handler, variables string) (TranscodeConfigInfo, []graphError) {
	t.Helper()
	data, errs := post(t, handler,
		`{"query":"mutation T($maxConcurrentTranscodes: Int, $clearMaxConcurrentTranscodes: Boolean, $tempDir: String, $toneMapEnabled: Boolean){ setTranscodeConfig(maxConcurrentTranscodes:$maxConcurrentTranscodes, clearMaxConcurrentTranscodes:$clearMaxConcurrentTranscodes, tempDir:$tempDir, toneMapEnabled:$toneMapEnabled){ `+transcodeResourceSelection+` } }","variables":`+variables+`}`,
		authorizeManager)
	var info TranscodeConfigInfo
	if len(errs) == 0 {
		mustUnmarshal(t, data["setTranscodeConfig"], &info)
	}
	return info, errs
}

// TestTranscodeResourceSettingsApplyLiveAndAudit proves the CORE-1970 settings
// round-trip through the wire: the cap and tone-map switch apply live, the temp
// directory reports restart-required, effective values sit beside the stored
// ones, and the mutation is audited like every transcode-config write.
func TestTranscodeResourceSettingsApplyLiveAndAudit(t *testing.T) {
	st := storetest.Open(t)
	setter := &recordingCapSetter{}
	active := t.TempDir()
	handler := newTranscodeRuntimeHandler(t, st, TranscodeRuntime{
		CapSetter: setter, StartupCap: 1, AutoCap: 1,
		ActiveTempDir: active, DefaultTempDir: active,
	})

	data, errs := post(t, handler, `{"query":"query { transcodeConfig { `+transcodeResourceSelection+` } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("transcodeConfig errors = %#v", errs)
	}
	var before TranscodeConfigInfo
	mustUnmarshal(t, data["transcodeConfig"], &before)
	if before.MaxConcurrentTranscodes != 0 || before.EffectiveMaxConcurrentTranscodes != 1 || before.EffectiveTempDir != active ||
		!before.ToneMapEnabled || before.TempDirRestartRequired || before.MaxConcurrentTranscodesRestartRequired {
		t.Fatalf("default transcodeConfig = %#v, want automatic cap 1, active temp dir, tone-map on", before)
	}

	newDir := t.TempDir()
	after, errs := setTranscodeResources(t, handler, `{"maxConcurrentTranscodes":3,"tempDir":"`+newDir+`","toneMapEnabled":false}`)
	if len(errs) != 0 {
		t.Fatalf("setTranscodeConfig errors = %#v", errs)
	}
	if after.MaxConcurrentTranscodes != 3 || after.EffectiveMaxConcurrentTranscodes != 3 || after.MaxConcurrentTranscodesRestartRequired {
		t.Fatalf("cap after write = %#v, want 3 applied live", after)
	}
	if len(setter.caps) != 1 || setter.caps[0] != 3 {
		t.Fatalf("live caps applied = %v, want [3]", setter.caps)
	}
	if after.TempDir != newDir || after.EffectiveTempDir != active || !after.TempDirRestartRequired {
		t.Fatalf("temp dir after write = %#v, want stored %q, active %q, restart required", after, newDir, active)
	}
	if after.ToneMapEnabled || ffmpeg.ToneMapEnabled() {
		t.Fatalf("tone-map after write = %v (live %v), want off live", after.ToneMapEnabled, ffmpeg.ToneMapEnabled())
	}

	cleared, errs := setTranscodeResources(t, handler, `{"clearMaxConcurrentTranscodes":true,"tempDir":"","toneMapEnabled":true}`)
	if len(errs) != 0 {
		t.Fatalf("clear setTranscodeConfig errors = %#v", errs)
	}
	if cleared.MaxConcurrentTranscodes != 0 || cleared.EffectiveMaxConcurrentTranscodes != 1 || cleared.TempDir != "" ||
		cleared.TempDirRestartRequired || !cleared.ToneMapEnabled || !ffmpeg.ToneMapEnabled() {
		t.Fatalf("after clear = %#v, want automatic cap 1, default temp dir, tone-map on", cleared)
	}

	entries, err := st.ActivityEntriesAfter(context.Background(), 0, []string{store.ActivityConfigUpdate}, 0)
	if err != nil {
		t.Fatal(err)
	}
	audited := 0
	for _, entry := range entries {
		if entry.TargetType == "transcode" {
			audited++
		}
	}
	if audited != 2 {
		t.Fatalf("audited transcode writes = %d, want 2", audited)
	}
}

// TestTranscodeResourceSettingsRejectInvalidValues proves a zero, negative or
// fractional cap and a nonexistent, relative or unwritable temp directory are
// client errors that leave the stored settings and the live cap unchanged.
func TestTranscodeResourceSettingsRejectInvalidValues(t *testing.T) {
	st := storetest.Open(t)
	setter := &recordingCapSetter{}
	handler := newTranscodeRuntimeHandler(t, st, TranscodeRuntime{CapSetter: setter, StartupCap: 2, AutoCap: 2})

	for _, tc := range []struct{ name, variables, want string }{
		{"zero cap", `{"maxConcurrentTranscodes":0}`, "maxConcurrentTranscodes must be at least 1"},
		{"negative cap", `{"maxConcurrentTranscodes":-1}`, "maxConcurrentTranscodes must be at least 1"},
		{"fractional cap", `{"maxConcurrentTranscodes":1.5}`, "maxConcurrentTranscodes must be a whole number"},
		{"nonexistent temp dir", `{"tempDir":"` + filepath.Join(t.TempDir(), "missing") + `"}`, "tempDir must be an existing, writable directory"},
		{"relative temp dir", `{"tempDir":"scratch"}`, "tempDir must be an absolute path"},
	} {
		if _, errs := setTranscodeResources(t, handler, tc.variables); len(errs) == 0 || !strings.Contains(errs[0].Message, tc.want) {
			t.Fatalf("%s errors = %#v, want %q", tc.name, errs, tc.want)
		}
	}

	if os.Geteuid() != 0 {
		readOnly := t.TempDir()
		if err := os.Chmod(readOnly, 0o500); err != nil { // #nosec G302 -- a read-only test directory
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) }) // #nosec G302 -- restores the test directory for removal
		if _, errs := setTranscodeResources(t, handler, `{"tempDir":"`+readOnly+`"}`); len(errs) == 0 || !strings.Contains(errs[0].Message, "tempDir must be an existing, writable directory") {
			t.Fatalf("unwritable temp dir errors = %#v, want writability client error", errs)
		}
	}

	cfg, err := st.TranscodeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrent != 0 || cfg.TempDir != "" {
		t.Fatalf("stored config after rejected writes = %#v, want untouched", cfg)
	}
	if len(setter.caps) != 0 {
		t.Fatalf("live caps applied after rejected writes = %v, want none", setter.caps)
	}
}

// TestTranscodeResourceSettingsEnvOverride proves an env/flag cap or temp dir
// wins over the stored value: the response flags the override, the live cap is
// not touched, and storing a value is still allowed for when the override goes.
func TestTranscodeResourceSettingsEnvOverride(t *testing.T) {
	st := storetest.Open(t)
	setter := &recordingCapSetter{}
	envDir := t.TempDir()
	handler := newTranscodeRuntimeHandler(t, st, TranscodeRuntime{
		CapSetter: setter, StartupCap: 6, AutoCap: 1, EnvCap: 6,
		ActiveTempDir: envDir, DefaultTempDir: t.TempDir(), EnvTempDir: true,
	})

	stored := t.TempDir()
	info, errs := setTranscodeResources(t, handler, `{"maxConcurrentTranscodes":2,"tempDir":"`+stored+`"}`)
	if len(errs) != 0 {
		t.Fatalf("setTranscodeConfig errors = %#v", errs)
	}
	if !info.MaxConcurrentTranscodesEnvOverride || info.MaxConcurrentTranscodes != 2 || info.EffectiveMaxConcurrentTranscodes != 6 || info.MaxConcurrentTranscodesRestartRequired {
		t.Fatalf("cap under env override = %#v, want stored 2, effective 6, override flagged", info)
	}
	if len(setter.caps) != 0 {
		t.Fatalf("live caps applied under env override = %v, want none", setter.caps)
	}
	if !info.TempDirEnvOverride || info.TempDir != stored || info.EffectiveTempDir != envDir || info.TempDirRestartRequired {
		t.Fatalf("temp dir under env override = %#v, want stored %q, effective %q, override flagged", info, stored, envDir)
	}
}

// TestResolveTranscodeCapPrecedence pins the startup cap precedence the app
// shares with the resolver: env/flag, then stored, then probe-derived.
func TestResolveTranscodeCapPrecedence(t *testing.T) {
	if got := ResolveTranscodeCap(store.TranscodeConfig{MaxConcurrent: 3}, 5, 1); got != 5 {
		t.Fatalf("env cap = %d, want 5", got)
	}
	if got := ResolveTranscodeCap(store.TranscodeConfig{MaxConcurrent: 3}, 0, 1); got != 3 {
		t.Fatalf("stored cap = %d, want 3", got)
	}
	if got := ResolveTranscodeCap(store.TranscodeConfig{}, 0, 4); got != 4 {
		t.Fatalf("automatic cap = %d, want 4", got)
	}
}

// TestTranscodeConfigOverlappingSavesApplyInStoredOrder proves overlapping
// saves leave the running transcoder on the stored value: each write and its
// live apply happen under one lock, so an older save cannot apply last.
func TestTranscodeConfigOverlappingSavesApplyInStoredOrder(t *testing.T) {
	st := storetest.Open(t)
	setter := &recordingCapSetter{}
	active := t.TempDir()
	handler := newTranscodeRuntimeHandler(t, st, TranscodeRuntime{
		CapSetter: setter, StartupCap: 1, AutoCap: 1,
		ActiveTempDir: active, DefaultTempDir: active,
	})

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 1; i <= 16; i++ {
		limit, toneMap := i, i%2 == 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := handler.saveAndApplyTranscodeConfig(context.Background(),
				store.TranscodeConfigPatch{MaxConcurrent: &limit, ToneMapEnabled: &toneMap})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	stored, err := st.TranscodeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(setter.caps) != 16 || setter.caps[len(setter.caps)-1] != stored.MaxConcurrent ||
		int(handler.liveTranscodeCap.Load()) != stored.MaxConcurrent {
		t.Fatalf("live caps %v (live %d), stored %d: the last applied cap must be the stored one",
			setter.caps, handler.liveTranscodeCap.Load(), stored.MaxConcurrent)
	}
	if ffmpeg.ToneMapEnabled() != stored.ToneMapEnabled {
		t.Fatalf("live tone-map %v, stored %v", ffmpeg.ToneMapEnabled(), stored.ToneMapEnabled)
	}
}
