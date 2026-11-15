package causation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"
)

func testLimits() Limits {
	return Limits{
		GlobalActions:      3,
		ChainLifetime:      15 * time.Minute,
		WorkerActions:      100,
		WorkerWindow:       time.Minute,
		MaxTrackedChains:   32,
		MaxTrackedWorkers:  8,
		DeadLetterRecords:  4,
		DeadLetterWindow:   time.Minute,
		DeadLetterCapacity: 16,
	}
}

func TestGlobalBudgetBoundsCrossWorkerChain(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	cap := NewCausationCap(testLimits(), func() time.Time { return now })
	token, err := cap.TokenFor("outbox:41", now)
	if err != nil {
		t.Fatal(err)
	}

	for i, workerID := range []string{"worker-a", "worker-b", "worker-a"} {
		if err := cap.Admit(workerID, token, "request-ingest"); err != nil {
			t.Fatalf("chain action %d by %s: %v", i+1, workerID, err)
		}
	}
	if err := cap.Admit("worker-b", token, "request-ingest"); !errors.Is(err, ErrGlobalBudget) {
		t.Fatalf("A→B→A→B action 4 error = %v, want %v", err, ErrGlobalBudget)
	}
}

func TestGlobalBudgetSurvivesWorkerRestart(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	limits := testLimits()
	limits.GlobalActions = 2
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("outbox:42", now)
	if err != nil {
		t.Fatal(err)
	}

	firstProcess := workerProcess{id: "worker-a", cap: cap}
	if err := firstProcess.act(token); err != nil {
		t.Fatal(err)
	}
	// A worker restart creates a new process handle, but the Core-owned cap is
	// intentionally the same object and retains the chain's global spend.
	restartedProcess := workerProcess{id: "worker-a", cap: cap}
	if err := restartedProcess.act(token); err != nil {
		t.Fatal(err)
	}
	if err := restartedProcess.act(token); !errors.Is(err, ErrGlobalBudget) {
		t.Fatalf("post-restart action error = %v, want %v", err, ErrGlobalBudget)
	}
}

type workerProcess struct {
	id  string
	cap *CausationCap
}

func (w workerProcess) act(token Token) error {
	return w.cap.Admit(w.id, token, "request-file-mutation")
}

func TestPerWorkerActionRateLimit(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	limits := testLimits()
	limits.WorkerActions = 2
	cap := NewCausationCap(limits, func() time.Time { return now })

	for i := 0; i < 2; i++ {
		token, err := cap.TokenFor(fmt.Sprintf("outbox:%d", i), now)
		if err != nil {
			t.Fatal(err)
		}
		if err := cap.Admit("worker-a", token, "enqueue-job"); err != nil {
			t.Fatalf("worker-a action %d: %v", i+1, err)
		}
	}
	token, err := cap.TokenFor("outbox:rate-limit", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.Admit("worker-a", token, "enqueue-job"); !errors.Is(err, ErrWorkerRateLimit) {
		t.Fatalf("worker-a action 3 error = %v, want %v", err, ErrWorkerRateLimit)
	}
	if err := cap.Admit("worker-b", token, "enqueue-job"); err != nil {
		t.Fatalf("worker-b must have an independent rate window: %v", err)
	}

	now = now.Add(time.Minute)
	if err := cap.Admit("worker-a", token, "enqueue-job"); err != nil {
		t.Fatalf("worker-a after rate-window reset: %v", err)
	}
}

func TestWorkerRateStateCapacityFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	limits := testLimits()
	limits.GlobalActions = 100
	limits.MaxTrackedWorkers = 3
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("outbox:worker-capacity", now)
	if err != nil {
		t.Fatal(err)
	}

	const workerAttempts = 100
	for i := 0; i < workerAttempts; i++ {
		workerID := fmt.Sprintf("worker-%d", i)
		err := cap.Admit(workerID, token, "enqueue-job")
		if i < limits.MaxTrackedWorkers && err != nil {
			t.Fatalf("admit retained %s: %v", workerID, err)
		}
		if i >= limits.MaxTrackedWorkers && !errors.Is(err, ErrTrackingCapacity) {
			t.Fatalf("new worker %s beyond capacity error = %v, want %v", workerID, err, ErrTrackingCapacity)
		}
	}
	if got := len(cap.workers); got != limits.MaxTrackedWorkers {
		t.Fatalf("retained worker states = %d, want fixed bound %d", got, limits.MaxTrackedWorkers)
	}
}

