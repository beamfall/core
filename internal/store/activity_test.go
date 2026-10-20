package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func appendActivity(t *testing.T, st *Store, entry ActivityEntry) {
	t.Helper()
	if err := st.AppendActivity(context.Background(), entry); err != nil {
		t.Fatalf("AppendActivity: %v", err)
	}
}

func TestActivityLogIsAppendOnly(t *testing.T) {
	// feature:activity-log
	st := openSeeded(t)
	appendActivity(t, st, ActivityEntry{
		OccurredAt: time.Unix(100, 0).UTC(),
		Action:     ActivityRelock,
		SessionID:  "session-default",
		ProfileID:  "profile-default",
	})
	// The store API has no update/delete path; the schema triggers reject
	// raw SQL that bypasses it.
	if _, err := st.eventsW.Exec("UPDATE activity_entry SET action = 'tampered'"); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE err = %v, want append-only abort", err)
	}
	if _, err := st.eventsW.Exec("DELETE FROM activity_entry"); err == nil ||
		!strings.Contains(err.Error(), "append-only") {
		t.Fatalf("DELETE err = %v, want append-only abort", err)
	}
	entries, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != ActivityRelock {
		t.Fatalf("entries = %+v, want the original relock entry intact", entries)
	}
}

func TestActivityAppendValidation(t *testing.T) {
	st := openSeeded(t)
	if err := st.AppendActivity(context.Background(), ActivityEntry{OccurredAt: time.Now()}); err == nil {
		t.Fatal("append without action succeeded")
	}
	if err := st.AppendActivity(context.Background(), ActivityEntry{Action: ActivityReveal}); err == nil {
		t.Fatal("append without time succeeded")
	}
}

func TestActivityOperationIDIsIdempotent(t *testing.T) {
	st := openSeeded(t)
	entry := ActivityEntry{
		OperationID: "op-idempotent",
		OccurredAt:  time.Unix(100, 0).UTC(),
		Action:      ActivityRelock,
		ProfileID:   "profile-default",
	}
	appendActivity(t, st, entry)
	appendActivity(t, st, entry)
	entries, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].OperationID != entry.OperationID {
		t.Fatalf("entries = %+v, want one idempotent operation", entries)
	}
}

func TestActivityEntriesPreserveCredentialAttribution(t *testing.T) {
	st := openSeeded(t)
	appendActivity(t, st, ActivityEntry{
		OccurredAt: time.Unix(100, 0).UTC(),
		Action:     ActivityTokenCreate,
		TargetType: "token",
		TargetID:   "tok-1",
		TokenID:    "tok-actor",
	})
	appendActivity(t, st, ActivityEntry{
		OccurredAt: time.Unix(101, 0).UTC(),
		Action:     ActivitySessionControl,
		ProfileID:  "profile-default",
		TargetType: "session",
		TargetID:   "session-default",
		APIKeyID:   "key-actor",
	})

	entries, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].APIKeyID != "key-actor" || entries[0].TokenID != "" {
		t.Fatalf("scoped-key entry attribution = %+v", entries[0])
	}
	if entries[1].TokenID != "tok-actor" || entries[1].APIKeyID != "" {
		t.Fatalf("token entry attribution = %+v", entries[1])
	}
}

