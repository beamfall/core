package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

type queueEntry struct {
	mediaID   MediaID
	addedAtMS int64
}

// ErrQueueMediaNotFound covers missing targets and targets hidden from the queue.
const ErrQueueMediaNotFound = ValidationError("media not found")

// ErrQueueMediaLocked reports a visible target blocked by certification.
var ErrQueueMediaLocked = errors.New("media is locked")

// ErrQueueProfileOutsideSchedule reports a switched Profile that cannot use
// the session at the current time.
var ErrQueueProfileOutsideSchedule = errors.New("profile access not allowed at this time")

// QueueAddResult is the visible first page and scope at the commit point.
type QueueAddResult struct {
	Items     []SourceItem
	Filter    LibraryFilter
	ProfileID ProfileID
}

// QueueAddForProfile adds or moves an item in the Profile's Play Queue using
// current store authorization. A nonempty expectedProfileID pins the write
// to that Profile. An empty sessionID is for sessionless scopes, which
// cannot Reveal. resolveCertification maps current ceilings against the
// active plugin scales and must not call back into the store.
func (s *Store) QueueAddForProfile(ctx context.Context, profileID ProfileID, sessionID SessionID, expectedProfileID ProfileID, mediaID MediaID, position *int, filter LibraryFilter, resolveCertification func([]ProfileCertificationCeiling) []CertificationConstraint) (QueueAddResult, error) {
	if resolveCertification == nil {
		return QueueAddResult{}, errors.New("queue add requires a certification resolver")
	}
	if profileID == "" || mediaID == "" {
		return QueueAddResult{}, ValidationError("profile ID and media ID are required")
	}
	// Keep the database handles and any unsealed plane registered through the
	// operation. Other readers can take the same lock while SQLite serializes
	// writes to the attached databases.
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return QueueAddResult{}, err
	}
	privateID, err := s.queueTargetLibrary(ctx, mediaID)
	if err != nil {
		return QueueAddResult{}, err
	}
	writer, err := s.openQueueWriter(ctx, privateID)
	if err != nil {
		return QueueAddResult{}, err
	}
	defer writer.Close()
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		return QueueAddResult{}, err
	}
	defer tx.Rollback()

	var session Session
	if sessionID != "" {
		session, err = readSession(ctx, tx, sessionID)
		if err != nil {
			return QueueAddResult{}, err
		}
		device, err := readDevice(ctx, tx, session.DeviceID)
		if err != nil {
			return QueueAddResult{}, err
		}
		if device.Revoked {
			return QueueAddResult{}, ErrDeviceRevoked
		}
	}
	rebound := sessionID != "" && session.ProfileID != profileID
	filter.Revealed = filter.Revealed && session.Revealed
	if rebound {
		profileID = session.ProfileID
		filter.Revealed = session.Revealed
	}
	if expectedProfileID != "" && expectedProfileID != profileID {
		return QueueAddResult{}, ErrSessionProfileMismatch
	}
	profile, err := readProfile(ctx, tx, profileID)
	if err != nil {
		return QueueAddResult{}, err
	}
	if profile.Disabled {
		return QueueAddResult{}, ErrProfileDisabled
	}
	var windows []ProfileAccessWindow
	if profile.Kind == ProfileManaged {
		windows, err = readProfileAccessWindows(ctx, tx, profileID)
		if err != nil {
			return QueueAddResult{}, err
		}
		filter.GrantedMediaIDs, err = readGrantedMediaIDs(ctx, tx, profileID)
		if err != nil {
			return QueueAddResult{}, err
		}
	} else {
		filter.GrantedMediaIDs = nil
	}
	if err := checkQueueAddAccess(ctx, tx, profileID, session, windows, s.serverNow()); err != nil {
		return QueueAddResult{}, err
	}
	filter = queueAddFilter(profile, rebound, filter)
	filter.Certification = resolveCertification(profile.CertificationCeilings)

	entries, err := queueEntries(ctx, tx, profileID)
	if err != nil {
		return QueueAddResult{}, err
	}
	readers := mediaReaders{catalog: tx, encrypted: func(id LibraryID) rowsQueryer {
		if id == privateID {
			return queueTargetReader{tx}
		}
		return s.encryptedMediaReaderLocked(id)
	}}
	visibleMedia, err := visibleMediaBatch(ctx, readers, queueMediaIDs(entries, mediaID), filter)
	if err != nil {
		return QueueAddResult{}, err
	}
	target, ok := visibleMedia[mediaID]
	// A private target first seen after routing has no attached writer lock.
	if !ok || (target.LibraryID != privateID && s.encryptedMediaReaderLocked(target.LibraryID) != nil) {
		return QueueAddResult{}, ErrQueueMediaNotFound
	}
	check := []SourceItem{queueSourceItem(target)}
	if err := annotateLocked(ctx, readers, filter.Certification, check, filter.GrantedMediaIDs); err != nil {
		return QueueAddResult{}, err
	}
	if check[0].Locked {
		return QueueAddResult{}, ErrQueueMediaLocked
	}

	addedAt := timeToMillis(s.serverNow())
	without := make([]queueEntry, 0, len(entries))
	visible := make(map[MediaID]bool, len(visibleMedia))
	for id := range visibleMedia {
		visible[id] = true
	}
	for _, entry := range entries {
		if entry.mediaID == mediaID {
			addedAt = entry.addedAtMS
			continue
		}
		without = append(without, entry)
	}
	updated := insertAtVisiblePosition(without, queueEntry{mediaID: mediaID, addedAtMS: addedAt}, position, visible)
	items := make([]SourceItem, 0, len(visibleMedia))
	for _, entry := range updated {
		if media, ok := visibleMedia[entry.mediaID]; ok {
			items = append(items, queueSourceItem(media))
		}
	}
	// Match the normal queue response's mute filtering after placement.
	items, err = s.applyMuteFilterLocked(ctx, s.catalogR, profileID, filter, items)
	if err != nil {
		return QueueAddResult{}, err
	}
	items = pageOf(items, defaultSourceItemsLimit, 0)
	if filter.Revealed {
		restricted, err := restrictedLibraryIDs(ctx, tx)
		if err != nil {
			return QueueAddResult{}, err
		}
		for i := range items {
			items[i].Restricted = restricted[items[i].LibraryID]
		}
	}
	if err := annotateLocked(ctx, readers, filter.Certification, items, filter.GrantedMediaIDs); err != nil {
		return QueueAddResult{}, err
	}
	// A deadline can pass while building the page or rewriting the queue even though
	// the policy rows are stable. Check just before writing and before commit.
	if err := checkQueueAddAccess(ctx, tx, profileID, session, windows, s.serverNow()); err != nil {
		return QueueAddResult{}, err
	}
	if err := replaceQueue(ctx, tx, profileID, updated); err != nil {
		return QueueAddResult{}, err
	}
	if err := checkQueueAddAccess(ctx, tx, profileID, session, windows, s.serverNow()); err != nil {
		return QueueAddResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return QueueAddResult{}, err
	}
	return QueueAddResult{Items: slices.Clone(items), Filter: filter, ProfileID: profileID}, nil
}

