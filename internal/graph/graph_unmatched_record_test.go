package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// METAPARITY-6/7 (ADR-0068 §A) graph coverage: the Media-independent record
// inbox, the hand-authored create, and the three scoped suppressions.

// queueTestRecord parks one scan-origin record in the visible Library so the
// resolvers have a real, visibility-filtered record to act on.
func queueTestRecord(t *testing.T, st *store.Store, handle string) string {
	t.Helper()
	id, err := st.QueueUnmatchedRecord(context.Background(), store.UnmatchedRecord{
		Origin:        store.UnmatchedOriginScan,
		LibraryID:     "library-local",
		ImporterID:    "demo",
		SourceHandle:  handle,
		FailureReason: store.UnmatchedReasonNoCandidates,
	})
	if err != nil {
		t.Fatalf("QueueUnmatchedRecord: %v", err)
	}
	return id
}

// TestCreateMediaFromUnmatchedResolvesRecord covers the whole METAPARITY-6
// journey at the wire boundary: a hand-entered title/year becomes a real Media
// with no external ID, and the record leaves the review inbox.
func TestCreateMediaFromUnmatchedResolvesRecord(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/home-movie.mkv")

	media, err := handler.createMediaFromUnmatched(ctx, managerScope(), request{
		Variables: map[string]any{
			"recordID": recordID, "libraryID": "library-local",
			"title": "Home Movie 1994", "year": 1994,
		},
	})
	if err != nil {
		t.Fatalf("createMediaFromUnmatched: %v", err)
	}
	if media.Title != "Home Movie 1994" || media.Year != 1994 {
		t.Fatalf("media = %#v, want the hand-entered identity", media)
	}
	ids, err := st.MediaExternalIDs(ctx, store.MediaID(media.ID))
	if err != nil {
		t.Fatalf("MediaExternalIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("external IDs = %#v, want none", ids)
	}
	records, err := handler.unmatchedRecords(ctx, managerScope(), request{})
	if err != nil {
		t.Fatalf("unmatchedRecords: %v", err)
	}
	for _, record := range records {
		if record.ID == recordID {
			t.Fatal("the resolved record is still in the pending inbox")
		}
	}
}

// TestCreateMediaFromUnmatchedRejectsUnauthorizedLibrary is the cross-Library
// guard: a Library the session cannot see is indistinguishable from one that
// does not exist, and no Media is created.
func TestCreateMediaFromUnmatchedRejectsUnauthorizedLibrary(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/restricted.mkv")

	_, err := handler.createMediaFromUnmatched(ctx, auth.Scope{
		Revealed: true, LibraryIDs: []store.LibraryID{"library-local"},
	}, request{
		Variables: map[string]any{
			"recordID": recordID, "libraryID": "library-archive", "title": "Sneaky",
		},
	})
	if err == nil || err.Error() != "unknown library" {
		t.Fatalf("cross-Library create = %v, want unknown library", err)
	}
}

// TestCreateMediaFromUnmatchedRejectsUnknownRecord keeps a create from
// inventing a Media for a record the caller cannot see.
func TestCreateMediaFromUnmatchedRejectsUnknownRecord(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	_, err := handler.createMediaFromUnmatched(context.Background(), managerScope(), request{
		Variables: map[string]any{
			"recordID": "urec-nope", "libraryID": "library-local", "title": "Ghost",
		},
	})
	if err == nil || err.Error() != "unknown unmatched record" {
		t.Fatalf("unknown record create = %v, want unknown unmatched record", err)
	}
}

// TestSuppressUnmatchedScopesAreIndependentOverTheWire is METAPARITY-7 at the
// resolver boundary: each scope is addressed by its own stated identity, the
// provenance comes back with the actor, and resetting one leaves its siblings
// suppressed.
func TestSuppressUnmatchedScopesAreIndependentOverTheWire(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/one.mkv")
	scope := managerScope()

	targets := []map[string]any{
		{"scope": store.SuppressionScopeSourceRecord, "recordID": recordID},
		{"scope": store.SuppressionScopeFile, "fingerprint": "sha256:one", "sourceHandle": "/media/one.mkv"},
		{"scope": store.SuppressionScopeCandidate, "recordID": recordID, "candidateID": "cand-9"},
	}
	for _, vars := range targets {
		ok, err := handler.suppressUnmatched(ctx, scope, request{Variables: vars})
		if err != nil || !ok {
			t.Fatalf("suppressUnmatched(%v) = %v, %v", vars["scope"], ok, err)
		}
	}

	listed, err := handler.unmatchedSuppressions(ctx, scope, request{})
	if err != nil {
		t.Fatalf("unmatchedSuppressions: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("suppressions = %d, want all three scopes", len(listed))
	}
	for _, info := range listed {
		if info.CreatedAt == "" {
			t.Fatalf("suppression %#v carries no provenance time", info)
		}
		if info.Scope == store.SuppressionScopeCandidate && info.CandidateID != "cand-9" {
			t.Fatalf("candidate suppression = %#v, want the candidate named", info)
		}
		if info.Scope != store.SuppressionScopeFile && info.ActorProfileID != "profile-default" {
			t.Fatalf("suppression %#v carries no actor", info)
		}
	}

	// Resetting the candidate scope restores ONLY that suggestion.
	reset, err := handler.resetUnmatchedSuppression(ctx, scope, request{Variables: targets[2]})
	if err != nil || !reset {
		t.Fatalf("resetUnmatchedSuppression(candidate) = %v, %v", reset, err)
	}
	remaining, err := handler.unmatchedSuppressions(ctx, scope, request{})
	if err != nil {
		t.Fatalf("unmatchedSuppressions after reset: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("suppressions after one reset = %d, want the two siblings intact", len(remaining))
	}
	for _, info := range remaining {
		if info.Scope == store.SuppressionScopeCandidate {
			t.Fatal("resetting the candidate scope left a candidate suppression behind")
		}
	}
}

// TestSuppressUnmatchedRequiresAnExplicitScope keeps the ADR-0068 rule that
// scope is never inferred from a generic "Ignore", and that each scope names the
// identity it actually needs.
func TestSuppressUnmatchedRequiresAnExplicitScope(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/one.mkv")
	for _, vars := range []map[string]any{
		{},
		{"scope": "everything", "recordID": recordID},
		{"scope": store.SuppressionScopeFile},
		{"scope": store.SuppressionScopeCandidate, "recordID": recordID},
	} {
		if _, err := handler.suppressUnmatched(ctx, managerScope(), request{Variables: vars}); err == nil {
			t.Fatalf("suppressUnmatched(%v) succeeded, want a client error", vars)
		}
	}
}

// TestUnmatchedRecordMutationsOverGraphQL exercises the three METAPARITY-6/7
// mutations through real GraphQL mutation documents, so the dispatch wiring and
// the manager role gate are covered alongside the resolver behaviour.
func TestUnmatchedRecordMutationsOverGraphQL(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	recordID := queueTestRecord(t, st, "/media/wire.mkv")

	suppressBody := `{"query":"mutation SuppressUnmatched($scope: String!, $recordID: String!) { suppressUnmatched(scope: $scope, recordID: $recordID) }","variables":{"scope":"source-record","recordID":"` + recordID + `"}}`
	if data := runManagerMutation(t, handler, suppressBody); data["suppressUnmatched"] != true {
		t.Fatalf("suppressUnmatched = %#v, want true", data["suppressUnmatched"])
	}

	resetBody := `{"query":"mutation ResetUnmatchedSuppression($scope: String!, $recordID: String!) { resetUnmatchedSuppression(scope: $scope, recordID: $recordID) }","variables":{"scope":"source-record","recordID":"` + recordID + `"}}`
	if data := runManagerMutation(t, handler, resetBody); data["resetUnmatchedSuppression"] != true {
		t.Fatalf("resetUnmatchedSuppression = %#v, want true", data["resetUnmatchedSuppression"])
	}

	createBody := `{"query":"mutation CreateMediaFromUnmatched($recordID: String!, $libraryID: String!, $title: String!, $year: Int) { createMediaFromUnmatched(recordID: $recordID, libraryID: $libraryID, title: $title, year: $year) { id title year } }","variables":{"recordID":"` + recordID + `","libraryID":"library-local","title":"Wire Movie","year":2001}}`
	created, ok := runManagerMutation(t, handler, createBody)["createMediaFromUnmatched"].(map[string]any)
	if !ok || created["title"] != "Wire Movie" {
		t.Fatalf("createMediaFromUnmatched = %#v, want the hand-authored Media", created)
	}
}

// runManagerMutation posts one GraphQL mutation as a manager and returns its
// data object, failing the test on any error.
func runManagerMutation(t *testing.T, handler *Handler, body string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Data   map[string]any             `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	return resp.Data
}

// TestCreateMediaFromUnmatchedBrowsesAndPlays closes the METAPARITY-6 loop past
// row existence: the hand-authored Media must come back through the client
// browse resolver and negotiate a real direct-play decision, not merely leave a
// media_file row behind.
func TestCreateMediaFromUnmatchedBrowsesAndPlays(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/home-movie.mkv")

	created, err := handler.createMediaFromUnmatched(ctx, managerScope(), request{
		Variables: map[string]any{
			"recordID": recordID, "libraryID": "library-local",
			"title": "Home Movie 1994", "year": 1994,
		},
	})
	if err != nil {
		t.Fatalf("createMediaFromUnmatched: %v", err)
	}

	browsed, err := handler.media(ctx, managerScope(), request{
		Variables: map[string]any{"id": created.ID},
	})
	if err != nil {
		t.Fatalf("media browse resolver: %v", err)
	}
	if browsed.Title != "Home Movie 1994" || browsed.Year != 1994 {
		t.Fatalf("browsed = %#v, want the hand-entered identity via the client query path", browsed)
	}

	playScope, err := auth.NewService(st).Scope(ctx, "session-default")
	if err != nil {
		t.Fatalf("session scope: %v", err)
	}
	tokens := streamtoken.NewService([]byte("test-token-key"), time.Now)
	resp, err := playback.NewService(st, tokens, time.Now).Info(ctx, playScope, playback.InfoRequest{
		MediaID: created.ID,
		CapabilityProfile: playback.CapabilityProfile{
			Client:      "beamfall-test",
			Containers:  []string{"mkv"},
			VideoCodecs: []playback.VideoCodecCap{{Codec: "h264", MaxHeight: 2160}},
		},
	})
	if err != nil {
		t.Fatalf("playback Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play for the hand-authored Media", resp.Decision)
	}
	if len(resp.Streams) == 0 || resp.Streams[0].URL == "" {
		t.Fatalf("Streams = %#v, want a playable direct stream URL", resp.Streams)
	}
}

// TestPreparedUploadedArtworkAppliesAndAudits covers METAPARITY-6-ARTWORK-1's
// server->graph seam: uploaded bytes for a hand-authored Media are imported
// and pinned through the same store side effects pinArtworkCandidate uses for
// a fetched candidate (ImportArtwork + MarkArtworkCandidateApplied), and the
// synthetic "upload:" candidate row never leaks its marker as a real fetch.
func TestPreparedUploadedArtworkAppliesAndAudits(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/home-movie.mkv")
	created, err := handler.createMediaFromUnmatched(ctx, managerScope(), request{
		Variables: map[string]any{
			"recordID": recordID, "libraryID": "library-local",
			"title": "Home Movie 1994", "year": 1994,
		},
	})
	if err != nil {
		t.Fatalf("createMediaFromUnmatched: %v", err)
	}

	fetcher := &fakeArtworkCandidateFetcher{}
	importer := &uploadedArtworkTestImporter{fakeArtworkImporter: &fakeArtworkImporter{}, dir: t.TempDir()}
	handler.SetArtworkCandidateServices(fetcher, importer)

	mediaID := store.MediaID(created.ID)
	upload := []byte("uploaded poster bytes")
	for attempt := 1; attempt <= 2; attempt++ {
		if err := applyUploadedArtworkForTest(
			ctx, handler, st, managerScope(), mediaID, artwork.SlotPoster, upload,
		); err != nil {
			t.Fatalf("apply uploaded artwork attempt %d: %v", attempt, err)
		}
	}

	if len(importer.imports) != 1 || importer.imports[0].mediaID != mediaID ||
		importer.imports[0].slot != artwork.SlotPoster || importer.imports[0].data != "uploaded poster bytes" {
		t.Fatalf("imports = %#v", importer.imports)
	}
	if len(fetcher.seen) != 0 {
		t.Fatalf("fetcher.seen = %#v, an uploaded candidate must never be dereferenced by URL", fetcher.seen)
	}
	candidates, err := st.ArtworkCandidates(ctx, mediaID, artwork.SlotPoster, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(upload)
	wantSourceURL := "beamfall-upload://sha256/" + hex.EncodeToString(digest[:])
	if len(candidates) != 1 || !candidates[0].Applied || candidates[0].SourceURL != wantSourceURL {
		t.Fatalf("candidates = %#v, want one applied synthetic candidate", candidates)
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	pins := 0
	for _, entry := range entries {
		if entry.Action == store.ActivityArtworkPin && entry.TargetID == created.ID {
			pins++
		}
	}
	if pins != 1 {
		t.Fatalf("artwork pin activity rows = %d, want 1: %#v", pins, entries)
	}
}

type uploadedArtworkTestImporter struct {
	*fakeArtworkImporter
	dir string
	err error
}

func applyUploadedArtworkForTest(
	ctx context.Context,
	handler *Handler,
	st *store.Store,
	scope auth.Scope,
	mediaID store.MediaID,
	slot string,
	data []byte,
) error {
	apply, err := handler.PrepareUploadedArtwork(ctx, scope, mediaID, slot, data)
	if err != nil {
		return err
	}
	return st.ApplyArtworkCandidate(ctx, apply)
}

func (i *uploadedArtworkTestImporter) ArtworkPath(mediaID store.MediaID, slot string) string {
	return filepath.Join(i.dir, string(mediaID), slot+".jpg")
}

func (i *uploadedArtworkTestImporter) ImportArtwork(
	ctx context.Context,
	mediaID store.MediaID,
	slot string,
	data []byte,
) error {
	if err := i.fakeArtworkImporter.ImportArtwork(ctx, mediaID, slot, data); err != nil {
		return err
	}
	path := i.ArtworkPath(mediaID, slot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if err := os.Remove(path + ".generated"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	derivatives, err := uploadedArtworkDerivativePaths(path)
	if err != nil {
		return err
	}
	for _, derivative := range derivatives {
		if err := os.Remove(derivative); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return i.err
}

func TestCreateMediaFromUnmatchedArtworkResolvesOnlyAfterArtworkSucceeds(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/retry-artwork.mkv")
	injected := errors.New("injected artwork failure")
	importer := &uploadedArtworkTestImporter{
		fakeArtworkImporter: &fakeArtworkImporter{},
		dir:                 t.TempDir(),
		err:                 injected,
	}
	handler.SetArtworkCandidateServices(nil, importer)
	upload := []byte("retry artwork bytes")

	if _, err := handler.CreateMediaFromUnmatchedArtwork(
		ctx, managerScope(), recordID, "library-local", "Retry Artwork", 2001, upload,
	); !errors.Is(err, injected) {
		t.Fatalf("failed artwork create = %v, want %v", err, injected)
	}
	record, err := st.UnmatchedRecordByID(
		ctx, recordID, store.LibraryFilter{AllLibraries: true, Revealed: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.Resolved() {
		t.Fatalf("artwork failure resolved the source record: %#v", record)
	}
	if record.MediaID == "" || record.LibraryID != "library-local" {
		t.Fatalf("failed create did not expose its pending reservation: %#v", record)
	}
	reservedMediaID := record.MediaID

	importer.err = nil
	mediaID, err := handler.CreateMediaFromUnmatchedArtwork(
		ctx, managerScope(), recordID, "library-local", "Retry Artwork", 2001, upload,
	)
	if err != nil {
		t.Fatalf("retry artwork create: %v", err)
	}
	if store.MediaID(mediaID) != reservedMediaID {
		t.Fatalf("retry Media = %q, want reserved %q", mediaID, reservedMediaID)
	}
	record, err = st.UnmatchedRecordByID(
		ctx, recordID, store.LibraryFilter{AllLibraries: true, Revealed: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Resolved() || record.ResolvedMediaID != store.MediaID(mediaID) {
		t.Fatalf("successful retry record = %#v, want resolution to %q", record, mediaID)
	}
	replacementURL := "https://artwork.example.test/replacement.jpg"
	if err := st.UpsertArtworkCandidate(
		ctx, store.MediaID(mediaID), artwork.SlotPoster, replacementURL, "demo", "lookup",
	); err != nil {
		t.Fatal(err)
	}
	replacementID := store.ArtworkCandidateID(store.MediaID(mediaID), artwork.SlotPoster, replacementURL)
	if err := st.MarkArtworkCandidateApplied(ctx, replacementID); err != nil {
		t.Fatal(err)
	}
	replayedID, err := handler.CreateMediaFromUnmatchedArtwork(
		ctx, managerScope(), recordID, "library-local", "Retry Artwork", 2001, upload,
	)
	if err != nil || replayedID != mediaID {
		t.Fatalf("lost-response retry media=%q err=%v, want %q", replayedID, err, mediaID)
	}
	if len(importer.imports) != 2 {
		t.Fatalf("artwork imports = %d, want failed attempt plus successful retry only", len(importer.imports))
	}
	candidates, err := st.ArtworkCandidates(
		ctx, store.MediaID(mediaID), artwork.SlotPoster, store.LibraryFilter{AllLibraries: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	appliedID := ""
	for _, candidate := range candidates {
		if candidate.Applied {
			appliedID = candidate.ID
		}
	}
	if appliedID != replacementID {
		t.Fatalf("stale create replay replaced newer artwork: %#v", candidates)
	}
}

func TestPreparedUploadedArtworkFailureLeavesNoPartialState(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/failed-artwork.mkv")
	created, err := handler.createMediaFromUnmatched(ctx, managerScope(), request{
		Variables: map[string]any{
			"recordID": recordID, "libraryID": "library-local", "title": "Failed Artwork",
		},
	})
	if err != nil {
		t.Fatalf("createMediaFromUnmatched: %v", err)
	}
	injected := errors.New("injected mid-apply failure")
	importer := &uploadedArtworkTestImporter{
		fakeArtworkImporter: &fakeArtworkImporter{},
		dir:                 t.TempDir(),
		err:                 injected,
	}
	handler.SetArtworkCandidateServices(nil, importer)
	mediaID := store.MediaID(created.ID)
	artworkPath := importer.ArtworkPath(mediaID, artwork.SlotPoster)
	markerPath := artworkPath + ".generated"
	derivativePath := filepath.Join(filepath.Dir(artworkPath), artwork.SlotPoster+".w320.jpg")
	for path, data := range map[string][]byte{
		artworkPath:    []byte("prior artwork"),
		markerPath:     []byte("prior generated marker"),
		derivativePath: []byte("prior derivative"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err = applyUploadedArtworkForTest(
		ctx, handler, st, managerScope(), mediaID, artwork.SlotPoster, []byte("partially written artwork"),
	)
	if !errors.Is(err, injected) {
		t.Fatalf("apply uploaded artwork error = %v, want injected failure", err)
	}
	candidates, err := st.ArtworkCandidates(ctx, mediaID, artwork.SlotPoster, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates after failed apply = %#v, want none", candidates)
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{
		Filter: store.LibraryFilter{Revealed: true, AllLibraries: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Action == store.ActivityArtworkPin && entry.TargetID == created.ID {
			t.Fatalf("activity after failed apply = %#v, want none", entry)
		}
	}
	for path, want := range map[string]string{
		artworkPath:    "prior artwork",
		markerPath:     "prior generated marker",
		derivativePath: "prior derivative",
	} {
		// Test paths are all rooted beneath the test-owned temporary directory.
		//nolint:gosec // G304: bounded test fixture path.
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read restored %s: %v", filepath.Base(path), err)
		}
		if string(got) != want {
			t.Fatalf("restored %s = %q, want %q", filepath.Base(path), got, want)
		}
	}
}

func TestPreparedUploadedArtworkFailureRestoresLatestSerializedState(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/serialized-artwork.mkv")
	created, err := handler.createMediaFromUnmatched(ctx, managerScope(), request{
		Variables: map[string]any{
			"recordID": recordID, "libraryID": "library-local", "title": "Serialized Artwork",
		},
	})
	if err != nil {
		t.Fatalf("createMediaFromUnmatched: %v", err)
	}
	importer := &uploadedArtworkTestImporter{
		fakeArtworkImporter: &fakeArtworkImporter{},
		dir:                 t.TempDir(),
	}
	handler.SetArtworkCandidateServices(nil, importer)
	mediaID := store.MediaID(created.ID)
	path := importer.ArtworkPath(mediaID, artwork.SlotPoster)
	markerPath := path + ".generated"
	derivativePath := filepath.Join(filepath.Dir(path), artwork.SlotPoster+".w320.jpg")
	for filePath, data := range map[string][]byte{
		path:           []byte("original artwork"),
		markerPath:     []byte("original marker"),
		derivativePath: []byte("original derivative"),
	} {
		if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := handler.PrepareUploadedArtwork(
		ctx, managerScope(), mediaID, artwork.SlotPoster, []byte("first replacement"),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := handler.PrepareUploadedArtwork(
		ctx, managerScope(), mediaID, artwork.SlotPoster, []byte("failed second replacement"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyArtworkCandidate(ctx, first); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	injected := errors.New("injected second import failure")
	importer.err = injected
	if err := st.ApplyArtworkCandidate(ctx, second); !errors.Is(err, injected) {
		t.Fatalf("second apply error = %v, want injected failure", err)
	}
	// path is rooted beneath the test-owned temporary directory.
	//nolint:gosec // G304: bounded test fixture path.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first replacement" {
		t.Fatalf("artwork after failed second apply = %q, want first replacement", got)
	}
	for _, removed := range []string{markerPath, derivativePath} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s after failed second apply error = %v, want not-exist", filepath.Base(removed), err)
		}
	}
	candidates, err := st.ArtworkCandidates(ctx, mediaID, artwork.SlotPoster, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !candidates[0].Applied || candidates[0].SourceURL == second.SourceURL {
		t.Fatalf("candidates after failed second apply = %#v, want only first applied", candidates)
	}
}

// TestPrepareUploadedArtworkRejectsBadInputFailClosed covers the seam's
// validation gate: an unknown media, an unknown slot, empty bytes, and an
// unwired importer all fail closed as a store.ValidationError (so
// internal/server's HTTP handler can classify them as 400/503 without
// depending on this package's unexported clientError type).
func TestPrepareUploadedArtworkRejectsBadInputFailClosed(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	ctx := context.Background()
	recordID := queueTestRecord(t, st, "/media/home-movie.mkv")
	created, err := handler.createMediaFromUnmatched(ctx, managerScope(), request{
		Variables: map[string]any{
			"recordID": recordID, "libraryID": "library-local",
			"title": "Home Movie 1994", "year": 1994,
		},
	})
	if err != nil {
		t.Fatalf("createMediaFromUnmatched: %v", err)
	}
	mediaID := store.MediaID(created.ID)

	// candidateImport is never wired on this handler.
	if _, err := handler.PrepareUploadedArtwork(ctx, managerScope(), mediaID, artwork.SlotPoster, []byte("bytes")); err == nil {
		t.Fatal("want an error when no ArtworkCandidateImporter is wired")
	} else if _, ok := err.(store.ValidationError); !ok {
		t.Fatalf("err = %#v, want store.ValidationError", err)
	}

	handler.SetArtworkCandidateServices(&fakeArtworkCandidateFetcher{}, &fakeArtworkImporter{})
	for name, tc := range map[string]struct {
		mediaID store.MediaID
		slot    string
		data    []byte
	}{
		"unknown media":  {mediaID: "media-does-not-exist", slot: artwork.SlotPoster, data: []byte("bytes")},
		"unknown slot":   {mediaID: mediaID, slot: "not-a-slot", data: []byte("bytes")},
		"empty data":     {mediaID: mediaID, slot: artwork.SlotPoster, data: nil},
		"empty media ID": {mediaID: "", slot: artwork.SlotPoster, data: []byte("bytes")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := handler.PrepareUploadedArtwork(ctx, managerScope(), tc.mediaID, tc.slot, tc.data)
			if _, ok := err.(store.ValidationError); !ok {
				t.Fatalf("err = %#v, want store.ValidationError", err)
			}
		})
	}
}
