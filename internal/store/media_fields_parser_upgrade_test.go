package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// legacyParserUpgradePlane is one catalog plane holding a Media written by an
// older build: a scan-tier parse result next to curated, user, AI and artwork
// values on the same Media.
type legacyParserUpgradePlane struct {
	st     *Store
	id     MediaID
	writer func() *sql.DB
	reader func() *sql.DB
}

// seedLegacyParserFields writes the Media's fields through the writers an
// older build used, before parser_owned existed.
func seedLegacyParserFields(t *testing.T, p legacyParserUpgradePlane) {
	t.Helper()
	ctx := context.Background()
	write := func(source MutationSource, fields ...MediaField) {
		t.Helper()
		if err := p.st.UpsertMediaFieldsWithSource(ctx, p.id, fields, source); err != nil {
			t.Fatalf("seed %s: %v", source, err)
		}
	}
	// The older parse: a movie class, a wrong episode index and a field the
	// corrected parser no longer writes.
	write(MutationSourceScan,
		MediaField{Name: "class", Type: EntityFieldText, Text: "movie"},
		MediaField{Name: "index", Type: EntityFieldNumber, Number: 101},
		MediaField{Name: "edition-note", Type: EntityFieldText, Text: "S1E1"},
	)
	write(MutationSourceManual, MediaField{Name: "mood", Type: EntityFieldText, Text: "calm"})
	write(MutationSourceOperator, MediaField{Name: "tagline", Type: EntityFieldText, Text: "curated"})
	// Artwork's scan-tier flag.
	write(MutationSourceScan, MediaField{Name: "preview-unavailable", Type: EntityFieldFlag, Flag: true})
	// An approved AI tag lands without a provenance row.
	if _, err := p.writer().ExecContext(ctx,
		"INSERT INTO media_field_text(media_id, field, field_type, value) VALUES (?, 'ai-genre', 'text', 'noir')", p.id); err != nil {
		t.Fatal(err)
	}
}

// rollBackParserOwnedMigration returns the database at path to its schema
// before the parser_owned migration, as an older build left it.
func rollBackParserOwnedMigration(t *testing.T, path, migration string) {
	t.Helper()
	ctx := context.Background()
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"ALTER TABLE media_field_provenance DROP COLUMN parser_owned",
		"DELETE FROM schema_migrations WHERE name = '" + migration + "'",
		fmt.Sprintf("PRAGMA user_version = %d", version-1),
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func (p legacyParserUpgradePlane) marker(t *testing.T, name string) (int, bool) {
	t.Helper()
	var marker int
	err := p.reader().QueryRowContext(context.Background(),
		"SELECT parser_owned FROM media_field_provenance WHERE media_id = ? AND field = ?", p.id, name).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("parser_owned(%s): %v", name, err)
	}
	return marker, true
}

// upgradeCatalogWithLegacyParserFields upgrades a catalog an older build
// wrote across catalog migration 0123.
func upgradeCatalogWithLegacyParserFields(t *testing.T) legacyParserUpgradePlane {
	t.Helper()
	ctx := context.Background()
	dataDir := migratedDataDir(t)
	st, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	p := legacyParserUpgradePlane{st: st, id: "media-alpha",
		writer: func() *sql.DB { return st.catalogW }, reader: func() *sql.DB { return st.catalogR }}
	seedLegacyParserFields(t, p)
	file := filepath.Join(t.TempDir(), "alpha.mkv")
	if err := os.WriteFile(file, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, p.id, file); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	rollBackParserOwnedMigration(t, filepath.Join(dataDir, "db", "catalog.db"), "0123_media_field_parser_owned.sql")
	upgraded, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	p.st = upgraded
	p.writer = func() *sql.DB { return upgraded.catalogW }
	p.reader = func() *sql.DB { return upgraded.catalogR }
	return p
}

// upgradeEncryptedLibraryWithLegacyParserFields upgrades an encrypted-library
// database an older build sealed across encrypted_library migration 0009.
func upgradeEncryptedLibraryWithLegacyParserFields(t *testing.T) legacyParserUpgradePlane {
	t.Helper()
	ctx := context.Background()
	const libraryID LibraryID = "library-local"
	st, media := seededEncryptedMedia(t, "legacy-parser")
	t.Cleanup(func() { _ = st.Close() })
	p := legacyParserUpgradePlane{st: st, id: media.ID,
		writer: func() *sql.DB { return st.encryptedDBs[libraryID].writer },
		reader: func() *sql.DB { return st.encryptedDBs[libraryID].reader }}
	seedLegacyParserFields(t, p)
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	rollBackParserOwnedMigration(t, st.encryptedLibraryDBPaths(libraryID).ciphertext, "0009_media_field_parser_owned.sql")
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	return p
}