// queueTargetLibrary selects a private plane to attach. The transaction still
// decides whether the target exists and is visible.
func (s *Store) queueTargetLibrary(ctx context.Context, mediaID MediaID) (LibraryID, error) {
	for id := range s.encryptedDBs {
		reader := s.encryptedMediaReaderLocked(id)
		if reader == nil {
			continue
		}
		rows, err := reader.QueryContext(ctx, "SELECT 1 FROM media WHERE id = ?", mediaID)
		if err != nil {
			return "", err
		}
		found := rows.Next()
		err = rows.Err()
		_ = rows.Close()
		if err != nil || found {
			return id, err
		}
	}
	return "", nil
}

// openQueueWriter uses an exclusive connection lifetime for the attachments.
// Only the events file is modified, so the commit requires no cross-file
// durability guarantee from SQLite's WAL mode.
func (s *Store) openQueueWriter(ctx context.Context, privateID LibraryID) (*sql.DB, error) {
	writer, err := openSQLiteWriterWithPragmas(s.catalogPath, []string{"temp_store(MEMORY)"})
	if err != nil {
		return nil, err
	}
	// Acquire database writer locks in catalog, private plane, events order.
	if privateID != "" {
		if _, err := writer.ExecContext(ctx, "ATTACH DATABASE ? AS queue_target", sqliteDSN(s.encryptedDBs[privateID].workingPath, nil, "")+"?mode=rw"); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if _, err := writer.ExecContext(ctx, "ATTACH DATABASE ? AS queue_events", sqliteDSN(s.eventsPath, nil, "")+"?mode=rw"); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if _, err := writer.ExecContext(ctx, "PRAGMA queue_events.synchronous = FULL"); err != nil {
		_ = writer.Close()
		return nil, err
	}
	return writer, nil
}

// queueTargetReader routes the existing private-plane visibility and
// certification queries to the attached target without shadowing main in
// any other query.
type queueTargetReader struct{ tx *sql.Tx }

func (q queueTargetReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return q.tx.QueryContext(ctx, `WITH media AS (SELECT * FROM queue_target.media),
		media_field_text AS (SELECT * FROM queue_target.media_field_text) `+query, args...)
}

func queueAddFilter(profile Profile, rebound bool, filter LibraryFilter) LibraryFilter {
	if rebound {
		filter.AllLibraries = false
		filter.AllowedIDs = profile.Library
	} else if !filter.AllLibraries {
		allowed := make([]LibraryID, 0, len(filter.AllowedIDs))
		for _, id := range filter.AllowedIDs {
			if slices.Contains(profile.Library, id) {
				allowed = append(allowed, id)
			}
		}
		filter.AllowedIDs = allowed
	}
	filter.Revealed = filter.Revealed && profile.Kind == ProfileAdult
	return filter
}

func checkQueueAddAccess(ctx context.Context, tx *sql.Tx, profileID ProfileID, session Session, windows []ProfileAccessWindow, now time.Time) error {
	if !session.ExpiresAt.IsZero() && !now.Before(session.ExpiresAt) {
		return ErrSessionExpired
	}
	if AllowedAt(windows, now.Local()) {
		return nil
	}
	if session.ID == "" {
		return ErrQueueProfileOutsideSchedule
	}
	granted, err := activeExtensionGrant(ctx, tx, profileID, session.ID, now)
	if err != nil {
		return err
	}
	if !granted {
		return ErrQueueProfileOutsideSchedule
	}
	return nil
}

// QueueRemove removes one visible Media item from a Profile's play queue and
// renumbers the remaining positions contiguously. A hidden-but-queued entry
// is left in place: hidden == missing, so for this caller there is nothing
// to remove — and the entry must still be there after a reveal.
func (s *Store) QueueRemove(ctx context.Context, profileID ProfileID, mediaID MediaID, filter LibraryFilter) error {
	if profileID == "" || mediaID == "" {
		return ValidationError("profile ID and media ID are required")
	}
	return s.updateQueue(ctx, profileID, filter, nil, func(entries []queueEntry, visible map[MediaID]bool) ([]queueEntry, error) {
		updated := entries[:0]
		for _, entry := range entries {
			if entry.mediaID == mediaID && visible[entry.mediaID] {
				continue
			}
			updated = append(updated, entry)
		}
		return updated, nil
	})
}

// QueueMove moves a visible queued item to a new position counted among the
// entries the caller can see. A hidden-but-queued id fails with the exact
// error an unqueued id gets — succeeding (or failing differently) would let
// a relocked session probe which hidden ids sit in its queue.
func (s *Store) QueueMove(ctx context.Context, profileID ProfileID, mediaID MediaID, position int, filter LibraryFilter) error {
	if profileID == "" || mediaID == "" {
		return ValidationError("profile ID and media ID are required")
	}
	return s.updateQueue(ctx, profileID, filter, nil, func(entries []queueEntry, visible map[MediaID]bool) ([]queueEntry, error) {
		var moved queueEntry
		found := false
		without := make([]queueEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.mediaID == mediaID && visible[entry.mediaID] {
				moved = entry
				found = true
				continue
			}
			without = append(without, entry)
		}
		if !found {
			return nil, ValidationError("media is not queued")
		}
		return insertAtVisiblePosition(without, moved, &position, visible), nil
	})
}

// ErrQueueReplaceNeedsConfirm signals that QueueReplaceWithStation refused
// because the caller's visible queue is non-empty (it holds user-added items)
// and confirmReplace was not set. Starting a Station materializes a rolling
// window into the existing Play Queue, but replacing a visible queue that
// already has items is an explicit user action with client confirmation
// (ADR-0098 §Stations). The caller re-issues with confirmReplace=true to commit.
var ErrQueueReplaceNeedsConfirm = errors.New("queue replacement needs confirmation")

// QueueReplaceWithStation materializes a generated Station window into the
// Profile's Play Queue (ADR-0098 area 5). It replaces the entries the caller can
// SEE with mediaIDs in order, while PRESERVING hidden-but-queued entries exactly
// like QueueClear — a relocked session must never destroy (or learn about) queue
// state it cannot read, and hidden entries reappear on reveal. The hidden
// entries are kept at the FRONT (their original relative order) and the new
// visible window follows; the visible positions the caller reads back are the
// Station window in generation order.
//
// confirmReplace gates the destructive replace: when the caller's visible queue
// is already non-empty (user-added items present) and confirmReplace is false,
// it makes no change and returns ErrQueueReplaceNeedsConfirm so the client can
// prompt. An empty visible queue needs no confirmation.
//
// mediaIDs are trusted to already be visible to the caller (the Station
// generator collects candidates through the same LibraryFilter, sealed-free);
// any id that is not currently visible to this caller is dropped defensively so
// a stale window can never inject an item the caller cannot itself read.
func (s *Store) QueueReplaceWithStation(ctx context.Context, profileID ProfileID, mediaIDs []MediaID, confirmReplace bool, filter LibraryFilter) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	return s.updateQueue(ctx, profileID, filter, mediaIDs, func(entries []queueEntry, visible map[MediaID]bool) ([]queueEntry, error) {
		hidden := make([]queueEntry, 0, len(entries))
		visibleCount := 0
		for _, entry := range entries {
			if visible[entry.mediaID] {
				visibleCount++
				continue
			}
			hidden = append(hidden, entry)
		}
		if visibleCount > 0 && !confirmReplace {
			return nil, ErrQueueReplaceNeedsConfirm
		}
		addedAt := timeToMillis(time.Now().UTC())
		updated := hidden
		seen := make(map[MediaID]bool, len(mediaIDs))
		for _, id := range mediaIDs {
			if id == "" || seen[id] || !visible[id] {
				continue
			}
			seen[id] = true
			updated = append(updated, queueEntry{mediaID: id, addedAtMS: addedAt})
		}
		return updated, nil
	})
}

