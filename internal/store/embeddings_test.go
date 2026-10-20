package store

// Tests for the ADR-0041 embedding store: UpsertEmbedding, SemanticSearch,
// ProposePendingAITag, SetPendingAITagStatus, RecordEnrichmentFailure, and the
// semantic reveal-contract conformance (the hard ADR-0041 rule that restricted
// Media is never returned by SemanticSearch to an unrevealed caller).

import (
	"context"
	"strings"
	"testing"
	"time"
)

func openConformanceSeed(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), t.TempDir(), Options{SeedProfile: SeedProfileConformance})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// unit32Vector returns a slice of n float32 values each equal to v.
func uniformVector(n int, v float32) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// --- SanitizeEmbeddingInput -------------------------------------------------

func TestSanitizeEmbeddingInputOK(t *testing.T) {
	out, err := SanitizeEmbeddingInput("  hello   world  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("got %q, want %q", out, "hello world")
	}
}

func TestSanitizeEmbeddingInputEmpty(t *testing.T) {
	if _, err := SanitizeEmbeddingInput("   "); err == nil {
		t.Fatal("accepted blank input")
	}
}

func TestSanitizeEmbeddingInputTooLong(t *testing.T) {
	long := strings.Repeat("x", MaxEmbeddingInputBytes+1)
	if _, err := SanitizeEmbeddingInput(long); err == nil {
		t.Fatal("accepted oversized input")
	}
}

// --- UpsertEmbedding validation ----------------------------------------------

func TestUpsertEmbeddingValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	vec := uniformVector(4, 1.0)
	const fp = "sha256:aabbcc"
	const kind = EmbeddingKind("text-v1")

	if err := st.UpsertEmbedding(ctx, "", kind, vec); err == nil {
		t.Fatal("accepted empty fingerprint")
	}
	if err := st.UpsertEmbedding(ctx, fp, "", vec); err == nil {
		t.Fatal("accepted empty kind")
	}
	if err := st.UpsertEmbedding(ctx, fp, kind, nil); err == nil {
		t.Fatal("accepted nil vector")
	}
}

func TestUpsertEmbeddingRejectsDimensionalityChangeForSameKey(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const fp = "sha256:stable-dims"
	const kind = EmbeddingKind("text-v1")

	if err := st.UpsertEmbedding(ctx, fp, kind, uniformVector(4, 1.0)); err != nil {
		t.Fatalf("initial UpsertEmbedding: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, fp, kind, uniformVector(8, 1.0)); err == nil {
		t.Fatal("accepted dimensionality change for same fingerprint+kind")
	}
	if err := st.UpsertEmbedding(ctx, fp, EmbeddingKind("text-v2"), uniformVector(8, 1.0)); err != nil {
		t.Fatalf("new kind with new dimensionality should be allowed: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, fp, kind, uniformVector(4, 0.5)); err != nil {
		t.Fatalf("same dimensionality rewrite should be allowed: %v", err)
	}
}

// --- UpsertEmbedding round-trip via SemanticSearch --------------------------

func TestSemanticSearchFindsUpsertedEmbedding(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const kind = EmbeddingKind("text-v1")
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	// Retrieve the fingerprint for media-alpha (the development seed media in
	// library-local, which is not restricted).
	var fingerprint string
	if err := st.catalogR.QueryRowContext(ctx,
		`SELECT content_fingerprint FROM media_file WHERE media_id = 'media-alpha'`,
	).Scan(&fingerprint); err != nil {
		// media-alpha has no MediaFile in the development seed (file plane); use
		// a synthetic fingerprint that we JOIN to via a direct SQL insert below.
		fingerprint = "sha256:000000alpha"
	}

	// Insert a media_file row that links the fingerprint to media-alpha so the
	// JOIN in semanticSearchSQL can resolve it.
	if _, err := st.catalogW.ExecContext(ctx,
		`INSERT OR IGNORE INTO media_file (id, media_id, path, content_fingerprint, missing_at_ms)
		 VALUES (?, 'media-alpha', '/dev/null', ?, NULL)`, fingerprint, fingerprint,
	); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}

	vec := uniformVector(4, 0.5)
	if err := st.UpsertEmbedding(ctx, fingerprint, kind, vec); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}

	results, err := st.SemanticSearch(ctx, filter, kind, vec, 10)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SemanticSearch returned empty results, want media-alpha")
	}
	found := false
	for _, r := range results {
		if r.MediaID == "media-alpha" {
			found = true
			if r.Score < 0.99 {
				t.Fatalf("identical vectors: score = %v, want ≈1.0", r.Score)
			}
		}
	}
	if !found {
		t.Fatalf("media-alpha not in results: %v", results)
	}
}

