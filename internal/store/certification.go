package store

import (
	"context"
	"sort"
	"strings"
)

// annotateLockedLocked marks SourceItems that are visible in browse but blocked
// by a locked certification ceiling. It is called
// only from browse paths (PlaybackGate=false), where locked constraints are
// skipped by certificationFilterSQL so the items appear in results.
//
// For each locked constraint, any media item whose library is covered by the
// constraint's plugin AND whose certification value is NOT in the allowed set
// is marked Locked=true. Entity items never carry a certification field and are
// never marked locked. Items in grantedIDs are skipped (ACCESS-3, ADR-0113
// §2.3): an active AccessGrant bypasses the ceiling for that item.
func (s *Store) annotateLockedLocked(ctx context.Context, constraints []CertificationConstraint, items []SourceItem, grantedIDs []MediaID) error {
	if len(items) == 0 {
		return nil
	}
	// Collect only locked constraints — hide-mode constraints were already
	// enforced by certificationFilterSQL; this is the locked-mode residual.
	var locked []CertificationConstraint
	for _, c := range constraints {
		if c.Locked {
			locked = append(locked, c)
		}
	}
	if len(locked) == 0 {
		return nil
	}
	// Build the granted-ID set so we can skip those items entirely.
	granted := make(map[MediaID]bool, len(grantedIDs))
	for _, id := range grantedIDs {
		granted[id] = true
	}
	// Collect media item IDs (entities never carry certification; granted items bypass the ceiling).
	ids := make([]MediaID, 0, len(items))
	idIndex := make(map[MediaID][]int)
	for i, item := range items {
		if item.Kind == "entity" || granted[item.ID] {
			continue
		}
		if _, seen := idIndex[item.ID]; !seen {
			ids = append(ids, item.ID)
		}
		idIndex[item.ID] = append(idIndex[item.ID], i)
	}
	if len(ids) == 0 {
		return nil
	}
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return err
	}
	// Query each Library's own catalog plane. Encrypted planes intentionally do
	// not contain the authoritative Library row or plugin binding.
	lockedIDs := make(map[MediaID]bool)
	for _, library := range libraries {
		pluginID := ""
		if library.PluginID != nil {
			pluginID = *library.PluginID
		}
		reader := s.catalogR
		if library.Encrypted {
			db := s.encryptedDBs[library.ID]
			if db == nil || db.state != "unsealed-registered" || db.reader == nil {
				continue
			}
			reader = db.reader
		}
		for _, c := range locked {
			if c.PluginID != pluginID {
				continue
			}
			placeholders := make([]string, len(ids))
			queryArgs := make([]any, 0, len(ids)+1+len(c.Allowed))
			queryArgs = append(queryArgs, library.ID)
			for i, id := range ids {
				placeholders[i] = "?"
				queryArgs = append(queryArgs, id)
			}
			blockedClause := ""
			if len(c.Allowed) > 0 {
				placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(c.Allowed)), ", ")
				blockedClause += ` AND NOT EXISTS (
					SELECT 1 FROM media_field_text cf
					WHERE cf.media_id = m.id AND cf.field = '` + certificationField + `' AND cf.value IN (` + placeholders + `))`
				for _, value := range c.Allowed {
					queryArgs = append(queryArgs, value)
				}
			}
			if c.AllowUnrated {
				blockedClause += ` AND EXISTS (
					SELECT 1 FROM media_field_text cf
					WHERE cf.media_id = m.id AND cf.field = '` + certificationField + `')`
			}
			// #nosec G202 -- only generated placeholders and closed certification clauses are interpolated.
			rows, err := reader.QueryContext(ctx, `SELECT m.id FROM media m
				WHERE m.library_id = ? AND m.id IN (`+strings.Join(placeholders, ", ")+`)`+blockedClause, queryArgs...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id MediaID
				if err := rows.Scan(&id); err != nil {
					_ = rows.Close()
					return err
				}
				lockedIDs[id] = true
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
	}
	for id, idxs := range idIndex {
		if lockedIDs[id] {
			for _, i := range idxs {
				items[i].Locked = true
			}
		}
	}
	return nil
}

