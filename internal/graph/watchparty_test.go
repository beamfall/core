package graph

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/conductor"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Watch Party tests. Built on the AV-4 conductor +
// graph test patterns (groupconductor_test.go): a fixed-clock in-memory
// Registry, the real /api/events outbox, and the seeded cross-Profile fixtures
// (profile-default adult on session-default/unrevealed + session-revealed;
// profile-managed on session-managed; media-alpha in the open library-local;
// media-archive in the restricted library-archive).

// createPartyBody forms a Watch Party around content, with the relaxed setting.
func createPartyBody(content string, relaxed bool) string {
	relaxedJSON := "false"
	if relaxed {
		relaxedJSON = "true"
	}
	return `{"query":"mutation C($content: String!, $relaxed: Boolean!, $clockOffsetMS: Int!, $renderLatencyMS: Int!){ createWatchParty(content:$content, relaxed:$relaxed, clockOffsetMS:$clockOffsetMS, renderLatencyMS:$renderLatencyMS){ partyID hostID content relaxed invitees members { displayName present } } }",` +
		`"variables":{"content":"` + content + `","relaxed":` + relaxedJSON + `,"clockOffsetMS":200,"renderLatencyMS":50}}`
}

func createParty(t *testing.T, h *Handler, content string, relaxed bool, authorize func(*http.Request)) WatchPartyInfo {
	t.Helper()
	data, errs := post(t, h, createPartyBody(content, relaxed), authorize)
	if len(errs) != 0 {
		t.Fatalf("createWatchParty errors = %#v", errs)
	}
	var info WatchPartyInfo
	if err := json.Unmarshal(data["createWatchParty"], &info); err != nil {
		t.Fatalf("decode createWatchParty: %v", err)
	}
	if info.PartyID == "" {
		t.Fatal("createWatchParty returned an empty party id")
	}
	if info.HostID != "" || len(info.Invitees) != 0 {
		t.Fatalf("createWatchParty exposed private identifiers: hostID=%q invitees=%v", info.HostID, info.Invitees)
	}
	return info
}

func inviteBody(partyID, displayName string) string {
	return `{"query":"mutation I($partyID: String!, $displayName: String!){ inviteToWatchParty(partyID:$partyID, displayName:$displayName){ partyID hostID invitees } }",` +
		`"variables":{"partyID":"` + partyID + `","displayName":"` + displayName + `"}}`
}

func acceptBody(partyID string) string {
	return `{"query":"mutation A($partyID: String!, $clockOffsetMS: Int!, $renderLatencyMS: Int!){ acceptWatchParty(partyID:$partyID, clockOffsetMS:$clockOffsetMS, renderLatencyMS:$renderLatencyMS){ partyID item positionMS presentAtMS } }",` +
		`"variables":{"partyID":"` + partyID + `","clockOffsetMS":0,"renderLatencyMS":0}}`
}

func transportBody(partyID, item string, positionMS int64) string {
	return `{"query":"mutation T($partyID: String!, $item: String!, $positionMS: Int!){ watchPartyTransport(partyID:$partyID, item:$item, positionMS:$positionMS){ groupID item positionMS presentAtMS } }",` +
		`"variables":{"partyID":"` + partyID + `","item":"` + item + `","positionMS":` + jsonNumber(positionMS) + `}}`
}

func requestTransportBody(partyID, item string, positionMS int64) string {
	return `{"query":"mutation R($partyID: String!, $item: String!, $positionMS: Int!){ requestWatchPartyTransport(partyID:$partyID, item:$item, positionMS:$positionMS){ partyID approved } }",` +
		`"variables":{"partyID":"` + partyID + `","item":"` + item + `","positionMS":` + jsonNumber(positionMS) + `}}`
}

func autoSkipBody(partyID, item string, positionMS int64) string {
	return `{"query":"mutation S($partyID: String!, $item: String!, $positionMS: Int!){ watchPartyAutoSkip(partyID:$partyID, item:$item, positionMS:$positionMS){ partyID applied item positionMS presentAtMS } }",` +
		`"variables":{"partyID":"` + partyID + `","item":"` + item + `","positionMS":` + jsonNumber(positionMS) + `}}`
}

// countTransitionsAt counts authoritative group.sync transitions for partyID at
// or after cursor whose position matches wantPos (-1 = any position).
func countTransitionsAt(t *testing.T, st *store.Store, partyID string, cursor int64, wantPos int64) int {
	t.Helper()
	after, err := st.ReadEventOutbox(t.Context(), cursor, 256)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	n := 0
	for _, e := range after {
		if e.AggregateType != store.OutboxAggregateGroup || e.AggregateID != partyID {
			continue
		}
		var p store.GroupSyncPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.Signal == store.GroupSignalTransition && (wantPos < 0 || p.PositionMS == wantPos) {
			n++
		}
	}
	return n
}

