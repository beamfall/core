package store

// The ADR-0011 §2 schema-freeze benchmark: a synthetic fixture (100k Media,
// 10k Entities, 2M entity relations) and the full Source/entity query suite
// over it. The EAV side-table design's "last schema" claim is only as good as
// this gate.
//
// Three entry points:
//   - go test ./internal/store -bench BenchmarkEntityQueries -run '^$'
//   - BEAMFALL_BENCH=1 go test ./internal/store -run TestEntityQueryLatencyBudget
//     (= make bench) — the CI latency gate.
//   - plain `go test` never builds the fixture (the latency test skips, and
//     benchmarks only run under -bench), so the default gate stays fast.
//
// The fixture is fully deterministic: pure arithmetic over fixed constants,
// no PRNG, no wall-clock values. It is built once per test process and
// removed in TestMain.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	benchLibraryCount       = 5 // index 4 is restricted, so reveal filtering is exercised honestly
	benchMediaCount         = 100_000
	benchEntityCount        = 10_000
	benchRelationsPerMedia  = 18 // entity→media rows: 1.8M
	benchRelationsPerEntity = 20 // entity→entity rows: 200k
	benchProgressCount      = 10_000
	benchProfileID          = ProfileID("profile-bench")
	benchTimeBaseMillis     = int64(1_700_000_000_000) // fixed epoch; no time.Now anywhere
)

var benchEntityKinds = [3]string{"person", "group", "category"}

var benchWords = [16]string{
	"amber", "basalt", "cobalt", "drift", "ember", "fjord", "garnet", "harbor",
	"indigo", "juniper", "krypton", "lumen", "meridian", "nimbus", "onyx", "prism",
}

func benchLibraryID(i int) LibraryID { return LibraryID(fmt.Sprintf("library-bench-%d", i)) }
func benchMediaID(i int) MediaID     { return MediaID(fmt.Sprintf("media-bench-%06d", i)) }
func benchEntityID(i int) EntityID   { return EntityID(fmt.Sprintf("entity-bench-%05d", i)) }

func benchTitle(i int) string {
	return benchWords[i%len(benchWords)] + " " + benchWords[(i/len(benchWords))%len(benchWords)] +
		" " + fmt.Sprintf("%05d", i)
}

var (
	benchOnce sync.Once
	benchDir  string
	benchSt   *Store
	benchErr  error
)

// benchStore builds the fixture once per process and shares it across every
// benchmark and the latency gate. TestMain removes the directory.
func benchStore(tb testing.TB) *Store {
	tb.Helper()
	benchOnce.Do(func() {
		benchDir, benchSt, benchErr = buildBenchFixture(context.Background())
	})
	if benchErr != nil {
		tb.Fatalf("bench fixture: %v", benchErr)
	}
	return benchSt
}

func TestMain(m *testing.M) {
	code := m.Run()
	if benchSt != nil {
		_ = benchSt.Close()
	}
	if benchDir != "" {
		_ = os.RemoveAll(benchDir)
	}
	os.Exit(code)
}

func buildBenchFixture(ctx context.Context) (string, *Store, error) {
	dir, err := os.MkdirTemp("", "beamfall-bench-")
	if err != nil {
		return "", nil, err
	}
	start := time.Now()
	st, err := Open(ctx, dir, Options{})
	if err != nil {
		return dir, nil, err
	}
	if err := insertBenchCatalog(ctx, st); err != nil {
		return dir, st, fmt.Errorf("catalog: %w", err)
	}
	if err := insertBenchEvents(ctx, st); err != nil {
		return dir, st, fmt.Errorf("events: %w", err)
	}
	relations := benchMediaCount*benchRelationsPerMedia + benchEntityCount*benchRelationsPerEntity
	fmt.Fprintf(os.Stderr, "bench fixture: %d media / %d entities / %d relations built in %s\n",
		benchMediaCount, benchEntityCount, relations, time.Since(start).Round(time.Millisecond))
	return dir, st, nil
}

