package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/l10n"
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
		EntityKinds: []plugin.EntityKind{
			{Name: "person", Archetype: "person"},
			{Name: "genre", Archetype: "category"},
		},
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

// stationRelatedFixture adds to the music fixture a genre linked to trackA and to a
// track by another artist (trackC, 1993), plus an unrelated track (trackD) that
// shares nothing with the seed (CORE-2772).
type stationRelatedFixture struct {
	musicFixture
	genreID store.EntityID
	trackC  store.MediaID
	trackD  store.MediaID
}

func seedStationRelatedFixture(t *testing.T, st *store.Store) stationRelatedFixture {
	t.Helper()
	ctx := context.Background()
	fx := seedMusicFixture(t, st)
	importTrack := func(path, title, fp string, year int) store.MediaID {
		m, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: fx.libraryID, Path: path, Title: title, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: fp, DurationSecondsHint: 200, Year: year,
		})
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	trackC := importTrack("/music/c.flac", "Track C", "fp-music-c", 1993)
	trackD := importTrack("/music/d.flac", "Track D", "fp-music-d", 1960)
	other := store.Entity{ID: "entity-artist-2", LibraryID: fx.libraryID, Kind: "person", Title: "Other Artist"}
	genre := store.Entity{ID: "entity-genre-1", LibraryID: fx.libraryID, Kind: "genre", Title: "Shoegaze"}
	for _, e := range []store.Entity{other, genre} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []store.EntityRelation{
		{FromEntityID: other.ID, Name: "appears-on", ToMediaID: trackC},
		{FromEntityID: genre.ID, Name: "classifies", ToMediaID: fx.trackA},
		{FromEntityID: genre.ID, Name: "classifies", ToMediaID: trackC},
	} {
		if err := st.UpsertEntityRelation(ctx, rel); err != nil {
			t.Fatal(err)
		}
	}
	return stationRelatedFixture{musicFixture: fx, genreID: genre.ID, trackC: trackC, trackD: trackD}
}

// TestStationExpandsToRelatedMedia pins similarity from existing data: a track
// seed leads its window and reaches another artist's track through a shared
// category, explained by that category; Media sharing nothing stay out.
func TestStationExpandsToRelatedMedia(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedStationRelatedFixture(t, st)
	got, _, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "media", id: string(fx.trackA)}, stationRules{}, stationTuning{}, 7)
	if err != nil {
		t.Fatal(err)
	}
	ids := candidateIDs(got)
	if len(ids) == 0 || ids[0] != string(fx.trackA) {
		t.Fatalf("seed track does not lead: %v", ids)
	}
	if !candidatesContain(got, string(fx.trackC)) {
		t.Fatalf("shared category did not reach trackC: %v", ids)
	}
	if !candidatesContain(got, string(fx.trackB)) {
		t.Fatalf("shared artist did not reach trackB: %v", ids)
	}
	if candidatesContain(got, string(fx.trackD)) {
		t.Fatalf("unrelated trackD entered the station: %v", ids)
	}
	for _, c := range got {
		if c.MediaID == string(fx.trackC) && c.Explanation != "Also in Shoegaze" {
			t.Fatalf("trackC explanation = %q, want the shared category", c.Explanation)
		}
		if c.MediaID == string(fx.trackA) && c.Explanation != "Where this station started" {
			t.Fatalf("seed explanation = %q", c.Explanation)
		}
	}
}

