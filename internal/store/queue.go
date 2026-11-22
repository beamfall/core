package store

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	randv2 "math/rand/v2"
	"slices"
	"time"
)

// QueueRepeatMode is the Profile's Play Queue repeat setting.
type QueueRepeatMode string

const (
	QueueRepeatOff QueueRepeatMode = "off"
	QueueRepeatAll QueueRepeatMode = "all"
	QueueRepeatOne QueueRepeatMode = "one"
)

type QueuePlacement string

const (
	QueueNext QueuePlacement = "next"
	QueueEnd  QueuePlacement = "end"
)

type PlayQueueEntry struct {
	ID   string
	Item SourceItem
}

type PlayQueue struct {
	Entries        []PlayQueueEntry
	CurrentEntryID *string
	PositionMS     int64
	Shuffle        bool
	RepeatMode     QueueRepeatMode
}

type queueEntry struct {
	id              string
	mediaID         MediaID
	addedAtMS       int64
	position        int
	shufflePosition sql.NullInt64
	persisted       bool
}

type queueSnapshot struct {
	entries    []queueEntry
	shuffled   []queueEntry
	currentID  string
	positionMS int64
	shuffle    bool
	repeat     QueueRepeatMode
}

func (q *queueSnapshot) activeOrder() *[]queueEntry {
	if q.shuffle {
		return &q.shuffled
	}
	return &q.entries
}

// addLegacy preserves the Media-addressed behavior for existing clients.
// Numeric positions count distinct visible Media, as legacy reads do.
func (q *queueSnapshot) addLegacy(mediaID MediaID, position *int, visible map[MediaID]Media, addedAt time.Time) {
	order := q.activeOrder()
	for i, entry := range *order {
		if entry.mediaID == mediaID {
			*order = moveLegacyQueueEntry(*order, i, position, visible)
			return
		}
	}
	entry := newQueueEntries([]MediaID{mediaID})[0]
	entry.addedAtMS = timeToMillis(addedAt)
	q.entries = insertLegacyQueueEntry(q.entries, entry, position, visible)
	if q.shuffle {
		q.shuffled = insertLegacyQueueEntry(q.shuffled, entry, position, visible)
	}
}

func validQueueBatch(mediaIDs []MediaID, placement QueuePlacement) bool {
	return len(mediaIDs) > 0 && (placement == QueueEnd || placement == QueueNext)
}

func (q *queueSnapshot) addMany(mediaIDs []MediaID, placement QueuePlacement, visible map[MediaID]Media) {
	entries := newQueueEntries(mediaIDs)
	q.entries = addQueueEntries(q.entries, entries, q.currentID, placement, visible)
	if q.shuffle {
		q.shuffled = addQueueEntries(q.shuffled, entries, q.currentID, placement, visible)
	}
}

func newQueueEntries(ids []MediaID) []queueEntry {
	addedAt := timeToMillis(time.Now().UTC())
	entries := make([]queueEntry, len(ids))
	for i, id := range ids {
		entries[i] = queueEntry{id: rand.Text(), mediaID: id, addedAtMS: addedAt}
	}
	return entries
}

func addQueueEntries(order, entries []queueEntry, currentID string, placement QueuePlacement, visible map[MediaID]Media) []queueEntry {
	if placement == QueueEnd {
		return append(order, entries...)
	}
	first := -1
	for i, entry := range order {
		if _, ok := visible[entry.mediaID]; !ok {
			continue
		}
		if entry.id == currentID {
			return slices.Insert(order, i+1, entries...)
		}
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		first = len(order)
	}
	return slices.Insert(order, first, entries...)
}

type queueTarget struct {
	entryID string
	mediaID MediaID
}

// QueueEdit applies a state change using the visibility resolved for the write.
type QueueEdit func(*queueSnapshot, map[MediaID]Media) error

func (t queueTarget) matches(entry queueEntry, visible map[MediaID]Media) bool {
	_, ok := visible[entry.mediaID]
	if t.entryID != "" {
		return ok && entry.id == t.entryID
	}
	return ok && entry.mediaID == t.mediaID
}

func (s *Store) QueueRemove(ctx context.Context, profileID ProfileID, mediaID MediaID, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, RemoveQueueMedia(mediaID))
}

func (s *Store) QueueRemoveEntry(ctx context.Context, profileID ProfileID, entryID string, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, RemoveQueueEntry(entryID))
}

func RemoveQueueEntry(entryID string) QueueEdit {
	return removeQueueTarget(queueTarget{entryID: entryID})
}

