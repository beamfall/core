package graph

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const ownTrackPreferenceMutation = `mutation UpdateTrackPreferences($profileID: String!, $audioLanguages: [String!], $subtitleLanguages: [String!], $subtitleMode: String, $preferSDH: Boolean) { updateProfile(profileID: $profileID, audioLanguages: $audioLanguages, subtitleLanguages: $subtitleLanguages, subtitleMode: $subtitleMode, preferSDH: $preferSDH) { id audioLanguages subtitleLanguages subtitleMode preferSDH } }`

func ownTrackPreferenceBody(t *testing.T, query string, variables map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestNonAdminSessionWritesOwnTrackPreferences pins CORE-1937: a session with
// no Admin Session writes its own Profile's audio and subtitle preferences
// through updateProfile, and another device's session for that Profile reads
// them back from currentSession.
func TestNonAdminSessionWritesOwnTrackPreferences(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	body := ownTrackPreferenceBody(t, ownTrackPreferenceMutation, map[string]any{
		"profileID":         "profile-default",
		"audioLanguages":    []string{"ja", "en"},
		"subtitleLanguages": []string{"en"},
		"subtitleMode":      "always",
		"preferSDH":         true,
	})
	data, errs := post(t, handler, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("own track preference write errors = %#v", errs)
	}
	var written ProfileInfo
	if err := json.Unmarshal(data["updateProfile"], &written); err != nil {
		t.Fatal(err)
	}
	if written.ID != "profile-default" || !slices.Equal(written.AudioLanguages, []string{"ja", "en"}) {
		t.Fatalf("updateProfile = %#v, want profile-default with [ja en]", written)
	}

	// session-manager is the same Profile on another device (device-mac).
	data, errs = post(t, handler, `{"query":"query { currentSession { profileID audioLanguages subtitleLanguages subtitleMode preferSDH } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("currentSession errors = %#v", errs)
	}
	var session SessionInfo
	if err := json.Unmarshal(data["currentSession"], &session); err != nil {
		t.Fatal(err)
	}
	if session.ProfileID != "profile-default" ||
		!slices.Equal(session.AudioLanguages, []string{"ja", "en"}) ||
		!slices.Equal(session.SubtitleLanguages, []string{"en"}) ||
		session.SubtitleMode != "always" || !session.PreferSDH {
		t.Fatalf("currentSession on another device = %#v, want the written track preferences", session)
	}

	// A managed Profile writes its own track preferences the same way.
	body = ownTrackPreferenceBody(t, ownTrackPreferenceMutation, map[string]any{
		"profileID":    "profile-managed",
		"subtitleMode": "off",
	})
	if _, errs := post(t, handler, body, authorizeManaged); len(errs) != 0 {
		t.Fatalf("managed own track preference write errors = %#v", errs)
	}
}

// TestNonAdminSessionCannotWriteOtherProfileFields pins the CORE-1937
// boundary: without an Admin Session, every other Profile policy field and
// every other Profile's preferences stay refused, and a refused write changes
// nothing.
func TestNonAdminSessionCannotWriteOtherProfileFields(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	profileIDs := []store.ProfileID{"profile-default", "profile-managed"}
	before := map[store.ProfileID]store.Profile{}
	for _, id := range profileIDs {
		profile, err := st.Profile(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		before[id] = profile
	}
	pinsBefore, err := st.ProfileEntryPINsSet(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	updateAll := `mutation U($profileID: String!) { updateProfile(profileID: $profileID) { id } }`
	cases := map[string]string{
		"own name": ownTrackPreferenceBody(t, updateAll, map[string]any{
			"profileID": "profile-default", "name": "Renamed",
		}),
		"own disabled with track preferences": ownTrackPreferenceBody(t, updateAll, map[string]any{
			"profileID": "profile-default", "audioLanguages": []string{"de"}, "disabled": true,
		}),
		"own kind": ownTrackPreferenceBody(t, updateAll, map[string]any{
			"profileID": "profile-default", "kind": "managed",
		}),
		"own stream cap": ownTrackPreferenceBody(t, updateAll, map[string]any{
			"profileID": "profile-default", "maxConcurrentStreams": 1,
		}),
		"own transcode gate": ownTrackPreferenceBody(t, updateAll, map[string]any{
			"profileID": "profile-default", "transcodeAllowed": false,
		}),
		"own entry PIN": ownTrackPreferenceBody(t, updateAll, map[string]any{
			"profileID": "profile-default", "entryPIN": "9999",
		}),
		"another profile's track preferences": ownTrackPreferenceBody(t, ownTrackPreferenceMutation, map[string]any{
			"profileID": "profile-managed", "audioLanguages": []string{"de"},
		}),
		"another profile through a second occurrence": ownTrackPreferenceBody(t,
			`mutation U($profileID: String!, $other: String!) { mine: updateProfile(profileID: $profileID, audioLanguages: ["de"]) { id } theirs: updateProfile(profileID: $other, audioLanguages: ["de"]) { id } }`,
			map[string]any{"profileID": "profile-default", "other": "profile-managed"}),
		"no track preference": ownTrackPreferenceBody(t, updateAll, map[string]any{
			"profileID": "profile-default",
		}),
	}
	for name, body := range cases {
		for _, authorize := range []struct {
			session string
			apply   func(*http.Request)
		}{
			{"viewer", authorizeViewer},
			{"manager without Admin Session", authorizeManager},
		} {
			_, errs := post(t, handler, body, authorize.apply)
			if len(errs) == 0 || errs[0].Message != "admin session required" {
				t.Errorf("%s via %s: errors = %#v, want \"admin session required\"", name, authorize.session, errs)
			}
		}
	}

	// A managed session cannot write the adult Profile's preferences either.
	body := ownTrackPreferenceBody(t, ownTrackPreferenceMutation, map[string]any{
		"profileID": "profile-default", "subtitleMode": "off",
	})
	if _, errs := post(t, handler, body, authorizeManaged); len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Errorf("managed session writing profile-default: errors = %#v, want \"admin session required\"", errs)
	}

	for _, id := range profileIDs {
		profile, err := st.Profile(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(profile, before[id]) {
			t.Errorf("%s changed by a refused write: %#v, want %#v", id, profile, before[id])
		}
	}
	pinsAfter, err := st.ProfileEntryPINsSet(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pinsAfter, pinsBefore) {
		t.Errorf("entry PINs changed by a refused write: %v, want %v", pinsAfter, pinsBefore)
	}
}