// TestStationEntitySeedExplainsAndLocalizes pins the generic entity seed kind
// and localized explanations.
func TestStationEntitySeedExplainsAndLocalizes(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedStationRelatedFixture(t, st)
	ctx := l10n.WithLocale(context.Background(), "de")
	got, _, err := h.generateStation(ctx, defaultScope(), stationSeed{kind: "entity", id: string(fx.artistID)}, stationRules{}, stationTuning{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.MediaID == string(fx.trackB) && c.Explanation != "Von The Artist" {
			t.Fatalf("trackB explanation = %q, want the localized seed Entity", c.Explanation)
		}
	}
	if !candidatesContain(got, string(fx.trackC)) {
		t.Fatalf("artist seed did not expand through its genre: %v", candidateIDs(got))
	}
}

// TestStationDecadeAndEntityRules pins the CORE-2772 rule vocabulary: decades
// and anyEntityIDs filter before ranking, and an entity rule finds that
// Entity's Media even when the seed never linked it.
func TestStationDecadeAndEntityRules(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedStationRelatedFixture(t, st)
	ctx := context.Background()
	rules, err := parseStationRules(`{"decades":[1990]}`)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := h.generateStation(ctx, defaultScope(), stationSeed{kind: "entity", id: string(fx.artistID)}, rules, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidatesContain(got, string(fx.trackB)) || !candidatesContain(got, string(fx.trackA)) || !candidatesContain(got, string(fx.trackC)) {
		t.Fatalf("decade rule 1990 = %v, want trackA and trackC only", candidateIDs(got))
	}

	got, _, err = h.generateStation(ctx, defaultScope(), stationSeed{kind: "media", id: string(fx.trackB)}, stationRules{AnyEntityIDs: []string{string(fx.genreID)}}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ids := candidateIDs(got)
	if len(ids) != 2 || !candidatesContain(got, string(fx.trackA)) || !candidatesContain(got, string(fx.trackC)) {
		t.Fatalf("entity rule = %v, want only the genre's trackA and trackC", ids)
	}
	for _, c := range got {
		if strings.Contains(c.Explanation, "Tunes") {
			t.Fatalf("explanation names a Library: %q", c.Explanation)
		}
	}
}

// importStationTracks adds n tracks of the given year to the fixture's music
// Library, each credited to entityID.
func importStationTracks(t *testing.T, st *store.Store, fx musicFixture, entityID store.EntityID, prefix string, n, year int) []store.MediaID {
	t.Helper()
	ctx := context.Background()
	ids := make([]store.MediaID, 0, n)
	for i := range n {
		name := fmt.Sprintf("%s-%04d", prefix, i)
		m, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: fx.libraryID, Path: "/music/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name, DurationSecondsHint: 200, Year: year,
		})
		if err != nil {
			t.Fatal(err)
		}
		if entityID != "" {
			if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: entityID, Name: "appears-on", ToMediaID: m.ID}); err != nil {
				t.Fatal(err)
			}
		}
		ids = append(ids, m.ID)
	}
	return ids
}

// Review finding P1: a Station never picks an item the Profile's
// certification ceiling locks, but does pick one the Profile was granted.
func TestStationHonoursLockedAndGranted(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedStationRelatedFixture(t, st)
	ctx := context.Background()
	for id, rating := range map[store.MediaID]string{fx.trackA: "low", fx.trackB: "high", fx.trackC: "high"} {
		if err := st.SetMediaFields(ctx, id, []store.MediaField{{Name: "certification", Type: store.EntityFieldText, Text: rating}}); err != nil {
			t.Fatal(err)
		}
	}
	scope := defaultScope()
	scope.Certification = []store.CertificationConstraint{{PluginID: stationPluginID, Allowed: []string{"low"}, Locked: true}}
	scope.GrantedMediaIDs = []store.MediaID{fx.trackC}
	got, _, err := h.generateStation(ctx, scope, stationSeed{kind: "media", id: string(fx.trackA)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidatesContain(got, string(fx.trackB)) {
		t.Fatalf("locked trackB entered the station: %v", candidateIDs(got))
	}
	if !candidatesContain(got, string(fx.trackA)) || !candidatesContain(got, string(fx.trackC)) {
		t.Fatalf("station = %v, want the allowed trackA and the granted trackC", candidateIDs(got))
	}
}

// Review finding P1: expansion honours the Profile's Mutes, as the related-media
// read does.
func TestStationExpansionLeavesOutMutedMedia(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedStationRelatedFixture(t, st)
	ctx := context.Background()
	disliked := store.Entity{ID: "entity-disliked", LibraryID: fx.libraryID, Kind: "person", Title: "Disliked"}
	if err := st.UpsertEntity(ctx, disliked); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: disliked.ID, Name: "appears-on", ToMediaID: fx.trackC}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, "profile-default", store.MuteKindEntity, string(disliked.ID), false, true); err != nil {
		t.Fatal(err)
	}
	got, _, err := h.generateStation(ctx, defaultScope(), stationSeed{kind: "media", id: string(fx.trackA)}, stationRules{}, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if candidatesContain(got, string(fx.trackC)) {
		t.Fatalf("muted trackC entered the station: %v", candidateIDs(got))
	}
	if !candidatesContain(got, string(fx.trackB)) {
		t.Fatalf("station = %v, want trackB still reached", candidateIDs(got))
	}
}

// Review finding P2: a decade rule filters an Entity's Media before sampling, so
// the one matching track among hundreds is always found.
func TestStationDecadeFiltersBeforeSampling(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	importStationTracks(t, st, fx, fx.artistID, "later", 250, 2005)
	match := importStationTracks(t, st, fx, fx.artistID, "eighties", 1, 1984)[0]
	rules, err := parseStationRules(`{"decades":[1980]}`)
	if err != nil {
		t.Fatal(err)
	}
	for seed := uint64(1); seed <= 5; seed++ {
		got, _, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "media", id: string(fx.trackA)}, rules, stationTuning{}, seed)
		if err != nil {
			t.Fatal(err)
		}
		if ids := candidateIDs(got); len(ids) != 1 || ids[0] != string(match) {
			t.Fatalf("rankSeed %d: decade 1980 = %v, want only %s", seed, ids, match)
		}
	}
}

