package migrate_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/migrate"
	"github.com/beamfall/core/internal/migrate/pathmap"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// runWithRewrites enqueues + runs an import with an operator prefix-rewrite
// ruleset wired in, returning the completed Job so the test reads
// the match-rate report and any collision line off the progress row.
func runWithRewrites(t *testing.T, st *store.Store, source migrate.Source, profileID string, rules *pathmap.Ruleset, now time.Time) store.Job {
	t.Helper()
	registry := jobs.NewRegistry(st, "test-worker")
	registry.Register(migrate.JobKind, migrate.Handler(st, fakeResolver{source: source}, migrate.WithPathRewrites(rules)))

	payload, err := json.Marshal(migrate.Payload{
		Version:    1,
		PluginID:   "filmtv",
		ImporterID: "plex",
		Categories: []string{migrate.CategoryWatchState},
		ProfileID:  profileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueued, err := st.EnqueueJob(context.Background(), store.Job{
		Kind:      migrate.JobKind,
		Class:     store.JobClassBackground,
		DedupeKey: "filmtv:plex:pathmap",
		Payload:   payload,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	done, err := st.Job(context.Background(), enqueued.ID)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	return done
}

// TestPathRewriteRaisesMatchRate proves the apply step of the acceptance
// criterion: a source path under a moved mount that would miss the §2.3 path
// match resolves once the operator's prefix rewrite is applied ahead of it, and
// the watch-state lands on the Beamfall Media at the rewritten path.
func TestPathRewriteRaisesMatchRate(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"
	mediaID := seedMediaInLibrary(t, st, "library-local", "/data/Movies/A.mkv", "fp-a-0001", "Catalog A")

	historical := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			{Path: "/mnt/oldnas/Movies/A.mkv", WatchState: migrate.WatchState{Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical}},
		},
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// Baseline: no rewrite → the moved-mount path misses.
	baseline := runWithRewrites(t, st, source, profileID, nil, now)
	if baseline.Progress.Current != 0 {
		t.Fatalf("baseline match = %d, want 0 (moved-mount path should miss without a rule)", baseline.Progress.Current)
	}

	// Apply the rewrite rule → the same source now resolves.
	rules := pathmap.NewRuleset(pathmap.POSIX, pathmap.Rule{SourcePrefix: "/mnt/oldnas", TargetPrefix: "/data"})
	applied := runWithRewrites(t, st, source, profileID, rules, now.Add(time.Hour))
	if applied.Progress.Current != 1 || applied.Progress.Total != 1 {
		t.Fatalf("applied match-rate = %d of %d, want 1 of 1", applied.Progress.Current, applied.Progress.Total)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, profileID, mediaID); !ok {
		t.Fatal("rewritten path did not apply watch-state to the Beamfall Media")
	}
}

// TestPathRewriteCollisionMergesAdditivelyWithReportLine proves the collision
// half of the acceptance criterion: when N distinct source paths rewrite onto
// one Beamfall path, their state merges additively under §2.4 AND the union is
// named on exactly one explicit report line — never a silent merge.
func TestPathRewriteCollisionMergesAdditivelyWithReportLine(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const profileID = "profile-default"
	mediaID := seedMediaInLibrary(t, st, "library-local", "/data/Movies/A.mkv", "fp-a-0001", "A")

	historical := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	// Two source mounts, two records for the same underlying file — one partial,
	// one completed — both rewrite onto /data/Movies/A.mkv.
	source := fakeSource{
		categories: []string{migrate.CategoryWatchState},
		items: []migrate.ForeignItem{
			{Path: "/mnt/nasA/Movies/A.mkv", WatchState: migrate.WatchState{Present: true, PositionSeconds: 100, DurationSeconds: 600, OccurredAt: historical}},
			{Path: "/mnt/nasB/Movies/A.mkv", WatchState: migrate.WatchState{Present: true, Watched: true, DurationSeconds: 600, OccurredAt: historical}},
		},
	}
	rules := pathmap.NewRuleset(pathmap.POSIX,
		pathmap.Rule{SourcePrefix: "/mnt/nasA", TargetPrefix: "/data"},
		pathmap.Rule{SourcePrefix: "/mnt/nasB", TargetPrefix: "/data"},
	)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	done := runWithRewrites(t, st, source, profileID, rules, now)

	// Both records matched (counted), and the additive union left the Media
	// watched (the completed record advanced past the partial one, §2.4).
	if done.Progress.Current != 2 {
		t.Fatalf("matched = %d, want 2 (both colliding records counted)", done.Progress.Current)
	}
	progress, ok, _ := st.PlaybackProgressFor(ctx, profileID, mediaID)
	if !ok || !progress.Watched {
		t.Fatalf("playback = %+v ok=%v; want Watched after additive merge of the two records", progress, ok)
	}

	// Exactly one explicit collision line names both source paths — never silent.
	msg := done.Progress.Message
	wantLine := "2 source paths mapped to /data/Movies/A.mkv — merged additively"
	if !strings.Contains(msg, wantLine) {
		t.Fatalf("message = %q, want collision line %q", msg, wantLine)
	}
	if got := strings.Count(msg, "merged additively"); got != 1 {
		t.Fatalf("collision line appeared %d times in %q; want exactly one", got, msg)
	}
}
