package graph

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// stubLibEncrypt is a minimal LibEncryptManager that reports a fixed set of
// Sealed Library ids, so the Station generator's sealed-subtraction path can be
// exercised without a real key model.
type stubLibEncrypt struct{ sealed map[string]bool }

func (s stubLibEncrypt) Provision(string, libencrypt.UnsealMode, string) (libencrypt.RecoveryKey, error) {
	return libencrypt.RecoveryKey{}, nil
}
func (s stubLibEncrypt) PassphraseUnseal(string, string) error { return nil }
func (s stubLibEncrypt) RecoveryUnseal(string, string) error   { return nil }
func (s stubLibEncrypt) Seal(string)                           {}
func (s stubLibEncrypt) Sealed(id string) bool                 { return s.sealed[id] }
func (s stubLibEncrypt) Provisioned(string) bool               { return true }
func (s stubLibEncrypt) Mode(string) (libencrypt.UnsealMode, error) {
	return libencrypt.UnsealModeAuto, nil
}
func (s stubLibEncrypt) Rewrap(string, libencrypt.UnsealMode, string, func(libencrypt.RewrapCipher) error) (libencrypt.RecoveryKey, error) {
	return libencrypt.RecoveryKey{}, nil
}

const stationPluginID = "starlog"

func newStationTestHandler(t *testing.T, st *store.Store) *Handler {
	t.Helper()
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:                 stationPluginID,
		Version:            "1.0.0",
		DisplayName:        plugin.L("Starlog"),
		MediaLabel:         plugin.L("Track"),
		MediaClasses:       []string{"audio"},
		FeatureEligibility: []string{plugin.FeatureAudioStation},
		Layouts:            []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil),
		auth.NewService(st),
		st,
		registry,
	)
}

// musicFixture seeds a music-plugin Library granted to profile-default with two
// tracks, an Artist Entity related to both, and a Collection holding one track
// plus one NON-music track (to prove the non-music constraint). It returns the
// music Library id, the two music Media ids, the Artist entity id, and the
// mixed Collection id.
type musicFixture struct {
	libraryID    store.LibraryID
	trackA       store.MediaID
	trackB       store.MediaID
	artistID     store.EntityID
	musicColl    store.CollectionID // collection in the music Library (holds trackA)
	nonMusicColl store.CollectionID // collection in non-music library-local (holds media-alpha)
}