func insertBenchCatalog(ctx context.Context, st *Store) error {
	tx, err := st.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i := 0; i < benchLibraryCount; i++ {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO library(id, title, plugin_id, is_restricted) VALUES (?, ?, NULL, ?)",
			benchLibraryID(i), fmt.Sprintf("Bench Library %d", i), boolInt(i == benchLibraryCount-1),
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO profile(id, name, kind) VALUES (?, 'Bench', 'adult')", benchProfileID,
	); err != nil {
		return err
	}
	for i := 0; i < benchLibraryCount; i++ {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO profile_library(profile_id, library_id, position) VALUES (?, ?, ?)",
			benchProfileID, benchLibraryID(i), i,
		); err != nil {
			return err
		}
	}

	// 100k Media round-robined across the five Libraries (20k restricted).
	err = batchInsert(ctx, tx,
		"INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class) VALUES ",
		7, benchMediaCount, func(i int) []any {
			return []any{benchMediaID(i), benchLibraryID(i % benchLibraryCount), benchTitle(i),
				600 + i%3600, "mkv", "h264", "video"}
		})
	if err != nil {
		return err
	}

	// 10k Entities across three kinds and all five Libraries.
	err = batchInsert(ctx, tx,
		"INSERT INTO entity(id, library_id, kind, title) VALUES ",
		4, benchEntityCount, func(i int) []any {
			return []any{benchEntityID(i), benchLibraryID(i % benchLibraryCount),
				benchEntityKinds[i%len(benchEntityKinds)], benchTitle(i)}
		})
	if err != nil {
		return err
	}

	// One field per typed side table on every Entity (40k rows total).
	type fieldTable struct {
		table, column, field, fieldType string
		value                           func(i int) any
	}
	for _, ft := range []fieldTable{
		{"entity_field_text", "value", "summary", "text", func(i int) any { return "summary " + benchTitle(i) }},
		{"entity_field_real", "value", "score", "number", func(i int) any { return float64(i%100) / 10 }},
		{"entity_field_int", "value", "favorite", "flag", func(i int) any { return i % 2 }},
		{"entity_field_date", "value_ms", "founded", "date", func(i int) any { return benchTimeBaseMillis + int64(i)*86_400_000 }},
	} {
		err = batchInsert(ctx, tx,
			"INSERT INTO "+ft.table+"(entity_id, kind, field, field_type, "+ft.column+") VALUES ",
			5, benchEntityCount, func(i int) []any {
				return []any{benchEntityID(i), benchEntityKinds[i%len(benchEntityKinds)],
					ft.field, ft.fieldType, ft.value(i)}
			})
		if err != nil {
			return err
		}
	}

	// 1.8M entity→media relations: every Media appears-in 18 distinct
	// Entities (5417 is coprime with 10000, so the 18 indexes are distinct);
	// every Entity backs ~180 Media. Every 5th row carries attributes.
	err = batchInsert(ctx, tx,
		"INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, to_media_id, attributes) VALUES ",
		5, benchMediaCount*benchRelationsPerMedia, func(k int) []any {
			i, j := k/benchRelationsPerMedia, k%benchRelationsPerMedia
			var attributes any
			if k%5 == 0 {
				attributes = `{"role":"primary"}`
			}
			return []any{benchEntityID((i + j*5417) % benchEntityCount), "appears-in",
				nil, benchMediaID(i), attributes}
		})
	if err != nil {
		return err
	}

	// 200k entity→entity relations: 20 per Entity (487 coprime with 10000).
	err = batchInsert(ctx, tx,
		"INSERT INTO entity_relation(from_entity_id, relation, to_entity_id, to_media_id, attributes) VALUES ",
		5, benchEntityCount*benchRelationsPerEntity, func(k int) []any {
			e, j := k/benchRelationsPerEntity, k%benchRelationsPerEntity+1
			return []any{benchEntityID(e), "related-to",
				benchEntityID((e + j*487) % benchEntityCount), nil, nil}
		})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// insertBenchEvents seeds the engagement projection: 5k in-progress rows
// (Continue Watching) and 5k watched rows with play counts (recently-played /
// most-played), spread across all five Libraries so the per-candidate reveal
// filtering in those Sources does real work.
func insertBenchEvents(ctx context.Context, st *Store) error {
	tx, err := st.eventsW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = batchInsert(ctx, tx,
		"INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms, play_count, last_played_at_ms) VALUES ",
		8, benchProgressCount, func(i int) []any {
			mediaID := benchMediaID(i*10 + i%benchLibraryCount) // unique, hits every Library
			updated := benchTimeBaseMillis + int64(i)*1000
			if i < benchProgressCount/2 {
				return []any{benchProfileID, mediaID, 60 + i%1800, 3600, 0, updated, 0, nil}
			}
			return []any{benchProfileID, mediaID, 0, 3600, 1, updated, 1 + i%9, updated}
		})
	if err != nil {
		return err
	}
	return tx.Commit()
}

