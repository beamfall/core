package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func partyHostFilter() LibraryFilter {
	return LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
}

func startTestParty(t *testing.T, st *Store, autoAdd bool) PartyView {
	t.Helper()
	ctx := context.Background()
	id, err := st.StartPartySession(ctx, PartyStartInput{
		Host: "profile-default", HostName: "Rowan", LibraryIDs: []LibraryID{"library-local", "library-archive"},
		AutoAdd: autoAdd, Filter: partyHostFilter(),
	})
	if err != nil {
		t.Fatalf("StartPartySession: %v", err)
	}
	view, ok, err := st.PartySessionForHost(ctx, "profile-default", partyHostFilter())
	if err != nil || !ok || view.Session.ID != id {
		t.Fatalf("PartySessionForHost = %v, %v, %v", view.Session.ID, ok, err)
	}
	return view
}

func waitingNames(view PartyView) []string {
	out := make([]string, len(view.Waiting))
	for i, entry := range view.Waiting {
		out[i] = entry.ParticipantName
	}
	return out
}

func TestPartySessionRotationAdvanceAndPlayback(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	clock := time.UnixMilli(1_900_000_000_000)
	st.now = func() time.Time { return clock }
	view := startTestParty(t, st, false)

	// Starting again returns the live session unchanged.
	again, err := st.StartPartySession(ctx, PartyStartInput{Host: "profile-default", LibraryIDs: []LibraryID{"library-local"}, Filter: partyHostFilter()})
	if err != nil || again != view.Session.ID {
		t.Fatalf("second start = %q, %v; want %q", again, err, view.Session.ID)
	}
	// The relocked host filter keeps the restricted Library out of the session.
	if len(view.Session.LibraryIDs) != 1 || view.Session.LibraryIDs[0] != "library-local" {
		t.Fatalf("session libraries = %v", view.Session.LibraryIDs)
	}

	host := partyHostFilter()
	for range 3 {
		clock = clock.Add(time.Second)
		if err := st.AddPartyEntry(ctx, "profile-default", host, "media-alpha", "", "Avery"); err != nil {
			t.Fatalf("AddPartyEntry Avery: %v", err)
		}
	}
	clock = clock.Add(time.Second)
	if err := st.AddPartyEntry(ctx, "profile-default", host, "media-alpha", "", "blake"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddPartyEntry(ctx, "profile-default", host, "media-archive", "", "Blake"); err == nil {
		t.Fatal("adding Media outside the session's visibility succeeded")
	}
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	if got := waitingNames(view); len(got) != 4 || got[0] != "Avery" || got[1] != "blake" || got[2] != "Avery" {
		t.Fatalf("waiting order = %v, want Avery, blake, Avery, Avery", got)
	}
	if view.Waiting[0].Title != "Sample Media" || view.Waiting[0].ETASeconds != 0 || view.Waiting[1].ETASeconds != 140 {
		t.Fatalf("first entries = %+v / %+v", view.Waiting[0], view.Waiting[1])
	}

	first := view.Waiting[0].ID
	if err := st.AdvanceParty(ctx, "profile-default", host, ""); err != nil {
		t.Fatal(err)
	}
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	if view.Current == nil || view.Current.ID != first || view.Playback.State != "paused" {
		t.Fatalf("current after advance = %+v", view.Current)
	}
	// A stale fromEntryID leaves the session as it is.
	if err := st.AdvanceParty(ctx, "profile-default", host, "pe-stale"); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := st.PartySessionForHost(ctx, "profile-default", host); again.Current.ID != first {
		t.Fatalf("stale advance moved the session to %s", again.Current.ID)
	}

	tempo := 1.5
	clock = clock.Add(10 * time.Second)
	if err := st.SetPartyPlayback(ctx, "profile-default", host, PartyPlaybackInput{EntryID: first, State: "playing", Tempo: &tempo}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPartyPlayback(ctx, "profile-default", host, PartyPlaybackInput{EntryID: "pe-other", State: "playing"}); !errors.Is(err, ErrPartyInvalid) {
		t.Fatalf("playback on a non-current entry = %v", err)
	}
	clock = clock.Add(30 * time.Second)
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	// 45s of 120s played at 1.5x leaves 50s; the observed changeover is 10s.
	if view.ChangeoverSeconds != 10 || view.Waiting[0].ETASeconds != 60 {
		t.Fatalf("changeover %d, first ETA %d; want 10 and 60", view.ChangeoverSeconds, view.Waiting[0].ETASeconds)
	}

	stage, err := st.PartyStage(ctx, view.Session.ID, "")
	if err != nil || stage.Current == nil || stage.Current.ID != first || len(stage.UpNext) != 3 {
		t.Fatalf("PartyStage = %+v, %v", stage, err)
	}

	if err := st.AdvanceParty(ctx, "profile-default", host, first); err != nil {
		t.Fatal(err)
	}
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	if len(view.Recent) != 1 || view.Recent[0].ID != first || view.Current.ParticipantName != "blake" {
		t.Fatalf("after second advance: recent %+v current %+v", view.Recent, view.Current)
	}
	if view.Current.LastPlayedAt.IsZero() {
		t.Fatal("the repeated Media carries no lastPlayedAt")
	}
}

func TestPartyGuestsJoinRequestAndEnd(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	view := startTestParty(t, st, false)
	host := partyHostFilter()

	if _, err := st.JoinParty(ctx, "ZZZZ", "Casey", "hash-casey"); !errors.Is(err, ErrPartyJoinRefused) {
		t.Fatalf("unknown code join = %v", err)
	}
	guest, err := st.JoinParty(ctx, " "+view.Session.JoinCode+" ", "Casey", "hash-casey")
	if err != nil {
		t.Fatalf("JoinParty: %v", err)
	}
	if _, err := st.PartyGuestByToken(ctx, "hash-casey"); err != nil {
		t.Fatalf("PartyGuestByToken: %v", err)
	}

	results, err := st.SearchPartyMedia(ctx, "hash-casey", "Media", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MediaID != "media-alpha" {
		t.Fatalf("guest search = %+v, want only the visible Media", results)
	}
	if _, err := st.RequestPartyEntry(ctx, "hash-casey", "media-archive"); !errors.Is(err, ErrPartyInvalid) {
		t.Fatalf("request outside visibility = %v", err)
	}
	var entries []string
	for range PartyMaxGuestWaiting {
		id, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha")
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, id)
	}
	if _, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha"); !errors.Is(err, ErrPartyWaitingLimit) {
		t.Fatalf("fourth request = %v, want waiting limit", err)
	}
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	if len(view.Requests) != 3 || len(view.Waiting) != 0 {
		t.Fatalf("requests %d waiting %d; want 3 pending", len(view.Requests), len(view.Waiting))
	}
	if err := st.ReviewPartyEntry(ctx, "profile-default", host, entries[0], true); err != nil {
		t.Fatal(err)
	}
	if err := st.ReviewPartyEntry(ctx, "profile-default", host, entries[1], false); err != nil {
		t.Fatal(err)
	}
	if err := st.CancelPartyEntry(ctx, "hash-casey", entries[1]); !errors.Is(err, ErrPartyNotFound) {
		t.Fatalf("cancel of a declined entry = %v", err)
	}
	if err := st.CancelPartyEntry(ctx, "hash-casey", entries[2]); err != nil {
		t.Fatal(err)
	}
	_, guestView, err := st.PartyGuestView(ctx, "hash-casey")
	if err != nil || len(guestView.Waiting) != 1 || guestView.Waiting[0].ParticipantID != guest.ParticipantID {
		t.Fatalf("guest view = %+v, %v", guestView.Waiting, err)
	}

	// Removing then restoring an entry keeps it in the rotation.
	if err := st.RemovePartyEntry(ctx, "profile-default", host, entries[0]); err != nil {
		t.Fatal(err)
	}
	if err := st.RestorePartyEntry(ctx, "profile-default", host, entries[0]); err != nil {
		t.Fatal(err)
	}

	// A rotated code stops admitting guests; the old guest stays in.
	old := view.Session.JoinCode
	if err := st.RotatePartyJoinCode(ctx, "profile-default", host); err != nil {
		t.Fatal(err)
	}
	if _, err := st.JoinParty(ctx, old, "Dana", "hash-dana"); !errors.Is(err, ErrPartyJoinRefused) {
		t.Fatalf("join with a rotated code = %v", err)
	}

	screen, err := st.RegisterPartyScreen(ctx, view.Session.ID, "profile-default", "Living room", "popout", "local")
	if err != nil || !screen.Lead || screen.Ticker {
		t.Fatalf("RegisterPartyScreen = %+v, %v", screen, err)
	}
	if err := st.ReportPartyScreen(ctx, screen.ID, "profile-managed", 5, 10); !errors.Is(err, ErrPartyInvalid) {
		t.Fatalf("report by another Profile = %v", err)
	}

	if ended, err := st.EndPartySession(ctx, "profile-default"); err != nil || !ended {
		t.Fatalf("EndPartySession = %v, %v", ended, err)
	}
	if _, err := st.PartyGuestByToken(ctx, "hash-casey"); !errors.Is(err, ErrPartyGuestRefused) {
		t.Fatalf("guest token after end = %v", err)
	}
	if _, ok, _ := st.PartySessionForHost(ctx, "profile-default", host); ok {
		t.Fatal("ended session still live")
	}

	// Erasing the host Profile removes its sessions.
	tx, err := st.eventsW.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := deletePartyProfileTx(ctx, tx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM party_entry`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("party entries after erasure = %d, %v", left, err)
	}
}
