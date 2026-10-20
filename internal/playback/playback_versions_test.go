package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
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
	path := filepath.Join(t.TempDir(), fingerprint+".bin")
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

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: main10_1080p,
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
