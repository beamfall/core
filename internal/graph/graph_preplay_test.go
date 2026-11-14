package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type fakePrePlayPolicy struct {
	plan PrePlayPlan
	err  error
	got  []PrePlayRequest
}

func (f *fakePrePlayPolicy) PlanPrePlay(_ context.Context, req PrePlayRequest) (PrePlayPlan, error) {
	f.got = append(f.got, req)
	return f.plan, f.err
}

func importPrePlayMedia(t *testing.T, st *store.Store, library store.LibraryID, name string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID:          library,
		Path:               "/library/" + name + ".mkv",
		Title:              name,
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: "sha256:" + name,
	})
	if err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
	return media.ID
}

func postPrePlay(t *testing.T, handler http.Handler, authorize func(*http.Request), body string) (map[string]json.RawMessage, []string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	authorize(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	messages := make([]string, 0, len(resp.Errors))
	for _, e := range resp.Errors {
		messages = append(messages, e.Message)
	}
	return resp.Data, messages
}

const prePlayQueueQuery = `{"query":"query Q($mediaID: String!) { prePlayQueue(mediaID: $mediaID) { mediaID title durationSeconds role label } }","variables":{"mediaID":"media-alpha"}}`

func prePlayQueueIDs(t *testing.T, handler http.Handler) ([]PrePlayItem, []string) {
	t.Helper()
	data, errs := postPrePlay(t, handler, authorizeViewer, prePlayQueueQuery)
	if len(errs) != 0 {
		return nil, errs
	}
	var items []PrePlayItem
	if err := json.Unmarshal(data["prePlayQueue"], &items); err != nil {
		t.Fatalf("decode prePlayQueue %s: %v", data["prePlayQueue"], err)
	}
	return items, nil
}

func TestPrePlayQueueOrdersClipsThenSuggestedAndFiltersVisibility(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	h := newTestHandlerWithStore(st)
	clipA := importPrePlayMedia(t, st, "library-local", "clip-a")
	clipB := importPrePlayMedia(t, st, "library-local", "clip-b")
	suggested := importPrePlayMedia(t, st, "library-local", "suggested-one")
	policy := &fakePrePlayPolicy{plan: PrePlayPlan{
		Eligible: true,
		Label:    "Preview",
		// media-archive sits in a restricted Library the viewer cannot see;
		// media-alpha is the feature itself; clipA is already a clip.
		MediaIDs: []store.MediaID{"media-archive", suggested, "media-alpha", clipA},
	}}
	h.prePlayPolicy = policy
	if _, err := st.SetPrePlayClips(ctx, []store.MediaID{clipB, "media-archive", clipA}, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Not opted in: empty, and the policy is never asked.
	items, errs := prePlayQueueIDs(t, h)
	if len(errs) != 0 || len(items) != 0 || len(policy.got) != 0 {
		t.Fatalf("disabled profile: items %v errors %q calls %d", items, errs, len(policy.got))
	}

	if err := st.SetClientPref(ctx, "profile-default", prePlayEnabledKey, "on", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClientPref(ctx, "profile-default", prePlayExtrasKey, "3", time.Now()); err != nil {
		t.Fatal(err)
	}
	items, errs = prePlayQueueIDs(t, h)
	if len(errs) != 0 {
		t.Fatalf("errors %q", errs)
	}
	want := []PrePlayItem{
		{MediaID: string(clipB), Title: "clip-b", Role: PrePlayRoleClip},
		{MediaID: string(clipA), Title: "clip-a", Role: PrePlayRoleClip},
		{MediaID: string(suggested), Title: "suggested-one", Role: PrePlayRoleSuggested, Label: "Preview"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v, want %+v", items, want)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Fatalf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
	got := policy.got[len(policy.got)-1]
	if got.FeatureID != "media-alpha" || got.Count != 3 || got.ProfileID != "profile-default" || !got.Filter.PlaybackGate {
		t.Fatalf("policy request = %+v", got)
	}
}

func TestPrePlayQueueEmptyWhenIneligibleOrPolicyFails(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	h := newTestHandlerWithStore(st)
	clip := importPrePlayMedia(t, st, "library-local", "clip-a")
	if _, err := st.SetPrePlayClips(ctx, []store.MediaID{clip}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SetClientPref(ctx, "profile-default", prePlayEnabledKey, "on", time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, policy := range map[string]PrePlayPolicy{
		"no policy":  nil,
		"ineligible": &fakePrePlayPolicy{plan: PrePlayPlan{MediaIDs: []store.MediaID{clip}}},
		"error":      &fakePrePlayPolicy{plan: PrePlayPlan{Eligible: true}, err: errors.New("boom")},
	} {
		h.prePlayPolicy = policy
		items, errs := prePlayQueueIDs(t, h)
		if len(errs) != 0 || len(items) != 0 {
			t.Fatalf("%s: items %v errors %q, want empty", name, items, errs)
		}
	}

	h.prePlayPolicy = &fakePrePlayPolicy{plan: PrePlayPlan{Eligible: true}}
	hidden := `{"query":"query Q($mediaID: String!) { prePlayQueue(mediaID: $mediaID) { mediaID } }","variables":{"mediaID":"media-archive"}}`
	if _, errs := postPrePlay(t, h, authorizeViewer, hidden); len(errs) == 0 {
		t.Fatal("hidden feature: want error")
	}
}

func TestPrePlaySettingsDefaultsAndClamps(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	h := newTestHandlerWithStore(st)
	if enabled, _ := h.prePlaySettings(ctx, ""); enabled {
		t.Fatal("blank profile enabled")
	}
	if err := st.SetClientPref(ctx, "profile-default", prePlayEnabledKey, "on", time.Now()); err != nil {
		t.Fatal(err)
	}
	if enabled, count := h.prePlaySettings(ctx, "profile-default"); !enabled || count != prePlayDefaultCount {
		t.Fatalf("defaults = %v %d", enabled, count)
	}
	for value, want := range map[string]int{"99": prePlayMaxCount, "-4": 0, "junk": prePlayDefaultCount, " 5 ": 5} {
		if err := st.SetClientPref(ctx, "profile-default", prePlayExtrasKey, value, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, count := h.prePlaySettings(ctx, "profile-default"); count != want {
			t.Fatalf("extras %q: count %d, want %d", value, count, want)
		}
	}
	if prePlaySeed("p", "m", time.Unix(0, 0)) == prePlaySeed("p", "m", time.Unix(86400, 0)) {
		t.Fatal("seed does not vary by day")
	}
}

func TestPrePlayClipsAreAdminGatedAndVisibilityChecked(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	clip := importPrePlayMedia(t, st, "library-local", "clip-a")
	set := func(ids string) string {
		return `{"query":"mutation S($mediaIDs: [String!]!) { setPrePlayClips(mediaIDs: $mediaIDs) { items { mediaID title role } } }","variables":{"mediaIDs":` + ids + `}}`
	}
	const read = `{"query":"query { prePlayClips { items { mediaID title } } }"}`

	for name, body := range map[string]string{"read": read, "set": set(`["` + string(clip) + `"]`)} {
		if _, errs := postPrePlay(t, h, authorizeManager, body); len(errs) == 0 || errs[0] != "admin session required" {
			t.Fatalf("%s without Admin Session: errors %q", name, errs)
		}
	}
	openManagerAdminSession(t, st)
	if _, errs := postPrePlay(t, h, authorizeManager, set(`["media-missing"]`)); len(errs) == 0 {
		t.Fatal("unknown clip: want error")
	}
	if _, errs := postPrePlay(t, h, authorizeManager, `{"query":"mutation { setPrePlayClips { items { mediaID } } }"}`); len(errs) == 0 {
		t.Fatal("missing mediaIDs: want error")
	}
	data, errs := postPrePlay(t, h, authorizeManager, set(`["`+string(clip)+`","`+string(clip)+`"]`))
	if len(errs) != 0 {
		t.Fatalf("set errors %q", errs)
	}
	var info PrePlayClipsInfo
	if err := json.Unmarshal(data["setPrePlayClips"], &info); err != nil || len(info.Items) != 1 || info.Items[0].MediaID != string(clip) {
		t.Fatalf("setPrePlayClips = %s (%v)", data["setPrePlayClips"], err)
	}
	data, errs = postPrePlay(t, h, authorizeManager, read)
	if err := json.Unmarshal(data["prePlayClips"], &info); err != nil || len(errs) != 0 || len(info.Items) != 1 {
		t.Fatalf("prePlayClips = %s errors %q (%v)", data["prePlayClips"], errs, err)
	}
	data, errs = postPrePlay(t, h, authorizeManager, set(`[]`))
	if err := json.Unmarshal(data["setPrePlayClips"], &info); err != nil || len(errs) != 0 || len(info.Items) != 0 {
		t.Fatalf("clear = %s errors %q (%v)", data["setPrePlayClips"], errs, err)
	}
}
