package store

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestOperatorRelationConcurrentHydrationAndRestart(t *testing.T) {
	ctx := t.Context()
	dir := migratedDataDir(t)
	st, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	filter := LibraryFilter{AllLibraries: true}
	operator := Entity{ID: "operator", Kind: "widget", LibraryID: "library-local", Title: "Operator"}
	derived := Entity{ID: "derived", Kind: "widget", LibraryID: "library-local", Title: "Derived"}
	for _, entity := range []Entity{operator, derived} {
		if err := st.EnsureEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	relation := EntityRelation{FromEntityID: operator.ID, ToMediaID: "media-alpha", Name: "member", Cardinality: "one", Attributes: map[string]string{"index": "3"}}
	hydrated := EntityRelation{FromEntityID: derived.ID, ToMediaID: relation.ToMediaID, Name: relation.Name, Cardinality: "one"}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 12 {
			if err := st.ReplaceMediaRelations(ctx, relation.ToMediaID, []EntityRelation{hydrated}); err != nil {
				t.Error(err)
				return
			}
			if err := st.UpsertHydratedEntityRelation(ctx, hydrated); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		_, err := st.SetOperatorMediaRelation(ctx, relation, Entity{}, filter, ActivityEntry{
			OperationID: "concurrent-operator-relation", ProfileID: "profile-default", OccurredAt: time.Now(),
		})
		if err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Even an additive writer missing cardinality honors the persisted lock.
	hydrated.Cardinality = ""
	if err := st.UpsertHydratedEntityRelation(ctx, hydrated); err != nil {
		t.Fatal(err)
	}
	rows, err := st.EntitiesForMedia(ctx, relation.ToMediaID, relation.Name, filter)
	if err != nil || len(rows) != 1 || rows[0].Entity.ID != operator.ID || !rows[0].OperatorLocked || rows[0].Attributes["index"] != "3" {
		t.Fatalf("rehydrated rows = %+v, %v", rows, err)
	}
}

func TestOperatorRelationAuditRecoversAfterPublicationFailure(t *testing.T) {
	ctx := t.Context()
	st := openSeeded(t)
	filter := LibraryFilter{AllLibraries: true}
	entity := widgetEntity("entity-audit", "library-local")
	if err := st.EnsureEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := st.eventsW.ExecContext(ctx, `
		CREATE TEMP TRIGGER reject_relation_activity BEFORE INSERT ON activity_entry
		WHEN NEW.action = 'media.relation.set'
		BEGIN SELECT RAISE(FAIL, 'injected activity publication failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	row := EntityRelation{FromEntityID: entity.ID, ToMediaID: "media-alpha", Name: "member"}
	event := ActivityEntry{OperationID: "deferred-activity", ProfileID: "profile-default", OccurredAt: time.Now()}
	if _, err := st.SetOperatorMediaRelation(ctx, row, Entity{}, filter, event); err != nil {
		t.Fatal(err)
	}
	var pending bool
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT pending_activity IS NOT NULL FROM media_relation_activity WHERE operation_id = ?
	`, event.OperationID).Scan(&pending); err != nil || !pending {
		t.Fatalf("publication was not deferred: pending=%v err=%v", pending, err)
	}
	if _, err := st.eventsW.ExecContext(ctx, `DROP TRIGGER reject_relation_activity`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: filter})
		if err != nil {
			t.Fatal(err)
		}
		matches := 0
		for _, entry := range entries {
			if entry.OperationID == event.OperationID {
				matches++
				change := entry.MediaRelationChange
				if entry.ProfileID != event.ProfileID || change == nil || len(change.Before) != 0 ||
					len(change.After) != 1 || change.After[0].EntityID != entity.ID || change.After[0].LockedAtMS == nil {
					t.Fatalf("recovered activity = %+v, change = %+v", entry, change)
				}
			}
		}
		if matches != 1 {
			t.Fatalf("found %d copies of deferred activity", matches)
		}
	}
}

