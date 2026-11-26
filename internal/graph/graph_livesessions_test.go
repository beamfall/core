package graph

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestActiveSessionIDsByDevicePartitionsAndDeduplicatesRegardlessOfOrder(t *testing.T) {
	first := []store.ActiveSession{
		{DeviceID: "device-a", SessionID: "session-1"},
		{DeviceID: "device-b", SessionID: "session-3"},
		{DeviceID: "device-a", SessionID: "session-1"},
		{DeviceID: "device-c", SessionID: "session-1"},
		{DeviceID: "device-a", SessionID: "session-2"},
		{DeviceID: "device-b", SessionID: "session-3"},
	}
	second := []store.ActiveSession{
		first[1], first[0], first[5], first[2], first[3], first[4],
	}
	want := map[store.DeviceID][]store.SessionID{
		"device-a": {"session-1", "session-2"},
		"device-b": {"session-3"},
		"device-c": {"session-1"},
	}

	for _, active := range [][]store.ActiveSession{first, second} {
		if got := activeSessionIDsByDevice(active); !reflect.DeepEqual(got, want) {
			t.Fatalf("activeSessionIDsByDevice() = %v, want %v", got, want)
		}
	}
}

func TestManagedLiveSessionsAdultSeesManagedOnly(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	adult2, err := st.CreateProfile(t.Context(), "Adult Two", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile adult2: %v", err)
	}
	if _, errs := post(t, handler,
		`{"query":"mutation($id: String!) { switchProfile(profileID: $id) { id } }","variables":{"id":"`+string(adult2.ID)+`"}}`,
		authorizeManager); len(errs) != 0 {
		t.Fatalf("switchProfile adult2 errs = %#v", errs)
	}

	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)
	recordActive(t, st, "session-manager", adult2.ID, "device-tv", "media-direct", "media-alpha", false)
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false)

	data, errs := post(t, handler,
		`{"query":"query { managedLiveSessions { sessionID profileID profileName mediaID title } }"}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("managedLiveSessions errors = %#v", errs)
	}
	var got []LiveSessionInfo
	if err := json.Unmarshal(data["managedLiveSessions"], &got); err != nil {
		t.Fatalf("decode managedLiveSessions: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "session-managed" || got[0].ProfileID != "profile-managed" {
		t.Fatalf("managedLiveSessions = %#v, want only session-managed", got)
	}
	if got[0].Title != "Sample Media" {
		t.Errorf("managedLiveSessions title = %q, want Sample Media", got[0].Title)
	}

	_, errs = post(t, handler,
		`{"query":"query { managedLiveSessions { sessionID } }"}`,
		func(req *http.Request) {
			req.Header.Set("X-Beamfall-Session", "session-managed")
			req.Header.Set("X-Beamfall-Device", "device-tv")
			req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
		})
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "only an adult profile") {
		t.Fatalf("managed caller managedLiveSessions errs = %#v, want adult-only error", errs)
	}
}