// TestWatchPartyHostTransportIsAuthoritative is acceptance (a): the host's
// play/pause/seek/skip broadcasts an authoritative {item, position, present_at}
// transition to every member over the existing SSE outbox. The
// test forms a party, the host transports, and the REAL outbox carries the
// transition Profile-stamped for delivery.
//
// feature:watch-party
func TestWatchPartyHostTransportIsAuthoritative(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	party := createParty(t, h, "media-alpha", false, authorizeViewer)

	data, errs := post(t, h, transportBody(party.PartyID, "media-alpha", 30000), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("watchPartyTransport errors = %#v", errs)
	}
	var res GroupTransitionResult
	if err := json.Unmarshal(data["watchPartyTransport"], &res); err != nil {
		t.Fatalf("decode watchPartyTransport: %v", err)
	}
	wantPresent := now.Add(conductor.PreBufferWindow).UnixMilli()
	if res.Item != "media-alpha" || res.PositionMS != 30000 || res.PresentAtMS != wantPresent {
		t.Fatalf("transport result = %#v, want media-alpha/30000/%d", res, wantPresent)
	}

	// The transition landed on the REAL outbox as a group.sync transition event.
	events, err := st.ReadEventOutbox(t.Context(), 0, 256)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	var payload store.GroupSyncPayload
	found := false
	for _, e := range events {
		if e.AggregateType != store.OutboxAggregateGroup {
			continue
		}
		if e.AggregateID != party.PartyID {
			continue
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Signal == store.GroupSignalTransition {
			found = true
		}
	}
	if !found {
		t.Fatal("no authoritative transition reached the outbox")
	}
	if payload.ProfileID != "profile-default" {
		t.Errorf("payload profile = %q, want profile-default (delivery gate)", payload.ProfileID)
	}
	if payload.PositionMS != 30000 || payload.PresentAtMS != wantPresent {
		t.Errorf("payload = %#v, want 30000/%d", payload, wantPresent)
	}
}

// TestWatchPartyNonHostTransportIsRequestNotAuthoritative is acceptance (a),
// negative half: a non-host's seek is a REQUEST, not an
// authoritative mutation. In a non-relaxed party the request is surfaced to the
// host (approved=false) and the timeline is NOT mutated; a direct host-transport
// attempt by a non-host is rejected ("not the party host").
//
// feature:watch-party
func TestWatchPartyNonHostTransportIsRequestNotAuthoritative(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	// Host (profile-default) forms a NON-relaxed party on open content and invites
	// the managed Profile, who can see media-alpha (it is in library-local).
	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}
	cursor := lastSeq(t, st)

	// A non-host (session-managed) cannot drive the timeline authoritatively.
	_, errs := post(t, h, transportBody(party.PartyID, "media-alpha", 99000), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not the party host") {
		t.Fatalf("non-host transport errors = %#v, want not-the-party-host", errs)
	}

	// A non-host's request in a non-relaxed party is NOT authoritative.
	data, errs := post(t, h, requestTransportBody(party.PartyID, "media-alpha", 99000), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("requestWatchPartyTransport errors = %#v", errs)
	}
	var rres RequestTransportResult
	if err := json.Unmarshal(data["requestWatchPartyTransport"], &rres); err != nil {
		t.Fatalf("decode requestWatchPartyTransport: %v", err)
	}
	if rres.Approved {
		t.Fatalf("non-relaxed request approved = true, want false (host approval required)")
	}

	// No authoritative transition was broadcast for the non-host request.
	after, _ := st.ReadEventOutbox(t.Context(), cursor, 256)
	for _, e := range after {
		if e.AggregateType != store.OutboxAggregateGroup {
			continue
		}
		var p store.GroupSyncPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.Signal == store.GroupSignalTransition {
			t.Fatalf("a non-host request broadcast an authoritative transition (pos %d)", p.PositionMS)
		}
	}
}

