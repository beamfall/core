package capabilities

import (
	"context"
	"errors"
	"time"
)

// Durability for compatibility decisions.
//
// The registry holds decisions in memory and is built empty at every boot. For
// an ACCEPTED decision that remains live, losing it costs one extra handshake.
// Losing a refusal or a generation advance is fail-open. Before this file, a
// restart erased those invalidations and Resolve reported them as ErrNoDecision
// — indistinguishable from a subject that never handshook. The Stream Token
// verify seam treats "no decision" as benign, so an already-issued unexpired
// PRE-BINDING (unstamped) token for an invalidated subject became usable again
// for as long as the signing key stayed active.
//
// So only invalidations are made durable, and the two states are separated:
//
//	genuinely unknown -> ErrNoDecision  (unbound behavior; the token still serves)
//	known-invalid     -> invalidation error (the verify seam rejects)
//
// Every tombstone carries its prior result. Refusals are non-servable and map to
// ErrInvalidated; records made stale by a generation advance were necessarily
// servable and map to ErrStaleGeneration. That existing closed-vocabulary fact
// distinguishes the causes without a schema column or a synthesized reason.
// Persisting full acceptances would add a hot-path write; the bounded tombstone
// set is the narrowest thing that closes both invalidation paths.
//
// DESIGN: Persist only refusal and generation-invalidation tombstones, not full
// decisions. The primary journal uses the compat_decision_invalidation and
// compat_decision_invalidation_staged tables created by
// internal/store/migrate/catalog/0106_compat_decision_invalidation.sql and
// 0107_compat_decision_invalidation_staged.sql. Both tables carry expires_at_ms;
// CompatDecisionInvalidations promotes staged rows before
// pruneCompatDecisionInvalidationsLocked removes expired rows, and
// PutCompatDecisionInvalidation runs the same prune on direct writes
// (internal/store/compat_decision.go), bounding the retained set. Refusals also
// have an independent, refusal-only write-ahead medium under the data directory
// (internal/server/compat_refusal_journal.go). Its synced per-subject files are
// the fallback commit point when both catalog writes fail, and carry the same
// expiry bound.
// Registry.Resolve in clientcompat_binding.go returns ErrNoDecision only for a
// genuinely unknown subject and ErrInvalidated for a known-invalid subject with
// a live refusal tombstone, including after restart; generation tombstones
// retain ErrStaleGeneration. compatStreamTokenResolver.Resolve in
// internal/server/compat_streamtoken_adapter.go preserves that distinction, so
// a subject with a live tombstone is never reported as unknown.

var (
	// ErrInvalidated means the subject HAD a compatibility decision and it was
	// refused. It is deliberately distinct from ErrNoDecision: it says the server
	// knows the subject is bad and rejects even a token with no decision stamp.
	// Generation advances use the existing ErrStaleGeneration rejection.
	ErrInvalidated = errors.New("compatibility decision was invalidated")

	// ErrInvalidationJournalUnavailable means a journal is configured but its
	// durable set has not been read yet. Every resolve fails with it until the
	// read succeeds: answering from a half-loaded set would report an
	// invalidated subject as unknown, which is exactly the failure this file
	// exists to prevent. It clears itself as soon as one load succeeds.
	ErrInvalidationJournalUnavailable = errors.New("compatibility invalidation journal is unavailable")
)

const (
	// InvalidationRetention is the preferred bounded lifetime of an invalidation
	// tombstone. InvalidationRetentionFor raises it if the maximum mintable
	// Stream Token lifetime ever requires a longer floor.
	InvalidationRetention = 8 * 24 * time.Hour
	// InvalidationSafetyMargin keeps a tombstone live after the last token minted
	// before that invalidation must have expired.
	InvalidationSafetyMargin = 24 * time.Hour
	// pendingRefusalRetryInterval bounds how long a recovered journal waits before
	// the process retries a refusal that direct and staged writes both rejected.
	pendingRefusalRetryInterval = 100 * time.Millisecond
)

