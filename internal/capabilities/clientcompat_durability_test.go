package capabilities

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/streamtoken"
)

// memoryJournal is a durable journal that survives a "restart" the way a table
// does: the registry is thrown away, the journal is not. It mirrors the store's
// batch contract: RecordInvalidationBatch stages the whole batch first (one
// durable step), then promotes rows into entries, and LoadInvalidations replays
// any staged batch before answering — so a promotion cut mid-batch loses
// nothing across a "restart".
type memoryJournal struct {
	mu       sync.Mutex
	entries  map[string]Invalidation
	staged   []Invalidation
	loadErr  error
	putErr   error
	stageErr error
	delErr   error
	// rowBudget cuts the journal after N durable row writes — the process-death
	// seam. Negative means unlimited. Both the per-row write and batch promotion
	// consume it, so it cuts whichever path the registry uses mid-batch.
	rowBudget int
	loads     int
}

func newMemoryJournal() *memoryJournal {
	return &memoryJournal{entries: make(map[string]Invalidation), rowBudget: -1}
}

var errMemoryJournalRowBudget = errors.New("journal cut mid-batch")

// spendRowLocked consumes one durable row write from the budget.
func (j *memoryJournal) spendRowLocked() error {
	if j.rowBudget < 0 {
		return nil
	}
	if j.rowBudget == 0 {
		return errMemoryJournalRowBudget
	}
	j.rowBudget--
	return nil
}

func (j *memoryJournal) LoadInvalidations(_ context.Context, now time.Time) ([]Invalidation, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.loads++
	if j.loadErr != nil {
		return nil, j.loadErr
	}
	for _, entry := range j.staged {
		j.entries[entry.Subject] = entry
	}
	j.staged = nil
	var live []Invalidation
	for subject, entry := range j.entries {
		if !entry.Live(now) {
			delete(j.entries, subject)
			continue
		}
		live = append(live, entry)
	}
	return live, nil
}

func (j *memoryJournal) RecordInvalidation(_ context.Context, entry Invalidation) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.putErr != nil {
		return j.putErr
	}
	if err := j.spendRowLocked(); err != nil {
		return err
	}
	j.entries[entry.Subject] = entry
	return nil
}

// StageInvalidation durably stages one pending-refusal row beside whatever is
// already staged, mirroring the store's single-row staging write.
func (j *memoryJournal) StageInvalidation(_ context.Context, entry Invalidation) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.stageErr != nil {
		return j.stageErr
	}
	for i, staged := range j.staged {
		if staged.Subject == entry.Subject {
			j.staged[i] = entry
			return nil
		}
	}
	j.staged = append(j.staged, entry)
	return nil
}

func (j *memoryJournal) RecordInvalidationBatch(_ context.Context, entries []Invalidation) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.putErr != nil {
		return j.putErr
	}
	// Staging the whole batch is one durable step that keeps any already-staged
	// pending refusal (the store promotes it in the same transaction); promotion
	// consumes the row budget so the seam can cut the journal "after N of M
	// rows". A cut leaves everything staged, exactly as the store's staging
	// table would across a process death, and LoadInvalidations replays it.
	j.staged = append(j.staged, entries...)
	for _, entry := range j.staged {
		if err := j.spendRowLocked(); err != nil {
			return err
		}
		j.entries[entry.Subject] = entry
	}
	j.staged = nil
	return nil
}

func (j *memoryJournal) ClearInvalidation(_ context.Context, subject string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.delErr != nil {
		return j.delErr
	}
	delete(j.entries, subject)
	kept := j.staged[:0]
	for _, entry := range j.staged {
		if entry.Subject != subject {
			kept = append(kept, entry)
		}
	}
	j.staged = kept
	return nil
}

func (j *memoryJournal) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entries)
}

func durabilityContract(surface string) ClientContract {
	contract := EmbeddedUIContract()
	contract.Surface = surface
	contract.BuildID = "durability-build"
	return contract
}