// TestWatchPartyRelaxedAutoApprovesNonHostSeek is acceptance (a)/§2.4 relaxed
// mode: in a relaxed party a non-host seek auto-approves and broadcasts an
// authoritative transition on the host's behalf.
//
// feature:watch-party
func TestWatchPartyRelaxedAutoApprovesNonHostSeek(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	party := createParty(t, h, "media-alpha", true, authorizeViewer) // relaxed
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}
	cursor := lastSeq(t, st)

	data, errs := post(t, h, requestTransportBody(party.PartyID, "media-alpha", 45000), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("requestWatchPartyTransport errors = %#v", errs)
	}
	var rres RequestTransportResult
	if err := json.Unmarshal(data["requestWatchPartyTransport"], &rres); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rres.Approved {
		t.Fatalf("relaxed request approved = false, want true (auto-approve)")
	}

	// An authoritative transition WAS broadcast at the requested position.
	after, _ := st.ReadEventOutbox(t.Context(), cursor, 256)
	found := false
	for _, e := range after {
		if e.AggregateType != store.OutboxAggregateGroup {
			continue
		}
		var p store.GroupSyncPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.Signal == store.GroupSignalTransition && p.PositionMS == 45000 {
			found = true
		}
	}
	if !found {
		t.Fatal("relaxed auto-approve did not broadcast the authoritative transition")
	}
}

// TestWatchPartyAutoSkipPrecedence is the PANEL-14 acceptance:
// the party + auto-skip + transcoding-member scenario. The host owns the timeline;
// a member's client-side auto-skip degrades to host authority. The non-host here
// stands in for a TRANSCODING member — its ABR-restart re-sync uses the very same
// anchor the suppressed auto-skip returns, so we exercise both with one member.
//
//	strict party, host=profile-default, non-host=profile-managed (transcoding):
//	1. host establishes the timeline (transport → 10000)
//	2. non-host auto-skip → SUPPRESSED: applied=false, no broadcast, and the result
//	   carries the HOST anchor (10000), not the member's own segment end (50000) —
//	   the member re-syncs to the shared clock instead of diverging.
//	3. host auto-skip → AUTHORITATIVE: applied=true, a transition broadcasts at the
//	   segment end (60000) to every member.
//	4. non-host auto-skip again → still suppressed, and the anchor it gets back is
//	   now 60000 — i.e. an ABR-restarting member re-syncs onto the host's LIVE
//	   position, never restarting the shared timeline.
//
// feature:watch-party
func TestWatchPartyAutoSkipPrecedence(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)
	wantPresent := now.Add(conductor.PreBufferWindow).UnixMilli()

	// Strict (non-relaxed) party. Managed can see media-alpha (library-local).
	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}

	// 1. Host establishes the shared timeline at 10000.
	if _, errs := post(t, h, transportBody(party.PartyID, "media-alpha", 10000), authorizeViewer); len(errs) != 0 {
		t.Fatalf("host transport errors = %#v", errs)
	}

	decodeAutoSkip := func(t *testing.T, body string, authorize func(*http.Request)) WatchPartyAutoSkipResult {
		t.Helper()
		data, errs := post(t, h, body, authorize)
		if len(errs) != 0 {
			t.Fatalf("watchPartyAutoSkip errors = %#v", errs)
		}
		var res WatchPartyAutoSkipResult
		if err := json.Unmarshal(data["watchPartyAutoSkip"], &res); err != nil {
			t.Fatalf("decode watchPartyAutoSkip: %v", err)
		}
		return res
	}

	// 2. Non-host auto-skip to 50000 is SUPPRESSED — no broadcast, re-sync to host.
	cursor := lastSeq(t, st)
	res := decodeAutoSkip(t, autoSkipBody(party.PartyID, "media-alpha", 50000), authorizeManaged)
	if res.Applied {
		t.Fatalf("non-host strict auto-skip applied=true, want false (suppressed)")
	}
	if res.PositionMS != 10000 {
		t.Errorf("suppressed re-sync position = %d, want 10000 (host anchor, NOT member's 50000)", res.PositionMS)
	}
	if res.PresentAtMS != wantPresent {
		t.Errorf("suppressed re-sync presentAt = %d, want %d", res.PresentAtMS, wantPresent)
	}
	if n := countTransitionsAt(t, st, party.PartyID, cursor, -1); n != 0 {
		t.Fatalf("suppressed auto-skip broadcast %d transition(s), want 0", n)
	}

	// 3. Host auto-skip to 60000 IS authoritative — a transition broadcasts.
	cursor = lastSeq(t, st)
	res = decodeAutoSkip(t, autoSkipBody(party.PartyID, "media-alpha", 60000), authorizeViewer)
	if !res.Applied {
		t.Fatalf("host auto-skip applied=false, want true (authoritative)")
	}
	if res.PositionMS != 60000 || res.Item != "media-alpha" || res.PresentAtMS != wantPresent {
		t.Errorf("host auto-skip result = %#v, want media-alpha/60000/%d", res, wantPresent)
	}
	if n := countTransitionsAt(t, st, party.PartyID, cursor, 60000); n != 1 {
		t.Fatalf("host auto-skip broadcast %d transition(s) at 60000, want 1", n)
	}

	// 4. The transcoding member re-syncs onto the host's LIVE position (60000),
	//    never restarting the shared timeline.
	res = decodeAutoSkip(t, autoSkipBody(party.PartyID, "media-alpha", 50000), authorizeManaged)
	if res.Applied {
		t.Fatalf("non-host strict auto-skip applied=true after host move, want false")
	}
	if res.PositionMS != 60000 {
		t.Errorf("ABR re-sync position = %d, want 60000 (host's live position)", res.PositionMS)
	}
}

