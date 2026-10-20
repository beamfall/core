package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

var compatScopes = []store.IntegrationScope{store.IntegrationScopeRead, store.IntegrationScopeRefresh}

func enableIntegrationSignIn(t *testing.T, st *store.Store, id store.ProfileID) {
	t.Helper()
	enabled := true
	if _, err := st.UpdateProfileSettings(context.Background(), id, store.ProfileSettings{IntegrationSignIn: &enabled}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationAuthenticationOptInScopeRevocationAndReveal(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	svc := NewServiceWithClock(st, func() time.Time { return now })

	if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", compatScopes); !errors.Is(err, ErrIntegrationAuthentication) {
		t.Fatalf("opt-out authentication = %v", err)
	}
	enableIntegrationSignIn(t, st, "profile-default")
	session, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", compatScopes)
	if err != nil {
		t.Fatalf("AuthenticateIntegrationByName: %v", err)
	}
	if session.ExpiresAt != now.Add(integrationSessionTTL) || session.Setup {
		t.Fatalf("session = %+v", session)
	}
	if _, err := svc.Scope(ctx, store.SessionID(session.Token)); err == nil {
		t.Fatal("ordinary auth accepted an integration Session")
	}
	scope, err := svc.IntegrationScope(ctx, session.Token, store.IntegrationScopeRead)
	if err != nil || scope.SessionKind != store.SessionKindIntegration {
		t.Fatalf("IntegrationScope = %+v, %v", scope, err)
	}
	if err := st.SetSessionRevealed(ctx, scope.SessionID, true); err != nil {
		t.Fatal(err)
	}
	scope, err = svc.IntegrationScope(ctx, session.Token, store.IntegrationScopeRead)
	if err != nil || !scope.Revealed {
		t.Fatalf("Reveal parity scope = %+v, %v", scope, err)
	}
	if err := svc.RevokeIntegrationSession(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IntegrationScope(ctx, session.Token, store.IntegrationScopeRead); err == nil {
		t.Fatal("revoked integration Session authorized")
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{AllLibraries: true, Revealed: true}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	revokedAudit := false
	for _, entry := range entries {
		revokedAudit = revokedAudit || (entry.Action == "integration.revoke" && entry.ProfileID == "profile-default")
	}
	if !revokedAudit {
		t.Fatal("integration revocation was not audited per Profile")
	}
	if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", []store.IntegrationScope{"compat.admin"}); !errors.Is(err, ErrIntegrationAuthentication) {
		t.Fatalf("outside-ceiling scope = %v", err)
	}
}

func TestIntegrationAdminSetupTTLAndNonRenewability(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "setup-secret", now); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithClock(st, func() time.Time { return now })
	session, err := svc.AuthenticateIntegrationByName(ctx, "admin", "setup-secret", compatScopes)
	if err != nil {
		t.Fatal(err)
	}
	if !session.Setup || session.ExpiresAt != now.Add(integrationSetupTTL) {
		t.Fatalf("setup session = %+v", session)
	}
	now = session.ExpiresAt
	if _, err := svc.IntegrationScope(ctx, session.Token, store.IntegrationScopeRead); err == nil {
		t.Fatal("expired setup Session authorized")
	}
}

func TestIntegrationFailuresAreUniformLockedAndRedacted(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Now().UTC()
	svc := NewServiceWithClock(st, func() time.Time { return now })
	root := []byte("server-secret")
	unknownName := "missing-profile-sensitive-name"
	svc.SetIntegrationAttemptSecret(root)
	enableIntegrationSignIn(t, st, "profile-default")
	profileErr := errors.New("profile not found")
	pseudonym := strings.TrimPrefix(svc.integrationAttemptKey(unknownName, store.Profile{}, profileErr, false), "unknown:")
	if repeat := strings.TrimPrefix(svc.integrationAttemptKey(unknownName, store.Profile{}, profileErr, false), "unknown:"); repeat != pseudonym {
		t.Fatalf("pseudonym changed for the same root and name: %q != %q", repeat, pseudonym)
	}
	rawMAC := hmac.New(sha256.New, root)
	_, _ = rawMAC.Write([]byte(strings.ToLower(unknownName)))
	if raw := hex.EncodeToString(rawMAC.Sum(nil)); pseudonym == raw {
		t.Fatal("pseudonym was computed directly under the Stream Token root")
	}
	other := NewServiceWithClock(st, func() time.Time { return now })
	other.SetIntegrationAttemptSecret([]byte("different-server-secret"))
	if different := strings.TrimPrefix(other.integrationAttemptKey(unknownName, store.Profile{}, profileErr, false), "unknown:"); different == pseudonym {
		t.Fatal("pseudonym did not change with a different root")
	}

	cases := []struct{ name, credential string }{
		{unknownName, "2468"},
		{"Default", "wrong"},
		{"Managed", "anything"},
	}
	for _, tc := range cases {
		started := time.Now()
		_, err := svc.AuthenticateIntegrationByName(ctx, tc.name, tc.credential, compatScopes)
		if !errors.Is(err, ErrIntegrationAuthentication) {
			t.Fatalf("%q error = %v", tc.name, err)
		}
		if elapsed := time.Since(started); elapsed < integrationFailureFloor {
			t.Fatalf("%q failure elapsed %s, want common floor %s", tc.name, elapsed, integrationFailureFloor)
		}
	}
	for range integrationBackoff.FreeAttempts {
		_, _ = svc.AuthenticateIntegrationByName(ctx, "Default", "wrong", compatScopes)
	}
	started := time.Now()
	if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", compatScopes); !errors.Is(err, ErrIntegrationAuthentication) {
		t.Fatalf("locked correct credential = %v", err)
	}
	if time.Since(started) < integrationFailureFloor {
		t.Fatal("locked refusal escaped the uniform timing floor")
	}

	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{AllLibraries: true, Revealed: true}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	unknownTarget := ""
	knownFound := false
	for _, entry := range entries {
		if strings.Contains(entry.TargetID, unknownName) {
			t.Fatal("unknown attempted name persisted in audit")
		}
		if entry.TargetType == "security-target" && len(entry.TargetID) == 64 {
			unknownTarget = entry.TargetID
		}
		if entry.ProfileID == "profile-default" && entry.Action == "integration.login.denied" {
			knownFound = true
		}
	}
	if unknownTarget != pseudonym || !knownFound {
		t.Fatalf("audit entries missing redacted unknown or per-Profile known record: %+v", entries)
	}
}