func durabilityKey(subject, surface string) BindingKey {
	return BindingKey{
		Subject:         subject,
		Surface:         surface,
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}
}

func journalRegistry(journal InvalidationJournal) *Registry {
	registry := NewRegistry()
	registry.SetInvalidationJournal(journal, streamtoken.MaxStreamTokenTTL)
	return registry
}

// The whole ticket in one test: a refused subject resolves as ErrInvalidated on
// a REBUILT registry, while a subject that was never refused stays ErrNoDecision.
// Those two must not be the same error — the verify seam rejects only the first.
func TestResolveDistinguishesInvalidatedFromUnknownAfterRestart(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()

	before := journalRegistry(journal)
	refusal := Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}
	if _, accepted, err := before.Accept(
		durabilityKey("session:doomed", string(SurfaceMacOSManager)),
		durabilityContract(string(SurfaceMacOSManager)),
		refusal, now, time.Hour,
	); err != nil || accepted {
		t.Fatalf("Accept(refusal) = (%t, %v), want (false, nil)", accepted, err)
	}

	// Restart: a brand-new registry over the same journal.
	after := journalRegistry(journal)

	record, err := after.Resolve("session:doomed", ResolveKey{
		Subject:         "session:doomed",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, now.Add(time.Minute))
	if !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve(invalidated) error = %v, want ErrInvalidated", err)
	}
	if errors.Is(err, ErrNoDecision) {
		t.Fatal("an invalidated subject must not be reported as ErrNoDecision")
	}
	if record.Result.Decision != DecisionClientTooOld || record.Result.ReasonCode != ReasonRevokedBuild {
		t.Fatalf("rehydrated refusal = %+v, want the original decision and reason", record.Result)
	}

	// Negative control: a subject that was never refused is still unknown, not
	// invalid. If this ever flipped, the fix would have become "reject
	// everything after a restart".
	if _, err := after.Resolve("session:innocent", ResolveKey{
		Subject:         "session:innocent",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, now.Add(time.Minute)); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("Resolve(never-refused) error = %v, want ErrNoDecision", err)
	}
}

// A fresh submitted handshake is the one event that rehabilitates a subject, and
// it must clear the tombstone durably — otherwise the subject is refused again
// at the next restart.
func TestSubmittedAcceptClearsInvalidationAcrossRestart(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	surface := string(SurfaceMacOSManager)

	before := journalRegistry(journal)
	if _, _, err := before.Accept(
		durabilityKey("session:recovering", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}, now, time.Hour,
	); err != nil {
		t.Fatalf("Accept(refusal): %v", err)
	}
	if _, accepted, err := before.Accept(
		durabilityKey("session:recovering", surface),
		durabilityContract(surface),
		Result{Decision: DecisionCompatible}, now, time.Hour,
	); err != nil || !accepted {
		t.Fatalf("Accept(recovery) = (%t, %v), want (true, nil)", accepted, err)
	}
	if journal.count() != 0 {
		t.Fatalf("journal still holds %d tombstones after a servable handshake", journal.count())
	}

	after := journalRegistry(journal)
	if _, err := after.Resolve("session:recovering", ResolveKey{
		Subject:         "session:recovering",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, now.Add(time.Minute)); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("Resolve(recovered) error = %v, want ErrNoDecision", err)
	}
}

// A server-DERIVED provisional decision must not rehabilitate an invalidated
// subject. Post-restart nothing is in memory, so the embedded-UI provisional
// path would otherwise mint a decision for a subject the operator refused and
// erase the tombstone that made the refusal durable.
func TestDerivedAcceptCannotEraseInvalidation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	surface := string(SurfaceCoreWebUI)

	before := journalRegistry(journal)
	if _, _, err := before.Accept(
		durabilityKey("session:web", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonEmbeddedUISkew}, now, time.Hour,
	); err != nil {
		t.Fatalf("Accept(refusal): %v", err)
	}

	after := journalRegistry(journal)
	_, accepted, err := after.AcceptDerived(
		durabilityKey("session:web", surface),
		durabilityContract(surface),
		Result{Decision: DecisionCompatible}, now.Add(time.Minute), 30*time.Second,
	)
	if !errors.Is(err, ErrInvalidated) || accepted {
		t.Fatalf("AcceptDerived on an invalidated subject = (%t, %v), want (false, ErrInvalidated)", accepted, err)
	}
	if journal.count() != 1 {
		t.Fatalf("journal holds %d tombstones, want the refusal intact", journal.count())
	}
}

