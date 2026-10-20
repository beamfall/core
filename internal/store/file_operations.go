package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Preview-first file organize / move / merge over Media files (ADR-0009 §5–6),
// sharing the dry-run / batch-undo envelope with bulk edit. Three verbs:
//
//   - OrganizePreview — pure read: the old→new path change-set a naming
//     template WOULD produce. No disk touch, no journal.
//   - OrganizeApply — moves each file on disk to its templated path, updates the
//     media_file.path record (the file's content fingerprint is unchanged, so
//     this is a move, never delete+create — ADR-0009 §1), and journals the prior
//     path as one undo unit. UndoBatch (bulk_edit.go) moves the bytes back.
//   - MergeApply — resolves a duplicate cluster: keep one survivor, route every
//     other member through the audited Trash path (invariant #6 — recoverable,
//     never hard-deleted). The journal records each trashed id so UndoBatch
//     restores them from Trash.
//
// Naming tokens are plugin-declared field names plus the core {title} token
// (ADR-0009 §5 / ADR-0003's fixed-vocabulary discipline). The engine substitutes
// whatever field values the Media actually carries; it knows no domain words, so
// the AGPL firewall (invariant #1) holds — every plugin's tokens render through
// the identical generic substitution path.

// FileChange is one Media's path-move preview row.
type FileChange struct {
	MediaID MediaID
	Title   string
	OldPath string
	NewPath string
}

