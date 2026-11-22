package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// TestMediaFileProvisional exercises the ADR-0052 §2.2-2.3 provisional-flag
// storage reservation: an imported (deliberately-present) file defaults to
// archived, the flag round-trips, and a Media with no file reports archived
// (false) rather than erroring.
func TestMediaFileProvisional(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)

	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:           "library-local",
		Path:                "/tmp/episode-1.mp3",
		Title:               "Episode 1",
		Container:           "mp3",
		Codec:               "aac",
		ContentFingerprint:  "sha256:episode-1",
		DurationSecondsHint: 1800,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	// Default: a deliberately-imported file is archived, not provisional.
	if prov, err := st.MediaFileProvisional(ctx, media.ID); err != nil || prov {
		t.Fatalf("default provisional must be false: got %v err %v", prov, err)
	}

	if err := st.SetMediaFileProvisional(ctx, media.ID, true); err != nil {
		t.Fatalf("SetMediaFileProvisional(true): %v", err)
	}
	if prov, err := st.MediaFileProvisional(ctx, media.ID); err != nil || !prov {
		t.Fatalf("want provisional true: got %v err %v", prov, err)
	}

	if err := st.SetMediaFileProvisional(ctx, media.ID, false); err != nil {
		t.Fatalf("SetMediaFileProvisional(false): %v", err)
	}
	if prov, _ := st.MediaFileProvisional(ctx, media.ID); prov {
		t.Fatal("want provisional false after clear")
	}

	// A Media with no file reports archived (false), not an error.
	if prov, err := st.MediaFileProvisional(ctx, "media-does-not-exist"); err != nil || prov {
		t.Fatalf("fileless media must report false, no error: got %v err %v", prov, err)
	}
}

// importedMedia imports a Media+file into the named Library and returns its ID.
func importedMedia(t *testing.T, st *Store, library LibraryID, slug string) MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          library,
		Path:               "/media/" + slug + ".mp3",
		Title:              slug,
		Container:          "mp3",
		Codec:              "aac",
		ContentFingerprint: "sha256:" + slug,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s): %v", slug, err)
	}
	return media.ID
}

func sourceItemIDSet(items []SourceItem) map[MediaID]bool {
	ids := map[MediaID]bool{}
	for _, item := range items {
		ids[item.ID] = true
	}
	return ids
}

// TestAvailabilitySourcePredicate proves the "availability" Source predicate
// partitions visible Media by media_file provenance into the
// archived / streamed-provisional / not-downloaded bands, and reveal-filters
// restricted Media out of every band for an unrevealed scope.
func TestAvailabilitySourcePredicate(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	// archived: an imported (kept) file in the open Library.
	archived := importedMedia(t, st, "library-local", "archived-ep")

	// streamed-provisional: an imported file flipped to provisional.
	provisional := importedMedia(t, st, "library-local", "provisional-ep")
	if err := st.SetMediaFileProvisional(ctx, provisional, true); err != nil {
		t.Fatalf("SetMediaFileProvisional: %v", err)
	}

	// not-downloaded: the seeded fileless open Media (media-alpha has no
	// media_file row under the development seed).
	const notDownloaded MediaID = "media-alpha"

	// A restricted, archived file in the restricted Library — the reveal probe.
	restricted := importedMedia(t, st, "library-archive", "restricted-archived-ep")

	archivedItems := mustSourceItems(t, st, filter, SourceQuery{Kind: "availability", Value: "archived"})
	gotArchived := sourceItemIDSet(archivedItems)
	if !gotArchived[archived] || !gotArchived[restricted] {
		t.Fatalf("archived band = %#v, want it to include %s and %s", archivedItems, archived, restricted)
	}
	if gotArchived[provisional] || gotArchived[notDownloaded] {
		t.Fatalf("archived band leaked a non-archived Media: %#v", archivedItems)
	}

	provItems := mustSourceItems(t, st, filter, SourceQuery{Kind: "availability", Value: "streamed-provisional"})
	gotProv := sourceItemIDSet(provItems)
	if !gotProv[provisional] {
		t.Fatalf("streamed-provisional band = %#v, want it to include %s", provItems, provisional)
	}
	if gotProv[archived] || gotProv[notDownloaded] || gotProv[restricted] {
		t.Fatalf("streamed-provisional band leaked a non-provisional Media: %#v", provItems)
	}

	ndItems := mustSourceItems(t, st, filter, SourceQuery{Kind: "availability", Value: "not-downloaded"})
	gotND := sourceItemIDSet(ndItems)
	if !gotND[notDownloaded] {
		t.Fatalf("not-downloaded band = %#v, want it to include %s", ndItems, notDownloaded)
	}
	if gotND[archived] || gotND[provisional] {
		t.Fatalf("not-downloaded band leaked a Media with a file: %#v", ndItems)
	}

	// An unrecognized value yields an empty feed, never an error.
	if other := mustSourceItems(t, st, filter, SourceQuery{Kind: "availability", Value: "downloading"}); len(other) != 0 {
		t.Fatalf("availability value=downloading = %#v, want empty", other)
	}

	// Reveal filtering: the restricted archived Media never surfaces in ANY band
	// to an unrevealed scope.
	for _, value := range []string{"archived", "streamed-provisional", "not-downloaded"} {
		hidden := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "availability", Value: value})
		if sourceItemIDSet(hidden)[restricted] {
			t.Fatalf("unrevealed availability band %q names restricted media: %#v", value, hidden)
		}
	}
}