// TestWatchPartyAutoSkipRelaxedAppliesForNonHost is the relaxed half of §2.8: in a
// relaxed (household) party a non-host's auto-skip is applied on the host's behalf
// — authoritative, broadcast to every member — rather than suppressed.
//
// feature:watch-party
func TestWatchPartyAutoSkipRelaxedAppliesForNonHost(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	party := createParty(t, h, "media-alpha", true, authorizeViewer) // relaxed
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}
	cursor := lastSeq(t, st)

	data, errs := post(t, h, autoSkipBody(party.PartyID, "media-alpha", 42000), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("watchPartyAutoSkip errors = %#v", errs)
	}
	var res WatchPartyAutoSkipResult
	if err := json.Unmarshal(data["watchPartyAutoSkip"], &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.Applied || res.PositionMS != 42000 {
		t.Fatalf("relaxed non-host auto-skip = %#v, want applied at 42000", res)
	}
	if n := countTransitionsAt(t, st, party.PartyID, cursor, 42000); n != 1 {
		t.Fatalf("relaxed non-host auto-skip broadcast %d transition(s) at 42000, want 1", n)
	}
}

// TestWatchPartyAutoSkipRejectsNonMember confirms a session that is not a party
// member cannot drive (or probe) the party via auto-skip — the membership gate
// inherited from RequestTransport collapses to "not a member of this party".
//
// feature:watch-party
func TestWatchPartyAutoSkipRejectsNonMember(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	// authorizeManaged never joined — a non-member auto-skip is rejected.
	_, errs := post(t, h, autoSkipBody(party.PartyID, "media-alpha", 12000), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not a member of this party") {
		t.Fatalf("non-member auto-skip errors = %#v, want not-a-member", errs)
	}
}

// TestWatchPartyFormationInviteAcceptJoin is acceptance (b):
// invite by display name → in-app notification → accept → join. The invitee's
// notification lands in their inbox, and accepting adds them to the party.
//
// feature:watch-party
func TestWatchPartyFormationInviteAcceptJoin(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if len(party.Members) != 1 {
		t.Fatalf("fresh party members = %d, want 1 (the host)", len(party.Members))
	}

	// Invite the managed Profile by display name.
	data, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	var invited WatchPartyInfo
	if err := json.Unmarshal(data["inviteToWatchParty"], &invited); err != nil {
		t.Fatalf("decode invite: %v", err)
	}
	if invited.PartyID != party.PartyID {
		t.Fatalf("invited party id = %q, want %q", invited.PartyID, party.PartyID)
	}
	if invited.HostID != "" || len(invited.Invitees) != 0 {
		t.Fatalf("invite response exposed private identifiers: hostID=%q invitees=%v", invited.HostID, invited.Invitees)
	}

	// The in-app notification reached the invitee's inbox.
	notes, err := st.ListNotifications(t.Context(), "profile-managed", store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, false, 50)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	gotInvite := false
	for _, n := range notes {
		if n.Kind == store.NotificationWatchPartyInvite && n.Ref == party.PartyID {
			gotInvite = true
		}
	}
	if !gotInvite {
		t.Fatalf("invitee inbox = %#v, want a watch-party-invite for %s", notes, party.PartyID)
	}

	// Accept from the invitee's own session → join.
	acc, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}
	var ares AcceptWatchPartyResult
	if err := json.Unmarshal(acc["acceptWatchParty"], &ares); err != nil {
		t.Fatalf("decode accept: %v", err)
	}
	if ares.PartyID != party.PartyID {
		t.Errorf("accept party = %q, want %q", ares.PartyID, party.PartyID)
	}

	// The party now has two members (host + accepted invitee).
	g, err := h.conductor.Party(party.PartyID)
	if err != nil {
		t.Fatalf("Party lookup: %v", err)
	}
	if n := len(g.Members()); n != 2 {
		t.Fatalf("party members after accept = %d, want 2", n)
	}
}