// The tombstone set is bounded: past its retention it stops applying and is
// dropped, so a server that refused many short-lived credentials does not carry
// them forever.
func TestInvalidationRetentionIsBounded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	surface := string(SurfaceMacOSManager)
	retention := InvalidationRetentionFor(streamtoken.MaxStreamTokenTTL)
	retentionFloor := streamtoken.MaxStreamTokenTTL + InvalidationSafetyMargin
	if retention < retentionFloor {
		t.Fatalf("invalidation retention = %s, want at least token maximum %s + safety margin %s", retention, streamtoken.MaxStreamTokenTTL, InvalidationSafetyMargin)
	}

	registry := journalRegistry(journal)
	if _, _, err := registry.Accept(
		durabilityKey("session:temporary", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}, now, time.Hour,
	); err != nil {
		t.Fatalf("Accept(refusal): %v", err)
	}

	// Restart at the maximum token lifetime: the durable tombstone must still
	// distinguish this subject from one that was genuinely never seen.
	restartedAtTokenLimit := journalRegistry(journal)
	_, err := restartedAtTokenLimit.Resolve("session:temporary", ResolveKey{
		Subject:         "session:temporary",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, now.Add(streamtoken.MaxStreamTokenTTL))
	if errors.Is(err, ErrNoDecision) {
		t.Fatal("an invalidated subject became unknown while its token could still be live")
	}
	if !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve at maximum token lifetime error = %v, want ErrInvalidated", err)
	}
	if live := restartedAtTokenLimit.Invalidations(now.Add(retention - time.Second)); len(live) != 1 {
		t.Fatalf("tombstone inside retention = %d entries, want 1", len(live))
	}

	// Past retention, a long-running registry drops the stale in-memory entry.
	expired := now.Add(retention + time.Second)
	if live := restartedAtTokenLimit.Invalidations(expired); len(live) != 0 {
		t.Fatalf("in-memory tombstones past retention = %d entries, want 0", len(live))
	}
	if count := len(restartedAtTokenLimit.invalidated); count != 0 {
		t.Fatalf("registry retained %d expired tombstones, want 0", count)
	}
	// A second restart proves the durable journal prunes the expired row too.
	restartedAfterRetention := journalRegistry(journal)
	if live := restartedAfterRetention.Invalidations(expired); len(live) != 0 {
		t.Fatalf("tombstone past retention = %d entries, want 0", len(live))
	}
	if _, err := restartedAfterRetention.Resolve("session:temporary", ResolveKey{
		Subject:         "session:temporary",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, expired); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("Resolve past retention error = %v, want ErrNoDecision", err)
	}
	if count := journal.count(); count != 0 {
		t.Fatalf("journal holds %d tombstones past retention, want 0", count)
	}
}

