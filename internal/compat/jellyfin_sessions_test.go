package compat

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

type sessionJSON struct {
	ID                    string `json:"Id"`
	UserID                string `json:"UserId"`
	UserName              string `json:"UserName"`
	ServerID              string `json:"ServerId"`
	SupportsRemoteControl bool   `json:"SupportsRemoteControl"`
	NowPlayingItem        struct {
		ID                string `json:"Id"`
		Name              string `json:"Name"`
		Type              string `json:"Type"`
		SeriesName        string `json:"SeriesName"`
		IndexNumber       *int   `json:"IndexNumber"`
		ParentIndexNumber *int   `json:"ParentIndexNumber"`
		RunTimeTicks      int64  `json:"RunTimeTicks"`
		MediaSources      []any  `json:"MediaSources"`
	} `json:"NowPlayingItem"`
	PlayState struct {
		PositionTicks int64 `json:"PositionTicks"`
	} `json:"PlayState"`
}

// play records an active playback of media for a session and Profile, with
// saved progress, the way the playback path writes the ledger.
func (f catalogFixture) play(t *testing.T, session store.SessionID, profile store.ProfileID, media store.MediaID, position, duration int) {
	t.Helper()
	ctx := t.Context()
	if err := f.st.RecordStreamToken(ctx, session, profile, "device-tv", "media-hls", media, false, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	progress := store.PlaybackProgress{ProfileID: profile, MediaID: media, PositionSeconds: position, DurationSeconds: duration, UpdatedAt: time.Now()}
	if err := f.st.UpsertPlaybackProgress(ctx, progress, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
}

func (f catalogFixture) sessions(t *testing.T) ([]sessionJSON, string) {
	t.Helper()
	body := f.get(t, "Sessions", http.StatusOK)
	var out []sessionJSON
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode sessions %s: %v", body, err)
	}
	return out, string(body)
}

func TestJellyfinSessionsListsOptedInNowPlaying(t *testing.T) {
	f := newCatalogFixture(t)
	if got, body := f.sessions(t); len(got) != 0 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("idle sessions = %s, want []", body)
	}
	f.play(t, "session-default", "profile-default", f.part2, 600, 2400)

	got, body := f.sessions(t)
	if len(got) != 1 {
		t.Fatalf("sessions = %s, want one", body)
	}
	s := got[0]
	if s.UserID != f.handler.compatUserID("profile-default") || s.UserName != "Default" || s.ServerID != "server-test" {
		t.Fatalf("session user = %+v", s)
	}
	if s.ID == "" || strings.Contains(body, "session-default") || strings.Contains(body, "profile-default") {
		t.Fatalf("session exposes a native identifier: %s", body)
	}
	if strings.Contains(body, "device-tv") || strings.Contains(body, "DeviceName") || strings.Contains(body, "Client") || strings.Contains(body, "RemoteEndPoint") {
		t.Fatalf("session exposes device fields: %s", body)
	}
	if s.SupportsRemoteControl {
		t.Fatalf("session claims remote control: %s", body)
	}
	item := s.NowPlayingItem
	if item.ID != string(f.part2) || item.Name != "Part A" || item.Type != "Episode" || item.SeriesName != "Beta Group" {
		t.Fatalf("now playing = %+v", item)
	}
	if item.IndexNumber == nil || *item.IndexNumber != 1 || item.MediaSources != nil {
		t.Fatalf("now playing index/sources = %+v", item)
	}
	if item.RunTimeTicks != 2400*ticksPerSecond || s.PlayState.PositionTicks != 600*ticksPerSecond {
		t.Fatalf("ticks = %d/%d, want %d/%d", s.PlayState.PositionTicks, item.RunTimeTicks, 600*ticksPerSecond, 2400*ticksPerSecond)
	}
	// The ID is stable across polls, so a dashboard can key on it.
	if again, _ := f.sessions(t); len(again) != 1 || again[0].ID != s.ID {
		t.Fatalf("session ID changed between polls: %+v", again)
	}

	// A movie has no series fields.
	f.play(t, "session-movie", "profile-default", f.single, 30, 7200)
	got, body = f.sessions(t)
	if len(got) != 2 {
		t.Fatalf("sessions = %s, want two", body)
	}
	for _, s := range got {
		if s.NowPlayingItem.ID == string(f.single) && (s.NowPlayingItem.Type != "Movie" || s.NowPlayingItem.SeriesName != "" || s.NowPlayingItem.ParentIndexNumber != nil) {
			t.Fatalf("movie session = %+v", s.NowPlayingItem)
		}
	}

	// An ended playback drops at once.
	if err := f.st.EndPlayback(t.Context(), "session-default", f.part2); err != nil {
		t.Fatal(err)
	}
	if got, body = f.sessions(t); len(got) != 1 || got[0].NowPlayingItem.ID != string(f.single) {
		t.Fatalf("after end, sessions = %s, want only the movie", body)
	}
}

