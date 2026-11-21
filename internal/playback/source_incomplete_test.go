//go:build unix

package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
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
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)

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
	// The scripted encoder exits at once, so its reap can observe and persist
	// the source end before the request for seg00038 does. Take the wake
	// channel before any encoder starts so either observer's write counts.
	changed := st.EventOutboxChanged()
	if _, err := sessions.FilePath(ctx, key, source, 6906, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
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
	_ = sessions.Close()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st = storetest.OpenAt(t, dataDir)
	service = NewService(st, streamtoken.NewService([]byte("test-token-key"), nil), nil)
	sessions = newTestSessions(t, transcoder, t.TempDir(), nil)

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
			var cache incompleteSourceCache
			if !changeBeforeObservation {
				if current, err := os.Stat(source); err == nil && sameSourceFile(current, observed) {
					cache.rememberVerified(source, observed)
				}
				if !cache.contains(source) {
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
				if current, err := os.Stat(source); err == nil && sameSourceFile(current, observed) {
					cache.rememberVerified(source, observed)
				}
			}
			if cache.contains(source) {
				t.Fatal("replacement poisoned by old source's EOF")
			}
		})
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
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)

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
	_ = sessions.Close()
	sessions = newTestSessions(t, transcoder, t.TempDir(), nil)

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
	sessions := newTestSessions(t, &fakeEncryptedTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	sessions.sourceStore = f.st
	key := SessionKey{SessionID: "evicted", MediaID: f.mediaID}
	artifact, err := sessions.LiveArtifactShaped(t.Context(), key, "", 120, 1280, 720, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, nil,
		StreamShape{Encrypted: testEncryptedSource(nil)}, PriorityNormal, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	session := artifact.session
	artifact.Release()
	if n := sessions.TerminateSession(key.SessionID); n != 1 {
		t.Fatalf("terminated %d sessions", n)
	}
	requireScratchMissing(t, session.dir)
	inspected := false
	_, observed, err := sessions.observeIncompleteSource(context.Background(), key, session, artifactObservation{}, false, func() (time.Duration, bool) {
		inspected = true
		return 0, true // removed output would look like EOF at zero
	})
	if !errors.Is(err, ErrSessionEvicted) || observed || inspected {
		t.Fatalf("evicted output inspected=%v observed=%v err=%v", inspected, observed, err)
	}
}

func TestT3EOFInspectionDiscardsLosingStoreCallback(t *testing.T) {
	for _, action := range []string{"cutoff", "lock conflict"} {
		t.Run(action, func(t *testing.T) {
			st := storetest.Open(t)
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			item, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
				LibraryID: "library-local", Path: src, Title: "Source",
				Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
				ContentFingerprint: "t3-source-end", DurationSecondsHint: 120,
			})
			if err != nil {
				t.Fatal(err)
			}
			files, err := st.MediaFilesForMedia(t.Context(), item.ID, store.LibraryFilter{AllLibraries: true})
			if err != nil || len(files) != 1 {
				t.Fatalf("source files = %v, error = %v", files, err)
			}
			s := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
			s.sourceStore = st
			key := SessionKey{SessionID: "inspection", MediaID: item.ID}
			a, err := s.LiveArtifactShaped(t.Context(), key, src, 120, 1280, 720, false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Release()
			entered, release := scratchLatch(t)
			defer release.open()
			outcome := make(chan error, 1)
			go func() {
				calls := 0
				_, observed, err := s.observeIncompleteSource(t.Context(), key, a.session, artifactObservation{}, false, func() (time.Duration, bool) {
					calls++
					if calls == 1 {
						close(entered)
						<-release.ch
						return 4 * time.Second, true
					}
					return 0, false
				})
				if observed || action == "lock conflict" && calls != 2 {
					err = fmt.Errorf("inspection = observed %v, attempts %d, error %w", observed, calls, err)
				}
				outcome <- err
			}()
			waitScratchSignal(t, entered)
			catalogClear := func() error {
				incomplete, err := st.MediaFileSourceIncomplete(t.Context(), files[0])
				if incomplete {
					return errors.New("discarded inspection reached the catalog")
				}
				return err
			}
			want := error(nil)
			if action == "cutoff" {
				s.TerminateSession(key.SessionID)
				release.open()
				want = ErrSessionEvicted
			} else {
				func() {
					s.mu.Lock()
					defer s.mu.Unlock()
					release.open()
					checked := make(chan error, 1)
					go func() { checked <- catalogClear() }()
					if err := waitScratchResult(t, checked); err != nil {
						t.Fatal(err)
					}
				}()
			}
			if err := waitScratchResult(t, outcome); !errors.Is(err, want) {
				t.Fatalf("inspection error = %v, want %v", err, want)
			}
			if err := catalogClear(); err != nil || s.incompleteSources.contains(src) {
				t.Fatalf("discarded EOF changed catalog or memory: %v", err)
			}
		})
	}
}

