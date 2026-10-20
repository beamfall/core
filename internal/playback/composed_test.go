package playback

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

const composedTestText = "[Script Info]\nScriptType: v4.00+\nPlayResX: 1280\nPlayResY: 720\n\n[V4+ Styles]\n" +
	"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n" +
	"Style: Default,Sans,64,&H0000B4FF,&H00FFFFFF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,4,2,2,60,60,0,1\n\n" +
	"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
	"Dialogue: 0,0:00:00.20,0:00:02.00,Default,,0,0,300,,{\\k20}{\\kf100}Hel{\\kf60}lo\n"

// importAudioMedia imports an audio-class Media whose source file is path.
func importAudioMedia(t *testing.T, st *store.Store, path, fingerprint string) store.Media {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, []byte("beamfall audio bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Song " + fingerprint,
		Container:           "mp3",
		Codec:               "mp3",
		Class:               store.MediaClassAudio,
		ContentFingerprint:  fingerprint,
		DurationSecondsHint: 3,
		Streams:             []store.MediaFileStream{audioStream(0, "mp3", 2)},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return media
}

func staticComposer(picture ComposedPicture, ok bool, err error) PictureComposer {
	return func(context.Context, store.Media, string) (ComposedPicture, bool, error) { return picture, ok, err }
}

// insertComposedRendition stores a Ready composed rendition keyed on picture.
func insertComposedRendition(t *testing.T, st *store.Store, media store.Media, fingerprint string, picture ComposedPicture) store.PlaybackRenditionID {
	t.Helper()
	digest, err := picture.digest()
	if err != nil {
		t.Fatal(err)
	}
	id := composedRenditionID(media.ID, fingerprint, digest)
	if err := st.UpsertPlaybackRendition(context.Background(), store.PlaybackRendition{
		ID: id, MediaID: media.ID, ContentFingerprint: fingerprint,
		Kind: store.PlaybackRenditionKindHLS, Status: store.PlaybackRenditionReady,
		RungID: ComposedRungID, Codec: composedCodec, Height: composedHeight, Bandwidth: composedBandwidth,
		Path: t.TempDir(), SizeBytes: 4096, CreatedAt: time.Unix(50, 0).UTC(), LastUsedAt: time.Unix(50, 0).UTC(),
	}); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}
	return id
}

var videoClient = CapabilityProfile{
	Client:       "beamfall-test",
	Containers:   []string{"mp4", "mp3"},
	DirectCodecs: []string{"h264", "mp3", "aac"},
}

var audioOnlyClient = CapabilityProfile{
	Client:       "beamfall-test",
	Containers:   []string{"mp3"},
	DirectCodecs: []string{"mp3", "aac"},
}

func TestInfoServesComposedPictureToVideoClient(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-composed")
	picture := ComposedPicture{TimedText: composedTestText}
	id := insertComposedRendition(t, st, media, "fp-composed", picture)
	service.SetPictureComposer(staticComposer(picture, true, nil))

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "precomputed-rendition" || len(resp.Streams) != 1 || resp.Streams[0].Renditions[0].ID != ComposedRungID {
		t.Fatalf("decision = %q streams = %#v, want the composed rendition", resp.Decision, resp.Streams)
	}
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil || grant.PlaybackRenditionID != id {
		t.Fatalf("grant = %+v err %v, want rendition %q", grant, err, id)
	}
}

func TestInfoComposedPictureFallsThrough(t *testing.T) {
	picture := ComposedPicture{TimedText: composedTestText}
	cases := map[string]struct {
		profile  CapabilityProfile
		composer PictureComposer
	}{
		"audio-only client keeps direct play":  {audioOnlyClient, staticComposer(picture, true, nil)},
		"edited picture no longer matches":     {videoClient, staticComposer(ComposedPicture{TimedText: composedTestText + "x"}, true, nil)},
		"composer declines":                    {videoClient, staticComposer(ComposedPicture{}, false, nil)},
		"composer error never breaks playback": {videoClient, staticComposer(ComposedPicture{}, false, errors.New("boom"))},
		"no composer wired":                    {videoClient, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			media := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-fallthrough")
			insertComposedRendition(t, st, media, "fp-fallthrough", picture)
			service.SetPictureComposer(c.composer)
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: c.profile})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Decision != "direct-play" {
				t.Fatalf("decision = %q, want direct-play of the bare audio", resp.Decision)
			}
		})
	}
}

