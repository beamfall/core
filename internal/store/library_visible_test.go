package store

import "testing"

// These tests pin the Library-visibility chokepoint contract documented on
// LibraryFilter and libraryVisible (H4, fail-closed since 2026-06-16). The
// load-bearing invariants: an empty/nil AllowedIDs admits NOTHING unless the
// explicit AllLibraries sentinel is set (so a zero-grant session fails closed);
// AllLibraries grants every Library; and the restricted-Library guard is an
// independent confidentiality boundary that holds regardless. A regression that
// re-opened the empty default would re-introduce the H4 fail-open; a regression
// that weakened the restricted guard would leak content.
func TestLibraryVisibleContract(t *testing.T) {
	const target = LibraryID("library-target")
	const other = LibraryID("library-other")

	open := Library{ID: target, IsRestricted: false}
	restricted := Library{ID: target, IsRestricted: true}

	tests := []struct {
		name    string
		library Library
		filter  LibraryFilter
		want    bool
	}{
		// Empty/nil AllowedIDs WITHOUT the sentinel fails CLOSED — admits nothing.
		{"nil allowed, no sentinel, unrestricted", open, LibraryFilter{AllowedIDs: nil}, false},
		{"nil allowed, no sentinel, unrestricted, revealed", open, LibraryFilter{AllowedIDs: nil, Revealed: true}, false},
		{"empty allowed, no sentinel, unrestricted", open, LibraryFilter{AllowedIDs: []LibraryID{}}, false},
		{"nil allowed, no sentinel, restricted", restricted, LibraryFilter{AllowedIDs: nil}, false},

		// AllLibraries sentinel grants every Library (subject to the restricted guard).
		{"sentinel, unrestricted, unrevealed", open, LibraryFilter{AllLibraries: true}, true},
		{"sentinel, unrestricted, revealed", open, LibraryFilter{AllLibraries: true, Revealed: true}, true},
		{"sentinel, restricted, unrevealed (guard holds)", restricted, LibraryFilter{AllLibraries: true}, false},
		{"sentinel, restricted, revealed", restricted, LibraryFilter{AllLibraries: true, Revealed: true}, true},
		// A populated AllowedIDs ignores the sentinel (explicit list wins, still scoped).
		{"sentinel + populated excludes target", open, LibraryFilter{AllowedIDs: []LibraryID{other}, AllLibraries: true}, false},

		// Populated AllowedIDs admits only listed members.
		{"populated includes target, unrestricted", open, LibraryFilter{AllowedIDs: []LibraryID{target}}, true},
		{"populated excludes target, unrestricted", open, LibraryFilter{AllowedIDs: []LibraryID{other}}, false},
		{"populated includes target, restricted, unrevealed (guard holds)", restricted, LibraryFilter{AllowedIDs: []LibraryID{target}}, false},
		{"populated includes target, restricted, revealed", restricted, LibraryFilter{AllowedIDs: []LibraryID{target}, Revealed: true}, true},
		// Allow-list miss wins even when Revealed: not in the list => hidden.
		{"populated excludes target, restricted, revealed", restricted, LibraryFilter{AllowedIDs: []LibraryID{other}, Revealed: true}, false},
		{"populated excludes target, unrestricted, revealed", open, LibraryFilter{AllowedIDs: []LibraryID{other}, Revealed: true}, false},

		// Sealed guard: a Library named in SealedIDs is never visible,
		// independent of AllowedIDs/AllLibraries/Revealed and independent of
		// IsRestricted. The BLOCKER case is a SEALED-but-UNRESTRICTED Library: it
		// passes the allow-list and the restricted guard, yet must still be hidden.
		{"sealed unrestricted target, populated allow", open, LibraryFilter{AllowedIDs: []LibraryID{target}, SealedIDs: []LibraryID{target}}, false},
		{"sealed unrestricted target, sentinel", open, LibraryFilter{AllLibraries: true, SealedIDs: []LibraryID{target}}, false},
		{"sealed unrestricted target, revealed", open, LibraryFilter{AllowedIDs: []LibraryID{target}, Revealed: true, SealedIDs: []LibraryID{target}}, false},
		{"sealed restricted target, revealed", restricted, LibraryFilter{AllowedIDs: []LibraryID{target}, Revealed: true, SealedIDs: []LibraryID{target}}, false},
		// A different Library being sealed must NOT hide the target.
		{"other sealed, target unsealed, unrestricted", open, LibraryFilter{AllowedIDs: []LibraryID{target}, SealedIDs: []LibraryID{other}}, true},
		// Empty SealedIDs is a no-op (legitimate unsealed Library still visible).
		{"empty sealed, unrestricted target", open, LibraryFilter{AllowedIDs: []LibraryID{target}, SealedIDs: []LibraryID{}}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := libraryVisible(tc.library, tc.filter); got != tc.want {
				t.Fatalf("libraryVisible(%+v, %+v) = %v, want %v", tc.library, tc.filter, got, tc.want)
			}
		})
	}
}

// TestLibraryVisibleRestrictedGuardIndependentOfAllowedIDs documents H4's core
// safety property as an explicit, named assertion: a restricted Library is
// never visible to an unrevealed filter, for any AllowedIDs/AllLibraries
// combination. The restricted guard is the confidentiality boundary and holds
// independently of the (now fail-closed) AllowedIDs/sentinel gate.
func TestLibraryVisibleRestrictedGuardIndependentOfAllowedIDs(t *testing.T) {
	const target = LibraryID("library-target")
	restricted := Library{ID: target, IsRestricted: true}

	cases := []LibraryFilter{
		{AllowedIDs: nil, Revealed: false},
		{AllowedIDs: []LibraryID{}, Revealed: false},
		{AllowedIDs: []LibraryID{target}, Revealed: false},
		{AllLibraries: true, Revealed: false},
	}
	for _, filter := range cases {
		if libraryVisible(restricted, filter) {
			t.Fatalf("restricted Library visible to unrevealed filter %+v; restricted guard must fail closed", filter)
		}
	}
}
