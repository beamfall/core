package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRetentionRuleSweepGraceKeepAndActivity(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Retention Keep")
	filter := LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	media := importRetentionMedia(t, st, library.ID, "keep", "Keep Candidate")

	rule, err := st.CreateRetentionRule(ctx, RetentionRule{
		Name:                "Leaving soon",
		SourceExpr:          `{"kind":"all-items"}`,
		TargetLibraryID:     library.ID,
		GraceDays:           2,
		RevealedAtAuthoring: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateRetentionRule: %v", err)
	}
	if _, skipped, err := st.ApplyRetentionRules(ctx, now); err != nil || skipped {
		t.Fatalf("initial ApplyRetentionRules skipped=%v err=%v", skipped, err)
	}
	if _, err := st.Media(ctx, media.ID, filter); err != nil {
		t.Fatalf("media hidden before grace elapsed: %v", err)
	}
	if err := st.SetMediaKeep(ctx, "profile-default", media.ID, true, filter); err != nil {
		t.Fatalf("SetMediaKeep: %v", err)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(3*24*time.Hour)); err != nil || skipped || trashed != 0 {
		t.Fatalf("kept ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if _, err := st.Media(ctx, media.ID, filter); err != nil {
		t.Fatalf("kept media hidden: %v", err)
	}
	if err := st.SetMediaKeep(ctx, "profile-default", media.ID, false, filter); err != nil {
		t.Fatalf("clear SetMediaKeep: %v", err)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(4*24*time.Hour)); err != nil || skipped || trashed != 1 {
		t.Fatalf("due ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if _, err := st.Media(ctx, media.ID, filter); err == nil {
		t.Fatal("retained media still visible after due sweep")
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: LibraryFilter{AllLibraries: true, Revealed: true}})
	if err != nil {
		t.Fatalf("ActivityEntries: %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action == ActivityRetentionTrash && entry.TargetType == "retention_rule" && entry.TargetID == rule.ID && entry.LibraryID == library.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("retention activity missing in %#v", entries)
	}
}

func TestRetentionRuleCandidateResetsWhenLeavingSource(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Retention Reset")
	filter := LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	media := importRetentionMedia(t, st, library.ID, "reset", "Reset Candidate")

	rule, err := st.CreateRetentionRule(ctx, RetentionRule{
		SourceExpr:          `{"kind":"all-items"}`,
		TargetLibraryID:     library.ID,
		GraceDays:           2,
		RevealedAtAuthoring: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateRetentionRule: %v", err)
	}
	if _, _, err := st.ApplyRetentionRules(ctx, now); err != nil {
		t.Fatalf("initial ApplyRetentionRules: %v", err)
	}
	if _, err := st.UpdateRetentionRule(ctx, rule.ID, RetentionRulePatch{SourceExpr: strPtr(`{"kind":"watched-by-any-profile","value":"any"}`)}, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("UpdateRetentionRule exclude: %v", err)
	}
	if _, _, err := st.ApplyRetentionRules(ctx, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("exclude ApplyRetentionRules: %v", err)
	}
	if _, err := st.UpdateRetentionRule(ctx, rule.ID, RetentionRulePatch{SourceExpr: strPtr(`{"kind":"all-items"}`)}, now.Add(2*24*time.Hour)); err != nil {
		t.Fatalf("UpdateRetentionRule include: %v", err)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(3*24*time.Hour)); err != nil || skipped || trashed != 0 {
		t.Fatalf("re-enter early ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if _, err := st.Media(ctx, media.ID, filter); err != nil {
		t.Fatalf("media trashed before reset grace elapsed: %v", err)
	}
}

func TestRetentionRuleTrashDisabledRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	library := createRetentionLibrary(t, st, "Retention Disabled")
	media := importRetentionMedia(t, st, library.ID, "disabled", "Disabled Candidate")
	if _, err := st.CreateRetentionRule(ctx, RetentionRule{
		SourceExpr:          `{"kind":"all-items"}`,
		TargetLibraryID:     library.ID,
		GraceDays:           1,
		RevealedAtAuthoring: true,
	}, now.Add(-2*24*time.Hour)); err != nil {
		t.Fatalf("CreateRetentionRule: %v", err)
	}
	trashed, skipped, err := st.ApplyRetentionRules(ctx, now)
	if !errors.Is(err, ErrTrashDisabledConfirmationRequired) || !skipped || trashed != 0 {
		t.Fatalf("ApplyRetentionRules trash disabled trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if _, err := st.Media(ctx, media.ID, LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}); err != nil {
		t.Fatalf("media hidden despite disabled trash: %v", err)
	}
}

func TestRetentionRulePagesFullSourceMembership(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Retention Page")
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	var last Media
	for i := 0; i < retentionSourcePageSize+5; i++ {
		last = importRetentionMedia(t, st, library.ID, fmt.Sprintf("p%03d", i), fmt.Sprintf("Page Candidate %03d", i))
	}
	if _, err := st.CreateRetentionRule(ctx, RetentionRule{
		SourceExpr:          `{"kind":"all-items"}`,
		TargetLibraryID:     library.ID,
		GraceDays:           1,
		RevealedAtAuthoring: true,
	}, now.Add(-2*24*time.Hour)); err != nil {
		t.Fatalf("CreateRetentionRule: %v", err)
	}
	if _, _, err := st.ApplyRetentionRules(ctx, now.Add(-2*24*time.Hour)); err != nil {
		t.Fatalf("seed candidates: %v", err)
	}
	trashed, skipped, err := st.ApplyRetentionRules(ctx, now)
	if err != nil || skipped || trashed < retentionSourcePageSize+5 {
		t.Fatalf("ApplyRetentionRules paged trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if _, err := st.Media(ctx, last.ID, LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}); err == nil {
		t.Fatal("last paged candidate still visible; sweep likely only processed first page")
	}
}

// TestRetentionCandidateHoldObjectAuthorization locks the
// SECAUDIT-20260817-RETENTION-KEEP-1 contract on the operator hold: the rule
// and the Media item resolve through the caller's LibraryFilter, only
// candidates of the rule can be held, and every refusal leaves the candidate
// cache untouched — no retention state for unknown, hidden, cross-Library, or
// non-candidate IDs.
//
// Red-first evidence (2026-08-20): with the rule/media/candidacy resolution
// deliberately removed from SetRetentionCandidateHold (pre-fix global-write
// behavior), this test failed with
// "unknown-rule hold err = <nil>, want ErrSmartCollectionNotFound";
// the guards were then restored and the test passed.
func TestRetentionCandidateHoldObjectAuthorization(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Retention Hold Auth")
	other := createRetentionLibrary(t, st, "Retention Hold Other")
	filter := LibraryFilter{AllowedIDs: []LibraryID{library.ID, other.ID}, Revealed: true}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	media := importRetentionMedia(t, st, library.ID, "holdauth", "Hold Candidate")
	crossMedia := importRetentionMedia(t, st, other.ID, "holdcross", "Cross Candidate")

	rule, err := st.CreateRetentionRule(ctx, RetentionRule{
		SourceExpr:          `{"kind":"all-items"}`,
		TargetLibraryID:     library.ID,
		GraceDays:           2,
		RevealedAtAuthoring: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateRetentionRule: %v", err)
	}
	if _, _, err := st.ApplyRetentionRules(ctx, now); err != nil {
		t.Fatalf("seed candidates: %v", err)
	}

	assertNoCandidateEntry := func(ruleID string, mediaID MediaID) {
		t.Helper()
		candidates, err := st.retentionCandidates(ctx, ruleID)
		if err != nil {
			t.Fatalf("retentionCandidates(%s): %v", ruleID, err)
		}
		if _, ok := candidates[mediaID]; ok {
			t.Fatalf("refused hold created candidate state for %s under %s", mediaID, ruleID)
		}
	}
	assertNotHeld := func(ruleID string, mediaID MediaID) {
		t.Helper()
		candidates, err := st.retentionCandidates(ctx, ruleID)
		if err != nil {
			t.Fatalf("retentionCandidates(%s): %v", ruleID, err)
		}
		if !candidates[mediaID].HeldAt.IsZero() {
			t.Fatalf("refused hold marked %s held under %s", mediaID, ruleID)
		}
	}

	// Unknown rule IDs refuse with the rule-not-found error and mint nothing.
	if err := st.SetRetentionCandidateHold(ctx, "retention-missing", media.ID, true, filter, now); !errors.Is(err, ErrSmartCollectionNotFound) {
		t.Fatalf("unknown-rule hold err = %v, want ErrSmartCollectionNotFound", err)
	}
	assertNoCandidateEntry("retention-missing", media.ID)
	// Unknown media IDs refuse with the same not-found the read paths use.
	if err := st.SetRetentionCandidateHold(ctx, rule.ID, "media-missing", true, filter, now); !errors.Is(err, ValidationError("media not found")) {
		t.Fatalf("unknown-media hold err = %v, want media not found", err)
	}
	assertNoCandidateEntry(rule.ID, "media-missing")
	// A caller whose filter cannot see the target Library gets the identical
	// refusal for a real candidate — no existence oracle, no held state.
	outOfFilter := LibraryFilter{AllowedIDs: []LibraryID{other.ID}, Revealed: true}
	if err := st.SetRetentionCandidateHold(ctx, rule.ID, media.ID, true, outOfFilter, now); !errors.Is(err, ValidationError("media not found")) {
		t.Fatalf("out-of-filter hold err = %v, want media not found", err)
	}
	assertNotHeld(rule.ID, media.ID)
	// Visible media in another Library than the rule target refuses too.
	if err := st.SetRetentionCandidateHold(ctx, rule.ID, crossMedia.ID, true, filter, now); !errors.Is(err, ValidationError("media not found")) {
		t.Fatalf("cross-Library hold err = %v, want media not found", err)
	}
	assertNoCandidateEntry(rule.ID, crossMedia.ID)
	// Visible media in the target Library but outside the rule's source set is
	// a non-candidate: refused without creating state.
	narrow, err := st.CreateRetentionRule(ctx, RetentionRule{
		SourceExpr:          `{"kind":"watched-by-any-profile","value":"any"}`,
		TargetLibraryID:     library.ID,
		GraceDays:           2,
		RevealedAtAuthoring: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateRetentionRule narrow: %v", err)
	}
	if err := st.SetRetentionCandidateHold(ctx, narrow.ID, media.ID, true, filter, now); !errors.Is(err, ValidationError("media is not a retention candidate")) {
		t.Fatalf("non-candidate hold err = %v, want retention-candidate refusal", err)
	}
	assertNoCandidateEntry(narrow.ID, media.ID)

	// The authorized hold lands, is distinct from the Favorite-class keep bit,
	// exempts the due sweep, and once cleared the sweep proceeds.
	if err := st.SetRetentionCandidateHold(ctx, rule.ID, media.ID, true, filter, now); err != nil {
		t.Fatalf("valid hold: %v", err)
	}
	if kept, err := st.MediaFavorited(ctx, "profile-default", media.ID); err != nil || kept {
		t.Fatalf("operator hold leaked into keep bit: kept=%v err=%v", kept, err)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(3*24*time.Hour)); err != nil || skipped || trashed != 0 {
		t.Fatalf("held ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	if _, err := st.Media(ctx, media.ID, filter); err != nil {
		t.Fatalf("held media hidden: %v", err)
	}
	if err := st.SetRetentionCandidateHold(ctx, rule.ID, media.ID, false, filter, now.Add(3*24*time.Hour)); err != nil {
		t.Fatalf("clear hold: %v", err)
	}
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(4*24*time.Hour)); err != nil || skipped || trashed != 1 {
		t.Fatalf("due ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
}

// TestRetentionKeepPerProfileIndependenceAndMyDataDelete locks the ADR-0051
// §2.5 viewer-rescue contract: keep is the per-Profile Favorite-class bit, two
// Profiles keep and unkeep independently, any one Profile's keep exempts the
// item from the autonomous sweep, and a Profile's My Data deletion removes
// that Profile's keep (after which retention resumes).
//
// Red-first evidence (2026-08-20): with the sweep's mediaFavoritedByAny
// consult deliberately removed from ApplyRetentionRules, this test failed with
// "kept ApplyRetentionRules trashed=1 skipped=false err=<nil>"; the exemption
// was then restored and the test passed.
func TestRetentionKeepPerProfileIndependenceAndMyDataDelete(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Retention Keep Profiles")
	filter := LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	media := importRetentionMedia(t, st, library.ID, "keep2", "Two Profile Keep")

	rule, err := st.CreateRetentionRule(ctx, RetentionRule{
		SourceExpr:          `{"kind":"all-items"}`,
		TargetLibraryID:     library.ID,
		GraceDays:           2,
		RevealedAtAuthoring: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateRetentionRule: %v", err)
	}
	if _, _, err := st.ApplyRetentionRules(ctx, now); err != nil {
		t.Fatalf("seed candidates: %v", err)
	}

	// Two Profiles keep independently; clearing one never clears the other.
	if err := st.SetMediaKeep(ctx, "profile-default", media.ID, true, filter); err != nil {
		t.Fatalf("profile-default keep: %v", err)
	}
	if err := st.SetMediaKeep(ctx, "profile-managed", media.ID, true, filter); err != nil {
		t.Fatalf("profile-managed keep: %v", err)
	}
	if err := st.SetMediaKeep(ctx, "profile-default", media.ID, false, filter); err != nil {
		t.Fatalf("profile-default unkeep: %v", err)
	}
	if kept, err := st.MediaFavorited(ctx, "profile-managed", media.ID); err != nil || !kept {
		t.Fatalf("profile-managed keep = %v err=%v, want true after sibling unkeep", kept, err)
	}
	if kept, err := st.MediaFavorited(ctx, "profile-default", media.ID); err != nil || kept {
		t.Fatalf("profile-default keep = %v err=%v, want false after unkeep", kept, err)
	}
	// The keep bit never leaks into the rule-scoped operator hold.
	candidates, err := st.retentionCandidates(ctx, rule.ID)
	if err != nil {
		t.Fatalf("retentionCandidates: %v", err)
	}
	if !candidates[media.ID].HeldAt.IsZero() {
		t.Fatal("viewer keep marked the candidate operator-held")
	}
	// The one remaining Profile keep exempts the item from the due sweep.
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(3*24*time.Hour)); err != nil || skipped || trashed != 0 {
		t.Fatalf("kept ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
	// My Data deletion for the keeping Profile removes that Profile's keep.
	if err := st.DeleteMyEngagementAudited(ctx, "profile-managed", MyDataDeleteAll, "", filter, ActivityEntry{
		OccurredAt: now.Add(3 * 24 * time.Hour),
		Action:     ActivityMyDataDelete,
		ProfileID:  "profile-managed",
		TargetType: "all",
	}); err != nil {
		t.Fatalf("DeleteMyEngagementAudited: %v", err)
	}
	if kept, err := st.MediaFavorited(ctx, "profile-managed", media.ID); err != nil || kept {
		t.Fatalf("keep survived My Data deletion: kept=%v err=%v", kept, err)
	}
	// With no keep left, ordinary retention resumes.
	if trashed, skipped, err := st.ApplyRetentionRules(ctx, now.Add(4*24*time.Hour)); err != nil || skipped || trashed != 1 {
		t.Fatalf("post-deletion ApplyRetentionRules trashed=%d skipped=%v err=%v", trashed, skipped, err)
	}
}

// TestRetentionKeepDoesNotBlockManagerTrash locks the ADR-0051 override: a
// manager hard-delete (explicit audited trash) proceeds over a Profile keep —
// keep only exempts the AUTONOMOUS sweep.
//
// Red-first evidence (2026-08-20): with TrashMedia deliberately made to refuse
// kept media, this test failed with "TrashMedia over keep: media is kept"; the
// override was then restored and the test passed.
func TestRetentionKeepDoesNotBlockManagerTrash(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	library := createRetentionLibrary(t, st, "Retention Keep Override")
	filter := LibraryFilter{AllowedIDs: []LibraryID{library.ID}, Revealed: true}
	media := importRetentionMedia(t, st, library.ID, "keepoverride", "Kept But Trashed")

	if err := st.SetMediaKeep(ctx, "profile-default", media.ID, true, filter); err != nil {
		t.Fatalf("SetMediaKeep: %v", err)
	}
	if err := st.TrashMedia(ctx, media.ID, filter); err != nil {
		t.Fatalf("TrashMedia over keep: %v", err)
	}
	if _, err := st.Media(ctx, media.ID, filter); err == nil {
		t.Fatal("kept media still visible after explicit manager trash")
	}
}

func createRetentionLibrary(t *testing.T, st *Store, title string) Library {
	t.Helper()
	library, err := st.CreateLibrary(context.Background(), title, false, "", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	return library
}

func importRetentionMedia(t *testing.T, st *Store, libraryID LibraryID, fp, title string) Media {
	t.Helper()
	media, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:           libraryID,
		Path:                "/media/local/" + fp + ".mp4",
		Title:               title,
		Container:           "mp4",
		Codec:               "h264",
		ContentFingerprint:  "sha256:" + fp + "-retention",
		Class:               MediaClassVideo,
		DurationSecondsHint: 120,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return media
}

func strPtr(value string) *string {
	return &value
}
