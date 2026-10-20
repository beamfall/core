package server

import (
	"context"
	"encoding/binary"
	"hash/fnv"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

const (
	compatDecisionTTL    = 24 * time.Hour
	compatProvisionalTTL = 30 * time.Second
)

type compatState struct {
	mu               sync.RWMutex
	policy           capabilities.Policy
	buildRevision    string
	pluginGeneration int
	registry         *capabilities.Registry
	refused          map[string]struct{}
	now              func() time.Time
}

// CompatGenerationInputs are the durable inputs whose change invalidates every
// previously accepted compatibility decision.
type CompatGenerationInputs struct {
	BuildRevision              string
	CoreRevisions              [2]capabilities.Revision
	RenderingRevisions         [2]capabilities.Revision
	RequiredRenderingStamps    []string
	RequiredVocabularies       []string
	KnownCapabilities          map[string]bool
	SupportedDeveloperAPIMajor int
	RevokedBuilds              map[string]bool
	BuildCatalogDigest         string
	EmbeddedUIRevision         capabilities.Revision
	PluginPolicyGeneration     int
}

// CompatGeneration returns a stable digest of every compatibility-policy input.
func CompatGeneration(inputs CompatGenerationInputs) int {
	h := fnv.New64a()
	writeGenerationValue(h, "build-revision", inputs.BuildRevision)
	writeGenerationRange(h, "core-revisions", inputs.CoreRevisions)
	writeGenerationRange(h, "rendering-revisions", inputs.RenderingRevisions)
	writeGenerationStrings(h, "required-rendering-stamps", inputs.RequiredRenderingStamps)
	writeGenerationStrings(h, "required-vocabularies", inputs.RequiredVocabularies)
	writeGenerationMap(h, "known-capabilities", inputs.KnownCapabilities)
	writeGenerationValue(h, "developer-api-major", strconv.Itoa(inputs.SupportedDeveloperAPIMajor))
	writeGenerationMap(h, "revoked-builds", inputs.RevokedBuilds)
	writeGenerationValue(h, "build-catalog", inputs.BuildCatalogDigest)
	writeGenerationValue(h, "embedded-ui-revision", strconv.Itoa(int(inputs.EmbeddedUIRevision)))
	writeGenerationValue(h, "plugin-policy-generation", strconv.Itoa(inputs.PluginPolicyGeneration))
	return int(h.Sum64() & uint64(^uint(0)>>1))
}

type generationHash interface {
	Write([]byte) (int, error)
}

func writeGenerationPart(h generationHash, value string) {
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}

func writeGenerationValue(h generationHash, name, value string) {
	writeGenerationPart(h, name)
	writeGenerationPart(h, value)
}

func writeGenerationRange(h generationHash, name string, values [2]capabilities.Revision) {
	writeGenerationPart(h, name)
	writeGenerationPart(h, strconv.Itoa(int(values[0])))
	writeGenerationPart(h, strconv.Itoa(int(values[1])))
}

func writeGenerationStrings(h generationHash, name string, values []string) {
	values = append([]string(nil), values...)
	sort.Strings(values)
	writeGenerationPart(h, name)
	writeGenerationPart(h, strconv.Itoa(len(values)))
	for _, value := range values {
		writeGenerationPart(h, value)
	}
}

func writeGenerationMap(h generationHash, name string, values map[string]bool) {
	keys := make([]string, 0, len(values))
	for key, enabled := range values {
		if enabled {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	writeGenerationPart(h, name)
	writeGenerationPart(h, strconv.Itoa(len(keys)))
	for _, key := range keys {
		writeGenerationPart(h, key)
	}
}

func newCompatState(buildRevision string, capabilityIDs []string) *compatState {
	policy := capabilities.Policy{
		SupportedCoreRevisions:      [2]capabilities.Revision{1, 1},
		SupportedRenderingRevisions: [2]capabilities.Revision{1, 1},
		RequiredRenderingStamps:     []string{"core-web-ui-v1"},
		KnownCapabilities:           capabilitySet(capabilityIDs),
		SupportedDeveloperAPIMajor:  1,
		EmbeddedUIRevision:          capabilities.EmbeddedUIContractRevision,
	}
	state := &compatState{
		buildRevision: strings.TrimSpace(buildRevision),
		registry:      capabilities.NewRegistry(),
		refused:       make(map[string]struct{}),
		now:           time.Now,
	}
	state.install(policy, state.buildRevision, 0)
	return state
}

// compatInvalidationStore is the durable seam behind the decision registry's
// refusal tombstones. It is the narrow slice of the catalog store the
// compatibility surface needs — never the whole store — so the dependency stays
// legible and a test can substitute it.
type compatInvalidationStore interface {
	PutCompatDecisionInvalidation(context.Context, store.CompatDecisionInvalidation, time.Time) error
	StageCompatDecisionInvalidation(context.Context, store.CompatDecisionInvalidation, time.Time) error
	StageCompatDecisionInvalidations(context.Context, []store.CompatDecisionInvalidation, time.Time) error
	CommitStagedCompatDecisionInvalidations(context.Context) error
	DeleteCompatDecisionInvalidation(context.Context, string) error
	CompatDecisionInvalidations(context.Context, time.Time) ([]store.CompatDecisionInvalidation, error)
}

// compatInvalidationJournal adapts the store rows to the registry's journal
// contract. It is only a type translation: the registry owns when the journal is
// read and written, and the ordering guarantees that make a refusal durable
// before it is reported live in capabilities.
type compatInvalidationJournal struct {
	store    compatInvalidationStore
	refusals *compatRefusalFileJournal
}

var (
	_ capabilities.InvalidationJournal = compatInvalidationJournal{}
	_ compatInvalidationStore          = (*store.Store)(nil)
)

func (j compatInvalidationJournal) LoadInvalidations(ctx context.Context, now time.Time) ([]capabilities.Invalidation, error) {
	if j.store == nil {
		return nil, errCompatInvalidationStoreUnavailable
	}
	rows, err := j.store.CompatDecisionInvalidations(ctx, now)
	if err != nil {
		return nil, err
	}
	entries := make([]capabilities.Invalidation, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, capabilities.Invalidation{
			Subject:    row.Subject,
			Decision:   capabilities.Decision(row.Decision),
			ReasonCode: capabilities.ReasonCode(row.ReasonCode),
			RecordedAt: row.RecordedAt,
			ExpiresAt:  row.ExpiresAt,
		})
	}
	if j.refusals == nil {
		return entries, nil
	}
	fileEntries, err := j.refusals.load(ctx, now)
	if err != nil {
		return nil, err
	}
	bySubject := make(map[string]capabilities.Invalidation, len(entries)+len(fileEntries))
	for _, entry := range entries {
		bySubject[entry.Subject] = entry
	}
	for _, entry := range fileEntries {
		if current, ok := bySubject[entry.Subject]; !ok || current.RecordedAt.Before(entry.RecordedAt) {
			bySubject[entry.Subject] = entry
		}
	}
	entries = entries[:0]
	for _, entry := range bySubject {
		entries = append(entries, entry)
	}
	return entries, nil
}

func (j compatInvalidationJournal) RecordInvalidation(ctx context.Context, entry capabilities.Invalidation) error {
	if j.store == nil {
		return errCompatInvalidationStoreUnavailable
	}
	err := j.store.PutCompatDecisionInvalidation(ctx, store.CompatDecisionInvalidation{
		Subject:    entry.Subject,
		Decision:   string(entry.Decision),
		ReasonCode: string(entry.ReasonCode),
		RecordedAt: entry.RecordedAt,
		ExpiresAt:  entry.ExpiresAt,
	}, entry.RecordedAt)
	if err != nil || j.refusals == nil {
		return err
	}
	// The catalog row is already durable. A stale duplicate file is safe and is
	// cleared again on rehabilitation, so cleanup failure cannot revoke commit.
	_ = j.refusals.clear(ctx, entry.Subject)
	return nil
}

// StageInvalidation commits a refusal to the independent file journal before it
// best-effort stages the same row in the catalog. The file makes success crash
// durable even when both catalog write paths are unavailable.
func (j compatInvalidationJournal) StageInvalidation(ctx context.Context, entry capabilities.Invalidation) error {
	if j.refusals != nil {
		if err := j.refusals.record(ctx, entry); err != nil {
			return err
		}
		// Preserve the existing staged-store path when it is available, but the
		// synced refusal file is already the independent durable commit point.
		if j.store != nil {
			_ = j.stageStoreInvalidation(ctx, entry)
		}
		return nil
	}
	return j.stageStoreInvalidation(ctx, entry)
}

func (j compatInvalidationJournal) stageStoreInvalidation(ctx context.Context, entry capabilities.Invalidation) error {
	if j.store == nil {
		return errCompatInvalidationStoreUnavailable
	}
	return j.store.StageCompatDecisionInvalidation(ctx, store.CompatDecisionInvalidation{
		Subject:    entry.Subject,
		Decision:   string(entry.Decision),
		ReasonCode: string(entry.ReasonCode),
		RecordedAt: entry.RecordedAt,
		ExpiresAt:  entry.ExpiresAt,
	}, entry.RecordedAt)
}

// RecordInvalidationBatch keeps the registry's batch-durability contract with
// the staged-promote pair: staging the whole batch is one transaction (the
// durable commit point), promotion plus the staging clear is another, and
// CompatDecisionInvalidations replays any staged batch before it answers. No
// crash point between or inside the two transactions can leave a strict subset
// of the batch durable.
func (j compatInvalidationJournal) RecordInvalidationBatch(ctx context.Context, entries []capabilities.Invalidation) error {
	if j.store == nil {
		return errCompatInvalidationStoreUnavailable
	}
	if len(entries) == 0 {
		return nil
	}
	rows := make([]store.CompatDecisionInvalidation, 0, len(entries))
	now := entries[0].RecordedAt
	for _, entry := range entries {
		rows = append(rows, store.CompatDecisionInvalidation{
			Subject:    entry.Subject,
			Decision:   string(entry.Decision),
			ReasonCode: string(entry.ReasonCode),
			RecordedAt: entry.RecordedAt,
			ExpiresAt:  entry.ExpiresAt,
		})
	}
	if err := j.store.StageCompatDecisionInvalidations(ctx, rows, now); err != nil {
		return err
	}
	return j.store.CommitStagedCompatDecisionInvalidations(ctx)
}

func (j compatInvalidationJournal) ClearInvalidation(ctx context.Context, subject string) error {
	if j.store == nil {
		return errCompatInvalidationStoreUnavailable
	}
	if err := j.store.DeleteCompatDecisionInvalidation(ctx, subject); err != nil {
		return err
	}
	if j.refusals == nil {
		return nil
	}
	return j.refusals.clear(ctx, subject)
}

// attachInvalidationJournal gives the decision registry its durable tombstone
// set and reads it once, at construction, before any route can serve.
//
// A failed read is not fatal and must not be: the registry retries it on first
// use and refuses every resolve until it succeeds, so an unreadable journal
// costs availability, never the invariant. Fatally exiting instead would turn a
// transient store error into a server that will not boot.
func (state *compatState) attachInvalidationJournal(journal capabilities.InvalidationJournal, maxStreamTokenTTL time.Duration) {
	state.registry.SetInvalidationJournal(journal, maxStreamTokenTTL)
	if err := state.registry.LoadInvalidations(context.Background(), state.now()); err != nil {
		slog.Error("compatibility invalidation journal unreadable; decisions fail closed until it loads", "err", err)
	}
}

func capabilitySet(ids []string) map[string]bool {
	values := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			values[id] = true
		}
	}
	return values
}

