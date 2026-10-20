package graph

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Saved Filters and Smart Collections (ADR-0011 §10, feature #59): per-Profile
// named filters and Library-level smart collections with persisted source
// expressions. SavedFilter CRUD is profile-scoped (any session); SmartCollection
// create/delete requires the manager role.

// ── SavedFilter ─────────────────────────────────────────────────────────────

// TestCreateSavedFilterRoundTrip verifies that createSavedFilter stores a named
// filter and savedFilters returns it back for the owning profile.
func TestCreateSavedFilterRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	create := `{"query":"mutation { createSavedFilter { id name filterJSON } }","variables":{"libraryID":"library-local","name":"Recent Drama","filterJSON":"{}"}}`
	data, errs := post(t, handler, create, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createSavedFilter errors = %#v", errs)
	}
	var result struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		FilterJSON string `json:"filterJSON"`
	}
	if err := json.Unmarshal(data["createSavedFilter"], &result); err != nil {
		t.Fatalf("decode createSavedFilter: %v", err)
	}
	if result.Name != "Recent Drama" {
		t.Errorf("name = %q, want %q", result.Name, "Recent Drama")
	}
	if !strings.HasPrefix(result.ID, "filter-") {
		t.Errorf("id = %q, want filter-… prefix", result.ID)
	}
}

// TestDeleteSavedFilterRoundTrip verifies that deleteSavedFilter removes the
// filter and subsequent savedFilters does not return it.
func TestDeleteSavedFilterRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	// Create a filter to delete.
	createBody := `{"query":"mutation { createSavedFilter { id } }","variables":{"libraryID":"library-local","name":"To Delete","filterJSON":"{}"}}`
	data, errs := post(t, handler, createBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createSavedFilter errors = %#v", errs)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createSavedFilter"], &created); err != nil {
		t.Fatalf("decode createSavedFilter: %v", err)
	}

	deleteBody := `{"query":"mutation V($id:String!){ deleteSavedFilter(id:$id){ id name } }","variables":{"id":"` + string(created.ID) + `"}}`
	data, errs = post(t, handler, deleteBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("deleteSavedFilter errors = %#v", errs)
	}
	var removed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["deleteSavedFilter"], &removed); err != nil {
		t.Fatalf("decode deleteSavedFilter: %v", err)
	}
	if removed.ID != created.ID {
		t.Errorf("deleteSavedFilter id = %q, want %q", removed.ID, created.ID)
	}

	// Re-delete must fail gracefully.
	_, errs = post(t, handler, deleteBody, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown saved filter") {
		t.Errorf("re-delete errors = %#v, want unknown saved filter", errs)
	}
}

// ── SmartCollection ──────────────────────────────────────────────────────────

// TestCreateSmartCollectionRoundTrip verifies that createSmartCollection (manager-
// gated) stores a named collection with its source expression.
func TestCreateSmartCollectionRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	create := `{"query":"mutation { createSmartCollection { id name sourceExpr } }","variables":{"libraryID":"library-local","name":"New Releases","sourceExpr":"{}"}}`
	data, errs := post(t, handler, create, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createSmartCollection errors = %#v", errs)
	}
	var result struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		SourceExpr string `json:"sourceExpr"`
	}
	if err := json.Unmarshal(data["createSmartCollection"], &result); err != nil {
		t.Fatalf("decode createSmartCollection: %v", err)
	}
	if result.Name != "New Releases" {
		t.Errorf("name = %q, want %q", result.Name, "New Releases")
	}
	if !strings.HasPrefix(result.ID, "collection-") {
		t.Errorf("id = %q, want collection-… prefix", result.ID)
	}
}

