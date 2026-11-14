package playback

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// resolveSel resolves a Part's opaque selector back to its (VersionLabel,
// PartOrdinal) coordinates via the service's selector, so version tests assert
// the chosen Version+Part without the raw fingerprint the wire no longer carries.
func resolveSel(t *testing.T, svc *Service, scope auth.Scope, p PartInfo) SelectedSource {
	t.Helper()
	if p.Selector == "" {
		t.Fatalf("Part %d carries an empty selector", p.Ordinal)
	}
	src, err := svc.selector.Resolve(p.Selector, scope)
	if err != nil {
		t.Fatalf("Resolve(selector) for ordinal %d: %v", p.Ordinal, err)
	}
	return src
}

// addVersion attaches an extra file to an existing Media at a Version/Part slot
// (the 1:N write path, ADR-0009 §2), with an optional probed stream inventory.
func addVersion(t *testing.T, st *store.Store, mediaID store.MediaID, fingerprint, versionLabel string, partOrdinal int, streams []store.MediaFileStream) {
	t.Helper()
	addVersionFile(t, st, mediaID, fingerprint+".mkv", fingerprint, versionLabel, partOrdinal, streams)
}

// addVersionFile is addVersion with the file name given, so a test controls
// the container a Version's path names.
func addVersionFile(t *testing.T, st *store.Store, mediaID store.MediaID, name, fingerprint, versionLabel string, partOrdinal int, streams []store.MediaFileStream) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := st.AddMediaFileVersion(context.Background(), mediaID, store.MediaFile{
		Path:               path,
		ContentFingerprint: fingerprint,
		VersionLabel:       versionLabel,
		PartOrdinal:        partOrdinal,
	}, streams)
	if err != nil {
		t.Fatalf("AddMediaFileVersion(%s/%s#%d): %v", fingerprint, versionLabel, partOrdinal, err)
	}
}

// heightUncapped returns profile without per-codec height ceilings. A Version
// other than the first has no stored frame height, so under a ceiling it is
// held off passthrough (unknownHeightBarred); tests of what else decides a
// Version use this to leave height out of the question.
func heightUncapped(profile CapabilityProfile) CapabilityProfile {
	profile.VideoCodecs = slices.Clone(profile.VideoCodecs)
	for i := range profile.VideoCodecs {
		profile.VideoCodecs[i].MaxHeight = 0
	}
	return profile
}

// TestNegotiateBestVersionDirectPlays guards the load-bearing behaviour: when a
// Media has two Versions whose per-stream inventory differs — a Dolby-Vision
// encode the client cannot direct play and an SDR encode it can — playback must
// pick the playable Version and direct-play it, not transcode the DV sibling
// (ADR-0009 §2 negotiation happens BEFORE transcode). The two Versions differ
// by their per-file stream inventory (HDR transfer), which is the file-plane
// data the negotiation reasons over; resolution/codec live on the shared Media
// row, so the distinguishing lever is per-stream.
func TestNegotiateBestVersionDirectPlays(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	// Default Version "" is the Dolby-Vision encode: under main10_1080p (HDR10/
	// HLG only) its DV transfer forces a tone-map transcode.
	mediaID := importProbedMedia(t, st, "fp-v-dv", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 5, "smpte2084"),
		audioStream(1, "eac3", 6),
	})
	// A second Version: HEVC Main 10 SDR — direct-playable under the same profile.
	// Give it two Parts so the chosen Version is observable in resp.Parts (a
	// single-Part Version emits no Parts array — see negotiateVersion).
	sdrStreams := []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "eac3", 6),
	}
	addVersion(t, st, mediaID, "fp-v-sdr-0", "sdr", 0, sdrStreams)
	addVersion(t, st, mediaID, "fp-v-sdr-1", "sdr", 1, sdrStreams)

	// Under a height ceiling the SDR Version's unknown frame height holds it off
	// direct play; the negotiation itself is shown with height left out.
	capped, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: main10_1080p,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if capped.Decision == "direct-play" {
		t.Fatalf("capped Decision = direct-play, want the SDR Version's unknown height to bar it")
	}
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: heightUncapped(main10_1080p),
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play (the SDR Version is playable)", resp.Decision)
	}
	// The chosen Version is the SDR one — its two Parts are reported in order,
	// confirming negotiation picked it over the default DV Version.
	if len(resp.Parts) != 2 {
		t.Fatalf("Parts = %#v, want the SDR Version's two ordered Parts", resp.Parts)
	}
	// The selector resolves to the SDR Version's two Parts, confirming negotiation
	// picked it over the default DV Version — without exposing the fingerprint.
	if s0 := resolveSel(t, service, scope, resp.Parts[0]); s0.VersionLabel != "sdr" || s0.PartOrdinal != 0 {
		t.Fatalf("Parts[0] selector resolved to %#v, want sdr#0", s0)
	}
	if s1 := resolveSel(t, service, scope, resp.Parts[1]); s1.VersionLabel != "sdr" || s1.PartOrdinal != 1 {
		t.Fatalf("Parts[1] selector resolved to %#v, want sdr#1", s1)
	}
}

