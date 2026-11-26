package store

import (
	"context"
	"database/sql"
	"testing"
)

const legacySubscriptionMarker = "feed-marker-7d21"

// strandLegacySubscriptionRows writes Subscription, entry, collision, seen and
// item-state import rows for an Encrypted Library straight into catalog.db,
// as a server from before Encrypted Libraries refused Subscriptions left them.
// The entry rows name a Media that lives on the plane, so foreign keys are
// off for the seed.
func strandLegacySubscriptionRows(t *testing.T, st *Store, libraryID LibraryID, mediaID MediaID) {
	t.Helper()
	ctx := context.Background()
	conn, err := st.catalogW.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	secret := legacySubscriptionMarker
	stmts := []struct {
		query string
		args  []any
	}{
		{`PRAGMA foreign_keys = OFF`, nil},
		{`INSERT OR IGNORE INTO subscription (id, library_id, feed_url, root, title, added_at_ms, auth_header)
		  VALUES ('sub-legacy', ?, ?, ?, ?, 1, ?)`, []any{libraryID, "https://feeds.example.test/" + secret + "?Token=AbC",
			"/enc/" + secret, "Feed " + secret, "Bearer " + secret}},
		{`INSERT OR IGNORE INTO subscription_seen_entry (subscription_id, guid, seen_at_ms, path) VALUES ('sub-legacy', ?, 2, ?)`,
			[]any{"guid-" + secret, "/enc/" + secret + "/one.mp3"}},
		{`INSERT OR IGNORE INTO subscription_entry (subscription_id, guid, media_id, first_seen_at_ms, enclosure_url, download_path)
		  VALUES ('sub-legacy', ?, ?, 3, ?, ?)`, []any{"guid-" + secret, mediaID, "https://cdn.example.test/" + secret + ".mp3", "/enc/" + secret + "/one.mp3"}},
		{`INSERT OR IGNORE INTO subscription_entry_collision (subscription_id, guid, bound_path, incoming_path, incoming_media_id, first_seen_at_ms, last_seen_at_ms)
		  VALUES ('sub-legacy', ?, ?, ?, ?, 4, 4)`, []any{"guid-" + secret, "/enc/" + secret + "/one.mp3", "/enc/" + secret + "/two.mp3", mediaID}},
		{`INSERT OR IGNORE INTO item_state_import (profile_id, subscription_id, item_guid, played, imported_at_ms)
		  VALUES ('profile-default', 'sub-legacy', ?, 1, 5)`, []any{"guid-" + secret}},
		{`PRAGMA foreign_keys = ON`, nil},
	}
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatalf("strand legacy subscription rows %q: %v", stmt.query, err)
		}
	}
}

var legacySubscriptionTables = []string{"subscription", "subscription_seen_entry", "subscription_entry", "subscription_entry_collision", "item_state_import"}

func legacySubscriptionCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range legacySubscriptionTables {
		query := `SELECT COUNT(*) FROM ` + table + ` WHERE subscription_id = 'sub-legacy'`
		if table == "subscription" {
			query = `SELECT COUNT(*) FROM subscription WHERE id = 'sub-legacy'`
		}
		out[table] = identificationRowCount(t, db, query)
	}
	return out
}

func assertLegacySubscriptionCounts(t *testing.T, label string, db *sql.DB, want int) {
	t.Helper()
	for table, n := range legacySubscriptionCounts(t, db) {
		if n != want {
			t.Fatalf("%s: %s rows = %d, want %d", label, table, n, want)
		}
	}
}

func armLegacySubscriptionEvacuation(t *testing.T, st *Store, libraryID LibraryID) {
	t.Helper()
	if _, err := st.encryptedDBs[libraryID].writer.ExecContext(context.Background(), `
		INSERT OR REPLACE INTO encrypted_library_metadata (key, value) VALUES ('legacy_subscription_evacuation', 'pending')`); err != nil {
		t.Fatal(err)
	}
}

