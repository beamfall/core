package playback

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// quirkDB wraps entries in the versioned, CC0 envelope ParseQuirkDB requires.
func quirkDB(entries ...QuirkEntry) QuirkDB {
	return QuirkDB{Version: "test.2026-07-21", License: "CC0-1.0", Entries: entries}
}

func installQuirks(t *testing.T, service *Service, entries ...QuirkEntry) {
	t.Helper()
	if err := service.SetQuirks(quirkDB(entries...)); err != nil {
		t.Fatalf("SetQuirks: %v", err)
	}
}

// quirkClientProfile is the unquirked device envelope every overlay test
// narrows: on its own it direct-plays the fixture below, so any change in
// verdict is attributable to the overlay and nothing else.
var quirkClientProfile = CapabilityProfile{
	Client:      "tv-x",
	Containers:  []string{"mkv"},
	VideoCodecs: []VideoCodecCap{{Codec: "hevc", Profiles: []string{"Main 10"}, MaxHeight: 2160}, {Codec: "h264", MaxHeight: 2160}},
	AudioCodecs: []AudioCodecCap{{Codec: "eac3", MaxChannels: 6}, {Codec: "aac", MaxChannels: 6}},
	HDRFormats:  []string{"hdr10", "hdr10plus", "hlg"},
}

func quirkProfile(tier, firmware string) CapabilityProfile {
	profile := quirkClientProfile
	profile.Tier = tier
	profile.Firmware = firmware
	return profile
}

// importQuirkMedia seeds the 1080p HEVC / E-AC-3 fixture the overlay tests
// narrow. It direct-plays under quirkProfile with no quirk matched.
func importQuirkMedia(t *testing.T, st *store.Store, fingerprint string) store.MediaID {
	return importProbedMedia(t, st, fingerprint, "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	})
}

// denyHEVCForGen1 is the canonical fixture entry: it selects one client, one
// tier, and one closed firmware range, and denies the fixture's video codec.
func denyHEVCForGen1() QuirkEntry {
	return QuirkEntry{
		ID:       "tv-x-gen1-hevc-stutter",
		Note:     "fixture: gen1 firmware 1.0.0–2.9.9 mis-decodes this codec",
		Selector: QuirkSelector{Client: "tv-x", Tiers: []string{"gen1"}, FirmwareMin: "1.0.0", FirmwareMax: "2.9.9"},
		Override: QuirkOverride{DenyVideoCodecs: []string{"hevc"}},
	}
}

// quirkInfo runs one playbackInfo against the standard fixture under the given
// profile and quirk entries.
func quirkInfo(t *testing.T, profile CapabilityProfile, entries ...QuirkEntry) InfoResponse {
	t.Helper()
	service, st, scope := capabilityTestService(t)
	installQuirks(t, service, entries...)
	mediaID := importQuirkMedia(t, st, "fp-quirk-fixture")
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: profile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	return resp
}

// TestQuirkOverlayFlipsDirectPlayToTranscode is the headline case: a matched
// entry denying the source codec moves a direct-play to a transcode, names
// itself in appliedQuirks, and leaves the existing reason vocabulary alone.
func TestQuirkOverlayFlipsDirectPlayToTranscode(t *testing.T) {
	resp := quirkInfo(t, quirkProfile("gen1", "2.4.0"), denyHEVCForGen1())
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode", resp.Decision)
	}
	if resp.Reason != "requires-transcode" {
		t.Fatalf("Reason = %q, want the unchanged requires-transcode", resp.Reason)
	}
	if !slices.Equal(resp.AppliedQuirks, []string{"tv-x-gen1-hevc-stutter"}) {
		t.Fatalf("AppliedQuirks = %v, want the matched entry ID", resp.AppliedQuirks)
	}
	if len(resp.BehaviorFlags) != 0 {
		t.Fatalf("BehaviorFlags = %v, want none (the entry raises no flag)", resp.BehaviorFlags)
	}
}

// TestQuirkOverlayTierScoped proves the "matching tier ONLY" half of the Verify
// clause: the same device on another tier is untouched.
func TestQuirkOverlayTierScoped(t *testing.T) {
	resp := quirkInfo(t, quirkProfile("gen2", "2.4.0"), denyHEVCForGen1())
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play (gen2 is outside the entry's tiers)", resp.Decision)
	}
	if len(resp.AppliedQuirks) != 0 {
		t.Fatalf("AppliedQuirks = %v, want none", resp.AppliedQuirks)
	}
}

// TestQuirkOverlayFirmwareScoped proves the "matching firmware ONLY" half,
// including that both bounds are inclusive.
func TestQuirkOverlayFirmwareScoped(t *testing.T) {
	cases := []struct {
		firmware string
		want     string
	}{
		{"3.0.1", "direct-play"}, // above firmwareMax
		{"0.9.9", "direct-play"}, // below firmwareMin
		{"1.0.0", "transcode"},   // inclusive lower bound
		{"2.9.9", "transcode"},   // inclusive upper bound
		{"2", "transcode"},       // short form compares segment-wise
	}
	for _, tc := range cases {
		t.Run(tc.firmware, func(t *testing.T) {
			resp := quirkInfo(t, quirkProfile("gen1", tc.firmware), denyHEVCForGen1())
			if resp.Decision != tc.want {
				t.Fatalf("Decision = %q, want %q for firmware %q", resp.Decision, tc.want, tc.firmware)
			}
		})
	}
}

// TestQuirkOverlayUnknownFirmwareDoesNotMatchBoundedRange pins the
// affirmative-evidence-only rule: a firmware we cannot read never satisfies a
// bounded range, so an unidentified device is not degraded by a range that
// means "broken up to X, fixed after". The same device DOES match an entry that
// declares no range at all.
func TestQuirkOverlayUnknownFirmwareDoesNotMatchBoundedRange(t *testing.T) {
	unbounded := QuirkEntry{
		ID:       "tv-x-gen1-whole-tier",
		Note:     "fixture: the whole tier, every firmware",
		Selector: QuirkSelector{Client: "tv-x", Tiers: []string{"gen1"}},
		Override: QuirkOverride{DenyVideoCodecs: []string{"hevc"}},
	}
	for _, firmware := range []string{"", "webOS-6", "1.bad", "1..2"} {
		t.Run("bounded/"+firmware, func(t *testing.T) {
			resp := quirkInfo(t, quirkProfile("gen1", firmware), denyHEVCForGen1())
			if resp.Decision != "direct-play" {
				t.Fatalf("Decision = %q, want direct-play (firmware %q is not affirmative evidence)", resp.Decision, firmware)
			}
		})
		t.Run("unbounded/"+firmware, func(t *testing.T) {
			resp := quirkInfo(t, quirkProfile("gen1", firmware), unbounded)
			if resp.Decision != "transcode" {
				t.Fatalf("Decision = %q, want transcode (an unbounded entry covers every firmware)", resp.Decision)
			}
		})
	}
}

// TestQuirkOverlayClientScoped proves a different client identity never matches.
func TestQuirkOverlayClientScoped(t *testing.T) {
	profile := quirkProfile("gen1", "2.4.0")
	profile.Client = "tv-y"
	resp := quirkInfo(t, profile, denyHEVCForGen1())
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play (tv-y is a different client)", resp.Decision)
	}
}

// verdictRank orders the decision ladder. The overlay may only ever move a
// verdict UP this ordering.
func verdictRank(t *testing.T, verdict string) int {
	t.Helper()
	rank := map[string]int{
		"direct-play":           0,
		"precomputed-rendition": 1,
		"direct-stream":         2,
		"transcode":             3,
		"unavailable":           4,
	}
	value, ok := rank[verdict]
	if !ok {
		t.Fatalf("unknown verdict %q", verdict)
	}
	return value
}

