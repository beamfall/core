//go:build unix

package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// CORE-2380 extends CORE-1343's no-segments-past-EOF rule to playbackInfo.
func TestPlaybackInfoRefusesResumeBeyondTruncatedSource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		laterPart bool
		version   string
	}{
		{name: "single Part"},
		{name: "later Part default Version", laterPart: true},
		{name: "later Part alternate Version", laterPart: true, version: "alternate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testPlaybackInfoRefusesIncompletePart(t, tc.laterPart, tc.version)
		})
	}
}

func testPlaybackInfoRefusesIncompletePart(t *testing.T, laterPart bool, version string) {
	ctx := context.Background()
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	scope, err := auth.NewService(st).Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(st, streamtoken.NewService([]byte("test-token-key"), nil), nil)
	service.SetSelector(NewSelectorService([]byte("test-token-key"), nil))
	source := filepath.Join(t.TempDir(), "truncated.mkv")
	if err := os.WriteFile(source, []byte("incomplete download"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: source, Title: "Truncated", Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo,
		ContentFingerprint: "fp-truncated", DurationSecondsHint: 6906, VideoWidthHint: 1280, VideoHeightHint: 720,
		Streams: []store.MediaFileStream{videoStream("hevc", "Main", 0, ""), audioStream(1, "aac", 2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if laterPart {
		streams := []store.MediaFileStream{videoStream("hevc", "Main", 0, ""), audioStream(1, "aac", 2)}
		addVersion(t, st, media.ID, "fp-default-later", "", 1, streams)
		addVersion(t, st, media.ID, "fp-alternate-first", "alternate", 0, streams)
		addVersion(t, st, media.ID, "fp-alternate-later", "alternate", 1, streams)
		files, err := st.MediaFilesForMedia(ctx, media.ID, scope.LibraryFilter())
		if err != nil {
			t.Fatal(err)
		}
		source = ""
		for _, file := range files {
			if file.VersionLabel == version && file.PartOrdinal == 1 {
				source = file.Path
			}
		}
		if source == "" {
			t.Fatal("later Part fixture missing")
		}
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{ProfileID: scope.ProfileID, MediaID: media.ID, PositionSeconds: 163, DurationSeconds: 6906}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	transcoder := &scriptedTranscoder{}
	transcoder.start = func(_ int, dir string, startSegment int) []string {
		writeArtifact(t, dir, "init.mp4", []byte("init"))
		for segment := startSegment; segment < 38; segment++ {
			writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", segment), mediaSegment())
		}
		return []string{"sh", "-c", "echo 'File ended prematurely' >&2; exit 0"}
	}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	service.SetSessions(sessions)
	profile := CapabilityProfile{Client: "web-h264", Containers: []string{"mp4"}, VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}}, AudioCodecs: []AudioCodecCap{{Codec: "aac", MaxChannels: 2}}}
	request := InfoRequest{MediaID: string(media.ID), CapabilityProfile: profile}
	if version != "" {
		request.SelectedVersion = versionID(media.ID, version)
	}
	before, err := service.Info(ctx, scope, request)
	if err != nil || before.Timeline.PositionSeconds != 163 || len(before.Streams) == 0 {
		t.Fatalf("baseline = %+v, %v", before, err)
	}
	if laterPart && (len(before.Parts) != 2 || resolveSel(t, service, scope, before.Parts[1]).VersionLabel != version) {
		t.Fatalf("baseline did not select the multi-Part Version: %+v", before)
	}
	key := SessionKey{SessionID: scope.SessionID, MediaID: media.ID}
	if _, err := sessions.FilePath(ctx, key, source, 6906, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	changed := st.EventOutboxChanged()
	if _, err := sessions.FilePath(ctx, key, source, 6906, 1280, 720, false, "720p/seg00038.m4s"); !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("truncated fixture did not reach EOF detection: %v", err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("observed EOF did not wake other clients")
	}
	events, err := st.ReadEventOutbox(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	notified := map[store.ProfileID]bool{}
	for _, event := range events {
		if event.Kind == store.InvalidationKind {
			var payload store.InvalidationPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Reason == store.InvalidationReasonEngagementChanged {
				notified[payload.ProfileID] = true
			}
		}
	}
	if !notified["profile-default"] || !notified["profile-managed"] {
		t.Fatalf("EOF must invalidate all granted Profiles, got %v", notified)
	}
	shelf := func(want bool) {
		t.Helper()
		for _, kind := range []string{"continue-watching", "resume-progress"} {
			query := store.SourceQuery{Kind: kind, ProfileID: scope.ProfileID, Limit: 100, Value: "in-progress"}
			items, err := st.SourceItems(ctx, scope.LibraryFilter(), query)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range items {
				if item.ID == media.ID {
					found = true
				}
			}
			if found != want {
				t.Fatalf("%s contains truncated source = %v, want %v", kind, found, want)
			}
		}
	}
	check := func() {
		t.Helper()
		info, err := service.Info(ctx, scope, request)
		if err != nil {
			t.Fatal(err)
		}
		if info.Decision != "unavailable" || info.Reason != "media-source-incomplete" || len(info.Streams) != 0 || len(info.Parts) != 0 || info.Timeline.PositionSeconds != 0 {
			t.Fatalf("known incomplete source still offers playback/resume: %+v", info)
		}
		if laterPart {
			otherVersion := "alternate"
			if version == otherVersion {
				otherVersion = ""
			}
			otherRequest := request
			otherRequest.SelectedVersion = versionID(media.ID, otherVersion)
			other, err := service.Info(ctx, scope, otherRequest)
			if err != nil || other.Decision == "unavailable" || len(other.Streams) == 0 || other.Timeline.PositionSeconds != 163 {
				t.Fatalf("complete alternate Version lost playback/resume: %+v, %v", other, err)
			}
		}
	}
	check()
	shelf(false)
	sessions.TerminatePlayback(scope.SessionID, media.ID)
	check() // A new player/session must not forget the same file's observed EOF.
	// Evict the old bounded observation cache without evicting the player.
	sessions.incompleteSources.mu.Lock()
	sessions.incompleteSources.files = nil
	sessions.incompleteSources.mu.Unlock()
	check()
	shelf(false)
	sessions.Close()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = storetest.OpenAt(t, dataDir)
	service = NewService(st, streamtoken.NewService([]byte("test-token-key"), nil), nil)
	sessions = NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	service.SetSessions(sessions)
	check()
	shelf(false)
	if err := os.WriteFile(source, []byte("completed download with more media bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	shelf(true)
	after, err := service.Info(ctx, scope, request)
	if err != nil || after.Decision == "unavailable" || after.Timeline.PositionSeconds != 163 {
		t.Fatalf("completed download remains blocked: %+v, %v", after, err)
	}
}

func TestIncompleteSourceObservationTracksOnlyTheObservedFileGeneration(t *testing.T) {
	for _, changeBeforeObservation := range []bool{false, true} {
		t.Run(fmt.Sprint(changeBeforeObservation), func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "download.mkv")
			if err := os.WriteFile(source, []byte("partial"), 0o600); err != nil {
				t.Fatal(err)
			}
			observed, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			key := SessionKey{SessionID: "device", MediaID: "download"}
			session := &hlsSession{src: source, sourceInfo: observed}
			sessions := &Sessions{sessions: map[SessionKey]*hlsSession{key: session}}
			observe := func() {
				t.Helper()
				if _, found, err := sessions.observeIncompleteSource(key, session, func() (time.Duration, bool) {
					return 10 * time.Second, true
				}); !found || err != nil {
					t.Fatalf("source EOF: found=%v, err=%v", found, err)
				}
			}
			if !changeBeforeObservation {
				observe()
				if !sessions.incompleteSources.contains(source) {
					t.Fatal("unchanged truncated source not remembered")
				}
			}
			// An atomic replacement may have the same size and mtime. Inode identity
			// must still invalidate the old encoder's observation.
			replacement := filepath.Join(dir, "completed.mkv")
			if err := os.WriteFile(replacement, []byte("updated"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(replacement, observed.ModTime(), observed.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, source); err != nil {
				t.Fatal(err)
			}
			if changeBeforeObservation {
				observe()
			}
			if sessions.incompleteSources.contains(source) {
				t.Fatal("replacement poisoned by old source's EOF")
			}
		})
	}
}

func TestSourceCacheStatDiscardsEvictedObservation(t *testing.T) {
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	src := filepath.Join(t.TempDir(), "source.mkv")
	if err := os.WriteFile(src, []byte("old generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := SessionKey{SessionID: "observing", MediaID: "media"}
	request := func() {
		t.Helper()
		if _, err := sessions.FilePath(t.Context(), key, src, 120, 1280, 720, false, masterPlaylistName); err != nil {
			t.Fatal(err)
		}
	}
	request()
	old := registeredSession(t, sessions, key)
	sessions.statSource = func(path string) (os.FileInfo, error) {
		info, err := os.Stat(path) // #nosec G703 -- fake source paths come from this test.
		if path == src && err == nil && sameSourceFile(old.sourceInfo, info) {
			latch.block() // hold the old stat result while the successor publishes
		}
		return info, err
	}
	seedUnrelatedSessions(t, sessions)
	observe := func() (time.Duration, bool) { return 10 * time.Second, true }
	done := make(chan error, 1)
	go func() {
		_, _, err := sessions.observeIncompleteSource(key, old, observe)
		done <- err
	}()
	latch.wait(t)
	assertUnrelatedSessionsProgress(t, sessions)

	next := src + ".replacement"
	if err := os.WriteFile(next, []byte("new generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, src); err != nil {
		t.Fatal(err)
	}
	request()
	replacement := registeredSession(t, sessions, key)
	if replacement == old || replacement.sourceGeneration == old.sourceGeneration {
		t.Fatal("fixture did not replace the session with a newer source generation")
	}
	if _, found, err := sessions.observeIncompleteSource(key, replacement, observe); !found || err != nil {
		t.Fatalf("replacement EOF: found=%v, err=%v", found, err)
	}
	if !sessions.incompleteSources.contains(src) {
		t.Fatal("replacement EOF was not cached")
	}

	latch.unblock()
	if err := awaitSessionOperation(t, done); !errors.Is(err, ErrSessionEvicted) {
		t.Fatalf("old observer returned %v, want ErrSessionEvicted", err)
	}
	if !sessions.incompleteSources.contains(src) {
		t.Fatal("evicted observer overwrote replacement's cached EOF")
	}
}

func TestSourceCacheStatPreservesOtherDevicesReplacementEOF(t *testing.T) {
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	latch := newSessionIOLatch(t)
	src := filepath.Join(t.TempDir(), "source.mkv")
	if err := os.WriteFile(src, []byte("old generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldKey := SessionKey{SessionID: "first-device", MediaID: "media"}
	newKey := SessionKey{SessionID: "second-device", MediaID: oldKey.MediaID}
	request := func(key SessionKey) *hlsSession {
		t.Helper()
		if _, err := sessions.FilePath(t.Context(), key, src, 120, 1280, 720, false, masterPlaylistName); err != nil {
			t.Fatal(err)
		}
		return registeredSession(t, sessions, key)
	}
	old := request(oldKey)
	sessions.statSource = func(path string) (os.FileInfo, error) {
		info, err := os.Stat(path) // #nosec G703 -- fake source paths come from this test.
		if path == src && err == nil && sameSourceFile(old.sourceInfo, info) {
			latch.block()
		}
		return info, err
	}
	observe := func() (time.Duration, bool) { return 10 * time.Second, true }
	done := make(chan error, 1)
	go func() {
		_, _, err := sessions.observeIncompleteSource(oldKey, old, observe)
		done <- err
	}()
	latch.wait(t)

	next := src + ".replacement"
	if err := os.WriteFile(next, []byte("new generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, src); err != nil {
		t.Fatal(err)
	}
	replacement := request(newKey)
	if sameSourceFile(old.sourceInfo, replacement.sourceInfo) {
		t.Fatal("fixture did not replace the source generation")
	}
	if _, found, err := sessions.observeIncompleteSource(newKey, replacement, observe); !found || err != nil {
		t.Fatalf("replacement EOF: found=%v, err=%v", found, err)
	}
	if !sessions.incompleteSources.contains(src) {
		t.Fatal("replacement EOF was not cached")
	}

	latch.unblock()
	if err := awaitSessionOperation(t, done); err != nil {
		t.Fatalf("old observer's session is still live: %v", err)
	}
	if !sessions.incompleteSources.contains(src) {
		t.Fatal("old generation overwrote the second device's cached EOF")
	}
}

// The encrypted engine has no filesystem source path. The immutable object
// fingerprint, scoped to its Library's catalog, must carry the observation.
func TestEncryptedSourceEndSurvivesSessionReplacement(t *testing.T) {
	ctx := context.Background()
	f := newEncryptedPrecomputeFixture(t)
	scope, err := auth.NewService(f.st).Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	scope.Revealed = true
	service := NewService(f.st, streamtoken.NewService([]byte("test-token-key"), nil), nil)
	transcoder := &fakeEncryptedTranscoder{segment: func(int) []byte { return emptySegment() }}
	sessions := NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	service.SetSessions(sessions)
	if err := f.st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{ProfileID: scope.ProfileID, MediaID: f.mediaID, PositionSeconds: 63, DurationSeconds: 120}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	files, err := f.st.MediaFilesForMedia(ctx, f.mediaID, scope.LibraryFilter())
	if err != nil || len(files) != 1 {
		t.Fatalf("encrypted fixture files = %v, %v", files, err)
	}
	request := InfoRequest{MediaID: string(f.mediaID), CapabilityProfile: CapabilityProfile{Client: "web-h264", Containers: []string{"mp4"}, VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}}, AudioCodecs: []AudioCodecCap{{Codec: "aac", MaxChannels: 2}}}}
	before, err := service.Info(ctx, scope, request)
	if err != nil || before.Decision == "unavailable" || before.Timeline.PositionSeconds != 63 {
		t.Fatalf("encrypted baseline = %+v, %v", before, err)
	}
	shelf := func(want bool) {
		t.Helper()
		items, err := f.st.SourceItems(ctx, scope.LibraryFilter(), store.SourceQuery{Kind: "continue-watching", ProfileID: scope.ProfileID, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			if item.ID == f.mediaID {
				found = true
			}
		}
		if found != want {
			t.Fatalf("encrypted shelf contains item = %v, want %v", found, want)
		}
	}
	shelf(true)
	secure := testEncryptedSource(nil)
	secure.LibraryID = "library-local"
	secure.Fingerprint = files[0].ContentFingerprint
	key := SessionKey{SessionID: scope.SessionID, MediaID: f.mediaID}
	if _, err := encryptedFilePath(sessions, key, secure, 120, -1, false, "720p/seg00010.m4s"); !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("encrypted fixture did not observe EOF: %v", err)
	}
	check := func() {
		t.Helper()
		info, err := service.Info(ctx, scope, request)
		if err != nil || info.Decision != "unavailable" || info.Reason != "media-source-incomplete" || info.Timeline.PositionSeconds != 0 {
			t.Fatalf("encrypted incomplete source advertised: %+v, %v", info, err)
		}
		shelf(false)
	}
	check()
	sessions.Close()
	sessions = NewSessions(transcoder, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	service.SetSessions(sessions)
	check()
	// A different content-addressed object must not inherit the old EOF.
	replacement, _, err := f.st.ImportMediaFile(ctx, store.ImportMediaFile{LibraryID: "library-local", Path: files[0].Path, Title: "Repaired", Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo, ContentFingerprint: "fp-repaired-encrypted", DurationSecondsHint: 120, VideoWidthHint: 1280, VideoHeightHint: 720})
	if err != nil {
		t.Fatal(err)
	}
	request.MediaID = string(replacement.ID)
	after, err := service.Info(ctx, scope, request)
	if err != nil || after.Reason == "media-source-incomplete" {
		t.Fatalf("encrypted replacement poisoned: %+v, %v", after, err)
	}
}

func TestEvictedSessionCannotObserveEOFInRemovedOutput(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	sessions := NewSessions(&fakeEncryptedTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	sessions.sourceStore = f.st
	key := SessionKey{SessionID: "evicted", MediaID: f.mediaID}
	session := &hlsSession{dir: t.TempDir(), secure: testEncryptedSource(nil)}
	sessions.sessions[key] = session
	if n := sessions.TerminateSession(key.SessionID); n != 1 {
		t.Fatalf("terminated %d sessions", n)
	}
	inspected := false
	_, observed, err := sessions.observeIncompleteSource(key, session, func() (time.Duration, bool) {
		inspected = true
		return 0, true // removed output would look like EOF at zero
	})
	if !errors.Is(err, ErrSessionEvicted) || observed || inspected {
		t.Fatalf("evicted output inspected=%v observed=%v err=%v", inspected, observed, err)
	}
}

func TestSourceInspectionDiscardsEvictedResult(t *testing.T) {
	for _, caller := range []string{"segment request", "process reaper"} {
		t.Run(caller, func(t *testing.T) {
			ctx := t.Context()
			st := storetest.OpenAt(t, storetest.MigratedDataDir(t))
			source := filepath.Join(t.TempDir(), "source.mkv")
			if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
				t.Fatal(err)
			}
			const fingerprint = "source-inspection-generation"
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: "library-local", Path: source, Title: "Source", ContentFingerprint: fingerprint,
				Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
			})
			if err != nil {
				t.Fatal(err)
			}
			var transcoder Transcoder = &fakeTranscoder{}
			name := "720p/seg00000.m4s"
			if caller == "process reaper" {
				name = "720p/init.mp4"
				transcoder = &scriptedTranscoder{start: func(_ int, dir string, segment int) []string {
					writeArtifact(t, dir, initSegmentName, []byte("init"))
					writeArtifact(t, dir, fmt.Sprintf("seg%05d.m4s", segment), mediaSegment())
					return []string{"sh", "-c", "exit 0"}
				}}
			}
			sessions := NewSessions(transcoder, t.TempDir(), nil)
			t.Cleanup(sessions.Close)
			latch := newSessionIOLatch(t)
			sessions.sourceStore = st
			sessions.inspectSegment = func(h *hlsSession, path string) bool {
				if h.src == source {
					latch.block()
					return true // teardown made the old output untrustworthy
				}
				return segmentLacksMedia(path)
			}
			seedUnrelatedSessions(t, sessions)
			key := SessionKey{SessionID: "inspecting", MediaID: media.ID}
			done := make(chan error, 1)
			var path string
			go func() {
				var err error
				path, err = sessions.FilePath(ctx, key, source, 120, 1280, 720, false, name)
				done <- err
			}()
			latch.wait(t)
			assertUnrelatedSessionsProgress(t, sessions)
			original := registeredSession(t, sessions, key)
			var exited <-chan struct{}
			if caller == "process reaper" {
				run, _, tracked := sessions.rungEncoder(key, original, Rung{ID: "720p"})
				if !tracked || run.exited == nil {
					t.Fatal("fixture did not start a tracked encoder")
				}
				exited = run.exited
			}
			if n := sessions.TerminateSession(key.SessionID); n != 1 {
				t.Fatalf("terminated %d sessions, want 1", n)
			}
			if _, err := sessions.FilePath(ctx, key, source, 120, 1280, 720, false, masterPlaylistName); err != nil {
				t.Fatal(err)
			}
			replacement := registeredSession(t, sessions, key)
			if replacement == original || replacement.sourceGeneration == "" {
				t.Fatal("fixture did not replace the session for the same durable source generation")
			}
			latch.unblock()
			requestErr := awaitSessionOperation(t, done)
			if caller == "segment request" {
				if !errors.Is(requestErr, ErrSessionEvicted) || path != "" {
					t.Fatalf("evicted segment returned path=%q, err=%v, want no path and ErrSessionEvicted", path, requestErr)
				}
			} else if requestErr != nil {
				t.Fatalf("init request failed: %v", requestErr)
			}
			if exited != nil {
				select {
				case <-exited:
				case <-time.After(5 * time.Second):
					t.Fatal("reaper did not finish")
				}
			}
			if sessions.beyondSourceEnd(replacement, Rung{ID: "720p"}, 0) || sessions.incompleteSources.contains(source) {
				t.Fatal("stale EOF was published in memory")
			}
			incomplete, err := st.MediaFileSourceIncomplete(ctx, store.MediaFile{
				MediaID: media.ID, Path: source, ContentFingerprint: fingerprint,
			})
			if err != nil || incomplete {
				t.Fatalf("stale EOF was published to the catalog: incomplete=%v, err=%v", incomplete, err)
			}
		})
	}
}
