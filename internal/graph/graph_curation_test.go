package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestCurationReadsExposeFieldsRelationsAndFiles(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)

	parent, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/parent.mp4",
		Title:              "Parent Media",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-parent",
	})
	if err != nil {
		t.Fatal(err)
	}
	extra, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/extra.mp4",
		Title:              "Extra Media",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-extra",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, parent.ID, []store.MediaField{{Name: "genre", Type: store.EntityFieldText, Text: "quiet"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{
		ID:        "entity-container",
		LibraryID: "library-local",
		Kind:      "container",
		Title:     "Container",
		Fields:    []store.EntityField{{Name: "sort", Type: store.EntityFieldText, Text: "manual"}},
	}); err != nil {
		t.Fatal(err)
	}
	order := 7
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-container",
		Name:         "contains",
		ToMediaID:    parent.ID,
		SortOrder:    &order,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkMediaRelation(ctx, extra.ID, store.MediaRelationExtraOf, parent.ID); err != nil {
		t.Fatal(err)
	}

	body := graphPost(t, handler, authorizeManager,
		`{"query":"query($mediaID:String!,$id:String!,$entityID:String!){ mediaFields(mediaID:$mediaID){ name type value } mediaFiles(mediaID:$mediaID){ mediaID path versionLabel partOrdinal contentFingerprint } entity(id:$id){ id kind title fields { name value } } entityRelations(entityID:$entityID){ relation targetKind sortOrder media { id title } attributes } }","variables":{"mediaID":"`+string(parent.ID)+`","id":"entity-container","entityID":"entity-container"}}`)
	var resp struct {
		Data struct {
			MediaFields     []EntityFieldInfo    `json:"mediaFields"`
			MediaFiles      []MediaFileInfo      `json:"mediaFiles"`
			Entity          EntityInfo           `json:"entity"`
			EntityRelations []EntityRelationInfo `json:"entityRelations"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("errors = %#v", resp.Errors)
	}
	if len(resp.Data.MediaFields) != 1 || resp.Data.MediaFields[0].Name != "genre" || resp.Data.MediaFields[0].Value != "quiet" {
		t.Fatalf("mediaFields = %#v", resp.Data.MediaFields)
	}
	if len(resp.Data.MediaFiles) != 1 || resp.Data.MediaFiles[0].MediaID != string(parent.ID) || resp.Data.MediaFiles[0].PartOrdinal != 0 {
		t.Fatalf("mediaFiles = %#v", resp.Data.MediaFiles)
	}
	if resp.Data.Entity.ID != "entity-container" || len(resp.Data.Entity.Fields) != 1 {
		t.Fatalf("entity = %#v", resp.Data.Entity)
	}
	if len(resp.Data.EntityRelations) != 1 || resp.Data.EntityRelations[0].SortOrder == nil || *resp.Data.EntityRelations[0].SortOrder != 7 {
		t.Fatalf("entityRelations = %#v", resp.Data.EntityRelations)
	}

	relationsBody := graphPost(t, handler, authorizeManager,
		`{"query":"query($mediaID:String!,$relation:String!){ mediaRelations(mediaID:$mediaID, relation:$relation){ relation media { id title } } }","variables":{"mediaID":"`+string(parent.ID)+`","relation":"extra-of"}}`)
	var relationsResp struct {
		Data struct {
			MediaRelations []MediaRelationInfo `json:"mediaRelations"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(relationsBody, &relationsResp); err != nil {
		t.Fatalf("decode mediaRelations: %v (%s)", err, relationsBody)
	}
	if len(relationsResp.Errors) != 0 {
		t.Fatalf("mediaRelations errors = %#v", relationsResp.Errors)
	}
	if len(relationsResp.Data.MediaRelations) != 1 || relationsResp.Data.MediaRelations[0].Media.ID != string(extra.ID) {
		t.Fatalf("mediaRelations = %#v", relationsResp.Data.MediaRelations)
	}
}

func TestRelationReadsBatchFavoriteFixtures(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)

	importMedia := func(path, title, fingerprint string) store.Media {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: path, Title: title,
			Container: "mp4", Codec: "h264", ContentFingerprint: fingerprint,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media
	}
	parent := importMedia("/media/batch-parent.mp4", "Batch Parent", "fp-batch-parent")
	favoriteExtra := importMedia("/media/a-favorite-extra.mp4", "A Favorite Extra", "fp-favorite-extra")
	plainExtra := importMedia("/media/b-plain-extra.mp4", "B Plain Extra", "fp-plain-extra")
	for _, mediaID := range []store.MediaID{parent.ID, favoriteExtra.ID} {
		if err := st.SetMediaFavorite(ctx, "profile-default", mediaID, true, store.LibraryFilter{AllLibraries: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, entity := range []store.Entity{
		{ID: "entity-batch-source", LibraryID: "library-local", Kind: "container", Title: "Batch Source"},
		{ID: "entity-batch-target", LibraryID: "library-local", Kind: "container", Title: "Batch Target"},
		{ID: "entity-batch-empty", LibraryID: "library-local", Kind: "container", Title: "Batch Empty"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	orders := []int{1, 2, 3}
	for _, relation := range []store.EntityRelation{
		{FromEntityID: "entity-batch-source", Name: "contains", ToEntityID: "entity-batch-target", Attributes: map[string]string{"role": "entity"}, SortOrder: &orders[0]},
		{FromEntityID: "entity-batch-source", Name: "contains", ToMediaID: parent.ID, Attributes: map[string]string{"role": "media"}, SortOrder: &orders[1]},
		{FromEntityID: "entity-batch-source", Name: "featured", ToMediaID: parent.ID, Attributes: map[string]string{"role": "duplicate"}, SortOrder: &orders[2]},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	for _, extra := range []store.Media{favoriteExtra, plainExtra} {
		if err := st.LinkMediaRelation(ctx, extra.ID, store.MediaRelationExtraOf, parent.ID); err != nil {
			t.Fatal(err)
		}
	}

	body := graphPost(t, handler, authorizeManager,
		`{"query":"query($entityID:String!){ entityRelations(entityID:$entityID){ relation targetKind sortOrder attributes entity { id kind title } media { id title summary year favorited } } }","variables":{"entityID":"entity-batch-source"}}`)
	var entityResp struct {
		Data struct {
			EntityRelations []EntityRelationInfo `json:"entityRelations"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &entityResp); err != nil {
		t.Fatalf("decode entity relations: %v (%s)", err, body)
	}
	if len(entityResp.Errors) != 0 || len(entityResp.Data.EntityRelations) != 3 {
		t.Fatalf("entity relations = %#v errors=%#v", entityResp.Data.EntityRelations, entityResp.Errors)
	}
	wantEntityRelations := []EntityRelationInfo{
		{Relation: "contains", TargetKind: "entity", Entity: &EntityInfo{ID: "entity-batch-target", Kind: "container", Title: "Batch Target", Fields: []EntityFieldInfo{}}, Attributes: map[string]string{"role": "entity"}, SortOrder: &orders[0]},
		{Relation: "contains", TargetKind: "media", Media: &MediaInfo{ID: string(parent.ID), Title: "Batch Parent", Favorited: true}, Attributes: map[string]string{"role": "media"}, SortOrder: &orders[1]},
		{Relation: "featured", TargetKind: "media", Media: &MediaInfo{ID: string(parent.ID), Title: "Batch Parent", Favorited: true}, Attributes: map[string]string{"role": "duplicate"}, SortOrder: &orders[2]},
	}
	if !reflect.DeepEqual(entityResp.Data.EntityRelations, wantEntityRelations) {
		t.Fatalf("entity relations = %#v, want %#v (body %s)", entityResp.Data.EntityRelations, wantEntityRelations, body)
	}

	body = graphPost(t, handler, authorizeManager,
		`{"query":"query($mediaID:String!,$relation:String!){ mediaRelations(mediaID:$mediaID, relation:$relation){ relation media { id title summary year favorited } } }","variables":{"mediaID":"`+string(parent.ID)+`","relation":"extra-of"}}`)
	var mediaResp struct {
		Data struct {
			MediaRelations []MediaRelationInfo `json:"mediaRelations"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &mediaResp); err != nil {
		t.Fatalf("decode media relations: %v (%s)", err, body)
	}
	if len(mediaResp.Errors) != 0 || len(mediaResp.Data.MediaRelations) != 2 {
		t.Fatalf("media relations = %#v errors=%#v", mediaResp.Data.MediaRelations, mediaResp.Errors)
	}
	wantMediaRelations := []MediaRelationInfo{
		{Relation: "extra-of", Media: MediaInfo{ID: string(favoriteExtra.ID), Title: "A Favorite Extra", Favorited: true, MediaClass: "video"}},
		{Relation: "extra-of", Media: MediaInfo{ID: string(plainExtra.ID), Title: "B Plain Extra", MediaClass: "video"}},
	}
	if !reflect.DeepEqual(mediaResp.Data.MediaRelations, wantMediaRelations) {
		t.Fatalf("media relations = %#v, want %#v", mediaResp.Data.MediaRelations, wantMediaRelations)
	}

	body = graphPost(t, handler, authorizeManager,
		`{"query":"query($entityID:String!){ entityRelations(entityID:$entityID){ relation } }","variables":{"entityID":"entity-batch-empty"}}`)
	if err := json.Unmarshal(body, &entityResp); err != nil || len(entityResp.Errors) != 0 || len(entityResp.Data.EntityRelations) != 0 {
		t.Fatalf("empty entity relations = %#v errors=%#v decode=%v", entityResp.Data.EntityRelations, entityResp.Errors, err)
	}
	body = graphPost(t, handler, authorizeManager,
		`{"query":"query($mediaID:String!,$relation:String!){ mediaRelations(mediaID:$mediaID, relation:$relation){ relation } }","variables":{"mediaID":"`+string(parent.ID)+`","relation":"edition-of"}}`)
	if err := json.Unmarshal(body, &mediaResp); err != nil || len(mediaResp.Errors) != 0 || len(mediaResp.Data.MediaRelations) != 0 {
		t.Fatalf("empty media relations = %#v errors=%#v decode=%v", mediaResp.Data.MediaRelations, mediaResp.Errors, err)
	}
}

func TestCurationRelationWritesAreManagerGatedAndRevealFiltered(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)

	visible, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/visible-relation.mp4",
		Title:              "Visible Relation",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-visible-relation",
	})
	if err != nil {
		t.Fatal(err)
	}
	hidden, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-archive",
		Path:               "/media/hidden-relation.mp4",
		Title:              "Hidden Relation",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-hidden-relation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{ID: "entity-source", LibraryID: "library-local", Kind: "container", Title: "Source"}); err != nil {
		t.Fatal(err)
	}

	mutation := `{"query":"mutation($fromEntityID:String!,$relation:String!,$toMediaID:String!,$sortOrder:Int){ upsertEntityRelation(fromEntityID:$fromEntityID, relation:$relation, toMediaID:$toMediaID, sortOrder:$sortOrder){ relation sourceID targetID } }","variables":{"fromEntityID":"entity-source","relation":"contains","toMediaID":"` + string(visible.ID) + `","sortOrder":2}}`
	if got := graphPost(t, handler, authorizeViewer, mutation); !bytes.Contains(got, []byte("manager role required")) {
		t.Fatalf("viewer write = %s", got)
	}
	body := graphPost(t, handler, authorizeManager, mutation)
	var resp struct {
		Data struct {
			UpsertEntityRelation RelationWriteResult `json:"upsertEntityRelation"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(resp.Errors) != 0 || resp.Data.UpsertEntityRelation.TargetID != string(visible.ID) {
		t.Fatalf("upsert visible = %#v errors=%#v", resp.Data.UpsertEntityRelation, resp.Errors)
	}

	linkMutation := `{"query":"mutation($fromMediaID:String!,$relation:String!,$toMediaID:String!){ linkMediaRelation(fromMediaID:$fromMediaID, relation:$relation, toMediaID:$toMediaID){ relation sourceID targetID } }","variables":{"fromMediaID":"` + string(visible.ID) + `","relation":"extra-of","toMediaID":"media-alpha"}}`
	var linkResp struct {
		Data struct {
			LinkMediaRelation RelationWriteResult `json:"linkMediaRelation"`
		} `json:"data"`
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(graphPost(t, handler, authorizeManager, linkMutation), &linkResp); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	if len(linkResp.Errors) != 0 || linkResp.Data.LinkMediaRelation.SourceID != string(visible.ID) || linkResp.Data.LinkMediaRelation.TargetID != "media-alpha" {
		t.Fatalf("linkMediaRelation = %#v errors=%#v", linkResp.Data.LinkMediaRelation, linkResp.Errors)
	}

	hiddenMutation := `{"query":"mutation($fromEntityID:String!,$relation:String!,$toMediaID:String!){ upsertEntityRelation(fromEntityID:$fromEntityID, relation:$relation, toMediaID:$toMediaID){ targetID } }","variables":{"fromEntityID":"entity-source","relation":"contains","toMediaID":"` + string(hidden.ID) + `"}}`
	if got := graphPost(t, handler, authorizeManager, hiddenMutation); !bytes.Contains(got, []byte("unknown target media")) {
		t.Fatalf("hidden target write = %s", got)
	}
}

// TestUpsertEntityRelationKeepsAttributes: the mutation re-orders a row that
// ingest wrote with attributes; it has no attributes argument, so it must keep
// them rather than clear them (CORE-2913).
func TestUpsertEntityRelationKeepsAttributes(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)

	member, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/member.mp4",
		Title:              "Member",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-member",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []store.EntityID{"entity-group", "entity-child"} {
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: "library-local", Kind: "container", Title: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	zero := 0
	for _, rel := range []store.EntityRelation{
		{FromEntityID: "entity-group", Name: "contains", ToMediaID: member.ID, Attributes: map[string]string{"role": "lead"}, SortOrder: &zero},
		{FromEntityID: "entity-group", Name: "contains", ToEntityID: "entity-child", Attributes: map[string]string{"role": "guest"}, SortOrder: &zero},
	} {
		if err := st.UpsertEntityRelation(ctx, rel); err != nil {
			t.Fatal(err)
		}
	}

	for _, target := range []string{`"toMediaID":"` + string(member.ID) + `"`, `"toEntityID":"entity-child"`} {
		body := graphPost(t, handler, authorizeManager, `{"query":"mutation($fromEntityID:String!,$relation:String!,$toEntityID:String,$toMediaID:String,$sortOrder:Int){ upsertEntityRelation(fromEntityID:$fromEntityID, relation:$relation, toEntityID:$toEntityID, toMediaID:$toMediaID, sortOrder:$sortOrder){ targetID } }","variables":{"fromEntityID":"entity-group","relation":"contains",`+target+`,"sortOrder":5}}`)
		if bytes.Contains(body, []byte(`"errors"`)) {
			t.Fatalf("upsert %s = %s", target, body)
		}
	}

	rows, err := st.EntityRelationRows(ctx, "entity-group", "contains", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	for _, row := range rows {
		want := "lead"
		if row.ToEntity != nil {
			want = "guest"
		}
		if row.Attributes["role"] != want || row.SortOrder == nil || *row.SortOrder != 5 {
			t.Fatalf("row %#v: attributes=%v sortOrder=%v, want role=%s sortOrder=5", row, row.Attributes, row.SortOrder, want)
		}
	}
}

// TestUpsertEntityRelationPositionPlacesAndRenumbers covers the position
// argument (CORE-2913): it moves a row among the relation's rows in one write,
// renumbers the rest and keeps their attributes, and refuses a request that
// also gives sortOrder.
func TestUpsertEntityRelationPositionPlacesAndRenumbers(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)
	for _, id := range []store.EntityID{"entity-group", "entity-a", "entity-b", "entity-c"} {
		if err := st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: "library-local", Kind: "container", Title: string(id)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []store.EntityRelation{
		{FromEntityID: "entity-group", Name: "contains", ToEntityID: "entity-a", Attributes: map[string]string{"role": "lead"}},
		{FromEntityID: "entity-group", Name: "contains", ToEntityID: "entity-b"},
		{FromEntityID: "entity-group", Name: "contains", ToEntityID: "entity-c"},
	} {
		if err := st.UpsertEntityRelation(ctx, rel); err != nil {
			t.Fatal(err)
		}
	}
	const mutation = `{"query":"mutation($fromEntityID:String!,$relation:String!,$toEntityID:String,$position:Int,$sortOrder:Int){ upsertEntityRelation(fromEntityID:$fromEntityID, relation:$relation, toEntityID:$toEntityID, position:$position, sortOrder:$sortOrder){ targetID } }","variables":{"fromEntityID":"entity-group","relation":"contains","toEntityID":"entity-c",`
	if body := graphPost(t, handler, authorizeManager, mutation+`"position":0}}`); bytes.Contains(body, []byte(`"errors"`)) {
		t.Fatalf("place = %s", body)
	}
	rows, err := st.EntityRelationRows(ctx, "entity-group", "contains", store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for i, row := range rows {
		if row.SortOrder == nil || *row.SortOrder != i {
			t.Fatalf("row %d sortOrder = %v, want %d", i, row.SortOrder, i)
		}
		got = append(got, string(row.ToEntity.ID))
	}
	if strings.Join(got, ",") != "entity-c,entity-a,entity-b" {
		t.Fatalf("order = %v", got)
	}
	if rows[1].Attributes["role"] != "lead" {
		t.Fatalf("entity-a attributes = %v, want role=lead kept", rows[1].Attributes)
	}
	if body := graphPost(t, handler, authorizeManager, mutation+`"position":0,"sortOrder":1}}`); !bytes.Contains(body, []byte("not both")) {
		t.Fatalf("position with sortOrder = %s, want refusal", body)
	}
}

// TestMediaRelationsCarryCardFields covers the detail page's Extras row read
// (CORE-2439): each related Media carries its artwork, duration and class, the
// same values media(id) returns, so a client can draw a card.
func TestMediaRelationsCarryCardFields(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)

	importMedia := func(path, title, fingerprint string, duration int) store.Media {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: path, Title: title,
			Container: "mp4", Codec: "h264", ContentFingerprint: fingerprint,
			Class: store.MediaClassVideo, DurationSecondsHint: duration,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media
	}
	parent := importMedia("/media/parent.mp4", "Parent", "fp-parent", 0)
	extra := importMedia("/media/extra.mp4", "Extra", "fp-extra", 95)
	if err := st.LinkMediaRelation(ctx, extra.ID, store.MediaRelationExtraOf, parent.ID); err != nil {
		t.Fatal(err)
	}
	handler.SetArtwork(fakeArtworkLocator{media: map[store.MediaID]map[string]bool{
		extra.ID: {artwork.SlotPoster: true},
	}})

	got := graphData[[]MediaRelationInfo](t, handler,
		`query($mediaID:String!){ mediaRelations(mediaID:$mediaID, relation:"extra-of"){ relation media { id title artworkSlots durationSeconds mediaClass } } }`,
		map[string]any{"mediaID": string(parent.ID)}, "mediaRelations")
	if len(got) != 1 {
		t.Fatalf("mediaRelations = %#v", got)
	}
	media := got[0].Media
	if media.ID != string(extra.ID) || media.MediaClass != string(store.MediaClassVideo) || media.DurationSeconds != 95 {
		t.Fatalf("media = %#v, want class %q duration 95 (stored %d)", media, store.MediaClassVideo, extra.DurationSeconds)
	}
	if poster := media.ArtworkSlots[artwork.SlotPoster]; poster != "/api/media/artwork/"+string(extra.ID)+"/poster?v="+fakeArtworkVersion {
		t.Fatalf("artworkSlots = %#v", media.ArtworkSlots)
	}
}

// TestMediaRelationsFlagLockedCeilingItems: under a locked certification
// ceiling, a related item above the ceiling stays listed but carries locked,
// so a client's play queue can step over it instead of stopping on a refusal.
func TestMediaRelationsFlagLockedCeilingItems(t *testing.T) {
	st := storetest.Open(t)
	handler := certTestHandler(t, st)
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Entries", false, "starlog", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]store.MediaID{}
	for _, cert := range []string{"parent", "low", "high"} {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: library.ID, Path: "/entries/" + cert + ".mp4", Title: "Entry " + cert,
			Container: "mp4", Codec: "h264", ContentFingerprint: "fp-" + cert,
		})
		if err != nil {
			t.Fatal(err)
		}
		rating := cert
		if cert == "parent" {
			rating = "low"
		}
		if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{
			{Name: "certification", Type: store.EntityFieldText, Text: rating},
		}); err != nil {
			t.Fatal(err)
		}
		ids[cert] = media.ID
	}
	for _, cert := range []string{"low", "high"} {
		if err := st.LinkMediaRelation(ctx, ids[cert], store.MediaRelationExtraOf, ids["parent"]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.SetProfileCertificationCeiling(ctx, "profile-default", store.ProfileCertificationCeiling{
		PluginID: "starlog", Ceiling: "mid", FilterVisibility: "locked",
	}); err != nil {
		t.Fatal(err)
	}

	got := graphData[[]MediaRelationInfo](t, handler,
		`query($mediaID:String!){ mediaRelations(mediaID:$mediaID, relation:"extra-of"){ relation locked media { id } } }`,
		map[string]any{"mediaID": string(ids["parent"])}, "mediaRelations")
	locked := map[string]bool{}
	for _, row := range got {
		locked[row.Media.ID] = row.Locked
	}
	if len(got) != 2 || locked[string(ids["low"])] || !locked[string(ids["high"])] {
		t.Fatalf("mediaRelations = %#v, want low unlocked and high locked", got)
	}
}
