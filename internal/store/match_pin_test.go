package store

import (
	"context"
	"testing"
)

// TestMatchPinSetAndRead proves the ADR-0009 §7 import match-pin
// reservation: SetMatchPinned / MatchPinned set and read the match_pinned flag
// on a media_file by content fingerprint (the column PIPE-1's "Fix match"
// mutation will consume). A fresh scanned match defaults unpinned; pinning and
// unpinning round-trip; an unknown fingerprint reports not-found.
func TestMatchPinSetAndRead(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const fingerprint = "sha256:matchpin"
	insertRawMediaFile(t, st, "media-alpha", fingerprint)

	// Default after scan: unpinned, a normal rescan would re-identify it.
	pinned, found, err := st.MatchPinned(ctx, fingerprint)
	if err != nil || !found {
		t.Fatalf("MatchPinned default: found=%v err=%v", found, err)
	}
	if pinned {
		t.Fatalf("freshly scanned match pinned = true, want false")
	}

	// Pin it (the import / Fix-match path): rescan must now leave it alone.
	ok, err := st.SetMatchPinned(ctx, fingerprint, true)
	if err != nil || !ok {
		t.Fatalf("SetMatchPinned(true): ok=%v err=%v", ok, err)
	}
	pinned, found, err = st.MatchPinned(ctx, fingerprint)
	if err != nil || !found {
		t.Fatalf("MatchPinned after pin: found=%v err=%v", found, err)
	}
	if !pinned {
		t.Fatalf("match_pinned = false after SetMatchPinned(true)")
	}

	// Unpin round-trips.
	if ok, err := st.SetMatchPinned(ctx, fingerprint, false); err != nil || !ok {
		t.Fatalf("SetMatchPinned(false): ok=%v err=%v", ok, err)
	}
	pinned, _, err = st.MatchPinned(ctx, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if pinned {
		t.Fatalf("match_pinned = true after SetMatchPinned(false)")
	}

	// Unknown fingerprint: found=false on both read and set, no error.
	if _, found, err := st.MatchPinned(ctx, "sha256:nonesuch"); err != nil || found {
		t.Fatalf("MatchPinned(unknown): found=%v err=%v, want found=false", found, err)
	}
	if ok, err := st.SetMatchPinned(ctx, "sha256:nonesuch", true); err != nil || ok {
		t.Fatalf("SetMatchPinned(unknown): ok=%v err=%v, want ok=false", ok, err)
	}
}
