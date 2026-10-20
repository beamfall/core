package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

var frozenMigrationDirs = []struct {
	name string
	dir  string
}{
	{"catalog", "internal/store/migrate/catalog"},
	{"events", "internal/store/migrate/events"},
	{"encrypted_library", "internal/store/migrate/encrypted_library"},
}

type frozenSchema map[string]frozenDatabase

type frozenDatabase struct {
	Migrations map[string]string `json:"migrations"`
	Columns    map[string]string `json:"columns"`
}

// VerifyFrozenSchema rejects rewritten migration history and removal or
// retyping of any column in the committed freeze manifest. Additive migrations
// and columns remain valid.
func VerifyFrozenSchema(fsys fs.FS, manifest []byte) error {
	var want frozenSchema
	if err := json.Unmarshal(manifest, &want); err != nil {
		return fmt.Errorf("decode frozen schema: %w", err)
	}
	got, err := captureFrozenSchema(fsys)
	if err != nil {
		return err
	}
	for database, frozen := range want {
		current, ok := got[database]
		if !ok {
			return fmt.Errorf("%s frozen schema is missing", database)
		}
		for name, checksum := range frozen.Migrations {
			if current.Migrations[name] != checksum {
				return fmt.Errorf("%s migration %s was removed or rewritten", database, name)
			}
		}
		for name, signature := range frozen.Columns {
			if current.Columns[name] != signature {
				return fmt.Errorf("%s frozen column %s was removed or retyped", database, name)
			}
		}
	}
	return nil
}

func captureFrozenSchema(fsys fs.FS) (frozenSchema, error) {
	schema := make(frozenSchema, len(frozenMigrationDirs))
	for _, migrationSet := range frozenMigrationDirs {
		database, err := captureFrozenDatabase(fsys, migrationSet.dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("%s: %w", migrationSet.name, err)
		}
		schema[migrationSet.name] = database
	}
	return schema, nil
}

func captureFrozenDatabase(fsys fs.FS, dir string) (frozenDatabase, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return frozenDatabase{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return frozenDatabase{}, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	migrations := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(fsys, filepath.ToSlash(filepath.Join(dir, entry.Name())))
		if err != nil {
			return frozenDatabase{}, err
		}
		migrations[entry.Name()] = migrationChecksum(data)
		if _, err := db.Exec(string(data)); err != nil {
			return frozenDatabase{}, fmt.Errorf("apply %s: %w", entry.Name(), err)
		}
	}
	columns, err := frozenColumns(db)
	if err != nil {
		return frozenDatabase{}, err
	}
	return frozenDatabase{Migrations: migrations, Columns: columns}, nil
}

func frozenColumns(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_schema
		WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return nil, err
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	columns := map[string]string{}
	for _, table := range tables {
		rows, err := db.Query("PRAGMA table_xinfo(" + quoteSQLiteString(table) + ")")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cid, notNull, primaryKey, hidden int
			var name, kind string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
				_ = rows.Close()
				return nil, err
			}
			columns[table+"."+name] = fmt.Sprintf("%s|notnull=%d|default=%q|pk=%d|hidden=%d",
				kind, notNull, defaultValue.String, primaryKey, hidden)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return columns, nil
}
