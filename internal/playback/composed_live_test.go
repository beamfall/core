package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// liveComposedService is capabilityTestService with transcode sessions and a
// composed cache root wired, so a picture without a Ready rendition can play
// live.
func liveComposedService(t *testing.T) (*Service, *store.Store, auth.Scope) {
	t.Helper()
	service, st, scope := capabilityTestService(t)
	sessions := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)

	sessions.SetTranscodeCap(4)
	service.SetSessions(sessions)
	service.SetComposedCacheRoot(t.TempDir())
	return service, st, scope
}

func composedJobs(t *testing.T, st *store.Store) []store.Job {
	t.Helper()
	all, err := st.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Job
	for _, job := range all {
		if job.Kind == PrecomputeJobKind && strings.Contains(string(job.Payload), `"rungID":"composed"`) {
			out = append(out, job)
		}
	}
	return out
}

func TestInfoComposesPictureLiveWithoutReadyRendition(t *testing.T) {
	for name, picture := range map[string]func(dir string) ComposedPicture{
		"graphics": func(dir string) ComposedPicture {
			path := filepath.Join(dir, "song.cdg")
			if err := os.WriteFile(path, make([]byte, 24*4), 0o600); err != nil {
				t.Fatal(err)
			}
			return ComposedPicture{GraphicsPath: path}
		},
		"timed text": func(string) ComposedPicture { return ComposedPicture{TimedText: composedTestText} },
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			service, st, scope := liveComposedService(t)
			dir := t.TempDir()
			media := importAudioMedia(t, st, filepath.Join(dir, "song.mp3"), "fp-live")
			service.SetPictureComposer(staticComposer(picture(dir), true, nil))

			resp, err := service.Info(ctx, scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Decision != "transcode" || resp.Reason != "composed-picture" || len(resp.Streams) != 1 {
				t.Fatalf("decision = %q reason = %q streams = %#v, want a live composed stream", resp.Decision, resp.Reason, resp.Streams)
			}
			stream := resp.Streams[0]
			if stream.Kind != "hls" || !strings.HasPrefix(stream.ID, "composed-") ||
				len(stream.Renditions) != 1 || stream.Renditions[0].ID != ComposedRungID || stream.Renditions[0].Height != composedHeight {
				t.Fatalf("stream = %#v, want one composed HLS rung", stream)
			}
			tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
			grant, err := tokens.UnverifiedGrant(stream.Token, streamtoken.TargetMediaHLS)
			if err != nil || !grant.ComposedPicture || grant.PlaybackRenditionID != "" {
				t.Fatalf("grant = %+v err %v, want a live composed-picture HLS grant", grant, err)
			}
			jobs := composedJobs(t, st)
			if len(jobs) != 1 || jobs[0].Priority != composedDemandPriority {
				t.Fatalf("composed jobs = %+v, want one at demand priority %d", jobs, composedDemandPriority)
			}
		})
	}
}

func TestInfoPrefersReadyComposedRenditionOverLive(t *testing.T) {
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-ready")
	picture := ComposedPicture{TimedText: composedTestText}
	id := insertComposedRendition(t, st, media, "fp-ready", picture)
	service.SetPictureComposer(staticComposer(picture, true, nil))

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "precomputed-rendition" || resp.Reason != "composed-picture" {
		t.Fatalf("decision = %q reason = %q, want the Ready rendition", resp.Decision, resp.Reason)
	}
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil || grant.PlaybackRenditionID != id || grant.ComposedPicture {
		t.Fatalf("grant = %+v err %v, want precomputed rendition %q", grant, err, id)
	}
	if jobs := composedJobs(t, st); len(jobs) != 0 {
		t.Fatalf("composed jobs = %+v, want none when a rendition is Ready", jobs)
	}
}

