package auth

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestAccessScheduleReasonVisibleUsesTheEvaluatedSessionSnapshot is CORE-2950:
// the schedule reason is visible only for an enrolled session's refusal, and a
// derived credential sees it only when the one session snapshot the schedule
// was evaluated against holds that credential's session, Profile and device. A
// Profile-bound scoped key's refusal never carries the reason.
// feature:access-schedules
func TestAccessScheduleReasonVisibleUsesTheEvaluatedSessionSnapshot(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	at := time.Date(2026, time.June, 15, 10, 0, 0, 0, time.Local)
	service := NewServiceWithClock(st, func() time.Time { return at })
	if err := st.SetProfileAccessWindows(ctx, "profile-managed", []store.ProfileAccessWindow{
		{Weekday: int(at.Weekday()), StartMinute: 0, EndMinute: at.Hour() * 60},
	}); err != nil {
		t.Fatal(err)
	}
	session, err := st.Session(ctx, "session-managed")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Scope(ctx, "session-managed")
	if !AccessScheduleEnded(err) {
		t.Fatalf("Scope err = %v, want the schedule refusal", err)
	}
	if !AccessScheduleReasonVisible(err, nil) {
		t.Fatal("the session's own refusal must carry the reason")
	}
	own := SessionBinding{SessionID: session.ID, ProfileID: "profile-managed", DeviceID: session.DeviceID}
	if !AccessScheduleReasonVisible(err, &own) {
		t.Fatal("a credential bound to the evaluated snapshot must see the reason")
	}
	for name, bound := range map[string]SessionBinding{
		"other Profile": {SessionID: session.ID, ProfileID: "profile-default", DeviceID: session.DeviceID},
		"other device":  {SessionID: session.ID, ProfileID: "profile-managed", DeviceID: "device-other"},
		"other session": {SessionID: "session-other", ProfileID: "profile-managed", DeviceID: session.DeviceID},
	} {
		if AccessScheduleReasonVisible(err, &bound) {
			t.Fatalf("%s: a credential the snapshot does not hold must not see the reason", name)
		}
	}

	_, key, err := st.CreateScopedAPIKey(ctx, "managed-dash", []store.APIScope{store.ScopeReadSessions}, "profile-managed", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ScopeFromScopedKey(ctx, key); !AccessScheduleEnded(err) || AccessScheduleReasonVisible(err, nil) {
		t.Fatalf("scoped key err = %v: want the schedule refusal without a visible reason", err)
	}
}