// batchInsert appends total generated rows through multi-row VALUES
// statements, capped well under SQLite's bound-parameter limit.
func batchInsert(ctx context.Context, tx *sql.Tx, prefix string, cols, total int, row func(i int) []any) error {
	// 8000 parameters per statement measured fastest with modernc SQLite:
	// the build took 64s here vs 155s at 30k params (and stays well under
	// the 32766 bound-parameter limit).
	const maxParams = 8000
	rowsPerBatch := maxParams / cols
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", cols), ",") + ")"
	values := func(n int) string {
		return strings.TrimSuffix(strings.Repeat(tuple+",", n), ",")
	}
	fullSQL := prefix + values(rowsPerBatch)
	args := make([]any, 0, rowsPerBatch*cols)
	for start := 0; start < total; start += rowsPerBatch {
		n := min(rowsPerBatch, total-start)
		sql := fullSQL
		if n != rowsPerBatch {
			sql = prefix + values(n)
		}
		args = args[:0]
		for i := start; i < start+n; i++ {
			args = append(args, row(i)...)
		}
		if _, err := tx.ExecContext(ctx, sql, args...); err != nil {
			return fmt.Errorf("batch insert at row %d: %w", start, err)
		}
	}
	return nil
}

// benchFilterCases are the two reveal scopes every query runs under: the same
// five-Library session hidden (restricted Library invisible) and revealed.
func benchFilterCases() []struct {
	name   string
	filter LibraryFilter
} {
	return []struct {
		name   string
		filter LibraryFilter
	}{
		{"hidden", LibraryFilter{AllLibraries: true}},
		{"revealed", LibraryFilter{Revealed: true, AllLibraries: true}},
	}
}

// benchQueries is the full Source/entity query suite the freeze gate covers.
// Fixed targets: media-bench-000040 and entity-bench-00060 both live in the
// unrestricted library-bench-0 (60%5==0) and entity 60 is kind "person"
// (60%3==0); library-bench-0 holds ~667 "person" entities (i%15==0).
func benchQueries() []struct {
	name string
	run  func(ctx context.Context, st *Store, filter LibraryFilter) error
} {
	type q = struct {
		name string
		run  func(ctx context.Context, st *Store, filter LibraryFilter) error
	}
	sourceItems := func(query SourceQuery) func(context.Context, *Store, LibraryFilter) error {
		return func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.SourceItems(ctx, filter, query)
			return err
		}
	}
	return []q{
		{"source/recently-added", sourceItems(SourceQuery{Kind: "recently-added"})},
		{"source/all-items", sourceItems(SourceQuery{Kind: "all-items"})},
		{"source/continue-watching", sourceItems(SourceQuery{Kind: "continue-watching", ProfileID: benchProfileID})},
		{"source/recently-played", sourceItems(SourceQuery{Kind: "recently-played", ProfileID: benchProfileID})},
		{"source/most-played", sourceItems(SourceQuery{Kind: "most-played", ProfileID: benchProfileID})},
		{"source/entity-items", sourceItems(SourceQuery{Kind: "entity-items", EntityKind: "person"})},
		{"source/entity-items-library", sourceItems(SourceQuery{Kind: "entity-items", EntityKind: "person", LibraryID: benchLibraryID(0)})},
		{"entity/by-id", func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.Entity(ctx, benchEntityID(60), filter)
			return err
		}},
		{"entity/by-kind-page", func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.EntitiesByKind(ctx, benchLibraryID(0), "person", filter, EntityKindOptions{}, 50, 0)
			return err
		}},
		{"entity/by-kind-deep-page", func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.EntitiesByKind(ctx, benchLibraryID(0), "person", filter, EntityKindOptions{}, 50, 500)
			return err
		}},
		{"entity/for-media", func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.EntitiesForMedia(ctx, benchMediaID(40), "appears-in", filter)
			return err
		}},
		{"entity/related", func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.RelatedEntities(ctx, benchEntityID(60), "related-to", filter)
			return err
		}},
		{"media/for-entity", func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.MediaForEntity(ctx, benchEntityID(60), filter)
			return err
		}},
		{"search/fts", func(ctx context.Context, st *Store, filter LibraryFilter) error {
			_, err := st.SearchMedia(ctx, filter, "cobalt", 50)
			return err
		}},
	}
}

// benchProbeQueries adds targeted stress cases for the two store paths this
// benchmark session is investigating. The latency gate keeps its original
// coverage; these extra probes are benchmark-only so their budgets can be
// read directly from the benchmark output.
func benchProbeQueries() []struct {
	name string
	run  func(ctx context.Context, st *Store, filter LibraryFilter) error
} {
	queries := benchQueries()
	queries = append(queries,
		struct {
			name string
			run  func(ctx context.Context, st *Store, filter LibraryFilter) error
		}{
			name: "source/all-items-deep-page",
			run: func(ctx context.Context, st *Store, filter LibraryFilter) error {
				_, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "all-items", Limit: 50, Offset: 50_000})
				return err
			},
		},
		struct {
			name string
			run  func(ctx context.Context, st *Store, filter LibraryFilter) error
		}{
			name: "source/continue-watching-page200",
			run: func(ctx context.Context, st *Store, filter LibraryFilter) error {
				_, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "continue-watching", ProfileID: benchProfileID, Limit: 200})
				return err
			},
		},
	)
	return queries
}