func TestExpiredChainCannotRefill(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	limits := testLimits()
	limits.ChainLifetime = time.Minute
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("outbox:expired", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.Admit("worker-a", token, "request-ingest"); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	if err := cap.Admit("worker-a", token, "request-ingest"); !errors.Is(err, ErrChainExpired) {
		t.Fatalf("expired chain error = %v, want %v", err, ErrChainExpired)
	}
}

func TestEveryEventTripProducesOneCoalescedDeadLetter(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	limits := testLimits()
	limits.GlobalActions = 1
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("outbox:43", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.Admit("worker-a", token, "request-ingest"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if err := cap.Admit("worker-a", token, "request-ingest"); !errors.Is(err, ErrGlobalBudget) {
			t.Fatalf("trip %d error = %v, want %v", i+1, err, ErrGlobalBudget)
		}
	}

	snapshot := cap.DeadLetters()
	if len(snapshot.Records) != 1 {
		t.Fatalf("dead-letter records = %d, want one coalesced record", len(snapshot.Records))
	}
	if got := snapshot.Records[0].Occurrences; got != 100 {
		t.Fatalf("coalesced occurrences = %d, want 100", got)
	}
}

func TestDeadLetterNewRecordRateIsBounded(t *testing.T) {
	now := time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC)
	limits := testLimits()
	limits.DeadLetterRecords = 2
	cap := NewCausationCap(limits, func() time.Time { return now })

	for i := 0; i < 20; i++ {
		if err := cap.Admit("worker-a", Token(fmt.Sprintf("forged-%d", i)), "request-outbound"); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("invalid token %d error = %v, want %v", i, err, ErrInvalidToken)
		}
	}
	snapshot := cap.DeadLetters()
	if got := len(snapshot.Records); got != 2 {
		t.Fatalf("new records in one window = %d, want rate cap 2", got)
	}
	if snapshot.Suppressed != 18 {
		t.Fatalf("suppressed records = %d, want 18", snapshot.Suppressed)
	}
}

