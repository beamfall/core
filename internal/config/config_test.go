package config

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadCreatesStreamTokenKeyFile0600(t *testing.T) {
	dataDir := t.TempDir()

	cfg, err := Load([]string{"--data-dir", dataDir}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.StreamTokenKey) != 32 {
		t.Fatalf("len(StreamTokenKey) = %d, want 32", len(cfg.StreamTokenKey))
	}
	info, err := os.Stat(filepath.Join(dataDir, "stream-token.key"))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestJellyfinCompatDefaultsOffAndValidatesCIDRs(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.JellyfinCompat.Enabled {
		t.Fatal("Jellyfin compatibility mode defaulted on")
	}
	if len(cfg.JellyfinCompat.AllowedCIDRs) != 2 {
		t.Fatalf("default allowed CIDRs = %v", cfg.JellyfinCompat.AllowedCIDRs)
	}
	cfg, err = Load([]string{
		"--data-dir", t.TempDir(), "--jellyfin-compat-enabled",
		"--jellyfin-compat-cidrs=10.0.0.0/8", "--jellyfin-compat-trusted-proxies=127.0.0.0/8",
	}, func(string) (string, bool) { return "", false })
	if err != nil || !cfg.JellyfinCompat.Enabled || !reflect.DeepEqual(cfg.JellyfinCompat.AllowedCIDRs, []string{"10.0.0.0/8"}) {
		t.Fatalf("compat config = %+v, %v", cfg.JellyfinCompat, err)
	}
	if _, err := Load([]string{"--data-dir", t.TempDir(), "--jellyfin-compat-cidrs=not-a-cidr"}, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("invalid compatibility CIDR accepted")
	}
}

func TestLoadRejectsWeakEnvStreamTokenKey(t *testing.T) {
	_, err := Load(nil, func(key string) (string, bool) {
		if key == "BEAMFALL_STREAM_TOKEN_KEY" {
			return base64.RawStdEncoding.EncodeToString([]byte("short")), true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("Load accepted weak stream token key")
	}
}

func TestHardwareAccelReportJSONIsSanitized(t *testing.T) {
	report := HardwareAccelReport{
		Working: []string{"qsv"},
		Results: []HardwareAccelProbe{
			{Accelerator: "qsv", Status: "working"},
			{Accelerator: "vaapi", Status: "configured-but-failed"},
			{Accelerator: "nvenc", Status: "unavailable"},
		},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, forbidden := range []string{"/dev/dri", "/Users/", "movie.mkv", "ffmpeg failed"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("hardware acceleration report leaked %q in %s", forbidden, body)
		}
	}
}

func TestLoadUsesEnvStreamTokenKey(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	cfg, err := Load(nil, func(name string) (string, bool) {
		if name == "BEAMFALL_STREAM_TOKEN_KEY" {
			return base64.RawStdEncoding.EncodeToString(key), true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if string(cfg.StreamTokenKey) != string(key) {
		t.Fatal("Load did not use env stream token key")
	}
}

func TestLoadDevelopmentSeedDefaultsOff(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SeedDevelopmentData {
		t.Fatal("SeedDevelopmentData defaulted true")
	}
}

func TestLoadDiscoveryDefaultsOn(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.Discovery.Enabled {
		t.Fatal("Discovery.Enabled defaulted false")
	}
}

func TestLoadDiscoveryCanBeDisabled(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--discovery-enabled=false"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Discovery.Enabled {
		t.Fatal("Discovery.Enabled = true, want false")
	}
}

func TestLoadPublicMetricsDefaultsOffAndCanBeEnabled(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.PublicMetrics {
		t.Fatal("PublicMetrics defaulted true")
	}

	cfg, err = Load([]string{"--data-dir", t.TempDir(), "--public-metrics"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load flag returned error: %v", err)
	}
	if !cfg.PublicMetrics {
		t.Fatal("PublicMetrics flag = false, want true")
	}

	cfg, err = Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_PUBLIC_METRICS" {
			return "true", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("Load env returned error: %v", err)
	}
	if !cfg.PublicMetrics {
		t.Fatal("PublicMetrics env = false, want true")
	}
}

func TestLoadResourceGovernanceLowPowerDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.GenerationWorkers != 1 || cfg.BackgroundCPUBudget != 1 || cfg.GenerationNiceness != 10 {
		t.Fatalf("resource defaults = workers %d budget %d niceness %d, want 1/1/10",
			cfg.GenerationWorkers, cfg.BackgroundCPUBudget, cfg.GenerationNiceness)
	}

	cfg, err = Load([]string{
		"--data-dir", t.TempDir(),
		"--generation-workers=3",
		"--background-cpu-budget=2",
		"--generation-niceness=7",
	}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load overrides returned error: %v", err)
	}
	if cfg.GenerationWorkers != 3 || cfg.BackgroundCPUBudget != 2 || cfg.GenerationNiceness != 7 {
		t.Fatalf("resource overrides = workers %d budget %d niceness %d, want 3/2/7",
			cfg.GenerationWorkers, cfg.BackgroundCPUBudget, cfg.GenerationNiceness)
	}
}

func TestLoadTranscodeScratchDirAndOperatorLogLevel(t *testing.T) {
	scratchDir := t.TempDir()
	cfg, err := Load([]string{
		"--data-dir", t.TempDir(),
		"--transcode-tmp", scratchDir,
		"--log-level", "debug",
	}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load flags returned error: %v", err)
	}
	if cfg.TranscodeScratchDir != scratchDir {
		t.Fatalf("TranscodeScratchDir = %q", cfg.TranscodeScratchDir)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %v, want DEBUG", cfg.LogLevel)
	}

	dataDir := t.TempDir()
	envScratchDir := t.TempDir()
	cfg, err = Load([]string{"--data-dir", dataDir}, func(name string) (string, bool) {
		switch name {
		case "BEAMFALL_TRANSCODE_TMP":
			return envScratchDir, true
		case "BEAMFALL_LOG_LEVEL":
			return "warn", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("Load env returned error: %v", err)
	}
	if cfg.TranscodeScratchDir != envScratchDir {
		t.Fatalf("env TranscodeScratchDir = %q", cfg.TranscodeScratchDir)
	}
	if cfg.LogLevel != slog.LevelWarn {
		t.Fatalf("env LogLevel = %v, want WARN", cfg.LogLevel)
	}
}

func TestLoadDefaultsTranscodeScratchDirUnderDataDir(t *testing.T) {
	dataDir := t.TempDir()
	cfg, err := Load([]string{"--data-dir", dataDir}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if want := filepath.Join(dataDir, "transcode"); cfg.TranscodeScratchDir != want {
		t.Fatalf("TranscodeScratchDir = %q, want %q", cfg.TranscodeScratchDir, want)
	}
}

func TestLoadRejectsInvalidOperatorLogLevelEnv(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_LOG_LEVEL" {
			return "verbose", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("Load accepted invalid BEAMFALL_LOG_LEVEL")
	}
}

func TestLoadRejectsInvalidTranscodeScratchDir(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir(), "--transcode-tmp", filepath.Join(t.TempDir(), "missing")}, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("Load accepted nonexistent --transcode-tmp")
	}

	unwritable := t.TempDir()
	if err := os.Chmod(unwritable, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unwritable, 0o700) })
	_, err = Load([]string{"--data-dir", t.TempDir(), "--transcode-tmp", unwritable}, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("Load accepted unwritable --transcode-tmp")
	}
}

func TestLoadRejectsInvalidDiscoveryEnabledEnv(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_DISCOVERY_ENABLED" {
			return "not-bool", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("Load accepted invalid BEAMFALL_DISCOVERY_ENABLED")
	}
}

func TestLoadRejectsNegativeCacheBudgetFlags(t *testing.T) {
	for _, flag := range []string{"--playback-cache-budget-bytes=-1", "--hot-segment-cache-bytes=-1"} {
		t.Run(flag, func(t *testing.T) {
			_, err := Load([]string{"--data-dir", t.TempDir(), flag}, func(string) (string, bool) { return "", false })
			if err == nil {
				t.Fatalf("Load accepted %s", flag)
			}
		})
	}
}

func TestLoadDevelopmentSeedCanBeEnabled(t *testing.T) {
	// --seed-development-data plants known dev credentials, so it is gated behind
	// the test/dev-only --allow-test-seed gate (ATEST-CORE-1 production refusal).
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--seed-development-data", "--allow-test-seed"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.SeedDevelopmentData {
		t.Fatal("SeedDevelopmentData = false, want true")
	}
}