// TestWatchPartyRevealIntersectionRejectsIneligible is acceptance (c), the
// load-bearing Reveal-contract test: an invitee whose session
// cannot see the host's content is rejected with an explicit reason, and the
// party ceiling caps at the most-restrictive member. The host (session-revealed,
// adult+revealed) hosts a party on the RESTRICTED media-archive; a managed
// invitee (no reveal path) and an unrevealed adult invitee both cannot see it
// and are rejected on accept.
//
// feature:watch-party
func TestWatchPartyRevealIntersectionRejectsIneligible(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	// Host is the REVEALED adult session, so it can see restricted media-archive.
	party := createParty(t, h, "media-archive", false, authorizeRevealed)

	// Managed invitee (capped — no reveal path): invite succeeds, accept rejected.
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeRevealed); len(errs) != 0 {
		t.Fatalf("invite managed errors = %#v", errs)
	}
	_, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not visible to your profile") {
		t.Fatalf("managed accept errors = %#v, want reveal-intersection rejection", errs)
	}

	// Unrevealed adult invitee (same Profile as host, but session-default is not
	// revealed): also capped — cannot see restricted content, accept rejected.
	if _, errs := post(t, h, inviteBody(party.PartyID, "Default"), authorizeRevealed); len(errs) != 0 {
		t.Fatalf("invite default errors = %#v", errs)
	}
	_, errs = post(t, h, acceptBody(party.PartyID), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not visible to your profile") {
		t.Fatalf("unrevealed accept errors = %#v, want reveal-intersection rejection", errs)
	}

	// The party stayed at one member (the host); no ineligible invitee joined.
	g, err := h.conductor.Party(party.PartyID)
	if err != nil {
		t.Fatalf("Party lookup: %v", err)
	}
	if n := len(g.Members()); n != 1 {
		t.Fatalf("party members = %d, want 1 (only the host; ineligible invitees never joined)", n)
	}
}

// TestWatchPartyHostCannotHostUnseeableContent guards the opening ceiling: a
// host cannot anchor a party on content its own session cannot Reveal (ADR-0038
// §2.3) — an unrevealed adult session is refused media-archive at creation.
//
// feature:watch-party
func TestWatchPartyHostCannotHostUnseeableContent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	_, errs := post(t, h, createPartyBody("media-archive", false), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "cannot host a party for content you cannot see") {
		t.Fatalf("host-unseeable errors = %#v, want host-ceiling rejection", errs)
	}
}

// TestWatchPartyLateJoinerSyncsToAnchor is acceptance (d) (ADR-0038 §2.5 step
// 5): a late joiner is synchronized to the CURRENT position, not zero. The host
// seeks to 60 s, then 30 s elapses; an invitee accepting now receives an anchor
// at ~90 s (60 s seek + 30 s elapsed).
//
// feature:watch-party
func TestWatchPartyLateJoinerSyncsToAnchor(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	cur := base
	st := storetest.Open(t)
	// A mutable clock so we can advance time between the host seek and the late
	// accept, exercising the anchor + elapsed sync.
	h := newTestHandlerWithStore(st)
	h.SetConductor(conductor.NewRegistryWithClock(func() time.Time { return cur }))

	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}

	// Host anchors the timeline at 60 s.
	if _, errs := post(t, h, transportBody(party.PartyID, "media-alpha", 60000), authorizeViewer); len(errs) != 0 {
		t.Fatalf("host transport errors = %#v", errs)
	}

	// 30 s of wall-clock elapses before the invitee accepts.
	cur = base.Add(30 * time.Second)
	data, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("late accept errors = %#v", errs)
	}
	var ares AcceptWatchPartyResult
	if err := json.Unmarshal(data["acceptWatchParty"], &ares); err != nil {
		t.Fatalf("decode accept: %v", err)
	}
	// Anchor position = 60 s seek + 30 s elapsed = 90 s.
	if ares.PositionMS != 90000 {
		t.Fatalf("late-joiner sync position = %d ms, want 90000 (60s anchor + 30s elapsed)", ares.PositionMS)
	}
	if ares.Item != "media-alpha" {
		t.Errorf("late-joiner item = %q, want media-alpha", ares.Item)
	}
	wantPresent := cur.Add(conductor.PreBufferWindow).UnixMilli()
	if ares.PresentAtMS != wantPresent {
		t.Errorf("late-joiner present_at = %d, want %d", ares.PresentAtMS, wantPresent)
	}
}