// TestParserOwnedUpgradeReconcilesLegacyParserFields: after the upgrade, the
// first authoritative parse of a file no other scan-tier writer touches
// corrects the older parse (index 101 becomes 1, the stale field goes, the
// movie class becomes an episode), and every curated, user, AI and artwork
// value on the same Media survives exactly, in both catalog planes
// (CORE-2554).
func TestParserOwnedUpgradeReconcilesLegacyParserFields(t *testing.T) {
	for name, upgrade := range map[string]func(*testing.T) legacyParserUpgradePlane{
		"catalog":           upgradeCatalogWithLegacyParserFields,
		"encrypted_library": upgradeEncryptedLibraryWithLegacyParserFields,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			p := upgrade(t)
			assertLegacyParserMarkers(t, p)

			// The corrected parse, which also emits names other writers hold.
			corrected := []MediaField{
				{Name: "class", Type: EntityFieldText, Text: "episode"},
				{Name: "index", Type: EntityFieldNumber, Number: 1},
				{Name: "mood", Type: EntityFieldText, Text: "parsed"},
				{Name: "tagline", Type: EntityFieldText, Text: "parsed"},
				{Name: "ai-genre", Type: EntityFieldText, Text: "parsed"},
			}
			// A parse without the probe leaves the older values alone.
			if err := p.st.RefreshParserMediaFields(ctx, p.id, ParserFieldRefresh{Fields: corrected, LegacyScanOwned: true}); err != nil {
				t.Fatal(err)
			}
			if got := parserFieldsByName(t, p.st, p.id)["index"].Number; got != 101 {
				t.Fatalf("unprobed parse index = %v, want legacy 101 kept", got)
			}
			if got, _ := p.marker(t, "index"); got != parserOwnedLegacyScan {
				t.Fatalf("unprobed parse index marker = %d, want %d", got, parserOwnedLegacyScan)
			}

			if err := p.st.RefreshParserMediaFields(ctx, p.id, ParserFieldRefresh{
				Fields: corrected, Authoritative: true, LegacyScanOwned: true,
			}); err != nil {
				t.Fatal(err)
			}
			fields := parserFieldsByName(t, p.st, p.id)
			if got := fields["index"].Number; got != 1 {
				t.Fatalf("index = %v, want corrected 1", got)
			}
			if got := fields["class"].Text; got != "episode" {
				t.Fatalf("class = %q, want episode", got)
			}
			if field, ok := fields["edition-note"]; ok {
				t.Fatalf("stale parser field survived: %#v", field)
			}
			if _, ok := p.marker(t, "edition-note"); ok {
				t.Fatal("stale parser field kept its provenance row")
			}
			for _, name := range []string{"index", "class"} {
				if got, _ := p.marker(t, name); got != parserOwnedStable {
					t.Fatalf("%s marker = %d, want %d", name, got, parserOwnedStable)
				}
			}
			assertNonParserValuesKept(t, p, fields)
		})
	}
}

// TestParserOwnedUpgradeSettlesLegacyScanValuesItCannotVouchFor: where
// hydration or image local metadata may have written the older scan-tier
// values, the authoritative parse keeps them and stops tracking them; the
// legacy class is still reconciled (CORE-2554).
func TestParserOwnedUpgradeSettlesLegacyScanValuesItCannotVouchFor(t *testing.T) {
	for name, upgrade := range map[string]func(*testing.T) legacyParserUpgradePlane{
		"catalog":           upgradeCatalogWithLegacyParserFields,
		"encrypted_library": upgradeEncryptedLibraryWithLegacyParserFields,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			p := upgrade(t)
			assertLegacyParserMarkers(t, p)
			refresh := ParserFieldRefresh{Fields: []MediaField{
				{Name: "class", Type: EntityFieldText, Text: "episode"},
				{Name: "index", Type: EntityFieldNumber, Number: 1},
			}, Authoritative: true}
			if err := p.st.RefreshParserMediaFields(ctx, p.id, refresh); err != nil {
				t.Fatal(err)
			}
			fields := parserFieldsByName(t, p.st, p.id)
			if got := fields["index"].Number; got != 101 {
				t.Fatalf("index = %v, want 101 kept", got)
			}
			if got := fields["edition-note"].Text; got != "S1E1" {
				t.Fatalf("edition-note = %q, want kept", got)
			}
			if got := fields["class"].Text; got != "episode" {
				t.Fatalf("class = %q, want episode", got)
			}
			for _, name := range []string{"index", "edition-note"} {
				if got, ok := p.marker(t, name); !ok || got != parserOwnedNone {
					t.Fatalf("%s marker = %d (row %v), want settled %d", name, got, ok, parserOwnedNone)
				}
			}
			// Settled values stay out of reach of later parses.
			refresh.LegacyScanOwned = true
			if err := p.st.RefreshParserMediaFields(ctx, p.id, refresh); err != nil {
				t.Fatal(err)
			}
			fields = parserFieldsByName(t, p.st, p.id)
			if got := fields["index"].Number; got != 101 {
				t.Fatalf("settled index = %v, want 101 kept", got)
			}
			if _, ok := fields["edition-note"]; !ok {
				t.Fatal("settled edition-note removed by a later parse")
			}
			assertNonParserValuesKept(t, p, fields)
		})
	}
}

