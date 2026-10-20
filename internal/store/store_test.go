package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openSeeded(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mustLibraries(t *testing.T, st *Store, filter LibraryFilter) []Library {
	t.Helper()
	libraries, err := st.Libraries(context.Background(), filter)
	if err != nil {
		t.Fatalf("Libraries returned error: %v", err)
	}
	return libraries
}

func mustSourceItems(t *testing.T, st *Store, filter LibraryFilter, query SourceQuery) []SourceItem {
	t.Helper()
	items, err := st.SourceItems(context.Background(), filter, query)
	if err != nil {
		t.Fatalf("SourceItems returned error: %v", err)
	}
	return items
}

func TestLibrariesHideRestrictedByDefault(t *testing.T) {
	store := openSeeded(t)

	libraries := mustLibraries(t, store, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
	})

	if got, want := libraryIDs(libraries), []LibraryID{"library-local"}; !equalIDs(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
}

func TestLibrariesIncludeRestrictedWhenRevealed(t *testing.T) {
	store := openSeeded(t)

	libraries := mustLibraries(t, store, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
		Revealed:   true,
	})

	if got, want := libraryIDs(libraries), []LibraryID{"library-local", "library-archive"}; !equalIDs(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
}

func TestLibrariesHonorProfileAccess(t *testing.T) {
	store := openSeeded(t)

	libraries := mustLibraries(t, store, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local"},
		Revealed:   true,
	})

	if got, want := libraryIDs(libraries), []LibraryID{"library-local"}; !equalIDs(got, want) {
		t.Fatalf("libraries = %v, want %v", got, want)
	}
}

func TestMediaRequiresVisibleLibrary(t *testing.T) {
	store := openSeeded(t)

	if _, err := store.Media(context.Background(), "media-archive", LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
	}); err == nil {
		t.Fatal("Media returned restricted row without reveal")
	}

	media, err := store.Media(context.Background(), "media-archive", LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
		Revealed:   true,
	})
	if err != nil {
		t.Fatalf("Media returned error: %v", err)
	}
	if media.ID != "media-archive" {
		t.Fatalf("Media ID = %q, want media-archive", media.ID)
	}
}

func TestPrimaryMediaFileRequiresVisibleMedia(t *testing.T) {
	store := openSeeded(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherPath := filepath.Join(t.TempDir(), "other.bin")
	if err := os.WriteFile(otherPath, []byte("other beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPrimaryMediaFile(context.Background(), "media-alpha", path); err != nil {
		t.Fatalf("SetPrimaryMediaFile returned error: %v", err)
	}
	if err := store.SetPrimaryMediaFile(context.Background(), "media-archive", otherPath); err != nil {
		t.Fatalf("SetPrimaryMediaFile returned error: %v", err)
	}

	file, err := store.PrimaryMediaFile(context.Background(), "media-alpha", LibraryFilter{
		AllowedIDs: []LibraryID{"library-local"},
	})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.MediaID != "media-alpha" {
		t.Fatalf("MediaID = %q, want media-alpha", file.MediaID)
	}
	if file.Path != path {
		t.Fatalf("Path = %q, want %q", file.Path, path)
	}

	if _, err := store.PrimaryMediaFile(context.Background(), "media-archive", LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
	}); err == nil {
		t.Fatal("PrimaryMediaFile returned restricted media file without reveal")
	}
}

func TestSourceItemsHydratesVisibleMedia(t *testing.T) {
	store := openSeeded(t)

	items := mustSourceItems(t, store, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
	}, SourceQuery{Kind: "recently-added"})

	if got, want := sourceItemIDs(items), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("source item IDs = %v, want %v", got, want)
	}
	if items[0].Kind != "media" {
		t.Fatalf("Kind = %q, want media", items[0].Kind)
	}
	if items[0].Summary != "2 min" {
		t.Fatalf("Summary = %q, want 2 min", items[0].Summary)
	}
}

func TestSourceItemsAllItemsQuerySearchesDescriptionAndTextMetadata(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()

	if _, err := store.catalogW.ExecContext(ctx, `
		UPDATE library SET plugin_id = 'fixture' WHERE id = 'library-local';
		UPDATE media SET title = 'Episode One', summary = 'A discussion of ceramic history'
		WHERE id = 'media-alpha'
	`); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceMediaFieldSearchPolicies(ctx, []MediaFieldSearchPolicy{{
		PluginID: "fixture", Field: "transcript", Type: EntityFieldText, Searchable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "transcript", Type: EntityFieldText, Text: "kiln calibration transcript"},
	}); err != nil {
		t.Fatal(err)
	}

	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	byDescription := mustSourceItems(t, store, filter, SourceQuery{Kind: "all-items", Query: "ceramic"})
	if len(byDescription) != 1 || byDescription[0].ID != "media-alpha" {
		t.Fatalf("description search = %#v, want media-alpha", byDescription)
	}
	byTranscript := mustSourceItems(t, store, filter, SourceQuery{Kind: "all-items", Query: "calibration"})
	if len(byTranscript) != 1 || byTranscript[0].ID != "media-alpha" {
		t.Fatalf("transcript search = %#v, want media-alpha", byTranscript)
	}
}

func TestSourceItemsIncludeRestrictedOnlyWhenRevealed(t *testing.T) {
	store := openSeeded(t)

	items := mustSourceItems(t, store, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
		Revealed:   true,
	}, SourceQuery{Kind: "recently-added"})

	if got, want := sourceItemIDs(items), []MediaID{"media-archive", "media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("source item IDs = %v, want %v", got, want)
	}
}

func TestSourceItemsUnknownSourceReturnsEmptyList(t *testing.T) {
	store := openSeeded(t)

	items := mustSourceItems(t, store, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
		Revealed:   true,
	}, SourceQuery{Kind: "unknown-source"})

	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0", len(items))
	}
}

func TestDirectorySourceReturnsNoCatalogMedia(t *testing.T) {
	store := openSeeded(t)

	items := mustSourceItems(t, store, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
		Revealed:   true,
	}, SourceQuery{Kind: "directory", Value: "trending"})

	if len(items) != 0 {
		t.Fatalf("directory source returned catalog media: %#v", items)
	}
}