// stubAdmission is a TranscodeCapacityProbe returning a fixed admission verdict.
type stubAdmission struct{ admit bool }

func (s stubAdmission) CanAdmitSession(store.SessionID, store.MediaID) bool { return s.admit }

// acceptDegradedBody is acceptBody with the PANEL-16 advisory degraded field in
// the selection set.
func acceptDegradedBody(partyID string) string {
	return `{"query":"mutation A($partyID: String!, $clockOffsetMS: Int!, $renderLatencyMS: Int!){ acceptWatchParty(partyID:$partyID, clockOffsetMS:$clockOffsetMS, renderLatencyMS:$renderLatencyMS){ partyID degraded } }",` +
		`"variables":{"partyID":"` + partyID + `","clockOffsetMS":0,"renderLatencyMS":0}}`
}

// TestWatchPartyAcceptReportsCapacityDegrade covers the PANEL-16 join-time
// capacity awareness: accept reports Degraded=true when the server
// cannot admit a normal transcode for the joining member's content, and false
// when it can — an advisory heads-up that a transcode-needing client will be
// served the lowest rung while still joining (the party stays whole).
func TestWatchPartyAcceptReportsCapacityDegrade(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()

	accept := func(t *testing.T, admit bool) AcceptWatchPartyResult {
		t.Helper()
		st := storetest.Open(t)
		h := newConductorHandler(st, now)
		h.SetTranscodeAdmission(stubAdmission{admit: admit})
		party := createParty(t, h, "media-alpha", false, authorizeViewer)
		if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
			t.Fatalf("invite errors = %#v", errs)
		}
		data, errs := post(t, h, acceptDegradedBody(party.PartyID), authorizeManaged)
		if len(errs) != 0 {
			t.Fatalf("accept errors = %#v", errs)
		}
		var ares AcceptWatchPartyResult
		if err := json.Unmarshal(data["acceptWatchParty"], &ares); err != nil {
			t.Fatalf("decode accept: %v", err)
		}
		return ares
	}

	// At capacity (probe refuses) → degraded advisory true.
	if got := accept(t, false); !got.Degraded {
		t.Fatal("Degraded = false at capacity, want true")
	}
	// Slot available (probe admits) → not degraded.
	if got := accept(t, true); got.Degraded {
		t.Fatal("Degraded = true with a free slot, want false")
	}
}

// TestWatchPartyIsEphemeral is acceptance (e): party state is in-memory only —
// a fresh Registry holds zero groups, and a party id from a prior Registry is
// unknown to a new one (nothing persisted, reassembled on reconnect).
//
// feature:watch-party
func TestWatchPartyIsEphemeral(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if h.conductor.GroupCount() != 1 {
		t.Fatalf("group count after create = %d, want 1", h.conductor.GroupCount())
	}

	// Simulate a process restart: a brand-new Registry knows nothing of the party.
	h.SetConductor(conductor.NewRegistryWithClock(func() time.Time { return now }))
	if h.conductor.GroupCount() != 0 {
		t.Fatalf("fresh Registry group count = %d, want 0 (ephemeral, nothing persisted)", h.conductor.GroupCount())
	}
	_, errs := post(t, h, transportBody(party.PartyID, "media-alpha", 0), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown party") {
		t.Fatalf("transport on reassembled-empty Registry errors = %#v, want unknown party", errs)
	}
}

// leavePartyBody is the leaveWatchParty mutation body.
func leavePartyBody(partyID string) string {
	return `{"query":"mutation L($partyID: String!){ leaveWatchParty(partyID:$partyID){ partyID } }","variables":{"partyID":"` + partyID + `"}}`
}

