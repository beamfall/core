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
	// 30s into the 80s stream of 120s at 1.5x leaves 50s; the observed changeover is 10s.
	if view.ChangeoverSeconds != 10 || view.Waiting[0].ETASeconds != 60 {
		t.Fatalf("changeover %d, first ETA %d; want 10 and 60", view.ChangeoverSeconds, view.Waiting[0].ETASeconds)
	}

	screen0, err := st.RegisterPartyScreen(ctx, view.Session.ID, "profile-default", "session-default", "Host popout", "popout", "local")
	if err != nil {
		t.Fatal(err)
	}
	stage, err := st.PartyStage(ctx, view.Session.ID, screen0.ID, "session-default", host, host, nil)
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
	if _, err := st.RequestPartyEntry(ctx, "hash-casey", "media-archive", 0); !errors.Is(err, ErrPartyInvalid) {
		t.Fatalf("request outside visibility = %v", err)
	}
	var entries []string
	for range PartyMaxGuestWaiting {
		id, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha", 0)
		if err != nil {
			t.Fatal(err)
		}
		if id.ID == "" || id.State != PartyEntryPending || id.Title != "Sample Media" {
			t.Fatalf("requested entry = %+v", id)
		}
		entries = append(entries, id.ID)
	}
	if _, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha", 0); !errors.Is(err, ErrPartyWaitingLimit) {
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

	screen, err := st.RegisterPartyScreen(ctx, view.Session.ID, "profile-default", "session-default", "Living room", "popout", "local")
	if err != nil || !screen.Lead || screen.Ticker {
		t.Fatalf("RegisterPartyScreen = %+v, %v", screen, err)
	}
	if err := st.ReportPartyScreen(ctx, screen.ID, "profile-managed", "session-managed", 5, 10); !errors.Is(err, ErrPartyInvalid) {
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

func TestPartyAnchorAdvancesAtTempo(t *testing.T) {
	session := PartySession{PlaybackState: "playing", PositionMS: 30_000, AnchorAtMS: 1_000, Tempo: 1.5}
	if got := session.targetPositionMS(11_000); got != 45_000 {
		t.Fatalf("target = %d, want 45000 (10s of wall time at 1.5x)", got)
	}

	st := openSeeded(t)
	ctx := context.Background()
	clock := time.UnixMilli(1_900_000_000_000)
	st.now = func() time.Time { return clock }
	startTestParty(t, st, false)
	host := partyHostFilter()
	for range 2 {
		if err := st.AddPartyEntry(ctx, "profile-default", host, "media-alpha", "", "Avery"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AdvanceParty(ctx, "profile-default", host, ""); err != nil {
		t.Fatal(err)
	}
	view, _, _ := st.PartySessionForHost(ctx, "profile-default", host)
	tempo := 1.5
	if err := st.SetPartyPlayback(ctx, "profile-default", host, PartyPlaybackInput{
		EntryID: view.Current.ID, State: "playing", PositionMS: 60_000, Tempo: &tempo,
	}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(10 * time.Second)
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	// 75s into the 120s Media at 1.5x leaves 30s of wall time.
	if eta := view.Waiting[0].ETASeconds - view.ChangeoverSeconds; eta != 30 {
		t.Fatalf("first ETA less changeover = %d, want 30", eta)
	}
}

func TestPartyGuestsFollowHostAccess(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	view := startTestParty(t, st, false)
	if _, err := st.JoinParty(ctx, view.Session.JoinCode, "Casey", "hash-casey"); err != nil {
		t.Fatal(err)
	}

	narrowed, _, err := st.partyHostFilterLocked(ctx, LibraryFilter{
		AllowedIDs: []LibraryID{"library-local"}, GrantedMediaIDs: []MediaID{"media-alpha"},
	}, []LibraryID{"library-local"})
	if err != nil || len(narrowed.GrantedMediaIDs) != 1 || narrowed.GrantedMediaIDs[0] != "media-alpha" {
		t.Fatalf("host filter grants = %v, %v", narrowed.GrantedMediaIDs, err)
	}

	if results, err := st.SearchPartyMedia(ctx, "hash-casey", "Media", 10); err != nil || len(results) != 1 {
		t.Fatalf("guest search before revocation = %+v, %v", results, err)
	}
	if _, err := st.SetProfileLibraries(ctx, "profile-default", []LibraryID{}); err != nil {
		t.Fatal(err)
	}
	if results, err := st.SearchPartyMedia(ctx, "hash-casey", "Media", 10); err != nil || len(results) != 0 {
		t.Fatalf("guest search after revocation = %+v, %v", results, err)
	}
	if _, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha", 0); !errors.Is(err, ErrPartyInvalid) {
		t.Fatalf("request after revocation = %v", err)
	}
}

func TestPartyScreenAdvanceAndDrop(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	view := startTestParty(t, st, false)
	host := partyHostFilter()
	for range 2 {
		if err := st.AddPartyEntry(ctx, "profile-default", host, "media-alpha", "", "Avery"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AdvanceParty(ctx, "profile-default", host, ""); err != nil {
		t.Fatal(err)
	}
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	first := view.Current.ID
	other := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	// Without a screen on the session, another Profile cannot advance it.
	if err := st.AdvanceParty(ctx, "profile-managed", other, first); !errors.Is(err, ErrPartyNotFound) {
		t.Fatalf("advance without a screen = %v", err)
	}
	screen, err := st.RegisterPartyScreen(ctx, view.Session.ID, "profile-managed", "session-managed", "Den", "joined", "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PartyStage(ctx, view.Session.ID, screen.ID, "session-managed", other, other, nil); err != nil {
		t.Fatalf("stage for a live screen = %v", err)
	}
	if err := st.AdvanceParty(ctx, "profile-managed", other, first); err != nil {
		t.Fatalf("screen advance = %v", err)
	}
	view, _, _ = st.PartySessionForHost(ctx, "profile-default", host)
	if view.Current == nil || view.Current.ID == first {
		t.Fatalf("screen advance left current at %+v", view.Current)
	}

	if err := st.DropPartyScreen(ctx, "profile-default", host, screen.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PartyStage(ctx, view.Session.ID, screen.ID, "session-managed", other, other, nil); !errors.Is(err, ErrPartyInvalid) {
		t.Fatalf("stage for a dropped screen = %v", err)
	}
	if err := st.AdvanceParty(ctx, "profile-managed", other, view.Current.ID); !errors.Is(err, ErrPartyNotFound) {
		t.Fatalf("advance from a dropped screen = %v", err)
	}
}

func TestPartyRequestedKeyStartsTheTurn(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	view := startTestParty(t, st, true)
	host := partyHostFilter()
	if _, err := st.JoinParty(ctx, view.Session.JoinCode, "Casey", "hash-casey"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []int{13, -13} {
		if _, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha", bad); !errors.Is(err, ErrPartyInvalid) {
			t.Fatalf("key %d request = %v, want invalid", bad, err)
		}
	}
	created, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha", -2)
	if err != nil {
		t.Fatal(err)
	}
	if created.PitchSemitones != -2 {
		t.Fatalf("created key = %d, want -2", created.PitchSemitones)
	}
	view, _, err = st.PartySessionForHost(ctx, "profile-default", host)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Waiting) != 1 || view.Waiting[0].PitchSemitones != -2 {
		t.Fatalf("waiting = %+v, want one entry at key -2", view.Waiting)
	}
	if err := st.AdvanceParty(ctx, "profile-default", host, ""); err != nil {
		t.Fatal(err)
	}
	view, _, err = st.PartySessionForHost(ctx, "profile-default", host)
	if err != nil {
		t.Fatal(err)
	}
	if view.Current == nil || view.Current.ID != created.ID || view.Playback == nil || view.Playback.PitchSemitones != -2 {
		t.Fatalf("current %+v playback %+v; want the requested entry at key -2", view.Current, view.Playback)
	}
}

// TestPartySubtitlesPreferThePluginTemplate pins CORE-3048: party entries and
// guest search name what the wired plugin subtitle renders, and fall back to
// the first linked Entity only where it renders nothing.
func TestPartySubtitlesPreferThePluginTemplate(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	st.SetPartySubtitler(func(_ context.Context, ids []MediaID) map[MediaID]string {
		out := map[MediaID]string{}
		for _, id := range ids {
			if id == "media-alpha" {
				out[id] = "Juniper Static"
			}
		}
		return out
	})
	view := startTestParty(t, st, true)
	if _, err := st.JoinParty(ctx, view.Session.JoinCode, "Casey", "hash-casey"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestPartyEntry(ctx, "hash-casey", "media-alpha", 0); err != nil {
		t.Fatal(err)
	}
	_, guestView, err := st.PartyGuestView(ctx, "hash-casey")
	if err != nil {
		t.Fatal(err)
	}
	if len(guestView.Waiting) != 1 || guestView.Waiting[0].Subtitle != "Juniper Static" {
		t.Fatalf("waiting = %+v, want the rendered subtitle", guestView.Waiting)
	}
	results, err := st.SearchPartyMedia(ctx, "hash-casey", "Media", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, result := range results {
		if result.MediaID == "media-alpha" {
			found = result.Subtitle == "Juniper Static"
		}
	}
	if !found {
		t.Fatalf("search = %+v, want media-alpha with the rendered subtitle", results)
	}
}