func TestOperatorRelationSurvivesEncryptedSeal(t *testing.T) {
	ctx := t.Context()
	st, media := seededEncryptedMedia(t, "operator-relation")
	defer st.Close()
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	entity := Entity{ID: "private-relation-entity", Kind: "widget", Title: "Private Group"}
	row := EntityRelation{FromEntityID: entity.ID, ToMediaID: media.ID, Name: "member", Cardinality: "one", Attributes: map[string]string{"private": "private value"}}
	if _, err := st.SetOperatorMediaRelation(ctx, row, entity, filter, ActivityEntry{
		OperationID: "encrypted-relation-activity", ProfileID: "profile-default", OccurredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	var leaked bool
	if err := st.catalogR.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM media_relation_activity WHERE media_id = ?)`, media.ID).Scan(&leaked); err != nil || leaked {
		t.Fatalf("shared catalog leaked recovery rows: %v, %v", leaked, err)
	}
	var entryID int64
	var target string
	if err := st.eventsR.QueryRowContext(ctx, `SELECT id, target_id FROM activity_entry WHERE operation_id = 'encrypted-relation-activity'`).Scan(&entryID, &target); err != nil || target != "" {
		t.Fatalf("shared activity indexed private target %q, %v", target, err)
	}
	cipher := copyEncryptedLibraryDBCipher{}
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, cipher); err != nil {
		t.Fatal(err)
	}
	entry, err := st.ActivityByID(ctx, entryID, filter)
	if err != nil || entry.MediaRelationChange != nil {
		t.Fatalf("sealed recovery read = %+v, %v", entry, err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, cipher); err != nil {
		t.Fatal(err)
	}
	rows, err := st.EntitiesForMedia(ctx, media.ID, "member", filter)
	if err != nil || len(rows) != 1 || !rows[0].OperatorLocked || rows[0].Entity.ID != entity.ID {
		t.Fatalf("unsealed relations = %+v, %v", rows, err)
	}
	entry, err = st.ActivityByID(ctx, entryID, filter)
	if err != nil || entry.MediaRelationChange == nil || len(entry.MediaRelationChange.After) != 1 ||
		entry.MediaRelationChange.After[0].Attributes["private"] != "private value" {
		t.Fatalf("unsealed activity = %+v, %v", entry, err)
	}
}

func TestOperatorRelationRemovalSurvivesMergeUndo(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	filter := LibraryFilter{AllLibraries: true}
	for _, id := range []EntityID{"merge-winner", "merge-loser"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	row := EntityRelation{FromEntityID: "merge-loser", ToMediaID: "media-alpha", Name: "member"}
	if err := st.UpsertEntityRelation(ctx, row); err != nil {
		t.Fatal(err)
	}
	batch, err := st.MergeEntities(ctx, "merge-winner", "merge-loser", filter, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row.FromEntityID = "merge-winner"
	if err := st.RemoveOperatorMediaRelation(ctx, row, filter, ActivityEntry{
		OperationID: "remove-after-merge", ProfileID: "profile-default", OccurredAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UndoBatch(ctx, batch.ID, filter, time.Now()); !errors.Is(err, ErrOperatorRelationUndoConflict) {
		t.Fatalf("undo error = %v, want operator conflict", err)
	}
	rows, err := st.EntitiesForMedia(ctx, row.ToMediaID, row.Name, filter)
	if err != nil || len(rows) != 0 {
		t.Fatalf("removed relation returned: %+v, %v", rows, err)
	}
}

// TestOperatorRelationRechecksRestrictionInsideWrite restricts a Library after
// the visibility pre-check and before the write transaction opens. The write
// must refuse, for either endpoint, on both catalog planes and both verbs.
func TestOperatorRelationRechecksRestrictionInsideWrite(t *testing.T) {
	ctx := t.Context()
	filter := LibraryFilter{AllLibraries: true}
	restrictInWindow := func(st *Store, library LibraryID) {
		st.operatorRelationWriteHook = func() {
			// The hook runs under the store read lock, so write directly.
			if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET is_restricted = 1 WHERE id = ?`, library); err != nil {
				t.Fatal(err)
			}
		}
	}
	write := func(st *Store, row EntityRelation, entity Entity, remove bool, op string) error {
		event := ActivityEntry{OperationID: op, ProfileID: "profile-default", OccurredAt: time.Now()}
		if remove {
			return st.RemoveOperatorMediaRelation(ctx, row, filter, event)
		}
		_, err := st.SetOperatorMediaRelation(ctx, row, entity, filter, event)
		return err
	}
	for _, remove := range []bool{false, true} {
		verb := "set "
		if remove {
			verb = "remove "
		}
		t.Run(verb+"shared media endpoint", func(t *testing.T) {
			st := openSeeded(t)
			entity := widgetEntity("restricted-media-entity", "library-local")
			if err := st.EnsureEntity(ctx, entity); err != nil {
				t.Fatal(err)
			}
			row := EntityRelation{FromEntityID: entity.ID, ToMediaID: "media-alpha", Name: "member"}
			if err := st.UpsertEntityRelation(ctx, row); err != nil {
				t.Fatal(err)
			}
			restrictInWindow(st, "library-local")
			if err := write(st, row, Entity{}, remove, "restricted-media"); err == nil {
				t.Fatal("write committed after its Media's Library was restricted")
			}
			assertRelationRows(t, st, row, 1, false)
		})
		t.Run(verb+"shared entity endpoint", func(t *testing.T) {
			st := openSeeded(t)
			if err := st.UpdateLibraryRestricted(ctx, "library-archive", false); err != nil {
				t.Fatal(err)
			}
			entity := widgetEntity("restricted-source-entity", "library-archive")
			if err := st.EnsureEntity(ctx, entity); err != nil {
				t.Fatal(err)
			}
			row := EntityRelation{FromEntityID: entity.ID, ToMediaID: "media-alpha", Name: "member"}
			if err := st.UpsertEntityRelation(ctx, row); err != nil {
				t.Fatal(err)
			}
			restrictInWindow(st, "library-archive")
			if err := write(st, row, Entity{}, remove, "restricted-entity"); err == nil {
				t.Fatal("write committed after its Entity's Library was restricted")
			}
			assertRelationRows(t, st, row, 1, false)
		})
		t.Run(verb+"encrypted plane", func(t *testing.T) {
			st, media := seededEncryptedMedia(t, "restricted-relation")
			defer st.Close()
			entity := Entity{ID: "restricted-private-entity", Kind: "widget", Title: "Private"}
			row := EntityRelation{FromEntityID: entity.ID, ToMediaID: media.ID, Name: "member"}
			if remove {
				if _, err := st.SetOperatorMediaRelation(ctx, row, entity, filter, ActivityEntry{
					OperationID: "restricted-encrypted-seed", ProfileID: "profile-default", OccurredAt: time.Now(),
				}); err != nil {
					t.Fatal(err)
				}
			}
			restrictInWindow(st, media.LibraryID)
			if err := write(st, row, entity, remove, "restricted-encrypted"); err == nil {
				t.Fatal("encrypted write committed after its Library was restricted")
			}
			want := 0
			if remove {
				want = 1
			}
			// A refused removal keeps the seeded operator lock.
			assertRelationRows(t, st, row, want, remove)
		})
	}
}

