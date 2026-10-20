package graph

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestChannelRoundTrip exercises the CHAN-1 wire surface: create a
// per-Profile Channel over an inline query, list it back, edit its ordering to a
// smart-collection-reference source, then delete it. The wire entity stays
// generic — a Channel is a structural object, not a domain type.
func TestChannelRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	create := `{"query":"mutation { createChannel { id name ordering repeat sourceExpr } }","variables":{"libraryID":"library-local","name":"Evening Drama","sourceExpr":"{\"kind\":\"all-items\"}","ordering":"chronological"}}`
	data, errs := post(t, handler, create, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createChannel errors = %#v", errs)
	}
	var created struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Ordering   string `json:"ordering"`
		Repeat     string `json:"repeat"`
		SourceExpr string `json:"sourceExpr"`
	}
	if err := json.Unmarshal(data["createChannel"], &created); err != nil {
		t.Fatalf("decode createChannel: %v", err)
	}
	if !strings.HasPrefix(created.ID, "channel-") {
		t.Errorf("id = %q, want channel-… prefix", created.ID)
	}
	if created.Ordering != "chronological" || created.Repeat != "off" {
		t.Errorf("policies = %q/%q, want chronological/off", created.Ordering, created.Repeat)
	}

	list := `{"query":"query { channels { id name ordering } }","variables":{"libraryID":"library-local"}}`
	data, errs = post(t, handler, list, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("channels errors = %#v", errs)
	}
	var listed []struct {
		ID       string `json:"id"`
		Ordering string `json:"ordering"`
	}
	if err := json.Unmarshal(data["channels"], &listed); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("channels = %#v, want [%s]", listed, created.ID)
	}

	update := `{"query":"mutation V($id:String!){ updateChannel(id:$id){ id ordering sourceExpr } }","variables":{"id":"` + created.ID + `","ordering":"shuffle","sourceExpr":"{\"kind\":\"smart-collection\",\"value\":\"collection-xyz\"}"}}`
	data, errs = post(t, handler, update, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("updateChannel errors = %#v", errs)
	}
	var updated struct {
		Ordering   string `json:"ordering"`
		SourceExpr string `json:"sourceExpr"`
	}
	if err := json.Unmarshal(data["updateChannel"], &updated); err != nil {
		t.Fatalf("decode updateChannel: %v", err)
	}
	if updated.Ordering != "shuffle" {
		t.Errorf("ordering = %q, want shuffle", updated.Ordering)
	}
	if !strings.Contains(updated.SourceExpr, "smart-collection") {
		t.Errorf("sourceExpr = %q, want smart-collection ref", updated.SourceExpr)
	}

	del := `{"query":"mutation V($id:String!){ deleteChannel(id:$id){ id } }","variables":{"id":"` + created.ID + `"}}`
	data, errs = post(t, handler, del, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("deleteChannel errors = %#v", errs)
	}
	var removed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data["deleteChannel"], &removed); err != nil {
		t.Fatalf("decode deleteChannel: %v", err)
	}
	if removed.ID != created.ID {
		t.Errorf("deleteChannel id = %q, want %q", removed.ID, created.ID)
	}

	// Re-delete fails gracefully.
	if _, errs = post(t, handler, del, authorizeManager); len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown channel") {
		t.Errorf("re-delete errors = %#v, want unknown channel", errs)
	}
}

