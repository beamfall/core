package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type libraryIconResult struct {
	Data struct {
		Libraries []LibraryInfo `json:"libraries"`
	} `json:"data"`
	Errors []graphError `json:"errors"`
}

func postLibraryIcon(t *testing.T, handler http.Handler, body string) libraryIconResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var result libraryIconResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return result
}

func setLibraryIconBody(libraryID, icon string) string {
	variables, _ := json.Marshal(map[string]string{"libraryID": libraryID, "libraryIcon": icon})
	return `{"query":"mutation SetLibraryIcon($libraryID: String!, $libraryIcon: String!) { setConsoleConfig(libraryID: $libraryID, libraryIcon: $libraryIcon) { backgroundWorkers } }","variables":` + string(variables) + `}`
}

const librariesIconQuery = `{"query":"query Libraries { libraries { id icon iconChoice defaultIcon } }"}`

// listedLibraryIcon reads one Library back through the management Libraries query.
func listedLibraryIcon(t *testing.T, handler http.Handler, id string) LibraryInfo {
	t.Helper()
	listed := postLibraryIcon(t, handler, librariesIconQuery)
	if len(listed.Errors) != 0 {
		t.Fatalf("libraries errors = %+v", listed.Errors)
	}
	for _, library := range listed.Data.Libraries {
		if library.ID == id {
			return library
		}
	}
	t.Fatalf("libraries query did not list %s", id)
	return LibraryInfo{}
}