// InvalidationRetentionFor derives the tombstone lifetime from the signer
// bound. This is the single coupling point: raising the maximum mintable token
// lifetime necessarily raises durable invalidation retention with it.
func InvalidationRetentionFor(maxStreamTokenTTL time.Duration) time.Duration {
	return max(InvalidationRetention, maxStreamTokenTTL+InvalidationSafetyMargin)
}

// Invalidation is one subject's durable compatibility-decision tombstone.
type Invalidation struct {
	Subject    string
	Decision   Decision
	ReasonCode ReasonCode
	RecordedAt time.Time
	ExpiresAt  time.Time
}

// refused distinguishes a policy refusal from a generation invalidation. The
// existing store shape needs no cause column: Accept creates refusal tombstones
// only for non-servable results, while generation invalidation starts only from
// accepted, necessarily servable records.
func (i Invalidation) refused() bool { return !i.Result().Servable() }

// Live reports whether the tombstone still applies at now.
func (i Invalidation) Live(now time.Time) bool { return now.Before(i.ExpiresAt) }

// Result renders the subject's decision immediately before invalidation.
func (i Invalidation) Result() Result {
	return Result{Decision: i.Decision, ReasonCode: i.ReasonCode}
}

// InvalidationJournal is the durable home of the invalidated-subject set. The
// composition root supplies it; the registry owns when it is read and written.
//
// Writes must be synchronous and durable on return: the registry calls Record
// before an invalidation returns, so that a crash in that instant cannot lose
// the invalidation.
type InvalidationJournal interface {
	// LoadInvalidations returns the tombstones still in force at now, and is
	// expected to prune the expired ones.
	LoadInvalidations(ctx context.Context, now time.Time) ([]Invalidation, error)
	// RecordInvalidation durably stores (or refreshes) one tombstone.
	RecordInvalidation(ctx context.Context, entry Invalidation) error
	// StageInvalidation durably stages one pending tombstone that the next
	// LoadInvalidations promotes into the tombstone set. It is the fallback
	// commit point for a refusal whose direct RecordInvalidation write failed:
	// once it returns nil the refusal survives a restart, so the caller may
	// report the refusal outcome even though the direct write did not land.
	StageInvalidation(ctx context.Context, entry Invalidation) error
	// RecordInvalidationBatch durably stores a whole invalidation batch with
	// all-or-nothing semantics across process death: once it returns nil, every
	// entry survives a restart, and no crash point may leave a strict subset of
	// the batch durable. The store keeps that promise by staging the batch in
	// one transaction and promoting it in another, replaying any staged batch
	// on the next LoadInvalidations.
	RecordInvalidationBatch(ctx context.Context, entries []Invalidation) error
	// ClearInvalidation removes a subject's tombstone.
	ClearInvalidation(ctx context.Context, subject string) error
}

// SetInvalidationJournal attaches the durable tombstone journal and derives its
// retention from the signer's maximum token lifetime. It is a boot-time setter,
// called before the process serves. The set itself is read lazily on first use
// and retried until it succeeds, so wiring order can never leave a window in
// which the registry answers from an unloaded set.
func (r *Registry) SetInvalidationJournal(journal InvalidationJournal, maxStreamTokenTTL time.Duration) {
	r.mu.Lock()
	r.journal = journal
	r.journalLoaded = journal == nil
	r.invalidationRetention = InvalidationRetentionFor(maxStreamTokenTTL)
	r.mu.Unlock()
}

// LoadInvalidations eagerly reads the durable set. Boot calls it so the normal
// path starts warm; a failure here is not fatal because ensureInvalidations
// retries, but until it succeeds every resolve fails closed.
func (r *Registry) LoadInvalidations(ctx context.Context, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadInvalidationsLocked(ctx, now)
}

// Invalidations returns the tombstones in force at now, sorted by nothing in
// particular — callers use it to distinguish known-invalid from unknown.
func (r *Registry) Invalidations(now time.Time) []Invalidation {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneInvalidationsLocked(now)
	live := make([]Invalidation, 0, len(r.invalidated))
	for _, entry := range r.invalidated {
		live = append(live, entry)
	}
	return live
}