// certificationField is the conventional text Media field a plugin's ordered
// certification scale ranks. The name is core convention; the
// values are plugin data.
const certificationField = "certification"

// SetProfileCertificationCeiling upserts the Profile's ceiling on one
// plugin's certification scale and returns the updated Profile. The caller
// validates the ceiling against the plugin's declared scale — the store
// never interprets scale values.
func (s *Store) SetProfileCertificationCeiling(ctx context.Context, id ProfileID, ceiling ProfileCertificationCeiling) (Profile, error) {
	if id == "" {
		return Profile{}, ValidationError("profile ID is required")
	}
	if strings.TrimSpace(ceiling.PluginID) == "" {
		return Profile{}, ValidationError("plugin ID is required")
	}
	if strings.TrimSpace(ceiling.Ceiling) == "" {
		return Profile{}, ValidationError("ceiling value is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	if _, err := s.profileSQL(ctx, id); err != nil {
		return Profile{}, err
	}
	if ceiling.FilterVisibility == "" {
		ceiling.FilterVisibility = "hide"
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO profile_certification_ceiling(profile_id, plugin_id, ceiling, allow_unrated, filter_visibility)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, plugin_id) DO UPDATE SET
			ceiling = excluded.ceiling, allow_unrated = excluded.allow_unrated,
			filter_visibility = excluded.filter_visibility`,
		id, ceiling.PluginID, ceiling.Ceiling, boolInt(ceiling.AllowUnrated), ceiling.FilterVisibility,
	); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}

// ClearProfileCertificationCeiling removes the Profile's ceiling for one
// plugin and returns the updated Profile. Clearing an absent ceiling is a
// no-op.
func (s *Store) ClearProfileCertificationCeiling(ctx context.Context, id ProfileID, pluginID string) (Profile, error) {
	if id == "" {
		return Profile{}, ValidationError("profile ID is required")
	}
	if strings.TrimSpace(pluginID) == "" {
		return Profile{}, ValidationError("plugin ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Profile{}, err
	}
	if _, err := s.profileSQL(ctx, id); err != nil {
		return Profile{}, err
	}
	if _, err := s.catalogW.ExecContext(ctx,
		`DELETE FROM profile_certification_ceiling WHERE profile_id = ? AND plugin_id = ?`,
		id, pluginID,
	); err != nil {
		return Profile{}, err
	}
	return s.profileSQL(ctx, id)
}

// certificationCeilingsSQL loads a Profile's ceilings, plugin-sorted.
func (s *Store) certificationCeilingsSQL(ctx context.Context, id ProfileID) ([]ProfileCertificationCeiling, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT plugin_id, ceiling, allow_unrated, filter_visibility
		FROM profile_certification_ceiling
		WHERE profile_id = ? ORDER BY plugin_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ceilings []ProfileCertificationCeiling
	for rows.Next() {
		var ceiling ProfileCertificationCeiling
		var allowUnrated int
		if err := rows.Scan(&ceiling.PluginID, &ceiling.Ceiling, &allowUnrated, &ceiling.FilterVisibility); err != nil {
			return nil, err
		}
		ceiling.AllowUnrated = allowUnrated != 0
		if ceiling.FilterVisibility == "" {
			ceiling.FilterVisibility = "hide"
		}
		ceilings = append(ceilings, ceiling)
	}
	return ceilings, rows.Err()
}

// certificationFilterSQL renders the ceiling clauses for a media query whose
// Media table is aliased `m`: inside each constrained plugin's Libraries,
// only Media whose certification field value is in the Allowed set survives
// — no value, or a value outside Allowed, filters fail-closed;
// AllowUnrated re-admits unrated Media only. Constraints are plugin-sorted
// so the SQL shape is deterministic. Empty constraints yield no clause.
//
// playbackGate distinguishes browse from the byte gate (ACCESS-1, ADR-0113
// §2.1). When false (browse), Locked constraints are skipped — blocked Media
// remains visible so the client can render the locked-but-visible state.
// When true (byte gate), ALL constraints apply their exclusion clause so the
// server denies the actual byte stream regardless of visibility mode.
//
// grantedIDs are media items that have an active
// AccessGrant for this Profile. When non-empty, an "OR m.id IN (?...)"
// escape hatch is appended to every constraint clause so granted items bypass
// the ceiling on both browse and byte-gate paths.
func certificationFilterSQL(constraints []CertificationConstraint, playbackGate bool, grantedIDs []MediaID) (string, []any) {
	if len(constraints) == 0 {
		return "", nil
	}
	ordered := make([]CertificationConstraint, len(constraints))
	copy(ordered, constraints)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PluginID < ordered[j].PluginID })
	var clause strings.Builder
	var args []any
	// Pre-build the grant escape hatch (shared across all constraint clauses).
	var grantClause string
	var grantArgs []any
	if len(grantedIDs) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?, ", len(grantedIDs)), ", ")
		grantClause = "\n		       OR m.id IN (" + ph + ")"
		for _, id := range grantedIDs {
			grantArgs = append(grantArgs, id)
		}
	}
	for _, constraint := range ordered {
		// Locked constraints skip the exclusion clause on browse paths so that
		// locked-but-visible Media appears in results; the byte gate always
		// enforces the exclusion regardless of Locked.
		if constraint.Locked && !playbackGate {
			continue
		}
		clause.WriteString(`
		  AND (m.library_id NOT IN (SELECT id FROM library WHERE plugin_id = ?)`)
		args = append(args, constraint.PluginID)
		if len(constraint.Allowed) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(constraint.Allowed)), ", ")
			clause.WriteString(`
		       OR EXISTS (SELECT 1 FROM media_field_text cf WHERE cf.media_id = m.id
		                  AND cf.field = '` + certificationField + `' AND cf.value IN (` + placeholders + `))`)
			for _, value := range constraint.Allowed {
				args = append(args, value)
			}
		}
		if constraint.AllowUnrated {
			clause.WriteString(`
		       OR NOT EXISTS (SELECT 1 FROM media_field_text cf WHERE cf.media_id = m.id
		                      AND cf.field = '` + certificationField + `')`)
		}
		if grantClause != "" {
			clause.WriteString(grantClause)
			args = append(args, grantArgs...)
		}
		clause.WriteString(")")
	}
	return clause.String(), args
}

// planeCertificationFilterSQL renders certification clauses for a query
// already scoped to one Library. pluginID must come from the catalog Library
// row; the returned SQL never queries a Library table in the media plane.
func planeCertificationFilterSQL(pluginID string, constraints []CertificationConstraint, playbackGate bool, grantedIDs []MediaID) (string, []any) {
	if pluginID == "" || len(constraints) == 0 {
		return "", nil
	}
	ordered := append([]CertificationConstraint(nil), constraints...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PluginID < ordered[j].PluginID })
	var clause strings.Builder
	var args []any
	for _, constraint := range ordered {
		if constraint.PluginID != pluginID || constraint.Locked && !playbackGate {
			continue
		}
		clause.WriteString("\n\t\t  AND (0")
		if len(constraint.Allowed) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(constraint.Allowed)), ", ")
			clause.WriteString(`
		       OR EXISTS (SELECT 1 FROM media_field_text cf WHERE cf.media_id = m.id
		                  AND cf.field = '` + certificationField + `' AND cf.value IN (` + placeholders + `))`)
			for _, value := range constraint.Allowed {
				args = append(args, value)
			}
		}
		if constraint.AllowUnrated {
			clause.WriteString(`
		       OR NOT EXISTS (SELECT 1 FROM media_field_text cf WHERE cf.media_id = m.id
		                      AND cf.field = '` + certificationField + `')`)
		}
		if len(grantedIDs) > 0 {
			placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(grantedIDs)), ", ")
			clause.WriteString("\n\t\t       OR m.id IN (" + placeholders + ")")
			for _, id := range grantedIDs {
				args = append(args, id)
			}
		}
		clause.WriteString(")")
	}
	return clause.String(), args
}