// BenchmarkEntityQueries runs the freeze-gate query suite under -bench:
//
//	go test ./internal/store -bench BenchmarkEntityQueries -run '^$'
func BenchmarkEntityQueries(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	for _, query := range benchProbeQueries() {
		for _, scope := range benchFilterCases() {
			b.Run(query.name+"/"+scope.name, func(b *testing.B) {
				for b.Loop() {
					if err := query.run(ctx, st, scope.filter); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// entityDeepPageDepth is how far into a kind's listing the cursor-vs-offset
// probe walks — deep enough that offset paging's O(offset) scan cost shows up
// against cursor paging's O(page) keyset seek. library-bench-0 holds ~667
// "person" entities, so a depth of 600 exercises a near-tail page.
const entityDeepPageDepth = 600

// walkOffsetToDepth reads the page at entityDeepPageDepth via offset paging (a
// single deep OFFSET), returning the page rows.
func walkOffsetToDepth(ctx context.Context, st *Store, filter LibraryFilter, pageSize int) ([]Entity, error) {
	return st.EntitiesByKind(ctx, benchLibraryID(0), "person", filter, EntityKindOptions{}, pageSize, entityDeepPageDepth)
}

// walkCursorToDepth reaches the same depth by walking keyset-cursor pages,
// returning the page that lands at entityDeepPageDepth.
func walkCursorToDepth(ctx context.Context, st *Store, filter LibraryFilter, pageSize int) ([]Entity, error) {
	cursor := ""
	walked := 0
	for {
		page, err := st.EntitiesByKindCursor(ctx, benchLibraryID(0), "person", filter, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		if walked >= entityDeepPageDepth || page.Next == "" {
			return page.Entities, nil
		}
		walked += len(page.Entities)
		cursor = page.Next
	}
}

// BenchmarkEntityCursorVsOffset is the CursorPaginated decision benchmark: it
// measures deep-page latency two ways — a single deep OFFSET vs a keyset-cursor
// walk to the same depth — on the 100k/10k/2M fixture, so the "is cursor
// pagination required for this scale" decision is evidenced by measured numbers
// rather than asserted. Run:
//
//	go test ./internal/store -bench BenchmarkEntityCursorVsOffset -run '^$'
func BenchmarkEntityCursorVsOffset(b *testing.B) {
	st := benchStore(b)
	ctx := context.Background()
	const pageSize = 50
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	b.Run("offset/deep-page", func(b *testing.B) {
		for b.Loop() {
			if _, err := walkOffsetToDepth(ctx, st, filter, pageSize); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("cursor/deep-page", func(b *testing.B) {
		for b.Loop() {
			if _, err := walkCursorToDepth(ctx, st, filter, pageSize); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestEntityCursorVsOffsetDecision records the cursor-vs-offset comparison as
// part of the schema-freeze scale gate (BEAMFALL_BENCH=1): it times a deep
// OFFSET page against a keyset-cursor walk to the same depth and LOGS both P50s
// plus the ratio, then asserts the cursor walk is not dramatically slower than
// the offset path at this scale (a loose ceiling — the log is the decision
// evidence, the assert only catches a gross regression). This is the recorded
// answer to "does this kind need cursor pagination at 100k/10k/2M?".
func TestEntityCursorVsOffsetDecision(t *testing.T) {
	if os.Getenv("BEAMFALL_BENCH") != "1" {
		t.Skip("set BEAMFALL_BENCH=1 (= make bench) to run the cursor-vs-offset scale decision; it builds the 100k/10k/2M fixture")
	}
	st := benchStore(t)
	ctx := context.Background()
	const pageSize, warmups, iterations = 50, 3, 40
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	measure := func(run func(context.Context, *Store, LibraryFilter, int) ([]Entity, error)) time.Duration {
		for i := 0; i < warmups; i++ {
			if _, err := run(ctx, st, filter, pageSize); err != nil {
				t.Fatal(err)
			}
		}
		durations := make([]time.Duration, iterations)
		for i := range durations {
			start := time.Now()
			if _, err := run(ctx, st, filter, pageSize); err != nil {
				t.Fatal(err)
			}
			durations[i] = time.Since(start)
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		return durations[iterations/2]
	}

	offsetP50 := measure(walkOffsetToDepth)
	cursorP50 := measure(walkCursorToDepth)
	ratio := float64(cursorP50) / float64(offsetP50)
	t.Logf("cursor-vs-offset decision @ depth %d, page %d: offset p50=%s cursor p50=%s (cursor/offset=%.2f)",
		entityDeepPageDepth, pageSize, offsetP50, cursorP50, ratio)

	// The cursor walk pays for multiple round trips to reach the same depth, so
	// it may be slower than a single deep OFFSET at THIS scale — the decision is
	// that offset paging is adequate here and cursor paging is the stability
	// tool for far larger kinds. Catch only a gross regression (>25x), not the
	// expected multi-trip overhead.
	if ratio > 25 {
		t.Errorf("cursor walk %.2fx slower than deep offset — unexpectedly bad even accounting for multi-trip overhead", ratio)
	}
}

// TestEntityQueryLatencyBudget is the CI latency gate (make bench): it builds
// the fixture once and asserts a P95 budget per query. Budgets are ~3× the
// P95s measured on the development laptop (Apple Silicon, 2026-06-11,
// modernc SQLite) — with a 1ms floor so sub-100µs queries don't flake on CI
// jitter — so the gate catches regressions, not machine variance. Measured
// P95s (worst of hidden/revealed):
//
//	source/recently-added        ~0.25ms   source/all-items         ~36ms
//	source/continue-watching     ~2.9ms    source/recently-played   ~5.4ms
//	source/most-played           ~5.5ms    source/entity-items      ~2.1ms
//	source/entity-items-library  ~0.60ms   entity/by-id             ~0.08ms
//	entity/by-kind-page          ~1.4ms    entity/by-kind-deep-page ~2.0ms
//	entity/for-media             ~0.45ms   entity/related           ~0.45ms
//	media/for-entity             ~0.70ms   search/fts               ~19ms
func TestEntityQueryLatencyBudget(t *testing.T) {
	if os.Getenv("BEAMFALL_BENCH") != "1" {
		t.Skip("set BEAMFALL_BENCH=1 (= make bench) to run the ADR-0011 §2 latency gate; it builds the 100k/10k/2M fixture")
	}
	budgets := map[string]time.Duration{
		"source/recently-added":       1 * time.Millisecond,
		"source/all-items":            110 * time.Millisecond,
		"source/continue-watching":    9 * time.Millisecond,
		"source/recently-played":      16 * time.Millisecond,
		"source/most-played":          17 * time.Millisecond,
		"source/entity-items":         7 * time.Millisecond,
		"source/entity-items-library": 2 * time.Millisecond,
		"entity/by-id":                1 * time.Millisecond,
		"entity/by-kind-page":         5 * time.Millisecond,
		"entity/by-kind-deep-page":    7 * time.Millisecond,
		"entity/for-media":            1500 * time.Microsecond,
		"entity/related":              1500 * time.Microsecond,
		"media/for-entity":            2500 * time.Microsecond,
		"search/fts":                  60 * time.Millisecond,
	}

	st := benchStore(t)
	ctx := context.Background()
	const warmups, iterations = 3, 40
	for _, query := range benchQueries() {
		budget, ok := budgets[query.name]
		if !ok {
			t.Fatalf("query %s has no latency budget", query.name)
		}
		for _, scope := range benchFilterCases() {
			for i := 0; i < warmups; i++ {
				if err := query.run(ctx, st, scope.filter); err != nil {
					t.Fatalf("%s/%s: %v", query.name, scope.name, err)
				}
			}
			durations := make([]time.Duration, iterations)
			for i := range durations {
				start := time.Now()
				if err := query.run(ctx, st, scope.filter); err != nil {
					t.Fatalf("%s/%s: %v", query.name, scope.name, err)
				}
				durations[i] = time.Since(start)
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			p95 := durations[iterations*95/100-1]
			t.Logf("%s/%s p95=%s budget=%s", query.name, scope.name, p95, budget)
			if p95 > budget {
				t.Errorf("%s/%s p95 %s exceeds budget %s", query.name, scope.name, p95, budget)
			}
		}
	}

	// Sanity: the fixture actually feeds every query shape.
	items, err := st.SourceItems(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, SourceQuery{Kind: "all-items"})
	if err != nil || len(items) != 50 {
		t.Fatalf("all-items sanity: %d items, err %v", len(items), err)
	}
	related, err := st.EntitiesForMedia(ctx, benchMediaID(40), "appears-in", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil || len(related) != benchRelationsPerMedia {
		t.Fatalf("entities-for-media sanity: %d entities, err %v", len(related), err)
	}
}
