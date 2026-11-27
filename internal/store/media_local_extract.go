package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

const localExtractionProvenancePrefix = "local-extract:"

// LocalExtractionEntity pairs an Entity and its Media-target relation for one
// atomic local-extraction commit.
type LocalExtractionEntity struct {
	Entity   Entity
	Relation EntityRelation
}

// LocalExtractionInput carries the fields, entities/relations, and optional
// per-fingerprint completion marker produced by running local extractors on one
// Media file.
type LocalExtractionInput struct {
	LibraryID          LibraryID
	MediaID            MediaID
	ContentFingerprint string
	FieldBatches       [][]MediaField
	Entities           []LocalExtractionEntity
	MarkComplete       bool
	AttemptField       string
}

type validatedLocalEntity struct {
	entity     Entity
	relation   EntityRelation
	attributes any
	sortOrder  any
}

// ApplyLocalExtraction persists locally extracted fields, entities/relations,
// and the per-fingerprint completion marker inside one transaction so a partial
// write or crash leaves nothing half-applied and never suppresses retries.
func (s *Store) ApplyLocalExtraction(ctx context.Context, in LocalExtractionInput) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if in.LibraryID == "" || in.MediaID == "" {
		return errors.New("library ID and media ID are required")
	}
	if in.MarkComplete && in.ContentFingerprint == "" {
		return errors.New("content fingerprint is required when marking local extraction complete")
	}
	for _, batch := range in.FieldBatches {
		if len(batch) == 0 {
			continue
		}
		if err := validateMediaFields(batch); err != nil {
			return err
		}
	}
	validatedEntities := make([]validatedLocalEntity, 0, len(in.Entities))
	for _, item := range in.Entities {
		if err := validateEntity(item.Entity); err != nil {
			return err
		}
		if item.Entity.LibraryID != in.LibraryID {
			return errors.New("local entity library mismatch")
		}
		if item.Relation.FromEntityID != item.Entity.ID || item.Relation.Name == "" || item.Relation.ToMediaID != in.MediaID || item.Relation.ToEntityID != "" {
			return errors.New("invalid local entity relation")
		}
		switch item.Relation.Cardinality {
		case "", "one", "many":
		default:
			return errors.New("unknown relation cardinality " + item.Relation.Cardinality)
		}
		attributes, sortOrder, err := entityRelationRowValues(item.Relation)
		if err != nil {
			return err
		}
		validatedEntities = append(validatedEntities, validatedLocalEntity{
			entity:     item.Entity,
			relation:   item.Relation,
			attributes: attributes,
			sortOrder:  sortOrder,
		})
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, in.LibraryID)
	if err != nil {
		return err
	}

	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var fieldsWrote bool
	for _, batch := range in.FieldBatches {
		if len(batch) == 0 {
			continue
		}
		batchWrote, err := upsertMediaFieldsTx(ctx, tx, in.MediaID, batch, MutationSourceScan, false)
		if err != nil {
			return err
		}
		fieldsWrote = fieldsWrote || batchWrote
	}
	if s.testLocalExtractAfterFieldsHook != nil {
		if err := s.testLocalExtractAfterFieldsHook(); err != nil {
			return err
		}
	}

	noted := make([]Entity, 0, len(validatedEntities))
	for _, item := range validatedEntities {
		title, err := s.upsertEntityTx(ctx, tx, item.entity, false)
		if err != nil {
			return err
		}
		blocked, err := hydrationRelationBlocked(ctx, tx, item.relation)
		if err != nil {
			return err
		}
		if !blocked {
			if item.relation.Cardinality == "one" {
				if _, err := tx.ExecContext(ctx, `
					DELETE FROM entity_relation
					WHERE relation = ? AND to_media_id = ? AND from_entity_id <> ?
					  AND operator_locked_at_ms IS NULL
				`, item.relation.Name, item.relation.ToMediaID, item.relation.FromEntityID); err != nil {
					return err
				}
			}
			if err := insertMediaRelationTx(ctx, tx, item.relation, item.attributes, item.sortOrder); err != nil {
				return err
			}
		}
		if !plane.encrypted {
			e := item.entity
			e.Title = title
			noted = append(noted, e)
		}
	}

	if in.MarkComplete {
		marker := localExtractionProvenancePrefix + in.ContentFingerprint
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, ?)
			ON CONFLICT(media_id, field) DO NOTHING
		`, in.MediaID, marker, string(MutationSourceScan)); err != nil {
			return err
		}
		if in.AttemptField != "" {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, ?)
				ON CONFLICT(media_id, field) DO NOTHING
			`, in.MediaID, in.AttemptField, string(MutationSourceScan)); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	for _, entity := range noted {
		s.titleFolds.note(entity.LibraryID, entity.Kind, entity.ID, entity.Title)
	}
	if fieldsWrote && !plane.encrypted {
		s.emitMediaUpdated(ctx, in.MediaID)
	}
	return nil
}

