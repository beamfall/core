package playback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// incapableCaps cannot direct-play the seeded mp4/h264 media, forcing the
// decision to the policy-gated transcode rung.
var incapableCaps = CapabilityProfile{Containers: []string{"mkv"}, DirectCodecs: []string{"av1"}}

func newPolicyTestService(t *testing.T, sessionID store.SessionID) (*Service, *store.Store, auth.Scope) {
	t.Helper()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), "media-alpha", path); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.NewService(st).Scope(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Scope returned error: %v", err)
	}
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	return NewService(st, tokens, func() time.Time { return now }), st, scope
}

func setProfileSettings(t *testing.T, st *store.Store, id store.ProfileID, settings store.ProfileSettings) {
	t.Helper()
	if _, err := st.UpdateProfileSettings(context.Background(), id, settings); err != nil {
		t.Fatalf("UpdateProfileSettings returned error: %v", err)
	}
}

func TestInfoRefusesTranscodeWhenProfileDisallowsIt(t *testing.T) {
	service, st, scope := newPolicyTestService(t, "session-default")
	transcode := false
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &transcode})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" {
		t.Fatalf("Decision = %q, want unavailable", resp.Decision)
	}
	if resp.Reason != "transcode-disallowed" {
		t.Fatalf("Reason = %q, want transcode-disallowed", resp.Reason)
	}
	if len(resp.Streams) != 0 {
		t.Fatalf("len(Streams) = %d, want 0 (no transcode rung offered)", len(resp.Streams))
	}
}

func TestInfoStillOffersDirectPlayWhenTranscodeDisallowed(t *testing.T) {
	service, st, scope := newPolicyTestService(t, "session-default")
	transcode := false
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &transcode})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
}

// startTranscode starts a fake HLS transcode under the given session key so
// the stream-cap counter sees it as active.
func startTranscode(t *testing.T, sessions *Sessions, key SessionKey) {
	t.Helper()
	if _, err := sessions.FilePath(context.Background(), key, "/src/media.bin", 120, 1280, 720, false, "720p/seg00000.m4s"); err != nil {
		t.Fatalf("starting fake transcode: %v", err)
	}
}

type countingProfileSessionStore struct {
	calls    int
	sessions []store.MyProfileSession
}

func (s *countingProfileSessionStore) MyProfileSessions(context.Context, store.ProfileID) ([]store.MyProfileSession, error) {
	s.calls++
	return s.sessions, nil
}

func TestInfoBatchesActiveProfileSessionLookup(t *testing.T) {
	for _, activeCount := range []int{1, 12} {
		t.Run(fmt.Sprintf("active-%d", activeCount), func(t *testing.T) {
			service, st, scope := newPolicyTestService(t, "session-default")
			limit := 1
			setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{MaxConcurrentStreams: &limit})

			sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
			defer sessions.Close()
			sessions.SetTranscodeCap(32)
			service.SetSessions(sessions)

			lookup := &countingProfileSessionStore{}
			service.profileSessions = lookup
			for i := range activeCount {
				sessionID := store.SessionID(fmt.Sprintf("session-batch-%02d", i))
				lookup.sessions = append(lookup.sessions, store.MyProfileSession{ID: sessionID})
				startTranscode(t, sessions, SessionKey{SessionID: sessionID, MediaID: store.MediaID(fmt.Sprintf("media-%02d", i))})
			}

			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           "media-alpha",
				CapabilityProfile: incapableCaps,
			})
			if err != nil {
				t.Fatalf("Info returned error: %v", err)
			}
			if resp.Reason != "stream-limit-reached" {
				t.Fatalf("Reason = %q, want stream-limit-reached", resp.Reason)
			}
			if lookup.calls != 1 {
				t.Fatalf("profile session queries = %d, want 1 for %d active sessions", lookup.calls, activeCount)
			}
		})
	}
}

func TestInfoRefusesTranscodePastProfileStreamCap(t *testing.T) {
	service, st, scope := newPolicyTestService(t, "session-default")
	limit := 1
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{MaxConcurrentStreams: &limit})
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)
	// session-revealed shares profile-default: its running transcode counts
	// against the same Profile cap.
	startTranscode(t, sessions, SessionKey{SessionID: "session-revealed", MediaID: "media-alpha"})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" {
		t.Fatalf("Decision = %q, want unavailable", resp.Decision)
	}
	if resp.Reason != "stream-limit-reached" {
		t.Fatalf("Reason = %q, want stream-limit-reached", resp.Reason)
	}
}

func TestInfoStreamCapIgnoresOwnTranscodeOfSameMedia(t *testing.T) {
	service, st, scope := newPolicyTestService(t, "session-default")
	limit := 1
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{MaxConcurrentStreams: &limit})
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)
	// The requesting session's own transcode of the same Media would be
	// reused, not forked — it never blocks its own playbackInfo refresh.
	startTranscode(t, sessions, SessionKey{SessionID: "session-default", MediaID: "media-alpha"})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode (own session reuse)", resp.Decision)
	}
}

func TestInfoStreamCapIgnoresOtherProfiles(t *testing.T) {
	service, st, scope := newPolicyTestService(t, "session-default")
	limit := 1
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{MaxConcurrentStreams: &limit})
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)
	// session-managed belongs to profile-managed: its transcode never counts
	// against profile-default's cap.
	startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-alpha"})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode (other profile's stream)", resp.Decision)
	}
}

