package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"maps"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"modernc.org/sqlite"
)

const progressCountingDriverName = "beamfall_progress_counting"

var (
	progressCountingDriverOnce sync.Once
	progressQueryCount         atomic.Int64
)

type progressCountingDriver struct{ inner driver.Driver }

func (d *progressCountingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &progressCountingConn{Conn: conn}, nil
}

type progressCountingConn struct{ driver.Conn }

func (c *progressCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	progressQueryCount.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

// TestProgressByMediaBatchesPerProfile is CORE-0353: a Source page's playback
// facts are one Profile-scoped query per 500 IDs, IDs without a row are
// absent, and a second Profile's row on the same Media never leaves the store.
func TestProgressByMediaBatchesPerProfile(t *testing.T) {
	progressCountingDriverOnce.Do(func() {
		sql.Register(progressCountingDriverName, &progressCountingDriver{inner: &sqlite.Driver{}})
	})
	db, err := sql.Open(progressCountingDriverName, filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE playback_progress (
		profile_id TEXT NOT NULL,
		media_id TEXT NOT NULL,
		position_seconds INTEGER NOT NULL,
		duration_seconds INTEGER NOT NULL,
		watched INTEGER NOT NULL,
		channel_context INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (profile_id, media_id)
	)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		profile, media           string
		position, duration, seen int
	}{
		{"profile-1", "media-000", 42, 120, 0},
		{"profile-1", "media-500", 0, 300, 1},
		{"profile-2", "media-000", 99, 120, 0},
		{"profile-2", "media-001", 10, 60, 0},
	} {
		if _, err := db.Exec(`INSERT INTO playback_progress (profile_id, media_id, position_seconds, duration_seconds, watched) VALUES (?, ?, ?, ?, ?)`,
			row.profile, row.media, row.position, row.duration, row.seen); err != nil {
			t.Fatal(err)
		}
	}
	st := &Store{eventsR: db}

	resumable := SourceEngagement{PositionSeconds: 42, DurationSeconds: 120}
	finished := SourceEngagement{Completed: true}
	tests := []struct {
		name        string
		ids         []MediaID
		want        map[MediaID]SourceEngagement
		wantQueries int64
	}{
		{name: "empty", want: map[MediaID]SourceEngagement{}},
		{name: "page of 200", ids: mediaIDs(200), want: map[MediaID]SourceEngagement{"media-000": resumable}, wantQueries: 1},
		{name: "500 IDs", ids: mediaIDs(500), want: map[MediaID]SourceEngagement{"media-000": resumable}, wantQueries: 1},
		{name: "501 IDs", ids: mediaIDs(501), want: map[MediaID]SourceEngagement{"media-000": resumable, "media-500": finished}, wantQueries: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			progressQueryCount.Store(0)
			got, err := st.progressByMediaLocked(context.Background(), "profile-1", tt.ids)
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(got, tt.want) {
				t.Fatalf("progress = %#v, want %#v", got, tt.want)
			}
			if got := progressQueryCount.Load(); got != tt.wantQueries {
				t.Fatalf("queries = %d, want %d", got, tt.wantQueries)
			}
		})
	}
}

// TestAnnotateEngagementHidesAnotherProfilesProgress is CORE-0353: the demo
// default Profile's progress never reaches a different acting Profile, and a
// container shows every visible child as unfinished for it.
func TestAnnotateEngagementHidesAnotherProfilesProgress(t *testing.T) {
	st := openDemoSeeded(t)
	items := engagementFixture()
	if err := st.AnnotateEngagement(context.Background(), "profile-demo-other", LibraryFilter{AllLibraries: true}, items); err != nil {
		t.Fatalf("AnnotateEngagement: %v", err)
	}
	for _, item := range items[:3] {
		if item.Engagement != nil {
			t.Fatalf("%s engagement = %+v, want nil for a Profile without progress", item.ID, item.Engagement)
		}
	}
	container := items[3].Engagement
	if container == nil || container.TotalCount != 2 || container.RemainingCount != 2 || container.Completed {
		t.Fatalf("container engagement = %+v, want 2 remaining of 2", container)
	}
}
