package store

import (
	"context"
	"strings"
	"testing"
)

// seedCertifiedLibrary creates a Library bound to the given plugin and
// imports one Media per certification value; "" imports an unrated item.
// Returned IDs are keyed by certification value ("" for the unrated one).
func seedCertifiedLibrary(t *testing.T, st *Store, pluginID string, restricted bool, certs ...string) (LibraryID, map[string]MediaID) {
	t.Helper()
	ctx := context.Background()
	library, err := st.CreateLibrary(ctx, "Certified "+pluginID, restricted, pluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]MediaID{}
	for i, cert := range certs {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          library.ID,
			Path:               "/films/" + pluginID + "/" + strings.ReplaceAll("item-"+cert, " ", "-") + ".mp4",
			Title:              "Item " + cert,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: pluginID + "-fp-" + cert + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if cert != "" {
			if err := st.SetMediaFields(ctx, media.ID, []MediaField{
				{Name: "certification", Type: EntityFieldText, Text: cert},
			}); err != nil {
				t.Fatal(err)
			}
		}
		ids[cert] = media.ID
	}
	return library.ID, ids
}

func TestCertificationCeilingFiltersMediaReads(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	libraryID, ids := seedCertifiedLibrary(t, st, "filmtv", false, "low", "mid", "high", "")

	constraint := CertificationConstraint{PluginID: "filmtv", Allowed: []string{"low", "mid"}}
	filter := LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{constraint}}

	// Single-media reads: below/at ceiling visible, above hidden, unrated
	// blocked fail-closed.
	for _, cert := range []string{"low", "mid"} {
		if _, err := st.Media(ctx, ids[cert], filter); err != nil {
			t.Fatalf("media %q hidden below ceiling: %v", cert, err)
		}
	}
	if _, err := st.Media(ctx, ids["high"], filter); err == nil {
		t.Fatal("media above the ceiling visible")
	}
	if _, err := st.Media(ctx, ids[""], filter); err == nil {
		t.Fatal("unrated media visible under a ceiling (fail-closed default)")
	}

	// AllowUnrated re-admits the unrated item only.
	allowUnrated := filter
	allowUnrated.Certification = []CertificationConstraint{{PluginID: "filmtv", Allowed: []string{"low", "mid"}, AllowUnrated: true}}
	if _, err := st.Media(ctx, ids[""], allowUnrated); err != nil {
		t.Fatalf("unrated media hidden despite AllowUnrated: %v", err)
	}
	if _, err := st.Media(ctx, ids["high"], allowUnrated); err == nil {
		t.Fatal("above-ceiling media visible despite AllowUnrated")
	}

	// Media in Libraries not bound to the constrained plugin is untouched
	// (the seeded media-alpha is unrated in an unbound Library).
	if _, err := st.Media(ctx, "media-alpha", filter); err != nil {
		t.Fatalf("unbound-library media hidden by a foreign ceiling: %v", err)
	}

	// List reads: SourceItems and the all-items grid exclude the same rows.
	scoped := filter
	scoped.AllowedIDs = []LibraryID{libraryID}
	items, err := st.SourceItems(ctx, scoped, SourceQuery{Kind: "recently-added", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	assertItemSet(t, "sourceItems", items, ids, "low", "mid")
	grid, err := st.SourceItems(ctx, scoped, SourceQuery{Kind: "all-items", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	assertItemSet(t, "all-items", grid, ids, "low", "mid")

	// Search must not leak above-ceiling or unrated titles.
	found, err := st.SearchMedia(ctx, scoped, "Item", 50)
	if err != nil {
		t.Fatal(err)
	}
	assertItemSet(t, "search", found, ids, "low", "mid")

	// An empty Allowed set (unknown scale or off-scale ceiling) is
	// fail-closed: nothing certified survives.
	closed := LibraryFilter{
		AllowedIDs:    []LibraryID{libraryID},
		Certification: []CertificationConstraint{{PluginID: "filmtv"}},
	}
	items, err = st.SourceItems(ctx, closed, SourceQuery{Kind: "all-items", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("fail-closed constraint leaked %d items", len(items))
	}
}

func assertItemSet(t *testing.T, surface string, items []SourceItem, ids map[string]MediaID, want ...string) {
	t.Helper()
	got := map[MediaID]bool{}
	for _, item := range items {
		got[MediaID(item.ID)] = true
	}
	if len(items) != len(want) {
		t.Fatalf("%s returned %d items, want %d (%#v)", surface, len(items), len(want), items)
	}
	for _, cert := range want {
		if !got[ids[cert]] {
			t.Fatalf("%s missing item %q", surface, cert)
		}
	}
}

func TestCertificationCeilingComposesWithRevealFiltering(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// A restricted, plugin-bound Library: both gates must hold at once.
	libraryID, ids := seedCertifiedLibrary(t, st, "afterdark", true, "low", "high")
	filter := LibraryFilter{
		AllowedIDs:    []LibraryID{libraryID},
		Certification: []CertificationConstraint{{PluginID: "afterdark", Allowed: []string{"low"}}},
	}

	// Unrevealed: nothing in the restricted Library, ceiling or not.
	if _, err := st.Media(ctx, ids["low"], filter); err == nil {
		t.Fatal("restricted media visible without reveal")
	}
	// Revealed: the ceiling still holds.
	filter.Revealed = true
	if _, err := st.Media(ctx, ids["low"], filter); err != nil {
		t.Fatalf("revealed below-ceiling media hidden: %v", err)
	}
	if _, err := st.Media(ctx, ids["high"], filter); err == nil {
		t.Fatal("reveal bypassed the certification ceiling")
	}
	items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || MediaID(items[0].ID) != ids["low"] {
		t.Fatalf("revealed+ceiling items = %#v, want only the low item", items)
	}
}

func TestProfileCertificationCeilingRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	profile, err := st.SetProfileCertificationCeiling(ctx, "profile-managed", ProfileCertificationCeiling{
		PluginID: "filmtv", Ceiling: "mid",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.CertificationCeilings) != 1 || profile.CertificationCeilings[0].Ceiling != "mid" ||
		profile.CertificationCeilings[0].AllowUnrated {
		t.Fatalf("ceilings = %#v", profile.CertificationCeilings)
	}

	// Upsert overwrites the same plugin's row.
	profile, err = st.SetProfileCertificationCeiling(ctx, "profile-managed", ProfileCertificationCeiling{
		PluginID: "filmtv", Ceiling: "low", AllowUnrated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.CertificationCeilings) != 1 || profile.CertificationCeilings[0].Ceiling != "low" ||
		!profile.CertificationCeilings[0].AllowUnrated {
		t.Fatalf("upserted ceilings = %#v", profile.CertificationCeilings)
	}

	// Another Profile is unaffected.
	other, err := st.Profile(ctx, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if len(other.CertificationCeilings) != 0 {
		t.Fatalf("ceiling leaked across profiles: %#v", other.CertificationCeilings)
	}

	// Clear removes the row; clearing again is a no-op.
	if profile, err = st.ClearProfileCertificationCeiling(ctx, "profile-managed", "filmtv"); err != nil {
		t.Fatal(err)
	}
	if len(profile.CertificationCeilings) != 0 {
		t.Fatalf("ceilings after clear = %#v", profile.CertificationCeilings)
	}
	if _, err = st.ClearProfileCertificationCeiling(ctx, "profile-managed", "filmtv"); err != nil {
		t.Fatalf("clearing an absent ceiling errored: %v", err)
	}

	// Validation: unknown profile, blank plugin, blank ceiling.
	if _, err = st.SetProfileCertificationCeiling(ctx, "profile-ghost", ProfileCertificationCeiling{PluginID: "filmtv", Ceiling: "mid"}); err == nil {
		t.Fatal("unknown profile accepted")
	}
	if _, err = st.SetProfileCertificationCeiling(ctx, "profile-managed", ProfileCertificationCeiling{Ceiling: "mid"}); err == nil {
		t.Fatal("blank plugin accepted")
	}
	if _, err = st.SetProfileCertificationCeiling(ctx, "profile-managed", ProfileCertificationCeiling{PluginID: "filmtv"}); err == nil {
		t.Fatal("blank ceiling accepted")
	}
}
