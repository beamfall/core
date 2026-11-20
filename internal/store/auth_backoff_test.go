package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openBackoffStore opens a seeded store for the auth-backoff lane tests.
func openBackoffStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), migratedDataDir(t), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func recordFailure(t *testing.T, st *Store, action, key string, at time.Time) {
	t.Helper()
	if err := st.RecordAuthAttempt(context.Background(), AuthAttempt{
		OccurredAt: at,
		Action:     action,
		Key:        key,
		Success:    false,
	}); err != nil {
		t.Fatal(err)
	}
}

func recordSuccess(t *testing.T, st *Store, action, key string, at time.Time) {
	t.Helper()
	if err := st.RecordAuthAttempt(context.Background(), AuthAttempt{
		OccurredAt: at,
		Action:     action,
		Key:        key,
		Success:    true,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAuthBackoffDelayGrowsWithConsecutiveFailures pins ADR-0012 §3: the lockout
// delay must grow with each consecutive failure (not a flat window).
func TestAuthBackoffDelayGrowsWithConsecutiveFailures(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: 10 * time.Minute, FreeAttempts: 1, BaseDelay: time.Second, MaxDelay: time.Hour}
	base := time.Unix(1_000_000, 0).UTC()

	const action, key = "test.lane", "subject-A"

	// No failures yet: lane open.
	until, err := st.AuthBackoffUntil(ctx, action, key, base, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !until.IsZero() {
		t.Fatalf("clean lane: want zero lockout, got %v", until)
	}

	var prevDelay time.Duration
	for i := 1; i <= 4; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		recordFailure(t, st, action, key, at)
		until, err := st.AuthBackoffUntil(ctx, action, key, at, policy)
		if err != nil {
			t.Fatal(err)
		}
		delay := until.Sub(at)
		if delay <= 0 {
			t.Fatalf("after %d failures: want a positive lockout delay, got %v", i, delay)
		}
		if i > 1 && delay <= prevDelay {
			t.Fatalf("after %d failures: delay %v did not grow beyond previous %v", i, delay, prevDelay)
		}
		prevDelay = delay
	}
}

// TestAuthBackoffResetsAfterSuccess pins the reset half of ADR-0012 §3: a
// successful attempt clears the growing delay for that lane.
func TestAuthBackoffResetsAfterSuccess(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: 10 * time.Minute, FreeAttempts: 1, BaseDelay: time.Second, MaxDelay: time.Hour}
	base := time.Unix(2_000_000, 0).UTC()

	const action, key = "test.lane", "subject-B"

	for i := 1; i <= 3; i++ {
		recordFailure(t, st, action, key, base.Add(time.Duration(i)*time.Second))
	}
	at := base.Add(4 * time.Second)
	until, err := st.AuthBackoffUntil(ctx, action, key, at, policy)
	if err != nil {
		t.Fatal(err)
	}
	if until.IsZero() || !until.After(at) {
		t.Fatalf("after 3 failures: want a future lockout, got %v", until)
	}

	// A success resets the lane to open.
	recordSuccess(t, st, action, key, base.Add(5*time.Second))
	after := base.Add(6 * time.Second)
	until, err = st.AuthBackoffUntil(ctx, action, key, after, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !until.IsZero() {
		t.Fatalf("after success: want lane reset to open, got lockout until %v", until)
	}
}

// TestAuthBackoffWindowAgesOutFailures confirms failures older than the window
// do not count toward the consecutive-failure delay.
func TestAuthBackoffWindowAgesOutFailures(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: 10 * time.Minute, FreeAttempts: 1, BaseDelay: time.Second, MaxDelay: time.Hour}
	base := time.Unix(3_000_000, 0).UTC()

	const action, key = "test.lane", "subject-C"

	recordFailure(t, st, action, key, base)
	// Far in the future, the lone failure has aged out of the window.
	at := base.Add(time.Hour)
	until, err := st.AuthBackoffUntil(ctx, action, key, at, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !until.IsZero() {
		t.Fatalf("aged-out failure: want open lane, got lockout until %v", until)
	}
}

// TestAuthBackoffDelayCapped confirms the growing delay never exceeds MaxDelay.
func TestAuthBackoffDelayCapped(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: time.Hour, FreeAttempts: 1, BaseDelay: time.Second, MaxDelay: 8 * time.Second}
	base := time.Unix(4_000_000, 0).UTC()

	const action, key = "test.lane", "subject-D"

	var at time.Time
	for i := 1; i <= 12; i++ {
		at = base.Add(time.Duration(i) * time.Second)
		recordFailure(t, st, action, key, at)
	}
	until, err := st.AuthBackoffUntil(ctx, action, key, at, policy)
	if err != nil {
		t.Fatal(err)
	}
	if delay := until.Sub(at); delay > policy.MaxDelay {
		t.Fatalf("delay %v exceeded cap %v", delay, policy.MaxDelay)
	}
}

// TestReserveAuthAttemptCountsEveryLaneOrNone: a reservation records a failure
// on every lane at once, records nothing once any lane is locked, and a later
// success at the same instant reopens the lanes.
func TestReserveAuthAttemptCountsEveryLaneOrNone(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: 10 * time.Minute, FreeAttempts: 2, BaseDelay: time.Second, MaxDelay: time.Hour}
	at := time.Unix(2_100_000, 0).UTC()
	const action = "test.reserve"
	keys := []string{"source", "global"}

	recordFailure(t, st, action, "source", at)
	if _, until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy); err != nil || !until.IsZero() {
		t.Fatalf("first reservation = (%v, %v), want open", until, err)
	}
	// "source" now holds two failures and is locked; "global" holds one.
	_, until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy)
	if err != nil || !until.After(at) {
		t.Fatalf("second reservation = (%v, %v), want locked", until, err)
	}
	if failures, _, err := st.consecutiveAuthFailures(ctx, action, "global", at, policy); err != nil || failures != 1 {
		t.Fatalf("global failures = (%d, %v), want 1: a locked reservation must record nothing", failures, err)
	}
	for _, key := range keys {
		recordSuccess(t, st, action, key, at)
	}
	if _, until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy); err != nil || !until.IsZero() {
		t.Fatalf("reservation after success = (%v, %v), want open", until, err)
	}
	if _, _, err := st.ReserveAuthAttempt(ctx, action, []string{"source", ""}, at, policy); err == nil {
		t.Fatal("reservation with an empty key succeeded")
	}
}

