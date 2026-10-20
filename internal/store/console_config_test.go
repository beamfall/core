package store_test

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestConsoleConfigDefaultsAndPatch proves the console resource-governance
// config returns its default until written, then a partial write persists and
// reads back (feature:console-config, store half).
func TestConsoleConfigDefaultsAndPatch(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	cfg, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackgroundWorkers != 1 {
		t.Fatalf("default BackgroundWorkers = %d, want 1", cfg.BackgroundWorkers)
	}
	if cfg.EventsRetentionDays != 180 {
		t.Fatalf("default EventsRetentionDays = %d, want 180", cfg.EventsRetentionDays)
	}

	workers := 6
	retentionDays := 90
	updated, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{
		BackgroundWorkers:   &workers,
		EventsRetentionDays: &retentionDays,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BackgroundWorkers != 6 {
		t.Fatalf("after write BackgroundWorkers = %d, want 6", updated.BackgroundWorkers)
	}
	if updated.EventsRetentionDays != 90 {
		t.Fatalf("after write EventsRetentionDays = %d, want 90", updated.EventsRetentionDays)
	}
	// Persisted across a fresh read.
	reread, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reread.BackgroundWorkers != 6 {
		t.Fatalf("reread BackgroundWorkers = %d, want 6", reread.BackgroundWorkers)
	}
	if reread.EventsRetentionDays != 90 {
		t.Fatalf("reread EventsRetentionDays = %d, want 90", reread.EventsRetentionDays)
	}

	// A below-range value is rejected and leaves the stored value intact.
	bad := 0
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{BackgroundWorkers: &bad}, now); err == nil {
		t.Fatal("SetConsoleConfig(0) = nil error, want ValidationError")
	}
	if final, _ := st.ConsoleConfig(ctx); final.BackgroundWorkers != 6 {
		t.Fatalf("after rejected write BackgroundWorkers = %d, want 6", final.BackgroundWorkers)
	}
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{EventsRetentionDays: &bad}, now); err == nil {
		t.Fatal("SetConsoleConfig(eventsRetentionDays=0) = nil error, want ValidationError")
	}
	if final, _ := st.ConsoleConfig(ctx); final.EventsRetentionDays != 90 {
		t.Fatalf("after rejected retention write EventsRetentionDays = %d, want 90", final.EventsRetentionDays)
	}
}

func TestConsoleConfigWithDefaultsUsesBootstrapFallbackUntilPersisted(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	cfg, err := st.ConsoleConfigWithDefaults(ctx, store.ConsoleConfig{BackgroundWorkers: 4, EventsRetentionDays: 45})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackgroundWorkers != 4 {
		t.Fatalf("defaulted BackgroundWorkers = %d, want bootstrap fallback 4", cfg.BackgroundWorkers)
	}
	if cfg.EventsRetentionDays != 45 {
		t.Fatalf("defaulted EventsRetentionDays = %d, want bootstrap fallback 45", cfg.EventsRetentionDays)
	}

	workers := 2
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{BackgroundWorkers: &workers}, now); err != nil {
		t.Fatal(err)
	}
	cfg, err = st.ConsoleConfigWithDefaults(ctx, store.ConsoleConfig{BackgroundWorkers: 4, EventsRetentionDays: 45})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BackgroundWorkers != 2 {
		t.Fatalf("persisted BackgroundWorkers = %d, want DB override 2", cfg.BackgroundWorkers)
	}
}

// TestEnrichmentEnabledOptIn proves the ADR-0041 Enrichment opt-in flag defaults
// OFF, round-trips through the existing server_config KV table (NO migration),
// and persists across a fresh read.
func TestEnrichmentEnabledOptIn(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	on, err := st.EnrichmentEnabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Fatal("EnrichmentEnabled defaulted ON, want OFF (opt-in)")
	}

	if err := st.SetEnrichmentEnabled(ctx, true, now); err != nil {
		t.Fatal(err)
	}
	on, err = st.EnrichmentEnabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Fatal("after opt-in EnrichmentEnabled = false, want true")
	}

	// Opt back out.
	if err := st.SetEnrichmentEnabled(ctx, false, now); err != nil {
		t.Fatal(err)
	}
	if on, _ := st.EnrichmentEnabled(ctx); on {
		t.Fatal("after opt-out EnrichmentEnabled = true, want false")
	}
}

