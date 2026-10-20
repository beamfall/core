package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// Compatibility-decision durability across a restart (ADR-0063,
// HANDSHAKE-0063-COMPAT-DECISION-DURABILITY-1).
//
// The predecessor slice bound the compatibility decision into Stream Token
// mint/verify and additionally checked UNSTAMPED (pre-binding) grants against the
// resolver, so an invalidated subject's already-issued tokens stop serving while
// the server stays up. The residual it could not reach was architectural: the
// decision registry was in-memory, so a restart turned "invalidated" back into
// "no decision" — which the verify path accepts — and an unexpired pre-binding
// token became usable again for as long as the signing key stayed active.
//
// These tests exercise the real handshake route, then throw the process state
// away and rebuild the server over the SAME data dir, which is the only honest
// stand-in for a restart. Each case then goes through the same
// streamtoken.VerifyCompatDecision seam a byte fetch goes through.

const (
	durabilityDoomedSession   = store.SessionID("session-default")
	durabilityInnocentSession = store.SessionID("session-revealed")
	durabilityTokenKey        = "12345678901234567890123456789012"
	durabilityBuildID         = "durability-build"
	durabilityBuildRevision   = "durability-revision"
)

type compatDurabilityWorld struct {
	srv    *server.Server
	tokens *streamtoken.Service
	store  *store.Store
}

type compatDurabilityJobStatsOnly struct {
	store *store.Store
}

func (s compatDurabilityJobStatsOnly) JobStats(ctx context.Context) ([]store.JobStatsRow, error) {
	return s.store.JobStats(ctx)
}

func (s compatDurabilityJobStatsOnly) Device(ctx context.Context, id store.DeviceID) (store.Device, error) {
	return s.store.Device(ctx, id)
}

func (s compatDurabilityJobStatsOnly) BindDeviceSurface(ctx context.Context, id store.DeviceID, surface store.DeviceSurface) error {
	return s.store.BindDeviceSurface(ctx, id, surface)
}

// bootCompatDurabilityServer builds a server over dataDir. Calling it twice with
// the same dataDir is the restart: nothing but the bytes on disk carries over.
func bootCompatDurabilityServer(t *testing.T, dataDir string) compatDurabilityWorld {
	return bootCompatDurabilityServerWithInvalidationStore(t, dataDir, true)
}

func bootCompatDurabilityServerWithInvalidationStore(t *testing.T, dataDir string, withInvalidationStore bool) compatDurabilityWorld {
	t.Helper()
	return bootCompatDurabilityWorld(t, dataDir, func(st *store.Store) server.JobStatsProvider {
		if !withInvalidationStore {
			return compatDurabilityJobStatsOnly{store: st}
		}
		return st
	})
}

// bootCompatDurabilityWorld builds the server over dataDir with a caller-chosen
// durable seam, so a test can boot the real composition root over a store whose
// invalidation journal misbehaves.
func bootCompatDurabilityWorld(t *testing.T, dataDir string, jobStatsFor func(*store.Store) server.JobStatsProvider) compatDurabilityWorld {
	t.Helper()
	st := storetest.OpenAt(t, dataDir)
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte(durabilityTokenKey), clock)
	playbackInfo := playback.NewService(st, tokens, clock)
	jobStats := jobStatsFor(st)
	srv := server.New(
		manifests,
		playbackInfo,
		beam.NewService(st, authService, clock),
		pairing.NewService(st, clock),
		graph.NewHandler(manifests, authService, st, nil),
		mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		jobStats,
	)
	// The default policy knows only the embedded Core Web UI build, so a native
	// client's build has no provenance and every handshake refuses as
	// unknown-build. Installing the catalog is what an operator does; it is
	// identical on both boots, so a restart changes nothing but the bytes on disk.
	srv.SetCompatPolicy(durabilityPolicy(), durabilityBuildRevision, 0)
	return compatDurabilityWorld{srv: srv, tokens: tokens, store: st}
}

