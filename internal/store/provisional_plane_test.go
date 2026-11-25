package store

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestProvisionalMediaGUIDKeyIsExact derives one Media ID from the exact feed
// GUID on every path (CORE-0706): the proxy's ProvisionalMediaID, Create and
// Register converge on one Media for a GUID with surrounding whitespace, and a
// GUID that differs only by whitespace is a different Media.
func TestProvisionalMediaGUIDKeyIsExact(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const guid = "  tag:example.com,2026:Ep-é \t"
	want := ProvisionalMediaID("library-local", guid)

	created, err := st.CreateProvisionalMedia(ctx, ProvisionalMedia{LibraryID: "library-local", GUID: guid, Title: "Exact", TeePath: "/tmp/exact.partial", Class: MediaClassAudio})
	if err != nil {
		t.Fatalf("CreateProvisionalMedia: %v", err)
	}
	registered, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{LibraryID: "library-local", GUID: guid, Title: "Exact", TeePath: "/tmp/exact.partial", Class: MediaClassAudio})
	if err != nil {
		t.Fatalf("RegisterProvisionalMedia: %v", err)
	}
	if created != want || registered != want {
		t.Fatalf("Create=%s Register=%s ProvisionalMediaID=%s, want one Media", created, registered, want)
	}
	var value string
	if err := st.catalogR.QueryRowContext(ctx, `SELECT value FROM media_external_id WHERE media_id = ? AND scheme = ?`, want, ProvisionalExternalIDScheme).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != provisionalGUIDExternalID(guid) {
		t.Fatalf("GUID External ID = %q, want the exact GUID form %q", value, provisionalGUIDExternalID(guid))
	}
	if other := ProvisionalMediaID("library-local", "tag:example.com,2026:Ep-é"); other == want {
		t.Fatal("a GUID differing only by whitespace must key a different Media")
	}
}

// TestConcurrentProvisionalRegistrationAndPromotionConverge races first plays
// of one entry and then its promotions: registration yields one Media and the
// fingerprint CAS lets exactly one promotion win.
func TestConcurrentProvisionalRegistrationAndPromotionConverge(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	p := ProvisionalMedia{LibraryID: "library-local", GUID: "guid-race", Title: "Race", TeePath: "/tmp/race.partial", Class: MediaClassAudio}

	const n = 8
	ids := make([]MediaID, n)
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := st.RegisterProvisionalMedia(ctx, p)
			ids[i] = id
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("RegisterProvisionalMedia: %v", err)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("concurrent registrations forked Media: %v", ids)
		}
	}
	var media, files int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM media WHERE id = ?), (SELECT COUNT(*) FROM media_file WHERE media_id = ?)`, ids[0], ids[0]).Scan(&media, &files); err != nil || media != 1 || files != 1 {
		t.Fatalf("media=%d files=%d err=%v, want one Media with one file", media, files, err)
	}

	var won atomic.Int32
	errs = make(chan error, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := st.PromoteProvisionalMediaGeneration(ctx, ids[0], p.TeePath, "sha256:race", "", "/tmp/race.mp3")
			if err != nil {
				errs <- err
				return
			}
			if ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("PromoteProvisionalMediaGeneration: %v", err)
	}
	if got := won.Load(); got != 1 {
		t.Fatalf("promotions won = %d, want exactly 1", got)
	}
	if prov, err := st.MediaFileProvisional(ctx, ids[0]); err != nil || prov {
		t.Fatalf("after promotion provisional=%v err=%v, want archived", prov, err)
	}
}

// TestEncryptedLibraryProvisionalMediaIsRefused keeps a streamed feed entry of
// an Encrypted Library out of the shared catalog: no Media, file or GUID row.
func TestEncryptedLibraryProvisionalMediaIsRefused(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-provisional")
	defer func() { _ = st.Close() }()
	const guid = "guid-private-prov-41d2"
	p := ProvisionalMedia{LibraryID: media.LibraryID, GUID: guid, Title: "Private Stream 41d2", TeePath: "/enc/feeds/41d2.partial", Class: MediaClassAudio}
	if _, err := st.CreateProvisionalMedia(ctx, p); !errors.Is(err, ErrEncryptedLibraryProvisionalMedia) {
		t.Fatalf("CreateProvisionalMedia error = %v, want ErrEncryptedLibraryProvisionalMedia", err)
	}
	if _, _, err := st.RegisterProvisionalMedia(ctx, p); !errors.Is(err, ErrEncryptedLibraryProvisionalMedia) {
		t.Fatalf("RegisterProvisionalMedia error = %v, want ErrEncryptedLibraryProvisionalMedia", err)
	}
	id := ProvisionalMediaID(media.LibraryID, guid)
	var leaked int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM media WHERE id = ? OR title = ?)
		     + (SELECT COUNT(*) FROM media_file WHERE media_id = ? OR path = ?)
		     + (SELECT COUNT(*) FROM media_external_id WHERE media_id = ? OR value LIKE ?)
	`, id, p.Title, id, p.TeePath, id, "%"+guid+"%").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("catalog rows written for the encrypted provisional Media = %d, %v", leaked, err)
	}
}
