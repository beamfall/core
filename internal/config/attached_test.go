package config

import (
	"testing"

	"github.com/beamfall/core/internal/serverrole"
)

func TestLoadRoleDefaultsToMain(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir()}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Role != serverrole.Main {
		t.Fatalf("default Role = %v, want Main", cfg.Role)
	}
	if cfg.Role.Attached() {
		t.Fatal("default config reports Attached")
	}
}

func TestLoadAttachedFlagSetsAttachedRole(t *testing.T) {
	cfg, err := Load([]string{"--data-dir", t.TempDir(), "--attached"}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Role != serverrole.Attached {
		t.Fatalf("--attached Role = %v, want Attached", cfg.Role)
	}
}