func RemoveQueueMedia(mediaID MediaID) QueueEdit {
	return removeQueueTarget(queueTarget{mediaID: mediaID})
}

func removeQueueTarget(target queueTarget) QueueEdit {
	return func(q *queueSnapshot, visible map[MediaID]Media) error {
		if target == (queueTarget{}) {
			return ValidationError("queue entry is required")
		}
		for _, entry := range *q.activeOrder() {
			if target.matches(entry, visible) {
				q.removeEntries(func(candidate queueEntry) bool { return candidate.id == entry.id })
				break
			}
		}
		return nil
	}
}

func (q *queueSnapshot) removeEntries(remove func(queueEntry) bool) {
	q.entries = slices.DeleteFunc(q.entries, remove)
	q.shuffled = slices.DeleteFunc(q.shuffled, remove)
	if !slices.ContainsFunc(q.entries, func(entry queueEntry) bool { return entry.id == q.currentID }) {
		q.currentID, q.positionMS = "", 0
	}
}

func (s *Store) QueueMove(ctx context.Context, profileID ProfileID, mediaID MediaID, position int, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, MoveQueueMedia(mediaID, position))
}

func (s *Store) QueueMoveEntry(ctx context.Context, profileID ProfileID, entryID string, position int, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, MoveQueueEntry(entryID, position))
}

func MoveQueueEntry(entryID string, position int) QueueEdit {
	return moveQueueTarget(queueTarget{entryID: entryID}, &position)
}

func MoveQueueMedia(mediaID MediaID, position int) QueueEdit {
	return moveQueueTarget(queueTarget{mediaID: mediaID}, &position)
}

func moveQueueTarget(target queueTarget, position *int) QueueEdit {
	return func(q *queueSnapshot, visible map[MediaID]Media) error {
		return q.moveEntry(target, position, visible)
	}
}

func (q *queueSnapshot) moveEntry(target queueTarget, position *int, visible map[MediaID]Media) error {
	order := q.activeOrder()
	for i, entry := range *order {
		if !target.matches(entry, visible) {
			continue
		}
		if position == nil && entry.id == q.currentID {
			return nil
		}
		if target.entryID == "" {
			*order = moveLegacyQueueEntry(*order, i, position, visible)
			return nil
		}
		remaining := slices.Delete(*order, i, i+1)
		if position == nil {
			*order = addQueueEntries(remaining, []queueEntry{entry}, q.currentID, QueueNext, visible)
		} else {
			*order = insertQueueEntry(remaining, entry, *position, visible)
		}
		return nil
	}
	return ValidationError("media is not queued")
}

func (s *Store) QueueSetCurrent(ctx context.Context, profileID ProfileID, entryID string, positionMS int64, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, SetQueueCurrent(entryID, positionMS))
}

func SetQueueCurrent(entryID string, positionMS int64) QueueEdit {
	return func(q *queueSnapshot, visible map[MediaID]Media) error {
		if positionMS < 0 || (entryID == "" && positionMS != 0) {
			return ValidationError("invalid queue position")
		}
		if entryID == "" && q.currentID == "" {
			return nil
		}
		targetID := entryID
		if targetID == "" {
			// A clear from a locked session must leave a hidden current entry alone.
			targetID = q.currentID
		}
		if slices.ContainsFunc(q.entries, func(e queueEntry) bool {
			return queueTarget{entryID: targetID}.matches(e, visible)
		}) {
			q.currentID, q.positionMS = entryID, positionMS
			return nil
		}
		if entryID != "" {
			return ValidationError("media is not queued")
		}
		return nil
	}
}

func (s *Store) QueueSetShuffle(ctx context.Context, profileID ProfileID, enabled bool, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, SetQueueShuffle(enabled))
}

func SetQueueShuffle(enabled bool) QueueEdit {
	return func(q *queueSnapshot, _ map[MediaID]Media) error {
		if enabled == q.shuffle {
			return nil
		}
		q.shuffle = enabled
		q.shuffled = nil
		if enabled {
			q.shuffled = slices.Clone(q.entries)
			randv2.Shuffle(len(q.shuffled), func(i, j int) { //nolint:gosec // Playback order does not require cryptographic randomness.
				q.shuffled[i], q.shuffled[j] = q.shuffled[j], q.shuffled[i]
			})
			if i := slices.IndexFunc(q.shuffled, func(e queueEntry) bool { return e.id == q.currentID }); i > 0 {
				q.shuffled[0], q.shuffled[i] = q.shuffled[i], q.shuffled[0]
			}
		}
		return nil
	}
}