func TestSessionControlEventWithActivityRollsBackTogether(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if _, err := st.eventsW.ExecContext(ctx, `
		CREATE TEMP TRIGGER fail_session_control_activity
		BEFORE INSERT ON activity_entry
		WHEN NEW.action = 'session.control'
		BEGIN
		  SELECT RAISE(ABORT, 'injected activity failure');
		END;
	`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	err := st.AppendSessionControlEventWithActivity(ctx, "session-default", SessionControlPayload{
		Command:   "pause",
		ProfileID: "profile-default",
		MediaID:   "media-alpha",
	}, ActivityEntry{
		OccurredAt: time.Unix(100, 0).UTC(),
		Action:     ActivitySessionControl,
		ProfileID:  "profile-default",
		TargetType: "session",
		TargetID:   "session-default",
		APIKeyID:   "key-actor",
	}, time.Unix(100, 0).UTC())
	if err == nil || !strings.Contains(err.Error(), "injected activity failure") {
		t.Fatalf("AppendSessionControlEventWithActivity err = %v, want injected activity failure", err)
	}

	events, err := st.ReadEventOutbox(ctx, 0, 100)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	for _, event := range events {
		if event.Kind == SessionControlKind {
			t.Fatalf("session-control event persisted after activity failure: %+v", event)
		}
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatalf("ActivityEntries: %v", err)
	}
	for _, entry := range entries {
		if entry.Action == ActivitySessionControl {
			t.Fatalf("session-control activity persisted after rollback: %+v", entry)
		}
	}
}

func TestActivityEntriesRevealFiltered(t *testing.T) {
	st := openSeeded(t)
	base := time.Unix(1000, 0).UTC()
	appendActivity(t, st, ActivityEntry{
		OccurredAt: base,
		Action:     ActivityMediaTrash,
		ProfileID:  "profile-default",
		TargetType: "media",
		TargetID:   "media-archive",
		LibraryID:  "library-archive", // restricted
	})
	appendActivity(t, st, ActivityEntry{
		OccurredAt: base.Add(time.Second),
		Action:     ActivityMediaTrash,
		ProfileID:  "profile-default",
		TargetType: "media",
		TargetID:   "media-alpha",
		LibraryID:  "library-local",
	})
	appendActivity(t, st, ActivityEntry{
		OccurredAt: base.Add(2 * time.Second),
		Action:     ActivityRelock,
		ProfileID:  "profile-default",
	})

	// Unrevealed reader: the restricted-library entry is missing, with no
	// trace it was filtered — indistinguishable from never having existed.
	hidden, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 2 {
		t.Fatalf("unrevealed entries = %d, want 2", len(hidden))
	}
	for _, entry := range hidden {
		if entry.LibraryID == "library-archive" || entry.TargetID == "media-archive" {
			t.Fatalf("unrevealed read leaked restricted entry: %+v", entry)
		}
	}

	// The unrevealed page is identical to a world where the entry does not
	// exist: same shape as querying a store that never logged it.
	revealed, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 3 {
		t.Fatalf("revealed entries = %d, want 3", len(revealed))
	}
	if revealed[0].Action != ActivityRelock || revealed[2].LibraryID != "library-archive" {
		t.Fatalf("revealed order wrong: %+v", revealed)
	}
}

func TestActivityEntriesAllowedLibrariesFilter(t *testing.T) {
	st := openSeeded(t)
	appendActivity(t, st, ActivityEntry{
		OccurredAt: time.Unix(1, 0).UTC(),
		Action:     ActivityMediaTrash,
		LibraryID:  "library-local",
	})
	// A reader whose Profile has no access to library-local cannot see the
	// entry even when revealed.
	entries, err := st.ActivityEntries(context.Background(), ActivityQuery{
		Filter: LibraryFilter{AllowedIDs: []LibraryID{"library-archive"}, Revealed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none", entries)
	}
}

func TestActivityEntriesUnknownLibraryHidden(t *testing.T) {
	st := openSeeded(t)
	appendActivity(t, st, ActivityEntry{
		OccurredAt: time.Unix(1, 0).UTC(),
		Action:     ActivityMediaTrash,
		LibraryID:  "library-deleted",
	})
	entries, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("entry naming an unknown Library visible: %+v", entries)
	}
}

func TestActivityEntriesPagingAndSelfScope(t *testing.T) {
	st := openSeeded(t)
	base := time.Unix(2000, 0).UTC()
	for i := range 5 {
		profile := ProfileID("profile-default")
		if i%2 == 1 {
			profile = "profile-managed"
		}
		appendActivity(t, st, ActivityEntry{
			OccurredAt: base.Add(time.Duration(i) * time.Second),
			Action:     ActivityProfileSwitch,
			ProfileID:  profile,
			TargetType: "profile",
			TargetID:   string(profile),
		})
	}
	// Paging, newest first.
	page, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}, Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || !page[0].OccurredAt.After(page[1].OccurredAt) {
		t.Fatalf("page = %+v, want 2 entries newest first", page)
	}
	if !page[0].OccurredAt.Equal(base.Add(3 * time.Second)) {
		t.Fatalf("offset ignored: first = %v", page[0].OccurredAt)
	}
	// Self scope: only the named Profile's entries.
	own, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}, ProfileID: "profile-managed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(own) != 2 {
		t.Fatalf("self-scoped entries = %d, want 2", len(own))
	}
	for _, entry := range own {
		if entry.ProfileID != "profile-managed" {
			t.Fatalf("self scope leaked entry: %+v", entry)
		}
	}
}