const legacyPartReservationField = "legacy-part-reservation"

type reconcileGroupedFile struct {
	mediaID            MediaID
	mediaClass         string
	extra              int
	titleIdentified    int
	fileID             string
	contentFingerprint string
	path               string
	versionLabel       string
	partOrdinal        int
	missing            bool
}

type reconcileMediaGroup struct {
	mediaID         MediaID
	mediaClass      string
	extra           int
	titleIdentified int
	files           []reconcileGroupedFile
}

// ReconcileStandaloneLibraryMedia splits any live Media row in libraryID that
// previously grouped multiple Parts/Versions (or carries a non-default
// live version_label / part_ordinal) into one standalone Media per file while
// preserving certification, curator links, and missing-Version reservations (CORE-3029).
func (s *Store) ReconcileStandaloneLibraryMedia(ctx context.Context, libraryID LibraryID) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if libraryID == "" {
		return errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return err
	}

	const groupedQuery = `
		SELECT m.id, m.media_class, m.extra, m.title_identified,
		       f.id, f.content_fingerprint, f.path, f.version_label, f.part_ordinal,
		       f.missing_at_ms IS NOT NULL
		FROM media m
		JOIN media_file f ON f.media_id = m.id
		WHERE m.library_id = ?
		  AND m.trashed_at_ms IS NULL
		  AND (
			(SELECT COUNT(*) FROM media_file allf WHERE allf.media_id = m.id) > 1
			OR EXISTS (
			  SELECT 1 FROM media_file gf
			  WHERE gf.media_id = m.id AND gf.missing_at_ms IS NULL
			    AND (gf.version_label <> '' OR gf.part_ordinal <> 0)
			)
		  )
		ORDER BY m.id,
		         CASE WHEN f.missing_at_ms IS NULL THEN 0 ELSE 1 END,
		         f.version_label, f.part_ordinal, f.path, f.id
	`

	var preCount int
	if err := plane.reader.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM media m
		JOIN media_file f ON f.media_id = m.id
		WHERE m.library_id = ?
		  AND m.trashed_at_ms IS NULL
		  AND (
			(SELECT COUNT(*) FROM media_file allf WHERE allf.media_id = m.id) > 1
			OR EXISTS (
			  SELECT 1 FROM media_file gf
			  WHERE gf.media_id = m.id AND gf.missing_at_ms IS NULL
			    AND (gf.version_label <> '' OR gf.part_ordinal <> 0)
			)
		  )
	`, libraryID).Scan(&preCount); err != nil {
		return err
	}
	if preCount == 0 {
		return nil
	}

	// Pre-allocate encrypted IDs before taking the writer transaction because
	// allocateEncryptedMediaIDLocked queries plane readers.
	var preAllocatedEncryptedIDs []MediaID
	if plane.encrypted {
		preAllocatedEncryptedIDs = make([]MediaID, 0, preCount)
		for range preCount {
			id, err := s.allocateEncryptedMediaIDLocked(ctx)
			if err != nil {
				return err
			}
			preAllocatedEncryptedIDs = append(preAllocatedEncryptedIDs, id)
		}
	}

	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	groups, err := loadGroupedMediaForReconcile(ctx, tx, groupedQuery, libraryID)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}

	encIdx := 0
	var createdPublicIDs []MediaID
	for _, group := range groups {
		primary := group.files[0]
		liveCount := 0
		for _, f := range group.files {
			if !f.missing {
				liveCount++
			}
		}
		primaryNormalized := !primary.missing && (primary.versionLabel != "" || primary.partOrdinal != 0)
		if primaryNormalized {
			if _, err := tx.ExecContext(ctx, `
				UPDATE media_file SET version_label = '', part_ordinal = 0 WHERE id = ?
			`, primary.fileID); err != nil {
				return err
			}
		}
		if len(group.files) > 1 || primaryNormalized {
			if _, err := tx.ExecContext(ctx, `
				UPDATE media
				SET duration_seconds = 0, container = '', codec = '', video_width = 0, video_height = 0,
				    title = CASE
				      WHEN title_identified = 1 OR EXISTS (
				        SELECT 1 FROM media_field_provenance p
				        WHERE p.media_id = media.id AND p.field = 'title' AND p.source IN ('manual', 'operator')
				      ) THEN title
				      ELSE ?
				    END
				WHERE id = ?
			`, standaloneMediaTitle(primary.path), group.mediaID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM media_field_provenance
				WHERE media_id = ? AND field LIKE ? AND field <> ?
			`, group.mediaID, localExtractionProvenancePrefix+"%", localExtractionProvenancePrefix+primary.contentFingerprint); err != nil {
				return err
			}
			if err := resetGroupMarkersTx(ctx, tx, plane, group.mediaID, []string{"part-boundary", "chapter-import"}); err != nil {
				return err
			}
		}

		for _, file := range group.files[1:] {
			var newID MediaID
			if plane.encrypted {
				if encIdx < len(preAllocatedEncryptedIDs) {
					newID = preAllocatedEncryptedIDs[encIdx]
					encIdx++
				} else {
					var err error
					newID, err = s.randomEncryptedMediaIDLocked()
					if err != nil {
						return err
					}
				}
			} else {
				newID = mediaIDForFingerprint(file.contentFingerprint)
				for salt := 0; ; salt++ {
					var existing int
					qErr := tx.QueryRowContext(ctx, `SELECT 1 FROM media WHERE id = ?`, newID).Scan(&existing)
					if errors.Is(qErr, sql.ErrNoRows) {
						break
					}
					if qErr != nil {
						return qErr
					}
					newID = mediaIDForFingerprint(HashMediaFilePath(fmt.Sprintf("%s\x00split\x00%s\x00%d", file.contentFingerprint, group.mediaID, salt)))
				}
			}

			if _, err := tx.ExecContext(ctx, `
				INSERT INTO media(id, library_id, title, summary, year, duration_seconds, container, codec, video_width, video_height, media_class, mutated_by, title_identified, summary_identified, year_identified, extra)
				SELECT ?, library_id,
				       CASE
				         WHEN title_identified = 1 OR EXISTS (
				           SELECT 1 FROM media_field_provenance p
				           WHERE p.media_id = media.id AND p.field = 'title' AND p.source IN ('manual', 'operator')
				         ) THEN title
				         ELSE ?
				       END,
				       summary, year, 0, '', '', 0, 0, media_class, 'scan',
				       CASE
				         WHEN title_identified = 1 OR EXISTS (
				           SELECT 1 FROM media_field_provenance p
				           WHERE p.media_id = media.id AND p.field = 'title' AND p.source IN ('manual', 'operator')
				         ) THEN 1
				         ELSE 0
				       END,
				       summary_identified, year_identified, extra
				FROM media
				WHERE id = ?
			`, newID, standaloneMediaTitle(file.path), group.mediaID); err != nil {
				return err
			}
			if file.missing {
				// Preserve missing Version/Part reservations until reconnect so
				// offline files keep their Media identity regardless of digest format.
				if _, err := tx.ExecContext(ctx, `
					UPDATE media_file SET media_id = ? WHERE id = ?
				`, newID, file.fileID); err != nil {
					return err
				}
			} else {
				if _, err := tx.ExecContext(ctx, `
					UPDATE media_file SET media_id = ?, version_label = '', part_ordinal = 0 WHERE id = ?
				`, newID, file.fileID); err != nil {
					return err
				}
			}
			if err := copySplitMediaMetadataAndLinksTx(ctx, tx, group.mediaID, newID, plane.encrypted); err != nil {
				return err
			}
			if file.missing || strings.HasPrefix(file.contentFingerprint, "sha256:") {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, ?)
					ON CONFLICT(media_id, field) DO NOTHING
				`, newID, legacyPartReservationField, string(MutationSourceScan)); err != nil {
					return err
				}
			}
			if !plane.encrypted {
				createdPublicIDs = append(createdPublicIDs, newID)
			}
		}
		if (len(group.files) > 1 && primary.missing) || ((len(group.files) > 1 || primaryNormalized) && strings.HasPrefix(primary.contentFingerprint, "sha256:")) {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, ?)
				ON CONFLICT(media_id, field) DO NOTHING
			`, group.mediaID, legacyPartReservationField, string(MutationSourceScan)); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if len(createdPublicIDs) > 0 {
		eventTime := time.Now().UTC()
		for _, id := range createdPublicIDs {
			if eventErr := s.appendMediaEventLocked(ctx, MediaEventCreated, id, nil, eventTime); eventErr != nil {
				slog.Warn("media outbox event append failed", "kind", MediaEventCreated, "media", id, "error", eventErr)
			}
		}
	}
	return nil
}

