package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type fakePartyTerminator struct {
	detached []store.MediaID
	sessions []store.SessionID
}

func (f *fakePartyTerminator) TerminateSession(sessionID store.SessionID) int {
	f.sessions = append(f.sessions, sessionID)
	return 1
}

func (f *fakePartyTerminator) DetachPlayback(_ store.SessionID, mediaID store.MediaID) (int, func()) {
	f.detached = append(f.detached, mediaID)
	return 1, func() {}
}

func TestPartyStageBindingTwoRegistrationsAndNonLeakingRefusal(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)

	var session PartySessionInfo
	execGraph(t, h, authorizeRevealed, `mutation { partyStart(libraryIDs: ["library-local", "library-archive"]) { id } }`, nil, "partyStart", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdd(mediaID: "media-alpha", participantName: "Avery") { id } }`, nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id } }`, nil, "partyAdvance", &session)

	// One Session registers two screens, each keeping its own flags.
	var scr1, scr2 PartyScreenInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Main Popout", kind: "popout", carrier: "local") { id lead ticker } }`, session.ID), nil, "partyScreenRegister", &scr1)
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Booth", kind: "joined", carrier: "local") { id lead ticker } }`, session.ID), nil, "partyScreenRegister", &scr2)
	if scr1.Ticker || !scr2.Ticker {
		t.Fatalf("flags = (%v, %v), want (false, true)", scr1.Ticker, scr2.Ticker)
	}

	var st1, st2 PartyStageInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`{ partyStage(sessionID: %q, screenID: %q) { sessionID ticker lead current { id mediaID title } } }`, session.ID, scr1.ID), nil, "partyStage", &st1)
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`{ partyStage(sessionID: %q, screenID: %q) { sessionID ticker lead current { id mediaID title } } }`, session.ID, scr2.ID), nil, "partyStage", &st2)
	if st1.Ticker || !st1.Lead || !st2.Ticker || st2.Lead {
		t.Fatalf("stage flags mismatch: st1=%+v st2=%+v", st1, st2)
	}

	// Another Session (even on the SAME Profile!) cannot read scr1 or scr2 and gets the exact same refusal as nonexistent.
	marshalQuery := func(q string) string {
		b, _ := json.Marshal(map[string]any{"query": q})
		return string(b)
	}
	_, errOther := post(t, h, marshalQuery(fmt.Sprintf(`query { partyStage(sessionID: %q, screenID: %q) { sessionID } }`, session.ID, scr1.ID)), authorizeViewer)
	_, errMissing := post(t, h, marshalQuery(fmt.Sprintf(`query { partyStage(sessionID: %q, screenID: %q) { sessionID } }`, session.ID, "screen-does-not-exist")), authorizeViewer)
	if len(errOther) != 1 || len(errMissing) != 1 ||
		errOther[0].Message != errMissing[0].Message ||
		errOther[0].Extensions.Code != ErrorCodeNotFound ||
		errOther[0].Extensions.Code != errMissing[0].Extensions.Code {
		t.Fatalf("other-session = %#v, missing = %#v; want identical NOT_FOUND", errOther, errMissing)
	}
}

