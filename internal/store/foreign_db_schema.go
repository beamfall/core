package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type ForeignDBDialect string

const (
	ForeignDBPlex     ForeignDBDialect = "plex"
	ForeignDBEmby     ForeignDBDialect = "emby"
	ForeignDBJellyfin ForeignDBDialect = "jellyfin"
)

var certifiedForeignDBSchemaVersions = map[ForeignDBDialect]map[string]bool{
	ForeignDBPlex: {
		"user_version:1": true,
	},
	ForeignDBEmby: {
		"user_version:1": true,
	},
	ForeignDBJellyfin: {
		"user_version:1": true,
	},
}

func CheckCertifiedForeignDBSchema(ctx context.Context, dialect ForeignDBDialect, path string) error {
	certified, ok := certifiedForeignDBSchemaVersions[dialect]
	if !ok {
		return nil
	}
	version, err := detectForeignDBSchemaVersion(ctx, dialect, path)
	if err != nil {
		return fmt.Errorf("%s database schema version check failed: %w", dialect, err)
	}
	if certified[version] {
		return nil
	}
	return fmt.Errorf("uncertified %s database schema version %s (certified: %s); refusing import because this foreign schema has not been verified", dialect, version, certifiedVersionList(certified))
}

func detectForeignDBSchemaVersion(ctx context.Context, dialect ForeignDBDialect, path string) (string, error) {
	db, err := sql.Open("sqlite", foreignDBSchemaDSN(path))
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	if dialect == ForeignDBPlex {
		if version, ok, err := plexSchemaMigrationVersion(ctx, db); err != nil {
			return "", err
		} else if ok {
			return version, nil
		}
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return "", err
	}
	return fmt.Sprintf("user_version:%d", version), nil
}

func foreignDBSchemaDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	u.RawQuery = q.Encode()
	return u.String()
}

func plexSchemaMigrationVersion(ctx context.Context, db *sql.DB) (string, bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&exists); err != nil {
		return "", false, err
	}
	if exists == 0 {
		return "", false, nil
	}
	var version sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return "", false, err
	}
	if !version.Valid || strings.TrimSpace(version.String) == "" {
		return "", false, nil
	}
	return "schema_migrations:" + strings.TrimSpace(version.String), true, nil
}

func certifiedVersionList(certified map[string]bool) string {
	versions := make([]string, 0, len(certified))
	for version := range certified {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return strings.Join(versions, ", ")
}