// TestNegotiateVersionTranscodeFallback guards the honest fallback: when NO
// Version direct-plays, negotiation keeps the default Version and transcodes it,
// rather than silently failing.
func TestNegotiateVersionTranscodeFallback(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-vt-4kdv", "mkv", "hevc", 3840, 2160, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 8, "smpte2084"),
		audioStream(1, "truehd", 8),
	})
	// Second Version: also un-direct-playable (4K exceeds the 1080p ceiling).
	addVersion(t, st, mediaID, "fp-vt-4ksdr", "4k-sdr", 0, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"}, // 4K, no height cap satisfied
		audioStream(1, "truehd", 8),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: main10_1080p,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode (no Version direct-plays)", resp.Decision)
	}
	// Each Version here is single-Part, so no Parts array is emitted (the
	// pre-1:N wire shape is preserved when there is nothing to sequence).
	if resp.Parts != nil {
		t.Fatalf("Parts = %#v, want nil for single-Part Versions", resp.Parts)
	}
}

// TestPartsOrderedAsOneTimeline guards that the negotiated Version's Parts are
// reported in ascending PartOrdinal — one continuous timeline.
func TestPartsOrderedAsOneTimeline(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	directStreams := []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "eac3", 6),
	}
	// Part 0 is the primary file (the default Version).
	mediaID := importProbedMedia(t, st, "fp-part0", "mkv", "hevc", 1920, 1080, directStreams)
	// Part 1 of the same default Version — inserted as a separate file.
	addVersion(t, st, mediaID, "fp-part1", "", 1, directStreams)

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: main10_1080p,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if len(resp.Parts) != 2 {
		t.Fatalf("Parts = %d, want 2 (one Version, two Parts)", len(resp.Parts))
	}
	if resp.Parts[0].Ordinal != 0 || resp.Parts[1].Ordinal != 1 {
		t.Fatalf("Parts ordinals = %d,%d, want 0,1", resp.Parts[0].Ordinal, resp.Parts[1].Ordinal)
	}
	// Both Parts belong to the default ("") Version, in ascending order.
	if s0 := resolveSel(t, service, scope, resp.Parts[0]); s0.VersionLabel != "" || s0.PartOrdinal != 0 {
		t.Fatalf("Parts[0] selector resolved to %#v, want default#0", s0)
	}
	if s1 := resolveSel(t, service, scope, resp.Parts[1]); s1.VersionLabel != "" || s1.PartOrdinal != 1 {
		t.Fatalf("Parts[1] selector resolved to %#v, want default#1", s1)
	}
}

// TestSingleFileStillPlays guards the no-regression contract: a Media with one
// file (the pre-1:N shape) negotiates to that single Version+Part and plays
// exactly as before.
func TestSingleFileStillPlays(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-single", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "eac3", 6),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: main10_1080p,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	// A lone file emits no Parts array — playbackInfo is byte-identical to the
	// pre-1:N shape, so existing clients see no change (no regression).
	if resp.Parts != nil {
		t.Fatalf("Parts = %#v, want nil for a single-file Media", resp.Parts)
	}
}