// durabilityCatalog is the server-known build record set for the native client
// these tests present, bound to the enrollments they hand-shake as.
func durabilityCatalog() capabilities.BuildCatalog {
	surface := capabilities.SurfaceMacOSManager
	catalog := capabilities.BuildCatalog{
		Builds: []capabilities.BuildRecord{{
			BuildID:      durabilityBuildID,
			Surface:      surface,
			ArtifactHash: clientCompatArtifactHash(durabilityBuildID),
			Sequence:     clientCompatBuildSequence,
			Distributions: []capabilities.Distribution{
				capabilities.DistributionDirect,
				capabilities.DistributionAppStore,
			},
			Roles: []capabilities.EnrollmentRole{
				capabilities.EnrollmentRoleViewer,
				capabilities.EnrollmentRoleManager,
			},
		}},
		MinimumSequence: map[capabilities.Surface]int{surface: clientCompatBuildSequence},
	}
	for _, session := range []store.SessionID{durabilityDoomedSession, durabilityInnocentSession} {
		catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
			EnrollmentID: "session:" + string(session),
			Surface:      surface,
			BuildID:      durabilityBuildID,
		})
	}
	return catalog
}

func durabilityPolicy() capabilities.Policy {
	return capabilities.Policy{
		Builds:                      durabilityCatalog(),
		SupportedCoreRevisions:      [2]capabilities.Revision{1, 1},
		SupportedRenderingRevisions: [2]capabilities.Revision{1, 1},
		RequiredRenderingStamps:     []string{"core-web-ui-v1"},
		SupportedDeveloperAPIMajor:  1,
		EmbeddedUIRevision:          capabilities.EmbeddedUIContractRevision,
	}
}

func TestMissingInvalidationStoreFailsStreamTokensClosed(t *testing.T) {
	world := bootCompatDurabilityServerWithInvalidationStore(t, t.TempDir(), false)
	resolver := world.tokens.CompatDecisionResolver()
	if resolver == nil {
		t.Fatal("no compatibility decision resolver was bound")
	}

	_, ok, err := resolver.Resolve(
		context.Background(),
		"session:"+string(durabilityDoomedSession),
		streamtoken.DecisionWant{SessionID: durabilityDoomedSession},
	)
	if !errors.Is(err, capabilities.ErrInvalidationJournalUnavailable) || ok {
		t.Fatalf("Resolve without invalidation store = (%t, %v), want (false, ErrInvalidationJournalUnavailable)", ok, err)
	}
	if errors.Is(err, capabilities.ErrNoDecision) {
		t.Fatal("missing invalidation store silently downgraded the subject to unknown")
	}
	if err := world.tokens.VerifyCompatDecision(context.Background(), unstampedGrant(durabilityDoomedSession)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token without invalidation store error = %v, want ErrCompatDecisionInvalid", err)
	}
}

func durabilityNativeRequest(t *testing.T, session store.SessionID, contract capabilities.ClientContract) *http.Request {
	t.Helper()
	body, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/compat/handshake", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", string(session))
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
	return req
}

func durabilityContractFor(surface string, coreRevision capabilities.Revision) capabilities.ClientContract {
	contract := capabilities.EmbeddedUIContract()
	contract.Surface = surface
	contract.AppVersion = "durability-test"
	contract.BuildID = durabilityBuildID
	contract.ArtifactHash = clientCompatArtifactHash(durabilityBuildID)
	contract.CoreContractRevRange = [2]capabilities.Revision{coreRevision, coreRevision}
	return contract
}

