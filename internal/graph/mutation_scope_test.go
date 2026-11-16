package graph

import (
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// CORE-0742: every field of one mutation operation runs under the session's
// authority as it stands when that field runs, not as it stood when the
// request arrived.

// createAdultProfileWithPINs adds an adult Profile with its own entry and
// Reveal PINs, so a switch to it is allowed and its Reveal PIN differs from
// the seeded "1234".
func createAdultProfileWithPINs(t *testing.T, st *store.Store, name, entryPIN, revealPIN string) store.ProfileID {
	t.Helper()
	profile, err := st.CreateProfile(t.Context(), name, store.ProfileAdult, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileEntryPIN(t.Context(), profile.ID, entryPIN); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileRevealPIN(t.Context(), profile.ID, revealPIN); err != nil {
		t.Fatal(err)
	}
	return profile.ID
}

func profileQueueIDs(t *testing.T, st *store.Store, profileID store.ProfileID) []string {
	t.Helper()
	items, err := st.SourceItems(t.Context(), store.LibraryFilter{AllLibraries: true, Revealed: true}, store.SourceQuery{Kind: "queue", ProfileID: profileID})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = string(item.ID)
	}
	return ids
}

func TestAuditCrossProfileRevealBatch(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	profileB := createAdultProfileWithPINs(t, st, "Second", "2468", "9753")

	body := `{"query":"mutation M($profileID: String!, $entryPIN: String, $revealPIN: String!){ switchProfile(profileID:$profileID, pin:$entryPIN){ profileID } reveal(pin:$revealPIN){ revealed } }","variables":{"profileID":"` + string(profileB) + `","entryPIN":"2468","revealPIN":"1234"}}`
	data, errs := post(t, h, body, authorizeViewer)
	if len(errs) != 1 || errs[0].Message != "reveal PIN mismatch" {
		t.Fatalf("switch then reveal with the previous Profile's PIN: data=%s errors=%#v, want reveal PIN mismatch", data, errs)
	}
	session, err := st.Session(t.Context(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	if session.ProfileID != profileB || session.Revealed {
		t.Fatalf("session after batch = profile %q revealed %v, want %q unrevealed", session.ProfileID, session.Revealed, profileB)
	}

	// The new Profile's own PIN still reveals it after an earlier field.
	own := `{"query":"mutation M($revealPIN: String!){ relock { revealed } reveal(pin:$revealPIN){ revealed } }","variables":{"revealPIN":"9753"}}`
	if _, errs := post(t, h, own, authorizeViewer); len(errs) != 0 {
		t.Fatalf("relock then reveal with the current Profile's PIN errors = %#v", errs)
	}
	if session, err = st.Session(t.Context(), "session-default"); err != nil {
		t.Fatal(err)
	}
	if session.ProfileID != profileB || !session.Revealed {
		t.Fatalf("session after own-PIN batch = profile %q revealed %v, want %q revealed", session.ProfileID, session.Revealed, profileB)
	}
}

func TestMutationBatchUsesSwitchedProfileLibraryAndRevealScope(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	// session-revealed holds profile-default revealed, which can see the
	// restricted media-archive; profile-managed has no restricted Library.
	body := `{"query":"mutation M($profileID: String!, $mediaID: String!){ switchProfile(profileID:$profileID){ profileID } queueAdd(mediaID:$mediaID){ id } }","variables":{"profileID":"profile-managed","mediaID":"media-archive"}}`
	data, errs := post(t, h, body, authorizeRevealed)
	if len(errs) != 1 || errs[0].Message != "unknown media" {
		t.Fatalf("switch then queueAdd restricted media: data=%s errors=%#v, want unknown media", data, errs)
	}
	for _, profile := range []store.ProfileID{"profile-default", "profile-managed"} {
		if got := profileQueueIDs(t, st, profile); len(got) != 0 {
			t.Fatalf("%s queue = %v, want unchanged (empty)", profile, got)
		}
	}
	session, err := st.Session(t.Context(), "session-revealed")
	if err != nil {
		t.Fatal(err)
	}
	if session.ProfileID != "profile-managed" || session.Revealed {
		t.Fatalf("session after batch = profile %q revealed %v, want profile-managed unrevealed", session.ProfileID, session.Revealed)
	}
}

func TestMutationBatchRevealThenRevealGatedFieldUsesNewReveal(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	body := `{"query":"mutation M($pin: String!, $mediaID: String!){ reveal(pin:$pin){ revealed } queueAdd(mediaID:$mediaID){ id } }","variables":{"pin":"1234","mediaID":"media-archive"}}`
	if data, errs := post(t, h, body, authorizeViewer); len(errs) != 0 {
		t.Fatalf("reveal then queueAdd restricted media: data=%s errors=%#v", data, errs)
	}
	if got := profileQueueIDs(t, st, "profile-default"); len(got) != 1 || got[0] != "media-archive" {
		t.Fatalf("profile-default queue = %v, want [media-archive]", got)
	}
}

func TestMutationBatchLaterAdminFieldLosesClearedAdminSession(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	body := `{"query":"mutation M($profileID: String!, $name: String!){ switchProfile(profileID:$profileID){ profileID } createApiToken(name:$name){ token } }","variables":{"profileID":"profile-managed","name":"after-switch"}}`
	data, errs := post(t, h, body, authorizeManager)
	if len(errs) != 1 || errs[0].Message != "admin session required" {
		t.Fatalf("switch then createApiToken: data=%s errors=%#v, want admin session required", data, errs)
	}
	tokens, err := st.APITokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("api tokens after refused batch = %+v, want none", tokens)
	}
}

func TestMutationBatchStopsWhenSessionNoLongerResolves(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	// Revoking the caller's own device leaves its session unresolvable, so the
	// later field must not run under the stale scope.
	body := `{"query":"mutation M($deviceID: String!, $mediaID: String!){ revokeDevice(deviceID:$deviceID){ deviceID } queueAdd(mediaID:$mediaID){ id } }","variables":{"deviceID":"device-mac","mediaID":"media-alpha"}}`
	data, errs := post(t, h, body, authorizeManager)
	if len(errs) != 1 || errs[0].Message != "invalid session" || errs[0].Extensions.Code != ErrorCodeUnauthenticated {
		t.Fatalf("revoke own device then queueAdd: data=%s errors=%#v, want UNAUTHENTICATED invalid session", data, errs)
	}
	if got := profileQueueIDs(t, st, "profile-default"); len(got) != 0 {
		t.Fatalf("profile-default queue = %v, want unchanged (empty)", got)
	}
}
