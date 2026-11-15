package store

import (
	"context"
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
	if until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy); err != nil || !until.IsZero() {
		t.Fatalf("first reservation = (%v, %v), want open", until, err)
	}
	// "source" now holds two failures and is locked; "global" holds one.
	until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy)
	if err != nil || !until.After(at) {
		t.Fatalf("second reservation = (%v, %v), want locked", until, err)
	}
	if failures, _, err := st.consecutiveAuthFailures(ctx, action, "global", at, policy); err != nil || failures != 1 {
		t.Fatalf("global failures = (%d, %v), want 1: a locked reservation must record nothing", failures, err)
	}
	for _, key := range keys {
		recordSuccess(t, st, action, key, at)
	}
	if until, err := st.ReserveAuthAttempt(ctx, action, keys, at, policy); err != nil || !until.IsZero() {
		t.Fatalf("reservation after success = (%v, %v), want open", until, err)
	}
	if _, err := st.ReserveAuthAttempt(ctx, action, []string{"source", ""}, at, policy); err == nil {
		t.Fatal("reservation with an empty key succeeded")
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