// TestDeleteSmartCollectionRoundTrip verifies that deleteSmartCollection removes
// the collection and returns the deleted row.
func TestDeleteSmartCollectionRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	// Create a collection to delete.
	createBody := `{"query":"mutation { createSmartCollection { id } }","variables":{"libraryID":"library-local","name":"Temp","sourceExpr":"{}"}}`
	data, errs := post(t, handler, createBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createSmartCollection errors = %#v", errs)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createSmartCollection"], &created); err != nil {
		t.Fatalf("decode createSmartCollection: %v", err)
	}

	deleteBody := `{"query":"mutation V($id:String!){ deleteSmartCollection(id:$id){ id name } }","variables":{"id":"` + string(created.ID) + `"}}`
	data, errs = post(t, handler, deleteBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("deleteSmartCollection errors = %#v", errs)
	}
	var removed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["deleteSmartCollection"], &removed); err != nil {
		t.Fatalf("decode deleteSmartCollection: %v", err)
	}
	if removed.ID != created.ID {
		t.Errorf("deleteSmartCollection id = %q, want %q", removed.ID, created.ID)
	}

	// Re-delete must fail gracefully.
	_, errs = post(t, handler, deleteBody, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown smart collection") {
		t.Errorf("re-delete errors = %#v, want unknown smart collection", errs)
	}
}

func TestUpdateSmartCollectionRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	createBody := `{"query":"mutation { createSmartCollection { id } }","variables":{"libraryID":"library-local","name":"Temp","sourceExpr":"{}","sortExpr":"{}"}}`
	data, errs := post(t, handler, createBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createSmartCollection errors = %#v", errs)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createSmartCollection"], &created); err != nil {
		t.Fatalf("decode createSmartCollection: %v", err)
	}

	updateBody := `{"query":"mutation U($id:String!,$name:String!,$sourceExpr:String!,$sortExpr:String!){ updateSmartCollection(id:$id,name:$name,sourceExpr:$sourceExpr,sortExpr:$sortExpr){ id name sourceExpr sortExpr } }","variables":{"id":"` + created.ID + `","name":"Updated","sourceExpr":"{\"kind\":\"recently-added\"}","sortExpr":"{\"field\":\"added\",\"direction\":\"desc\"}"}}`
	data, errs = post(t, handler, updateBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("updateSmartCollection errors = %#v", errs)
	}
	var updated struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		SourceExpr string `json:"sourceExpr"`
		SortExpr   string `json:"sortExpr"`
	}
	if err := json.Unmarshal(data["updateSmartCollection"], &updated); err != nil {
		t.Fatalf("decode updateSmartCollection: %v", err)
	}
	if updated.Name != "Updated" || updated.SourceExpr != `{"kind":"recently-added"}` || updated.SortExpr != `{"field":"added","direction":"desc"}` {
		t.Fatalf("updated smart collection = %#v", updated)
	}
}

func TestCollectionSubcollectionMembershipRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	createParent := `{"query":"mutation { createCollection { id name memberCount } }","variables":{"libraryID":"library-local","name":"Favorites"}}`
	data, errs := post(t, handler, createParent, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createCollection parent errors = %#v", errs)
	}
	var parent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createCollection"], &parent); err != nil {
		t.Fatalf("decode parent: %v", err)
	}

	createChild := `{"query":"mutation C($parentID:String!){ createCollection(parentID:$parentID){ id parentID } }","variables":{"libraryID":"library-local","name":"Weekend","parentID":"` + parent.ID + `"}}`
	data, errs = post(t, handler, createChild, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createCollection child errors = %#v", errs)
	}
	var child struct {
		ID       string `json:"id"`
		ParentID string `json:"parentID"`
	}
	if err := json.Unmarshal(data["createCollection"], &child); err != nil {
		t.Fatalf("decode child: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Fatalf("child parentID = %q, want %q", child.ParentID, parent.ID)
	}

	addMedia := `{"query":"mutation A($id:String!,$mediaIDs:[String!]!){ addCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ id memberCount members { mediaID sortOrder } } }","variables":{"id":"` + parent.ID + `","mediaIDs":["media-alpha"]}}`
	data, errs = post(t, handler, addMedia, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("addCollectionMedia errors = %#v", errs)
	}
	var withMedia struct {
		MemberCount int `json:"memberCount"`
		Members     []struct {
			MediaID   string `json:"mediaID"`
			SortOrder int    `json:"sortOrder"`
		} `json:"members"`
	}
	if err := json.Unmarshal(data["addCollectionMedia"], &withMedia); err != nil {
		t.Fatalf("decode addCollectionMedia: %v", err)
	}
	if withMedia.MemberCount != 1 || len(withMedia.Members) != 1 || withMedia.Members[0].MediaID != "media-alpha" {
		t.Fatalf("addCollectionMedia = %#v", withMedia)
	}
}

