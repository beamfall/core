package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/beamfall/core/internal/serverrole"
)

// LocalRelationWriter is the Writer of the Media-target relation rows a local
// sidecar beside a Media's files owns. Only a later sidecar apply or release
// changes those rows: an automated upsert under another writer leaves them as
// they are, while an operator may still take one over.
const LocalRelationWriter = "local"

// releasedRelationWriter is the Writer a released sidecar's rows move to: an
// unowned automated writer, so the rows stay until another writer replaces
// them. It is its own name so a restored sidecar that lists a row again takes
// it back as local.
const releasedRelationWriter = "local-released"

// LocalMetadata is one parse of a Media's local sidecar, applied as a whole by
// ApplyLocalMetadata.
type LocalMetadata struct {
	// Metadata carries the built-in values the sidecar states. Its Source is
	// ignored: every value is written at the local tier.
	Metadata MediaMetadata
	// Fields are the plugin-declared Media fields the sidecar states.
	Fields []MediaField
	// Relations are the Media-target rows the sidecar states. Each must target
	// the Media and carry a (source Kind, Name) pair in OwnedRelations.
	Relations []EntityRelation
	// OwnedRelations are the relations the sidecar format owns. Each one's
	// local rows are replaced by Relations, so an owned relation with no
	// rows is emptied.
	OwnedRelations []WrittenRelation
	// SignatureField and Signature record which sidecar bytes were applied.
	SignatureField string
	Signature      string
}

