package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestRemoteMaxBitrateRoundTrip covers CORE-1717's store half: the
// server-wide and per-Profile remote bitrate caps default to 0 (no cap),
// persist, read back, clear with 0 and refuse out-of-range values.
func TestRemoteMaxBitrateRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	now := time.Unix(0, 0).UTC()

	cfg, err := st.ConsoleConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.Profile(ctx, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RemoteMaxBitrate != 0 || profile.RemoteMaxBitrate != 0 {
		t.Fatalf("defaults = server %d / profile %d, want 0 / 0", cfg.RemoteMaxBitrate, profile.RemoteMaxBitrate)
	}

	for _, want := range []int{8_000_000, store.MaxRemoteBitrate, 0} {
		value := want
		updated, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteMaxBitrate: &value}, now)
		if err != nil {
			t.Fatalf("SetConsoleConfig(%d): %v", want, err)
		}
		reread, err := st.ConsoleConfig(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if updated.RemoteMaxBitrate != want || reread.RemoteMaxBitrate != want {
			t.Fatalf("server cap after write %d = %d (reread %d)", want, updated.RemoteMaxBitrate, reread.RemoteMaxBitrate)
		}
		saved, err := st.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{RemoteMaxBitrate: &value})
		if err != nil {
			t.Fatalf("UpdateProfileSettings(%d): %v", want, err)
		}
		profile, err := st.Profile(ctx, "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		if saved.RemoteMaxBitrate != want || profile.RemoteMaxBitrate != want {
			t.Fatalf("profile cap after write %d = %d (reread %d)", want, saved.RemoteMaxBitrate, profile.RemoteMaxBitrate)
		}
	}

	// An unrelated Profile write leaves the cap alone.
	value := 3_000_000
	if _, err := st.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{RemoteMaxBitrate: &value}); err != nil {
		t.Fatal(err)
	}
	sdh := true
	after, err := st.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{PreferSDH: &sdh})
	if err != nil {
		t.Fatal(err)
	}
	if after.RemoteMaxBitrate != value {
		t.Fatalf("unrelated write changed the profile cap to %d", after.RemoteMaxBitrate)
	}

	for _, bad := range []int{-1, store.MaxRemoteBitrate + 1} {
		value := bad
		var validation store.ValidationError
		if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteMaxBitrate: &value}, now); !errors.As(err, &validation) {
			t.Fatalf("SetConsoleConfig(%d) error = %v, want ValidationError", bad, err)
		}
		if _, err := st.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{RemoteMaxBitrate: &value}); !errors.As(err, &validation) {
			t.Fatalf("UpdateProfileSettings(%d) error = %v, want ValidationError", bad, err)
		}
	}
}
