package store

import (
	"context"
	"testing"
)

// lockedConstraint is the filmtv ceiling at "low"/"mid" in locked mode: "high"
// and unrated are blocked, but locked mode keeps them visible on browse.
func lockedConstraint() CertificationConstraint {
	return CertificationConstraint{PluginID: "filmtv", Allowed: []string{"low", "mid"}, Locked: true}
}

func hideConstraint() CertificationConstraint {
	return CertificationConstraint{PluginID: "filmtv", Allowed: []string{"low", "mid"}}
}

// lockedSet returns the set of MediaIDs flagged Locked in a result.
func lockedSet(items []SourceItem) map[MediaID]bool {
	out := map[MediaID]bool{}
	for _, item := range items {
		if item.Locked {
			out[MediaID(item.ID)] = true
		}
	}
	return out
}

// TestEnforcedFilterHideModeKeepsContentAbsent — ACCESS-1 acceptance 1: hide
// mode (the default) leaves above-ceiling and unrated Media absent from every
// browse surface, with no leakage. This is the pre-ACCESS-1 behavior and must
// be byte-identical when FilterVisibility is unset/"hide".
func TestEnforcedFilterHideModeKeepsContentAbsent(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	libraryID, ids := seedCertifiedLibrary(t, st, "filmtv", false, "low", "mid", "high", "")

	filter := LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Certification: []CertificationConstraint{hideConstraint()}}

	items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	// Only the in-ceiling items survive; "high" and unrated are absent.
	assertItemSet(t, "hide all-items", items, ids, "low", "mid")
	// And none are flagged Locked — hide mode never marks, it removes.
	if locked := lockedSet(items); len(locked) != 0 {
		t.Fatalf("hide mode flagged %d items Locked, want 0", len(locked))
	}
}

// TestEnforcedFilterLockedModeRendersVisibleButFlagged — ACCESS-1 acceptance 2:
// locked mode keeps above-ceiling/unrated Media visible on browse + search,
// flagged Locked=true; in-ceiling items stay visible and unflagged.
func TestEnforcedFilterLockedModeRendersVisibleButFlagged(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	libraryID, ids := seedCertifiedLibrary(t, st, "filmtv", false, "low", "mid", "high", "")

	filter := LibraryFilter{AllowedIDs: []LibraryID{libraryID}, Certification: []CertificationConstraint{lockedConstraint()}}

	items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	// All four items are now visible (locked mode does not remove).
	assertItemSet(t, "locked all-items", items, ids, "low", "mid", "high", "")
	// The two blocked items ("high", unrated) carry Locked=true; the two
	// in-ceiling items do not.
	locked := lockedSet(items)
	for _, cert := range []string{"high", ""} {
		if !locked[ids[cert]] {
			t.Fatalf("locked-mode blocked item %q not flagged Locked", cert)
		}
	}
	for _, cert := range []string{"low", "mid"} {
		if locked[ids[cert]] {
			t.Fatalf("in-ceiling item %q wrongly flagged Locked", cert)
		}
	}

	// Search surfaces the same locked items (no leakage difference between
	// surfaces, but visible-with-affordance not absent).
	found, err := st.SearchMedia(ctx, filter, "Item", 50)
	if err != nil {
		t.Fatal(err)
	}
	assertItemSet(t, "locked search", found, ids, "low", "mid", "high", "")
	if !lockedSet(found)[ids["high"]] {
		t.Fatal("search did not flag the above-ceiling item Locked")
	}

	// AllowUnrated in locked mode: the unrated item is admitted (not blocked),
	// so it is visible AND unflagged; "high" stays locked.
	au := filter
	au.Certification = []CertificationConstraint{{PluginID: "filmtv", Allowed: []string{"low", "mid"}, AllowUnrated: true, Locked: true}}
	auItems, err := st.SourceItems(ctx, au, SourceQuery{Kind: "all-items", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	auLocked := lockedSet(auItems)
	if auLocked[ids[""]] {
		t.Fatal("AllowUnrated locked item wrongly flagged (unrated should be admitted)")
	}
	if !auLocked[ids["high"]] {
		t.Fatal("above-ceiling item not flagged under AllowUnrated+locked")
	}
}

// TestEnforcedFilterLockedModePlaybackDeniedServerSide — ACCESS-1 acceptance
// 4: even though a locked Media is visible on browse, the playback gate
// (PlaybackGate=true, as set by Scope.PlaybackFilter) denies it server-side —
// the same fail-closed result as hide mode. This is the byte-gate enforcement,
// independent of any client field.
func TestEnforcedFilterLockedModePlaybackDeniedServerSide(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	_, ids := seedCertifiedLibrary(t, st, "filmtv", false, "low", "high", "")

	browse := LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{lockedConstraint()}}
	playback := browse
	playback.PlaybackGate = true

	// Browse: the above-ceiling item resolves (visible).
	if _, err := st.Media(ctx, ids["high"], browse); err != nil {
		t.Fatalf("locked-mode browse hid the above-ceiling item: %v", err)
	}
	// Playback gate: the SAME item is denied (server-enforced).
	if _, err := st.Media(ctx, ids["high"], playback); err == nil {
		t.Fatal("playback gate served a locked above-ceiling item")
	}
	// Unrated is likewise denied at the gate but visible on browse.
	if _, err := st.Media(ctx, ids[""], browse); err != nil {
		t.Fatalf("locked-mode browse hid the unrated item: %v", err)
	}
	if _, err := st.Media(ctx, ids[""], playback); err == nil {
		t.Fatal("playback gate served a locked unrated item")
	}
	// In-ceiling item plays in both modes.
	if _, err := st.Media(ctx, ids["low"], playback); err != nil {
		t.Fatalf("playback gate denied an in-ceiling item: %v", err)
	}
}

// TestEnforcedFilterRevealNeverLocked — ACCESS-1 acceptance 3: a restricted
// (Reveal-contract) Library is NEVER rendered as Locked under any filter mode.
// Its Media stays *gone* for an unrevealed session — hidden, not visible-with-
// affordance — regardless of hide vs locked certification mode. Reveal and
// Locked are orthogonal boundaries.
func TestEnforcedFilterRevealNeverLocked(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// Restricted Library bound to filmtv, with an above-ceiling item.
	libraryID, ids := seedCertifiedLibrary(t, st, "filmtv", true, "low", "high")

	for _, mode := range []struct {
		name   string
		locked bool
	}{{"hide", false}, {"locked", true}} {
		filter := LibraryFilter{
			AllowedIDs:    []LibraryID{libraryID},
			Certification: []CertificationConstraint{{PluginID: "filmtv", Allowed: []string{"low"}, Locked: mode.locked}},
			// Revealed defaults false: the restricted Library is hidden.
		}

		// Browse: NOTHING from the restricted Library is returned — not even
		// flagged-Locked. The above-ceiling item is gone, not Locked.
		items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 0 {
			t.Fatalf("[%s] restricted Library leaked %d items to an unrevealed session: %#v", mode.name, len(items), items)
		}

		// Single read of the above-ceiling restricted item: hidden (the Reveal
		// gate fires first), never resolved-and-Locked.
		if _, err := st.Media(ctx, ids["high"], filter); err == nil {
			t.Fatalf("[%s] restricted above-ceiling media visible without reveal", mode.name)
		}
		// Even the in-ceiling restricted item is gone without reveal.
		if _, err := st.Media(ctx, ids["low"], filter); err == nil {
			t.Fatalf("[%s] restricted in-ceiling media visible without reveal", mode.name)
		}
	}
}
