package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// stubEmbedder is a fake QueryEmbedder for the semantic-search tests. It maps a
// query to a fixed vector so SemanticSearch is deterministic, and reports a
// fixed kind. available toggles the "model loaded" state for the fallback test.
type stubEmbedder struct {
	kind      store.EmbeddingKind
	available bool
	vector    []float32
}

func (s stubEmbedder) Kind() store.EmbeddingKind { return s.kind }
func (s stubEmbedder) Available() bool           { return s.available }
func (s stubEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	return s.vector, nil
}

type failingEmbedder struct {
	calls int
}

func (e *failingEmbedder) Kind() store.EmbeddingKind { return "text-v1" }
func (e *failingEmbedder) Available() bool           { return true }
func (e *failingEmbedder) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	e.calls++
	return nil, errors.New("embedder offline for raw query " + query)
}

// TestEnrichmentPanelAdminGated is the Console Enrichment vertical slice
// (ADR-0041 #107): per-Library index status, the auto-tag review queue, the
// accept gate (§2.5 promotion to canonical metadata), reject (discard), and the
// per-Library AI-indexing opt-out — all Admin-Session gated.
//
// feature:enrichment-index-status
// feature:auto-tag-review
// feature:ai-indexing-optout
func TestEnrichmentPanelAdminGated(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Seed one Media in library-local for the proposal to target.
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/m/alpha.mp4",
		Title:              "Alpha",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:alpha",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	// Stage one auto-tag proposal (the write path a plugin would use).
	if err := st.ProposePendingAITag(ctx, store.PendingAITag{
		ID:         "tag-1",
		MediaID:    media.ID,
		TagKind:    "mood",
		Value:      "noir",
		ProposedBy: "enrichment-plugin",
	}); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}

	handler := newTestHandlerWithStore(st)

	// Admin gate: each Enrichment field is denied before re-auth.
	for _, q := range []string{
		`{"query":"query { libraryIndexStatus { libraryID state } }"}`,
		`{"query":"query { pendingAITagQueue { id } }"}`,
	} {
		_, errs := post(t, handler, q, authorizeManager)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "admin session required") {
			t.Fatalf("pre-admin %s errors = %#v, want admin gate", q, errs)
		}
	}

	// Open the Admin Session window.
	if _, errs := post(t, handler,
		`{"query":"mutation V($password:String!){ verifyAdminSession(password:$password){ active } }","variables":{"password":"correct horse battery"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("admin re-auth errors = %#v", errs)
	}

	// Index status: with no embedder, library-local lists with state=empty and
	// AI indexing on by default.
	data, errs := post(t, handler, `{"query":"query { libraryIndexStatus { libraryID title indexed total aiIndexing state } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("libraryIndexStatus errors = %#v", errs)
	}
	var statuses []LibraryIndexStatusInfo
	mustUnmarshal(t, data["libraryIndexStatus"], &statuses)
	local := findIndexStatus(statuses, "library-local")
	if local == nil {
		t.Fatal("library-local missing from libraryIndexStatus")
	}
	if !local.AIIndexing {
		t.Error("library-local aiIndexing should default ON")
	}
	if local.State != store.IndexStateEmpty {
		t.Errorf("library-local state = %q, want empty", local.State)
	}

	// Review queue: the staged proposal is present with its diff-preview fields.
	data, errs = post(t, handler, `{"query":"query { pendingAITagQueue { id mediaID mediaTitle tagKind value proposedBy } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("pendingAITagQueue errors = %#v", errs)
	}
	var queue []PendingAITagInfo
	mustUnmarshal(t, data["pendingAITagQueue"], &queue)
	if len(queue) != 1 || queue[0].ID != "tag-1" || queue[0].MediaTitle != "Alpha" || queue[0].TagKind != "mood" || queue[0].Value != "noir" {
		t.Fatalf("queue = %#v, want one tag-1 mood=noir on Alpha", queue)
	}

	// AI-safety: BEFORE accept, the canonical media has NO mood field and is not
	// AI-mutated. This is the gate — staged ≠ canonical.
	assertNoCanonicalTag(t, st, media.ID, "mood")

	// Accept: promotes the staged tag to a canonical media_field with AI
	// provenance, and the queue empties.
	data, errs = post(t, handler,
		`{"query":"mutation A($tagID:String!){ acceptPendingAITag(tagID:$tagID){ id } }","variables":{"tagID":"tag-1"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("acceptPendingAITag errors = %#v", errs)
	}
	var afterAccept []PendingAITagInfo
	mustUnmarshal(t, data["acceptPendingAITag"], &afterAccept)
	if len(afterAccept) != 0 {
		t.Errorf("queue after accept = %#v, want empty", afterAccept)
	}
	// The canonical media now carries mood=noir with mutated_by=ai.
	assertCanonicalTag(t, st, media.ID, "mood", "noir")
	got, err := st.Media(ctx, media.ID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if got.MutatedBy != store.MutationSourceAI {
		t.Errorf("mutatedBy = %q, want %q", got.MutatedBy, store.MutationSourceAI)
	}

	// Re-accepting the same tag fails (it is no longer pending) — the gate is
	// not replayable.
	if _, errs := post(t, handler,
		`{"query":"mutation A($tagID:String!){ acceptPendingAITag(tagID:$tagID){ id } }","variables":{"tagID":"tag-1"}}`,
		authorizeManager); len(errs) == 0 {
		t.Error("re-accept of an approved tag should error")
	}

	// Reject path: stage a second proposal, reject it, confirm it never reaches
	// canonical metadata.
	if err := st.ProposePendingAITag(ctx, store.PendingAITag{
		ID: "tag-2", MediaID: media.ID, TagKind: "era", Value: "1950s", ProposedBy: "enrichment-plugin",
	}); err != nil {
		t.Fatalf("ProposePendingAITag tag-2: %v", err)
	}
	if _, errs := post(t, handler,
		`{"query":"mutation R($tagID:String!){ rejectPendingAITag(tagID:$tagID){ id } }","variables":{"tagID":"tag-2"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("rejectPendingAITag errors = %#v", errs)
	}
	assertNoCanonicalTag(t, st, media.ID, "era")

	// Opt-out toggle: turning AI indexing off flips the state to "off".
	data, errs = post(t, handler,
		`{"query":"mutation S($libraryID:String!,$enabled:Boolean!){ setLibraryAIIndexing(libraryID:$libraryID,enabled:$enabled){ libraryID aiIndexing state } }","variables":{"libraryID":"library-local","enabled":false}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setLibraryAIIndexing errors = %#v", errs)
	}
	var afterToggle []LibraryIndexStatusInfo
	mustUnmarshal(t, data["setLibraryAIIndexing"], &afterToggle)
	local = findIndexStatus(afterToggle, "library-local")
	if local == nil || local.AIIndexing {
		t.Fatalf("after opt-out library-local = %#v, want aiIndexing false", local)
	}
	if local.State != store.IndexStateOff {
		t.Errorf("after opt-out state = %q, want off", local.State)
	}
}

// TestSearchModeKeywordAndFallback covers the explainable search resolver
// (ADR-0041 #62/#106): keyword mode returns scored items + the restricted
// boolean, explicit semantic stays empty without an index, and auto honestly
// falls back to keyword.
//
// feature:search-mode
// feature:search-explainability
func TestSearchModeKeywordAndFallback(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/m/keyworded.mp4",
		Title:              "Keyworded Title",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:kw",
	}); err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	handler := newTestHandlerWithStore(st)

	// keyword mode (default): an unrevealed viewer searching gets keyword mode
	// and restrictedResultsPresent=true (library-archive is restricted+hidden).
	data, errs := post(t, handler,
		`{"query":"query S($query:String!){ searchResult(query:$query){ mode restrictedResultsPresent items { id title mode score } } }","variables":{"query":"Keyworded"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("searchResult keyword errors = %#v", errs)
	}
	var resp SearchResultResponse
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeKeyword {
		t.Errorf("mode = %q, want keyword", resp.Mode)
	}
	if !resp.RestrictedResultsPresent {
		t.Error("restrictedResultsPresent should be true for an unrevealed viewer with a hidden restricted library")
	}
	if len(resp.Items) == 0 || resp.Items[0].Mode != SearchModeKeyword {
		t.Fatalf("items = %#v, want at least one keyword item", resp.Items)
	}

	// Explicit semantic with no populated index never falls back to keyword.
	data, errs = post(t, handler,
		`{"query":"query S($query:String!,$mode:SearchMode){ searchResult(query:$query,mode:$mode){ mode items { id } } }","variables":{"query":"Keyworded","mode":"semantic"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("searchResult semantic-no-plugin errors = %#v", errs)
	}
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeSemantic || len(resp.Items) != 0 {
		t.Errorf("semantic-with-no-index = %#v, want empty semantic result", resp)
	}

	// Auto may fall back to keyword when semantic is unavailable.
	data, errs = post(t, handler,
		`{"query":"query S($query:String!,$mode:SearchMode){ searchResult(query:$query,mode:$mode){ mode items { id } } }","variables":{"query":"Keyworded","mode":"auto"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("searchResult auto-no-plugin errors = %#v", errs)
	}
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeKeyword || len(resp.Items) == 0 {
		t.Errorf("auto-with-no-plugin = %#v, want keyword fallback", resp)
	}

	// A bad mode is a client error, not a silent downgrade.
	if _, errs := post(t, handler,
		`{"query":"query S($query:String!,$mode:SearchMode){ searchResult(query:$query,mode:$mode){ mode } }","variables":{"query":"x","mode":"telepathic"}}`,
		authorizeViewer); len(errs) == 0 {
		t.Error("unknown search mode should be a client error")
	}
}

// TestSemanticCapabilityReportsPluginAbsent confirms the capability endpoint
// the viewer toggle reads: with no Enrichment Plugin, every visible Library
// reports pluginActive=false (the 4-state plugin-absent case), and the endpoint
// is reveal-filtered (an unrevealed viewer never sees library-archive).
//
// feature:semantic-capability
func TestSemanticCapabilityReportsPluginAbsent(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	data, errs := post(t, handler,
		`{"query":"query { semanticCapability { libraryID pluginActive state } }"}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("semanticCapability errors = %#v", errs)
	}
	var caps []SemanticCapabilityInfo
	mustUnmarshal(t, data["semanticCapability"], &caps)
	if len(caps) == 0 {
		t.Fatal("semanticCapability returned no libraries")
	}
	for _, c := range caps {
		if c.PluginActive {
			t.Errorf("library %s pluginActive=true with no embedder wired", c.LibraryID)
		}
		if c.LibraryID == "library-archive" {
			t.Error("unrevealed viewer must not see restricted library-archive in capability")
		}
	}
}

// TestSearchModeSemanticRevealScoped confirms that with a wired embedder the
// semantic path serves results, reports mode=semantic with scores, and remains
// reveal-scoped: an unrevealed viewer never gets a restricted-library hit even
// when its embedding would score highest. The store's SemanticSearch enforces
// the pre-scoping; this asserts the resolver wiring preserves it.
//
// feature:search-mode
func TestSearchModeSemanticRevealScoped(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	vec := []float32{1, 0, 0}
	// Seed a local (visible) Media and a restricted (archive) Media, both with
	// an embedding identical to the query vector so both would score 1.0.
	seed := func(lib store.LibraryID, id string) {
		_, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:          lib,
			Path:               "/m/" + id + ".mp4",
			Title:              id,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: "sha256:" + id,
		})
		if err != nil {
			t.Fatalf("ImportMediaFile %s: %v", id, err)
		}
		if err := st.UpsertEmbedding(ctx, "sha256:"+id, "text-v1", vec); err != nil {
			t.Fatalf("UpsertEmbedding %s: %v", id, err)
		}
	}
	seed("library-local", "localhit")
	seed("library-archive", "archivehit")

	handler := newTestHandlerWithStore(st)
	handler.SetQueryEmbedder(stubEmbedder{kind: "text-v1", available: true, vector: vec})

	// Unrevealed viewer: semantic mode is served, returns the local hit, and
	// NEVER the restricted archive hit.
	data, errs := post(t, handler,
		`{"query":"query S($query:String!,$mode:SearchMode){ searchResult(query:$query,mode:$mode){ mode items { id title score } } }","variables":{"query":"anything","mode":"semantic"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("searchResult semantic errors = %#v", errs)
	}
	var resp SearchResultResponse
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeSemantic {
		t.Fatalf("served mode = %q, want semantic", resp.Mode)
	}
	if len(resp.Items) == 0 {
		t.Fatal("semantic search returned no items")
	}
	for _, item := range resp.Items {
		if item.Title == "archivehit" {
			t.Fatalf("semantic search LEAKED restricted archivehit to unrevealed viewer (score=%v)", item.Score)
		}
	}
	// The local hit is present with a near-1.0 score (explainability).
	var foundLocal bool
	for _, item := range resp.Items {
		if item.Title == "localhit" {
			foundLocal = true
			if item.Score < 0.99 {
				t.Errorf("localhit score = %v, want ~1.0", item.Score)
			}
		}
	}
	if !foundLocal {
		t.Error("localhit missing from semantic results")
	}
}

func TestSearchModeSemanticRequiresPopulatedVisibleIndex(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/m/keyword-only.mp4",
		Title:              "Keyword Only",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:keyword-only",
	}); err != nil {
		t.Fatalf("ImportMediaFile local: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-archive",
		Path:               "/m/restricted-indexed.mp4",
		Title:              "Restricted Indexed",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:restricted-indexed",
	}); err != nil {
		t.Fatalf("ImportMediaFile restricted: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, "sha256:restricted-indexed", "text-v1", []float32{1, 0, 0}); err != nil {
		t.Fatalf("UpsertEmbedding restricted: %v", err)
	}

	handler := newTestHandlerWithStore(st)
	embedder := &failingEmbedder{}
	handler.SetQueryEmbedder(embedder)
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(oldLogger)

	data, errs := post(t, handler,
		`{"query":"query S($query:String!,$mode:SearchMode){ searchResult(query:$query,mode:$mode){ mode items { id title score } } }","variables":{"query":"Keyword","mode":"semantic"}}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("searchResult semantic restricted-only-index errors = %#v", errs)
	}
	if embedder.calls != 0 {
		t.Fatalf("EmbedQuery calls = %d, want 0 before visible index is populated", embedder.calls)
	}
	var resp SearchResultResponse
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeSemantic || len(resp.Items) != 0 {
		t.Fatalf("restricted-only semantic result = %#v, want empty semantic result", resp)
	}
	wire, err := json.Marshal(struct {
		Data   map[string]json.RawMessage
		Errors []graphError
	}{data, errs})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	for _, leaked := range []string{"restricted-indexed", "Restricted Indexed"} {
		if bytes.Contains(wire, []byte(leaked)) {
			t.Fatalf("semantic response leaked hidden token %q: %s", leaked, wire)
		}
		if strings.Contains(logs.String(), leaked) {
			t.Fatalf("semantic logs leaked hidden token %q: %s", leaked, logs.String())
		}
	}
}

func TestSearchResultEmbedQueryErrorFallsBackWithoutLoggingRawQuery(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/m/error-path.mp4",
		Title:              "SensitiveNeedle Result",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:error-path",
	}); err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, "sha256:error-path", "text-v1", []float32{1, 0, 0}); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}
	handler := newTestHandlerWithStore(st)
	embedder := &failingEmbedder{}
	handler.SetQueryEmbedder(embedder)
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(oldLogger)

	data, errs := post(t, handler,
		`{"query":"query S($query:String!,$mode:SearchMode){ serverInfo { name } searchResult(query:$query,mode:$mode){ mode items { title } } }","variables":{"query":"SensitiveNeedle","mode":"semantic"}}`,
		authorizeViewer)

	if embedder.calls != 1 {
		t.Fatalf("EmbedQuery calls = %d, want 1", embedder.calls)
	}
	if _, ok := data["serverInfo"]; !ok {
		t.Fatalf("serverInfo missing from partial response: data=%#v errors=%#v", data, errs)
	}
	if len(errs) != 0 {
		t.Fatalf("errors = %#v, want keyword fallback without graph error", errs)
	}
	var resp SearchResultResponse
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeKeyword {
		t.Fatalf("mode = %q, want keyword fallback", resp.Mode)
	}
	if len(resp.Items) == 0 || resp.Items[0].Title != "SensitiveNeedle Result" {
		t.Fatalf("fallback items = %#v, want SensitiveNeedle Result", resp.Items)
	}
	if strings.Contains(logs.String(), "SensitiveNeedle") {
		t.Fatalf("logs leaked raw query: %s", logs.String())
	}
}

