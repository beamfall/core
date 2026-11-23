package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestControlRendererSessionApplyReplayConflictReuseAndPrune(t *testing.T) {
	ctx := t.Context()
	st := openSeeded(t)
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	rev0, err := st.RendererSessionRevision(ctx, "session-default")
	if err != nil {
		t.Fatalf("RendererSessionRevision initial: %v", err)
	}
	if rev0 != 0 {
		t.Fatalf("initial revision = %d, want 0", rev0)
	}

	payloadPause, err := NewSessionControlPayload(SessionCommandPause, SessionControlArgs{}, "device-mac", "profile-default", "media-alpha")
	if err != nil {
		t.Fatalf("NewSessionControlPayload: %v", err)
	}
	cmd1 := "cmd_apply_00000001"
	res1, err := st.ControlRendererSession(ctx, "session-default", 0, cmd1, payloadPause, &ActivityEntry{
		OccurredAt: t0,
		Action:     ActivitySessionControl,
		SessionID:  "session-manager",
		DeviceID:   "device-mac",
		ProfileID:  "profile-default",
		TargetType: "session",
		TargetID:   "session-default",
	}, t0)
	if err != nil {
		t.Fatalf("ControlRendererSession apply: %v", err)
	}
	if res1.Status != RendererSessionControlApplied || res1.Revision != 1 || res1.Replayed || res1.SessionID != "session-default" || res1.Command != SessionCommandPause {
		t.Fatalf("apply result = %+v, want APPLIED at rev 1", res1)
	}

	rev1, err := st.RendererSessionRevision(ctx, "session-default")
	if err != nil || rev1 != 1 {
		t.Fatalf("revision after apply = %d (%v), want 1", rev1, err)
	}

	// Idempotent replay with the same commandId and canonical body returns the
	// stored result even though current revision is 1 and expectedRevision is 0,
	// without emitting a second outbox event or bumping the revision.
	replay1, err := st.ControlRendererSession(ctx, "session-default", 0, cmd1, payloadPause, nil, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("ControlRendererSession replay: %v", err)
	}
	if !replay1.Replayed || replay1.Status != RendererSessionControlApplied || replay1.Revision != 1 || replay1.Command != SessionCommandPause {
		t.Fatalf("replay result = %+v, want Replayed=true APPLIED rev 1", replay1)
	}
	if revAfterReplay, err := st.RendererSessionRevision(ctx, "session-default"); err != nil || revAfterReplay != 1 {
		t.Fatalf("revision after replay = %d (%v), want 1", revAfterReplay, err)
	}

	// Normalized argument defaults hash identically on replay: skip-media with
	// empty Direction normalizes to "next".
	payloadSkipEmpty, err := NewSessionControlPayload(SessionCommandSkipMedia, SessionControlArgs{}, "device-mac", "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	payloadSkipExplicit, err := NewSessionControlPayload(SessionCommandSkipMedia, SessionControlArgs{Direction: SessionDirectionNext}, "device-mac", "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	cmdSkip := "cmd_skip_000000002"
	resSkip, err := st.ControlRendererSession(ctx, "session-default", 1, cmdSkip, payloadSkipEmpty, nil, t0.Add(2*time.Minute))
	if err != nil || resSkip.Revision != 2 {
		t.Fatalf("skip-media apply = %+v, %v", resSkip, err)
	}
	replaySkip, err := st.ControlRendererSession(ctx, "session-default", 1, cmdSkip, payloadSkipExplicit, nil, t0.Add(3*time.Minute))
	if err != nil || !replaySkip.Replayed || replaySkip.Revision != 2 {
		t.Fatalf("skip-media normalized replay = %+v, %v", replaySkip, err)
	}

	// Stale revision returns typed conflict with current revision (2) and
	// emits no event and records no command row.
	cmdConflict := "cmd_conflict_00003"
	_, err = st.ControlRendererSession(ctx, "session-default", 1, cmdConflict, payloadPause, nil, t0.Add(4*time.Minute))
	var conflict RendererSessionRevisionConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrRendererSessionRevisionConflict) {
		t.Fatalf("stale revision err = %v, want RendererSessionRevisionConflictError", err)
	}
	if conflict.CurrentRevision != 2 || conflict.ExpectedRevision != 1 || conflict.SessionID != "session-default" {
		t.Fatalf("conflict = %+v, want session-default expected 1 current 2", conflict)
	}

	// Reusing an existing commandId with a different body or different session
	// returns a typed reuse error and performs no mutation.
	payloadResume, err := NewSessionControlPayload(SessionCommandResume, SessionControlArgs{}, "device-mac", "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ControlRendererSession(ctx, "session-default", 2, cmd1, payloadResume, nil, t0.Add(5*time.Minute))
	var reuse RendererSessionCommandReuseError
	if !errors.As(err, &reuse) || !errors.Is(err, ErrRendererSessionCommandReuse) {
		t.Fatalf("commandId reuse different command err = %v, want RendererSessionCommandReuseError", err)
	}
	_, err = st.ControlRendererSession(ctx, "session-manager", 0, cmd1, payloadPause, nil, t0.Add(5*time.Minute))
	if !errors.Is(err, ErrRendererSessionCommandReuse) {
		t.Fatalf("commandId reuse different session err = %v, want ErrRendererSessionCommandReuse", err)
	}

	// Only the two applied commands wrote session.control outbox events.
	events, err := st.ReadEventOutbox(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sessionEvents int
	for _, ev := range events {
		if ev.Kind == SessionControlKind {
			sessionEvents++
		}
	}
	if sessionEvents != 2 {
		t.Fatalf("session.control outbox events = %d, want 2", sessionEvents)
	}

	// Writing more than 24h later prunes the expired command ledger rows.
	cmdAfterTTL := "cmd_after_ttl_0004"
	later := t0.Add(25 * time.Hour)
	if _, err := st.ControlRendererSession(ctx, "session-default", 2, cmdAfterTTL, payloadPause, nil, later); err != nil {
		t.Fatalf("apply after 24h: %v", err)
	}
	var oldCount int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM renderer_session_command WHERE command_id = ?`, cmd1).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 {
		t.Fatalf("expired command row %q survived 24h prune on write", cmd1)
	}
	var keptCount int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM renderer_session_command WHERE command_id = ?`, cmdAfterTTL).Scan(&keptCount); err != nil {
		t.Fatal(err)
	}
	if keptCount != 1 {
		t.Fatalf("fresh command row %q missing after write", cmdAfterTTL)
	}
}

func TestLegacySessionControlBumpsRendererRevision(t *testing.T) {
	ctx := t.Context()
	st := openSeeded(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	payload, err := NewSessionControlPayload(SessionCommandPause, SessionControlArgs{}, "device-mac", "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSessionControlEvent(ctx, "session-default", payload, now); err != nil {
		t.Fatalf("AppendSessionControlEvent: %v", err)
	}
	if err := st.AppendSessionControlEventWithActivity(ctx, "session-default", payload, ActivityEntry{
		OccurredAt: now.Add(time.Second),
		Action:     ActivitySessionControl,
		SessionID:  "session-manager",
		DeviceID:   "device-mac",
		ProfileID:  "profile-default",
		TargetType: "session",
		TargetID:   "session-default",
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendSessionControlEventWithActivity: %v", err)
	}

	revs, err := st.RendererSessionRevisions(ctx, []SessionID{"session-default", "session-manager"})
	if err != nil {
		t.Fatal(err)
	}
	if revs["session-default"] != 2 {
		t.Fatalf("session-default revision after 2 legacy publishes = %d, want 2", revs["session-default"])
	}
	if revs["session-manager"] != 0 {
		t.Fatalf("session-manager revision = %d, want 0", revs["session-manager"])
	}

	// A revision-checked caller still holding stale expectedRevision=0 sees
	// the conflict at current revision 2.
	_, err = st.ControlRendererSession(ctx, "session-default", 0, "cmd_after_legacy_1", payload, nil, now.Add(2*time.Second))
	var conflict RendererSessionRevisionConflictError
	if !errors.As(err, &conflict) || conflict.CurrentRevision != 2 {
		t.Fatalf("post-legacy conflict = %v, want CurrentRevision=2", err)
	}
}

func TestControlRendererSessionValidationAndRollback(t *testing.T) {
	ctx := t.Context()
	st := openSeeded(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	payload, err := NewSessionControlPayload(SessionCommandPause, SessionControlArgs{}, "device-mac", "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ControlRendererSession(ctx, "session-default", -1, "cmd_valid_00000001", payload, nil, now); err == nil {
		t.Fatal("negative expectedRevision was accepted")
	}
	for _, badID := range []string{"", "too-short", strings.Repeat("a", 129), "bad space 00000001"} {
		if _, err := st.ControlRendererSession(ctx, "session-default", 0, badID, payload, nil, now); err == nil {
			t.Errorf("bad commandID %q was accepted", badID)
		}
	}

	// An activity insert failure rolls back the event, revision bump, and command row.
	if _, err := st.eventsW.ExecContext(ctx, `
		CREATE TEMP TRIGGER fail_renderer_control_activity
		BEFORE INSERT ON activity_entry
		WHEN NEW.action = 'session.control'
		BEGIN
		  SELECT RAISE(ABORT, 'injected activity failure');
		END;
	`); err != nil {
		t.Fatal(err)
	}
	err = func() error {
		_, callErr := st.ControlRendererSession(ctx, "session-default", 0, "cmd_rollback_00001", payload, &ActivityEntry{
			OccurredAt: now,
			Action:     ActivitySessionControl,
			ProfileID:  "profile-default",
			TargetType: "session",
			TargetID:   "session-default",
		}, now)
		return callErr
	}()
	if err == nil || !strings.Contains(err.Error(), "injected activity failure") {
		t.Fatalf("rollback test err = %v, want injected activity failure", err)
	}
	if rev, err := st.RendererSessionRevision(ctx, "session-default"); err != nil || rev != 0 {
		t.Fatalf("revision after rollback = %d (%v), want 0", rev, err)
	}
	var cmdCount int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM renderer_session_command`).Scan(&cmdCount); err != nil {
		t.Fatal(err)
	}
	if cmdCount != 0 {
		t.Fatalf("command ledger rows after rollback = %d, want 0", cmdCount)
	}
}