func TestIntegrationSettlementPreservesLaterFailuresAtSameMillisecond(t *testing.T) {
	st := openBackoffStore(t)
	ctx := t.Context()
	policy := AuthBackoff{Window: 10 * time.Minute, FreeAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Hour}
	at := time.Unix(2_100_000, 0).UTC()
	const action = IntegrationAttemptAction
	keys := []string{"name", "source"}
	for _, key := range keys {
		recordFailure(t, st, action, key, at)
	}
	first, until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy)
	if err != nil || !until.IsZero() {
		t.Fatalf("reserve successful credential: (%v, %v)", until, err)
	}
	if _, until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy); err != nil || !until.IsZero() {
		t.Fatalf("reserve later failure: (%v, %v)", until, err)
	}
	session := Session{
		ID: "integration-order", ProfileID: "profile-default", Kind: SessionKindIntegration,
		IntegrationScopes: []IntegrationScope{IntegrationScopeRead}, ExpiresAt: at.Add(time.Hour),
	}
	optIn := true
	if _, err := st.UpdateProfileSettings(ctx, session.ProfileID, ProfileSettings{IntegrationSignIn: &optIn}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateIntegrationSession(ctx, session, true, first, time.Now); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if failures, _, err := st.consecutiveAuthFailures(ctx, action, key, at, policy); err != nil || failures != 1 {
			t.Fatalf("%s lane failures = (%d, %v), want only the later failure", key, failures, err)
		}
	}
}

