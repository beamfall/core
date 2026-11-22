package app

import (
	"io"
	"log/slog"
	"testing"
)

func TestEmulatorAttestationOptInIsDevelopmentOnly(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if emulatorAttestationAllowed("development", logger) {
		t.Fatal("emulator attestation allowed without the opt-in")
	}
	t.Setenv(devEmulatorAttestationEnv, "1")
	if !emulatorAttestationAllowed("development", logger) {
		t.Fatal("opt-in ignored in a development build")
	}
	if emulatorAttestationAllowed("1.0.0", logger) {
		t.Fatal("opt-in honoured in a release build")
	}
}