// invalidateSubject drives the real handshake route to a refusal, which is how a
// subject becomes invalidated in production.
func invalidateSubject(t *testing.T, world compatDurabilityWorld, session store.SessionID) {
	t.Helper()
	// A macOS Manager build claiming a core contract revision this server cannot
	// serve: refused as server-too-old, so the decision is not servable.
	req := durabilityNativeRequest(t, session, durabilityContractFor(string(capabilities.SurfaceMacOSManager), 9))
	rec := httptest.NewRecorder()
	world.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Decision   string `json:"decision"`
		DecisionID string `json:"decisionID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode handshake: %v", err)
	}
	if response.Decision == string(capabilities.DecisionCompatible) || response.DecisionID != "" {
		t.Fatalf("handshake was expected to refuse the subject, got %+v", response)
	}
}

// unstampedGrant is a token minted BEFORE decision binding existed: it carries no
// compatibility subject and no decision id. These are the grants an invalidation
// has to stop serving, and the ones a restart used to revive.
func unstampedGrant(session store.SessionID) streamtoken.Grant {
	return streamtoken.Grant{SessionID: session}
}

func stampedGrant(session store.SessionID, decisionID string) streamtoken.Grant {
	return streamtoken.Grant{
		SessionID:        session,
		CompatSubject:    "session:" + string(session),
		CompatDecisionID: decisionID,
	}
}

func TestGenerationInvalidatedSubjectStaysRejectedAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Unix(0, 0).UTC()

	before := bootCompatDurabilityServer(t, dataDir)
	req := durabilityNativeRequest(t, durabilityDoomedSession,
		durabilityContractFor(string(capabilities.SurfaceMacOSManager), capabilities.EmbeddedUIContractRevision))
	rec := httptest.NewRecorder()
	before.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("accepted handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Decision   string `json:"decision"`
		DecisionID string `json:"decisionID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode accepted handshake: %v", err)
	}
	if response.DecisionID == "" {
		t.Fatalf("handshake did not accept the subject: %+v", response)
	}

	legacyIssuer := streamtoken.NewService([]byte(durabilityTokenKey), func() time.Time { return now })
	token, err := legacyIssuer.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-generation-stale",
		DeviceID:  "device-tv",
		SessionID: durabilityDoomedSession,
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("issue legacy token: %v", err)
	}
	scope := auth.Scope{
		DeviceID:  "device-tv",
		SessionID: durabilityDoomedSession,
		ProfileID: "profile-default",
	}
	grant, err := before.tokens.Verify(token, streamtoken.TargetMediaDirect, scope)
	if err != nil {
		t.Fatalf("verify legacy token: %v", err)
	}
	if grant.CompatSubject != "" || grant.CompatDecisionID != "" {
		t.Fatalf("legacy token unexpectedly stamped with %q/%q", grant.CompatSubject, grant.CompatDecisionID)
	}

	before.srv.SetCompatPolicy(clientCompatPolicy(), "generation-next", 1)
	if err := before.tokens.VerifyCompatDecision(ctx, grant); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("generation-invalid token before restart = %v, want ErrCompatDecisionInvalid", err)
	}
	if err := before.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	restartedGrant, err := after.tokens.Verify(token, streamtoken.TargetMediaDirect, scope)
	if err != nil {
		t.Fatalf("verify unexpired legacy token after restart: %v", err)
	}
	if err := after.tokens.VerifyCompatDecision(ctx, restartedGrant); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("generation-invalid token after restart = %v, want ErrCompatDecisionInvalid", err)
	}
}

// errCompatDurabilityJournalReadOnly stands in for the durable store refusing
// writes — a full disk, a read-only catalog, a failed transaction.
var errCompatDurabilityJournalReadOnly = errors.New("catalog read-only")

// compatDurabilityFailingJournal is the real store with narrow seams: its
// durable invalidation writes can be switched off mid-test (failWrites), cut
// after a budgeted number of rows (rowBudget, the process-death-mid-batch
// seam), or cut between staging and promotion (failPromotion). Everything else
// the composition root asks of the store is the genuine implementation, so the
// server under test is wired the way production is.
type compatDurabilityFailingJournal struct {
	*store.Store
	failWrites    atomic.Bool
	failPromotion atomic.Bool
	// failPrimaryWrites fails only the direct per-row tombstone write, leaving
	// the staged fallback write working — the seam for a refusal whose durable
	// commit point has to be the staged row.
	failPrimaryWrites atomic.Bool
	primaryWrites     atomic.Int64
	stageWrites       atomic.Int64
	// rowBudget is the number of per-row durable writes still allowed; negative
	// means unlimited. It cuts the journal "after N of M rows".
	rowBudget atomic.Int64
}

func newCompatDurabilityFailingJournal(st *store.Store) *compatDurabilityFailingJournal {
	journal := &compatDurabilityFailingJournal{Store: st}
	journal.rowBudget.Store(-1)
	return journal
}

func (s *compatDurabilityFailingJournal) spendRow() error {
	budget := s.rowBudget.Load()
	if budget < 0 {
		return nil
	}
	if budget == 0 {
		return errCompatDurabilityJournalReadOnly
	}
	s.rowBudget.Add(-1)
	return nil
}

func (s *compatDurabilityFailingJournal) PutCompatDecisionInvalidation(
	ctx context.Context, entry store.CompatDecisionInvalidation, now time.Time,
) error {
	if s.failWrites.Load() || s.failPrimaryWrites.Load() {
		return errCompatDurabilityJournalReadOnly
	}
	if err := s.spendRow(); err != nil {
		return err
	}
	if err := s.Store.PutCompatDecisionInvalidation(ctx, entry, now); err != nil {
		return err
	}
	s.primaryWrites.Add(1)
	return nil
}

func (s *compatDurabilityFailingJournal) StageCompatDecisionInvalidation(
	ctx context.Context, entry store.CompatDecisionInvalidation, now time.Time,
) error {
	if s.failWrites.Load() {
		return errCompatDurabilityJournalReadOnly
	}
	if err := s.spendRow(); err != nil {
		return err
	}
	if err := s.Store.StageCompatDecisionInvalidation(ctx, entry, now); err != nil {
		return err
	}
	s.stageWrites.Add(1)
	return nil
}

