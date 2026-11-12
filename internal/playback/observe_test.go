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
