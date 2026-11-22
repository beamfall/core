package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestQueueAddManyProjectsEncryptedEntriesAtCommit(t *testing.T) {
	st, queued := seededEncryptedMedia(t, "queue-commit-certification")
	t.Cleanup(func() { _ = st.Close() })
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-revealed"
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'starlog' WHERE id = ?`, queued.LibraryID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, queued.ID, []MediaField{{Name: certificationField, Type: EntityFieldText, Text: "low"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileCertificationCeiling(ctx, profile, ProfileCertificationCeiling{
		PluginID: "starlog", Ceiling: "low", FilterVisibility: "hide",
	}); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	auth := QueueWriteAuthorization{
		ProfileID: profile, SessionID: session, Filter: filter, Now: time.Now, ResolveCertification: testQueueCertification,
	}
	seed, err := st.QueueAddMany(ctx, auth, []MediaID{queued.ID}, QueueEnd)
	if err != nil || len(seed.Entries) != 1 || seed.Entries[0].Item.ID != queued.ID {
		t.Fatalf("seed encrypted queue = %+v, err = %v", seed.Entries, err)
	}
	// A second connection attempts a recertification after the queue's first
	// visibility read. Do not wait on a writer reservation held by this write.
	writer, err := openSQLiteWriter(st.encryptedDBs[queued.LibraryID].workingPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if _, err := writer.ExecContext(ctx, `PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}
	hide := func() error {
		_, err := writer.ExecContext(ctx, `UPDATE media_field_text SET value = 'high' WHERE media_id = ? AND field = ?`, queued.ID, certificationField)
		return err
	}
	var hideErr error
	var clockReads int
	auth.Now = func() time.Time {
		clockReads++
		if clockReads == 2 {
			hideErr = hide()
		}
		return time.Now()
	}
	const added = "media-archive"
	got, err := st.QueueAddMany(ctx, auth, []MediaID{added}, QueueEnd)
	if err != nil || clockReads < 2 {
		t.Fatalf("batch did not reach the racing write: queue = %+v, err = %v, clock reads = %d", got.Entries, err, clockReads)
	}
	var busy *sqlite.Error
	want := []MediaID{added}
	if hideErr != nil {
		if !errors.As(hideErr, &busy) || busy.Code() != sqlite3.SQLITE_BUSY {
			t.Fatalf("recertifying encrypted Media: %v", hideErr)
		}
		want = []MediaID{queued.ID, added}
		if err := hide(); err != nil {
			t.Fatalf("recertifying after commit: %v", err)
		}
	}
	var ids []MediaID
	for _, entry := range got.Entries {
		ids = append(ids, entry.Item.ID)
	}
	if !slices.Equal(ids, want) {
		t.Errorf("batch response = %v, want %v at commit", ids, want)
	}
	filter.Certification = []CertificationConstraint{{PluginID: "starlog", Allowed: []string{"low"}}}
	assertQueueIDs(t, st, filter, profile, []MediaID{added})
	filter.Certification = nil
	assertQueueIDs(t, st, filter, profile, []MediaID{queued.ID, added})
}

func queueAddFixture(ctx context.Context, st *Store, profileID ProfileID, mediaID MediaID, position *int, filter LibraryFilter) error {
	var sessionID SessionID
	if profileID == "profile-default" {
		sessionID = "session-revealed"
	}
	_, err := st.QueueAddForProfile(ctx, profileID, sessionID, "", mediaID, position, filter, time.Now, testQueueCertification)
	return err
}

