package store

// Tests for the ADR-0053 `recommend` retrieval mode (RecommendFromEngagement):
// a content-based, engagement-seeded read over the ADR-0041 vector store. The
// store layer owns the seed→neighbour read, the exclusion of seeds + finished
// items, and the reveal-filtered candidate scan; the per-Profile opt-in gate is
// enforced one layer up in the graph resolver (graph_enrichment_test.go).
//
// Each test seeds embeddings manually (no Enrichment Plugin in tests) and uses
// ImportMediaFile to plant Media with controlled vectors so neighbour ordering
// is deterministic.

import (
	"context"
	"testing"
)

const recommendKind = EmbeddingKind("text-v1")

// importWithVector imports one Media into the given library with a unique
// fingerprint and seeds its embedding vector of recommendKind. Returns the
// MediaID.
func importWithVector(t *testing.T, st *Store, libraryID LibraryID, title, fp string, vec []float32) MediaID {
	t.Helper()
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          libraryID,
		Path:               "/m/" + fp + ".mp4",
		Title:              title,
		Container:          "mp4",
		Codec:              "h264",
		Class:              MediaClassVideo,
		ContentFingerprint: fp,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile %s: %v", title, err)
	}
	if err := st.UpsertEmbedding(ctx, fp, recommendKind, vec); err != nil {
		t.Fatalf("UpsertEmbedding %s: %v", title, err)
	}
	return media.ID
}

// playOnce records a single (unfinished) play for the Profile, so the item
// becomes a recently-played / most-played engagement seed without being marked
// finished.
func playOnce(t *testing.T, st *Store, profileID ProfileID, mediaID MediaID) {
	t.Helper()
	if err := st.UpsertPlaybackProgress(context.Background(), PlaybackProgress{
		ProfileID:       profileID,
		MediaID:         mediaID,
		PositionSeconds: 30,
		DurationSeconds: 600,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatalf("UpsertPlaybackProgress: %v", err)
	}
}

// (a) recommend returns nearest content-neighbours of the engagement seeds,
// excluding the seeds themselves and finished items.
func TestRecommendReturnsNeighborsExcludingSeedsAndFinished(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const profile = ProfileID("profile-default")
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	// Seed media the Profile has engaged (played) — the recommendation seed.
	seed := importWithVector(t, st, "library-local", "Seed", "sha256:rec-seed", []float32{1, 0, 0, 0})
	playOnce(t, st, profile, seed)

	// A near neighbour (high cosine to the seed) — should be recommended.
	near := importWithVector(t, st, "library-local", "Near", "sha256:rec-near", []float32{0.95, 0.05, 0, 0})
	// A far item (orthogonal to the seed) — should rank below near.
	far := importWithVector(t, st, "library-local", "Far", "sha256:rec-far", []float32{0, 0, 0, 1})
	// A finished item that is ALSO a near neighbour — must be excluded from the
	// result even though it ranks high, because the Profile already finished it.
	finished := importWithVector(t, st, "library-local", "Finished", "sha256:rec-finished", []float32{0.96, 0.04, 0, 0})
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: profile, MediaID: finished, PositionSeconds: 600, DurationSeconds: 600,
	}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatalf("finish play: %v", err)
	}

	results, err := st.RecommendFromEngagement(ctx, profile, filter, recommendKind, 10)
	if err != nil {
		t.Fatalf("RecommendFromEngagement: %v", err)
	}

	ids := map[MediaID]float32{}
	for _, r := range results {
		ids[r.MediaID] = r.Score
	}
	if _, ok := ids[seed]; ok {
		t.Error("recommend returned the seed item itself")
	}
	if _, ok := ids[finished]; ok {
		t.Error("recommend returned an already-finished item")
	}
	if _, ok := ids[near]; !ok {
		t.Fatalf("recommend omitted the near neighbour; got %v", results)
	}
	// near must outrank far (higher cosine to the seed centroid).
	if nearScore, farScore := ids[near], ids[far]; farScore != 0 && nearScore <= farScore {
		t.Errorf("near score %v did not outrank far score %v", nearScore, farScore)
	}
}

