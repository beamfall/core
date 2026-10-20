package capabilities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/streamtoken"
)

type durabilityDecisionResolver struct {
	registry *Registry
	want     ResolveKey
	now      time.Time
}

func (r durabilityDecisionResolver) Resolve(_ context.Context, subject string, _ streamtoken.DecisionWant) (string, bool, error) {
	record, err := r.registry.Resolve(subject, r.want, r.now)
	if errors.Is(err, ErrNoDecision) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return record.DecisionID, true, nil
}

func TestGenerationInvalidationStaysRejectedAcrossRestart(t *testing.T) {
	now := time.Now().UTC()
	journal := newMemoryJournal()
	const (
		sessionID  = "generation-stale"
		subject    = "session:" + sessionID
		generation = 7
	)
	tokenKey := []byte("generation-invalidation-test-key")

	before := journalRegistry(journal)
	if err := before.Invalidate(generation); err != nil {
		t.Fatalf("set generation %d: %v", generation, err)
	}
	key := durabilityKey(subject, string(SurfaceMacOSManager))
	key.Generation = generation
	if _, accepted, err := before.Accept(
		key,
		durabilityContract(string(SurfaceMacOSManager)),
		Result{Decision: DecisionCompatible},
		now,
		time.Hour,
	); err != nil || !accepted {
		t.Fatalf("Accept at generation %d = (%t, %v), want (true, nil)", generation, accepted, err)
	}

	issuer := streamtoken.NewService(tokenKey, func() time.Time { return now })
	token, err := issuer.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-generation-stale",
		DeviceID:  "device-generation-stale",
		SessionID: sessionID,
		ProfileID: "profile-generation-stale",
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue unstamped token: %v", err)
	}
	grant, err := issuer.UnverifiedGrant(token, streamtoken.TargetMediaDirect)
	if err != nil {
		t.Fatalf("verify issued token: %v", err)
	}
	if grant.CompatSubject != "" || grant.CompatDecisionID != "" {
		t.Fatalf("legacy token unexpectedly stamped with %q/%q", grant.CompatSubject, grant.CompatDecisionID)
	}

	if err := before.Invalidate(generation + 1); err != nil {
		t.Fatalf("invalidate generation %d: %v", generation+1, err)
	}
	journal.mu.Lock()
	tombstone, persisted := journal.entries[subject]
	journal.mu.Unlock()
	if !persisted {
		t.Fatal("generation advance did not persist the affected subject")
	}
	if tombstone.refused() {
		t.Fatalf("generation tombstone lost the prior servable result: %+v", tombstone)
	}
	retention := InvalidationRetentionFor(streamtoken.MaxStreamTokenTTL)
	if lifetime := tombstone.ExpiresAt.Sub(tombstone.RecordedAt); lifetime != retention {
		t.Fatalf("generation tombstone lifetime = %s, want %s", lifetime, retention)
	}
	want := ResolveKey{
		Subject:         subject,
		Distribution:    DistributionDirect,
		AuthFingerprint: "fingerprint",
		Generation:      generation + 1,
	}
	if _, err := before.Resolve(subject, want, now); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Resolve before restart error = %v, want ErrStaleGeneration", err)
	}
	issuer.SetCompatDecisionResolver(durabilityDecisionResolver{registry: before, want: want, now: now})
	if err := issuer.VerifyCompatDecision(context.Background(), grant); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token before restart error = %v, want ErrCompatDecisionInvalid", err)
	}

	// Restart: discard the registry and rebuild it over the same journal.
	after := journalRegistry(journal)
	if err := after.Invalidate(generation + 1); err != nil {
		t.Fatalf("restore generation %d: %v", generation+1, err)
	}
	if _, err := after.Resolve(subject, want, now.Add(time.Minute)); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Resolve after restart error = %v, want ErrStaleGeneration", err)
	}
	restartedTokens := streamtoken.NewService(tokenKey, func() time.Time { return now.Add(time.Minute) })
	restartedGrant, err := restartedTokens.UnverifiedGrant(token, streamtoken.TargetMediaDirect)
	if err != nil {
		t.Fatalf("verify unexpired token after restart: %v", err)
	}
	restartedTokens.SetCompatDecisionResolver(durabilityDecisionResolver{
		registry: after,
		want:     want,
		now:      now.Add(time.Minute),
	})
	if err := restartedTokens.VerifyCompatDecision(context.Background(), restartedGrant); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token after restart error = %v, want ErrCompatDecisionInvalid", err)
	}

	unknown := "session:genuinely-unknown"
	if _, err := after.Resolve(unknown, ResolveKey{Subject: unknown, Generation: generation + 1}, now.Add(time.Minute)); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("Resolve genuinely unknown subject error = %v, want ErrNoDecision", err)
	}
}

func TestGenerationInvalidationRetriesFailedJournalWrite(t *testing.T) {
	now := time.Now().UTC()
	journal := newMemoryJournal()
	registry := journalRegistry(journal)
	if err := registry.Invalidate(3); err != nil {
		t.Fatalf("set generation: %v", err)
	}
	key := durabilityKey("session:retry", string(SurfaceMacOSManager))
	key.Generation = 3
	if _, accepted, err := registry.Accept(
		key,
		durabilityContract(string(SurfaceMacOSManager)),
		Result{Decision: DecisionCompatible},
		now,
		time.Hour,
	); err != nil || !accepted {
		t.Fatalf("Accept = (%t, %v), want (true, nil)", accepted, err)
	}

	journal.mu.Lock()
	journal.putErr = errors.New("catalog read-only")
	journal.mu.Unlock()
	if err := registry.Invalidate(4); !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Invalidate with failed journal = %v, want ErrInvalidationJournalUnavailable", err)
	}
	want := ResolveKey{Subject: key.Subject, Distribution: key.Distribution, AuthFingerprint: key.AuthFingerprint, Generation: 4}
	// The advance is uncommitted, so the subject resolves neither to its
	// superseded decision (fail-open) nor to a rejection the journal never
	// accepted: it reports the durability failure until the write lands.
	if _, err := registry.Resolve(key.Subject, want, now); !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Resolve with an uncommitted generation advance = %v, want ErrInvalidationJournalUnavailable", err)
	}

	journal.mu.Lock()
	journal.putErr = nil
	journal.mu.Unlock()
	if err := registry.Invalidate(4); err != nil {
		t.Fatalf("retry Invalidate: %v", err)
	}
	restarted := journalRegistry(journal)
	if err := restarted.Invalidate(4); err != nil {
		t.Fatalf("restore generation: %v", err)
	}
	if _, err := restarted.Resolve(key.Subject, want, now); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Resolve after successful retry and restart = %v, want ErrStaleGeneration", err)
	}
}

