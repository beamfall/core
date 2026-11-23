package manifest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

func homeScope(t *testing.T, authService *auth.Service, session string) auth.Scope {
	t.Helper()
	scope, err := authService.Scope(context.Background(), store.SessionID(session))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func homeSectionIDs(t *testing.T, service *Service, scope auth.Scope) []string {
	t.Helper()
	doc, err := service.Document(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, section := range layoutByID(doc, HomeLayoutID).Sections {
		ids = append(ids, section.ID)
	}
	return ids
}

func addSmartShelf(t *testing.T, service *Service, libraryID, name string) string {
	t.Helper()
	collection, err := service.store.CreateSmartCollection(context.Background(), store.SmartCollection{
		LibraryID: store.LibraryID(libraryID),
		Name:      name,
	}, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	return "smart-" + string(collection.ID)
}

func TestHomeLayoutReordersHidesAndResets(t *testing.T) {
	ctx := context.Background()
	service, authService := newTestService(t)
	scope := homeScope(t, authService, "session-default")
	defaults := homeSectionIDs(t, service, scope)
	if defaults[0] != "continue" {
		t.Fatalf("default home = %v, want continue first", defaults)
	}

	saved, err := service.SetHomeLayout(ctx, scope, []string{"recent", "continue", "recent", "gone"}, []string{"watchlist", "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Customized {
		t.Fatal("saved layout not reported customized")
	}
	got := homeSectionIDs(t, service, scope)
	if want := []string{"recent", "continue", "next-up", "continue-listening", "your-most-played"}; !slices.Equal(got, want) {
		t.Fatalf("customized home = %v, want %v", got, want)
	}
	var hidden []string
	for _, entry := range saved.Entries {
		if entry.Hidden {
			hidden = append(hidden, entry.Section.ID)
		}
	}
	if !slices.Equal(hidden, []string{"watchlist"}) || len(saved.Entries) != len(defaults) {
		t.Fatalf("homeLayout entries = %+v, want every section with watchlist hidden", saved.Entries)
	}

	// Another Profile is unaffected.
	if other := homeSectionIDs(t, service, homeScope(t, authService, "session-managed")); other[0] != "continue" {
		t.Fatalf("other Profile home = %v, want default", other)
	}

	reset, err := service.ResetHomeLayout(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if reset.Customized {
		t.Fatal("reset layout still customized")
	}
	if got := homeSectionIDs(t, service, scope); !slices.Equal(got, defaults) {
		t.Fatalf("reset home = %v, want %v", got, defaults)
	}
}

func TestHomeLayoutAppendsSectionsAddedAfterCustomizing(t *testing.T) {
	service, authService := newTestService(t)
	scope := homeScope(t, authService, "session-default")
	if _, err := service.SetHomeLayout(context.Background(), scope, []string{"recent", "continue"}, nil); err != nil {
		t.Fatal(err)
	}
	added := addSmartShelf(t, service, "library-local", "Late arrivals")
	got := homeSectionIDs(t, service, scope)
	if got[len(got)-1] != added || got[0] != "recent" {
		t.Fatalf("home after adding a shelf = %v, want recent first and %s last", got, added)
	}
}

func TestHomeLayoutCannotNameSectionsTheProfileDoesNotReceive(t *testing.T) {
	ctx := context.Background()
	service, authService := newTestService(t)
	restricted := addSmartShelf(t, service, "library-archive", "Archive shelf")
	// The owner hides Most-Played server-wide.
	override := store.NavOverrideData{LayoutSectionOverrides: map[string]store.SectionOverrideData{
		HomeLayoutID: {HiddenSectionIDs: []string{"your-most-played"}},
	}}
	if err := service.store.SetNavOverride(ctx, "server", nil, override, time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"session-default", "session-managed"} {
		scope := homeScope(t, authService, session)
		saved, err := service.SetHomeLayout(ctx, scope, []string{restricted, "your-most-played", "recent"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range saved.Entries {
			if entry.Section.ID == restricted || entry.Section.ID == "your-most-played" {
				t.Fatalf("%s homeLayout exposes %s", session, entry.Section.ID)
			}
		}
		got := homeSectionIDs(t, service, scope)
		if slices.Contains(got, restricted) || slices.Contains(got, "your-most-played") || got[0] != "recent" {
			t.Fatalf("%s home = %v, want recent first and no restricted or owner-hidden shelf", session, got)
		}
	}
}

func TestDefaultHomeLeadsWithContinueOverServerOrder(t *testing.T) {
	ctx := context.Background()
	service, authService := newTestService(t)
	override := store.NavOverrideData{LayoutSectionOverrides: map[string]store.SectionOverrideData{
		HomeLayoutID: {SectionOrder: []string{"recent", "watchlist", "continue"}},
	}}
	if err := service.store.SetNavOverride(ctx, "server", nil, override, time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	got := homeSectionIDs(t, service, homeScope(t, authService, "session-default"))
	if want := []string{"continue", "recent", "watchlist"}; !slices.Equal(got[:3], want) {
		t.Fatalf("default home = %v, want prefix %v", got, want)
	}
}

func TestHomeLayoutRefusesOversizedListsAndProfilelessScopes(t *testing.T) {
	ctx := context.Background()
	service, authService := newTestService(t)
	scope := homeScope(t, authService, "session-default")
	tooMany := make([]string, MaxHomeLayoutIDs+1)
	var ve store.ValidationError
	if _, err := service.SetHomeLayout(ctx, scope, tooMany, nil); !errors.As(err, &ve) {
		t.Fatalf("oversized order err = %v, want validation error", err)
	}
	if _, err := service.SetHomeLayout(ctx, auth.Scope{}, nil, nil); !errors.As(err, &ve) {
		t.Fatalf("profileless set err = %v, want validation error", err)
	}
	if _, err := service.HomeLayout(ctx, auth.Scope{}); !errors.As(err, &ve) {
		t.Fatalf("profileless read err = %v, want validation error", err)
	}
}

func TestHomeLayoutSaveKeepsSettingsForShelvesOutOfScope(t *testing.T) {
	ctx := context.Background()
	service, authService := newTestService(t)
	scope := homeScope(t, authService, "session-default")
	restricted := addSmartShelf(t, service, "library-archive", "Archive shelf")
	// Saved earlier while the restricted Library was revealed.
	profileID := string(scope.ProfileID)
	earlier := store.NavOverrideData{LayoutSectionOverrides: map[string]store.SectionOverrideData{
		HomeLayoutID: {SectionOrder: []string{restricted, "recent"}, HiddenSectionIDs: []string{restricted}},
	}}
	if err := service.store.SetNavOverride(ctx, navScopeProfile, &profileID, earlier, time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetHomeLayout(ctx, scope, []string{"watchlist", "recent"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := service.homePreference(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.SectionOrder, []string{"watchlist", "recent", restricted}) || !slices.Equal(got.HiddenSectionIDs, []string{restricted}) {
		t.Fatalf("preference after relocked save = %+v, want the out-of-scope shelf kept", got)
	}
}

func TestHomeLayoutPartialOrderKeepsContinueBeforeOwnerOrder(t *testing.T) {
	ctx := context.Background()
	service, authService := newTestService(t)
	override := store.NavOverrideData{LayoutSectionOverrides: map[string]store.SectionOverrideData{
		HomeLayoutID: {SectionOrder: []string{"recent", "watchlist", "continue"}},
	}}
	if err := service.store.SetNavOverride(ctx, "server", nil, override, time.Unix(1, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	scope := homeScope(t, authService, "session-default")
	if _, err := service.SetHomeLayout(ctx, scope, []string{"watchlist"}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := homeSectionIDs(t, service, scope), []string{"watchlist", "continue", "recent"}; !slices.Equal(got[:3], want) {
		t.Fatalf("home = %v, want prefix %v", got, want)
	}
}

func TestLibraryPagesIgnoreTheHomePreference(t *testing.T) {
	ctx := context.Background()
	service, authService := newTestService(t)
	scope := homeScope(t, authService, "session-default")
	if _, err := service.SetHomeLayout(ctx, scope, []string{"recent"}, []string{"continue", "watchlist"}); err != nil {
		t.Fatal(err)
	}
	doc, err := service.Document(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range doc.Navigation {
		if node.LibraryID != "" && node.LayoutID == HomeLayoutID {
			t.Fatalf("library node %s still opens the customized Home Layout", node.ID)
		}
	}
	library := layoutByID(doc, LibraryHomeLayoutID)
	var ids []string
	for _, section := range library.Sections {
		ids = append(ids, section.ID)
	}
	if len(ids) == 0 || !slices.Contains(ids, "watchlist") || ids[0] != "continue" {
		t.Fatalf("library layout = %v, want the default Home order with every shelf", ids)
	}
}