// openDemo seeds the richer demo world (library-demo-open holds eight Media;
// library-demo-guarded is restricted) — enough cardinality to tell a real
// shuffle from coincidence, which the two-row development seed cannot.
func openDemo(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), t.TempDir(), Options{SeedProfile: SeedProfileDemo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRandomSourceIsDeterministicPerSeed(t *testing.T) {
	store := openDemo(t)
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-demo-open"}}

	first := sourceItemIDs(mustSourceItems(t, store, filter, SourceQuery{Kind: "random", Seed: "alpha"}))
	if len(first) < 2 {
		t.Fatalf("random source returned %d items, need >= 2 to test ordering", len(first))
	}
	// Same seed → byte-identical order across repeated calls.
	for i := 0; i < 3; i++ {
		again := sourceItemIDs(mustSourceItems(t, store, filter, SourceQuery{Kind: "random", Seed: "alpha"}))
		if !equalMediaIDs(first, again) {
			t.Fatalf("seed \"alpha\" call %d reordered: %v then %v", i, first, again)
		}
	}

	// The shuffle is not the catalog's natural (title) order — otherwise it is
	// not shuffling at all. all-items orders by title; random must diverge.
	natural := sourceItemIDs(mustSourceItems(t, store, filter, SourceQuery{Kind: "all-items"}))
	if equalMediaIDs(first, natural) {
		t.Fatalf("random order equals natural title order %v — not shuffled", natural)
	}
	// Same membership as all-items, just permuted (reveal/extras parity).
	if !sameMediaSet(first, natural) {
		t.Fatalf("random membership %v differs from catalog %v", first, natural)
	}
}

func TestRandomSourceDifferentSeedsDiffer(t *testing.T) {
	store := openDemo(t)
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-demo-open"}}

	alpha := sourceItemIDs(mustSourceItems(t, store, filter, SourceQuery{Kind: "random", Seed: "alpha"}))
	beta := sourceItemIDs(mustSourceItems(t, store, filter, SourceQuery{Kind: "random", Seed: "beta"}))

	if equalMediaIDs(alpha, beta) {
		t.Fatalf("seeds \"alpha\" and \"beta\" produced the same order %v", alpha)
	}
	// Different order, identical membership — a permutation, not a filter.
	if !sameMediaSet(alpha, beta) {
		t.Fatalf("seeds shuffled different sets: %v vs %v", alpha, beta)
	}
}

func TestRandomSourceAppliesRevealFilter(t *testing.T) {
	store := openDemo(t)
	// library-demo-guarded is restricted; its Media must never surface unless
	// the filter is Revealed — the same Reveal contract every Source obeys.
	all := []LibraryID{"library-demo-open", "library-demo-guarded"}

	hidden := mustSourceItems(t, store, LibraryFilter{AllowedIDs: all}, SourceQuery{Kind: "random", Seed: "alpha", Limit: 200})
	for _, restricted := range []MediaID{"media-demo-guarded-1", "media-demo-guarded-2"} {
		if hasStoreSourceItemWithID(hidden, restricted) {
			t.Fatalf("random source surfaced restricted media %q without reveal", restricted)
		}
	}

	revealed := mustSourceItems(t, store, LibraryFilter{AllowedIDs: all, Revealed: true}, SourceQuery{Kind: "random", Seed: "alpha", Limit: 200})
	if !hasStoreSourceItemWithID(revealed, "media-demo-guarded-1") {
		t.Fatalf("revealed random source omitted restricted media-demo-guarded-1")
	}
}

func sameMediaSet(a, b []MediaID) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[MediaID]int, len(a))
	for _, id := range a {
		seen[id]++
	}
	for _, id := range b {
		seen[id]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestMediaIDsAfterKeysetPaginates(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()

	for _, seed := range []struct {
		id string
		fp string
	}{
		{"aaa", "sha256:keyset-aaa"},
		{"mmm", "sha256:keyset-mmm"},
		{"zzz", "sha256:keyset-zzz"},
	} {
		if _, _, err := store.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "library-local",
			Path:               "/media/" + seed.id + ".mp4",
			Title:              seed.id,
			Container:          "mp4",
			Codec:              "h264",
			ContentFingerprint: seed.fp,
		}); err != nil {
			t.Fatalf("ImportMediaFile(%s): %v", seed.id, err)
		}
	}

	var want []MediaID
	rows, err := store.catalogR.QueryContext(ctx, `SELECT id FROM media ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id MediaID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	var got []MediaID
	var after MediaID
	for {
		page, err := store.MediaIDsAfter(ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		after = page[len(page)-1]
	}
	if !equalMediaIDs(got, want) {
		t.Fatalf("paged IDs = %v, want %v", got, want)
	}
}

func TestMediaFileLastSeenIndexMigrates(t *testing.T) {
	store := openSeeded(t)

	var found bool
	rows, err := store.catalogR.QueryContext(context.Background(), `PRAGMA index_list(media_file)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		if name == "media_file_last_seen_live_idx" && partial == 1 {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("media_file_last_seen_live_idx missing from migrated catalog")
	}
}

func TestForEachMediaFileForLibraryStopsOnYieldError(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	if _, _, err := store.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/streamed.mp4",
		Title:              "Streamed",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:streamed",
	}); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("stop")
	seen := 0
	err := store.ForEachMediaFileForLibrary(ctx, "library-local", func(MediaFile) error {
		seen++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ForEachMediaFileForLibrary error = %v, want %v", err, sentinel)
	}
	if seen != 1 {
		t.Fatalf("yield calls = %d, want 1", seen)
	}
}

func TestImportMediaFileUsesContentFingerprintForIdentity(t *testing.T) {
	store := openSeeded(t)

	first, firstFile, err := store.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/media/first.mp4",
		Title:               "First",
		Container:           "mp4",
		Codec:               "h264",
		ContentFingerprint:  "sha256:aaaaaaaa",
		DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile first returned error: %v", err)
	}
	second, secondFile, err := store.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/media/moved.mp4",
		Title:               "Moved",
		Container:           "mp4",
		Codec:               "h264",
		ContentFingerprint:  "sha256:aaaaaaaa",
		DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile second returned error: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("second media ID = %q, want same ID %q", second.ID, first.ID)
	}
	if secondFile.MediaID != firstFile.MediaID {
		t.Fatalf("second file media ID = %q, want %q", secondFile.MediaID, firstFile.MediaID)
	}
	file, err := store.PrimaryMediaFile(context.Background(), first.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.Path != "/media/moved.mp4" {
		t.Fatalf("Path = %q, want moved path", file.Path)
	}
	items := mustSourceItems(t, store, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}, SourceQuery{Kind: "recently-added"})
	if got, want := len(itemIDsByPrefix(items, "media-sha256-")), 1; got != want {
		t.Fatalf("imported media count = %d, want %d", got, want)
	}
}

