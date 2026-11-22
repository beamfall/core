package store

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type similarMediaFixture struct {
	Kind  EmbeddingKind `json:"kind"`
	Media []struct {
		ID      MediaID   `json:"id"`
		Library LibraryID `json:"library"`
		Vector  []float32 `json:"vector"`
	} `json:"media"`
	Expect []struct {
		Seeds    []MediaID `json:"seeds"`
		Revealed bool      `json:"revealed"`
		Order    []MediaID `json:"order"`
	} `json:"expect"`
}

// loadSimilarMediaFixture plants testdata/similar_media_fixture.json into the
// conformance seed: one Media and file per entry, and an embedding for each entry
// that has a vector.
func loadSimilarMediaFixture(t *testing.T) (*Store, similarMediaFixture) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "similar_media_fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx similarMediaFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	st := openConformanceSeed(t)
	ctx := context.Background()
	for _, m := range fx.Media {
		fp := "sha256:fixture-" + string(m.ID)
		if _, err := st.catalogW.ExecContext(ctx,
			`INSERT INTO media (id, library_id, title, duration_seconds, container, codec)
			 VALUES (?, ?, ?, 240, 'flac', 'flac')`, m.ID, m.Library, string(m.ID),
		); err != nil {
			t.Fatalf("insert media %s: %v", m.ID, err)
		}
		if _, err := st.catalogW.ExecContext(ctx,
			`INSERT INTO media_file (id, media_id, path, content_fingerprint) VALUES (?, ?, ?, ?)`,
			"file-"+string(m.ID), m.ID, "/fixture/"+string(m.ID)+".flac", fp,
		); err != nil {
			t.Fatalf("insert media_file %s: %v", m.ID, err)
		}
		if m.Vector != nil {
			if err := st.UpsertEmbedding(ctx, fp, fx.Kind, m.Vector); err != nil {
				t.Fatalf("UpsertEmbedding %s: %v", m.ID, err)
			}
		}
	}
	return st, fx
}

func TestSimilarMediaFixtureOrder(t *testing.T) {
	st, fx := loadSimilarMediaFixture(t)
	ctx := context.Background()
	fixtureIDs := map[MediaID]bool{}
	for _, m := range fx.Media {
		fixtureIDs[m.ID] = true
	}
	for _, want := range fx.Expect {
		filter := LibraryFilter{AllLibraries: true}
		if want.Revealed {
			filter = LibraryFilter{Revealed: true, AllLibraries: true}
		}
		results, err := st.SimilarMedia(ctx, filter, fx.Kind, want.Seeds, 0)
		if err != nil {
			t.Fatalf("seeds %v: %v", want.Seeds, err)
		}
		got := []MediaID{}
		for _, r := range results {
			if !fixtureIDs[r.MediaID] {
				t.Fatalf("seeds %v: listed %s, which has no fixture embedding", want.Seeds, r.MediaID)
			}
			got = append(got, r.MediaID)
		}
		if !reflect.DeepEqual(got, want.Order) {
			t.Errorf("seeds %v revealed=%v: got %v, want %v", want.Seeds, want.Revealed, got, want.Order)
		}
	}
}

func TestSimilarMediaKeepsExactOpposite(t *testing.T) {
	st, fx := loadSimilarMediaFixture(t)
	results, err := st.SimilarMedia(context.Background(), LibraryFilter{AllLibraries: true}, fx.Kind, []MediaID{"sonic-fast-bright"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	last := results[len(results)-1]
	if last.MediaID != "sonic-slow-dark" || last.Score > -0.999 {
		t.Fatalf("last result = %+v, want sonic-slow-dark scoring about -1", last)
	}
}

func TestSimilarMediaLimit(t *testing.T) {
	st, fx := loadSimilarMediaFixture(t)
	results, err := st.SimilarMedia(context.Background(), LibraryFilter{AllLibraries: true}, fx.Kind, []MediaID{"sonic-fast-bright"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MediaID != "sonic-fast-bright-2" {
		t.Fatalf("limit 1: got %+v", results)
	}
}

func TestSimilarMediaValidation(t *testing.T) {
	st := openConformanceSeed(t)
	ctx := context.Background()
	if _, err := st.SimilarMedia(ctx, LibraryFilter{}, "", []MediaID{"media-alpha"}, 10); err == nil {
		t.Fatal("accepted empty kind")
	}
	if _, err := st.SimilarMedia(ctx, LibraryFilter{}, "k", nil, 10); err == nil {
		t.Fatal("accepted no seeds")
	}
	tooMany := make([]MediaID, MaxSimilarMediaSeeds+1)
	for i := range tooMany {
		tooMany[i] = MediaID("m")
	}
	if _, err := st.SimilarMedia(ctx, LibraryFilter{}, "k", tooMany, 10); err == nil {
		t.Fatal("accepted too many seeds")
	}
	results, err := st.SimilarMedia(ctx, LibraryFilter{AllLibraries: true}, "k", []MediaID{"media-alpha"}, 10)
	if err != nil || len(results) != 0 {
		t.Fatalf("seed without embedding: got %v, %v; want empty", results, err)
	}
}

func TestSimilarMediaCentroidSkipsMismatchedAndCancelling(t *testing.T) {
	vectors := map[MediaID][]float32{
		"a": {1, 0},
		"b": {0, 1, 0}, // differs from the first usable seed's width
		"c": {-1, 0},   // cancels a
	}
	query, seeds := similarMediaCentroid([]MediaID{"missing", "a", "b"}, vectors)
	if !reflect.DeepEqual(query, []float32{1, 0}) {
		t.Fatalf("query = %v, want [1 0]", query)
	}
	if !seeds["missing"] || !seeds["a"] || !seeds["b"] {
		t.Fatalf("seed set %v must name every seed", seeds)
	}
	// An unusable first seed must not fix the width (Codex review).
	vectors["zero"] = []float32{0, 0, 0}
	vectors["nan"] = []float32{float32(math.NaN()), 0, 0}
	if query, _ := similarMediaCentroid([]MediaID{"zero", "nan", "a"}, vectors); !reflect.DeepEqual(query, []float32{1, 0}) {
		t.Fatalf("unusable leading seeds: query = %v, want [1 0]", query)
	}
	if query, _ := similarMediaCentroid([]MediaID{"a", "c"}, vectors); query != nil {
		t.Fatalf("cancelling seeds: query = %v, want nil", query)
	}
}

func TestSimilarMediaCandidateLimit(t *testing.T) {
	for _, tc := range []struct{ dims, want int }{
		{0, maxSemanticSearchCandidates},
		{32, 125_000},
		{4_000, maxSemanticSearchCandidates},
		{8_192, 488},
		{1_000_000, 4},
		{8_000_000, 1},
	} {
		if got := similarMediaCandidateLimit(tc.dims); got != tc.want {
			t.Errorf("dims %d: got %d, want %d", tc.dims, got, tc.want)
		}
	}
}

func TestSortCandidatesStableDescending(t *testing.T) {
	got := []embeddingCandidate{{"a", 0.1}, {"b", 0.9}, {"c", 0.1}, {"d", -0.5}, {"e", 0.9}}
	sortCandidates(got)
	want := []embeddingCandidate{{"b", 0.9}, {"e", 0.9}, {"a", 0.1}, {"c", 0.1}, {"d", -0.5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