func TestProvisionalPromotionPreservesQueueResumeAndAvailability(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}

	mediaID, err := st.CreateProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local",
		GUID:      "guid-preserve-refs",
		Title:     "Preserve Refs",
		TeePath:   "/tmp/preserve-refs.partial",
		Class:     MediaClassVideo,
	})
	if err != nil {
		t.Fatalf("CreateProvisionalMedia: %v", err)
	}
	if err := queueAddFixture(ctx, st, "profile-default", mediaID, nil, filter); err != nil {
		t.Fatalf("QueueAdd provisional: %v", err)
	}
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         mediaID,
		PositionSeconds: 45,
		DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatalf("UpsertPlaybackProgress provisional: %v", err)
	}

	if err := st.PromoteProvisionalMedia(ctx, mediaID, "sha256:preserve-refs", "/tmp/preserve-refs.mp4"); err != nil {
		t.Fatalf("PromoteProvisionalMedia: %v", err)
	}

	queue := mustSourceItems(t, st, filter, SourceQuery{Kind: "queue", ProfileID: "profile-default"})
	if len(queue) != 1 || queue[0].ID != mediaID {
		t.Fatalf("queue after promotion = %#v, want preserved MediaID %s", queue, mediaID)
	}
	resume := mustSourceItems(t, st, filter, SourceQuery{Kind: "resume-progress", Value: "in-progress", ProfileID: "profile-default"})
	if len(resume) != 1 || resume[0].ID != mediaID {
		t.Fatalf("resume-progress after promotion = %#v, want preserved MediaID %s", resume, mediaID)
	}
	archived := sourceItemIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "availability", Value: "archived"}))
	streamed := sourceItemIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "availability", Value: "streamed-provisional"}))
	if !archived[mediaID] || streamed[mediaID] {
		t.Fatalf("availability after promotion archived=%v streamed=%v, want archived only for %s", archived[mediaID], streamed[mediaID], mediaID)
	}
}