func TestAdmitWithDescendantsBindsRowsToTheChain(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	cap := NewCausationCap(DefaultLimits(), func() time.Time { return now })
	token, err := cap.TokenFor("root", now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := cap.TokenFor("other", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.AdmitWithDescendants("worker", token, "write", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if got := cap.Inherited("a"); got != token {
		t.Fatalf("Inherited(a) = %q, want the admitting chain", got)
	}
	if got := cap.Inherited("unbound"); got != "" {
		t.Fatalf("Inherited(unbound) = %q, want empty", got)
	}
	// A bound key collides for any chain, its own included, and the refusal
	// spends nothing and records nothing: the caller restamps and retries.
	for _, tok := range []Token{other, token} {
		if err := cap.AdmitWithDescendants("worker", tok, "write", []string{"c", "a"}); !errors.Is(err, ErrDescendantCollision) {
			t.Fatalf("admit over a bound key: err = %v, want %v", err, ErrDescendantCollision)
		}
	}
	if got := cap.Inherited("a"); got != token {
		t.Fatal("a later chain rebound a descendant already bound to another chain")
	}
	if got := cap.Inherited("c"); got != "" {
		t.Fatal("a colliding admission bound its other keys")
	}
	if snapshot := cap.DeadLetters(); len(snapshot.Records) != 0 {
		t.Fatalf("a collision recorded dead letters: %+v", snapshot.Records)
	}
	if got := cap.chains[mustChainID(t, cap, other, now)].actions; got != 0 {
		t.Fatalf("a collision spent %d actions of the other chain", got)
	}
	now = now.Add(DefaultLimits().ChainLifetime + time.Millisecond)
	if got := cap.Inherited("b"); got != "" {
		t.Fatalf("Inherited(b) after the binding's window = %q, want empty", got)
	}
}

func TestAdmitWithDescendantsFailsClosedWhenTheLedgerIsFull(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	limits := DefaultLimits()
	limits.MaxTrackedDescendants = 2
	limits.GlobalActions = 2
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("root", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.AdmitWithDescendants("worker", token, "write", []string{"a", "b", "c"}); !errors.Is(err, ErrTrackingCapacity) {
		t.Fatalf("over-capacity admit: err = %v, want %v", err, ErrTrackingCapacity)
	}
	if got := cap.Inherited("a"); got != "" {
		t.Fatal("a refused admission bound a descendant")
	}
	// The refusal spent no budget: both actions remain.
	for i := 0; i < 2; i++ {
		if err := cap.AdmitWithDescendants("worker", token, "write", []string{string(rune('a' + i))}); err != nil {
			t.Fatalf("admit %d after refusal: %v", i, err)
		}
	}
	snapshot := cap.DeadLetters()
	if len(snapshot.Records) != 1 || snapshot.Records[0].Reason != DeadLetterTrackingCapacity {
		t.Fatalf("dead letters = %+v, want one tracking-capacity record", snapshot.Records)
	}
}

func TestAdmitWithDescendantsBoundsEachWorkersShare(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	limits := DefaultLimits()
	limits.MaxDescendantsPerWorker = 2
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("root", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.AdmitWithDescendants("greedy", token, "write", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := cap.AdmitWithDescendants("greedy", token, "write", []string{"c"}); !errors.Is(err, ErrTrackingCapacity) {
		t.Fatalf("over-share admit: err = %v, want %v", err, ErrTrackingCapacity)
	}
	if err := cap.AdmitWithDescendants("other", token, "write", []string{"c", "d"}); err != nil {
		t.Fatalf("another worker was denied by the greedy worker's share: %v", err)
	}
	// Expiry returns the share.
	now = now.Add(limits.ChainLifetime + time.Millisecond)
	fresh, err := cap.TokenFor("fresh", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.AdmitWithDescendants("greedy", fresh, "write", []string{"e", "f"}); err != nil {
		t.Fatalf("share not returned after expiry: %v", err)
	}
}

func TestReleaseDescendantsUnbindsOnlyTheChainsKeys(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	limits := DefaultLimits()
	limits.MaxDescendantsPerWorker = 2
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("root", now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := cap.TokenFor("other", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.AdmitWithDescendants("worker", token, "write", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := cap.AdmitWithDescendants("peer", other, "write", []string{"c"}); err != nil {
		t.Fatal(err)
	}
	cap.ReleaseDescendants(token, []string{"a", "b", "c"})
	if cap.Inherited("a") != "" || cap.Inherited("b") != "" {
		t.Fatal("released keys are still bound")
	}
	if cap.Inherited("c") != other {
		t.Fatal("release unbound a key bound to another chain")
	}
	// The released keys returned the worker's share.
	if err := cap.AdmitWithDescendants("worker", token, "write", []string{"a", "b"}); err != nil {
		t.Fatalf("share not returned after release: %v", err)
	}
}

func mustChainID(t *testing.T, cap *CausationCap, token Token, now time.Time) [sha256.Size]byte {
	t.Helper()
	id, _, err := cap.validateToken(token, now)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Codex review of CORE-1695: a row stamped after admission (a restamp after a
// collision) keeps its binding until a fresh root started at its stamp would
// be expired, so it never regains budget once its chain has expired.
func TestRestampedDescendantNeverYieldsALiveRoot(t *testing.T) {
	admittedAt := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	now := admittedAt
	limits := DefaultLimits()
	cap := NewCausationCap(limits, func() time.Time { return now })
	token, err := cap.TokenFor("root", admittedAt)
	if err != nil {
		t.Fatal(err)
	}
	stampedAt := admittedAt.Add(5 * time.Millisecond)
	if err := cap.AdmitWithDescendantsAt("w", token, "write", []string{"row"}, stampedAt); err != nil {
		t.Fatal(err)
	}
	// The chain has expired, but a root re-minted from the row would not have.
	now = admittedAt.Add(limits.ChainLifetime + time.Millisecond)
	if err := cap.Admit("w", token, "write"); !errors.Is(err, ErrChainExpired) {
		t.Fatalf("original chain: err = %v, want %v", err, ErrChainExpired)
	}
	if got := cap.Inherited("row"); got != token {
		t.Fatalf("row inherited %q before its fresh root expired, want the spent chain %q", got, token)
	}
	// Once the binding lapses, the root a delivery would mint is already expired.
	now = stampedAt.Add(limits.ChainLifetime + time.Millisecond)
	if got := cap.Inherited("row"); got != "" {
		t.Fatalf("binding outlived its window: %q", got)
	}
	root, err := cap.TokenFor("outbox:1", stampedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.Admit("w", root, "write"); !errors.Is(err, ErrChainExpired) {
		t.Fatalf("re-minted root: err = %v, want %v", err, ErrChainExpired)
	}
}