func assertRelationRows(t *testing.T, st *Store, row EntityRelation, want int, locked bool) {
	t.Helper()
	st.operatorRelationWriteHook = nil
	rows, err := st.EntitiesForMedia(t.Context(), row.ToMediaID, row.Name, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(rows) != want {
		t.Fatalf("relation rows = %+v, %v; want %d", rows, err, want)
	}
	for _, got := range rows {
		if got.OperatorLocked != locked {
			t.Fatalf("refused write changed the operator lock: %+v", got)
		}
	}
}

// TestMergedRelationLeavesScraperCleanup (CORE-3131): a merge re-points the
// loser's scraper link to the winner as the operator's row, so the scraper's
// cleanup keeps it; undo restores the scraper's row and its cleanup.
func TestMergedRelationLeavesScraperCleanup(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	filter := LibraryFilter{AllLibraries: true}
	const scraper = "scraper:demo/item"
	for _, id := range []EntityID{"merge-winner", "merge-loser"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	row := EntityRelation{FromEntityID: "merge-loser", ToMediaID: "media-alpha", Name: "member", Writer: scraper}
	if err := st.UpsertEntityRelation(ctx, row); err != nil {
		t.Fatal(err)
	}
	batch, err := st.MergeEntities(ctx, "merge-winner", "merge-loser", filter, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	member := []WrittenRelation{{Kind: "widget", Name: "member"}}
	if err := st.RemoveWrittenMediaRelations(ctx, row.ToMediaID, scraper, member); err != nil {
		t.Fatal(err)
	}
	rows, err := st.EntitiesForMedia(ctx, row.ToMediaID, row.Name, filter)
	if err != nil || len(rows) != 1 || rows[0].Entity.ID != "merge-winner" {
		t.Fatalf("merged row after scraper cleanup = %+v, %v; want the winner kept", rows, err)
	}
	if _, err := st.UndoBatch(ctx, batch.ID, filter, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveWrittenMediaRelations(ctx, row.ToMediaID, scraper, member); err != nil {
		t.Fatal(err)
	}
	if rows, err = st.EntitiesForMedia(ctx, row.ToMediaID, row.Name, filter); err != nil || len(rows) != 0 {
		t.Fatalf("restored row after scraper cleanup = %+v, %v; want the scraper's row removed", rows, err)
	}
}

// TestHydratedRelationWrittenReportsLock (CORE-3131): the reporting upsert
// says false when an operator's lock kept the derived link out and true for
// a written Media or Entity link.
func TestHydratedRelationWrittenReportsLock(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	filter := LibraryFilter{AllLibraries: true}
	for _, id := range []EntityID{"operator", "derived"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	derived := EntityRelation{FromEntityID: "derived", ToMediaID: "media-alpha", Name: "member", Cardinality: "one"}
	if written, err := st.UpsertHydratedEntityRelationWritten(ctx, derived); err != nil || !written {
		t.Fatalf("unlocked write = %t, %v; want written", written, err)
	}
	if _, err := st.SetOperatorMediaRelation(ctx, EntityRelation{
		FromEntityID: "operator", ToMediaID: derived.ToMediaID, Name: derived.Name, Cardinality: "one",
	}, Entity{}, filter, ActivityEntry{OperationID: "lock-written", ProfileID: "profile-default", OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if written, err := st.UpsertHydratedEntityRelationWritten(ctx, derived); err != nil || written {
		t.Fatalf("locked write = %t, %v; want blocked", written, err)
	}
	parent := EntityRelation{FromEntityID: "derived", ToEntityID: "operator", Name: "parent"}
	if written, err := st.UpsertHydratedEntityRelationWritten(ctx, parent); err != nil || !written {
		t.Fatalf("entity write = %t, %v; want written", written, err)
	}
}