func (s *compatDurabilityFailingJournal) StageCompatDecisionInvalidations(
	ctx context.Context, entries []store.CompatDecisionInvalidation, now time.Time,
) error {
	if s.failWrites.Load() {
		return errCompatDurabilityJournalReadOnly
	}
	return s.Store.StageCompatDecisionInvalidations(ctx, entries, now)
}

func (s *compatDurabilityFailingJournal) CommitStagedCompatDecisionInvalidations(ctx context.Context) error {
	if s.failWrites.Load() || s.failPromotion.Load() {
		return errCompatDurabilityJournalReadOnly
	}
	return s.Store.CommitStagedCompatDecisionInvalidations(ctx)
}

// Policy publication is the other way a subject becomes invalid, and it has no
// caller to hand a failure to. If the generation advance could be published
// while its invalidations stayed unjournaled, the newly invalidated subject
// would keep resolving to the decision the new policy just superseded, and the
// Stream Token verify seam — which only rejects on a resolver error — would keep
// serving its already-issued tokens. So the advance is not observable by
// resolution until it is durable, and resolution fails closed until then.
func TestPolicyPublicationFailsClosedWhenInvalidationIsNotJournaled(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()
	subject := "session:" + string(durabilityDoomedSession)

	var journal *compatDurabilityFailingJournal
	world := bootCompatDurabilityWorld(t, t.TempDir(), func(st *store.Store) server.JobStatsProvider {
		journal = newCompatDurabilityFailingJournal(st)
		return journal
	})

	req := durabilityNativeRequest(t, durabilityDoomedSession,
		durabilityContractFor(string(capabilities.SurfaceMacOSManager), capabilities.EmbeddedUIContractRevision))
	rec := httptest.NewRecorder()
	world.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("accepted handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		DecisionID string `json:"decisionID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode accepted handshake: %v", err)
	}
	if response.DecisionID == "" {
		t.Fatalf("handshake did not accept the subject: %s", rec.Body.String())
	}
	superseded := response.DecisionID

	legacyIssuer := streamtoken.NewService([]byte(durabilityTokenKey), func() time.Time { return now })
	token, err := legacyIssuer.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaDirect,
		MediaID:   "media-journal-outage",
		DeviceID:  "device-tv",
		SessionID: durabilityDoomedSession,
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("issue legacy token: %v", err)
	}
	grant, err := world.tokens.Verify(token, streamtoken.TargetMediaDirect, auth.Scope{
		DeviceID:  "device-tv",
		SessionID: durabilityDoomedSession,
		ProfileID: "profile-default",
	})
	if err != nil {
		t.Fatalf("verify legacy token: %v", err)
	}

	journal.failWrites.Store(true)
	world.srv.SetCompatPolicy(clientCompatPolicy(), "generation-next", 1)

	resolver := world.tokens.CompatDecisionResolver()
	if resolver == nil {
		t.Fatal("no compatibility decision resolver was bound")
	}
	decisionID, ok, err := resolver.Resolve(ctx, subject, streamtoken.DecisionWant{SessionID: durabilityDoomedSession})
	if err == nil || ok {
		t.Fatalf("Resolve under an unjournaled policy = (%q, %t, %v), want a fail-closed error", decisionID, ok, err)
	}
	if decisionID == superseded {
		t.Fatal("resolution served the decision the newly published policy invalidates")
	}
	if !errors.Is(err, capabilities.ErrInvalidationJournalUnavailable) {
		t.Fatalf("Resolve under an unjournaled policy error = %v, want ErrInvalidationJournalUnavailable", err)
	}

	if err := world.tokens.VerifyCompatDecision(ctx, grant); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token under an unjournaled policy = %v, want ErrCompatDecisionInvalid", err)
	}
	if err := world.tokens.VerifyCompatDecision(ctx, stampedGrant(durabilityDoomedSession, superseded)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("token stamped with the superseded decision = %v, want ErrCompatDecisionInvalid", err)
	}

	// The outage costs availability, never the invariant: the advance is retried
	// on the next resolve, and once it is durable the subject is rejected as
	// stale rather than as a durability failure.
	journal.failWrites.Store(false)
	if err := world.tokens.VerifyCompatDecision(ctx, grant); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token after the journal recovered = %v, want ErrCompatDecisionInvalid", err)
	}
	entries, err := world.store.CompatDecisionInvalidations(ctx, now)
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != 1 || entries[0].Subject != subject {
		t.Fatalf("journaled tombstones after recovery = %+v, want exactly %q", entries, subject)
	}
}

