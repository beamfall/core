package graph

import (
	"context"
	"net/http"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const (
	setRemoteCapMutation     = `mutation S($remoteMaxBitrate: Int!) { setConsoleConfig(remoteMaxBitrate: $remoteMaxBitrate) { remoteMaxBitrate } }`
	updateProfileCapMutation = `mutation U($profileID: String!, $remoteMaxBitrate: Int!) { updateProfile(profileID: $profileID, remoteMaxBitrate: $remoteMaxBitrate) { id remoteMaxBitrate } }`
)

// TestRemoteMaxBitrateAdminWrites covers CORE-1717's graph half: an Admin
// Session sets the server-wide and per-Profile remote bitrate caps, both read
// back on the wire and in the store, and out-of-range values are refused.
func TestRemoteMaxBitrateAdminWrites(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	resp := postGraphJSON(t, handler, authorizeManager, setRemoteCapMutation, map[string]any{"remoteMaxBitrate": 6_000_000})
	if len(resp.Errors) != 0 {
		t.Fatalf("setConsoleConfig errors = %#v", resp.Errors)
	}
	var cfg ConsoleConfigInfo
	mustUnmarshal(t, resp.Data["setConsoleConfig"], &cfg)
	if cfg.RemoteMaxBitrate != 6_000_000 {
		t.Fatalf("setConsoleConfig remoteMaxBitrate = %d, want 6000000", cfg.RemoteMaxBitrate)
	}
	resp = postGraphJSON(t, handler, authorizeManager, `query { consoleConfig { remoteMaxBitrate } }`, nil)
	mustUnmarshal(t, resp.Data["consoleConfig"], &cfg)
	if cfg.RemoteMaxBitrate != 6_000_000 {
		t.Fatalf("consoleConfig remoteMaxBitrate = %d, want 6000000", cfg.RemoteMaxBitrate)
	}

	resp = postGraphJSON(t, handler, authorizeManager, updateProfileCapMutation, map[string]any{"profileID": "profile-default", "remoteMaxBitrate": 3_000_000})
	if len(resp.Errors) != 0 {
		t.Fatalf("updateProfile errors = %#v", resp.Errors)
	}
	var profile ProfileInfo
	mustUnmarshal(t, resp.Data["updateProfile"], &profile)
	if profile.RemoteMaxBitrate != 3_000_000 {
		t.Fatalf("updateProfile remoteMaxBitrate = %d, want 3000000", profile.RemoteMaxBitrate)
	}
	stored, err := st.Profile(context.Background(), "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if stored.RemoteMaxBitrate != 3_000_000 {
		t.Fatalf("store RemoteMaxBitrate = %d, want 3000000", stored.RemoteMaxBitrate)
	}

	// The Console reads the stored cap back from the profiles query (CORE-1853).
	resp = postGraphJSON(t, handler, authorizeManager, `query { profiles { id remoteMaxBitrate } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("profiles errors = %#v", resp.Errors)
	}
	var summaries []ProfileSummary
	mustUnmarshal(t, resp.Data["profiles"], &summaries)
	found := false
	for _, summary := range summaries {
		if summary.ID == "profile-default" {
			found = true
			if summary.RemoteMaxBitrate != 3_000_000 {
				t.Fatalf("profiles remoteMaxBitrate = %d, want 3000000", summary.RemoteMaxBitrate)
			}
		}
	}
	if !found {
		t.Fatalf("profiles = %#v, want profile-default", summaries)
	}

	for _, bad := range []any{-1, store.MaxRemoteBitrate + 1, 1.5} {
		if resp := postGraphJSON(t, handler, authorizeManager, setRemoteCapMutation, map[string]any{"remoteMaxBitrate": bad}); len(resp.Errors) == 0 {
			t.Fatalf("setConsoleConfig(%v) accepted an out-of-range cap", bad)
		}
		if resp := postGraphJSON(t, handler, authorizeManager, updateProfileCapMutation, map[string]any{"profileID": "profile-default", "remoteMaxBitrate": bad}); len(resp.Errors) == 0 {
			t.Fatalf("updateProfile(%v) accepted an out-of-range cap", bad)
		}
	}
	after, err := st.ConsoleConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.RemoteMaxBitrate != 6_000_000 {
		t.Fatalf("refused writes changed the server cap to %d", after.RemoteMaxBitrate)
	}
}

// TestRemoteMaxBitrateRequiresAdminSession proves both cap writes are
// Admin-Session-gated: a manager without an open Admin Session and a viewer
// are refused and nothing is stored.
func TestRemoteMaxBitrateRequiresAdminSession(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	for name, authorize := range map[string]func(*http.Request){
		"manager without admin session": authorizeManager,
		"viewer":                        authorizeViewer,
	} {
		if resp := postGraphJSON(t, handler, authorize, setRemoteCapMutation, map[string]any{"remoteMaxBitrate": 1_000_000}); len(resp.Errors) == 0 {
			t.Fatalf("%s: setConsoleConfig accepted", name)
		}
		if resp := postGraphJSON(t, handler, authorize, updateProfileCapMutation, map[string]any{"profileID": "profile-default", "remoteMaxBitrate": 1_000_000}); len(resp.Errors) == 0 {
			t.Fatalf("%s: updateProfile accepted", name)
		}
	}
	cfg, err := st.ConsoleConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.Profile(context.Background(), "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RemoteMaxBitrate != 0 || profile.RemoteMaxBitrate != 0 {
		t.Fatalf("refused writes stored caps: server %d, profile %d", cfg.RemoteMaxBitrate, profile.RemoteMaxBitrate)
	}
}