// QueueClear removes every entry the caller can see. Hidden entries stay —
// a relocked session must not be able to destroy queue state it cannot read.
func (s *Store) QueueClear(ctx context.Context, profileID ProfileID, filter LibraryFilter) error {
	if profileID == "" {
		return ValidationError("profile ID is required")
	}
	return s.updateQueue(ctx, profileID, filter, nil, func(entries []queueEntry, visible map[MediaID]bool) ([]queueEntry, error) {
		var kept []queueEntry
		for _, entry := range entries {
			if !visible[entry.mediaID] {
				kept = append(kept, entry)
			}
		}
		return kept, nil
	})
}

func (s *Store) updateQueue(ctx context.Context, profileID ProfileID, filter LibraryFilter, extra []MediaID, mutate func([]queueEntry, map[MediaID]bool) ([]queueEntry, error)) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entries, err := queueEntries(ctx, tx, profileID)
	if err != nil {
		return err
	}
	visible, err := s.queueVisibility(ctx, entries, filter, extra...)
	if err != nil {
		return err
	}
	updated, err := mutate(entries, visible)
	if err != nil {
		return err
	}
	if err := replaceQueue(ctx, tx, profileID, updated); err != nil {
		return err
	}
	return tx.Commit()
}

// queueVisibility resolves which queued and candidate IDs the scope can see.
func (s *Store) queueVisibility(ctx context.Context, entries []queueEntry, filter LibraryFilter, extra ...MediaID) (map[MediaID]bool, error) {
	media, err := s.visibleMediaBatchSQL(ctx, queueMediaIDs(entries, extra...), filter)
	if err != nil {
		return nil, err
	}
	visible := make(map[MediaID]bool, len(media))
	for id := range media {
		visible[id] = true
	}
	return visible, nil
}

