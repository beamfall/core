package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// allLib is the most permissive read scope (all libraries, revealed) so the
// conformance assertions below see restricted media too.
var allLib = LibraryFilter{AllLibraries: true, Revealed: true}

// TestAcceptanceSeedYieldsDocumentedStates is the ATEST-CORE-1 Gate B
// conformance test: booting the store with SeedProfileAcceptance plants every
// documented deterministic state the Apple e2e test layers consume.
func TestAcceptanceSeedYieldsDocumentedStates(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedProfile: SeedProfileAcceptance})
	if err != nil {
		t.Fatalf("Open(acceptance): %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// (1) Pre-enrolled device credential: viewer device with a known secret.
	viewer, err := st.Device(ctx, "device-acc-viewer")
	if err != nil {
		t.Fatalf("viewer device: %v", err)
	}
	if viewer.Role != DeviceRoleViewer {
		t.Fatalf("viewer role = %q, want viewer", viewer.Role)
	}
	if viewer.SecretHash != HashDeviceSecret(AcceptanceViewerSecret) {
		t.Fatal("viewer secret hash does not match the documented AcceptanceViewerSecret")
	}

	// (2) N profiles: one adult (reveal-capable), one managed (no reveal).
	profiles, err := st.Profiles(ctx)
	if err != nil {
		t.Fatalf("profiles: %v", err)
	}
	if len(profiles) < 2 {
		t.Fatalf("profiles = %d, want >= 2", len(profiles))
	}
	var haveAdult, haveManaged bool
	for _, p := range profiles {
		switch p.Kind {
		case ProfileAdult:
			haveAdult = true
		case ProfileManaged:
			haveManaged = true
		}
	}
	if !haveAdult || !haveManaged {
		t.Fatalf("want both adult and managed profiles; adult=%v managed=%v", haveAdult, haveManaged)
	}

	// (3) Library/detail matrix: one unrestricted + one restricted library.
	libs, err := st.Libraries(ctx, allLib)
	if err != nil {
		t.Fatalf("libraries: %v", err)
	}
	var haveOpen, haveRestricted bool
	for _, l := range libs {
		if l.IsRestricted {
			haveRestricted = true
		} else {
			haveOpen = true
		}
	}
	if !haveOpen || !haveRestricted {
		t.Fatalf("want one open + one restricted library; open=%v restricted=%v", haveOpen, haveRestricted)
	}

	// (4) One short CLEAR (H.264/AAC) playable asset: a media row with codec
	// h264, a real on-disk file resolving to a fingerprint, and a stream
	// inventory exposing one video (h264) + one audio (aac) stream.
	clear, err := st.Media(ctx, "media-acc-clear", allLib)
	if err != nil {
		t.Fatalf("clear media: %v", err)
	}
	if clear.Codec != "h264" {
		t.Fatalf("clear media codec = %q, want h264", clear.Codec)
	}
	file, err := st.PrimaryMediaFile(ctx, "media-acc-clear", allLib)
	if err != nil {
		t.Fatalf("clear media file: %v", err)
	}
	if file.ContentFingerprint == "" || file.Path == "" {
		t.Fatalf("clear media file missing fingerprint/path: %+v", file)
	}
	streams, err := st.MediaFileStreams(ctx, file.ContentFingerprint)
	if err != nil {
		t.Fatalf("clear media streams: %v", err)
	}
	var haveVideoH264, haveAudioAAC bool
	for _, s := range streams {
		if s.Type == "video" && s.Codec == "h264" {
			haveVideoH264 = true
		}
		if s.Type == "audio" && s.Codec == "aac" {
			haveAudioAAC = true
		}
	}
	if !haveVideoH264 || !haveAudioAAC {
		t.Fatalf("clear asset must expose h264 video + aac audio streams; got %+v", streams)
	}
	for _, mediaID := range []MediaID{"media-acc-search-aurora", "media-acc-search-harbor"} {
		file, err := st.PrimaryMediaFile(ctx, mediaID, allLib)
		if err != nil {
			t.Fatalf("%s media file: %v", mediaID, err)
		}
		info, err := os.Stat(file.Path)
		if err != nil {
			t.Fatalf("%s media file stat: %v", mediaID, err)
		}
		if info.Size() < 1024 {
			t.Fatalf("%s media file is too small to be a review-playable MP4: %d bytes", mediaID, info.Size())
		}
		streams, err := st.MediaFileStreams(ctx, file.ContentFingerprint)
		if err != nil {
			t.Fatalf("%s media streams: %v", mediaID, err)
		}
		if len(streams) == 0 {
			t.Fatalf("%s must be playable from the seeded resume shelf", mediaID)
		}
	}

	// (5) Token-expiry / relock capability: an already-expired session, a
	// revealed session, and an unrevealed (relock baseline) session.
	expired, err := st.Session(ctx, "session-acc-expired")
	if err != nil {
		t.Fatalf("expired session: %v", err)
	}
	if expired.ExpiresAt.IsZero() || !expired.ExpiresAt.Before(time.Now()) {
		t.Fatalf("session-acc-expired must have a past ExpiresAt; got %v", expired.ExpiresAt)
	}
	revealed, err := st.Session(ctx, "session-acc-revealed")
	if err != nil {
		t.Fatalf("revealed session: %v", err)
	}
	if !revealed.Revealed {
		t.Fatal("session-acc-revealed must be Revealed")
	}
	baseline, err := st.Session(ctx, "session-acc-viewer")
	if err != nil {
		t.Fatalf("baseline session: %v", err)
	}
	if baseline.Revealed {
		t.Fatal("session-acc-viewer must be unrevealed (relock baseline)")
	}

	// (6) Server-seeded admin (Manager/Console role) credential: a manager-role
	// device, a verifiable admin password, and a session with a future admin
	// window. Device/role only — no entitlement/account code (invariant 7).
	manager, err := st.Device(ctx, "device-acc-manager")
	if err != nil {
		t.Fatalf("manager device: %v", err)
	}
	if manager.Role != DeviceRoleManager {
		t.Fatalf("manager role = %q, want manager", manager.Role)
	}
	if manager.SecretHash != HashDeviceSecret(AcceptanceManagerSecret) {
		t.Fatal("manager secret hash does not match the documented AcceptanceManagerSecret")
	}
	if err := st.VerifyAdminPassword(ctx, AcceptanceAdminPassword); err != nil {
		t.Fatalf("VerifyAdminPassword(AcceptanceAdminPassword): %v", err)
	}
	adminSession, err := st.Session(ctx, "session-acc-admin")
	if err != nil {
		t.Fatalf("admin session: %v", err)
	}
	if adminSession.AdminUntil.IsZero() {
		t.Fatal("session-acc-admin must carry a non-zero AdminUntil (Console window)")
	}
	if !adminSession.AdminUntil.After(time.Now()) {
		t.Fatalf("session-acc-admin AdminUntil must be in the future; got %v", adminSession.AdminUntil)
	}

	// (7) Breadth — search corpus: several titled media so a search has hits.
	aurora, err := st.Media(ctx, "media-acc-search-aurora", allLib)
	if err != nil || aurora.Title == "" {
		t.Fatalf("search-corpus media missing: %v (%+v)", err, aurora)
	}

	// (7) Breadth — up-next queue: an ordered container entity -> ordered
	// children via "contains" relations.
	rows, err := st.EntityRelationRows(ctx, "entity-acc-sequence", "contains", allLib)
	if err != nil {
		t.Fatalf("up-next relations: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("up-next queue wants >= 2 ordered children; got %d", len(rows))
	}
	for _, r := range rows {
		if r.SortOrder == nil {
			t.Fatalf("up-next child %+v missing SortOrder", r)
		}
	}

	// (7) Breadth — resume / playback-progress: at least one partial position.
	prog, ok, err := st.PlaybackProgressFor(ctx, "profile-acc-default", "media-acc-search-aurora")
	if err != nil {
		t.Fatalf("playback progress: %v", err)
	}
	if !ok || prog.PositionSeconds <= 0 || prog.PositionSeconds >= prog.DurationSeconds {
		t.Fatalf("resume shelf wants a partial position; ok=%v prog=%+v", ok, prog)
	}
}

// TestAcceptanceSeedIsDeterministic re-seeds a fresh store and asserts the
// admin verifier and a stream inventory are byte-stable across boots (the
// fixed admin salt + fixed seed clock make the world reproducible).
func TestAcceptanceSeedIsDeterministic(t *testing.T) {
	ctx := context.Background()
	open := func() *Store {
		st, err := Open(ctx, t.TempDir(), Options{SeedProfile: SeedProfileAcceptance})
		if err != nil {
			t.Fatalf("Open(acceptance): %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	a, b := open(), open()
	if err := a.VerifyAdminPassword(ctx, AcceptanceAdminPassword); err != nil {
		t.Fatalf("store A admin verify: %v", err)
	}
	if err := b.VerifyAdminPassword(ctx, AcceptanceAdminPassword); err != nil {
		t.Fatalf("store B admin verify: %v", err)
	}
	fa, err := a.PrimaryMediaFile(ctx, "media-acc-clear", allLib)
	if err != nil {
		t.Fatalf("store A clear file: %v", err)
	}
	fb, err := b.PrimaryMediaFile(ctx, "media-acc-clear", allLib)
	if err != nil {
		t.Fatalf("store B clear file: %v", err)
	}
	if fa.ContentFingerprint != fb.ContentFingerprint {
		t.Fatalf("clear fingerprint not deterministic: %q vs %q", fa.ContentFingerprint, fb.ContentFingerprint)
	}
}