// fileMoveItemState is the per-item journal snapshot for an organize/move: the
// path the file occupied before the move, restored on undo.
type fileMoveItemState struct {
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

// mergeItemState is the per-item journal snapshot for a merge: the trashed
// member's id (restored from Trash on undo). The survivor is never journaled —
// it was not mutated.
type mergeItemState struct {
	Trashed bool `json:"trashed"`
}

// OrganizePreview renders the template against every live, visible Media in one
// Library and returns the old→new path change-set (ADR-0009 §5 preview-first).
// A Media whose templated path equals its current path, or whose template
// references a field it lacks, is omitted (nothing to move / cannot place). No
// file is touched.
func (s *Store) OrganizePreview(ctx context.Context, libraryID LibraryID, template string, filter LibraryFilter) ([]FileChange, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return nil, ValidationError("organize template is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	root, ok, err := s.libraryRootForOrganize(ctx, libraryID, filter)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []FileChange{}, nil
	}
	plan, err := s.organizePlanLocked(ctx, libraryID, template, root)
	if err != nil {
		return nil, err
	}
	changes := make([]FileChange, len(plan))
	for i, p := range plan {
		changes[i] = FileChange{MediaID: p.mediaID, Title: p.title, OldPath: p.oldPath, NewPath: p.newPath}
	}
	return changes, nil
}

// OrganizeApply moves each planned file to its templated path and journals the
// move as one undo-able batch. Disk move is mkdir-parents + rename; the
// media_file.path record is repointed in the same transaction. A failure aborts
// the catalog transaction and reverse-compensates completed disk moves; any
// compensation failure is returned with the primary error. Returns the batch id
// and count.
func (s *Store) OrganizeApply(ctx context.Context, libraryID LibraryID, template string, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return BulkOpBatch{}, ValidationError("organize template is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BulkOpBatch{}, err
	}
	root, ok, err := s.libraryRootForOrganize(ctx, libraryID, filter)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if !ok {
		return BulkOpBatch{}, ValidationError("unknown library")
	}
	plan, err := s.organizePlanLocked(ctx, libraryID, template, root)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if len(plan) == 0 {
		return BulkOpBatch{ID: "", Kind: opKindOrganize, Affected: 0}, nil
	}
	plan, err = s.prepareFileMoves(plan)
	if err != nil {
		return BulkOpBatch{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()

	batchID, err := newBatchID()
	if err != nil {
		return BulkOpBatch{}, err
	}
	// The batch parent must precede its items (op_batch_item.batch_id FK).
	if err := insertBatchTx(ctx, tx, batchID, opKindOrganize, now); err != nil {
		return BulkOpBatch{}, err
	}
	moved, err := applyFileMovesTx(ctx, tx, batchID, plan)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		// The catalog write failed after the bytes already moved on disk;
		// move them back so disk and catalog stay consistent.
		return BulkOpBatch{}, compensateFileMoves(err, plan[:moved])
	}
	// Emit AFTER the catalog tx committed (cross-DB ordering: see
	// appendMediaEventLocked). A path move changes the file location clients read
	// from the Media payload, so announce media.updated for each moved item.
	for _, item := range plan[:moved] {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, item.mediaID, nil, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", item.mediaID, "error", eventErr)
		}
	}
	return BulkOpBatch{ID: batchID, Kind: opKindOrganize, Affected: moved}, nil
}

// DuplicateMergeMembers returns the exact live Media set MergeApply would
// consider for keepID after library visibility has been applied. The returned
// slice includes keepID; an empty slice means keepID is not in a duplicate
// cluster.
func (s *Store) DuplicateMergeMembers(ctx context.Context, libraryID LibraryID, keepID MediaID, filter LibraryFilter) ([]MediaID, error) {
	if keepID == "" {
		return nil, ValidationError("keep media id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	visible, err := s.libraryVisibleLocked(ctx, libraryID, filter)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ValidationError("unknown library")
	}
	members, err := s.duplicateClusterLocked(ctx, libraryID, keepID)
	if err != nil {
		return nil, err
	}
	return append([]MediaID(nil), members...), nil
}

// MergeApply resolves a duplicate cluster by keeping keepID and routing every
// other member of the cluster (the live Media sharing keepID's non-empty oshash
// in this Library — ADR-0009 §1 content-fingerprint identity) through the
// audited Trash path. Each trashed id is journaled so UndoBatch restores it
// from Trash. Routing through Trash keeps a wrong merge reversible
// (invariant #6); no bytes are ever hard-deleted here.
func (s *Store) MergeApply(ctx context.Context, libraryID LibraryID, keepID MediaID, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	if keepID == "" {
		return BulkOpBatch{}, ValidationError("keep media id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BulkOpBatch{}, err
	}

	visible, err := s.libraryVisibleLocked(ctx, libraryID, filter)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if !visible {
		return BulkOpBatch{}, ValidationError("unknown library")
	}
	members, err := s.duplicateClusterLocked(ctx, libraryID, keepID)
	if err != nil {
		return BulkOpBatch{}, err
	}
	if len(members) == 0 {
		return BulkOpBatch{}, ValidationError("keep media is not part of a duplicate cluster")
	}

	batchID, err := newBatchID()
	if err != nil {
		return BulkOpBatch{}, err
	}
	state, err := json.Marshal(mergeItemState{Trashed: true})
	if err != nil {
		return BulkOpBatch{}, err
	}

	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BulkOpBatch{}, err
	}
	defer tx.Rollback()

	// The batch parent must precede its items (op_batch_item.batch_id FK); a
	// zero-item merge rolls the whole transaction back.
	if err := insertBatchTx(ctx, tx, batchID, opKindMerge, now); err != nil {
		return BulkOpBatch{}, err
	}
	seq := 0
	trashed := make([]MediaID, 0, len(members))
	for _, member := range members {
		if member == keepID {
			continue
		}
		// Soft-delete only — destruction routes through the audited Trash
		// state (invariant #6). The WHERE trashed_at_ms IS NULL guard makes a
		// re-merge idempotent: an already-trashed member is left alone.
		result, err := tx.ExecContext(ctx,
			`UPDATE media SET trashed_at_ms = ? WHERE id = ? AND trashed_at_ms IS NULL`,
			timeToMillis(now.UTC()), member)
		if err != nil {
			return BulkOpBatch{}, err
		}
		if err := appendBatchItemTx(ctx, tx, batchID, seq, member, state); err != nil {
			return BulkOpBatch{}, err
		}
		// Only a member this call actually soft-deleted gets a deletion event;
		// the idempotent re-merge guard above leaves an already-trashed member's
		// row untouched, so it must not re-announce a deletion.
		if affected, raErr := result.RowsAffected(); raErr == nil && affected > 0 {
			trashed = append(trashed, member)
		}
		seq++
	}
	if seq == 0 {
		return BulkOpBatch{ID: "", Kind: opKindMerge, Affected: 0}, tx.Rollback()
	}
	if err := tx.Commit(); err != nil {
		return BulkOpBatch{}, err
	}
	// Emit AFTER the catalog tx committed (cross-DB ordering: see
	// appendMediaEventLocked). Each merged-away loser is gone like a trash, so it
	// emits media.deleted carrying the cluster's LibraryID (the delivery side
	// reveal-filters by Library restriction, not a live read of the now-trashed
	// row). The survivor changed identity-wise (it absorbed the cluster), so it
	// emits media.updated. A failed outbox append never fails the merge.
	deletePayload := mediaDeletedPayload(libraryID)
	for _, member := range trashed {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventDeleted, member, deletePayload, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventDeleted, "media", member, "error", eventErr)
		}
	}
	if len(trashed) > 0 {
		if eventErr := s.appendMediaEventLocked(ctx, MediaEventUpdated, keepID, nil, now); eventErr != nil {
			slog.Warn("media outbox event append failed", "kind", MediaEventUpdated, "media", keepID, "error", eventErr)
		}
	}
	return BulkOpBatch{ID: batchID, Kind: opKindMerge, Affected: seq}, nil
}