func queueMediaIDs(entries []queueEntry, extra ...MediaID) []MediaID {
	ids := make([]MediaID, 0, len(entries)+len(extra))
	for _, entry := range entries {
		ids = append(ids, entry.mediaID)
	}
	return append(ids, extra...)
}

// insertAtVisiblePosition inserts entry so it lands at index position among
// the entries the caller can see (nil or past-the-end position = the end of
// the queue). Hidden entries keep their slots; the caller's position
// arithmetic only ever counts items it can read back.
func insertAtVisiblePosition(entries []queueEntry, entry queueEntry, position *int, visible map[MediaID]bool) []queueEntry {
	visibleCount := 0
	for _, e := range entries {
		if visible[e.mediaID] {
			visibleCount++
		}
	}
	target := visibleCount
	if position != nil {
		target = clampQueuePosition(*position, visibleCount)
	}
	at := len(entries)
	seen := 0
	for i, e := range entries {
		if !visible[e.mediaID] {
			continue
		}
		if seen == target {
			at = i
			break
		}
		seen++
	}
	updated := append(entries[:at:at], entry)
	return append(updated, entries[at:]...)
}

func (s *Store) queueItemsSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id
		FROM play_queue
		WHERE profile_id = ?
		ORDER BY position
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []MediaID
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	visible, err := s.visibleMediaBatchSQL(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	items := []SourceItem{}
	visibleIndex := 0
	for _, id := range ids {
		media, ok := visible[id]
		if !ok {
			continue
		}
		if visibleIndex < offset {
			visibleIndex++
			continue
		}
		items = append(items, SourceItem{ID: media.ID, Kind: "media", Title: media.Title, LibraryID: media.LibraryID, Summary: mediaDurationSummary(media.DurationSeconds)})
		visibleIndex++
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

func queueSourceItem(media Media) SourceItem {
	return SourceItem{
		ID: media.ID, Kind: "media", Title: media.Title, LibraryID: media.LibraryID,
		Summary: mediaDurationSummary(media.DurationSeconds),
	}
}

// myQueueRows returns every queued entry for the Profile in queue order,
// unfiltered. My Data reveal-filters the whole export centrally, so
// this read deliberately returns hidden entries too; it lives here, not in
// mydata.go, because the play_queue table is firewalled behind the queue API
// (see TestPlayQueueWritesStayBehindQueueAPI). The caller (ExportMyEngagement)
// holds s.mu and has guarded; this reads via eventsR like the other My Data
// projection reads.
func (s *Store) myQueueRows(ctx context.Context, profileID ProfileID) ([]MyQueueEntry, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT media_id, added_at_ms
		FROM play_queue
		WHERE profile_id = ?
		ORDER BY position
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MyQueueEntry
	for rows.Next() {
		var row MyQueueEntry
		var at int64
		if err := rows.Scan(&row.MediaID, &at); err != nil {
			return nil, err
		}
		row.AddedAt = millisToTime(at)
		out = append(out, row)
	}
	return out, rows.Err()
}

// deleteMyQueueAll removes every queue entry for the Profile within the caller's
// transaction, INCLUDING hidden ones. My Data's `all` delete is not disclosure,
// so unlike QueueClear (which keeps hidden entries) it purges everything. It
// lives in queue.go so all play_queue SQL stays behind the queue firewall.
func deleteMyQueueAll(ctx context.Context, tx *sql.Tx, profileID ProfileID) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM play_queue WHERE profile_id = ?`, profileID)
	return err
}

// deleteMyQueueMedia removes one Media item's queue entry for the Profile within
// the caller's transaction. My Data's `media:<id>` delete has already resolved
// the id through the caller's visibility filter, so this is an unconditional
// purge of that single row. It lives in queue.go for the same firewall reason.
func deleteMyQueueMedia(ctx context.Context, tx *sql.Tx, profileID ProfileID, mediaID MediaID) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM play_queue WHERE profile_id = ? AND media_id = ?`, profileID, mediaID)
	return err
}

