package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestInfoRequiresMediaID(t *testing.T) {
	service, scope := newTestService(t, "session-default")
	_, err := service.Info(context.Background(), scope, InfoRequest{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	_, err = service.PreviewInfo(context.Background(), scope, PreviewInfoRequest{})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("preview error = %v, want ErrInvalidRequest", err)
	}
}

func TestCapabilityProfileTierJSON(t *testing.T) {
	raw, err := json.Marshal(CapabilityProfile{Client: "beamfall-roku", Tier: "current"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tier":"current"`) {
		t.Fatalf("profile JSON = %s, want tier", raw)
	}

	var profile CapabilityProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Client != "beamfall-roku" || profile.Tier != "current" {
		t.Fatalf("profile = %#v, want stable client with current tier", profile)
	}

	raw, err = json.Marshal(CapabilityProfile{Client: "beamfall-roku"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"tier"`) {
		t.Fatalf("zero-tier profile JSON = %s, want omitted tier", raw)
	}
}

func TestInfoReturnsDirectPlayWithStreamToken(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: "media-alpha",
		CapabilityProfile: CapabilityProfile{
			Client:       "beamfall-test",
			Containers:   []string{"mp4"},
			DirectCodecs: []string{"h264"},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.MediaID != "media-alpha" {
		t.Fatalf("MediaID = %q, want media-alpha", resp.MediaID)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if !resp.StreamTokenRequired {
		t.Fatal("StreamTokenRequired = false, want true")
	}
	// Direct-play carries the direct choice plus the manual-rendition HLS
	// pin (ADR-0010 §8: offered on direct-playable files too).
	if len(resp.Streams) != 2 {
		t.Fatalf("len(Streams) = %d, want 2 (direct + manual hls pin)", len(resp.Streams))
	}
	if resp.Streams[0].Kind != "direct-play" || resp.Streams[0].Token == "" {
		t.Fatalf("Streams[0] = %#v, want direct-play with token", resp.Streams[0])
	}
	if resp.Streams[1].Kind != "hls" || len(resp.Streams[1].Renditions) == 0 {
		t.Fatalf("Streams[1] = %#v, want hls manual pin with renditions", resp.Streams[1])
	}
	if resp.Timeline.DurationSeconds != 120 {
		t.Fatalf("DurationSeconds = %d, want 120", resp.Timeline.DurationSeconds)
	}
}

func TestInfoSignsForegroundVerdictIntoHLSGrant(t *testing.T) {
	for _, foreground := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreground=%t", foreground), func(t *testing.T) {
			service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:    "media-alpha",
				Foreground: foreground,
				CapabilityProfile: CapabilityProfile{
					Client:       "beamfall-test",
					Containers:   []string{"mp4"},
					DirectCodecs: []string{"h264"},
				},
			})
			if err != nil {
				t.Fatalf("Info returned error: %v", err)
			}
			if len(resp.Streams) != 2 || resp.Streams[1].Kind != "hls" {
				t.Fatalf("Streams = %#v, want direct-play plus HLS", resp.Streams)
			}
			grant, err := service.tokens.Verify(resp.Streams[1].Token, streamtoken.TargetMediaHLS, scope)
			if err != nil {
				t.Fatalf("Verify HLS token: %v", err)
			}
			if grant.Foreground != foreground {
				t.Fatalf("grant.Foreground = %t, want %t", grant.Foreground, foreground)
			}
		})
	}
}

// TestInfoEmitsMediaClass pins the audio/video playback class onto the
// playbackInfo wire (H2): native clients decode `mediaClass` to route
// audio-vs-video transport (background audio, lock screen, CarPlay/Android
// Auto). The class comes from store.Media.Class, known at decision time.
func TestInfoEmitsMediaClass(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")

	// Seed an audio-class item in the same unrestricted, session-visible
	// library. media-alpha is the development seed's video item.
	audioPath := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(audioPath, []byte("beamfall audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	audio, _, err := service.store.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               audioPath,
		Title:              "Sample Song",
		Container:          "mp3",
		Codec:              "mp3",
		ContentFingerprint: "sha256:mediaclass-audio",
		Class:              store.MediaClassAudio,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(audio): %v", err)
	}

	profile := CapabilityProfile{
		Client:       "beamfall-test",
		Containers:   []string{"mp4", "mp3"},
		DirectCodecs: []string{"h264", "mp3"},
	}

	videoResp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: "media-alpha", CapabilityProfile: profile})
	if err != nil {
		t.Fatalf("Info(video) returned error: %v", err)
	}
	if videoResp.MediaClass != "video" {
		t.Fatalf("video MediaClass = %q, want video", videoResp.MediaClass)
	}

	// The audio item declares the video item as its next queue entry, so the
	// next-item class carry-through is exercised in the same call.
	audioResp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(audio.ID),
		NextMediaID:       "media-alpha",
		CapabilityProfile: profile,
	})
	if err != nil {
		t.Fatalf("Info(audio) returned error: %v", err)
	}
	if audioResp.MediaClass != "audio" {
		t.Fatalf("audio MediaClass = %q, want audio", audioResp.MediaClass)
	}
	if audioResp.Next == nil {
		t.Fatal("audio response Next = nil, want the declared video next item")
	}
	if audioResp.Next.MediaClass != "video" {
		t.Fatalf("next MediaClass = %q, want video", audioResp.Next.MediaClass)
	}

	// The wire field must be `mediaClass` exactly (the key both native clients
	// decode) — guard against a struct-tag rename silently breaking routing.
	wire, err := json.Marshal(audioResp)
	if err != nil {
		t.Fatalf("marshal audio response: %v", err)
	}
	if !strings.Contains(string(wire), `"mediaClass":"audio"`) {
		t.Fatalf("wire missing mediaClass:audio: %s", wire)
	}
}

// TestInfoServesImageAsDirectBytesWithoutTranscode is the ADR-0043 image-class
// posture: a still is served as direct authenticated bytes with no HLS/transcode
// rung, even under a Capability Profile that declares none of the image's
// container (so a non-image file would fall to transcode). The single stream is
// a TargetMediaDirect byte route, and there is no timeline.
func TestInfoServesImageAsDirectBytesWithoutTranscode(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")

	imagePath := filepath.Join(t.TempDir(), "vacation.jpg")
	if err := os.WriteFile(imagePath, []byte("\xff\xd8\xff\xe0 jpeg bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	image, _, err := service.store.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               imagePath,
		Title:              "Vacation Photo",
		Container:          "jpg",
		Codec:              "mjpeg",
		ContentFingerprint: "sha256:image-direct",
		Class:              store.MediaClassImage,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(image): %v", err)
	}

	// A video-only profile: no image container is declared, so a non-image file
	// would force a transcode. The image must still come back direct.
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(image.ID),
		CapabilityProfile: CapabilityProfile{Containers: []string{"mkv"}, DirectCodecs: []string{"av1"}},
	})
	if err != nil {
		t.Fatalf("Info(image) returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if resp.MediaClass != "image" {
		t.Fatalf("MediaClass = %q, want image", resp.MediaClass)
	}
	if len(resp.Streams) != 1 {
		t.Fatalf("len(Streams) = %d, want 1 (no HLS/transcode rung for an image)", len(resp.Streams))
	}
	s := resp.Streams[0]
	if s.Kind != "direct-play" || s.Token == "" {
		t.Fatalf("Streams[0] = %#v, want direct-play with token", s)
	}
	if !strings.HasPrefix(s.URL, "/api/media/direct/") {
		t.Fatalf("Streams[0].URL = %q, want /api/media/direct/ prefix", s.URL)
	}
	if resp.Timeline.DurationSeconds != 0 || resp.Timeline.PositionSeconds != 0 {
		t.Fatalf("Timeline = %#v, want zero (an image has no timeline)", resp.Timeline)
	}
	// ADR-0098 area 2: the image path emits a one-element Parts array carrying an
	// opaque selector, never the raw content fingerprint.
	if len(resp.Parts) != 1 || resp.Parts[0].Selector == "" {
		t.Fatalf("Parts = %#v, want one Part with a non-empty selector", resp.Parts)
	}
	if blob, _ := json.Marshal(resp); strings.Contains(string(blob), "contentFingerprint") {
		t.Fatalf("playbackInfo JSON leaked a raw contentFingerprint: %s", blob)
	}
}

func TestInfoTokenExpiryOutlivesPlayback(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: "media-alpha",
		CapabilityProfile: CapabilityProfile{
			Client:       "beamfall-test",
			Containers:   []string{"mp4"},
			DirectCodecs: []string{"h264"},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, resp.Streams[0].ExpiresAt)
	if err != nil {
		t.Fatalf("parsing ExpiresAt: %v", err)
	}
	issuedAt := time.Unix(100, 0).UTC()
	// A token that dies before the media finishes breaks playback mid-stream:
	// the floor is one hour, and longer media gets at least double its runtime.
	if lifetime := expiresAt.Sub(issuedAt); lifetime < time.Hour {
		t.Fatalf("token lifetime = %v, want >= 1h", lifetime)
	}
}

func TestInfoDoesNotRevealRestrictedMedia(t *testing.T) {
	service, scope := newTestService(t, "session-default")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: "media-archive",
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" {
		t.Fatalf("Decision = %q, want unavailable", resp.Decision)
	}
	if len(resp.Streams) != 0 {
		t.Fatalf("len(Streams) = %d, want 0", len(resp.Streams))
	}
	if resp.RestrictedOrigin {
		t.Fatal("RestrictedOrigin = true leaked the hidden restricted origin")
	}
}

func TestInfoAllowsRestrictedMediaWhenRevealed(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-revealed", "media-archive")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-archive",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if !resp.RestrictedOrigin {
		t.Fatal("RestrictedOrigin = false, want true for revealed restricted Library")
	}
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal playbackInfo: %v", err)
	}
	if !strings.Contains(string(wire), `"restrictedOrigin":true`) {
		t.Fatalf("wire missing restrictedOrigin:true: %s", wire)
	}
}

func TestInfoFallsToTranscodeWhenProfileCannotDirectPlay(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mkv"}, DirectCodecs: []string{"av1"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode", resp.Decision)
	}
	if resp.Reason != "requires-transcode" {
		t.Fatalf("Reason = %q, want requires-transcode", resp.Reason)
	}
	if len(resp.Streams) != 1 || resp.Streams[0].Kind != "hls" {
		t.Fatalf("Streams = %#v, want one hls stream", resp.Streams)
	}
	if resp.Streams[0].Token == "" || !strings.HasSuffix(resp.Streams[0].URL, "/master.m3u8") {
		t.Fatalf("hls stream = %#v, want token and /master.m3u8 URL", resp.Streams[0])
	}
	// Honest rung inventory: the seeded 720p source never advertises 1080p.
	renditions := resp.Streams[0].Renditions
	if len(renditions) != 2 || renditions[0].ID != "720p" || renditions[1].ID != "480p" {
		t.Fatalf("Renditions = %#v, want [720p 480p]", renditions)
	}
	for _, rendition := range renditions {
		if rendition.Height > 720 {
			t.Fatalf("rendition %q above the 720p source", rendition.ID)
		}
		if rendition.Bandwidth <= 0 {
			t.Fatalf("rendition %q missing bandwidth", rendition.ID)
		}
	}
}

func TestInfoReturnsUnavailableWhenMediaHasNoPrimaryFile(t *testing.T) {
	service, scope := newTestService(t, "session-default")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" {
		t.Fatalf("Decision = %q, want unavailable", resp.Decision)
	}
	if resp.Reason != "media-file-unavailable" {
		t.Fatalf("Reason = %q, want media-file-unavailable", resp.Reason)
	}
	if len(resp.Streams) != 0 {
		t.Fatalf("len(Streams) = %d, want 0", len(resp.Streams))
	}
}

func newTestService(t *testing.T, sessionID store.SessionID) (*Service, auth.Scope) {
	t.Helper()
	st := storetest.Open(t)
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Scope returned error: %v", err)
	}
	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	svc := NewService(st, tokens, clock)
	svc.SetSelector(NewSelectorService([]byte("test-token-key"), clock))
	return svc, scope
}

func newTestServiceWithMediaFile(t *testing.T, sessionID store.SessionID, mediaIDs ...store.MediaID) (*Service, auth.Scope) {
	t.Helper()
	st := storetest.Open(t)
	for i, mediaID := range mediaIDs {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("media-%d.bin", i))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("beamfall bytes %d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPrimaryMediaFile(context.Background(), mediaID, path); err != nil {
			t.Fatal(err)
		}
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Scope returned error: %v", err)
	}
	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	svc := NewService(st, tokens, clock)
	svc.SetSelector(NewSelectorService([]byte("test-token-key"), clock))
	return svc, scope
}

func TestInfoIssuesRestrictedTokenForRestrictedLibraryMedia(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-revealed", "media-archive")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-archive",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}

	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	grant, err := tokens.Verify(resp.Streams[0].Token, streamtoken.TargetMediaDirect, scope)
	if err != nil {
		t.Fatalf("Verify with revealed scope returned error: %v", err)
	}
	if !grant.Restricted {
		t.Fatal("grant for restricted-Library media is not marked Restricted")
	}
	relocked := scope
	relocked.Revealed = false
	if _, err := tokens.Verify(resp.Streams[0].Token, streamtoken.TargetMediaDirect, relocked); err == nil {
		t.Fatal("restricted token verified after relock")
	}
}

func TestInfoIssuesUnrestrictedTokenForLocalLibraryMedia(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-revealed", "media-alpha")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}

	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	relocked := scope
	relocked.Revealed = false
	grant, err := tokens.Verify(resp.Streams[0].Token, streamtoken.TargetMediaDirect, relocked)
	if err != nil {
		t.Fatalf("unrestricted token rejected after relock: %v", err)
	}
	if grant.Restricted {
		t.Fatal("grant for unrestricted media is marked Restricted")
	}
}

func TestInfoIssuesNextItemTokenForVisiblePlayableMedia(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-revealed", "media-alpha", "media-archive")

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		NextMediaID:       "media-archive",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if resp.Next == nil {
		t.Fatal("Next = nil, want ahead-of-time next-item decision")
	}
	if resp.Next.MediaID != "media-archive" {
		t.Fatalf("Next.MediaID = %q, want media-archive", resp.Next.MediaID)
	}
	if resp.Next.Decision != "direct-play" {
		t.Fatalf("Next.Decision = %q, want direct-play", resp.Next.Decision)
	}
	if len(resp.Next.Streams) != 2 {
		t.Fatalf("len(Next.Streams) = %d, want 2 (direct + manual hls pin)", len(resp.Next.Streams))
	}
	next := resp.Next.Streams[0]
	if next.ID != "direct-next" {
		t.Fatalf("Next stream ID = %q, want direct-next", next.ID)
	}
	if next.Token == "" || next.Token == resp.Streams[0].Token {
		t.Fatalf("Next stream token = %q, want a distinct non-empty token", next.Token)
	}
	if next.ExpiresAt != resp.Streams[0].ExpiresAt {
		t.Fatalf("Next ExpiresAt = %q, want same lifetime as current item %q", next.ExpiresAt, resp.Streams[0].ExpiresAt)
	}

	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	grant, err := tokens.Verify(next.Token, streamtoken.TargetMediaDirect, scope)
	if err != nil {
		t.Fatalf("Verify next token returned error: %v", err)
	}
	if grant.MediaID != "media-archive" {
		t.Fatalf("next grant MediaID = %q, want media-archive", grant.MediaID)
	}
	if !grant.Restricted {
		t.Fatal("next grant for restricted-Library media is not marked Restricted")
	}
}

func TestInfoDoesNotLeakRestrictedNextItemToUnrevealedScope(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha", "media-archive")
	caps := CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}}

	restricted, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: "media-alpha", NextMediaID: "media-archive", CapabilityProfile: caps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	unknown, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: "media-alpha", NextMediaID: "media-no-such", CapabilityProfile: caps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if restricted.Next != nil {
		t.Fatal("Next issued for media the scope cannot see")
	}
	restrictedJSON, _ := json.Marshal(restricted)
	unknownJSON, _ := json.Marshal(unknown)
	if string(restrictedJSON) != string(unknownJSON) {
		t.Fatalf("restricted next item is distinguishable from unknown ID:\n%s\n%s", restrictedJSON, unknownJSON)
	}
}

func TestInfoIgnoresMissingOrGarbageNextMediaID(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")

	for _, nextID := range []string{"", "   ", "no-such-media", "media-archive"} {
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:           "media-alpha",
			NextMediaID:       nextID,
			CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
		})
		if err != nil {
			t.Fatalf("Info(next=%q) returned error: %v", nextID, err)
		}
		if resp.Decision != "direct-play" {
			t.Fatalf("Info(next=%q) Decision = %q, want direct-play for current item", nextID, resp.Decision)
		}
		if len(resp.Streams) == 0 || resp.Streams[0].Token == "" {
			t.Fatalf("Info(next=%q) current stream missing", nextID)
		}
		if resp.Next != nil {
			t.Fatalf("Info(next=%q) Next = %+v, want nil", nextID, resp.Next)
		}
	}
}

// fakeAssetsLocator reports asset availability per fingerprint.
type fakeAssetsLocator struct {
	trickplay bool
	preview   bool
	// loudness, when set, is returned by LoudnessForFingerprint/LoudnessAsset so a
	// test can drive the rung-specific normalization state.
	loudness        *LoudnessData
	loudnessIs      bool
	waveform        bool
	beatGrid        bool
	beatGridLookups *int
	syncLanes       bool
	silenceMap      bool
}

func (f fakeAssetsLocator) TrickplayAsset(string) (string, bool) {
	if !f.trickplay {
		return "", false
	}
	return "trickplay.json", true
}

func (f fakeAssetsLocator) PreviewClipAsset(string) (string, bool) {
	if !f.preview {
		return "", false
	}
	return "preview.mp4", true
}

func (f fakeAssetsLocator) LoudnessAsset(string) (string, bool) {
	if f.loudness == nil {
		return "", false
	}
	return "loudness.json", true
}

func (f fakeAssetsLocator) LoudnessForFingerprint(string) (LoudnessData, bool) {
	if f.loudness == nil {
		return LoudnessData{}, false
	}
	return *f.loudness, true
}

func (f fakeAssetsLocator) WaveformAsset(string) (string, bool) {
	if !f.waveform {
		return "", false
	}
	return "waveform.json", true
}

func (f fakeAssetsLocator) BeatGridAsset(string) (string, bool) {
	if f.beatGridLookups != nil {
		(*f.beatGridLookups)++
	}
	if !f.beatGrid {
		return "", false
	}
	return "beatgrid.json", true
}

func (f fakeAssetsLocator) SyncLanesAsset(string) (string, bool) {
	if !f.syncLanes {
		return "", false
	}
	return "sync-lanes.json", true
}

func (f fakeAssetsLocator) SilenceMapAsset(string) (string, bool) {
	if !f.silenceMap {
		return "", false
	}
	return "silence-map.json", true
}

func TestInfoAdvertisesGeneratedAssets(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	service.SetAssets(fakeAssetsLocator{trickplay: true, preview: true})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Assets == nil {
		t.Fatal("Assets is nil, want advertisement")
	}
	if !strings.HasPrefix(resp.Assets.Trickplay, "/api/media/assets/") ||
		!strings.HasSuffix(resp.Assets.Trickplay, "/trickplay.json") {
		t.Fatalf("Trickplay = %q, want token-bearing assets URL", resp.Assets.Trickplay)
	}
	if !strings.HasSuffix(resp.Assets.PreviewClip, "/preview.mp4") {
		t.Fatalf("PreviewClip = %q, want preview clip URL", resp.Assets.PreviewClip)
	}
	if !strings.HasPrefix(resp.Assets.MarkerThumbnailBase, "/api/media/assets/") ||
		!strings.HasSuffix(resp.Assets.MarkerThumbnailBase, "/") {
		t.Fatalf("MarkerThumbnailBase = %q, want assets URL prefix", resp.Assets.MarkerThumbnailBase)
	}
	if resp.Assets.ExpiresAt == "" {
		t.Fatal("Assets.ExpiresAt is empty")
	}
	// The embedded token must be assets-target: it unlocks asset bytes only.
	token := strings.TrimSuffix(strings.TrimPrefix(resp.Assets.MarkerThumbnailBase, "/api/media/assets/"), "/")
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	if _, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaAssets); err != nil {
		t.Fatalf("assets token did not verify for the assets target: %v", err)
	}
	if _, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaDirect); err == nil {
		t.Fatal("assets token verified for the direct target")
	}
}

// TestInfoAdvertisesAudioAssetsWhenPresent: the first-class optional audio
// Generated Asset URLs (waveform/beatGrid/silenceMap) are advertised only when
// the asset exists, and omitted otherwise (ADR-0098 area 2).
func TestInfoAdvertisesAudioAssetsWhenPresent(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	service.SetAssets(fakeAssetsLocator{waveform: true, silenceMap: true}) // beatGrid absent

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Assets == nil {
		t.Fatal("Assets is nil, want advertisement")
	}
	if !strings.HasSuffix(resp.Assets.Waveform, "/waveform.json") {
		t.Fatalf("Waveform = %q, want token-bearing waveform URL", resp.Assets.Waveform)
	}
	if !strings.HasSuffix(resp.Assets.SilenceMap, "/silence-map.json") {
		t.Fatalf("SilenceMap = %q, want silence-map URL", resp.Assets.SilenceMap)
	}
	if resp.Assets.BeatGrid != "" {
		t.Fatalf("BeatGrid = %q, want empty (asset absent → field omitted)", resp.Assets.BeatGrid)
	}
}

// TestInfoAdvertisesBeatGridWhenPresent: when the beat grid Generated Asset
// exists the Info response carries a Stream-Token-bearing URL in Assets.BeatGrid;
// when it does not exist the field is omitted so clients degrade gracefully to
// non-beat-synced behavior.
func TestInfoAdvertisesBeatGridWhenPresent(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	service.SetAssets(fakeAssetsLocator{beatGrid: true})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Assets == nil {
		t.Fatal("Assets is nil, want advertisement")
	}
	if !strings.HasSuffix(resp.Assets.BeatGrid, "/beatgrid.json") {
		t.Fatalf("BeatGrid = %q, want token-bearing beatgrid.json URL", resp.Assets.BeatGrid)
	}
	// Absent assets must remain omitted (graceful degradation).
	if resp.Assets.Waveform != "" {
		t.Fatalf("Waveform = %q, want empty (absent → omitted)", resp.Assets.Waveform)
	}
}

func TestInfoAdvertisesSyncLanesWhenPresent(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	service.SetAssets(fakeAssetsLocator{syncLanes: true})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Assets == nil {
		t.Fatal("Assets is nil, want advertisement")
	}
	if !strings.HasSuffix(resp.Assets.SyncLanes, "/sync-lanes.json") {
		t.Fatalf("SyncLanes = %q, want token-bearing sync-lanes.json URL", resp.Assets.SyncLanes)
	}
}

func TestInfoDoesNotGenerateBeatGridOnPlaybackPath(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	beatGridLookups := 0
	service.SetAssets(fakeAssetsLocator{beatGridLookups: &beatGridLookups})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play with absent beat grid", resp.Decision)
	}
	if resp.Assets == nil || resp.Assets.BeatGrid != "" {
		t.Fatalf("Assets = %+v, want absent beat grid fallback", resp.Assets)
	}
	if beatGridLookups != 1 {
		t.Fatalf("BeatGridAsset lookups = %d, want one availability check and no generation hook", beatGridLookups)
	}
}

// TestInfoSealedLibraryServesNothing: a Sealed Library's Media is unavailable —
// no streams, no selector-bearing Parts, no asset advertisement (ADR-0098 area 2).
func TestInfoSealedLibraryServesNothing(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	service.SetAssets(fakeAssetsLocator{trickplay: true, waveform: true})
	// media-alpha lives in library-local; seal that Library.
	service.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" {
		t.Fatalf("Decision = %q, want unavailable for a sealed Library", resp.Decision)
	}
	if len(resp.Streams) != 0 || resp.Assets != nil || len(resp.Parts) != 0 {
		t.Fatalf("sealed Media leaked playback surface: streams=%d assets=%v parts=%d", len(resp.Streams), resp.Assets, len(resp.Parts))
	}
}

func TestPreviewInfoSealedLibraryServesNothing(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	service.SetAssets(fakeAssetsLocator{preview: true})
	service.SetSealed(func(libraryID store.LibraryID) bool { return libraryID == "library-local" })

	resp, err := service.PreviewInfo(context.Background(), scope, PreviewInfoRequest{MediaID: "media-alpha"})
	if err != nil {
		t.Fatalf("PreviewInfo returned error: %v", err)
	}
	if resp.MediaID != "" || resp.Assets != nil {
		t.Fatalf("sealed preview leaked surface: %+v", resp)
	}
}

func TestInfoSurfacesSidecarCaptionTracks(t *testing.T) {
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("movie bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Import a local-Library media file carrying two sidecar caption tracks:
	// a forced English srt and an SDH Spanish vtt.
	fingerprint := "sha256:feat4sidecartest"
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: fingerprint,
		Sidecars: []store.MediaFileSidecarCaption{
			{RelPath: "movie.en.forced.srt", Format: "srt", Language: "en", Forced: true},
			{RelPath: "movie.es.sdh.vtt", Format: "vtt", Language: "es", SDH: true},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	service := NewService(st, tokens, func() time.Time { return now })

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(media.ID),
		CapabilityProfile: CapabilityProfile{
			Client:          "beamfall-web",
			Containers:      []string{"mkv", "mp4"},
			DirectCodecs:    []string{"h264"},
			SubtitleFormats: []string{"srt", "webvtt"},
			DownstreamAudio: "stereo",
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play for SRT sidecar on web profile", resp.Decision)
	}
	if len(resp.CaptionTracks) != 2 {
		t.Fatalf("CaptionTracks = %#v, want 2", resp.CaptionTracks)
	}
	forced := resp.CaptionTracks[0]
	if forced.Language != "en" || !forced.Forced || forced.Format != "srt" {
		t.Fatalf("track[0] = %+v, want en/forced/srt", forced)
	}
	sdh := resp.CaptionTracks[1]
	if sdh.Language != "es" || !sdh.SDH || sdh.Format != "vtt" {
		t.Fatalf("track[1] = %+v, want es/sdh/vtt", sdh)
	}
	for i, track := range resp.CaptionTracks {
		if !strings.HasPrefix(track.URL, "/api/media/sidecar/") || !strings.HasSuffix(track.URL, fmt.Sprintf("/%d.vtt", i)) {
			t.Fatalf("track[%d].URL = %q, want token-bearing sidecar URL ending /%d.vtt", i, track.URL, i)
		}
	}
	// The embedded token is sidecar-target only.
	token := strings.TrimSuffix(strings.TrimPrefix(resp.CaptionTracks[0].URL, "/api/media/sidecar/"), "/0.vtt")
	if _, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaSidecar); err != nil {
		t.Fatalf("sidecar token did not verify for the sidecar target: %v", err)
	}
	if _, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaDirect); err == nil {
		t.Fatal("sidecar token verified for the direct target")
	}
}

// A declared-`ass` (libass) profile gets a raw-ASS passthrough URL (".ass") for
// an .ass sidecar; a non-libass profile gets the flattened WebVTT URL (".vtt")
// for the same sidecar.
func TestInfoASSDeliveryByProfileCapability(t *testing.T) {
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("movie bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:asssidecartest",
		Sidecars: []store.MediaFileSidecarCaption{
			{RelPath: "movie.en.ass", Format: "ass", Language: "en"},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	service := NewService(st, tokens, func() time.Time { return now })

	base := CapabilityProfile{
		Client:          "beamfall-web",
		Containers:      []string{"mkv", "mp4"},
		DirectCodecs:    []string{"h264"},
		DownstreamAudio: "stereo",
	}
	for _, tc := range []struct {
		name            string
		subtitleFormats []string
		wantSuffix      string
		wantRawASS      bool
	}{
		{"libass profile", []string{"srt", "webvtt", "ass"}, "/0.ass", true},
		{"non-libass profile", []string{"srt", "webvtt"}, "/0.vtt", false},
	} {
		profile := base
		profile.SubtitleFormats = tc.subtitleFormats
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: profile})
		if err != nil {
			t.Fatalf("%s: Info error: %v", tc.name, err)
		}
		if len(resp.CaptionTracks) != 1 {
			t.Fatalf("%s: CaptionTracks = %#v, want 1", tc.name, resp.CaptionTracks)
		}
		if url := resp.CaptionTracks[0].URL; !strings.HasSuffix(url, tc.wantSuffix) {
			t.Fatalf("%s: URL = %q, want suffix %q", tc.name, url, tc.wantSuffix)
		}
		if resp.CaptionTracks[0].Format != "ass" {
			t.Fatalf("%s: Format = %q, want ass (source format preserved)", tc.name, resp.CaptionTracks[0].Format)
		}
		tokenAndPath := strings.TrimPrefix(resp.CaptionTracks[0].URL, "/api/media/sidecar/")
		token, _, ok := strings.Cut(tokenAndPath, "/")
		if !ok {
			t.Fatalf("%s: malformed sidecar URL %q", tc.name, resp.CaptionTracks[0].URL)
		}
		grant, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaSidecar)
		if err != nil {
			t.Fatalf("%s: sidecar token did not verify: %v", tc.name, err)
		}
		if grant.SidecarRawASS != tc.wantRawASS {
			t.Fatalf("%s: SidecarRawASS = %v, want %v", tc.name, grant.SidecarRawASS, tc.wantRawASS)
		}
	}
}

// Info stamps the server's authoritative-at-start subtitle pre-selection,
// and a pinned series-scoped choice beats the profile policy.
func TestInfoStampsSelectedSubtitleTrack(t *testing.T) {
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("movie bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:preselecttest",
		Sidecars: []store.MediaFileSidecarCaption{
			{RelPath: "movie.en.srt", Format: "srt", Language: "en"},
			{RelPath: "movie.fr.srt", Format: "srt", Language: "fr"},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if _, err := st.UpdateProfileSettings(context.Background(), "profile-default", store.ProfileSettings{
		SubtitleLanguages: &[]string{"en"},
		SubtitleMode:      ptrSubtitleMode(store.SubtitleModeAlways),
	}); err != nil {
		t.Fatalf("UpdateProfileSettings: %v", err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	service := NewService(st, tokens, func() time.Time { return now })
	profile := CapabilityProfile{
		Client: "beamfall-web", Containers: []string{"mkv"},
		DirectCodecs: []string{"h264"}, SubtitleFormats: []string{"srt", "webvtt"},
		DownstreamAudio: "stereo",
	}

	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: profile})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.SelectedSubtitleTrack == nil {
		t.Fatal("SelectedSubtitleTrack = nil, want the preferred-language pre-selection")
	}
	if resp.SelectedSubtitleTrack.Track.Language != "en" {
		t.Fatalf("pre-selected language = %q, want en", resp.SelectedSubtitleTrack.Track.Language)
	}
	if resp.SelectedSubtitleTrack.Track.Kind != SubtitleTrackKindSidecar {
		t.Fatalf("pre-selected kind = %q, want sidecar", resp.SelectedSubtitleTrack.Track.Kind)
	}

	// Pin the French track for this item's group: the pin now beats the profile
	// preference for English.
	groupKey, err := st.MediaSubtitleGroup(context.Background(), media.ID)
	if err != nil {
		t.Fatalf("MediaSubtitleGroup: %v", err)
	}
	if err := st.PutSubtitleChoice(context.Background(), scope.ProfileID, store.SubtitleChoiceRecord{
		GroupKey: groupKey, Selected: true, SubLanguage: "fr", SubKind: SubtitleTrackKindSidecar,
	}, now); err != nil {
		t.Fatalf("PutSubtitleChoice: %v", err)
	}
	pinnedResp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: profile})
	if err != nil {
		t.Fatalf("Info after pin: %v", err)
	}
	if pinnedResp.SelectedSubtitleTrack == nil || pinnedResp.SelectedSubtitleTrack.Track.Language != "fr" {
		t.Fatalf("pinned pre-selection = %+v, want the French track", pinnedResp.SelectedSubtitleTrack)
	}
}

func ptrSubtitleMode(m store.SubtitleMode) *store.SubtitleMode { return &m }

func TestInfoSurfacesMuxedCaptionTrackDispositions(t *testing.T) {
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("movie bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:muxedsdhtest",
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "video", Codec: "h264"},
			{Index: 1, Type: "subtitle", Codec: "subrip", Language: "eng", HearingImpaired: true, VisualImpaired: true, Commentary: true},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}

	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	sdh := true
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{PreferSDH: &sdh})
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	service := NewService(st, tokens, func() time.Time { return now })

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(media.ID),
		CapabilityProfile: CapabilityProfile{
			Client:          "beamfall-web",
			Containers:      []string{"mkv", "mp4"},
			DirectCodecs:    []string{"h264"},
			SubtitleFormats: []string{"srt", "subrip", "webvtt"},
			DownstreamAudio: "stereo",
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if !resp.TrackPreferences.PreferSDH {
		t.Fatal("PreferSDH = false, want true")
	}
	if len(resp.CaptionTracks) != 1 {
		t.Fatalf("CaptionTracks = %#v, want 1 muxed track", resp.CaptionTracks)
	}
	track := resp.CaptionTracks[0]
	if track.StreamIndex == nil || *track.StreamIndex != 1 || !strings.HasSuffix(track.URL, "/1.vtt") {
		t.Fatalf("muxed track addressing = index %v url %q, want stream index 1 and a /1.vtt subtitle URL", track.StreamIndex, track.URL)
	}
	if track.Language != "eng" || !track.SDH || !track.VisualImpaired || !track.Commentary || track.Format != "subrip" {
		t.Fatalf("muxed track = %+v, want eng/SDH/visual-impaired/commentary/subrip", track)
	}
}

// TestInfoMintsMuxedTextSubtitleGrants covers CORE-MUXED-SUBTITLE-GRANT-1: a
// transcoded MKV with muxed srt + ass tracks advertises both with a URL under
// the media-subtitle target whose token verifies for that file, while a muxed
// PGS track keeps no URL.
func TestInfoMintsMuxedTextSubtitleGrants(t *testing.T) {
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("movie bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "hevc",
		ContentFingerprint: "sha256:muxedgranttest",
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc"},
			{Index: 1, Type: "audio", Codec: "aac"},
			{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng"},
			{Index: 3, Type: "subtitle", Codec: "ass", Language: "jpn"},
			{Index: 4, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "eng"},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	service := NewService(st, tokens, func() time.Time { return now })

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(media.ID),
		CapabilityProfile: CapabilityProfile{
			Client:          "beamfall-web",
			Containers:      []string{"mp4"},
			DirectCodecs:    []string{"h264"},
			SubtitleFormats: []string{"webvtt"},
			DownstreamAudio: "stereo",
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode (hevc in mkv against an h264/mp4 profile)", resp.Decision)
	}
	if len(resp.CaptionTracks) != 3 {
		t.Fatalf("CaptionTracks = %#v, want srt + ass + pgs", resp.CaptionTracks)
	}
	byIndex := map[int]CaptionTrack{}
	for _, track := range resp.CaptionTracks {
		if track.StreamIndex == nil {
			t.Fatalf("muxed track %q has no StreamIndex", track.ID)
		}
		byIndex[*track.StreamIndex] = track
	}
	if url := byIndex[4].URL; url != "" {
		t.Fatalf("PGS track URL = %q, want none (bitmap keeps burn-in/withhold routing)", url)
	}
	seenTokens := map[string]bool{}
	for _, index := range []int{2, 3} {
		track := byIndex[index]
		prefix := "/api/media/subtitle/"
		suffix := fmt.Sprintf("/%d.vtt", index)
		if !strings.HasPrefix(track.URL, prefix) || !strings.HasSuffix(track.URL, suffix) {
			t.Fatalf("track %d URL = %q, want %s<token>%s", index, track.URL, prefix, suffix)
		}
		token := strings.TrimSuffix(strings.TrimPrefix(track.URL, prefix), suffix)
		if seenTokens[token] {
			t.Fatalf("track %d reused another muxed track's token; want one grant per text stream", index)
		}
		seenTokens[token] = true
		grant, err := tokens.UnverifiedGrant(token, streamtoken.TargetMediaSubtitle)
		if err != nil {
			t.Fatalf("track %d token does not verify as media-subtitle: %v", index, err)
		}
		if grant.MediaID != media.ID || grant.SessionID != scope.SessionID {
			t.Fatalf("track %d grant = %+v, want media %s session %s", index, grant, media.ID, scope.SessionID)
		}
		if grant.SubtitleStreamIndex != index+1 {
			t.Fatalf("track %d grant subtitle index = %d, want encoded index %d", index, grant.SubtitleStreamIndex, index+1)
		}
		if grant.SubtitleFileFingerprint != "sha256:muxedgranttest" {
			t.Fatalf("track %d grant file fingerprint = %q, want negotiated file sha256:muxedgranttest", index, grant.SubtitleFileFingerprint)
		}
		if !grant.ExpiresAt.Equal(resp.Streams[0].expiresAt(t)) {
			t.Fatalf("track %d grant expiry %v, want the playback token expiry %v", index, grant.ExpiresAt, resp.Streams[0].expiresAt(t))
		}
	}
}

// expiresAt parses the stream choice's RFC3339 expiry for comparison with a
// grant's ExpiresAt.
func (c StreamChoice) expiresAt(t *testing.T) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		t.Fatalf("ExpiresAt %q: %v", c.ExpiresAt, err)
	}
	return parsed
}

func TestInfoAdvertisesGeneratedAssetsForNextItem(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-revealed", "media-alpha", "media-archive")
	service.SetAssets(fakeAssetsLocator{trickplay: true, preview: true})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		NextMediaID:       "media-archive",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Next == nil {
		t.Fatal("Next = nil, want next-item decision")
	}
	if resp.Next.Assets == nil {
		t.Fatal("Next.Assets is nil, want generated asset advertisement")
	}
	if !strings.HasPrefix(resp.Next.Assets.MarkerThumbnailBase, "/api/media/assets/") ||
		!strings.HasSuffix(resp.Next.Assets.Trickplay, "/trickplay.json") ||
		!strings.HasSuffix(resp.Next.Assets.PreviewClip, "/preview.mp4") {
		t.Fatalf("Next.Assets = %+v, want token-bearing generated asset URLs", resp.Next.Assets)
	}
	if resp.Assets == nil || resp.Next.Assets.MarkerThumbnailBase == resp.Assets.MarkerThumbnailBase {
		t.Fatalf("next asset token reused current item token: current=%+v next=%+v", resp.Assets, resp.Next.Assets)
	}
}

func TestInfoOmitsAssetsWithoutLocatorOrFiles(t *testing.T) {
	// No locator wired: the field stays omitted (wire shape unchanged).
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Assets != nil {
		t.Fatalf("Assets = %+v, want nil without a locator", resp.Assets)
	}

	// Locator wired but no assets generated yet: the block carries the token
	// base (markers are always-on) with no trickplay/preview URLs.
	service.SetAssets(fakeAssetsLocator{})
	resp, err = service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Assets == nil || resp.Assets.Trickplay != "" || resp.Assets.PreviewClip != "" {
		t.Fatalf("Assets = %+v, want base-only block", resp.Assets)
	}
}

// TestSelectedSubtitleTrackCarriesCoOccurringRoles pins the role SET decision:
// forced and SDH co-occur on real tracks, so the ref mirrors CaptionTrack's four
// independent booleans instead of collapsing to one role string. A single-role
// shape would silently drop one of these.
func TestSelectedSubtitleTrackCarriesCoOccurringRoles(t *testing.T) {
	selected := SelectedSubtitleTrack{
		Track: SubtitleTrackRef{
			Language: "eng",
			Kind:     SubtitleTrackKindSidecar,
			Roles:    SubtitleTrackRoles{Forced: true, SDH: true},
		},
		Reason: "profile-prefers-sdh",
	}

	raw, err := json.Marshal(selected)
	if err != nil {
		t.Fatalf("marshal SelectedSubtitleTrack: %v", err)
	}
	var round SelectedSubtitleTrack
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshal SelectedSubtitleTrack: %v", err)
	}
	if !round.Track.Roles.Forced || !round.Track.Roles.SDH {
		t.Fatalf("roles = %+v, want forced AND sdh both preserved", round.Track.Roles)
	}
	if round.Track.Roles.VisualImpaired || round.Track.Roles.Commentary {
		t.Fatalf("roles = %+v, want the unset roles to stay unset", round.Track.Roles)
	}
	if round.Track.Language != "eng" || round.Track.Kind != SubtitleTrackKindSidecar {
		t.Fatalf("track = %+v, want the language+kind identity preserved", round.Track)
	}
	if round.Reason != "profile-prefers-sdh" {
		t.Fatalf("reason = %q, want the human-readable reason preserved", round.Reason)
	}
}

// TestSelectedSubtitleTrackIsNotTheFfprobeIndex pins ADR-0194 §3: the ref is the
// format-independent tuple, and the unstable request-side ffprobe stream index
// must not leak into it under any key.
func TestSelectedSubtitleTrackIsNotTheFfprobeIndex(t *testing.T) {
	raw, err := json.Marshal(SelectedSubtitleTrack{
		Track: SubtitleTrackRef{Language: "eng", Kind: SubtitleTrackKindMuxed},
	})
	if err != nil {
		t.Fatalf("marshal SelectedSubtitleTrack: %v", err)
	}
	for _, leak := range []string{"selectedSubtitleIndex", "streamIndex", "index"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("selectedSubtitleTrack carries %q: %s", leak, raw)
		}
	}
}

// TestSubtitleFieldsAreOmittedWhenUnset keeps the change additive: an
// InfoResponse that states neither subtitle field serialises exactly as before.
func TestSubtitleFieldsAreOmittedWhenUnset(t *testing.T) {
	raw, err := json.Marshal(InfoResponse{MediaID: "media-alpha", Decision: "direct-play"})
	if err != nil {
		t.Fatalf("marshal InfoResponse: %v", err)
	}
	for _, key := range []string{"selectedSubtitleTrack", "subtitlesUnavailableReason"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("InfoResponse emitted %q with nothing to state: %s", key, raw)
		}
	}
}

// A bitmap sidecar is served as raw bytes, so a client that declares no renderer
// for its family cannot draw it. Advertising it anyway produces a PHANTOM track:
// the menu offers it, the pre-selection stamps it, the byte route serves raw
// PGS, and the viewer sees "subtitles on" with nothing on screen. It must be
// withheld from BOTH the advertisement and the pre-selection, and the absence
// must be stated.
func TestInfoWithholdsBitmapSidecarsAClientCannotRender(t *testing.T) {
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(path, []byte("movie bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "movie",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:bitmapsidecartest",
		Sidecars: []store.MediaFileSidecarCaption{
			{RelPath: "movie.en.sup", Format: "sup", Language: "en"},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if _, err := st.UpdateProfileSettings(context.Background(), "profile-default", store.ProfileSettings{
		SubtitleLanguages: &[]string{"en"},
		SubtitleMode:      ptrSubtitleMode(store.SubtitleModeAlways),
	}); err != nil {
		t.Fatalf("UpdateProfileSettings: %v", err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	service := NewService(st, tokens, func() time.Time { return now })

	web := CapabilityProfile{
		Client: "beamfall-web", Containers: []string{"mkv"},
		DirectCodecs: []string{"h264"}, SubtitleFormats: []string{"vtt"},
		DownstreamAudio: "stereo",
	}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: web})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	for _, track := range resp.CaptionTracks {
		if track.Format == "sup" {
			t.Fatalf("web profile was advertised a PGS sidecar it cannot render: %+v", track)
		}
	}
	if resp.SelectedSubtitleTrack != nil {
		t.Fatalf("SelectedSubtitleTrack = %+v, want none — the only track is unrenderable", resp.SelectedSubtitleTrack)
	}
	if resp.SubtitlesUnavailableReason != "no-client-renderable-subtitle-track" {
		t.Fatalf("SubtitlesUnavailableReason = %q, want the withheld-caption notice", resp.SubtitlesUnavailableReason)
	}

	// The same item on a PGS-capable profile keeps the track and pre-selects it.
	native := web
	native.Client = "beamfall-desktop"
	native.SubtitleFormats = []string{"vtt", "pgs"}
	nativeResp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: native})
	if err != nil {
		t.Fatalf("Info (pgs-capable): %v", err)
	}
	if len(nativeResp.CaptionTracks) != 1 || nativeResp.CaptionTracks[0].Format != "sup" {
		t.Fatalf("pgs-capable caption tracks = %+v, want the PGS sidecar", nativeResp.CaptionTracks)
	}
	if nativeResp.SelectedSubtitleTrack == nil {
		t.Fatal("pgs-capable SelectedSubtitleTrack = nil, want the PGS sidecar pre-selected")
	}
	if nativeResp.SubtitlesUnavailableReason != "" {
		t.Fatalf("pgs-capable SubtitlesUnavailableReason = %q, want empty", nativeResp.SubtitlesUnavailableReason)
	}
}
