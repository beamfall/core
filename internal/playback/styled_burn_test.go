package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback/subtitleingest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func withTextSubtitleBurnIn(t *testing.T, supported bool) {
	t.Helper()
	previous := textSubtitleBurnInSupported
	textSubtitleBurnInSupported = func(context.Context) bool { return supported }
	t.Cleanup(func() { textSubtitleBurnInSupported = previous })
}

// TestStyledSubtitleBurnInOnOverlayProfile: with a libass-capable FFmpeg, a
// selected ASS track on a client that cannot render ASS is burned into the
// picture, so positioned signs and karaoke survive. A client that declares
// "ass" keeps rendering it itself.
func TestStyledSubtitleBurnInOnOverlayProfile(t *testing.T) {
	withTextSubtitleBurnIn(t, true)
	assIndex := 2
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-mkv-ass-burn", "matroska", "h264", 1920, 1080, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""),
		audioStream(1, "aac", 2),
		{Index: assIndex, Type: "subtitle", Codec: "ass", Language: "jpn"},
		{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng"},
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID), CapabilityProfile: appleOverlayProfile, SelectedSubtitleIndex: &assIndex,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q (%q), want transcode to burn in the ASS track", resp.Decision, resp.Reason)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS token: %v", err)
	}
	if got := grant.BurnSubtitleIndex - 1; got != assIndex {
		t.Fatalf("BurnSubtitleIndex = %d, want %d", got, assIndex)
	}
	// The burned ASS track is advertised without a WebVTT URL, so a native
	// client routes it to burn-in; the plain SRT track keeps its sidecar.
	assertCaptionURLs(t, resp.CaptionTracks, map[int]bool{assIndex: false, 3: true})

	assProfile := appleOverlayProfile
	assProfile.SubtitleFormats = []string{"webvtt", "ass"}
	resp, err = service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID), CapabilityProfile: assProfile, SelectedSubtitleIndex: &assIndex,
	})
	if err != nil {
		t.Fatalf("Info (ass profile): %v", err)
	}
	if resp.Decision != "direct-stream" {
		t.Fatalf("Decision = %q (%q), want direct-stream for a client that renders ASS", resp.Decision, resp.Reason)
	}
	assertCaptionURLs(t, resp.CaptionTracks, map[int]bool{assIndex: true, 3: true})

	// A sidecar consumer that declares webvtt (a Cast receiver) cannot request
	// burn-in, so it keeps the flattened sidecar rather than losing the track.
	castProfile := appleOverlayProfile
	castProfile.SubtitleFormats = []string{"webvtt"}
	resp, err = service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: castProfile})
	if err != nil {
		t.Fatalf("Info (cast profile): %v", err)
	}
	assertCaptionURLs(t, resp.CaptionTracks, map[int]bool{assIndex: true, 3: true})

	// A profile that may not transcode could never receive the burn, so it
	// keeps the flattened sidecar too.
	disallowed := false
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &disallowed})
	resp, err = service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: appleOverlayProfile})
	if err != nil {
		t.Fatalf("Info (transcode disallowed): %v", err)
	}
	assertCaptionURLs(t, resp.CaptionTracks, map[int]bool{assIndex: true, 3: true})
	allowed := true
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &allowed})

	// Without libass the ASS track keeps its flattened WebVTT sidecar.
	withTextSubtitleBurnIn(t, false)
	resp, err = service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: appleOverlayProfile})
	if err != nil {
		t.Fatalf("Info (no libass): %v", err)
	}
	assertCaptionURLs(t, resp.CaptionTracks, map[int]bool{assIndex: true, 3: true})
}

// assertCaptionURLs checks, per muxed stream index, whether its caption track
// carries a WebVTT URL.
func assertCaptionURLs(t *testing.T, tracks []CaptionTrack, wantURL map[int]bool) {
	t.Helper()
	seen := map[int]bool{}
	for _, track := range tracks {
		if track.StreamIndex == nil {
			continue
		}
		want, ok := wantURL[*track.StreamIndex]
		if !ok {
			continue
		}
		seen[*track.StreamIndex] = true
		if got := track.URL != ""; got != want {
			t.Errorf("stream %d (%s): has URL = %v, want %v (%q)", *track.StreamIndex, track.Format, got, want, track.URL)
		}
	}
	if len(seen) != len(wantURL) {
		t.Errorf("caption tracks %+v missing some of %v", tracks, wantURL)
	}
}

