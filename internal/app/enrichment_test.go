package app

import (
	"context"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

const enrichmentKind = store.EmbeddingKind("fixture-embedding")

type testEnrichmentProvider struct{ enabled bool }

func (p testEnrichmentProvider) Available() bool         { return p.enabled }
func (testEnrichmentProvider) Kind() store.EmbeddingKind { return enrichmentKind }
func (testEnrichmentProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return fixtureEmbedding(text), nil
}
func (testEnrichmentProvider) EmbedDocument(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return fixtureEmbedding(text), nil
}

func newTestEnrichmentProvider(enabled bool) testEnrichmentProvider {
	return testEnrichmentProvider{enabled: enabled}
}

type unavailableEnrichmentProviderSpy struct {
	kindCalls          int
	embedDocumentCalls int
}

func (*unavailableEnrichmentProviderSpy) Available() bool { return false }
func (p *unavailableEnrichmentProviderSpy) Kind() store.EmbeddingKind {
	p.kindCalls++
	return enrichmentKind
}
func (*unavailableEnrichmentProviderSpy) EmbedQuery(context.Context, string) ([]float32, error) {
	return nil, nil
}
func (p *unavailableEnrichmentProviderSpy) EmbedDocument(context.Context, string) ([]float32, error) {
	p.embedDocumentCalls++
	return nil, nil
}

type countingEmbeddingStore struct {
	store                *store.Store
	candidateCalls       int
	failureRecordCalls   int
	upsertEmbeddingCalls int
}

func (s *countingEmbeddingStore) MediaEmbeddingCandidates(ctx context.Context, kind store.EmbeddingKind, limit int) ([]store.MediaEmbeddingCandidate, error) {
	s.candidateCalls++
	return s.store.MediaEmbeddingCandidates(ctx, kind, limit)
}

func (s *countingEmbeddingStore) RecordEnrichmentFailure(ctx context.Context, mediaID store.MediaID, kind store.EmbeddingKind, errText string) error {
	s.failureRecordCalls++
	return s.store.RecordEnrichmentFailure(ctx, mediaID, kind, errText)
}

func (s *countingEmbeddingStore) UpsertEmbedding(ctx context.Context, fingerprint string, kind store.EmbeddingKind, vector []float32) error {
	s.upsertEmbeddingCalls++
	return s.store.UpsertEmbedding(ctx, fingerprint, kind, vector)
}

func fixtureEmbedding(text string) []float32 {
	const dimensions = 64
	vector := make([]float32, dimensions)
	for _, token := range strings.Fields(strings.ToLower(text)) {
		token = strings.Trim(token, ",.;:!?()[]{}")
		if token == "" {
			continue
		}
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(token))
		vector[hash.Sum32()%dimensions]++
	}
	return vector
}

// These core-side integration tests exercise the store-coupled indexing loop
// and Reveal contract with a deterministic local provider. First-party plugin
// implementation behavior stays in the plugin module's own tests.

// enrichSeedMedia imports a Media file into the named library and returns its ID.
func enrichSeedMedia(t *testing.T, st *store.Store, libraryID, path, title, summary, fp string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: store.LibraryID(libraryID), Path: path, Title: title, Summary: summary,
		Container: "mp4", Codec: "h264", ContentFingerprint: fp,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile %s: %v", path, err)
	}
	return media.ID
}

func enrichOpenSeed(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{SeedProfile: store.SeedProfileConformance})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestIndexAllPopulatesAndSemanticSearchFinds(t *testing.T) {
	st := enrichOpenSeed(t)
	ctx := context.Background()
	revealed := store.LibraryFilter{Revealed: true, AllLibraries: true}

	target := enrichSeedMedia(t, st, "library-local", "/m/a.mp4",
		"Atmospheric Space Mystery", "A slow-burn mystery aboard a derelict space station", "sha256:enr-a")
	enrichSeedMedia(t, st, "library-local", "/m/b.mp4",
		"Paris Romance", "A light romantic comedy set in springtime Paris", "sha256:enr-b")

	// Enabled provider indexes every candidate.
	p := newTestEnrichmentProvider(true)
	n, err := indexEmbeddings(ctx, st, p)
	if err != nil {
		t.Fatalf("indexEmbeddings: %v", err)
	}
	if n < 2 {
		t.Fatalf("indexEmbeddings wrote %d embeddings, want >= 2 (the two seeded items)", n)
	}

	// Idempotent: a second pass writes nothing (all candidates already indexed).
	again, err := indexEmbeddings(ctx, st, p)
	if err != nil {
		t.Fatalf("indexEmbeddings second pass: %v", err)
	}
	if again != 0 {
		t.Fatalf("indexEmbeddings re-indexed %d already-indexed items, want 0", again)
	}

	// Semantic search returns the matching item, top-ranked.
	qv, err := p.EmbedQuery(ctx, "mystery on a space station")
	if err != nil {
		t.Fatal(err)
	}
	results, err := st.SemanticSearch(ctx, revealed, enrichmentKind, qv, 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SemanticSearch returned empty after indexing")
	}
	if results[0].MediaID != target {
		t.Fatalf("top result = %q, want the space-mystery item %q (results=%v)",
			results[0].MediaID, target, results)
	}
}

