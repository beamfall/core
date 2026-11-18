package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/beamfall/core/internal/serverrole"
)

// entityTitleFoldPrefix prefixes the server_config rows recording that
// ReopenFoldedEntityTitles already ran for one (plugin, kind).
const entityTitleFoldPrefix = "entity-title-fold."

// FoldEntityTitle is the comparison form of an Entity title: lower case,
// letters and digits only. Two titles that differ only in case, spacing or
// punctuation ("Gangs of London", "gangs.of.london") fold equal.
func FoldEntityTitle(title string) string {
	var out strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(unicode.ToLower(r))
		}
	}
	return out.String()
}

// ReopenFoldedEntityTitles is the one-time repair for a plugin's Entities of a
// kind that hydration used to identify by a title read from each file name,
// so files whose names spell one title in different case or punctuation were
// split across several Entities. For every set of two or more live Entities
// of the kind in one Library whose titles fold equal (FoldEntityTitle), the
// hydration marker of every Media they relate to, directly or through an
// Entity that relates to them, is cleared and the Library is recorded as
// awaiting a backfill scan (see PendingEntityScopeBackfills). The next scan
// hydrates those Media again under the mapper's current identity, and the
// hydration adapter keeps the oldest of the variants and merges the others
// into it. Nothing is deleted here.
//
// It runs once per (plugin, kind): the run is recorded in server_config. It
// returns how many Entities it reopened. Only the shared catalog is repaired.
func (s *Store) ReopenFoldedEntityTitles(ctx context.Context, pluginID, kind, markerField string, now time.Time) (int, error) {
	// Authority face: Entity-relation catalog rows are main-only.
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return 0, err
	}
	if pluginID == "" || kind == "" || markerField == "" {
		return 0, errors.New("plugin ID, entity kind and marker field are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return 0, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
		ON CONFLICT(key) DO NOTHING
	`, entityTitleFoldPrefix+pluginID+"."+kind, timeToMillis(now))
	if err != nil {
		return 0, err
	}
	if first, err := result.RowsAffected(); err != nil || first == 0 {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.library_id, e.title FROM entity e
		JOIN library l ON l.id = e.library_id AND l.plugin_id = ?
		WHERE e.kind = ? AND e.trashed_at_ms IS NULL
		ORDER BY e.id
	`, pluginID, kind)
	if err != nil {
		return 0, err
	}
	type foldKey struct {
		library LibraryID
		title   string
	}
	groups := map[foldKey][]EntityID{}
	for rows.Next() {
		var id EntityID
		var library LibraryID
		var title string
		if err := rows.Scan(&id, &library, &title); err != nil {
			_ = rows.Close()
			return 0, err
		}
		key := foldKey{library: library, title: FoldEntityTitle(title)}
		groups[key] = append(groups[key], id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	keys := make([]foldKey, 0, len(groups))
	for key, ids := range groups {
		if len(ids) > 1 && key.title != "" {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].library != keys[j].library {
			return keys[i].library < keys[j].library
		}
		return keys[i].title < keys[j].title
	})
	reopened := 0
	for _, key := range keys {
		for _, id := range groups[key] {
			cleared, err := tx.ExecContext(ctx, `
				DELETE FROM media_field_int
				WHERE field = ? AND media_id IN (
					SELECT to_media_id FROM entity_relation
					WHERE from_entity_id = ? AND to_media_id IS NOT NULL
					UNION
					SELECT member.to_media_id FROM entity_relation holder
					JOIN entity_relation member ON member.from_entity_id = holder.from_entity_id
					WHERE holder.to_entity_id = ? AND member.to_media_id IS NOT NULL
				)
			`, markerField, id, id)
			if err != nil {
				return 0, err
			}
			reopened++
			if n, err := cleared.RowsAffected(); err != nil {
				return 0, err
			} else if n > 0 {
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
					ON CONFLICT(key) DO NOTHING
				`, entityScopeBackfillPrefix+string(key.library), timeToMillis(now)); err != nil {
					return 0, err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return reopened, nil
}

// ReopenMediaHydration leaves one Media for hydration to finish later: its
// hydration marker is cleared and its Library recorded as awaiting a backfill
// scan (see PendingEntityScopeBackfills), in one transaction. Hydration calls
// it when a relation write (a title-variant merge among them) fails, since an
// ordinary scan does not hydrate the unchanged file again and a backfill skips
// a Media that still carries its marker.
func (s *Store) ReopenMediaHydration(ctx context.Context, libraryID LibraryID, mediaID MediaID, markerField string, now time.Time) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if libraryID == "" || mediaID == "" || markerField == "" {
		return errors.New("library ID, media ID and marker field are required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM media_field_int WHERE field = ? AND media_id = ?`, markerField, mediaID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO server_config(key, value, updated_at_ms) VALUES (?, '', ?)
		ON CONFLICT(key) DO NOTHING
	`, entityScopeBackfillPrefix+string(libraryID), timeToMillis(now)); err != nil {
		return err
	}
	return tx.Commit()
}

// CopyEntityItemState copies the per-Profile item state (ratings and
// watchlist entries) and the tags of one Entity onto another, so merging two
// Entities that are one item keeps what people recorded against either. A
// row the destination already has wins. The source's rows are kept, so
// undoing the merge loses nothing.
func (s *Store) CopyEntityItemState(ctx context.Context, from, to EntityID) error {
	if from == "" || to == "" {
		return errors.New("source and destination entity IDs are required")
	}
	if from == to {
		return nil
	}
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if err := s.guardAuthority(serverrole.FaceEngagement); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		INSERT OR IGNORE INTO entity_tag(entity_id, tag_id)
		SELECT ?, tag_id FROM entity_tag WHERE entity_id = ?
	`, to, from); err != nil {
		return err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms)
		SELECT profile_id, target_kind, ?, rating, rated_at_ms FROM item_rating
		WHERE target_kind = 'entity' AND target_id = ?
	`, to, from); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms)
		SELECT profile_id, target_kind, ?, added_at_ms FROM watchlist_item
		WHERE target_kind = 'entity' AND target_id = ?
	`, to, from); err != nil {
		return err
	}
	return tx.Commit()
}

// titleFoldUnrecordedPrefix prefixes the server_config row MergeTitleFoldVariants
// writes in the merge's own transaction, naming a merge batch whose activity
// entry is still owed (the value is the Library ID). The activity log lives in
// another database, so the two cannot commit together; the row is how a merge
// whose entry failed, or never got written because the process stopped, is
// found again.
const titleFoldUnrecordedPrefix = "entity-title-fold-unrecorded."

// TitleFoldMerge is a title-fold merge batch whose activity entry is owed.
type TitleFoldMerge struct {
	BatchID   string
	LibraryID LibraryID
	MergedAt  time.Time
}

// MergeTitleFoldVariants is MergeEntities for hydration's title-fold repair.
// In the same transaction it records the batch as owing its activity entry
// (UnrecordedTitleFoldMerges), until MarkTitleFoldMergeRecorded clears it.
func (s *Store) MergeTitleFoldVariants(ctx context.Context, winnerID, loserID EntityID, filter LibraryFilter, now time.Time) (BulkOpBatch, error) {
	return s.mergeEntities(ctx, winnerID, loserID, filter, now, true)
}

// UnrecordedTitleFoldMerges lists the title-fold merges whose activity entry
// is owed, oldest first. It reads a key range of server_config, normally
// empty.
func (s *Store) UnrecordedTitleFoldMerges(ctx context.Context) ([]TitleFoldMerge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	end := titleFoldUnrecordedPrefix[:len(titleFoldUnrecordedPrefix)-1] + "/" // the byte after "."
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT key, value, updated_at_ms FROM server_config
		WHERE key >= ? AND key < ?
		ORDER BY updated_at_ms, key
	`, titleFoldUnrecordedPrefix, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TitleFoldMerge
	for rows.Next() {
		var key, library string
		var mergedAt int64
		if err := rows.Scan(&key, &library, &mergedAt); err != nil {
			return nil, err
		}
		out = append(out, TitleFoldMerge{
			BatchID:   strings.TrimPrefix(key, titleFoldUnrecordedPrefix),
			LibraryID: LibraryID(library),
			MergedAt:  millisToTime(mergedAt),
		})
	}
	return out, rows.Err()
}

// MarkTitleFoldMergeRecorded clears the owed-entry row of a title-fold merge
// once its activity entry is written.
func (s *Store) MarkTitleFoldMergeRecorded(ctx context.Context, batchID string) error {
	if batchID == "" {
		return errors.New("batch ID is required")
	}
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `DELETE FROM server_config WHERE key = ?`, titleFoldUnrecordedPrefix+batchID)
	return err
}

// LiveFoldEqualEntities lists the live Entities of a kind in a Library whose
// titles fold equal to title, oldest first (catalog insertion order, which an
// Entity keeps through updates, trash and restore). With a scope relation,
// only Entities whose relation of that name points at scopeTarget are listed:
// that lookup seeks the relation index and reads only the scope's members.
// Without one it reads the Store's title-fold index for the Library and kind
// (entityTitleFoldIndex), so a scan's lookups cost one pass over the kind,
// not one per call, and checks each candidate against its row.
func (s *Store) LiveFoldEqualEntities(ctx context.Context, libraryID LibraryID, kind, title, scopeRelation string, scopeTarget EntityID) ([]Entity, error) {
	if libraryID == "" || kind == "" {
		return nil, errors.New("library ID and entity kind are required")
	}
	folded := FoldEntityTitle(title)
	if folded == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	if scopeRelation != "" {
		return foldEqualRows(ctx, plane.reader, folded, libraryID, kind, titleFoldScopedSQL, scopeTarget, scopeRelation, libraryID, kind)
	}
	if plane.encrypted {
		// An encrypted Library's Entities live in its own database, which
		// title folding never merges (EntityTitleFoldApplies); read it plainly.
		return foldEqualRows(ctx, plane.reader, folded, libraryID, kind, `
			SELECT rowid, id, title FROM entity
			WHERE library_id = ? AND kind = ? AND trashed_at_ms IS NULL
			ORDER BY rowid
		`, libraryID, kind)
	}
	candidates, err := s.titleFolds.candidates(ctx, plane.reader, libraryID, kind, folded)
	if err != nil {
		return nil, err
	}
	return foldEqualRowsByID(ctx, plane.reader, folded, libraryID, kind, candidates)
}

// LiveFoldEqualEntitiesByID is LiveFoldEqualEntities limited to the given
// IDs: those that are live Entities of the kind in the Library with a title
// that folds equal to title, oldest first. It reads only those rows, so it
// orders two known variants without listing the others.
func (s *Store) LiveFoldEqualEntitiesByID(ctx context.Context, libraryID LibraryID, kind, title string, ids []EntityID) ([]Entity, error) {
	if libraryID == "" || kind == "" {
		return nil, errors.New("library ID and entity kind are required")
	}
	folded := FoldEntityTitle(title)
	if folded == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return nil, err
	}
	return foldEqualRowsByID(ctx, plane.reader, folded, libraryID, kind, ids)
}

// foldEqualRowsByID reads the given IDs' rows by primary key (the unary plus
// keeps the planner off the (library_id, kind) index) and keeps the live ones
// of the kind in the Library whose titles fold to folded, oldest first.
func foldEqualRowsByID(ctx context.Context, reader *sql.DB, folded string, libraryID LibraryID, kind string, ids []EntityID) ([]Entity, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{libraryID, kind}
	for _, id := range ids {
		args = append(args, id)
	}
	return foldEqualRows(ctx, reader, folded, libraryID, kind, titleFoldByIDSQL(len(ids)), args...)
}

// titleFoldByIDSQL selects the live rows of a Library's kind among n IDs,
// oldest first, by primary key.
func titleFoldByIDSQL(n int) string {
	return `
		SELECT rowid, id, title FROM entity
		WHERE +library_id = ? AND +kind = ? AND trashed_at_ms IS NULL
		  AND id IN (?` + strings.Repeat(", ?", n-1) + `)
		ORDER BY rowid
	`
}

// titleFoldScopedSQL lists a scope's live members of a kind, oldest first,
// through entity_relation_to_entity_idx; the unary plus keeps the planner off
// the (library_id, kind) index, which would read the whole kind.
const titleFoldScopedSQL = `
	SELECT e.rowid, e.id, e.title FROM entity_relation r
	JOIN entity e ON e.id = r.from_entity_id
	WHERE r.to_entity_id = ? AND r.relation = ?
	  AND +e.library_id = ? AND +e.kind = ? AND e.trashed_at_ms IS NULL
	ORDER BY e.rowid
`

// titleFoldCatchUpSQL reads a Library's rows of a kind in a rowid range. The
// unary plus keeps the planner on the rowid range rather than the
// (library_id, kind) index, which would read the whole kind again.
const titleFoldCatchUpSQL = `SELECT id, title FROM entity WHERE rowid > ? AND rowid <= ? AND +library_id = ? AND +kind = ?`

// foldEqualRows runs a query selecting (rowid, id, title) and keeps the rows
// whose titles fold to folded, in the query's order.
func foldEqualRows(ctx context.Context, reader *sql.DB, folded string, libraryID LibraryID, kind, query string, args ...any) ([]Entity, error) {
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entity
	for rows.Next() {
		var rowid int64
		var entity Entity
		if err := rows.Scan(&rowid, &entity.ID, &entity.Title); err != nil {
			return nil, err
		}
		if FoldEntityTitle(entity.Title) == folded {
			entity.LibraryID, entity.Kind = libraryID, kind
			out = append(out, entity)
		}
	}
	return out, rows.Err()
}

// entityTitleFoldIndex maps the folded titles of a shared-catalog Library's
// Entities of one kind to the IDs that have carried them, built by one read
// of the kind the first time it is asked for. It is a hint, not a record:
// every candidate is checked against its row before use, so an Entity that
// was trashed, purged, renamed or moved since drops out there. It learns of
// new rows two ways: UpsertEntity notes each one it writes, and each lookup
// first reads the rows added past the highest rowid it has seen, which also
// covers rows another writer of the catalog added.
type entityTitleFoldIndex struct {
	mu    sync.Mutex
	lists map[entityTitleFoldKey]*entityTitleFoldList
}

type entityTitleFoldKey struct {
	library LibraryID
	kind    string
}

type entityTitleFoldList struct {
	// seen is the highest entity rowid in the catalog when the list was last
	// brought up to date.
	seen   int64
	byFold map[string][]EntityID
}

func (l *entityTitleFoldList) add(id EntityID, title string) {
	folded := FoldEntityTitle(title)
	if folded == "" {
		return
	}
	for _, known := range l.byFold[folded] {
		if known == id {
			return
		}
	}
	l.byFold[folded] = append(l.byFold[folded], id)
}

// note records that UpsertEntity wrote id with title, if the list for its
// Library and kind is built; an unbuilt list reads it when it is built.
func (x *entityTitleFoldIndex) note(libraryID LibraryID, kind string, id EntityID, title string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if list := x.lists[entityTitleFoldKey{library: libraryID, kind: kind}]; list != nil {
		list.add(id, title)
	}
}

// candidates returns the IDs the index holds for folded in a Library and
// kind, building the list or bringing it up to date first.
func (x *entityTitleFoldIndex) candidates(ctx context.Context, reader *sql.DB, libraryID LibraryID, kind, folded string) ([]EntityID, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	key := entityTitleFoldKey{library: libraryID, kind: kind}
	list := x.lists[key]
	if list == nil {
		list = &entityTitleFoldList{byFold: map[string][]EntityID{}}
	}
	if err := list.catchUp(ctx, reader, libraryID, kind, list == x.lists[key]); err != nil {
		return nil, err
	}
	if x.lists == nil {
		x.lists = map[entityTitleFoldKey]*entityTitleFoldList{}
	}
	x.lists[key] = list
	return append([]EntityID(nil), list.byFold[folded]...), nil
}

// catchUp reads, in one snapshot, the catalog's highest entity rowid and the
// Library's rows of the kind past what the list has seen: every row (trashed
// ones too, which a restore can bring back) when the list is new, or the
// rowid range added since otherwise.
func (l *entityTitleFoldList) catchUp(ctx context.Context, reader *sql.DB, libraryID LibraryID, kind string, built bool) error {
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var high sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(rowid) FROM entity`).Scan(&high); err != nil {
		return err
	}
	if built && high.Int64 <= l.seen {
		return nil
	}
	query := `SELECT id, title FROM entity WHERE library_id = ? AND kind = ?`
	args := []any{libraryID, kind}
	if built {
		query = titleFoldCatchUpSQL
		args = []any{l.seen, high.Int64, libraryID, kind}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id EntityID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return err
		}
		l.add(id, title)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	l.seen = high.Int64
	return nil
}

// EntityTitleFoldApplies reports whether title-fold merging can run in a
// Library. It cannot in an encrypted Library: the entity merge journals in the
// shared catalog, where that Library's Entities do not exist.
func (s *Store) EntityTitleFoldApplies(ctx context.Context, libraryID LibraryID) (bool, error) {
	if libraryID == "" {
		return false, errors.New("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return false, err
	}
	plane, err := s.catalogPlaneForLibraryLocked(ctx, libraryID)
	if err != nil {
		return false, err
	}
	return !plane.encrypted, nil
}
