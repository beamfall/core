package streamtoken

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

func FuzzUnverifiedGrant(f *testing.F) {
	now := time.Unix(100, 0).UTC()
	service := NewService([]byte("test-key"), func() time.Time { return now })
	valid, err := service.Issue(context.Background(), Grant{
		Target:    TargetMediaDirect,
		MediaID:   store.MediaID("media-alpha"),
		DeviceID:  store.DeviceID("device-tv"),
		SessionID: store.SessionID("session-default"),
		ProfileID: store.ProfileID("profile-default"),
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		f.Fatalf("issue valid token seed: %v", err)
	}

	f.Add(valid)
	f.Add("not-a-token")
	f.Add("aaa.bbb")

	f.Fuzz(func(t *testing.T, token string) {
		grant, err := service.UnverifiedGrant(token, TargetMediaDirect)
		if err != nil {
			return
		}
		if grant.Target != TargetMediaDirect {
			t.Fatalf("target = %q, want %q", grant.Target, TargetMediaDirect)
		}
	})
}