func TestDisabledProviderWritesNoEmbeddings(t *testing.T) {
	st := enrichOpenSeed(t)
	ctx := context.Background()

	enrichSeedMedia(t, st, "library-local", "/m/c.mp4",
		"Atmospheric Space Mystery", "A slow-burn mystery aboard a space station", "sha256:enr-c")

	// Unavailable providers return before Kind, candidate enumeration, or writes.
	p := &unavailableEnrichmentProviderSpy{}
	countingStore := &countingEmbeddingStore{store: st}
	n, err := indexEmbeddings(ctx, countingStore, p)
	if err != nil {
		t.Fatalf("indexEmbeddings disabled: %v", err)
	}
	if n != 0 {
		t.Fatalf("disabled provider wrote %d embeddings, want 0", n)
	}
	if p.kindCalls != 0 {
		t.Fatalf("unavailable provider Kind calls = %d, want 0", p.kindCalls)
	}
	if p.embedDocumentCalls != 0 {
		t.Fatalf("unavailable provider EmbedDocument calls = %d, want 0", p.embedDocumentCalls)
	}
	if countingStore.candidateCalls != 0 {
		t.Fatalf("unavailable provider candidate enumeration calls = %d, want 0", countingStore.candidateCalls)
	}
	if countingStore.failureRecordCalls != 0 {
		t.Fatalf("unavailable provider failure-record calls = %d, want 0", countingStore.failureRecordCalls)
	}
	if countingStore.upsertEmbeddingCalls != 0 {
		t.Fatalf("unavailable provider embedding write calls = %d, want 0", countingStore.upsertEmbeddingCalls)
	}

	// The candidate is still pending (nothing was written), and semantic search
	// against any query is empty.
	pending, err := st.MediaEmbeddingCandidates(ctx, enrichmentKind, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) == 0 {
		t.Fatal("expected the unindexed item to remain a candidate")
	}
	qv := fixtureEmbedding("space mystery")
	results, err := st.SemanticSearch(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true}, enrichmentKind, qv, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("semantic search returned %d results with no embeddings written, want 0", len(results))
	}
}

func TestRevealContractHoldsAfterIndexing(t *testing.T) {
	st := enrichOpenSeed(t)
	ctx := context.Background()

	// One item in the unrestricted library, one in the restricted library, with
	// the SAME text so they would score identically — the only thing keeping the
	// restricted one out of an unrevealed result is the Reveal contract.
	const text = "atmospheric space mystery aboard a derelict station"
	local := enrichSeedMedia(t, st, "library-local", "/m/local.mp4", "Local", text, "sha256:enr-local")
	archive := enrichSeedMedia(t, st, "library-archive", "/m/archive.mp4", "Archive", text, "sha256:enr-archive")

	p := newTestEnrichmentProvider(true)
	if _, err := indexEmbeddings(ctx, st, p); err != nil {
		t.Fatalf("indexEmbeddings: %v", err)
	}

	qv := fixtureEmbedding(text)

	// Unrevealed caller: the restricted item must NOT appear.
	unrevealed, err := st.SemanticSearch(ctx, store.LibraryFilter{Revealed: false}, enrichmentKind, qv, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range unrevealed {
		if r.MediaID == archive {
			t.Fatalf("restricted media %q leaked to unrevealed semantic caller", archive)
		}
	}

	// Revealed caller: both visible.
	revealed, err := st.SemanticSearch(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true}, enrichmentKind, qv, 50)
	if err != nil {
		t.Fatal(err)
	}
	var sawLocal, sawArchive bool
	for _, r := range revealed {
		switch r.MediaID {
		case local:
			sawLocal = true
		case archive:
			sawArchive = true
		}
	}
	if !sawLocal {
		t.Error("unrestricted media missing from revealed semantic results")
	}
	if !sawArchive {
		t.Error("restricted media missing from REVEALED semantic results")
	}
}
