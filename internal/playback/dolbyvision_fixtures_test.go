package playback

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// dvFixtureVideo is the probed primary-video shape for a fixture case. It mirrors
// the Android probe output (DoViProfile + DoViBLCompatID), so a server-side change
// that misreads either field fails this contract gate.
type dvFixtureVideo struct {
	Profile        string `json:"profile"`
	DoViProfile    int    `json:"doViProfile"`
	DoViBLCompatID int    `json:"doViBLCompatID"`
	ColorTransfer  string `json:"colorTransfer"`
}

type dvFixtureAudio struct {
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
}

type dvFixtureCase struct {
	Name         string         `json:"name"`
	Container    string         `json:"container"`
	Codec        string         `json:"codec"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	Video        dvFixtureVideo `json:"video"`
	Audio        dvFixtureAudio `json:"audio"`
	Profile      string         `json:"profile"`
	WantDecision string         `json:"wantDecision"`
	WantReason   string         `json:"wantReason"`
}

type dvFixtures struct {
	Profiles map[string]CapabilityProfile `json:"profiles"`
	Cases    []dvFixtureCase              `json:"cases"`
}

// TestDolbyVisionPlaybackInfoFixtures drives the cross-repo Dolby Vision decision
// matrix (NGAUDIT-B4, ADR-0017 §4) end-to-end through Info(). The fixture profiles
// assert the closed per-profile HDR vocabulary the Android device-quirks table
// declares verbatim (dolby-vision-p5 / dolby-vision-p8), so any drift between the
// server tokens and that vocabulary fails the gate here instead of silently
// breaking DV direct-play.
func TestDolbyVisionPlaybackInfoFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "dv-playbackinfo-fixtures.json"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var fx dvFixtures
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("fixture corpus has no cases")
	}

	for i, tc := range fx.Cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			profile, ok := fx.Profiles[tc.Profile]
			if !ok {
				t.Fatalf("case %q references unknown profile %q", tc.Name, tc.Profile)
			}
			service, st, scope := capabilityTestService(t)
			fingerprint := "fp-dv-fixture-" + strconv.Itoa(i)
			streams := []store.MediaFileStream{
				{
					Index:          0,
					Type:           "video",
					Codec:          tc.Codec,
					Profile:        tc.Video.Profile,
					DoViProfile:    tc.Video.DoViProfile,
					DoViBLCompatID: tc.Video.DoViBLCompatID,
					ColorTransfer:  tc.Video.ColorTransfer,
				},
				{Index: 1, Type: "audio", Codec: tc.Audio.Codec, Channels: tc.Audio.Channels},
			}
			mediaID := importProbedMedia(t, st, fingerprint, tc.Container, tc.Codec, tc.Width, tc.Height, streams)

			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: profile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != tc.WantDecision {
				t.Errorf("Decision = %q, want %q", resp.Decision, tc.WantDecision)
			}
			if resp.Reason != tc.WantReason {
				t.Errorf("Reason = %q, want %q", resp.Reason, tc.WantReason)
			}
		})
	}
}
