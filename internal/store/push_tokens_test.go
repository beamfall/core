package store

import (
	"context"
	"testing"
)

// TestUpsertDevicePushTokenPersistsAndRefreshes covers PUSH-1 acceptance (a):
// the persisting registrar's store write. It asserts the first register inserts
// a row keyed on device, a second register from the same device REPLACES it in
// place (a refreshed token, no duplicate), and the fan-out read returns it.
func TestUpsertDevicePushTokenPersistsAndRefreshes(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.UpsertDevicePushToken(ctx, DevicePushToken{
		DeviceID: "device-tv", ProfileID: "profile-default", Platform: "apns", Token: "tok-1",
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	tokens, err := st.DevicePushTokensForProfile(ctx, "profile-default")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(tokens) != 1 || tokens[0].DeviceID != "device-tv" || tokens[0].Token != "tok-1" || tokens[0].Platform != "apns" {
		t.Fatalf("after insert: %+v", tokens)
	}

	// Refresh: same device, new token. Must replace, not duplicate.
	if err := st.UpsertDevicePushToken(ctx, DevicePushToken{
		DeviceID: "device-tv", ProfileID: "profile-default", Platform: "webpush", Token: "tok-2",
	}); err != nil {
		t.Fatalf("refresh upsert: %v", err)
	}
	tokens, err = st.DevicePushTokensForProfile(ctx, "profile-default")
	if err != nil {
		t.Fatalf("read after refresh: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("refresh duplicated the row: %+v", tokens)
	}
	if tokens[0].Token != "tok-2" || tokens[0].Platform != "webpush" {
		t.Fatalf("refresh did not replace: %+v", tokens[0])
	}
}

// TestDevicePushTokenValidation asserts the required-field guards.
func TestDevicePushTokenValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	cases := []DevicePushToken{
		{ProfileID: "profile-default", Platform: "apns", Token: "t"}, // no device
		{DeviceID: "device-tv", Platform: "apns", Token: "t"},        // no profile
		{DeviceID: "device-tv", ProfileID: "profile-default", Token: "t"},
		{DeviceID: "device-tv", ProfileID: "profile-default", Platform: "apns"},
	}
	for i, c := range cases {
		if err := st.UpsertDevicePushToken(ctx, c); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, c)
		}
	}
}

// TestDevicePushTokensForProfileExcludesRevokedDevice covers PANEL-20: a revoked
// device must not receive the parental-approval push fan-out. Revocation flips
// device.revoked in place (it does not delete the device row), so the
// device_push_token ON DELETE CASCADE never fires; the fan-out read must exclude
// revoked devices explicitly. The seeded store has an active "device-tv" and an
// already-revoked "device-revoked"; both register a token, only the active one
// is returned.
func TestDevicePushTokensForProfileExcludesRevokedDevice(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.UpsertDevicePushToken(ctx, DevicePushToken{
		DeviceID: "device-tv", ProfileID: "profile-default", Platform: "apns", Token: "tok-active",
	}); err != nil {
		t.Fatalf("active upsert: %v", err)
	}
	if err := st.UpsertDevicePushToken(ctx, DevicePushToken{
		DeviceID: "device-revoked", ProfileID: "profile-default", Platform: "apns", Token: "tok-revoked",
	}); err != nil {
		t.Fatalf("revoked upsert: %v", err)
	}

	tokens, err := st.DevicePushTokensForProfile(ctx, "profile-default")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("expected only the active device's token, got %d: %+v", len(tokens), tokens)
	}
	if tokens[0].DeviceID != "device-tv" || tokens[0].Token != "tok-active" {
		t.Fatalf("returned the wrong (revoked?) device: %+v", tokens[0])
	}

	// Revoking the active device must now drop it from the fan-out too.
	if err := st.RevokeDevice(ctx, "device-tv"); err != nil {
		t.Fatalf("revoke active device: %v", err)
	}
	tokens, err = st.DevicePushTokensForProfile(ctx, "profile-default")
	if err != nil {
		t.Fatalf("read after revoke: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("revoked device still in fan-out: %+v", tokens)
	}
}

// TestDevicePushTokensForProfileEmpty returns an empty slice (not nil/error) for
// a Profile with no registered devices.
func TestDevicePushTokensForProfileEmpty(t *testing.T) {
	st := openSeeded(t)
	tokens, err := st.DevicePushTokensForProfile(context.Background(), "profile-default")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("expected empty, got %+v", tokens)
	}
}