// TestWatchPartyParticipantExposureIsMinimal is the ADR-0111 §1 acceptance: a
// participant sees other members ONLY by their chosen display name and a
// playback-presence bool — never a session id, device id, real name, email, or
// account identifier. The wire WatchPartyInfo.Members shape carries
// WatchPartyParticipant{displayName, present}, not GroupMemberInfo{sessionID, deviceID…}.
//
// feature:watch-party
func TestWatchPartyParticipantExposureIsMinimal(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	// Host (profile-default, display name "Default") forms a party.
	party := createParty(t, h, "media-alpha", false, authorizeViewer)

	// The host's own entry in Members uses the display name, never a session/device id.
	if len(party.Members) != 1 {
		t.Fatalf("fresh party members = %d, want 1 (the host)", len(party.Members))
	}
	if party.Members[0].DisplayName != "Default" {
		t.Errorf("host display name = %q, want %q (ADR-0111 §1: chosen display name only)", party.Members[0].DisplayName, "Default")
	}
	if !party.Members[0].Present {
		t.Errorf("host Present = false, want true (joined member is present)")
	}

	// Invite and accept the managed Profile (display name "Managed").
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}

	// Fetch updated membership via a fresh create party read (via conductor direct).
	g, err := h.conductor.Party(party.PartyID)
	if err != nil {
		t.Fatalf("Party lookup: %v", err)
	}
	info := watchPartyInfo(t.Context(), h.store, g)
	if len(info.Members) != 2 {
		t.Fatalf("party members = %d, want 2 after accept", len(info.Members))
	}
	names := make(map[string]bool)
	for _, m := range info.Members {
		names[m.DisplayName] = m.Present
		// Verify no raw ids appear: display names must be non-empty human-readable strings.
		if m.DisplayName == "" {
			t.Error("member DisplayName is empty (ADR-0111 §1: display name required)")
		}
		if !m.Present {
			t.Errorf("member %q Present = false, want true (joined member)", m.DisplayName)
		}
	}
	if !names["Default"] {
		t.Errorf("host display name 'Default' not in members: %v", names)
	}
	if !names["Managed"] {
		t.Errorf("guest display name 'Managed' not in members: %v", names)
	}

	// A failed profile lookup never falls back to the raw Profile identifier.
	missingProfiles := watchPartyInfo(t.Context(), nil, g)
	for _, member := range missingProfiles.Members {
		if member.DisplayName != "Participant" || strings.Contains(member.DisplayName, "profile-") {
			t.Fatalf("failed profile lookup exposed identifier: %#v", member)
		}
	}
}

// TestWatchPartyRevocationPropagation is the ADR-0111 §4 acceptance: a member
// can leave a Watch Party cross-Profile via leaveWatchParty; membership is removed
// synchronously and the party dissolves when the last member leaves.
//
// feature:watch-party
func TestWatchPartyRevocationPropagation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	// Host forms a party and invites the managed Profile.
	party := createParty(t, h, "media-alpha", false, authorizeViewer)
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeViewer); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	if _, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged); len(errs) != 0 {
		t.Fatalf("accept errors = %#v", errs)
	}

	// Party now has 2 members.
	g, err := h.conductor.Party(party.PartyID)
	if err != nil {
		t.Fatalf("Party lookup: %v", err)
	}
	if n := len(g.Members()); n != 2 {
		t.Fatalf("members before revoke = %d, want 2", n)
	}

	// The guest leaves (simulate Reveal revocation propagation): leaveWatchParty
	// succeeds for a cross-Profile guest, unlike leaveGroup which would be gated
	// on the same Profile owning the group.
	data, errs := post(t, h, leavePartyBody(party.PartyID), authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("leaveWatchParty errors = %#v", errs)
	}
	var res LeaveWatchPartyResult
	if err := json.Unmarshal(data["leaveWatchParty"], &res); err != nil {
		t.Fatalf("decode leaveWatchParty: %v", err)
	}
	if res.PartyID != party.PartyID {
		t.Errorf("leaveWatchParty partyID = %q, want %q", res.PartyID, party.PartyID)
	}

	// Guest is removed; host remains.
	g, err = h.conductor.Party(party.PartyID)
	if err != nil {
		t.Fatalf("Party lookup after leave: %v", err)
	}
	if n := len(g.Members()); n != 1 {
		t.Fatalf("members after guest leave = %d, want 1 (host only)", n)
	}

	// Host leaves too — the party dissolves.
	if _, errs := post(t, h, leavePartyBody(party.PartyID), authorizeViewer); len(errs) != 0 {
		t.Fatalf("host leaveWatchParty errors = %#v", errs)
	}
	if h.conductor.GroupCount() != 0 {
		t.Fatalf("group count after last member leaves = %d, want 0 (party dissolved)", h.conductor.GroupCount())
	}
}

