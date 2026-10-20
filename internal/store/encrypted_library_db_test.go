package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type copyEncryptedLibraryDBCipher struct{}

func (copyEncryptedLibraryDBCipher) UnsealDBFile(ctx context.Context, libraryID LibraryID, ciphertextPath, workingPath string) error {
	if _, err := os.Stat(ciphertextPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return copyFile0600(ciphertextPath, workingPath)
}

func (copyEncryptedLibraryDBCipher) SealDBFile(ctx context.Context, libraryID LibraryID, workingPath, ciphertextPath string) error {
	if err := os.MkdirAll(filepath.Dir(ciphertextPath), 0o700); err != nil {
		return err
	}
	if err := copyFile0600(workingPath, ciphertextPath); err != nil {
		return err
	}
	return os.Remove(workingPath)
}

func TestEncryptedLibraryDBLifecycleMigratesBeforeRegistrationAndSealsSidecars(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-enc")
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if !st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("encrypted-library DB was not registered after successful migration")
	}
	db := st.encryptedDBs[libraryID]
	assertEncryptedLibraryMigrated(t, db.writer)

	for _, name := range []string{"working.db-wal", "working.db-shm", "working.db-journal", "vacuum.db-wal", "rollback-journal", "sqlite-tmp"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(db.workingPath), name), []byte("plaintext"), 0o600); err != nil {
			t.Fatalf("write sidecar %s: %v", name, err)
		}
	}
	ciphertextPath := db.ciphertextPath
	workingDir := filepath.Dir(db.workingPath)

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("encrypted-library DB remained registered after seal")
	}
	if _, err := os.Stat(ciphertextPath); err != nil {
		t.Fatalf("ciphertext missing after seal: %v", err)
	}
	entries, err := os.ReadDir(workingDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read working dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("working dir leftovers after seal = %v, want none", entries)
	}
}

func TestEncryptedLibraryDBTempSpillStaysInWorkingAreaAndSealsClean(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-temp-spill")
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	db := st.encryptedDBs[libraryID]
	workingDir := filepath.Dir(db.workingPath)
	assertEncryptedDBTempDir(t, db.writer, workingDir)
	assertEncryptedDBTempDir(t, db.reader, workingDir)
	if _, err := os.Stat(filepath.Join(st.encryptedDBWorkingRoot, cacheDirTagName)); err != nil {
		t.Fatalf("CACHEDIR.TAG missing: %v", err)
	}

	if _, err := db.writer.ExecContext(ctx, `
		PRAGMA cache_size = 8;
		CREATE TEMP TABLE spill AS
		WITH RECURSIVE rows(i, payload) AS (
			VALUES(0, randomblob(4096))
			UNION ALL
			SELECT i + 1, randomblob(4096) FROM rows WHERE i < 512
		)
		SELECT payload FROM rows ORDER BY payload;
	`); err != nil {
		t.Fatalf("force temp spill: %v", err)
	}
	var count int
	if err := db.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM temp.spill`).Scan(&count); err != nil {
		t.Fatalf("temp spill count: %v", err)
	}
	if count != 513 {
		t.Fatalf("temp spill count = %d, want 513", count)
	}

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if _, err := os.Stat(workingDir); !os.IsNotExist(err) {
		t.Fatalf("working dir after seal stat err = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(st.encryptedDBWorkingRoot, cacheDirTagName)); err != nil {
		t.Fatalf("CACHEDIR.TAG after seal: %v", err)
	}
}

func TestRewrapEncryptedLibraryDBRequiresSealedCiphertextAndChangesBytes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-enc")
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if err := st.RewrapEncryptedLibraryDB(ctx, libraryID, func(string) error { return nil }); err == nil {
		t.Fatal("RewrapEncryptedLibraryDB succeeded while DB was registered")
	}
	db := st.encryptedDBs[libraryID]
	ciphertextPath := db.ciphertextPath
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	before, err := os.ReadFile(ciphertextPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RewrapEncryptedLibraryDB(ctx, libraryID, func(path string) error {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, append(raw, byte('x')), 0o600)
	}); err != nil {
		t.Fatalf("RewrapEncryptedLibraryDB: %v", err)
	}
	after, err := os.ReadFile(ciphertextPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatal("metadata DB ciphertext did not change")
	}
}

func assertEncryptedDBTempDir(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var tempStore int
	if err := db.QueryRow("PRAGMA temp_store").Scan(&tempStore); err != nil {
		t.Fatalf("temp_store: %v", err)
	}
	if tempStore != 1 {
		t.Fatalf("temp_store = %d, want FILE", tempStore)
	}
	var got string
	if err := db.QueryRow("PRAGMA temp_store_directory").Scan(&got); err != nil {
		t.Fatalf("temp_store_directory: %v", err)
	}
	if got != want {
		t.Fatalf("temp_store_directory = %q, want %q", got, want)
	}
}

func TestEncryptedLibraryDBLifecycleDisabledKeepsThirdDBUnreachable(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	err = st.UnsealEncryptedLibraryDB(context.Background(), "library-enc", copyEncryptedLibraryDBCipher{})
	if !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("UnsealEncryptedLibraryDB err = %v, want ErrEncryptedLibraryDBDisabled", err)
	}
}

func TestEncryptedLibraryPlaintextLibraryControlPlaneColumnsFrozen(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// These are the only encrypted-library `library` locator/control-plane
	// fields allowed to remain in plaintext. `title` is the operator-facing
	// library display name and is classified explicitly; media titles and other
	// descriptive content live in the encrypted per-library DB.
	allowed := map[string]string{
		"id":                    "opaque library locator",
		"title":                 "operator-facing library display name",
		"plugin_id":             "plugin routing handle",
		"is_restricted":         "Reveal policy bit",
		"preview_clips_enabled": "generation policy bit",
		"trashed_at_ms":         "recoverable lifecycle marker",
		"ai_indexing_enabled":   "enrichment policy bit",
		"family_scope":          "profile access policy handle",
		"custodial":             "storage custody policy bit",
		"encrypted":             "encrypted backing policy bit",
	}
	rows, err := st.catalogR.QueryContext(ctx, `PRAGMA table_info(library)`)
	if err != nil {
		t.Fatalf("table_info(library): %v", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan library column: %v", err)
		}
		classification, ok := allowed[name]
		if !ok {
			t.Fatalf("unclassified plaintext library column %q (%s)", name, typ)
		}
		if classification == "" {
			t.Fatalf("plaintext library column %q has empty classification", name)
		}
		seen[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("library column rows: %v", err)
	}
	for name := range allowed {
		if !seen[name] {
			t.Fatalf("allowed plaintext library column %q missing from schema", name)
		}
	}
}

func TestEncryptedLibraryRestartReconciliationRepairsPlaintextLeftoversToCiphertext(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	workingDir := filepath.Join(dataDir, "db", "encrypted-library-working", "library-crash")
	if err := os.MkdirAll(workingDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	workingPath := filepath.Join(workingDir, "working.db")
	db, err := openSQLiteWriter(workingPath)
	if err != nil {
		t.Fatalf("open crash working DB: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE crash_marker (id TEXT PRIMARY KEY); INSERT INTO crash_marker(id) VALUES ('survived')`); err != nil {
		_ = db.Close()
		t.Fatalf("seed crash working DB: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close crash working DB: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "working.db-wal"), []byte("plaintext wal"), 0o600); err != nil {
		t.Fatalf("write leftover: %v", err)
	}
	st, err := Open(ctx, dataDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if _, ok := st.EncryptedLibraryDBQuarantine("library-crash"); !ok {
		t.Fatal("leftover plaintext sidecar was not quarantined at startup")
	}
	err = st.UnsealEncryptedLibraryDB(ctx, "library-crash", copyEncryptedLibraryDBCipher{})
	if !errors.Is(err, ErrEncryptedLibraryDBQuarantined) {
		t.Fatalf("UnsealEncryptedLibraryDB err = %v, want ErrEncryptedLibraryDBQuarantined", err)
	}
	if err := st.RepairEncryptedLibraryDBQuarantine(ctx, "library-crash", copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("RepairEncryptedLibraryDBQuarantine: %v", err)
	}
	if _, ok := st.EncryptedLibraryDBQuarantine("library-crash"); ok {
		t.Fatal("quarantine remained after repair")
	}
	if _, err := os.Stat(workingDir); !os.IsNotExist(err) {
		t.Fatalf("working dir after repair stat err = %v, want not exist", err)
	}
	ciphertextPath := filepath.Join(dataDir, "db", "encrypted-library", "library-crash", "encrypted_library"+encryptedLibraryDBCipherExt)
	if _, err := os.Stat(ciphertextPath); err != nil {
		t.Fatalf("ciphertext after repair: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, "library-crash", copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB after repair: %v", err)
	}
	repaired := st.encryptedDBs["library-crash"]
	var marker string
	if err := repaired.reader.QueryRowContext(ctx, `SELECT id FROM crash_marker`).Scan(&marker); err != nil {
		t.Fatalf("query repaired marker: %v", err)
	}
	if marker != "survived" {
		t.Fatalf("marker = %q, want survived", marker)
	}
}

func TestEncryptedLibraryImportRoutesToEncryptedDBAndSuppressesOutbox(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	imported := encryptedImport(libraryID, "sha256:encrypted-route-a", "/enc/a.mkv")
	media, file, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if media.ID == mediaIDForFingerprint(imported.ContentFingerprint) {
		t.Fatalf("encrypted media ID = %q, want opaque random ID", media.ID)
	}
	if file.MediaID != media.ID {
		t.Fatalf("file media id = %q, want %q", file.MediaID, media.ID)
	}
	var catalogRows int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, media.ID).Scan(&catalogRows); err != nil {
		t.Fatalf("catalog count: %v", err)
	}
	if catalogRows != 0 {
		t.Fatalf("catalog media rows = %d, want 0", catalogRows)
	}
	db := st.encryptedDBs[libraryID]
	var encryptedRows int
	if err := db.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM media WHERE id = ?`, media.ID).Scan(&encryptedRows); err != nil {
		t.Fatalf("encrypted count: %v", err)
	}
	if encryptedRows != 1 {
		t.Fatalf("encrypted media rows = %d, want 1", encryptedRows)
	}
	if got, err := st.Media(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{libraryID}}); err != nil || got.ID != media.ID {
		t.Fatalf("Media() = (%+v, %v), want %q", got, err, media.ID)
	}
	var outboxRows int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?`, media.ID).Scan(&outboxRows); err != nil {
		t.Fatalf("outbox count: %v", err)
	}
	if outboxRows != 0 {
		t.Fatalf("encrypted import outbox rows = %d, want 0", outboxRows)
	}
}