func TestQueueAddResponseMatchesQueueReadAfterMute(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-default"
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	target := importQueueMedia(t, st, "queue-after-mute")
	for _, id := range []MediaID{"media-alpha", target} {
		if _, err := st.QueueAddForProfile(ctx, profile, session, "", id, nil, filter, time.Now, testQueueCertification); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMute(ctx, profile, MuteKindMedia, "media-alpha", false, true); err != nil {
		t.Fatal(err)
	}
	newTarget := importQueueMedia(t, st, "queue-after-mute-next")
	committed, err := st.QueueAddForProfile(ctx, profile, session, "", newTarget, queueIntPtr(1), filter, time.Now, testQueueCertification)
	if err != nil {
		t.Fatal(err)
	}
	query := SourceQuery{Kind: "queue", ProfileID: profile}
	read := mustSourceItems(t, st, filter, query)
	if len(read) != 2 || read[0].ID != newTarget || read[1].ID != target {
		t.Fatalf("queue read after mute = %#v", read)
	}
	if !slices.EqualFunc(committed.Items, read, func(a, b SourceItem) bool { return a.ID == b.ID }) {
		t.Fatalf("queueAdd response = %#v, want the same order as the queue read", committed.Items)
	}
	if committed.MediaID != newTarget || committed.Position == nil || *committed.Position != 0 {
		t.Fatalf("committed placement = %s at %v, want %s at 0", committed.MediaID, committed.Position, newTarget)
	}
	query.Limit, query.Offset = 1, 1
	if page := mustSourceItems(t, st, filter, query); len(page) != 1 || page[0].ID != target {
		t.Fatalf("second visible page = %#v, want %s", page, target)
	}
	if err := st.SetMute(ctx, profile, MuteKindMedia, "media-alpha", false, false); err != nil {
		t.Fatal(err)
	}
	assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha", newTarget, target})
}

func TestQueueAddCommitsMutedTargetsWithoutReturningThem(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-default"
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	queued := importQueueMedia(t, st, "queue-muted-existing")
	unqueued := importQueueMedia(t, st, "queue-muted-new")
	for _, id := range []MediaID{"media-alpha", queued} {
		if _, err := st.QueueAddForProfile(ctx, profile, session, "", id, nil, filter, time.Now, testQueueCertification); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []MediaID{queued, unqueued} {
		if err := st.SetMute(ctx, profile, MuteKindMedia, string(id), false, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []MediaID{queued, unqueued} {
		committed, err := st.QueueAddForProfile(ctx, profile, session, "", id, queueIntPtr(0), filter, time.Now, testQueueCertification)
		if err != nil {
			t.Fatalf("queue add for muted target %s: %v", id, err)
		}
		if len(committed.Items) != 1 || committed.Items[0].ID != "media-alpha" {
			t.Fatalf("queue add response for muted target %s = %#v, want only media-alpha", id, committed.Items)
		}
		if committed.MediaID != id || committed.Position != nil {
			t.Fatalf("committed placement for muted target %s = %s at %v", id, committed.MediaID, committed.Position)
		}
	}
	assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha"})
	for _, id := range []MediaID{queued, unqueued} {
		if err := st.SetMute(ctx, profile, MuteKindMedia, string(id), false, false); err != nil {
			t.Fatal(err)
		}
	}
	assertQueueIDs(t, st, filter, profile, []MediaID{unqueued, queued, "media-alpha"})
}

func TestQueueAddMatchesQueueReadWithEncryptedCatalogMutes(t *testing.T) {
	for _, kind := range []MuteKind{MuteKindEntity, MuteKindTag} {
		t.Run(string(kind), func(t *testing.T) {
			st, library, _, entity, muted := openEncryptedPackLibrary(t)
			ctx := t.Context()
			const profile = "profile-default"
			const session = "session-revealed"
			filter := LibraryFilter{AllowedIDs: []LibraryID{library, "library-archive"}, Revealed: true}
			other, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:enc-queue-visible", "/enc/queue-visible.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []MediaID{muted, other.ID} {
				if _, err := st.QueueAddForProfile(ctx, profile, session, "", id, nil, filter, time.Now, testQueueCertification); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.SetMute(ctx, profile, kind, string(entity.ID), false, true); err != nil {
				t.Fatal(err)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, session, "", muted, nil, filter, time.Now, testQueueCertification); err != nil {
				t.Fatalf("queue add for muted encrypted target: %v", err)
			}
			committed, err := st.QueueAddForProfile(ctx, profile, session, "", "media-archive", queueIntPtr(1), filter, time.Now, testQueueCertification)
			if err != nil {
				t.Fatal(err)
			}
			// Like the queue read, the response resolves entity and tag mutes
			// against the main catalog, where this encrypted relation is absent.
			want := []MediaID{other.ID, "media-archive", muted}
			if !slices.EqualFunc(committed.Items, want, func(item SourceItem, id MediaID) bool { return item.ID == id }) {
				t.Errorf("committed response = %#v, want %v", committed.Items, want)
			}
			assertQueueIDs(t, st, filter, profile, want)
			cipher := copyEncryptedLibraryDBCipher{}
			if err := st.SealEncryptedLibraryDB(ctx, library, cipher); err != nil {
				t.Fatal(err)
			}
			assertQueueIDs(t, st, filter, profile, []MediaID{"media-archive"})
			if _, err := st.QueueAddForProfile(ctx, profile, session, "", muted, nil, filter, time.Now, testQueueCertification); !errors.Is(err, ErrQueueMediaNotFound) {
				t.Errorf("queue add for sealed target = %v, want hidden-target refusal", err)
			}
			if err := st.UnsealEncryptedLibraryDB(ctx, library, cipher); err != nil {
				t.Fatal(err)
			}
			assertQueueIDs(t, st, filter, profile, want)
		})
	}
}

func TestQueueAddSkipsEncryptedReaderOutsideCurrentProfileGrants(t *testing.T) {
	st, library, _, _, privateMedia := openEncryptedPackLibrary(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-revealed"
	preflight := LibraryFilter{AllowedIDs: []LibraryID{library, "library-archive"}, Revealed: true}
	for _, id := range []MediaID{privateMedia, "media-archive"} {
		if _, err := st.Media(ctx, id, preflight); err != nil {
			t.Fatalf("preflight for %s: %v", id, err)
		}
	}
	if _, err := st.SetProfileLibraries(ctx, profile, []LibraryID{"library-archive"}); err != nil {
		t.Fatal(err)
	}
	reader := st.encryptedDBs[library].reader
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	var one int
	if err := reader.QueryRowContext(ctx, "SELECT 1 FROM media WHERE id = ?", privateMedia).Scan(&one); err == nil {
		t.Fatal("encrypted reader fault was not installed")
	}

	if _, err := st.QueueAddForProfile(ctx, profile, session, profile, "media-archive", nil, preflight, time.Now, testQueueCertification); err != nil {
		t.Fatalf("catalog add with inaccessible encrypted reader: %v", err)
	}
	if _, err := st.QueueAddForProfile(ctx, profile, session, profile, privateMedia, queueIntPtr(0), preflight, time.Now, testQueueCertification); !errors.Is(err, ErrQueueMediaNotFound) {
		t.Fatalf("inaccessible encrypted add = %v, want hidden-target refusal", err)
	}
	current := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true}
	assertQueueIDs(t, st, current, profile, []MediaID{"media-archive"})
}

func TestQueueAddRefusesInvalidatedRevealSession(t *testing.T) {
	for _, test := range []struct {
		name       string
		invalidate func(context.Context, *Store) error
		want       error
	}{
		{"revoked device", func(ctx context.Context, st *Store) error { return st.RevokeDevice(ctx, "device-tv") }, ErrDeviceRevoked},
		{"expired session", func(ctx context.Context, st *Store) error {
			_, err := st.catalogW.ExecContext(ctx, `UPDATE auth_session SET expires_at_ms = ? WHERE id = 'session-revealed'`, timeToMillis(time.Now().Add(-time.Minute)))
			return err
		}, ErrSessionExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := openSeeded(t)
			ctx := t.Context()
			const profile = "profile-default"
			const sessionID = "session-revealed"
			filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
			if _, err := st.Media(ctx, "media-archive", filter); err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, sessionID, "", "media-alpha", nil, filter, time.Now, testQueueCertification); err != nil {
				t.Fatal(err)
			}
			if err := test.invalidate(ctx, st); err != nil {
				t.Fatal(err)
			}
			session, err := st.Session(ctx, sessionID)
			if err != nil || !session.Revealed {
				t.Fatalf("fixture must retain Reveal: session = %#v, err = %v", session, err)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, sessionID, "", "media-archive", queueIntPtr(0), filter, time.Now, testQueueCertification); !errors.Is(err, test.want) {
				t.Errorf("queue add after invalidation = %v, want %v", err, test.want)
			}
			assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha"})
		})
	}
}

func TestQueueAddWithoutSessionCannotUseReveal(t *testing.T) {
	st := openSeeded(t)
	const profile = "profile-default"
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true}
	_, err := st.QueueAddForProfile(t.Context(), profile, "", "", "media-archive", nil, filter, time.Now, testQueueCertification)
	if !errors.Is(err, ErrQueueMediaNotFound) {
		t.Fatalf("queue add without a Reveal session = %v, want hidden-target refusal", err)
	}
	assertQueueIDs(t, st, filter, profile, nil)
}

func TestQueueAddRefusesAfterRelock(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-revealed"

	preflight := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	if _, err := st.Media(ctx, "media-archive", preflight); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := queueAddFixture(ctx, st, profile, "media-alpha", nil, preflight); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionRevealed(ctx, session, false); err != nil {
		t.Fatal(err)
	}

	for _, position := range []*int{queueIntPtr(0), nil} {
		_, err := st.QueueAddForProfile(ctx, profile, session, "", "media-archive", position, preflight, time.Now, testQueueCertification)
		if !errors.Is(err, ErrQueueMediaNotFound) {
			t.Fatalf("queue add after relock = %v, want hidden-target refusal", err)
		}
		assertQueueIDs(t, st, preflight, profile, []MediaID{"media-alpha"})
	}
}

func TestQueueAddRechecksManagedDeadlineBeforeWrite(t *testing.T) {
	for _, extension := range []bool{false, true} {
		name := "schedule"
		if extension {
			name = "extension"
		}
		t.Run(name, func(t *testing.T) {
			st := openSeeded(t)
			ctx := t.Context()
			const profile = "profile-managed"
			const session = "session-managed"
			filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
			now := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local)
			authNow := func() time.Time { return now }
			deadline := now.Add(time.Minute)
			window := ProfileAccessWindow{Weekday: int(now.Weekday()), StartMinute: 600, EndMinute: 601}
			if extension {
				window.EndMinute = window.StartMinute
				window.StartMinute--
				req, err := st.CreateExtensionRequest(ctx, ExtensionRequestInput{
					ProfileID: profile, RequestingSessionID: session, Now: now,
				})
				if err != nil {
					t.Fatal(err)
				}
				grant, err := st.RespondExtensionRequest(ctx, req.ID, req.ApprovalNonce(), "profile-default", ExtensionApproved, 1, now)
				if err != nil || !grant.GrantExpiresAt.Equal(deadline) {
					t.Fatalf("extension fixture = %+v, %v", grant, err)
				}
			}
			if err := st.SetProfileAccessWindows(ctx, profile, []ProfileAccessWindow{window}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, session, profile, "media-alpha", nil, filter, authNow, testQueueCertification); err != nil {
				t.Fatalf("queue add before deadline: %v", err)
			}
			target := importQueueMedia(t, st, "queue-managed-deadline")
			expire := func(ceilings []ProfileCertificationCeiling) []CertificationConstraint {
				now = deadline
				return testQueueCertification(ceilings)
			}
			if _, err := st.QueueAddForProfile(ctx, profile, session, profile, target, queueIntPtr(0), filter, authNow, expire); !errors.Is(err, ErrQueueProfileOutsideSchedule) {
				t.Fatalf("queue add at deadline = %v, want schedule refusal", err)
			}
			assertQueueIDs(t, st, filter, profile, []MediaID{"media-alpha"})
		})
	}
}

func TestQueueAddKeepsSessionReadersUnblocked(t *testing.T) {
	st := openSeeded(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	addDone := make(chan struct{})
	var addErr error
	defer func() {
		close(release)
		<-addDone
		if !t.Failed() && addErr != nil {
			t.Errorf("queue add: %v", addErr)
		}
	}()
	go func() {
		defer close(addDone)
		filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
		_, addErr = st.QueueAddForProfile(ctx, "profile-default", "session-default", "", "media-alpha", nil, filter, time.Now,
			func(ceilings []ProfileCertificationCeiling) []CertificationConstraint {
				close(started)
				<-release
				return testQueueCertification(ceilings)
			})
	}()
	select {
	case <-started:
	case <-addDone:
		t.Fatalf("queue add stopped before certification: %v", addErr)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := st.Session(ctx, "session-default")
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("session read waited for the queue writer")
	}
}

func TestQueueAddChecksCurrentCertification(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const profile = "profile-default"
	const session = "session-default"
	library, ids := seedCertifiedLibrary(t, st, "starlog", false, "low", "high")
	preflight := LibraryFilter{AllowedIDs: []LibraryID{"library-local", library}}
	if _, err := st.Media(ctx, ids["high"], preflight); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := st.SetProfileCertificationCeiling(ctx, profile, ProfileCertificationCeiling{
		PluginID: "starlog", Ceiling: "low", FilterVisibility: "locked",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.QueueAddForProfile(ctx, profile, session, "", ids["low"], nil, preflight, time.Now, testQueueCertification); err != nil {
		t.Fatalf("queue eligible media: %v", err)
	}
	if err := queueAddFixture(ctx, st, profile, "media-alpha", nil, preflight); err != nil {
		t.Fatal(err)
	}
	_, err := st.QueueAddForProfile(ctx, profile, session, "", ids["high"], queueIntPtr(0), preflight, time.Now, testQueueCertification)
	if !errors.Is(err, ErrQueueMediaLocked) {
		t.Fatalf("queue add after ceiling change = %v, want locked-target refusal", err)
	}
	assertQueueIDs(t, st, preflight, profile, []MediaID{ids["low"], "media-alpha"})

	if err := st.SetMediaFields(ctx, ids["low"], []MediaField{
		{Name: certificationField, Type: EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = st.QueueAddForProfile(ctx, profile, session, "", ids["low"], nil, preflight, time.Now, testQueueCertification)
	if !errors.Is(err, ErrQueueMediaLocked) {
		t.Fatalf("queue add after recertification = %v, want locked-target refusal", err)
	}
	assertQueueIDs(t, st, preflight, profile, []MediaID{ids["low"], "media-alpha"})
}

func testQueueCertification(ceilings []ProfileCertificationCeiling) []CertificationConstraint {
	var constraints []CertificationConstraint
	for _, ceiling := range ceilings {
		constraints = append(constraints, CertificationConstraint{
			PluginID: ceiling.PluginID, Allowed: []string{ceiling.Ceiling}, Locked: ceiling.FilterVisibility == "locked",
		})
	}
	return constraints
}