// --- Semantic Reveal Contract Conformance (ADR-0041 §semantic-reveal-contract) ---
//
// This is the hard rule: restricted-library Media MUST NOT appear in
// SemanticSearch results when filter.Revealed is false, even if a matching
// embedding exists for it.  The conformance seed contains:
//   - media-alpha   in library-local  (not restricted)
//   - media-archive in library-archive (is_restricted = 1)
//
// We seed embeddings for both, then assert that an unrevealed caller only ever
// sees media-alpha.

func TestSemanticRevealContract(t *testing.T) {
	st := openConformanceSeed(t)
	ctx := context.Background()
	const kind = EmbeddingKind("text-v1")

	// The conformance seed does provide MediaFile rows for both media items.
	// Retrieve fingerprints from the DB so the test stays in sync with the seed.
	fpFor := func(mediaID string) string {
		var fp string
		err := st.catalogR.QueryRowContext(ctx,
			`SELECT content_fingerprint FROM media_file WHERE media_id = ?`, mediaID,
		).Scan(&fp)
		if err != nil {
			// No media_file row in the conformance seed for this run of the test;
			// plant a synthetic fingerprint linked to the media row.
			fp = "sha256:synth-" + mediaID
			if _, insErr := st.catalogW.ExecContext(ctx,
				`INSERT OR IGNORE INTO media_file (id, media_id, path, content_fingerprint, missing_at_ms)
				 VALUES (?, ?, '/dev/null', ?, NULL)`, fp, mediaID, fp,
			); insErr != nil {
				t.Fatalf("insert media_file for %s: %v", mediaID, insErr)
			}
		}
		return fp
	}

	alphaFP := fpFor("media-alpha")
	archiveFP := fpFor("media-archive")

	// Both items get the same vector so cosine similarity ranks them equally —
	// the test is about inclusion, not ordering.
	vec := uniformVector(8, 1.0)
	if err := st.UpsertEmbedding(ctx, alphaFP, kind, vec); err != nil {
		t.Fatalf("UpsertEmbedding alpha: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, archiveFP, kind, vec); err != nil {
		t.Fatalf("UpsertEmbedding archive: %v", err)
	}

	// Unrevealed caller: restricted library must be invisible.
	unrevealed := LibraryFilter{Revealed: false}
	results, err := st.SemanticSearch(ctx, unrevealed, kind, vec, 50)
	if err != nil {
		t.Fatalf("SemanticSearch unrevealed: %v", err)
	}
	for _, r := range results {
		if r.MediaID == "media-archive" {
			t.Fatalf("SemanticSearch leaked restricted media-archive to unrevealed caller (score=%v)", r.Score)
		}
	}

	// Revealed caller: both items should be visible.
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}
	results, err = st.SemanticSearch(ctx, revealed, kind, vec, 50)
	if err != nil {
		t.Fatalf("SemanticSearch revealed: %v", err)
	}
	var sawAlpha, sawArchive bool
	for _, r := range results {
		switch r.MediaID {
		case "media-alpha":
			sawAlpha = true
		case "media-archive":
			sawArchive = true
		}
	}
	if !sawAlpha {
		t.Error("media-alpha missing from revealed SemanticSearch results")
	}
	if !sawArchive {
		t.Error("media-archive missing from revealed SemanticSearch results")
	}
}

