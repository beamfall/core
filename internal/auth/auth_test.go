package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestRequestScopeRequiresSessionHeader(t *testing.T) {
	service := NewService(storetest.Open(t))
	req := httptest.NewRequest("GET", "/", nil)

	if _, err := service.ScopeFromRequest(context.Background(), req); err == nil {
		t.Fatal("ScopeFromRequest returned nil error")
	}
}

func TestRevealedAdultScopeCanReveal(t *testing.T) {
	service := NewService(storetest.Open(t))
	req := authenticatedRequest("session-revealed", "device-tv", "dev-tv-secret")

	scope, err := service.ScopeFromRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ScopeFromRequest returned error: %v", err)
	}
	if !scope.Revealed {
		t.Fatal("revealed adult scope is hidden")
	}
}

func TestRequestScopeRejectsBareSessionHeader(t *testing.T) {
	service := NewService(storetest.Open(t))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Beamfall-Session", "session-default")

	if _, err := service.ScopeFromRequest(context.Background(), req); err == nil {
		t.Fatal("ScopeFromRequest accepted bare session header")
	}
}

func TestUnrevealedAdultScopeIsHidden(t *testing.T) {
	service := NewService(storetest.Open(t))

	scope, err := service.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatalf("Scope returned error: %v", err)
	}
	if scope.Revealed {
		t.Fatal("unrevealed adult scope is revealed")
	}
}

func TestManagedScopeCannotReveal(t *testing.T) {
	service := NewService(storetest.Open(t))

	scope, err := service.Scope(context.Background(), "session-managed")
	if err != nil {
		t.Fatalf("Scope returned error: %v", err)
	}
	if scope.Revealed {
		t.Fatal("managed scope is revealed")
	}
	if got, want := scope.LibraryIDs, []store.LibraryID{"library-local"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("LibraryIDs = %v, want %v", got, want)
	}
}

func TestRevokedDeviceScopeIsRejected(t *testing.T) {
	service := NewService(storetest.Open(t))

	if _, err := service.Scope(context.Background(), "session-revoked"); err == nil {
		t.Fatal("Scope accepted revoked device")
	}
}