// ---- undo item handlers (called from UndoBatch) ----

// undoFileMoveItemTx moves one file back to its prior path on disk and repoints
// the media_file.path record. A missing source (the file was moved again out of
// band) leaves the record repointed to the journaled prior path without erroring
// the batch — the next scan reconciles disk reality, and the catalog reflects
// the intended post-undo location.
func (s *Store) undoFileMoveItemTx(ctx context.Context, tx *sql.Tx, item opBatchItem) (organizeMove, bool, error) {
	var state fileMoveItemState
	if err := json.Unmarshal([]byte(item.PriorState), &state); err != nil {
		return organizeMove{}, false, err
	}
	move := organizeMove{mediaID: item.MediaID, oldPath: state.NewPath, newPath: state.OldPath}
	prepared, err := s.prepareFileMoves([]organizeMove{move})
	moved := false
	if err == nil {
		move = prepared[0]
		err = executeManagedMove(move.move, state.NewPath, state.OldPath)
		moved = err == nil
	}
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return organizeMove{}, false, err
		}
		moved = false
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE media_file SET path = ? WHERE media_id = ? AND path = ?`,
		state.OldPath, item.MediaID, state.NewPath)
	if err != nil {
		return move, moved, err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE subscription_seen_entry SET path = ? WHERE path = ?`,
		state.OldPath, state.NewPath)
	return move, moved, err
}