func TestSemanticSearchHidesSealedEncryptedLibrary(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const libraryID = LibraryID("library-local")
	const fingerprint = "sha256:sealed-semantic"
	const kind = EmbeddingKind("text-v1")
	vec := uniformVector(4, 1.0)
	filter := LibraryFilter{AllLibraries: true}

	if _, err := st.catalogW.ExecContext(ctx,
		`INSERT OR IGNORE INTO media_file (id, media_id, path, content_fingerprint, missing_at_ms)
		 VALUES (?, 'media-alpha', '/dev/null/sealed-semantic', ?, NULL)`, fingerprint, fingerprint,
	); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, fingerprint, kind, vec); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}

	results, err := st.SemanticSearch(ctx, filter, kind, vec, 10)
	if err != nil {
		t.Fatalf("SemanticSearch unsealed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("unsealed encrypted library returned no semantic results")
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	results, err = st.SemanticSearch(ctx, filter, kind, vec, 10)
	if err != nil {
		t.Fatalf("SemanticSearch sealed: %v", err)
	}
	for _, result := range results {
		if result.MediaID == "media-alpha" {
			t.Fatalf("sealed encrypted library leaked semantic result: %v", results)
		}
	}
}

// --- Stale-dimension guard (H20) --------------------------------------------
//
// A kind may hold mixed-dimension vectors (e.g. after a 768→1024 model
// upgrade). A stored row whose dims differ from the query vector is not
// comparable: it must be excluded from the ranked results, never scored on a
// truncated overlap. We import two distinct Media into the (non-restricted)
// library-local, embed one at the query dimensionality and one at a different
// dimensionality, and assert only the matching-dim row is returned.

func TestSemanticSearchExcludesMismatchedDimension(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const kind = EmbeddingKind("text-v1")
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	const matchFP = "sha256:dim-match"
	const mismatchFP = "sha256:dim-mismatch"

	matchMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/dim-match.mp4", Title: "DimMatch",
		Container: "mp4", Codec: "h264", ContentFingerprint: matchFP,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile match: %v", err)
	}
	mismatchMedia, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/dim-mismatch.mp4", Title: "DimMismatch",
		Container: "mp4", Codec: "h264", ContentFingerprint: mismatchFP,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile mismatch: %v", err)
	}

	// Same kind, different dimensionality: 8 dims for the match, 4 for the stale row.
	if err := st.UpsertEmbedding(ctx, matchFP, kind, uniformVector(8, 1.0)); err != nil {
		t.Fatalf("UpsertEmbedding match: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, mismatchFP, kind, uniformVector(4, 1.0)); err != nil {
		t.Fatalf("UpsertEmbedding mismatch: %v", err)
	}

	// Query at the matching (8) dimensionality.
	results, err := st.SemanticSearch(ctx, filter, kind, uniformVector(8, 1.0), 50)
	if err != nil {
		t.Fatalf("SemanticSearch: %v", err)
	}

	var sawMatch, sawMismatch bool
	for _, r := range results {
		switch r.MediaID {
		case matchMedia.ID:
			sawMatch = true
			if r.Score < 0.99 {
				t.Fatalf("matching-dim row: score = %v, want ≈1.0", r.Score)
			}
		case mismatchMedia.ID:
			sawMismatch = true
		}
	}
	if !sawMatch {
		t.Fatalf("matching-dim media %q missing from results: %v", matchMedia.ID, results)
	}
	if sawMismatch {
		t.Fatalf("mismatched-dim media %q was ranked (truncated-overlap leak): %v", mismatchMedia.ID, results)
	}
}

func TestSemanticSearchCapsCandidateScanAt100KScale(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const (
		kind  = EmbeddingKind("perf-v1")
		scale = 100_000
	)
	vector := uniformVector(4, 1)
	blob := float32SliceToBlob(vector)

	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`
			WITH RECURSIVE seq(n) AS (
				VALUES(0)
				UNION ALL
				SELECT n + 1 FROM seq WHERE n < ?
			)
			INSERT INTO media (id, library_id, title, duration_seconds, container, codec)
			SELECT printf('semantic-perf-media-%06d', n), 'library-local',
			       printf('Semantic Perf %06d', n), 1, 'mp4', 'h264'
			FROM seq
		`, []any{scale - 1}},
		{`
			WITH RECURSIVE seq(n) AS (
				VALUES(0)
				UNION ALL
				SELECT n + 1 FROM seq WHERE n < ?
			)
			INSERT INTO media_file (id, media_id, path, content_fingerprint)
			SELECT printf('semantic-perf-file-%06d', n),
			       printf('semantic-perf-media-%06d', n),
			       printf('/semantic-perf/%06d.mp4', n),
			       printf('sha256:semantic-perf-%06d', n)
			FROM seq
		`, []any{scale - 1}},
		{`
			WITH RECURSIVE seq(n) AS (
				VALUES(0)
				UNION ALL
				SELECT n + 1 FROM seq WHERE n < ?
			)
			INSERT INTO media_embedding
			       (media_fingerprint, embedding_kind, dims, vector, indexed_at_ms)
			SELECT printf('sha256:semantic-perf-%06d', n), ?, 4, ?, n
			FROM seq
		`, []any{scale - 1, string(kind), blob}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed 100k semantic candidates: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit 100k semantic candidates: %v", err)
	}

	query := semanticSearchCandidateQuery([]string{"?"}, "")
	planRows, err := st.catalogR.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query,
		"library-local", string(kind), len(vector), maxSemanticSearchCandidates)
	if err != nil {
		t.Fatalf("explain candidate query: %v", err)
	}
	defer planRows.Close()
	var plan strings.Builder
	for planRows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := planRows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := planRows.Err(); err != nil {
		t.Fatalf("query plan rows: %v", err)
	}
	planText := plan.String()
	if strings.Contains(planText, "SCAN e") {
		t.Fatalf("candidate query scans the 100k-row embedding table:\n%s", planText)
	}
	if strings.Contains(planText, "USE TEMP B-TREE FOR ORDER BY") {
		t.Fatalf("candidate query sorts the full eligible set before LIMIT:\n%s", planText)
	}
	if !strings.Contains(planText, "SEARCH e USING INDEX") ||
		!strings.Contains(planText, "media_fingerprint=? AND embedding_kind=?") {
		t.Fatalf("candidate query does not use the fingerprint+kind index:\n%s", planText)
	}

	results, err := st.SemanticSearch(ctx,
		LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true},
		kind, vector, scale)
	if err != nil {
		t.Fatalf("SemanticSearch at 100k scale: %v", err)
	}
	if got := len(results); got != maxSemanticSearchCandidates {
		t.Fatalf("scored candidates = %d, want hard cap %d", got, maxSemanticSearchCandidates)
	}
}