func seedMusicFixture(t *testing.T, st *store.Store) musicFixture {
	t.Helper()
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Tunes", false, stationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	importTrack := func(path, title, fp string, year int) store.MediaID {
		m, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: path, Title: title, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: fp, DurationSecondsHint: 200, Year: year,
		})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	trackA := importTrack("/music/a.flac", "Track A", "fp-music-a", 1990)
	trackB := importTrack("/music/b.flac", "Track B", "fp-music-b", 2020)

	artist := store.Entity{ID: "entity-artist-1", LibraryID: lib.ID, Kind: "person", Title: "The Artist"}
	if err := st.UpsertEntity(ctx, artist); err != nil {
		t.Fatal(err)
	}
	for _, mid := range []store.MediaID{trackA, trackB} {
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: artist.ID, Name: "appears-on", ToMediaID: mid,
		}); err != nil {
			t.Fatal(err)
		}
	}

	revealed := auth.Scope{ProfileID: "profile-default", AllLibraries: true, Revealed: true}.LibraryFilter()

	// A music-Library Collection holding a music track — a valid music seed.
	musicColl, err := st.CreateCollection(ctx, store.Collection{LibraryID: lib.ID, Name: "Music Coll"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCollectionMedia(ctx, musicColl.ID, []store.MediaID{trackA}, revealed, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// A NON-music (library-local) Collection holding the seeded non-music
	// media-alpha. Used to prove a non-music Collection seed admits no music
	// candidates — the sealed-free MUSIC filter excludes every non-music-Library
	// member (ADR-0098: Stations never cross non-music Libraries).
	nonMusicColl, err := st.CreateCollection(ctx, store.Collection{LibraryID: "library-local", Name: "Non-Music Coll"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCollectionMedia(ctx, nonMusicColl.ID, []store.MediaID{"media-alpha"}, revealed, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return musicFixture{libraryID: lib.ID, trackA: trackA, trackB: trackB, artistID: artist.ID, musicColl: musicColl.ID, nonMusicColl: nonMusicColl.ID}
}

func defaultScope() auth.Scope {
	return auth.Scope{ProfileID: "profile-default", AllLibraries: true, Revealed: true}
}

// TestStationGeneratorSeedTypes pins that each seed kind collects the right
// music candidates (ADR-0098 §Stations: Track/Artist/Album/Genre/Collection).
func TestStationGeneratorSeedTypes(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	ctx := context.Background()
	scope := defaultScope()

	// Track seed: includes the seed track itself.
	got, _, err := h.generateStation(ctx, scope, stationSeed{kind: "track", id: string(fx.trackA)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !candidatesContain(got, string(fx.trackA)) {
		t.Fatalf("track seed missing seed track: %v", candidateIDs(got))
	}

	// Artist seed: both related tracks.
	got, _, err = h.generateStation(ctx, scope, stationSeed{kind: "artist", id: string(fx.artistID)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !candidatesContain(got, string(fx.trackA)) || !candidatesContain(got, string(fx.trackB)) {
		t.Fatalf("artist seed missing related tracks: %v", candidateIDs(got))
	}
}

// TestStationCollectionSeedExcludesNonMusic pins the ADR-0098 non-music
// constraint (the MED plan-review finding): a mixed Collection seed never admits
// non-music Media into candidates.
func TestStationCollectionSeedExcludesNonMusic(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	// Non-music Collection seed: yields no music candidates (media-alpha excluded).
	got, _, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "collection", id: string(fx.nonMusicColl)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidatesContain(got, "media-alpha") {
		t.Fatalf("non-music collection seed admitted non-music media-alpha: %v", candidateIDs(got))
	}
	// Music Collection seed: yields the music track.
	music, _, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "collection", id: string(fx.musicColl)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !candidatesContain(music, string(fx.trackA)) {
		t.Fatalf("music collection seed dropped the music track: %v", candidateIDs(music))
	}
}

// TestStationSealedSuppression pins that a Sealed music Library's Media never
// enters candidates or explanations (ADR-0098: subtract Sealed before
// collection). With the only music Library Sealed, generation yields an empty
// window and a generic scarcity note that names no Library.
func TestStationSealedSuppression(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	h.SetLibEncrypt(stubLibEncrypt{sealed: map[string]bool{string(fx.libraryID): true}})

	got, explanation, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "artist", id: string(fx.artistID)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("sealed library leaked candidates: %v", candidateIDs(got))
	}
	if explanation == "" {
		t.Fatal("expected a scarcity explanation when no candidates available")
	}
	for _, name := range []string{string(fx.libraryID), "Tunes"} {
		if containsSubstr(explanation, name) {
			t.Fatalf("explanation leaked sealed library identity %q: %q", name, explanation)
		}
	}
}

// TestStationDebugReproducibility pins debug reproducibility: the same rank seed
// yields the same order; a different seed may reorder.
func TestStationDebugReproducibility(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	ctx := context.Background()
	scope := defaultScope()

	a, _, err := h.generateStation(ctx, scope, stationSeed{kind: "artist", id: string(fx.artistID)}, stationRules{}, stationTuning{}, 42)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := h.generateStation(ctx, scope, stationSeed{kind: "artist", id: string(fx.artistID)}, stationRules{}, stationTuning{}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if !sameOrder(a, b) {
		t.Fatalf("same seed produced different order: %v vs %v", candidateIDs(a), candidateIDs(b))
	}
}

// TestStationMutationsWireUp exercises the three Station MUTATIONS over the HTTP
// handler (this is also the action-conformance behavior test for
// stationStart/stationSave/stationDelete). It asserts: stationStart materializes
// a window into the queue; replacing a non-empty visible queue needs
// confirmReplace; stationSave persists inputs; stationDelete removes them.
func TestStationMutationsWireUp(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)

	// stationStart on an empty queue: materializes the artist-seed window.
	var started []SourceItem
	execGraph(t, h, authorizeViewer,
		`mutation Start($seedKind: String!, $seedID: String!, $seed: String!) { stationStart(seedKind: $seedKind, seedID: $seedID, seed: $seed) { id } }`,
		map[string]any{"seedKind": "artist", "seedID": string(fx.artistID), "seed": "pin"},
		"stationStart", &started)
	if len(started) == 0 {
		t.Fatal("stationStart materialized an empty queue")
	}

	// Now the visible queue is non-empty: a second start without confirmReplace is
	// refused with the needs-confirmation client error.
	resp := postGraphJSON(t, h, authorizeViewer,
		`mutation Start($seedKind: String!, $seedID: String!) { stationStart(seedKind: $seedKind, seedID: $seedID) { id } }`,
		map[string]any{"seedKind": "track", "seedID": string(fx.trackA)})
	if len(resp.Errors) == 0 {
		t.Fatal("expected needs-confirmation error on second start without confirmReplace")
	}

	// confirmReplace=true commits the replacement.
	var replaced []SourceItem
	execGraph(t, h, authorizeViewer,
		`mutation Start($seedKind: String!, $seedID: String!, $confirmReplace: Boolean!) { stationStart(seedKind: $seedKind, seedID: $seedID, confirmReplace: $confirmReplace) { id } }`,
		map[string]any{"seedKind": "track", "seedID": string(fx.trackA), "confirmReplace": true},
		"stationStart", &replaced)
	if !sourceItemsContain(replaced, string(fx.trackA)) {
		t.Fatalf("confirmed replace missing seed track: %v", sourceItemIDs(replaced))
	}

	// stationSave persists inputs (never an order).
	var saved SavedStationInfo
	execGraph(t, h, authorizeViewer,
		`mutation Save($libraryID: String!, $name: String!, $seedJSON: String!) { stationSave(libraryID: $libraryID, name: $name, seedJSON: $seedJSON) { id name seedJSON } }`,
		map[string]any{"libraryID": string(fx.libraryID), "name": "My Station", "seedJSON": `{"kind":"artist"}`},
		"stationSave", &saved)
	if saved.ID == "" || saved.Name != "My Station" || saved.SeedJSON != `{"kind":"artist"}` {
		t.Fatalf("stationSave round-trip = %+v", saved)
	}

	// stationDelete removes it.
	var deleted SavedStationInfo
	execGraph(t, h, authorizeViewer,
		`mutation Del($id: String!) { stationDelete(id: $id) { id } }`,
		map[string]any{"id": saved.ID}, "stationDelete", &deleted)
	if deleted.ID != saved.ID {
		t.Fatalf("stationDelete returned %q, want %q", deleted.ID, saved.ID)
	}
}

// TestStationHardFilterBeforeRankingAndTuningRankingOnly pins the ADR-0098
// invariant that hard Source filters apply BEFORE ranking and that tuning hints
// are ranking-only — they reorder the post-filter set but can never admit an
// item the hard filter rejected. trackA=1990, trackB=2020 in the fixture.
func TestStationHardFilterBeforeRankingAndTuningRankingOnly(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	ctx := context.Background()
	scope := defaultScope()

	// Hard filter minYear=2000 drops trackA (1990); only trackB survives.
	rules := stationRules{MinYear: 2000}
	got, _, err := h.generateStation(ctx, scope, stationSeed{kind: "artist", id: string(fx.artistID)}, rules, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidatesContain(got, string(fx.trackA)) {
		t.Fatalf("hard filter let through filtered trackA (1990): %v", candidateIDs(got))
	}
	if !candidatesContain(got, string(fx.trackB)) {
		t.Fatalf("hard filter dropped passing trackB (2020): %v", candidateIDs(got))
	}

	// Tuning preferRecent CANNOT readmit the filtered trackA: even with the
	// strongest recency bias, the hard filter has already removed it.
	got, _, err = h.generateStation(ctx, scope, stationSeed{kind: "artist", id: string(fx.artistID)}, rules, stationTuning{PreferRecent: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidatesContain(got, string(fx.trackA)) {
		t.Fatalf("tuning admitted a hard-filtered item: %v", candidateIDs(got))
	}

	// With no hard filter, tuning preferRecent biases trackB (2020) ahead of
	// trackA (1990) — proving tuning executes (reorders), not a no-op.
	ordered, _, err := h.generateStation(ctx, scope, stationSeed{kind: "artist", id: string(fx.artistID)}, stationRules{}, stationTuning{PreferRecent: true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ids := candidateIDs(ordered)
	if len(ids) != 2 || ids[0] != string(fx.trackB) {
		t.Fatalf("preferRecent did not rank trackB (2020) first: %v", ids)
	}
}

func candidateIDs(cs []StationCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.MediaID
	}
	return out
}

func candidatesContain(cs []StationCandidate, id string) bool {
	for _, c := range cs {
		if c.MediaID == id {
			return true
		}
	}
	return false
}

func sameOrder(a, b []StationCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].MediaID != b[i].MediaID {
			return false
		}
	}
	return true
}

func sourceItemsContain(items []SourceItem, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func sourceItemIDs(items []SourceItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func containsSubstr(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