// (b) restricted neighbours never surface unrevealed (reveal contract).
func TestRecommendRevealContract(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const profile = ProfileID("profile-default")

	seed := importWithVector(t, st, "library-local", "Seed", "sha256:rec-rev-seed", []float32{1, 0, 0, 0})
	playOnce(t, st, profile, seed)
	// A near neighbour that lives in the RESTRICTED library.
	restrictedNeighbor := importWithVector(t, st, "library-archive", "RestrictedNear", "sha256:rec-rev-restricted", []float32{0.97, 0.03, 0, 0})

	// Unrevealed caller: the restricted neighbour must never appear.
	unrevealed, err := st.RecommendFromEngagement(ctx, profile, LibraryFilter{Revealed: false}, recommendKind, 10)
	if err != nil {
		t.Fatalf("RecommendFromEngagement unrevealed: %v", err)
	}
	for _, r := range unrevealed {
		if r.MediaID == restrictedNeighbor {
			t.Fatalf("recommend leaked restricted neighbour to unrevealed caller (score=%v)", r.Score)
		}
	}

	// Revealed caller: the restricted neighbour is now eligible to surface.
	revealed, err := st.RecommendFromEngagement(ctx, profile, LibraryFilter{Revealed: true, AllLibraries: true}, recommendKind, 10)
	if err != nil {
		t.Fatalf("RecommendFromEngagement revealed: %v", err)
	}
	var saw bool
	for _, r := range revealed {
		if r.MediaID == restrictedNeighbor {
			saw = true
		}
	}
	if !saw {
		t.Errorf("revealed caller did not see the restricted neighbour; got %v", revealed)
	}
}

// (d.1) empty/graceful when the Profile has no engagement at all.
func TestRecommendEmptyWhenNoEngagement(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// Embeddings exist, but this Profile has engaged nothing.
	importWithVector(t, st, "library-local", "Lonely", "sha256:rec-lonely", []float32{1, 0, 0, 0})

	results, err := st.RecommendFromEngagement(ctx, "profile-default", LibraryFilter{Revealed: true, AllLibraries: true}, recommendKind, 10)
	if err != nil {
		t.Fatalf("RecommendFromEngagement: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty result with no engagement, got %v", results)
	}
}

// (d.2) empty/graceful when the Profile HAS engagement but no embeddings of the
// requested kind exist yet (the pre-ENR-1 state).
func TestRecommendEmptyWhenNoEmbeddings(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const profile = ProfileID("profile-default")

	// Import a Media and engage it, but seed NO embedding for it.
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/m/noembed.mp4", Title: "NoEmbed",
		Container: "mp4", Codec: "h264", Class: MediaClassVideo,
		ContentFingerprint: "sha256:rec-noembed",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	playOnce(t, st, profile, media.ID)

	results, err := st.RecommendFromEngagement(ctx, profile, LibraryFilter{Revealed: true, AllLibraries: true}, recommendKind, 10)
	if err != nil {
		t.Fatalf("RecommendFromEngagement: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty result with no embeddings, got %v", results)
	}
}

// Favorites are the strongest seed signal: a favorited item
// seeds the recommendation even with no playback, and its neighbour surfaces.
func TestRecommendFavoriteSeed(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const profile = ProfileID("profile-default")
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	seed := importWithVector(t, st, "library-local", "FavSeed", "sha256:rec-fav-seed", []float32{0, 1, 0, 0})
	near := importWithVector(t, st, "library-local", "FavNear", "sha256:rec-fav-near", []float32{0.05, 0.95, 0, 0})
	if err := st.SetMediaFavorite(ctx, profile, seed, true, filter); err != nil {
		t.Fatalf("SetMediaFavorite: %v", err)
	}

	results, err := st.RecommendFromEngagement(ctx, profile, filter, recommendKind, 10)
	if err != nil {
		t.Fatalf("RecommendFromEngagement: %v", err)
	}
	var sawNear, sawSeed bool
	for _, r := range results {
		switch r.MediaID {
		case near:
			sawNear = true
		case seed:
			sawSeed = true
		}
	}
	if sawSeed {
		t.Error("recommend returned the favorited seed itself")
	}
	if !sawNear {
		t.Fatalf("favorite-seeded recommend omitted the neighbour; got %v", results)
	}
}

// Validation: missing profile / kind are client-side ValidationErrors.
func TestRecommendValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if _, err := st.RecommendFromEngagement(ctx, "", LibraryFilter{Revealed: true, AllLibraries: true}, recommendKind, 10); err == nil {
		t.Error("accepted empty profile ID")
	}
	if _, err := st.RecommendFromEngagement(ctx, "profile-default", LibraryFilter{Revealed: true, AllLibraries: true}, "", 10); err == nil {
		t.Error("accepted empty kind")
	}
}