func TestEncryptedLibrarySearchUnionsCatalogAndOnlyOpenEncryptedDBs(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	encryptedLibraryID := LibraryID("library-local")
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = ?`, encryptedLibraryID); err != nil {
		t.Fatalf("bind encrypted Library plugin: %v", err)
	}
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, []MediaFieldSearchPolicy{
		{PluginID: "photos", Field: "camera-model", Type: EntityFieldText, Searchable: true},
		{PluginID: "photos", Field: "place", Type: EntityFieldText, Searchable: true},
		{PluginID: "photos", Field: "album", Type: EntityFieldText, Searchable: true},
		{PluginID: "photos", Field: "capture-time", Type: EntityFieldText, Searchable: true},
	}); err != nil {
		t.Fatalf("ReplaceMediaFieldSearchPolicies: %v", err)
	}
	if err := st.SetLibraryBacking(ctx, encryptedLibraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, encryptedLibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	plainLibrary, err := st.CreateLibrary(ctx, "Plain Union", false, "photos", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	plainImport := encryptedImport(plainLibrary.ID, "sha256:enc-db-6-plain", "/plain/union.mkv")
	plainImport.Title = "ENC DB Six Union Plain"
	plain, _, err := st.ImportMediaFile(ctx, plainImport)
	if err != nil {
		t.Fatalf("Import plain: %v", err)
	}
	encryptedImport := encryptedImport(encryptedLibraryID, "sha256:enc-db-6-secret", "/enc/union.mkv")
	encryptedImport.Title = "ENC DB Six Union Secret"
	encrypted, _, err := st.ImportMediaFile(ctx, encryptedImport)
	if err != nil {
		t.Fatalf("Import encrypted: %v", err)
	}
	searchFields := []MediaField{
		{Name: "camera-model", Type: EntityFieldText, Text: "EOS R5"},
		{Name: "place", Type: EntityFieldText, Text: "Paris France"},
		{Name: "album", Type: EntityFieldText, Text: "Summer Trip"},
		{Name: "capture-time", Type: EntityFieldText, Text: "2021-07-14T09:30:15Z"},
	}
	if err := st.SetMediaFields(ctx, plain.ID, append(searchFields, MediaField{Name: certificationField, Type: EntityFieldText, Text: "low"})); err != nil {
		t.Fatalf("SetMediaFields plain: %v", err)
	}
	encryptedTx, err := st.encryptedDBs[encryptedLibraryID].writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin encrypted media-field transaction: %v", err)
	}
	if err := insertMediaFields(ctx, encryptedTx, encrypted.ID, append(searchFields, MediaField{Name: certificationField, Type: EntityFieldText, Text: "high"})); err != nil {
		_ = encryptedTx.Rollback()
		t.Fatalf("insert encrypted media fields: %v", err)
	}
	if err := encryptedTx.Commit(); err != nil {
		t.Fatalf("commit encrypted media fields: %v", err)
	}

	filter := LibraryFilter{AllLibraries: true}
	items, err := st.SearchMedia(ctx, filter, "ENC DB Six Union", 10)
	if err != nil {
		t.Fatalf("SearchMedia unsealed: %v", err)
	}
	if !sourceItemsContain(items, plain.ID) || !sourceItemsContain(items, encrypted.ID) {
		t.Fatalf("unsealed search IDs = %v, want plain %s and encrypted %s", encryptedLibrarySourceItemIDs(items), plain.ID, encrypted.ID)
	}
	for _, query := range []string{"Paris", "EOS", "Summer", "2021-07-14"} {
		items, err = st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Query: query, Limit: 10})
		if err != nil {
			t.Fatalf("SourceItems(%q) unsealed: %v", query, err)
		}
		if !sourceItemsContain(items, plain.ID) || !sourceItemsContain(items, encrypted.ID) {
			t.Fatalf("unsealed metadata search %q IDs = %v, want matching plain %s and encrypted %s", query, encryptedLibrarySourceItemIDs(items), plain.ID, encrypted.ID)
		}
	}
	gated := filter
	gated.PlaybackGate = true
	gated.Certification = []CertificationConstraint{{PluginID: "photos", Allowed: []string{"low"}}}
	items, err = st.SourceItems(ctx, gated, SourceQuery{Kind: "all-items", Query: "Paris", Limit: 10})
	if err != nil {
		t.Fatalf("SourceItems metadata certification gate: %v", err)
	}
	if !sourceItemsContain(items, plain.ID) || sourceItemsContain(items, encrypted.ID) {
		t.Fatalf("metadata certification IDs = %v, want only low plain %s", encryptedLibrarySourceItemIDs(items), plain.ID)
	}
	if err := st.SealEncryptedLibraryDB(ctx, encryptedLibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	items, err = st.SearchMedia(ctx, filter, "ENC DB Six Union", 10)
	if err != nil {
		t.Fatalf("SearchMedia sealed: %v", err)
	}
	if !sourceItemsContain(items, plain.ID) || sourceItemsContain(items, encrypted.ID) {
		t.Fatalf("sealed search IDs = %v, want only plain %s", encryptedLibrarySourceItemIDs(items), plain.ID)
	}
	for _, query := range []string{"Paris", "EOS", "Summer", "2021-07-14"} {
		items, err = st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Query: query, Limit: 10})
		if err != nil {
			t.Fatalf("SourceItems(%q) sealed: %v", query, err)
		}
		if !sourceItemsContain(items, plain.ID) || sourceItemsContain(items, encrypted.ID) {
			t.Fatalf("sealed metadata search %q IDs = %v, want only plain %s", query, encryptedLibrarySourceItemIDs(items), plain.ID)
		}
	}
	if _, err := st.Media(ctx, encrypted.ID, filter); err == nil {
		t.Fatal("Media sealed encrypted row succeeded, want hidden")
	}
}

func TestEncryptedLibraryActiveSessionsShowDetailsOnlyWhileUnsealed(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "ENC DB Six Live Secret")
	defer st.Close()

	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", media.ID, false, now.Add(time.Hour)); err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}
	filter := LibraryFilter{AllLibraries: true}
	active, err := st.ActiveSessions(ctx, now, filter)
	if err != nil {
		t.Fatalf("ActiveSessions unsealed: %v", err)
	}
	session := activeSessionForMedia(t, active, media.ID)
	if session.Title != media.Title || session.LibraryID != media.LibraryID {
		t.Fatalf("unsealed session media = (%q, %q), want (%q, %q)", session.Title, session.LibraryID, media.Title, media.LibraryID)
	}
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	active, err = st.ActiveSessions(ctx, now, filter)
	if err != nil {
		t.Fatalf("ActiveSessions sealed: %v", err)
	}
	session = activeSessionForMedia(t, active, media.ID)
	if session.Title != "" || session.LibraryID != "" || session.DurationSeconds != 0 {
		t.Fatalf("sealed session exposed media details: %+v", session)
	}
}

func TestEncryptedLibraryStreamTokensStayNonIdentifyingAfterSeal(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "ENC DB Six Token Secret")
	defer st.Close()

	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-hls", media.ID, false, expires); err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	tokens, err := st.MyStreamTokens(ctx, "profile-default", now)
	if err != nil {
		t.Fatalf("MyStreamTokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("token count = %d, want 1", len(tokens))
	}
	if tokens[0].TargetKind != "media-hls" || !tokens[0].ExpiresAt.Equal(expires) {
		t.Fatalf("token = %+v, want target media-hls expiry %v", tokens[0], expires)
	}
}

func TestEncryptedLibraryPlaintextTargetSweepDetectsLeaks(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "sweep-target")
	defer st.Close()
	entity := Entity{ID: "entity-sweep-target", LibraryID: media.LibraryID, Kind: "widget", Title: "Sweep Target"}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	mediaIDs := []MediaID{media.ID}
	entityIDs := []EntityID{entity.ID}
	if leaks, err := plaintextEncryptedTargetLeaks(ctx, st, mediaIDs, entityIDs); err != nil || len(leaks) != 0 {
		t.Fatalf("clean encrypted import leaks = %v err=%v, want none", leaks, err)
	}

	if _, err := st.eventsW.ExecContext(ctx, `
		INSERT INTO engagement_event(id, profile_id, media_id, kind, occurred_at_ms, created_at_ms)
		VALUES ('leak-encrypted-media', 'profile-default', ?, 'watch', 1, 1)
	`, media.ID); err != nil {
		t.Fatalf("seed plaintext engagement leak: %v", err)
	}
	leaks, err := plaintextEncryptedTargetLeaks(ctx, st, mediaIDs, entityIDs)
	if err != nil {
		t.Fatalf("plaintextEncryptedTargetLeaks: %v", err)
	}
	if len(leaks) != 1 || leaks[0] != "events.engagement_event.media_id="+string(media.ID) {
		t.Fatalf("leaks = %v, want engagement_event media leak", leaks)
	}
}

func TestEncryptedLibraryEntityRelationUpsertReadAndSealBoundary(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "entity-relation-target")
	defer st.Close()

	source := Entity{ID: "entity-relation-source", LibraryID: media.LibraryID, Kind: "widget", Title: "Source"}
	target := Entity{ID: "entity-relation-target", LibraryID: media.LibraryID, Kind: "widget", Title: "Target"}
	for _, entity := range []Entity{source, target} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity(%s): %v", entity.ID, err)
		}
	}

	firstOrder, updatedOrder := 1, 2
	relations := []EntityRelation{
		{FromEntityID: source.ID, Name: "entity-target", ToEntityID: target.ID},
		{FromEntityID: source.ID, Name: "media-target", ToMediaID: media.ID},
	}
	for _, relation := range relations {
		relation.Attributes = map[string]string{"state": "first"}
		relation.SortOrder = &firstOrder
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatalf("first UpsertEntityRelation(%s): %v", relation.Name, err)
		}
		relation.Attributes = map[string]string{"state": "updated"}
		relation.SortOrder = &updatedOrder
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatalf("second UpsertEntityRelation(%s): %v", relation.Name, err)
		}
	}

	rows, err := st.EntityRelationRows(ctx, source.ID, "", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("EntityRelationRows unsealed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("relation rows = %d, want 2: %+v", len(rows), rows)
	}
	for _, row := range rows {
		if row.Attributes["state"] != "updated" || row.SortOrder == nil || *row.SortOrder != updatedOrder {
			t.Fatalf("relation %q was not updated in place: %+v", row.Name, row)
		}
		switch row.Name {
		case "entity-target":
			if row.ToEntity == nil || row.ToEntity.ID != target.ID || row.ToMedia != nil {
				t.Fatalf("entity relation target = %+v", row)
			}
		case "media-target":
			if row.ToMedia == nil || row.ToMedia.ID != media.ID || row.ToEntity != nil {
				t.Fatalf("media relation target = %+v", row)
			}
		default:
			t.Fatalf("unexpected relation row: %+v", row)
		}
	}
	if leaks, err := plaintextEncryptedTargetLeaks(
		ctx, st, []MediaID{media.ID}, []EntityID{source.ID, target.ID}); err != nil || len(leaks) != 0 {
		t.Fatalf("encrypted relation leaked to plaintext planes: leaks=%v err=%v", leaks, err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, relations[0]); err == nil {
		t.Fatal("sealed encrypted relation upsert succeeded")
	}
	if _, err := st.EntityRelationRows(ctx, source.ID, "", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("sealed encrypted relation read succeeded")
	}
}

func TestEncryptedLibraryEntityRelationIndexesUpgradeFromV4(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "encrypted.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before := migrationsBefore(t, encryptedLibraryMigrations, "migrate/encrypted_library", "0005")
	backupDir := filepath.Join(t.TempDir(), "backups")
	if err := migrate(ctx, slog.Default(), db, dbPath, encryptedLibraryDBLabel, before, "migrate/encrypted_library", backupDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO entity(id, library_id, kind, title) VALUES
			('source', 'library-encrypted', 'widget', 'Source'),
			('target', 'library-encrypted', 'widget', 'Target');
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec)
			VALUES ('media', 'library-encrypted', 'Media', 1, 'jpeg', 'jpeg');
		INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, attributes, sort_order)
			VALUES ('source', 'entity-target', 'target', '{"state":"first"}', 1);
		INSERT INTO entity_relation(from_entity_id, relation, to_media_id, attributes, sort_order)
			VALUES ('source', 'media-target', 'media', '{"state":"first"}', 1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, encryptedLibraryDBLabel, encryptedLibraryMigrations, "migrate/encrypted_library", backupDir); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"entity_relation_entity_uq", "entity_relation_media_uq",
		"entity_relation_to_entity_idx", "entity_relation_to_media_idx",
	} {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", name, count)
		}
	}
	statements := []string{
		`INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, attributes, sort_order)
		 VALUES ('source', 'entity-target', 'target', '{"state":"updated"}', 2)
		 ON CONFLICT(from_entity_id, relation, to_entity_id) WHERE to_entity_id IS NOT NULL
		 DO UPDATE SET attributes = excluded.attributes, sort_order = excluded.sort_order`,
		`INSERT INTO entity_relation(from_entity_id, relation, to_media_id, attributes, sort_order)
		 VALUES ('source', 'media-target', 'media', '{"state":"updated"}', 2)
		 ON CONFLICT(from_entity_id, relation, to_media_id) WHERE to_media_id IS NOT NULL
		 DO UPDATE SET attributes = excluded.attributes, sort_order = excluded.sort_order`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entity_relation WHERE attributes = '{"state":"updated"}' AND sort_order = 2`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("updated relation rows = %d, want 2", count)
	}
}