func TestDisabledProfileInvalidatesEnrolledSessionScope(t *testing.T) {
	st := storetest.Open(t)
	disabled := true
	if _, err := st.UpdateProfileSettings(context.Background(), "profile-managed", store.ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	service := NewService(st)
	req := authenticatedRequest("session-managed", "device-tv", "dev-tv-secret")

	if _, err := service.ScopeFromRequest(context.Background(), req); !errors.Is(err, store.ErrProfileDisabled) {
		t.Fatalf("ScopeFromRequest error = %v, want ErrProfileDisabled", err)
	}
}

func TestDisabledProfileInvalidatesBoundScopedKey(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	_, key, err := st.CreateScopedAPIKey(ctx, "managed-reader", []store.APIScope{store.ScopeReadCatalog}, "profile-managed", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if _, err := st.UpdateProfileSettings(ctx, "profile-managed", store.ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	if _, err := NewService(st).ScopeFromScopedKey(ctx, key); !errors.Is(err, store.ErrProfileDisabled) {
		t.Fatalf("ScopeFromScopedKey error = %v, want ErrProfileDisabled", err)
	}
}

func TestManagerScopeCarriesManagerRole(t *testing.T) {
	service := NewService(storetest.Open(t))

	scope, err := service.Scope(context.Background(), "session-manager")
	if err != nil {
		t.Fatalf("Scope returned error: %v", err)
	}
	if scope.DeviceRole != store.DeviceRoleManager {
		t.Fatalf("DeviceRole = %q, want manager", scope.DeviceRole)
	}
	if scope.DistributionClass != store.DeviceDistributionDirect {
		t.Fatalf("DistributionClass = %q, want direct", scope.DistributionClass)
	}
}

func TestRequestScopeAcceptsMatchingDeviceCredential(t *testing.T) {
	service := NewService(storetest.Open(t))
	req := authenticatedRequest("session-default", "device-tv", "dev-tv-secret")

	scope, err := service.ScopeFromRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("ScopeFromRequest returned error: %v", err)
	}
	if scope.DeviceID != "device-tv" {
		t.Fatalf("DeviceID = %q, want device-tv", scope.DeviceID)
	}
}

func TestRequestScopeRejectsDeviceCredentialMismatch(t *testing.T) {
	service := NewService(storetest.Open(t))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")

	if _, err := service.ScopeFromRequest(context.Background(), req); err == nil {
		t.Fatal("ScopeFromRequest accepted credential for a different device")
	}
}

func TestRequestScopeRejectsWrongDeviceSecret(t *testing.T) {
	service := NewService(storetest.Open(t))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "wrong-secret")

	if _, err := service.ScopeFromRequest(context.Background(), req); err == nil {
		t.Fatal("ScopeFromRequest accepted wrong device secret")
	}
}

func TestRequestScopeRejectsPartialDeviceCredential(t *testing.T) {
	service := NewService(storetest.Open(t))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")

	if _, err := service.ScopeFromRequest(context.Background(), req); err == nil {
		t.Fatal("ScopeFromRequest accepted partial device credential")
	}
}

// bearerRequest builds a request carrying a Bearer credential, the shape a
// scoped developer key or admin API token authenticates
// with.
func bearerRequest(token string) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// TestScopedKeyResolvesToScopeWithItsScopes proves the Bearer bfk_ path:
// a scoped developer key authenticates via VerifyScopedAPIKey
// and resolves to a Scope that carries the key's closed-vocabulary scope set,
// which per-route enforcement reads via HasAPIScope. An ingest-only key carries
// ingest and not read-catalog — the seam a route uses to let an ingest key
// trigger a scan but refuse a catalog read.
func TestScopedKeyResolvesToScopeWithItsScopes(t *testing.T) {
	st := storetest.Open(t)
	service := NewService(st)
	ctx := context.Background()
	_, key, err := st.CreateScopedAPIKey(ctx, "ingest-bot", []store.APIScope{store.ScopeIngest}, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateScopedAPIKey: %v", err)
	}

	scope, err := service.ScopeFromRequest(ctx, bearerRequest(key))
	if err != nil {
		t.Fatalf("ScopeFromRequest returned error: %v", err)
	}
	if !scope.IsScopedKey() {
		t.Fatal("scope is not recognized as a scoped key")
	}
	if !scope.HasAPIScope(store.ScopeIngest) {
		t.Fatal("ingest-only key lacks the ingest scope")
	}
	if scope.HasAPIScope(store.ScopeReadCatalog) {
		t.Fatal("ingest-only key must NOT carry read-catalog")
	}
	// A session or admin token carries no API scopes, so an API-scope gate
	// rejects them — the scope vocabulary is the developer-key surface only.
	sessionScope, err := service.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	if sessionScope.IsScopedKey() || sessionScope.HasAPIScope(store.ScopeIngest) {
		t.Fatal("a session scope must not be treated as a scoped key")
	}
}

// TestScopedKeyRejectsInvalidAndRevoked proves a malformed, unknown, or revoked
// key never resolves a Scope (ADR-0040 §2.2 / VerifyScopedAPIKey's no-oracle
// contract): every failure path returns an error, not a partial principal.
func TestScopedKeyRejectsInvalidAndRevoked(t *testing.T) {
	st := storetest.Open(t)
	service := NewService(st)
	ctx := context.Background()

	if _, err := service.ScopeFromRequest(ctx, bearerRequest("bfk_not_a_real_key")); err == nil {
		t.Fatal("ScopeFromRequest accepted a malformed scoped key")
	}
	created, key, err := st.CreateScopedAPIKey(ctx, "soon-revoked", []store.APIScope{store.ScopeReadCatalog}, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateScopedAPIKey: %v", err)
	}
	if _, err := service.ScopeFromRequest(ctx, bearerRequest(key)); err != nil {
		t.Fatalf("live key rejected: %v", err)
	}
	if err := st.RevokeScopedAPIKey(ctx, created.ID); err != nil {
		t.Fatalf("RevokeScopedAPIKey: %v", err)
	}
	if _, err := service.ScopeFromRequest(ctx, bearerRequest(key)); err == nil {
		t.Fatal("ScopeFromRequest accepted a revoked scoped key")
	}
}

// TestProfileBoundScopedKeyInheritsProfileVisibility proves a Profile-bound key
// inherits exactly that Profile's Library grants — so the
// Reveal contract filters its reads (and thus its session list) to its own
// Profile, never the broader admin view. profile-managed is granted only
// library-local; an admin-scoped key (no binding) carries no Library
// restriction at all. A scoped key is never a standing Reveal.
func TestProfileBoundScopedKeyInheritsProfileVisibility(t *testing.T) {
	st := storetest.Open(t)
	service := NewService(st)
	ctx := context.Background()

	_, boundKey, err := st.CreateScopedAPIKey(ctx, "dash", []store.APIScope{store.ScopeReadSessions}, "profile-managed", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateScopedAPIKey (bound): %v", err)
	}
	bound, err := service.ScopeFromRequest(ctx, bearerRequest(boundKey))
	if err != nil {
		t.Fatalf("ScopeFromRequest (bound) returned error: %v", err)
	}
	if bound.ProfileID != "profile-managed" {
		t.Fatalf("bound key ProfileID = %q, want profile-managed", bound.ProfileID)
	}
	if got, want := bound.LibraryIDs, []store.LibraryID{"library-local"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("bound key LibraryIDs = %v, want %v (only its Profile's grant)", got, want)
	}
	if bound.Revealed {
		t.Fatal("a Profile-bound scoped key must never be a standing Reveal")
	}
	// The binding travels into the store read policy: the key's session list is
	// filtered by exactly its Profile's libraries (library-archive is excluded).
	filter := bound.LibraryFilter()
	if len(filter.AllowedIDs) != 1 || filter.AllowedIDs[0] != "library-local" {
		t.Fatalf("bound key LibraryFilter.AllowedIDs = %v, want [library-local]", filter.AllowedIDs)
	}
	// A Profile-bound key is NOT the all-Libraries sentinel (H4): its visibility
	// is exactly its grant, so a zero-grant Profile fails closed.
	if bound.AllLibraries || filter.AllLibraries {
		t.Fatal("a Profile-bound scoped key must not carry the AllLibraries sentinel")
	}

	_, adminKey, err := st.CreateScopedAPIKey(ctx, "admin-dash", []store.APIScope{store.ScopeReadSessions}, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateScopedAPIKey (admin): %v", err)
	}
	admin, err := service.ScopeFromRequest(ctx, bearerRequest(adminKey))
	if err != nil {
		t.Fatalf("ScopeFromRequest (admin) returned error: %v", err)
	}
	if admin.ProfileID != "" {
		t.Fatalf("admin-scoped key carries a Profile binding: %q", admin.ProfileID)
	}
	if len(admin.LibraryIDs) != 0 {
		t.Fatalf("admin-scoped key carries Library restriction: %v", admin.LibraryIDs)
	}
	if admin.DeviceRole != store.DeviceRoleManager {
		t.Fatalf("admin-scoped key DeviceRole = %q, want manager-class", admin.DeviceRole)
	}
	// H4: an admin-scoped key's empty LibraryIDs means "every Library" only
	// because it carries the explicit sentinel — without it the empty grant
	// would fail closed and the admin would see nothing.
	if !admin.AllLibraries {
		t.Fatal("admin-scoped key must carry the AllLibraries sentinel")
	}
	if !admin.LibraryFilter().AllLibraries {
		t.Fatal("admin-scoped key LibraryFilter must carry the AllLibraries sentinel")
	}
}

// TestProfileBoundScopedKeyEnforcesManagedSchedule pins CORE-AUDIT-1: a
// Profile-bound scoped key shares managed schedule enforcement with sessions,
// but has no session-scoped extension grant to lift the block.
func TestProfileBoundScopedKeyEnforcesManagedSchedule(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()

	at := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local) // Monday 10:00 local
	service := NewServiceWithClock(st, func() time.Time { return at })
	atMinute := at.Hour()*60 + at.Minute()
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{
		{Weekday: int(at.Weekday()), StartMinute: 0, EndMinute: atMinute},
	}); err != nil {
		t.Fatal(err)
	}
	req, err := st.CreateExtensionRequest(ctx, store.ExtensionRequestInput{
		ProfileID:           "profile-managed",
		RequestingSessionID: "session-managed",
		TriggerSource:       "schedule",
		Now:                 at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", store.ExtensionApproved, 30, at); err != nil {
		t.Fatal(err)
	}
	_, key, err := st.CreateScopedAPIKey(ctx, "managed-dash", []store.APIScope{store.ScopeReadSessions}, "profile-managed", at)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey: %v", err)
	}
	if _, err := service.ScopeFromRequest(ctx, bearerRequest(key)); err != ErrAccessOutsideSchedule {
		t.Fatalf("ScopeFromRequest err = %v, want ErrAccessOutsideSchedule", err)
	}
}