func TestImportMediaFileKeepsClassOnProbelessRescan(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	classOf := func(id MediaID) string {
		var class string
		if err := st.catalogR.QueryRowContext(ctx, "SELECT media_class FROM media WHERE id = ?", id).Scan(&class); err != nil {
			t.Fatal(err)
		}
		return class
	}

	// An audio file the probe classified.
	audio, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/song.flac", Title: "Song",
		Container: "flac", Codec: "flac", Class: MediaClassAudio,
		ContentFingerprint: "sha256:audio",
	})
	if err != nil {
		t.Fatalf("import audio: %v", err)
	}
	if got := classOf(audio.ID); got != string(MediaClassAudio) {
		t.Fatalf("first import class = %q, want audio", got)
	}

	// A probe-less re-scan (FFmpeg absent) carries no class; it must NOT reset
	// the stored audio class to the new-row video default.
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/song.flac", Title: "Song",
		Container: "flac", Codec: "flac", // Class "" — no probe ran
		ContentFingerprint: "sha256:audio",
	}); err != nil {
		t.Fatalf("probe-less re-scan: %v", err)
	}
	if got := classOf(audio.ID); got != string(MediaClassAudio) {
		t.Fatalf("after probe-less re-scan class = %q, want audio preserved", got)
	}

	// A brand-new probe-less import still seeds the video default.
	video, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/clip.mp4", Title: "Clip",
		Container: "mp4", Codec: "h264", // Class "" — new row seeds video
		ContentFingerprint: "sha256:video",
	})
	if err != nil {
		t.Fatalf("import new probe-less: %v", err)
	}
	if got := classOf(video.ID); got != string(MediaClassVideo) {
		t.Fatalf("new probe-less import class = %q, want video default", got)
	}
}

func TestImportMediaFilePersistsStreamInventory(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	countStreams := func(fp string) int {
		var n int
		if err := st.catalogR.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_file_stream WHERE content_fingerprint = ?", fp).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	countFonts := func(fp string) int {
		var n int
		if err := st.catalogR.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_file_font_attachment WHERE content_fingerprint = ?", fp).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	const fp = "sha256:inventory"
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/Feature.mkv", Title: "Feature",
		Container: "matroska", Codec: "hevc", ContentFingerprint: fp,
		Streams: []MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Language: "eng", Default: true,
				Level: 153, BitDepth: 10, ColorTransfer: "smpte2084", ColorPrimaries: "bt2020", HDR10Plus: true, DoViProfile: 7, DoViBLCompatID: 6},
			{Index: 1, Type: "audio", Codec: "truehd", Language: "eng", Default: true, Channels: 8, ChannelLayout: "7.1"},
			{Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "spa", Forced: true, HearingImpaired: true, VisualImpaired: true, Commentary: true},
			{Index: 3, Type: "attachment", Codec: "ttf"},
		},
		FontAttachments: []MediaFileFontAttachment{{Filename: "Arial.ttf", MimeType: "application/x-truetype-font"}},
	}); err != nil {
		t.Fatalf("import with inventory: %v", err)
	}
	if got := countStreams(fp); got != 4 {
		t.Fatalf("stream rows = %d, want 4", got)
	}
	if got := countFonts(fp); got != 1 {
		t.Fatalf("font rows = %d, want 1", got)
	}

	// The HDR/DV row must round-trip exactly.
	var (
		profile, transfer, primaries, layout            string
		level, bitDepth, hdr10Plus, dvProfile, dvCompat int
		isDefault                                       int
	)
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT profile, level, color_transfer, color_primaries, bit_depth, hdr10_plus, dovi_profile, dovi_bl_compat_id, is_default
		FROM media_file_stream WHERE content_fingerprint = ? AND stream_index = 0`, fp).
		Scan(&profile, &level, &transfer, &primaries, &bitDepth, &hdr10Plus, &dvProfile, &dvCompat, &isDefault); err != nil {
		t.Fatal(err)
	}
	if profile != "Main 10" || level != 153 || transfer != "smpte2084" || primaries != "bt2020" || bitDepth != 10 ||
		hdr10Plus != 1 || dvProfile != 7 || dvCompat != 6 || isDefault != 1 {
		t.Fatalf("video row = profile %q level %d transfer %q primaries %q depth %d hdr10+ %d dv %d/%d default %d",
			profile, level, transfer, primaries, bitDepth, hdr10Plus, dvProfile, dvCompat, isDefault)
	}
	if err := st.catalogR.QueryRowContext(ctx,
		"SELECT channel_layout FROM media_file_stream WHERE content_fingerprint = ? AND stream_index = 1", fp).Scan(&layout); err != nil {
		t.Fatal(err)
	}
	if layout != "7.1" {
		t.Fatalf("audio channel_layout = %q, want 7.1", layout)
	}
	var hearingImpaired, visualImpaired, commentary int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT is_hearing_impaired, is_visual_impaired, is_commentary
		FROM media_file_stream WHERE content_fingerprint = ? AND stream_index = 2`, fp).
		Scan(&hearingImpaired, &visualImpaired, &commentary); err != nil {
		t.Fatal(err)
	}
	if hearingImpaired != 1 || visualImpaired != 1 || commentary != 1 {
		t.Fatalf("subtitle dispositions = %d/%d/%d, want 1/1/1", hearingImpaired, visualImpaired, commentary)
	}

	// A probe-less re-scan (nil slices) must NOT clobber the stored inventory —
	// the same empty-never-clobbers posture as the fingerprint hashes.
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/Feature.mkv", Title: "Feature",
		Container: "matroska", Codec: "hevc", ContentFingerprint: fp,
	}); err != nil {
		t.Fatalf("probe-less re-scan: %v", err)
	}
	if got := countStreams(fp); got != 4 {
		t.Fatalf("after probe-less re-scan stream rows = %d, want 4 preserved", got)
	}

	// A re-probe full-replaces: a file that lost a stream drops its row, no
	// partial backfill.
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/Feature.mkv", Title: "Feature",
		Container: "matroska", Codec: "hevc", ContentFingerprint: fp,
		Streams: []MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc"},
			{Index: 1, Type: "audio", Codec: "aac", ChannelLayout: "stereo"},
		},
		FontAttachments: []MediaFileFontAttachment{},
	}); err != nil {
		t.Fatalf("re-probe: %v", err)
	}
	if got := countStreams(fp); got != 2 {
		t.Fatalf("after re-probe stream rows = %d, want 2 (full replace)", got)
	}
	if got := countFonts(fp); got != 0 {
		t.Fatalf("after re-probe font rows = %d, want 0 (full replace)", got)
	}
}