func TestPromotionCompensationIsExactAndRestoresProvisionalGeneration(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	teePath := "/tmp/compensated.partial"
	finalPath := "/tmp/compensated.mp4"
	fingerprint := "sha256:compensated"
	mediaID, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: "guid-compensated", Title: "Compensated",
		TeePath: teePath, Class: MediaClassVideo,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, mediaID)
	updated, err := st.PromoteProvisionalMediaGeneration(ctx, mediaID, teePath, fingerprint, "", finalPath)
	if err != nil || !updated {
		t.Fatalf("promotion updated=%v err=%v", updated, err)
	}
	compensated, err := st.CompensateProvisionalMediaPromotion(ctx, mediaID, fingerprint, finalPath+".stale", teePath)
	if err != nil || compensated {
		t.Fatalf("stale compensation updated=%v err=%v", compensated, err)
	}
	compensated, err = st.CompensateProvisionalMediaPromotion(ctx, mediaID, fingerprint, finalPath, teePath)
	if err != nil || !compensated {
		t.Fatalf("exact compensation updated=%v err=%v", compensated, err)
	}
	file, err := st.PrimaryMediaFile(ctx, mediaID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if file.Path != teePath || file.ContentFingerprint != provisionalFingerprint(mediaID) {
		t.Fatalf("compensated file = %#v", file)
	}
	if provisional, err := st.MediaFileProvisional(ctx, mediaID); err != nil || !provisional {
		t.Fatalf("provisional=%v err=%v", provisional, err)
	}
	if got := countMediaEvents(mediaOutboxEvents(t, st), MediaEventUpdated, mediaID); got != before+2 {
		t.Fatalf("media.updated count = %d, want %d for promotion and compensation", got, before+2)
	}
}

func TestDuplicatePromotionChecksArchiveOwnershipAfterCancellation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		owned       bool
		lookupFails bool
	}{
		{name: "unowned"},
		{name: "owned", owned: true},
		{name: "lookup error", lookupFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openSeeded(t)
			ctx := t.Context()
			dir := t.TempDir()
			finalPath := filepath.Join(dir, "archive.mp4")
			teePath := finalPath + ".partial"
			duplicatePath := filepath.Join(dir, "existing.mp4")
			if tc.owned {
				duplicatePath = finalPath
			}
			const sample = "sample1:duplicate"
			if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
				LibraryID: "library-local", Path: duplicatePath, Title: "Existing", Container: "mp4", Codec: "h264", ContentFingerprint: sample,
			}); err != nil {
				t.Fatal(err)
			}
			mediaID, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
				LibraryID: "library-local", GUID: "duplicate-promotion", Title: "Provisional",
				TeePath: teePath, Class: MediaClassVideo,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.lookupFails {
				reader := st.catalogR
				st.catalogR, err = sql.Open("sqlite", ":memory:")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = st.catalogR.Close(); st.catalogR = reader })
			}
			promotionCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if err := st.catalogW.Close(); err != nil {
				t.Fatal(err)
			}
			st.catalogW = sql.OpenDB(cancelAfterQueryConnector{path: st.catalogPath, cancel: cancel})
			updated, err := st.PromoteProvisionalMediaGeneration(promotionCtx, mediaID, teePath, sample, "sha256:duplicate", finalPath)
			if updated || promotionCtx.Err() == nil {
				t.Fatalf("promotion updated = %t, cancellation = %v, err = %v", updated, promotionCtx.Err(), err)
			}
			var duplicate *ProvisionalPromotionDuplicateError
			if tc.lookupFails {
				if err == nil || errors.As(err, &duplicate) {
					t.Fatalf("ownership failure = %v, want an error that triggers rollback", err)
				}
			} else if !errors.As(err, &duplicate) || duplicate.KeepArchive != tc.owned {
				t.Fatalf("ownership decision = %+v, err = %v, want KeepArchive = %t", duplicate, err, tc.owned)
			}
		})
	}
}

type cancelAfterQueryConnector struct {
	path   string
	cancel context.CancelFunc
}

func (c cancelAfterQueryConnector) Driver() driver.Driver { return &sqlite.Driver{} }

func (c cancelAfterQueryConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(sqliteDSN(c.path, nil, "immediate"))
	if err != nil {
		return nil, err
	}
	return cancelAfterQueryConn{Conn: conn, cancel: c.cancel}, nil
}

type cancelAfterQueryConn struct {
	driver.Conn
	cancel context.CancelFunc
}

func (c cancelAfterQueryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return cancelAfterQueryRows{Rows: rows, cancel: c.cancel}, nil
}

type cancelAfterQueryRows struct {
	driver.Rows
	cancel context.CancelFunc
}

func (r cancelAfterQueryRows) Close() error {
	err := r.Rows.Close()
	r.cancel()
	return err
}

