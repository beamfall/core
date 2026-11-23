package pairing

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestApprovalAndRenewForBoundPlayerProfile(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(2_000_000, 0).UTC()
	svc := NewService(st, func() time.Time { return now })

	player, err := st.CreateProfile(ctx, "Kitchen TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}

	// Device requests pairing; admin approves for the Player Profile.
	reqResp, err := svc.RequestDevicePairing(ctx, DeviceRequest{
		DeviceName: "Kitchen Apple TV",
		RemoteAddr: "192.168.1.50:4000",
		IsLAN:      true,
	})
	if err != nil {
		t.Fatalf("RequestDevicePairing: %v", err)
	}
	adminScope := auth.Scope{
		SessionID: "session-manager", DeviceID: "device-mac",
		ProfileID: "profile-default", Profile: store.ProfileAdult,
		DeviceRole: store.DeviceRoleManager, Admin: true,
	}
	if _, err := svc.ApproveDeviceRequest(ctx, adminScope, DeviceRequestDecision{
		Code:              reqResp.Code,
		ProfileID:         string(player.ID),
		PlayerDisplayName: "Kitchen Display",
	}); err != nil {
		t.Fatalf("ApproveDeviceRequest: %v", err)
	}
	pollResp, err := svc.PollDevicePairing(ctx, DevicePollRequest{
		RequestSecret: reqResp.RequestSecret,
		RemoteAddr:    "192.168.1.50:4000",
		IsLAN:         true,
	})
	if err != nil || pollResp.Enrollment == nil {
		t.Fatalf("PollDevicePairing = %+v, %v", pollResp, err)
	}
	if pollResp.Enrollment.ProfileID != string(player.ID) {
		t.Fatalf("enrolled ProfileID = %q, want %q", pollResp.Enrollment.ProfileID, player.ID)
	}
	dev, err := st.Device(ctx, store.DeviceID(pollResp.Enrollment.DeviceID))
	if err != nil {
		t.Fatal(err)
	}
	if dev.PlayerProfileID != player.ID || dev.PlayerDisplayName != "Kitchen Display" || dev.PlayerBindingGeneration != 1 {
		t.Fatalf("enrolled device = %+v", dev)
	}

	// RenewSession issues the renewed session for the bound Player Profile only.
	renewed, err := svc.RenewSession(ctx, RenewRequest{
		DeviceID:     pollResp.Enrollment.DeviceID,
		DeviceSecret: pollResp.Enrollment.DeviceSecret,
		RemoteAddr:   "192.168.1.50:4000",
	})
	if err != nil {
		t.Fatalf("RenewSession: %v", err)
	}
	if renewed.ProfileID != string(player.ID) {
		t.Fatalf("renewed ProfileID = %q, want %q", renewed.ProfileID, player.ID)
	}

	// Re-pair / Promote keeps the bound Player Profile.
	code, err := svc.CreateCode(ctx, adminScope, CodeRequest{
		ProfileID:  "profile-default",
		DeviceRole: string(store.DeviceRoleManager),
	})
	if err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
	promoted, err := svc.Promote(ctx, auth.Scope{
		SessionID: store.SessionID(renewed.SessionID),
		DeviceID:  dev.ID,
		ProfileID: player.ID,
	}, PromoteRequest{Code: code.Code, RemoteAddr: "192.168.1.50:4000"})
	if err != nil {
		t.Fatalf("Promote(re-pair): %v", err)
	}
	sess, err := st.Session(ctx, store.SessionID(promoted.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	if sess.ProfileID != player.ID {
		t.Fatalf("re-paired session ProfileID = %q, want %q", sess.ProfileID, player.ID)
	}
}
