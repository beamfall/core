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
	WantDecision Decision       `json:"wantDecision"`
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

// TestDolbyVisionFormatByBaseLayerCompatibility pins the per-profile Dolby Vision
// token for each base-layer compatibility ID (CORE-2157, ADR-0017 §4). ID 1 is
// the HDR10 base of streaming profile 8.1 and 6 the Ultra HD Blu-ray HDR10 base;
// both carry the advertisable profile token. 2 (SDR base) and 4 (HLG base) are
// not HDR10, so they take the un-advertised generic sentinel.
func TestDolbyVisionFormatByBaseLayerCompatibility(t *testing.T) {
	cases := []struct {
		name       string
		profile    int
		blCompatID int
		want       string
	}{
		{"P8.1 HDR10 base", 8, 1, hdrDolbyVisionP8},
		{"P8.2 SDR base", 8, 2, hdrDolbyVisionGeneric},
		{"P8.4 HLG base", 8, 4, hdrDolbyVisionGeneric},
		{"P8 Blu-ray HDR10 base", 8, 6, hdrDolbyVisionP8},
		{"P8 no base compatibility", 8, 0, hdrDolbyVisionGeneric},
		{"P7 HDR10 base", 7, 1, hdrDolbyVisionP7},
		{"P7 Blu-ray HDR10 base", 7, 6, hdrDolbyVisionP7},
		{"P7 SDR base", 7, 2, hdrDolbyVisionGeneric},
		{"P5 no base", 5, 0, hdrDolbyVisionP5},
		{"P4 SDR base", 4, 2, hdrDolbyVisionGeneric},
		{"P9 SDR base", 9, 2, hdrDolbyVisionGeneric},
		{"P10 HDR10 base", 10, 1, hdrDolbyVisionGeneric},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			video := store.MediaFileStream{Type: "video", Codec: "hevc", DoViProfile: tc.profile, DoViBLCompatID: tc.blCompatID, ColorTransfer: "smpte2084"}
			if got := hdrFormat(video); got != tc.want {
				t.Errorf("hdrFormat(profile %d, BL compat %d) = %q, want %q", tc.profile, tc.blCompatID, got, tc.want)
			}
		})
	}
}
