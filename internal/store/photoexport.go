package store

import (
	"context"
	"database/sql"
	"errors"
)

// ExportFile is one byte-exact original backing an exported Media item: a single
// (Version, Part) file. Path is the on-disk location of the bytes — plaintext
// for a non-Encrypted Library, the `.libencrypt` at-rest ciphertext for an
// Encrypted one (the export decrypts it through the cipher seam to recover the
// byte-exact original). ContentFingerprint is the `sha256:` content identity the
// ingest recorded, so a third party can verify the exported bytes round-trip.
type ExportFile struct {
	Path               string
	ContentFingerprint string
	// VersionLabel and PartOrdinal identify which file of a multi-file Media this
	// is: a RAW+JPEG pair is two files under one Media; a Live Photo
	// is the still as Part 0 and the motion as Part 1. The export
	// emits every file and records this identity so the pairing round-trips.
	VersionLabel string
	PartOrdinal  int
}

// ExportItem is one Media item assembled for the bulk photo-export manifest
// (ADR-0045 §2.9 mode 2): the descriptive row, every byte-exact file backing it,
// the plugin-enriched fields (EXIF, Location, captions text, …), the sidecar
// caption tracks, and the generic Entity memberships (album membership, etc.).
// All reads come from the Media's own catalog plane so an Encrypted Library
// exports the same shape as a plaintext one.
type ExportItem struct {
	Media       Media
	Files       []ExportFile
	Fields      []MediaField
	Captions    []MediaFileSidecarCaption
	Memberships []MediaMembership
	// Encrypted is true when the Media lives in an Encrypted Library, so its
	// Files' Path values are at-rest ciphertext the export must decrypt to
	// recover byte-exact originals (a plaintext Library copies the bytes as-is).
	Encrypted bool
}

// ExportItem gathers one Media item's full export payload from its own catalog
// plane: the global catalog for a plaintext Library, or the
// per-Library encrypted DB when the Library is Encrypted and Unsealed. It is a
// server-internal, reveal-unfiltered read for the bulk photo-export job — the
// caller owns Library authority and Seal state. A Sealed Encrypted Library has
// no readable plane and surfaces as ErrEncryptedLibraryDBDisabled ("absent until
// Unsealed", §2.9 mode 2).
func (s *Store) ExportItem(ctx context.Context, mediaID MediaID) (ExportItem, error) {
	if mediaID == "" {
		return ExportItem{}, errors.New("media ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return ExportItem{}, err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, mediaID)
	if err != nil {
		return ExportItem{}, err
	}

	item := ExportItem{Encrypted: plane.encrypted}

	// Reveal filtering is the caller's job (full-Library export), so read the row
	// directly from the resolved plane rather than the visibility-gated path.
	allLibraries := LibraryFilter{AllLibraries: true, Revealed: true}
	item.Media, err = s.visibleMediaSQLFrom(ctx, plane.reader, mediaID, allLibraries)
	if err != nil {
		return ExportItem{}, err
	}

	files, err := exportFilesFromPlane(ctx, plane.reader, mediaID)
	if err != nil {
		return ExportItem{}, err
	}
	item.Files = files

	item.Fields, err = s.mediaFieldsSQL(ctx, mediaID)
	if err != nil {
		return ExportItem{}, err
	}

	// Sidecar captions are keyed by the primary file's content fingerprint
	// (caption tracks belong to the bytes, ADR-0005 §4), read from the same plane.
	if len(files) > 0 {
		item.Captions, err = mediaFileSidecarCaptionsFromPlane(ctx, plane.reader, files[0].ContentFingerprint)
		if err != nil {
			return ExportItem{}, err
		}
	}

	item.Memberships, err = exportMembershipsFromPlane(ctx, plane.reader, mediaID)
	if err != nil {
		return ExportItem{}, err
	}

	return item, nil
}

func exportFilesFromPlane(ctx context.Context, db *sql.DB, mediaID MediaID) ([]ExportFile, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT path, content_fingerprint, version_label, part_ordinal
		FROM media_file
		WHERE media_id = ? AND missing_at_ms IS NULL
		ORDER BY version_label, part_ordinal
	`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []ExportFile
	for rows.Next() {
		var f ExportFile
		if err := rows.Scan(&f.Path, &f.ContentFingerprint, &f.VersionLabel, &f.PartOrdinal); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("media file not found")
	}
	return files, nil
}

func exportMembershipsFromPlane(ctx context.Context, db *sql.DB, mediaID MediaID) ([]MediaMembership, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.relation, e.kind, e.title
		FROM entity_relation r
		JOIN entity e ON e.id = r.from_entity_id
		WHERE r.to_media_id = ? AND e.trashed_at_ms IS NULL
		ORDER BY r.relation, e.kind, e.title, e.id
	`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	memberships := []MediaMembership{}
	for rows.Next() {
		var m MediaMembership
		if err := rows.Scan(&m.Relation, &m.Kind, &m.Title); err != nil {
			return nil, err
		}
		memberships = append(memberships, m)
	}
	return memberships, rows.Err()
}
