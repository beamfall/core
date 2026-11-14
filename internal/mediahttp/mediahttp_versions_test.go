package mediahttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

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
			Client:      "beamfall-test",
			Containers:  []string{"mkv"},
			VideoCodecs: []playback.VideoCodecCap{{Codec: "hevc", Profiles: []string{"Main 10"}, MaxHeight: 1080}},
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
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
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