// TestQuirkOverlayIsMonotoneRestrictive is the anti-inversion regression test.
// The sharpest hazard in the overlay is implementing a deny as list
// subtraction: the profile gates treat an empty declared list as "not gated",
// so emptying one would silently WIDEN capability. This sweeps the decision
// matrix and asserts the overlaid verdict is never less conservative than the
// un-overlaid one — including at the maximal outcome, unavailable.
func TestQuirkOverlayIsMonotoneRestrictive(t *testing.T) {
	audioOnlyDeny := QuirkEntry{
		ID:       "tv-x-audio-deny",
		Note:     "fixture: this sink mangles the source audio codec",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyAudioCodecs: []string{"eac3"}},
	}
	forceTranscode := QuirkEntry{
		ID:       "tv-x-force-transcode",
		Note:     "fixture: never hand this device the source video",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{ForceTranscode: true},
	}
	cases := []struct {
		name    string
		entries []QuirkEntry
		media   func(*testing.T, *store.Store) store.MediaID
		setup   func(*testing.T, *Service, *store.Store, auth.Scope)
		want    string
	}{
		{
			name:    "denied video codec",
			entries: []QuirkEntry{denyHEVCForGen1()},
			want:    "transcode",
		},
		{
			name:    "denied audio codec selects the video-copy rung",
			entries: []QuirkEntry{audioOnlyDeny},
			want:    "direct-stream",
		},
		{
			name:    "height cap",
			entries: []QuirkEntry{{ID: "tv-x-height", Note: "fixture", Selector: QuirkSelector{Client: "tv-x"}, Override: QuirkOverride{MaxHeight: 720}}},
			want:    "transcode",
		},
		{
			name:    "forced transcode leaves a cached rendition alone",
			entries: []QuirkEntry{forceTranscode},
			media: func(t *testing.T, st *store.Store) store.MediaID {
				mediaID := importProbedMedia(t, st, "fp-quirk-cached", "mkv", "h264", 3840, 2160, []store.MediaFileStream{
					videoStream("h264", "High", 0, ""),
				})
				insertReadyPlaybackRendition(t, st, mediaID, "quirk-cached-720p", 720)
				return mediaID
			},
			want: "precomputed-rendition",
		},
		{
			name:    "quirk-forced transcode the Profile disallows",
			entries: []QuirkEntry{denyHEVCForGen1()},
			setup: func(t *testing.T, _ *Service, st *store.Store, scope auth.Scope) {
				transcode := false
				setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &transcode})
			},
			want: "unavailable",
		},
		{
			name:    "quirk-forced transcode over the per-Profile stream cap",
			entries: []QuirkEntry{denyHEVCForGen1()},
			setup: func(t *testing.T, service *Service, st *store.Store, scope auth.Scope) {
				limit := 1
				setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{MaxConcurrentStreams: &limit})
				sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
				t.Cleanup(sessions.Close)
				service.SetSessions(sessions)
				startTranscode(t, sessions, SessionKey{SessionID: scope.SessionID, MediaID: "media-other"})
			},
			want: "unavailable",
		},
		{
			name:    "quirk-forced transcode at server encoder capacity",
			entries: []QuirkEntry{denyHEVCForGen1()},
			setup: func(t *testing.T, service *Service, _ *store.Store, scope auth.Scope) {
				sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
				t.Cleanup(sessions.Close)
				sessions.SetTranscodeCap(1)
				service.SetSessions(sessions)
				startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-other"})
			},
			want: "unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := quirkProfile("gen1", "2.4.0")
			run := func(entries []QuirkEntry) InfoResponse {
				service, st, scope := capabilityTestService(t)
				installQuirks(t, service, entries...)
				seed := importQuirkMedia
				if tc.media != nil {
					seed = func(t *testing.T, st *store.Store, _ string) store.MediaID { return tc.media(t, st) }
				}
				mediaID := seed(t, st, "fp-quirk-monotone")
				if tc.setup != nil {
					tc.setup(t, service, st, scope)
				}
				resp, err := service.Info(context.Background(), scope, InfoRequest{
					MediaID:           string(mediaID),
					CapabilityProfile: profile,
				})
				if err != nil {
					t.Fatalf("Info: %v", err)
				}
				return resp
			}
			base := run(nil)
			overlaid := run(tc.entries)
			if overlaid.Decision != tc.want {
				t.Fatalf("overlaid Decision = %q, want %q (base was %q)", overlaid.Decision, tc.want, base.Decision)
			}
			if verdictRank(t, overlaid.Decision) < verdictRank(t, base.Decision) {
				t.Fatalf("overlay WIDENED the verdict: %q -> %q", base.Decision, overlaid.Decision)
			}
		})
	}
}

// TestQuirkOverlayDeniedAudioSelectsAudioCopy proves the veto has two faces: an
// audio-only deny selects the cheap video-copy rung rather than killing it.
func TestQuirkOverlayDeniedAudioSelectsAudioCopy(t *testing.T) {
	resp := quirkInfo(t, quirkProfile("gen1", "2.4.0"), QuirkEntry{
		ID:       "tv-x-audio-deny",
		Note:     "fixture",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyAudioCodecs: []string{"eac3"}},
	})
	if resp.Decision != "direct-stream" || resp.Reason != "audio-transcode-only" {
		t.Fatalf("Decision/Reason = %q/%q, want direct-stream/audio-transcode-only", resp.Decision, resp.Reason)
	}
}

// TestQuirkForceRemuxSelectsVideoCopy pins the forced-remux rung and its one
// new reason value. Reporting "audio-transcode-only" here would claim an audio
// transcode that is not happening.
func TestQuirkForceRemuxSelectsVideoCopy(t *testing.T) {
	remux := QuirkEntry{
		ID:       "tv-x-container-remux",
		Note:     "fixture: this device mishandles the source container",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{ForceRemux: true},
	}
	resp := quirkInfo(t, quirkProfile("gen1", "2.4.0"), remux)
	if resp.Decision != "direct-stream" || resp.Reason != "container-remux" {
		t.Fatalf("Decision/Reason = %q/%q, want direct-stream/container-remux", resp.Decision, resp.Reason)
	}
	if !slices.Equal(resp.AppliedQuirks, []string{"tv-x-container-remux"}) {
		t.Fatalf("AppliedQuirks = %v, want the forced-remux entry", resp.AppliedQuirks)
	}

	// An audio-deny-driven copy still reports the pre-existing reason.
	withAudioDeny := remux
	withAudioDeny.Override.DenyAudioCodecs = []string{"eac3"}
	resp = quirkInfo(t, quirkProfile("gen1", "2.4.0"), withAudioDeny)
	if resp.Decision != "direct-stream" || resp.Reason != "audio-transcode-only" {
		t.Fatalf("Decision/Reason = %q/%q, want direct-stream/audio-transcode-only", resp.Decision, resp.Reason)
	}
}

