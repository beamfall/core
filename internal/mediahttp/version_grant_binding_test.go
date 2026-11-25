package mediahttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

const (
	bindingDefaultFingerprint  = "sha256:binding-default"
	bindingSelectedFingerprint = "sha256:binding-selected"
)

// versionBindingFixture is a two-Version Media whose 1080p Version
// playbackInfo negotiates, plus one precomputed HLS rendition cut from each
// Version, so every byte route can be asked for the other Version's bytes.
type versionBindingFixture struct {
	handler  *Handler
	tokens   *streamtoken.Service
	selected playback.StreamChoice
	grant    streamtoken.Grant
}

func newVersionBindingFixture(t *testing.T) versionBindingFixture {
	t.Helper()
	st := storetest.Open(t)
	dir := t.TempDir()
	defaultPath := filepath.Join(dir, "Night Harbor (2004) - 4K.mkv")
	selectedPath := filepath.Join(dir, "Night Harbor (2004) - 1080p.mkv")
	for path, body := range map[string]string{defaultPath: "default version bytes", selectedPath: "selected version bytes"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: defaultPath, Title: "Night Harbor", Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: bindingDefaultFingerprint,
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
		Path: selectedPath, ContentFingerprint: bindingSelectedFingerprint, VersionLabel: "1080p",
	}, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
	}); err != nil {
		t.Fatal(err)
	}
	for id, fingerprint := range map[store.PlaybackRenditionID]string{
		"rendition-default":  bindingDefaultFingerprint,
		"rendition-selected": bindingSelectedFingerprint,
	} {
		cacheDir := filepath.Join(t.TempDir(), string(id))
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cacheDir, "master.m3u8"), []byte("#EXTM3U\n# "+fingerprint+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPlaybackRendition(ctx, store.PlaybackRendition{
			ID: id, MediaID: media.ID, ContentFingerprint: fingerprint,
			Kind: store.PlaybackRenditionKindHLS, Status: store.PlaybackRenditionReady,
			RungID: "720p", Codec: "h264", Height: 720, Bandwidth: 4_256_000, Path: cacheDir, SizeBytes: 64,
			CreatedAt: time.Unix(100, 0).UTC(), LastUsedAt: time.Unix(100, 0).UTC(),
		}); err != nil {
			t.Fatal(err)
		}
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
			VideoCodecs: []playback.VideoCodecCap{{Codec: "hevc", Profiles: []string{"Main 10"}}},
			AudioCodecs: []playback.AudioCodecCap{{Codec: "eac3", MaxChannels: 6}},
			HDRFormats:  []string{"hdr10"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Decision != "direct-play" || len(resp.Streams) == 0 || len(resp.Versions) != 2 || !resp.Versions[1].Selected {
		t.Fatalf("playbackInfo = %s with %+v, want direct-play of the 1080p Version", resp.Decision, resp.Versions)
	}
	grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaDirect)
	if err != nil {
		t.Fatal(err)
	}
	if grant.FileFingerprint != bindingSelectedFingerprint {
		t.Fatalf("negotiated grant fingerprint = %q, want the 1080p Version", grant.FileFingerprint)
	}
	handler := NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), sessiontest.New(t, fakeHLSTranscoder{}, t.TempDir(), nil))
	return versionBindingFixture{handler: handler, tokens: tokens, selected: resp.Streams[0], grant: grant}
}

