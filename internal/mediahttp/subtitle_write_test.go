package mediahttp

import (
	"bytes"
	"context"
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
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// These are the ROUND-TRIP tests for the subtitle write halves: each one drives
// the real HTTP handler against a real migrated store and then observes the
// result through the READ half that ships to viewers — the playback resolver for
// the pin, the served WebVTT bytes for the offset. A store-API assertion would
// pass even if the two halves keyed on different things, which is exactly the
// defect this lane exists to close.

const (
	subtitleWriteSession = "session-subs"
	subtitleWriteDevice  = "device-subs"
	subtitleWriteSecret  = "subs-secret"
)

// subtitleWriteFixture is a series of two episodes, each with one .srt sidecar on
// disk, plus an enrolled Profile session to write under.
type subtitleWriteFixture struct {
	store    *store.Store
	handler  *Handler
	tokens   *streamtoken.Service
	episodes []store.MediaID
	now      time.Time
}

// newSubtitleWriteFixture seeds two episodes under ONE ordered container entity,
// so both resolve to the same series-scoped subtitle group and a pin on the first
// must be honoured on the second.
func newSubtitleWriteFixture(t *testing.T) *subtitleWriteFixture {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	dir := t.TempDir()

	if err := st.EnrollDevice(ctx, store.Device{
		ID: subtitleWriteDevice, Name: "Subs", Role: store.DeviceRoleViewer,
		SecretHash: store.HashDeviceSecret(subtitleWriteSecret),
	}, store.Session{
		ID: subtitleWriteSession, DeviceID: subtitleWriteDevice, ProfileID: "profile-default",
	}); err != nil {
		t.Fatalf("EnrollDevice: %v", err)
	}
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-series", LibraryID: "library-local", Kind: "container", Title: "Series",
	}); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}

	episodes := make([]store.MediaID, 0, 2)
	for i, name := range []string{"episode-one", "episode-two"} {
		path := filepath.Join(dir, name+".mkv")
		if err := os.WriteFile(path, []byte("mkv bytes "+name), 0o600); err != nil {
			t.Fatal(err)
		}
		// One English and one French sidecar per episode, so a pin has a real
		// alternative to beat and index 0/1 address distinct tracks.
		writeSidecar(t, dir, name+".en.srt", "Hello")
		writeSidecar(t, dir, name+".fr.srt", "Bonjour")
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:          "library-local",
			Path:               path,
			Title:              name,
			Container:          "mkv",
			Codec:              "h264",
			ContentFingerprint: "sha256:" + name,
			Sidecars: []store.MediaFileSidecarCaption{
				{RelPath: name + ".en.srt", Format: "srt", Language: "en"},
				{RelPath: name + ".fr.srt", Format: "srt", Language: "fr"},
			},
		})
		if err != nil {
			t.Fatalf("ImportMediaFile(%s): %v", name, err)
		}
		order := i
		if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: "entity-series", Name: "children", ToMediaID: media.ID, SortOrder: &order,
		}); err != nil {
			t.Fatalf("UpsertEntityRelation(%s): %v", media.ID, err)
		}
		episodes = append(episodes, media.ID)
	}

	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	handler := NewHandler(st, auth.NewService(st), tokens,
		artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil))
	return &subtitleWriteFixture{store: st, handler: handler, tokens: tokens, episodes: episodes, now: now}
}