func TestInfoLiveComposedFallsBackToAudio(t *testing.T) {
	disallowed := false
	cases := map[string]struct {
		profile CapabilityProfile
		policy  *store.ProfileSettings
	}{
		"client without H.264 gets the audio": {profile: audioOnlyClient},
		"transcode policy refuses live":       {profile: videoClient, policy: &store.ProfileSettings{TranscodeAllowed: &disallowed}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			service, st, scope := liveComposedService(t)
			if c.policy != nil {
				setProfileSettings(t, st, scope.ProfileID, *c.policy)
			}
			media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-audio")
			service.SetPictureComposer(staticComposer(ComposedPicture{TimedText: composedTestText}, true, nil))
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: c.profile})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Decision != "direct-play" {
				t.Fatalf("decision = %q reason = %q, want direct play of the audio", resp.Decision, resp.Reason)
			}
			if jobs := composedJobs(t, st); len(jobs) != 0 {
				t.Fatalf("composed jobs = %+v, want none", jobs)
			}
		})
	}
}

// A render the sweep already queued at its default priority is raised, not
// duplicated, by a live play.
func TestLiveComposedPlayRaisesQueuedSweepRender(t *testing.T) {
	ctx := context.Background()
	service, st, scope := liveComposedService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-raise")
	composer := staticComposer(ComposedPicture{TimedText: composedTestText}, true, nil)
	service.SetPictureComposer(composer)
	if queued, err := EnsureComposedRendition(ctx, st, media.ID, composer, nil, service.composedCache, time.Unix(90, 0)); err != nil || !queued {
		t.Fatalf("sweep enqueue = %v err %v", queued, err)
	}
	if _, err := service.Info(ctx, scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient}); err != nil {
		t.Fatal(err)
	}
	jobs := composedJobs(t, st)
	if len(jobs) != 1 || jobs[0].Priority != composedDemandPriority {
		t.Fatalf("composed jobs = %+v, want the one sweep job raised to %d", jobs, composedDemandPriority)
	}
}

func TestComposedShapedSessionEncodesOneComposedRung(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	calls := 0
	sessions.SetPictureComposer(func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
		calls++
		return ComposedPicture{TimedText: composedTestText}, true, nil
	})
	key := SessionKey{SessionID: "session-default", MediaID: "media-song"}
	shape := StreamShape{ComposedPicture: true}
	src := filepath.Join(t.TempDir(), "song.mp3")
	seedScratchFile(t, src)
	masterPath, err := sessions.FilePathShaped(t.Context(), key, src, 30, 0, 0, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, shape, PriorityNormal, masterPlaylistName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.FilePathShaped(t.Context(), key, src, 30, 0, 0, false, false,
		ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, shape, PriorityNormal, ComposedRungID+"/seg00000.m4s"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("composer resolved %d times, want once per session", calls)
	}
	if len(transcoder.renditions) != 1 {
		t.Fatalf("encodes = %d, want 1", len(transcoder.renditions))
	}
	picture, ok := transcoder.renditions[0].Picture()
	wantTimedText := filepath.Join(filepath.Dir(masterPath), ComposedRungID, composedTimedTextFile)
	if !ok || picture.TimedTextPath != wantTimedText {
		t.Fatalf("rendition picture = %+v ok %v, want timed text at %q", picture, ok, wantTimedText)
	}
	if text, err := os.ReadFile(picture.TimedTextPath); err != nil || string(text) != composedTestText {
		t.Fatalf("timed text = %q err %v", text, err)
	}
	master := string(mustRead(t, masterPath))
	if strings.Count(master, "#EXT-X-STREAM-INF") != 1 || !strings.Contains(master, ComposedRungID+"/") {
		t.Fatalf("master = %q, want only the composed rung", master)
	}
}

func TestComposedShapedSessionRefusesUnresolvedPicture(t *testing.T) {
	t.Parallel()
	for name, composer := range map[string]GenerationComposer{
		"no composer": nil,
		"declines": func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
			return ComposedPicture{}, false, nil
		},
	} {
		sessions := newTestSessions(t, &fakeTranscoder{}, t.TempDir(), nil)
		sessions.SetPictureComposer(composer)
		_, err := sessions.FilePathShaped(context.Background(), SessionKey{SessionID: "s", MediaID: "m"}, "/src/song.mp3", 30, 0, 0,
			false, false, ladderCaps{}, VideoSignal{}, -1, ffmpeg.AudioProfile{}, nil, StreamShape{ComposedPicture: true}, PriorityNormal, masterPlaylistName)
		if err := sessions.Close(); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(err, ErrComposedPictureUnavailable) {
			t.Errorf("%s: err = %v, want ErrComposedPictureUnavailable", name, err)
		}
	}
}