// TestQuirkUnverifiableSourceFactRestricts pins the "unverifiable ⇒ restrict"
// rule, one case per enforceable override — and, crucially, pins that the base
// profile's deliberate leniency toward probe-less files is UNTOUCHED: with no
// quirk matched every one of these files still direct-plays.
func TestQuirkUnverifiableSourceFactRestricts(t *testing.T) {
	// The audio cases use a profile with no declared audio envelope, which is
	// exactly the leniency the quirk path must not inherit: audioDirectPlayable
	// returns true there and skips absent inventory entirely.
	noAudioEnvelope := quirkClientProfile
	noAudioEnvelope.AudioCodecs = nil

	cases := []struct {
		name     string
		profile  CapabilityProfile
		streams  []store.MediaFileStream
		height   int
		override QuirkOverride
		want     string
	}{
		{
			name:     "bitrate cap with unknown size",
			streams:  []store.MediaFileStream{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "eac3", 6)},
			height:   1080,
			override: QuirkOverride{MaxBitrate: 8_000_000},
			want:     "transcode",
		},
		{
			name:     "height cap with unknown source height",
			streams:  []store.MediaFileStream{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "eac3", 6)},
			height:   0,
			override: QuirkOverride{MaxHeight: 1080},
			want:     "transcode",
		},
		{
			name:     "HDR deny with no recorded video stream",
			streams:  []store.MediaFileStream{audioStream(1, "eac3", 6)},
			height:   1080,
			override: QuirkOverride{DenyHDRFormats: []string{"hdr10"}},
			want:     "transcode",
		},
		{
			name:     "audio deny with no audio inventory",
			profile:  noAudioEnvelope,
			streams:  []store.MediaFileStream{videoStream("hevc", "Main 10", 0, "")},
			height:   1080,
			override: QuirkOverride{DenyAudioCodecs: []string{"truehd"}},
			want:     "direct-stream",
		},
		{
			name:     "audio deny against an empty stream codec",
			profile:  noAudioEnvelope,
			streams:  []store.MediaFileStream{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "", 6)},
			height:   1080,
			override: QuirkOverride{DenyAudioCodecs: []string{"truehd"}},
			want:     "direct-stream",
		},
		{
			name:     "channel cap against an unrecorded channel count",
			profile:  noAudioEnvelope,
			streams:  []store.MediaFileStream{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "eac3", 0)},
			height:   1080,
			override: QuirkOverride{MaxAudioChannels: 6},
			want:     "direct-stream",
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := tc.profile
			if profile.Client == "" {
				profile = quirkClientProfile
			}
			run := func(entries ...QuirkEntry) string {
				service, st, scope := capabilityTestService(t)
				installQuirks(t, service, entries...)
				mediaID := importProbedMedia(t, st, "fp-unverifiable", "mkv", "hevc", 1920, tc.height, tc.streams)
				resp, err := service.Info(context.Background(), scope, InfoRequest{
					MediaID:           string(mediaID),
					CapabilityProfile: profile,
				})
				if err != nil {
					t.Fatalf("Info: %v", err)
				}
				return resp.Decision
			}
			if base := run(); base != "direct-play" {
				t.Fatalf("unquirked Decision = %q, want direct-play — the base profile's leniency must be untouched", base)
			}
			entry := QuirkEntry{
				ID:       "tv-x-unverifiable",
				Note:     "fixture",
				Selector: QuirkSelector{Client: profile.Client},
				Override: tc.override,
			}
			if got := run(entry); got != tc.want {
				t.Fatalf("case %d quirked Decision = %q, want %q", i, got, tc.want)
			}
		})
	}
}

// TestQuirkHDRDenyLeavesSDRDirectPlayable guards the regression that would push
// every ordinary SDR file off direct-play on any device carrying any HDR deny:
// an empty hdrFormat on a RECORDED stream is affirmative evidence of SDR, not
// missing evidence.
func TestQuirkHDRDenyLeavesSDRDirectPlayable(t *testing.T) {
	deny := QuirkEntry{
		ID:       "tv-x-hdr10-tonemap",
		Note:     "fixture: this panel tone-maps HDR10 badly",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyHDRFormats: []string{"hdr10"}},
	}
	cases := []struct {
		name    string
		streams []store.MediaFileStream
		want    string
	}{
		{"SDR still direct-plays", []store.MediaFileStream{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "eac3", 6)}, "direct-play"},
		{"real HDR10 transcodes", []store.MediaFileStream{videoStream("hevc", "Main 10", 0, "smpte2084"), audioStream(1, "eac3", 6)}, "transcode"},
		{"no video stream row transcodes", []store.MediaFileStream{audioStream(1, "eac3", 6)}, "transcode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, deny)
			mediaID := importProbedMedia(t, st, "fp-hdr-deny", "mkv", "hevc", 1920, 1080, tc.streams)
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: quirkClientProfile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != tc.want {
				t.Fatalf("Decision = %q, want %q", resp.Decision, tc.want)
			}
		})
	}
}

// TestQuirkForceRemuxOnHDRSourceFallsToTranscode proves a quirk cannot override
// a different ADR's correctness rule: ADR-0010 §2 forbids a remux that would
// drop HDR10+ or Dolby Vision metadata, so forceRemux degrades to the full
// transcode instead. The Dolby Vision profile 5 case is narrower still — this
// build has no software transcode path for it, so the refusal
// fires before the transcode rung and the verdict stays unavailable.
func TestQuirkForceRemuxOnHDRSourceFallsToTranscode(t *testing.T) {
	remux := QuirkEntry{
		ID:       "tv-x-container-remux",
		Note:     "fixture",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{ForceRemux: true},
	}
	dolbyVision := func(profileID int) store.MediaFileStream {
		return store.MediaFileStream{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", DoViProfile: profileID, DoViBLCompatID: doViBLCompatHDR10}
	}
	cases := []struct {
		name    string
		streams []store.MediaFileStream
		want    string
	}{
		{"HDR10+ source", []store.MediaFileStream{hdr10PlusVideoStream("hevc", "Main 10"), audioStream(1, "eac3", 6)}, "transcode"},
		{"Dolby Vision profile 7 source", []store.MediaFileStream{dolbyVision(7), audioStream(1, "eac3", 6)}, "transcode"},
		{"no recorded video stream", []store.MediaFileStream{audioStream(1, "eac3", 6)}, "transcode"},
		{"Dolby Vision profile 5 source", []store.MediaFileStream{dolbyVision(5), audioStream(1, "eac3", 6)}, "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, remux)
			mediaID := importProbedMedia(t, st, "fp-remux-hdr", "mkv", "hevc", 1920, 1080, tc.streams)
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: quirkClientProfile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != tc.want {
				t.Fatalf("Decision = %q (reason %q), want %q", resp.Decision, resp.Reason, tc.want)
			}
		})
	}
}

// TestQuirkDeniedCodecExcludedFromTranscodeTarget proves the deny leaves the
// transcode TARGET, not only source passthrough: otherwise the transcode that
// replaced direct-play would re-emit exactly the codec the veto refused.
func TestQuirkDeniedCodecExcludedFromTranscodeTarget(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	sessions := NewSessions(&hevcCapableTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)
	installQuirks(t, service, QuirkEntry{
		ID:       "tv-x-no-hevc",
		Note:     "fixture: this decoder mis-handles HEVC entirely",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyVideoCodecs: []string{"hevc"}},
	})
	mediaID := importProbedMedia(t, st, "fp-hevc-target", "mkv", "hevc", 3840, 2160, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "aac", 2),
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: quirkClientProfile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode", resp.Decision)
	}
	for _, rendition := range resp.Streams[0].Renditions {
		if strings.Contains(rendition.ID, "hevc") {
			t.Fatalf("advertised rung %q targets the denied codec", rendition.ID)
		}
	}
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS grant did not verify: %v", err)
	}
	if LadderCapsFromGrant(grant).HEVC {
		t.Fatal("grant still permits an HEVC target, so the byte layer could re-emit the denied codec")
	}
}

// TestQuirkOverlayCapsTranscodeLadder proves a height cap reaches rung
// selection — including for a legacy profile whose VideoCodecs envelope is
// empty and therefore has nowhere to store a ceiling.
func TestQuirkOverlayCapsTranscodeLadder(t *testing.T) {
	cap1080 := QuirkEntry{
		ID:       "tv-x-1080-ceiling",
		Note:     "fixture: 4K decode is unstable on this panel",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{MaxHeight: 1080},
	}
	legacy := CapabilityProfile{Client: "tv-x", Containers: []string{"mkv"}, DirectCodecs: []string{"hevc"}}
	for _, profile := range []CapabilityProfile{quirkClientProfile, legacy} {
		t.Run(profile.Client+"/"+map[bool]string{true: "legacy", false: "rich"}[len(profile.VideoCodecs) == 0], func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, cap1080)
			mediaID := importProbedMedia(t, st, "fp-ladder-cap", "mkv", "hevc", 3840, 2160, []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, ""),
				audioStream(1, "eac3", 6),
			})
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: profile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != "transcode" {
				t.Fatalf("Decision = %q, want transcode", resp.Decision)
			}
			for _, rendition := range resp.Streams[0].Renditions {
				if rendition.Height > 1080 {
					t.Fatalf("advertised rung %q at %dp exceeds the quirk ceiling", rendition.ID, rendition.Height)
				}
			}
		})
	}
}

