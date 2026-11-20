package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const integrationTestIP = "192.0.2.1"

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

	if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", integrationTestIP, compatScopes); !errors.Is(err, ErrIntegrationAuthentication) {
		t.Fatalf("opt-out authentication = %v", err)
	}
	enableIntegrationSignIn(t, st, "profile-default")
	session, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", integrationTestIP, compatScopes)
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
	if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", integrationTestIP, []store.IntegrationScope{"compat.admin"}); !errors.Is(err, ErrIntegrationAuthentication) {
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
	session, err := svc.AuthenticateIntegrationByName(ctx, "admin", "setup-secret", integrationTestIP, compatScopes)
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
		_, err := svc.AuthenticateIntegrationByName(ctx, tc.name, tc.credential, integrationTestIP, compatScopes)
		if !errors.Is(err, ErrIntegrationAuthentication) {
			t.Fatalf("%q error = %v", tc.name, err)
		}
		if elapsed := time.Since(started); elapsed < integrationFailureFloor {
			t.Fatalf("%q failure elapsed %s, want common floor %s", tc.name, elapsed, integrationFailureFloor)
		}
	}
	for range integrationBackoff.FreeAttempts {
		_, _ = svc.AuthenticateIntegrationByName(ctx, "Default", "wrong", integrationTestIP, compatScopes)
	}
	started := time.Now()
	if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", integrationTestIP, compatScopes); !errors.Is(err, ErrIntegrationAuthentication) {
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

func TestIntegrationReservesBeforeConcurrentCredentialVerification(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	enableIntegrationSignIn(t, st, "profile-default")
	now := time.Now()
	svc := NewServiceWithClock(st, func() time.Time { return now })
	const attempts = 12
	verification := make(chan store.ProfileID, attempts)
	release := make(chan struct{})
	results := make(chan error, attempts)
	svc.integrationVerifier = func(_ context.Context, id store.ProfileID, _ string, _ bool) error {
		verification <- id
		<-release
		return ErrIntegrationAuthentication
	}
	svc.integrationDecoy = func(context.Context, string) {
		verification <- ""
		<-release
	}
	var workers sync.WaitGroup
	defer workers.Wait()
	for i := range attempts {
		workers.Go(func() {
			clientIP := fmt.Sprintf("192.0.2.%d", i+1)
			_, err := svc.AuthenticateIntegrationByName(ctx, "Default", "wrong", clientIP, compatScopes)
			results <- err
		})
	}
	func() {
		defer close(release)
		deadline := time.After(5 * time.Second)
		realVerifications := 0
		for range attempts {
			select {
			case id := <-verification:
				if id == "profile-default" {
					realVerifications++
				}
			case err := <-results:
				t.Fatalf("authentication returned before reaching the real or decoy verifier: %v", err)
			case <-deadline:
				t.Fatal("parallel authentication did not reach the verifier")
			}
		}
		if realVerifications != integrationBackoff.FreeAttempts {
			t.Fatalf("real verifications = %d, want %d", realVerifications, integrationBackoff.FreeAttempts)
		}
	}()
	for range attempts {
		if err := <-results; !errors.Is(err, ErrIntegrationAuthentication) {
			t.Fatalf("parallel authentication error = %v", err)
		}
	}
}

func TestIntegrationRefusalsPaySlowVerifierCost(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	enableIntegrationSignIn(t, st, "profile-default")
	now := time.Now().UTC()
	svc := NewServiceWithClock(st, func() time.Time { return now })
	keys := []string{"profile:profile-default"}
	for range integrationBackoff.FreeAttempts {
		if locked, err := st.ReserveAuthAttempt(ctx, integrationAttemptAction, keys, now, integrationBackoff); err != nil || !locked.IsZero() {
			t.Fatalf("reserve name lockout fixture: locked until %v, error %v", locked, err)
		}
	}

	const verifyCost = 200 * time.Millisecond
	decoyChecked := false
	svc.integrationDecoy = func(context.Context, string) {
		decoyChecked = true
		time.Sleep(verifyCost)
	}
	for _, tc := range []struct {
		name   string
		reason RefusalReason
	}{
		{"unknown profile", RefusalIntegrationRejected},
		{"Default", RefusalIntegrationLocked},
	} {
		decoyChecked = false
		requestCtx := WithRefusalNote(ctx)
		started := time.Now()
		_, err := svc.AuthenticateIntegrationByName(requestCtx, tc.name, "wrong", integrationTestIP, compatScopes)
		elapsed := time.Since(started)
		note, _ := RefusalFromContext(requestCtx)
		if !errors.Is(err, ErrIntegrationAuthentication) || note.Reason != tc.reason {
			t.Fatalf("%q refusal = %v (%q), want %q", tc.name, err, note.Reason, tc.reason)
		}
		if !decoyChecked || elapsed < verifyCost {
			t.Fatalf("%q decoy called = %t, elapsed %s, want at least %s", tc.name, decoyChecked, elapsed, verifyCost)
		}
	}
}