type orderedSourceEndStore struct {
	sourceEndObserver
	calls                                       atomic.Int32
	firstCallback, firstReturned, reaperStarted chan struct{}
	release                                     <-chan struct{}
	// staged records that the first source's own observation was persisted. A
	// busy TryLock makes production retry through a later call, so any call for
	// the first fingerprint counts.
	staged           atomic.Bool
	firstFingerprint atomic.Pointer[string]
}

func TestT3PathlessEncryptedItemsKeepIndependentEOFObservations(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	ctx := t.Context()
	first, err := f.st.MediaFilesForMedia(ctx, f.mediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil || len(first) != 1 {
		t.Fatalf("first encrypted source = %v, %v", first, err)
	}
	second, secondFile, err := f.st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: filepath.Join(t.TempDir(), "second"), Title: "Second source",
		Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo, ContentFingerprint: "second-encrypted-source",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestSessions(t, &fakeEncryptedTranscoder{segment: func(int) []byte { return emptySegment() }}, t.TempDir(), nil)
	entered, release := scratchLatch(t)
	defer release.open()
	observer := &orderedSourceEndStore{
		sourceEndObserver: f.st, firstCallback: entered, release: release.ch,
		firstReturned: make(chan struct{}), reaperStarted: make(chan struct{}),
	}
	s.sourceStore = observer
	request := func(file store.MediaFile, name string) error {
		secure := testEncryptedSource(nil)
		secure.LibraryID, secure.Fingerprint = "library-local", file.ContentFingerprint
		_, err := encryptedFilePath(s, SessionKey{SessionID: "viewer", MediaID: file.MediaID}, secure, 120, -1, false, name)
		return err
	}
	const segment = "720p/seg00010.m4s"
	pending := make(chan error, 1)
	go func() { pending <- request(first[0], segment) }()
	waitScratchSignal(t, entered)
	if err := request(secondFile, masterPlaylistName); err != nil {
		t.Fatalf("second encrypted item %s setup: %v", second.ID, err)
	}
	release.open()
	if err := waitScratchResult(t, pending); !errors.Is(err, ErrSourceEnded) || !observer.staged.Load() {
		t.Fatalf("first inspection = %v, staged = %t", err, observer.staged.Load())
	}
	if err := request(secondFile, segment); !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("second inspection = %v", err)
	}
	for _, file := range []store.MediaFile{first[0], secondFile} {
		if incomplete, err := f.st.MediaFileSourceIncomplete(ctx, file); err != nil || !incomplete {
			t.Fatalf("catalog EOF for %s = %t, %v", file.ContentFingerprint, incomplete, err)
		}
	}
}

func (s *orderedSourceEndStore) ObserveSourceEnd(ctx context.Context, id store.MediaID, path, fingerprint, generation string, observe func() (float64, bool)) error {
	switch s.calls.Add(1) {
	case 1:
		s.firstFingerprint.Store(&fingerprint)
		defer close(s.firstReturned)
		return s.sourceEndObserver.ObserveSourceEnd(ctx, id, path, fingerprint, generation, func() (float64, bool) {
			close(s.firstCallback)
			<-s.release
			end, ok := observe()
			if ok {
				s.staged.Store(true)
			}
			return end, ok
		})
	case 2:
		close(s.reaperStarted)
	}
	if first := s.firstFingerprint.Load(); first != nil && *first == fingerprint {
		inner := observe
		observe = func() (float64, bool) {
			end, ok := inner()
			if ok {
				s.staged.Store(true)
			}
			return end, ok
		}
	}
	return s.sourceEndObserver.ObserveSourceEnd(ctx, id, path, fingerprint, generation, observe)
}