func TestEncryptedLibraryEntityWritesStayInsideSealBoundary(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "entity-boundary")
	defer st.Close()
	entity := Entity{ID: "entity-boundary", LibraryID: media.LibraryID, Kind: "widget", Title: "Before Seal"}

	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("UpsertEntity unsealed encrypted library: %v", err)
	}
	var plaintextRows int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM entity WHERE id = ?`, entity.ID).Scan(&plaintextRows); err != nil {
		t.Fatalf("count plaintext entity rows: %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	entity.Title = "After Seal"
	if err := st.UpsertEntity(ctx, entity); err == nil {
		t.Fatal("UpsertEntity after seal succeeded, want rejection")
	}
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM entity WHERE id = ?`, entity.ID).Scan(&plaintextRows); err != nil {
		t.Fatalf("count plaintext entity rows after seal: %v", err)
	}
	if plaintextRows != 0 {
		t.Fatalf("post-seal plaintext entity rows = %d, want 0", plaintextRows)
	}
}

func TestEncryptedLibraryImportsAllocateDistinctIDsForDuplicateFingerprints(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	first := LibraryID("library-local")
	second, err := st.CreateLibrary(ctx, "Encrypted B", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	for _, id := range []LibraryID{first, second.ID} {
		if err := st.SetLibraryBacking(ctx, id, false, true); err != nil {
			t.Fatalf("SetLibraryBacking %s: %v", id, err)
		}
		if err := st.UnsealEncryptedLibraryDB(ctx, id, copyEncryptedLibraryDBCipher{}); err != nil {
			t.Fatalf("UnsealEncryptedLibraryDB %s: %v", id, err)
		}
	}

	fp := "sha256:duplicate-encrypted-fingerprint"
	a, _, err := st.ImportMediaFile(ctx, encryptedImport(first, fp, "/enc/a.mkv"))
	if err != nil {
		t.Fatalf("Import first: %v", err)
	}
	b, _, err := st.ImportMediaFile(ctx, encryptedImport(second.ID, fp, "/enc/b.mkv"))
	if err != nil {
		t.Fatalf("Import second: %v", err)
	}
	if a.ID == b.ID {
		t.Fatalf("duplicate encrypted fingerprint reused media ID %q", a.ID)
	}
}

func TestEncryptedLibraryEntityFieldIndexReplaysToRegisteredDB(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	entity := Entity{
		ID:        "entity-encrypted-index",
		LibraryID: libraryID,
		Kind:      "widget",
		Title:     "Encrypted Widget",
		Fields: []EntityField{{
			Name: "summary",
			Type: EntityFieldText,
			Text: "needle-value",
		}},
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	registerWidgetKindPolicy(t, st) // widget opts into FullTextSearchable
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{{
		Kind:       "widget",
		Field:      "summary",
		Type:       EntityFieldText,
		Searchable: true,
		Filterable: true,
	}}); err != nil {
		t.Fatalf("RegisterEntityFieldIndexes: %v", err)
	}
	db := st.encryptedDBs[libraryID]
	var fields string
	if err := db.reader.QueryRowContext(ctx, `SELECT fields FROM entity_fts WHERE entity_id = ?`, entity.ID).Scan(&fields); err != nil {
		t.Fatalf("entity_fts fields: %v", err)
	}
	if fields != "needle-value" {
		t.Fatalf("entity_fts fields = %q, want needle-value", fields)
	}
	var indexName string
	if err := db.reader.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_entity_field_text_widget_summary'`).Scan(&indexName); err != nil {
		t.Fatalf("index replay missing: %v", err)
	}
}