func TestRegistrationCompensationIsExact(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	previousPath := "/tmp/registration-previous.partial"
	registeredPath := "/tmp/registration-new.partial"
	mediaID, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: "guid-registration-compensated", Title: "Compensated",
		TeePath: previousPath, Class: MediaClassVideo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, gotPrevious, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: "guid-registration-compensated", Title: "Compensated",
		TeePath: registeredPath, Class: MediaClassVideo,
	}); err != nil || gotPrevious != previousPath {
		t.Fatalf("refresh previousPath=%q err=%v", gotPrevious, err)
	}
	compensated, err := st.CompensateProvisionalMediaRegistration(ctx, mediaID, registeredPath+".stale", previousPath)
	if err != nil || compensated {
		t.Fatalf("stale compensation updated=%v err=%v", compensated, err)
	}
	compensated, err = st.CompensateProvisionalMediaRegistration(ctx, mediaID, registeredPath, previousPath)
	if err != nil || !compensated {
		t.Fatalf("exact compensation updated=%v err=%v", compensated, err)
	}
	file, err := st.PrimaryMediaFile(ctx, mediaID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || file.Path != previousPath {
		t.Fatalf("compensated path=%q err=%v, want %q", file.Path, err, previousPath)
	}

	createdPath := "/tmp/registration-created.partial"
	createdID, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: "guid-registration-created", Title: "Created",
		TeePath: createdPath, Class: MediaClassVideo,
	})
	if err != nil {
		t.Fatal(err)
	}
	compensated, err = st.CompensateProvisionalMediaRegistration(ctx, createdID, createdPath, "")
	if err != nil || !compensated {
		t.Fatalf("new-row compensation updated=%v err=%v", compensated, err)
	}
	if _, gotPrevious, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: "guid-registration-created", Title: "Created",
		TeePath: createdPath, Class: MediaClassVideo,
	}); err != nil || gotPrevious != "" {
		t.Fatalf("registration after compensation previousPath=%q err=%v", gotPrevious, err)
	}
}