// The exact case that failed before this slice: invalidate, restart, and the
// unexpired UNSTAMPED pre-binding token is still rejected.
func TestInvalidatedSubjectStaysRejectedAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	before := bootCompatDurabilityServer(t, dataDir)
	invalidateSubject(t, before, durabilityDoomedSession)
	// Control: the invalidation bites while the server is up (the predecessor
	// slice's guarantee). If this stopped holding, the restart case below would
	// be proving nothing.
	if err := before.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); err == nil {
		t.Fatal("unstamped token for an invalidated subject was accepted before the restart")
	}
	if err := before.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	if err := after.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); err == nil {
		t.Fatal("unstamped pre-binding token for an invalidated subject was accepted after the restart")
	}
	// Same for a stamped token: its decision id cannot be the subject's live
	// decision, because the subject has none and is known-invalid.
	if err := after.tokens.VerifyCompatDecision(ctx, stampedGrant(durabilityDoomedSession, "decision-from-before")); err == nil {
		t.Fatal("stamped token for an invalidated subject was accepted after the restart")
	}
}

// Negative control: the fix must not become "reject everything after a restart".
// A subject that was never invalidated keeps working across the same restart.
func TestNeverInvalidatedSubjectKeepsWorkingAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	before := bootCompatDurabilityServer(t, dataDir)
	invalidateSubject(t, before, durabilityDoomedSession)
	if err := before.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	if err := after.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityInnocentSession)); err != nil {
		t.Fatalf("unstamped token for a never-invalidated subject was rejected after the restart: %v", err)
	}
}

// The resolver's return distinguishes a genuinely unknown subject from a
// known-invalid one. Collapsing the two is what made the hole unfixable at the
// verify seam, so the distinction is asserted directly, not only through its
// effect on a token.
func TestResolverDistinguishesUnknownFromInvalidAfterRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	before := bootCompatDurabilityServer(t, dataDir)
	invalidateSubject(t, before, durabilityDoomedSession)
	if err := before.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	resolver := after.tokens.CompatDecisionResolver()
	if resolver == nil {
		t.Fatal("no compatibility decision resolver was bound")
	}

	_, ok, err := resolver.Resolve(ctx, "session:"+string(durabilityDoomedSession),
		streamtoken.DecisionWant{SessionID: durabilityDoomedSession})
	if err == nil {
		t.Fatal("a known-invalid subject resolved without an error; the verify path would accept its tokens")
	}
	if ok {
		t.Fatal("a known-invalid subject reported a usable decision")
	}

	unknownID, unknownOK, err := resolver.Resolve(ctx, "session:"+string(durabilityInnocentSession),
		streamtoken.DecisionWant{SessionID: durabilityInnocentSession})
	if err != nil {
		t.Fatalf("a genuinely unknown subject resolved with an error: %v", err)
	}
	if unknownOK || unknownID != "" {
		t.Fatalf("unknown subject resolved to (%q, %t), want no decision", unknownID, unknownOK)
	}
}

// The persisted set has a bounded lifetime, so it cannot grow forever. Past its
// retention the tombstone is pruned and the subject goes back to being unknown.
func TestInvalidationSetIsBounded(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	before := bootCompatDurabilityServer(t, dataDir)
	invalidateSubject(t, before, durabilityDoomedSession)
	if err := before.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	// Every persisted tombstone carries a finite expiry exactly one retention
	// window after it was recorded — no row is written without one.
	entries, err := after.store.CompatDecisionInvalidations(ctx, time.Now())
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("persisted tombstones = %+v, want exactly the refused subject", entries)
	}
	retention := capabilities.InvalidationRetentionFor(streamtoken.MaxStreamTokenTTL)
	if lifetime := entries[0].ExpiresAt.Sub(entries[0].RecordedAt); lifetime != retention {
		t.Fatalf("tombstone lifetime = %s, want the bounded retention %s", lifetime, retention)
	}

	// Past that expiry the set is empty AND the row is gone from the table, so it
	// cannot grow forever.
	pastRetention := entries[0].ExpiresAt.Add(time.Hour)
	expired, err := after.store.CompatDecisionInvalidations(ctx, pastRetention)
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("tombstones past retention = %+v, want the set pruned", expired)
	}
	remaining, err := after.store.CompatDecisionInvalidations(ctx, entries[0].RecordedAt)
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("pruned tombstone came back = %+v", remaining)
	}
	if capabilities.InvalidationSafetyMargin <= 0 {
		t.Fatalf("InvalidationSafetyMargin = %s, want a positive duration", capabilities.InvalidationSafetyMargin)
	}
	retentionFloor := streamtoken.MaxStreamTokenTTL + capabilities.InvalidationSafetyMargin
	if retention < retentionFloor {
		t.Fatalf("invalidation retention = %s, want at least token maximum %s + safety margin %s", retention, streamtoken.MaxStreamTokenTTL, capabilities.InvalidationSafetyMargin)
	}
}

