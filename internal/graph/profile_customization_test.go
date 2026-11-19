package graph

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const (
	reorderProfilesMutation  = `mutation R($profileIDs: [String!]!) { reorderProfiles(profileIDs: $profileIDs) { id } }`
	setProfileAvatarMutation = `mutation A($profileID: String!, $avatar: String!) { setProfileAvatar(profileID: $profileID, avatar: $avatar) { id avatar pictureURL } }`
)

func pickerOrder(t *testing.T, handler *Handler) []string {
	t.Helper()
	resp := postGraphJSON(t, handler, authorizeManager, `query { profiles { id } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("profiles errors = %#v", resp.Errors)
	}
	var profiles []ProfileSummary
	mustUnmarshal(t, resp.Data["profiles"], &profiles)
	ids := make([]string, 0, len(profiles))
	for _, p := range profiles {
		ids = append(ids, p.ID)
	}
	return ids
}

// TestReorderProfilesSetsPickerOrder covers CORE-2477: an Admin Session sets
// the order, the mutation returns it and the profiles query follows it.
func TestReorderProfilesSetsPickerOrder(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	before := pickerOrder(t, handler)
	if len(before) < 2 {
		t.Fatalf("seed has %d profiles, want at least 2", len(before))
	}
	want := slices.Clone(before)
	slices.Reverse(want)
	resp := postGraphJSON(t, handler, authorizeManager, reorderProfilesMutation, map[string]any{"profileIDs": want})
	if len(resp.Errors) != 0 {
		t.Fatalf("reorderProfiles errors = %#v", resp.Errors)
	}
	var returned []ProfileSummary
	mustUnmarshal(t, resp.Data["reorderProfiles"], &returned)
	got := make([]string, 0, len(returned))
	for _, p := range returned {
		got = append(got, p.ID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("reorderProfiles returned %v, want %v", got, want)
	}
	if got := pickerOrder(t, handler); !slices.Equal(got, want) {
		t.Fatalf("profiles order = %v, want %v", got, want)
	}

	if resp := postGraphJSON(t, handler, authorizeManager, reorderProfilesMutation, map[string]any{"profileIDs": []string{"profile-nobody"}}); len(resp.Errors) == 0 {
		t.Fatal("reorderProfiles accepted an unknown Profile")
	}
}

// TestSetProfileAvatarAndPictureURL covers CORE-1241 and CORE-1240's graph
// half: an Admin sets another Profile's avatar token, and a stored picture
// surfaces as a versioned pictureURL.
func TestSetProfileAvatarAndPictureURL(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	openManagerAdminSession(t, st)

	resp := postGraphJSON(t, handler, authorizeManager, setProfileAvatarMutation, map[string]any{"profileID": "profile-default", "avatar": "star.3"})
	if len(resp.Errors) != 0 {
		t.Fatalf("setProfileAvatar errors = %#v", resp.Errors)
	}
	var profile ProfileSummary
	mustUnmarshal(t, resp.Data["setProfileAvatar"], &profile)
	if profile.ID != "profile-default" || profile.Avatar != "star.3" || profile.PictureURL != "" {
		t.Fatalf("setProfileAvatar = %+v, want profile-default with avatar star.3 and no picture", profile)
	}

	version, err := st.SetProfilePicture(context.Background(), "profile-default", []byte{0xff, 0xd8, 0xff, 0xd9}, time.UnixMilli(1_700_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	resp = postGraphJSON(t, handler, authorizeViewer, `query { profiles { id avatar pictureURL } }`, nil)
	if len(resp.Errors) != 0 {
		t.Fatalf("profiles errors = %#v", resp.Errors)
	}
	var profiles []ProfileSummary
	mustUnmarshal(t, resp.Data["profiles"], &profiles)
	for _, p := range profiles {
		if p.ID == "profile-default" {
			want := "/api/profiles/picture/profile-default?v=2-" + strconv.FormatInt(version, 10)
			if p.PictureURL != want || p.Avatar != "star.3" {
				t.Fatalf("profile-default = %+v, want avatar star.3 and pictureURL %s", p, want)
			}
		} else if p.PictureURL != "" {
			t.Fatalf("%s pictureURL = %q, want empty", p.ID, p.PictureURL)
		}
	}

	if resp := postGraphJSON(t, handler, authorizeManager, setProfileAvatarMutation, map[string]any{"profileID": "profile-nobody", "avatar": "star.3"}); len(resp.Errors) == 0 {
		t.Fatal("setProfileAvatar accepted an unknown Profile")
	}
}

// Both mutations need an Admin Session; nothing changes when refused.
func TestProfileCustomizationRequiresAdminSession(t *testing.T) {
	st := storetest.Open(t)
	handler := newConsoleHandler(t, st)
	before, err := st.Profiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(before))
	for _, p := range before {
		ids = append(ids, string(p.ID))
	}
	slices.Reverse(ids)
	for name, authorize := range map[string]func(*http.Request){
		"manager without admin session": authorizeManager,
		"viewer":                        authorizeViewer,
	} {
		if resp := postGraphJSON(t, handler, authorize, reorderProfilesMutation, map[string]any{"profileIDs": ids}); len(resp.Errors) == 0 {
			t.Fatalf("%s: reorderProfiles accepted", name)
		}
		if resp := postGraphJSON(t, handler, authorize, setProfileAvatarMutation, map[string]any{"profileID": "profile-managed", "avatar": "star.3"}); len(resp.Errors) == 0 {
			t.Fatalf("%s: setProfileAvatar accepted", name)
		}
	}
	after, err := st.Profiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(before, after, func(a, b store.Profile) bool { return a.ID == b.ID }) {
		t.Fatal("refused reorderProfiles changed the order")
	}
}