// TestQuirkBitrateCapClipsLadder proves a bitrate cap resolves to a rung
// ceiling, and that a sub-480 height cap is ENFORCED via the ladder's
// synthesized rung rather than rejected at load.
func TestQuirkBitrateCapClipsLadder(t *testing.T) {
	t.Run("bitrate cap between rungs", func(t *testing.T) {
		resp := ladderCapResponse(t, 3840, 2160, QuirkOverride{MaxBitrate: 6_000_000})
		for _, rendition := range resp.Streams[0].Renditions {
			if rendition.Height > 720 {
				t.Fatalf("advertised rung %q at %dp exceeds the 720p-equivalent bitrate cap", rendition.ID, rendition.Height)
			}
		}
	})
	t.Run("sub-480 height cap is synthesized, not rejected", func(t *testing.T) {
		if _, err := ParseQuirkDB([]byte(`{"version":"t","license":"CC0-1.0","entries":[{"id":"tiny","note":"n","selector":{"client":"tv-x"},"override":{"maxHeight":300}}]}`)); err != nil {
			t.Fatalf("a positive sub-480 maxHeight must load: %v", err)
		}
		resp := ladderCapResponse(t, 1920, 1080, QuirkOverride{MaxHeight: 300})
		renditions := resp.Streams[0].Renditions
		if len(renditions) != 1 || renditions[0].Height != 300 || renditions[0].Bandwidth != bandwidthFor(1500) {
			t.Fatalf("renditions = %#v, want one synthesized 300p rung at the lowest rung's bitrate", renditions)
		}
	})
}

func ladderCapResponse(t *testing.T, width, height int, override QuirkOverride) InfoResponse {
	t.Helper()
	service, st, scope := capabilityTestService(t)
	installQuirks(t, service, QuirkEntry{
		ID:       "tv-x-ladder-cap",
		Note:     "fixture",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: override,
	})
	mediaID := importProbedMedia(t, st, "fp-ladder-bitrate", "mkv", "hevc", width, height, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: quirkClientProfile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode", resp.Decision)
	}
	return resp
}

// TestQuirkAudioChannelCapReachesEncoder proves the channel cap reaches the
// encoder rather than stopping at the decision. A cap OF 2 is not a literal
// grant round-trip — withHLSAudioGrant treats 2 as its default sentinel — so
// the assertion is on the EFFECTIVE reconstructed encode.
func TestQuirkAudioChannelCapReachesEncoder(t *testing.T) {
	passthrough := quirkClientProfile
	passthrough.DownstreamAudio = "passthrough"
	passthrough.AudioCodecs = []AudioCodecCap{{Codec: "truehd"}, {Codec: "eac3", MaxChannels: 6}, {Codec: "aac", MaxChannels: 6}}

	for _, cap := range []int{1, 2} {
		t.Run(map[int]string{1: "mono cap", 2: "stereo cap"}[cap], func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, QuirkEntry{
				ID:       "tv-x-avr-channels",
				Note:     "fixture: this AVR mangles multichannel passthrough",
				Selector: QuirkSelector{Client: "tv-x"},
				Override: QuirkOverride{MaxAudioChannels: cap},
			})
			mediaID := importProbedMedia(t, st, "fp-channel-cap", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, ""),
				audioStream(1, "truehd", 8),
			})
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: passthrough,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != "direct-stream" {
				t.Fatalf("Decision = %q, want direct-stream", resp.Decision)
			}
			tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
			grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
			if err != nil {
				t.Fatalf("HLS grant did not verify: %v", err)
			}
			audio := HLSAudioProfileFromGrant(grant)
			if audio.Codec == "copy" {
				t.Fatal("bitstream passthrough survived a channel cap")
			}
			// 0 is the byte layer's stereo default, which IS the capped encode
			// for a cap of 2; a cap of 1 must ride the grant literally.
			if cap == 1 && audio.Channels != 1 {
				t.Fatalf("reconstructed channels = %d, want the literal cap 1", audio.Channels)
			}
			if cap == 2 && audio.Channels != 0 {
				t.Fatalf("reconstructed channels = %d, want the stereo default (0)", audio.Channels)
			}
		})
	}
}

// TestQuirkCapsApplyToUnknownDimensionSource closes the probe-less hole: the
// ladder's unknown-dimension branch used to return an UNCAPPED rung before
// consulting the caps at all. The bound reaches the encoder while the
// advertised rendition omits the height, since the server never measured it.
func TestQuirkCapsApplyToUnknownDimensionSource(t *testing.T) {
	for _, override := range []QuirkOverride{{MaxHeight: 720}, {MaxBitrate: 6_000_000}} {
		t.Run("", func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, QuirkEntry{
				ID:       "tv-x-probeless-cap",
				Note:     "fixture",
				Selector: QuirkSelector{Client: "tv-x"},
				Override: override,
			})
			mediaID := importProbedMedia(t, st, "fp-probeless", "mkv", "hevc", 0, 0, []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, ""),
				audioStream(1, "eac3", 6),
			})
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: quirkClientProfile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != "transcode" {
				t.Fatalf("Decision = %q, want transcode", resp.Decision)
			}
			renditions := resp.Streams[0].Renditions
			if len(renditions) != 1 || renditions[0].Height != 0 {
				t.Fatalf("renditions = %#v, want one rung claiming no measured height", renditions)
			}
			tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
			grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
			if err != nil {
				t.Fatalf("HLS grant did not verify: %v", err)
			}
			caps := LadderCapsFromGrant(grant)
			if caps.ClientMaxHeight != 720 {
				t.Fatalf("grant ClientMaxHeight = %d, want the 720p ceiling", caps.ClientMaxHeight)
			}
			rungs := rungsForLadder(0, 0, caps)
			if len(rungs) != 1 || rungs[0].Height != 720 {
				t.Fatalf("reconstructed rungs = %#v, want a single 720p-bounded rung", rungs)
			}
		})
	}

	t.Run("no overlay keeps the unbounded source rung", func(t *testing.T) {
		service, st, scope := capabilityTestService(t)
		mediaID := importProbedMedia(t, st, "fp-probeless-base", "mkv", "hevc", 0, 0, []store.MediaFileStream{
			videoStream("hevc", "Main 10", 0, ""),
			audioStream(1, "eac3", 6),
		})
		legacy := CapabilityProfile{Client: "tv-x", Containers: []string{"mp4"}, DirectCodecs: []string{"av1"}}
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:           string(mediaID),
			CapabilityProfile: legacy,
		})
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		renditions := resp.Streams[0].Renditions
		if len(renditions) != 1 || renditions[0].ID != sourceRungID {
			t.Fatalf("renditions = %#v, want the unbounded source rung, byte-identical to before", renditions)
		}
	})

	t.Run("capacity-degraded path suppresses the same fabricated height", func(t *testing.T) {
		service, st, scope := capabilityTestService(t)
		installQuirks(t, service, QuirkEntry{
			ID:       "tv-x-probeless-cap",
			Note:     "fixture",
			Selector: QuirkSelector{Client: "tv-x"},
			Override: QuirkOverride{MaxHeight: 720},
		})
		sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
		defer sessions.Close()
		sessions.SetTranscodeCap(1)
		service.SetSessions(sessions)
		service.SetPartyRoster(stubRoster{member: true})
		startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-other"})

		mediaID := importProbedMedia(t, st, "fp-probeless-degraded", "mkv", "hevc", 0, 0, []store.MediaFileStream{
			videoStream("hevc", "Main 10", 0, ""),
			audioStream(1, "eac3", 6),
		})
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:           string(mediaID),
			CapabilityProfile: quirkClientProfile,
		})
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if resp.Decision != "transcode" || resp.Reason != "capacity-degraded" {
			t.Fatalf("Decision/Reason = %q/%q, want transcode/capacity-degraded", resp.Decision, resp.Reason)
		}
		renditions := resp.Streams[0].Renditions
		if len(renditions) != 1 || renditions[0].Height != 0 {
			t.Fatalf("degraded renditions = %#v, want no fabricated measured height", renditions)
		}
	})
}

