package capabilities

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"
)

var (
	ErrNoDecision          = errors.New("no compatibility decision")
	ErrWrongSurface        = errors.New("compatibility decision belongs to another surface")
	ErrDistributionChanged = errors.New("compatibility decision distribution changed")
	ErrStaleGeneration     = errors.New("compatibility decision generation is stale")
	ErrAuthChanged         = errors.New("compatibility decision authentication changed")
	ErrExpired             = errors.New("compatibility decision expired")
)

// BindingKey contains only server-derived binding inputs.
type BindingKey struct {
	Subject         string
	Surface         string
	Distribution    Distribution
	AuthFingerprint string
	Generation      int
}

// Record is a server-held compatibility decision.
type Record struct {
	Key          BindingKey
	Contract     ClientContract
	ContractHash string
	Submitted    bool
	Result       Result
	DecisionID   string
	ExpiresAt    time.Time
}

// ResolveKey contains the current server-derived binding inputs.
type ResolveKey struct {
	Subject         string
	Distribution    Distribution
	AuthFingerprint string
	Generation      int
	DerivedSurface  string
}

// Registry stores at most one compatibility decision per subject, plus durable
// tombstones for refused decisions and decisions invalidated by a generation
// advance (clientcompat_durability.go). Records are in-memory and die with the
// process; tombstones survive it, so an invalidated subject stays invalidated.
type Registry struct {
	mu                    sync.RWMutex
	records               map[string]Record
	invalidated           map[string]Invalidation
	journal               InvalidationJournal
	journalLoaded         bool
	pending               pendingInvalidation
	pendingRefusals       map[string]pendingRefusal
	refusalRetryRunning   bool
	invalidationRetention time.Duration
	nextID                uint64
	generation            int
	generationSet         bool
}

// NewRegistry returns an empty decision registry with no durable journal. Until
// SetInvalidationJournal attaches one, invalidations are remembered only in
// memory.
func NewRegistry() *Registry {
	return &Registry{
		records:               make(map[string]Record),
		invalidated:           make(map[string]Invalidation),
		journalLoaded:         true,
		invalidationRetention: InvalidationRetention,
	}
}

// Accept atomically replaces a subject's record for a servable submitted result,
// or deletes the prior record for a refusal.
func (r *Registry) Accept(
	key BindingKey,
	contract ClientContract,
	result Result,
	now time.Time,
	ttl time.Duration,
) (Record, bool, error) {
	return r.accept(key, contract, result, now, ttl, true)
}

// AcceptDerived is Accept for a server-derived, non-submitted contract.
func (r *Registry) AcceptDerived(
	key BindingKey,
	contract ClientContract,
	result Result,
	now time.Time,
	ttl time.Duration,
) (Record, bool, error) {
	return r.accept(key, contract, result, now, ttl, false)
}