// ensureInvalidations settles the durable set before any resolve or accept
// answers from it: it loads the set if it has not been read yet, retries a
// pending refusal whose journal writes failed, and retries a generation
// invalidation whose journal write failed. The fast path is a read-locked flag
// check, so a warm registry pays nothing.
//
// Every fully-unsettled state fails every resolve, for the same reason. An
// unread set would report an invalidated subject as unknown; an unjournaled
// generation advance would report the subject's PRE-ADVANCE decision as live;
// an unstaged refusal would report a refusal outcome a restart forgets. Each
// answer is the fail-open one this file exists to prevent, so the caller gets
// the durability error until the journal catches up. The one pending state
// that does NOT fail resolves is a STAGED refusal: its staged row already
// survives a restart, so only its promotion is still being retried.
func (r *Registry) ensureInvalidations(now time.Time) error {
	r.mu.RLock()
	settled := (r.journalLoaded || r.journal == nil) &&
		!r.pending.set && len(r.pendingRefusals) == 0
	r.mu.RUnlock()
	if settled {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadInvalidationsLocked(context.Background(), now); err != nil {
		return err
	}
	r.flushPendingRefusalsLocked()
	if err := r.commitPendingInvalidationLocked(); err != nil {
		return err
	}
	return r.unstagedRefusalDebtLocked()
}

// unstagedRefusalDebtLocked fails every resolve and accept while any refusal
// tombstone exists ONLY in process memory — recorded, but with both its direct
// journal write and its staged fallback write failed. Such a refusal is a fact
// this process enforces and a restart forgets, so no caller may be handed an
// outcome derived from it; a STAGED pending refusal is already durable and
// carries no debt.
func (r *Registry) unstagedRefusalDebtLocked() error {
	for _, pending := range r.pendingRefusals {
		if !pending.staged {
			return ErrInvalidationJournalUnavailable
		}
	}
	return nil
}

// pendingInvalidation is a generation advance that has been computed but not yet
// durably journaled. It is deliberately state rather than a returned error: the
// caller of Invalidate is policy publication, which has nowhere to put a
// failure, so the registry itself holds the advance uncommitted and refuses to
// answer from the superseded decisions until the write lands.
type pendingInvalidation struct {
	generation    int
	invalidations []Invalidation
	set           bool
}

// commitPendingInvalidationLocked is the single commit point of a generation
// advance. The tombstones are journaled FIRST; only a durable write publishes
// the generation and its in-memory tombstones together. A failed write commits
// nothing and leaves the pending state in place, so the advance is retried on
// the next resolve or accept while every one of them fails closed.
func (r *Registry) commitPendingInvalidationLocked() error {
	if !r.pending.set {
		return nil
	}
	if err := r.persistInvalidationsLocked(r.pending.invalidations); err != nil {
		return err
	}
	if r.invalidated == nil {
		r.invalidated = make(map[string]Invalidation)
	}
	for _, invalidation := range r.pending.invalidations {
		r.invalidated[invalidation.Subject] = invalidation
	}
	r.generation = r.pending.generation
	r.generationSet = true
	r.pending = pendingInvalidation{}
	return nil
}

func (r *Registry) loadInvalidationsLocked(ctx context.Context, now time.Time) error {
	if r.journal == nil {
		r.journalLoaded = true
		return nil
	}
	if r.journalLoaded {
		return nil
	}
	entries, err := r.journal.LoadInvalidations(ctx, now)
	if err != nil {
		return errors.Join(ErrInvalidationJournalUnavailable, err)
	}
	r.invalidated = make(map[string]Invalidation, len(entries))
	for _, entry := range entries {
		if entry.Subject != "" && entry.Live(now) {
			r.invalidated[entry.Subject] = entry
		}
	}
	r.journalLoaded = true
	return nil
}

// liveInvalidationLocked returns the subject's tombstone if one is still in
// force, dropping an expired one as it goes so the in-memory set stays bounded
// between rehydrations.
func (r *Registry) liveInvalidationLocked(subject string, now time.Time) (Invalidation, bool) {
	entry, ok := r.invalidated[subject]
	if !ok {
		return Invalidation{}, false
	}
	if !entry.Live(now) {
		delete(r.invalidated, subject)
		return Invalidation{}, false
	}
	return entry, true
}

func (r *Registry) pruneInvalidationsLocked(now time.Time) {
	for subject, entry := range r.invalidated {
		if !entry.Live(now) {
			delete(r.invalidated, subject)
		}
	}
}

// recordInvalidationLocked marks a subject invalid in memory FIRST and only then
// writes the journal. The order matters: the running process is already at
// least as strict as it was before, so a journal outage can never make an
// invalidation weaker in-process. A failed direct write falls back to staging
// (stagePendingRefusalLocked): nil is returned only once the refusal is durable
// through one write or the other, and if both fail the caller gets the
// durability error and the fail-closed debt keeps any other caller from being
// handed the memory-only refusal.
//
// Journal I/O happens under the registry's write lock on purpose. Invalidation
// writes are rare, and holding the lock makes "record tombstone" and "clear
// tombstone" atomic against each other — otherwise a concurrent accept could
// delete a tombstone a newer invalidation had just written.
func (r *Registry) recordInvalidationLocked(subject string, result Result, now time.Time) error {
	if subject == "" {
		return nil
	}
	retention := r.invalidationRetention
	if retention <= 0 {
		retention = InvalidationRetention
	}
	r.pruneInvalidationsLocked(now)
	entry := Invalidation{
		Subject:    subject,
		Decision:   result.Decision,
		ReasonCode: result.ReasonCode,
		RecordedAt: now,
		ExpiresAt:  now.Add(retention),
	}
	if r.invalidated == nil {
		r.invalidated = make(map[string]Invalidation)
	}
	r.invalidated[subject] = entry
	if r.journal == nil {
		return nil
	}
	if err := r.journal.RecordInvalidation(context.Background(), entry); err != nil {
		return r.stagePendingRefusalLocked(entry, err)
	}
	return nil
}

// pendingRefusal is a refusal tombstone whose direct journal write failed.
// staged records whether the fallback staged write landed: a staged refusal is
// durable (rehydration replays the staged row), so its outcome may be reported
// and the remaining retries only promote it; an unstaged one exists solely in
// memory, so every resolve and accept fails closed until a retry stages it.
type pendingRefusal struct {
	entry  Invalidation
	staged bool
}

// stagePendingRefusalLocked is the fallback durable commit point of a refusal:
// the direct tombstone write failed, so the entry is staged instead. A staged
// entry is durable — the composition root's implementation first commits it to
// the independent refusal file, then best-effort stages the catalog row — so
// the caller gets nil and may report the refusal outcome. If even the secondary
// medium fails, the caller gets the durability error and the process starts
// retrying through that same flush. The exact contract is: restart forgets only
// state that was never caller-visible; every caller-visible refusal has a
// durable record that precedes it.
func (r *Registry) stagePendingRefusalLocked(entry Invalidation, cause error) error {
	if r.pendingRefusals == nil {
		r.pendingRefusals = make(map[string]pendingRefusal)
	}
	if stageErr := r.journal.StageInvalidation(context.Background(), entry); stageErr != nil {
		r.pendingRefusals[entry.Subject] = pendingRefusal{entry: entry}
		r.startPendingRefusalRetryLocked()
		return errors.Join(ErrInvalidationJournalUnavailable, cause, stageErr)
	}
	r.pendingRefusals[entry.Subject] = pendingRefusal{entry: entry, staged: true}
	return nil
}

// startPendingRefusalRetryLocked starts one process-owned retry worker. The
// worker reuses ensureInvalidations so background and caller-driven retries have
// one ordering contract: stage first, then promote. It stops once rehabilitation
// or a successful flush removes every pending refusal; while debt remains it
// deliberately lives for the process, because stopping would restore the
// caller-action dependency this retry closes.
func (r *Registry) startPendingRefusalRetryLocked() {
	if r.refusalRetryRunning {
		return
	}
	r.refusalRetryRunning = true
	go r.retryPendingRefusals()
}

func (r *Registry) retryPendingRefusals() {
	ticker := time.NewTicker(pendingRefusalRetryInterval)
	defer ticker.Stop()
	for range ticker.C {
		_ = r.ensureInvalidations(time.Now())
		r.mu.Lock()
		if len(r.pendingRefusals) == 0 {
			r.refusalRetryRunning = false
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
	}
}

// flushPendingRefusalsLocked retries pending refusal tombstones on every
// resolve and accept until each one lands in the tombstone table. An unstaged
// entry is staged first — that write is what clears the fail-closed debt — and
// a staged entry is promoted with the direct write; a leftover staged row after
// promotion is harmless, because replay upserts the same tombstone and a
// durable rehabilitation deletes both rows together.
func (r *Registry) flushPendingRefusalsLocked() {
	if len(r.pendingRefusals) == 0 || r.journal == nil {
		return
	}
	for subject, pending := range r.pendingRefusals {
		if !pending.staged {
			if err := r.journal.StageInvalidation(context.Background(), pending.entry); err != nil {
				continue
			}
			pending.staged = true
			r.pendingRefusals[subject] = pending
		}
		if err := r.journal.RecordInvalidation(context.Background(), pending.entry); err != nil {
			continue
		}
		delete(r.pendingRefusals, subject)
	}
}

func (r *Registry) generationInvalidationsLocked(generation int, now time.Time) []Invalidation {
	r.pruneInvalidationsLocked(now)
	retention := r.invalidationRetention
	if retention <= 0 {
		retention = InvalidationRetention
	}
	invalidations := make([]Invalidation, 0, len(r.records))
	for _, record := range r.records {
		if record.Key.Generation == generation {
			continue
		}
		invalidations = append(invalidations, Invalidation{
			Subject:    record.Key.Subject,
			Decision:   record.Result.Decision,
			ReasonCode: record.Result.ReasonCode,
			RecordedAt: now,
			ExpiresAt:  now.Add(retention),
		})
	}
	return invalidations
}

// persistInvalidationsLocked journals a generation batch through the single
// atomically-durable batch write. Per-row writes would reopen the gap this
// exists to close: process death between independent row transactions
// rehydrates only the committed rows, and an omitted invalid subject reads
// back as ErrNoDecision. The batch contract (RecordInvalidationBatch) forbids
// any crash point that leaves a strict subset of the batch durable.
func (r *Registry) persistInvalidationsLocked(invalidations []Invalidation) error {
	if r.journal == nil || len(invalidations) == 0 {
		return nil
	}
	if err := r.journal.RecordInvalidationBatch(context.Background(), invalidations); err != nil {
		return errors.Join(ErrInvalidationJournalUnavailable, err)
	}
	return nil
}

// clearInvalidationLocked drops a subject's tombstone durably before dropping it
// in memory. This order is the mirror of recordInvalidationLocked: a failed
// durable clear leaves the tombstone in place, which refuses the subject again
// after a restart — the safe direction — and the caller is told so it can fail
// the handshake rather than report a clean acceptance it could not persist.
func (r *Registry) clearInvalidationLocked(subject string) error {
	if subject == "" {
		return nil
	}
	// The loaded in-memory set is authoritative, so a subject with no tombstone
	// costs no journal write. Provisional accepts run on the request path and
	// must not pay a DELETE each time.
	if _, ok := r.invalidated[subject]; !ok {
		return nil
	}
	if r.journal != nil {
		if err := r.journal.ClearInvalidation(context.Background(), subject); err != nil {
			return errors.Join(ErrInvalidationJournalUnavailable, err)
		}
	}
	delete(r.invalidated, subject)
	// A successful durable clear also retires any pending retry for the same
	// subject's refusal: the rehabilitation supersedes it, and replaying the
	// stale refusal later would durably refuse a subject the server just
	// accepted.
	delete(r.pendingRefusals, subject)
	return nil
}