// TestQuirkDeniedSubtitleFormatForcesBurnIn proves the subtitle view reaches
// the ISSUED GRANT, not just the predicate: burn-in only reaches the pipeline
// through grant.BurnSubtitleIndex, and a veto cannot set a grant field.
func TestQuirkDeniedSubtitleFormatForcesBurnIn(t *testing.T) {
	profile := quirkClientProfile
	profile.SubtitleFormats = []string{"pgs"}
	streams := []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
		{Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle"},
	}
	selected := 2

	run := func(entries ...QuirkEntry) InfoResponse {
		service, st, scope := capabilityTestService(t)
		installQuirks(t, service, entries...)
		mediaID := importProbedMedia(t, st, "fp-burnin", "mkv", "hevc", 1920, 1080, streams)
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:               string(mediaID),
			CapabilityProfile:     profile,
			SelectedSubtitleIndex: &selected,
		})
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		return resp
	}
	if base := run(); base.Decision != "direct-play" {
		t.Fatalf("unquirked Decision = %q, want direct-play (the engine renders PGS natively)", base.Decision)
	}
	resp := run(QuirkEntry{
		ID:       "tv-x-pgs-burn-in",
		Note:     "fixture: this engine mis-renders PGS overlays",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenySubtitleFormats: []string{"pgs"}},
	})
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode", resp.Decision)
	}
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	grant, err := tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS grant did not verify: %v", err)
	}
	if grant.BurnSubtitleIndex == 0 {
		t.Fatal("grant carries no burn-in index, so the denied subtitle never reaches the pipeline")
	}
}

// TestQuirkOverlayScopedToNegotiatedPrimaryPart documents the deliberate scope:
// the overlay is evaluated on the negotiated Version's PRIMARY Part — the same
// evidence ADR-0010's own decision uses — so it is never weaker than the
// contract it overlays. A heterogeneous multi-Part Version can already
// mis-negotiate against the client's own Capability Profile today, identically
// and for the same reason; closing it needs per-Part grants and byte routes.
func TestQuirkOverlayScopedToNegotiatedPrimaryPart(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	installQuirks(t, service, QuirkEntry{
		ID:       "tv-x-no-truehd",
		Note:     "fixture",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyAudioCodecs: []string{"truehd"}},
	})
	mediaID := importQuirkMedia(t, st, "fp-multipart")
	addVersion(t, st, mediaID, "fp-multipart-1", "", 1, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "truehd", 8),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: quirkClientProfile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play — the overlay judges the primary Part, exactly as ADR-0010 does", resp.Decision)
	}
	if len(resp.Parts) != 2 {
		t.Fatalf("Parts = %#v, want the two-Part Version", resp.Parts)
	}
}

// TestQuirkBaselineFullFunctionOffline proves the shipped default is a working
// default, not an opt-in: a Service built with no setter call loads the
// embedded baseline, reports a version, and leaves every decision unchanged.
func TestQuirkBaselineFullFunctionOffline(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	if service.QuirksVersion() == "" {
		t.Fatal("QuirksVersion is empty — the embedded baseline did not load")
	}
	if ov := service.quirks.Resolve(quirkProfile("gen1", "2.4.0")); !overridesEqual(QuirkOverride{
		DenyVideoCodecs:     ov.DenyVideoCodecs,
		DenyAudioCodecs:     ov.DenyAudioCodecs,
		DenyHDRFormats:      ov.DenyHDRFormats,
		DenySubtitleFormats: ov.DenySubtitleFormats,
		MaxHeight:           ov.MaxHeight,
		MaxBitrate:          ov.MaxBitrate,
		MaxAudioChannels:    ov.MaxAudioChannels,
		ForceRemux:          ov.ForceRemux,
		ForceTranscode:      ov.ForceTranscode,
	}, QuirkOverride{}) || len(ov.flags) != 0 || len(ov.overrideIDs) != 0 {
		t.Fatalf("baseline overlay = %+v, want empty", ov)
	}
	mediaID := importQuirkMedia(t, st, "fp-baseline")
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: quirkProfile("gen1", "2.4.0"),
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" || len(resp.AppliedQuirks) != 0 || len(resp.BehaviorFlags) != 0 {
		t.Fatalf("baseline response = %q/%v/%v, want an untouched direct-play", resp.Decision, resp.AppliedQuirks, resp.BehaviorFlags)
	}
}