func (f versionBindingFixture) issue(t *testing.T, grant streamtoken.Grant) string {
	t.Helper()
	token, err := f.tokens.Issue(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func (f versionBindingFixture) direct(token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.Direct(rec, httptest.NewRequest(http.MethodGet, "/api/media/direct/"+token, nil))
	return rec
}

func (f versionBindingFixture) hlsMaster(token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/master.m3u8", nil))
	return rec
}

// TestDirectGrantForOneVersionNeverServesAnother: each Version's signed
// Direct grant serves exactly that Version's bytes, and a grant whose signed
// payload was edited to name the other Version is refused before any byte.
func TestDirectGrantForOneVersionNeverServesAnother(t *testing.T) {
	f := newVersionBindingFixture(t)
	if rec := f.direct(f.selected.Token); rec.Code != http.StatusOK || rec.Body.String() != "selected version bytes" {
		t.Fatalf("negotiated 1080p grant = %d %q, want the 1080p bytes", rec.Code, rec.Body.String())
	}
	other := f.grant
	other.FileFingerprint = bindingDefaultFingerprint
	if rec := f.direct(f.issue(t, other)); rec.Code != http.StatusOK || rec.Body.String() != "default version bytes" {
		t.Fatalf("4K grant = %d %q, want the 4K bytes only", rec.Code, rec.Body.String())
	}

	payload, signature, ok := strings.Cut(f.selected.Token, ".")
	if !ok {
		t.Fatalf("token %q has no signature segment", f.selected.Token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(map[string]any){
		"other version":    func(m map[string]any) { m["fileFingerprint"] = bindingDefaultFingerprint },
		"fingerprint drop": func(m map[string]any) { delete(m, "fileFingerprint") },
	} {
		tampered := map[string]any{}
		for k, v := range fields {
			tampered[k] = v
		}
		edit(tampered)
		body, err := json.Marshal(tampered)
		if err != nil {
			t.Fatal(err)
		}
		token := base64.RawURLEncoding.EncodeToString(body) + "." + signature
		if rec := f.direct(token); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "bytes") {
			t.Fatalf("%s tampered grant = %d %q, want 401 without bytes", name, rec.Code, rec.Body.String())
		}
	}
}

// TestVersionGrantDoesNotReplayAcrossDirectAndHLS: a negotiated Version's
// Direct grant is refused by the HLS route and the matching HLS grant by the
// Direct route, so neither target can be replayed to reach the other.
func TestVersionGrantDoesNotReplayAcrossDirectAndHLS(t *testing.T) {
	f := newVersionBindingFixture(t)
	if rec := f.hlsMaster(f.selected.Token); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "EXTM3U") {
		t.Fatalf("direct grant at HLS = %d %q, want 401", rec.Code, rec.Body.String())
	}
	hls := f.grant
	hls.Target = streamtoken.TargetMediaHLS
	hls.PlaybackRenditionID = "rendition-selected"
	token := f.issue(t, hls)
	if rec := f.hlsMaster(token); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), bindingSelectedFingerprint) {
		t.Fatalf("HLS grant at HLS = %d %q, want the 1080p rendition", rec.Code, rec.Body.String())
	}
	if rec := f.direct(token); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "bytes") {
		t.Fatalf("HLS grant at Direct = %d %q, want 401 without bytes", rec.Code, rec.Body.String())
	}
}

// TestPrecomputedHLSRenditionBoundToNegotiatedVersion: a precomputed HLS
// grant serves its rendition only when the rendition was cut from the file the
// grant negotiated. A rendition of the Media's other Version, or one whose
// granted file is not a live file of the Media, is refused.
func TestPrecomputedHLSRenditionBoundToNegotiatedVersion(t *testing.T) {
	f := newVersionBindingFixture(t)
	for _, tc := range []struct {
		name        string
		fingerprint string
		rendition   store.PlaybackRenditionID
		want        int
	}{
		{"negotiated version", bindingSelectedFingerprint, "rendition-selected", http.StatusOK},
		{"primary version", bindingDefaultFingerprint, "rendition-default", http.StatusOK},
		{"unbound grant reads primary", "", "rendition-default", http.StatusOK},
		{"other version rendition", bindingSelectedFingerprint, "rendition-default", http.StatusNotFound},
		{"primary grant other rendition", bindingDefaultFingerprint, "rendition-selected", http.StatusNotFound},
		{"unbound grant alternate rendition", "", "rendition-selected", http.StatusNotFound},
		{"file not live", "sha256:absent", "rendition-selected", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant := f.grant
			grant.Target = streamtoken.TargetMediaHLS
			grant.FileFingerprint = tc.fingerprint
			grant.PlaybackRenditionID = tc.rendition
			rec := f.hlsMaster(f.issue(t, grant))
			if rec.Code != tc.want {
				t.Fatalf("status = %d %q, want %d", rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want != http.StatusOK && strings.Contains(rec.Body.String(), "EXTM3U") {
				t.Fatalf("refused grant leaked a playlist: %q", rec.Body.String())
			}
		})
	}
}