// The ladder's cached-rendition path must never hand out a composed
// rendition: its picture is not the source's.
func TestLadderNeverReturnsComposedRendition(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-ladder-composed", "mkv", "hevc", 3840, 2160, []store.MediaFileStream{
		videoStream("hevc", "Main", 0, ""),
	})
	media, err := st.Media(context.Background(), mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	insertComposedRendition(t, st, media, "fp-ladder-composed", ComposedPicture{TimedText: composedTestText})
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: CapabilityProfile{
		Client: "beamfall-test", Containers: []string{"mp4"}, VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision == "precomputed-rendition" {
		t.Fatalf("ladder returned the composed rendition: %#v", resp.Streams)
	}
}

func TestComposedPictureDigest(t *testing.T) {
	a, _ := ComposedPicture{TimedText: "one"}.digest()
	b, _ := ComposedPicture{TimedText: "two"}.digest()
	if a == "" || a == b || len(a) != composedDigestLen {
		t.Fatalf("timed-text digests %q %q", a, b)
	}
	graphics := filepath.Join(t.TempDir(), "song.cdg")
	if err := os.WriteFile(graphics, make([]byte, 24), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := ComposedPicture{GraphicsPath: graphics}.digest()
	if err := os.WriteFile(graphics, make([]byte, 48), 0o600); err != nil {
		t.Fatal(err)
	}
	after, _ := ComposedPicture{GraphicsPath: graphics}.digest()
	if before == after {
		t.Fatal("an edited graphics track kept its digest")
	}
	for _, bad := range []ComposedPicture{{}, {GraphicsPath: graphics, TimedText: "x"}, {GraphicsPath: graphics + ".missing"}} {
		if _, err := bad.digest(); err == nil {
			t.Errorf("digest(%+v) accepted", bad)
		}
	}
}

func TestPrecomputeComposedRenditionEnqueue(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(500, 0).UTC()
	cache := t.TempDir()
	audio := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-enqueue")
	videoID := importProbedMedia(t, st, "fp-enqueue-video", "mp4", "h264", 1280, 720, []store.MediaFileStream{videoStream("h264", "High", 0, "")})
	picture := ComposedPicture{TimedText: composedTestText}

	jobsOut, err := PrecomputeComposedRendition(ctx, st, audio.ID, staticComposer(picture, true, nil), cache, now)
	if err != nil || len(jobsOut) != 1 {
		t.Fatalf("jobs = %v err %v, want one composed job", jobsOut, err)
	}
	digest, _ := picture.digest()
	if !strings.Contains(string(jobsOut[0].Payload), `"rungID":"composed"`) ||
		!strings.Contains(string(jobsOut[0].Payload), string(composedRenditionID(audio.ID, "fp-enqueue", digest))) {
		t.Fatalf("payload = %s", jobsOut[0].Payload)
	}
	for name, run := range map[string]func() ([]store.Job, error){
		"declined": func() ([]store.Job, error) {
			return PrecomputeComposedRendition(ctx, st, audio.ID, staticComposer(picture, false, nil), cache, now)
		},
		"no composer": func() ([]store.Job, error) { return PrecomputeComposedRendition(ctx, st, audio.ID, nil, cache, now) },
		"video media": func() ([]store.Job, error) {
			return PrecomputeComposedRendition(ctx, st, videoID, staticComposer(picture, true, nil), cache, now)
		},
	} {
		if got, err := run(); err != nil || len(got) != 0 {
			t.Errorf("%s: jobs = %v err %v, want none", name, got, err)
		}
	}
}

func TestComposedGeneratorRefusesChangedPicture(t *testing.T) {
	spec := PrecomputeSpec{
		MediaID: "m", ContentFingerprint: "fp", RungID: ComposedRungID, Codec: composedCodec,
		Height: composedHeight, Bandwidth: composedBandwidth, SourcePath: "/lib/song.mp3", OutputDir: t.TempDir(),
	}
	digest, _ := ComposedPicture{TimedText: "original"}.digest()
	spec.RenditionID = composedRenditionID("m", "fp", digest)
	for name, composer := range map[string]GenerationComposer{
		"declines now": func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
			return ComposedPicture{}, false, nil
		},
		"edited since enqueue": func(context.Context, store.MediaID, string) (ComposedPicture, bool, error) {
			return ComposedPicture{TimedText: "edited"}, true, nil
		},
	} {
		err := FFmpegPrecomputeGenerator{Composer: composer}.PrecomputeHLS(context.Background(), spec)
		if !errors.Is(err, ErrPrecomputeInputChanged) {
			t.Errorf("%s: err = %v, want ErrPrecomputeInputChanged", name, err)
		}
	}
	if err := (FFmpegPrecomputeGenerator{}).PrecomputeHLS(context.Background(), spec); err == nil {
		t.Error("composed rung generated without a composer")
	}
}

// TestComposedRenditionEndToEnd drives the real chain: explicit precompute
// trigger → the precompute job handler with the real FFmpeg generator (the
// audio-only source must pass the handler, which skips the source-video
// gate) → a Ready rendition → playbackInfo serving it to a video client.
func TestComposedRenditionEndToEnd(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	if filters, _ := exec.Command("ffmpeg", "-hide_banner", "-filters").Output(); !strings.Contains(string(filters), " ass ") {
		t.Skip("ffmpeg lacks the libass filter")
	}
	ctx := context.Background()
	service, st, scope := capabilityTestService(t)
	audioPath := filepath.Join(t.TempDir(), "song.mp3")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=duration=3", "-y", audioPath).CombinedOutput(); err != nil {
		t.Fatalf("synthesize audio: %v\n%s", err, out)
	}
	media := importAudioMedia(t, st, audioPath, "fp-e2e")
	picture := ComposedPicture{TimedText: composedTestText}
	composer := staticComposer(picture, true, nil)
	now := time.Unix(100, 0).UTC()
	cache := t.TempDir()

	queued, err := PrecomputeComposedRendition(ctx, st, media.ID, composer, cache, now)
	if err != nil || len(queued) != 1 {
		t.Fatalf("enqueue: %v %v", queued, err)
	}
	handler := PrecomputeJobHandler{
		Store: st,
		Generator: FFmpegPrecomputeGenerator{Composer: func(ctx context.Context, _ store.MediaID, path string) (ComposedPicture, bool, error) {
			return composer(ctx, media, path)
		}},
		Admission: NewPrecomputeResourceGovernor(1 << 30),
		Now:       func() time.Time { return now },
	}
	registry := jobs.NewRegistry(st, "composed-e2e")
	registry.Register(PrecomputeJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		return jobs.Result{}, handler.Handle(ctx, job)
	})
	if _, err := registry.RunOne(ctx, []store.JobClass{store.JobClassBackground}, now); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	job, err := st.Job(ctx, queued[0].ID)
	if err != nil || job.Status != store.JobStatusCompleted {
		t.Fatalf("job = %+v err %v, want completed", job, err)
	}
	ready, err := st.ReadyPlaybackRenditions(ctx, media.ID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(ready) != 1 || ready[0].RungID != ComposedRungID {
		t.Fatalf("ready = %+v err %v", ready, err)
	}
	if _, err := os.Stat(filepath.Join(ready[0].Path, ComposedRungID, composedTimedTextFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("timed-text document left in the served rung dir: %v", err)
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,height", "-of", "compact",
		filepath.Join(ready[0].Path, ComposedRungID, "index.m3u8")).CombinedOutput()
	if err != nil || !strings.Contains(string(probe), "codec_name=h264|height=720") || !strings.Contains(string(probe), "codec_name=aac") {
		t.Fatalf("composed rendition probe: %s %v", probe, err)
	}

	service.SetPictureComposer(composer)
	resp, err := service.Info(ctx, scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: videoClient})
	if err != nil || resp.Decision != "precomputed-rendition" || resp.Streams[0].Renditions[0].ID != ComposedRungID {
		t.Fatalf("Info = %+v err %v, want the composed rendition", resp, err)
	}
}

