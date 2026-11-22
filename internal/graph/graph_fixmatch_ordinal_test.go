package graph

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

const fixMatchOrdinalMutation = `mutation($mediaID: String!, $candidateID: String!, $sequence: String, $index: Int, $clearLocks: [String!]) {
	fixMatch(mediaID: $mediaID, pluginID: "demo", scraperID: "lookup", candidateID: $candidateID, sequence: $sequence, index: $index, clearLocks: $clearLocks) { title }
}`

const fixMatchOrdinalPreview = `query($mediaID: String!, $candidateID: String!, $sequence: String, $index: Int) {
	fixMatchPreview(mediaID: $mediaID, pluginID: "demo", scraperID: "lookup", candidateID: $candidateID, sequence: $sequence, index: $index) { field proposed }
}`

const clearMediaOrdinalMutation = `mutation($mediaID: String!) { clearMediaOrdinal(mediaID: $mediaID) }`
const mediaOrdinalMetadataQuery = `query($mediaID: String!) { mediaMetadata(mediaID: $mediaID) { operatorOrdinal { sequence index } } }`

type ordinalTestParser struct{}

func (ordinalTestParser) ParsedMediaTitle(context.Context, store.MediaID, string) (string, error) {
	return "", nil
}

func (ordinalTestParser) ParsedMediaOrdinal(context.Context, store.MediaID, string) (string, int, error) {
	return "1", 3, nil
}

type ordinalTestEngine struct {
	identifyTestEngine
	lookedUp []store.MediaOrdinal
}

func (e *ordinalTestEngine) Lookup(_ context.Context, _ plugin.ScraperSpec, id string, vars ...scrape.Variables) (scrape.Result, error) {
	ordinal := store.MediaOrdinal{Sequence: vars[0].Sequence, Index: vars[0].Index}
	e.lookedUp = append(e.lookedUp, ordinal)
	if id == "failed-lookup" {
		return scrape.Result{}, errors.New("injected lookup failure")
	}
	result := scrape.Result{ID: id, Fields: map[string]any{"title": fmt.Sprintf("Sequence %s, index %d", ordinal.Sequence, ordinal.Index)}}
	if id == "failed-hydration" {
		result.Entities = []scrape.EntityResult{{Kind: "group", Name: "Hydrated group"}}
	}
	return result, nil
}

func newOrdinalTestService(engine *ordinalTestEngine, bundles identify.Bundles, st *store.Store) *identify.Service {
	service := identify.NewService(engine, bundles, st)
	service.SetMediaTitles(ordinalTestParser{})
	return service
}