func TestImportMediaFileCreatesDifferentMediaForDifferentContent(t *testing.T) {
	store := openSeeded(t)

	first, _, err := store.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/first.mp4",
		Title:              "First",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:aaaaaaaa",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile first returned error: %v", err)
	}
	second, _, err := store.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/second.mp4",
		Title:              "Second",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:bbbbbbbb",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile second returned error: %v", err)
	}

	if second.ID == first.ID {
		t.Fatalf("different content reused media ID %q", first.ID)
	}
}

func TestImportMediaFileMarksOldContentMissingWhenPathIsReplaced(t *testing.T) {
	store := openSeeded(t)

	first, _, err := store.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/replaced.mp4",
		Title:              "Old",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:aaaaaaaa",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile first returned error: %v", err)
	}
	second, _, err := store.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/replaced.mp4",
		Title:              "New",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:bbbbbbbb",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile second returned error: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("replaced path reused media ID %q", first.ID)
	}
	if _, err := store.PrimaryMediaFile(context.Background(), first.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}); err == nil {
		t.Fatal("old content still has a primary media file")
	}
	file, err := store.PrimaryMediaFile(context.Background(), second.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("new PrimaryMediaFile returned error: %v", err)
	}
	if file.Path != "/media/replaced.mp4" {
		t.Fatalf("new file path = %q, want replaced path", file.Path)
	}
	items := mustSourceItems(t, store, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}, SourceQuery{Kind: "recently-added"})
	if hasStoreSourceItemWithID(items, first.ID) {
		t.Fatalf("sourceItems contains missing old media %q", first.ID)
	}
	if !hasStoreSourceItemWithID(items, second.ID) {
		t.Fatalf("sourceItems missing new media %q", second.ID)
	}
}

func TestImportMediaFilePersistsContentIdentityAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/first.mp4",
		Title:              "First",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:aaaaaaaa",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile first returned error: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	second, _, err := reopened.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/moved.mp4",
		Title:              "Moved",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:aaaaaaaa",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile second returned error: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second media ID = %q, want %q", second.ID, first.ID)
	}
	file, err := reopened.PrimaryMediaFile(ctx, first.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.Path != "/media/moved.mp4" {
		t.Fatalf("Path = %q, want moved path", file.Path)
	}
}

func TestSetPrimaryMediaFileUpdatesReadableFilePath(t *testing.T) {
	store := openSeeded(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := store.SetPrimaryMediaFile(context.Background(), "media-alpha", path); err != nil {
		t.Fatalf("SetPrimaryMediaFile returned error: %v", err)
	}
	file, err := store.PrimaryMediaFile(context.Background(), "media-alpha", LibraryFilter{
		AllowedIDs: []LibraryID{"library-local"},
	})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.Path != path {
		t.Fatalf("Path = %q, want %q", file.Path, path)
	}
}

func TestRevokeDevice(t *testing.T) {
	store := openSeeded(t)

	if err := store.RevokeDevice(context.Background(), "device-tv"); err != nil {
		t.Fatalf("RevokeDevice returned error: %v", err)
	}
	device, err := store.Device(context.Background(), "device-tv")
	if err != nil {
		t.Fatalf("Device returned error: %v", err)
	}
	if !device.Revoked {
		t.Fatal("device is not revoked")
	}
}

func TestSetSessionProfileClearsAdminWindow(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	until := time.Now().Add(time.Hour).UTC()
	if err := store.SetSessionAdminUntil(ctx, "session-manager", until); err != nil {
		t.Fatalf("SetSessionAdminUntil returned error: %v", err)
	}

	if err := store.SetSessionProfile(ctx, "session-manager", "profile-managed"); err != nil {
		t.Fatalf("SetSessionProfile returned error: %v", err)
	}

	session, err := store.Session(ctx, "session-manager")
	if err != nil {
		t.Fatalf("Session returned error: %v", err)
	}
	if !session.AdminUntil.IsZero() {
		t.Fatalf("AdminUntil survived Profile switch: %v", session.AdminUntil)
	}
	if session.Revealed {
		t.Fatal("Reveal survived Profile switch")
	}
}

func TestSetAdminPasswordClearsAdminWindows(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	if err := store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("SetSessionAdminUntil returned error: %v", err)
	}

	if err := store.SetAdminPassword(ctx, "rotated-password", time.Now().UTC()); err != nil {
		t.Fatalf("SetAdminPassword returned error: %v", err)
	}

	session, err := store.Session(ctx, "session-manager")
	if err != nil {
		t.Fatalf("Session returned error: %v", err)
	}
	if !session.AdminUntil.IsZero() {
		t.Fatalf("AdminUntil survived admin password rotation: %v", session.AdminUntil)
	}
}

func TestEnrollDeviceCreatesSession(t *testing.T) {
	store := openSeeded(t)
	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-new", DeviceID: "device-new", ProfileID: "profile-default"},
	)
	if err != nil {
		t.Fatalf("EnrollDevice returned error: %v", err)
	}

	device, err := store.Device(context.Background(), "device-new")
	if err != nil {
		t.Fatalf("Device returned error: %v", err)
	}
	if device.Role != DeviceRoleViewer {
		t.Fatalf("Role = %q, want viewer", device.Role)
	}
	if device.DistributionClass != DeviceDistributionDirect {
		t.Fatalf("DistributionClass = %q, want direct", device.DistributionClass)
	}
	if device.SecretHash == "test-device-secret" {
		t.Fatal("raw device secret was stored")
	}
	session, err := store.Session(context.Background(), "session-new")
	if err != nil {
		t.Fatalf("Session returned error: %v", err)
	}
	if session.DeviceID != "device-new" {
		t.Fatalf("DeviceID = %q, want device-new", session.DeviceID)
	}
}