func TestInfoCarriesProfileTrackPreferences(t *testing.T) {
	service, st, scope := newPolicyTestService(t, "session-default")
	audio := []string{"fr-CA", "fr", "en"}
	subs := []string{"fr"}
	mode := store.SubtitleModeSmart
	sdh := true
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{
		AudioLanguages:    &audio,
		SubtitleLanguages: &subs,
		SubtitleMode:      &mode,
		PreferSDH:         &sdh,
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	prefs := resp.TrackPreferences
	if len(prefs.AudioLanguages) != 3 || prefs.AudioLanguages[0] != "fr-CA" {
		t.Fatalf("AudioLanguages = %#v, want ordered [fr-CA fr en]", prefs.AudioLanguages)
	}
	if len(prefs.SubtitleLanguages) != 1 || prefs.SubtitleLanguages[0] != "fr" {
		t.Fatalf("SubtitleLanguages = %#v, want [fr]", prefs.SubtitleLanguages)
	}
	if prefs.SubtitleMode != "smart" || !prefs.PreferSDH {
		t.Fatalf("SubtitleMode = %q PreferSDH = %v, want smart/true", prefs.SubtitleMode, prefs.PreferSDH)
	}
}

func TestInfoTrackPreferencesDefaultToEmptyListsNeverNull(t *testing.T) {
	service, _, scope := newPolicyTestService(t, "session-default")
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	prefs := resp.TrackPreferences
	if prefs.AudioLanguages == nil || prefs.SubtitleLanguages == nil {
		t.Fatalf("preference lists must serialize as [], got %#v / %#v", prefs.AudioLanguages, prefs.SubtitleLanguages)
	}
	if prefs.SubtitleMode != "off" || prefs.PreferSDH {
		t.Fatalf("defaults = %q/%v, want off/false", prefs.SubtitleMode, prefs.PreferSDH)
	}
}

func TestInfoRefusesTranscodeAtServerCapacity(t *testing.T) {
	service, _, scope := newPolicyTestService(t, "session-default")
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(1)
	service.SetSessions(sessions)
	// Another session's transcode holds the only slot and is actively
	// streaming — admission control refuses honestly at playbackInfo time
	// instead of failing after the player commits.
	startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-alpha"})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" {
		t.Fatalf("Decision = %q, want unavailable", resp.Decision)
	}
	if resp.Reason != "transcode-capacity-reached" {
		t.Fatalf("Reason = %q, want transcode-capacity-reached", resp.Reason)
	}
	if len(resp.Streams) != 0 {
		t.Fatalf("len(Streams) = %d, want 0 (no transcode rung offered)", len(resp.Streams))
	}
}

// stubRoster is a PartyRoster that reports a fixed membership verdict.
type stubRoster struct{ member bool }

func (s stubRoster) SessionInWatchPartyForMedia(store.SessionID, store.MediaID) bool {
	return s.member
}

// TestInfoDegradesWatchPartyMemberAtServerCapacity proves the PANEL-16 degrade:
// a Watch-Party member blocked only by the server transcode cap is admitted at
// the single lowest rung (verdict transcode, reason capacity-degraded, one
// rendition, grant.Degraded set) instead of being refused.
func TestInfoDegradesWatchPartyMemberAtServerCapacity(t *testing.T) {
	service, _, scope := newPolicyTestService(t, "session-default")
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(1)
	service.SetSessions(sessions)
	service.SetPartyRoster(stubRoster{member: true})
	startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-alpha"})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" || resp.Reason != "capacity-degraded" {
		t.Fatalf("Decision/Reason = %q/%q, want transcode/capacity-degraded", resp.Decision, resp.Reason)
	}
	if len(resp.Streams) != 1 || len(resp.Streams[0].Renditions) != 1 {
		t.Fatalf("Streams = %#v, want one HLS choice with exactly one (lowest) rung", resp.Streams)
	}
	// The degrade verdict rides the signed grant so the byte layer caps + over-cap
	// admits the lowest rung.
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if !grant.Degraded {
		t.Fatal("grant.Degraded = false, want true on the degraded HLS choice")
	}
}

// TestInfoRefusesNonPartyMemberAtServerCapacity proves the degrade is gated on
// authoritative party membership: a non-member at the same cap still hard-refuses
// (the roster, not a client flag, is the gate).
func TestInfoRefusesNonPartyMemberAtServerCapacity(t *testing.T) {
	service, _, scope := newPolicyTestService(t, "session-default")
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(1)
	service.SetSessions(sessions)
	service.SetPartyRoster(stubRoster{member: false})
	startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-alpha"})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" || resp.Reason != "transcode-capacity-reached" {
		t.Fatalf("Decision/Reason = %q/%q, want unavailable/transcode-capacity-reached", resp.Decision, resp.Reason)
	}
}

// TestInfoDoesNotDegradeWatchPartyMemberPastPolicyGate proves only the server
// encoder-capacity refusal degrades: a Profile that forbids transcoding entirely
// still hard-refuses a party member (a policy gate is never a degrade trigger).
func TestInfoDoesNotDegradeWatchPartyMemberPastPolicyGate(t *testing.T) {
	service, st, scope := newPolicyTestService(t, "session-default")
	transcode := false
	setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &transcode})
	service.SetPartyRoster(stubRoster{member: true})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: incapableCaps,
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" || resp.Reason != "transcode-disallowed" {
		t.Fatalf("Decision/Reason = %q/%q, want unavailable/transcode-disallowed", resp.Decision, resp.Reason)
	}
}

func TestInfoOmitsManualPinWhenTranscodeCannotStart(t *testing.T) {
	service, _, scope := newPolicyTestService(t, "session-default")
	sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	sessions.SetTranscodeCap(1)
	service.SetSessions(sessions)
	startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-alpha"})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           "media-alpha",
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	// Direct play is unaffected by transcode capacity, but the manual
	// rendition pin is omitted — never advertise a stream that cannot start.
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if len(resp.Streams) != 1 || resp.Streams[0].Kind != "direct-play" {
		t.Fatalf("Streams = %#v, want only the direct choice", resp.Streams)
	}
}