func TestPartyScreenLeadRefusesWhenAccessScheduleCloses(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	today := time.Now().Local()
	now := time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, time.Local)
	h.auth = auth.NewServiceWithClock(st, func() time.Time { return now })
	if err := st.SetProfileAccessWindows(t.Context(), "profile-managed", []store.ProfileAccessWindow{
		{Weekday: int(now.Weekday()), StartMinute: 720, EndMinute: 721},
	}); err != nil {
		t.Fatal(err)
	}

	var session PartySessionInfo
	execGraph(t, h, authorizeRevealed, `mutation { partyStart(libraryIDs: ["library-local"]) { id } }`, nil, "partyStart", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdd(mediaID: "media-alpha", participantName: "Avery") { id } }`, nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id } }`, nil, "partyAdvance", &session)
	var popout, joined PartyScreenInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Host", kind: "popout", carrier: "local") { id } }`, session.ID), nil, "partyScreenRegister", &popout)
	execGraph(t, h, authorizeManaged, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Joined", kind: "joined", carrier: "local") { id } }`, session.ID), nil, "partyScreenRegister", &joined)
	const selectLead = `mutation { partyScreenLead(screenID: %q) { id screens { id lead } } }`
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(selectLead, joined.ID), nil, "partyScreenLead", &session)
	if len(session.Screens) != 2 {
		t.Fatalf("screens while schedule is open = %+v, want both registrations", session.Screens)
	}
	for _, screen := range session.Screens {
		if screen.Lead != (screen.ID == joined.ID) {
			t.Fatalf("lead while schedule is open = %+v", session.Screens)
		}
	}

	now = now.Add(time.Minute)
	requestBody, err := json.Marshal(map[string]string{"query": fmt.Sprintf(selectLead, joined.ID)})
	if err != nil {
		t.Fatal(err)
	}
	_, errs := post(t, h, string(requestBody), authorizeRevealed)
	if len(errs) != 1 || errs[0].Extensions.Code != ErrorCodeForbidden || errs[0].Message != "screen cannot play the current entry" {
		t.Fatalf("selecting lead after the schedule closes = %#v, want ineligible refusal", errs)
	}
	execGraph(t, h, authorizeRevealed, `{ partySession { id screens { id lead } } }`, nil, "partySession", &session)
	if len(session.Screens) != 2 {
		t.Fatalf("screens after schedule closes = %+v, want both registrations", session.Screens)
	}
	for _, screen := range session.Screens {
		if screen.Lead != (screen.ID == popout.ID) {
			t.Fatalf("lead after schedule closes = %+v, want pop-out", session.Screens)
		}
	}
}

