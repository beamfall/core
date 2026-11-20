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
	"sync/atomic"
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
	ipKey := svc.integrationIPAttemptKey(integrationTestIP)
	spoofedNameKey := svc.integrationAttemptKey("ip:"+integrationTestIP, store.Profile{}, profileErr, false)
	if strings.TrimPrefix(spoofedNameKey, "unknown:") == strings.TrimPrefix(ipKey, "ip:") {
		t.Fatal("unknown name and IP share the same digest")
	}
	if other.integrationIPAttemptKey(integrationTestIP) == ipKey {
		t.Fatal("IP digest did not change with a different root")
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
	st := storetest.Open(t)
	enableIntegrationSignIn(t, st, "profile-default")
	now := time.Now()
	svc := NewServiceWithClock(st, func() time.Time { return now })
	previousWait := adminKDFSlotWait
	adminKDFSlotWait = 30 * time.Second
	t.Cleanup(func() { adminKDFSlotWait = previousWait })
	var real, decoy atomic.Int64
	ctx := store.WithCredentialDerivationObserver(t.Context(), func(got store.CredentialDerivation) {
		if got.SaltBytes == 0 {
			decoy.Add(1)
		} else {
			real.Add(1)
		}
	})
	const attempts = 12
	var workers sync.WaitGroup
	for i := range attempts {
		workers.Go(func() {
			clientIP := fmt.Sprintf("192.0.2.%d", i+1)
			if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "wrong", clientIP, compatScopes); err != ErrIntegrationAuthentication {
				t.Errorf("parallel authentication returned %v", err)
			}
		})
	}
	workers.Wait()
	if free := int64(integrationBackoff.FreeAttempts); real.Load() != free || decoy.Load() != attempts-free {
		t.Fatalf("real and decoy checks = %d and %d, want %d and %d", real.Load(), decoy.Load(), free, attempts-free)
	}
}

func TestIntegrationKDFBusyRefusalPreservesBothBudgets(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	enableIntegrationSignIn(t, st, "profile-default")
	now := time.Now().UTC()
	svc := NewServiceWithClock(st, func() time.Time { return now })
	keys := []string{"profile:profile-default", svc.integrationIPAttemptKey(integrationTestIP)}
	for range integrationBackoff.FreeAttempts - 1 {
		if _, locked, err := st.ReserveAuthAttempt(ctx, integrationAttemptAction, keys, now, integrationBackoff); err != nil || !locked.IsZero() {
			t.Fatalf("reserve near-lockout fixture: locked until %v, error %v", locked, err)
		}
	}
	func() {
		previousWait := adminKDFSlotWait
		adminKDFSlotWait = time.Millisecond
		for range adminKDFSlotCount {
			adminKDFSlots <- struct{}{}
		}
		defer func() {
			for range adminKDFSlotCount {
				<-adminKDFSlots
			}
			adminKDFSlotWait = previousWait
		}()
		busyCtx := store.WithCredentialDerivationObserver(ctx, func(store.CredentialDerivation) {
			t.Error("busy request reached Argon2id")
		})
		if session, err := svc.AuthenticateIntegrationByName(busyCtx, "Default", "2468", integrationTestIP, compatScopes); session.Token != "" || err != ErrIntegrationAuthentication {
			t.Fatalf("busy request returned a token (%t) or wrong error (%v)", session.Token != "", err)
		}
	}()
	for _, key := range keys {
		if locked, err := st.AuthBackoffUntil(ctx, integrationAttemptAction, key, now, integrationBackoff); err != nil || !locked.IsZero() {
			t.Fatalf("lane after busy refusal: locked until %v, error %v", locked, err)
		}
	}
	if _, err := svc.AuthenticateIntegrationByName(ctx, "Default", "2468", integrationTestIP, compatScopes); err != nil {
		t.Fatalf("correct credential after a slot freed: %v", err)
	}
}

