package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ScrobbleLink is one per-Profile third-party scrobble account link (ADR-0049
// §2.2): the Profile deliberately linked its own account on one service. The
// row is NON-SECRET by construction — it carries a SecretRef (a pointer to the
// 0600 secure-store file that actually holds the OAuth token / session key,
// invariant #5), never the credential itself. AccountLabel is an optional
// non-secret display name (e.g. the linked username) for the My-Data "Where
// your data goes" panel. Service is the closed adapter
// vocabulary value the plugin owns ("trakt"/"lastfm"/"listenbrainz"); Core
// stores it verbatim and never parses it (invariant #1).
type ScrobbleLink struct {
	ProfileID    ProfileID
	Service      string
	SecretRef    string
	AccountLabel string
	LinkedAt     time.Time
}

// UpsertScrobbleLink records (or refreshes) a Profile's link to one service.
// The SecretRef names the 0600 file holding the credential — the caller writes
// the actual token to the secure store separately (composition root), so no
// secret ever reaches this row (invariant #5). Relinking the same service
// replaces the prior ref/label, never duplicating the link.
func (s *Store) UpsertScrobbleLink(ctx context.Context, link ScrobbleLink) error {
	if link.ProfileID == "" {
		return ValidationError("profile ID is required")
	}
	if strings.TrimSpace(link.Service) == "" {
		return ValidationError("scrobble service is required")
	}
	if strings.TrimSpace(link.SecretRef) == "" {
		return ValidationError("scrobble secret ref is required")
	}
	linkedAt := link.LinkedAt
	if linkedAt.IsZero() {
		linkedAt = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO scrobble_link(profile_id, service, secret_ref, account_label, linked_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(profile_id, service) DO UPDATE SET
		  secret_ref = excluded.secret_ref,
		  account_label = excluded.account_label,
		  linked_at_ms = excluded.linked_at_ms
	`, link.ProfileID, link.Service, link.SecretRef, link.AccountLabel, timeToMillis(linkedAt.UTC()))
	return err
}

// DeleteScrobbleLink removes a Profile's link to one service (the My-Data
// one-tap unlink, ADR-0049 §2.9). The credential file is cleared by the caller
// (composition root) so the 0600 secret never outlives the link. A missing link
// is a no-op success.
func (s *Store) DeleteScrobbleLink(ctx context.Context, profileID ProfileID, service string) error {
	if profileID == "" || strings.TrimSpace(service) == "" {
		return ValidationError("profile ID and service are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx,
		`DELETE FROM scrobble_link WHERE profile_id = ? AND service = ?`, profileID, service)
	return err
}

// ScrobbleLinksFor returns a Profile's scrobble links, ordered by service. It is
// the dispatcher's per-Profile target lookup and the My-Data "Where your data
// goes" read. The SecretRef is carried (the dispatcher resolves it through the
// secure store at push time); the export half deliberately drops it.
func (s *Store) ScrobbleLinksFor(ctx context.Context, profileID ProfileID) ([]ScrobbleLink, error) {
	if profileID == "" {
		return nil, ValidationError("profile ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT service, secret_ref, account_label, linked_at_ms
		FROM scrobble_link
		WHERE profile_id = ?
		ORDER BY service
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []ScrobbleLink{}
	for rows.Next() {
		link := ScrobbleLink{ProfileID: profileID}
		var linkedAt int64
		if err := rows.Scan(&link.Service, &link.SecretRef, &link.AccountLabel, &linkedAt); err != nil {
			return nil, err
		}
		link.LinkedAt = millisToTime(linkedAt)
		links = append(links, link)
	}
	return links, rows.Err()
}

// SetScrobbleLibraryEligibility sets a Library's per-Library scrobble opt-in.
// Eligibility is opt-in: the ABSENCE of an eligible=1 row is
// "not eligible", so a Library only scrobbles after a deliberate operator
// enable. Disabling writes eligible=0 (an explicit off, distinct from never
// configured, but both block scrobbling).
func (s *Store) SetScrobbleLibraryEligibility(ctx context.Context, libraryID LibraryID, eligible bool) error {
	if libraryID == "" {
		return ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO scrobble_library_eligibility(library_id, eligible, updated_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(library_id) DO UPDATE SET
		  eligible = excluded.eligible,
		  updated_at_ms = excluded.updated_at_ms
	`, libraryID, boolToInt(eligible), timeToMillis(time.Now().UTC()))
	return err
}

// ScrobbleLibraryEligible reports whether a Library is scrobble-eligible. It is
// true ONLY when an explicit eligible=1 row exists. The absence of a row — the
// default state of every Library, including every adult/Restricted Library —
// returns false, so "adult never default" falls out of the
// opt-in default rather than a special case. The dispatcher additionally refuses
// a Restricted Library (defence in depth), but the default-off model is the
// primary guard.
func (s *Store) ScrobbleLibraryEligible(ctx context.Context, libraryID LibraryID) (bool, error) {
	if libraryID == "" {
		return false, ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	var eligible int
	err := s.catalogR.QueryRowContext(ctx,
		`SELECT eligible FROM scrobble_library_eligibility WHERE library_id = ?`, libraryID).Scan(&eligible)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil // no row = not eligible (the opt-in default)
		}
		return false, err
	}
	return eligible != 0, nil
}