func TestLoadDevelopmentSeedRefusedWithoutAllowTestSeed(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir(), "--seed-development-data"}, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("Load accepted --seed-development-data without --allow-test-seed")
	}
}

func TestLoadAcceptanceSeedRequiresAllowTestSeed(t *testing.T) {
	if _, err := Load([]string{"--data-dir", t.TempDir(), "--seed-profile=acceptance"}, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("Load accepted --seed-profile=acceptance without --allow-test-seed")
	}
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--seed-profile=acceptance", "--allow-test-seed"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SeedProfile != "acceptance" {
		t.Fatalf("SeedProfile = %q, want acceptance", cfg.SeedProfile)
	}
}

func TestLoadSeedProfileFlagAndEnvPrecedence(t *testing.T) {
	// --seed-profile is the contract-named flag; it parses into SeedProfile.
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--seed-profile=demo"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SeedProfile != "demo" {
		t.Fatalf("--seed-profile: SeedProfile = %q, want demo", cfg.SeedProfile)
	}
	// BEAMFALL_SEED_PROFILE wins over BEAMFALL_SEED when both are set.
	env := map[string]string{"BEAMFALL_SEED": "development", "BEAMFALL_SEED_PROFILE": "demo"}
	cfg, err = Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SeedProfile != "demo" {
		t.Fatalf("env precedence: SeedProfile = %q, want demo (BEAMFALL_SEED_PROFILE wins)", cfg.SeedProfile)
	}
}

