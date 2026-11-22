package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestJellyfinCompatConfigFallsBackToBootstrapUntilWritten(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	defaults := JellyfinCompatConfig{Enabled: true, AllowedCIDRs: []string{"192.168.1.0/24"}}
	cfg, err := st.JellyfinCompatConfig(ctx, defaults)
	if err != nil || !cfg.Enabled || !slices.Equal(cfg.AllowedCIDRs, defaults.AllowedCIDRs) || len(cfg.TrustedProxyCIDRs) != 0 || len(cfg.LibraryIDs) != 0 {
		t.Fatalf("unwritten config = %+v, %v; want the bootstrap defaults", cfg, err)
	}
	off := false
	cfg, err = st.SetJellyfinCompatConfig(ctx, JellyfinCompatPatch{Enabled: &off}, defaults, time.Now())
	if err != nil || cfg.Enabled || !slices.Equal(cfg.AllowedCIDRs, defaults.AllowedCIDRs) {
		t.Fatalf("after turning off = %+v, %v; want off with the bootstrap CIDRs kept", cfg, err)
	}
	if fresh, err := st.JellyfinCompatConfig(ctx, JellyfinCompatConfig{}); err != nil || fresh.Enabled {
		t.Fatalf("fresh defaults = %+v, %v; want off", fresh, err)
	}
}

func TestJellyfinCompatCIDRsMustSitInATrustZone(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	good := []string{" 192.168.1.7/24 ", "10.0.0.0/8", "127.0.0.1/32", "fd12:3456::/48", "::1/128", "169.254.0.0/16", "192.168.1.0/24"}
	cfg, err := st.SetJellyfinCompatConfig(ctx, JellyfinCompatPatch{AllowedCIDRs: &good, TrustedProxyCIDRs: &[]string{"172.16.5.0/24"}}, JellyfinCompatConfig{}, time.Now())
	want := []string{"192.168.1.0/24", "10.0.0.0/8", "127.0.0.1/32", "fd12:3456::/48", "::1/128", "169.254.0.0/16"}
	if err != nil || !slices.Equal(cfg.AllowedCIDRs, want) || !slices.Equal(cfg.TrustedProxyCIDRs, []string{"172.16.5.0/24"}) {
		t.Fatalf("trust-zone CIDRs = %+v, %v; want normalized and deduplicated %v", cfg, err, want)
	}
	for _, bad := range []string{"100.64.0.0/10", "100.100.1.0/24", "8.8.8.0/24", "0.0.0.0/0", "10.0.0.0/7", "2001:db8::/32", "::/0", "192.168.1.1", "nonsense", "::ffff:10.0.0.0/104"} {
		_, err := st.SetJellyfinCompatConfig(ctx, JellyfinCompatPatch{AllowedCIDRs: &[]string{"10.0.0.0/8", bad}}, JellyfinCompatConfig{}, time.Now())
		var validation ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("CIDR %q accepted (err %v); want a validation error", bad, err)
		}
	}
	if cfg, _ := st.JellyfinCompatConfig(ctx, JellyfinCompatConfig{}); !slices.Equal(cfg.AllowedCIDRs, want) {
		t.Fatalf("refused writes changed the stored CIDRs to %v", cfg.AllowedCIDRs)
	}
}