func cloneCompatPolicy(policy capabilities.Policy) capabilities.Policy {
	policy.RequiredRenderingStamps = append([]string(nil), policy.RequiredRenderingStamps...)
	policy.RequiredVocabularies = append([]string(nil), policy.RequiredVocabularies...)
	policy.KnownCapabilities = cloneBoolMap(policy.KnownCapabilities)
	policy.RevokedBuilds = cloneBoolMap(policy.RevokedBuilds)
	policy.Builds = policy.Builds.Clone()
	return policy
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (state *compatState) install(policy capabilities.Policy, buildRevision string, pluginGeneration int) {
	policy = cloneCompatPolicy(policy)
	// The embedded Core Web UI is server-known by construction: policy
	// installation can add builds to the catalog but never remove the one
	// build this binary itself ships.
	policy.Builds = policy.Builds.WithEmbeddedUIBuild()
	policy.Generation = CompatGeneration(CompatGenerationInputs{
		BuildRevision:              strings.TrimSpace(buildRevision),
		CoreRevisions:              policy.SupportedCoreRevisions,
		RenderingRevisions:         policy.SupportedRenderingRevisions,
		RequiredRenderingStamps:    policy.RequiredRenderingStamps,
		RequiredVocabularies:       policy.RequiredVocabularies,
		KnownCapabilities:          policy.KnownCapabilities,
		SupportedDeveloperAPIMajor: policy.SupportedDeveloperAPIMajor,
		RevokedBuilds:              policy.RevokedBuilds,
		BuildCatalogDigest:         policy.Builds.Digest(),
		EmbeddedUIRevision:         policy.EmbeddedUIRevision,
		PluginPolicyGeneration:     pluginGeneration,
	})
	state.mu.Lock()
	state.policy = policy
	state.buildRevision = strings.TrimSpace(buildRevision)
	state.pluginGeneration = pluginGeneration
	// Publication has no caller to return a failure to, so the registry holds the
	// generation advance uncommitted instead: it is not observable by resolution
	// until its invalidations are durably journaled, and every decision resolve
	// fails closed in the meantime rather than serving the superseded decision
	// this policy was installed to invalidate. The write is retried on each
	// resolve, so the outage costs availability, never the invariant.
	if err := state.registry.Invalidate(policy.Generation); err != nil {
		slog.Error("compatibility invalidation persist failed; decisions fail closed until the journal accepts the new policy", "err", err)
	}
	state.mu.Unlock()
}

func (state *compatState) snapshot() capabilities.Policy {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return cloneCompatPolicy(state.policy)
}

func (state *compatState) snapshotInstallInputs() (capabilities.Policy, int) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	return cloneCompatPolicy(state.policy), state.pluginGeneration
}

// SetCompatPolicy installs the live compatibility policy and invalidates every
// record whose generation was derived from older durable inputs.
func (s *Server) SetCompatPolicy(policy capabilities.Policy, buildRevision string, pluginPolicyGeneration int) {
	s.compat.install(policy, buildRevision, pluginPolicyGeneration)
}

func (s *Server) refreshCompatPolicy() {
	if s.compat == nil {
		return
	}
	policy, pluginGeneration := s.compat.snapshotInstallInputs()
	policy.KnownCapabilities = capabilitySet(s.capabilities)
	s.compat.install(policy, s.buildSHA, pluginGeneration)
}
