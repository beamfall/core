package store

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestMediaInsertSeqMigrationBackfillAndPersistence(t *testing.T) {
	for _, plane := range []struct {
		name       string
		migrations fs.FS
		ordinal    string
	}{
		{"catalog", catalogMigrations, "0123"},
		{"encrypted_library", encryptedLibraryMigrations, "0009"},
	} {
		t.Run(plane.name, func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "catalog.db")
			backupDir := t.TempDir()
			db, err := openSQLite(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			dir := "migrate/" + plane.name
			before := migrationsBefore(t, plane.migrations, dir, plane.ordinal)
			if err := migrate(ctx, slog.Default(), db, dbPath, plane.name, before, dir, backupDir); err != nil {
				t.Fatal(err)
			}
			if plane.name == "catalog" {
				if _, err := db.ExecContext(ctx, `INSERT INTO library(id, title) VALUES ('library-test', 'Test')`); err != nil {
					t.Fatal(err)
				}
			}
			// Sparse rowids deliberately disagree with lexical ID order and with
			// the new sequence range, as a real upgraded catalog can do.
			if _, err := db.ExecContext(ctx, `INSERT INTO media(rowid, id, library_id, title, duration_seconds, container, codec) VALUES
				(10, 'z-first', 'library-test', 'First', 60, 'mp4', 'h264'),
				(100, 'a-second', 'library-test', 'Second', 60, 'mp4', 'h264')`); err != nil {
				t.Fatal(err)
			}
			if err := migrate(ctx, slog.Default(), db, dbPath, plane.name, plane.migrations, dir, backupDir); err != nil {
				t.Fatal(err)
			}
			for i, id := range []string{"z-first", "a-second"} {
				var seq int
				if err := db.QueryRowContext(ctx, `SELECT seq FROM media_insert_seq WHERE media_id = ?`, id).Scan(&seq); err != nil || seq != i+1 {
					t.Fatalf("backfill %s = %d, %v", id, seq, err)
				}
			}
			var originalRowID int
			if err := db.QueryRowContext(ctx, `SELECT rowid FROM media WHERE id = 'a-second'`).Scan(&originalRowID); err != nil || originalRowID != 100 {
				t.Fatalf("media rowid changed: %d, %v", originalRowID, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE media SET title = 'Updated' WHERE id = 'a-second'; DELETE FROM media`); err != nil {
				t.Fatal(err)
			}
			var count, watermark int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM media_insert_seq`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("purge left sequence rows: %d, %v", count, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'media_insert_seq'`).Scan(&watermark); err != nil || watermark != 2 {
				t.Fatalf("purge lost watermark: %d, %v", watermark, err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = openSQLite(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			// Reapplying migrations must leave the counter alone across restart.
			if err := migrate(ctx, slog.Default(), db, dbPath, plane.name, plane.migrations, dir, backupDir); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO media(id, library_id, title, duration_seconds, container, codec)
				VALUES ('replacement', 'library-test', 'Replacement', 60, 'mp4', 'h264')`); err != nil {
				t.Fatal(err)
			}
			var seq, rowID int
			if err := db.QueryRowContext(ctx, `SELECT s.seq, m.rowid FROM media_insert_seq s JOIN media m ON m.id = s.media_id`).Scan(&seq, &rowID); err != nil || seq != 3 || rowID != 1 {
				t.Fatalf("replacement seq/rowid = %d/%d, %v; want 3/1", seq, rowID, err)
			}
		})
	}
}