// TestOpsAPITokenScopeIsAllLibraries pins the H4 contract for the ops-class
// admin API token (auth.ScopeFromAPIToken) — the path with zero prior test
// coverage. Its empty LibraryIDs is the hidden-world "every Library" grant, so
// it MUST resolve the AllLibraries sentinel; without it the token would fail
// closed and every ops read would silently return nothing. It is still never a
// standing Reveal (the Reveal contract is per-session, PIN-gated).
func TestOpsAPITokenScopeIsAllLibraries(t *testing.T) {
	st := storetest.Open(t)
	service := NewService(st)
	ctx := context.Background()

	_, token, err := st.CreateAPIToken(ctx, "ops", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	scope, err := service.ScopeFromRequest(ctx, bearerRequest(token))
	if err != nil {
		t.Fatalf("ScopeFromRequest (api token) returned error: %v", err)
	}
	if scope.ProfileID != "" {
		t.Fatalf("ops API token carries a Profile binding: %q", scope.ProfileID)
	}
	if len(scope.LibraryIDs) != 0 {
		t.Fatalf("ops API token carries Library restriction: %v", scope.LibraryIDs)
	}
	if !scope.AllLibraries || !scope.LibraryFilter().AllLibraries {
		t.Fatal("ops API token must carry the AllLibraries sentinel (hidden-world, every Library)")
	}
	if scope.Revealed || scope.LibraryFilter().Revealed {
		t.Fatal("a standing ops API token must never be a standing Reveal")
	}
}

func authenticatedRequest(sessionID, deviceID, deviceSecret string) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Beamfall-Session", sessionID)
	req.Header.Set("X-Beamfall-Device", deviceID)
	req.Header.Set("X-Beamfall-Device-Secret", deviceSecret)
	return req
}