func TestLoadDemoSeedCanBeSelected(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--seed=demo"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SeedProfile != "demo" {
		t.Fatalf("SeedProfile = %q, want demo", cfg.SeedProfile)
	}
	if cfg.SeedDevelopmentData {
		t.Fatal("SeedDevelopmentData = true for named demo seed")
	}
}

func TestLoadReviewDemoEnrollmentMetadata(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--review-demo-enrollment-enabled", "--review-demo-profile-id=profile-custom"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.ReviewDemoEnrollment.Enabled {
		t.Fatal("ReviewDemoEnrollment.Enabled = false, want true")
	}
	if cfg.ReviewDemoEnrollment.ProfileID != "profile-custom" {
		t.Fatalf("ReviewDemoEnrollment.ProfileID = %q, want profile-custom", cfg.ReviewDemoEnrollment.ProfileID)
	}
}

func TestLoadReviewDemoEnrollmentDefaultsProfile(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--review-demo-enrollment-enabled"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.ReviewDemoEnrollment.ProfileID != "profile-demo-default" {
		t.Fatalf("ReviewDemoEnrollment.ProfileID = %q, want profile-demo-default", cfg.ReviewDemoEnrollment.ProfileID)
	}
}

func TestLoadRejectsInvalidReviewDemoEnrollmentEnv(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_REVIEW_DEMO_ENROLLMENT_ENABLED" {
			return "not-bool", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("Load accepted invalid BEAMFALL_REVIEW_DEMO_ENROLLMENT_ENABLED")
	}
}

func TestLoadRejectsUnknownSeedProfile(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir(), "--seed=nope"}, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("Load accepted unknown seed profile")
	}
}

func TestLoadRejectsConformanceSeedWithoutGate(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir(), "--seed=conformance"}, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("Load accepted conformance seed without --allow-conformance-seed (must stay test/dev-only)")
	}
}

