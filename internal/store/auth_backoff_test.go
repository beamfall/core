package store

import (
	"context"
	"testing"
	"time"
)

// openBackoffStore opens a seeded store for the auth-backoff lane tests.
func openBackoffStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
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