// TestReconcileEntityFieldIndexesReconcilesEncryptedDB covers ADR-0104 §2 for an
// encrypted library: a removed field/Kind drops its index and clears its FTS
// contribution in the encrypted DB too, while the stored value stays inert.
func TestReconcileEntityFieldIndexesReconcilesEncryptedDB(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	registerWidgetKindPolicy(t, st) // widget opts into FullTextSearchable
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{{
		Kind: "widget", Field: "summary", Type: EntityFieldText, Searchable: true, Filterable: true,
	}}); err != nil {
		t.Fatalf("RegisterEntityFieldIndexes: %v", err)
	}
	entity := widgetEntity("entity-enc", libraryID)
	entity.Title = "Encrypted Widget"
	entity.Fields = []EntityField{{Name: "summary", Type: EntityFieldText, Text: "needle-value"}}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	db := st.encryptedDBs[libraryID]

	encIndexCount := func(name string) int {
		t.Helper()
		var count int
		if err := db.reader.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&count); err != nil {
			t.Fatalf("index count: %v", err)
		}
		return count
	}
	encFTSFields := func() string {
		t.Helper()
		var fields string
		if err := db.reader.QueryRowContext(ctx,
			`SELECT fields FROM entity_fts WHERE entity_id = ?`, entity.ID).Scan(&fields); err != nil {
			t.Fatalf("entity_fts fields: %v", err)
		}
		return fields
	}

	// Active surface present in the encrypted DB.
	if encIndexCount("idx_entity_field_text_widget_summary") != 1 {
		t.Fatal("active encrypted index missing")
	}
	if got := encFTSFields(); got != "needle-value" {
		t.Fatalf("encrypted entity_fts fields = %q, want needle-value", got)
	}

	// A later revision REMOVED the widget Kind: drop it from the active registry,
	// then reconcile. The encrypted DB's index + FTS contribution must clear.
	st.mu.Lock()
	delete(st.entityIndexes, "widget")
	st.mu.Unlock()
	if err := st.ReconcileEntityFieldIndexes(ctx); err != nil {
		t.Fatalf("ReconcileEntityFieldIndexes: %v", err)
	}
	if encIndexCount("idx_entity_field_text_widget_summary") != 0 {
		t.Fatal("retired encrypted index not dropped")
	}
	if got := encFTSFields(); got != "" {
		t.Fatalf("retired encrypted entity_fts fields = %q, want empty", got)
	}
	// Stored value stays inert in the encrypted DB.
	var count int
	if err := db.reader.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entity_field_text WHERE entity_id = ? AND field = 'summary'`, entity.ID).Scan(&count); err != nil {
		t.Fatalf("stored value count: %v", err)
	}
	if count == 0 {
		t.Fatal("encrypted summary value purged; must stay inert until Uninstall")
	}
}

// TestEncryptedLibraryReconcilesRetiredSurfaceOnUnseal covers the unseal half of
// ADR-0104 §2 for encrypted libraries: a revision that removed a field/Kind while
// the library was sealed must have its dead surface reconciled when the library is
// next unsealed (replayEntityFieldIndexesForEncryptedDBLocked), not only on boot.
func TestEncryptedLibraryReconcilesRetiredSurfaceOnUnseal(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	registerWidgetKindPolicy(t, st) // widget opts into FullTextSearchable
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{{
		Kind: "widget", Field: "summary", Type: EntityFieldText, Searchable: true, Filterable: true,
	}}); err != nil {
		t.Fatalf("RegisterEntityFieldIndexes: %v", err)
	}
	entity := widgetEntity("entity-unseal", libraryID)
	entity.Title = "Sealed Widget"
	entity.Fields = []EntityField{{Name: "summary", Type: EntityFieldText, Text: "buried-needle"}}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}

	indexCount := func() int {
		t.Helper()
		db := st.encryptedDBs[libraryID]
		var count int
		if err := db.reader.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_entity_field_text_widget_summary'`).Scan(&count); err != nil {
			t.Fatalf("index count: %v", err)
		}
		return count
	}
	if indexCount() != 1 {
		t.Fatal("active encrypted index missing before seal")
	}

	// Seal the library — its working DB (index + FTS included) round-trips into
	// ciphertext and the working dir is cleaned up.
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}

	// A revision change while sealed removed the widget Kind from the registry.
	st.mu.Lock()
	delete(st.entityIndexes, "widget")
	st.mu.Unlock()

	// Re-unseal: replayEntityFieldIndexesForEncryptedDBLocked reconciles the
	// restored DB down to the (now empty) active set — drop the retired index,
	// clear its FTS contribution — without touching the stored value.
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("re-unseal: %v", err)
	}
	if indexCount() != 0 {
		t.Fatal("retired encrypted index not dropped on unseal")
	}
	db := st.encryptedDBs[libraryID]
	var fields string
	if err := db.reader.QueryRowContext(ctx,
		`SELECT fields FROM entity_fts WHERE entity_id = ?`, entity.ID).Scan(&fields); err != nil {
		t.Fatalf("entity_fts fields: %v", err)
	}
	if fields != "" {
		t.Fatalf("retired encrypted entity_fts fields = %q, want empty after unseal", fields)
	}
	var stored int
	if err := db.reader.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entity_field_text WHERE entity_id = ? AND field = 'summary'`, entity.ID).Scan(&stored); err != nil {
		t.Fatalf("stored value count: %v", err)
	}
	if stored == 0 {
		t.Fatal("encrypted summary value purged on unseal; must stay inert until Uninstall")
	}
}

func TestEncryptedLibraryCrossLibraryRelationsRejected(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	first := LibraryID("library-local")
	second, err := st.CreateLibrary(ctx, "Encrypted Relation Target", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	for _, id := range []LibraryID{first, second.ID} {
		if err := st.SetLibraryBacking(ctx, id, false, true); err != nil {
			t.Fatalf("SetLibraryBacking %s: %v", id, err)
		}
		if err := st.UnsealEncryptedLibraryDB(ctx, id, copyEncryptedLibraryDBCipher{}); err != nil {
			t.Fatalf("UnsealEncryptedLibraryDB %s: %v", id, err)
		}
	}
	a, _, err := st.ImportMediaFile(ctx, encryptedImport(first, "sha256:relation-a", "/enc/relation-a.mkv"))
	if err != nil {
		t.Fatalf("Import first: %v", err)
	}
	b, _, err := st.ImportMediaFile(ctx, encryptedImport(second.ID, "sha256:relation-b", "/enc/relation-b.mkv"))
	if err != nil {
		t.Fatalf("Import second: %v", err)
	}
	if err := st.LinkMediaRelation(ctx, a.ID, MediaRelationEditionOf, b.ID); err == nil {
		t.Fatal("encrypted cross-library media relation succeeded, want rejection")
	}
	ea := Entity{ID: "entity-relation-a", LibraryID: first, Kind: "widget", Title: "A"}
	eb := Entity{ID: "entity-relation-b", LibraryID: second.ID, Kind: "widget", Title: "B"}
	if err := st.UpsertEntity(ctx, ea); err != nil {
		t.Fatalf("Upsert first entity: %v", err)
	}
	if err := st.UpsertEntity(ctx, eb); err != nil {
		t.Fatalf("Upsert second entity: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: ea.ID,
		Name:         "related",
		ToEntityID:   eb.ID,
		Cardinality:  "many",
	}); err == nil {
		t.Fatal("encrypted cross-library entity relation succeeded, want rejection")
	}
}

func TestPlaintextCrossLibraryMediaRelationStillAllowed(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	second, err := st.CreateLibrary(ctx, "Plain Relation Target", false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	a, _, err := st.ImportMediaFile(ctx, encryptedImport("library-local", "sha256:plain-relation-a", "/plain/relation-a.mkv"))
	if err != nil {
		t.Fatalf("Import first: %v", err)
	}
	b, _, err := st.ImportMediaFile(ctx, encryptedImport(second.ID, "sha256:plain-relation-b", "/plain/relation-b.mkv"))
	if err != nil {
		t.Fatalf("Import second: %v", err)
	}
	if err := st.LinkMediaRelation(ctx, a.ID, MediaRelationEditionOf, b.ID); err != nil {
		t.Fatalf("plaintext cross-library media relation: %v", err)
	}
}

func encryptedImport(libraryID LibraryID, fingerprint, path string) ImportMediaFile {
	return ImportMediaFile{
		LibraryID:           libraryID,
		Path:                path,
		ContentFingerprint:  fingerprint,
		Title:               "Encrypted Import",
		Container:           "matroska",
		Codec:               "h264",
		DurationSecondsHint: 60,
		Class:               MediaClassVideo,
	}
}

func seededEncryptedMedia(t *testing.T, title string) (*Store, Media) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		st.Close()
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		st.Close()
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	imported := encryptedImport(libraryID, "sha256:"+title, "/enc/"+title+".mkv")
	imported.Title = title
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		st.Close()
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return st, media
}

func sourceItemsContain(items []SourceItem, id MediaID) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func encryptedLibrarySourceItemIDs(items []SourceItem) []MediaID {
	ids := make([]MediaID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func activeSessionForMedia(t *testing.T, sessions []ActiveSession, mediaID MediaID) ActiveSession {
	t.Helper()
	for _, session := range sessions {
		if session.MediaID == mediaID {
			return session
		}
	}
	t.Fatalf("active session for media %s missing in %+v", mediaID, sessions)
	return ActiveSession{}
}

func plaintextEncryptedTargetLeaks(ctx context.Context, st *Store, mediaIDs []MediaID, entityIDs []EntityID) ([]string, error) {
	var leaks []string
	mediaChecks := []struct {
		label string
		db    *sql.DB
		query string
	}{
		{"catalog.media_field_text.media_id", st.catalogR, `SELECT 1 FROM media_field_text WHERE media_id = ? LIMIT 1`},
		{"catalog.media_field_real.media_id", st.catalogR, `SELECT 1 FROM media_field_real WHERE media_id = ? LIMIT 1`},
		{"catalog.media_field_int.media_id", st.catalogR, `SELECT 1 FROM media_field_int WHERE media_id = ? LIMIT 1`},
		{"catalog.media_field_date.media_id", st.catalogR, `SELECT 1 FROM media_field_date WHERE media_id = ? LIMIT 1`},
		{"catalog.media_relation.from_media_id", st.catalogR, `SELECT 1 FROM media_relation WHERE from_media_id = ? LIMIT 1`},
		{"catalog.media_relation.to_media_id", st.catalogR, `SELECT 1 FROM media_relation WHERE to_media_id = ? LIMIT 1`},
		{"events.playback_progress.media_id", st.eventsR, `SELECT 1 FROM playback_progress WHERE media_id = ? LIMIT 1`},
		{"events.media_favorite.media_id", st.eventsR, `SELECT 1 FROM media_favorite WHERE media_id = ? LIMIT 1`},
		{"events.queue.media_id", st.eventsR, `SELECT 1 FROM ` + ("play" + "_queue") + ` WHERE media_id = ? LIMIT 1`},
		{"events.stream_token_ledger.media_id", st.eventsR, `SELECT 1 FROM stream_token_ledger WHERE media_id = ? LIMIT 1`},
		{"events.engagement_event.media_id", st.eventsR, `SELECT 1 FROM engagement_event WHERE media_id = ? LIMIT 1`},
		{"events.event_outbox.media.aggregate_id", st.eventsR, `SELECT 1 FROM event_outbox WHERE aggregate_type = 'media' AND aggregate_id = ? LIMIT 1`},
		{"events.event_outbox.payload_json", st.eventsR, `SELECT 1 FROM event_outbox WHERE payload_json LIKE '%' || ? || '%' LIMIT 1`},
		{"events.job.payload_json", st.eventsR, `SELECT 1 FROM job WHERE payload_json LIKE '%' || ? || '%' LIMIT 1`},
		{"events.job_checkpoint.checkpoint_json", st.eventsR, `SELECT 1 FROM job_checkpoint WHERE checkpoint_json LIKE '%' || ? || '%' LIMIT 1`},
		{"events.job_event.payload_json", st.eventsR, `SELECT 1 FROM job_event WHERE payload_json LIKE '%' || ? || '%' LIMIT 1`},
	}
	for _, id := range mediaIDs {
		for _, check := range mediaChecks {
			leak, err := plaintextEncryptedTargetPresent(ctx, check.db, check.query, string(id))
			if err != nil {
				return nil, err
			}
			if leak {
				leaks = append(leaks, fmt.Sprintf("%s=%s", check.label, id))
			}
		}
	}
	entityChecks := []struct {
		label string
		db    *sql.DB
		query string
	}{
		{"catalog.entity_field_text.entity_id", st.catalogR, `SELECT 1 FROM entity_field_text WHERE entity_id = ? LIMIT 1`},
		{"catalog.entity_field_real.entity_id", st.catalogR, `SELECT 1 FROM entity_field_real WHERE entity_id = ? LIMIT 1`},
		{"catalog.entity_field_int.entity_id", st.catalogR, `SELECT 1 FROM entity_field_int WHERE entity_id = ? LIMIT 1`},
		{"catalog.entity_field_date.entity_id", st.catalogR, `SELECT 1 FROM entity_field_date WHERE entity_id = ? LIMIT 1`},
		{"catalog.entity_relation.from_entity_id", st.catalogR, `SELECT 1 FROM entity_relation WHERE from_entity_id = ? LIMIT 1`},
		{"catalog.entity_relation.to_entity_id", st.catalogR, `SELECT 1 FROM entity_relation WHERE to_entity_id = ? LIMIT 1`},
		{"events.engagement_event.entity_id", st.eventsR, `SELECT 1 FROM engagement_event WHERE entity_id = ? LIMIT 1`},
		{"events.event_outbox.payload_json", st.eventsR, `SELECT 1 FROM event_outbox WHERE payload_json LIKE '%' || ? || '%' LIMIT 1`},
		{"events.job.payload_json", st.eventsR, `SELECT 1 FROM job WHERE payload_json LIKE '%' || ? || '%' LIMIT 1`},
		{"events.job_checkpoint.checkpoint_json", st.eventsR, `SELECT 1 FROM job_checkpoint WHERE checkpoint_json LIKE '%' || ? || '%' LIMIT 1`},
		{"events.job_event.payload_json", st.eventsR, `SELECT 1 FROM job_event WHERE payload_json LIKE '%' || ? || '%' LIMIT 1`},
	}
	for _, id := range entityIDs {
		for _, check := range entityChecks {
			leak, err := plaintextEncryptedTargetPresent(ctx, check.db, check.query, string(id))
			if err != nil {
				return nil, err
			}
			if leak {
				leaks = append(leaks, fmt.Sprintf("%s=%s", check.label, id))
			}
		}
	}
	return leaks, nil
}

func plaintextEncryptedTargetPresent(ctx context.Context, db *sql.DB, query, target string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, query, target).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func assertEncryptedLibraryMigrated(t *testing.T, db *sql.DB) {
	t.Helper()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != schemaFreezeEncryptedLibraryMigrations {
		t.Fatalf("user_version = %d, want %d", version, schemaFreezeEncryptedLibraryMigrations)
	}
	if !tableExists(t, db, "encrypted_library_metadata") {
		t.Fatal("encrypted_library_metadata table missing")
	}
	var applied int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE name = '0001_initial.sql'").Scan(&applied); err != nil {
		t.Fatalf("schema_migrations count: %v", err)
	}
	if applied != 1 {
		t.Fatalf("encrypted-library migration rows = %d, want 1", applied)
	}
}

// recordingDerivativeReconciler captures the unseal→register reconcile calls so a
// test can assert the snapshot-before-flip inventory and (optionally) fail closed.
type recordingDerivativeReconciler struct {
	calls   []reconcileCall
	failErr error
}

type reconcileCall struct {
	libraryID LibraryID
	liveKeys  []MediaDerivativeKey
}

func (r *recordingDerivativeReconciler) ReconcileEncryptedDerivatives(ctx context.Context, libraryID LibraryID, live []MediaDerivativeKey) error {
	r.calls = append(r.calls, reconcileCall{libraryID: libraryID, liveKeys: append([]MediaDerivativeKey(nil), live...)})
	return r.failErr
}

func TestEncryptedLibraryUnsealFiresDerivativeReconcileWithSnapshotInventory(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	first := encryptedImport(libraryID, "sha256:reconcile-a", "/enc/reconcile-a.mkv")
	mediaA, _, err := st.ImportMediaFile(ctx, first)
	if err != nil {
		t.Fatalf("ImportMediaFile a: %v", err)
	}
	second := encryptedImport(libraryID, "sha256:reconcile-b", "/enc/reconcile-b.mkv")
	mediaB, _, err := st.ImportMediaFile(ctx, second)
	if err != nil {
		t.Fatalf("ImportMediaFile b: %v", err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}

	reconciler := &recordingDerivativeReconciler{}
	st.SetEncryptedDerivativeReconciler(reconciler)
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("re-unseal: %v", err)
	}
	if len(reconciler.calls) != 1 {
		t.Fatalf("reconcile calls = %d, want 1", len(reconciler.calls))
	}
	call := reconciler.calls[0]
	if call.libraryID != libraryID {
		t.Fatalf("reconcile libraryID = %q, want %q", call.libraryID, libraryID)
	}
	got := map[MediaID][]string{}
	for _, key := range call.liveKeys {
		got[key.MediaID] = key.Fingerprints
	}
	if _, okA := got[mediaA.ID]; len(got) != 2 || !okA {
		t.Fatalf("reconcile inventory = %v, want {%q, %q}", call.liveKeys, mediaA.ID, mediaB.ID)
	}
	// The snapshot must carry each Media's content fingerprint so the reconciler
	// can address the fingerprint-keyed derivative classes (generated assets,
	// subtitle sidecars) it otherwise could not join back to this Library.
	if fps := got[mediaA.ID]; len(fps) != 1 || fps[0] != "sha256:reconcile-a" {
		t.Fatalf("mediaA fingerprints = %v, want [sha256:reconcile-a]", fps)
	}
	if fps := got[mediaB.ID]; len(fps) != 1 || fps[0] != "sha256:reconcile-b" {
		t.Fatalf("mediaB fingerprints = %v, want [sha256:reconcile-b]", fps)
	}
}

func TestEncryptedLibraryUnsealFailsClosedWhenDerivativeReconcileErrors(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	reconcileErr := errors.New("cannot re-protect orphaned derivatives")
	st.SetEncryptedDerivativeReconciler(&recordingDerivativeReconciler{failErr: reconcileErr})

	err = st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{})
	if !errors.Is(err, reconcileErr) {
		t.Fatalf("UnsealEncryptedLibraryDB error = %v, want %v", err, reconcileErr)
	}
	if st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("library registered despite failed derivative reconcile, want Sealed")
	}
	if _, ok := st.encryptedDBs[libraryID]; ok {
		t.Fatal("encrypted DB handle retained after fail-closed unseal, want unwound")
	}
	paths := st.encryptedLibraryDBPaths(libraryID)
	leftovers, err := encryptedLibraryPlaintextLeftovers(filepath.Dir(paths.working), paths.working)
	if err != nil {
		t.Fatalf("leftover scan: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("plaintext working leftovers after fail-closed unseal = %v, want none", leftovers)
	}
}

func TestEncryptedLibraryUnsealWithoutReconcilerIsUnchanged(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	if !st.EncryptedLibraryDBRegistered(libraryID) {
		t.Fatal("library not registered with nil reconciler (default), want registered")
	}
}

// purgingDerivativeReconciler removes a real on-disk file named after each live
// Media in a scratch derivative root — a stand-in for the app-layer purge — so a
// transition-level test can prove the unseal→register flip actually drives a
// purge of an orphaned plaintext derivative (not just that the seam is invoked).
type purgingDerivativeReconciler struct {
	root string
}

func (r *purgingDerivativeReconciler) ReconcileEncryptedDerivatives(ctx context.Context, libraryID LibraryID, live []MediaDerivativeKey) error {
	for _, key := range live {
		if err := os.Remove(filepath.Join(r.root, string(key.MediaID)+".plain")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// TestEncryptedLibraryUnsealPurgesOrphanedPlaintextDerivative is the ENCPRIV-10d
// acceptance test at the store transition boundary: it writes a plaintext
// derivative for an Encrypted Library's Media (a pre-crash orphan), performs the
// unseal→register transition with a reconciler installed, and asserts the orphan
// is removed. The nil-reconciler control proves the removal is the reconciler's
// doing — with the seam disabled the orphan SURVIVES, so this test fails if the
// reconciler is ever silently dropped.
func TestEncryptedLibraryUnsealPurgesOrphanedPlaintextDerivative(t *testing.T) {
	ctx := context.Background()

	seedOrphan := func(t *testing.T) (*Store, LibraryID, MediaID, string) {
		t.Helper()
		st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		libraryID := LibraryID("library-local")
		if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
			t.Fatalf("SetLibraryBacking: %v", err)
		}
		if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
			t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
		}
		media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, "sha256:orphan", "/enc/orphan.mkv"))
		if err != nil {
			t.Fatalf("ImportMediaFile: %v", err)
		}
		if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
			t.Fatalf("SealEncryptedLibraryDB: %v", err)
		}
		root := t.TempDir()
		orphan := filepath.Join(root, string(media.ID)+".plain")
		if err := os.WriteFile(orphan, []byte("leaked plaintext derivative"), 0o600); err != nil {
			t.Fatalf("seed orphan: %v", err)
		}
		return st, libraryID, media.ID, orphan
	}

	t.Run("reconciler purges the orphan", func(t *testing.T) {
		st, libraryID, mediaID, orphan := seedOrphan(t)
		defer st.Close()
		st.SetEncryptedDerivativeReconciler(&purgingDerivativeReconciler{root: filepath.Dir(orphan)})
		if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
			t.Fatalf("re-unseal: %v", err)
		}
		if !st.EncryptedLibraryDBRegistered(libraryID) {
			t.Fatal("library not registered after successful reconcile")
		}
		if _, err := os.Stat(orphan); !os.IsNotExist(err) {
			t.Fatalf("orphaned plaintext derivative for %s survived reconcile (stat err=%v), want removed", mediaID, err)
		}
	})

	t.Run("nil reconciler leaves the orphan (control)", func(t *testing.T) {
		st, libraryID, _, orphan := seedOrphan(t)
		defer st.Close()
		if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
			t.Fatalf("re-unseal: %v", err)
		}
		if _, err := os.Stat(orphan); err != nil {
			t.Fatalf("orphan must survive with the reconciler disabled, but stat failed: %v", err)
		}
	})
}

func TestWipeOrRemoveFileWipesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plaintext.db")
	if err := os.WriteFile(path, []byte("secret-bytes"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := wipeOrRemoveFile(path); err != nil {
		t.Fatalf("wipeOrRemoveFile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file must be removed, stat err=%v", err)
	}
}

func TestWipeOrRemoveFileSurfacesOpenErrorButStillRemoves(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not block opens")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "unwritable.db")
	if err := os.WriteFile(path, []byte("secret-bytes"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	err := wipeOrRemoveFile(path)
	if err == nil {
		t.Fatal("expected wipeOrRemoveFile to surface the open-for-wipe failure, got nil error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("file must still be removed despite failed wipe, stat err=%v", statErr)
	}
}