func TestBurnSubtitleIsText(t *testing.T) {
	streams := []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "h264"},
		{Index: 2, Type: "subtitle", Codec: "ASS"},
		{Index: 3, Type: "subtitle", Codec: "ssa"},
		{Index: 4, Type: "subtitle", Codec: "hdmv_pgs_subtitle"},
	}
	for index, want := range map[int]bool{-1: false, 0: false, 2: true, 3: true, 4: false, 9: false} {
		if got := BurnSubtitleIsText(streams, index); got != want {
			t.Errorf("BurnSubtitleIsText(%d) = %v, want %v", index, got, want)
		}
	}
}

type fakeStyledExtractor struct {
	Transcoder
	scriptErr error
	script    []byte
	fonts     map[int][]byte
}

func (f fakeStyledExtractor) ExtractStyledSubtitle(_ context.Context, _ string, index int) ([]byte, error) {
	if f.scriptErr != nil {
		return nil, f.scriptErr
	}
	if f.script != nil {
		return f.script, nil
	}
	return []byte("[Script Info]\n; stream " + string(rune('0'+index)) + "\n"), nil
}

func (f fakeStyledExtractor) SubtitleFontIndexes(context.Context, string) ([]int, error) {
	return []int{5, 6}, nil
}

func (f fakeStyledExtractor) ExtractSubtitleFont(_ context.Context, _ string, index int) ([]byte, error) {
	if data, ok := f.fonts[index]; ok {
		return data, nil
	}
	return nil, ffmpeg.ErrFontAttachmentNotFound
}

