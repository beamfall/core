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