// TestQuirkDBRejectsMalformed is the RED table. Every case is a WHOLE-database
// rejection: a half-loaded quirks DB would make the playback decision depend on
// which entries happened to parse.
func TestQuirkDBRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"unknown flag name", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"flags":["seek-restarts-everything"]}]}`},
		{"unknown JSON key", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"override":{"denyEverything":["x"]}}]}`},
		{"duplicate entry ID", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"override":{"forceRemux":true}},{"id":"a","note":"n","selector":{"client":"d"},"override":{"forceRemux":true}}]}`},
		{"empty entry ID", `{"version":"t","license":"CC0-1.0","entries":[{"id":"","note":"n","selector":{"client":"c"},"override":{"forceRemux":true}}]}`},
		{"uppercase entry ID", `{"version":"t","license":"CC0-1.0","entries":[{"id":"TV-X","note":"n","selector":{"client":"c"},"override":{"forceRemux":true}}]}`},
		{"whitespace entry ID", `{"version":"t","license":"CC0-1.0","entries":[{"id":"tv x","note":"n","selector":{"client":"c"},"override":{"forceRemux":true}}]}`},
		{"punctuated entry ID", `{"version":"t","license":"CC0-1.0","entries":[{"id":"tv.x","note":"n","selector":{"client":"c"},"override":{"forceRemux":true}}]}`},
		{"trailing-hyphen entry ID", `{"version":"t","license":"CC0-1.0","entries":[{"id":"tv-","note":"n","selector":{"client":"c"},"override":{"forceRemux":true}}]}`},
		{"empty selector client", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"  "},"override":{"forceRemux":true}}]}`},
		{"downstreamAudio outside the vocabulary", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","downstreamAudio":"atmos"},"override":{"maxAudioChannels":2}}]}`},
		{"non-numeric firmware segment", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","firmwareMin":"1.bad"},"override":{"forceRemux":true}}]}`},
		{"empty firmware segment", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","firmwareMin":"1..2"},"override":{"forceRemux":true}}]}`},
		{"v-prefixed firmware", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","firmwareMax":"v1.2"},"override":{"forceRemux":true}}]}`},
		{"inverted firmware range", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","firmwareMin":"3.0","firmwareMax":"1.0"},"override":{"forceRemux":true}}]}`},
		{"negative cap", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"override":{"maxHeight":-1}}]}`},
		{"maxBitrate below the ladder floor", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"override":{"maxBitrate":900000}}]}`},
		{"missing license", `{"version":"t","entries":[]}`},
		{"wrong license", `{"version":"t","license":"MIT","entries":[]}`},
		{"missing version", `{"license":"CC0-1.0","entries":[]}`},
		{"denies the last-resort audio target", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"override":{"denyAudioCodecs":["aac"]}}]}`},
		{"denies the floor video target", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"override":{"denyVideoCodecs":["h264"]}}]}`},
		{"neither override nor flag", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"}}]}`},
		{"empty selector tier", `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","tiers":[""]},"override":{"forceRemux":true}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseQuirkDB([]byte(tc.json)); err == nil {
				t.Fatal("ParseQuirkDB accepted a malformed database")
			}
		})
	}
}

// TestQuirkDBRejectsUnenforceableOverrides is the RED proof that ADR-0161's
// 2026-07-21 staging is ENFORCED rather than merely documented: an override
// this build cannot carry through to the emitted stream is unrepresentable, and
// a database mixing one valid entry with one such entry loads nothing.
func TestQuirkDBRejectsUnenforceableOverrides(t *testing.T) {
	for _, tc := range []struct{ field, body string }{
		{"denyVideoProfiles", `"denyVideoProfiles":["Main 10"]`},
		{"maxLevel", `"maxLevel":120`},
	} {
		field, body := tc.field, tc.body
		t.Run(field, func(t *testing.T) {
			data := `{"version":"t","license":"CC0-1.0","entries":[` +
				`{"id":"valid-entry","note":"n","selector":{"client":"c"},"override":{"forceRemux":true}},` +
				`{"id":"staged-entry","note":"n","selector":{"client":"c"},"override":{` + body + `}}]}`
			db, err := ParseQuirkDB([]byte(data))
			if err == nil {
				t.Fatal("ParseQuirkDB accepted a staged override family")
			}
			for _, want := range []string{field, "staged-entry"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not name %q", err, want)
				}
			}
			if len(db.Entries) != 0 {
				t.Fatalf("rejected database still loaded %d entries", len(db.Entries))
			}
		})
	}
}

// TestQuirkDBRejectsDownstreamAudioOverreach confines the downstream-audio axis
// to conservative audio policy: the server cannot identify the
// AVR, so such an entry must not reach past audio.
func TestQuirkDBRejectsDownstreamAudioOverreach(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"video deny", `"denyVideoCodecs":["hevc"]`},
		{"HDR deny", `"denyHDRFormats":["hdr10"]`},
		{"subtitle deny", `"denySubtitleFormats":["pgs"]`},
		{"height cap", `"maxHeight":720`},
		{"bitrate cap", `"maxBitrate":6000000`},
		{"force remux", `"forceRemux":true`},
		{"force transcode", `"forceTranscode":true`},
	} {
		name, body := tc.name, tc.body
		t.Run(name, func(t *testing.T) {
			data := `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","downstreamAudio":"passthrough"},"override":{"maxAudioChannels":2,` + body + `}}]}`
			if _, err := ParseQuirkDB([]byte(data)); err == nil {
				t.Fatalf("a downstreamAudio entry carrying %s was accepted", name)
			}
		})
	}
	t.Run("behavior flag", func(t *testing.T) {
		data := `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","downstreamAudio":"stereo"},"override":{"maxAudioChannels":2},"flags":["seek-restarts-session"]}]}`
		if _, err := ParseQuirkDB([]byte(data)); err == nil {
			t.Fatal("a downstreamAudio entry carrying a behavior flag was accepted")
		}
	})
	t.Run("audio-only entry loads", func(t *testing.T) {
		data := `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c","downstreamAudio":"passthrough"},"override":{"maxAudioChannels":2,"denyAudioCodecs":["truehd"]}}]}`
		if _, err := ParseQuirkDB([]byte(data)); err != nil {
			t.Fatalf("a conservative-audio downstreamAudio entry was rejected: %v", err)
		}
	})
}

// TestQuirkDBRejectsUnknownDenyValues proves a typo is a load-time error, not a
// silent no-op that would report itself in appliedQuirks while constraining
// nothing.
func TestQuirkDBRejectsUnknownDenyValues(t *testing.T) {
	cases := []struct {
		name     string
		override string
		wantLoad bool
	}{
		{name: "misspelt HDR format", override: `"denyHDRFormats":["hdr-10"]`},
		{name: "aliased video codec", override: `"denyVideoCodecs":["h265"]`},
		{name: "invented audio codec", override: `"denyAudioCodecs":["e-ac-3-jocx"]`},
		{name: "invented subtitle", override: `"denySubtitleFormats":["subrip2"]`},
		{name: "staged srt", override: `"denySubtitleFormats":["srt"]`},
		{name: "staged webvtt", override: `"denySubtitleFormats":["webvtt"]`},
		{name: "staged ass", override: `"denySubtitleFormats":["ass"]`},
		{name: "empty video token", override: `"denyVideoCodecs":[""]`},
		{name: "empty audio token", override: `"denyAudioCodecs":[""]`},
		{name: "empty HDR token", override: `"denyHDRFormats":[""]`},
		{name: "empty subtitle token", override: `"denySubtitleFormats":[""]`},
		{name: "unknown video codec", override: `"denyVideoCodecs":["theora"]`},
		{name: "unknown audio codec", override: `"denyAudioCodecs":["mystery"]`},
		{name: "bitmap pgs", override: `"denySubtitleFormats":["pgs"]`, wantLoad: true},
		{name: "bitmap vobsub", override: `"denySubtitleFormats":["vobsub"]`, wantLoad: true},
		{name: "bitmap dvbsub", override: `"denySubtitleFormats":["dvbsub"]`, wantLoad: true},
		{name: "canonical hevc", override: `"denyVideoCodecs":["hevc"]`, wantLoad: true},
		{name: "canonical hdr10", override: `"denyHDRFormats":["hdr10"]`, wantLoad: true},
		{name: "canonical truehd", override: `"denyAudioCodecs":["truehd"]`, wantLoad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := `{"version":"t","license":"CC0-1.0","entries":[{"id":"a","note":"n","selector":{"client":"c"},"override":{` + tc.override + `}}]}`
			_, err := ParseQuirkDB([]byte(data))
			if tc.wantLoad && err != nil {
				t.Fatalf("%s was rejected: %v", tc.name, err)
			}
			if !tc.wantLoad && err == nil {
				t.Fatalf("%s loaded instead of erroring", tc.name)
			}
		})
	}
	t.Run("E-AC-3 spellings resolve to one token", func(t *testing.T) {
		for _, spelling := range []string{"EAC-3", "eac3", "ec3", "e-ac-3", "EC-3"} {
			token, ok := canonicalAudioCodec(spelling)
			if !ok || token != "eac3" {
				t.Fatalf("canonicalAudioCodec(%q) = %q/%v, want eac3", spelling, token, ok)
			}
		}
		// The shared canonicalizer is deliberately NOT normalizedAudioCodec,
		// which still yields the distinct "eac-3" for the hyphenated spelling.
		if normalizedAudioCodec("EAC-3") == normalizedAudioCodec("eac3") {
			t.Fatal("normalizedAudioCodec was changed; the base-profile path must keep its current behaviour")
		}
	})
	t.Run("an absent tier never satisfies a declared tiers list", func(t *testing.T) {
		entry := QuirkEntry{
			ID:       "a",
			Note:     "n",
			Selector: QuirkSelector{Client: "tv-x", Tiers: []string{"gen1"}},
			Override: QuirkOverride{ForceTranscode: true},
		}
		db, err := validateQuirkDB(quirkDB(entry))
		if err != nil {
			t.Fatalf("validateQuirkDB: %v", err)
		}
		if ov := db.Resolve(CapabilityProfile{Client: "tv-x"}); ov.ForceTranscode {
			t.Fatal("a profile with no tier matched a declared tiers list")
		}
	})
}