func TestEnrollDeviceStoresDistributionClass(t *testing.T) {
	store := openSeeded(t)
	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-app", Name: "iPad", Role: DeviceRoleViewer, DistributionClass: DeviceDistributionAppStore, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-app", DeviceID: "device-app", ProfileID: "profile-default"},
	)
	if err != nil {
		t.Fatalf("EnrollDevice returned error: %v", err)
	}
	device, err := store.Device(context.Background(), "device-app")
	if err != nil {
		t.Fatalf("Device returned error: %v", err)
	}
	if device.DistributionClass != DeviceDistributionAppStore {
		t.Fatalf("DistributionClass = %q, want appStore", device.DistributionClass)
	}
}

func TestVerifyDeviceSecret(t *testing.T) {
	store := openSeeded(t)

	device, err := store.VerifyDeviceSecret(context.Background(), "device-tv", "dev-tv-secret")
	if err != nil {
		t.Fatalf("VerifyDeviceSecret returned error: %v", err)
	}
	if device.ID != "device-tv" {
		t.Fatalf("Device ID = %q, want device-tv", device.ID)
	}
	if _, err := store.VerifyDeviceSecret(context.Background(), "device-tv", "wrong-secret"); err == nil {
		t.Fatal("VerifyDeviceSecret accepted wrong secret")
	}
	if _, err := store.VerifyDeviceSecret(context.Background(), "device-tv", ""); err == nil {
		t.Fatal("VerifyDeviceSecret accepted empty secret")
	}
}

func TestSetDeviceRole(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()

	device, err := store.SetDeviceRole(ctx, "device-tv", DeviceRoleManager)
	if err != nil {
		t.Fatalf("SetDeviceRole returned error: %v", err)
	}
	if device.Role != DeviceRoleManager {
		t.Fatalf("Role = %q, want manager", device.Role)
	}
	if _, err := store.SetDeviceRole(ctx, "device-tv", DeviceRole("owner")); err == nil {
		t.Fatal("SetDeviceRole accepted unknown role")
	}
}

func TestSetDeviceRoleDemotionClearsAdminWindows(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	if err := store.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("SetSessionAdminUntil returned error: %v", err)
	}

	if _, err := store.SetDeviceRole(ctx, "device-mac", DeviceRoleViewer); err != nil {
		t.Fatalf("SetDeviceRole returned error: %v", err)
	}

	session, err := store.Session(ctx, "session-manager")
	if err != nil {
		t.Fatalf("Session returned error: %v", err)
	}
	if !session.AdminUntil.IsZero() {
		t.Fatalf("AdminUntil survived device demotion: %v", session.AdminUntil)
	}
}

func TestEnrollDeviceRejectsDuplicateDevice(t *testing.T) {
	store := openSeeded(t)

	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-tv", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-new", DeviceID: "device-tv", ProfileID: "profile-default"},
	)

	if err == nil {
		t.Fatal("EnrollDevice accepted duplicate device")
	}
}

func TestEnrollDeviceRejectsDuplicateSession(t *testing.T) {
	store := openSeeded(t)

	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-default", DeviceID: "device-new", ProfileID: "profile-default"},
	)

	if err == nil {
		t.Fatal("EnrollDevice accepted duplicate session")
	}
}

func TestEnrollDeviceRejectsMissingSecretHash(t *testing.T) {
	store := openSeeded(t)

	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer},
		Session{ID: "session-new", DeviceID: "device-new", ProfileID: "profile-default"},
	)

	if err == nil {
		t.Fatal("EnrollDevice accepted missing secret hash")
	}
}

func TestEnrollDeviceRejectsSessionDeviceMismatch(t *testing.T) {
	store := openSeeded(t)

	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-new", DeviceID: "device-other", ProfileID: "profile-default"},
	)

	if err == nil {
		t.Fatal("EnrollDevice accepted session device mismatch")
	}
}

func TestEnrollDeviceRejectsUnknownProfile(t *testing.T) {
	store := openSeeded(t)

	err := store.EnrollDevice(
		context.Background(),
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("test-device-secret")},
		Session{ID: "session-new", DeviceID: "device-new", ProfileID: "profile-missing"},
	)

	if err == nil {
		t.Fatal("EnrollDevice accepted unknown profile")
	}
}