func TestT3AdjustedEOFPersistsSourceSeconds(t *testing.T) {
	for _, inspection := range []string{"segment", "reaper"} {
		t.Run(inspection, func(t *testing.T) {
			ctx := t.Context()
			dataDir := storetest.MigratedDataDir(t)
			st := storetest.OpenAt(t, dataDir)
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			const fingerprint = "adjusted-eof"
			item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: "library-local", Path: src, Title: "Source", Container: "m4a",
				Codec: "aac", Class: store.MediaClassAudio, ContentFingerprint: fingerprint, DurationSecondsHint: 120,
			})
			if err != nil {
				t.Fatal(err)
			}
			input, signal, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer signal.Close()
			s := newTestSessions(t, &eofInspectionTranscoder{input: input}, t.TempDir(), nil)
			s.sourceStore = st
			key := SessionKey{SessionID: "adjusted-eof", MediaID: item.ID}
			shape := StreamShape{AudioOnly: true, Adjust: ffmpeg.AudioAdjust{Tempo: 1.25}}
			request := func(name string) (string, error) {
				return s.FilePathShaped(ctx, key, src, 120, 0, 0, false, false, ladderCaps{}, VideoSignal{}, -1,
					ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, nil, shape, PriorityNormal, audioOnlyRungID+"/"+name)
			}
			path, err := request("init.mp4")
			if err != nil {
				t.Fatal(err)
			}
			h := registeredSession(t, s, key)
			run := h.run
			dir := filepath.Dir(path)
			// The 20th boundary is at 80 output seconds, or 100 source seconds.
			writeArtifact(t, dir, "seg00019.m4s", mediaSegment())
			if inspection == "segment" {
				writeArtifact(t, dir, "seg00020.m4s", emptySegment())
				if _, err := request("seg00020.m4s"); !errors.Is(err, ErrSourceEnded) {
					t.Fatalf("segment request = %v, want EOF", err)
				}
				select {
				case <-run.reaped:
					t.Fatal("child reaped before the segment could record EOF")
				default:
				}
			} else {
				if err := signal.Close(); err != nil {
					t.Fatal(err)
				}
				waitScratchSignal(t, run.exited)
			}
			if end := storetest.RecordedSourceEnd(t, dataDir, fingerprint, h.sourceGeneration); end != 100 {
				t.Fatalf("persisted EOF = %v seconds, want 100 source seconds", end)
			}
		})
	}
}

func TestT3SegmentRetriesOutsideStoreWhileReaperOwnsOutput(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	src := filepath.Join(t.TempDir(), "source")
	seedScratchFile(t, src)
	item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: src, Title: "Source",
		Container: "mkv", Codec: "h264", Class: store.MediaClassVideo,
		ContentFingerprint: "ordered-source-end", DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	input, signal, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer signal.Close()
	s := newTestSessions(t, &eofInspectionTranscoder{input: input}, t.TempDir(), nil)
	entered, release := scratchLatch(t)
	defer release.open()
	observer := &orderedSourceEndStore{
		sourceEndObserver: st, firstCallback: entered, release: release.ch,
		firstReturned: make(chan struct{}), reaperStarted: make(chan struct{}),
	}
	s.sourceStore = observer
	key := SessionKey{SessionID: "eof", MediaID: item.ID}
	path, err := s.FilePath(ctx, key, src, 120, 1280, 720, false, "720p/init.mp4")
	if err != nil {
		t.Fatal(err)
	}
	run := registeredSession(t, s, key).run
	// An actual finalized empty segment would stage EOF from the segment
	// callback if it were allowed to inspect during reap.
	writeArtifact(t, filepath.Dir(path), "seg00000.m4s", emptySegment())
	result := make(chan error, 1)
	go func() {
		_, err := s.FilePath(ctx, key, src, 120, 1280, 720, false, "720p/seg00000.m4s")
		result <- err
	}()
	waitScratchSignal(t, observer.firstCallback)
	_ = signal.Close()
	waitScratchSignal(t, run.reaped)
	waitScratchSignal(t, observer.reaperStarted)
	release.open()
	waitScratchSignal(t, observer.firstReturned)
	if observer.staged.Load() {
		t.Fatal("segment callback staged EOF ahead of the reaper")
	}
	waitScratchSignal(t, run.exited)
	if err := waitScratchResult(t, result); !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("segment did not observe the settled result: %v", err)
	}
	if got := observer.calls.Load(); got != 3 {
		t.Fatalf("Store calls = %d, want segment, reaper, and segment retry", got)
	}
}