// ApplyLocalMetadata replaces one Media's local-tier state with a sidecar
// parse in a single transaction on the Media's catalog plane: fields the
// previous parse wrote at the local tier drop to the scan tier, the parse's
// values are written at the local tier under the usual precedence, the local
// rows of each owned relation are replaced, and the signature is recorded.
// A failure leaves the previous state, signature included. A manual value
// and a row an operator or another writer owns are never changed.
//
// The encrypted catalog plane holds no sort titles, so a sort title is
// skipped there.
func (s *Store) ApplyLocalMetadata(ctx context.Context, id MediaID, local LocalMetadata) error {
	if id == "" {
		return ValidationError("media ID is required")
	}
	if local.SignatureField == "" || local.Signature == "" {
		return ValidationError("sidecar signature is required")
	}
	if local.Metadata.Title != nil && strings.TrimSpace(*local.Metadata.Title) == "" {
		return ValidationError("media title cannot be empty")
	}
	if err := validateMediaFields(local.Fields); err != nil {
		return err
	}
	owned := map[string]bool{}
	for _, relation := range local.OwnedRelations {
		if relation.Kind == "" || relation.Name == "" {
			return ValidationError("owned relation needs a kind and a name")
		}
		owned[relation.Kind+"\x00"+relation.Name] = true
	}
	if len(owned) > 0 || len(local.Relations) > 0 {
		// Authority face: Entity-relation catalog rows are main-only.
		if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
			return err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := releaseLocalFieldsTx(ctx, tx, id); err != nil {
		return err
	}
	metadata := local.Metadata
	if plane.encrypted {
		metadata.SortTitle = nil
	}
	if metadata.Title != nil || metadata.Summary != nil || metadata.Year != nil || metadata.SortTitle != nil {
		if err := updateMediaMetadataTx(ctx, tx, id, metadata, MutationSourceLocal); err != nil {
			return err
		}
	}
	if _, err := upsertMediaFieldsTx(ctx, tx, id, local.Fields, MutationSourceLocal); err != nil {
		return err
	}
	if err := replaceLocalRelationsTx(ctx, tx, id, local.OwnedRelations, owned, local.Relations); err != nil {
		return err
	}
	if err := recordSidecarSignatureTx(ctx, tx, id, local.SignatureField, local.Signature); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !plane.encrypted {
		s.emitMediaUpdated(ctx, id)
	}
	return nil
}

// ReleaseLocalMetadata records that one Media no longer has a local sidecar,
// in a single transaction: its local-tier fields drop to the scan tier and
// its local relation rows move to an unowned writer, both keeping their
// values, and the signature is cleared.
func (s *Store) ReleaseLocalMetadata(ctx context.Context, id MediaID, signatureField string) error {
	if id == "" {
		return ValidationError("media ID is required")
	}
	if signatureField == "" {
		return ValidationError("sidecar signature field is required")
	}
	// An Attached Origin Node authors no relation rows, so it holds no local
	// rows to release.
	releaseRelations := s.guardAuthority(serverrole.FaceCatalogRow) == nil
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	plane, err := s.catalogPlaneForMediaLocked(ctx, id)
	if err != nil {
		return err
	}
	tx, err := plane.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := releaseLocalFieldsTx(ctx, tx, id); err != nil {
		return err
	}
	if releaseRelations {
		if _, err := tx.ExecContext(ctx,
			`UPDATE entity_relation SET writer = ? WHERE to_media_id = ? AND writer = ?`,
			releasedRelationWriter, id, LocalRelationWriter); err != nil {
			return err
		}
	}
	if err := recordSidecarSignatureTx(ctx, tx, id, signatureField, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if !plane.encrypted {
		s.emitMediaUpdated(ctx, id)
	}
	return nil
}

// releaseLocalFieldsTx moves every local-tier field of one Media to the scan
// tier, keeping the values, so they stop reading as locked. A released
// built-in field's legacy identified mark is cleared with it, so a scan
// import can replace the value again.
func releaseLocalFieldsTx(ctx context.Context, tx *sql.Tx, id MediaID) error {
	rows, err := tx.QueryContext(ctx,
		"SELECT field FROM media_field_provenance WHERE media_id = ? AND source = ?",
		id, string(MutationSourceLocal))
	if err != nil {
		return err
	}
	var mirrors []string
	for rows.Next() {
		var field string
		if err := rows.Scan(&field); err != nil {
			_ = rows.Close()
			return err
		}
		if mirror, _ := lockMirror(field); mirror != "" {
			mirrors = append(mirrors, mirror)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, mirror := range mirrors {
		if _, err := tx.ExecContext(ctx, mirror, boolInt(false), id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx,
		"UPDATE media_field_provenance SET source = ? WHERE media_id = ? AND source = ?",
		string(MutationSourceScan), id, string(MutationSourceLocal))
	return err
}

// replaceLocalRelationsTx deletes the local rows of each owned relation and
// inserts the given rows as local. A row a released sidecar left behind is
// taken back as local; a row that exists under any other writer is left with
// that writer, and a single-cardinality relation another writer holds for the
// Media is not taken over. A released single-cardinality row is replaced by
// the restored sidecar's own.
func replaceLocalRelationsTx(ctx context.Context, tx *sql.Tx, id MediaID, ownedRelations []WrittenRelation, owned map[string]bool, relations []EntityRelation) error {
	for _, relation := range ownedRelations {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM entity_relation
			WHERE to_media_id = ? AND relation = ? AND writer = ?
			  AND from_entity_id IN (SELECT id FROM entity WHERE kind = ?)
		`, id, relation.Name, LocalRelationWriter, relation.Kind); err != nil {
			return err
		}
	}
	for _, relation := range relations {
		if relation.ToMediaID != id || relation.ToEntityID != "" {
			return ValidationError("sidecar relation must target its media")
		}
		if relation.FromEntityID == "" || relation.Name == "" {
			return ValidationError("sidecar relation needs a source entity and a name")
		}
		var kind string
		err := tx.QueryRowContext(ctx, `SELECT kind FROM entity WHERE id = ?`, relation.FromEntityID).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return ValidationError("sidecar relation source entity not found")
		}
		if err != nil {
			return err
		}
		if !owned[kind+"\x00"+relation.Name] {
			return ValidationError("sidecar relation is not one the sidecar owns")
		}
		if relation.Cardinality == "one" {
			// The sidecar's own released rows give way to its restored
			// statement; another writer's holder still wins (CORE-3175).
			// Single cardinality is per relation name, whatever the source
			// kind, as in UpsertEntityRelation.
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM entity_relation
				WHERE to_media_id = ? AND relation = ? AND from_entity_id <> ? AND writer = ?
			`, id, relation.Name, relation.FromEntityID, releasedRelationWriter); err != nil {
				return err
			}
			var held int
			err := tx.QueryRowContext(ctx, `
				SELECT 1 FROM entity_relation
				WHERE to_media_id = ? AND relation = ? AND from_entity_id <> ? LIMIT 1
			`, id, relation.Name, relation.FromEntityID).Scan(&held)
			if err == nil {
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		attributes, sortOrder, err := entityRelationRowValues(relation)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO entity_relation(from_entity_id, relation, to_media_id, attributes, sort_order, writer)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(from_entity_id, relation, to_media_id) WHERE to_media_id IS NOT NULL DO UPDATE SET
			  attributes = excluded.attributes, sort_order = excluded.sort_order, writer = excluded.writer
			WHERE entity_relation.writer = ?
		`, relation.FromEntityID, relation.Name, id, attributes, sortOrder, LocalRelationWriter, releasedRelationWriter); err != nil {
			return err
		}
	}
	return nil
}

// recordSidecarSignatureTx stores the sidecar signature whatever the field's
// lock: it is bookkeeping of what was applied, not a value an operator edits.
// An empty signature clears it.
func recordSidecarSignatureTx(ctx context.Context, tx *sql.Tx, id MediaID, field, signature string) error {
	if err := replaceMediaFieldValueTx(ctx, tx, id, MediaField{Name: field, Type: EntityFieldText, Text: signature}); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO media_field_provenance(media_id, field, source) VALUES (?, ?, ?)
		ON CONFLICT(media_id, field) DO UPDATE SET source = excluded.source, parser_owned = 0
	`, id, field, string(MutationSourceScan))
	return err
}