// TestCollectionMembershipRevealFiltering verifies ADR-0098 area 4 at the graph
// surface: a static Collection in a restricted Library reports its members and
// memberCount only to a revealed session; an unrevealed session sees an empty,
// zero-count membership (no leak through counts, gaps, or ordering).
func TestCollectionMembershipRevealFiltering(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(t.Context(),
		store.Device{ID: "device-mgr2", Name: "Manager 2", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr2-secret")},
		store.Session{ID: "session-mgr-revealed", DeviceID: "device-mgr2", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	authorizeRevealedManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-revealed")
		req.Header.Set("X-Beamfall-Device", "device-mgr2")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr2-secret")
	}
	handler := newTestHandlerWithStore(st)

	// Create a Collection in the restricted Archive Library and add the
	// restricted member — both require a revealed session.
	createBody := `{"query":"mutation { createCollection { id } }","variables":{"libraryID":"library-archive","name":"Sealed Set"}}`
	data, errs := post(t, handler, createBody, authorizeRevealedManager)
	if len(errs) != 0 {
		t.Fatalf("createCollection errors = %#v", errs)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createCollection"], &created); err != nil {
		t.Fatalf("decode createCollection: %v", err)
	}
	addBody := `{"query":"mutation A($id:String!,$mediaIDs:[String!]!){ addCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ memberCount members { mediaID } } }","variables":{"id":"` + created.ID + `","mediaIDs":["media-archive"]}}`
	data, errs = post(t, handler, addBody, authorizeRevealedManager)
	if len(errs) != 0 {
		t.Fatalf("addCollectionMedia errors = %#v", errs)
	}
	var added struct {
		MemberCount int `json:"memberCount"`
		Members     []struct {
			MediaID string `json:"mediaID"`
		} `json:"members"`
	}
	if err := json.Unmarshal(data["addCollectionMedia"], &added); err != nil {
		t.Fatalf("decode addCollectionMedia: %v", err)
	}
	if added.MemberCount != 1 || len(added.Members) != 1 || added.Members[0].MediaID != "media-archive" {
		t.Fatalf("revealed addCollectionMedia = %#v, want 1 member media-archive", added)
	}

	// Revealed read of the Archive Library's Collections shows the member.
	listBody := `{"query":"query { collections { id memberCount members { mediaID } } }","variables":{"libraryID":"library-archive","includeTrashed":true}}`
	data, errs = post(t, handler, listBody, authorizeRevealedManager)
	if len(errs) != 0 {
		t.Fatalf("revealed collections errors = %#v", errs)
	}
	var revealedList []struct {
		ID          string `json:"id"`
		MemberCount int    `json:"memberCount"`
		Members     []struct {
			MediaID string `json:"mediaID"`
		} `json:"members"`
	}
	if err := json.Unmarshal(data["collections"], &revealedList); err != nil {
		t.Fatalf("decode revealed collections: %v", err)
	}
	var revealedCol *struct {
		ID          string `json:"id"`
		MemberCount int    `json:"memberCount"`
		Members     []struct {
			MediaID string `json:"mediaID"`
		} `json:"members"`
	}
	for i := range revealedList {
		if revealedList[i].ID == created.ID {
			revealedCol = &revealedList[i]
		}
	}
	if revealedCol == nil {
		t.Fatalf("revealed collections omitted %s: %#v", created.ID, revealedList)
	}
	if revealedCol.MemberCount != 1 || len(revealedCol.Members) != 1 || revealedCol.Members[0].MediaID != "media-archive" {
		t.Fatalf("revealed collection = %#v, want 1 member media-archive", *revealedCol)
	}

	// Unrevealed manager cannot see the restricted Library at all — the
	// collections query is reveal-gated upstream, so it returns an error rather
	// than a leaking membership. This confirms no count/member leaks to an
	// unrevealed session.
	_, unrevealedErrs := post(t, handler, listBody, authorizeManager)
	if len(unrevealedErrs) == 0 {
		t.Fatal("unrevealed collections on restricted library should be reveal-gated, got success")
	}
}

func TestCollectionMediaMutationsRejectHiddenCollectionBeforeWrite(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	if err := st.SetAdminPassword(ctx, "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-mgr-revealed", Name: "Manager Revealed", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr-revealed-secret")},
		store.Session{ID: "session-mgr-revealed", DeviceID: "device-mgr-revealed", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	authorizeRevealedManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-revealed")
		req.Header.Set("X-Beamfall-Device", "device-mgr-revealed")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr-revealed-secret")
	}
	handler := newTestHandlerWithStore(st)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	revealed := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-archive"}, Revealed: true}

	extra, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-archive",
		Path:               "/archive/extra-hidden.mp4",
		Title:              "Extra Hidden",
		Container:          "mp4",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: "fingerprint-extra-hidden",
	})
	if err != nil {
		t.Fatalf("seed extra archive media: %v", err)
	}
	col, err := st.CreateCollection(ctx, store.Collection{LibraryID: "library-archive", Name: "Hidden Set"}, now)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, col.ID, []store.MediaID{"media-archive", extra.ID}, revealed, now); err != nil {
		t.Fatalf("seed collection media: %v", err)
	}

	assertMembers := func(label string) {
		t.Helper()
		members, err := st.ResolveCollectionMembers(ctx, col.ID, revealed)
		if err != nil {
			t.Fatalf("%s ResolveCollectionMembers: %v", label, err)
		}
		if len(members) != 2 || members[0].MediaID != "media-archive" || members[1].MediaID != extra.ID {
			t.Fatalf("%s members = %#v, want unchanged [media-archive %s]", label, members, extra.ID)
		}
	}

	attempts := []struct {
		name string
		body string
	}{
		{
			name: "add",
			body: `{"query":"mutation A($id:String!,$mediaIDs:[String!]!){ addCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ id } }","variables":{"id":"` + string(col.ID) + `","mediaIDs":["media-alpha"]}}`,
		},
		{
			name: "remove",
			body: `{"query":"mutation RM($id:String!,$mediaIDs:[String!]!){ removeCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ id } }","variables":{"id":"` + string(col.ID) + `","mediaIDs":["media-archive"]}}`,
		},
		{
			name: "reorder",
			body: `{"query":"mutation R($id:String!,$mediaIDs:[String!]!){ reorderCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ id } }","variables":{"id":"` + string(col.ID) + `","mediaIDs":["` + string(extra.ID) + `","media-archive"]}}`,
		},
	}
	for _, attempt := range attempts {
		_, errs := post(t, handler, attempt.body, authorizeManager)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown collection") {
			t.Fatalf("%s hidden collection errors = %#v, want unknown collection", attempt.name, errs)
		}
		assertMembers(attempt.name)
	}

	data, errs := post(t, handler, `{"query":"query { collections { id } }","variables":{"libraryID":"library-archive"}}`, authorizeRevealedManager)
	if len(errs) != 0 || data["collections"] == nil {
		t.Fatalf("revealed collection read after rejected mutations data=%#v errors=%#v", data, errs)
	}
}

func TestUpdateReorderRemoveCollectionRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	createBody := `{"query":"mutation { createCollection { id } }","variables":{"libraryID":"library-local","name":"Temp"}}`
	data, errs := post(t, handler, createBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createCollection errors = %#v", errs)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createCollection"], &created); err != nil {
		t.Fatalf("decode createCollection: %v", err)
	}

	updateBody := `{"query":"mutation U($id:String!,$name:String!,$sortOrder:Int!){ updateCollection(id:$id,name:$name,sortOrder:$sortOrder){ id name sortOrder } }","variables":{"id":"` + created.ID + `","name":"Renamed","sortOrder":7}}`
	data, errs = post(t, handler, updateBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("updateCollection errors = %#v", errs)
	}
	var updated struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sortOrder"`
	}
	if err := json.Unmarshal(data["updateCollection"], &updated); err != nil {
		t.Fatalf("decode updateCollection: %v", err)
	}
	if updated.Name != "Renamed" || updated.SortOrder != 7 {
		t.Fatalf("updateCollection = %#v", updated)
	}

	extra, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/library/extra.mp4",
		Title:              "Extra",
		Container:          "mp4",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: "fingerprint-extra",
	})
	if err != nil {
		t.Fatalf("seed extra media: %v", err)
	}
	extraID := string(extra.ID)
	addBody := `{"query":"mutation A($id:String!,$mediaIDs:[String!]!){ addCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ id memberCount } }","variables":{"id":"` + created.ID + `","mediaIDs":["media-alpha","` + extraID + `"]}}`
	if _, errs = post(t, handler, addBody, authorizeManager); len(errs) != 0 {
		t.Fatalf("addCollectionMedia errors = %#v", errs)
	}

	reorderBody := `{"query":"mutation R($id:String!,$mediaIDs:[String!]!){ reorderCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ members { mediaID sortOrder } } }","variables":{"id":"` + created.ID + `","mediaIDs":["` + extraID + `","media-alpha"]}}`
	data, errs = post(t, handler, reorderBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("reorderCollectionMedia errors = %#v", errs)
	}
	var reordered struct {
		Members []struct {
			MediaID   string `json:"mediaID"`
			SortOrder int    `json:"sortOrder"`
		} `json:"members"`
	}
	if err := json.Unmarshal(data["reorderCollectionMedia"], &reordered); err != nil {
		t.Fatalf("decode reorderCollectionMedia: %v", err)
	}
	if len(reordered.Members) != 2 || reordered.Members[0].MediaID != extraID || reordered.Members[0].SortOrder != 1 {
		t.Fatalf("reorderCollectionMedia = %#v", reordered)
	}

	removeBody := `{"query":"mutation RM($id:String!,$mediaIDs:[String!]!){ removeCollectionMedia(collectionID:$id,mediaIDs:$mediaIDs){ memberCount members { mediaID } } }","variables":{"id":"` + created.ID + `","mediaIDs":["media-alpha"]}}`
	data, errs = post(t, handler, removeBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("removeCollectionMedia errors = %#v", errs)
	}
	var removed struct {
		MemberCount int `json:"memberCount"`
		Members     []struct {
			MediaID string `json:"mediaID"`
		} `json:"members"`
	}
	if err := json.Unmarshal(data["removeCollectionMedia"], &removed); err != nil {
		t.Fatalf("decode removeCollectionMedia: %v", err)
	}
	if removed.MemberCount != 1 || len(removed.Members) != 1 || removed.Members[0].MediaID != extraID {
		t.Fatalf("removeCollectionMedia = %#v", removed)
	}
}

func TestCollectionDeleteRestoreRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	createBody := `{"query":"mutation { createCollection { id } }","variables":{"libraryID":"library-local","name":"Temp"}}`
	data, errs := post(t, handler, createBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createCollection errors = %#v", errs)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["createCollection"], &created); err != nil {
		t.Fatalf("decode createCollection: %v", err)
	}

	deleteBody := `{"query":"mutation D($id:String!){ deleteCollection(id:$id){ id trashedAt } }","variables":{"id":"` + created.ID + `"}}`
	data, errs = post(t, handler, deleteBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("deleteCollection errors = %#v", errs)
	}
	var deleted struct {
		TrashedAt string `json:"trashedAt"`
	}
	if err := json.Unmarshal(data["deleteCollection"], &deleted); err != nil {
		t.Fatalf("decode deleteCollection: %v", err)
	}
	if deleted.TrashedAt == "" {
		t.Fatal("deleteCollection returned empty trashedAt")
	}

	restoreBody := `{"query":"mutation R($id:String!){ restoreCollection(id:$id){ id trashedAt } }","variables":{"id":"` + created.ID + `"}}`
	data, errs = post(t, handler, restoreBody, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("restoreCollection errors = %#v", errs)
	}
	var restored struct {
		TrashedAt string `json:"trashedAt"`
	}
	if err := json.Unmarshal(data["restoreCollection"], &restored); err != nil {
		t.Fatalf("decode restoreCollection: %v", err)
	}
	if restored.TrashedAt != "" {
		t.Fatalf("restoreCollection trashedAt = %q, want empty", restored.TrashedAt)
	}
}