func TestPurgeTrashedBeforeDeletesEmbeddingForPurgedFingerprint(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	const purgedFP = "sha256:embedding-purge"
	const survivorFP = "sha256:embedding-survivor"
	purged, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/embedding-purge.mp4", Title: "Embedding Purge",
		Container: "mp4", Codec: "h264", ContentFingerprint: purgedFP,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile purged: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-archive", Path: "/m/embedding-survivor.mp4", Title: "Embedding Survivor",
		Container: "mp4", Codec: "h264", ContentFingerprint: survivorFP,
	}); err != nil {
		t.Fatalf("ImportMediaFile survivor: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, purgedFP, "text-v1", uniformVector(4, 1)); err != nil {
		t.Fatalf("UpsertEmbedding purged: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, survivorFP, "text-v1", uniformVector(4, 1)); err != nil {
		t.Fatalf("UpsertEmbedding survivor: %v", err)
	}
	if err := st.TrashMedia(ctx, purged.ID, filter); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}
	if _, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("PurgeTrashedBefore: %v", err)
	}

	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM media_embedding WHERE media_fingerprint = '`+purgedFP+`'`); got != 0 {
		t.Fatalf("purged embedding rows = %d, want 0", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM media_embedding WHERE media_fingerprint = '`+survivorFP+`'`); got != 1 {
		t.Fatalf("survivor embedding rows = %d, want 1", got)
	}
}

func TestCosineSimilarityMismatchedDimension(t *testing.T) {
	if s := cosineSimilarity([]float32{1, 2, 3}, []float32{1, 2}); s != cosineSimilarityMismatch {
		t.Fatalf("mismatched dims: score = %v, want sentinel %v", s, cosineSimilarityMismatch)
	}
}

// --- MediaEmbeddingCandidates (ENR-1 write-path enumeration) ----------------

func TestMediaEmbeddingCandidatesRespectsAIIndexingAndIndexedState(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const kind = EmbeddingKind("text-v1")

	indexed, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/cand-indexed.mp4", Title: "Indexed",
		Container: "mp4", Codec: "h264", ContentFingerprint: "sha256:cand-indexed",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile indexed: %v", err)
	}
	pending, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/cand-pending.mp4", Title: "Pending",
		Container: "mp4", Codec: "h264", ContentFingerprint: "sha256:cand-pending",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile pending: %v", err)
	}

	// One of the two already has an embedding → only the other is a candidate.
	if err := st.UpsertEmbedding(ctx, "sha256:cand-indexed", kind, uniformVector(8, 1.0)); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}

	candidates, err := st.MediaEmbeddingCandidates(ctx, kind, 100)
	if err != nil {
		t.Fatalf("MediaEmbeddingCandidates: %v", err)
	}
	ids := map[MediaID]bool{}
	for _, c := range candidates {
		ids[c.MediaID] = true
	}
	if ids[indexed.ID] {
		t.Error("already-indexed media returned as a candidate")
	}
	if !ids[pending.ID] {
		t.Error("unindexed media missing from candidates")
	}

	// Opt the Library out of AI indexing: NO media in it is a candidate anymore.
	if err := st.SetLibraryAIIndexing(ctx, "library-local", false); err != nil {
		t.Fatalf("SetLibraryAIIndexing: %v", err)
	}
	after, err := st.MediaEmbeddingCandidates(ctx, kind, 100)
	if err != nil {
		t.Fatalf("MediaEmbeddingCandidates after opt-out: %v", err)
	}
	for _, c := range after {
		if c.LibraryID == "library-local" {
			t.Fatalf("opted-out Library still yielded candidate %q", c.MediaID)
		}
	}
}