func (r *Registry) accept(
	key BindingKey,
	contract ClientContract,
	result Result,
	now time.Time,
	ttl time.Duration,
	submitted bool,
) (Record, bool, error) {
	contract = NormalizeClientContract(contract)

	if err := r.ensureInvalidations(now); err != nil {
		return Record{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureRecords()
	if key.Surface != contract.Surface {
		return Record{}, false, ErrWrongSurface
	}
	if r.generationSet && key.Generation != r.generation {
		return Record{}, false, ErrStaleGeneration
	}
	// Only a client-submitted handshake can rehabilitate a REFUSED subject. A
	// server-derived decision may refresh a generation tombstone after evaluating
	// the current policy, but it must not erase a prior refusal after restart.
	if invalidation, invalid := r.liveInvalidationLocked(key.Subject, now); invalid {
		if invalidation.refused() && !submitted {
			return Record{}, false, ErrInvalidated
		}
	}
	r.nextID++
	record := Record{
		Key:          key,
		Contract:     contract,
		ContractHash: ContractHash(contract, key.Distribution),
		Submitted:    submitted,
		Result:       cloneResult(result),
		DecisionID:   decisionID(key, contract, now, r.nextID),
		ExpiresAt:    now.Add(ttl),
	}
	if !result.Servable() {
		delete(r.records, key.Subject)
		// The refusal becomes durable BEFORE the caller can report it, so a crash
		// in that instant cannot resurrect the subject's already-issued tokens.
		if err := r.recordInvalidationLocked(key.Subject, result, now); err != nil {
			return cloneRecord(record), false, err
		}
		return cloneRecord(record), false, nil
	}
	// A servable submitted decision rehabilitates either tombstone kind; a fresh
	// derived decision can reach this point only for a generation tombstone. The
	// durable clear goes first, so an acceptance we could not persist is reported
	// as an error instead of as a clean pass.
	if err := r.clearInvalidationLocked(key.Subject); err != nil {
		return Record{}, false, err
	}
	r.records[key.Subject] = cloneRecord(record)
	return cloneRecord(record), true, nil
}

// Resolve validates a subject's stored decision against current server-derived
// inputs. It returns the matched record with any mismatch error so callers can
// distinguish submitted records from server-derived provisional records.
//
// A subject carrying a live tombstone resolves to ErrInvalidated (refusal) or
// ErrStaleGeneration (generation advance), never ErrNoDecision. That distinction
// is what survives a restart: ErrNoDecision means the server has never heard of
// this subject and its unstamped tokens keep the established unbound behavior;
// either invalidation error rejects every token of the known-invalid subject.
func (r *Registry) Resolve(subject string, want ResolveKey, now time.Time) (Record, error) {
	if err := r.ensureInvalidations(now); err != nil {
		return Record{}, err
	}
	r.mu.RLock()
	invalidation, invalid := r.invalidated[subject]
	record, ok := r.records[subject]
	generation := r.generation
	generationSet := r.generationSet
	r.mu.RUnlock()
	// A refusal always outranks an in-memory record. A generation tombstone
	// coexists with the stale record until restart so this process can preserve
	// the record-level mismatch precedence; after restart the tombstone has no
	// record beside it and reports ErrStaleGeneration instead of ErrNoDecision.
	if invalid {
		if !invalidation.Live(now) {
			r.mu.Lock()
			r.liveInvalidationLocked(subject, now)
			r.mu.Unlock()
			return r.Resolve(subject, want, now)
		}
		if invalidation.refused() {
			return Record{
				Key:    BindingKey{Subject: subject},
				Result: invalidation.Result(),
			}, ErrInvalidated
		}
		if !ok {
			return Record{
				Key:    BindingKey{Subject: subject},
				Result: invalidation.Result(),
			}, ErrStaleGeneration
		}
	}
	if !ok {
		return Record{}, ErrNoDecision
	}
	record = cloneRecord(record)
	if want.Subject != "" && want.Subject != subject {
		return Record{}, ErrNoDecision
	}
	if record.Key.Subject != subject {
		return Record{}, ErrNoDecision
	}
	if record.Key.Distribution != want.Distribution {
		return record, ErrDistributionChanged
	}
	if record.Key.AuthFingerprint != want.AuthFingerprint {
		return record, ErrAuthChanged
	}
	if !now.Before(record.ExpiresAt) {
		return record, ErrExpired
	}
	if generationSet && record.Key.Generation != generation {
		return record, ErrStaleGeneration
	}
	if !generationSet && record.Key.Generation != want.Generation {
		return record, ErrStaleGeneration
	}
	if want.DerivedSurface != "" && record.Key.Surface != want.DerivedSurface {
		return record, ErrWrongSurface
	}
	return record, nil
}

// Invalidate durably tombstones every record made stale by generation before it
// publishes the generation. Resolve therefore reports ErrStaleGeneration both
// before and after restart.
//
// The durable write is the single commit point: until it lands, the generation
// advance is held as pending state (commitPendingInvalidationLocked) and every
// resolve fails closed instead of answering from the pre-advance decision. A
// later Invalidate supersedes a pending one — the generation it could not
// publish was never observable, and the stale set is recomputed from the same
// unchanged records.
func (r *Registry) Invalidate(generation int) error {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadInvalidationsLocked(context.Background(), now); err != nil {
		return err
	}
	r.pending = pendingInvalidation{
		generation:    generation,
		invalidations: r.generationInvalidationsLocked(generation, now),
		set:           true,
	}
	return r.commitPendingInvalidationLocked()
}

func (r *Registry) ensureRecords() {
	if r.records == nil {
		r.records = make(map[string]Record)
	}
}

func decisionID(key BindingKey, contract ClientContract, now time.Time, sequence uint64) string {
	sum := sha256.Sum256([]byte(
		key.Subject + "\x00" +
			key.Surface + "\x00" +
			key.AuthFingerprint + "\x00" +
			strconv.Itoa(key.Generation) + "\x00" +
			ContractHash(contract, key.Distribution) + "\x00" +
			now.UTC().Format(time.RFC3339Nano) + "\x00" +
			strconv.FormatUint(sequence, 10),
	))
	return hex.EncodeToString(sum[:])
}

func cloneRecord(record Record) Record {
	record.Contract = NormalizeClientContract(record.Contract)
	record.Result = cloneResult(record.Result)
	return record
}

func cloneResult(result Result) Result {
	result.DisabledCapabilities = append([]string(nil), result.DisabledCapabilities...)
	return result
}
