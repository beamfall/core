package graph

import (
	"net/http"
	"testing"
	"time"

	sdk "github.com/beamfall/plugin-sdk"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const setOperatorRelationTest = `mutation($mediaID: String!, $relation: String!, $entityID: String, $newEntity: JSON, $attributes: JSON, $sortOrder: Int) {
	setMediaRelation(mediaID: $mediaID, relation: $relation, entityID: $entityID, newEntity: $newEntity, attributes: $attributes, sortOrder: $sortOrder) { relation sourceID targetID }
}`

const removeOperatorRelationTest = `mutation($mediaID: String!, $relation: String!, $entityID: String!) {
	removeMediaRelation(mediaID: $mediaID, relation: $relation, entityID: $entityID) { sourceID }
}`

const operatorRelationReadTest = `query($mediaID: String!) {
	entitiesForMedia(mediaID: $mediaID) { id kind relation attributes sortOrder operatorLocked undeclared }
}`

func operatorRelationTestBundle() plugin.Bundle {
	return plugin.Bundle{
		ID: "starlog", Version: "0.1.0", DisplayName: plugin.L("Starlog"), MediaLabel: plugin.L("Entry"),
		Layouts:            []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
		MediaFields:        []plugin.FieldSpec{{Name: "certification", Type: "text"}},
		CertificationScale: []string{"low", "mid", "high"},
		EntityKinds: []plugin.EntityKind{
			{Name: "series", Archetype: "container", Relations: []plugin.RelationSpec{{
				Name: "series-member", ToKind: plugin.MediaKindName, Cardinality: "one",
				TypedAttributes: []sdk.RelationAttributeSpec{{Name: "sequence", Type: "number"}},
			}}},
			{Name: "contributor", Archetype: "person", Relations: []plugin.RelationSpec{{
				Name: "credit", ToKind: plugin.MediaKindName, Attributes: []string{"role"},
			}}},
		},
	}
}

func operatorRelationTestHandler(t *testing.T, st *store.Store, bundle plugin.Bundle) *Handler {
	t.Helper()
	registry, err := newTestPluginRegistry([]plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	service := auth.NewService(st)
	service.SetCertificationScales(map[string][]string{"starlog": bundle.CertificationScale})
	return NewHandler(manifest.NewService(st, time.Now, registry), service, st, registry)
}

func operatorRelationTestMedia(t *testing.T, st *store.Store, restricted bool) store.Media {
	t.Helper()
	ctx := t.Context()
	lib, err := st.CreateLibrary(ctx, "Entries", restricted, "starlog", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: lib.ID, Path: "/entries/" + string(lib.ID) + ".mp4", Title: "Entry",
		Container: "mp4", Codec: "h264", ContentFingerprint: "fp-" + string(lib.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	return media
}

func TestOperatorRelationMutationAndBundleUpgrade(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	bundle := operatorRelationTestBundle()
	handler := operatorRelationTestHandler(t, st, bundle)
	media := operatorRelationTestMedia(t, st, false)
	hidden := operatorRelationTestMedia(t, st, true)
	for _, entity := range []store.Entity{
		{ID: "old-series", LibraryID: media.LibraryID, Kind: "series", Title: "Previous"},
		{ID: "existing-series", LibraryID: media.LibraryID, Kind: "series", Title: "North. Wind"},
		{ID: "hidden-series", LibraryID: hidden.LibraryID, Kind: "series", Title: "North Wind Hidden"},
		{ID: "wrong-kind", LibraryID: media.LibraryID, Kind: "contributor", Title: "North Wind Contributor"},
		{ID: "other-title", LibraryID: media.LibraryID, Kind: "series", Title: "The North Wind"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "old-series", ToMediaID: media.ID, Name: "series-member",
	}); err != nil {
		t.Fatal(err)
	}
	set := func(relation string, extra map[string]any) RelationWriteResult {
		t.Helper()
		vars := map[string]any{"mediaID": string(media.ID), "relation": relation}
		for key, value := range extra {
			vars[key] = value
		}
		var got RelationWriteResult
		execGraph(t, handler, authorizeManager, setOperatorRelationTest, vars, "setMediaRelation", &got)
		return got
	}
	series := set("series-member", map[string]any{
		"newEntity":  map[string]any{"kind": "series", "name": " north wind "},
		"attributes": map[string]any{"sequence": "4.5"}, "sortOrder": 4,
	})
	if series.SourceID != "existing-series" {
		t.Fatalf("set deduplication = %+v", series)
	}

	for _, credit := range []struct{ name, role string }{{"Lee", "author"}, {"Ari", "narrator"}} {
		set("credit", map[string]any{
			"newEntity":  map[string]any{"kind": "contributor", "name": credit.name},
			"attributes": map[string]any{"role": credit.role},
		})
	}
	read := func() []RelatedEntityInfo {
		t.Helper()
		var rows []RelatedEntityInfo
		execGraph(t, handler, authorizeManager, operatorRelationReadTest, map[string]any{"mediaID": string(media.ID)}, "entitiesForMedia", &rows)
		return rows
	}
	rows := read()
	if len(rows) != 3 || rows[0].Attributes["role"] != "narrator" || rows[1].Attributes["role"] != "author" ||
		rows[2].Attributes["sequence"] != "4.5" || rows[2].SortOrder == nil || *rows[2].SortOrder != 4 {
		t.Fatalf("relations = %+v", rows)
	}
	for _, row := range rows {
		if !row.OperatorLocked || row.Undeclared {
			t.Fatalf("relation flags = %+v", row)
		}
	}
	set("credit", map[string]any{"entityID": rows[0].ID, "attributes": map[string]any{"role": "reader"}})
	if rows := read(); len(rows) != 3 || rows[0].Attributes["role"] != "reader" {
		t.Fatalf("updating an existing credit = %+v", rows)
	}

	var matches []EntitySearchInfo
	execGraph(t, handler, authorizeManager, `{ entitySearch(kind: "series", query: "north", limit: 99) { id title libraryID } }`,
		nil, "entitySearch", &matches)
	if len(matches) != 1 || matches[0].ID != "existing-series" {
		t.Fatalf("entity search = %+v", matches)
	}

	bundle.EntityKinds[0].Relations = nil
	handler = operatorRelationTestHandler(t, st, bundle)
	rows = read()
	if len(rows) != 3 || !rows[2].OperatorLocked || !rows[2].Undeclared {
		t.Fatalf("upgraded relation flags = %+v", rows)
	}
	var removed RelationWriteResult
	execGraph(t, handler, authorizeManager, removeOperatorRelationTest, map[string]any{
		"mediaID": string(media.ID), "relation": series.Relation, "entityID": series.SourceID,
	}, "removeMediaRelation", &removed)
	if rows := read(); len(rows) != 2 {
		t.Fatalf("relations after removal = %+v", rows)
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	var foundSet, foundRemove bool
	for _, entry := range entries {
		change := entry.MediaRelationChange
		if change == nil || change.Relation != series.Relation {
			continue
		}
		if entry.ProfileID != "profile-default" || entry.SessionID != "session-manager" {
			t.Fatalf("activity actor = %+v", entry)
		}
		switch entry.Action {
		case store.ActivitySetMediaRelation:
			foundSet = len(change.Before) == 1 && change.Before[0].EntityID == "old-series" &&
				len(change.After) == 1 && change.After[0].EntityID == "existing-series" &&
				change.After[0].Attributes["sequence"] == "4.5" && change.After[0].LockedAtMS != nil
		case store.ActivityRemoveMediaRelation:
			foundRemove = len(change.Before) == 1 && change.Before[0].EntityID == "existing-series" && len(change.After) == 0
		}
	}
	if !foundSet || !foundRemove {
		t.Fatalf("missing before/after recovery rows: set=%v remove=%v", foundSet, foundRemove)
	}
}

func TestOperatorRelationRefusals(t *testing.T) {
	st := storetest.Open(t)
	handler := operatorRelationTestHandler(t, st, operatorRelationTestBundle())
	media := operatorRelationTestMedia(t, st, false)
	hidden := operatorRelationTestMedia(t, st, true)
	if err := st.UpsertEntity(t.Context(), store.Entity{ID: "series", LibraryID: media.LibraryID, Kind: "series", Title: "Series"}); err != nil {
		t.Fatal(err)
	}

	valid := map[string]any{"mediaID": string(media.ID), "relation": "series-member", "entityID": "series"}
	for _, query := range []string{setOperatorRelationTest, removeOperatorRelationTest} {
		resp := postGraphJSON(t, handler, authorizeViewer, query, valid)
		if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeForbidden {
			t.Fatalf("viewer errors = %+v", resp.Errors)
		}
		resp = postGraphJSON(t, handler, authorizeManager, query, map[string]any{
			"mediaID": string(hidden.ID), "relation": "series-member", "entityID": "series",
		})
		if len(resp.Errors) != 1 || resp.Errors[0].Extensions.Code != ErrorCodeNotFound {
			t.Fatalf("hidden media errors = %+v", resp.Errors)
		}
	}

	for name, overrides := range map[string]map[string]any{
		"unknown relation":  {"relation": "unknown"},
		"unknown kind":      {"entityID": nil, "newEntity": map[string]any{"kind": "unknown", "name": "New"}},
		"unknown attribute": {"attributes": map[string]any{"unknown": "4.5"}},
		"both":              {"newEntity": map[string]any{"kind": "series", "name": "New"}},
		"neither":           {"entityID": nil},
	} {
		t.Run(name, func(t *testing.T) {
			vars := map[string]any{}
			for key, value := range valid {
				vars[key] = value
			}
			for key, value := range overrides {
				vars[key] = value
			}
			resp := postGraphJSON(t, handler, func(req *http.Request) {
				*req = *req.WithContext(l10n.WithLocale(req.Context(), "fr"))
				authorizeManager(req)
			}, setOperatorRelationTest, vars)
			if len(resp.Errors) != 1 || resp.Errors[0].Message != "Vérifiez le média, le type d’entité, la relation et les attributs." {
				t.Fatalf("localized refusal = %+v", resp.Errors)
			}
		})
	}
}

func TestOperatorRelationLeavesPlaybackCertificationLockAlone(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	handler := operatorRelationTestHandler(t, st, operatorRelationTestBundle())
	parent := operatorRelationTestMedia(t, st, false)
	ids := map[string]store.MediaID{}
	for _, rating := range []string{"low", "high"} {
		item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: parent.LibraryID, Path: "/entries/" + rating + ".mp4",
			Title: rating, Container: "mp4", Codec: "h264", ContentFingerprint: "rating-" + rating,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, item.ID, []store.MediaField{{Name: "certification", Type: store.EntityFieldText, Text: rating}}); err != nil {
			t.Fatal(err)
		}
		if err := st.LinkMediaRelation(ctx, item.ID, store.MediaRelationExtraOf, parent.ID); err != nil {
			t.Fatal(err)
		}
		ids[rating] = item.ID
	}
	if _, err := st.SetProfileCertificationCeiling(ctx, "profile-default", store.ProfileCertificationCeiling{
		PluginID: "starlog", Ceiling: "mid", FilterVisibility: "locked",
	}); err != nil {
		t.Fatal(err)
	}
	assertPlaybackLocks := func() {
		t.Helper()
		rows := graphData[[]MediaRelationInfo](t, handler,
			`query($mediaID:String!){ mediaRelations(mediaID:$mediaID, relation:"extra-of"){ relation locked media { id } } }`,
			map[string]any{"mediaID": string(parent.ID)}, "mediaRelations")
		got := map[string]bool{}
		for _, row := range rows {
			got[row.Media.ID] = row.Locked
		}
		if len(rows) != 2 || got[string(ids["low"])] || !got[string(ids["high"])] {
			t.Fatalf("playback certification flags = %+v", rows)
		}
	}
	assertPlaybackLocks()
	var relation RelationWriteResult
	vars := map[string]any{"mediaID": string(ids["low"]), "relation": "credit",
		"newEntity": map[string]any{"kind": "contributor", "name": "Participant"}}
	execGraph(t, handler, authorizeManager, setOperatorRelationTest, vars, "setMediaRelation", &relation)
	assertPlaybackLocks()
	vars["entityID"] = relation.SourceID
	execGraph(t, handler, authorizeManager, removeOperatorRelationTest, vars, "removeMediaRelation", &relation)
	assertPlaybackLocks()
}

func TestOperatorRelationUndoConflictIsLocalized(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	handler := operatorRelationTestHandler(t, st, operatorRelationTestBundle())
	media := operatorRelationTestMedia(t, st, false)
	for _, id := range []store.EntityID{"winner", "loser"} {
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: media.LibraryID, Kind: "series", Title: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "loser", ToMediaID: media.ID, Name: "series-member",
	}); err != nil {
		t.Fatal(err)
	}
	batch, err := st.MergeEntities(ctx, "winner", "loser", store.LibraryFilter{AllLibraries: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var removed RelationWriteResult
	execGraph(t, handler, authorizeManager, removeOperatorRelationTest, map[string]any{
		"mediaID": string(media.ID), "relation": "series-member", "entityID": "winner",
	}, "removeMediaRelation", &removed)

	resp := postGraphJSON(t, handler, func(req *http.Request) {
		*req = *req.WithContext(l10n.WithLocale(req.Context(), "fr"))
		authorizeManager(req)
	}, `mutation($batchID:String!){ undoBatch(batchID:$batchID) { batchID } }`, map[string]any{"batchID": batch.ID})
	if len(resp.Errors) != 1 || resp.Errors[0].Message != "Cette fusion ne peut pas être annulée, car une relation a été modifiée depuis." {
		t.Fatalf("undo response = %+v", resp)
	}
}
