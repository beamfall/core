package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestPurgeTrashAdminRevealedConfirmGated(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	if err := st.TrashMedia(ctx, "media-alpha", store.LibraryFilter{AllLibraries: true}); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}

	body := `{"query":"mutation PurgeTrash($before:String!,$confirm:Boolean!){ purgeTrash(before:$before, confirm:$confirm){ affected } }","variables":{"before":"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `","confirm":true}}`
	_, errs := post(t, handler, body, authorizeManager)
	if len(errs) != 1 || errs[0].Message != "admin session required" {
		t.Fatalf("pre-admin purgeTrash errors = %#v, want admin session required", errs)
	}

	openManagerAdminSession(t, st)
	_, errs = post(t, handler, body, authorizeManager)
	if len(errs) != 1 || errs[0].Message != "reveal required to purge trash" {
		t.Fatalf("unrevealed purgeTrash errors = %#v, want reveal gate", errs)
	}

	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-mgr2", Name: "Manager 2", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr2-secret")},
		store.Session{ID: "session-mgr-revealed", DeviceID: "device-mgr2", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	openAdminSession(t, st, "session-mgr-revealed")
	authorizeRevealedManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-revealed")
		req.Header.Set("X-Beamfall-Device", "device-mgr2")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr2-secret")
	}

	noConfirm := `{"query":"mutation PurgeTrash($before:String!,$confirm:Boolean!){ purgeTrash(before:$before, confirm:$confirm){ affected } }","variables":{"before":"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `","confirm":false}}`
	_, errs = post(t, handler, noConfirm, authorizeRevealedManager)
	if len(errs) != 1 || errs[0].Message != "confirm is required to purge trash" {
		t.Fatalf("unconfirmed purgeTrash errors = %#v, want confirm gate", errs)
	}

	data, errs := post(t, handler, body, authorizeRevealedManager)
	if len(errs) != 0 {
		t.Fatalf("purgeTrash errors = %#v", errs)
	}
	var result BulkOpResult
	if err := json.Unmarshal(data["purgeTrash"], &result); err != nil {
		t.Fatalf("decode purgeTrash: %v", err)
	}
	if result.Affected != 1 {
		t.Fatalf("purgeTrash affected = %d, want 1", result.Affected)
	}
	if _, err := st.Media(ctx, "media-alpha", store.LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("purged Media is still readable")
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{AllLibraries: true}, Limit: 10})
	if err != nil {
		t.Fatalf("ActivityEntries: %v", err)
	}
	if len(entries) == 0 || entries[0].Action != store.ActivityTrashPurge {
		t.Fatalf("latest activity = %#v, want %q", entries, store.ActivityTrashPurge)
	}
}

func TestPurgeTrashConfirmedPurgesWhenTrashDisabled(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	handler := newTestHandlerWithStore(st)
	filter := store.LibraryFilter{AllLibraries: true}
	if err := st.TrashMedia(ctx, "media-alpha", filter); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-mgr-disabled", Name: "Manager Disabled", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("mgr-disabled-secret")},
		store.Session{ID: "session-mgr-disabled-revealed", DeviceID: "device-mgr-disabled", ProfileID: "profile-default", Revealed: true},
	); err != nil {
		t.Fatal(err)
	}
	openAdminSession(t, st, "session-mgr-disabled-revealed")
	authorizeRevealedManager := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-mgr-disabled-revealed")
		req.Header.Set("X-Beamfall-Device", "device-mgr-disabled")
		req.Header.Set("X-Beamfall-Device-Secret", "mgr-disabled-secret")
	}

	body := `{"query":"mutation PurgeTrash($before:String!,$confirm:Boolean!){ purgeTrash(before:$before, confirm:$confirm){ affected } }","variables":{"before":"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `","confirm":true}}`
	data, errs := post(t, handler, body, authorizeRevealedManager)
	if len(errs) != 0 {
		t.Fatalf("purgeTrash errors = %#v", errs)
	}
	var result BulkOpResult
	if err := json.Unmarshal(data["purgeTrash"], &result); err != nil {
		t.Fatalf("decode purgeTrash: %v", err)
	}
	if result.Affected != 1 {
		t.Fatalf("purgeTrash affected = %d, want 1", result.Affected)
	}
	if got := st.TrashDisabledSweepSkips(); got != 0 {
		t.Fatalf("TrashDisabledSweepSkips = %d, want 0 for confirmed purge", got)
	}
	if _, err := st.Media(ctx, "media-alpha", filter); err == nil {
		t.Fatal("purged Media is still readable")
	}
}