func (s *Store) QueueSetRepeat(ctx context.Context, profileID ProfileID, mode QueueRepeatMode, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, SetQueueRepeat(mode))
}

func SetQueueRepeat(mode QueueRepeatMode) QueueEdit {
	return func(q *queueSnapshot, _ map[MediaID]Media) error {
		switch mode {
		case QueueRepeatOff, QueueRepeatAll, QueueRepeatOne:
		default:
			return ValidationError("invalid queue repeat mode")
		}
		q.repeat = mode
		return nil
	}
}

// ErrQueueReplaceNeedsConfirm protects a visible, nonempty queue from an
// implicit replacement when a caller starts a Station (ADR-0098).
var ErrQueueReplaceNeedsConfirm = errors.New("queue replacement needs confirmation")

func (s *Store) QueueReplaceWithStation(ctx context.Context, profileID ProfileID, mediaIDs []MediaID, confirmReplace bool, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, mediaIDs, replaceQueueWithStation(mediaIDs, confirmReplace))
}

func replaceQueueWithStation(mediaIDs []MediaID, confirmReplace bool) QueueEdit {
	return func(q *queueSnapshot, visible map[MediaID]Media) error {
		isVisible := func(entry queueEntry) bool {
			_, ok := visible[entry.mediaID]
			return ok
		}
		if !confirmReplace && slices.ContainsFunc(q.entries, isVisible) {
			return ErrQueueReplaceNeedsConfirm
		}
		q.removeEntries(isVisible)
		seen := make(map[MediaID]bool, len(mediaIDs))
		var ids []MediaID
		for _, id := range mediaIDs {
			if _, ok := visible[id]; ok && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		entries := newQueueEntries(ids)
		q.entries = append(q.entries, entries...)
		if q.shuffle {
			q.shuffled = append(q.shuffled, entries...)
		}
		return nil
	}
}

// QueueClear removes every entry the caller can see.
func (s *Store) QueueClear(ctx context.Context, profileID ProfileID, filter LibraryFilter) error {
	return s.updateQueue(ctx, profileID, filter, nil, ClearQueue())
}

func ClearQueue() QueueEdit {
	return func(q *queueSnapshot, visible map[MediaID]Media) error {
		q.removeEntries(func(entry queueEntry) bool {
			_, ok := visible[entry.mediaID]
			return ok
		})
		return nil
	}
}

func (s *Store) updateQueue(ctx context.Context, profileID ProfileID, filter LibraryFilter, extra []MediaID, mutate QueueEdit) error {
	auth := QueueWriteAuthorization{ProfileID: profileID, Filter: filter, Now: s.serverNow, trustedFilter: true}
	return s.writeQueue(ctx, auth, queueWriteTargets{ids: extra}, func(view queueWriteView) error {
		return mutate(view.queue, view.visible)
	})
}

func queueMediaIDs(entries []queueEntry, extra []MediaID) []MediaID {
	seen := make(map[MediaID]bool, len(entries)+len(extra))
	ids := make([]MediaID, 0, len(entries)+len(extra))
	for _, entry := range entries {
		if !seen[entry.mediaID] {
			seen[entry.mediaID] = true
			ids = append(ids, entry.mediaID)
		}
	}
	for _, id := range extra {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func moveLegacyQueueEntry(order []queueEntry, index int, position *int, visible map[MediaID]Media) []queueEntry {
	if position != nil {
		// The target is the first occurrence of its Media in playback order.
		// Compare its legacy index before regrouping any duplicate entries.
		seen := make(map[MediaID]bool)
		for _, entry := range order[:index] {
			if _, ok := visible[entry.mediaID]; ok {
				seen[entry.mediaID] = true
			}
		}
		if len(seen) == max(0, *position) {
			return order
		}
	}
	entry := order[index]
	return insertLegacyQueueEntry(slices.Delete(order, index, index+1), entry, position, visible)
}

// insertLegacyQueueEntry counts the first visible occurrence of each other
// Media. Carry any earlier duplicates of the moved Media with it so the
// legacy read sees its first occurrence at the requested position.
func insertLegacyQueueEntry(order []queueEntry, entry queueEntry, position *int, visible map[MediaID]Media) []queueEntry {
	if position == nil {
		return append(order, entry)
	}
	target := max(0, *position)
	seen := make(map[MediaID]bool)
	reordered := make([]queueEntry, 0, len(order)+1)
	moved := []queueEntry{entry}
	for i, candidate := range order {
		if candidate.mediaID == entry.mediaID {
			moved = append(moved, candidate)
			continue
		}
		if _, ok := visible[candidate.mediaID]; ok && !seen[candidate.mediaID] {
			if target == 0 {
				reordered = append(reordered, moved...)
				return append(reordered, order[i:]...)
			}
			seen[candidate.mediaID] = true
			target--
		}
		reordered = append(reordered, candidate)
	}
	return append(reordered, moved...)
}

// insertQueueEntry counts each visible entry, including duplicate Media.
func insertQueueEntry(order []queueEntry, entry queueEntry, position int, visible map[MediaID]Media) []queueEntry {
	target := max(0, position)
	for i, candidate := range order {
		if _, ok := visible[candidate.mediaID]; !ok {
			continue
		}
		if target == 0 {
			return slices.Insert(order, i, entry)
		}
		target--
	}
	return append(order, entry)
}

func (s *Store) PlayQueue(ctx context.Context, profileID ProfileID, filter LibraryFilter) (PlayQueue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return PlayQueue{}, err
	}
	return s.readPlayQueue(ctx, profileID, filter)
}

// readPlayQueue is called with the Store read lock held. The events read
// transaction binds the entry order and playback state to the same snapshot.
func (s *Store) readPlayQueue(ctx context.Context, profileID ProfileID, filter LibraryFilter) (PlayQueue, error) {
	q, err := s.readQueueSnapshot(ctx, profileID)
	if err != nil {
		return PlayQueue{}, err
	}
	visible, err := s.visibleMediaBatchSQL(ctx, queueMediaIDs(q.entries, nil), filter)
	if err != nil {
		return PlayQueue{}, err
	}
	return q.playQueue(visible), nil
}

func (s *Store) readQueueSnapshot(ctx context.Context, profileID ProfileID) (queueSnapshot, error) {
	tx, err := s.eventsR.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return queueSnapshot{}, err
	}
	defer tx.Rollback()
	return loadQueue(ctx, tx, profileID)
}

func (q *queueSnapshot) visibleCurrent(visible map[MediaID]Media) (*string, int64) {
	for _, entry := range q.entries {
		if entry.id == q.currentID {
			if _, ok := visible[entry.mediaID]; ok {
				return &entry.id, q.positionMS
			}
			break
		}
	}
	return nil, 0
}

func (q *queueSnapshot) playQueue(visible map[MediaID]Media) PlayQueue {
	result := PlayQueue{Entries: []PlayQueueEntry{}, Shuffle: q.shuffle, RepeatMode: q.repeat}
	result.CurrentEntryID, result.PositionMS = q.visibleCurrent(visible)
	for _, entry := range *q.activeOrder() {
		media, ok := visible[entry.mediaID]
		if !ok {
			continue
		}
		result.Entries = append(result.Entries, PlayQueueEntry{ID: entry.id, Item: queueSourceItem(media)})
	}
	return result
}

func (s *Store) queueItemsSQL(ctx context.Context, profileID ProfileID, filter LibraryFilter, limit, offset int) ([]SourceItem, error) {
	queue, err := s.readPlayQueue(ctx, profileID, filter)
	if err != nil {
		return nil, err
	}
	// Legacy clients identify rows by Media ID. Apply pagination to the
	// first occurrence of each visible Media in playback order.
	items := make([]SourceItem, 0, min(limit, len(queue.Entries)))
	seen := make(map[MediaID]bool, len(queue.Entries))
	for _, entry := range queue.Entries {
		if len(items) == limit {
			break
		}
		if seen[entry.Item.ID] {
			continue
		}
		seen[entry.Item.ID] = true
		if offset > 0 {
			offset--
			continue
		}
		items = append(items, entry.Item)
	}
	return items, nil
}

func (q *queueSnapshot) export(visible map[MediaID]Media) ([]MyQueueEntry, MyQueueState) {
	entries := []MyQueueEntry{}
	state := MyQueueState{Shuffle: q.shuffle, RepeatMode: q.repeat, ShuffledEntryIDs: []string{}}
	state.CurrentEntryID, state.PositionMS = q.visibleCurrent(visible)
	for _, entry := range q.entries {
		if _, ok := visible[entry.mediaID]; ok {
			entries = append(entries, MyQueueEntry{ID: entry.id, MediaID: entry.mediaID, AddedAt: millisToTime(entry.addedAtMS)})
		}
	}
	for _, entry := range q.shuffled {
		if _, ok := visible[entry.mediaID]; ok {
			state.ShuffledEntryIDs = append(state.ShuffledEntryIDs, entry.id)
		}
	}
	return entries, state
}

// myQueueRows reads the unfiltered canonical order for My Data Summary.
// Its caller holds the Store read lock and applies visibility.
func (s *Store) myQueueRows(ctx context.Context, profileID ProfileID) ([]MyQueueEntry, error) {
	rows, err := s.eventsR.QueryContext(ctx, `
		SELECT entry_id, media_id, added_at_ms
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
		if err := rows.Scan(&row.ID, &row.MediaID, &at); err != nil {
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM play_queue_state WHERE profile_id = ?`, profileID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM play_queue WHERE profile_id = ?`, profileID)
	return err
}

// deleteMyQueueMedia purges all occurrences after My Data resolves the
// Media through the caller's visibility filter.
func deleteMyQueueMedia(ctx context.Context, tx *sql.Tx, profileID ProfileID, mediaID MediaID) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM play_queue WHERE profile_id = ? AND media_id = ?`, profileID, mediaID)
	return err
}

// queueMove records an entry repointed by a Version merge. Older undo
// records contain the Profile and timestamp only.
type queueMove struct {
	Profile   ProfileID `json:"profile"`
	AddedAtMS int64     `json:"addedAtMs"`
	EntryID   string    `json:"entryID,omitempty"`
}

func moveQueueEntriesTx(ctx context.Context, tx *sql.Tx, from, to MediaID) ([]queueMove, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT profile_id, added_at_ms, entry_id FROM play_queue
		WHERE media_id = ? ORDER BY profile_id, position
	`, from)
	if err != nil {
		return nil, err
	}
	var moves []queueMove
	for rows.Next() {
		var m queueMove
		if err := rows.Scan(&m.Profile, &m.AddedAtMS, &m.EntryID); err != nil {
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

func repointQueueEntryTx(ctx context.Context, tx *sql.Tx, m queueMove, from, to MediaID) error {
	if m.EntryID != "" {
		_, err := tx.ExecContext(ctx, `
			UPDATE play_queue SET media_id = ?
			WHERE entry_id = ? AND profile_id = ? AND media_id = ?
		`, to, m.EntryID, m.Profile, from)
		return err
	}
	// A pre-migration undo record can only refer to a migrated row. Preserve
	// the earlier conflict rule for these records, which lack an entry ID.
	_, err := tx.ExecContext(ctx, `
		UPDATE play_queue SET media_id = ?
		WHERE profile_id = ? AND media_id = ? AND added_at_ms = ? AND entry_id GLOB 'legacy-*'
		  AND NOT EXISTS (SELECT 1 FROM play_queue WHERE profile_id = ? AND media_id = ?)
		  AND 1 = (SELECT COUNT(*) FROM play_queue WHERE profile_id = ? AND media_id = ? AND added_at_ms = ? AND entry_id GLOB 'legacy-*')
	`, to, m.Profile, from, m.AddedAtMS, m.Profile, to, m.Profile, from, m.AddedAtMS)
	return err
}

func loadQueue(ctx context.Context, tx *sql.Tx, profileID ProfileID) (queueSnapshot, error) {
	q := queueSnapshot{repeat: QueueRepeatOff}
	var current sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT current_entry_id, position_ms, shuffle, repeat_mode FROM play_queue_state WHERE profile_id = ?
	`, profileID).Scan(&current, &q.positionMS, &q.shuffle, &q.repeat)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return queueSnapshot{}, err
	}
	q.currentID = current.String

	rows, err := tx.QueryContext(ctx, `
		SELECT entry_id, media_id, added_at_ms, position, shuffle_position
		FROM play_queue WHERE profile_id = ? ORDER BY position
	`, profileID)
	if err != nil {
		return queueSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry queueEntry
		if err := rows.Scan(&entry.id, &entry.mediaID, &entry.addedAtMS, &entry.position, &entry.shufflePosition); err != nil {
			return queueSnapshot{}, err
		}
		entry.persisted = true
		q.entries = append(q.entries, entry)
	}
	if err := rows.Err(); err != nil {
		return queueSnapshot{}, err
	}
	if q.shuffle {
		q.shuffled = slices.Clone(q.entries)
		// Entries without a rank (such as rows imported by maintenance tools)
		// follow the ranked entries in canonical order.
		slices.SortStableFunc(q.shuffled, func(a, b queueEntry) int {
			ar, br := a.shufflePosition, b.shufflePosition
			switch {
			case ar.Valid && br.Valid:
				return cmp.Compare(ar.Int64, br.Int64)
			case ar.Valid:
				return -1
			case br.Valid:
				return 1
			default:
				return 0
			}
		})
	}
	return q, nil
}

