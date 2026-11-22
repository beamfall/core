package graph

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/observability"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// updateProfileAllArguments binds every updateProfile argument to the
// variable of the same name; a variable left out of a request reads as absent.
const updateProfileAllArguments = `mutation UpdateProfile($profileID: String!, $name: String, $kind: String, $disabled: Boolean, $maxConcurrentStreams: Int, $remoteMaxBitrate: Int, $transcodeAllowed: Boolean, $entryPIN: String, $audioLanguages: [String!], $subtitleLanguages: [String!], $subtitleMode: String, $preferSDH: Boolean) { updateProfile(profileID: $profileID, name: $name, kind: $kind, disabled: $disabled, maxConcurrentStreams: $maxConcurrentStreams, remoteMaxBitrate: $remoteMaxBitrate, transcodeAllowed: $transcodeAllowed, entryPIN: $entryPIN, audioLanguages: $audioLanguages, subtitleLanguages: $subtitleLanguages, subtitleMode: $subtitleMode, preferSDH: $preferSDH) { id name kind maxConcurrentStreams disabled audioLanguages } }`

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

	// The reply lists only the Library grants the caller can see: the
	// unrevealed viewer's Profile holds the restricted library-archive.
	assertOwnWriteLibraryIDs(t, handler, authorizeViewer, []string{"library-local"})
	assertOwnWriteLibraryIDs(t, handler, authorizeRevealed, []string{"library-local", "library-archive"})

	// A variable of any name binding profileID is the same write: authorization
	// and the resolver read the same effective arguments.
	body = ownTrackPreferenceBody(t,
		`mutation U($id: String!, $langs: [String!]) { updateProfile(profileID: $id, audioLanguages: $langs) { id audioLanguages } }`,
		map[string]any{"id": "profile-default", "langs": []string{"fr"}})
	data, errs = post(t, handler, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("renamed-variable own write errors = %#v", errs)
	}
	if err := json.Unmarshal(data["updateProfile"], &written); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(written.AudioLanguages, []string{"fr"}) {
		t.Fatalf("renamed-variable own write = %#v, want [fr]", written)
	}

	// An operation variable no argument binds counts for nothing: the first
	// occurrence of the two-target batch is allowed on its own, so refusing the
	// batch rests on its second target alone.
	body = ownTrackPreferenceBody(t,
		`mutation U($profileID: String!, $other: String!) { mine: updateProfile(profileID: $profileID, audioLanguages: ["de"]) { id } }`,
		map[string]any{"profileID": "profile-default", "other": "profile-managed"})
	if _, errs := post(t, handler, body, authorizeViewer); len(errs) != 0 {
		t.Fatalf("own write with an unbound variable errors = %#v", errs)
	}

	// An operation variable named like an argument the field does not bind is
	// not that argument: an unbound $subtitleMode leaves the stored mode
	// alone, and an unbound $name does not turn the write into a Console one.
	body = ownTrackPreferenceBody(t,
		`mutation U($profileID: String!, $subtitleMode: String) { updateProfile(profileID: $profileID, audioLanguages: ["it"]) { id audioLanguages subtitleMode } }`,
		map[string]any{"profileID": "profile-default", "subtitleMode": "smart"})
	data, errs = post(t, handler, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("own write with an unbound subtitleMode errors = %#v", errs)
	}
	if err := json.Unmarshal(data["updateProfile"], &written); err != nil {
		t.Fatal(err)
	}
	if written.SubtitleMode != "always" || !slices.Equal(written.AudioLanguages, []string{"it"}) {
		t.Fatalf("own write with an unbound subtitleMode = %#v, want mode always kept, audio [it]", written)
	}
	body = ownTrackPreferenceBody(t,
		`mutation U($profileID: String!, $name: String) { updateProfile(profileID: $profileID, preferSDH: false) { id name preferSDH } }`,
		map[string]any{"profileID": "profile-default", "name": "Renamed"})
	data, errs = post(t, handler, body, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("own write with an unbound name errors = %#v", errs)
	}
	if err := json.Unmarshal(data["updateProfile"], &written); err != nil {
		t.Fatal(err)
	}
	if written.Name == "Renamed" || written.PreferSDH {
		t.Fatalf("own write with an unbound name = %#v, want the name kept and preferSDH false", written)
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
	updateAll := updateProfileAllArguments
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

func assertOwnWriteLibraryIDs(t *testing.T, handler *Handler, authorize func(*http.Request), want []string) {
	t.Helper()
	body := ownTrackPreferenceBody(t,
		`mutation U($profileID: String!) { updateProfile(profileID: $profileID, preferSDH: true) { libraryIDs } }`,
		map[string]any{"profileID": "profile-default"})
	data, errs := post(t, handler, body, authorize)
	if len(errs) != 0 {
		t.Fatalf("own write errors = %#v", errs)
	}
	var reply struct {
		LibraryIDs []string `json:"libraryIDs"`
	}
	if err := json.Unmarshal(data["updateProfile"], &reply); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reply.LibraryIDs, want) {
		t.Fatalf("serialized libraryIDs = %s, want %v", data["updateProfile"], want)
	}
}

// TestOwnTrackPreferenceAuditNamesProfileAfterSwitch pins CORE-1937: after an
// Admin Session switches Profile, the updateProfile audit record names the
// Profile the write ran as, not the one the request arrived with.
func TestOwnTrackPreferenceAuditNamesProfileAfterSwitch(t *testing.T) {
	st := storetest.Open(t)
	openManagerAdminSession(t, st)
	handler := newTestHandlerWithStore(st)
	audit := &recordingAudit{}
	handler.SetAuditRecorder(audit)

	body := ownTrackPreferenceBody(t,
		`mutation M($profileID: String!) { switchProfile(profileID: $profileID) { profileID } updateProfile(profileID: $profileID, subtitleMode: "off") { id subtitleMode } }`,
		map[string]any{"profileID": "profile-managed"})
	if data, errs := post(t, handler, body, authorizeManager); len(errs) != 0 {
		t.Fatalf("switch then own write: data=%v errors=%#v", data, errs)
	}
	var finished []observability.AuditRecord
	for _, record := range audit.records {
		if record.Operation == "graphql:updateprofile" && record.Outcome == observability.AuditSuccess {
			finished = append(finished, record)
		}
	}
	if len(finished) != 1 || finished[0].Actor.ProfileID != "profile-managed" || finished[0].Actor.SessionID != "session-manager" {
		t.Fatalf("updateProfile audit = %+v (all: %+v), want one success naming profile-managed", finished, audit.records)
	}
}

// TestOwnTrackPreferenceWriteClassifiesEveryArgument: the self-service check
// names every updateProfile argument but profileID, so an argument added
// later cannot slip past it unclassified (CORE-1937).
func TestOwnTrackPreferenceWriteClassifiesEveryArgument(t *testing.T) {
	var want []string
	for _, arg := range fieldArgumentRegistry["updateprofile"] {
		if arg.Name != "profileID" {
			want = append(want, arg.Name)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(slices.Values(dynamicArgumentReads["ownTrackPreferenceWrite"])); !slices.Equal(got, want) {
		t.Fatalf("ownTrackPreferenceWrite classifies %q; updateProfile takes %q", got, want)
	}
}