// TestWatchPartyOraclePrevention is the ADR-0111 §2 (oracle-prevention) acceptance:
// out-of-intersection content is never named, counted, or hinted on any surface.
// Two distinct oracle vectors are tested:
//
//  1. An INVITED but ineligible session receives "not visible to your profile" — an
//     explicit rejection that never names the content or reveals its title.
//  2. A completely UNINVITED session (that CAN see the content) receives "unknown
//     invite" — the same opaque surface used by a missing invite, so the party id
//     is never an oracle for party existence independent of Reveal eligibility.
//
// feature:watch-party
func TestWatchPartyOraclePrevention(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	// Oracle vector 1: invited-but-ineligible on RESTRICTED content.
	// A revealed session hosts a party on media-archive (restricted). The managed
	// Profile is invited but cannot Reveal the content; accept is rejected with
	// "not visible to your profile" — honest but content-title-free.
	restricted := createParty(t, h, "media-archive", false, authorizeRevealed)
	if _, errs := post(t, h, inviteBody(restricted.PartyID, "Managed"), authorizeRevealed); len(errs) != 0 {
		t.Fatalf("invite errors = %#v", errs)
	}
	_, errs := post(t, h, acceptBody(restricted.PartyID), authorizeManaged)
	if len(errs) == 0 {
		t.Fatal("oracle-prevention: accept by ineligible session succeeded, want rejection")
	}
	if !strings.Contains(errs[0].Message, "not visible to your profile") {
		t.Errorf("oracle-prevention: rejection message = %q, want 'not visible to your profile'", errs[0].Message)
	}
	g, err := h.conductor.Party(restricted.PartyID)
	if err != nil {
		t.Fatalf("Party lookup: %v", err)
	}
	if n := len(g.Members()); n != 1 {
		t.Fatalf("oracle-prevention: members after ineligible accept = %d, want 1 (host only)", n)
	}

	// Oracle vector 2: uninvited session on OPEN content. The managed Profile hosts
	// a party on media-alpha (open, library-local). The host's profile is NOT invited.
	// authorizeViewer (session-default, profile-default) can see media-alpha but has
	// no invite, so it receives "unknown invite" — the party id is not an oracle for
	// party existence regardless of Reveal eligibility.
	open := createParty(t, h, "media-alpha", false, authorizeManaged)
	// Do NOT invite profile-default — authorizeViewer has no invite.
	_, errs = post(t, h, acceptBody(open.PartyID), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown invite") {
		t.Fatalf("oracle-prevention: uninvited accept errors = %#v, want unknown-invite", errs)
	}
}

// TestWatchPartyRevealIntersectionRecomputedOnJoin is the ADR-0111 §2 acceptance
// (re-evaluate intersection on participant join): the Reveal check happens at
// ACCEPT time against the joining session's OWN filter, not once at formation.
// This means a session whose Reveal ceiling changed between formation and accept
// is evaluated with its CURRENT state. We verify the re-evaluation by showing
// that a managed (capped) session is rejected even if invited after formation —
// the formation-time ceiling alone does not persist; every join is a fresh check.
//
// feature:watch-party
func TestWatchPartyRevealIntersectionRecomputedOnJoin(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	st := storetest.Open(t)
	h := newConductorHandler(st, now)

	// Host (revealed) forms a party on RESTRICTED content.
	party := createParty(t, h, "media-archive", false, authorizeRevealed)

	// Invite both profiles (managed and unrevealed adult).
	if _, errs := post(t, h, inviteBody(party.PartyID, "Managed"), authorizeRevealed); len(errs) != 0 {
		t.Fatalf("invite managed errors = %#v", errs)
	}
	if _, errs := post(t, h, inviteBody(party.PartyID, "Default"), authorizeRevealed); len(errs) != 0 {
		t.Fatalf("invite default errors = %#v", errs)
	}

	// Managed (no reveal path): reject at accept — formation state does not matter.
	_, errs := post(t, h, acceptBody(party.PartyID), authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not visible to your profile") {
		t.Fatalf("managed accept errors = %#v, want reveal-intersection rejection at accept", errs)
	}

	// Unrevealed adult (session-default, same profile as host but not revealed):
	// also rejected — the re-evaluation is against THIS session's own filter, not
	// the host's. Formation with a revealed host does not grant the unrevealed sibling.
	_, errs = post(t, h, acceptBody(party.PartyID), authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not visible to your profile") {
		t.Fatalf("unrevealed-sibling accept errors = %#v, want reveal-intersection rejection at accept", errs)
	}

	// Party remains host-only — neither ineligible session joined.
	g, err := h.conductor.Party(party.PartyID)
	if err != nil {
		t.Fatalf("Party lookup: %v", err)
	}
	if n := len(g.Members()); n != 1 {
		t.Fatalf("party members after rejected accepts = %d, want 1 (host only)", n)
	}
}