// undoMergeItemTx restores one merged-away member from Trash (the reverse of the
// merge's audited soft-delete), inside the undo transaction. Visibility was
// already re-confirmed by UndoBatch before this runs. The bool reports whether
// the item was actually resurrected (a loser that wasn't trashed needs no work,
// and must not emit a media.created — there is nothing to re-add).
func undoMergeItemTx(ctx context.Context, tx *sql.Tx, item opBatchItem) (bool, error) {
	var state mergeItemState
	if err := json.Unmarshal([]byte(item.PriorState), &state); err != nil {
		return false, err
	}
	if !state.Trashed {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE media SET trashed_at_ms = NULL WHERE id = ?`, item.MediaID); err != nil {
		return false, err
	}
	return true, nil
}

// ---- organize plan + disk move helpers ----

type organizeMove struct {
	mediaID MediaID
	title   string
	oldPath string
	newPath string
	move    managedMove
}

// libraryRootForOrganize resolves the (single) root a templated organize places
// files under, gated on the Library being visible to the caller. The second
// return is false when the Library is invisible/unknown or has no root.
func (s *Store) libraryRootForOrganize(ctx context.Context, libraryID LibraryID, filter LibraryFilter) (string, bool, error) {
	visible, err := s.libraryVisibleLocked(ctx, libraryID, filter)
	if err != nil {
		return "", false, err
	}
	if !visible {
		return "", false, nil
	}
	roots, err := libraryRootsSQL(ctx, s.catalogR, libraryID)
	if err != nil {
		return "", false, err
	}
	if len(roots) == 0 {
		return "", false, nil
	}
	return roots[0], true, nil
}

// libraryVisibleLocked reports whether a Library exists and is visible to the
// caller's filter. Caller holds the read lock.
func (s *Store) libraryVisibleLocked(ctx context.Context, libraryID LibraryID, filter LibraryFilter) (bool, error) {
	libraries, err := loadLibraries(ctx, s.catalogR)
	if err != nil {
		return false, err
	}
	for _, library := range libraries {
		if library.ID == libraryID && libraryVisible(library, filter) {
			return true, nil
		}
	}
	return false, nil
}

// duplicateClusterLocked returns the live Media in one Library that are
// byte-identical to keepID: distinct Media whose available (non-missing) files
// share keepID's non-empty oshash (ADR-0009 §1 content-fingerprint identity).
// The result includes keepID itself; an empty result means keepID has no
// duplicate (or no hashable file). Caller holds the read lock.
func (s *Store) duplicateClusterLocked(ctx context.Context, libraryID LibraryID, keepID MediaID) ([]MediaID, error) {
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT DISTINCT m.id
		FROM media m
		JOIN media_file f ON f.media_id = m.id AND f.missing_at_ms IS NULL AND f.oshash <> ''
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL`+mediaExcludeExtrasClause+`
		  AND f.oshash IN (
			SELECT g.oshash FROM media_file g
			WHERE g.media_id = ? AND g.missing_at_ms IS NULL AND g.oshash <> ''
		  )
		ORDER BY m.id
	`, libraryID, keepID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []MediaID
	keepPresent := false
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id == keepID {
			keepPresent = true
		}
		members = append(members, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A cluster needs keepID plus at least one other member; a lone file (or a
	// keepID with no hashable file) is not a duplicate cluster.
	if !keepPresent || len(members) < 2 {
		return nil, nil
	}
	return members, nil
}

// organizePlanLocked builds the move plan for one Library: for every live Media
// with an available primary file, render the template and pair the current path
// with the new absolute path under root. Media the template cannot fully render
// (a referenced token has no value), or whose new path equals the old, are
// dropped. Caller holds the read lock.
func (s *Store) organizePlanLocked(ctx context.Context, libraryID LibraryID, template, root string) ([]organizeMove, error) {
	if templateHasTraversal(template) {
		return nil, ValidationError("organize template must not contain a \"..\" path segment")
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT m.id, m.title, f.path
		FROM media m
		JOIN media_file f ON f.media_id = m.id AND f.missing_at_ms IS NULL
		WHERE m.library_id = ? AND m.trashed_at_ms IS NULL
		ORDER BY m.id
	`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type mediaRow struct {
		id      MediaID
		title   string
		oldPath string
	}
	var mediaRows []mediaRow
	for rows.Next() {
		var r mediaRow
		if err := rows.Scan(&r.id, &r.title, &r.oldPath); err != nil {
			return nil, err
		}
		mediaRows = append(mediaRows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	plan := make([]organizeMove, 0, len(mediaRows))
	for _, r := range mediaRows {
		fields, err := s.mediaFieldsSQL(ctx, r.id)
		if err != nil {
			return nil, err
		}
		rendered, ok := renderTemplate(template, r.title, fields)
		if !ok {
			continue
		}
		ext := filepath.Ext(r.oldPath)
		newPath := filepath.Join(root, filepath.FromSlash(rendered)+ext)
		// Containment guard: a substituted field or a literal template segment
		// could still resolve outside the Library root once Join/Clean runs.
		// Refuse to move a file (and repoint its record) anywhere but under root.
		if err := assertUnderRoot(root, newPath); err != nil {
			return nil, err
		}
		if newPath == r.oldPath {
			continue
		}
		plan = append(plan, organizeMove{mediaID: r.id, title: r.title, oldPath: r.oldPath, newPath: newPath})
	}
	return plan, nil
}

// applyFileMovesTx performs each planned move on disk and repoints the
// media_file.path record, journaling the prior path. On any error mid-plan it
// reverse-compensates disk moves it already made (the transaction the caller
// will roll back covers the catalog side) and reports every compensation error.
// Returns the number moved.
func applyFileMovesTx(ctx context.Context, tx *sql.Tx, batchID string, plan []organizeMove) (int, error) {
	seq := 0
	for _, m := range plan {
		state, err := json.Marshal(fileMoveItemState{OldPath: m.oldPath, NewPath: m.newPath})
		if err != nil {
			return 0, compensateFileMoves(err, plan[:seq])
		}
		if err := executeManagedMove(m.move, m.oldPath, m.newPath); err != nil {
			return 0, compensateFileMoves(err, plan[:seq])
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE media_file SET path = ? WHERE media_id = ? AND path = ?`,
			m.newPath, m.mediaID, m.oldPath); err != nil {
			return 0, compensateFileMoves(err, plan[:seq+1])
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE subscription_seen_entry SET path = ? WHERE path = ?`,
			m.newPath, m.oldPath); err != nil {
			return 0, compensateFileMoves(err, plan[:seq+1])
		}
		if err := appendBatchItemTx(ctx, tx, batchID, seq, m.mediaID, state); err != nil {
			return 0, compensateFileMoves(err, plan[:seq+1])
		}
		seq++
	}
	return seq, nil
}

// revertFileMoves moves completed moves back in reverse order so chained paths
// are vacated before their predecessors are restored. Every failure is returned.
func revertFileMoves(done []organizeMove) error {
	var joined error
	for i := len(done) - 1; i >= 0; i-- {
		m := done[i]
		if err := executeManagedMove(m.move, m.newPath, m.oldPath); err != nil {
			joined = errors.Join(joined, fmt.Errorf("compensate file move %q -> %q: %w", m.newPath, m.oldPath, err))
		}
	}
	return joined
}

func compensateFileMoves(cause error, done []organizeMove) error {
	return errors.Join(cause, revertFileMoves(done))
}

func (s *Store) prepareFileMoves(plan []organizeMove) ([]organizeMove, error) {
	prepared := append([]organizeMove(nil), plan...)
	for i := range prepared {
		move, err := s.prepareManagedMove(prepared[i].oldPath, prepared[i].newPath)
		if err != nil {
			return nil, err
		}
		prepared[i].move = move
	}
	return prepared, nil
}

// renderTemplate substitutes {title} and {<field name>} tokens with the Media's
// values. Returns false if any referenced token has no value — a partial
// organize path would misfile the content, so the item is dropped from the plan
// rather than placed under a half-rendered name. The token vocabulary is
// open-ended and domain-agnostic: whatever fields the Media carries.
func renderTemplate(template, title string, fields []MediaField) (string, bool) {
	values := map[string]string{"title": title}
	for _, f := range fields {
		values[f.Name] = fieldDisplayValue(f)
	}
	var out strings.Builder
	i := 0
	for i < len(template) {
		c := template[i]
		if c != '{' {
			out.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			out.WriteByte(c)
			i++
			continue
		}
		name := template[i+1 : i+end]
		value, ok := values[name]
		if !ok || strings.TrimSpace(value) == "" {
			return "", false
		}
		out.WriteString(sanitizePathSegment(value))
		i += end + 1
	}
	return out.String(), true
}

// fieldDisplayValue renders a field value to a string for template
// substitution, by its declared type.
func fieldDisplayValue(f MediaField) string {
	switch f.Type {
	case EntityFieldText, EntityFieldURL:
		return f.Text
	case EntityFieldNumber:
		return strconv.FormatFloat(f.Number, 'f', -1, 64)
	case EntityFieldFlag:
		if f.Flag {
			return "true"
		}
		return "false"
	case EntityFieldDate:
		return f.Date.UTC().Format("2006-01-02")
	}
	return ""
}

// sanitizePathSegment strips path separators and other characters that would
// break out of the intended directory tree from a substituted token value. A
// value that is exactly "." or ".." is itself a traversal segment, so it is
// neutralised to "_" rather than passed through verbatim.
func sanitizePathSegment(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-", "?", "-",
		"\"", "-", "<", "-", ">", "-", "|", "-",
	)
	value = replacer.Replace(value)
	if value == "." || value == ".." {
		return "_"
	}
	return value
}

// templateHasTraversal reports whether the template's literal text (the
// characters renderTemplate copies through verbatim, outside {token} spans)
// contains a "." or ".." path segment, which would escape the Library root.
func templateHasTraversal(template string) bool {
	for _, seg := range strings.FieldsFunc(filepath.ToSlash(template), func(r rune) bool { return r == '/' }) {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}

// assertUnderRoot returns a ValidationError unless target resolves to a path at
// or below root. It is the containment backstop: even after sanitisation and the
// literal-template check, a crafted field value or template combination could
// resolve outside root once filepath.Join/Clean runs, and an escaping path must
// never be moved to or recorded.
func assertUnderRoot(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ValidationError("organize target escapes the library root: " + target)
	}
	return nil
}