// TestSetLibraryIcon pins CORE-1200 on the management API: the icon write on
// the console-config mutation is Admin-Session-gated, validates against the
// fixed vocabulary and is audited against the Library, and the Libraries query
// reports the effective icon, the stored choice and the media-class default.
func TestSetLibraryIcon(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	registry, err := newTestPluginRegistry([]plugin.Bundle{{
		ID:           "tapes",
		DisplayName:  plugin.L("Tapes"),
		MediaLabel:   plugin.L("Tape"),
		MediaClasses: []string{"audio"},
		Layouts: []plugin.LayoutSpec{
			{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "everything", Kind: "grid", Title: plugin.L("All"),
					Source: plugin.SourceSpec{Kind: "all-items"}},
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateLibrary(ctx, "Tapes", false, "tapes", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	id := string(created.ID)
	handler := newTestHandlerWithPlugins(st, registry)

	storedIcon := func() string {
		t.Helper()
		libraries, err := st.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, library := range libraries {
			if library.ID == created.ID {
				return library.Icon
			}
		}
		t.Fatalf("library %s not found", id)
		return ""
	}

	// Without an Admin Session the write is refused and nothing changes.
	denied := postLibraryIcon(t, handler, setLibraryIconBody(id, store.LibraryIconHeadphones))
	if len(denied.Errors) == 0 || denied.Errors[0].Message != "admin session required" {
		t.Fatalf("non-admin icon write = %+v, want admin session required", denied)
	}
	if got := storedIcon(); got != "" {
		t.Fatalf("stored icon after refused write = %q, want empty", got)
	}

	openManagerAdminSession(t, st)

	// Before any choice the Library reports its media-class default.
	listed := postLibraryIcon(t, handler, librariesIconQuery)
	if len(listed.Errors) != 0 {
		t.Fatalf("libraries errors = %+v", listed.Errors)
	}
	found := false
	for _, library := range listed.Data.Libraries {
		if library.Icon == "" || !store.IsLibraryIcon(library.Icon) || library.Icon != library.DefaultIcon || library.IconChoice != "" {
			t.Fatalf("library %s before any choice = %+v, want icon == defaultIcon and no choice", library.ID, library)
		}
		if library.ID == id {
			found = true
			if library.Icon != store.LibraryIconNote {
				t.Fatalf("audio library default icon = %q, want %q", library.Icon, store.LibraryIconNote)
			}
		}
	}
	if !found {
		t.Fatalf("libraries query did not list %s", id)
	}

	// Choosing an icon stores it; the Libraries query reports all three fields.
	chosen := postLibraryIcon(t, handler, setLibraryIconBody(id, store.LibraryIconHeadphones))
	if len(chosen.Errors) != 0 {
		t.Fatalf("icon write errors = %+v", chosen.Errors)
	}
	got := listedLibraryIcon(t, handler, id)
	if got.Icon != store.LibraryIconHeadphones || got.IconChoice != store.LibraryIconHeadphones || got.DefaultIcon != store.LibraryIconNote {
		t.Fatalf("library after choosing = %+v, want headphones chosen over the note default", got)
	}
	if stored := storedIcon(); stored != store.LibraryIconHeadphones {
		t.Fatalf("stored icon = %q, want %q", stored, store.LibraryIconHeadphones)
	}

	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityLibraryIcon ||
		entries[0].TargetType != "library" || entries[0].TargetID != id || string(entries[0].LibraryID) != id {
		t.Fatalf("activity entries = %+v, want an audited library icon change", entries)
	}
	audited := len(entries)

	// The manifest every client reads carries the choice.
	manifestReq := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"query { navigationManifest { libraries { id icon } } }"}`)))
	authorizeManager(manifestReq)
	manifestRec := httptest.NewRecorder()
	handler.ServeHTTP(manifestRec, manifestReq)
	var manifestResult struct {
		Data struct {
			NavigationManifest struct {
				Libraries []struct {
					ID   string `json:"id"`
					Icon string `json:"icon"`
				} `json:"libraries"`
			} `json:"navigationManifest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(manifestRec.Body.Bytes(), &manifestResult); err != nil {
		t.Fatal(err)
	}
	inManifest := ""
	for _, library := range manifestResult.Data.NavigationManifest.Libraries {
		if library.ID == id {
			inManifest = library.Icon
		}
	}
	if inManifest != store.LibraryIconHeadphones {
		t.Fatalf("manifest icon = %q in %s, want %q", inManifest, manifestRec.Body.String(), store.LibraryIconHeadphones)
	}

	// A name outside the vocabulary is refused; nothing changes or is audited.
	unknown := postLibraryIcon(t, handler, setLibraryIconBody(id, "spaceship"))
	if len(unknown.Errors) == 0 || unknown.Errors[0].Message != "unknown library icon" {
		t.Fatalf("unknown icon = %+v, want unknown library icon", unknown)
	}
	if stored := storedIcon(); stored != store.LibraryIconHeadphones {
		t.Fatalf("stored icon after unknown name = %q, want %q", stored, store.LibraryIconHeadphones)
	}
	entries, err = st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != audited {
		t.Fatalf("refused write was audited: %d entries, want %d", len(entries), audited)
	}

	missing := postLibraryIcon(t, handler, setLibraryIconBody("library-missing", store.LibraryIconStar))
	if len(missing.Errors) == 0 || missing.Errors[0].Message != "unknown library" {
		t.Fatalf("missing library = %+v, want unknown library", missing)
	}
	unnamed := postLibraryIcon(t, handler, setLibraryIconBody("", store.LibraryIconStar))
	if len(unnamed.Errors) == 0 || unnamed.Errors[0].Message != "libraryID is required" {
		t.Fatalf("icon without a library = %+v, want libraryID is required", unnamed)
	}

	// An icon never rides along with other settings, so a call cannot half-apply.
	mixed := postLibraryIcon(t, handler, `{"query":"mutation { setConsoleConfig { backgroundWorkers } }","variables":{"libraryID":"`+id+`","libraryIcon":"star","backgroundWorkers":3}}`)
	if len(mixed.Errors) == 0 || mixed.Errors[0].Message != "libraryIcon cannot be combined with other console config" {
		t.Fatalf("mixed write = %+v, want a refusal", mixed)
	}
	if stored := storedIcon(); stored != store.LibraryIconHeadphones {
		t.Fatalf("stored icon after mixed write = %q, want %q", stored, store.LibraryIconHeadphones)
	}

	// An empty icon returns the Library to its default.
	cleared := postLibraryIcon(t, handler, setLibraryIconBody(id, ""))
	if len(cleared.Errors) != 0 {
		t.Fatalf("clearing errors = %+v", cleared.Errors)
	}
	got = listedLibraryIcon(t, handler, id)
	if got.Icon != store.LibraryIconNote || got.IconChoice != "" || got.DefaultIcon != store.LibraryIconNote {
		t.Fatalf("library after clearing = %+v, want the note default and no choice", got)
	}
	if stored := storedIcon(); stored != "" {
		t.Fatalf("stored icon after clearing = %q, want empty", stored)
	}
}
