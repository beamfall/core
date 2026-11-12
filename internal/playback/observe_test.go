package playback

import (
	"context"
	"testing"
	"time"
)

func TestInfoRecordsPlayObservationForLiveSessions(t *testing.T) {
	cases := []struct {
		name       string
		profile    CapabilityProfile
		wantMethod string
		wantReason string
	}{
		{
			name:       "direct play",
			profile:    CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
			wantMethod: PlayMethodDirect,
		},
		{
			name:       "container refused",
			profile:    CapabilityProfile{Containers: []string{"mkv"}, DirectCodecs: []string{"h264"}},
			wantMethod: PlayMethodTranscode,
			wantReason: PlayReasonContainer,
		},
		{
			name:       "video codec refused",
			profile:    CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"av1"}},
			wantMethod: PlayMethodTranscode,
			wantReason: PlayReasonVideoCodec,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
			if _, ok := service.PlayObservation(scope.SessionID, "media-alpha"); ok {
				t.Fatal("observation present before any decision")
			}
			if _, err := service.Info(context.Background(), scope, InfoRequest{MediaID: "media-alpha", CapabilityProfile: tc.profile}); err != nil {
				t.Fatalf("Info returned error: %v", err)
			}
			obs, ok := service.PlayObservation(scope.SessionID, "media-alpha")
			if !ok {
				t.Fatal("no observation after Info")
			}
			if obs.Method != tc.wantMethod || obs.Reason != tc.wantReason {
				t.Fatalf("observation = %q/%q, want %q/%q", obs.Method, obs.Reason, tc.wantMethod, tc.wantReason)
			}
			if obs.Method == PlayMethodTranscode && obs.Bitrate <= 0 {
				t.Fatalf("transcode bitrate = %d, want the top rendition bandwidth", obs.Bitrate)
			}
		})
	}
}

// A viewer offered direct play who picks a lower quality is on a transcode for
// as long as the session lasts, including after the encoder has finished the
// item and exited.
func TestPlayObservationKeepsTheChosenQualityAfterTheEncoderFinishes(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	direct := CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}}
	if _, err := service.Info(context.Background(), scope, InfoRequest{MediaID: "media-alpha", CapabilityProfile: direct}); err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	source, _ := service.PlayObservation(scope.SessionID, "media-alpha")
	if source.Method != PlayMethodDirect {
		t.Fatalf("method before a quality choice = %q, want direct", source.Method)
	}

	rung := Rung{ID: "low", Bandwidth: 1_500_000}
	session := &hlsSession{rungs: []Rung{rung}}
	service.sessions = NewSessionsInDir(nil, t.TempDir(), time.Now)
	service.sessions.sessions[SessionKey{SessionID: scope.SessionID, MediaID: "media-alpha"}] = session

	if obs, _ := service.PlayObservation(scope.SessionID, "media-alpha"); obs.Method != PlayMethodDirect {
		t.Fatalf("method with a session that never started an encoder = %q, want direct", obs.Method)
	}

	// The encoder starts, then finishes and is reaped.
	session.started, session.currentRung, session.deliveredRung = true, rung.ID, rung.ID
	session.currentRung = ""
	obs, ok := service.PlayObservation(scope.SessionID, "media-alpha")
	if !ok || obs.Method != PlayMethodTranscode || obs.Reason != PlayReasonQualityChoice || obs.Bitrate != rung.Bandwidth {
		t.Fatalf("observation after the encoder finished = %#v, want a quality-choice transcode at the chosen rendition", obs)
	}

	// Ending the session returns the reading to the recorded decision.
	delete(service.sessions.sessions, SessionKey{SessionID: scope.SessionID, MediaID: "media-alpha"})
	if obs, _ := service.PlayObservation(scope.SessionID, "media-alpha"); obs.Method != PlayMethodDirect || obs.Bitrate != source.Bitrate {
		t.Fatalf("observation after the session ended = %#v, want the direct source reading", obs)
	}
}

func TestPlayObservationFollowsTheRouteReadMostRecently(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	direct := CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}}
	if _, err := service.Info(context.Background(), scope, InfoRequest{MediaID: "media-alpha", CapabilityProfile: direct}); err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	start := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC)
	rungs := []Rung{{ID: "high", Bandwidth: 8_000_000}, {ID: "low", Bandwidth: 1_500_000}}
	sender := &hlsSession{rungs: rungs, started: true, deliveredRung: "high", lastAccess: start}
	receiver := &hlsSession{rungs: rungs, started: true, deliveredRung: "low", lastAccess: start.Add(time.Minute)}
	service.sessions = NewSessionsInDir(nil, t.TempDir(), time.Now)
	service.sessions.sessions[SessionKey{SessionID: scope.SessionID, MediaID: "media-alpha", ProfileCapability: "sender"}] = sender
	service.sessions.sessions[SessionKey{SessionID: scope.SessionID, MediaID: "media-alpha", ProfileCapability: "receiver"}] = receiver

	// Map order must not decide: read often enough to meet both orders.
	for range 64 {
		if obs, _ := service.PlayObservation(scope.SessionID, "media-alpha"); obs.Bitrate != 1_500_000 {
			t.Fatalf("bitrate on the receiver route = %d, want the receiver's 1500000", obs.Bitrate)
		}
	}
	sender.lastAccess = start.Add(2 * time.Minute)
	if obs, _ := service.PlayObservation(scope.SessionID, "media-alpha"); obs.Bitrate != 8_000_000 {
		t.Fatalf("bitrate back on the sender route = %d, want 8000000", obs.Bitrate)
	}
}

// Copying the video while converting the audio is a transcode on the
// dashboard; repackaging alone is a remux.
func TestPlayObservationTellsAudioConversionFromRemux(t *testing.T) {
	cases := []struct {
		name       string
		override   QuirkOverride
		wantMethod string
	}{
		{"audio converted", QuirkOverride{DenyAudioCodecs: []string{"eac3"}}, PlayMethodTranscode},
		{"repackaged only", QuirkOverride{ForceRemux: true}, PlayMethodRemux},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, QuirkEntry{ID: "tv-x-fixture", Note: "fixture", Selector: QuirkSelector{Client: "tv-x"}, Override: tc.override})
			mediaID := importQuirkMedia(t, st, "fp-quirk-fixture")
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: quirkProfile("gen1", "2.4.0")})
			if err != nil || resp.Decision != "direct-stream" {
				t.Fatalf("Info = %q (err %v), want direct-stream", resp.Decision, err)
			}
			obs, ok := service.PlayObservation(scope.SessionID, mediaID)
			if !ok || obs.Method != tc.wantMethod || obs.Reason != PlayReasonDeviceQuirk {
				t.Fatalf("observation = %#v, want %s because of a device quirk", obs, tc.wantMethod)
			}
		})
	}
}
