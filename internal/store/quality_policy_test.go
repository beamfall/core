package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// feature:quality-policy — per-Library quality policy + the Radarr-style upgrade
// loop + the keep-as-Version merge verb. These tests exercise the
// store engine: the policy's ranking/cutoff decision is pure, and the merge verb
// folds a candidate encode into an existing Media as a Version, promoting the
// policy-preferred one to active while keeping the prior encode (invariant #6,
// non-destructive).

// TestQualityPolicyRankingAndCutoff guards the upgrade decision: a candidate
// upgrades the current active Version only when the current is BELOW the cutoff
// AND the candidate ranks strictly higher in the preference order. Once the
// current is at-or-above cutoff the loop stops, even for a technically-better
// candidate (no churn past "good enough").
func TestQualityPolicyRankingAndCutoff(t *testing.T) {
	// Best-first ranking, cutoff at 1080p: SD/720p are below cutoff, 1080p/UHD
	// are good enough.
	policy := QualityPolicy{
		Order:  []string{QualityTierUHD, QualityTierHD1080, QualityTierHD720, QualityTierSD},
		Cutoff: QualityTierHD1080,
	}
	cases := []struct {
		name        string
		current     string
		candidate   string
		wantUpgrade bool
	}{
		{"sd upgrades to 720 (both below cutoff, better)", QualityTierSD, QualityTierHD720, true},
		{"sd upgrades to 1080 (reaches cutoff)", QualityTierSD, QualityTierHD1080, true},
		{"720 upgrades to 1080", QualityTierHD720, QualityTierHD1080, true},
		{"1080 does NOT upgrade to UHD (already at cutoff)", QualityTierHD1080, QualityTierUHD, false},
		{"uhd does NOT downgrade to 1080", QualityTierUHD, QualityTierHD1080, false},
		{"720 does NOT take a worse SD candidate", QualityTierHD720, QualityTierSD, false},
		{"equal tier never upgrades", QualityTierHD720, QualityTierHD720, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.ShouldUpgrade(tc.current, tc.candidate); got != tc.wantUpgrade {
				t.Fatalf("ShouldUpgrade(%s, %s) = %v, want %v", tc.current, tc.candidate, got, tc.wantUpgrade)
			}
		})
	}

	// No cutoff: the loop always upgrades to a strictly-better candidate.
	open := QualityPolicy{Order: []string{QualityTierUHD, QualityTierHD1080, QualityTierHD720, QualityTierSD}}
	if !open.ShouldUpgrade(QualityTierHD1080, QualityTierUHD) {
		t.Fatal("no-cutoff policy should upgrade 1080 -> UHD")
	}
	if open.ShouldUpgrade(QualityTierUHD, QualityTierHD1080) {
		t.Fatal("no-cutoff policy must not downgrade UHD -> 1080")
	}
}

func TestQualityTierForHeight(t *testing.T) {
	cases := []struct {
		height int
		want   string
	}{
		{0, QualityTierSD}, // a probe-less candidate is the SD floor.
		{480, QualityTierSD},
		{720, QualityTierHD720},
		{1080, QualityTierHD1080},
		{1440, QualityTierHD1080},
		{2160, QualityTierUHD},
		{4320, QualityTierUHD},
	}
	for _, tc := range cases {
		if got := QualityTierForHeight(tc.height); got != tc.want {
			t.Fatalf("QualityTierForHeight(%d) = %s, want %s", tc.height, got, tc.want)
		}
	}
}