func copySplitMediaMetadataAndLinksTx(ctx context.Context, tx *sql.Tx, srcID, dstID MediaID, encrypted bool) error {
	const splitPreservableFieldPredicate = `
		   (
			t.field = 'certification'
			OR (
			  t.field NOT LIKE 'local-extract:%'
			  AND t.field NOT LIKE 'artwork%'
			  AND (
				(t.field NOT IN ('capture-date', 'camera-make', 'camera-model', 'width', 'height', 'orientation', 'place')
				  AND NOT EXISTS (
					SELECT 1 FROM media_field_provenance p
					WHERE p.media_id = t.media_id AND p.field = t.field AND p.source = 'scan'
				  ))
				OR EXISTS (
				  SELECT 1 FROM media_field_provenance p
				  WHERE p.media_id = t.media_id AND p.field = t.field AND p.source IN ('manual', 'operator')
				)
			  )
			)
		   )`
	for _, stmt := range []string{
		`INSERT OR IGNORE INTO media_field_text(media_id, field, field_type, value)
		 SELECT ?, t.field, t.field_type, t.value FROM media_field_text t
		 WHERE t.media_id = ? AND` + splitPreservableFieldPredicate,
		`INSERT OR IGNORE INTO media_field_int(media_id, field, field_type, value)
		 SELECT ?, t.field, t.field_type, t.value FROM media_field_int t
		 WHERE t.media_id = ? AND` + splitPreservableFieldPredicate,
		`INSERT OR IGNORE INTO media_field_real(media_id, field, field_type, value)
		 SELECT ?, t.field, t.field_type, t.value FROM media_field_real t
		 WHERE t.media_id = ? AND` + splitPreservableFieldPredicate,
		`INSERT OR IGNORE INTO media_field_date(media_id, field, field_type, value_ms)
		 SELECT ?, t.field, t.field_type, t.value_ms FROM media_field_date t
		 WHERE t.media_id = ? AND` + splitPreservableFieldPredicate,
		`INSERT OR IGNORE INTO media_field_provenance(media_id, field, source, parser_owned)
		 SELECT ?, t.field, t.source, t.parser_owned
		 FROM media_field_provenance t
		 WHERE t.media_id = ?
		   AND (
			t.field = 'certification'
			OR (
			  t.field NOT LIKE 'local-extract:%'
			  AND t.field NOT LIKE 'artwork%'
			  AND (t.field <> 'title' OR t.source IN ('manual', 'operator'))
			  AND (
				(t.field NOT IN ('capture-date', 'camera-make', 'camera-model', 'width', 'height', 'orientation', 'place')
				  AND t.source <> 'scan')
				OR t.source IN ('manual', 'operator')
			  )
			)
		   )`,
		`INSERT OR IGNORE INTO media_field_rejection(media_id, field, field_type, value, rejected_at_ms)
		 SELECT ?, field, field_type, value, rejected_at_ms
		 FROM media_field_rejection
		 WHERE media_id = ?
		   AND field NOT LIKE 'local-extract:%'
		   AND field NOT LIKE 'artwork%'
		   AND field NOT IN ('capture-date', 'camera-make', 'camera-model', 'width', 'height', 'orientation', 'place')`,
		`INSERT OR IGNORE INTO collection_media(collection_id, media_id, sort_order, added_at_ms)
		 SELECT collection_id, ?, sort_order, added_at_ms
		 FROM collection_media
		 WHERE media_id = ?`,
		`INSERT OR IGNORE INTO entity_relation(
			from_entity_id, relation, to_media_id, attributes, sort_order, writer,
			operator_locked_at_ms, operator_lock_activity_id, operator_lock_one
		 )
		 SELECT from_entity_id, relation, ?, attributes, sort_order, writer,
		        operator_locked_at_ms, operator_lock_activity_id, operator_lock_one
		 FROM entity_relation
		 WHERE to_media_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, dstID, srcID); err != nil {
			return err
		}
	}
	if encrypted {
		// Encrypted library catalogs omit media_relation.attributes (0002_routed_private_catalog.sql).
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_relation(from_media_id, relation, to_media_id)
			SELECT ?, relation, to_media_id
			FROM media_relation
			WHERE from_media_id = ? AND to_media_id <> ?
		`, dstID, srcID, dstID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_relation(from_media_id, relation, to_media_id)
			SELECT from_media_id, relation, ?
			FROM media_relation
			WHERE to_media_id = ? AND from_media_id <> ?
		`, dstID, srcID, dstID); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_relation(from_media_id, relation, to_media_id, attributes)
			SELECT ?, relation, to_media_id, attributes
			FROM media_relation
			WHERE from_media_id = ? AND to_media_id <> ?
		`, dstID, srcID, dstID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_relation(from_media_id, relation, to_media_id, attributes)
			SELECT from_media_id, relation, ?, attributes
			FROM media_relation
			WHERE to_media_id = ? AND from_media_id <> ?
		`, dstID, srcID, dstID); err != nil {
			return err
		}
	}
	if !encrypted {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_tag(media_id, tag_id)
			SELECT ?, tag_id FROM media_tag WHERE media_id = ?
		`, dstID, srcID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO access_grant(id, profile_id, media_id, request_id, actor_profile_id, created_at_ms, updated_at_ms)
			SELECT id || ':' || ?, profile_id, ?, request_id, actor_profile_id, created_at_ms, updated_at_ms
			FROM access_grant
			WHERE media_id = ?
		`, string(dstID), dstID, srcID); err != nil {
			return err
		}
	}
	return nil
}

func loadGroupedMediaForReconcile(ctx context.Context, tx *sql.Tx, query string, libraryID LibraryID) ([]reconcileMediaGroup, error) {
	rows, err := tx.QueryContext(ctx, query, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []reconcileMediaGroup
	byMedia := map[MediaID]int{}
	for rows.Next() {
		var r reconcileGroupedFile
		if err := rows.Scan(
			&r.mediaID, &r.mediaClass, &r.extra, &r.titleIdentified,
			&r.fileID, &r.contentFingerprint, &r.path, &r.versionLabel, &r.partOrdinal, &r.missing,
		); err != nil {
			return nil, err
		}
		idx, ok := byMedia[r.mediaID]
		if !ok {
			idx = len(groups)
			byMedia[r.mediaID] = idx
			groups = append(groups, reconcileMediaGroup{
				mediaID:         r.mediaID,
				mediaClass:      r.mediaClass,
				extra:           r.extra,
				titleIdentified: r.titleIdentified,
			})
		}
		groups[idx].files = append(groups[idx].files, r)
	}
	return groups, rows.Err()
}

func standaloneMediaTitle(path string) string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem != "" {
		return stem
	}
	return base
}
