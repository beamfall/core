package graph

import (
	"fmt"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

const partySelection = `{ id joinCode joinPath fairness autoAdd joinOpen libraryIDs expiresAt changeoverSeconds
	current { id mediaID title state participantName artworkURL lastPlayedAt }
	playback { entryID state positionMS anchorAtMS tempo pitchSemitones }
	entries { id mediaID participantID participantName state pinned position etaSeconds addedAt }
	requests { id } recent { id } removed { id } skipped { id }
	participants { id name guest waiting lastTurnAt }
	screens { id name kind carrier lead ticker offsetMS rttMS reportedAt } }`

func TestPartySessionGraphOperations(t *testing.T) {
	h := newTestHandlerWithStore(storetest.Open(t))
	write := func(field, operation string) PartySessionInfo {
		t.Helper()
		var out PartySessionInfo
		execGraph(t, h, authorizeRevealed, operation+" "+partySelection+" }", nil, field, &out)
		return out
	}

	var none *PartySessionInfo
	execGraph(t, h, authorizeRevealed, "{ partySession "+partySelection+" }", nil, "partySession", &none)
	if none != nil {
		t.Fatalf("partySession before start = %+v", none)
	}
	session := write("partyStart", `mutation { partyStart(libraryIDs: ["library-local", "library-archive"], fairness: "round-robin", autoAdd: false)`)
	if session.ID == "" || len(session.JoinCode) != 4 || session.JoinPath != "/web/join/"+session.JoinCode ||
		session.Entries == nil || session.Screens == nil || session.Current != nil {
		t.Fatalf("partyStart = %+v", session)
	}
	session = write("partyUpdate", `mutation { partyUpdate(fairness: "fifo", autoAdd: true, joinOpen: true)`)
	if session.Fairness != "fifo" || !session.AutoAdd || !session.JoinOpen {
		t.Fatalf("partyUpdate = %+v", session)
	}
	oldCode := session.JoinCode
	session = write("partyRotateCode", `mutation { partyRotateCode`)
	if session.JoinCode == oldCode {
		t.Fatal("partyRotateCode kept the old code")
	}

	session = write("partyAdd", `mutation { partyAdd(mediaID: "media-alpha", participantName: "Avery")`)
	session = write("partyAdd", `mutation { partyAdd(mediaID: "media-alpha", participantName: "Blake")`)
	if len(session.Entries) != 2 || session.Entries[0].ParticipantName != "Avery" || session.Entries[0].MediaID != "media-alpha" {
		t.Fatalf("entries after partyAdd = %+v", session.Entries)
	}
	first, second := session.Entries[0].ID, session.Entries[1].ID
	session = write("partyMove", fmt.Sprintf(`mutation { partyMove(entryID: %q, position: 1)`, second))
	if session.Entries[0].ID != second || !session.Entries[0].Pinned {
		t.Fatalf("partyMove = %+v", session.Entries)
	}
	session = write("partyRemove", fmt.Sprintf(`mutation { partyRemove(entryID: %q)`, first))
	if len(session.Removed) != 1 || session.Removed[0].ID != first {
		t.Fatalf("partyRemove = %+v", session.Removed)
	}
	session = write("partyRestore", fmt.Sprintf(`mutation { partyRestore(entryID: %q)`, first))
	if len(session.Entries) != 2 || len(session.Removed) != 0 {
		t.Fatalf("partyRestore = %+v", session)
	}

	session = write("partyAdvance", `mutation { partyAdvance`)
	if session.Current == nil || session.Current.ID != second || session.Playback == nil || session.Playback.State != "paused" {
		t.Fatalf("partyAdvance = %+v", session.Current)
	}
	session = write("partyPlayback", fmt.Sprintf(`mutation { partyPlayback(entryID: %q, state: "playing", positionMS: 1500, tempo: 1.25, pitchSemitones: -2)`, second))
	if session.Playback.State != "playing" || session.Playback.PositionMS != 1500 || session.Playback.Tempo != 1.25 || session.Playback.PitchSemitones != -2 {
		t.Fatalf("partyPlayback = %+v", session.Playback)
	}

	var screen PartyScreenInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenRegister(sessionID: %q, name: "Living room", kind: "popout", carrier: "local") { id lead ticker kind carrier } }`, session.ID),
		nil, "partyScreenRegister", &screen)
	if screen.ID == "" || !screen.Lead || screen.Kind != "popout" {
		t.Fatalf("partyScreenRegister = %+v", screen)
	}
	var reported bool
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`mutation { partyScreenReport(screenID: %q, offsetMS: -12, rttMS: 40) }`, screen.ID), nil, "partyScreenReport", &reported)
	if !reported {
		t.Fatal("partyScreenReport = false")
	}
	session = write("partyScreenTicker", fmt.Sprintf(`mutation { partyScreenTicker(screenID: %q, ticker: true)`, screen.ID))
	if len(session.Screens) != 1 || !session.Screens[0].Ticker || session.Screens[0].OffsetMS != -12 {
		t.Fatalf("partyScreenTicker = %+v", session.Screens)
	}

	var stage PartyStageInfo
	execGraph(t, h, authorizeRevealed, fmt.Sprintf(`{ partyStage(sessionID: %q, screenID: %q) { sessionID serverNowMS current { id } playback { state } upNext { id } ticker joinCode joinPath } }`, session.ID, screen.ID),
		nil, "partyStage", &stage)
	if stage.SessionID != session.ID || stage.Current == nil || stage.Current.ID != second || !stage.Ticker || stage.JoinPath == "" || stage.ServerNowMS == 0 {
		t.Fatalf("partyStage = %+v", stage)
	}

	session = write("partyScreenDrop", fmt.Sprintf(`mutation { partyScreenDrop(screenID: %q)`, screen.ID))
	if len(session.Screens) != 0 {
		t.Fatalf("partyScreenDrop = %+v", session.Screens)
	}
	var avery string
	for _, participant := range session.Participants {
		if participant.Name == "Avery" {
			avery = participant.ID
		}
	}
	session = write("partyRemoveParticipant", fmt.Sprintf(`mutation { partyRemoveParticipant(participantID: %q)`, avery))
	for _, participant := range session.Participants {
		if participant.ID == avery {
			t.Fatalf("partyRemoveParticipant kept %+v", participant)
		}
	}

	resp := postGraphJSON(t, h, authorizeRevealed, `mutation { partyReview(entryID: "pe-missing", approve: true) `+partySelection+" }", nil)
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0].Message, "entry not found") {
		t.Fatalf("partyReview of a missing entry = %+v", resp.Errors)
	}

	var ended bool
	execGraph(t, h, authorizeRevealed, `mutation { partyEnd }`, nil, "partyEnd", &ended)
	if !ended {
		t.Fatal("partyEnd = false")
	}
	resp = postGraphJSON(t, h, authorizeRevealed, `mutation { partyAdvance `+partySelection+" }", nil)
	if len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0].Message, "party session not found") {
		t.Fatalf("partyAdvance after end = %+v", resp.Errors)
	}
}

func TestPartyBasePathOf(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                "",
		"https://media.example.test":      "",
		"https://media.example.test/":     "",
		"https://media.example.test/bf/":  "/bf",
		"https://media.example.test/a//b": "/a/b",
	} {
		if got := partyBasePathOf(raw); got != want {
			t.Errorf("partyBasePathOf(%q) = %q, want %q", raw, got, want)
		}
	}
}
