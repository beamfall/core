package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

var errCompatRefusalStoreWriteOutage = errors.New("compatibility store writes unavailable")

type compatRefusalWriteOutageStore struct {
	failWrites atomic.Bool
	putCalls   atomic.Int64
	stageCalls atomic.Int64
}

func newCompatRefusalWriteOutageStore() *compatRefusalWriteOutageStore {
	store := &compatRefusalWriteOutageStore{}
	store.failWrites.Store(true)
	return store
}

func (s *compatRefusalWriteOutageStore) PutCompatDecisionInvalidation(
	context.Context, store.CompatDecisionInvalidation, time.Time,
) error {
	s.putCalls.Add(1)
	if s.failWrites.Load() {
		return errCompatRefusalStoreWriteOutage
	}
	return nil
}

func (s *compatRefusalWriteOutageStore) StageCompatDecisionInvalidation(
	context.Context, store.CompatDecisionInvalidation, time.Time,
) error {
	s.stageCalls.Add(1)
	if s.failWrites.Load() {
		return errCompatRefusalStoreWriteOutage
	}
	return nil
}

func (s *compatRefusalWriteOutageStore) StageCompatDecisionInvalidations(
	context.Context, []store.CompatDecisionInvalidation, time.Time,
) error {
	return errCompatRefusalStoreWriteOutage
}

func (s *compatRefusalWriteOutageStore) CommitStagedCompatDecisionInvalidations(context.Context) error {
	return errCompatRefusalStoreWriteOutage
}

func (s *compatRefusalWriteOutageStore) DeleteCompatDecisionInvalidation(context.Context, string) error {
	if s.failWrites.Load() {
		return errCompatRefusalStoreWriteOutage
	}
	return nil
}

func (*compatRefusalWriteOutageStore) CompatDecisionInvalidations(
	context.Context, time.Time,
) ([]store.CompatDecisionInvalidation, error) {
	return nil, nil
}

func TestRefusalFileSurvivesRestartDuringTotalStoreWriteOutage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	primary := newCompatRefusalWriteOutageStore()
	journalDir := filepath.Join(t.TempDir(), "compat-refusals")
	journal := compatInvalidationJournal{
		store:    primary,
		refusals: newCompatRefusalFileJournal(journalDir),
	}
	before := registryWithCompatJournal(t, journal, now)
	subject := "session:refused-during-store-outage"
	surface := string(capabilities.SurfaceMacOSManager)
	contract := capabilities.EmbeddedUIContract()
	contract.Surface = surface
	contract.BuildID = "refused-build"

	_, accepted, err := before.Accept(
		capabilities.BindingKey{Subject: subject, Surface: surface},
		contract,
		capabilities.Result{
			Decision:   capabilities.DecisionClientTooOld,
			ReasonCode: capabilities.ReasonRevokedBuild,
		},
		now,
		time.Hour,
	)
	if accepted || err != nil {
		t.Fatalf("Accept during total store write outage = (%t, %v), want reported refusal (false, nil)", accepted, err)
	}
	if got := primary.putCalls.Load(); got != 1 {
		t.Fatalf("primary store writes = %d, want 1 failed attempt", got)
	}
	if got := primary.stageCalls.Load(); got != 1 {
		t.Fatalf("staged store writes = %d, want 1 failed attempt", got)
	}
	files, err := os.ReadDir(journalDir)
	if err != nil {
		t.Fatalf("read refusal journal: %v", err)
	}
	if len(files) != 1 || files[0].Name() != compatRefusalFileName(subject) {
		t.Fatalf("refusal journal files = %v, want the durable subject record", files)
	}

	// Rebuild every process-local object while the store remains unable to
	// write. The refusal file is the only state carried across the restart.
	after := registryWithCompatJournal(t, journal, now.Add(time.Minute))
	record, err := after.Resolve(subject, capabilities.ResolveKey{Subject: subject}, now.Add(time.Minute))
	if errors.Is(err, capabilities.ErrNoDecision) {
		t.Fatal("restart forgot the refusal and reported the subject as unknown")
	}
	if !errors.Is(err, capabilities.ErrInvalidated) {
		t.Fatalf("Resolve after restart = %v, want ErrInvalidated", err)
	}
	if record.Result.Decision != capabilities.DecisionClientTooOld || record.Result.ReasonCode != capabilities.ReasonRevokedBuild {
		t.Fatalf("rehydrated refusal = %+v, want the recorded decision and reason", record.Result)
	}
}

