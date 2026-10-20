package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// IntegrationArtifactRoot is the server-owned store for verified immutable
// Integration Worker objects. Update-source folders are never execution roots.
func (s *Store) IntegrationArtifactRoot() string {
	return filepath.Join(s.dataDir, "integration-workers")
}

// ADR-0058 third-party Integration Worker runtime persistence. The store owns
// rows; internal/integrationhost owns the runtime and maps these store-native
// row types onto its own contract types (the store never imports the runtime —
// that would be a cycle, and the store stays domain/runtime-agnostic).

// IntegrationWorkerRow is one plugin's installed worker state.
type IntegrationWorkerRow struct {
	PluginID       string
	Version        string
	ArtifactSHA256 string
	SigningKeyID   string
	State          string
	LastError      string
}

// PluginGrantRow is one (plugin, permission) grant plus its bound Reveal scope.
// BoundProfileID and AllLibraries are mutually exclusive (DB CHECK + Go
// validation): a grant carrying both could bypass Reveal filtering.
type PluginGrantRow struct {
	PluginID       string
	Kind           string
	Approved       bool
	BoundProfileID ProfileID
	AllLibraries   bool
}

// PluginUpdateSourceRow is one plugin's operator-configured Update Source. For a
// signing-key pin (PinKind == "signing-key"), SigningKey is the hex-encoded
// trusted Ed25519 public key and Signature is the hex-encoded detached signature
// over the artifact bytes — both public trust material the launch path verifies
// against (Pin keeps the operator-facing signing-key id label). Hash pins leave
// SigningKey/Signature empty and use Pin for the hex hash.
type PluginUpdateSourceRow struct {
	PluginID   string
	Kind       string
	Location   string
	PinKind    string
	Pin        string
	SigningKey string
	Signature  string
}

// ErrGrantScopeConflict is returned by SetPluginGrant when a grant sets both a
// bound Profile and the all-Libraries opt-in. Enforced here (before the INSERT)
// in addition to the DB CHECK, mirroring validateActivityEntry: a single bad
// write can never produce a Reveal-bypassing grant.
var ErrGrantScopeConflict = errors.New("store: plugin grant binds both a Profile and all-Libraries")

// IntegrationWorker reads one plugin's worker row. The bool is false when no row
// exists yet (no worker installed).
func (s *Store) IntegrationWorker(ctx context.Context, pluginID string) (IntegrationWorkerRow, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return IntegrationWorkerRow{}, false, err
	}
	row := s.catalogR.QueryRowContext(ctx, `
		SELECT plugin_id, version, artifact_sha256, signing_key_id, state, last_error
		FROM integration_worker WHERE plugin_id = ?`, pluginID)
	var w IntegrationWorkerRow
	if err := row.Scan(&w.PluginID, &w.Version, &w.ArtifactSHA256, &w.SigningKeyID, &w.State, &w.LastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationWorkerRow{}, false, nil
		}
		return IntegrationWorkerRow{}, false, err
	}
	return w, true, nil
}