func TestLoadAcceptsConformanceSeedWithFlag(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--seed=conformance", "--allow-conformance-seed"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SeedProfile != "conformance" {
		t.Fatalf("SeedProfile = %q, want conformance", cfg.SeedProfile)
	}
	if !cfg.AllowConformanceSeed {
		t.Fatal("AllowConformanceSeed = false after --allow-conformance-seed")
	}
}

// TestPodcastIndexCredentialsAbsentFromConfig proves the Podcast Index API key
// and secret have left argv/env/config entirely (invariant 5: they live in 0600
// secret files, read at use time). It asserts three migration facts that each
// fail if a field, flag, or env binding is re-added:
//   - the Config struct has no PodcastIndexKey / PodcastIndexSecret field
//     (reflection — a re-added field is caught here, not silently tolerated);
//   - Load rejects the removed --podcast-index-key / --podcast-index-secret
//     flags ("flag provided but not defined");
//   - a BEAMFALL_PODCAST_INDEX_SECRET env value is ignored, Load still succeeds.
func TestPodcastIndexCredentialsAbsentFromConfig(t *testing.T) {
	configType := reflect.TypeOf(Config{})
	for _, field := range []string{"PodcastIndexKey", "PodcastIndexSecret"} {
		if _, ok := configType.FieldByName(field); ok {
			t.Fatalf("Config.%s still exists; Podcast Index credentials must live in a 0600 secret file, not config", field)
		}
	}

	noEnv := func(string) (string, bool) { return "", false }
	for _, flag := range []string{"--podcast-index-key", "--podcast-index-secret"} {
		_, err := Load([]string{"--data-dir", t.TempDir(), flag, "x"}, noEnv)
		if err == nil {
			t.Fatalf("Load accepted removed flag %s; it must be undefined", flag)
		}
		if !strings.Contains(err.Error(), "not defined") {
			t.Fatalf("Load(%s) error = %q, want 'flag provided but not defined'", flag, err)
		}
	}

	ignoredEnv := func(key string) (string, bool) {
		if key == "BEAMFALL_PODCAST_INDEX_KEY" || key == "BEAMFALL_PODCAST_INDEX_SECRET" {
			return "leaked-secret", true
		}
		return "", false
	}
	if _, err := Load([]string{"--data-dir", t.TempDir()}, ignoredEnv); err != nil {
		t.Fatalf("Load with podcast-index env set returned error: %v; env must be ignored, not consumed", err)
	}
}

func TestLoadAcceptsConformanceSeedViaEnvGate(t *testing.T) {
	env := func(key string) (string, bool) {
		if key == "BEAMFALL_ALLOW_CONFORMANCE_SEED" {
			return "1", true
		}
		return "", false
	}
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--seed=conformance"}, env)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.AllowConformanceSeed {
		t.Fatal("AllowConformanceSeed = false after BEAMFALL_ALLOW_CONFORMANCE_SEED=1")
	}
}

func TestLoadAllowNetworkDataDirCanBeEnabled(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--allow-network-data-dir"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.AllowNetworkDataDir {
		t.Fatal("AllowNetworkDataDir = false, want true")
	}
}

func TestLoadRejectsInvalidAllowNetworkDataDirEnv(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_ALLOW_NETWORK_DATA_DIR" {
			return "not-bool", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("Load accepted invalid BEAMFALL_ALLOW_NETWORK_DATA_DIR")
	}
}

func TestLoadRejectsInvalidDevelopmentSeedEnv(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_SEED_DEVELOPMENT_DATA" {
			return "not-bool", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("Load accepted invalid BEAMFALL_SEED_DEVELOPMENT_DATA")
	}
}

func TestLoadTrashEnabledDefaultsOn(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.TrashEnabled {
		t.Fatal("TrashEnabled defaulted false; hard invariant 6 requires trash ON by default")
	}
}

func TestLoadTrashCanBeDisabledByEnv(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_TRASH_ENABLED" {
			return "false", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.TrashEnabled {
		t.Fatal("TrashEnabled = true with BEAMFALL_TRASH_ENABLED=false")
	}
}

func TestLoadTrashCanBeDisabledByFlag(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--trash-enabled=false"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.TrashEnabled {
		t.Fatal("TrashEnabled = true with --trash-enabled=false")
	}
}