func TestEnsureComposedRendition(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(600, 0).UTC()
	cache := t.TempDir()
	audio := importAudioMedia(t, st, filepath.Join(t.TempDir(), "song.mp3"), "fp-ensure")
	picture := ComposedPicture{TimedText: composedTestText}

	added, err := EnsureComposedRendition(ctx, st, audio.ID, staticComposer(picture, true, nil), cache, now)
	if err != nil || !added {
		t.Fatalf("no rendition yet: added %v err %v, want an enqueue", added, err)
	}
	if got := countJobs(t, st, PrecomputeJobKind); got != 1 {
		t.Fatalf("precompute jobs = %d, want 1", got)
	}
	// A queued but unclaimed job dedupes: no second job.
	if _, err := EnsureComposedRendition(ctx, st, audio.ID, staticComposer(picture, true, nil), cache, now); err != nil {
		t.Fatal(err)
	}
	if got := countJobs(t, st, PrecomputeJobKind); got != 1 {
		t.Fatalf("precompute jobs after re-ensure = %d, want 1", got)
	}

	id := insertComposedRendition(t, st, audio, "fp-ensure", picture)
	for _, status := range []store.PlaybackRenditionStatus{store.PlaybackRenditionGenerating, store.PlaybackRenditionReady, store.PlaybackRenditionFailed, store.PlaybackRenditionEvicted} {
		r, err := st.PlaybackRendition(ctx, id, store.LibraryFilter{AllLibraries: true, Revealed: true})
		if err != nil {
			t.Fatal(err)
		}
		r.Status = status
		if err := st.UpsertPlaybackRendition(ctx, r); err != nil {
			t.Fatal(err)
		}
		if added, err := EnsureComposedRendition(ctx, st, audio.ID, staticComposer(picture, true, nil), cache, now); err != nil || added {
			t.Fatalf("%s rendition exists: added %v err %v, want no enqueue", status, added, err)
		}
	}

	edited := ComposedPicture{TimedText: composedTestText + "Dialogue: 0,0:00:02.00,0:00:03.00,Default,,0,0,300,,{\\kf50}again\n"}
	if added, err := EnsureComposedRendition(ctx, st, audio.ID, staticComposer(edited, true, nil), cache, now); err != nil || !added {
		t.Fatalf("edited picture: added %v err %v, want an enqueue for the new digest", added, err)
	}
	if added, err := EnsureComposedRendition(ctx, st, audio.ID, staticComposer(picture, false, nil), cache, now); err != nil || added {
		t.Fatalf("declined: added %v err %v, want none", added, err)
	}
	wantErr := errors.New("composer broke")
	if _, err := EnsureComposedRendition(ctx, st, audio.ID, staticComposer(picture, true, wantErr), cache, now); !errors.Is(err, wantErr) {
		t.Fatalf("composer error = %v, want %v", err, wantErr)
	}
}

func countJobs(t *testing.T, st *store.Store, kind store.JobKind) int {
	t.Helper()
	all, err := st.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, job := range all {
		if job.Kind == kind {
			n++
		}
	}
	return n
}