// TestChannelEditsAreProfileScoped pins the CHAN-1 ownership boundary: a
// per-Profile Channel must be unreachable for edits from another Profile even
// when that Profile can see the Channel's Library. Library visibility alone is
// not authorization — Profiles sharing a Library must not reach each other's
// private Channels.
//
// The foreign Profile creates its own Channel in the same Library first, which
// proves the Library really is visible to it. Without that step a denial could
// just as easily mean "cannot see the Library", and the test would pass while
// the ownership guard was absent.
func TestChannelEditsAreProfileScoped(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	newChannel := func(name string) string {
		t.Helper()
		q := `{"query":"mutation { createChannel { id } }","variables":{"libraryID":"library-local","name":"` + name + `","sourceExpr":"{\"kind\":\"all-items\"}"}}`
		data, errs := post(t, handler, q, authorizeManager)
		if len(errs) != 0 {
			t.Fatalf("createChannel(%s) errors = %#v", name, errs)
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data["createChannel"], &created); err != nil {
			t.Fatalf("decode createChannel(%s): %v", name, err)
		}
		return created.ID
	}
	useProfile := func(p store.ProfileID) {
		t.Helper()
		if err := st.SetSessionProfile(t.Context(), "session-manager", p); err != nil {
			t.Fatalf("SetSessionProfile(%s): %v", p, err)
		}
	}

	owned := newChannel("Owner Evening")

	useProfile("profile-managed")
	foreign := newChannel("Foreign Evening") // proves library-local is visible here

	update := `{"query":"mutation V($id:String!){ updateChannel(id:$id,name:\"Hijacked\"){ id } }","variables":{"id":"` + owned + `"}}`
	if _, errs := post(t, handler, update, authorizeManager); len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown channel") {
		t.Errorf("cross-Profile updateChannel errors = %#v, want unknown channel", errs)
	}
	del := `{"query":"mutation V($id:String!){ deleteChannel(id:$id){ id } }","variables":{"id":"` + owned + `"}}`
	if _, errs := post(t, handler, del, authorizeManager); len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown channel") {
		t.Errorf("cross-Profile deleteChannel errors = %#v, want unknown channel", errs)
	}

	// The guard scopes edits; it does not lock a Profile out of its own Channel.
	ownDel := `{"query":"mutation V($id:String!){ deleteChannel(id:$id){ id } }","variables":{"id":"` + foreign + `"}}`
	if _, errs := post(t, handler, ownDel, authorizeManager); len(errs) != 0 {
		t.Errorf("same-Profile deleteChannel errors = %#v, want success", errs)
	}

	// And the owner's Channel survived the cross-Profile attempts.
	useProfile("profile-default")
	if _, errs := post(t, handler, del, authorizeManager); len(errs) != 0 {
		t.Errorf("owner deleteChannel errors = %#v, want success", errs)
	}
}

// TestServerSharedChannelIsEditableByAnyProfile pins the other arm of
// channelEditable: a server-shared Channel (empty ProfileID) stays editable by
// any Profile that can see its Library. Only the store can produce one —
// createChannel always stamps the caller's ProfileID (graph_channels.go:85) —
// so it is seeded directly here.
//
// Without this, deleting the `ProfileID == ""` branch from channelEditable
// leaves the whole suite green, i.e. the shared-Channel policy would be
// asserted only in a comment.
func TestServerSharedChannelIsEditableByAnyProfile(t *testing.T) {
	st := storetest.Open(t)
	if err := st.SetAdminPassword(t.Context(), "correct horse battery", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	handler := newTestHandlerWithStore(st)

	shared, err := st.CreateChannel(t.Context(), store.Channel{
		LibraryID:  "library-local",
		Name:       "House Channel",
		SourceExpr: `{"kind":"all-items"}`,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateChannel(shared): %v", err)
	}
	if shared.ProfileID != "" {
		t.Fatalf("seeded ProfileID = %q, want empty (server-shared)", shared.ProfileID)
	}

	// A Profile that is not the creator — there is no creator — can edit it.
	if err := st.SetSessionProfile(t.Context(), "session-manager", "profile-managed"); err != nil {
		t.Fatalf("SetSessionProfile: %v", err)
	}
	update := `{"query":"mutation V($id:String!){ updateChannel(id:$id,name:\"House Rules\"){ id name } }","variables":{"id":"` + string(shared.ID) + `"}}`
	if _, errs := post(t, handler, update, authorizeManager); len(errs) != 0 {
		t.Errorf("updateChannel on server-shared Channel errors = %#v, want success", errs)
	}
	del := `{"query":"mutation V($id:String!){ deleteChannel(id:$id){ id } }","variables":{"id":"` + string(shared.ID) + `"}}`
	if _, errs := post(t, handler, del, authorizeManager); len(errs) != 0 {
		t.Errorf("deleteChannel on server-shared Channel errors = %#v, want success", errs)
	}
}