// A refused client that comes back with a contract the server can serve is
// rehabilitated, durably: the tombstone is cleared, so the next restart does not
// resurrect the refusal.
func TestSuccessfulHandshakeClearsInvalidationAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	before := bootCompatDurabilityServer(t, dataDir)
	invalidateSubject(t, before, durabilityDoomedSession)

	req := durabilityNativeRequest(t, durabilityDoomedSession,
		durabilityContractFor(string(capabilities.SurfaceMacOSManager), capabilities.EmbeddedUIContractRevision))
	rec := httptest.NewRecorder()
	before.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Decision   string `json:"decision"`
		DecisionID string `json:"decisionID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode recovery handshake: %v", err)
	}
	if response.DecisionID == "" {
		t.Fatalf("recovery handshake did not accept the client: %+v", response)
	}
	if err := before.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	if err := after.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); err != nil {
		t.Fatalf("a rehabilitated subject was still rejected after the restart: %v", err)
	}
}

// acceptDurabilitySubject drives the real handshake route to an acceptance.
func acceptDurabilitySubject(t *testing.T, world compatDurabilityWorld, session store.SessionID) {
	t.Helper()
	req := durabilityNativeRequest(t, session,
		durabilityContractFor(string(capabilities.SurfaceMacOSManager), capabilities.EmbeddedUIContractRevision))
	rec := httptest.NewRecorder()
	world.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("accepted handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		DecisionID string `json:"decisionID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode accepted handshake: %v", err)
	}
	if response.DecisionID == "" {
		t.Fatalf("handshake did not accept the subject: %s", rec.Body.String())
	}
}

// A generation advance invalidates a BATCH of subjects, and the batch must be
// atomically durable across process death. The seams cut the journal both ways
// a mid-batch death could land — after one of two per-row writes, and between
// staging and promotion — then the process "dies" and a healthy server boots
// over the same bytes. Every subject of the batch must come back invalid;
// rehydrating a strict subset would let the omitted subject's unexpired
// pre-binding token serve again as never-handshook.
func TestGenerationBatchSurvivesProcessDeathMidJournalWrite(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	sessions := []store.SessionID{durabilityDoomedSession, durabilityInnocentSession}

	var journal *compatDurabilityFailingJournal
	world := bootCompatDurabilityWorld(t, dataDir, func(st *store.Store) server.JobStatsProvider {
		journal = newCompatDurabilityFailingJournal(st)
		return journal
	})
	for _, session := range sessions {
		acceptDurabilitySubject(t, world, session)
	}

	// The cut: at most one per-row write lands, and promotion of a staged batch
	// never runs — the durable state a process death mid-batch leaves behind.
	journal.rowBudget.Store(1)
	journal.failPromotion.Store(true)
	world.srv.SetCompatPolicy(clientCompatPolicy(), "generation-next", 1)

	// In-process the cut advance fails closed rather than serving either
	// superseded decision.
	resolver := world.tokens.CompatDecisionResolver()
	if resolver == nil {
		t.Fatal("no compatibility decision resolver was bound")
	}
	for _, session := range sessions {
		if _, ok, err := resolver.Resolve(ctx, "session:"+string(session),
			streamtoken.DecisionWant{SessionID: session}); err == nil || ok {
			t.Fatalf("Resolve(%s) under a cut batch = (%t, %v), want a fail-closed error", session, ok, err)
		}
	}
	if err := world.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	entries, err := after.store.CompatDecisionInvalidations(ctx, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != len(sessions) {
		t.Fatalf("rehydrated tombstones = %+v, want the whole %d-subject batch", entries, len(sessions))
	}
	for _, session := range sessions {
		if err := after.tokens.VerifyCompatDecision(ctx, unstampedGrant(session)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
			t.Fatalf("unstamped token for batch subject %s after restart = %v, want ErrCompatDecisionInvalid (the subject must never come back as never-handshook)", session, err)
		}
	}
}

// A refusal whose journal write fails transiently must not become a fact the
// process enforces but a restart forgets. The failed write is a pending retry:
// the handshake reports the durability failure (never a refusal decision), the
// subject stays refused in-process, and the next resolve after the journal
// recovers lands the tombstone — so the refusal survives the restart.
func TestRefusalTransientJournalOutageSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	subject := "session:" + string(durabilityDoomedSession)

	var journal *compatDurabilityFailingJournal
	world := bootCompatDurabilityWorld(t, dataDir, func(st *store.Store) server.JobStatsProvider {
		journal = newCompatDurabilityFailingJournal(st)
		return journal
	})

	journal.failWrites.Store(true)
	req := durabilityNativeRequest(t, durabilityDoomedSession,
		durabilityContractFor(string(capabilities.SurfaceMacOSManager), 9))
	rec := httptest.NewRecorder()
	world.srv.ServeHTTP(rec, req)
	// The refusal is NOT reported as a decision while it is not durable: the
	// caller gets the durability failure and retries.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("refusal handshake under a journal outage status = %d, want %d: %s",
			rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	// In-process the subject is already refused.
	if err := world.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token during the outage = %v, want ErrCompatDecisionInvalid", err)
	}

	// The journal recovers; the next resolve retries the pending refusal write.
	journal.failWrites.Store(false)
	if err := world.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token after journal recovery = %v, want ErrCompatDecisionInvalid", err)
	}
	entries, err := world.store.CompatDecisionInvalidations(ctx, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != 1 || entries[0].Subject != subject {
		t.Fatalf("durable tombstones after recovery = %+v, want the retried refusal for %s", entries, subject)
	}
	if err := world.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	after := bootCompatDurabilityServer(t, dataDir)
	if err := after.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token after restart = %v, want ErrCompatDecisionInvalid (the refusal must never be forgotten)", err)
	}
}