// Review finding P2: with excludeQueued, each top-up leaves out what is already
// queued before sampling, so repeated generations reach past the first window.
func TestStationExcludeQueuedReachesPastFirstWindow(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	importStationTracks(t, st, fx, fx.artistID, "deep", 160, 2001)
	ctx := context.Background()
	filter, err := h.sealedFreeStationFilter(ctx, defaultScope())
	if err != nil {
		t.Fatal(err)
	}
	noCertification := func([]store.ProfileCertificationCeiling) []store.CertificationConstraint { return nil }
	rules := stationRules{ExcludeQueued: true}
	seen := map[string]bool{}
	for round := range 3 {
		got, _, err := h.generateStation(ctx, defaultScope(), stationSeed{kind: "entity", id: string(fx.artistID)}, rules, stationTuning{}, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("round %d: empty station with %d queued", round, len(seen))
		}
		for _, c := range got {
			if seen[c.MediaID] {
				t.Fatalf("round %d: %s was already queued", round, c.MediaID)
			}
			seen[c.MediaID] = true
			if _, err := st.QueueAddForProfile(ctx, "profile-default", "session-default", "", store.MediaID(c.MediaID), nil, filter, time.Now, noCertification); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(seen) <= stationPerEntityCap {
		t.Fatalf("three top-ups reached %d tracks, want past the %d-track sample", len(seen), stationPerEntityCap)
	}
}

// Review rounds 2 and 3, P2: the Entity read carries each Media's length, so
// length rules filter related Media while reading without dropping a match.
func TestStationLengthRulesKeepExpandedMedia(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	want := importStationTracks(t, st, fx, fx.artistID, "long", 3, 2001)
	rules, err := parseStationRules(`{"minDurationSeconds":100}`)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := h.generateStation(context.Background(), defaultScope(), stationSeed{kind: "media", id: string(fx.trackA)}, rules, stationTuning{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, id := range candidateIDs(got) {
		ids[id] = true
	}
	for _, id := range want {
		if !ids[string(id)] {
			t.Fatalf("minDurationSeconds 100 dropped 200s track %s: %v", id, candidateIDs(got))
		}
	}
}

// Review round 2, P2: the read cap counts matching Media, so an Entity with
// more than the cap of non-matching Media still yields its one match.
func TestStationEntityReadFiltersBeforeTheCap(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	importStationTracks(t, st, fx, fx.artistID, "a-later", stationEntityReadCap+50, 2005)
	match := importStationTracks(t, st, fx, fx.artistID, "z-eighties", 1, 1984)[0]
	ctx := context.Background()
	filter, err := h.sealedFreeStationFilter(ctx, defaultScope())
	if err != nil {
		t.Fatal(err)
	}
	items, err := h.stationEntityMedia(ctx, defaultScope(), filter, fx.artistID, func(it store.SourceItem) bool { return it.Year == 1984 })
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != match {
		t.Fatalf("filtered read = %d items, want only %s", len(items), match)
	}
}

// Review round 2, P2: a large Collection seed samples from its unqueued
// members, so a top-up reaches every member not yet queued.
func TestStationCollectionSeedSamplesUnqueued(t *testing.T) {
	// feature:music-stations
	st := storetest.Open(t)
	h := newStationTestHandler(t, st)
	fx := seedMusicFixture(t, st)
	ctx := context.Background()
	members := importStationTracks(t, st, fx, "", "member", stationSeedMediaCap+10, 2001)
	revealed := auth.Scope{ProfileID: "profile-default", AllLibraries: true, Revealed: true}.LibraryFilter()
	coll, err := st.CreateCollection(ctx, store.Collection{LibraryID: fx.libraryID, Name: "Big Coll"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCollectionMedia(ctx, coll.ID, members, revealed, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	filter, err := h.sealedFreeStationFilter(ctx, defaultScope())
	if err != nil {
		t.Fatal(err)
	}
	noCertification := func([]store.ProfileCertificationCeiling) []store.CertificationConstraint { return nil }
	for _, id := range members[:stationSeedMediaCap] {
		if _, err := st.QueueAddForProfile(ctx, "profile-default", "session-default", "", id, nil, filter, time.Now, noCertification); err != nil {
			t.Fatal(err)
		}
	}
	for seed := uint64(1); seed <= 5; seed++ {
		got, _, err := h.generateStation(ctx, defaultScope(), stationSeed{kind: "collection", id: string(coll.ID)}, stationRules{ExcludeQueued: true}, stationTuning{}, seed)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, id := range candidateIDs(got) {
			ids[id] = true
		}
		for _, id := range members[stationSeedMediaCap:] {
			if !ids[string(id)] {
				t.Fatalf("rankSeed %d: unqueued member %s missing from %v", seed, id, candidateIDs(got))
			}
		}
	}
}
