package store

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// ProvisionalExternalIDScheme is the External ID scheme under which the
// proxy-tee keys a provisional Media to its origin feed entry's GUID
// (ADR-0052 §2.2: "a Media row keyed by GUID External ID"). It is a generic
// file-provenance scheme (invariant #1 — no podcast vocabulary); the GUID is
// already an External ID, and the value never holds a secret URL.
const ProvisionalExternalIDScheme = "provisional-guid"

func provisionalGUIDExternalID(key string) string {
	if id, guid, ok := strings.Cut(key, "\x00"); ok {
		return id + "\x00" + feedGUIDExternalID(guid)
	}
	return feedGUIDExternalID(key)
}

// provisionalMediaID derives a stable, durable Media ID from the (libraryID,
// GUID) of a provisional play. Unlike a scanned file's ID,
// which is content-fingerprint-derived (mediaIDForFingerprint), a provisional
// Media has no fingerprint yet — so its ID is keyed by the feed entry's GUID
// External ID, and is PRESERVED unchanged across promotion to archived (only
// the file's fingerprint + provisional flag flip). Do not change this derivation
// or existing provisional Media IDs would silently fork.
func provisionalMediaID(libraryID LibraryID, guid string) MediaID {
	sum := sha256.Sum256([]byte(string(libraryID) + "\x00" + guid))
	return MediaID("media-prov-" + hex.EncodeToString(sum[:])[:20])
}

// ProvisionalMediaID returns the stable identity used by the proxy lifecycle
// coordinator before it mutates durable provisional state.
//
// The GUID is the feed's exact key (CORE-0706): it is never trimmed, so the
// proxy, the feed catalogue and RegisterProvisionalMedia all derive the same
// Media ID from the same bytes and concurrent paths converge on one Media.
func ProvisionalMediaID(libraryID LibraryID, guid string) MediaID {
	return provisionalMediaID(libraryID, guid)
}

// ErrEncryptedLibraryProvisionalMedia refuses a provisional feed Media in an
// Encrypted Library (CORE-0706) when the library's encrypted plane is sealed
// or unavailable.
var ErrEncryptedLibraryProvisionalMedia = ValidationError("provisional feed Media are not available in an Encrypted Library yet")

// ErrProvisionalRegistrationConflict means a registration attempted to revive
// an already-promoted or otherwise non-provisional Media.
var ErrProvisionalRegistrationConflict = errors.New("provisional media is no longer refreshable")

// provisionalFingerprint is the placeholder content_fingerprint a provisional
// media_file carries until tee completion computes the real one. content_
// fingerprint is the media_file PRIMARY KEY, so the placeholder must be unique
// per provisional row; deriving it from the Media ID guarantees that and keeps
// it collision-free against any real "sha256:" fingerprint.
func provisionalFingerprint(mediaID MediaID) string {
	return "provisional:" + string(mediaID)
}

// ProvisionalMedia describes a first-play provisional Media to materialize.
// The origin enclosure URL is deliberately NOT a field: it is
// a server-only secret (invariant #5/#7) held by the proxy-tee out of the DB,
// never persisted in plaintext and never reachable by a client.
type ProvisionalMedia struct {
	LibraryID LibraryID
	// GUID is the feed entry's identity External ID; it keys the
	// durable Media ID so resume/queue refs survive promotion.
	GUID  string
	Title string
	// Container/Codec seed the playback decision before the real probe lands;
	// the audio class is the common case (podcast enclosures), but the caller
	// supplies both so this stays domain-agnostic.
	Container string
	Codec     string
	Class     MediaClass
	// TeePath is where the proxy-tee writes the growing copy. It is the
	// provisional media_file's path until promotion moves the file into place.
	TeePath string
}