func TestFixMatchOperatorOrdinalFollowsMedia(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	engine := &ordinalTestEngine{}
	service := newOrdinalTestService(engine, handler.plugins, st)
	handler.SetIdentify(service)
	ctx := t.Context()
	imported := store.ImportMediaFile{
		LibraryID: "library-local", Path: "/fixtures/S01E03.mkv", Title: "Original",
		Container: "mkv", Codec: "h264", ContentFingerprint: "sha256:ordinal",
	}
	item, file, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]any{"mediaID": string(item.ID), "candidateID": "42", "sequence": "2", "index": 5}
	want := store.MediaOrdinal{Sequence: "2", Index: 5}
	parsed := store.MediaOrdinal{Sequence: "1", Index: 3}
	assertLookup := func(want store.MediaOrdinal) {
		t.Helper()
		if len(engine.lookedUp) == 0 || engine.lookedUp[len(engine.lookedUp)-1] != want {
			t.Fatalf("lookups = %v, want last lookup %+v", engine.lookedUp, want)
		}
	}

	var preview []FixMatchFieldDiff
	execGraph(t, handler, authorizeManager, fixMatchOrdinalPreview, vars, "fixMatchPreview", &preview)
	assertLookup(want)
	if preview[0].Proposed != "Sequence 2, index 5" {
		t.Fatalf("preview = %+v", preview)
	}
	var metadata MediaMetadataInfo
	execGraph(t, handler, authorizeManager, mediaOrdinalMetadataQuery, vars, "mediaMetadata", &metadata)
	if metadata.OperatorOrdinal != nil {
		t.Fatalf("preview saved ordinal %+v", metadata.OperatorOrdinal)
	}

	var matched MediaInfo
	execGraph(t, handler, authorizeManager, fixMatchOrdinalMutation, vars, "fixMatch", &matched)
	assertLookup(want)
	if matched.Title != "Sequence 2, index 5" {
		t.Fatalf("applied title = %q", matched.Title)
	}
	execGraph(t, handler, authorizeManager, mediaOrdinalMetadataQuery, vars, "mediaMetadata", &metadata)
	if metadata.OperatorOrdinal == nil || *metadata.OperatorOrdinal != (MediaOrdinalInfo{Sequence: "2", Index: 5}) {
		t.Fatalf("editor ordinal = %+v", metadata.OperatorOrdinal)
	}
	var locks []FixMatchLockedFieldInfo
	execGraph(t, handler, authorizeManager, `query($mediaID: String!) { fixMatchLockedFields(mediaID: $mediaID) { field value } }`,
		vars, "fixMatchLockedFields", &locks)
	if !slices.Contains(locks, FixMatchLockedFieldInfo{Field: "ordinal", Value: "2 / 5"}) {
		t.Fatalf("preview's locked fields = %+v", locks)
	}
	export, err := st.ExportCatalog(ctx, managerScope().LibraryFilter())
	if err != nil {
		t.Fatal(err)
	}
	exported := slices.IndexFunc(export.Media, func(row store.ExportedMedia) bool { return row.Media.ID == item.ID })
	if exported < 0 || export.Media[exported].OperatorOrdinal == nil || *export.Media[exported].OperatorOrdinal != want {
		t.Fatalf("catalog export omitted ordinal for %s", item.ID)
	}

	assertRefresh := func() {
		t.Helper()
		if err := service.Refresh(ctx, item.ID); err != nil {
			t.Fatal(err)
		}
		assertLookup(want)
	}
	rescanned, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil || rescanned.ID != item.ID {
		t.Fatalf("rescan = %s, %v, want %s", rescanned.ID, err, item.ID)
	}
	assertRefresh()
	parserFields := []store.MediaField{{Name: "sequence", Type: store.EntityFieldText, Text: "1"}, {Name: "index", Type: store.EntityFieldNumber, Number: 3}}
	if err := st.RefreshParserMediaFields(ctx, item.ID, store.ParserFieldRefresh{Fields: parserFields}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, item.ID, parserFields, store.MutationSourceScan); err != nil {
		t.Fatal(err)
	}
	assertRefresh()

	bundle, ok := handler.plugins.Bundle("demo")
	if !ok {
		t.Fatal("demo plugin missing")
	}
	bundle.Version = "0.2.0"
	upgraded, err := newTestPluginRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	service = newOrdinalTestService(engine, upgraded, st)
	handler.SetIdentify(service)
	assertRefresh()

	imported.Path = "/moved/renamed-S01E03.mkv"
	moved, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil || moved.ID != item.ID {
		t.Fatalf("renamed item = %s, %v, want %s", moved.ID, err, item.ID)
	}
	assertRefresh()
	imported.ContentFingerprint = "sha256:different"
	different, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil || different.ID == item.ID {
		t.Fatalf("different content = %s, %v", different.ID, err)
	}
	if err := service.Apply(ctx, different.ID, "demo", "lookup", "42"); err != nil {
		t.Fatal(err)
	}
	assertLookup(parsed)

	var cleared bool
	execGraph(t, handler, authorizeManager, clearMediaOrdinalMutation, vars, "clearMediaOrdinal", &cleared)
	if !cleared {
		t.Fatal("ordinal was not cleared")
	}
	execGraph(t, handler, authorizeManager, mediaOrdinalMetadataQuery, vars, "mediaMetadata", &metadata)
	if metadata.OperatorOrdinal != nil {
		t.Fatalf("after clear, editor ordinal = %+v", metadata.OperatorOrdinal)
	}
	if err := service.Refresh(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	assertLookup(parsed)
	if pinned, found, err := st.MatchPinned(ctx, file.ContentFingerprint); err != nil || !found || !pinned {
		t.Fatalf("match pin after clear = %v, %v, %v", pinned, found, err)
	}
}

