package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

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
		{Relation: "extra-of", Media: MediaInfo{ID: string(favoriteExtra.ID), Title: "A Favorite Extra", Favorited: true}},
		{Relation: "extra-of", Media: MediaInfo{ID: string(plainExtra.ID), Title: "B Plain Extra"}},
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