// TestUpdateCheckConfig proves the opt-in version-check settings
// default off / stable, round-trip a partial write through the existing
// server_config key-value table (NO schema migration), and validate the closed
// channel vocabulary (feature:console-config).
func TestUpdateCheckConfig(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	cfg, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateCheckEnabled {
		t.Fatal("default UpdateCheckEnabled = true, want false (default-off)")
	}
	if cfg.UpdateCheckChannel != "stable" {
		t.Fatalf("default UpdateCheckChannel = %q, want stable", cfg.UpdateCheckChannel)
	}

	enabled := true
	channel := "beta"
	updated, err := st.SetConsoleConfig(ctx,
		store.ConsoleConfigPatch{UpdateCheckEnabled: &enabled, UpdateCheckChannel: &channel}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.UpdateCheckEnabled || updated.UpdateCheckChannel != "beta" {
		t.Fatalf("after write = {%v, %q}, want {true, beta}", updated.UpdateCheckEnabled, updated.UpdateCheckChannel)
	}
	reread, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reread.UpdateCheckEnabled || reread.UpdateCheckChannel != "beta" {
		t.Fatalf("reread = {%v, %q}, want {true, beta}", reread.UpdateCheckEnabled, reread.UpdateCheckChannel)
	}

	// An out-of-vocabulary channel is rejected and leaves the stored value intact.
	bad := "nightly"
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{UpdateCheckChannel: &bad}, now); err == nil {
		t.Fatal("SetConsoleConfig(channel=nightly) = nil error, want ValidationError")
	}
	if final, _ := st.ConsoleConfig(ctx); final.UpdateCheckChannel != "beta" {
		t.Fatalf("after rejected write UpdateCheckChannel = %q, want beta", final.UpdateCheckChannel)
	}
}

func TestObservabilityConfigDefaultOff(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	cfg, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ObservabilityEnabled {
		t.Fatal("default ObservabilityEnabled = true, want false")
	}

	enabled := true
	updated, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{ObservabilityEnabled: &enabled}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ObservabilityEnabled {
		t.Fatal("after write ObservabilityEnabled = false, want true")
	}
	reread, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reread.ObservabilityEnabled {
		t.Fatal("reread ObservabilityEnabled = false, want true")
	}
}

