package causation

import (
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