// Until the durable set has been read, every resolve fails closed. Answering
// from an unloaded set is precisely the restart-window race this ticket closes:
// the tombstones exist, the registry just has not read them yet.
func TestResolveFailsClosedUntilJournalLoads(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	journal.entries["session:doomed"] = Invalidation{
		Subject:    "session:doomed",
		Decision:   DecisionClientTooOld,
		ReasonCode: ReasonRevokedBuild,
		RecordedAt: now,
		ExpiresAt:  now.Add(InvalidationRetention),
	}
	journal.loadErr = errors.New("catalog unavailable")

	registry := journalRegistry(journal)
	if err := registry.LoadInvalidations(context.Background(), now); !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("eager load error = %v, want ErrInvalidationJournalUnavailable", err)
	}
	_, err := registry.Resolve("session:innocent", ResolveKey{Subject: "session:innocent"}, now)
	if !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Resolve with an unread journal error = %v, want ErrInvalidationJournalUnavailable", err)
	}
	if errors.Is(err, ErrNoDecision) {
		t.Fatal("an unread journal must not resolve as ErrNoDecision")
	}

	// The registry retries: once the journal reads, the tombstone applies and
	// unrelated subjects go back to the unbound behavior.
	journal.mu.Lock()
	journal.loadErr = nil
	journal.mu.Unlock()
	if _, err := registry.Resolve("session:doomed", ResolveKey{Subject: "session:doomed"}, now); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve after recovery error = %v, want ErrInvalidated", err)
	}
	if _, err := registry.Resolve("session:innocent", ResolveKey{Subject: "session:innocent"}, now); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("Resolve(unknown) after recovery error = %v, want ErrNoDecision", err)
	}
}

// A journal that can record the refusal neither directly nor staged reports the
// failure, and the memory-only refusal is never handed to any caller: the
// recording Accept gets the durability error instead of a refusal outcome, and
// every resolve fails closed rather than answering ErrInvalidated from a fact a
// restart would forget. The subject still cannot serve — the outage costs
// availability, never enforcement.
func TestRefusalStaysEnforcedWhenJournalWriteFails(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	journal.putErr = errors.New("catalog read-only")
	journal.stageErr = errors.New("catalog read-only")
	surface := string(SurfaceMacOSManager)

	registry := journalRegistry(journal)
	t.Cleanup(func() {
		// Let the process-owned retry stop before this test releases the journal,
		// including when a falsifiable assertion exits the test early.
		journal.mu.Lock()
		journal.putErr = nil
		journal.stageErr = nil
		journal.mu.Unlock()
		if err := registry.ensureInvalidations(now); err != nil {
			t.Errorf("settle pending refusal after recovery: %v", err)
		}
	})
	_, accepted, err := registry.Accept(
		durabilityKey("session:doomed", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}, now, time.Hour,
	)
	if accepted {
		t.Fatal("a refusal must never report acceptance")
	}
	if !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Accept error = %v, want ErrInvalidationJournalUnavailable", err)
	}
	_, err = registry.Resolve("session:doomed", ResolveKey{
		Subject:         "session:doomed",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, now)
	if !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Resolve over a memory-only refusal = %v, want fail-closed ErrInvalidationJournalUnavailable", err)
	}
	if errors.Is(err, ErrNoDecision) {
		t.Fatal("memory-only refusal downgraded the subject to unknown")
	}
}

// When the direct tombstone write fails but staging succeeds, the staged row IS
// the durable commit point: Accept reports the refusal outcome (nil error), and
// a restarted registry replays the staged row instead of forgetting the refusal
// — even though no retry ever promoted it while the first process lived.
func TestRefusalStagedFallbackReportsOutcomeAndSurvivesRestart(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	journal.putErr = errors.New("catalog read-only")
	surface := string(SurfaceMacOSManager)
	resolveKey := ResolveKey{
		Subject:         "session:doomed",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}

	before := journalRegistry(journal)
	_, accepted, err := before.Accept(
		durabilityKey("session:doomed", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}, now, time.Hour,
	)
	if accepted || err != nil {
		t.Fatalf("Accept(refusal) with a staged fallback = (%t, %v), want the refusal outcome reported (false, nil)", accepted, err)
	}
	if _, err := before.Resolve("session:doomed", resolveKey, now); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve over a staged refusal = %v, want ErrInvalidated", err)
	}

	// The process dies before any retry promotes the staged row; the journal
	// (the durable bytes) is all that carries over.
	after := journalRegistry(journal)
	_, err = after.Resolve("session:doomed", resolveKey, now.Add(time.Minute))
	if errors.Is(err, ErrNoDecision) {
		t.Fatal("restart forgot the staged refusal: subject resolved as never-handshook")
	}
	if !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve after restart = %v, want ErrInvalidated", err)
	}
}