// The durable commit point of a refusal precedes its caller-visible outcome.
// When the direct tombstone write fails but the staged write lands, the staged
// row is that commit point: the handshake reports the refusal outcome, and the
// process then dies BEFORE any retry promotes the row. The restarted server
// replays the staged row, so the reported refusal is never forgotten — Resolve
// reports the refusal, never ErrNoDecision.
func TestRefusalStagedFallbackSurvivesRestartBeforeAnyRetry(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	subject := "session:" + string(durabilityDoomedSession)

	var journal *compatDurabilityFailingJournal
	world := bootCompatDurabilityWorld(t, dataDir, func(st *store.Store) server.JobStatsProvider {
		journal = newCompatDurabilityFailingJournal(st)
		return journal
	})

	// Direct tombstone writes fail; the staged fallback works. The handshake
	// still reports the refusal outcome, because the staged row made it durable.
	journal.failPrimaryWrites.Store(true)
	invalidateSubject(t, world, durabilityDoomedSession)

	// In-process the subject is refused off the staged pending tombstone.
	if err := world.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token while the refusal is staged = %v, want ErrCompatDecisionInvalid", err)
	}

	// The process dies with the direct write still failing, so no retry ever
	// promoted the staged row. Only the bytes on disk carry over.
	if err := world.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	after := bootCompatDurabilityServer(t, dataDir)
	entries, err := after.store.CompatDecisionInvalidations(ctx, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations: %v", err)
	}
	if len(entries) != 1 || entries[0].Subject != subject {
		t.Fatalf("rehydrated tombstones = %+v, want the staged refusal for %s", entries, subject)
	}
	resolver := after.tokens.CompatDecisionResolver()
	if resolver == nil {
		t.Fatal("no compatibility decision resolver was bound")
	}
	_, ok, resolveErr := resolver.Resolve(ctx, subject, streamtoken.DecisionWant{SessionID: durabilityDoomedSession})
	if ok || resolveErr == nil {
		t.Fatalf("Resolve after restart = (%t, %v), want the refusal reported as an error", ok, resolveErr)
	}
	if errors.Is(resolveErr, capabilities.ErrNoDecision) {
		t.Fatal("restart forgot the reported refusal: subject resolved as never-handshook")
	}
	if !errors.Is(resolveErr, capabilities.ErrInvalidated) {
		t.Fatalf("Resolve after restart = %v, want ErrInvalidated", resolveErr)
	}
	if err := after.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token after restart = %v, want ErrCompatDecisionInvalid (the reported refusal must never be forgotten)", err)
	}
}