// TestInfoListsVersionsAndHonoursSelection: a Media with two Versions lists
// both with opaque IDs, binds the negotiated non-primary file into its token,
// and a selectedVersion wins over negotiation even when it needs a transcode.
func TestInfoListsVersionsAndHonoursSelection(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-pick-dv", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 8, "smpte2084"),
		audioStream(1, "eac3", 6),
	})
	addVersion(t, st, mediaID, "fp-pick-sdr", "1080p SDR", 0, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "eac3", 6),
	})
	info := func(selected string) InfoResponse {
		t.Helper()
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:           string(mediaID),
			CapabilityProfile: heightUncapped(main10_1080p),
			SelectedVersion:   selected,
		})
		if err != nil {
			t.Fatalf("Info(%q): %v", selected, err)
		}
		return resp
	}
	grantFile := func(resp InfoResponse) string {
		t.Helper()
		stream := resp.Streams[0]
		target := streamtoken.TargetMediaDirect
		if stream.Kind == "hls" {
			target = streamtoken.TargetMediaHLS
		}
		grant, err := service.tokens.Verify(stream.Token, target, scope)
		if err != nil {
			t.Fatalf("Verify %s token: %v", stream.Kind, err)
		}
		return grant.FileFingerprint
	}

	negotiated := info("")
	if negotiated.Decision != "direct-play" || len(negotiated.Versions) != 2 {
		t.Fatalf("negotiated = %s with versions %+v, want direct-play listing 2 Versions", negotiated.Decision, negotiated.Versions)
	}
	primary, sdr := negotiated.Versions[0], negotiated.Versions[1]
	if primary.Label != "" || primary.Selected || primary.VideoCodec != "hevc" || primary.HDR == "" {
		t.Fatalf("default Version = %+v, want unselected hevc HDR with empty label", primary)
	}
	if sdr.Label != "1080p SDR" || !sdr.Selected || sdr.HDR != "" || sdr.ID == primary.ID {
		t.Fatalf("SDR Version = %+v, want selected SDR with its own ID", sdr)
	}
	for _, v := range negotiated.Versions {
		if strings.Contains(v.ID, "fp-pick") || strings.Contains(v.ID, v.Label) && v.Label != "" {
			t.Fatalf("Version ID %q leaks a fingerprint or label", v.ID)
		}
	}
	if got := grantFile(negotiated); got != "fp-pick-sdr" {
		t.Fatalf("negotiated token file = %q, want the SDR Version's", got)
	}

	picked := info(primary.ID)
	if picked.Decision != "transcode" || !picked.Versions[0].Selected || picked.Versions[1].Selected {
		t.Fatalf("picked default = %s (%s) with %+v, want a transcode of the selected default Version", picked.Decision, picked.Reason, picked.Versions)
	}
	if got := grantFile(picked); got != "fp-pick-dv" {
		t.Fatalf("primary Version token file = %q, want the primary Version's own file", got)
	}
	if again := info(primary.ID); again.Versions[0].ID != primary.ID {
		t.Fatalf("Version ID not stable: %q then %q", primary.ID, again.Versions[0].ID)
	}

	if unknown := info("v0000000000000000"); unknown.Decision != "direct-play" || !unknown.Versions[1].Selected {
		t.Fatalf("unknown selection = %s with %+v, want the negotiated decision", unknown.Decision, unknown.Versions)
	}
}

// TestInfoOmitsVersionsForOneVersion: a single-Version Media keeps the
// pre-picker response shape.
func TestInfoOmitsVersionsForOneVersion(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-one", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "eac3", 6),
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: main10_1080p, SelectedVersion: "v0000000000000000"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Versions != nil || resp.Decision != "direct-play" {
		t.Fatalf("single Version = %s with versions %+v, want direct-play and no list", resp.Decision, resp.Versions)
	}
}