// writeSidecar drops a one-cue SubRip sidecar whose single cue starts at 1s, so a
// shift is unambiguous in the served WebVTT.
func writeSidecar(t *testing.T, dir, name, text string) {
	t.Helper()
	body := "1\n00:00:01,000 --> 00:00:02,000\n" + text + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// post builds an authenticated Profile-session command request.
func (f *subtitleWriteFixture) post(t *testing.T, path string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return f.rawPost(path, string(raw))
}

func (f *subtitleWriteFixture) rawPost(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Beamfall-Session", subtitleWriteSession)
	req.Header.Set("X-Beamfall-Device", subtitleWriteDevice)
	req.Header.Set("X-Beamfall-Device-Secret", subtitleWriteSecret)
	return req
}

// sidecarToken mints the byte-route token the serve path needs for one episode.
func (f *subtitleWriteFixture) sidecarToken(t *testing.T, mediaID store.MediaID) string {
	t.Helper()
	token, err := f.tokens.Issue(context.Background(), streamtoken.Grant{
		Target:    streamtoken.TargetMediaSidecar,
		MediaID:   mediaID,
		DeviceID:  subtitleWriteDevice,
		SessionID: subtitleWriteSession,
		ProfileID: "profile-default",
		ExpiresAt: f.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// serveSidecar fetches one episode's sidecar through the real byte route, which
// is where applySubtitleOffset runs.
func (f *subtitleWriteFixture) serveSidecar(t *testing.T, mediaID store.MediaID, index string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	token := f.sidecarToken(t, mediaID)
	f.handler.Sidecars(rec, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/"+index+".vtt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sidecar status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// REQUIREMENT 2 round trip: an offset written through the HTTP command is
// observed in the CUE TIMINGS the sidecar route serves for the same track. The
// write never names the fingerprint — the handler derives it from the same bytes
// the serve path hashes — so this passing is what proves the two halves agree on
// the key.
func TestSubtitleOffsetWriteIsAppliedToTheServedSidecar(t *testing.T) {
	f := newSubtitleWriteFixture(t)

	before := f.serveSidecar(t, f.episodes[0], "0")
	if !strings.Contains(before, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("unshifted sidecar = %q, want the authored cue timing", before)
	}

	rec := httptest.NewRecorder()
	f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", map[string]any{
		"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": 1500,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("SetSubtitleOffset status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	after := f.serveSidecar(t, f.episodes[0], "0")
	if !strings.Contains(after, "00:00:02.500 --> 00:00:03.500") {
		t.Fatalf("served sidecar = %q, want cues shifted +1500ms", after)
	}

	// The nudge is per-TRACK: the French sidecar of the same episode is untouched.
	if other := f.serveSidecar(t, f.episodes[0], "1"); !strings.Contains(other, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("second track = %q, want it unshifted", other)
	}

	// Nudging back to centre clears the correction end to end.
	rec = httptest.NewRecorder()
	f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", map[string]any{
		"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": 0,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("centring status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if centred := f.serveSidecar(t, f.episodes[0], "0"); !strings.Contains(centred, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("after centring = %q, want the authored cue timing back", centred)
	}
}

// REQUIREMENT 1 round trip: a pin written through the HTTP command on episode ONE
// is resolved by playback.resolveSubtitles on episode TWO, and BEATS the
// Profile's own English preference there (ADR-0194 §3 semantic 4). Observing it
// through Info is the point — the store API alone cannot show that the series
// group key the writer used is the one the resolver looks up.
func TestSubtitlePinWriteResolvesOnALaterEpisodeAndBeatsPreferences(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	ctx := context.Background()
	mode := store.SubtitleModeAlways
	if _, err := f.store.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{
		SubtitleLanguages: &[]string{"en"},
		SubtitleMode:      &mode,
	}); err != nil {
		t.Fatalf("UpdateProfileSettings: %v", err)
	}
	service := playback.NewService(f.store, f.tokens, func() time.Time { return f.now })
	scope, err := auth.NewService(f.store).Scope(ctx, subtitleWriteSession)
	if err != nil {
		t.Fatal(err)
	}
	profile := playback.CapabilityProfile{
		Client: "beamfall-web", Containers: []string{"mkv"},
		DirectCodecs: []string{"h264"}, SubtitleFormats: []string{"srt", "webvtt"},
		DownstreamAudio: "stereo",
	}
	selected := func(mediaID store.MediaID) *playback.SelectedSubtitleTrack {
		t.Helper()
		resp, err := service.Info(ctx, scope, playback.InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile})
		if err != nil {
			t.Fatalf("Info(%s): %v", mediaID, err)
		}
		return resp.SelectedSubtitleTrack
	}

	// Baseline: the preference policy picks English on the SECOND episode.
	if got := selected(f.episodes[1]); got == nil || got.Track.Language != "en" {
		t.Fatalf("pre-pin selection = %+v, want the preferred English track", got)
	}

	rec := httptest.NewRecorder()
	f.handler.SetSubtitlePin(rec, f.post(t, "/api/media/subtitle-pin", map[string]any{
		"mediaId":  string(f.episodes[0]),
		"selected": true,
		"track":    map[string]any{"language": "fr", "kind": playback.SubtitleTrackKindSidecar},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("SetSubtitlePin status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// The pin was written on episode one; it must resolve on episode two, and beat
	// the English preference there.
	got := selected(f.episodes[1])
	if got == nil || got.Track.Language != "fr" {
		t.Fatalf("post-pin selection on the later episode = %+v, want the pinned French track", got)
	}
	if got.Track.Kind != playback.SubtitleTrackKindSidecar {
		t.Fatalf("pinned kind = %q, want sidecar", got.Track.Kind)
	}
}

// A subtitles-OFF pin is a real pin, not the absence of one: it must survive the
// round trip and suppress the pre-selection the preference policy would make.
func TestSubtitlePinWriteCanPinSubtitlesOff(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	ctx := context.Background()
	mode := store.SubtitleModeAlways
	if _, err := f.store.UpdateProfileSettings(ctx, "profile-default", store.ProfileSettings{
		SubtitleLanguages: &[]string{"en"},
		SubtitleMode:      &mode,
	}); err != nil {
		t.Fatalf("UpdateProfileSettings: %v", err)
	}
	rec := httptest.NewRecorder()
	f.handler.SetSubtitlePin(rec, f.post(t, "/api/media/subtitle-pin", map[string]any{
		"mediaId": string(f.episodes[0]), "selected": false,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	service := playback.NewService(f.store, f.tokens, func() time.Time { return f.now })
	scope, err := auth.NewService(f.store).Scope(ctx, subtitleWriteSession)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := service.Info(ctx, scope, playback.InfoRequest{
		MediaID: string(f.episodes[1]),
		CapabilityProfile: playback.CapabilityProfile{
			Client: "beamfall-web", Containers: []string{"mkv"},
			DirectCodecs: []string{"h264"}, SubtitleFormats: []string{"srt", "webvtt"},
			DownstreamAudio: "stereo",
		},
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.SelectedSubtitleTrack != nil {
		t.Fatalf("selection = %+v, want none under a subtitles-off pin", resp.SelectedSubtitleTrack)
	}
}

func TestSubtitlePinWriteRejectsMissingSelected(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	rec := httptest.NewRecorder()
	f.handler.SetSubtitlePin(rec, f.post(t, "/api/media/subtitle-pin", map[string]any{
		"mediaId": string(f.episodes[0]),
	}))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "selected is required") {
		t.Fatalf("body = %q, want a missing-selected error", body)
	}
}

func TestSubtitleOffsetWriteRejectsMissingRequiredFields(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{"both omitted", map[string]any{"mediaId": string(f.episodes[0])}, "sidecar index is required"},
		{"sidecar index omitted", map[string]any{"mediaId": string(f.episodes[0]), "offsetMs": 100}, "sidecar index is required"},
		{"offset omitted", map[string]any{"mediaId": string(f.episodes[0]), "sidecarIndex": 0}, "subtitle offset is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", tc.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if body := rec.Body.String(); !strings.Contains(body, tc.want) {
				t.Fatalf("body = %q, want %q", body, tc.want)
			}
		})
	}
}

func TestSubtitleOffsetWriteCanExplicitlyClearNudge(t *testing.T) {
	f := newSubtitleWriteFixture(t)

	rec := httptest.NewRecorder()
	f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", map[string]any{
		"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": 1500,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("initial nudge status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", map[string]any{
		"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": 0,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := f.serveSidecar(t, f.episodes[0], "0"); !strings.Contains(got, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("after explicit zero = %q, want the authored cue timing back", got)
	}
}

// The offset boundary rejects out-of-range and malformed values without
// replacing the last accepted nudge.
func TestSubtitleOffsetWriteRejectsOutOfRangeAndMalformedInput(t *testing.T) {
	f := newSubtitleWriteFixture(t)

	t.Run("boundary value is accepted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", map[string]any{
			"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": store.MaxSubtitleOffsetMillis,
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 at the exact cap: %s", rec.Code, rec.Body.String())
		}
	})

	rejected := []struct {
		name string
		body string
	}{
		{"one past the positive cap", `{"mediaId":"MEDIA","sidecarIndex":0,"offsetMs":600001}`},
		{"one past the negative cap", `{"mediaId":"MEDIA","sidecarIndex":0,"offsetMs":-600001}`},
		{"absurd shift", `{"mediaId":"MEDIA","sidecarIndex":0,"offsetMs":1073741824}`},
		{"offset is a string", `{"mediaId":"MEDIA","sidecarIndex":0,"offsetMs":"1500"}`},
		{"offset is fractional", `{"mediaId":"MEDIA","sidecarIndex":0,"offsetMs":1.5}`},
		{"body is not JSON", `not json at all`},
		{"body is empty", ``},
		{"media ID missing", `{"sidecarIndex":0,"offsetMs":100}`},
		{"sidecar index negative", `{"mediaId":"MEDIA","sidecarIndex":-1,"offsetMs":100}`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.ReplaceAll(tc.body, "MEDIA", string(f.episodes[0]))
			rec := httptest.NewRecorder()
			f.handler.SetSubtitleOffset(rec, f.rawPost("/api/media/subtitle-offset", body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %s", rec.Code, tc.name)
			}
		})
	}

	// Validate the value before resolving its target.
	t.Run("range is refused before target resolution", func(t *testing.T) {
		rec := httptest.NewRecorder()
		f.handler.SetSubtitleOffset(rec, f.rawPost("/api/media/subtitle-offset",
			`{"mediaId":"media-nope","sidecarIndex":0,"offsetMs":600001}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (bad value) rather than 404 (bad target)", rec.Code)
		}
	})

	// The served track still carries the accepted shift, not a rejected one.
	served := f.serveSidecar(t, f.episodes[0], "0")
	if !strings.Contains(served, "00:10:01.000 --> 00:10:02.000") {
		t.Fatalf("served sidecar = %q, want only the accepted +600000ms shift", served)
	}
}

// Unknown targets are refused rather than producing an unresolvable nudge.
func TestSubtitleOffsetWriteRejectsUnknownTargets(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unknown media", map[string]any{"mediaId": "media-nope", "sidecarIndex": 0, "offsetMs": 100}},
		{"index past the sidecar list", map[string]any{"mediaId": string(f.episodes[0]), "sidecarIndex": 9, "offsetMs": 100}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", tc.body))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}

// A pin must name a track the resolver can match later.
func TestSubtitlePinWriteRejectsUnresolvableTracks(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	cases := []struct {
		name string
		body map[string]any
	}{
		{"no language", map[string]any{"mediaId": string(f.episodes[0]), "selected": true,
			"track": map[string]any{"kind": playback.SubtitleTrackKindSidecar}}},
		{"unknown kind", map[string]any{"mediaId": string(f.episodes[0]), "selected": true,
			"track": map[string]any{"language": "fr", "kind": "telepathy"}}},
		{"no media", map[string]any{"selected": true,
			"track": map[string]any{"language": "fr", "kind": playback.SubtitleTrackKindSidecar}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.handler.SetSubtitlePin(rec, f.post(t, "/api/media/subtitle-pin", tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

// Both commands require an authenticated Profile session.
func TestSubtitleWritesRequireAProfileSession(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	body := map[string]any{"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": 100}

	for _, route := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{"offset", "/api/media/subtitle-offset", f.handler.SetSubtitleOffset},
		{"pin", "/api/media/subtitle-pin", f.handler.SetSubtitlePin},
	} {
		t.Run(route.name+" unauthenticated", func(t *testing.T) {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			route.handler(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

// An authenticated credential with no Profile is refused.
func TestSubtitleWritesRejectNonProfileCredentials(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	_, secret, err := f.store.CreateAPIToken(context.Background(), "ops", f.now)
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	for _, route := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
		body    map[string]any
	}{
		{"offset", "/api/media/subtitle-offset", f.handler.SetSubtitleOffset,
			map[string]any{"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": 100}},
		{"pin", "/api/media/subtitle-pin", f.handler.SetSubtitlePin,
			map[string]any{"mediaId": string(f.episodes[0]), "selected": false}},
	} {
		t.Run(route.name, func(t *testing.T) {
			raw, err := json.Marshal(route.body)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, route.path, bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+secret)
			rec := httptest.NewRecorder()
			route.handler(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 for a Profile-less credential", rec.Code)
			}
		})
	}
}

// The nudge is per-PROFILE: one Profile's correction never reaches another's
// playback of the very same track bytes.
func TestSubtitleOffsetWriteIsScopedToTheWritingProfile(t *testing.T) {
	f := newSubtitleWriteFixture(t)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	f.handler.SetSubtitleOffset(rec, f.post(t, "/api/media/subtitle-offset", map[string]any{
		"mediaId": string(f.episodes[0]), "sidecarIndex": 0, "offsetMs": 2000,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// A second Profile on the same device fetches the same track: unshifted.
	if err := f.store.EnrollDevice(ctx, store.Device{
		ID: "device-other", Name: "Other", Role: store.DeviceRoleViewer,
		SecretHash: store.HashDeviceSecret("other-secret"),
	}, store.Session{
		ID: "session-other", DeviceID: "device-other", ProfileID: "profile-managed",
	}); err != nil {
		t.Fatalf("EnrollDevice: %v", err)
	}
	token, err := f.tokens.Issue(ctx, streamtoken.Grant{
		Target:    streamtoken.TargetMediaSidecar,
		MediaID:   f.episodes[0],
		DeviceID:  "device-other",
		SessionID: "session-other",
		ProfileID: "profile-managed",
		ExpiresAt: f.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewRecorder()
	f.handler.Sidecars(other, httptest.NewRequest(http.MethodGet, "/api/media/sidecar/"+token+"/0.vtt", nil))
	if other.Code != http.StatusOK {
		t.Fatalf("other-profile sidecar status = %d: %s", other.Code, other.Body.String())
	}
	if !strings.Contains(other.Body.String(), "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("other profile saw a shifted track: %q", other.Body.String())
	}
}