// queueMove is one Profile's queue entry a version merge pointed at another
// Media (CORE-2490). AddedAtMS tells it from an entry the Profile queues later.
type queueMove struct {
	Profile   ProfileID `json:"profile"`
	AddedAtMS int64     `json:"addedAtMs"`
}

// moveQueueEntriesTx points each Profile's queue entry for from at to,
// keeping its position and added time, unless that Profile already queues to,
// and returns the entries it moved. It lives in queue.go for the firewall
// reason above.
func moveQueueEntriesTx(ctx context.Context, tx *sql.Tx, from, to MediaID) ([]queueMove, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT profile_id, added_at_ms FROM play_queue AS q
		WHERE media_id = ?
		  AND NOT EXISTS (SELECT 1 FROM play_queue WHERE profile_id = q.profile_id AND media_id = ?)
		ORDER BY profile_id
	`, from, to)
	if err != nil {
		return nil, err
	}
	var moves []queueMove
	for rows.Next() {
		var m queueMove
		if err := rows.Scan(&m.Profile, &m.AddedAtMS); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		moves = append(moves, m)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, m := range moves {
		if err := repointQueueEntryTx(ctx, tx, m, from, to); err != nil {
			return nil, err
		}
	}
	return moves, nil
}

// repointQueueEntryTx points m's queue entry for from at to, unless the
// Profile has since removed it, queued it again (a new added time) or already
// queues to.
func repointQueueEntryTx(ctx context.Context, tx *sql.Tx, m queueMove, from, to MediaID) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE play_queue SET media_id = ?
		WHERE profile_id = ? AND media_id = ? AND added_at_ms = ?
		  AND NOT EXISTS (SELECT 1 FROM play_queue WHERE profile_id = ? AND media_id = ?)
	`, to, m.Profile, from, m.AddedAtMS, m.Profile, to)
	return err
}