// TestExtractBurnSubtitleWritesScriptAndFonts pins the session's libass inputs:
// the script and every readable font land under the session directory, an
// unreadable font is skipped, and the rendition burns them with libass.
func TestExtractBurnSubtitleWritesScriptAndFonts(t *testing.T) {
	dir := t.TempDir()
	s := &Sessions{transcoder: fakeStyledExtractor{fonts: map[int][]byte{5: []byte("OTTOfontbytes")}}}
	file, fontsDir, err := s.extractBurnSubtitle(context.Background(), "/src.mkv", dir, 2)
	if err != nil {
		t.Fatalf("extractBurnSubtitle: %v", err)
	}
	if file != filepath.Join(dir, "burn-subtitle", "subtitle.ass") || fontsDir != filepath.Join(dir, "burn-subtitle", "fonts") {
		t.Fatalf("paths = %q, %q", file, fontsDir)
	}
	if data, err := os.ReadFile(file); err != nil || !strings.Contains(string(data), "stream 2") {
		t.Fatalf("script = %q, %v", data, err)
	}
	entries, err := os.ReadDir(fontsDir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "font-5.ttf" {
		t.Fatalf("fonts = %v, %v; want only font-5.ttf", entries, err)
	}

	rung := Rung{Height: 720, Codec: "h264", VideoKbps: 3000}
	h := &hlsSession{burnSubtitleIndex: 2, burnSubtitleFile: file, burnFontsDir: fontsDir}
	want := (&hlsSession{burnSubtitleIndex: -1}).rendition(rung).WithBurnTextSubtitle(2, file, fontsDir)
	if got := h.rendition(rung); !reflect.DeepEqual(got, want) {
		t.Fatalf("rendition = %+v, want the libass burn of the extracted script %+v", got, want)
	}

	s.transcoder = fakeStyledExtractor{scriptErr: errors.New("boom")}
	if _, _, err := s.extractBurnSubtitle(context.Background(), "/src.mkv", t.TempDir(), 2); err == nil {
		t.Fatal("extractBurnSubtitle succeeded without a script")
	}

	// The script passes the same input limits as the styled sidecar route
	// before libass parses it in the encoder.
	oversized := append([]byte("[Script Info]\n; "), strings.Repeat("x", subtitleingest.MaxASSLineBytes+1)...)
	s.transcoder = fakeStyledExtractor{script: oversized}
	if _, _, err := s.extractBurnSubtitle(context.Background(), "/src.mkv", t.TempDir(), 2); err == nil {
		t.Fatal("extractBurnSubtitle accepted a script over the ASS line limit")
	}
}

// TestPromoteReservationKeepsBurnInputs: a fresh session is prepared apart from
// its reservation, and the promoted session must keep the extracted script and
// fonts, or the encoder falls back to the bitmap overlay path.
func TestPromoteReservationKeepsBurnInputs(t *testing.T) {
	prepared := &hlsSession{burnSubtitleIndex: 2, burnSubtitleFile: "/s/subtitle.ass", burnFontsDir: "/s/fonts"}
	got := promoteReservation(&hlsSession{burnSubtitleIndex: -1}, prepared)
	if got.burnSubtitleIndex != 2 || got.burnSubtitleFile != prepared.burnSubtitleFile || got.burnFontsDir != prepared.burnFontsDir {
		t.Fatalf("promoted burn inputs = %d %q %q", got.burnSubtitleIndex, got.burnSubtitleFile, got.burnFontsDir)
	}
}

// TestStyledBurnStaysOnAdvertisingVersion: a selected subtitle index names a
// stream of the Version that advertised it. Selecting an ASS track that needs
// a burn must not move playback to a sibling Version that direct-plays but
// lacks that stream.
func TestStyledBurnStaysOnAdvertisingVersion(t *testing.T) {
	withTextSubtitleBurnIn(t, true)
	assIndex := 2
	service, st, scope := capabilityTestService(t)
	// Both Versions direct-play on the profile, so only the subtitle
	// selection could steer negotiation to the sibling without the track.
	// A multi-Version Media judges each Version's container by its file
	// name, so the default file is named as an mp4 too.
	path := filepath.Join(t.TempDir(), "fp-ass-v0.mp4")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "Styled versions", Container: "mp4", Codec: "h264",
		Class: store.MediaClassVideo, ContentFingerprint: "fp-ass-v0", DurationSecondsHint: 120,
		VideoWidthHint: 1920, VideoHeightHint: 1080,
		Streams: []store.MediaFileStream{
			videoStream("h264", "High", 0, ""),
			audioStream(1, "aac", 2),
			{Index: assIndex, Type: "subtitle", Codec: "ass", Language: "jpn"},
		},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	mediaID := media.ID
	addVersionFile(t, st, mediaID, "fp-ass-alt.mp4", "fp-ass-alt", "alt", 0,
		[]store.MediaFileStream{videoStream("h264", "High", 0, ""), audioStream(1, "aac", 2)})
	if resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: appleOverlayProfile}); err != nil || resp.Decision != "direct-play" || !resp.Versions[0].Selected {
		t.Fatalf("precondition: unselected Info = %q, Versions %+v, %v; want direct play of the default Version", resp.Decision, resp.Versions, err)
	}

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID), CapabilityProfile: appleOverlayProfile, SelectedSubtitleIndex: &assIndex,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "transcode" || !resp.Versions[0].Selected {
		t.Fatalf("Decision = %q (%q), Versions %+v; want a transcode of the default Version that burns the track", resp.Decision, resp.Reason, resp.Versions)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS token: %v", err)
	}
	if got := grant.BurnSubtitleIndex - 1; got != assIndex {
		t.Fatalf("BurnSubtitleIndex = %d, want %d", got, assIndex)
	}
}

// TestStyledBurnSkipsSourceThatCannotTranscode: a Dolby Vision profile 5
// source has no base layer to re-encode, so a burn could never start. The ASS
// track keeps its WebVTT URL and the source keeps its direct-play rung.
func TestStyledBurnSkipsSourceThatCannotTranscode(t *testing.T) {
	withTextSubtitleBurnIn(t, true)
	assIndex := 2
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-ass-dv5", "mp4", "hevc", 3840, 2160, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 5, "smpte2084"),
		audioStream(1, "aac", 2),
		{Index: assIndex, Type: "subtitle", Codec: "ass", Language: "jpn"},
	})
	profile := appleOverlayProfile
	profile.HDRFormats = []string{"dolby-vision-p5"}
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID), CapabilityProfile: profile, SelectedSubtitleIndex: &assIndex,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision == "unavailable" || resp.Decision == "transcode" {
		t.Fatalf("Decision = %q (%q); want the source to play without a burn", resp.Decision, resp.Reason)
	}
	assertCaptionURLs(t, resp.CaptionTracks, map[int]bool{assIndex: true})
}