// TestSearchModeRecommendOptIn covers the ADR-0053 recommend retrieval mode at
// the resolver layer: the per-Profile opt-in gate (default off) and the
// engagement-seeded happy path. An opted-out Profile gets nothing even with an
// active embedder and engagement; once opted in via the existing per-Profile
// preference store, the nearest content-neighbour of the engagement seed
// surfaces with mode=recommend.
//
// feature:search-mode
func TestSearchModeRecommendOptIn(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profile = store.ProfileID("profile-default")
	vec := []float32{1, 0, 0}
	near := []float32{0.95, 0.05, 0}

	// Seed the engagement seed (played) and a near content-neighbour, both in the
	// visible local library, with embeddings of the active kind.
	seedMedia, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/m/recseed.mp4", Title: "RecSeed",
		Container: "mp4", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: "sha256:recseed",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile seed: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, "sha256:recseed", "text-v1", vec); err != nil {
		t.Fatalf("UpsertEmbedding seed: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: "/m/recnear.mp4", Title: "RecNear",
		Container: "mp4", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: "sha256:recnear",
	}); err != nil {
		t.Fatalf("ImportMediaFile near: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, "sha256:recnear", "text-v1", near); err != nil {
		t.Fatalf("UpsertEmbedding near: %v", err)
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: profile, MediaID: seedMedia.ID, PositionSeconds: 30, DurationSeconds: 600,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatalf("UpsertPlaybackProgress: %v", err)
	}

	handler := newTestHandlerWithStore(st)
	handler.SetQueryEmbedder(stubEmbedder{kind: "text-v1", available: true, vector: vec})

	recommendQuery := `{"query":"query S($mode:SearchMode){ searchResult(mode:$mode){ mode items { id title score } } }","variables":{"mode":"recommend"}}`

	// Opted-OUT (default): recommend mode is served but yields nothing — the
	// opt-in gate is enforced even with engagement + an active embedder.
	data, errs := post(t, handler, recommendQuery, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("recommend opted-out errors = %#v", errs)
	}
	var resp SearchResultResponse
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeRecommend {
		t.Errorf("served mode = %q, want recommend", resp.Mode)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("opted-out recommend returned %d items, want 0 (opt-in enforced)", len(resp.Items))
	}

	// Opt in via the existing per-Profile preference store (no new storage).
	if err := st.SetClientPref(ctx, profile, "recommend.opt-in", "on", time.Now().UTC()); err != nil {
		t.Fatalf("SetClientPref opt-in: %v", err)
	}

	// Opted-IN: the near content-neighbour of the seed surfaces, the seed itself
	// is excluded, mode=recommend.
	data, errs = post(t, handler, recommendQuery, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("recommend opted-in errors = %#v", errs)
	}
	mustUnmarshal(t, data["searchResult"], &resp)
	if resp.Mode != SearchModeRecommend {
		t.Errorf("served mode = %q, want recommend", resp.Mode)
	}
	var sawNear, sawSeed bool
	for _, item := range resp.Items {
		switch item.Title {
		case "RecNear":
			sawNear = true
		case "RecSeed":
			sawSeed = true
		}
	}
	if sawSeed {
		t.Error("recommend returned the engagement seed itself")
	}
	if !sawNear {
		t.Fatalf("opted-in recommend omitted the neighbour; items = %#v", resp.Items)
	}
}

func findIndexStatus(statuses []LibraryIndexStatusInfo, id string) *LibraryIndexStatusInfo {
	for i := range statuses {
		if statuses[i].LibraryID == id {
			return &statuses[i]
		}
	}
	return nil
}

func assertCanonicalTag(t *testing.T, st *store.Store, id store.MediaID, field, want string) {
	t.Helper()
	fields, err := st.MediaFields(context.Background(), id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("MediaFields: %v", err)
	}
	for _, f := range fields {
		if f.Name == field {
			if f.Text != want {
				t.Errorf("canonical field %q = %q, want %q", field, f.Text, want)
			}
			return
		}
	}
	t.Errorf("canonical field %q not found after accept", field)
}

func assertNoCanonicalTag(t *testing.T, st *store.Store, id store.MediaID, field string) {
	t.Helper()
	fields, err := st.MediaFields(context.Background(), id, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("MediaFields: %v", err)
	}
	for _, f := range fields {
		if f.Name == field {
			t.Fatalf("canonical field %q present (%q) but should be absent — staged tag must not reach canonical without accept", field, f.Text)
		}
	}
}
