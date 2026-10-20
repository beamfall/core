package streamtoken

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// TestIssueRefusedWhenAttached asserts the ADR-0123 §2 / ADR-0129 §1 authority
// boundary: an Attached Origin Node never issues a Stream Token.
func TestIssueRefusedWhenAttached(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	service.SetRole(serverrole.Attached)

	grant := Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	}
	token, err := service.Issue(context.Background(), grant)
	if err == nil {
		t.Fatal("attached Issue returned a token; it must refuse")
	}
	if token != "" {
		t.Fatalf("attached Issue returned a non-empty token %q", token)
	}
	if !errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("attached Issue error %v does not match the authority sentinel", err)
	}
}

// TestIssueAllowedWhenMain confirms the default (Main) role still issues — the
// single-server operator story is unchanged.
func TestIssueAllowedWhenMain(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	service.SetRole(serverrole.Main)

	if _, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   "media-alpha",
		DeviceID:  "device-tv",
		SessionID: "session-default",
		ProfileID: "profile-default",
		ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("main Issue returned error: %v", err)
	}
}