// TestEncryptedUnsealEvacuatesLegacySubscriptionRows: pre-release Subscription
// and item-state import rows for an Encrypted Library leave catalog.db at the
// next unseal and land on the plane exactly once; a failed shared delete
// unwinds with every row intact; a later unseal moves nothing twice.
func TestEncryptedUnsealEvacuatesLegacySubscriptionRows(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "legacy-subscription")
	defer func() { _ = st.Close() }()
	cipher := copyEncryptedLibraryDBCipher{}
	libraryID := media.LibraryID

	armLegacySubscriptionEvacuation(t, st, libraryID)
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	strandLegacySubscriptionRows(t, st, libraryID, media.ID)

	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER fault_subscription_delete BEFORE DELETE ON subscription
		BEGIN SELECT RAISE(ABORT, 'injected subscription delete fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err == nil {
		t.Fatal("unseal through a failed subscription delete reported success")
	}
	if st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("a failed subscription evacuation left the plane registered")
	}
	assertLegacySubscriptionCounts(t, "catalog after the delete fault", st.catalogR, 1)
	if _, err := st.catalogW.ExecContext(ctx, `DROP TRIGGER fault_subscription_delete`); err != nil {
		t.Fatal(err)
	}

	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	assertLegacySubscriptionCounts(t, "catalog after evacuation", st.catalogR, 0)
	plane := st.encryptedDBs[libraryID].reader
	assertLegacySubscriptionCounts(t, "plane after evacuation", plane, 1)
	assertNoLegacySubscriptionSecret(t, st)
	if set, err := encryptedLibraryMetadataKeySet(ctx, plane, "legacy_subscription_evacuation"); err != nil || set {
		t.Fatalf("pending key after evacuation = %v, %v", set, err)
	}
	var auth string
	if err := plane.QueryRowContext(ctx, `SELECT auth_header FROM subscription WHERE id = 'sub-legacy'`).Scan(&auth); err != nil || auth != "Bearer "+legacySubscriptionMarker {
		t.Fatalf("plane auth header = %q, %v", auth, err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("second Unseal: %v", err)
	}
	assertLegacySubscriptionCounts(t, "plane after a second unseal", st.encryptedDBs[libraryID].reader, 1)
}