func TestPartyStageEligibilityPerScreenAndRelockSwitchProfile(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	term := &fakePartyTerminator{}
	h.SetTranscodeTerminator(term)

	// Configure a certification-gated library so PlaybackFilter vs LibraryFilter diverge for profile-managed.
	certLib, err := st.CreateLibraryWithRoots(ctx, "Cert Karaoke", false, "demo", "profile-default", []string{t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	lockedMedia, err := st.CreateManualMedia(ctx, store.ManualMedia{
		LibraryID: certLib.ID, Title: "Locked Ballad",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, lockedMedia, []store.MediaField{
		{Name: "certification", Type: store.EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	h.auth.SetCertificationScales(map[string][]string{"demo": {"low", "high"}})
	if _, err := st.SetProfileLibraries(ctx, "profile-default", []store.LibraryID{"library-local", "library-archive", certLib.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileLibraries(ctx, "profile-managed", []store.LibraryID{"library-local", certLib.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileCertificationCeiling(ctx, "profile-managed", store.ProfileCertificationCeiling{
		PluginID: "demo", Ceiling: "low", FilterVisibility: "locked",
	}); err != nil {
		t.Fatal(err)
	}

	// Host (revealed) starts party with local, archive, and certLib.
	var session PartySessionInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyStart(libraryIDs: ["library-local", "library-archive", %q]) { id } }`, certLib.ID), nil, "partyStart", &session)
	// Current = media-archive (restricted); UpNext[0] = lockedMedia (visible in managed LibraryFilter, denied in PlaybackFilter); UpNext[1] = media-archive.
	execGraph(t, h, authorizeRevealed, `mutation { partyAdd(mediaID: "media-archive", participantName: "Rowan") { id } }`, nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyAdd(mediaID: %q, participantName: "Casey") { id } }`, lockedMedia), nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdd(mediaID: "media-archive", participantName: "Blake") { id } }`, nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id } }`, nil, "partyAdvance", &session)

	// Host screen (revealed) + ordinary joined screen on session-default (unrevealed) + managed screen (session-managed).
	var hostScr, unrevScr, managedScr PartyScreenInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Host", kind: "popout", carrier: "local") { id } }`, session.ID), nil, "partyScreenRegister", &hostScr)
	execGraph(t, h, authorizeViewer, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Viewer", kind: "joined", carrier: "local") { id } }`, session.ID), nil, "partyScreenRegister", &unrevScr)
	execGraph(t, h, authorizeManaged, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Managed", kind: "joined", carrier: "local") { id } }`, session.ID), nil, "partyScreenRegister", &managedScr)

	const stageQ = `{ partyStage(sessionID: %q, screenID: %q) { current { id mediaID title artworkURL participantName ineligible label } playback { entryID } upNext { id mediaID title artworkURL participantName ineligible label } } }`
	var hostStage, unrevStage, managedStage PartyStageInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(stageQ, session.ID, hostScr.ID), nil, "partyStage", &hostStage)
	execGraph(t, h, authorizeViewer, fmt.Sprintf(stageQ, session.ID, unrevScr.ID), nil, "partyStage", &unrevStage)
	execGraph(t, h, authorizeManaged, fmt.Sprintf(stageQ, session.ID, managedScr.ID), nil, "partyStage", &managedStage)

	// Eligible host screen sees full metadata.
	if hostStage.Current == nil || hostStage.Current.MediaID != "media-archive" || hostStage.Current.Title == "" || hostStage.Current.Ineligible {
		t.Fatalf("eligible hostStage.Current = %+v", hostStage.Current)
	}
	if hostStage.Playback == nil {
		t.Fatal("eligible hostStage.Playback is nil")
	}

	// Unrevealed screen sees NO mediaID/title/artwork on restricted current or restricted upNext, rendering "Up next: Rowan".
	if unrevStage.Current == nil || !unrevStage.Current.Ineligible || unrevStage.Current.MediaID != "" || unrevStage.Current.Title != "" || unrevStage.Current.ArtworkURL != "" ||
		unrevStage.Current.Label != "Up next: Rowan" || unrevStage.Playback != nil {
		t.Fatalf("unrevealed current = %+v, playback=%+v", unrevStage.Current, unrevStage.Playback)
	}
	if len(unrevStage.UpNext) != 2 || unrevStage.UpNext[1].MediaID != "" || unrevStage.UpNext[1].Title != "" || unrevStage.UpNext[1].Label != "Blake" {
		t.Fatalf("unrevealed upNext = %+v", unrevStage.UpNext)
	}

	// Managed screen sees lockedMedia title in upNext (LibraryFilter admits locked), but restricted media-archive is redacted.
	if len(managedStage.UpNext) != 2 || managedStage.UpNext[0].MediaID != lockedMedia || managedStage.UpNext[0].Title != "Locked Ballad" || managedStage.UpNext[0].Ineligible {
		t.Fatalf("managed upNext[0] (LibraryFilter) = %+v", managedStage.UpNext[0])
	}
	if managedStage.UpNext[1].MediaID != "" || managedStage.UpNext[1].Title != "" || managedStage.UpNext[1].Label != "Blake" {
		t.Fatalf("managed upNext[1] = %+v", managedStage.UpNext[1])
	}

	// Advance to lockedMedia: managed screen uses PlaybackFilter for current -> ineligible ("Up next: Casey")!
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id } }`, nil, "partyAdvance", &session)
	execGraph(t, h, authorizeManaged, fmt.Sprintf(stageQ, session.ID, managedScr.ID), nil, "partyStage", &managedStage)
	if managedStage.Current == nil || !managedStage.Current.Ineligible || managedStage.Current.MediaID != "" || managedStage.Current.Title != "" || managedStage.Current.Label != "Up next: Casey" || managedStage.Playback != nil {
		t.Fatalf("managed current for locked media = %+v, playback=%+v", managedStage.Current, managedStage.Playback)
	}

	// Create a second revealed session on an adult Profile with a joined screen holding a restricted playback grant, then relock!
	adult2, err := st.CreateProfile(ctx, "Jordan", store.ProfileAdult, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-jordan", Name: "Jordan iPad", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("j-secret")},
		store.Session{ID: "session-jordan", DeviceID: "device-jordan", ProfileID: adult2.ID, Revealed: true, ExpiresAt: time.Now().UTC().Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	authJordan := func(req *http.Request) {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Beamfall-Session", "session-jordan")
		req.Header.Set("X-Beamfall-Device", "device-jordan")
		req.Header.Set("X-Beamfall-Device-Secret", "j-secret")
	}
	var jordanScr PartyScreenInfo
	execGraph(t, h, authJordan, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Jordan Tab", kind: "joined", carrier: "local") { id } }`, session.ID), nil, "partyScreenRegister", &jordanScr)
	if err := st.RecordStreamToken(ctx, "session-jordan", adult2.ID, "device-jordan", "media-hls", "media-archive", true, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Advance back to a restricted item (media-archive).
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id } }`, nil, "partyAdvance", &session)
	var jordanStage PartyStageInfo
	execGraph(t, h, authJordan, fmt.Sprintf(stageQ, session.ID, jordanScr.ID), nil, "partyStage", &jordanStage)
	if jordanStage.Current == nil || jordanStage.Current.MediaID != "media-archive" || jordanStage.Current.Ineligible {
		t.Fatalf("jordan before relock = %+v", jordanStage.Current)
	}

	// Relock Jordan's session -> detaches restricted transcode, clears restricted grant, and clears restricted stage metadata.
	_, relockErrs := post(t, h, `{"query":"mutation { relock { revealed } }"}`, authJordan)
	if len(relockErrs) != 0 {
		t.Fatalf("relock errors = %#v", relockErrs)
	}
	if len(term.detached) != 1 || term.detached[0] != "media-archive" {
		t.Fatalf("relock detached = %v, want [media-archive]", term.detached)
	}
	execGraph(t, h, authJordan, fmt.Sprintf(stageQ, session.ID, jordanScr.ID), nil, "partyStage", &jordanStage)
	if jordanStage.Current == nil || !jordanStage.Current.Ineligible || jordanStage.Current.MediaID != "" || jordanStage.Current.Title != "" || jordanStage.Current.Label != "Up next: Blake" {
		t.Fatalf("jordan after relock = %+v", jordanStage.Current)
	}

	// switchProfile on Jordan's joined screen to managed updates scope immediately and terminates session transcodes.
	_, switchErrs := post(t, h, `{"query":"mutation { switchProfile(profileID: \"profile-managed\") { id profileID } }"}`, authJordan)
	if len(switchErrs) != 0 {
		t.Fatalf("switchProfile errors = %#v", switchErrs)
	}
	if len(term.sessions) == 0 || term.sessions[len(term.sessions)-1] != "session-jordan" {
		t.Fatalf("switchProfile terminated sessions = %v", term.sessions)
	}
	execGraph(t, h, authJordan, fmt.Sprintf(stageQ, session.ID, jordanScr.ID), nil, "partyStage", &jordanStage)
	if jordanStage.Current == nil || !jordanStage.Current.Ineligible || jordanStage.Current.MediaID != "" {
		t.Fatalf("jordan after switchProfile = %+v", jordanStage.Current)
	}
}

func TestPartyPlayerDisplayJoinLeadInPartyD4AndRelease(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	clock := time.Now().UTC().Truncate(time.Millisecond)
	h := newTestHandlerWithStore(st)
	h.nowFunc = func() time.Time { return clock }

	player, displaySess := setupPlayerDisplayFixture(t, st, h, &clock)
	if _, err := st.SetProfileLibraries(ctx, player.ID, []store.LibraryID{"library-local"}); err != nil {
		t.Fatal(err)
	}
	// Display 2 is NOT controlled by profile-default (exclude it!).
	player2, err := st.CreateProfile(ctx, "Bedroom TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-bedroom-tv", Name: "Bedroom TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("bed-secret"),
			PlayerProfileID: player2.ID, PlayerDisplayName: "Bedroom TV", PlayerBindingGeneration: 1,
		},
		store.Session{
			ID: "session-bedroom-tv", DeviceID: "device-bedroom-tv", ProfileID: player2.ID,
			ExpiresAt: clock.Add(24 * time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	h.EventStreamOpened("session-bedroom-tv")
	if _, err := st.SetPlayerControllers(ctx, player2.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Host starts party with local + archive.
	var session PartySessionInfo
	execGraph(t, h, authorizeRevealed, `mutation { partyStart(libraryIDs: ["library-local", "library-archive"]) {
		id availableDisplays { handle displayName presence joined coversLibraries missingLibraryIDs }
	} }`, nil, "partyStart", &session)
	// Screens list offers only controlled displays (Karaoke room TV, NOT Bedroom TV).
	if len(session.AvailableDisplays) != 1 || session.AvailableDisplays[0].DisplayName != "Karaoke room TV" ||
		session.AvailableDisplays[0].CoversLibraries || len(session.AvailableDisplays[0].MissingLibraryIDs) != 1 ||
		session.AvailableDisplays[0].MissingLibraryIDs[0] != "library-archive" {
		t.Fatalf("availableDisplays = %+v", session.AvailableDisplays)
	}
	displayHandle := session.AvailableDisplays[0].Handle
	bedroomHandle := h.playerDisplayHandle("device-bedroom-tv")

	// Host cannot join an uncontrolled display.
	marshalQuery := func(q string) string {
		b, _ := json.Marshal(map[string]any{"query": q})
		return string(b)
	}
	_, unctrlErrs := post(t, h, marshalQuery(fmt.Sprintf(`mutation { partyScreenAddDisplay(handle: %q) { screen { id } } }`, bedroomHandle)), authorizeRevealed)
	if len(unctrlErrs) != 1 || unctrlErrs[0].Extensions.Code != ErrorCodeNotFound {
		t.Fatalf("add uncontrolled display = %#v, want NOT_FOUND", unctrlErrs)
	}

	// Register popout lead + queue restricted media-archive then unrestricted media-alpha.
	var popout PartyScreenInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Host Popout", kind: "popout", carrier: "local") { id lead } }`, session.ID), nil, "partyScreenRegister", &popout)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdd(mediaID: "media-archive", participantName: "Rowan") { id } }`, nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdd(mediaID: "media-alpha", participantName: "Avery") { id } }`, nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id } }`, nil, "partyAdvance", &session)

	// Host adds Karaoke room TV -> joins as follower, reports missing library-archive.
	var joinRes PartyScreenJoinResult
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenAddDisplay(handle: %q) {
		coversLibraries missingLibraryIDs
		screen { id kind lead ticker playerDisplay playerDisplayHandle }
		session { id screens { id lead playerDisplay } availableDisplays { handle joined } }
	} }`, displayHandle), nil, "partyScreenAddDisplay", &joinRes)
	if joinRes.CoversLibraries || len(joinRes.MissingLibraryIDs) != 1 || joinRes.MissingLibraryIDs[0] != "library-archive" ||
		joinRes.Screen.Lead || joinRes.Screen.Kind != "joined" || !joinRes.Screen.PlayerDisplay || joinRes.Screen.PlayerDisplayHandle != displayHandle {
		t.Fatalf("joinRes = %+v", joinRes)
	}

	// While media-archive (ineligible for display) is current, choosing display as lead is refused; popout stays lead.
	_, leadErrs := post(t, h, marshalQuery(fmt.Sprintf(`mutation { partyScreenLead(screenID: %q) { id } }`, joinRes.Screen.ID)), authorizeRevealed)
	if len(leadErrs) != 1 || leadErrs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("ineligible partyScreenLead = %#v, want FORBIDDEN", leadErrs)
	}

	// Advance to media-alpha (eligible for display) -> display CAN be chosen lead.
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id } }`, nil, "partyAdvance", &session)
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenLead(screenID: %q) { id screens { id lead } } }`, joinRes.Screen.ID), nil, "partyScreenLead", &session)
	var dispLead bool
	for _, scr := range session.Screens {
		if scr.ID == joinRes.Screen.ID {
			dispLead = scr.Lead
		}
	}
	if !dispLead {
		t.Fatalf("display did not become lead: %+v", session.Screens)
	}
	// Display with walk-up off can read current eligible party item (media-alpha) while joined, not uncommanded media.
	authDisp := authorizeBoundDevice("device-karaoke-tv", displaySess, "tv-secret", true)
	if err := h.requireWalkUpOrCommandedMedia(ctx, mustScope(t, h, displaySess), "media-alpha"); err != nil {
		t.Fatalf("joined display current party media denied: %v", err)
	}
	if err := h.requireWalkUpOrCommandedMedia(ctx, mustScope(t, h, displaySess), "media-archive"); err == nil {
		t.Fatal("joined display allowed uncommanded restricted media")
	}
	// Display reads its own stage screen, and cannot self-register a screen.
	var dispStage PartyStageInfo
	execGraph(t, h, authDisp, fmt.Sprintf(`{ partyStage(sessionID: %q, screenID: %q) { sessionID lead current { mediaID title } } }`, session.ID, joinRes.Screen.ID), nil, "partyStage", &dispStage)
	if !dispStage.Lead || dispStage.Current == nil || dispStage.Current.MediaID != "media-alpha" {
		t.Fatalf("display stage = %+v", dispStage)
	}
	_, selfRegErrs := post(t, h, marshalQuery(fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Hack", kind: "joined", carrier: "local") { id } }`, session.ID)), authDisp)
	if len(selfRegErrs) != 1 || selfRegErrs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("player self-register = %#v, want FORBIDDEN", selfRegErrs)
	}
	// If the rotation advances to an entry the chosen-lead display cannot play, the pop-out leads instead,
	// and media-alpha is no longer admitted as the current party item.
	execGraph(t, h, authorizeRevealed, `mutation { partyAdd(mediaID: "media-archive", participantName: "Blake") { id } }`, nil, "partyAdd", &session)
	execGraph(t, h, authorizeRevealed, `mutation { partyAdvance { id screens { id lead } } }`, nil, "partyAdvance", &session)
	var popoutLeads, displayLeads bool
	for _, scr := range session.Screens {
		if scr.ID == popout.ID {
			popoutLeads = scr.Lead
		}
		if scr.ID == joinRes.Screen.ID {
			displayLeads = scr.Lead
		}
	}
	if !popoutLeads || displayLeads {
		t.Fatalf("after advancing to ineligible entry, screens = %+v; want popout lead, display follower", session.Screens)
	}
	if err := h.requireWalkUpOrCommandedMedia(ctx, mustScope(t, h, displaySess), "media-alpha"); err == nil {
		t.Fatal("previous party item stayed allowed after advancing away from it")
	}

	// D4 commands while in party:
	// - Controller receives typed CONFLICT / DISPLAY_IN_PARTY ("display in party")
	// - Non-controller (profile-managed) still receives NOT_FOUND "unknown session"
	_, inPartyErrs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 0,
		"commandID":        "cmd_party_pause_0001",
		"command":          "pause",
	}, authorizeViewer)
	if len(inPartyErrs) != 1 || inPartyErrs[0].Extensions.Code != ErrorCodeConflict ||
		inPartyErrs[0].Extensions.Reason != ErrorReasonDisplayInParty ||
		!strings.Contains(inPartyErrs[0].Message, "in party") {
		t.Fatalf("controller D4 while in party = %#v, want CONFLICT DISPLAY_IN_PARTY", inPartyErrs)
	}
	_, nonCtrlErrs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 0,
		"commandID":        "cmd_party_nonctrl_01",
		"command":          "pause",
	}, authorizeManaged)
	if len(nonCtrlErrs) != 1 || nonCtrlErrs[0].Extensions.Code != ErrorCodeNotFound || nonCtrlErrs[0].Message != "unknown session" {
		t.Fatalf("non-controller D4 while in party = %#v, want NOT_FOUND unknown session", nonCtrlErrs)
	}

	// Stop releases the display from the party!
	data, stopErrs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 0,
		"commandID":        "cmd_party_stop_00001",
		"command":          "stop",
	}, authorizeViewer)
	if len(stopErrs) != 0 {
		t.Fatalf("stop in party errors = %#v", stopErrs)
	}
	var stopRes ControlRendererSessionResult
	mustUnmarshal(t, data["controlRendererSession"], &stopRes)
	if stopRes.Status != "APPLIED" || stopRes.Revision != 1 {
		t.Fatalf("stopRes = %+v", stopRes)
	}
	// Display is no longer in party -> stage read refused and D4 play-media works again.
	_, postStopStageErrs := post(t, h, marshalQuery(fmt.Sprintf(`query { partyStage(sessionID: %q, screenID: %q) { sessionID } }`, session.ID, joinRes.Screen.ID)), authDisp)
	if len(postStopStageErrs) != 1 || postStopStageErrs[0].Extensions.Code != ErrorCodeNotFound {
		t.Fatalf("stage after stop = %#v, want NOT_FOUND", postStopStageErrs)
	}
	_, postStopPlayErrs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 1,
		"commandID":        "cmd_after_stop_play1",
		"command":          "play-media",
		"mediaID":          "media-alpha",
	}, authorizeViewer)
	if len(postStopPlayErrs) != 0 {
		t.Fatalf("play-media after stop release errors = %#v", postStopPlayErrs)
	}

	// Re-join display, then remove host from controllers list -> releases display!
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenAddDisplay(handle: %q) { screen { id } } }`, displayHandle), nil, "partyScreenAddDisplay", &joinRes)
	if _, err := st.SetPlayerControllers(ctx, player.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	execGraph(t, h, authorizeRevealed, `{ partySession { id screens { id } } }`, nil, "partySession", &session)
	if len(session.Screens) != 1 || session.Screens[0].ID != popout.ID {
		t.Fatalf("screens after controller removal = %+v, want only popout", session.Screens)
	}

	// Re-enable allAdults, re-join display, then test lazy release when host adult Profile changes to managed!
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenAddDisplay(handle: %q) { screen { id } } }`, displayHandle), nil, "partyScreenAddDisplay", &joinRes)
	// Demote a second adult host -> managed via UpdateProfileSettings -> lazily releases on next party change!
	host2, err := st.CreateProfile(ctx, "Second Host", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-host2", Name: "Host2", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("h2-secret")},
		store.Session{ID: "session-host2", DeviceID: "device-host2", ProfileID: host2.ID, ExpiresAt: clock.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	authHost2 := func(req *http.Request) {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Beamfall-Session", "session-host2")
		req.Header.Set("X-Beamfall-Device", "device-host2")
		req.Header.Set("X-Beamfall-Device-Secret", "h2-secret")
	}
	// End first party, start party as host2, join display, demote host2 -> managed, then run partyUpdate!
	var ended bool
	execGraph(t, h, authorizeRevealed, `mutation { partyEnd }`, nil, "partyEnd", &ended)
	execGraph(t, h, authHost2, `mutation { partyStart(libraryIDs: ["library-local"]) { id } }`, nil, "partyStart", &session)
	execGraph(t, h, authHost2, fmt.Sprintf(`mutation { partyScreenAddDisplay(handle: %q) { screen { id } } }`, displayHandle), nil, "partyScreenAddDisplay", &joinRes)
	managedKind := store.ProfileManaged
	if _, err := st.UpdateProfileSettings(ctx, host2.ID, store.ProfileSettings{Kind: &managedKind}); err != nil {
		t.Fatal(err)
	}
	execGraph(t, h, authHost2, `mutation { partyUpdate(autoAdd: true) { id screens { id } } }`, nil, "partyUpdate", &session)
	if len(session.Screens) != 0 {
		t.Fatalf("screens after lazy host demotion = %+v, want empty", session.Screens)
	}
}

func TestPartyStageAndD4RejectGuestTokens(t *testing.T) {
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	ctx := context.Background()

	var session PartySessionInfo
	execGraph(t, h, authorizeRevealed, `mutation { partyStart(libraryIDs: ["library-local"]) { id joinCode } }`, nil, "partyStart", &session)
	if _, err := st.JoinParty(ctx, session.JoinCode, "Guest Casey", "guest-token-hash-only"); err != nil {
		t.Fatal(err)
	}

	marshalQuery := func(q string) string {
		b, _ := json.Marshal(map[string]any{"query": q})
		return string(b)
	}
	// Guest header alone (no enrolled Device/Session) cannot register a screen, read stage, or call D4.
	for _, query := range []string{
		marshalQuery(fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Guest", kind: "joined", carrier: "local") { id } }`, session.ID)),
		marshalQuery(fmt.Sprintf(`query { partyStage(sessionID: %q, screenID: "screen-1") { sessionID } }`, session.ID)),
		marshalQuery(`mutation { controlRendererSession(sessionID: "session-default", expectedRevision: 0, commandID: "cmd_guest_blocked_1", command: "pause") { status } }`),
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewBufferString(query))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Beamfall-Party-Guest", "bfg_guest-token")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("guest GraphQL status = %d, want 401; body=%s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Errors []graphError `json:"errors"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Errors) == 0 || resp.Errors[0].Extensions.Code != ErrorCodeUnauthenticated {
			t.Fatalf("guest GraphQL errors = %#v, want UNAUTHENTICATED", resp.Errors)
		}
	}
}

func mustScope(t *testing.T, h *Handler, sessionID store.SessionID) auth.Scope {
	t.Helper()
	got, err := h.auth.Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Scope(%s): %v", sessionID, err)
	}
	return got
}