func TestMediaEmbeddingCandidatesIncludesDescriptionAndTextMetadata(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const kind = EmbeddingKind("text-v1")

	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/podcast-episode.mp3", Title: "Episode",
		Container: "mp3", Codec: "mp3", Summary: "description phrase",
		ContentFingerprint: "sha256:podcast-episode",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.SetMediaFields(ctx, media.ID, []MediaField{
		{Name: "transcript", Type: EntityFieldText, Text: "transcript phrase"},
	}); err != nil {
		t.Fatal(err)
	}

	candidates, err := st.MediaEmbeddingCandidates(ctx, kind, 100)
	if err != nil {
		t.Fatalf("MediaEmbeddingCandidates: %v", err)
	}
	for _, c := range candidates {
		if c.MediaID == media.ID {
			if !strings.Contains(c.Summary, "description phrase") || !strings.Contains(c.Summary, "transcript phrase") {
				t.Fatalf("candidate summary = %q, want description and transcript text", c.Summary)
			}
			return
		}
	}
	t.Fatalf("candidate for %s not found", media.ID)
}

// --- RetryCapEnforcement via RecordEnrichmentFailure ------------------------

func TestEnrichmentRetryCapBlocks(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const kind = EmbeddingKind("text-v1")

	// Record MaxEnrichmentAttempts failures for media-alpha.
	for i := 0; i < MaxEnrichmentAttempts; i++ {
		if err := st.RecordEnrichmentFailure(ctx, "media-alpha", kind, "timeout"); err != nil {
			t.Fatalf("RecordEnrichmentFailure %d: %v", i+1, err)
		}
	}

	// Now ensure the media-alpha has a media_file so UpsertEmbedding can look it up.
	const fp = "sha256:synth-alpha-cap"
	if _, err := st.catalogW.ExecContext(ctx,
		`INSERT OR IGNORE INTO media_file (id, media_id, path, content_fingerprint, missing_at_ms)
		 VALUES (?, 'media-alpha', '/dev/null/cap', ?, NULL)`, fp, fp,
	); err != nil {
		t.Fatalf("insert media_file: %v", err)
	}
	// The retry cap check in UpsertEmbedding queries by fingerprint->media_id
	// using the failed_enrichment table which is keyed by media_id not fingerprint.
	// Since the failed_enrichment rows are keyed by media_id='media-alpha', a
	// subsequent UpsertEmbedding call for the same fingerprint should hit the cap.
	vec := uniformVector(4, 1.0)
	if err := st.UpsertEmbedding(ctx, fp, kind, vec); err == nil {
		t.Fatal("UpsertEmbedding accepted a write after max retry cap was reached")
	}
}

// --- PendingAITag lifecycle -------------------------------------------------

func TestPendingAITagLifecycle(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	tag := PendingAITag{
		ID:         "tag-001",
		MediaID:    "media-alpha",
		TagKind:    "genre",
		Value:      "comedy",
		ProposedBy: "ai",
	}
	if err := st.ProposePendingAITag(ctx, tag); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}

	tags, err := st.PendingAITags(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("PendingAITags: %v", err)
	}
	if len(tags) != 1 || tags[0].Status != PendingAITagPending {
		t.Fatalf("expected 1 pending tag, got %v", tags)
	}

	if err := st.SetPendingAITagStatus(ctx, "tag-001", PendingAITagApproved); err != nil {
		t.Fatalf("SetPendingAITagStatus: %v", err)
	}

	tags, err = st.PendingAITags(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("PendingAITags after approve: %v", err)
	}
	if len(tags) != 1 || tags[0].Status != PendingAITagApproved {
		t.Fatalf("expected approved tag, got %v", tags)
	}
}

func TestPendingAITagRevealContract(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// media-archive is in library-archive (is_restricted = true).
	tag := PendingAITag{
		ID:         "tag-restricted",
		MediaID:    "media-archive",
		TagKind:    "genre",
		Value:      "drama",
		ProposedBy: "ai",
	}
	if err := st.ProposePendingAITag(ctx, tag); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}

	// Unrevealed caller must see nothing.
	tags, err := st.PendingAITags(ctx, "media-archive", LibraryFilter{Revealed: false})
	if err != nil {
		t.Fatalf("PendingAITags unrevealed: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("PendingAITags leaked restricted tags to unrevealed caller: %v", tags)
	}

	// Revealed caller can see the proposal.
	tags, err = st.PendingAITags(ctx, "media-archive", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("PendingAITags revealed: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("revealed caller: expected 1 tag, got %v", tags)
	}
}

func TestPendingAITagValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	base := PendingAITag{ID: "v1", MediaID: "media-alpha", TagKind: "genre", Value: "comedy", ProposedBy: "ai"}

	missing := func(field string, tag PendingAITag) {
		if err := st.ProposePendingAITag(ctx, tag); err == nil {
			t.Errorf("accepted tag with empty %s", field)
		}
	}
	t0 := base
	t0.ID = ""
	missing("id", t0)
	t1 := base
	t1.MediaID = ""
	missing("media_id", t1)
	t2 := base
	t2.TagKind = ""
	missing("tag_kind", t2)
	t3 := base
	t3.Value = ""
	missing("value", t3)
	t4 := base
	t4.ProposedBy = ""
	missing("proposed_by", t4)

	if err := st.SetPendingAITagStatus(ctx, "", PendingAITagApproved); err == nil {
		t.Fatal("SetPendingAITagStatus accepted empty id")
	}
	if err := st.SetPendingAITagStatus(ctx, "v1", "pending"); err == nil {
		t.Fatal("SetPendingAITagStatus accepted 'pending' as target status")
	}
}