func TestRefusalFileFailureRemainsFailClosed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	primary := newCompatRefusalWriteOutageStore()
	journalDir := filepath.Join(t.TempDir(), "compat-refusals")
	journal := compatInvalidationJournal{
		store:    primary,
		refusals: newCompatRefusalFileJournal(journalDir),
	}
	registry := registryWithCompatJournal(t, journal, now)
	if err := os.WriteFile(journalDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("block refusal journal directory: %v", err)
	}
	t.Cleanup(func() {
		primary.failWrites.Store(false)
		if err := os.Remove(journalDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("repair refusal journal path: %v", err)
			return
		}
		if err := os.MkdirAll(journalDir, 0o700); err != nil {
			t.Errorf("repair refusal journal directory: %v", err)
			return
		}
		_, _ = registry.Resolve("session:no-secondary-medium", capabilities.ResolveKey{Subject: "session:no-secondary-medium"}, now)
	})

	surface := string(capabilities.SurfaceMacOSManager)
	contract := capabilities.EmbeddedUIContract()
	contract.Surface = surface
	_, accepted, err := registry.Accept(
		capabilities.BindingKey{Subject: "session:no-secondary-medium", Surface: surface},
		contract,
		capabilities.Result{
			Decision:   capabilities.DecisionClientTooOld,
			ReasonCode: capabilities.ReasonRevokedBuild,
		},
		now,
		time.Hour,
	)
	if accepted || !errors.Is(err, capabilities.ErrInvalidationJournalUnavailable) {
		t.Fatalf("Accept with both durable media unavailable = (%t, %v), want fail-closed durability error", accepted, err)
	}
	if _, err := registry.Resolve(
		"session:no-secondary-medium",
		capabilities.ResolveKey{Subject: "session:no-secondary-medium"},
		now,
	); !errors.Is(err, capabilities.ErrInvalidationJournalUnavailable) {
		t.Fatalf("Resolve with an unwritten refusal = %v, want ErrInvalidationJournalUnavailable", err)
	}

	// A restart while the configured secondary medium itself is unreadable also
	// fails closed globally; it never converts the missing record to ErrNoDecision.
	restarted := capabilities.NewRegistry()
	restarted.SetInvalidationJournal(journal, time.Hour)
	if err := restarted.LoadInvalidations(context.Background(), now); !errors.Is(err, capabilities.ErrInvalidationJournalUnavailable) {
		t.Fatalf("restart load with unreadable refusal medium = %v, want ErrInvalidationJournalUnavailable", err)
	}
	if _, err := restarted.Resolve(
		"session:no-secondary-medium",
		capabilities.ResolveKey{Subject: "session:no-secondary-medium"},
		now,
	); !errors.Is(err, capabilities.ErrInvalidationJournalUnavailable) {
		t.Fatalf("restart resolve with unreadable refusal medium = %v, want ErrInvalidationJournalUnavailable", err)
	}
}

func registryWithCompatJournal(t *testing.T, journal capabilities.InvalidationJournal, now time.Time) *capabilities.Registry {
	t.Helper()
	registry := capabilities.NewRegistry()
	registry.SetInvalidationJournal(journal, time.Hour)
	if err := registry.LoadInvalidations(context.Background(), now); err != nil {
		t.Fatalf("LoadInvalidations: %v", err)
	}
	return registry
}
