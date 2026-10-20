package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"modernc.org/sqlite"
)

const restrictedCountingDriverName = "beamfall_restricted_counting"

var (
	restrictedCountingDriverOnce sync.Once
	restrictedQueryCount         atomic.Int64
)

type restrictedCountingDriver struct {
	inner driver.Driver
	count *atomic.Int64
}

func (d *restrictedCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &restrictedCountingConn{Conn: conn, count: d.count}, nil
}

type restrictedCountingConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c *restrictedCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.count.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestAnnotateRestrictedLockedBatchesQueriesPerPage(t *testing.T) {
	restrictedCountingDriverOnce.Do(func() {
		sql.Register(restrictedCountingDriverName, &restrictedCountingDriver{
			inner: &sqlite.Driver{},
			count: &restrictedQueryCount,
		})
	})
	db, err := sql.Open(restrictedCountingDriverName, filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE entity (id TEXT PRIMARY KEY, library_id TEXT NOT NULL);
		CREATE TABLE media (id TEXT PRIMARY KEY, library_id TEXT NOT NULL);
	`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		libraryID := "library-open"
		if i%2 == 0 {
			libraryID = "library-restricted"
		}
		if _, err := db.Exec(`INSERT INTO entity (id, library_id) VALUES (?, ?)`, fmt.Sprintf("entity-%03d", i), libraryID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO media (id, library_id) VALUES (?, ?)`, fmt.Sprintf("media-%03d", i), libraryID); err != nil {
			t.Fatal(err)
		}
	}

	st := &Store{catalogR: db}
	restricted := map[LibraryID]bool{"library-restricted": true}
	for _, pageSize := range []int{1, 100} {
		items := make([]SourceItem, 0, pageSize*2+2)
		for i := 0; i < pageSize; i++ {
			items = append(items,
				SourceItem{ID: MediaID(fmt.Sprintf("entity-%03d", i)), Kind: "entity"},
				SourceItem{ID: MediaID(fmt.Sprintf("media-%03d", i)), Kind: "media"},
			)
		}
		items = append(items,
			SourceItem{ID: "missing", Kind: "media"},
			SourceItem{ID: "already-restricted", Kind: "media", Restricted: true},
		)

		restrictedQueryCount.Store(0)
		if err := st.annotateRestrictedLocked(context.Background(), restricted, items); err != nil {
			t.Fatal(err)
		}
		if got := restrictedQueryCount.Load(); got != 2 {
			t.Fatalf("page size %d executed %d mapping queries, want 2", pageSize, got)
		}
		for i := 0; i < pageSize; i++ {
			want := i%2 == 0
			if items[i*2].Restricted != want || items[i*2+1].Restricted != want {
				t.Fatalf("page size %d item %d restricted = (%t, %t), want (%t, %t)",
					pageSize, i, items[i*2].Restricted, items[i*2+1].Restricted, want, want)
			}
		}
		if items[len(items)-2].Restricted {
			t.Fatalf("unresolvable item became restricted: %#v", items[len(items)-2])
		}
		if !items[len(items)-1].Restricted {
			t.Fatalf("additive annotation cleared an existing flag: %#v", items[len(items)-1])
		}
	}
}
