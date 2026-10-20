package store

import (
	"context"
	"testing"
	"time"
)

// ADR-0045 §2.1: every existing Library defaults to Scanned + not-encrypted
// after the 0066 migration backfill (DEFAULT 0 on both columns), so the new
// orthogonal attributes are back-compatible — a Library that predates ENC-1
// reads back as the old model.
func TestLibraryBackingDefaultsScannedNotEncrypted(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	libraries, err := st.Libraries(ctx, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("Libraries returned error: %v", err)
	}
	if len(libraries) == 0 {
		t.Fatal("seed produced no libraries")
	}
	for _, lib := range libraries {
		if lib.Custodial {
			t.Fatalf("library %q Custodial = true, want false (Scanned default)", lib.ID)
		}
		if lib.Encrypted {
			t.Fatalf("library %q Encrypted = true, want false (not-encrypted default)", lib.ID)
		}
	}
}

// The two attributes are independent and round-trip through SetLibraryBacking
// and the Libraries load (ADR-0045 §2.1: orthogonal axes that compose).
func TestSetLibraryBackingRoundTrips(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	// Custodial + Encrypted (the Photo Backup composition), Custodial-only, and
	// Encrypted-only all persist exactly as written.
	cases := []struct {
		id        LibraryID
		custodial bool
		encrypted bool
	}{
		{"library-local", true, true},
		{"library-archive", true, false},
	}
	for _, c := range cases {
		if err := st.SetLibraryBacking(ctx, c.id, c.custodial, c.encrypted); err != nil {
			t.Fatalf("SetLibraryBacking(%q) returned error: %v", c.id, err)
		}
	}

	libraries, err := st.Libraries(ctx, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("Libraries returned error: %v", err)
	}
	got := map[LibraryID]Library{}
	for _, lib := range libraries {
		got[lib.ID] = lib
	}
	for _, c := range cases {
		lib, ok := got[c.id]
		if !ok {
			t.Fatalf("library %q missing from load", c.id)
		}
		if lib.Custodial != c.custodial || lib.Encrypted != c.encrypted {
			t.Fatalf("library %q backing = {custodial:%v encrypted:%v}, want {%v %v}",
				c.id, lib.Custodial, lib.Encrypted, c.custodial, c.encrypted)
		}
	}

	if err := st.SetLibraryBacking(ctx, "no-such-library", true, true); err == nil {
		t.Fatal("SetLibraryBacking on unknown library returned nil, want error")
	}
}

// Custodial-durability contract: the server holds
// a Custodial Library's only copy, so losing a fingerprint is data loss, not a
// health-report missing. PurgeTrashedBefore must therefore never auto-purge a
// Custodial Library's trashed Media — it stays trash-recoverable indefinitely —
// while a Scanned Library's trashed Media purges as before (its bytes re-import
// from the operator's disk on the next scan).
func TestPurgeExemptsCustodialLibraryMedia(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	// library-local holds the canonical copy of the custodial Media.
	if err := st.SetLibraryBacking(ctx, "library-local", true, false); err != nil {
		t.Fatalf("SetLibraryBacking returned error: %v", err)
	}
	custodial, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/tmp/beamfall-custodial-test.mp4",
		Title:              "Camera Roll Clip",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:custodial-test",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile (custodial) returned error: %v", err)
	}

	// library-archive stays Scanned (the default); media-archive is its Media.
	if err := st.TrashMedia(ctx, custodial.ID, filter); err != nil {
		t.Fatalf("TrashMedia (custodial) returned error: %v", err)
	}
	if err := st.TrashMedia(ctx, "media-archive", filter); err != nil {
		t.Fatalf("TrashMedia (scanned) returned error: %v", err)
	}

	// A far-future cutoff would purge any non-exempt trashed Media.
	purged, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("PurgeTrashedBefore returned error: %v", err)
	}
	// Only the Scanned Library's Media is purged; the Custodial one is exempt.
	if purged != 1 {
		t.Fatalf("purged = %d, want 1 (only the Scanned-Library Media)", purged)
	}
	if _, err := st.Media(ctx, "media-archive", filter); err == nil {
		t.Fatal("Scanned-Library media still readable after purge")
	}

	// The custodial Media survives in trash, recoverable (invariant #6): it is
	// still listed and restores cleanly back into the catalog.
	entries, err := st.TrashedMediaList(ctx, filter)
	if err != nil {
		t.Fatalf("TrashedMediaList returned error: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.ID == custodial.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("custodial media not in trash after purge; the only copy would be data loss")
	}
	if err := st.RestoreMedia(ctx, custodial.ID, filter); err != nil {
		t.Fatalf("RestoreMedia (custodial) returned error: %v", err)
	}
	if _, err := st.Media(ctx, custodial.ID, filter); err != nil {
		t.Fatalf("custodial media not readable after restore: %v", err)
	}
}