// When the staged write ALSO fails, no durable commit point exists, so the
// handshake returns the durability error instead of a refusal outcome, and
// resolution fails closed over the memory-only tombstone rather than answering
// ErrInvalidated from a fact a restart would forget. The process keeps retrying
// that debt itself, so a recovered journal becomes durable without another
// handshake, resolve, or token-verification call.
func TestRefusalWithoutAnyDurableWriteFailsClosedAndRetriesAfterRecovery(t *testing.T) {
	ctx := context.Background()
	subject := "session:" + string(durabilityDoomedSession)

	var journal *compatDurabilityFailingJournal
	world := bootCompatDurabilityWorld(t, t.TempDir(), func(st *store.Store) server.JobStatsProvider {
		journal = newCompatDurabilityFailingJournal(st)
		return journal
	})

	// Both the direct write and the staged fallback fail.
	journal.failWrites.Store(true)
	req := durabilityNativeRequest(t, durabilityDoomedSession,
		durabilityContractFor(string(capabilities.SurfaceMacOSManager), 9))
	rec := httptest.NewRecorder()
	world.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("refusal handshake with no durable write path status = %d, want %d: %s",
			rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	var outcome struct {
		Decision             string   `json:"decision"`
		DecisionID           string   `json:"decisionID"`
		ReasonCode           string   `json:"reasonCode"`
		DisabledCapabilities []string `json:"disabledCapabilities"`
		MessageClass         string   `json:"messageClass"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &outcome); err != nil {
		t.Fatalf("decode durability error response: %v", err)
	}
	if outcome.Decision != "" || outcome.DecisionID != "" || outcome.ReasonCode != "" ||
		len(outcome.DisabledCapabilities) != 0 || outcome.MessageClass != "" {
		t.Fatalf("durability error exposed a refusal outcome: %+v", outcome)
	}

	// The memory-only refusal is never handed to a caller as a refusal outcome:
	// resolution reports the durability failure, not ErrInvalidated.
	resolver := world.tokens.CompatDecisionResolver()
	if resolver == nil {
		t.Fatal("no compatibility decision resolver was bound")
	}
	_, ok, err := resolver.Resolve(ctx, subject, streamtoken.DecisionWant{SessionID: durabilityDoomedSession})
	if ok || err == nil {
		t.Fatalf("Resolve over a memory-only refusal = (%t, %v), want a fail-closed error", ok, err)
	}
	if !errors.Is(err, capabilities.ErrInvalidationJournalUnavailable) {
		t.Fatalf("Resolve over a memory-only refusal = %v, want ErrInvalidationJournalUnavailable", err)
	}
	// Fail-closed still rejects the subject's tokens while the process lives.
	if err := world.tokens.VerifyCompatDecision(ctx, unstampedGrant(durabilityDoomedSession)); !errors.Is(err, streamtoken.ErrCompatDecisionInvalid) {
		t.Fatalf("unstamped token with no durable write path = %v, want ErrCompatDecisionInvalid", err)
	}

	// Recovery itself is the only action after this point. Recover staging first
	// while primary writes remain refused, proving the retry preserves the
	// stage-before-promotion ordering; then recover primary writes and wait for
	// the same worker to finish. Counter baselines prevent setup writes from
	// satisfying either wait.
	stageBaseline := journal.stageWrites.Load()
	primaryBaseline := journal.primaryWrites.Load()
	journal.failPrimaryWrites.Store(true)
	journal.failWrites.Store(false)
	deadline := time.Now().Add(2 * time.Second)
	for journal.stageWrites.Load() == stageBaseline {
		if time.Now().After(deadline) {
			t.Fatalf("retry flush did not stage refusal for %s without caller action", subject)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := journal.primaryWrites.Load(); got != primaryBaseline {
		t.Fatalf("primary writes advanced before recovery: got %d, baseline %d", got, primaryBaseline)
	}
	journal.failPrimaryWrites.Store(false)
	deadline = time.Now().Add(2 * time.Second)
	for journal.primaryWrites.Load() == primaryBaseline {
		if time.Now().After(deadline) {
			t.Fatalf("retry flush did not persist refusal for %s without caller action", subject)
		}
		time.Sleep(10 * time.Millisecond)
	}
	entries, err := world.store.CompatDecisionInvalidations(ctx, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("CompatDecisionInvalidations after recovery: %v", err)
	}
	if len(entries) != 1 || entries[0].Subject != subject {
		t.Fatalf("durable tombstones after recovery = %+v, want only %s", entries, subject)
	}
}