// TestEncryptedUnsealEvacuationSurvivesCrashBetweenCommits: a process that
// dies after the private copy committed and before the shared delete leaves
// the plaintext working plane and the catalog rows behind. The restart
// quarantines the working plane, the repair seals it with the copied rows, and
// the next unseal converges: the catalog rows go, the plane keeps one copy.
func TestEncryptedUnsealEvacuationSurvivesCrashBetweenCommits(t *testing.T) {
	ctx := context.Background()
	dataDir := migratedDataDir(t)
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	cipher := copyEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:crash-between", "/enc/crash-between.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	armLegacySubscriptionEvacuation(t, st, libraryID)
	strandLegacySubscriptionRows(t, st, libraryID, media.ID)

	// Phase one of the evacuation commits on the plane; the process then
	// stops before phase two, so the plane is never sealed.
	st.mu.Lock()
	db := st.encryptedDBs[libraryID]
	plane := catalogPlane{libraryID: libraryID, encrypted: true, writer: db.writer, reader: db.reader}
	if _, err := st.copyPlaintextEncryptedLibraryRowsLocked(ctx, plane); err != nil {
		st.mu.Unlock()
		t.Fatalf("copy phase: %v", err)
	}
	_ = db.reader.Close()
	_ = db.writer.Close()
	delete(st.encryptedDBs, libraryID)
	st.mu.Unlock()
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st, err = Open(ctx, dataDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = st.Close() }()
	assertLegacySubscriptionCounts(t, "catalog after the crash", st.catalogR, 1)
	if _, ok := st.EncryptedLibraryDBQuarantine(libraryID); !ok {
		t.Fatal("the crashed working plane was not quarantined at restart")
	}
	if err := st.RepairEncryptedLibraryDBQuarantine(ctx, libraryID, cipher); err != nil {
		t.Fatalf("RepairEncryptedLibraryDBQuarantine: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("Unseal after repair: %v", err)
	}
	assertLegacySubscriptionCounts(t, "catalog after recovery", st.catalogR, 0)
	assertLegacySubscriptionCounts(t, "plane after recovery", st.encryptedDBs[libraryID].reader, 1)
	assertNoLegacySubscriptionSecret(t, st)
}

// CORE-3516 #2: when an evacuation group stays in catalog because its
// referenced Media is outside the protected plane (rows == nil), the pending
// retry marker must remain intact so a later unseal retries after repair.
func TestEncryptedUnsealBlockedEvacuationRetainsRetryMarker(t *testing.T) {
	ctx := context.Background()
	dataDir := migratedDataDir(t)
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	cipher := copyEncryptedLibraryDBCipher{}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:blocked-evac", "/enc/blocked-evac.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	armLegacySubscriptionEvacuation(t, st, libraryID)
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	// Seed legacy rows naming a Media not yet on the protected plane.
	strandLegacySubscriptionRows(t, st, libraryID, "media-missing-from-plane")

	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("blocked unseal: %v", err)
	}
	assertLegacySubscriptionCounts(t, "catalog while media missing", st.catalogR, 1)
	pendingSet, err := encryptedLibraryMetadataKeySet(ctx, st.encryptedDBs[libraryID].reader, "legacy_subscription_evacuation")
	if err != nil || !pendingSet {
		t.Fatalf("legacy_subscription_evacuation pending = %v, err=%v; want true", pendingSet, err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	// Repair the dangling Media reference in catalog, then unseal again (with FKs
	// off because the encrypted Media lives on the protected plane).
	conn, err := st.catalogW.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`PRAGMA foreign_keys = OFF`, nil},
		{`UPDATE subscription_entry SET media_id = ? WHERE subscription_id = 'sub-legacy'`, []any{media.ID}},
		{`UPDATE subscription_entry_collision SET incoming_media_id = ? WHERE subscription_id = 'sub-legacy'`, []any{media.ID}},
		{`PRAGMA foreign_keys = ON`, nil},
	} {
		if _, err := conn.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			_ = conn.Close()
			t.Fatalf("repair %s: %v", stmt.query, err)
		}
	}
	_ = conn.Close()
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatalf("unseal after media repaired: %v", err)
	}
	assertLegacySubscriptionCounts(t, "catalog after repair unseal", st.catalogR, 0)
	assertLegacySubscriptionCounts(t, "plane after repair unseal", st.encryptedDBs[libraryID].reader, 1)
	pendingSet, err = encryptedLibraryMetadataKeySet(ctx, st.encryptedDBs[libraryID].reader, "legacy_subscription_evacuation")
	if err != nil || pendingSet {
		t.Fatalf("legacy_subscription_evacuation after completion = %v, err=%v; want false", pendingSet, err)
	}
}

func assertNoLegacySubscriptionSecret(t *testing.T, st *Store) {
	t.Helper()
	pattern := "%" + legacySubscriptionMarker + "%"
	n := identificationRowCount(t, st.catalogR, `
		SELECT (SELECT COUNT(*) FROM subscription WHERE feed_url LIKE ?1 OR title LIKE ?1 OR auth_header LIKE ?1 OR root LIKE ?1)
		     + (SELECT COUNT(*) FROM subscription_seen_entry WHERE guid LIKE ?1 OR path LIKE ?1)
		     + (SELECT COUNT(*) FROM subscription_entry WHERE guid LIKE ?1 OR enclosure_url LIKE ?1 OR download_path LIKE ?1)
		     + (SELECT COUNT(*) FROM subscription_entry_collision WHERE guid LIKE ?1 OR bound_path LIKE ?1 OR incoming_path LIKE ?1)
		     + (SELECT COUNT(*) FROM item_state_import WHERE item_guid LIKE ?1)`, pattern)
	if n != 0 {
		t.Fatalf("shared catalog rows naming the legacy feed = %d", n)
	}
}