func queueEntries(ctx context.Context, db rowsQueryer, profileID ProfileID) ([]queueEntry, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT media_id, added_at_ms
		FROM play_queue
		WHERE profile_id = ?
		ORDER BY position
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []queueEntry
	for rows.Next() {
		var entry queueEntry
		if err := rows.Scan(&entry.mediaID, &entry.addedAtMS); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func replaceQueue(ctx context.Context, tx *sql.Tx, profileID ProfileID, entries []queueEntry) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM play_queue WHERE profile_id = ?`, profileID); err != nil {
		return err
	}
	for position, entry := range entries {
		if entry.mediaID == "" {
			return errors.New("queue entry media ID is required")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO play_queue(profile_id, position, media_id, added_at_ms)
			VALUES (?, ?, ?, ?)
		`, profileID, position, entry.mediaID, entry.addedAtMS); err != nil {
			return err
		}
	}
	return nil
}

// myEngagementChunkLocked keyset-pages the Profile's matching engagement IDs
// and their rows in one events query. It lives in the queue API because the
// combined projection includes queue rows; callers resolve only this bounded
// ID chunk through the catalog before advancing the keyset.
func (s *Store) myEngagementChunkLocked(ctx context.Context, profileID ProfileID, after MediaID, query MyEngagementLogQuery) ([]MediaID, []myEngagementRankedEvent, error) {
	args := []any{profileID, profileID, profileID}
	dateWhere := "1 = 1"
	if !query.From.IsZero() {
		dateWhere += ` AND occurred_at_ms >= ?`
		args = append(args, timeToMillis(query.From))
	}
	if !query.To.IsZero() {
		dateWhere += ` AND occurred_at_ms <= ?`
		args = append(args, timeToMillis(query.To))
	}
	args = append(args, after, myEngagementLogIDChunk)
	// #nosec G202 -- dateWhere is assembled only from fixed timestamp predicates.
	rows, err := s.eventsR.QueryContext(ctx, `
		WITH engagement_rows(kind, rank, media_id, occurred_at_ms) AS (
			SELECT 'play', 0, media_id, updated_at_ms FROM playback_progress WHERE profile_id = ?
			UNION ALL
			SELECT 'favorite', 1, media_id, favorited_at_ms FROM media_favorite WHERE profile_id = ?
			UNION ALL
			SELECT 'queue', 2, media_id, added_at_ms FROM play_queue WHERE profile_id = ?
		),
		matching AS (
			SELECT kind, rank, media_id, occurred_at_ms
			FROM engagement_rows
			WHERE `+dateWhere+`
		),
		next_ids AS (
			SELECT media_id
			FROM matching
			WHERE media_id > ?
			GROUP BY media_id
			ORDER BY media_id
			LIMIT ?
		)
		SELECT m.kind, m.rank, m.media_id, m.occurred_at_ms
		FROM matching m
		JOIN next_ids n ON n.media_id = m.media_id
		ORDER BY m.media_id, m.rank
	`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	ids := make([]MediaID, 0, myEngagementLogIDChunk)
	events := make([]myEngagementRankedEvent, 0, myEngagementLogIDChunk*3)
	var previous MediaID
	for rows.Next() {
		var kind string
		var ranked myEngagementRankedEvent
		var occurredAt int64
		if err := rows.Scan(&kind, &ranked.rank, &ranked.event.MediaID, &occurredAt); err != nil {
			return nil, nil, err
		}
		if ranked.event.MediaID != previous {
			ids = append(ids, ranked.event.MediaID)
			previous = ranked.event.MediaID
		}
		ranked.event.Kind = MyEngagementEventKind(kind)
		ranked.event.OccurredAt = millisToTime(occurredAt)
		events = append(events, ranked)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return ids, events, nil
}

func clampQueuePosition(position, length int) int {
	if position < 0 {
		return 0
	}
	if position > length {
		return length
	}
	return position
}