// TestSetQuirksValidatesLikeParse proves there is no unvalidated back door and
// no shared mutable state: the invariants are properties of the DATABASE, so
// they must hold however it arrives — including from tests and from a future
// signed-import path.
func TestSetQuirksValidatesLikeParse(t *testing.T) {
	service, _, _ := capabilityTestService(t)
	baseline := service.QuirksVersion()

	for _, tc := range []struct {
		name     string
		override QuirkOverride
	}{
		{"denies aac", QuirkOverride{DenyAudioCodecs: []string{"aac"}}},
		{"denies h264", QuirkOverride{DenyVideoCodecs: []string{"h264"}}},
		{"declares a staged field", QuirkOverride{DenyVideoProfiles: []string{"Main 10"}}},
	} {
		name, override := tc.name, tc.override
		t.Run(name, func(t *testing.T) {
			err := service.SetQuirks(quirkDB(QuirkEntry{ID: "bad", Note: "n", Selector: QuirkSelector{Client: "c"}, Override: override}))
			if err == nil {
				t.Fatal("SetQuirks installed a database ParseQuirkDB would reject")
			}
			if service.QuirksVersion() != baseline {
				t.Fatal("a rejected SetQuirks replaced the installed database")
			}
		})
	}
	t.Run("malformed ID", func(t *testing.T) {
		if err := service.SetQuirks(quirkDB(QuirkEntry{ID: "Bad ID", Note: "n", Selector: QuirkSelector{Client: "c"}, Override: QuirkOverride{ForceRemux: true}})); err == nil {
			t.Fatal("SetQuirks accepted a malformed entry ID")
		}
	})
	t.Run("installed database is a deep clone", func(t *testing.T) {
		entry := QuirkEntry{
			ID:       "mutable",
			Note:     "n",
			Selector: QuirkSelector{Client: "tv-x"},
			Override: QuirkOverride{DenyVideoCodecs: []string{"hevc"}},
		}
		db := quirkDB(entry)
		if err := service.SetQuirks(db); err != nil {
			t.Fatalf("SetQuirks: %v", err)
		}
		before := service.quirks.Resolve(quirkClientProfile)
		db.Entries[0].Override.DenyVideoCodecs[0] = "av1"
		db.Entries[0].ID = "mutated"
		after := service.quirks.Resolve(quirkClientProfile)
		if !slices.Equal(before.DenyVideoCodecs, after.DenyVideoCodecs) || !slices.Equal(before.overrideIDs, after.overrideIDs) {
			t.Fatalf("mutating the caller's slices changed the installed database: %+v -> %+v", before, after)
		}
	})
}

// TestQuirkOverlayCompositionIsOrderIndependent pins the meet semantics: there
// is no last-writer, so the "which entry wins" class of nondeterminism cannot
// exist.
func TestQuirkOverlayCompositionIsOrderIndependent(t *testing.T) {
	first := QuirkEntry{
		ID:       "entry-one",
		Note:     "n",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyVideoCodecs: []string{"hevc"}, MaxHeight: 1080, MaxBitrate: 8_000_000, ForceRemux: true},
		Flags:    []string{"seek-restarts-session"},
	}
	second := QuirkEntry{
		ID:       "entry-two",
		Note:     "n",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyVideoCodecs: []string{"vp9"}, DenyAudioCodecs: []string{"truehd"}, MaxHeight: 720, MaxAudioChannels: 2},
		Flags:    []string{"avoid-engine:web-mse"},
	}
	forward, err := validateQuirkDB(quirkDB(first, second))
	if err != nil {
		t.Fatalf("validateQuirkDB: %v", err)
	}
	reverse, err := validateQuirkDB(quirkDB(second, first))
	if err != nil {
		t.Fatalf("validateQuirkDB: %v", err)
	}
	a := forward.Resolve(quirkClientProfile)
	b := reverse.Resolve(quirkClientProfile)
	if !slices.Equal(a.DenyVideoCodecs, b.DenyVideoCodecs) ||
		!slices.Equal(a.DenyAudioCodecs, b.DenyAudioCodecs) ||
		!slices.Equal(a.DenyHDRFormats, b.DenyHDRFormats) ||
		!slices.Equal(a.DenySubtitleFormats, b.DenySubtitleFormats) ||
		a.MaxHeight != b.MaxHeight || a.MaxBitrate != b.MaxBitrate || a.MaxAudioChannels != b.MaxAudioChannels ||
		a.ForceRemux != b.ForceRemux || a.ForceTranscode != b.ForceTranscode ||
		!slices.Equal(a.flags, b.flags) || !slices.Equal(a.overrideIDs, b.overrideIDs) {
		t.Fatalf("composition is order-dependent:\n%+v\n%+v", a, b)
	}
	// Every override family must survive composition — none may be silently
	// dropped between QuirkOverride and quirkOverlay.
	if a.MaxHeight != 720 || a.MaxBitrate != 8_000_000 || a.MaxAudioChannels != 2 || !a.ForceRemux {
		t.Fatalf("composed overlay = %+v, want the meet of both entries", a)
	}
	if !slices.Equal(a.DenyVideoCodecs, []string{"hevc", "vp9"}) {
		t.Fatalf("DenyVideoCodecs = %v, want the sorted union", a.DenyVideoCodecs)
	}
}

// TestOverlayCompositionDropsUnknownFlag asserts the emission filter where an
// invalid overlay can actually be constructed. It is deliberately not an HTTP
// test: SetQuirks validates, so an invalid DB cannot be installed through any
// public API, and adding a production back door to make it testable would be
// worse than the gap.
func TestOverlayCompositionDropsUnknownFlag(t *testing.T) {
	db := QuirkDB{Version: "t", License: "CC0-1.0", Entries: []QuirkEntry{{
		ID:       "unfiltered",
		Selector: QuirkSelector{Client: "tv-x"},
		Flags:    []string{"seek-restarts-everything", "seek-restarts-session"},
	}}}
	ov := db.Resolve(quirkClientProfile)
	if !slices.Equal(ov.flags, []string{"seek-restarts-session"}) {
		t.Fatalf("flags = %v, want the unknown token filtered out at composition", ov.flags)
	}
}

// TestCanonicalCodecVocabularyMatchesMatcher stops the pinned vocabulary and
// the matchers it must agree with from drifting apart silently.
func TestCanonicalCodecVocabularyMatchesMatcher(t *testing.T) {
	for _, codec := range canonicalVideoCodecs {
		if _, ok := videoCodecCap([]VideoCodecCap{{Codec: codec}}, codec); !ok {
			t.Fatalf("videoCodecCap cannot match canonical token %q", codec)
		}
		if token, ok := normalizeVideoCodec(strings.ToUpper(codec)); !ok || token != codec {
			t.Fatalf("normalizeVideoCodec(%q) = %q/%v, want %q", strings.ToUpper(codec), token, ok, codec)
		}
	}
	for _, codec := range canonicalAudioCodecs {
		if token, ok := canonicalAudioCodec(strings.ToUpper(codec)); !ok || token != codec {
			t.Fatalf("canonicalAudioCodec(%q) = %q/%v, want %q", codec, token, ok, codec)
		}
	}
	for _, format := range canonicalHDRFormats {
		if !slices.Contains([]string{"hdr10", "hdr10plus", "hlg"}, format) && !strings.HasPrefix(format, hdrDolbyVisionGeneric) {
			t.Fatalf("canonical HDR token %q is outside what hdrFormat can emit", format)
		}
	}

	// The alias cross-product: a deny authored in ANY E-AC-3 spelling must veto
	// an eac3 source stream AND exclude the E-AC-3 transcode target.
	for _, spelling := range []string{"EAC-3", "eac-3", "eac3", "ec3", "e-ac-3"} {
		db, err := validateQuirkDB(quirkDB(QuirkEntry{
			ID:       "alias",
			Note:     "n",
			Selector: QuirkSelector{Client: "tv-x"},
			Override: QuirkOverride{DenyAudioCodecs: []string{spelling}},
		}))
		if err != nil {
			t.Fatalf("deny authored as %q was rejected: %v", spelling, err)
		}
		ov := db.Resolve(quirkClientProfile)
		if !ov.vetoesAudioPassthrough([]store.MediaFileStream{audioStream(1, "eac3", 6)}) {
			t.Fatalf("deny authored as %q missed an eac3 source stream", spelling)
		}
		profile := quirkClientProfile
		profile.DownstreamAudio = "passthrough"
		audio := hlsAudioProfile(profile, nil, LoudnessData{}, false, ov)
		if audio.Codec != "aac" {
			t.Fatalf("deny authored as %q still targets %q; want the AAC fallback", spelling, audio.Codec)
		}
	}
}

