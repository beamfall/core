package pairing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func activityEntries(t *testing.T, st *store.Store) []store.ActivityEntry {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatalf("ActivityEntries: %v", err)
	}
	return entries
}

func TestEnrollmentWritesActivityEntriesWithoutTheCode(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	managerScope := managerAdminScope(t, st, now)
	service := deterministicService(st, now)

	if _, err := service.CreateCode(context.Background(), managerScope, CodeRequest{
		DeviceRole: string(store.DeviceRoleViewer),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true}); err != nil {
		t.Fatal(err)
	}

	entries := activityEntries(t, st)
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (code mint + enroll)", len(entries))
	}
	enroll, mint := entries[0], entries[1]
	if mint.Action != store.ActivityPairingCode || mint.SessionID != "session-manager" ||
		mint.TargetType != "profile" || mint.TargetID != "profile-default" {
		t.Fatalf("mint entry = %+v", mint)
	}
	if enroll.Action != store.ActivityEnroll || enroll.SessionID != "session-new" ||
		enroll.DeviceID != "device-new" || enroll.TargetID != "device-new" {
		t.Fatalf("enroll entry = %+v", enroll)
	}
	// The code is a credential: it must appear in no entry field.
	for _, entry := range entries {
		for _, field := range []string{entry.Action, string(entry.SessionID), string(entry.DeviceID),
			string(entry.ProfileID), entry.TargetType, entry.TargetID, string(entry.LibraryID)} {
			if field == "PAIR1234" {
				t.Fatalf("pairing code leaked into activity entry: %+v", entry)
			}
		}
	}
}

func TestFailedEnrollmentLogsDeniedEntryWithoutTheCode(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)

	if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "WRONG999", DeviceName: "TV", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Enroll error = %v, want ErrInvalidCode", err)
	}

	entries := activityEntries(t, st)
	if len(entries) != 1 || entries[0].Action != store.ActivityEnrollDenied {
		t.Fatalf("entries = %+v, want one enroll.denied", entries)
	}
	denied := entries[0]
	if denied.TargetID != "" || denied.SessionID != "" || denied.DeviceID != "" {
		t.Fatalf("denied entry carries identifiers (or the code): %+v", denied)
	}
}

func TestPromotionWritesActivityEntries(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	managerScope := managerAdminScope(t, st, now)
	viewerScope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	service := deterministicService(st, now)

	// Denied first: wrong code.
	if _, err := service.Promote(context.Background(), viewerScope, PromoteRequest{Code: "WRONG999"}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Promote error = %v, want ErrInvalidCode", err)
	}
	if _, err := service.CreateCode(context.Background(), managerScope, CodeRequest{
		DeviceRole: string(store.DeviceRoleManager),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Promote(context.Background(), viewerScope, PromoteRequest{Code: "PAIR1234"}); err != nil {
		t.Fatal(err)
	}

	entries := activityEntries(t, st)
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (denied + mint + promote)", len(entries))
	}
	promote := entries[0]
	if promote.Action != store.ActivityPromote || promote.DeviceID != "device-tv" ||
		promote.TargetType != "device" || promote.TargetID != "device-tv" {
		t.Fatalf("promote entry = %+v", promote)
	}
	denied := entries[2]
	if denied.Action != store.ActivityPromoteDenied || denied.SessionID != "session-default" {
		t.Fatalf("denied entry = %+v", denied)
	}
}