func TestScopeRejectsExpiredSession(t *testing.T) {
	// feature:session-lifecycle
	st := storetest.Open(t)
	err := st.EnrollDevice(
		context.Background(),
		store.Device{ID: "device-expired", Name: "Expired", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("expired-secret")},
		store.Session{
			ID:        "session-expired",
			DeviceID:  "device-expired",
			ProfileID: "profile-default",
			ExpiresAt: time.Unix(100, 0).UTC(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return time.Unix(101, 0).UTC() })

	if _, err := service.Scope(context.Background(), "session-expired"); err == nil {
		t.Fatal("Scope accepted expired session")
	}
}

func TestScopeAcceptsUnexpiredSession(t *testing.T) {
	st := storetest.Open(t)
	err := st.EnrollDevice(
		context.Background(),
		store.Device{ID: "device-fresh", Name: "Fresh", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("fresh-secret")},
		store.Session{
			ID:        "session-fresh",
			DeviceID:  "device-fresh",
			ProfileID: "profile-default",
			ExpiresAt: time.Unix(100, 0).UTC(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return time.Unix(99, 0).UTC() })

	if _, err := service.Scope(context.Background(), "session-fresh"); err != nil {
		t.Fatalf("Scope rejected unexpired session: %v", err)
	}
}

func TestScopeRejectsSessionAtExpiryBoundary(t *testing.T) {
	st := storetest.Open(t)
	err := st.EnrollDevice(
		context.Background(),
		store.Device{ID: "device-boundary", Name: "Boundary", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("boundary-secret")},
		store.Session{
			ID:        "session-boundary",
			DeviceID:  "device-boundary",
			ProfileID: "profile-default",
			ExpiresAt: time.Unix(100, 0).UTC(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return time.Unix(100, 0).UTC() })

	if _, err := service.Scope(context.Background(), "session-boundary"); err == nil {
		t.Fatal("Scope accepted session at expiry boundary")
	}
}

func TestScopeResolvesCertificationCeilings(t *testing.T) {
	st := storetest.Open(t)
	service := NewService(st)
	service.SetCertificationScales(map[string][]string{
		"filmtv": {"low", "mid", "high"},
	})
	ctx := context.Background()
	if _, err := st.SetProfileCertificationCeiling(ctx, "profile-default", store.ProfileCertificationCeiling{
		PluginID: "filmtv", Ceiling: "mid", AllowUnrated: true,
	}); err != nil {
		t.Fatal(err)
	}
	// A ceiling against a plugin with no wired scale resolves fail-closed.
	if _, err := st.SetProfileCertificationCeiling(ctx, "profile-default", store.ProfileCertificationCeiling{
		PluginID: "ghost", Ceiling: "anything",
	}); err != nil {
		t.Fatal(err)
	}

	scope, err := service.ScopeFromRequest(ctx, authenticatedRequest("session-default", "device-tv", "dev-tv-secret"))
	if err != nil {
		t.Fatalf("ScopeFromRequest returned error: %v", err)
	}
	if len(scope.Certification) != 2 {
		t.Fatalf("constraints = %#v, want 2", scope.Certification)
	}
	byPlugin := map[string]store.CertificationConstraint{}
	for _, constraint := range scope.Certification {
		byPlugin[constraint.PluginID] = constraint
	}
	filmtv := byPlugin["filmtv"]
	if len(filmtv.Allowed) != 2 || filmtv.Allowed[0] != "low" || filmtv.Allowed[1] != "mid" || !filmtv.AllowUnrated {
		t.Fatalf("filmtv constraint = %#v, want allowed prefix [low mid] + AllowUnrated", filmtv)
	}
	ghost := byPlugin["ghost"]
	if len(ghost.Allowed) != 0 || ghost.AllowUnrated {
		t.Fatalf("ghost constraint = %#v, want fail-closed empty allowed set", ghost)
	}

	// The constraints travel into the store read policy.
	filter := scope.LibraryFilter()
	if len(filter.Certification) != 2 {
		t.Fatalf("LibraryFilter constraints = %#v", filter.Certification)
	}

	// A Profile with no ceilings yields no constraints.
	managed, err := service.ScopeFromRequest(ctx, authenticatedRequest("session-managed", "device-tv", "dev-tv-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if len(managed.Certification) != 0 {
		t.Fatalf("unconstrained profile constraints = %#v", managed.Certification)
	}
}

// TestBeginAdminSessionLocksOutAfterRepeatedFailures asserts the admin re-auth
// throttle: after the free-attempt budget of failed password
// attempts the session is locked (ErrAdminLocked), and the lane resets once the
// attempt window has elapsed. feature:admin-session
func TestBeginAdminSessionLocksOutAfterRepeatedFailures(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetAdminPassword(context.Background(), "right-password", now); err != nil {
		t.Fatal(err)
	}
	clock := now
	service := NewServiceWithClock(st, func() time.Time { return clock })
	scope, err := service.ScopeFromRequest(context.Background(),
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}

	// The free-attempt budget of wrong guesses, each rejected with
	// ErrAdminPassword and not yet locked out.
	for i := 0; i < adminBackoff.FreeAttempts; i++ {
		if _, err := service.BeginAdminSession(context.Background(), scope, "wrong"); err != ErrAdminPassword {
			t.Fatalf("attempt %d err = %v, want ErrAdminPassword", i, err)
		}
	}
	// The next attempt — even with the CORRECT password — is locked out by the
	// exponential backoff that arms past the free-attempt budget.
	if _, err := service.BeginAdminSession(context.Background(), scope, "right-password"); err != ErrAdminLocked {
		t.Fatalf("post-budget err = %v, want ErrAdminLocked", err)
	}

	// After the attempt window elapses, the lane resets and the correct password
	// works again.
	clock = now.Add(adminAttemptWindow + time.Minute)
	until, err := service.BeginAdminSession(context.Background(), scope, "right-password")
	if err != nil {
		t.Fatalf("post-window err = %v, want success", err)
	}
	if !until.After(clock) {
		t.Fatalf("admin window end %v not after now %v", until, clock)
	}

	// The opened window is reflected in the resolved scope.
	adminScope, err := service.ScopeFromRequest(context.Background(),
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if !adminScope.Admin {
		t.Fatal("scope.Admin false after successful admin re-auth")
	}

	// Once the admin window passes, the scope is no longer admin.
	clock = until.Add(time.Second)
	lapsed, err := service.ScopeFromRequest(context.Background(),
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if lapsed.Admin {
		t.Fatal("scope.Admin still true after admin window lapsed")
	}
}

// TestBeginAdminSessionRequiresOwnerProfile pins ADR-0007 §2/§4: once an owner
// Profile is designated, only that Profile may open an Admin Session; a server
// with no owner designated falls back to admitting any adult Profile, so a
// migration can never brick Admin access. A non-owner adult is rejected with
// the same ErrAdminPassword as a bad password (no owner-identity oracle).
// feature:admin-session
func TestBeginAdminSessionRequiresOwnerProfile(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(2_000_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	clock := now
	service := NewServiceWithClock(st, func() time.Time { return clock })
	scope, err := service.ScopeFromRequest(ctx,
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}

	// Lockout-safety fallback: no owner designated → any adult Profile may
	// open the window (the pre-0069 behaviour).
	if _, err := service.BeginAdminSession(ctx, scope, "right-password"); err != nil {
		t.Fatalf("no-owner fallback err = %v, want success", err)
	}

	// Designate the caller's own Profile as owner → it still works.
	if err := st.SetProfileOwner(ctx, scope.ProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginAdminSession(ctx, scope, "right-password"); err != nil {
		t.Fatalf("owner admin err = %v, want success", err)
	}

	// Move ownership to a different adult Profile → the caller is now denied.
	other, err := st.CreateProfile(ctx, "Other Owner", store.ProfileAdult,
		store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginAdminSession(ctx, scope, "right-password"); err != ErrAdminPassword {
		t.Fatalf("non-owner admin err = %v, want ErrAdminPassword", err)
	}
}

// TestProfileSwitchInvalidatesAdminSession pins ADR-0085's Profile-switch
// invalidation: the Admin Session window is scoped to the selected owner-adult
// Profile and must not survive rebinding the same browser session.
// feature:admin-session
func TestProfileSwitchInvalidatesAdminSession(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(2_500_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope, err := service.ScopeFromRequest(ctx,
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginAdminSession(ctx, scope, "right-password"); err != nil {
		t.Fatalf("BeginAdminSession err = %v, want success", err)
	}
	adminScope, err := service.ScopeFromRequest(ctx,
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if !adminScope.Admin || adminScope.AdminUntil.IsZero() {
		t.Fatalf("admin scope = %#v, want active Admin Session with expiry", adminScope)
	}

	if err := st.SetSessionProfile(ctx, scope.SessionID, "profile-managed"); err != nil {
		t.Fatal(err)
	}
	switched, err := service.ScopeFromRequest(ctx,
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if switched.Admin || !switched.AdminUntil.IsZero() {
		t.Fatalf("admin survived Profile switch: %#v", switched)
	}
}

// TestBeginAdminSessionBackoffGrowsAndResets pins the exponential-backoff curve
// of ADR-0012 §3 on the admin re-auth lane: past the free-attempt budget the
// lockout doubles per consecutive failure, and a successful re-auth resets the
// lane to its free budget again. feature:admin-session
func TestBeginAdminSessionBackoffGrowsAndResets(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetAdminPassword(context.Background(), "right-password", now); err != nil {
		t.Fatal(err)
	}
	clock := now
	service := NewServiceWithClock(st, func() time.Time { return clock })
	scope, err := service.ScopeFromRequest(context.Background(),
		authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}

	// failOnce drives one wrong-password attempt at the current clock and returns
	// the lockout horizon the lane reports immediately afterward (zero == open).
	failOnce := func() time.Time {
		t.Helper()
		if _, e := service.BeginAdminSession(context.Background(), scope, "wrong"); e != ErrAdminPassword && e != ErrAdminLocked {
			t.Fatalf("unexpected wrong-password err = %v", e)
		}
		until, e := st.AuthBackoffUntil(context.Background(), adminAttemptAction, "session:"+string(scope.SessionID), clock, adminBackoff)
		if e != nil {
			t.Fatal(e)
		}
		return until
	}

	// The failures below the budget impose no lockout.
	for i := 0; i < adminBackoff.FreeAttempts-1; i++ {
		if until := failOnce(); !until.IsZero() {
			t.Fatalf("free attempt %d already locked until %v", i, until)
		}
	}

	// The budget-th failure arms BaseDelay; the next consecutive failure doubles it.
	firstLock := failOnce()
	firstDelay := firstLock.Sub(clock)
	if firstDelay <= 0 {
		t.Fatalf("first post-budget failure did not arm a lockout (delay %v)", firstDelay)
	}
	// Advance past the lockout so the next attempt is allowed to run and fail.
	clock = firstLock.Add(time.Second)
	secondLock := failOnce()
	secondDelay := secondLock.Sub(clock)
	if secondDelay <= firstDelay {
		t.Fatalf("backoff did not grow: second delay %v not greater than first %v", secondDelay, firstDelay)
	}

	// A successful re-auth resets the lane: the next failure is free again.
	clock = secondLock.Add(time.Second)
	if _, err := service.BeginAdminSession(context.Background(), scope, "right-password"); err != nil {
		t.Fatalf("re-auth after backoff err = %v, want success", err)
	}
	clock = clock.Add(time.Second)
	if until := failOnce(); !until.IsZero() {
		t.Fatalf("lane not reset after success: locked until %v", until)
	}
}

// TestManagedScopeEnforcesAccessSchedule covers the enforcement deny-path of
// the access-schedules subsystem: a managed Profile is
// refused a resolved Scope when the server-local instant falls outside its
// allowed time windows, allowed inside one, and — additive default — always
// allowed when it has no windows at all. The clock is anchored in server-local
// time so the weekday/minute the enforcement reads is deterministic.
// feature:access-schedules
func TestManagedScopeEnforcesAccessSchedule(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()

	// A fixed local instant: 10:00 on its weekday (minute 600). The deny window
	// is one minute that does NOT contain 600; the allow window straddles it.
	at := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local) // a Monday, 10:00 local
	service := NewServiceWithClock(st, func() time.Time { return at })
	weekday := int(at.Weekday())
	atMinute := at.Hour()*60 + at.Minute() // 600

	// No windows: the always-allowed additive default — a managed Profile with
	// no schedule resolves exactly as before this subsystem existed.
	if _, err := service.Scope(ctx, "session-managed"); err != nil {
		t.Fatalf("Scope rejected a managed profile with no access schedule: %v", err)
	}

	// A window that excludes the current minute denies the session entirely.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{
		{Weekday: weekday, StartMinute: 0, EndMinute: atMinute}, // [00:00, 10:00) — excludes 10:00 (half-open)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scope(ctx, "session-managed"); err != ErrAccessOutsideSchedule {
		t.Fatalf("outside-window Scope err = %v, want ErrAccessOutsideSchedule", err)
	}

	// A window containing the current minute admits the session.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{
		{Weekday: weekday, StartMinute: atMinute - 30, EndMinute: atMinute + 30}, // [09:30, 10:30)
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := service.Scope(ctx, "session-managed")
	if err != nil {
		t.Fatalf("inside-window Scope rejected the session: %v", err)
	}
	if scope.ProfileID != "profile-managed" {
		t.Fatalf("ProfileID = %q, want profile-managed", scope.ProfileID)
	}

	// Clearing the schedule restores the always-allowed default.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scope(ctx, "session-managed"); err != nil {
		t.Fatalf("Scope rejected after the schedule was cleared: %v", err)
	}

	// An adult Profile is never time-gated, even with the same clock — only
	// managed Profiles carry a schedule, and SetProfileAccessWindows refuses a
	// non-managed target so an adult session can never be locked out this way.
	if _, err := service.Scope(ctx, "session-managed"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scope(ctx, "session-default"); err != nil {
		t.Fatalf("adult Scope was time-gated: %v", err)
	}
}

// TestApprovedExtensionLiftsScheduleBlock is PUSH-2 acceptance (d): an approved,
// unexpired extension grant scoped to the managed session lifts the
// access-schedule block in auth.Resolve for the granted window, and only for
// that session. ADR-0039 §2.2 — the extension is per-Session and time-bounded.
func TestApprovedExtensionLiftsScheduleBlock(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()

	at := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local) // Monday 10:00 local
	service := NewServiceWithClock(st, func() time.Time { return at })
	weekday := int(at.Weekday())
	atMinute := at.Hour()*60 + at.Minute()

	// A schedule that excludes the current minute — the managed session is blocked.
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{
		{Weekday: weekday, StartMinute: 0, EndMinute: atMinute}, // [00:00, 10:00) excludes 10:00
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scope(ctx, "session-managed"); err != ErrAccessOutsideSchedule {
		t.Fatalf("pre-grant Scope err = %v, want ErrAccessOutsideSchedule", err)
	}

	// Raise + approve an extension scoped to session-managed, granting 30 minutes
	// from `at` — the grant window now covers `at`.
	req, err := st.CreateExtensionRequest(ctx, store.ExtensionRequestInput{
		ProfileID:           "profile-managed",
		RequestingSessionID: "session-managed",
		TriggerSource:       "schedule",
		Now:                 at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", store.ExtensionApproved, 30, at); err != nil {
		t.Fatal(err)
	}

	// The block is now lifted for session-managed.
	if _, err := service.Scope(ctx, "session-managed"); err != nil {
		t.Fatalf("post-approval Scope still blocked: %v", err)
	}

	// Session-scoping: a DIFFERENT managed session of the same Profile is NOT
	// lifted by this grant. Enroll a second session for profile-managed.
	if err := st.SetSessionProfile(ctx, "session-revoked", "profile-managed"); err == nil {
		// session-revoked is on a revoked device, so it would fail for device reasons
		// before the schedule check — instead assert via the store grant directly:
		// the grant is for session-managed only.
	}
	granted, err := st.ActiveExtensionGrant(ctx, "profile-managed", "session-other", at)
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		t.Fatal("grant for session-managed wrongly applies to a different session")
	}

	// Time-bounded: once the 30-minute grant window passes, the block resumes.
	after := NewServiceWithClock(st, func() time.Time { return at.Add(31 * time.Minute) })
	if _, err := after.Scope(ctx, "session-managed"); err != ErrAccessOutsideSchedule {
		t.Fatalf("expired-grant Scope err = %v, want ErrAccessOutsideSchedule (grant window closed)", err)
	}
}