func TestJellyfinCompatAllowlistRefusesRestrictedAndChecksAtUseTime(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	open, err := st.CreateLibrary(ctx, "Open", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := st.CreateLibrary(ctx, "Restricted", true, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	selectable, err := st.JellyfinCompatSelectableLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, library := range selectable {
		if library.ID == restricted.ID || library.IsRestricted {
			t.Fatalf("selectable libraries include restricted library %+v", library)
		}
	}
	for _, ids := range [][]LibraryID{{restricted.ID}, {open.ID, "missing-library"}} {
		_, err := st.SetJellyfinCompatConfig(ctx, JellyfinCompatPatch{LibraryIDs: &ids}, JellyfinCompatConfig{}, time.Now())
		var validation ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("allowlist %v accepted (err %v); want a validation error", ids, err)
		}
	}
	cfg, err := st.SetJellyfinCompatConfig(ctx, JellyfinCompatPatch{LibraryIDs: &[]LibraryID{open.ID, open.ID}}, JellyfinCompatConfig{}, time.Now())
	if err != nil || !slices.Equal(cfg.LibraryIDs, []LibraryID{open.ID}) {
		t.Fatalf("allowlist = %+v, %v", cfg, err)
	}
	if eligible, err := st.JellyfinCompatEligibleLibraries(ctx); err != nil || !eligible[open.ID] {
		t.Fatalf("allowlisted library eligible = %v, %v; want true", eligible, err)
	} else if eligible[restricted.ID] {
		t.Fatal("restricted library not on the allowlist is eligible")
	}
	if err := st.UpdateLibraryRestricted(ctx, open.ID, true); err != nil {
		t.Fatal(err)
	}
	if eligible, err := st.JellyfinCompatEligibleLibraries(ctx); err != nil || eligible[open.ID] {
		t.Fatalf("library restricted after allowlisting eligible = %v, %v; want false", eligible, err)
	}
}

func TestProfileIntegrationSignInOptIn(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	child, err := st.CreateProfile(ctx, "Kid", ProfileManaged, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	var validation ValidationError
	if _, err := st.SetProfileIntegrationSignIn(ctx, child.ID, true); !errors.As(err, &validation) {
		t.Fatalf("managed profile opt-in: %v; want a validation error", err)
	}
	away, err := st.CreateProfile(ctx, "Away", ProfileAdult, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if _, err := st.UpdateProfileSettings(ctx, away.ID, ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileIntegrationSignIn(ctx, away.ID, true); !errors.As(err, &validation) {
		t.Fatalf("disabled adult profile opt-in: %v; want a validation error", err)
	}
	if _, err := st.SetProfileIntegrationSignIn(ctx, "profile-missing", true); err == nil || err.Error() != "profile not found" {
		t.Fatalf("missing profile opt-in: %v; want profile not found", err)
	}
	profile, err := st.SetProfileIntegrationSignIn(ctx, "profile-default", true)
	if err != nil || !profile.IntegrationSignIn {
		t.Fatalf("adult opt-in = %+v, %v", profile, err)
	}
	session := Session{
		ID: "integration-opt-in", ProfileID: "profile-default", Kind: SessionKindIntegration,
		IntegrationScopes: []IntegrationScope{IntegrationScopeRead}, ExpiresAt: time.Now().Add(time.Hour),
	}
	policy := AuthBackoff{Window: time.Minute, FreeAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Minute}
	reservation, _, err := st.ReserveAuthAttempt(ctx, IntegrationAttemptAction, []string{"profile:profile-default"}, time.Now(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateIntegrationSession(ctx, session, true, reservation, time.Now); err != nil {
		t.Fatal(err)
	}
	if profile, err := st.SetProfileIntegrationSignIn(ctx, "profile-default", false); err != nil || profile.IntegrationSignIn {
		t.Fatalf("opt-out = %+v, %v", profile, err)
	}
	if _, err := st.Session(ctx, session.ID); err == nil {
		t.Fatal("opt-out did not revoke the integration session")
	}
	// A Profile edit that read the row before the opt-out must not write the
	// stale opt-in back when it lands after it.
	stale := profile
	stale.IntegrationSignIn = true
	stale.Name = "Renamed"
	if err := st.updateProfileSettingsSQL(ctx, stale, ProfileSettings{Name: &stale.Name}); err != nil {
		t.Fatal(err)
	}
	if after, err := st.Profile(ctx, "profile-default"); err != nil || after.IntegrationSignIn || after.Name != "Renamed" {
		t.Fatalf("stale edit after opt-out = %+v, %v; want renamed and still opted out", after, err)
	}
}
