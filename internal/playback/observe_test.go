package playback

import (
	"context"
	"testing"
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