func TestStreamedProvisionalRetentionRoutesThroughTrash(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	mediaID, err := st.CreateProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local",
		GUID:      "guid-reapable",
		Title:     "Reapable Stream",
		TeePath:   "/tmp/reapable.partial",
		Class:     MediaClassVideo,
	})
	if err != nil {
		t.Fatalf("CreateProvisionalMedia: %v", err)
	}
	if _, err := st.CreateRetentionRule(ctx, RetentionRule{
		Name:                "Streamed provisional",
		SourceExpr:          `{"kind":"availability","value":"streamed-provisional"}`,
		TargetLibraryID:     "library-local",
		GraceDays:           1,
		RevealedAtAuthoring: true,
	}, now); err != nil {
		t.Fatalf("CreateRetentionRule: %v", err)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now); err != nil || skipped || trashed != 0 {
		t.Fatalf("initial ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(25*time.Hour)); err != nil || skipped || trashed != 1 {
		t.Fatalf("due ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}

	entries, err := st.TrashedMediaList(ctx, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true})
	if err != nil {
		t.Fatalf("TrashedMediaList: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != mediaID {
		t.Fatalf("trash entries = %#v, want provisional media %s", entries, mediaID)
	}
}

// seedFilelessEntryMedia writes a catalogued, file-less Media keyed by the
// provisional GUID External ID, optionally bound by a subscription entry.
func seedFilelessEntryMedia(t *testing.T, st *Store, guid string, bound bool) MediaID {
	t.Helper()
	ctx := context.Background()
	mediaID := ProvisionalMediaID("library-local", guid)
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class, mutated_by)
		VALUES (?, 'library-local', 'Catalogued Title', 1800, 'mp3', 'mp3', 'audio', 'scan')
	`, mediaID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `INSERT INTO media_external_id(media_id, scheme, value) VALUES (?, ?, ?)`, mediaID, ProvisionalExternalIDScheme, guid); err != nil {
		t.Fatal(err)
	}
	if bound {
		sub, err := st.AddSubscription(ctx, Subscription{LibraryID: "library-local", FeedURL: "https://feeds.example.test/catalogue.xml", Root: t.TempDir()}, time.Unix(100, 0))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.catalogW.ExecContext(ctx, `
			INSERT INTO subscription_entry(subscription_id, guid, media_id, first_seen_at_ms, enclosure_type)
			VALUES (?, ?, ?, 100, 'audio/mpeg')
		`, sub.ID, guid, mediaID); err != nil {
			t.Fatal(err)
		}
	}
	return mediaID
}

func TestRegisterProvisionalAttachesToFilelessCataloguedMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	guid := "sub-1\x00entry-1"
	mediaID := seedFilelessEntryMedia(t, st, guid, true)
	teePath := "/tmp/catalogued.partial"

	gotID, previous, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: guid, Title: "Registration Title", TeePath: teePath,
		Container: "m4a", Codec: "aac", Class: MediaClassAudio,
	})
	if err != nil || gotID != mediaID || previous != "" {
		t.Fatalf("RegisterProvisionalMedia = %q, %q, %v; want %q", gotID, previous, err, mediaID)
	}
	all := LibraryFilter{AllLibraries: true, Revealed: true}
	media, err := st.Media(ctx, mediaID, all)
	if err != nil || media.Title != "Catalogued Title" || media.Container != "mp3" {
		t.Fatalf("Media = %+v, %v; want catalogue title and container kept", media, err)
	}
	files, err := st.MediaFilesForMedia(ctx, mediaID, all)
	if err != nil || len(files) != 1 || files[0].Path != teePath || !IsProvisionalMediaFile(files[0]) {
		t.Fatalf("files = %+v, %v; want one provisional file at the tee path", files, err)
	}
	// A repeat registration refreshes the same row.
	if again, previous, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: guid, Title: "Registration Title", TeePath: teePath,
	}); err != nil || again != mediaID || previous != teePath {
		t.Fatalf("repeat registration = %q, %q, %v", again, previous, err)
	}

	// Compensation removes only the file; the catalogued Media stays.
	compensated, err := st.CompensateProvisionalMediaRegistration(ctx, mediaID, teePath, "")
	if err != nil || !compensated {
		t.Fatalf("compensation = %v, %v", compensated, err)
	}
	if _, err := st.Media(ctx, mediaID, all); err != nil {
		t.Fatalf("catalogued Media removed by compensation: %v", err)
	}
	if _, err := st.MediaFilesForMedia(ctx, mediaID, all); err == nil {
		t.Fatal("provisional file survived compensation")
	}
	var entries int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM subscription_entry WHERE media_id = ?`, mediaID).Scan(&entries); err != nil || entries != 1 {
		t.Fatalf("subscription entries = %d, %v; want 1", entries, err)
	}
}

func TestRegisterProvisionalRefusesFilelessMediaWithoutMatchingGUID(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	guid := "sub-1\x00entry-2"
	mediaID := seedFilelessEntryMedia(t, st, guid, false)
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE media_external_id SET value = 'other' WHERE media_id = ?`, mediaID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: guid, Title: "T", TeePath: "/tmp/x.partial",
	}); !errors.Is(err, ErrProvisionalRegistrationConflict) {
		t.Fatalf("err = %v, want ErrProvisionalRegistrationConflict", err)
	}
}

func TestRegisterProvisionalCompensationRemovesUnboundAttachedRow(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	guid := "sub-1\x00entry-3"
	mediaID := seedFilelessEntryMedia(t, st, guid, false)
	if _, _, err := st.RegisterProvisionalMedia(ctx, ProvisionalMedia{
		LibraryID: "library-local", GUID: guid, Title: "T", TeePath: "/tmp/y.partial",
	}); err != nil {
		t.Fatal(err)
	}
	// A stale path compensates nothing.
	if compensated, err := st.CompensateProvisionalMediaRegistration(ctx, mediaID, "/tmp/other.partial", ""); err != nil || compensated {
		t.Fatalf("stale compensation = %v, %v", compensated, err)
	}
	if compensated, err := st.CompensateProvisionalMediaRegistration(ctx, mediaID, "/tmp/y.partial", ""); err != nil || !compensated {
		t.Fatalf("compensation = %v, %v", compensated, err)
	}
	if _, err := st.Media(ctx, mediaID, LibraryFilter{AllLibraries: true, Revealed: true}); err == nil {
		t.Fatal("unbound Media survived compensation of a new registration")
	}
}