func saveQueue(ctx context.Context, tx *sql.Tx, profileID ProfileID, q queueSnapshot, original []queueEntry) error {
	ranks := make(map[string]int, len(q.shuffled))
	for i, entry := range q.shuffled {
		ranks[entry.id] = i
	}
	kept := make(map[string]bool, len(q.entries))
	for position, entry := range q.entries {
		kept[entry.id] = true
		rank, ranked := ranks[entry.id]
		shufflePosition := sql.NullInt64{Int64: int64(rank), Valid: ranked}
		if !entry.persisted {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO play_queue(entry_id, profile_id, position, media_id, added_at_ms, shuffle_position)
				VALUES (?, ?, ?, ?, ?, ?)
			`, entry.id, profileID, position, entry.mediaID, entry.addedAtMS, shufflePosition); err != nil {
				return err
			}
		} else if entry.position != position || entry.shufflePosition != shufflePosition {
			// Edit order in place so future entry metadata survives moves.
			if _, err := tx.ExecContext(ctx, `
				UPDATE play_queue SET position = ?, shuffle_position = ? WHERE entry_id = ?
			`, position, shufflePosition, entry.id); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO play_queue_state(profile_id, current_entry_id, position_ms, shuffle, repeat_mode)
		VALUES (?, NULLIF(?, ''), ?, ?, ?)
		ON CONFLICT(profile_id) DO UPDATE SET current_entry_id = excluded.current_entry_id,
			position_ms = excluded.position_ms, shuffle = excluded.shuffle, repeat_mode = excluded.repeat_mode
		WHERE play_queue_state.current_entry_id IS NOT excluded.current_entry_id
			OR play_queue_state.position_ms != excluded.position_ms OR play_queue_state.shuffle != excluded.shuffle
			OR play_queue_state.repeat_mode != excluded.repeat_mode
	`, profileID, q.currentID, q.positionMS, q.shuffle, q.repeat); err != nil {
		return err
	}
	for _, entry := range original {
		if !kept[entry.id] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM play_queue WHERE entry_id = ?`, entry.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// myEngagementChunk pages engagement rows before resolving Media
// visibility. The queue entry ID keeps the cursor unique even when a single
// Media has enough additions to span several chunks.
func myEngagementChunk(ctx context.Context, tx *sql.Tx, profileID ProfileID, after myEngagementRankedEvent, query MyEngagementLogQuery) ([]MediaID, []myEngagementRankedEvent, error) {
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
	args = append(args, after.event.MediaID, after.rank, after.entryID, myEngagementLogChunkSize)
	// #nosec G202 -- dateWhere is assembled only from fixed timestamp predicates.
	rows, err := tx.QueryContext(ctx, `
		WITH engagement_rows(kind, rank, media_id, occurred_at_ms, entry_id) AS (
			SELECT 'play', 0, media_id, updated_at_ms, '' FROM playback_progress WHERE profile_id = ?
			UNION ALL
			SELECT 'favorite', 1, media_id, favorited_at_ms, '' FROM media_favorite WHERE profile_id = ?
			UNION ALL
			SELECT 'queue', 2, media_id, added_at_ms, entry_id FROM play_queue WHERE profile_id = ?
		)
		SELECT kind, rank, media_id, occurred_at_ms, entry_id
		FROM engagement_rows
		WHERE `+dateWhere+` AND (media_id, rank, entry_id) > (?, ?, ?)
		ORDER BY media_id, rank, entry_id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	ids := make([]MediaID, 0, myEngagementLogChunkSize)
	events := make([]myEngagementRankedEvent, 0, myEngagementLogChunkSize)
	var previous MediaID
	for rows.Next() {
		var kind string
		var ranked myEngagementRankedEvent
		var occurredAt int64
		if err := rows.Scan(&kind, &ranked.rank, &ranked.event.MediaID, &occurredAt, &ranked.entryID); err != nil {
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

func queueSourceItem(media Media) SourceItem {
	return SourceItem{
		ID: media.ID, Kind: "media", Title: media.Title, LibraryID: media.LibraryID,
		Summary: mediaDurationSummary(media.DurationSeconds),
	}
}