// TestVersionJudgedByItsOwnFileFacts guards that a Version other than the one
// the Media row describes is judged by its own container and video codec, not
// the row's: a selected MP4 Version is not offered as direct play to an
// MKV-only client, and an AV1 Version is not direct-played by a client without
// AV1, even though the row says "mkv" and "hevc".
func TestVersionJudgedByItsOwnFileFacts(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	sdrHEVC := []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "eac3", 6),
	}
	anchorPath := filepath.Join(t.TempDir(), "Night Harbor.mkv")
	if err := os.WriteFile(anchorPath, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	anchor, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: anchorPath, Title: "Night Harbor",
		Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo,
		ContentFingerprint: "fp-facts-mkv", DurationSecondsHint: 120,
		VideoWidthHint: 1920, VideoHeightHint: 1080, Streams: sdrHEVC,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	mediaID := anchor.ID
	addVersionFile(t, st, mediaID, "Night Harbor - Remux.mp4", "fp-facts-mp4", "Remux", 0, sdrHEVC)
	addVersionFile(t, st, mediaID, "Night Harbor - AV1.mkv", "fp-facts-av1", "AV1", 0, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "av1", Profile: "Main"},
		audioStream(1, "eac3", 6),
	})
	mkvOnly := heightUncapped(main10_1080p)
	mkvOnly.Containers = []string{"mkv"}
	info := func(label string, profile CapabilityProfile) InfoResponse {
		t.Helper()
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:           string(mediaID),
			CapabilityProfile: profile,
			SelectedVersion:   versionID(mediaID, label),
		})
		if err != nil {
			t.Fatalf("Info(%q): %v", label, err)
		}
		for _, v := range resp.Versions {
			if v.Selected != (v.Label == label) {
				t.Fatalf("Info(%q) Versions = %+v, want only %q selected", label, resp.Versions, label)
			}
		}
		return resp
	}
	if resp := info("", mkvOnly); resp.Decision != "direct-play" {
		t.Fatalf("MKV default Version = %s (%s), want direct-play", resp.Decision, resp.Reason)
	}
	if resp := info("Remux", mkvOnly); resp.Decision == "direct-play" {
		t.Fatalf("MP4 Version to an MKV-only client = direct-play, want it judged by its own container")
	}
	if resp := info("Remux", heightUncapped(main10_1080p)); resp.Decision != "direct-play" {
		t.Fatalf("MP4 Version to an MP4 client = %s (%s), want direct-play", resp.Decision, resp.Reason)
	}
	if resp := info("AV1", main10_1080p); resp.Decision == "direct-play" {
		t.Fatalf("AV1 Version to a client without AV1 = direct-play, want it judged by its own codec")
	}
}

// TestAlternateWithUnknownHeightTranscodes guards the fail-safe for a frame
// height playback cannot know: a 4K HEVC Version beside a 1080p first Version
// has no stored height of its own, so a client capped at 1080p gets a
// transcode of it, never direct play or direct stream, while the first
// Version, whose height the row holds, still direct-plays.
func TestAlternateWithUnknownHeightTranscodes(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	hevc := []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "eac3", 6),
	}
	anchorPath := filepath.Join(t.TempDir(), "Night Harbor.mkv")
	if err := os.WriteFile(anchorPath, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	anchor, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: anchorPath, Title: "Night Harbor",
		Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo,
		ContentFingerprint: "fp-height-1080", DurationSecondsHint: 120,
		VideoWidthHint: 1920, VideoHeightHint: 1080, Streams: hevc,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	mediaID := anchor.ID
	addVersionFile(t, st, mediaID, "Night Harbor - 4K.mkv", "fp-height-4k", "4K", 0, hevc)
	addVersionFile(t, st, mediaID, "Night Harbor - 4K TrueHD.mkv", "fp-height-4k-truehd", "4K TrueHD", 0, []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		audioStream(1, "truehd", 8),
	})
	info := func(label string, profile CapabilityProfile) InfoResponse {
		t.Helper()
		resp, err := service.Info(context.Background(), scope, InfoRequest{
			MediaID:           string(mediaID),
			CapabilityProfile: profile,
			SelectedVersion:   versionID(mediaID, label),
		})
		if err != nil {
			t.Fatalf("Info(%q): %v", label, err)
		}
		return resp
	}
	if resp := info("", main10_1080p); resp.Decision != "direct-play" {
		t.Fatalf("1080p first Version = %s (%s), want direct-play", resp.Decision, resp.Reason)
	}
	if resp := info("4K", main10_1080p); resp.Decision != "transcode" {
		t.Fatalf("4K Version to a 1080p-capped client = %s (%s), want transcode", resp.Decision, resp.Reason)
	}
	if resp := info("4K TrueHD", main10_1080p); resp.Decision != "transcode" {
		t.Fatalf("4K Version needing only an audio transcode = %s (%s), want a full transcode, not direct stream", resp.Decision, resp.Reason)
	}
	if resp := info("4K", heightUncapped(main10_1080p)); resp.Decision != "direct-play" {
		t.Fatalf("4K Version to a client without a height ceiling = %s (%s), want direct-play", resp.Decision, resp.Reason)
	}
}