// TestParserOwnedUpgradeSettlesFieldsTheHydratorShares: a field the Library's
// hydrator names is kept and settled even where the scan vouches for the
// other older scan-tier values, which the same parse reconciles (CORE-2554).
func TestParserOwnedUpgradeSettlesFieldsTheHydratorShares(t *testing.T) {
	for name, upgrade := range map[string]func(*testing.T) legacyParserUpgradePlane{
		"catalog":           upgradeCatalogWithLegacyParserFields,
		"encrypted_library": upgradeEncryptedLibraryWithLegacyParserFields,
	} {
		t.Run(name, func(t *testing.T) {
			p := upgrade(t)
			if err := p.st.RefreshParserMediaFields(context.Background(), p.id, ParserFieldRefresh{
				Fields:           []MediaField{{Name: "index", Type: EntityFieldNumber, Number: 1}},
				Authoritative:    true,
				LegacyScanOwned:  true,
				LegacyScanShared: map[string]bool{"edition-note": true},
			}); err != nil {
				t.Fatal(err)
			}
			fields := parserFieldsByName(t, p.st, p.id)
			if got := fields["index"].Number; got != 1 {
				t.Fatalf("index = %v, want corrected 1", got)
			}
			if got := fields["edition-note"].Text; got != "S1E1" {
				t.Fatalf("shared edition-note = %q, want kept", got)
			}
			if got, ok := p.marker(t, "edition-note"); !ok || got != parserOwnedNone {
				t.Fatalf("shared edition-note marker = %d (row %v), want settled %d", got, ok, parserOwnedNone)
			}
			assertNonParserValuesKept(t, p, fields)
		})
	}
}

func assertLegacyParserMarkers(t *testing.T, p legacyParserUpgradePlane) {
	t.Helper()
	for name, want := range map[string]int{
		"class":               parserOwnedLegacy,
		"index":               parserOwnedLegacyScan,
		"edition-note":        parserOwnedLegacyScan,
		"mood":                parserOwnedNone,
		"tagline":             parserOwnedNone,
		"preview-unavailable": parserOwnedNone,
	} {
		if got, ok := p.marker(t, name); !ok || got != want {
			t.Fatalf("upgraded %s marker = %d (row %v), want %d", name, got, ok, want)
		}
	}
	if _, ok := p.marker(t, "ai-genre"); ok {
		t.Fatal("upgrade recorded provenance for an approved AI value")
	}
	paths, err := p.st.LibraryFilePathsWithLegacyParserFields(context.Background(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("upgraded Media not listed for its one-off probe")
	}
}

func assertNonParserValuesKept(t *testing.T, p legacyParserUpgradePlane, fields map[string]MediaField) {
	t.Helper()
	for name, want := range map[string]string{
		"mood": "calm", "tagline": "curated", "ai-genre": "noir",
	} {
		if got := fields[name].Text; got != want {
			t.Fatalf("%s = %q, want %q kept", name, got, want)
		}
	}
	if !fields["preview-unavailable"].Flag {
		t.Fatal("artwork flag lost")
	}
}

// TestLibraryFilePathsWithLegacyParserFieldsListsLegacyScanValues: Media
// holding only an older scan-tier value is probed once on the fast path, so
// its next parse is authoritative and settles it (CORE-2554).
func TestLibraryFilePathsWithLegacyParserFieldsListsLegacyScanValues(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const id MediaID = "media-alpha"
	if err := st.UpsertMediaFields(ctx, id, []MediaField{{Name: "index", Type: EntityFieldNumber, Number: 101}}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "alpha.mkv")
	if err := os.WriteFile(file, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, id, file); err != nil {
		t.Fatal(err)
	}
	paths, err := st.LibraryFilePathsWithLegacyParserFields(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("paths before marking = %v, want none", paths)
	}
	if _, err := st.catalogW.ExecContext(ctx,
		"UPDATE media_field_provenance SET parser_owned = ? WHERE media_id = ? AND field = 'index'", parserOwnedLegacyScan, id); err != nil {
		t.Fatal(err)
	}
	paths, err = st.LibraryFilePathsWithLegacyParserFields(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := paths[file]; !ok {
		t.Fatalf("legacy file paths = %v, want %s", paths, file)
	}
}
