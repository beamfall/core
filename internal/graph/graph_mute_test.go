package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Per-Profile Mute / "Not interested", graph acceptance. These
// wire tests boot the real seeded store + HTTP handler and exercise set/clear/
// list + the My-Data integration over GraphQL. Every field is own-Profile-only
// by construction (scope.ProfileID, never a variable), so the cross-Profile
// test below leans on the seeded two-Profile session model:
//
//   - session-default / authorizeViewer  → profile-default (adult)
//   - session-managed / authorizeManaged  → profile-managed  (a DIFFERENT Profile)
//
// A Mute set as one Profile must be invisible to — and untouched by — the other.

// muteList returns the caller's own mute list via the `mutes` query.
func muteList(t *testing.T, handler *Handler, authorize func(*http.Request)) []MyMuteInfo {
	t.Helper()
	data, errs := post(t, handler,
		`{"query":"query { mutes { kind targetID includeDescendants mutedAt } }"}`,
		authorize)
	if len(errs) != 0 {
		t.Fatalf("mutes errors = %#v", errs)
	}
	var list []MyMuteInfo
	mustUnmarshal(t, data["mutes"], &list)
	return list
}

// setMuteWire sets a Mute on (kind,targetID,includeDescendants) for the
// authorizing Profile, asserting no errors, and returns the echoed result.
func setMuteWire(t *testing.T, handler *Handler, authorize func(*http.Request), kind, targetID string, includeDescendants bool) MuteResult {
	t.Helper()
	body := `{"query":"mutation { setMute(kind: \"` + kind + `\", targetID: \"` + targetID + `\", includeDescendants: ` + boolLit(includeDescendants) + `) { kind targetID includeDescendants muted } }",` +
		`"variables":{"kind":"` + kind + `","targetID":"` + targetID + `","includeDescendants":` + boolLit(includeDescendants) + `}}`
	data, errs := post(t, handler, body, authorize)
	if len(errs) != 0 {
		t.Fatalf("setMute(%s,%s) errors = %#v", kind, targetID, errs)
	}
	var result MuteResult
	mustUnmarshal(t, data["setMute"], &result)
	return result
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestSetClearListMutesForActingProfile is the ENR-4 acceptance for (a) set,
// (c) list, and (b-self) clear on the acting Profile: a Profile
// sets two mutes (one Entity, one descendants-Tag), lists both back newest-first,
// clears one, and the list shrinks to the survivor.
func TestSetClearListMutesForActingProfile(t *testing.T) {
	// feature:per-profile-mute
	handler := newTestHandler(t)

	if got := muteList(t, handler, authorizeViewer); len(got) != 0 {
		t.Fatalf("initial mutes = %#v, want empty", got)
	}

	r1 := setMuteWire(t, handler, authorizeViewer, "entity", "entity-demo-comedy", false)
	if r1.Kind != "entity" || r1.TargetID != "entity-demo-comedy" || r1.IncludeDescendants || !r1.Muted {
		t.Fatalf("setMute entity result = %+v", r1)
	}
	r2 := setMuteWire(t, handler, authorizeViewer, "tag", "tag-romance", true)
	if r2.Kind != "tag" || r2.TargetID != "tag-romance" || !r2.IncludeDescendants || !r2.Muted {
		t.Fatalf("setMute tag result = %+v", r2)
	}

	list := muteList(t, handler, authorizeViewer)
	if len(list) != 2 {
		t.Fatalf("after two setMutes, list = %#v, want 2", list)
	}
	// Ordering is store-deterministic: ORDER BY muted_at_ms DESC, target_kind,
	// target_id. Both mutes set in the same test millisecond tie on muted_at_ms,
	// so the kind tiebreak ("entity" < "tag") puts the entity first. Assert the
	// fields by target rather than position so the test pins behavior, not a
	// timing artifact.
	byID := map[string]MyMuteInfo{}
	for _, m := range list {
		byID[m.TargetID] = m
	}
	if e := byID["entity-demo-comedy"]; e.Kind != "entity" || e.IncludeDescendants {
		t.Errorf("entity-demo-comedy entry = %+v, want entity/no-descendants", e)
	}
	if tg := byID["tag-romance"]; tg.Kind != "tag" || !tg.IncludeDescendants {
		t.Errorf("tag-romance entry = %+v, want tag/includeDescendants", tg)
	}

	// (b-self) clear one target; only it disappears.
	clearBody := `{"query":"mutation { clearMute(kind: \"entity\", targetID: \"entity-demo-comedy\") { targetID muted } }","variables":{"kind":"entity","targetID":"entity-demo-comedy"}}`
	data, errs := post(t, handler, clearBody, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("clearMute errors = %#v", errs)
	}
	var cleared MuteResult
	mustUnmarshal(t, data["clearMute"], &cleared)
	if cleared.TargetID != "entity-demo-comedy" || cleared.Muted {
		t.Fatalf("clearMute result = %+v, want entity-demo-comedy/muted=false", cleared)
	}

	list = muteList(t, handler, authorizeViewer)
	if len(list) != 1 || list[0].TargetID != "tag-romance" {
		t.Fatalf("after clear, list = %#v, want only tag-romance", list)
	}
}

// TestMutesAreProfileScoped is the ENR-4 self-authored guarantee (ADR-0054
// §2.4, HARD CONSTRAINT (b)): a Profile can only set/clear/list its OWN mutes,
// never another Profile's. profile-default mutes a target; profile-managed (a
// different Profile) sees an EMPTY list, can mute its own target without
// touching profile-default's, and a clearAllMutes on one Profile leaves the
// other's intact. The isolation is structural — no resolver accepts a foreign
// profile id — so this test demonstrates the two Profiles never observe or
// mutate each other's mutes.
func TestMutesAreProfileScoped(t *testing.T) {
	// feature:per-profile-mute
	handler := newTestHandler(t)

	setMuteWire(t, handler, authorizeViewer, "entity", "entity-demo-comedy", false)

	// The OTHER Profile (profile-managed) sees nothing of profile-default's mute.
	if got := muteList(t, handler, authorizeManaged); len(got) != 0 {
		t.Fatalf("managed Profile sees profile-default's mutes = %#v, want empty", got)
	}

	// profile-managed mutes its own target; profile-default is unaffected.
	setMuteWire(t, handler, authorizeManaged, "entity", "entity-demo-science", false)
	if got := muteList(t, handler, authorizeManaged); len(got) != 1 || got[0].TargetID != "entity-demo-science" {
		t.Fatalf("managed Profile own list = %#v, want only entity-demo-science", got)
	}
	if got := muteList(t, handler, authorizeViewer); len(got) != 1 || got[0].TargetID != "entity-demo-comedy" {
		t.Fatalf("default Profile list = %#v, want only entity-demo-comedy (unchanged)", got)
	}

	// clearAllMutes on profile-managed clears ONLY its own; profile-default keeps its mute.
	data, errs := post(t, handler,
		`{"query":"mutation { clearAllMutes { cleared } }"}`, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("clearAllMutes(managed) errors = %#v", errs)
	}
	var cleared ClearAllMutesResult
	mustUnmarshal(t, data["clearAllMutes"], &cleared)
	if cleared.Cleared != 1 {
		t.Errorf("clearAllMutes cleared = %d, want 1", cleared.Cleared)
	}
	if got := muteList(t, handler, authorizeManaged); len(got) != 0 {
		t.Fatalf("managed list after clearAll = %#v, want empty", got)
	}
	if got := muteList(t, handler, authorizeViewer); len(got) != 1 || got[0].TargetID != "entity-demo-comedy" {
		t.Fatalf("default list after managed clearAll = %#v, want still entity-demo-comedy", got)
	}
}

// TestMyDataExportIncludesMutesAndClearAll is the ENR-4 My-Data integration
// acceptance: (c) the mute list appears in the Profile's
// My-Data export, and (d) clearAllMutes removes them — the export reflects the
// reset.
func TestMyDataExportIncludesMutesAndClearAll(t *testing.T) {
	// feature:per-profile-mute
	handler := newTestHandler(t)

	setMuteWire(t, handler, authorizeViewer, "entity", "entity-demo-comedy", false)
	setMuteWire(t, handler, authorizeViewer, "tag", "tag-romance", true)

	// (c) the mute list is a My-Data export citizen.
	export := readExportMutes(t, handler, authorizeViewer)
	if len(export.Mutes) != 2 {
		t.Fatalf("export.Mutes = %#v, want 2", export.Mutes)
	}
	exported := map[string]bool{}
	for _, m := range export.Mutes {
		exported[m.TargetID] = true
	}
	if !exported["entity-demo-comedy"] || !exported["tag-romance"] {
		t.Errorf("export.Mutes = %+v, want both entity-demo-comedy and tag-romance", export.Mutes)
	}

	// (d) clear-all removes them; the export reflects the empty list.
	data, errs := post(t, handler,
		`{"query":"mutation { clearAllMutes { cleared } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("clearAllMutes errors = %#v", errs)
	}
	var cleared ClearAllMutesResult
	mustUnmarshal(t, data["clearAllMutes"], &cleared)
	if cleared.Cleared != 2 {
		t.Errorf("clearAllMutes cleared = %d, want 2", cleared.Cleared)
	}
	if export := readExportMutes(t, handler, authorizeViewer); len(export.Mutes) != 0 {
		t.Fatalf("export.Mutes after clearAll = %#v, want empty", export.Mutes)
	}
}

// readExportMutes reads the caller's My-Data export, selecting the mutes block.
func readExportMutes(t *testing.T, handler *Handler, authorize func(*http.Request)) MyEngagementExportInfo {
	t.Helper()
	data, errs := post(t, handler,
		`{"query":"query Q($pin: String){ myEngagement(pin: $pin){ schema mutes { kind targetID includeDescendants mutedAt } } }","variables":{"pin":"1234"}}`,
		authorize)
	if len(errs) != 0 {
		t.Fatalf("myEngagement errors = %#v", errs)
	}
	var export MyEngagementExportInfo
	mustUnmarshal(t, data["myEngagement"], &export)
	return export
}

// TestSearchBadgesMutedItemsOnWire is the NGAUDIT-B8-MUTEBADGE [auto] acceptance:
// a muted entity's Media appears in the `search` result carrying muted:true on
// the wire, while a co-resident unmuted item does not (ADR-0054 §2.2 searchable
// + §2.7 badge). It boots the real seeded store + HTTP handler and asserts the
// JSON `muted` field the tvOS/Android search cards consume.
func TestSearchBadgesMutedItemsOnWire(t *testing.T) {
	// feature:per-profile-mute
	ctx := context.Background()
	st := storetest.Open(t)

	// Two searchable Media in the viewer-visible (non-restricted) library-local.
	mutedMedia, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/mutebadge-hit.mp4",
		Title:              "Mutebadge Hit One",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:mutebadge-hit",
	})
	if err != nil {
		t.Fatalf("import muted media: %v", err)
	}
	keptMedia, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/mutebadge-kept.mp4",
		Title:              "Mutebadge Hit Two",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:mutebadge-kept",
	})
	if err != nil {
		t.Fatalf("import kept media: %v", err)
	}
	// Relate the muted Media to a fresh entity in the same viewer-visible library
	// and mute that entity as the acting Profile (profile-default = authorizeViewer).
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-mutebadge", LibraryID: "library-local", Kind: "category", Title: "Mutebadge Category",
	}); err != nil {
		t.Fatalf("upsert entity: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-mutebadge", Name: "linked", ToMediaID: mutedMedia.ID,
	}); err != nil {
		t.Fatalf("relate: %v", err)
	}
	if err := st.SetMute(ctx, "profile-default", store.MuteKindEntity, "entity-mutebadge", false, true); err != nil {
		t.Fatalf("set mute: %v", err)
	}

	handler := newTestHandlerWithStore(st)
	body := []byte(`{"query":"query Search($query: String!) { search(query: $query) { id muted } }","variables":{"query":"Mutebadge"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Data struct {
			Search []SourceItem `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}

	byID := map[string]bool{}
	for _, it := range resp.Data.Search {
		byID[it.ID] = it.Muted
	}
	muted, sawMuted := byID[string(mutedMedia.ID)]
	kept, sawKept := byID[string(keptMedia.ID)]
	if !sawMuted || !sawKept {
		t.Fatalf("both items must remain searchable, got %#v", resp.Data.Search)
	}
	if !muted {
		t.Errorf("muted entity's Media must carry muted:true, got %#v", resp.Data.Search)
	}
	if kept {
		t.Errorf("unmuted co-resident item must not carry muted:true, got %#v", resp.Data.Search)
	}
}
