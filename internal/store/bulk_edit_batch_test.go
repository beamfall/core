package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// feature:bulk-edit-batching — BulkEditApply and the
// collection mutations used to run one query/exec per Media on the single
// writer connection; a large selection meant tens of thousands of serialized
// round trips. This test proves the fix behaviourally: it counts the actual
// statements the catalog writer connection executes and asserts the count
// does not scale linearly with the selection size, the same way
// TestMediaFavoritedSetBatchesUniqueIDs and
// TestAnnotateRestrictedLockedBatchesQueriesPerPage already prove batching
// elsewhere in this package. BulkEditApply also emits one media.updated
// outbox event per edited Media on the SEPARATE events-writer connection
// (appendMediaEventsLocked, events.db), so the events writer is counted too —
// otherwise a regression back to a per-id events loop would be invisible to
// this test even with the catalog side fully batched.

const (
	bulkEditCatalogCountingDriverName = "beamfall_bulkedit_catalog_counting"
	bulkEditEventsCountingDriverName  = "beamfall_bulkedit_events_counting"
)

var (
	bulkEditCountingDriverOnce  sync.Once
	bulkEditStatementCount      atomic.Int64
	bulkEditEventStatementCount atomic.Int64
)

type bulkEditCountingDriver struct {
	inner   driver.Driver
	counter *atomic.Int64
}

func (d *bulkEditCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &bulkEditCountingConn{Conn: conn, counter: d.counter}, nil
}

// bulkEditCountingConn counts every statement the wrapped connection actually
// executes, into whichever counter its driver was registered with (catalog vs
// events writer). It implements ExecContext (not just QueryContext) because
// the code under test is write-heavy (INSERT/DELETE/UPDATE inside a
// transaction); without an explicit ExecContext override, database/sql would
// silently fall back to Prepare+Stmt.Exec on the *unwrapped* real connection
// and every write would go uncounted.
type bulkEditCountingConn struct {
	driver.Conn
	counter *atomic.Int64
}