func TestJellyfinSessionsExcludeIneligibleProfiles(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := t.Context()
	on, off := true, false
	profile := func(name string, kind store.ProfileKind) store.ProfileID {
		p, err := f.st.CreateProfile(ctx, name, kind, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	optedOut := profile("Opted Out", store.ProfileAdult)
	disabled := profile("Disabled", store.ProfileAdult)
	if _, err := f.st.UpdateProfileSettings(ctx, disabled, store.ProfileSettings{IntegrationSignIn: &on}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.UpdateProfileSettings(ctx, disabled, store.ProfileSettings{Disabled: &on}); err != nil {
		t.Fatal(err)
	}
	managed := profile("Managed", store.ProfileManaged)
	_, _ = f.st.UpdateProfileSettings(ctx, managed, store.ProfileSettings{IntegrationSignIn: &on}) // refused for managed Profiles
	for i, p := range []store.ProfileID{optedOut, disabled, managed} {
		f.play(t, store.SessionID("session-"+string(rune('a'+i))), p, f.single, 10, 100)
	}
	if got, body := f.sessions(t); len(got) != 0 {
		t.Fatalf("sessions = %s, want none for opted-out, disabled and managed Profiles", body)
	}

	// Another opted-in Profile is listed until it opts out.
	second := profile("Second", store.ProfileAdult)
	if _, err := f.st.UpdateProfileSettings(ctx, second, store.ProfileSettings{IntegrationSignIn: &on}); err != nil {
		t.Fatal(err)
	}
	f.play(t, "session-second", second, f.single, 10, 100)
	if got, body := f.sessions(t); len(got) != 1 || got[0].UserName != "Second" {
		t.Fatalf("opted-in playback missing: %s", body)
	}
	if _, err := f.st.UpdateProfileSettings(ctx, second, store.ProfileSettings{IntegrationSignIn: &off}); err != nil {
		t.Fatal(err)
	}
	if got, body := f.sessions(t); len(got) != 0 {
		t.Fatalf("after opt-out, sessions = %s, want none", body)
	}
}

func TestJellyfinSessionsExcludeInvisibleItems(t *testing.T) {
	f := newCatalogFixture(t)
	for i, media := range []store.MediaID{f.hiddenItem, f.sealedM, f.unmapped} {
		f.play(t, store.SessionID("session-"+string(rune('a'+i))), "profile-default", media, 10, 100)
	}
	if got, body := f.sessions(t); len(got) != 0 {
		t.Fatalf("sessions = %s, want none for hidden, sealed and unmapped items", body)
	}
}

func TestJellyfinSessionsAndCountsRequireReadScope(t *testing.T) {
	f := newCatalogFixture(t)
	refreshOnly := scopedIntegrationToken(t, f.service, store.IntegrationScopeRefresh)
	for _, path := range []string{"Sessions", "Items/Counts"} {
		for _, token := range []string{"", refreshOnly} {
			if rec := sendCompat(f.handler, seerrRequest(t, http.MethodGet, path, token)); rec.Code != http.StatusUnauthorized {
				t.Fatalf("GET %s with token present=%v returned %d, want 401", path, token != "", rec.Code)
			}
		}
	}
}

func TestJellyfinItemCountsAreFiltered(t *testing.T) {
	f := newCatalogFixture(t)
	var counts map[string]int
	body := f.get(t, "Items/Counts", http.StatusOK)
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"MovieCount": 1, "EpisodeCount": 3, "SeriesCount": 1, "SeasonCount": 2}
	if len(counts) != len(want) {
		t.Fatalf("counts = %s, want %v", body, want)
	}
	for k, v := range want {
		if counts[k] != v {
			t.Fatalf("counts = %s, want %v (hidden, sealed and unmapped items uncounted)", body, want)
		}
	}
}

// A Profile switch keeps the session, so one session can hold grants for the
// same item under an opted-out and an opted-in Profile: only the opted-in
// Profile's playback is listed, under its own name.
func TestJellyfinSessionsKeepSwitchedProfilesApart(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := t.Context()
	other, err := f.st.CreateProfile(ctx, "Opted Out", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	// The opted-out grant sorts first (media-direct before media-hls).
	if err := f.st.RecordStreamToken(ctx, "session-default", other.ID, "device-tv", "media-direct", f.single, false, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, body := f.sessions(t); len(got) != 0 {
		t.Fatalf("opted-out playback listed: %s", body)
	}
	f.play(t, "session-default", "profile-default", f.single, 30, 7200)
	got, body := f.sessions(t)
	if len(got) != 1 || got[0].UserName != "Default" || got[0].PlayState.PositionTicks != 30*ticksPerSecond {
		t.Fatalf("sessions = %s, want only Default's playback", body)
	}
}

func (f catalogFixture) counts(t *testing.T) map[string]int {
	t.Helper()
	var counts map[string]int
	body := f.get(t, "Items/Counts", http.StatusOK)
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatal(err)
	}
	return counts
}

// Counts and Sessions read current rows through a warm index: an item whose
// type field no longer maps, or a container trashed since the index was
// built, drops at once, and every declared item type stays present at zero.
func TestJellyfinCountsAndSessionsReadCurrentRows(t *testing.T) {
	f := newCatalogFixture(t)
	ctx := t.Context()
	now := time.Unix(1_000_000, 0)
	f.handler.indexes.now = func() time.Time { return now }
	f.play(t, "session-movie", "profile-default", f.single, 30, 7200)
	if got := f.counts(t); got["MovieCount"] != 1 {
		t.Fatalf("counts = %v", got)
	}
	if got, body := f.sessions(t); len(got) != 1 {
		t.Fatalf("sessions = %s, want the movie", body)
	}
	var key string
	for key = range f.handler.indexes.entries {
	}
	warm := f.handler.indexes.recent(key)

	if err := f.st.UpsertMediaFields(ctx, f.single, []store.MediaField{{Name: "class", Type: store.EntityFieldText, Text: "not-declared"}}); err != nil {
		t.Fatal(err)
	}
	if got, body := f.sessions(t); len(got) != 0 {
		t.Fatalf("sessions after unmapping = %s, want none", body)
	}
	if err := f.st.TrashEntity(ctx, f.sub1, store.LibraryFilter{AllLibraries: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"MovieCount": 0, "EpisodeCount": 1, "SeriesCount": 1, "SeasonCount": 1}
	if got := f.counts(t); !mapsEqual(got, want) {
		t.Fatalf("counts after unmapping and season trash = %v, want %v", got, want)
	}
	if err := f.st.TrashEntity(ctx, f.group, store.LibraryFilter{AllLibraries: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	want = map[string]int{"MovieCount": 0, "EpisodeCount": 0, "SeriesCount": 0, "SeasonCount": 0}
	if got := f.counts(t); !mapsEqual(got, want) {
		t.Fatalf("counts with nothing visible = %v, want %v", got, want)
	}
	if f.handler.indexes.recent(key) != warm {
		t.Fatal("the reads rebuilt the index, so the warm path went untested")
	}
}

func mapsEqual(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if n, ok := got[k]; !ok || n != v {
			return false
		}
	}
	return true
}
