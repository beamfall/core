package graph

import (
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestAdultProfileSwitchRequiresIndependentEntryPIN(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	setBody := `{"query":"mutation U($profileID: String!, $entryPIN: String!){ updateProfile(profileID:$profileID, entryPIN:$entryPIN){ id } }","variables":{"profileID":"profile-default","entryPIN":"2468"}}`
	if _, errs := post(t, h, setBody, authorizeManager); len(errs) != 0 {
		t.Fatalf("set entry PIN errors = %#v", errs)
	}
	switchBody := func(pin string) string {
		return `{"query":"mutation S($profileID: String!, $pin: String){ switchProfile(profileID:$profileID, pin:$pin){ profileID revealed } }","variables":{"profileID":"profile-default","pin":"` + pin + `"}}`
	}
	for _, pin := range []string{"", "1234", "0000"} {
		_, errs := post(t, h, switchBody(pin), authorizeManaged)
		if len(errs) != 1 || errs[0].Message != "profile PIN required" {
			t.Fatalf("switch with PIN %q errors = %#v", pin, errs)
		}
		session, err := st.Session(t.Context(), "session-managed")
		if err != nil {
			t.Fatal(err)
		}
		if session.ProfileID != "profile-managed" {
			t.Fatalf("rejected switch rebound session: %+v", session)
		}
	}
	data, errs := post(t, h, switchBody("2468"), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("correct PIN errors = %#v", errs)
	}
	var switched SessionInfo
	mustUnmarshal(t, data["switchProfile"], &switched)
	if switched.ProfileID != "profile-default" || switched.Revealed {
		t.Fatalf("switch result = %+v", switched)
	}
	if err := st.VerifyRevealPIN(t.Context(), store.ProfileID("profile-default"), "2468"); err == nil {
		t.Fatal("entry PIN also verified as Reveal PIN")
	}
}

// TestAdultProfileWithoutEntryPINSwitchesFreely covers CORE-2581: an adult
// Profile with no entry PIN opens without one, profiles reports entryPinSet so
// a picker can skip its prompt, and a Profile with a PIN still refuses a wrong one.
func TestAdultProfileWithoutEntryPINSwitchesFreely(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	open, err := st.CreateProfile(t.Context(), "Open Adult", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}

	data, errs := post(t, h, `{"query":"{ profiles { id entryPinSet } }"}`, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("profiles errors = %#v", errs)
	}
	var profiles []struct {
		ID          string `json:"id"`
		EntryPinSet *bool  `json:"entryPinSet"`
	}
	mustUnmarshal(t, data["profiles"], &profiles)
	want := map[string]bool{"profile-default": true, "profile-managed": false, string(open.ID): false}
	for id, pinSet := range want {
		found := false
		for _, p := range profiles {
			if p.ID != id {
				continue
			}
			found = true
			if p.EntryPinSet == nil || *p.EntryPinSet != pinSet {
				t.Fatalf("profile %s entryPinSet = %v, want %v", id, p.EntryPinSet, pinSet)
			}
		}
		if !found {
			t.Fatalf("profiles = %+v, missing %s", profiles, id)
		}
	}

	switchBody := func(profileID, pin string) string {
		return `{"query":"mutation S($profileID: String!, $pin: String){ switchProfile(profileID:$profileID, pin:$pin){ profileID revealed } }","variables":{"profileID":"` + profileID + `","pin":"` + pin + `"}}`
	}
	// The PIN-protected adult still refuses a wrong or missing PIN.
	for _, pin := range []string{"", "0000"} {
		if _, errs := post(t, h, switchBody("profile-default", pin), authorizeManaged); len(errs) != 1 || errs[0].Message != "profile PIN required" {
			t.Fatalf("switch to PIN adult with %q errors = %#v", pin, errs)
		}
	}
	// The PIN-less adult opens with no PIN.
	data, errs = post(t, h, switchBody(string(open.ID), ""), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("switch to PIN-less adult errors = %#v", errs)
	}
	var switched SessionInfo
	mustUnmarshal(t, data["switchProfile"], &switched)
	if switched.ProfileID != string(open.ID) || switched.Revealed {
		t.Fatalf("switch result = %+v", switched)
	}
}
