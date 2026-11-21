package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

type versionRuntimeTranscoder struct {
	fakeHLSTranscoder
	durations map[string]int
}

func (f versionRuntimeTranscoder) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	return ffmpeg.Probe{DurationSeconds: f.durations[path]}, nil
}

func TestHLSAndPlaybackInfoUseSelectedVersionRuntime(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	dir := t.TempDir()
	file := func(name string) store.MediaFile {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := store.HashMediaFileContent(path)
		if err != nil {
			t.Fatal(err)
		}
		return store.MediaFile{Path: path, ContentFingerprint: fingerprint}
	}
	primary, alternate := file("primary.mkv"), file("alternate.mkv")
	streams := []store.MediaFileStream{{Index: 0, Type: "video", Codec: "h264"}}
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Title: "Runtime fixture", Class: store.MediaClassVideo,
		Container: "mkv", Codec: "h264", Path: primary.Path, ContentFingerprint: primary.ContentFingerprint,
		VideoWidthHint: 1280, VideoHeightHint: 720, Streams: streams,
	})
	if err != nil {
		t.Fatal(err)
	}
	alternate.VersionLabel = "Alternate"
	if err := st.AddMediaFileVersion(ctx, media.ID, alternate, streams); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	tokens := streamtoken.NewService([]byte("version-runtime-test"), nil)
	const primarySeconds, alternateSeconds = 45, 93
	transcoder := versionRuntimeTranscoder{durations: map[string]int{primary.Path: primarySeconds, alternate.Path: alternateSeconds}}
	sessions := sessiontest.New(t, transcoder, t.TempDir(), nil)
	service := playback.NewService(st, tokens, nil)
	service.SetSessions(sessions)
	request := playback.InfoRequest{MediaID: string(media.ID)}
	first, err := service.Info(ctx, scope, request)
	if err != nil || first.Timeline.DurationSeconds != primarySeconds || len(first.Versions) != 2 || !first.Versions[0].Selected {
		t.Fatalf("primary playback runtime = %d, Versions = %+v, error = %v", first.Timeline.DurationSeconds, first.Versions, err)
	}
	checkCatalog := func() {
		t.Helper()
		stored, err := st.Media(ctx, media.ID, scope.LibraryFilter())
		if err != nil || stored.DurationSeconds != primarySeconds {
			t.Fatalf("catalog runtime = %d, %v; want %d", stored.DurationSeconds, err, primarySeconds)
		}
	}
	checkCatalog()
	request.SelectedVersion = first.Versions[1].ID
	second, err := service.Info(ctx, scope, request)
	if err != nil || second.Timeline.DurationSeconds != alternateSeconds || len(second.Versions) != 2 || !second.Versions[1].Selected {
		t.Fatalf("alternate playback runtime = %d, Versions = %+v, error = %v", second.Timeline.DurationSeconds, second.Versions, err)
	}
	if len(second.Streams) == 0 || second.Streams[0].Kind != "hls" {
		t.Fatalf("alternate streams = %+v; want live HLS", second.Streams)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessions)
	token := second.Streams[0].Token
	master := fetchCastHLS(handler, token, "master.m3u8")
	var variant string
	for line := range strings.SplitSeq(master.Body.String(), "\n") {
		if strings.HasSuffix(line, "/index.m3u8") {
			variant = line
			break
		}
	}
	if master.Code != http.StatusOK || variant == "" {
		t.Fatalf("HLS master = %d %q; want a live variant", master.Code, master.Body.String())
	}
	playlist := fetchCastHLS(handler, token, variant)
	var seconds float64
	for line := range strings.SplitSeq(playlist.Body.String(), "\n") {
		if length, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			value, err := strconv.ParseFloat(strings.TrimSuffix(length, ","), 64)
			if err != nil {
				t.Fatal(err)
			}
			seconds += value
		}
	}
	if playlist.Code != http.StatusOK || seconds != alternateSeconds {
		t.Fatalf("HLS variant = %d, %.3f seconds; want %d seconds\n%s", playlist.Code, seconds, alternateSeconds, playlist.Body.String())
	}
	checkCatalog()
}

// TestDirectServesNegotiatedVersionFile: the direct URL of a non-primary
// Version serves that Version's bytes, and a signed file fingerprint that
// names no file of the grant's Media is refused.
func TestDirectServesNegotiatedVersionFile(t *testing.T) {
	st := storetest.Open(t)
	dir := t.TempDir()
	defaultPath := filepath.Join(dir, "Night Harbor (2004) - 4K.mkv")
	selectedPath := filepath.Join(dir, "Night Harbor (2004) - 1080p.mkv")
	otherPath := filepath.Join(dir, "Coastline (1998).mkv")
	for path, body := range map[string]string{
		defaultPath:  "default version bytes",
		selectedPath: "selected version bytes",
		otherPath:    "other media bytes",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: defaultPath, Title: "Night Harbor", Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: "sha256:version-default",
		VideoWidthHint: 1920, VideoHeightHint: 1080,
		Streams: []store.MediaFileStream{
			{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", DoViProfile: 5},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaFileVersion(ctx, media.ID, store.MediaFile{
		Path: selectedPath, ContentFingerprint: "sha256:version-selected", VersionLabel: "1080p",
	}, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: otherPath, Title: "Coastline", Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: "sha256:version-other",
	}); err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	resp, err := playback.NewService(st, tokens, clock).Info(ctx, scope, playback.InfoRequest{
		MediaID: string(media.ID),
		CapabilityProfile: playback.CapabilityProfile{
			Client:     "beamfall-test",
			Containers: []string{"mkv"},
			// No height ceiling: a non-first Version has no stored frame height, and
			// under a ceiling playback would transcode it instead of serving its file.
			VideoCodecs: []playback.VideoCodecCap{{Codec: "hevc", Profiles: []string{"Main 10"}}},
			AudioCodecs: []playback.AudioCodecCap{{Codec: "eac3", MaxChannels: 6}},
			HDRFormats:  []string{"hdr10"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "direct-play" || len(resp.Versions) != 2 || !resp.Versions[1].Selected {
		t.Fatalf("playbackInfo = %s with %+v, want direct-play of the 1080p Version", resp.Decision, resp.Versions)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessiontest.New(t, nil, t.TempDir(), nil))
	get := func(url string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.Direct(rec, httptest.NewRequest(http.MethodGet, url, nil))
		return rec
	}
	if rec := get(resp.Streams[0].URL); rec.Code != http.StatusOK || rec.Body.String() != "selected version bytes" {
		t.Fatalf("direct = %d %q, want the 1080p Version's bytes", rec.Code, rec.Body.String())
	}

	grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaDirect)
	if err != nil {
		t.Fatal(err)
	}
	for name, fingerprint := range map[string]string{"another media": "sha256:version-other", "unknown": "sha256:absent"} {
		forged := grant
		forged.FileFingerprint = fingerprint
		token, err := tokens.Issue(ctx, forged)
		if err != nil {
			t.Fatal(err)
		}
		if rec := get("/api/media/direct/" + token); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "bytes") {
			t.Fatalf("%s fingerprint = %d %q, want 404", name, rec.Code, rec.Body.String())
		}
	}
}