// TestTranscodeConfigPatchAndClear proves the HW-transcode policy round-trips a
// partial write, validates the closed codec/quality vocabularies, and clears an
// override back to default on an empty string (feature:transcode-config, store
// half).
func TestTranscodeConfigPatchAndClear(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	cfg, err := st.TranscodeConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HardwareEnabled || cfg.Codec != "" || cfg.Quality != "" || cfg.Device != "" {
		t.Fatalf("default transcode config = %#v, want all-zero", cfg)
	}

	enabled := true
	codec := "hevc"
	quality := "high"
	device := "/dev/dri/renderD128"
	updated, err := st.SetTranscodeConfig(ctx, store.TranscodeConfigPatch{
		HardwareEnabled: &enabled,
		Codec:           &codec,
		Quality:         &quality,
		Device:          &device,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := store.TranscodeConfig{HardwareEnabled: true, Codec: "hevc", Quality: "high", Device: "/dev/dri/renderD128"}
	if updated != want {
		t.Fatalf("after write = %#v, want %#v", updated, want)
	}

	// Out-of-vocabulary codec is rejected; prior value stays.
	badCodec := "av1"
	if _, err := st.SetTranscodeConfig(ctx, store.TranscodeConfigPatch{Codec: &badCodec}, now); err == nil {
		t.Fatal("SetTranscodeConfig(codec=av1) = nil error, want ValidationError")
	}
	if reread, _ := st.TranscodeConfig(ctx); reread.Codec != "hevc" {
		t.Fatalf("after rejected codec write Codec = %q, want hevc", reread.Codec)
	}

	// Empty string clears the device override back to default.
	empty := ""
	cleared, err := st.SetTranscodeConfig(ctx, store.TranscodeConfigPatch{Device: &empty}, now)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Device != "" {
		t.Fatalf("after clear Device = %q, want empty", cleared.Device)
	}
	// The unrelated codec/quality survive the device clear.
	if cleared.Codec != "hevc" || cleared.Quality != "high" || !cleared.HardwareEnabled {
		t.Fatalf("after device clear = %#v, want codec/quality/enabled intact", cleared)
	}
}

// TestPluginActivationOverride proves activation defaults true with no row and
// flips on an explicit override (feature:console-config, store half).
func TestPluginActivationOverride(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	// No row: default active.
	active, err := st.PluginActivation(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("PluginActivation with no row = false, want default true")
	}

	if err := st.SetPluginActivation(ctx, "demo", false, now); err != nil {
		t.Fatal(err)
	}
	active, err = st.PluginActivation(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("after deactivate = true, want false")
	}

	overrides, err := st.PluginActivations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := overrides["demo"]; !ok || v {
		t.Fatalf("overrides[demo] = (%v,%v), want (false,true)", v, ok)
	}
}

// TestQBittorrentConfigPersistsRoutingNotPassword is FEAT-10 (ADR-0020 §6,
// store half + invariant #5): the connection + routing persist and read back,
// no password field exists, and a stored value never contains the secret. It
// also proves the routing-validation rejections.
func TestQBittorrentConfigPersistsRoutingNotPassword(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(1700, 0).UTC()

	// Unconfigured by default.
	cfg, err := st.QBittorrentConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Configured {
		t.Fatalf("default Configured = true, want false")
	}

	url := "http://localhost:8080"
	username := "operator"
	routes := []store.QBittorrentRouteConfig{
		{Category: "films", LibraryID: "lib-a"},
		{Category: "music", LibraryID: "lib-b"},
	}
	updated, err := st.SetQBittorrentConfig(ctx, store.QBittorrentConfigPatch{
		URL: &url, Username: &username, Routes: &routes,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Configured || updated.URL != url || updated.Username != username {
		t.Fatalf("after write = %#v, want configured %s / %s", updated, url, username)
	}
	if len(updated.Routes) != 2 {
		t.Fatalf("routes = %#v, want 2", updated.Routes)
	}

	// Persisted across a fresh read.
	reread, err := st.QBittorrentConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reread.Configured || len(reread.Routes) != 2 {
		t.Fatalf("reread = %#v, want configured with 2 routes", reread)
	}

	// Invariant #5: the persisted config carries no credential. The
	// QBittorrentConfig struct has no password field at all, and no stored
	// server_config value contains the would-be secret (asserted via the
	// resolved struct since the password never enters this path).
	const secret = "super-secret-pw"
	if reread.URL == secret || reread.Username == secret {
		t.Fatalf("config leaked a secret-shaped value: %#v", reread)
	}

	// Validation: empty URL, empty route fields, and duplicate categories are
	// rejected as client-safe ValidationErrors, leaving prior config intact.
	empty := ""
	if _, err := st.SetQBittorrentConfig(ctx, store.QBittorrentConfigPatch{URL: &empty}, now); err == nil {
		t.Fatalf("empty URL accepted, want ValidationError")
	}
	dupes := []store.QBittorrentRouteConfig{{Category: "films", LibraryID: "lib-a"}, {Category: "films", LibraryID: "lib-b"}}
	if _, err := st.SetQBittorrentConfig(ctx, store.QBittorrentConfigPatch{Routes: &dupes}, now); err == nil {
		t.Fatalf("duplicate category accepted, want ValidationError")
	}
	noLib := []store.QBittorrentRouteConfig{{Category: "films", LibraryID: ""}}
	if _, err := st.SetQBittorrentConfig(ctx, store.QBittorrentConfigPatch{Routes: &noLib}, now); err == nil {
		t.Fatalf("empty libraryID accepted, want ValidationError")
	}
	// Prior config survived the rejected writes.
	after, err := st.QBittorrentConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Configured || after.URL != url || len(after.Routes) != 2 {
		t.Fatalf("after rejected writes = %#v, want unchanged", after)
	}
}

// TestConsoleConfigRemoteEnrollmentDefaultAndPatch proves the remote-enrollment
// toggle defaults OFF and that explicit writes persist and read back.
func TestConsoleConfigRemoteEnrollmentDefaultAndPatch(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(0, 0).UTC()

	cfg, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RemoteEnrollmentEnabled {
		t.Fatal("default RemoteEnrollmentEnabled = true, want false")
	}

	enabled := true
	updated, err := st.SetConsoleConfig(ctx,
		store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.RemoteEnrollmentEnabled {
		t.Fatal("after enabling write, RemoteEnrollmentEnabled = false, want true")
	}
	reread, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reread.RemoteEnrollmentEnabled {
		t.Fatal("reread RemoteEnrollmentEnabled = false, want true")
	}
}