func TestFixMatchOperatorOrdinalKeepClearAndFailure(t *testing.T) {
	handler, st := newIdentifyTestHandler(t)
	engine := &ordinalTestEngine{}
	handler.SetIdentify(newOrdinalTestService(engine, handler.plugins, st))
	ctx := t.Context()
	const id = "media-alpha"
	want := store.MediaOrdinal{Sequence: "2", Index: 5}
	parsed := store.MediaOrdinal{Sequence: "1", Index: 3}
	set := map[string]any{"mediaID": id, "candidateID": "42", "sequence": "2", "index": 5}
	apply := func(t *testing.T, vars map[string]any) {
		t.Helper()
		var matched MediaInfo
		execGraph(t, handler, authorizeManager, fixMatchOrdinalMutation, vars, "fixMatch", &matched)
	}
	assertStored := func(t *testing.T, want *store.MediaOrdinal) {
		t.Helper()
		got, err := st.MediaOperatorOrdinal(ctx, id)
		if err != nil || (got == nil) != (want == nil) || (want != nil && *got != *want) {
			t.Fatalf("stored ordinal = %+v, %v, want %+v", got, err, want)
		}
	}
	for _, failure := range []string{"failed-lookup", "failed-hydration"} {
		t.Run(failure, func(t *testing.T) {
			attempt := maps.Clone(set)
			attempt["candidateID"], attempt["sequence"] = failure, "9"
			for _, previous := range []*store.MediaOrdinal{nil, &want} {
				if previous != nil {
					apply(t, set)
				}
				before := len(engine.lookedUp)
				resp := postGraphJSON(t, handler, authorizeManager, fixMatchOrdinalMutation, attempt)
				if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeInternal ||
					len(engine.lookedUp) != before+1 || engine.lookedUp[before] != (store.MediaOrdinal{Sequence: "9", Index: 5}) {
					t.Fatalf("failure response = %+v, lookups = %v", resp.Errors, engine.lookedUp)
				}
				assertStored(t, previous)
			}
			var cleared bool
			execGraph(t, handler, authorizeManager, clearMediaOrdinalMutation, set, "clearMediaOrdinal", &cleared)
		})
	}

	for _, tc := range []struct {
		name      string
		candidate string
		locks     []string
		lookup    store.MediaOrdinal
		stored    *store.MediaOrdinal
	}{
		{"same candidate", "42", nil, want, &want},
		{"different candidate", "43", nil, parsed, nil},
		{"explicit clear", "42", []string{"ordinal"}, parsed, nil},
		{"explicitly keep", "43", []string{}, want, &want},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apply(t, set)
			vars := map[string]any{"mediaID": id, "candidateID": tc.candidate}
			if tc.locks != nil {
				vars["clearLocks"] = tc.locks
			}
			apply(t, vars)
			if got := engine.lookedUp[len(engine.lookedUp)-1]; got != tc.lookup {
				t.Fatalf("lookup = %+v, want %+v", got, tc.lookup)
			}
			assertStored(t, tc.stored)
		})
	}

	partial := map[string]any{"mediaID": id, "candidateID": "42", "index": 0}
	apply(t, partial)
	assertStored(t, &store.MediaOrdinal{})
}

func TestFixMatchOperatorOrdinalValidationAndAccess(t *testing.T) {
	handler, _ := newIdentifyTestHandler(t)
	for _, tc := range []struct {
		field string
		value any
		want  string
	}{
		{"index", -1, "L’indice doit être un entier compris entre 0 et 2147483647."},
		{"sequence", strings.Repeat("é", 33), "La séquence doit être une chaîne de 32 caractères au maximum."},
	} {
		for _, query := range []string{fixMatchOrdinalMutation, fixMatchOrdinalPreview} {
			resp := postGraphJSON(t, handler, func(req *http.Request) {
				authorizeManager(req)
				*req = *req.WithContext(l10n.WithLocale(req.Context(), "fr"))
			}, query, map[string]any{"mediaID": "media-alpha", "candidateID": "42", tc.field: tc.value})
			if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeInvalid || resp.Errors[0].Message != tc.want {
				t.Fatalf("invalid %s = %+v, want %q", tc.field, resp.Errors, tc.want)
			}
		}
	}
	for _, query := range []string{fixMatchOrdinalMutation, fixMatchOrdinalPreview, clearMediaOrdinalMutation, mediaOrdinalMetadataQuery} {
		for _, tc := range []struct {
			id        string
			authorize func(*http.Request)
			code      ErrorCode
		}{
			{"media-alpha", authorizeViewer, ErrorCodeForbidden},
			{"media-archive", authorizeManager, ErrorCodeNotFound},
		} {
			resp := postGraphJSON(t, handler, tc.authorize, query, map[string]any{"mediaID": tc.id, "candidateID": "42", "index": 5})
			if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != tc.code {
				t.Fatalf("%s with %s = %+v, want %s", query, tc.id, resp.Errors, tc.code)
			}
		}
	}
}