// TestQualityPolicyPersistenceRoundTrip stores a Library's policy and reads it
// back; an un-set Library falls back to the default. Validation rejects unknown
// tiers and a cutoff outside the order.
func TestQualityPolicyPersistenceRoundTrip(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Unset → default.
	got, err := st.QualityPolicyForLibrary(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if want := DefaultQualityPolicy(); got.Cutoff != want.Cutoff || len(got.Order) != len(want.Order) {
		t.Fatalf("unset policy = %#v, want default %#v", got, want)
	}

	custom := QualityPolicy{Order: []string{QualityTierHD1080, QualityTierHD720, QualityTierSD}, Cutoff: QualityTierHD720}
	if err := st.SetQualityPolicyForLibrary(ctx, "library-local", custom, now); err != nil {
		t.Fatal(err)
	}
	got, err = st.QualityPolicyForLibrary(ctx, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cutoff != QualityTierHD720 || len(got.Order) != 3 || got.Order[0] != QualityTierHD1080 {
		t.Fatalf("round-trip policy = %#v, want %#v", got, custom)
	}

	// A different Library still sees the default (per-Library keying).
	other, err := st.QualityPolicyForLibrary(ctx, "library-archive")
	if err != nil {
		t.Fatal(err)
	}
	if other.Cutoff != DefaultQualityPolicy().Cutoff {
		t.Fatalf("other library policy = %#v, want default", other)
	}

	// Validation.
	if err := st.SetQualityPolicyForLibrary(ctx, "library-local", QualityPolicy{Order: []string{"4k"}}, now); err == nil {
		t.Fatal("expected error for unknown tier")
	}
	if err := st.SetQualityPolicyForLibrary(ctx, "library-local", QualityPolicy{Order: []string{QualityTierSD}, Cutoff: QualityTierUHD}, now); err == nil {
		t.Fatal("expected error for cutoff outside order")
	}
	if err := st.SetQualityPolicyForLibrary(ctx, "library-local", QualityPolicy{}, now); err == nil {
		t.Fatal("expected error for empty order")
	}
}

// importHeightMedia imports a Media whose probed frame height is recorded, and
// returns its id. Height drives the quality tier the upgrade loop ranks on.
func importHeightMedia(t *testing.T, st *Store, libraryID LibraryID, path, title string, height int) MediaID {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(title+" bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	width := height * 16 / 9
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          libraryID,
		Path:               path,
		Title:              title,
		Container:          "mkv",
		Codec:              "hevc",
		Class:              MediaClassVideo,
		ContentFingerprint: HashMediaFilePath(path),
		VideoWidthHint:     width,
		VideoHeightHint:    height,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(%s): %v", path, err)
	}
	return media.ID
}

// TestMergeKeepAsVersionPromotesPreferredEncodeNonDestructively is the
// load-bearing keep-as-Version test: a Media's current active
// encode is below the Library's cutoff; a higher-quality candidate is folded in.
// The policy says upgrade, so the candidate becomes the active (default "")
// Version and the prior encode is KEPT as a non-default Version — both files
// survive (non-destructive, invariant #6), the Media row is untouched.
func TestMergeKeepAsVersionPromotesPreferredEncodeNonDestructively(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	root := t.TempDir()

	// Current active encode: 720p (below the default 1080p cutoff).
	currentPath := filepath.Join(root, "atlas-720p.mkv")
	mediaID := importHeightMedia(t, st, "library-local", currentPath, "Atlas", 720)
	currentFP := HashMediaFilePath(currentPath)

	// A 1080p candidate appears (a distinct encode = distinct bytes/fingerprint).
	candPath := filepath.Join(root, "atlas-1080p.mkv")
	if err := os.WriteFile(candPath, []byte("atlas 1080p bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	candFP := HashMediaFilePath(candPath)
	candidate := QualityCandidate{
		ContentFingerprint: candFP,
		Path:               candPath,
		VideoHeight:        1080,
	}

	// Current Media height is 720 (the active encode); policy default upgrades
	// 720 → 1080 (reaches cutoff).
	promoted, err := st.MergeKeepAsVersion(ctx, "library-local", mediaID, 720, candidate, now)
	if err != nil {
		t.Fatal(err)
	}
	if !promoted {
		t.Fatal("candidate should have been promoted to active (720 below 1080 cutoff)")
	}

	files, err := st.MediaFilesForMedia(ctx, mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 Versions after keep-as-Version (non-destructive), got %d: %#v", len(files), files)
	}

	// The active Version (default "" label) must be the promoted candidate.
	active, err := st.PrimaryMediaFile(ctx, mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if active.VersionLabel != "" {
		t.Fatalf("active Version label = %q, want default \"\"", active.VersionLabel)
	}
	if active.ContentFingerprint != candFP {
		t.Fatalf("active fingerprint = %q, want candidate %q", active.ContentFingerprint, candFP)
	}

	// The prior encode is preserved as a non-default Version, not trashed.
	var keptFound bool
	for _, f := range files {
		if f.ContentFingerprint == currentFP {
			keptFound = true
			if f.VersionLabel == "" {
				t.Fatalf("prior encode kept at default label; should be demoted to a kept Version")
			}
		}
	}
	if !keptFound {
		t.Fatalf("prior encode %q was destroyed; keep-as-Version must preserve it (invariant #6)", currentFP)
	}
}

// TestMergeKeepAsVersionFoldsWorseCandidateWithoutPromoting guards the other
// branch: when the policy does NOT favour the candidate (it is worse, or the
// current is already at/above cutoff), the candidate is still folded into the
// SAME Media as a sibling Version (not a separate Media), but the current active
// encode stays active.
func TestMergeKeepAsVersionFoldsWorseCandidateWithoutPromoting(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	root := t.TempDir()

	// Current active encode: 1080p (already at the default cutoff — good enough).
	currentPath := filepath.Join(root, "atlas-1080p.mkv")
	mediaID := importHeightMedia(t, st, "library-local", currentPath, "Atlas", 1080)
	currentFP := HashMediaFilePath(currentPath)

	// A UHD candidate appears, but the current is already at cutoff: no upgrade.
	candPath := filepath.Join(root, "atlas-uhd.mkv")
	if err := os.WriteFile(candPath, []byte("atlas uhd bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	candFP := HashMediaFilePath(candPath)
	promoted, err := st.MergeKeepAsVersion(ctx, "library-local", mediaID, 1080, QualityCandidate{
		ContentFingerprint: candFP,
		Path:               candPath,
		VideoHeight:        2160,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if promoted {
		t.Fatal("current already at cutoff; candidate must not be promoted")
	}

	files, err := st.MediaFilesForMedia(ctx, mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected the candidate folded as a sibling Version (2 files), got %d", len(files))
	}
	active, err := st.PrimaryMediaFile(ctx, mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if active.ContentFingerprint != currentFP || active.VersionLabel != "" {
		t.Fatalf("active Version changed; current encode should stay active: %#v", active)
	}
}