func (c *bulkEditCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.counter.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *bulkEditCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.counter.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

// installCountingCatalogWriter swaps the Store's real catalog writer
// connection for one that counts every statement it executes, pointed at the
// same on-disk catalog.db so all the existing seeded state carries over.
func installCountingCatalogWriter(t *testing.T, st *Store) {
	t.Helper()
	bulkEditCountingDriverOnce.Do(registerBulkEditCountingDrivers)
	if err := st.catalogW.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open(bulkEditCatalogCountingDriverName, st.catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	st.catalogW = writer
}

// installCountingEventsWriter is installCountingCatalogWriter's twin for the
// events-writer connection (events.db): it counts every statement
// appendMediaEventsLocked (and any other events-writer path) actually
// executes, pointed at the same on-disk events.db.
func installCountingEventsWriter(t *testing.T, st *Store) {
	t.Helper()
	bulkEditCountingDriverOnce.Do(registerBulkEditCountingDrivers)
	if err := st.eventsW.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open(bulkEditEventsCountingDriverName, st.eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	st.eventsW = writer
}

func registerBulkEditCountingDrivers() {
	sql.Register(bulkEditCatalogCountingDriverName, &bulkEditCountingDriver{inner: &sqlite.Driver{}, counter: &bulkEditStatementCount})
	sql.Register(bulkEditEventsCountingDriverName, &bulkEditCountingDriver{inner: &sqlite.Driver{}, counter: &bulkEditEventStatementCount})
}

// importBulkEditMedia seeds n plain Media rows in library-local via the real
// import path (so the full schema — media, media_file, fts, etc. — is
// populated exactly like production), returning their ids in import order.
func importBulkEditMedia(t *testing.T, st *Store, n int, offset int) []MediaID {
	t.Helper()
	ids := make([]MediaID, n)
	for i := 0; i < n; i++ {
		idx := offset + i
		media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/bulkedit/media-%05d.mp4", idx),
			Title:              fmt.Sprintf("bulk-edit-media-%05d", idx),
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("sha256:bulkedit-%05d", idx),
			OSHash:             fmt.Sprintf("%016x", idx+1),
		})
		if err != nil {
			t.Fatalf("import media %d: %v", idx, err)
		}
		ids[i] = media.ID
	}
	return ids
}

// TestBulkEditApplyBatchesStatementsAcrossChunks is the AUDIT0724-PERF-BULK-1
// regression test: statement count must grow with the number of 500-id chunks
// the selection needs, not with the selection size itself.
func TestBulkEditApplyBatchesStatementsAcrossChunks(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	smallIDs := importBulkEditMedia(t, st, 10, 0)
	// 501 total ids straddles the 500-id chunk boundary: one more chunk, not a
	// 50x-bigger selection's worth of statements.
	largeIDs := append(append([]MediaID{}, smallIDs...), importBulkEditMedia(t, st, 491, 10)...)
	if len(largeIDs) != 501 {
		t.Fatalf("largeIDs = %d, want 501", len(largeIDs))
	}

	installCountingCatalogWriter(t, st)
	installCountingEventsWriter(t, st)
	edit := []MediaField{{Name: "genre", Type: EntityFieldText, Text: "drama"}}
	filter := LibraryFilter{AllLibraries: true}

	bulkEditStatementCount.Store(0)
	bulkEditEventStatementCount.Store(0)
	smallBatch, err := st.BulkEditApply(ctx, smallIDs, edit, filter, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if smallBatch.Affected != len(smallIDs) {
		t.Fatalf("small batch affected = %d, want %d", smallBatch.Affected, len(smallIDs))
	}
	smallCatalog := bulkEditStatementCount.Load()
	smallEvents := bulkEditEventStatementCount.Load()
	small := smallCatalog + smallEvents
	if small == 0 {
		t.Fatal("expected at least one statement for the small batch")
	}
	if smallEvents == 0 {
		t.Fatal("expected at least one events-writer statement for the small batch")
	}

	bulkEditStatementCount.Store(0)
	bulkEditEventStatementCount.Store(0)
	largeBatch, err := st.BulkEditApply(ctx, largeIDs, edit, filter, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if largeBatch.Affected != len(largeIDs) {
		t.Fatalf("large batch affected = %d, want %d", largeBatch.Affected, len(largeIDs))
	}
	largeCatalog := bulkEditStatementCount.Load()
	largeEvents := bulkEditEventStatementCount.Load()
	large := largeCatalog + largeEvents

	t.Logf("statements: %d ids -> %d total (%d catalog + %d events), %d ids -> %d total (%d catalog + %d events)",
		len(smallIDs), small, smallCatalog, smallEvents, len(largeIDs), large, largeCatalog, largeEvents)

	// A per-ID events loop (the confirmed regression: one events-writer
	// statement per selected Media) would cost 501 events
	// statements for the large batch alone — 50x the small batch's ~10 and
	// enough by itself to blow the ratio budget below, even with the catalog
	// side fully batched. Asserting on catalog+events together, not the
	// catalog count alone, is what makes this test catch that regression.
	if largeEvents > 10 {
		t.Fatalf("events-writer statement count = %d for %d ids — want a small, chunk-bounded count, not ~1 per id",
			largeEvents, len(largeIDs))
	}

	// 50x the ids (10 -> 501) must not cost anywhere close to 50x the
	// statements. The pre-fix shape (one query/exec per field per table per
	// Media, plus one events-writer INSERT per Media) would have scaled ~1:1
	// with selection size; the batched shape only grows with the number of
	// 500-id chunks (1 -> 2 here).
	if ratio := float64(large) / float64(small); ratio > 5 {
		t.Fatalf("statement count grew %.1fx going from %d to %d ids (small=%d, large=%d) — want well under linear growth",
			ratio, len(smallIDs), len(largeIDs), small, large)
	}
}

// BenchmarkBulkEditApplyLargeSelection reports the wall-clock cost of a
// large bulk edit — the number that actually matters
// for single-writer contention, alongside the statement-count proof above.
func BenchmarkBulkEditApplyLargeSelection(b *testing.B) {
	st, err := Open(context.Background(), b.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ids := make([]MediaID, 3000)
	for i := range ids {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/bulkedit-bench/media-%05d.mp4", i),
			Title:              fmt.Sprintf("bulkedit-bench-media-%05d", i),
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("sha256:be-bench-%05d", i),
			OSHash:             fmt.Sprintf("%016x", i+1),
		})
		if err != nil {
			b.Fatalf("import media %d: %v", i, err)
		}
		ids[i] = media.ID
	}
	edit := []MediaField{{Name: "genre", Type: EntityFieldText, Text: "drama"}}
	filter := LibraryFilter{AllLibraries: true}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.BulkEditApply(ctx, ids, edit, filter, time.Now().UTC()); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBulkEditApplyLargeSelectionOldShape is the pre-AUDIT0724-PERF-BULK-1
// baseline for the benchmark above: the same 3000-Media edit, run with the
// exact one-query/exec-per-Media shape BulkEditApply used before this ticket,
// reassembled here from the still-present primitives (mediaFieldsTx,
// recordFieldProvenanceTx, insertMediaFields, appendBatchItemTx — all still
// used elsewhere, e.g. UndoBatch and file_operations.go) rather than
// fabricated. This exists only to measure the before/after wall-clock delta;
// it is not wired into any production path.
func BenchmarkBulkEditApplyLargeSelectionOldShape(b *testing.B) {
	st, err := Open(context.Background(), b.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ids := make([]MediaID, 3000)
	for i := range ids {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/bulkedit-bench-old/media-%05d.mp4", i),
			Title:              fmt.Sprintf("bulkedit-bench-old-media-%05d", i),
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("sha256:bo-%05d", i),
			OSHash:             fmt.Sprintf("%016x", i+1),
		})
		if err != nil {
			b.Fatalf("import media %d: %v", i, err)
		}
		ids[i] = media.ID
	}
	fields := []MediaField{{Name: "genre", Type: EntityFieldText, Text: "drama"}}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		now := time.Now().UTC()
		if err := func() error {
			tx, err := st.catalogW.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			batchID, err := newBatchID()
			if err != nil {
				return err
			}
			if err := insertBatchTx(ctx, tx, batchID, opKindBulkEdit, now); err != nil {
				return err
			}
			for seq, id := range ids {
				current, err := mediaFieldsTx(ctx, tx, id)
				if err != nil {
					return err
				}
				state, err := json.Marshal(bulkEditItemState{Edited: []string{"genre"}, Fields: current})
				if err != nil {
					return err
				}
				for _, field := range fields {
					for _, table := range mediaFieldTables {
						if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE media_id = ? AND field = ?`, id, field.Name); err != nil { //nolint:gosec // G202: table from mediaFieldTables' closed enum; values bound.
							return err
						}
					}
					if err := recordFieldProvenanceTx(ctx, tx, id, field.Name, MutationSourceManual); err != nil {
						return err
					}
				}
				if err := insertMediaFields(ctx, tx, id, fields); err != nil {
					return err
				}
				if err := appendBatchItemTx(ctx, tx, batchID, seq, id, state); err != nil {
					return err
				}
			}
			return tx.Commit()
		}(); err != nil {
			b.Fatal(err)
		}
	}
}