// CreateProvisionalMedia materializes a provisional Media on first play:
// a Media row keyed by GUID External ID with a media_file
// attached at the tee target path, provisional = 1 and fingerprint pending. It
// is idempotent on the (LibraryID, GUID)-derived Media ID — a second first-play
// of the same entry returns the existing provisional Media rather than forking
// it. The returned Media ID is preserved unchanged through PromoteProvisionalMedia.
// Server-internal like ImportMediaFile: the calling surface resolves visibility.
func (s *Store) CreateProvisionalMedia(ctx context.Context, p ProvisionalMedia) (MediaID, error) {
	// Authority face: a provisional Media is a catalog-identity row
	// (`INSERT INTO media`); authoring it is main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return "", err
	}
	if p.LibraryID == "" {
		return "", ValidationError("library ID is required")
	}
	if strings.TrimSpace(p.GUID) == "" {
		return "", ValidationError("provisional GUID is required")
	}
	if p.Title == "" {
		return "", ValidationError("media title is required")
	}
	if p.TeePath == "" {
		return "", ValidationError("tee path is required")
	}
	if p.Container == "" {
		p.Container = "mp3"
	}
	if p.Codec == "" {
		p.Codec = "mp3"
	}
	if p.Class == "" {
		p.Class = MediaClassAudio
	}
	mediaID := provisionalMediaID(p.LibraryID, p.GUID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	plane, err := s.createProvisionalMediaSQL(ctx, mediaID, p, now)
	if err != nil {
		return "", err
	}
	if !plane.encrypted {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventCreated, mediaID, nil, now); eventErr != nil {
			// The Media is already persisted; a failed outbox append must not fail
			// the create (same posture as ImportMediaFile). Log it so the resulting
			// catalog/outbox divergence is visible to the operator.
			slog.Warn("media outbox event append failed", "kind", MediaEventCreated, "media", mediaID, "error", eventErr)
		}
	}
	return mediaID, nil
}

// RegisterProvisionalMedia transactionally creates a provisional Media or
// refreshes the tee path of the still-live provisional row. It never revives an
// archived row. The previous path is returned so the caller can remove stale
// partial residue after publishing the new in-memory generation.
func (s *Store) RegisterProvisionalMedia(ctx context.Context, p ProvisionalMedia) (mediaID MediaID, previousPath string, err error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return "", "", err
	}
	if p.LibraryID == "" || strings.TrimSpace(p.GUID) == "" || p.Title == "" || p.TeePath == "" {
		return "", "", ValidationError("complete provisional registration is required")
	}
	if p.Container == "" {
		p.Container = "mp3"
	}
	if p.Codec == "" {
		p.Codec = "mp3"
	}
	if p.Class == "" {
		p.Class = MediaClassAudio
	}
	mediaID = provisionalMediaID(p.LibraryID, p.GUID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return "", "", err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, p.LibraryID)
	if err != nil {
		return "", "", err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var one int
	var provisional int
	err = tx.QueryRowContext(ctx, `SELECT path, provisional FROM media_file WHERE media_id = ?`, mediaID).Scan(&previousPath, &provisional)
	if err == nil {
		if provisional == 0 {
			return "", "", ErrProvisionalRegistrationConflict
		}
		if previousPath != p.TeePath {
			res, updateErr := tx.ExecContext(ctx, `
				UPDATE media_file SET path = ?, last_seen_at_ms = ?
				WHERE media_id = ? AND path = ? AND provisional = 1
			`, p.TeePath, timeToMillis(time.Now().UTC()), mediaID, previousPath)
			if updateErr != nil {
				return "", "", updateErr
			}
			if rows, _ := res.RowsAffected(); rows != 1 {
				return "", "", ErrProvisionalRegistrationConflict
			}
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return mediaID, previousPath, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, mediaID).Scan(&one); err == nil {
		// A file-less Media keyed by this registration's GUID External ID (a
		// catalogued entry not yet fetched) gains the provisional file in place;
		// its title and metadata stay. Any other existing row is not ours.
		// The exact GUID forms match; the trimmed forms match rows written
		// before GUIDs stopped being trimmed.
		trimmed := strings.TrimSpace(p.GUID)
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM media_external_id WHERE media_id = ? AND scheme = ? AND value IN (?, ?, ?, ?)
		`, mediaID, ProvisionalExternalIDScheme, provisionalGUIDExternalID(p.GUID), p.GUID,
			provisionalGUIDExternalID(trimmed), trimmed).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", "", ErrProvisionalRegistrationConflict
			}
			return "", "", err
		}
		if err := insertProvisionalMediaFile(ctx, tx, mediaID, p.TeePath); err != nil {
			return "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", err
		}
		return mediaID, "", nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class, mutated_by)
		VALUES (?, ?, ?, 0, ?, ?, ?, 'scan')
		ON CONFLICT(id) DO NOTHING
	`, mediaID, p.LibraryID, p.Title, p.Container, p.Codec, p.Class); err != nil {
		return "", "", err
	}
	if err := insertProvisionalMediaFile(ctx, tx, mediaID, p.TeePath); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO media_external_id(media_id, scheme, value) VALUES (?, ?, ?)`, mediaID, ProvisionalExternalIDScheme, provisionalGUIDExternalID(p.GUID)); err != nil {
		return "", "", err
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return mediaID, "", nil
}

func insertProvisionalMediaFile(ctx context.Context, tx *sql.Tx, mediaID MediaID, teePath string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, provisional, last_seen_at_ms)
		VALUES (?, ?, ?, ?, 1, ?)
	`, provisionalFingerprint(mediaID), provisionalFingerprint(mediaID), mediaID, teePath, timeToMillis(time.Now().UTC()))
	return err
}

