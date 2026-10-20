package store

import (
	"context"
	"testing"
)

// TestZeroGrantFilterFailsClosedEndToEnd is the H4 (2026-06-16) integration
// guard. The libraryVisible unit test pins the in-memory helper; this exercises
// the REAL exported read methods, so it covers the SQL `library_id IN (...)`
// construction those methods build from the visible set — the path a zero-grant
// session actually travels. A default-constructed LibraryFilter (empty
// AllowedIDs, no AllLibraries sentinel) must return NOTHING from every
// Library-scoped read; a regression that re-opened the empty default would let
// a zero-grant Profile session see every unrestricted Library/Media here.
func TestZeroGrantFilterFailsClosedEndToEnd(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Empty AllowedIDs, no sentinel — the dangerous zero-grant filter.
	zeroGrant := LibraryFilter{}

	if libs := mustLibraries(t, st, zeroGrant); len(libs) != 0 {
		t.Fatalf("zero-grant Libraries = %v, want none (fail closed)", libraryIDs(libs))
	}
	// Revealed must not widen a zero-grant filter — reveal is an independent
	// gate, not a grant.
	if libs := mustLibraries(t, st, LibraryFilter{Revealed: true}); len(libs) != 0 {
		t.Fatalf("zero-grant+revealed Libraries = %v, want none (fail closed)", libraryIDs(libs))
	}

	if items := mustSourceItems(t, st, zeroGrant, SourceQuery{Kind: "recently-added"}); len(items) != 0 {
		t.Fatalf("zero-grant SourceItems = %v, want none (fail closed)", sourceItemIDs(items))
	}
	// A library-scoped query must not widen the zero-grant filter either.
	if items := mustSourceItems(t, st, zeroGrant, SourceQuery{Kind: "recently-added", LibraryID: "library-local"}); len(items) != 0 {
		t.Fatalf("zero-grant library-scoped SourceItems = %v, want none (fail closed)", sourceItemIDs(items))
	}

	if _, err := st.Media(ctx, "media-alpha", zeroGrant); err == nil {
		t.Fatal("zero-grant Media(media-alpha) returned a row; must fail closed")
	}
}

// TestAllLibrariesSentinelGrantsThroughRealReads pins the other half of the H4
// contract end-to-end: the explicit AllLibraries sentinel restores full
// visibility through the real read SQL (still subject to the independent
// restricted-Library reveal gate). This is the regression guard that the
// sentinel actually threads through Libraries/SourceItems/Media — not just the
// helper — so the 14 server-internal callers that set it keep working.
func TestAllLibrariesSentinelGrantsThroughRealReads(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Sentinel, unrevealed: every UNrestricted Library, restricted still hidden.
	libs := mustLibraries(t, st, LibraryFilter{AllLibraries: true})
	if got, want := libraryIDs(libs), []LibraryID{"library-local"}; !equalIDs(got, want) {
		t.Fatalf("sentinel Libraries = %v, want %v (restricted hidden)", got, want)
	}

	// Sentinel + revealed: restricted Library joins.
	libs = mustLibraries(t, st, LibraryFilter{AllLibraries: true, Revealed: true})
	if got, want := libraryIDs(libs), []LibraryID{"library-local", "library-archive"}; !equalIDs(got, want) {
		t.Fatalf("sentinel+revealed Libraries = %v, want %v", got, want)
	}

	items := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "recently-added"})
	if got, want := sourceItemIDs(items), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("sentinel SourceItems = %v, want %v", got, want)
	}

	if _, err := st.Media(ctx, "media-alpha", LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("sentinel Media(media-alpha) errored: %v", err)
	}
	// Restricted Media stays hidden to the sentinel without reveal.
	if _, err := st.Media(ctx, "media-archive", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("sentinel Media(media-archive) returned restricted row without reveal")
	}
}
