package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// feature:collections-batching — AddCollectionMedia,
// RemoveCollectionMedia, and ReorderCollectionMedia used to run one
// INSERT/DELETE/UPDATE per Media on the single writer connection. This test
// counts the actual statements the catalog writer executes and asserts the
// count does not scale linearly with the selection size, mirroring
// TestBulkEditApplyBatchesStatementsAcrossChunks in bulk_edit_batch_test.go.

const collectionsCountingDriverName = "beamfall_collections_counting"

var (
	collectionsCountingDriverOnce sync.Once
	collectionsStatementCount     atomic.Int64
)

type collectionsCountingDriver struct{ inner driver.Driver }

func (d *collectionsCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &collectionsCountingConn{Conn: conn}, nil
}

// collectionsCountingConn counts every statement the wrapped connection
// actually executes, implementing ExecContext explicitly (not just
// QueryContext) since collection mutations are write-heavy — without it,
// database/sql falls back to Prepare+Stmt.Exec on the unwrapped real
// connection and the writes go uncounted.
type collectionsCountingConn struct{ driver.Conn }

func (c *collectionsCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	collectionsStatementCount.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *collectionsCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	collectionsStatementCount.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func installCountingCollectionsCatalogWriter(t *testing.T, st *Store) {
	t.Helper()
	collectionsCountingDriverOnce.Do(func() {
		sql.Register(collectionsCountingDriverName, &collectionsCountingDriver{inner: &sqlite.Driver{}})
	})
	if err := st.catalogW.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open(collectionsCountingDriverName, st.catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	st.catalogW = writer
}

func importCollectionMedia(t *testing.T, st *Store, n int) []MediaID {
	t.Helper()
	ids := make([]MediaID, n)
	for i := 0; i < n; i++ {
		media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fmt.Sprintf("/collections-batch/media-%05d.mp4", i),
			Title:              fmt.Sprintf("collections-batch-media-%05d", i),
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: fmt.Sprintf("sha256:coll-%05d", i),
			OSHash:             fmt.Sprintf("%016x", i+1),
		})
		if err != nil {
			t.Fatalf("import media %d: %v", i, err)
		}
		ids[i] = media.ID
	}
	return ids
}

// assertSublinearStatementGrowth fails if going from smallN to largeN ids
// (a 50x jump in this test's cases) costs anywhere close to a proportional
// jump in statements. The pre-fix per-id loops scaled ~1:1 with selection
// size; the batched shape only grows with the number of 500-id chunks
// (1 -> 2 for 10 -> 501 ids here).
func assertSublinearStatementGrowth(t *testing.T, op string, small, large int64, smallN, largeN int) {
	t.Helper()
	if small == 0 {
		t.Fatalf("%s: expected at least one statement for the small selection", op)
	}
	t.Logf("%s statements: %d ids -> %d statements, %d ids -> %d statements", op, smallN, small, largeN, large)
	if ratio := float64(large) / float64(small); ratio > 5 {
		t.Fatalf("%s statement count grew %.1fx going from %d to %d ids (small=%d, large=%d) — want well under linear growth",
			op, ratio, smallN, largeN, small, large)
	}
}

// TestCollectionMediaMutationsBatchStatementsAcrossChunks is the
// AUDIT0724-PERF-BULK-1 regression test for the three collection mutations:
// statement count must grow with the number of 500-id chunks the selection
// needs, not with the selection size itself.
func TestCollectionMediaMutationsBatchStatementsAcrossChunks(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllLibraries: true}

	allIDs := importCollectionMedia(t, st, 501)
	smallIDs := allIDs[:10]

	newCollection := func(t *testing.T) Collection {
		t.Helper()
		c, err := st.CreateCollection(ctx, Collection{LibraryID: "library-local", Name: "batch-test"}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	measure := func(t *testing.T, run func() error) int64 {
		t.Helper()
		collectionsStatementCount.Store(0)
		if err := run(); err != nil {
			t.Fatal(err)
		}
		return collectionsStatementCount.Load()
	}

	installCountingCollectionsCatalogWriter(t, st)

	t.Run("AddCollectionMedia", func(t *testing.T) {
		smallColl := newCollection(t)
		small := measure(t, func() error {
			_, err := st.AddCollectionMedia(ctx, smallColl.ID, smallIDs, filter, time.Now().UTC())
			return err
		})
		largeColl := newCollection(t)
		large := measure(t, func() error {
			_, err := st.AddCollectionMedia(ctx, largeColl.ID, allIDs, filter, time.Now().UTC())
			return err
		})
		assertSublinearStatementGrowth(t, "AddCollectionMedia", small, large, len(smallIDs), len(allIDs))
	})

	t.Run("RemoveCollectionMedia", func(t *testing.T) {
		smallColl := newCollection(t)
		if _, err := st.AddCollectionMedia(ctx, smallColl.ID, smallIDs, filter, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		largeColl := newCollection(t)
		if _, err := st.AddCollectionMedia(ctx, largeColl.ID, allIDs, filter, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		small := measure(t, func() error {
			_, err := st.RemoveCollectionMedia(ctx, smallColl.ID, smallIDs, filter, time.Now().UTC())
			return err
		})
		large := measure(t, func() error {
			_, err := st.RemoveCollectionMedia(ctx, largeColl.ID, allIDs, filter, time.Now().UTC())
			return err
		})
		assertSublinearStatementGrowth(t, "RemoveCollectionMedia", small, large, len(smallIDs), len(allIDs))
	})

	t.Run("ReorderCollectionMedia", func(t *testing.T) {
		coll := newCollection(t)
		if _, err := st.AddCollectionMedia(ctx, coll.ID, allIDs, filter, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		// Reordering doesn't change membership, so the same populated
		// collection can take both the small and large reorder call.
		small := measure(t, func() error {
			_, err := st.ReorderCollectionMedia(ctx, coll.ID, smallIDs, filter, time.Now().UTC())
			return err
		})
		large := measure(t, func() error {
			_, err := st.ReorderCollectionMedia(ctx, coll.ID, allIDs, filter, time.Now().UTC())
			return err
		})
		assertSublinearStatementGrowth(t, "ReorderCollectionMedia", small, large, len(smallIDs), len(allIDs))
	})
}