// A generation batch is atomically durable: the journal cut "after N of M rows"
// simulates process death mid-batch, and a restart must report EVERY subject of
// the batch invalid — a partially rehydrated batch that reports an omitted
// subject as ErrNoDecision is the fail-open observable this ticket forbids. The
// staged batch write is the mechanism: the whole batch becomes durable in one
// step before promotion, and rehydration replays whatever promotion did not
// finish.
func TestGenerationBatchCutMidWriteIsCompletedAcrossRestart(t *testing.T) {
	now := time.Now().UTC()
	journal := newMemoryJournal()
	subjects := []string{"session:batch-a", "session:batch-b", "session:batch-c"}

	before := journalRegistry(journal)
	if err := before.Invalidate(8); err != nil {
		t.Fatalf("set generation: %v", err)
	}
	for _, subject := range subjects {
		key := durabilityKey(subject, string(SurfaceMacOSManager))
		key.Generation = 8
		if _, accepted, err := before.Accept(
			key,
			durabilityContract(string(SurfaceMacOSManager)),
			Result{Decision: DecisionCompatible},
			now,
			time.Hour,
		); err != nil || !accepted {
			t.Fatalf("Accept(%s) = (%t, %v), want (true, nil)", subject, accepted, err)
		}
	}

	journal.mu.Lock()
	journal.rowBudget = 1
	journal.mu.Unlock()
	if err := before.Invalidate(9); !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Invalidate cut mid-batch = %v, want ErrInvalidationJournalUnavailable", err)
	}
	want := func(subject string) ResolveKey {
		return ResolveKey{
			Subject:         subject,
			Distribution:    DistributionDirect,
			AuthFingerprint: "fingerprint",
			Generation:      9,
		}
	}
	// The cut advance is uncommitted in-process: resolution fails closed rather
	// than answering from the superseded decisions.
	if _, err := before.Resolve(subjects[0], want(subjects[0]), now); !errors.Is(err, ErrInvalidationJournalUnavailable) {
		t.Fatalf("Resolve under a cut batch = %v, want ErrInvalidationJournalUnavailable", err)
	}

	// Process death mid-batch: the registry dies, the journal's bytes survive.
	// The restarted process finds a healthy journal.
	journal.mu.Lock()
	journal.rowBudget = -1
	journal.mu.Unlock()
	after := journalRegistry(journal)
	if err := after.Invalidate(9); err != nil {
		t.Fatalf("restore generation: %v", err)
	}
	for _, subject := range subjects {
		_, err := after.Resolve(subject, want(subject), now.Add(time.Minute))
		if errors.Is(err, ErrNoDecision) {
			t.Fatalf("restart forgot batch subject %s: resolved as never-handshook", subject)
		}
		if !errors.Is(err, ErrStaleGeneration) {
			t.Fatalf("Resolve(%s) after restart = %v, want ErrStaleGeneration", subject, err)
		}
	}
}

func TestDerivedAcceptRefreshesGenerationInvalidation(t *testing.T) {
	now := time.Now().UTC()
	journal := newMemoryJournal()
	before := journalRegistry(journal)
	if err := before.Invalidate(5); err != nil {
		t.Fatalf("set generation: %v", err)
	}
	key := durabilityKey("session:embedded", string(SurfaceCoreWebUI))
	key.Generation = 5
	if _, accepted, err := before.AcceptDerived(
		key,
		durabilityContract(string(SurfaceCoreWebUI)),
		Result{Decision: DecisionCompatible},
		now,
		time.Minute,
	); err != nil || !accepted {
		t.Fatalf("AcceptDerived = (%t, %v), want (true, nil)", accepted, err)
	}
	if err := before.Invalidate(6); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	after := journalRegistry(journal)
	if err := after.Invalidate(6); err != nil {
		t.Fatalf("restore generation: %v", err)
	}
	key.Generation = 6
	if _, accepted, err := after.AcceptDerived(
		key,
		durabilityContract(string(SurfaceCoreWebUI)),
		Result{Decision: DecisionCompatible},
		now.Add(time.Second),
		time.Minute,
	); err != nil || !accepted {
		t.Fatalf("fresh AcceptDerived = (%t, %v), want (true, nil)", accepted, err)
	}
	if count := journal.count(); count != 0 {
		t.Fatalf("journal holds %d tombstones after derived refresh, want 0", count)
	}

	restarted := journalRegistry(journal)
	if err := restarted.Invalidate(6); err != nil {
		t.Fatalf("restore refreshed generation: %v", err)
	}
	if _, err := restarted.Resolve(key.Subject, ResolveKey{Subject: key.Subject, Generation: 6}, now.Add(2*time.Second)); !errors.Is(err, ErrNoDecision) {
		t.Fatalf("Resolve after derived refresh and restart = %v, want ErrNoDecision", err)
	}
}
