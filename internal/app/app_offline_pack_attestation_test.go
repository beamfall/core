package app

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"log/slog"
	"testing"

	"github.com/beamfall/core/internal/capabilities"
)

func TestOfflinePackAttestationVerifierApplePolicy(t *testing.T) {
	prevVersion := capabilities.ReleaseVersion
	t.Cleanup(func() { capabilities.ReleaseVersion = prevVersion })

	deviceKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	minimalCBOR := []byte{0xa1, 0x63, 'f', 'm', 't', 0x60}

	t.Run("release build ignores dev opt-in", func(t *testing.T) {
		capabilities.ReleaseVersion = "v1.0.0"
		t.Setenv(devAppleAttestationEnv, "1")
		if appleDevAttestationAllowed(capabilities.ReleaseVersion, slog.Default()) {
			t.Fatal("release build accepted Apple dev attestation opt-in")
		}
	})

	t.Run("development build honors dev opt-in", func(t *testing.T) {
		capabilities.ReleaseVersion = "development"
		t.Setenv(devAppleAttestationEnv, "1")
		if !appleDevAttestationAllowed(capabilities.ReleaseVersion, slog.Default()) {
			t.Fatal("development build rejected explicit Apple dev attestation opt-in")
		}
	})

	t.Run("empty app IDs fails closed on Apple attestation", func(t *testing.T) {
		capabilities.ReleaseVersion = "development"
		t.Setenv(appleAttestAllowedAppIDsEnv, "")
		verifier, err := offlinePackAttestationVerifier(nil, slog.Default())
		if err != nil {
			t.Fatalf("offlinePackAttestationVerifier startup failed: %v", err)
		}
		if err := verifier.VerifyDeviceKey(context.Background(), "device-1", deviceKey.PublicKey().Bytes(), minimalCBOR); err == nil {
			t.Fatal("expected Apple attestation failure when allowed app IDs are unconfigured")
		}
	})

	t.Run("configured app IDs parse comma separated values", func(t *testing.T) {
		t.Setenv(appleAttestAllowedAppIDsEnv, " T3Y5YFTYRN.com.beamfall.mobile , , TEAM2.com.beamfall.tv ")
		got := appleAllowedAppIDs()
		if len(got) != 2 || got[0] != "T3Y5YFTYRN.com.beamfall.mobile" || got[1] != "TEAM2.com.beamfall.tv" {
			t.Fatalf("appleAllowedAppIDs() = %#v", got)
		}
	})
}
