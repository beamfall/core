package compat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

const catalogPlugin = "plugin-test"

// catalogInterop is a plugin presentation built only from test tokens: core
// compares them and never names one.
var catalogInterop = map[string]Interop{catalogPlugin: {
	MediaField:      "class",
	MediaTypes:      map[string]string{"single": "Movie", "part": "Episode"},
	KindTypes:       map[string]string{"group": "Series", "subgroup": "Season"},
	ScopeTypes:      map[string]string{"scope-a": "tvshows"},
	MemberRelations: map[string]bool{MemberRelationKey("subgroup", "member"): true},
	ParentRelations: map[string]bool{
		ParentRelationKey("subgroup", "member-of", "group"): true,
		ParentRelationKey("subgroup", "next", "subgroup"):   true,
	},
	ProviderSchemes: map[string]string{"by-id": "tvdb"},
}}

type catalogFixture struct {
	st                            *store.Store
	service                       *auth.Service
	handler                       *Handler
	token                         string
	library, hidden, sealed       store.LibraryID
	single, part1, part2, part3   store.MediaID
	unmapped, hiddenItem, sealedM store.MediaID
	group, sub1, sub2             store.EntityID
}

func newCatalogFixture(t *testing.T) catalogFixture {
	t.Helper()
	ctx := t.Context()
	st, service := integrationService(t, time.Now)
	f := catalogFixture{st: st, service: service}
	library := func(title, scope string, restricted bool) store.LibraryID {
		lib, err := st.CreateLibraryWithRoots(ctx, title, restricted, catalogPlugin, "profile-default", []string{t.TempDir()}, scope)
		if err != nil {
			t.Fatal(err)
		}
		return lib.ID
	}
	f.library = library("Compat Shelf", "scope-a", false)
	f.hidden = library("Unlisted Shelf", "scope-a", false)
	f.sealed = library("Sealed Shelf", "scope-a", false)
	unmappedScope := library("Unmapped Scope Shelf", "scope-b", false)
	n := 0
	item := func(lib store.LibraryID, title string, year, width, height int, fields ...store.MediaField) store.MediaID {
		n++
		m, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib, Path: filepath.Join("/catalog", fmt.Sprint(n), title+".mkv"), Title: title,
			Container: "mkv", Codec: "h264", Class: store.MediaClassVideo, Year: year,
			ContentFingerprint: fmt.Sprintf("catalog-%d", n), VideoWidthHint: width, VideoHeightHint: height,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(fields) > 0 {
			if err := st.UpsertMediaFields(ctx, m.ID, fields); err != nil {
				t.Fatal(err)
			}
		}
		return m.ID
	}
	class := func(value string) store.MediaField {
		return store.MediaField{Name: "class", Type: store.EntityFieldText, Text: value}
	}
	number := func(name string, value float64) store.MediaField {
		return store.MediaField{Name: name, Type: store.EntityFieldNumber, Number: value}
	}
	f.single = item(f.library, "Alpha Single", 2001, 3840, 2160, class("single"))
	f.part1 = item(f.library, "Part B", 2010, 1920, 1080, class("part"), number("sequence", 1), number("index", 2))
	f.part2 = item(f.library, "Part A", 2009, 1920, 1080, class("part"), number("sequence", 1), number("index", 1))
	f.part3 = item(f.library, "Part C", 2011, 1280, 720, class("part"), number("sequence", 2), number("index", 1))
	f.unmapped = item(f.library, "Loose Item", 2005, 640, 480)
	f.hiddenItem = item(f.hidden, "Unlisted Single", 2002, 1920, 1080, class("single"))
	f.sealedM = item(f.sealed, "Sealed Single", 2003, 1920, 1080, class("single"))
	item(unmappedScope, "Unmapped Scope Single", 2004, 1920, 1080, class("single"))

	f.group, f.sub1, f.sub2 = "entity-group", "entity-sub-1", "entity-sub-2"
	for _, entity := range []store.Entity{
		{ID: f.group, LibraryID: f.library, Kind: "group", Title: "Beta Group"},
		{ID: f.sub1, LibraryID: f.library, Kind: "subgroup", Title: "Group Part 1"},
		{ID: f.sub2, LibraryID: f.library, Kind: "subgroup", Title: "Group Part 2"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []store.EntityRelation{
		{FromEntityID: f.sub1, Name: "member", ToMediaID: f.part1, Cardinality: "one"},
		{FromEntityID: f.sub1, Name: "member", ToMediaID: f.part2, Cardinality: "one"},
		{FromEntityID: f.sub2, Name: "member", ToMediaID: f.part3, Cardinality: "one"},
		{FromEntityID: f.sub1, Name: "member-of", ToEntityID: f.group, Cardinality: "one"},
		{FromEntityID: f.sub2, Name: "member-of", ToEntityID: f.group, Cardinality: "one"},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddMediaExternalID(ctx, f.single, "tmdb", "111"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaExternalID(ctx, f.single, "unknown-scheme", "999"); err != nil {
		t.Fatal(err)
	}
	// A stored value that is not a bare identifier is never echoed.
	if err := st.AddMediaExternalID(ctx, f.single, "imdb", "https://example.invalid/tt1"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, f.part2, catalogPlugin, "by-id", "777"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, f.part3, catalogPlugin, "by-id", "888"); err != nil {
		t.Fatal(err)
	}
	allow := []store.LibraryID{f.library, f.sealed, unmappedScope, "library-local"}
	if _, err := st.SetJellyfinCompatConfig(ctx, store.JellyfinCompatPatch{LibraryIDs: &allow}, store.JellyfinCompatConfig{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.handler = newIdentityHandler(t, Config{
		Enabled: true, ServerID: "server-test", Store: st,
		Eligible: st.JellyfinCompatEligibleLibraries,
		Sealed:   func(id store.LibraryID) bool { return id == f.sealed },
		Interop:  catalogInterop,
	}, service)
	f.token = scopedIntegrationToken(t, service, store.IntegrationScopeRead)
	return f
}

func (f catalogFixture) get(t *testing.T, path string, want int) []byte {
	t.Helper()
	rec := sendCompat(f.handler, seerrRequest(t, http.MethodGet, path, f.token))
	if rec.Code != want {
		t.Fatalf("GET %s returned %d, want %d: %s", path, rec.Code, want, rec.Body.String())
	}
	return rec.Body.Bytes()
}

type catalogItemJSON struct {
	ID             string            `json:"Id"`
	Name           string            `json:"Name"`
	ServerID       string            `json:"ServerId"`
	Type           string            `json:"Type"`
	ProviderIDs    map[string]string `json:"ProviderIds"`
	IndexNumber    *int              `json:"IndexNumber"`
	ProductionYear int               `json:"ProductionYear"`
	SeriesID       string            `json:"SeriesId"`
	SeasonID       string            `json:"SeasonId"`
	MediaSources   []struct {
		MediaStreams []struct {
			Type          string
			Width, Height int
		}
	}
}

type catalogListJSON struct {
	Items            []catalogItemJSON
	TotalRecordCount int
	StartIndex       int
}

func decodeList(t *testing.T, body []byte) catalogListJSON {
	t.Helper()
	var list catalogListJSON
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if list.Items == nil {
		t.Fatalf("list %s has no Items array", body)
	}
	return list
}

func itemIDs(items []catalogItemJSON) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func TestJellyfinCatalogMediaFolders(t *testing.T) {
	f := newCatalogFixture(t)
	body := f.get(t, "Library/MediaFolders", http.StatusOK)
	want := fmt.Sprintf(`{"Items":[{"Id":%q,"Name":"Compat Shelf","Type":"CollectionFolder","CollectionType":"tvshows"}],"TotalRecordCount":1,"StartIndex":0}`+"\n", f.library)
	if string(body) != want {
		t.Fatalf("media folders = %s, want %s", body, want)
	}
}

func TestJellyfinCatalogLibraryItems(t *testing.T) {
	f := newCatalogFixture(t)
	base := "Items?ParentId=" + string(f.library) + "&Recursive=true&IncludeItemTypes=Series,Movie,Others"
	all := decodeList(t, f.get(t, base+"&StartIndex=0", http.StatusOK))
	if got, want := itemIDs(all.Items), []string{string(f.single), string(f.group)}; !slices.Equal(got, want) || all.TotalRecordCount != 2 {
		t.Fatalf("library items = %v (total %d), want %v (total 2)", got, all.TotalRecordCount, want)
	}
	single, group := all.Items[0], all.Items[1]
	if single.Type != "Movie" || single.ProductionYear != 2001 || single.ServerID != "server-test" ||
		!maps(single.ProviderIDs, map[string]string{"Tmdb": "111"}) || single.SeriesID != "" {
		t.Fatalf("single item = %+v", single)
	}
	if len(single.MediaSources) != 1 || len(single.MediaSources[0].MediaStreams) != 1 ||
		single.MediaSources[0].MediaStreams[0].Width != 3840 || single.MediaSources[0].MediaStreams[0].Height != 2160 {
		t.Fatalf("single media sources = %+v, want one 3840x2160 video stream", single.MediaSources)
	}
	// The group's year is its earliest member; its provider ID is the first
	// identified member in tree order (Part A, index 1 of part 1).
	if group.Type != "Series" || group.Name != "Beta Group" || group.ProductionYear != 2009 ||
		!maps(group.ProviderIDs, map[string]string{"Tvdb": "777"}) || len(group.MediaSources) != 0 {
		t.Fatalf("group item = %+v", group)
	}

	page := decodeList(t, f.get(t, base+"&StartIndex=1&Limit=1", http.StatusOK))
	if got := itemIDs(page.Items); !slices.Equal(got, []string{string(f.group)}) || page.TotalRecordCount != 2 || page.StartIndex != 1 {
		t.Fatalf("second page = %v (total %d, start %d)", got, page.TotalRecordCount, page.StartIndex)
	}
	again := decodeList(t, f.get(t, base+"&StartIndex=1&Limit=1", http.StatusOK))
	if !slices.Equal(itemIDs(again.Items), itemIDs(page.Items)) {
		t.Fatal("repeating a page returned different items")
	}
	past := decodeList(t, f.get(t, base+"&StartIndex=5", http.StatusOK))
	if len(past.Items) != 0 || past.TotalRecordCount != 2 {
		t.Fatalf("page past the end = %+v", past)
	}
	filtered := decodeList(t, f.get(t, "Items?parentId="+string(f.library)+"&includeItemTypes=movie", http.StatusOK))
	if got := itemIDs(filtered.Items); !slices.Equal(got, []string{string(f.single)}) || filtered.TotalRecordCount != 1 {
		t.Fatalf("type filter = %v (total %d), want only the single", got, filtered.TotalRecordCount)
	}
	zero := decodeList(t, f.get(t, base+"&Limit=0", http.StatusOK))
	if len(zero.Items) != 0 || zero.TotalRecordCount != 2 {
		t.Fatalf("Limit=0 = %+v", zero)
	}
	none := decodeList(t, f.get(t, "Items?Recursive=true&IncludeItemTypes=Movie", http.StatusOK))
	if len(none.Items) != 0 || none.TotalRecordCount != 0 {
		t.Fatalf("Items without ParentId or ids = %+v, want empty", none)
	}
	for _, id := range []store.LibraryID{f.hidden, f.sealed, "library-local", "library-archive", "no-such-library"} {
		f.get(t, "Items?ParentId="+string(id), http.StatusNotFound)
	}
}

func maps(got, want map[string]string) bool {
	if got == nil || len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

func TestJellyfinCatalogItemsByID(t *testing.T) {
	f := newCatalogFixture(t)
	ids := []string{string(f.part3), "missing", string(f.hiddenItem), string(f.group), string(f.part3), string(f.unmapped), string(f.sealedM)}
	list := decodeList(t, f.get(t, "Items?ids="+strings.Join(ids, ","), http.StatusOK))
	if got, want := itemIDs(list.Items), []string{string(f.part3), string(f.group)}; !slices.Equal(got, want) || list.TotalRecordCount != 2 {
		t.Fatalf("ids lookup = %v (total %d), want %v", got, list.TotalRecordCount, want)
	}
	part := list.Items[0]
	if part.Type != "Episode" || part.SeriesID != string(f.group) || part.SeasonID != string(f.sub2) ||
		part.IndexNumber == nil || *part.IndexNumber != 1 || part.ProviderIDs == nil || len(part.ProviderIDs) != 0 {
		t.Fatalf("part item = %+v", part)
	}
}

func TestJellyfinCatalogLatest(t *testing.T) {
	f := newCatalogFixture(t)
	var latest []catalogItemJSON
	if err := json.Unmarshal(f.get(t, "Items/Latest?Limit=2&ParentId="+string(f.library), http.StatusOK), &latest); err != nil {
		t.Fatal(err)
	}
	if got, want := itemIDs(latest), []string{string(f.part3), string(f.part2)}; !slices.Equal(got, want) {
		t.Fatalf("latest = %v, want the two newest mapped items %v", got, want)
	}
	if err := json.Unmarshal(f.get(t, "Items/Latest?Limit=500&ParentId="+string(f.library), http.StatusOK), &latest); err != nil {
		t.Fatal(err)
	}
	if len(latest) != 4 {
		t.Fatalf("latest with a large limit returned %d items, want the 4 mapped items", len(latest))
	}
	f.get(t, "Items/Latest?ParentId="+string(f.hidden), http.StatusNotFound)
	f.get(t, "Items/Latest", http.StatusNotFound)
}

func TestJellyfinCatalogContainerChildren(t *testing.T) {
	f := newCatalogFixture(t)
	seasons := decodeList(t, f.get(t, "Shows/"+string(f.group)+"/Seasons", http.StatusOK))
	if got, want := itemIDs(seasons.Items), []string{string(f.sub1), string(f.sub2)}; !slices.Equal(got, want) {
		t.Fatalf("seasons = %v, want %v", got, want)
	}
	if first := seasons.Items[0]; first.Type != "Season" || first.IndexNumber == nil || *first.IndexNumber != 1 || first.SeriesID != string(f.group) || first.SeasonID != "" {
		t.Fatalf("first season = %+v", first)
	}
	episodes := decodeList(t, f.get(t, "Shows/"+string(f.group)+"/Episodes", http.StatusOK))
	if got, want := itemIDs(episodes.Items), []string{string(f.part2), string(f.part1), string(f.part3)}; !slices.Equal(got, want) {
		t.Fatalf("episodes = %v, want %v", got, want)
	}
	narrowed := decodeList(t, f.get(t, "Shows/"+string(f.group)+"/Episodes?seasonId="+string(f.sub2), http.StatusOK))
	if got := itemIDs(narrowed.Items); !slices.Equal(got, []string{string(f.part3)}) {
		t.Fatalf("episodes of part 2 = %v", got)
	}
	paged := decodeList(t, f.get(t, "Shows/"+string(f.group)+"/Episodes?StartIndex=1&Limit=1", http.StatusOK))
	if got := itemIDs(paged.Items); !slices.Equal(got, []string{string(f.part1)}) || paged.TotalRecordCount != 3 || paged.StartIndex != 1 {
		t.Fatalf("episode page = %v (total %d, start %d)", got, paged.TotalRecordCount, paged.StartIndex)
	}
	huge := decodeList(t, f.get(t, "Shows/"+string(f.group)+"/Episodes?StartIndex=9223372036854775807&Limit=1000", http.StatusOK))
	if len(huge.Items) != 0 || huge.TotalRecordCount != 3 {
		t.Fatalf("episode page past the end = %+v", huge)
	}
	// A seasonId that is no container under the item is a 404, not an empty list.
	for _, id := range []string{"missing", string(f.single), string(f.part1), string(f.group)} {
		f.get(t, "Shows/"+string(f.group)+"/Episodes?seasonId="+id, http.StatusNotFound)
	}
	// Only a visible top container answers; everything else is a uniform 404.
	for _, id := range []string{string(f.sub1), string(f.single), "missing", string(f.hiddenItem)} {
		f.get(t, "Shows/"+id+"/Seasons", http.StatusNotFound)
		f.get(t, "Shows/"+id+"/Episodes", http.StatusNotFound)
	}
}

func TestJellyfinCatalogNeverExposesRestrictedOrUnlisted(t *testing.T) {
	f := newCatalogFixture(t)
	revealed := restrictedFixture(t, f.st)
	unlisted := []string{string(f.hidden), "Unlisted Shelf", string(f.hiddenItem), "Unlisted Single",
		string(f.sealed), "Sealed Shelf", string(f.sealedM), "Sealed Single", "Unmapped Scope", "Loose Item", "/catalog"}
	for _, path := range []string{
		"Library/MediaFolders",
		"Items?ParentId=" + string(f.library) + "&Recursive=true",
		"Items?ids=" + strings.Join([]string{string(f.hiddenItem), string(f.sealedM), string(f.unmapped)}, ","),
		"Items/Latest?ParentId=" + string(f.library),
		"Shows/" + string(f.group) + "/Episodes",
		"Users",
	} {
		body := f.get(t, path, http.StatusOK)
		revealed.assertAbsent(t, body)
		for _, value := range unlisted {
			if strings.Contains(string(body), value) {
				t.Fatalf("GET %s exposes %q: %s", path, value, body)
			}
		}
	}
}

func TestJellyfinCatalogRequiresReadScope(t *testing.T) {
	f := newCatalogFixture(t)
	refreshOnly := scopedIntegrationToken(t, f.service, store.IntegrationScopeRefresh)
	for _, path := range []string{
		"Library/MediaFolders", "Items?ParentId=" + string(f.library), "Items/Latest?ParentId=" + string(f.library),
		"Shows/" + string(f.group) + "/Seasons", "Users", "Users/profile-default", "Users/profile-default/Images/Primary",
	} {
		for _, token := range []string{"", refreshOnly} {
			if rec := sendCompat(f.handler, seerrRequest(t, http.MethodGet, path, token)); rec.Code != http.StatusUnauthorized {
				t.Fatalf("GET %s with token present=%v returned %d, want 401", path, token != "", rec.Code)
			}
		}
	}
}

func TestJellyfinCatalogUsers(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := t.Context()
	on := true
	for _, kind := range []store.ProfileKind{store.ProfileAdult, store.ProfileManaged} {
		profile, err := f.st.CreateProfile(ctx, "Other "+string(kind), kind, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		// Only the adult one opts in, and it is then disabled; neither is listed.
		if _, err := f.st.UpdateProfileSettings(ctx, profile.ID, store.ProfileSettings{IntegrationSignIn: &on}); err != nil && kind == store.ProfileAdult {
			t.Fatal(err)
		}
		if kind == store.ProfileAdult {
			disabled := true
			if _, err := f.st.UpdateProfileSettings(ctx, profile.ID, store.ProfileSettings{Disabled: &disabled}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var users []map[string]any
	if err := json.Unmarshal(f.get(t, "Users", http.StatusOK), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0]["Id"] != "profile-default" || users[0]["Name"] != "Default" || len(users[0]) != 3 {
		t.Fatalf("users = %v, want only the opted-in Profile's ID, name and server", users)
	}
	body := f.get(t, "Users/profile-default", http.StatusOK)
	if want := `{"Id":"profile-default","Name":"Default","ServerId":"server-test","Configuration":{"GroupedFolders":[]}}` + "\n"; string(body) != want {
		t.Fatalf("user = %s, want %s", body, want)
	}
	setupID, setupName := f.handler.setupUser()
	body = f.get(t, "Users/"+setupID, http.StatusOK)
	if !strings.Contains(string(body), `"Id":"`+setupID+`"`) || !strings.Contains(string(body), setupName) {
		t.Fatalf("setup user = %s", body)
	}
	f.get(t, "Users/missing", http.StatusNotFound)

	f.get(t, "Users/profile-default/Images/Primary", http.StatusNotFound)
	f.get(t, "Users/"+setupID+"/Images/Primary", http.StatusNotFound)
	picture := []byte("\x89PNG\r\n\x1a\n0000")
	if _, err := f.st.SetProfilePicture(ctx, "profile-default", picture, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec := sendCompat(f.handler, seerrRequest(t, http.MethodGet, "Users/profile-default/Images/Primary", f.token))
	if rec.Code != http.StatusOK || rec.Body.String() != string(picture) || rec.Header().Get("Content-Type") != "image/png" ||
		rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("avatar returned %d %q with headers %v", rec.Code, rec.Body.String(), rec.Header())
	}

	off := false
	if _, err := f.st.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{IntegrationSignIn: &off}); err != nil {
		t.Fatal(err)
	}
	// Opting out withdraws the Profile's compatibility sessions at once.
	f.get(t, "Users", http.StatusUnauthorized)
	f.get(t, "Users/profile-default/Images/Primary", http.StatusUnauthorized)
}

func TestJellyfinCatalogPagingNeverOverflows(t *testing.T) {
	f := newCatalogFixture(t)
	page := decodeList(t, f.get(t, "Items?ParentId="+string(f.library)+"&StartIndex=9223372036854775807&Limit=1000", http.StatusOK))
	if len(page.Items) != 0 || page.TotalRecordCount != 2 || page.StartIndex != 2 {
		t.Fatalf("page at the largest StartIndex = %+v", page)
	}
}

// catalogPart adds one mapped member to the fixture's visible Library.
func (f catalogFixture) catalogPart(t *testing.T, n int) store.MediaID {
	t.Helper()
	m, _, err := f.st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID: f.library, Path: filepath.Join("/catalog", "extra", fmt.Sprint(n)+".mkv"), Title: fmt.Sprintf("Extra %d", n),
		Container: "mkv", Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: fmt.Sprintf("catalog-extra-%d", n),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpsertMediaFields(t.Context(), m.ID, []store.MediaField{{Name: "class", Type: store.EntityFieldText, Text: "part"}}); err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func TestJellyfinCatalogParentRingTerminates(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := t.Context()
	const ring = 12
	for i := range ring {
		id := store.EntityID(fmt.Sprintf("entity-ring-%02d", i))
		if err := f.st.UpsertEntity(ctx, store.Entity{ID: id, LibraryID: f.library, Kind: "subgroup", Title: string(id)}); err != nil {
			t.Fatal(err)
		}
		if err := f.st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: id, Name: "member", ToMediaID: f.catalogPart(t, i), Cardinality: "one"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range ring {
		from, to := store.EntityID(fmt.Sprintf("entity-ring-%02d", i)), store.EntityID(fmt.Sprintf("entity-ring-%02d", (i+1)%ring))
		if err := f.st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: from, Name: "next", ToEntityID: to, Cardinality: "one"}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []byte, 1)
	go func() {
		rec := sendCompat(f.handler, seerrRequest(t, http.MethodGet, "Items?ParentId="+string(f.library), f.token))
		done <- rec.Body.Bytes()
	}()
	select {
	case body := <-done:
		// The ring is broken at one link, so it lists as one more root.
		if list := decodeList(t, body); list.TotalRecordCount != 3 {
			t.Fatalf("library with a parent ring = %d roots, want 3: %s", list.TotalRecordCount, body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("listing a Library with a parent ring never finished")
	}
}

func TestJellyfinCatalogRelationsBindToTheirKind(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := t.Context()
	// "member" is declared on the subgroup kind only, so a group holding a
	// Media by that name does not become its container.
	part := f.catalogPart(t, 1)
	if err := f.st.UpsertEntity(ctx, store.Entity{ID: "entity-direct", LibraryID: f.library, Kind: "group", Title: "Direct Group"}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "entity-direct", Name: "member", ToMediaID: part, Cardinality: "one"}); err != nil {
		t.Fatal(err)
	}
	list := decodeList(t, f.get(t, "Items?ids="+string(part)+",entity-direct", http.StatusOK))
	if got := itemIDs(list.Items); !slices.Equal(got, []string{string(part)}) || list.Items[0].SeriesID != "" {
		t.Fatalf("item held by an undeclared relation = %+v", list.Items)
	}
}

func TestJellyfinCatalogHidesTrashedMembersAndEmptyContainers(t *testing.T) {
	f := newCatalogFixture(t)
	// part3 is the only member of sub2: trashing it hides both.
	if err := f.st.TrashMedia(t.Context(), f.part3, store.LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	seasons := decodeList(t, f.get(t, "Shows/"+string(f.group)+"/Seasons", http.StatusOK))
	if got := itemIDs(seasons.Items); !slices.Equal(got, []string{string(f.sub1)}) || seasons.TotalRecordCount != 1 {
		t.Fatalf("seasons after trash = %v (total %d)", got, seasons.TotalRecordCount)
	}
	episodes := decodeList(t, f.get(t, "Shows/"+string(f.group)+"/Episodes", http.StatusOK))
	if got := itemIDs(episodes.Items); !slices.Equal(got, []string{string(f.part2), string(f.part1)}) || episodes.TotalRecordCount != 2 {
		t.Fatalf("episodes after trash = %v (total %d)", got, episodes.TotalRecordCount)
	}
	if list := decodeList(t, f.get(t, "Items?ids="+string(f.part3)+","+string(f.sub2), http.StatusOK)); len(list.Items) != 0 {
		t.Fatalf("trashed item lookup = %+v", list.Items)
	}
	f.get(t, "Shows/"+string(f.group)+"/Episodes?seasonId="+string(f.sub2), http.StatusNotFound)
}

func TestJellyfinCatalogHidesAllowlistedRestrictedLibrary(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := t.Context()
	// Mapped, allowlisted and otherwise eligible: only Reveal hides it. The
	// allowlist refuses a restricted Library, so restriction comes after.
	lib, err := f.st.CreateLibraryWithRoots(ctx, "Veiled Shelf", false, catalogPlugin, "profile-default", []string{t.TempDir()}, "scope-a")
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := f.st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: lib.ID, Path: "/catalog/veiled/item.mkv", Title: "Veiled Single",
		Container: "mkv", Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: "catalog-veiled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpsertMediaFields(ctx, m.ID, []store.MediaField{{Name: "class", Type: store.EntityFieldText, Text: "single"}}); err != nil {
		t.Fatal(err)
	}
	allow := []store.LibraryID{f.library, lib.ID}
	if _, err := f.st.SetJellyfinCompatConfig(ctx, store.JellyfinCompatPatch{LibraryIDs: &allow}, store.JellyfinCompatConfig{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateLibraryRestricted(ctx, lib.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"Library/MediaFolders", "Items?ParentId=" + string(f.library), "Items?ids=" + string(m.ID),
		"Items/Latest?ParentId=" + string(f.library), "Users",
	} {
		body := string(f.get(t, path, http.StatusOK))
		for _, value := range []string{string(lib.ID), "Veiled Shelf", string(m.ID), "Veiled Single"} {
			if strings.Contains(body, value) {
				t.Fatalf("GET %s exposes %q: %s", path, value, body)
			}
		}
	}
	f.get(t, "Items?ParentId="+string(lib.ID), http.StatusNotFound)
	f.get(t, "Items/Latest?ParentId="+string(lib.ID), http.StatusNotFound)
}

func TestJellyfinCatalogIndexCacheExpires(t *testing.T) {
	f := newCatalogFixture(t)
	now := time.Unix(1_000_000, 0)
	f.handler.indexes.now = func() time.Time { return now }
	libraries, filter, err := f.handler.catalogLibraries(t.Context(), auth.Scope{AllLibraries: true})
	if err != nil || len(libraries) != 1 {
		t.Fatalf("catalog libraries = %v, %v", libraries, err)
	}
	read := func(filter store.LibraryFilter) *catalogIndex {
		t.Helper()
		index, err := f.handler.libraryIndex(t.Context(), filter, libraries[0])
		if err != nil {
			t.Fatal(err)
		}
		return index
	}
	first := read(filter)
	if first.byID[string(f.single)] == nil || first.byID[string(f.part3)] == nil {
		t.Fatal("the first index is missing visible items")
	}
	if read(filter) != first {
		t.Fatal("a repeat read within the TTL rebuilt the index")
	}
	narrow := filter
	narrow.Certification = []store.CertificationConstraint{{PluginID: catalogPlugin}}
	if read(narrow) == first {
		t.Fatal("a different visibility filter reused another filter's index")
	}
	// A visibility change applies on the next read, even inside the TTL.
	if err := f.st.TrashMedia(t.Context(), f.part3, store.LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	trashed := read(filter)
	if trashed == first || trashed.byID[string(f.part3)] != nil || trashed.byID[string(f.single)] == nil {
		t.Fatal("a cached index still served an item trashed after it was built")
	}
	now = now.Add(catalogIndexTTL)
	if read(filter) == trashed {
		t.Fatal("an index older than the TTL was reused")
	}
}

func TestJellyfinCatalogIndexCacheEvictsOldest(t *testing.T) {
	var c indexCache
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }
	var visible [sha256.Size]byte
	indexes := map[string]*catalogIndex{}
	for i := range catalogIndexEntries + 1 {
		key := strconv.Itoa(i)
		indexes[key] = &catalogIndex{}
		c.store(key, visible, indexes[key])
		now = now.Add(time.Millisecond)
	}
	if len(c.entries) != catalogIndexEntries || c.lookup("0", visible) != nil {
		t.Fatalf("entries = %d, oldest kept = %v", len(c.entries), c.lookup("0", visible) != nil)
	}
	for i := 1; i <= catalogIndexEntries; i++ {
		key := strconv.Itoa(i)
		if c.lookup(key, visible) != indexes[key] {
			t.Fatalf("entry %s was evicted before the oldest", key)
		}
	}
}

func TestJellyfinProviderIDAndQueryValue(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		want       bool
	}{
		{"Imdb", "tt0111161", true}, {"Imdb", "0111161", false}, {"Imdb", "tt", false}, {"Imdb", "ttx1", false},
		{"Tmdb", "12345", true}, {"Tvdb", "81189", true}, {"AniDB", "1", true}, {"Tmdb", "tt0111161", false},
		{"Tmdb", "", false}, {"Tmdb", "../x", false}, {"Tmdb", "private-recording.mkv", false},
		{"Tmdb", "a.b_c-d", false}, {"Tmdb", "https://example.invalid/1", false}, {"Tmdb", "1 2", false},
		{"Tmdb", strings.Repeat("9", catalogProviderIDMax+1), false},
	} {
		if got := validProviderID(tc.key, tc.value); got != tc.want {
			t.Errorf("validProviderID(%q, %q) = %v, want %v", tc.key, tc.value, got, tc.want)
		}
	}
	query := map[string][]string{"parentid": {"lower"}, "PARENTID": {"upper"}, "ParentID": {"mixed"}}
	for range 20 {
		if got := queryValue(query, "ParentId"); got != "upper" {
			t.Fatalf("queryValue without the exact key = %q, want the smallest key's value", got)
		}
	}
	query["ParentId"] = []string{"exact"}
	if got := queryValue(query, "ParentId"); got != "exact" {
		t.Fatalf("queryValue = %q, want the exact key's value", got)
	}
}
