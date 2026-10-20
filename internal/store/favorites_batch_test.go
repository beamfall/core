package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"maps"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"modernc.org/sqlite"
)

const favoriteCountingDriverName = "beamfall_favorite_counting"

var (
	favoriteCountingDriverOnce sync.Once
	favoriteQueryCount         atomic.Int64
)

type favoriteCountingDriver struct{ inner driver.Driver }

func (d *favoriteCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &favoriteCountingConn{Conn: conn}, nil
}

type favoriteCountingConn struct{ driver.Conn }

func (c *favoriteCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	favoriteQueryCount.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestMediaFavoritedSetBatchesUniqueIDs(t *testing.T) {
	favoriteCountingDriverOnce.Do(func() {
		sql.Register(favoriteCountingDriverName, &favoriteCountingDriver{inner: &sqlite.Driver{}})
	})
	db, err := sql.Open(favoriteCountingDriverName, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE media_favorite (
		profile_id TEXT NOT NULL,
		media_id TEXT NOT NULL,
		favorited_at_ms INTEGER NOT NULL,
		PRIMARY KEY (profile_id, media_id)
	)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []MediaID{"media-favorite", "media-000", "media-500"} {
		if _, err := db.Exec(`INSERT INTO media_favorite VALUES (?, ?, 1)`, "profile-1", id); err != nil {
			t.Fatal(err)
		}
	}
	st := &Store{eventsR: db}

	if _, err := st.MediaFavoritedSet(context.Background(), "", nil); err == nil {
		t.Fatal("empty profile ID accepted")
	}

	tests := []struct {
		name        string
		ids         []MediaID
		want        map[MediaID]bool
		wantQueries int64
	}{
		{name: "empty", want: map[MediaID]bool{}},
		{
			name:        "duplicates and missing",
			ids:         []MediaID{"media-favorite", "media-missing", "media-favorite", "media-missing"},
			want:        map[MediaID]bool{"media-favorite": true},
			wantQueries: 1,
		},
		{name: "300 unique", ids: mediaIDs(300), want: map[MediaID]bool{"media-000": true}, wantQueries: 1},
		{name: "500 unique plus duplicate", ids: append(mediaIDs(500), "media-000"), want: map[MediaID]bool{"media-000": true}, wantQueries: 1},
		{name: "501 unique", ids: mediaIDs(501), want: map[MediaID]bool{"media-000": true, "media-500": true}, wantQueries: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			favoriteQueryCount.Store(0)
			got, err := st.MediaFavoritedSet(context.Background(), "profile-1", tt.ids)
			if err != nil {
				t.Fatal(err)
			}
			if got == nil {
				t.Fatal("result map is nil")
			}
			if !maps.Equal(got, tt.want) {
				t.Fatalf("favorites = %v, want %v", got, tt.want)
			}
			if got := favoriteQueryCount.Load(); got != tt.wantQueries {
				t.Fatalf("queries = %d, want %d", got, tt.wantQueries)
			}
		})
	}
}

func mediaIDs(count int) []MediaID {
	ids := make([]MediaID, count)
	for i := range ids {
		ids[i] = MediaID(fmt.Sprintf("media-%03d", i))
	}
	return ids
}
