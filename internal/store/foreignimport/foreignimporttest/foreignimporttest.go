// Package foreignimporttest builds minimal foreign-server SQLite fixture
// databases for tests of Migration Wizard sources such as Film & TV
// and Podcast export DBs. It
// lives under internal/store/ so it may open a SQLite file — the conformance
// storage-import gate confines database/sql + modernc.org/sqlite to
// internal/store/**, and a test in internal/filmtvimport (outside the store
// subtree) cannot import the driver. It is the foreign-DB analogue of
// internal/store/storetest: test-support code, not part of the storage engine.
package foreignimporttest

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	_ "modernc.org/sqlite"
)

func exec(t *testing.T, path string, stmts []string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture %s: %v", path, err)
	}
	defer db.Close()
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec fixture stmt %q: %v", stmt, err)
		}
	}
	return path
}

// WriteVersionedSQLiteFixture writes a minimal SQLite database with only
// PRAGMA user_version set. It is for compatibility-gate tests that must fail
// before a foreign importer reads any source rows.
func WriteVersionedSQLiteFixture(t *testing.T, dir, name string, userVersion int) string {
	t.Helper()
	return exec(t, filepath.Join(dir, name), []string{
		`PRAGMA user_version = ` + strconv.Itoa(userVersion),
	})
}

// WritePlexFixture writes a minimal Plex `com.plexapp.plugins.library.db` to a
// temp file and returns its path: a completed item (view_count > 0 + a
// last_viewed_at instant) at watchedPath and an in-progress item (view_offset
// in milliseconds) at resumePath, plus an untouched item the reader's WHERE
// clause excludes. The watched item's last_viewed_at is the fixed unix instant
// 1614834367 so a caller can assert the historical timestamp.
func WritePlexFixture(t *testing.T, dir, watchedPath, resumePath string) string {
	t.Helper()
	return exec(t, filepath.Join(dir, "plex.db"), []string{
		`PRAGMA user_version = 1`,
		`CREATE TABLE metadata_items (id INTEGER PRIMARY KEY, guid TEXT, duration INTEGER)`,
		`CREATE TABLE media_items (id INTEGER PRIMARY KEY, metadata_item_id INTEGER, duration INTEGER)`,
		`CREATE TABLE media_parts (id INTEGER PRIMARY KEY, media_item_id INTEGER, file TEXT)`,
		`CREATE TABLE metadata_item_settings (id INTEGER PRIMARY KEY, guid TEXT, view_offset INTEGER, view_count INTEGER, last_viewed_at INTEGER)`,
		`INSERT INTO metadata_items VALUES (1, 'plex://item/aaa', 5400000)`,
		`INSERT INTO media_items VALUES (10, 1, 5400000)`,
		`INSERT INTO media_parts VALUES (100, 10, '` + watchedPath + `')`,
		`INSERT INTO metadata_item_settings VALUES (1, 'plex://item/aaa', 0, 2, 1614834367)`,
		`INSERT INTO metadata_items VALUES (2, 'plex://item/bbb', 6000000)`,
		`INSERT INTO media_items VALUES (20, 2, 6000000)`,
		`INSERT INTO media_parts VALUES (200, 20, '` + resumePath + `')`,
		`INSERT INTO metadata_item_settings VALUES (2, 'plex://item/bbb', 120000, 0, 0)`,
		`INSERT INTO metadata_items VALUES (3, 'plex://item/ccc', 100000)`,
		`INSERT INTO media_items VALUES (30, 3, 100000)`,
		`INSERT INTO media_parts VALUES (300, 30, '/media/films/Untouched.mkv')`,
	})
}

// WriteAntennaPodFixture writes a minimal AntennaPod export database containing
// one feed-scoped item-state row.
func WriteAntennaPodFixture(t *testing.T, dir, feedURL, guid string, played bool, position, duration int) string {
	t.Helper()
	playedInt := 0
	if played {
		playedInt = 1
	}
	return exec(t, filepath.Join(dir, "antennapod.db"), []string{
		`CREATE TABLE Feeds (id INTEGER PRIMARY KEY, download_url TEXT NOT NULL)`,
		`CREATE TABLE FeedItems (id INTEGER PRIMARY KEY, feed INTEGER NOT NULL, item_identifier TEXT NOT NULL, read INTEGER NOT NULL)`,
		`CREATE TABLE FeedMedia (id INTEGER PRIMARY KEY, feeditem INTEGER NOT NULL, position INTEGER NOT NULL, duration INTEGER NOT NULL)`,
		`INSERT INTO Feeds(id, download_url) VALUES (101, '` + feedURL + `')`,
		`INSERT INTO FeedItems(id, feed, item_identifier, read) VALUES (1, 101, '` + guid + `', ` + strconv.Itoa(playedInt) + `)`,
		`INSERT INTO FeedMedia(id, feeditem, position, duration) VALUES (11, 1, ` + strconv.Itoa(position) + `, ` + strconv.Itoa(duration) + `)`,
	})
}
