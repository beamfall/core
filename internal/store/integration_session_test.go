package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
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
	policy := AuthBackoff{Window: time.Minute, FreeAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Minute}
	keys := []string{"profile:profile-default", "ip:fixture"}
	reservation, until, err := st.ReserveAuthAttempt(ctx, IntegrationAttemptAction, keys, now, policy)
	if err != nil || !until.IsZero() {
		t.Fatalf("reserve integration attempts: locked until %v, error %v", until, err)
	}
	if err := st.CreateIntegrationSession(ctx, session, true, reservation); err != ValidationError("integration sign-in is disabled") {
		t.Fatalf("integration session without Profile opt-in: %v", err)
	}
	enabled := true
	if _, err := st.UpdateProfileSettings(ctx, session.ProfileID, ProfileSettings{IntegrationSignIn: &enabled}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateIntegrationSession(ctx, session, true, reservation); err != nil {
		t.Fatalf("CreateIntegrationSession: %v", err)
	}
	got, err := st.Session(ctx, session.ID)
	if err != nil || got.Kind != SessionKindIntegration || got.DeviceID != "" || len(got.IntegrationScopes) != 1 {
		t.Fatalf("integration session = %+v, %v", got, err)
	}

	invalid := session
	invalid.ID = "integration-invalid"
	invalid.IntegrationScopes = []IntegrationScope{"compat.admin"}
	if err := st.CreateIntegrationSession(ctx, invalid, true, reservation); err != ValidationError("unknown integration session scope") {
		t.Fatalf("integration session with a scope outside the ceiling: %v", err)
	}

	enabled = false
	if _, err := st.UpdateProfileSettings(ctx, session.ProfileID, ProfileSettings{IntegrationSignIn: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Session(ctx, session.ID); err == nil {
		t.Fatal("withdrawing Profile opt-in did not revoke integration sessions")
	}
}

func TestIntegrationSessionTransactionRollbackSurvivesReopen(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger string
		message string
	}{
		{
			name: "session insert",
			trigger: `CREATE TRIGGER fixture_reject_integration_insert BEFORE INSERT ON auth_session
				WHEN NEW.kind = 'integration'
				BEGIN SELECT RAISE(ABORT, 'fixture session insert failure'); END`,
			message: "fixture session insert failure",
		},
		{
			name: "second lane settlement",
			trigger: `CREATE TRIGGER fixture_reject_ip_settlement BEFORE UPDATE OF success ON auth_attempt
				WHEN OLD.key = 'ip:fixture' AND NEW.success = 1
				BEGIN SELECT RAISE(ABORT, 'fixture settlement failure'); END`,
			message: "fixture settlement failure",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			dataDir := migratedDataDir(t)
			opts := Options{SeedDevelopmentData: true}
			st, err := Open(ctx, dataDir, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			enabled := true
			if _, err := st.UpdateProfileSettings(ctx, "profile-default", ProfileSettings{IntegrationSignIn: &enabled}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			policy := AuthBackoff{Window: time.Minute, FreeAttempts: 1, BaseDelay: time.Second, MaxDelay: time.Minute}
			keys := []string{"profile:profile-default", "ip:fixture"}
			reservation, until, err := st.ReserveAuthAttempt(ctx, IntegrationAttemptAction, keys, now, policy)
			if err != nil || !until.IsZero() {
				t.Fatalf("reserve integration attempts: locked until %v, error %v", until, err)
			}
			scopes := []IntegrationScope{IntegrationScopeRead}
			if _, err := encodeIntegrationScopes(scopes); err != nil {
				t.Fatalf("encode fixture scopes: %v", err)
			}
			if _, err := st.catalogW.ExecContext(ctx, tc.trigger); err != nil {
				t.Fatal(err)
			}
			session := Session{
				ID: "rejected-integration", ProfileID: "profile-default", Kind: SessionKindIntegration,
				IntegrationScopes: scopes, ExpiresAt: now.Add(time.Hour),
			}
			if err := st.CreateIntegrationSession(ctx, session, true, reservation); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("CreateIntegrationSession = %v, want injected failure %q", err, tc.message)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, dataDir, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			var id SessionID
			if err := reopened.catalogR.QueryRowContext(ctx, "SELECT id FROM auth_session WHERE id = ?", session.ID).Scan(&id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("reopened session row = %q, %v, want absent", id, err)
			}
			for _, key := range keys {
				if until, err := reopened.AuthBackoffUntil(ctx, IntegrationAttemptAction, key, now, policy); err != nil || !until.After(now) {
					t.Fatalf("reopened lane %q: locked until %v, error %v", key, until, err)
				}
			}
		})
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

// TestIntegrationSetupSessionBinding pins that the setup scope and the opt-in
// bypass go together and stay tied to the owner, the admin throttle lane and
// the setup window, whatever the caller passes.
func TestIntegrationSetupSessionBinding(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProfile(ctx, "Other Adult", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policy := AuthBackoff{Window: time.Minute, FreeAttempts: 100, BaseDelay: time.Second, MaxDelay: time.Minute}
	reserve := func(key string) AuthAttemptReservation {
		t.Helper()
		reservation, until, err := st.ReserveAuthAttempt(ctx, IntegrationAttemptAction, []string{key, "ip:fixture"}, now, policy)
		if err != nil || !until.IsZero() {
			t.Fatalf("reserve %s: locked until %v, error %v", key, until, err)
		}
		return reservation
	}
	setupScopes := []IntegrationScope{IntegrationScopeRead, IntegrationScopeSetup}
	adminKey := IntegrationSetupAttemptKey("profile-default")
	for i, tc := range []struct {
		name         string
		profile      ProfileID
		scopes       []IntegrationScope
		ttl          time.Duration
		key          string
		requireOptIn bool
		want         string
	}{
		{"opt-in bypass without the setup scope", "profile-default", []IntegrationScope{IntegrationScopeRead}, time.Minute, adminKey, false, "requires the admin setup sign-in"},
		{"setup scope with opt-in required", "profile-default", setupScopes, time.Minute, adminKey, true, "requires the admin setup sign-in"},
		{"profile throttle lane", "profile-default", setupScopes, time.Minute, "profile:profile-default", false, "requires the admin sign-in reservation"},
		{"longer than the setup window", "profile-default", setupScopes, IntegrationSetupTTL + time.Second, adminKey, false, "outlives the setup window"},
		{"not the owner", other.ID, setupScopes, time.Minute, IntegrationSetupAttemptKey(other.ID), false, "requires the owner profile"},
		{"owner setup", "profile-default", setupScopes, IntegrationSetupTTL, adminKey, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := Session{
				ID: SessionID(fmt.Sprintf("setup-%d", i)), ProfileID: tc.profile, Kind: SessionKindIntegration,
				IntegrationScopes: tc.scopes, ExpiresAt: now.Add(tc.ttl),
			}
			err := st.CreateIntegrationSession(ctx, session, tc.requireOptIn, reserve(tc.key))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("owner setup session: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
