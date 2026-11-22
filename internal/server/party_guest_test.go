package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func partyGuestCall(t *testing.T, srv *Server, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	return devicePairingCall(t, srv, method, path, body, func(r *http.Request) {
		if token != "" {
			r.Header.Set(PartyGuestHeader, token)
		}
	})
}

func TestPartyGuestRoutes(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	host := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	if _, err := st.StartPartySession(ctx, store.PartyStartInput{
		Host: "profile-default", HostName: "Rowan", LibraryIDs: []store.LibraryID{"library-local"}, Filter: host,
	}); err != nil {
		t.Fatal(err)
	}
	view, _, err := st.PartySessionForHost(ctx, "profile-default", host)
	if err != nil {
		t.Fatal(err)
	}

	rec := partyGuestCall(t, srv, http.MethodPost, "/api/party/join", `{"code":"`+strings.ToLower(view.Session.JoinCode)+`","name":"Casey"}`, "")
	wantDevicePairingStatus(t, rec, http.StatusOK, "join")
	joined := decodeDevicePairing[map[string]string](t, rec)
	token := joined["token"]
	if !strings.HasPrefix(token, store.PartyGuestTokenPrefix) || len(token) != len(store.PartyGuestTokenPrefix)+43 ||
		joined["sessionID"] != view.Session.ID || joined["name"] != "Casey" || joined["expiresAt"] == "" {
		t.Fatalf("join = %v", joined)
	}

	// A guest token never authenticates the Graph API, whatever header carries it.
	for _, authorize := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set(PartyGuestHeader, token) },
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) },
		func(r *http.Request) { r.Header.Set("X-Beamfall-Session", token) },
	} {
		rec := devicePairingCall(t, srv, http.MethodPost, "/api/graphql", `{"query":"{ partySession { id } }"}`, authorize)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("graph with a guest token = %d: %s", rec.Code, rec.Body.String())
		}
	}

	rec = partyGuestCall(t, srv, http.MethodGet, "/api/party/guest/search?q=Media", "", token)
	wantDevicePairingStatus(t, rec, http.StatusOK, "search")
	search := decodeDevicePairing[struct {
		Items []partyGuestMedia `json:"items"`
	}](t, rec)
	if len(search.Items) != 1 || search.Items[0].MediaID != "media-alpha" {
		t.Fatalf("search = %+v", search.Items)
	}

	var entryIDs []string
	for range store.PartyMaxGuestWaiting {
		rec = partyGuestCall(t, srv, http.MethodPost, "/api/party/guest/request", `{"mediaID":"media-alpha"}`, token)
		wantDevicePairingStatus(t, rec, http.StatusOK, "request")
		entry := decodeDevicePairing[map[string]partyGuestEntry](t, rec)["entry"]
		if entry.ID == "" || entry.MediaID != "media-alpha" || entry.State != store.PartyEntryPending {
			t.Fatalf("request entry = %+v", entry)
		}
		entryIDs = append(entryIDs, entry.ID)
	}
	rec = partyGuestCall(t, srv, http.MethodPost, "/api/party/guest/request", `{"mediaID":"media-alpha"}`, token)
	wantDevicePairingStatus(t, rec, http.StatusTooManyRequests, "fourth request")
	if rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), `"rate-limited"`) {
		t.Fatalf("rate-limited request = %v %s", rec.Header(), rec.Body.String())
	}

	if err := st.ReviewPartyEntry(ctx, "profile-default", host, entryIDs[0], true); err != nil {
		t.Fatal(err)
	}
	if err := st.ReviewPartyEntry(ctx, "profile-default", host, entryIDs[1], false); err != nil {
		t.Fatal(err)
	}
	rec = partyGuestCall(t, srv, http.MethodPost, "/api/party/guest/cancel", `{"entryID":"`+entryIDs[1]+`"}`, token)
	wantDevicePairingStatus(t, rec, http.StatusNotFound, "cancel declined")
	rec = partyGuestCall(t, srv, http.MethodPost, "/api/party/guest/cancel", `{"entryID":"`+entryIDs[2]+`"}`, token)
	wantDevicePairingStatus(t, rec, http.StatusOK, "cancel pending")

	rec = partyGuestCall(t, srv, http.MethodGet, "/api/party/guest", "", token)
	wantDevicePairingStatus(t, rec, http.StatusOK, "guest view")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("guest view Cache-Control = %q", got)
	}
	guestView := decodeDevicePairing[partyGuestViewResponse](t, rec)
	if guestView.SessionID != view.Session.ID || guestView.MaxWaiting != store.PartyMaxGuestWaiting ||
		len(guestView.Entries) != 1 || guestView.Entries[0].MediaID != "" || len(guestView.Mine) != 2 {
		t.Fatalf("guest view = %+v", guestView)
	}
	states := map[string]string{}
	for _, entry := range guestView.Mine {
		if entry.MediaID != "media-alpha" {
			t.Fatalf("mine entry without mediaID: %+v", entry)
		}
		states[entry.ID] = entry.State
	}
	if states[entryIDs[0]] != store.PartyEntryWaiting || states[entryIDs[1]] != store.PartyEntryDeclined {
		t.Fatalf("mine states = %v", states)
	}

	rec = partyGuestCall(t, srv, http.MethodGet, "/api/party/guest", "", "bfg_unknown")
	wantDevicePairingStatus(t, rec, http.StatusUnauthorized, "unknown token")
	rec = partyGuestCall(t, srv, http.MethodGet, "/api/party/guest", "", "")
	wantDevicePairingStatus(t, rec, http.StatusUnauthorized, "missing token")

	// Unknown codes all answer alike until the source address is throttled.
	for i := range 10 {
		rec = partyGuestCall(t, srv, http.MethodPost, "/api/party/join", `{"code":"ZZZ`+string(rune('A'+i))+`","name":"Dana"}`, "")
		wantDevicePairingStatus(t, rec, http.StatusNotFound, "unknown code")
		if !strings.Contains(rec.Body.String(), store.ErrPartyJoinRefused.Error()) {
			t.Fatalf("unknown code body = %s", rec.Body.String())
		}
	}
	rec = partyGuestCall(t, srv, http.MethodPost, "/api/party/join", `{"code":"`+view.Session.JoinCode+`","name":"Dana"}`, "")
	wantDevicePairingStatus(t, rec, http.StatusTooManyRequests, "throttled join")
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("throttled join carries no Retry-After")
	}

	if _, err := st.EndPartySession(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	rec = partyGuestCall(t, srv, http.MethodGet, "/api/party/guest", "", token)
	wantDevicePairingStatus(t, rec, http.StatusUnauthorized, "ended session")
	if !strings.Contains(rec.Body.String(), `"party session ended"`) {
		t.Fatalf("ended session body = %s", rec.Body.String())
	}
}

func TestPartyGuestErrorBodies(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		body   string
	}{
		{store.ErrPartyFull, http.StatusConflict, `{"error":"full"}`},
		{store.ErrPartyWaitingLimit, http.StatusConflict, `{"error":"waiting-limit"}`},
		{store.PartyInputError{Message: "name is required"}, http.StatusBadRequest, `{"error":"name is required"}`},
		{errors.Join(store.ErrPartyGuestRefused), http.StatusUnauthorized, `{"error":"party session ended"}`},
	} {
		rec := httptest.NewRecorder()
		writePartyGuestError(rec, httptest.NewRequest(http.MethodPost, "/api/party/join", nil), test.err)
		if rec.Code != test.status || strings.TrimSpace(rec.Body.String()) != test.body {
			t.Errorf("%v = %d %s, want %d %s", test.err, rec.Code, rec.Body.String(), test.status, test.body)
		}
	}
}