func TestClosedStoreReturnsErrClosed(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/late.mp4",
		Title:              "Late",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:cccccccc",
	}); !errors.Is(err, ErrClosed) {
		t.Fatalf("mutation after Close err = %v, want ErrClosed", err)
	}
	if _, err := st.Session(context.Background(), "session-default"); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after Close err = %v, want ErrClosed", err)
	}
	if _, err := st.EnqueueJob(context.Background(), Job{Kind: "scan"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("job mutation after Close err = %v, want ErrClosed", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestTwoStoreInstancesShareStateWithoutReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	// Pairing code minted by one process is redeemable by the other.
	now := time.Unix(100, 0).UTC()
	if err := first.CreatePairingCode(ctx, PairingCode{
		Code:       "CROSS123",
		ProfileID:  "profile-default",
		DeviceRole: DeviceRoleViewer,
		ExpiresAt:  now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := second.RedeemPairingCode(ctx, "CROSS123", now,
		Device{ID: "device-cross", Name: "Cross", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("cross-secret")},
		Session{ID: "session-cross", DeviceID: "device-cross"},
	)
	if err != nil {
		t.Fatalf("second instance could not redeem pairing code: %v", err)
	}
	if pending.ProfileID != "profile-default" {
		t.Fatalf("ProfileID = %q, want profile-default", pending.ProfileID)
	}
	// The first instance must observe the consumption immediately.
	if _, err := first.RedeemPairingCode(ctx, "CROSS123", now,
		Device{ID: "device-again", Name: "Again", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("again-secret")},
		Session{ID: "session-again", DeviceID: "device-again"},
	); !errors.Is(err, ErrPairingCodeInvalid) {
		t.Fatalf("first instance redeemed consumed code, err = %v", err)
	}

	// Media imported via one instance is listed by the other.
	imported, _, err := first.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/cross.mp4",
		Title:              "Cross",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:dddddddd",
	})
	if err != nil {
		t.Fatal(err)
	}
	items := mustSourceItems(t, second, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}, SourceQuery{Kind: "recently-added"})
	if !hasStoreSourceItemWithID(items, imported.ID) {
		t.Fatalf("second instance does not see imported media %q: %#v", imported.ID, items)
	}
}

func libraryIDs(libraries []Library) []LibraryID {
	ids := make([]LibraryID, len(libraries))
	for i, library := range libraries {
		ids[i] = library.ID
	}
	return ids
}

func equalIDs(a, b []LibraryID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sourceItemIDs(items []SourceItem) []MediaID {
	ids := make([]MediaID, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func equalMediaIDs(a, b []MediaID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasStoreSourceItemWithID(items []SourceItem, id MediaID) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func itemIDsByPrefix(items []SourceItem, prefix string) []MediaID {
	var ids []MediaID
	for _, item := range items {
		if strings.HasPrefix(string(item.ID), prefix) {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func TestCreateProfileGrantsByKind(t *testing.T) {
	st := openSeeded(t)
	revealed := LibraryFilter{Revealed: true, AllLibraries: true}

	managed, err := st.CreateProfile(context.Background(), "Kids", ProfileManaged, revealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range managed.Library {
		if id == "library-archive" {
			t.Fatalf("managed profile granted restricted library: %v", managed.Library)
		}
	}
	if len(managed.Library) == 0 {
		t.Fatal("managed profile granted no libraries")
	}

	adult, err := st.CreateProfile(context.Background(), "Guest", ProfileAdult, revealed)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range adult.Library {
		if id == "library-archive" {
			found = true
		}
	}
	if !found {
		t.Fatalf("adult profile created by revealed scope missing restricted library: %v", adult.Library)
	}

	// An unrevealed creator cannot grant what it cannot see.
	hiddenAdult, err := st.CreateProfile(context.Background(), "Guest Two", ProfileAdult, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range hiddenAdult.Library {
		if id == "library-archive" {
			t.Fatalf("unrevealed creator granted restricted library: %v", hiddenAdult.Library)
		}
	}

	if _, err := st.CreateProfile(context.Background(), "Bad", ProfileKind("weird"), revealed); err == nil {
		t.Fatal("accepted unknown profile kind")
	}
}

func TestSourceItemsLibraryScopingAndPagination(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Library scoping: the restricted archive is invisible unrevealed even
	// when asked for directly; the local library scopes to itself.
	items, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "recently-added", LibraryID: "library-archive"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("unrevealed archive-scoped items = %#v, want empty", items)
	}
	items, err = st.SourceItems(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, SourceQuery{Kind: "recently-added", LibraryID: "library-archive"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "media-archive" {
		t.Fatalf("revealed archive-scoped items = %#v, want only media-archive", items)
	}

	// Pagination: offset walks the all-items ordering.
	all, err := st.SourceItems(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, SourceQuery{Kind: "all-items"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Fatalf("seeded all-items = %#v, want at least 2", all)
	}
	paged, err := st.SourceItems(ctx, LibraryFilter{Revealed: true, AllLibraries: true}, SourceQuery{Kind: "all-items", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(paged) != 1 || paged[0].ID != all[1].ID {
		t.Fatalf("offset page = %#v, want %v", paged, all[1].ID)
	}
}

func TestSourceItemsOnThisDayUsesPriorYearCaptureDateAndReveal(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	today := time.Date(2026, time.June, 16, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time {
		return today
	}
	priorYear := time.Date(2025, time.June, 16, 12, 0, 0, 0, time.UTC)
	currentYear := today

	dateField := func(t time.Time) []MediaField {
		return []MediaField{{Name: "capture-date", Type: EntityFieldDate, Date: t}}
	}
	if err := st.UpsertMediaFields(ctx, "media-alpha", dateField(priorYear)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFields(ctx, "media-archive", dateField(priorYear)); err != nil {
		t.Fatal(err)
	}
	current, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/current-year-photo.jpg", Title: "Current Year",
		Container: "jpg", Class: MediaClassImage, ContentFingerprint: HashMediaFilePath("/media/current-year-photo.jpg"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFields(ctx, current.ID, dateField(currentYear)); err != nil {
		t.Fatal(err)
	}

	hidden := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "on-this-day"})
	if got, want := sourceItemIDs(hidden), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("unrevealed on-this-day = %v, want %v", got, want)
	}
	revealed := mustSourceItems(t, st, LibraryFilter{Revealed: true, AllLibraries: true}, SourceQuery{Kind: "on-this-day"})
	if got, want := sourceItemIDs(revealed), []MediaID{"media-alpha", "media-archive"}; !equalMediaIDs(got, want) {
		t.Fatalf("revealed on-this-day = %v, want %v", got, want)
	}
}

func TestSourceItemsOnThisDayUsesServerLocalCalendarDate(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	serverZone := time.FixedZone("server-local", -4*60*60)
	st.now = func() time.Time {
		return time.Date(2026, time.June, 16, 23, 30, 0, 0, serverZone)
	}

	if err := st.UpsertMediaFields(ctx, "media-alpha", []MediaField{{
		Name: "capture-date", Type: EntityFieldDate, Date: time.Date(2025, time.June, 16, 12, 0, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatal(err)
	}
	other, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/utc-tomorrow.mp4", Title: "UTC Tomorrow",
		Container: "mp4", Codec: "h264", ContentFingerprint: HashMediaFilePath("/media/utc-tomorrow.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFields(ctx, other.ID, []MediaField{{
		Name: "capture-date", Type: EntityFieldDate, Date: time.Date(2025, time.June, 17, 12, 0, 0, 0, time.UTC),
	}}); err != nil {
		t.Fatal(err)
	}

	items := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "on-this-day"})
	if got, want := sourceItemIDs(items), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("local-calendar on-this-day = %v, want %v", got, want)
	}
}

// FieldFilters narrow the catalog feeds by equality on plugin-declared Media
// field values, AND-combined, with reveal filtering unchanged: a restricted
// item that matches the filter still hides until reveal.
func TestSourceItemsFieldFiltersEquality(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	phase := func(value string) []MediaField {
		return []MediaField{{Name: "phase", Type: EntityFieldText, Text: value}}
	}
	if err := store.SetMediaFields(ctx, "media-alpha", append(phase("flight"),
		MediaField{Name: "bay", Type: EntityFieldText, Text: "north"})); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMediaFields(ctx, "media-archive", phase("flight")); err != nil {
		t.Fatal(err)
	}
	docked, _, err := store.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/docked.mp4", Title: "Docked",
		Container: "mp4", Codec: "h264", Class: MediaClassVideo,
		ContentFingerprint: HashMediaFilePath("/media/docked.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMediaFields(ctx, docked.ID, phase("dock")); err != nil {
		t.Fatal(err)
	}
	both := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}

	for _, kind := range []string{"all-items", "recently-added"} {
		items := mustSourceItems(t, store, both, SourceQuery{
			Kind: kind, FieldFilters: map[string]any{"phase": "flight"},
		})
		if got, want := sourceItemIDs(items), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
			t.Fatalf("%s phase=flight unrevealed = %v, want %v", kind, got, want)
		}
	}

	revealed := both
	revealed.Revealed = true
	items := mustSourceItems(t, store, revealed, SourceQuery{
		Kind: "all-items", FieldFilters: map[string]any{"phase": "flight"},
	})
	// all-items orders by title, so the archive item sorts first.
	if got, want := sourceItemIDs(items), []MediaID{"media-archive", "media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("revealed phase=flight = %v, want %v", got, want)
	}

	items = mustSourceItems(t, store, revealed, SourceQuery{
		Kind: "all-items", FieldFilters: map[string]any{"phase": "dock"},
	})
	if got, want := sourceItemIDs(items), []MediaID{docked.ID}; !equalMediaIDs(got, want) {
		t.Fatalf("phase=dock = %v, want %v", got, want)
	}

	// Multiple filters AND together.
	items = mustSourceItems(t, store, revealed, SourceQuery{
		Kind: "all-items", FieldFilters: map[string]any{"phase": "flight", "bay": "north"},
	})
	if got, want := sourceItemIDs(items), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
		t.Fatalf("phase=flight AND bay=north = %v, want %v", got, want)
	}

	// No matching value: empty list, not an error.
	items = mustSourceItems(t, store, revealed, SourceQuery{
		Kind: "all-items", FieldFilters: map[string]any{"phase": "orbit"},
	})
	if len(items) != 0 {
		t.Fatalf("phase=orbit = %v, want empty", sourceItemIDs(items))
	}
}

func TestSourceItemsFieldFiltersTypedComparisons(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()
	released := time.Date(2020, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := store.SetMediaFields(ctx, "media-alpha", []MediaField{
		{Name: "phase", Type: EntityFieldText, Text: "flight"},
		{Name: "score", Type: EntityFieldNumber, Number: 8.5},
		{Name: "verified", Type: EntityFieldFlag, Flag: true},
		{Name: "released", Type: EntityFieldDate, Date: released},
	}); err != nil {
		t.Fatal(err)
	}
	docked, _, err := store.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/docked.mp4", Title: "Docked",
		Container: "mp4", Codec: "h264", Class: MediaClassVideo,
		ContentFingerprint: HashMediaFilePath("/media/docked.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMediaFields(ctx, docked.ID, []MediaField{
		{Name: "score", Type: EntityFieldNumber, Number: 4},
		{Name: "verified", Type: EntityFieldFlag, Flag: false},
		{Name: "released", Type: EntityFieldDate, Date: time.Date(2021, 1, 5, 0, 0, 0, 0, time.UTC)},
	}); err != nil {
		t.Fatal(err)
	}

	both := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	cases := []struct {
		name   string
		filter map[string]any
		want   []MediaID
	}{
		{"number equality", map[string]any{"score": 8.5}, []MediaID{"media-alpha"}},
		{"flag equality", map[string]any{"verified": true}, []MediaID{"media-alpha"}},
		{"date equality", map[string]any{"released": released.Format(time.RFC3339)}, []MediaID{"media-alpha"}},
		{"number range", map[string]any{"score": map[string]any{"gte": 4, "lte": 5}}, []MediaID{docked.ID}},
		{"date range", map[string]any{"released": map[string]any{"gte": "2020-01-01T00:00:00Z", "lte": "2020-12-31T23:59:59Z"}}, []MediaID{"media-alpha"}},
	}
	for _, tc := range cases {
		items := mustSourceItems(t, store, both, SourceQuery{Kind: "all-items", FieldFilters: tc.filter})
		if got := sourceItemIDs(items); !equalMediaIDs(got, tc.want) {
			t.Fatalf("%s = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// EntityFilter narrows the catalog feeds to Media related to one named
// entity (declared kind + title). A missing entity yields an empty feed, not
// an error; an entity in a hidden Library behaves exactly like a missing one
// (hidden == missing), and the Media-side reveal funnel still applies.
func TestSourceItemsEntityFilter(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()

	docked, _, err := store.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/media/docked.mp4", Title: "Docked",
		Container: "mp4", Codec: "h264", Class: MediaClassVideo,
		ContentFingerprint: HashMediaFilePath("/media/docked.mp4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Acme made media-alpha; Zenith made the docked item — two same-kind
	// entities prove the name narrows, not just the kind.
	for _, seed := range []struct {
		id, title string
		media     MediaID
	}{
		{"entity-acme", "Acme", "media-alpha"},
		{"entity-zenith", "Zenith", docked.ID},
	} {
		if err := store.UpsertEntity(ctx, Entity{
			ID: EntityID(seed.id), LibraryID: "library-local", Kind: "maker", Title: seed.title,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertEntityRelation(ctx, EntityRelation{
			FromEntityID: EntityID(seed.id), Name: "made", ToMediaID: seed.media,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// A same-named entity in the restricted archive relates to the archive
	// item: invisible unrevealed, indistinguishable from missing.
	if err := store.UpsertEntity(ctx, Entity{
		ID: "entity-acme-archive", LibraryID: "library-archive", Kind: "maker", Title: "Acme",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEntityRelation(ctx, EntityRelation{
		FromEntityID: "entity-acme-archive", Name: "made", ToMediaID: "media-archive",
	}); err != nil {
		t.Fatal(err)
	}

	both := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	acme := &EntityFilter{Kind: "maker", Name: "Acme"}

	// Both catalog feeds narrow to the related Media; the archive relation
	// stays hidden unrevealed.
	for _, kind := range []string{"all-items", "recently-added"} {
		items := mustSourceItems(t, store, both, SourceQuery{Kind: kind, EntityFilter: acme})
		if got, want := sourceItemIDs(items), []MediaID{"media-alpha"}; !equalMediaIDs(got, want) {
			t.Fatalf("%s maker=Acme unrevealed = %v, want %v", kind, got, want)
		}
	}

	// Revealed, the archive entity and its related item both surface.
	revealed := both
	revealed.Revealed = true
	items := mustSourceItems(t, store, revealed, SourceQuery{Kind: "all-items", EntityFilter: acme})
	// all-items orders by title: Archived Item before Local Alpha.
	if got := sourceItemIDs(items); len(got) != 2 || got[0] != "media-archive" || got[1] != "media-alpha" {
		t.Fatalf("revealed maker=Acme = %v, want [media-archive media-alpha]", got)
	}

	// The other name reaches the other item.
	items = mustSourceItems(t, store, both, SourceQuery{
		Kind: "all-items", EntityFilter: &EntityFilter{Kind: "maker", Name: "Zenith"},
	})
	if got, want := sourceItemIDs(items), []MediaID{docked.ID}; !equalMediaIDs(got, want) {
		t.Fatalf("maker=Zenith = %v, want %v", got, want)
	}

	// Missing entity: empty list, never an error.
	items = mustSourceItems(t, store, revealed, SourceQuery{
		Kind: "all-items", EntityFilter: &EntityFilter{Kind: "maker", Name: "Nimbus"},
	})
	if len(items) != 0 {
		t.Fatalf("maker=Nimbus = %v, want empty", sourceItemIDs(items))
	}
}

// MediaLibrary is the unfiltered server-internal media→Library lookup the
// identification entity-hydration write path scopes Entities with.
func TestMediaLibraryResolvesUnfiltered(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	library, err := st.MediaLibrary(ctx, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if library != "library-local" {
		t.Fatalf("library = %q, want library-local", library)
	}
	if _, err := st.MediaLibrary(ctx, "media-ghost"); err == nil {
		t.Fatal("accepted unknown media")
	}
	if _, err := st.MediaLibrary(ctx, ""); err == nil {
		t.Fatal("accepted empty media ID")
	}
}

// MediaClass is the unfiltered server-internal media→class lookup the migration
// import handler uses after the match chain has resolved authority.
func TestMediaClassResolvesUnfiltered(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	class, err := st.MediaClass(ctx, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if class != MediaClassVideo {
		t.Fatalf("class = %q, want video", class)
	}
	restrictedClass, err := st.MediaClass(ctx, "media-archive")
	if err != nil {
		t.Fatal(err)
	}
	if restrictedClass != MediaClassVideo {
		t.Fatalf("restricted class = %q, want video", restrictedClass)
	}
	if _, err := st.MediaClass(ctx, "media-ghost"); err == nil {
		t.Fatal("accepted unknown media")
	}
	if _, err := st.MediaClass(ctx, ""); err == nil {
		t.Fatal("accepted empty media ID")
	}
}

func TestImportMediaFileStoresFingerprintHashes(t *testing.T) {
	store := openSeeded(t)
	ctx := context.Background()

	media, _, err := store.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/media/fingerprinted.mp4",
		Title:               "Fingerprinted",
		Container:           "mp4",
		Codec:               "h264",
		ContentFingerprint:  "sha256:bbbbbbbb",
		DurationSecondsHint: 321,
		OSHash:              "00000000075bcd15",
		PHash:               "f0f0f0f0f0f0f0f0",
	})
	if err != nil {
		t.Fatalf("ImportMediaFile returned error: %v", err)
	}

	file, err := store.PrimaryMediaFile(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.OSHash != "00000000075bcd15" || file.PHash != "f0f0f0f0f0f0f0f0" {
		t.Fatalf("hashes = %q/%q, want stored values", file.OSHash, file.PHash)
	}

	fp, err := store.MediaFingerprints(ctx, media.ID)
	if err != nil {
		t.Fatalf("MediaFingerprints returned error: %v", err)
	}
	if fp.OSHash != "00000000075bcd15" || fp.PHash != "f0f0f0f0f0f0f0f0" || fp.DurationSeconds != 321 {
		t.Fatalf("MediaFingerprints = %+v, want stored values", fp)
	}

	// A re-import without hashes (e.g. a scan on a machine without FFmpeg)
	// must not erase the stored ones.
	if _, _, err := store.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/media/fingerprinted.mp4",
		Title:               "Fingerprinted",
		Container:           "mp4",
		Codec:               "h264",
		ContentFingerprint:  "sha256:bbbbbbbb",
		DurationSecondsHint: 321,
	}); err != nil {
		t.Fatalf("ImportMediaFile re-import returned error: %v", err)
	}
	fp, err = store.MediaFingerprints(ctx, media.ID)
	if err != nil {
		t.Fatalf("MediaFingerprints after re-import returned error: %v", err)
	}
	if fp.OSHash != "00000000075bcd15" || fp.PHash != "f0f0f0f0f0f0f0f0" {
		t.Fatalf("hashes after empty re-import = %q/%q, want preserved", fp.OSHash, fp.PHash)
	}
}

func TestMediaFingerprintsUnknownMedia(t *testing.T) {
	store := openSeeded(t)
	if _, err := store.MediaFingerprints(context.Background(), "media-nope"); err == nil {
		t.Fatal("MediaFingerprints on unknown media returned nil error")
	}
}

func TestLibraryPreviewClipsToggle(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// ADR-0033 §4: the toggle defaults ON for every Library.
	enabled, err := st.LibraryPreviewClips(ctx, "library-local")
	if err != nil {
		t.Fatalf("LibraryPreviewClips returned error: %v", err)
	}
	if !enabled {
		t.Fatal("preview clips default = off, want on")
	}

	if err := st.SetLibraryPreviewClips(ctx, "library-local", false); err != nil {
		t.Fatalf("SetLibraryPreviewClips returned error: %v", err)
	}
	enabled, err = st.LibraryPreviewClips(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("preview clips still on after disabling")
	}

	// The Libraries read surfaces the toggle too.
	libraries, err := st.Libraries(ctx, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, library := range libraries {
		switch library.ID {
		case "library-local":
			if library.PreviewClips {
				t.Fatal("library-local PreviewClips = true after disabling")
			}
		case "library-archive":
			if !library.PreviewClips {
				t.Fatal("library-archive PreviewClips = false, want default on")
			}
		}
	}

	if err := st.SetLibraryPreviewClips(ctx, "library-missing", true); err == nil {
		t.Fatal("SetLibraryPreviewClips accepted an unknown library")
	}
	if _, err := st.LibraryPreviewClips(ctx, "library-missing"); err == nil {
		t.Fatal("LibraryPreviewClips accepted an unknown library")
	}
}
