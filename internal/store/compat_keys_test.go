package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCompatIntegrationKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.UnixMilli(1_800_000_000_000).UTC()
	readOnly := []IntegrationScope{IntegrationScopeRead, IntegrationScopeRefresh}
	var validation ValidationError
	if _, _, err := st.CreateCompatIntegrationKey(ctx, "profile-default", "Seerr", readOnly, now); !errors.As(err, &validation) {
		t.Fatalf("mint before opt-in: %v; want a validation error", err)
	}
	if _, err := st.SetProfileIntegrationSignIn(ctx, "profile-default", true); err != nil {
		t.Fatal(err)
	}
	for name, scopes := range map[string][]IntegrationScope{
		"setup":   {IntegrationScopeRead, IntegrationScopeSetup},
		"empty":   nil,
		"unknown": {"compat.stream"},
	} {
		if _, _, err := st.CreateCompatIntegrationKey(ctx, "profile-default", "Seerr", scopes, now); !errors.As(err, &validation) {
			t.Fatalf("%s scopes: %v; want a validation error", name, err)
		}
	}
	key, token, err := st.CreateCompatIntegrationKey(ctx, "profile-default", " Seerr ", readOnly, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, CompatKeyPrefix) || len(token) != len(CompatKeyPrefix)+64 || key.Name != "Seerr" {
		t.Fatalf("minted %+v with token shape %d", key, len(token))
	}
	var stored string
	if err := st.catalogR.QueryRowContext(ctx, `SELECT digest FROM compat_integration_key WHERE id = ?`, key.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != hashAPITokenSecret(token) {
		t.Fatal("key secret is not stored as its digest")
	}

	got, err := st.CompatIntegrationKeyByToken(ctx, token, now.Add(time.Second))
	if err != nil || got.ID != key.ID || got.ProfileID != "profile-default" || !got.LastUsedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("lookup = %+v, %v", got, err)
	}
	// Use inside the throttle window does not move last use.
	got, err = st.CompatIntegrationKeyByToken(ctx, token, now.Add(30*time.Second))
	if err != nil || !got.LastUsedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("throttled lookup = %+v, %v", got, err)
	}
	later := now.Add(25 * time.Hour)
	if got, err = st.CompatIntegrationKeyByToken(ctx, token, later); err != nil || !got.LastUsedAt.Equal(later) {
		t.Fatalf("lookup past 24h = %+v, %v", got, err)
	}
	for _, bad := range []string{"", token + "0", strings.ToUpper(token), CompatKeyPrefix + strings.Repeat("0", 64)} {
		if _, err := st.CompatIntegrationKeyByToken(ctx, bad, later); !errors.Is(err, ErrCompatKeyInvalid) {
			t.Fatalf("lookup %q: %v; want ErrCompatKeyInvalid", bad, err)
		}
	}

	keys, err := st.CompatIntegrationKeys(ctx)
	if err != nil || len(keys) != 1 || !keys[0].LastUsedAt.Equal(later) || keys[0].Revoked() {
		t.Fatalf("list = %+v, %v", keys, err)
	}
	revoked, err := st.RevokeCompatIntegrationKey(ctx, key.ID, later.Add(time.Minute))
	if err != nil || !revoked.Revoked() {
		t.Fatalf("revoke = %+v, %v", revoked, err)
	}
	if again, err := st.RevokeCompatIntegrationKey(ctx, key.ID, later.Add(time.Hour)); err != nil || !again.RevokedAt.Equal(revoked.RevokedAt) {
		t.Fatalf("second revoke = %+v, %v; want the first revocation time kept", again, err)
	}
	if _, err := st.CompatIntegrationKeyByToken(ctx, token, later.Add(2*time.Minute)); !errors.Is(err, ErrCompatKeyInvalid) {
		t.Fatalf("revoked lookup: %v; want ErrCompatKeyInvalid", err)
	}
	if _, err := st.RevokeCompatIntegrationKey(ctx, "missing", later); !errors.As(err, &validation) {
		t.Fatalf("revoke missing: %v; want a validation error", err)
	}
	if keys, err = st.CompatIntegrationKeys(ctx); err != nil || len(keys) != 1 || !keys[0].Revoked() {
		t.Fatalf("list after revoke = %+v, %v; want the revoked row kept", keys, err)
	}
}

func TestCompatIntegrationKeyRefusesIneligibleProfilesAndCaps(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now()
	scopes := []IntegrationScope{IntegrationScopeRead}
	child, err := st.CreateProfile(ctx, "Kid", ProfileManaged, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateCompatIntegrationKey(ctx, child.ID, "Seerr", scopes, now); !errors.Is(err, ErrProfileDisabled) {
		t.Fatalf("managed profile mint: %v; want ErrProfileDisabled", err)
	}
	if _, _, err := st.CreateCompatIntegrationKey(ctx, "profile-missing", "Seerr", scopes, now); err == nil {
		t.Fatal("missing profile mint succeeded")
	}
	if _, err := st.SetProfileIntegrationSignIn(ctx, "profile-default", true); err != nil {
		t.Fatal(err)
	}
	tokens := []string{}
	for i := range MaxActiveCompatKeys {
		_, token, err := st.CreateCompatIntegrationKey(ctx, "profile-default", "tool", scopes, now.Add(time.Duration(i)*time.Millisecond))
		if err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
		tokens = append(tokens, token)
	}
	var validation ValidationError
	if _, _, err := st.CreateCompatIntegrationKey(ctx, "profile-default", "one more", scopes, now); !errors.As(err, &validation) {
		t.Fatalf("mint past the cap: %v; want a validation error", err)
	}
	// Opting out revokes every key in the same transaction; opting back in
	// does not revive them.
	if _, err := st.SetProfileIntegrationSignIn(ctx, "profile-default", false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileIntegrationSignIn(ctx, "profile-default", true); err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if _, err := st.CompatIntegrationKeyByToken(ctx, token, now); !errors.Is(err, ErrCompatKeyInvalid) {
			t.Fatalf("key after opt-out: %v; want ErrCompatKeyInvalid", err)
		}
	}
	if _, _, err := st.CreateCompatIntegrationKey(ctx, "profile-default", "fresh", scopes, now); err != nil {
		t.Fatalf("mint after opt-out freed the cap: %v", err)
	}
}