// TestContainerRemuxReasonOnlyOnForcedRemux sweeps the decision matrix with no
// quirks loaded and proves the one new reason value is unreachable there, then
// pins the precedence of capacity-degraded over it.
func TestContainerRemuxReasonOnlyOnForcedRemux(t *testing.T) {
	profiles := []CapabilityProfile{
		quirkClientProfile,
		{Client: "tv-x", Containers: []string{"mkv"}, DirectCodecs: []string{"hevc"}},
		{Client: "tv-x", Containers: []string{"mp4"}, DirectCodecs: []string{"av1"}},
	}
	streamSets := [][]store.MediaFileStream{
		{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "eac3", 6)},
		{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "truehd", 8)},
		{videoStream("hevc", "Main 10", 0, "smpte2084"), audioStream(1, "eac3", 6)},
	}
	for i, profile := range profiles {
		for j, streams := range streamSets {
			service, st, scope := capabilityTestService(t)
			mediaID := importProbedMedia(t, st, "fp-no-remux", "mkv", "hevc", 1920, 1080, streams)
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: profile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Reason == "container-remux" {
				t.Fatalf("profile %d / streams %d emitted container-remux with no quirk loaded", i, j)
			}
		}
	}

	t.Run("capacity degradation outranks the remux reason", func(t *testing.T) {
		service, st, scope := capabilityTestService(t)
		installQuirks(t, service, QuirkEntry{
			ID:       "tv-x-container-remux",
			Note:     "fixture",
			Selector: QuirkSelector{Client: "tv-x"},
			Override: QuirkOverride{ForceRemux: true},
		})
		sessions := NewSessions(&fakeTranscoder{}, t.TempDir(), nil)
		defer sessions.Close()
		sessions.SetTranscodeCap(1)
		service.SetSessions(sessions)
		service.SetPartyRoster(stubRoster{member: true})
		startTranscode(t, sessions, SessionKey{SessionID: "session-managed", MediaID: "media-other"})

		mediaID := importQuirkMedia(t, st, "fp-remux-degraded")
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:           string(mediaID),
			CapabilityProfile: quirkClientProfile,
		})
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if resp.Reason != "capacity-degraded" {
			t.Fatalf("Reason = %q, want capacity-degraded (an operational degradation the user can act on)", resp.Reason)
		}
		if !slices.Equal(resp.AppliedQuirks, []string{"tv-x-container-remux"}) {
			t.Fatalf("AppliedQuirks = %v, want the entry named even though the reason is not its own", resp.AppliedQuirks)
		}
	})
}

// importQuirkImageMedia seeds an image-class Media, which short-circuits BEFORE
// the capability ladder and therefore never consults the overlay.
func importQuirkImageMedia(t *testing.T, st *store.Store, fingerprint string) store.MediaID {
	t.Helper()
	path := filepath.Join(t.TempDir(), fingerprint+".jpg")
	if err := os.WriteFile(path, []byte("\xff\xd8\xff\xe0 jpeg bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              "Still " + fingerprint,
		Container:          "jpg",
		Codec:              "mjpeg",
		ContentFingerprint: fingerprint,
		Class:              store.MediaClassImage,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(image): %v", err)
	}
	return media.ID
}

// TestAppliedQuirksTracksOverlayApplication proves attribution keys on whether
// the overlay actually governed the decision, not on the verdict. Suppressing
// on "unavailable" would lose the field exactly where triage needs it most:
// "the quirk forced a transcode and the server refused it" is the single most
// valuable thing a playback report can say. behaviorFlags, which describe the
// device rather than the decision, are present in every branch.
func TestAppliedQuirksTracksOverlayApplication(t *testing.T) {
	entry := QuirkEntry{
		ID:       "tv-x-attribution",
		Note:     "fixture",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyVideoCodecs: []string{"hevc"}},
		Flags:    []string{"seek-restarts-session"},
	}
	cases := []struct {
		name     string
		request  func(*testing.T, *Service, *store.Store, auth.Scope) InfoRequest
		setup    func(*testing.T, *Service, *store.Store, auth.Scope)
		attribed bool
	}{
		{
			name: "image-class item never consults the overlay",
			request: func(t *testing.T, _ *Service, st *store.Store, _ auth.Scope) InfoRequest {
				return InfoRequest{MediaID: string(importQuirkImageMedia(t, st, "fp-attrib-image")), CapabilityProfile: quirkClientProfile}
			},
		},
		{
			name: "unknown item never consults the overlay",
			request: func(*testing.T, *Service, *store.Store, auth.Scope) InfoRequest {
				return InfoRequest{MediaID: "media-does-not-exist", CapabilityProfile: quirkClientProfile}
			},
		},
		{
			name: "sealed Library never consults the overlay",
			request: func(t *testing.T, _ *Service, st *store.Store, _ auth.Scope) InfoRequest {
				return InfoRequest{MediaID: string(importQuirkMedia(t, st, "fp-attrib-sealed")), CapabilityProfile: quirkClientProfile}
			},
			setup: func(_ *testing.T, service *Service, _ *store.Store, _ auth.Scope) {
				service.SetSealed(func(store.LibraryID) bool { return true })
			},
		},
		{
			name: "negotiated video item reports the override",
			request: func(t *testing.T, _ *Service, st *store.Store, _ auth.Scope) InfoRequest {
				return InfoRequest{MediaID: string(importQuirkMedia(t, st, "fp-attrib-video")), CapabilityProfile: quirkClientProfile}
			},
			attribed: true,
		},
		{
			name: "quirk-forced transcode the Profile refuses still reports it",
			request: func(t *testing.T, _ *Service, st *store.Store, _ auth.Scope) InfoRequest {
				return InfoRequest{MediaID: string(importQuirkMedia(t, st, "fp-attrib-refused")), CapabilityProfile: quirkClientProfile}
			},
			setup: func(t *testing.T, _ *Service, st *store.Store, scope auth.Scope) {
				transcode := false
				setProfileSettings(t, st, scope.ProfileID, store.ProfileSettings{TranscodeAllowed: &transcode})
			},
			attribed: true,
		},
		{
			name: "image main with a negotiated next item reports it",
			request: func(t *testing.T, _ *Service, st *store.Store, _ auth.Scope) InfoRequest {
				return InfoRequest{
					MediaID:           string(importQuirkImageMedia(t, st, "fp-attrib-mixed-image")),
					NextMediaID:       string(importQuirkMedia(t, st, "fp-attrib-mixed-video")),
					CapabilityProfile: quirkClientProfile,
				}
			},
			attribed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, entry)
			req := tc.request(t, service, st, scope)
			if tc.setup != nil {
				tc.setup(t, service, st, scope)
			}
			resp, err := service.Info(context.Background(), scope, req)
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if got := len(resp.AppliedQuirks) > 0; got != tc.attribed {
				t.Fatalf("AppliedQuirks = %v (decision %q), want attributed=%v", resp.AppliedQuirks, resp.Decision, tc.attribed)
			}
			if !slices.Equal(resp.BehaviorFlags, []string{"seek-restarts-session"}) {
				t.Fatalf("BehaviorFlags = %v, want the device flag on every branch", resp.BehaviorFlags)
			}
		})
	}
}