func TestT3OldEOFPublicationLosesToReplacementGeneration(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(fmt.Sprintf("same key %v", sameKey), func(t *testing.T) {
			ctx := t.Context()
			st := storetest.Open(t)
			src := filepath.Join(t.TempDir(), "source")
			seedScratchFile(t, src)
			item, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: "library-local", Path: src, Title: "Source", Container: "mkv",
				Codec: "h264", Class: store.MediaClassVideo, ContentFingerprint: "source-replacement",
				DurationSecondsHint: 120,
			})
			if err != nil {
				t.Fatal(err)
			}
			s := newTestSessions(t, &fakeTranscoder{holdChild: true}, t.TempDir(), nil)
			s.sourceStore = st
			oldKey := SessionKey{SessionID: "before-replacement", MediaID: item.ID}
			playlist := func(key SessionKey) *LiveArtifact {
				t.Helper()
				a, err := s.LiveArtifactShaped(ctx, key, src, 120, 1280, 720, false, false,
					ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{}, PriorityNormal, masterPlaylistName)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(a.Release)
				return a
			}
			old := playlist(oldKey)
			entered, release := scratchLatch(t)
			defer release.open()
			var latch atomic.Bool
			latch.Store(true)
			s.statSource = func(path string) (os.FileInfo, error) {
				info, err := os.Stat(path) // #nosec G703 -- source belongs to the private fixture.
				if latch.CompareAndSwap(true, false) {
					close(entered)
					<-release.ch
				}
				return info, err
			}
			outcome := make(chan error, 1)
			go func() {
				_, observed, err := s.observeIncompleteSource(ctx, oldKey, old.session, artifactObservation{}, false, func() (time.Duration, bool) {
					return 4 * time.Second, true
				})
				if observed {
					err = errors.New("old inspection published an EOF bound")
				}
				outcome <- err
			}()
			waitScratchSignal(t, entered)
			replacement := filepath.Join(filepath.Dir(src), "replacement")
			seedScratchFile(t, replacement)
			if err := os.Rename(replacement, src); err != nil {
				t.Fatal(err)
			}
			newKey, want := oldKey, ErrSessionEvicted
			if !sameKey {
				newKey.SessionID, want = "after-replacement", errSetupUnavailable
			}
			next := playlist(newKey)
			if next.session == old.session {
				t.Fatal("replacement generation reused the old instance")
			}
			release.open()
			if err := waitScratchResult(t, outcome); !errors.Is(err, want) {
				t.Fatalf("old EOF inspection = %v, want %v", err, want)
			}
			files, err := st.MediaFilesForMedia(ctx, item.ID, store.LibraryFilter{AllLibraries: true})
			if err != nil || len(files) != 1 {
				t.Fatalf("source files = %v, error %v", files, err)
			}
			if incomplete, err := st.MediaFileSourceIncomplete(ctx, files[0]); err != nil || incomplete || s.incompleteSources.contains(src) {
				t.Fatalf("replacement inherited EOF, catalog=%v, error=%v", incomplete, err)
			}
			if _, err := s.FilePath(ctx, newKey, src, 120, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
				t.Fatalf("replacement could not serve bytes: %v", err)
			}
		})
	}
}