// IsProvisionalMediaFile reports whether a file is a provisional placeholder:
// its bytes are an in-progress proxy-tee copy, never a direct-play source.
func IsProvisionalMediaFile(file MediaFile) bool {
	return strings.HasPrefix(file.ContentFingerprint, "provisional:")
}

// CompensateProvisionalMediaRegistration reverses only the exact provisional
// path written by a registration that could not publish its in-memory stream.
// A refresh restores its previous path; a new registration removes its file,
// and also its Media row unless a catalogued subscription entry binds that row
// (the registration attached a file to a Media it did not create).
func (s *Store) CompensateProvisionalMediaRegistration(ctx context.Context, mediaID MediaID, registeredPath, previousPath string) (bool, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return false, err
	}
	if mediaID == "" || registeredPath == "" {
		return false, ValidationError("complete provisional registration compensation is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if previousPath == "" {
		return s.compensateNewProvisionalRegistration(ctx, plane, mediaID, registeredPath)
	}
	res, err := plane.writer.ExecContext(ctx, `
		UPDATE media_file SET path = ?
		WHERE media_id = ? AND content_fingerprint = ? AND path = ? AND provisional = 1
	`, previousPath, mediaID, provisionalFingerprint(mediaID), registeredPath)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows == 1, err
}

func (s *Store) compensateNewProvisionalRegistration(ctx context.Context, plane catalogPlane, mediaID MediaID, registeredPath string) (bool, error) {
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		DELETE FROM media_file
		WHERE media_id = ? AND content_fingerprint = ? AND path = ? AND provisional = 1
		  AND (SELECT COUNT(*) FROM media_file WHERE media_id = ?) = 1
	`, mediaID, provisionalFingerprint(mediaID), registeredPath, mediaID)
	if err != nil {
		return false, err
	}
	if rows, err := res.RowsAffected(); err != nil || rows != 1 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM media
		WHERE id = ?
		  AND NOT EXISTS (SELECT 1 FROM media_file WHERE media_id = ?)
		  AND NOT EXISTS (SELECT 1 FROM subscription_entry WHERE media_id = ?)
	`, mediaID, mediaID, mediaID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) createProvisionalMediaSQL(ctx context.Context, mediaID MediaID, p ProvisionalMedia, now time.Time) (catalogPlane, error) {
	plane, err := s.catalogPlaneForLibraryLocked(ctx, p.LibraryID)
	if err != nil {
		return catalogPlane{}, err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return catalogPlane{}, err
	}
	defer tx.Rollback()
	// Idempotent on the GUID-derived ID: a re-play of the same entry must not
	// fork the Media or overwrite a title an importer/identification set. The
	// ON CONFLICT is a no-op (id stays, provisional flag untouched) so a
	// provisional already mid-promotion is never reset.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class, mutated_by)
		VALUES (?, ?, ?, 0, ?, ?, ?, 'scan')
		ON CONFLICT(id) DO NOTHING
	`, mediaID, p.LibraryID, p.Title, p.Container, p.Codec, p.Class); err != nil {
		return catalogPlane{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, provisional, last_seen_at_ms)
		VALUES (?, ?, ?, ?, 1, ?)
		ON CONFLICT(content_fingerprint) DO NOTHING
	`, provisionalFingerprint(mediaID), provisionalFingerprint(mediaID), mediaID, p.TeePath, timeToMillis(now)); err != nil {
		return catalogPlane{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO media_external_id(media_id, scheme, value)
		VALUES (?, ?, ?)
	`, mediaID, ProvisionalExternalIDScheme, provisionalGUIDExternalID(p.GUID)); err != nil {
		return catalogPlane{}, err
	}
	return plane, tx.Commit()
}

// PromoteProvisionalMedia promotes a provisional Media to archived in place on
// tee completion: the media_file's placeholder fingerprint is
// replaced with the real content_fingerprint, its path is updated to the final
// in-place location, and provisional flips 1 → 0 — all WITHOUT re-keying the
// Media, so every queue/resume/Snapshot reference made while streaming survives.
// It is a no-op-safe upsert keyed by Media ID; a Media with no provisional file
// (already promoted, or never provisional) is left untouched and reports no error.
// An abandoned partial is NEVER promoted: the proxy-tee only calls this on a full,
// verified body.
func (s *Store) PromoteProvisionalMedia(ctx context.Context, mediaID MediaID, contentFingerprint, finalPath string) error {
	_, err := s.PromoteProvisionalMediaGeneration(ctx, mediaID, "", contentFingerprint, "", finalPath)
	return err
}

// ProvisionalPromotionDuplicateError means another live Media owns the content
// identity. KeepArchive protects the file when that Media already uses finalPath.
type ProvisionalPromotionDuplicateError struct {
	KeepArchive bool
}

func (*ProvisionalPromotionDuplicateError) Error() string {
	return "provisional promotion duplicates cataloged content"
}

// PromoteProvisionalMediaGeneration promotes only the live provisional path
// observed by the caller. A supplied legacy fingerprint enables duplicate checks
// against both fingerprint schemes in the publication transaction.
func (s *Store) PromoteProvisionalMediaGeneration(ctx context.Context, mediaID MediaID, expectedTeePath, contentFingerprint, legacyFingerprint, finalPath string) (bool, error) {
	if mediaID == "" {
		return false, ValidationError("media ID is required")
	}
	if strings.TrimSpace(contentFingerprint) == "" {
		return false, ValidationError("content fingerprint is required")
	}
	if finalPath == "" {
		return false, ValidationError("final path is required")
	}
	var archivePath string
	if legacyFingerprint != "" {
		// The archive can be cataloged under its canonical path.
		archivePath, _ = filepath.EvalSymlinks(finalPath)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	// Route unsealed encrypted planes first so plaintext promotions touch
	// catalogW cleanly inside the transaction without pre-querying catalogR.
	plane := catalogPlane{writer: s.catalogW, reader: s.catalogR}
	for _, enc := range s.registeredEncryptedPlanesLocked() {
		var found int
		err := enc.reader.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, mediaID).Scan(&found)
		if err == nil {
			plane = enc
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	// BEGIN IMMEDIATE serializes the decision and UPDATE with scan and managed
	// import admissions, even when they use a different fingerprint key.
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if legacyFingerprint != "" {
		_, duplicate, err := liveMediaFileByFingerprint(ctx, tx, contentFingerprint, legacyFingerprint)
		if err != nil {
			return false, err
		}
		if duplicate {
			// A concurrent admission can own the archive path even when the
			// duplicate was found at a different path. Read the committed plane
			// independently of the transaction, which cancellation can roll back.
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			var inPlane, keep bool
			err := plane.reader.QueryRowContext(cleanupCtx, `
				SELECT
					EXISTS (SELECT 1 FROM media WHERE id = ?),
					EXISTS (SELECT 1 FROM media_file WHERE path IN (?, ?) AND missing_at_ms IS NULL)
			`, mediaID, finalPath, archivePath).Scan(&inPlane, &keep)
			if err != nil {
				return false, err
			}
			if !inPlane {
				return false, nil
			}
			return false, &ProvisionalPromotionDuplicateError{KeepArchive: keep}
		}
	}
	query := `
		UPDATE media_file
		SET content_fingerprint = ?, path = ?, provisional = 0
		WHERE media_id = ? AND content_fingerprint = ? AND provisional = 1
	`
	args := []any{contentFingerprint, finalPath, mediaID, provisionalFingerprint(mediaID)}
	if expectedTeePath != "" {
		query += " AND path = ?"
		args = append(args, expectedTeePath)
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		// No provisional file for this Media (already promoted, or never
		// provisional): nothing to do, not an error.
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if !plane.encrypted {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, mediaID, nil, time.Now().UTC()); eventErr != nil {
			// The promotion is already committed; the append must not fail it, but the
			// divergence has to be visible.
			slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", mediaID, "error", eventErr)
		}
	}
	return true, nil
}

// CompensateProvisionalMediaPromotion restores exactly one just-promoted
// generation when its filesystem publication validation fails. The tuple
// predicate prevents a stale compensator from reverting a later promotion or
// any concurrent file mutation.
func (s *Store) CompensateProvisionalMediaPromotion(ctx context.Context, mediaID MediaID, contentFingerprint, finalPath, teePath string) (bool, error) {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return false, err
	}
	if mediaID == "" || strings.TrimSpace(contentFingerprint) == "" || finalPath == "" || teePath == "" {
		return false, ValidationError("complete provisional promotion compensation is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE media_file
		SET content_fingerprint = ?, path = ?, provisional = 1
		WHERE media_id = ? AND content_fingerprint = ? AND path = ? AND provisional = 0
	`, provisionalFingerprint(mediaID), teePath, mediaID, contentFingerprint, finalPath)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, tx.Commit()
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if !plane.encrypted {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, mediaID, nil, time.Now().UTC()); eventErr != nil {
			slog.Warn("media promotion compensation outbox event append failed", "kind", MediaEventUpdated, "media", mediaID, "error", eventErr)
		}
	}
	return true, nil
}