func TestLoadRejectsInvalidTrashEnabledEnv(t *testing.T) {
	_, err := Load([]string{"--data-dir", t.TempDir()}, func(name string) (string, bool) {
		if name == "BEAMFALL_TRASH_ENABLED" {
			return "not-bool", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("Load accepted invalid BEAMFALL_TRASH_ENABLED")
	}
}

func TestLoadRejectsStreamTokenKeyFileNot0600(t *testing.T) {
	dataDir := t.TempDir()
	key := make([]byte, 32)
	path := filepath.Join(dataDir, "stream-token.key")
	if err := os.WriteFile(path, []byte(base64.RawStdEncoding.EncodeToString(key)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load([]string{"--data-dir", dataDir}, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("Load accepted stream token key file with broad permissions")
	}
}

func TestLoadQBittorrentRoutingFromFlag(t *testing.T) {
	cfg, err := Load(
		[]string{"--data-dir", t.TempDir(), "--qbittorrent-routing", "inbox=lib-a, archive = lib-b "},
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.QBittorrentRouting["inbox"]; got != "lib-a" {
		t.Errorf("inbox route: got %q want lib-a", got)
	}
	if got := cfg.QBittorrentRouting["archive"]; got != "lib-b" {
		t.Errorf("archive route (whitespace-trimmed): got %q want lib-b", got)
	}
}

func TestQBittorrentRoutingHelpUsesNeutralExamples(t *testing.T) {
	help := strings.ToLower(qbittorrentRoutingHelp)
	for _, forbidden := range []string{"film", "movie", "music", "show", "series"} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("qbittorrent-routing help contains domain example %q: %s", forbidden, qbittorrentRoutingHelp)
		}
	}
	for _, want := range []string{"inbox=lib-a", "archive=lib-b"} {
		if !strings.Contains(help, want) {
			t.Fatalf("qbittorrent-routing help missing neutral example %q: %s", want, qbittorrentRoutingHelp)
		}
	}
}

func TestLoadQBittorrentRoutingRejectsMalformedPair(t *testing.T) {
	_, err := Load(
		[]string{"--data-dir", t.TempDir(), "--qbittorrent-routing", "inbox"},
		func(string) (string, bool) { return "", false },
	)
	if err == nil {
		t.Fatal("Load accepted a routing entry with no '=' separator")
	}
}

func TestLoadQBittorrentRoutingRejectsConflict(t *testing.T) {
	_, err := Load(
		[]string{"--data-dir", t.TempDir(), "--qbittorrent-routing", "inbox=lib-a,inbox=lib-b"},
		func(string) (string, bool) { return "", false },
	)
	if err == nil {
		t.Fatal("Load accepted conflicting routes for the same category")
	}
}

func TestPlaybackCacheBudgetDefaultsOnAndZeroDisables(t *testing.T) {
	noEnv := func(string) (string, bool) { return "", false }
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlaybackCacheBudgetBytes != DefaultPlaybackCacheBudgetBytes || DefaultPlaybackCacheBudgetBytes <= 0 {
		t.Fatalf("default budget = %d, want %d", cfg.PlaybackCacheBudgetBytes, DefaultPlaybackCacheBudgetBytes)
	}
	cfg, err = Load([]string{"--data-dir", t.TempDir(), "--playback-cache-budget-bytes=0"}, noEnv)
	if err != nil || cfg.PlaybackCacheBudgetBytes != 0 {
		t.Fatalf("flag 0: budget = %d err %v, want 0", cfg.PlaybackCacheBudgetBytes, err)
	}
	cfg, err = Load([]string{"--data-dir", t.TempDir()}, func(key string) (string, bool) {
		if key == "BEAMFALL_PLAYBACK_CACHE_BUDGET_BYTES" {
			return "0", true
		}
		return "", false
	})
	if err != nil || cfg.PlaybackCacheBudgetBytes != 0 {
		t.Fatalf("env 0: budget = %d err %v, want 0", cfg.PlaybackCacheBudgetBytes, err)
	}
}