func TestIntegrationAttemptsSurviveCatalogUpgradeWithoutReplay(t *testing.T) {
	ctx := t.Context()
	dataDir := t.TempDir()
	dbDir := filepath.Join(dataDir, "db")
	if err := os.Mkdir(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dataDir, "backups")
	catalogPath := filepath.Join(dbDir, "catalog.db")
	catalog, err := openSQLite(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	before := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0126")
	if err := migrate(ctx, slog.Default(), catalog, catalogPath, "catalog", before, "migrate/catalog", backupDir); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(dbDir, "events.db")
	events, err := openSQLite(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	if err := migrate(ctx, slog.Default(), events, eventsPath, "events", eventsMigrations, "migrate/events", backupDir); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := events.ExecContext(ctx, `
		INSERT INTO auth_attempt(occurred_at_ms, action, key, success)
		VALUES (?, ?, 'profile:profile-default', 0), (?, ?, 'ip:fixture', 0)`,
		at.UnixMilli(), IntegrationAttemptAction, at.UnixMilli(), IntegrationAttemptAction); err != nil {
		t.Fatal(err)
	}
	if err := events.Close(); err != nil {
		t.Fatal(err)
	}

	opts := Options{SeedDevelopmentData: true}
	st, err := Open(ctx, dataDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	policy := AuthBackoff{Window: time.Minute, FreeAttempts: 1, BaseDelay: time.Second, MaxDelay: time.Minute}
	keys := []string{"profile:profile-default", "ip:fixture"}
	for _, key := range keys {
		if until, err := st.AuthBackoffUntil(ctx, IntegrationAttemptAction, key, at, policy); err != nil || !until.Equal(at.Add(policy.BaseDelay)) {
			t.Fatalf("upgraded lane %q: locked until %v, error %v", key, until, err)
		}
	}
	reservation, until, err := st.ReserveAuthAttempt(ctx, IntegrationAttemptAction, keys, at.Add(policy.BaseDelay), policy)
	if err != nil || !until.IsZero() {
		t.Fatalf("reserve after upgraded lockout: locked until %v, error %v", until, err)
	}
	session := Session{
		ID: "upgraded-integration", ProfileID: "profile-default", Kind: SessionKindIntegration,
		IntegrationScopes: []IntegrationScope{IntegrationScopeRead}, ExpiresAt: at.Add(time.Hour),
	}
	optIn := true
	if _, err := st.UpdateProfileSettings(ctx, session.ProfileID, ProfileSettings{IntegrationSignIn: &optIn}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateIntegrationSession(ctx, session, true, reservation, time.Now); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dataDir, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Session(ctx, session.ID); err != nil {
		t.Fatalf("reopened integration session: %v", err)
	}
	for _, key := range keys {
		if failures, _, err := reopened.consecutiveAuthFailures(ctx, IntegrationAttemptAction, key, at.Add(policy.BaseDelay), policy); err != nil || failures != 0 {
			t.Fatalf("reopened lane %q: %d failures, error %v", key, failures, err)
		}
	}
}

// TestAuthBackoffHoldsTheCapUnderSustainedGuessing pins ADR-0012 §3 against a
// caller that guesses the moment each lockout ends (CORE-2119): once the delay
// reaches MaxDelay it must stay there, so guessing yields about one attempt
// per MaxDelay and not a fresh set of free attempts every Window. Three days
// takes the run past authFailureScanLimit stored failures.
func TestAuthBackoffHoldsTheCapUnderSustainedGuessing(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: 5 * time.Minute, FreeAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: 5 * time.Minute}
	start := time.Unix(5_000_000, 0).UTC()
	end := start.Add(72 * time.Hour)

	const action, key = "test.lane", "subject-E"

	guesses := 0
	for now := start; now.Before(end); {
		until, err := st.AuthBackoffUntil(ctx, action, key, now, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !until.IsZero() {
			now = until
			continue
		}
		recordFailure(t, st, action, key, now)
		guesses++
		now = now.Add(time.Millisecond)
	}
	// Free attempts, the doubling steps up to the cap, then one per MaxDelay.
	const climb = 5 + 8
	if limit := climb + int(72*time.Hour/policy.MaxDelay); guesses > limit {
		t.Fatalf("three days of guessing at every lockout end: %d attempts, want at most %d", guesses, limit)
	}
}

// TestAuthBackoffIdleLaneResets: a lane left alone for Window after its lockout
// ended starts again with its free attempts.
func TestAuthBackoffIdleLaneResets(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: 5 * time.Minute, FreeAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: 5 * time.Minute}
	base := time.Unix(6_000_000, 0).UTC()

	const action, key = "test.lane", "subject-F"

	// Six quick failures arm the second step (4s).
	for i := 0; i < 6; i++ {
		recordFailure(t, st, action, key, base.Add(time.Duration(i)*time.Millisecond))
	}
	last := base.Add(5 * time.Millisecond)
	if until, err := st.AuthBackoffUntil(ctx, action, key, last, policy); err != nil || !until.Equal(last.Add(4*time.Second)) {
		t.Fatalf("after 6 failures: want lockout until %v, got %v (err %v)", last.Add(4*time.Second), until, err)
	}

	// Idle for Window past the lockout: four more failures are free again.
	at := last.Add(4*time.Second + policy.Window)
	for i := 0; i < 4; i++ {
		recordFailure(t, st, action, key, at.Add(time.Duration(i)*time.Millisecond))
	}
	if until, err := st.AuthBackoffUntil(ctx, action, key, at.Add(time.Second), policy); err != nil || !until.IsZero() {
		t.Fatalf("after an idle Window: want an open lane, got lockout until %v (err %v)", until, err)
	}

	// Returning just before that point keeps the run going.
	const other = "subject-G"
	for i := 0; i < 6; i++ {
		recordFailure(t, st, action, other, base.Add(time.Duration(i)*time.Millisecond))
	}
	soon := last.Add(4*time.Second + policy.Window - time.Second)
	recordFailure(t, st, action, other, soon)
	if until, err := st.AuthBackoffUntil(ctx, action, other, soon, policy); err != nil || !until.Equal(soon.Add(8*time.Second)) {
		t.Fatalf("returning inside the Window: want lockout until %v, got %v (err %v)", soon.Add(8*time.Second), until, err)
	}
}

// TestAuthBackoffSlowFailuresStayFreshRuns: failures that each arrive after the
// lane has been idle for Window never add up.
func TestAuthBackoffSlowFailuresStayFreshRuns(t *testing.T) {
	st := openBackoffStore(t)
	ctx := context.Background()
	policy := AuthBackoff{Window: 10 * time.Minute, FreeAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: 10 * time.Minute}
	base := time.Unix(7_000_000, 0).UTC()

	const action, key = "test.lane", "subject-H"
	const failures = 100
	step := policy.Window + time.Second
	for i := 0; i < failures; i++ {
		recordFailure(t, st, action, key, base.Add(time.Duration(i)*step))
	}
	last := base.Add((failures - 1) * step)
	if until, err := st.AuthBackoffUntil(ctx, action, key, last.Add(time.Second), policy); err != nil || !until.IsZero() {
		t.Fatalf("after %d slow failures: want an open lane, got lockout until %v (err %v)", failures, until, err)
	}
	if count, _, err := st.consecutiveAuthFailures(ctx, action, key, last.Add(time.Second), policy); err != nil || count != 1 {
		t.Fatalf("after %d slow failures: want a run of 1, got %d (err %v)", failures, count, err)
	}
}