// SetMediaFileProvisional marks a Media's backing file as a provisional copy
// (true) or an archived, keep-all copy (false) — ADR-0052 §2.2-2.3. The
// streaming proxy-tee sets it true when it materializes a streamed item on play, and
// clears it on promotion to a kept copy (the MediaID is preserved across
// promotion — only this flag flips). A no-op if the Media has no file yet.
func (s *Store) SetMediaFileProvisional(ctx context.Context, mediaID MediaID, provisional bool) error {
	if mediaID == "" {
		return ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = plane.writer.ExecContext(ctx, `
		UPDATE media_file SET provisional = ? WHERE media_id = ?
	`, boolToInt(provisional), mediaID)
	return err
}

// MediaFileProvisional reports whether the Media's backing file is a provisional
// copy. A Media with no file reports false (it is "not-downloaded", a distinct
// availability state the predicate handles by file absence, not this flag).
func (s *Store) MediaFileProvisional(ctx context.Context, mediaID MediaID) (bool, error) {
	if mediaID == "" {
		return false, ValidationError("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if errors.Is(err, errPlaneMediaNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var provisional int
	err = plane.reader.QueryRowContext(ctx, `
		SELECT provisional FROM media_file WHERE media_id = ?
	`, mediaID).Scan(&provisional)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return provisional != 0, err
}

// availabilitySQL backs the "availability" Source predicate:
// the per-Media file-provenance axis over the frozen media_file.provisional
// flag. The three bands, each a generic file-provenance state (invariant #1 —
// no podcast vocabulary):
//
//   - archived             — the Media has at least one kept (provisional = 0) file.
//   - streamed-provisional — the Media has file(s) but every one is provisional
//     (a proxy-tee'd, reapable copy not yet promoted in place to archived).
//   - not-downloaded       — the Media has no file at all.
//
// It rides the exact reveal/cert/extras/Library funnel allItemsSQL uses — the
// candidate set is the same set the catalog feeds expose — so a restricted
// Library's Media never surfaces to an unrevealed scope. Called under the
// SourceItems read lock, like the other catalog feeds, so it does not lock.
func (s *Store) availabilitySQL(ctx context.Context, value string, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	// EXISTS predicate selecting the band; an unrecognized value yields an empty
	// feed (never an error), like a missing entity.
	var bandClause string
	switch value {
	case "archived":
		bandClause = ` AND EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.provisional = 0)`
	case "streamed-provisional":
		bandClause = ` AND EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)` +
			` AND NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id AND f.provisional = 0)`
	case "not-downloaded":
		bandClause = ` AND NOT EXISTS (SELECT 1 FROM media_file f WHERE f.media_id = m.id)`
	default:
		return []SourceItem{}, nil
	}

	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return nil, err
	}
	var plainVisible []LibraryID
	encVisible := map[LibraryID]bool{}
	for _, library := range libraries {
		if !libraryVisible(library, filter) {
			continue
		}
		if library.Encrypted {
			encVisible[library.ID] = true
		} else {
			plainVisible = append(plainVisible, library.ID)
		}
	}
	if len(plainVisible) == 0 && len(encVisible) == 0 {
		return []SourceItem{}, nil
	}
	fetchLimit := limit + offset
	if limit <= 0 {
		fetchLimit = -1
	}
	certClause, certArgs := certificationFilterSQL(filter.Certification, filter.PlaybackGate, filter.GrantedMediaIDs)
	items := []SourceItem{}
	for _, plane := range s.allCatalogPlanesLocked() {
		var libs []LibraryID
		if plane.encrypted {
			if !encVisible[plane.libraryID] {
				continue
			}
			libs = []LibraryID{plane.libraryID}
		} else {
			if len(plainVisible) == 0 {
				continue
			}
			libs = plainVisible
		}
		placeholders := make([]string, len(libs))
		args := make([]any, 0, len(libs)+len(certArgs)+1)
		for i, id := range libs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		args = append(args, certArgs...)
		limitSQL := ""
		if fetchLimit >= 0 {
			limitSQL = ` LIMIT ?`
			args = append(args, fetchLimit)
		}
		// #nosec G202 -- only generated placeholders and closed clauses are interpolated.
		rows, err := plane.reader.QueryContext(ctx, `
			SELECT m.id, m.title, m.library_id, m.duration_seconds
			FROM media m
			WHERE m.library_id IN (`+strings.Join(placeholders, ", ")+`)
			  AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+certClause+bandClause+`
			ORDER BY m.title COLLATE NOCASE, m.id`+limitSQL, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id MediaID
			var title string
			var libraryID LibraryID
			var durationSeconds int
			if err := rows.Scan(&id, &title, &libraryID, &durationSeconds); err != nil {
				_ = rows.Close()
				return nil, err
			}
			items = append(items, SourceItem{
				ID:        id,
				Kind:      "media",
				Title:     title,
				LibraryID: libraryID,
				Summary:   mediaDurationSummary(durationSeconds),
			})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(items, func(a, b SourceItem) int {
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
			cmp.Compare(a.ID, b.ID),
		)
	})
	start := min(max(offset, 0), len(items))
	end := len(items)
	if limit > 0 {
		end = min(start+limit, end)
	}
	return items[start:end], nil
}
