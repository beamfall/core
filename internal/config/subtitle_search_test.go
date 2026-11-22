package config

import (
	"slices"
	"testing"
)

func TestSubtitleSearchProvidersDefaultOffAndValidated(t *testing.T) {
	none := func(string) (string, bool) { return "", false }
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, none)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SubtitleSearchProviders) != 0 {
		t.Fatalf("default providers = %v, want none (opt-in)", cfg.SubtitleSearchProviders)
	}
	env := func(key string) (string, bool) {
		if key == "BEAMFALL_SUBTITLE_SEARCH_PROVIDERS" {
			return " opensubtitles ", true
		}
		return "", false
	}
	cfg, err = Load([]string{"--data-dir", t.TempDir()}, env)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.SubtitleSearchProviders, []string{"opensubtitles"}) {
		t.Fatalf("env providers = %v", cfg.SubtitleSearchProviders)
	}
	cfg, err = Load([]string{"--data-dir", t.TempDir(), "--subtitle-search-providers="}, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SubtitleSearchProviders) != 0 {
		t.Fatalf("explicit empty flag kept env providers %v", cfg.SubtitleSearchProviders)
	}
	if _, err := Load([]string{"--data-dir", t.TempDir(), "--subtitle-search-providers", "opensubtitles,elsewhere"}, none); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
