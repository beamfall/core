package store

import (
	"context"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store/foreignimport/foreignimporttest"
)

func TestCheckCertifiedForeignDBSchemaAllowsRecordedVersions(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		dialect ForeignDBDialect
		file    string
	}{
		{"plex", ForeignDBPlex, "plex.db"},
		{"emby", ForeignDBEmby, "emby.db"},
		{"jellyfin", ForeignDBJellyfin, "jellyfin.db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := foreignimporttest.WriteVersionedSQLiteFixture(t, t.TempDir(), tc.file, 1)
			if err := CheckCertifiedForeignDBSchema(ctx, tc.dialect, path); err != nil {
				t.Fatalf("certified schema rejected: %v", err)
			}
		})
	}
}

func TestCheckCertifiedForeignDBSchemaFailsClosedOnUncertifiedVersion(t *testing.T) {
	path := foreignimporttest.WriteVersionedSQLiteFixture(t, t.TempDir(), "plex.db", 999)
	err := CheckCertifiedForeignDBSchema(context.Background(), ForeignDBPlex, path)
	if err == nil {
		t.Fatal("uncertified schema version was accepted")
	}
	if got := err.Error(); !strings.Contains(got, "uncertified plex database schema version user_version:999") {
		t.Fatalf("error = %q, want clear uncertified version report", got)
	}
}