// SetIntegrationWorker upserts a plugin's worker row.
func (s *Store) SetIntegrationWorker(ctx context.Context, w IntegrationWorkerRow, now time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO integration_worker(plugin_id, version, artifact_sha256, signing_key_id, state, last_error, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(plugin_id) DO UPDATE SET
			version = excluded.version,
			artifact_sha256 = excluded.artifact_sha256,
			signing_key_id = excluded.signing_key_id,
			state = excluded.state,
			last_error = excluded.last_error,
			updated_at_ms = excluded.updated_at_ms`,
		w.PluginID, w.Version, w.ArtifactSHA256, w.SigningKeyID, w.State, w.LastError, timeToMillis(now))
	return err
}

// PluginGrants reads all grants for one plugin.
func (s *Store) PluginGrants(ctx context.Context, pluginID string) ([]PluginGrantRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT plugin_id, kind, approved, bound_profile_id, all_libraries
		FROM plugin_grant WHERE plugin_id = ? ORDER BY kind`, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PluginGrantRow
	for rows.Next() {
		var g PluginGrantRow
		var approved, allLibs int
		var profileID string
		if err := rows.Scan(&g.PluginID, &g.Kind, &approved, &profileID, &allLibs); err != nil {
			return nil, err
		}
		g.Approved = approved != 0
		g.AllLibraries = allLibs != 0
		g.BoundProfileID = ProfileID(profileID)
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetPluginGrant upserts one grant. It rejects a grant binding both a Profile
// and all-Libraries (ErrGrantScopeConflict) before the write; the DB CHECK is
// the backstop.
func (s *Store) SetPluginGrant(ctx context.Context, g PluginGrantRow, now time.Time) error {
	if g.BoundProfileID != "" && g.AllLibraries {
		return ErrGrantScopeConflict
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO plugin_grant(plugin_id, kind, approved, bound_profile_id, all_libraries, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(plugin_id, kind) DO UPDATE SET
			approved = excluded.approved,
			bound_profile_id = excluded.bound_profile_id,
			all_libraries = excluded.all_libraries,
			updated_at_ms = excluded.updated_at_ms`,
		g.PluginID, g.Kind, boolInt(g.Approved), string(g.BoundProfileID), boolInt(g.AllLibraries), timeToMillis(now))
	return err
}

// PluginUpdateSource reads one plugin's Update Source. The bool is false when
// none is configured.
func (s *Store) PluginUpdateSource(ctx context.Context, pluginID string) (PluginUpdateSourceRow, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PluginUpdateSourceRow{}, false, err
	}
	row := s.catalogR.QueryRowContext(ctx, `
		SELECT plugin_id, kind, location, pin_kind, pin, signing_key, signature
		FROM plugin_update_source WHERE plugin_id = ?`, pluginID)
	var u PluginUpdateSourceRow
	if err := row.Scan(&u.PluginID, &u.Kind, &u.Location, &u.PinKind, &u.Pin, &u.SigningKey, &u.Signature); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PluginUpdateSourceRow{}, false, nil
		}
		return PluginUpdateSourceRow{}, false, err
	}
	return u, true, nil
}

// SetPluginUpdateSource upserts one plugin's Update Source.
func (s *Store) SetPluginUpdateSource(ctx context.Context, u PluginUpdateSourceRow, now time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO plugin_update_source(plugin_id, kind, location, pin_kind, pin, signing_key, signature, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(plugin_id) DO UPDATE SET
			kind = excluded.kind,
			location = excluded.location,
			pin_kind = excluded.pin_kind,
			pin = excluded.pin,
			signing_key = excluded.signing_key,
			signature = excluded.signature,
			updated_at_ms = excluded.updated_at_ms`,
		u.PluginID, u.Kind, u.Location, u.PinKind, u.Pin, u.SigningKey, u.Signature, timeToMillis(now))
	return err
}

const pluginProfileLinkPrefix = "plugin-link."

func pluginProfileLinkKey(pluginID string, profileID ProfileID) (string, error) {
	if !pluginConfigKeyPartPattern.MatchString(pluginID) {
		return "", ValidationError("pluginID must be a lowercase slug")
	}
	if !pluginConfigKeyPartPattern.MatchString(string(profileID)) {
		return "", ValidationError("profileID must be a lowercase slug")
	}
	return pluginProfileLinkPrefix + pluginID + "." + string(profileID), nil
}

// SetPluginProfileLink persists one non-secret, per-Profile account-link
// reference for an Integration Plugin. The credential itself must live in the
// plugin-scoped secret store; this row is only the opaque account/reference a
// Profile linked to (ADR-0058 profile-link), stored in server_config to avoid a
// schema migration for the first Host seam.
func (s *Store) SetPluginProfileLink(ctx context.Context, pluginID string, profileID ProfileID, ref string, now time.Time) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ValidationError("profile link ref is required")
	}
	if _, err := s.Profile(ctx, profileID); err != nil {
		return err
	}
	key, err := pluginProfileLinkKey(pluginID, profileID)
	if err != nil {
		return err
	}
	return s.setServerConfigValues(ctx, map[string]string{key: ref}, now)
}

// PluginProfileLink reads a persisted Integration Plugin profile-link reference.
func (s *Store) PluginProfileLink(ctx context.Context, pluginID string, profileID ProfileID) (string, bool, error) {
	key, err := pluginProfileLinkKey(pluginID, profileID)
	if err != nil {
		return "", false, err
	}
	values, err := s.serverConfigValues(ctx)
	if err != nil {
		return "", false, err
	}
	ref, ok := values[key]
	return ref, ok, nil
}