// --- vector encoding round-trip ---------------------------------------------

func TestVectorBlobRoundTrip(t *testing.T) {
	orig := []float32{1.0, -0.5, 0.0, 3.14}
	blob := float32SliceToBlob(orig)
	back, err := blobToFloat32Slice(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(back) != len(orig) {
		t.Fatalf("len %d, want %d", len(back), len(orig))
	}
	for i := range orig {
		if back[i] != orig[i] {
			t.Fatalf("element %d: got %v, want %v", i, back[i], orig[i])
		}
	}
}

func TestCosineSimilarityIdentical(t *testing.T) {
	v := []float32{1, 2, 3}
	if s := cosineSimilarity(v, v); s < 0.999 {
		t.Fatalf("identical vectors: score = %v, want ≈1.0", s)
	}
}

func TestCosineSimilarityZeroVector(t *testing.T) {
	z := []float32{0, 0, 0}
	v := []float32{1, 2, 3}
	if s := cosineSimilarity(z, v); s != 0 {
		t.Fatalf("zero vector: score = %v, want 0", s)
	}
}

// --- PromotePendingAITag (the §2.5 approval gate) ---------------------------

// TestPromotePendingAITagGate confirms the only canonical-promotion path:
// promote writes the staged value as a media_field, stamps machine-derived
// provenance, flips the proposal to approved, and refuses replay.
func TestPromotePendingAITagGate(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-1", MediaID: "media-alpha", TagKind: "mood", Value: "noir", ProposedBy: "ai",
	}); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}

	// Before promotion: no canonical mood field.
	fields, err := st.MediaFields(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("MediaFields: %v", err)
	}
	if hasField(fields, "mood") {
		t.Fatal("mood field present before promotion — staged must not be canonical")
	}

	if err := st.PromotePendingAITag(ctx, "pt-1", LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatalf("PromotePendingAITag: %v", err)
	}

	// After: canonical mood=noir, mutated_by=machine-derived, proposal approved.
	fields, _ = st.MediaFields(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if v, ok := fieldText(fields, "mood"); !ok || v != "noir" {
		t.Fatalf("canonical mood = %q (ok=%v), want noir", v, ok)
	}
	media, err := st.Media(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if media.MutatedBy != MutationSourceAI {
		t.Errorf("mutatedBy = %q, want %q", media.MutatedBy, MutationSourceAI)
	}
	tags, _ := st.PendingAITags(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if len(tags) != 1 || tags[0].Status != PendingAITagApproved {
		t.Fatalf("proposal status = %v, want approved", tags)
	}

	// Re-promote fails: the gate is not replayable.
	if err := st.PromotePendingAITag(ctx, "pt-1", LibraryFilter{Revealed: true, AllLibraries: true}); err == nil {
		t.Error("second PromotePendingAITag should fail (no longer pending)")
	}
}

func TestLegacyAISourceReadsAsMachineDerived(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media SET mutated_by = 'ai' WHERE id = ?`, "media-alpha"); err != nil {
		t.Fatalf("seed legacy source: %v", err)
	}
	media, err := st.Media(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if media.MutatedBy != MutationSourceAI {
		t.Fatalf("legacy AI source read as %q, want %q", media.MutatedBy, MutationSourceAI)
	}
}

func TestPendingAITagRunIDIsDurableAndPromoteUsesIt(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	now := time.Date(2026, 7, 3, 13, 0, 0, 0, time.UTC)
	run, err := st.BeginEnrichmentRun(ctx, "fixture-ai", now)
	if err != nil {
		t.Fatalf("BeginEnrichmentRun: %v", err)
	}
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-durable-run", MediaID: "media-alpha", TagKind: "tone", Value: "dry", ProposedBy: "fixture-ai", RunID: run.ID,
	}); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}
	tags, err := st.PendingAITags(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatalf("PendingAITags: %v", err)
	}
	if len(tags) != 1 || tags[0].RunID != run.ID {
		t.Fatalf("pending tag run id = %#v, want %q", tags, run.ID)
	}
	queue, err := st.PendingAITagQueue(ctx, filter, 10)
	if err != nil {
		t.Fatalf("PendingAITagQueue: %v", err)
	}
	if len(queue) != 1 || queue[0].Tag.RunID != run.ID {
		t.Fatalf("queue run id = %#v, want %q", queue, run.ID)
	}
	if err := st.PromotePendingAITag(ctx, "pt-durable-run", filter); err != nil {
		t.Fatalf("PromotePendingAITag: %v", err)
	}
	if _, err := st.RevertEnrichmentRun(ctx, run.ID, filter, now.Add(time.Minute)); err != nil {
		t.Fatalf("RevertEnrichmentRun: %v", err)
	}
	fields, _ := st.MediaFields(ctx, "media-alpha", filter)
	if hasField(fields, "tone") {
		t.Fatalf("durably attributed tag survived run revert: %#v", fields)
	}
}

func TestEnrichmentRunRevertTombstonesAndSecondRunSkips(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	run, err := st.BeginEnrichmentRun(ctx, "fixture-ai", now)
	if err != nil {
		t.Fatalf("BeginEnrichmentRun: %v", err)
	}
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-run-1", MediaID: "media-alpha", TagKind: "mood", Value: "noir", ProposedBy: "fixture-ai", RunID: run.ID,
	}); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}
	if err := st.PromotePendingAITagInRun(ctx, "pt-run-1", run.ID, filter); err != nil {
		t.Fatalf("PromotePendingAITagInRun: %v", err)
	}
	fields, _ := st.MediaFields(ctx, "media-alpha", filter)
	if v, ok := fieldText(fields, "mood"); !ok || v != "noir" {
		t.Fatalf("mood after promote = %q (ok=%v), want noir", v, ok)
	}

	undone, err := st.RevertEnrichmentRun(ctx, run.ID, filter, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RevertEnrichmentRun: %v", err)
	}
	if undone.Kind != opKindAITagRun || undone.Affected != 1 {
		t.Fatalf("revert batch = %#v, want ai-enrichment/1", undone)
	}
	fields, _ = st.MediaFields(ctx, "media-alpha", filter)
	if hasField(fields, "mood") {
		t.Fatalf("mood still present after run revert: %#v", fields)
	}

	second, err := st.BeginEnrichmentRun(ctx, "fixture-ai", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("BeginEnrichmentRun second: %v", err)
	}
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-run-2", MediaID: "media-alpha", TagKind: "mood", Value: "noir", ProposedBy: "fixture-ai", RunID: second.ID,
	}); err != nil {
		t.Fatalf("ProposePendingAITag second: %v", err)
	}
	if err := st.PromotePendingAITagInRun(ctx, "pt-run-2", second.ID, filter); err == nil || !strings.Contains(err.Error(), "pending tag not found") {
		t.Fatalf("second promote err = %v, want tombstoned proposal absent", err)
	}
	fields, _ = st.MediaFields(ctx, "media-alpha", filter)
	if hasField(fields, "mood") {
		t.Fatalf("tombstoned mood reappeared: %#v", fields)
	}
}

func TestPromotePendingAITagDoesNotOverwriteAuthoredField(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	// Hydration/import/parser/user-authored fields outrank enrichment proposals.
	if err := st.UpsertMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "mood", Type: EntityFieldText, Text: "operator"},
	}); err != nil {
		t.Fatalf("UpsertMediaFields: %v", err)
	}
	run, err := st.BeginEnrichmentRun(ctx, "fixture-ai", time.Now().UTC())
	if err != nil {
		t.Fatalf("BeginEnrichmentRun: %v", err)
	}
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-authored", MediaID: "media-alpha", TagKind: "mood", Value: "noir", ProposedBy: "fixture-ai", RunID: run.ID,
	}); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}
	if err := st.PromotePendingAITagInRun(ctx, "pt-authored", run.ID, filter); err == nil || !strings.Contains(err.Error(), "already authored") {
		t.Fatalf("PromotePendingAITag err = %v, want authored-field refusal", err)
	}
	fields, _ := st.MediaFields(ctx, "media-alpha", filter)
	if v, ok := fieldText(fields, "mood"); !ok || v != "operator" {
		t.Fatalf("authored mood = %q (ok=%v), want operator", v, ok)
	}
}

// TestPromotePendingAITagRevealGated confirms an unrevealed caller cannot
// promote a proposal on restricted Media — the value stays staged, never
// canonical, and the call errors without confirming the proposal's existence.
func TestPromotePendingAITagRevealGated(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-restricted", MediaID: "media-archive", TagKind: "mood", Value: "tense", ProposedBy: "ai",
	}); err != nil {
		t.Fatalf("ProposePendingAITag: %v", err)
	}
	if err := st.PromotePendingAITag(ctx, "pt-restricted", LibraryFilter{Revealed: false}); err == nil {
		t.Fatal("unrevealed PromotePendingAITag on restricted media should fail")
	}
	// The restricted Media must not have gained a canonical mood field.
	fields, _ := st.MediaFields(ctx, "media-archive", LibraryFilter{Revealed: true, AllLibraries: true})
	if hasField(fields, "mood") {
		t.Fatal("restricted media gained a canonical mood field from an unrevealed promote")
	}
}

// --- PendingAITagQueue + LibraryIndexStatuses reveal scoping ----------------

// TestPendingAITagQueueRevealScoped confirms the operator review queue excludes
// proposals on restricted Media from an unrevealed caller.
func TestPendingAITagQueueRevealScoped(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	mustPropose := func(id, media string) {
		if err := st.ProposePendingAITag(ctx, PendingAITag{
			ID: id, MediaID: MediaID(media), TagKind: "genre", Value: "x", ProposedBy: "ai",
		}); err != nil {
			t.Fatalf("ProposePendingAITag %s: %v", id, err)
		}
	}
	mustPropose("q-local", "media-alpha")
	mustPropose("q-restricted", "media-archive")

	unrevealed, err := st.PendingAITagQueue(ctx, LibraryFilter{Revealed: false}, 100)
	if err != nil {
		t.Fatalf("PendingAITagQueue unrevealed: %v", err)
	}
	for _, item := range unrevealed {
		if item.Tag.ID == "q-restricted" {
			t.Fatal("queue leaked a restricted-media proposal to an unrevealed caller")
		}
	}
	revealed, err := st.PendingAITagQueue(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, 100)
	if err != nil {
		t.Fatalf("PendingAITagQueue revealed: %v", err)
	}
	if len(revealed) < 2 {
		t.Fatalf("revealed queue = %d items, want both proposals", len(revealed))
	}
}

// TestLibraryIndexStatusesCounts confirms per-Library indexed/total counts and
// the derived state label, and that the opt-out flag overrides to "off".
func TestLibraryIndexStatusesCounts(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const kind = EmbeddingKind("text-v1")

	// Import one Media into library-local and index it; library-local already
	// has the seeded media-alpha (no embedding) so it should read partial.
	_, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/idx.mp4", Title: "Indexed",
		Container: "mp4", Codec: "h264", ContentFingerprint: "sha256:idx",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := st.UpsertEmbedding(ctx, "sha256:idx", kind, uniformVector(4, 1)); err != nil {
		t.Fatalf("UpsertEmbedding: %v", err)
	}
	if err := st.RecordEnrichmentFailure(ctx, "media-alpha", kind, "unmatched labels: 1"); err != nil {
		t.Fatalf("RecordEnrichmentFailure: %v", err)
	}

	statuses, err := st.LibraryIndexStatuses(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, kind)
	if err != nil {
		t.Fatalf("LibraryIndexStatuses: %v", err)
	}
	var local *LibraryIndexStatus
	for i := range statuses {
		if statuses[i].LibraryID == "library-local" {
			local = &statuses[i]
		}
	}
	if local == nil {
		t.Fatal("library-local missing from index statuses")
	}
	if local.Indexed < 1 || local.Total <= local.Indexed {
		t.Fatalf("library-local indexed=%d total=%d, want partial (indexed>=1, total>indexed)", local.Indexed, local.Total)
	}
	if local.State != IndexStatePartial {
		t.Errorf("state = %q, want partial", local.State)
	}
	if local.Failures != 1 {
		t.Errorf("failures = %d, want 1", local.Failures)
	}

	// Opt out: state must flip to off regardless of counts.
	if err := st.SetLibraryAIIndexing(ctx, "library-local", false); err != nil {
		t.Fatalf("SetLibraryAIIndexing: %v", err)
	}
	statuses, _ = st.LibraryIndexStatuses(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, kind)
	for _, s := range statuses {
		if s.LibraryID == "library-local" && s.State != IndexStateOff {
			t.Errorf("after opt-out state = %q, want off", s.State)
		}
	}
}

func hasField(fields []MediaField, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func fieldText(fields []MediaField, name string) (string, bool) {
	for _, f := range fields {
		if f.Name == name {
			return f.Text, true
		}
	}
	return "", false
}
