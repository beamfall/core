package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegrationSessionKindsAndOptIn(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO auth_session(id, device_id, profile_id, revealed, kind, integration_scopes)
		VALUES ('legacy-device-bound', 'device-tv', 'profile-default', 0, NULL, '')`); err != nil {
		t.Fatal(err)
	}
	legacy, err := st.Session(ctx, "legacy-device-bound")
	if err != nil || legacy.Kind != SessionKindEnrolled {
		t.Fatalf("legacy session = %+v, %v; want enrolled", legacy, err)
	}

	now := time.Now().UTC()
	session := Session{
		ID: "integration-one", ProfileID: "profile-default", Kind: SessionKindIntegration,
		IntegrationScopes: []IntegrationScope{IntegrationScopeRead}, ExpiresAt: now.Add(time.Hour),
	}
	if err := st.CreateIntegrationSession(ctx, session, true); err == nil {
		t.Fatal("integration session minted without Profile opt-in")
	}
	enabled := true
	if _, err := st.UpdateProfileSettings(ctx, session.ProfileID, ProfileSettings{IntegrationSignIn: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateIntegrationSession(ctx, session, true); err != nil {
		t.Fatalf("CreateIntegrationSession: %v", err)
	}
	got, err := st.Session(ctx, session.ID)
	if err != nil || got.Kind != SessionKindIntegration || got.DeviceID != "" || len(got.IntegrationScopes) != 1 {
		t.Fatalf("integration session = %+v, %v", got, err)
	}

	invalid := session
	invalid.ID = "integration-invalid"
	invalid.IntegrationScopes = []IntegrationScope{"compat.admin"}
	if err := st.CreateIntegrationSession(ctx, invalid, true); err == nil {
		t.Fatal("integration session accepted a scope outside the ceiling")
	}

	enabled = false
	if _, err := st.UpdateProfileSettings(ctx, session.ProfileID, ProfileSettings{IntegrationSignIn: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, session.ID); err == nil {
		t.Fatal("withdrawing Profile opt-in did not revoke integration sessions")
	}
}

func TestIntegrationSessionMigrationPreservesLegacyRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")
	before := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0110")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", before, "migrate/catalog", backupDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title) VALUES ('legacy-library', 'Legacy');
		INSERT INTO profile(id, name, kind) VALUES ('legacy-profile', 'Legacy', 'adult');
		INSERT INTO device(id, name, role, secret_hash) VALUES ('legacy-device', 'Legacy', 'viewer', 'hash');
		INSERT INTO auth_session(id, device_id, profile_id, revealed) VALUES ('legacy-session', 'legacy-device', 'legacy-profile', 0);
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class)
		VALUES ('legacy-media', 'legacy-library', 'Legacy', 1, 'mp4', 'h264', 'video');
		INSERT INTO beam_handoff_offer(token_hash, source_session_id, source_device_id, source_profile_id,
		  media_id, position_seconds, expires_at_ms, created_at_ms)
		VALUES ('legacy-offer', 'legacy-session', 'legacy-device', 'legacy-profile', 'legacy-media', 0, 2, 1);`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations, "migrate/catalog", backupDir); err != nil {
		t.Fatal(err)
	}
	var deviceID string
	var kind sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT device_id, kind FROM auth_session WHERE id = 'legacy-session'`).Scan(&deviceID, &kind); err != nil {
		t.Fatal(err)
	}
	if deviceID != "legacy-device" || kind.Valid {
		t.Fatalf("migrated legacy row device=%q kind=%v", deviceID, kind)
	}
	var offers int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM beam_handoff_offer WHERE token_hash = 'legacy-offer'`).Scan(&offers); err != nil || offers != 1 {
		t.Fatalf("migrated handoff offers = %d, %v; want 1", offers, err)
	}
	if rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`); err != nil {
		t.Fatal(err)
	} else {
		defer rows.Close()
		if rows.Next() {
			t.Fatal("integration Session migration left a foreign-key violation")
		}
	}
}

func TestSessionReadRejectsKeylessAndUnknownLegacyRows(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO auth_session(id, device_id, profile_id, revealed, integration_scopes)
		VALUES ('keyless-legacy', NULL, 'profile-default', 0, '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, "keyless-legacy"); err == nil {
		t.Fatal("keyless legacy row authorized")
	}
	if _, err := st.catalogW.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO auth_session(id, device_id, profile_id, revealed, kind, integration_scopes)
		VALUES ('unknown-kind', 'device-tv', 'profile-default', 0, 'future-kind', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, "unknown-kind"); err == nil {
		t.Fatal("unknown Session kind authorized")
	}
}