// A refusal whose journal write failed transiently is a pending retry, not a
// dropped write: the next resolve after the journal recovers lands it, so a
// restart cannot forget a refusal the process was enforcing. Without the retry,
// the refusal lived only in memory — enforced until the process died, then
// resolved as never-handshook.
func TestRefusalRetriedAfterTransientJournalFailureSurvivesRestart(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	journal.putErr = errors.New("catalog read-only")
	journal.stageErr = errors.New("catalog read-only")
	surface := string(SurfaceMacOSManager)
	resolveKey := ResolveKey{
		Subject:         "session:doomed",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}

	before := journalRegistry(journal)
	_, accepted, err := before.Accept(
		durabilityKey("session:doomed", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}, now, time.Hour,
	)
	if accepted || !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Accept(refusal) under a failed write = (%t, %v), want (false, ErrInvalidationJournalUnavailable)", accepted, err)
	}
	if count := journal.count(); count != 0 {
		t.Fatalf("journal holds %d tombstones during the outage, want 0", count)
	}

	// The journal recovers; the next resolve retries the pending write.
	journal.mu.Lock()
	journal.putErr = nil
	journal.stageErr = nil
	journal.mu.Unlock()
	if _, err := before.Resolve("session:doomed", resolveKey, now); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve after journal recovery = %v, want ErrInvalidated", err)
	}
	if count := journal.count(); count != 1 {
		t.Fatalf("journal holds %d tombstones after recovery, want the retried refusal", count)
	}

	after := journalRegistry(journal)
	_, err = after.Resolve("session:doomed", resolveKey, now.Add(time.Minute))
	if errors.Is(err, ErrNoDecision) {
		t.Fatal("restart forgot the refusal: subject resolved as never-handshook")
	}
	if !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve after restart = %v, want ErrInvalidated", err)
	}
}

// A journal that cannot clear the tombstone must not report a clean acceptance:
// the subject would look valid now and be refused again after a restart.
func TestAcceptFailsWhenInvalidationCannotBeCleared(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	journal := newMemoryJournal()
	surface := string(SurfaceMacOSManager)

	registry := journalRegistry(journal)
	if _, _, err := registry.Accept(
		durabilityKey("session:doomed", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}, now, time.Hour,
	); err != nil {
		t.Fatalf("Accept(refusal): %v", err)
	}
	journal.mu.Lock()
	journal.delErr = errors.New("catalog read-only")
	journal.mu.Unlock()

	_, accepted, err := registry.Accept(
		durabilityKey("session:doomed", surface),
		durabilityContract(surface),
		Result{Decision: DecisionCompatible}, now, time.Hour,
	)
	if accepted || !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Accept with an unclearable tombstone = (%t, %v), want (false, ErrInvalidationJournalUnavailable)", accepted, err)
	}
	if _, err := registry.Resolve("session:doomed", ResolveKey{
		Subject:         "session:doomed",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, now); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve after a failed clear = %v, want the tombstone still in force", err)
	}
}

// With no journal attached the registry keeps its pre-existing in-memory
// behavior, so a deployment that supplies no durable store still boots and still
// enforces refusals for the life of the process.
func TestRegistryWithoutJournalKeepsInMemoryBehavior(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	surface := string(SurfaceMacOSManager)
	registry := NewRegistry()

	if _, _, err := registry.Accept(
		durabilityKey("session:doomed", surface),
		durabilityContract(surface),
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild}, now, time.Hour,
	); err != nil {
		t.Fatalf("Accept(refusal) without a journal: %v", err)
	}
	if _, err := registry.Resolve("session:doomed", ResolveKey{
		Subject:         "session:doomed",
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
	}, now); !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve without a journal = %v, want ErrInvalidated in-process", err)
	}
}
