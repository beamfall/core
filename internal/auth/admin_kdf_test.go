package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// kdfProbe replaces the admin KDF with a slow wrong-password verifier that
// records how many verifications ran and the most that ran at once.
type kdfProbe struct {
	calls, inFlight, peak atomic.Int64
}

func installKDFProbe(t *testing.T, delay time.Duration) *kdfProbe {
	t.Helper()
	probe := &kdfProbe{}
	previous := verifyAdminPasswordKDF
	verifyAdminPasswordKDF = func(context.Context, *store.Store, string) error {
		probe.calls.Add(1)
		now := probe.inFlight.Add(1)
		for {
			peak := probe.peak.Load()
			if now <= peak || probe.peak.CompareAndSwap(peak, now) {
				break
			}
		}
		time.Sleep(delay)
		probe.inFlight.Add(-1)
		return store.ErrAdminPasswordMismatch
	}
	t.Cleanup(func() { verifyAdminPasswordKDF = previous })
	return probe
}

func adminKDFScope(t *testing.T) (*Service, *store.Store, Scope, time.Time) {
	t.Helper()
	st := storetest.Open(t)
	now := time.Unix(2_800_000, 0).UTC()
	if err := st.SetAdminPassword(context.Background(), "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope, err := service.ScopeFromRequest(context.Background(), authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return service, st, scope, now
}

func parallelAdminAttempts(service *Service, scope Scope, n int) []error {
	errs := make([]error, n)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range errs {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			_, errs[i] = service.BeginAdminSession(context.Background(), scope, "wrong")
		}(i)
	}
	start.Done()
	done.Wait()
	return errs
}

// TestParallelAdminAttemptsEachCountAndShareTwoKDFSlots: N parallel wrong
// attempts consume N attempts, never more than the lane allows, and at most
// two KDF verifications run at once.
// feature:admin-session
func TestParallelAdminAttemptsEachCountAndShareTwoKDFSlots(t *testing.T) {
	service, st, scope, now := adminKDFScope(t)
	probe := installKDFProbe(t, 30*time.Millisecond)

	free := adminBackoff.FreeAttempts
	for i, err := range parallelAdminAttempts(service, scope, free) {
		if err != ErrAdminPassword {
			t.Fatalf("attempt %d err = %v, want ErrAdminPassword", i, err)
		}
	}
	if got := probe.calls.Load(); got != int64(free) {
		t.Fatalf("KDF calls = %d, want %d", got, free)
	}
	until, err := st.AuthBackoffUntil(context.Background(), adminAttemptAction, adminAttemptKey(scope), now, adminBackoff)
	if err != nil || until.IsZero() {
		t.Fatalf("lane after %d parallel failures = (%v, %v), want locked", free, until, err)
	}

	// Past the budget, parallel attempts are refused before the KDF.
	for i, err := range parallelAdminAttempts(service, scope, 3*free) {
		if err != ErrAdminLocked {
			t.Fatalf("over-budget attempt %d err = %v, want ErrAdminLocked", i, err)
		}
	}
	if got := probe.calls.Load(); got != int64(free) {
		t.Fatalf("KDF calls after lockout = %d, want %d", got, free)
	}
	if peak := probe.peak.Load(); peak > adminKDFSlotCount {
		t.Fatalf("peak concurrent KDFs = %d, want <= %d", peak, adminKDFSlotCount)
	}
}

// TestParallelAdminAttemptsCannotOutrunTheLane: many more parallel attempts
// than the budget still reach the KDF only budget times.
// feature:admin-session
func TestParallelAdminAttemptsCannotOutrunTheLane(t *testing.T) {
	service, _, scope, _ := adminKDFScope(t)
	probe := installKDFProbe(t, 20*time.Millisecond)

	var rejected, locked int
	for i, err := range parallelAdminAttempts(service, scope, 4*adminBackoff.FreeAttempts) {
		switch err {
		case ErrAdminPassword:
			rejected++
		case ErrAdminLocked:
			locked++
		default:
			t.Fatalf("attempt %d err = %v", i, err)
		}
	}
	if rejected != adminBackoff.FreeAttempts || probe.calls.Load() != int64(adminBackoff.FreeAttempts) {
		t.Fatalf("rejected = %d, KDF calls = %d, want %d each", rejected, probe.calls.Load(), adminBackoff.FreeAttempts)
	}
	if peak := probe.peak.Load(); peak > adminKDFSlotCount {
		t.Fatalf("peak concurrent KDFs = %d, want <= %d", peak, adminKDFSlotCount)
	}
}

// TestAdminAttemptWithoutKDFSlotIsRefusedUncounted: when both slots stay busy
// the request gets the wrong-password refusal without running the KDF or
// spending an attempt.
// feature:admin-session
func TestAdminAttemptWithoutKDFSlotIsRefusedUncounted(t *testing.T) {
	service, st, scope, now := adminKDFScope(t)
	probe := installKDFProbe(t, 0)
	previousWait := adminKDFSlotWait
	adminKDFSlotWait = 20 * time.Millisecond
	t.Cleanup(func() { adminKDFSlotWait = previousWait })
	for range adminKDFSlotCount {
		adminKDFSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range adminKDFSlotCount {
			<-adminKDFSlots
		}
	})

	for i := 0; i < adminBackoff.FreeAttempts+1; i++ {
		if _, err := service.BeginAdminSession(context.Background(), scope, "right-password"); err != ErrAdminPassword {
			t.Fatalf("busy attempt %d err = %v, want ErrAdminPassword", i, err)
		}
	}
	if probe.calls.Load() != 0 {
		t.Fatalf("KDF ran %d times without a slot", probe.calls.Load())
	}
	until, err := st.AuthBackoffUntil(context.Background(), adminAttemptAction, adminAttemptKey(scope), now, adminBackoff)
	if err != nil || !until.IsZero() {
		t.Fatalf("lane after busy refusals = (%v, %v), want open", until, err)
	}
}