func TestIntegrationSettlementFailureRefusesAndLeavesBothLanesCharged(t *testing.T) {
	ctx := t.Context()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	enableIntegrationSignIn(t, st, "profile-default")
	now := time.Now().UTC()
	svc := NewServiceWithClock(st, func() time.Time { return now })
	keys := []string{"profile:profile-default", svc.integrationIPAttemptKey(integrationTestIP)}
	for range integrationBackoff.FreeAttempts - 1 {
		if _, locked, err := st.ReserveAuthAttempt(ctx, integrationAttemptAction, keys, now, integrationBackoff); err != nil || !locked.IsZero() {
			t.Fatalf("reserve near-lockout fixture: locked until %v, error %v", locked, err)
		}
	}
	storetest.RejectIntegrationIPSettlement(t, dataDir)
	verified := false
	requestCtx := store.WithCredentialDerivationObserver(ctx, func(got store.CredentialDerivation) { verified = got.SaltBytes != 0 })
	session, err := svc.AuthenticateIntegrationByName(requestCtx, "Default", "2468", integrationTestIP, compatScopes)
	if session != (IntegrationSession{}) || err != ErrIntegrationAuthentication || !verified {
		t.Fatalf("settlement failure returned a session (%t), error %v, real verification %t", session != (IntegrationSession{}), err, verified)
	}
	for _, key := range keys {
		if locked, err := st.AuthBackoffUntil(ctx, integrationAttemptAction, key, now, integrationBackoff); err != nil || !locked.After(now) {
			t.Fatalf("lane after settlement failure: locked until %v, error %v", locked, err)
		}
	}
}

func TestIntegrationDerivesArgon2idForRealAndDecoyChecks(t *testing.T) {
	ctx := t.Context()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	enableIntegrationSignIn(t, st, "profile-default")
	now := time.Now().UTC()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "setup-secret", now); err != nil {
		t.Fatal(err)
	}
	svc := NewServiceWithClock(st, func() time.Time { return now })
	const lockedIP = "192.0.2.2"
	for range integrationBackoff.FreeAttempts {
		if _, locked, err := st.ReserveAuthAttempt(ctx, integrationAttemptAction, []string{svc.integrationIPAttemptKey(lockedIP)}, now, integrationBackoff); err != nil || !locked.IsZero() {
			t.Fatalf("reserve lockout fixture: locked until %v, error %v", locked, err)
		}
	}
	for _, tc := range []struct {
		path, name, credential, ip string
		saltBytes                  int
		reason                     RefusalReason
	}{
		{"entry PIN", "Default", "2468", integrationTestIP, 16, ""},
		{"admin password", "admin", "setup-secret", integrationTestIP, 16, ""},
		{"unknown name", "unknown profile", "wrong", integrationTestIP, 0, RefusalIntegrationRejected},
		{"locked", "Default", "2468", lockedIP, 0, RefusalIntegrationLocked},
		{"malformed verifier", "Default", "2468", integrationTestIP, 0, RefusalIntegrationRejected},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if tc.path == "malformed verifier" {
				storetest.CorruptProfileEntryPIN(t, dataDir, "profile-default")
			}
			var derivations []store.CredentialDerivation
			requestCtx := store.WithCredentialDerivationObserver(WithRefusalNote(ctx), func(got store.CredentialDerivation) {
				derivations = append(derivations, got)
			})
			_, err := svc.AuthenticateIntegrationByName(requestCtx, tc.name, tc.credential, tc.ip, compatScopes)
			note, _ := RefusalFromContext(requestCtx)
			var wantErr error
			if tc.reason != "" {
				wantErr = ErrIntegrationAuthentication
			}
			if err != wantErr || note.Reason != tc.reason {
				t.Errorf("returned %v (%q), want %v (%q)", err, note.Reason, wantErr, tc.reason)
			}
			want := store.CredentialDerivation{SaltBytes: tc.saltBytes, Passes: 1, MemoryKiB: 64 * 1024, Lanes: 4, KeyBytes: 32}
			if len(derivations) != 1 || derivations[0] != want {
				t.Errorf("derivations = %+v, want one %+v", derivations, want)
			}
		})
	}
}
