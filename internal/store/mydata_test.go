package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// profileDefaultFilter is the LibraryFilter for the seeded adult Profile that
// can see both the local and restricted Archive Libraries. Revealed toggles
// the reveal state — relocked hides library-archive, revealed shows it.
func profileDefaultFilter(revealed bool) LibraryFilter {
	return LibraryFilter{
		AllowedIDs: []LibraryID{"library-local", "library-archive"},
		Revealed:   revealed,
	}
}

// seedEngagement records playback progress, a favorite, and a queue entry for
// one Media item so the My Data export/delete paths have rows to operate on.
func seedEngagement(t *testing.T, st *Store, mediaID MediaID) {
	t.Helper()
	ctx := context.Background()
	// Use a revealed filter for the writes so restricted Media can be seeded;
	// the export/delete reads then exercise reveal filtering independently.
	revealed := profileDefaultFilter(true)
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID:       "profile-default",
		MediaID:         mediaID,
		PositionSeconds: 30,
		DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatalf("seed progress %s: %v", mediaID, err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", mediaID, true, revealed); err != nil {
		t.Fatalf("seed favorite %s: %v", mediaID, err)
	}
	if err := st.QueueAdd(ctx, "profile-default", mediaID, nil, revealed); err != nil {
		t.Fatalf("seed queue %s: %v", mediaID, err)
	}
}

func TestExportMyEngagementRevealFilters(t *testing.T) {
	// feature:my-data-panel
	st := openSeeded(t)
	ctx := context.Background()
	seedEngagement(t, st, "media-alpha")   // visible (library-local)
	seedEngagement(t, st, "media-archive") // restricted (library-archive)

	// Relocked: the restricted-Library rows are hidden from the export.
	relocked, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(false))
	if err != nil {
		t.Fatal(err)
	}
	if got := mediaIDsInExport(relocked); len(got) != 1 || got[0] != "media-alpha" {
		t.Fatalf("relocked export media = %v, want only media-alpha", got)
	}

	// Revealed: a revealed session exports everything (Reveal contract
	// outranks completeness only when relocked).
	revealed, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(true))
	if err != nil {
		t.Fatal(err)
	}
	got := mediaIDsInExport(revealed)
	if len(got) != 2 {
		t.Fatalf("revealed export media = %v, want media-alpha + media-archive", got)
	}
	if len(revealed.PlaybackProgress) != 2 || len(revealed.Favorites) != 2 || len(revealed.Queue) != 2 {
		t.Fatalf("revealed export not complete: %#v", revealed)
	}
}

func TestExportMyEngagementDisclosesSealedLibraryExclusions(t *testing.T) {
	// feature:my-data-panel feature:encrypted-library
	st := openSeeded(t)
	ctx := context.Background()
	seedEngagement(t, st, "media-alpha")

	filter := LibraryFilter{
		AllowedIDs: []LibraryID{"library-local"},
		SealedIDs:  []LibraryID{"library-local"},
	}
	export, err := st.ExportMyEngagement(ctx, "profile-default", filter)
	if err != nil {
		t.Fatal(err)
	}
	if export.Complete {
		t.Fatal("sealed-library export reported complete")
	}
	if len(export.ExcludedLibraries) != 1 || export.ExcludedLibraries[0].LibraryID != "library-local" {
		t.Fatalf("excluded libraries = %#v, want library-local", export.ExcludedLibraries)
	}
	if got := mediaIDsInExport(export); len(got) != 0 {
		t.Fatalf("sealed-library export media = %v, want none", got)
	}
}

// TestExportMyEngagementCarriesProfileCreatedAt pins ADR-0036 §2.7: the export
// surfaces the Profile creation timestamp. A Profile minted via CreateProfile
// carries a non-zero created_at_ms; a seeded (raw-inserted, pre-0069-shaped)
// Profile reports 0, which the graph wire then omits. feature:my-data-panel
func TestExportMyEngagementCarriesProfileCreatedAt(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	// Seeded profile-default predates the column path (raw INSERT) → 0.
	seeded, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(false))
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Profile.CreatedAtMs != 0 {
		t.Fatalf("seeded profile CreatedAtMs = %d, want 0", seeded.Profile.CreatedAtMs)
	}

	// A freshly created Profile records its creation time.
	p, err := st.CreateProfile(ctx, "Fresh", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := st.ExportMyEngagement(ctx, p.ID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Profile.CreatedAtMs <= 0 {
		t.Fatalf("fresh profile CreatedAtMs = %d, want > 0", fresh.Profile.CreatedAtMs)
	}
	if fresh.Profile.CreatedAtMs != p.CreatedAtMs {
		t.Fatalf("export CreatedAtMs = %d, want %d (CreateProfile echo)", fresh.Profile.CreatedAtMs, p.CreatedAtMs)
	}
}

func TestDeleteMyEngagementHiddenIDFailsLikeUnknown(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	seedEngagement(t, st, "media-archive") // restricted

	relocked := profileDefaultFilter(false)
	// Hidden id (restricted Library, relocked) must fail.
	hiddenErr := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteMedia, "media-archive", relocked)
	if hiddenErr == nil {
		t.Fatal("delete of hidden media succeeded — existence oracle")
	}
	// Unknown id must fail identically (same error value).
	unknownErr := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteMedia, "media-does-not-exist", relocked)
	if unknownErr == nil {
		t.Fatal("delete of unknown media succeeded")
	}
	if hiddenErr.Error() != unknownErr.Error() {
		t.Fatalf("hidden vs unknown delete error differ: %q vs %q", hiddenErr.Error(), unknownErr.Error())
	}

	// The hidden row must still exist (the failed delete left it intact); a
	// revealed export still sees it.
	revealed, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(revealed.PlaybackProgress) != 1 {
		t.Fatalf("hidden row was deleted by failed delete: %#v", revealed)
	}
}

func TestDeleteMyEngagementAllRemovesHiddenRows(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	seedEngagement(t, st, "media-alpha")
	seedEngagement(t, st, "media-archive")

	// `all` deletes from a RELOCKED session and still removes the hidden
	// (restricted) rows — deletion is not disclosure, so no reveal is required.
	if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteAll, "", profileDefaultFilter(false)); err != nil {
		t.Fatal(err)
	}
	// A revealed export now sees nothing — every row across all three tables
	// is gone, including the hidden-media ones.
	revealed, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(revealed.PlaybackProgress) != 0 || len(revealed.Favorites) != 0 || len(revealed.Queue) != 0 {
		t.Fatalf("`all` left rows behind: %#v", revealed)
	}
}

func TestDeleteMyEngagementMediaHardDeletesAllTables(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	seedEngagement(t, st, "media-alpha")
	seedEngagement(t, st, "media-archive")

	// Delete one visible item; the other survives.
	if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteMedia, "media-alpha", profileDefaultFilter(false)); err != nil {
		t.Fatal(err)
	}
	// Progress projection gone for media-alpha across all three tables.
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); err != nil || ok {
		t.Fatalf("media-alpha progress still present: ok=%v err=%v", ok, err)
	}
	if fav, err := st.MediaFavorited(ctx, "profile-default", "media-alpha"); err != nil || fav {
		t.Fatalf("media-alpha favorite still present: fav=%v err=%v", fav, err)
	}
	revealed, err := st.ExportMyEngagement(ctx, "profile-default", profileDefaultFilter(true))
	if err != nil {
		t.Fatal(err)
	}
	got := mediaIDsInExport(revealed)
	if len(got) != 1 || got[0] != "media-archive" {
		t.Fatalf("after media delete, export media = %v, want only media-archive", got)
	}
}

func TestDeleteMyEngagementProfileIsolation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	// Two profiles each have engagement on the same visible Media.
	visible := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	for _, profile := range []ProfileID{"profile-default", "profile-managed"} {
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID:       profile,
			MediaID:         "media-alpha",
			PositionSeconds: 30,
			DurationSeconds: 120,
		}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatalf("seed %s: %v", profile, err)
		}
	}
	// Deleting profile-default's data leaves profile-managed's intact: the
	// store method only ever touches the profile id it is handed (the graph
	// layer always hands it scope.ProfileID — no cross-profile path exists).
	if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteAll, "", visible); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); ok {
		t.Fatal("profile-default row survived its own delete")
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, "profile-managed", "media-alpha"); !ok {
		t.Fatal("profile-default delete clobbered profile-managed's data")
	}
}

func TestMyProfileSessionsListsOwnSessions(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	sessions, err := st.MyProfileSessions(ctx, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	// The seed binds session-default, session-revealed, session-manager, and
	// session-revoked to profile-default. Every returned session belongs to
	// the asked profile and carries its Device name + role.
	if len(sessions) == 0 {
		t.Fatal("no sessions returned for profile-default")
	}
	foundManager := false
	for _, s := range sessions {
		if s.DeviceName == "" || s.DeviceRole == "" {
			t.Fatalf("session %s missing device name/role: %#v", s.ID, s)
		}
		if s.ID == "session-manager" {
			foundManager = true
			if s.DeviceRole != DeviceRoleManager || s.DeviceName != "Manager" {
				t.Fatalf("session-manager device wrong: %#v", s)
			}
		}
	}
	if !foundManager {
		t.Fatal("session-manager not in profile-default sessions")
	}
	// A different profile's sessions never leak in (managed profile has its own).
	managed, err := st.MyProfileSessions(ctx, "profile-managed")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range managed {
		for _, d := range sessions {
			if s.ID == d.ID {
				t.Fatalf("session %s appears under two profiles", s.ID)
			}
		}
	}
}

func TestMyStreamTokensShapeIdenticalForHiddenMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)

	// One token for visible Media, one for hidden (restricted) Media — same
	// session, same profile, same target kind. The My Data read must return
	// shape-identical rows: target kind + expiry only, no media id.
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true, future); err != nil {
		t.Fatal(err)
	}
	tokens, err := st.MyStreamTokens(ctx, "profile-default", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Fatalf("token count = %d, want 2", len(tokens))
	}
	// Both rows have the same observable shape — only their count differs from
	// the hidden case (there is no field that identifies the Media).
	for _, tok := range tokens {
		if tok.TargetKind != "media-direct" {
			t.Fatalf("token target kind = %q, want media-direct", tok.TargetKind)
		}
		if tok.ExpiresAt.IsZero() {
			t.Fatal("token expiry missing")
		}
	}
}

func TestMyStreamTokensDropsExpiredAndUpserts(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	// An already-expired token is never reported.
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Re-issuing the same (session, target, media) grant upserts expiry rather
	// than adding a duplicate row.
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tokens, err := st.MyStreamTokens(ctx, "profile-default", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Fatalf("token count = %d, want 1 (upsert, not duplicate)", len(tokens))
	}
	if !tokens[0].ExpiresAt.After(now) {
		t.Fatalf("token expiry %v not refreshed past now %v", tokens[0].ExpiresAt, now)
	}
}

func collectionAuthor(t *testing.T, st *Store, id SmartCollectionID) (string, bool) {
	t.Helper()
	var author sql.NullString
	if err := st.catalogR.QueryRowContext(context.Background(),
		`SELECT created_by_profile_id FROM smart_collection WHERE id = ?`, id).Scan(&author); err != nil {
		t.Fatalf("read collection author %s: %v", id, err)
	}
	return author.String, author.Valid
}

func markerAuthor(t *testing.T, st *Store, id MarkerID) (string, bool) {
	t.Helper()
	var author sql.NullString
	if err := st.catalogR.QueryRowContext(context.Background(),
		`SELECT created_by_profile_id FROM marker WHERE id = ?`, id).Scan(&author); err != nil {
		t.Fatalf("read marker author %s: %v", id, err)
	}
	return author.String, author.Valid
}

// TestMyDataFullFootprint exercises the full per-Profile footprint (ADR-0036
// §2.4/§2.7): export carries Profile metadata, Saved Filters, the Profile's
// authored Collections and Markers; delete removes the per-Profile artifacts
// (Saved Filters) while DISOWNING — not deleting — the shared Collection and
// Marker the Profile authored.
func TestMyDataFullFootprint(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	visible := profileDefaultFilter(true)

	// A per-Profile Saved Filter owned by profile-default.
	filter, err := st.CreateSavedFilter(ctx, SavedFilter{
		ProfileID:  "profile-default",
		LibraryID:  "library-local",
		Name:       "My Filter",
		FilterJSON: `{"tag":"x"}`,
	}, now)
	if err != nil {
		t.Fatalf("create saved filter: %v", err)
	}

	// A Collection authored by profile-default (per-Profile) and one authored
	// by another Profile (shared from profile-default's perspective).
	// Authorship is stamped at CREATE time via CreatedByProfile
	// — the production write path, not a test-only UPDATE — so this exercises
	// what the real create APIs persist.
	mine, err := st.CreateSmartCollection(ctx, SmartCollection{
		LibraryID: "library-local", Name: "Mine", SourceExpr: `{"a":1}`,
		CreatedByProfile: "profile-default",
	}, now)
	if err != nil {
		t.Fatalf("create my collection: %v", err)
	}
	if author, ok := collectionAuthor(t, st, mine.ID); !ok || author != "profile-default" {
		t.Fatalf("create did not stamp collection author: %q ok=%v", author, ok)
	}
	shared, err := st.CreateSmartCollection(ctx, SmartCollection{
		LibraryID: "library-local", Name: "Shared", SourceExpr: `{"b":2}`,
		CreatedByProfile: "profile-managed",
	}, now)
	if err != nil {
		t.Fatalf("create shared collection: %v", err)
	}

	// A Marker authored by profile-default and one authored by another Profile,
	// both stamped at create time.
	myMarker, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", Title: "Mine", StartSeconds: 5,
		CreatedByProfile: "profile-default",
	}, visible, now)
	if err != nil {
		t.Fatalf("create my marker: %v", err)
	}
	if author, ok := markerAuthor(t, st, myMarker.ID); !ok || author != "profile-default" {
		t.Fatalf("create did not stamp marker author: %q ok=%v", author, ok)
	}
	sharedMarker, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", Title: "Theirs", StartSeconds: 10,
		CreatedByProfile: "profile-managed",
	}, visible, now)
	if err != nil {
		t.Fatalf("create shared marker: %v", err)
	}
	if err := st.SetPluginProfileLink(ctx, "webhook", "profile-default", "acct-123", now); err != nil {
		t.Fatalf("set plugin profile link: %v", err)
	}

	// Export carries Profile metadata + Saved Filters + authored Collections +
	// authored Markers, and includes only the Profile's own authored artifacts.
	export, err := st.ExportMyEngagement(ctx, "profile-default", visible)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if export.Profile.ID != "profile-default" || export.Profile.Name != "Default" || export.Profile.Kind != string(ProfileAdult) {
		t.Fatalf("export profile metadata wrong: %#v", export.Profile)
	}
	if len(export.SavedFilters) != 1 || export.SavedFilters[0].Name != "My Filter" || export.SavedFilters[0].FilterJSON != `{"tag":"x"}` {
		t.Fatalf("export saved filters = %#v, want 1 (My Filter)", export.SavedFilters)
	}
	if len(export.Collections) != 1 || export.Collections[0].Name != "Mine" {
		t.Fatalf("export collections = %#v, want only the authored one", export.Collections)
	}
	if len(export.Markers) != 1 || export.Markers[0].Title != "Mine" {
		t.Fatalf("export markers = %#v, want only the authored one", export.Markers)
	}
	foundPluginConnection := false
	for _, conn := range export.Connections {
		if conn.Service == "webhook" && conn.AccountLabel == "acct-123" {
			foundPluginConnection = true
			break
		}
	}
	if !foundPluginConnection {
		t.Fatalf("export connections = %#v, want webhook acct-123 profile link", export.Connections)
	}

	// Delete-all: per-Profile artifacts (Saved Filters) removed; the authored
	// Collection and Marker are DISOWNED (created_by cleared), never deleted.
	if err := st.DeleteMyEngagement(ctx, "profile-default", MyDataDeleteAll, "", visible); err != nil {
		t.Fatalf("delete all: %v", err)
	}

	// Saved Filter gone.
	if _, err := st.SavedFilterByID(ctx, filter.ID); err == nil {
		t.Fatal("saved filter survived delete-all")
	}
	if ref, linked, err := st.PluginProfileLink(ctx, "webhook", "profile-default"); err != nil || linked || ref != "" {
		t.Fatalf("plugin profile link survived delete-all: ref=%q linked=%v err=%v", ref, linked, err)
	}

	// The authored Collection row SURVIVES, now disowned.
	if _, err := st.SmartCollectionByID(ctx, mine.ID); err != nil {
		t.Fatalf("authored collection was deleted, not disowned: %v", err)
	}
	if _, ok := collectionAuthor(t, st, mine.ID); ok {
		t.Fatal("authored collection still owned after delete — not disowned")
	}
	// The shared Collection keeps its (other-Profile) author untouched.
	if author, ok := collectionAuthor(t, st, shared.ID); !ok || author != "profile-managed" {
		t.Fatalf("shared collection author changed: %q ok=%v", author, ok)
	}

	// The authored Marker row SURVIVES, now disowned.
	if _, ok := markerAuthor(t, st, myMarker.ID); ok {
		t.Fatal("authored marker still owned after delete — not disowned")
	}
	// MarkersForMedia still returns both markers (neither was deleted).
	markers, err := st.MarkersForMedia(ctx, "media-alpha", visible)
	if err != nil {
		t.Fatalf("markers for media: %v", err)
	}
	if len(markers) != 2 {
		t.Fatalf("marker count after disown = %d, want 2 (no marker deleted)", len(markers))
	}
	// The other Profile's Marker keeps its author.
	if author, ok := markerAuthor(t, st, sharedMarker.ID); !ok || author != "profile-managed" {
		t.Fatalf("shared marker author changed: %q ok=%v", author, ok)
	}
}

// mediaIDsInExport collects the unique Media ids referenced anywhere in an
// export, for assertions about reveal filtering.
// TestDeleteProfileFootprintOperatorTarget exercises the ADR-0036 §2.5/§2.6
// operator/guardian footprint-delete: the store deletes a TARGET Profile's
// engagement (here profile-managed) with the SAME semantics as the §2.4
// self-delete — engagement projections + Saved Filters gone, server-global
// Markers/Collections DISOWNED not deleted — while leaving every OTHER Profile's
// data untouched. It is the MyDataDeleteAll path with the target parameterized.
func TestDeleteProfileFootprintOperatorTarget(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	visible := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

	// Seed engagement for BOTH the target (profile-managed) and a bystander
	// (profile-default) on the same visible Media.
	for _, profile := range []ProfileID{"profile-managed", "profile-default"} {
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: profile, MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120,
		}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatalf("seed progress %s: %v", profile, err)
		}
		if err := st.SetMediaFavorite(ctx, profile, "media-alpha", true, visible); err != nil {
			t.Fatalf("seed favorite %s: %v", profile, err)
		}
	}

	// A per-Profile Saved Filter owned by the target.
	filter, err := st.CreateSavedFilter(ctx, SavedFilter{
		ProfileID: "profile-managed", LibraryID: "library-local", Name: "Kid Filter", FilterJSON: `{"tag":"x"}`,
	}, now)
	if err != nil {
		t.Fatalf("create saved filter: %v", err)
	}
	// A SmartCollection + Marker authored by the target (server-global, must be
	// DISOWNED) and one authored by the bystander (must be untouched).
	targetCol, err := st.CreateSmartCollection(ctx, SmartCollection{
		LibraryID: "library-local", Name: "Kid Col", SourceExpr: `{"a":1}`, CreatedByProfile: "profile-managed",
	}, now)
	if err != nil {
		t.Fatalf("create target collection: %v", err)
	}
	bystanderCol, err := st.CreateSmartCollection(ctx, SmartCollection{
		LibraryID: "library-local", Name: "Adult Col", SourceExpr: `{"b":2}`, CreatedByProfile: "profile-default",
	}, now)
	if err != nil {
		t.Fatalf("create bystander collection: %v", err)
	}
	targetMarker, err := st.CreateMarker(ctx, Marker{
		MediaID: "media-alpha", Title: "Kid", StartSeconds: 5, CreatedByProfile: "profile-managed",
	}, visible, now)
	if err != nil {
		t.Fatalf("create target marker: %v", err)
	}
	targetPack, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-managed",
		DeviceID:   "device-managed",
		TargetKind: "media",
		TargetRef:  "media-alpha",
		State:      OfflinePackReady,
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, now)
	if err != nil {
		t.Fatalf("create target offline pack: %v", err)
	}
	if _, err := st.CreateOfflinePack(ctx, OfflinePack{
		ProfileID:  "profile-default",
		DeviceID:   "device-default",
		TargetKind: "media",
		TargetRef:  "media-alpha",
		State:      OfflinePackReady,
	}, []OfflinePackItem{{MediaID: "media-alpha"}}, now); err != nil {
		t.Fatalf("create bystander offline pack: %v", err)
	}
	targetReq, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: "profile-managed",
		MediaID:   "media-alpha",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("create target access request: %v", err)
	}
	if _, err := st.ResolveAccessRequest(ctx, targetReq.ID, "profile-default", AccessVerdictGranted, now.Add(time.Minute)); err != nil {
		t.Fatalf("resolve target access request: %v", err)
	}
	bystanderReq, err := st.CreateAccessRequest(ctx, AccessRequestInput{
		ProfileID: "profile-default",
		MediaID:   "media-alpha",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("create bystander access request: %v", err)
	}
	if _, err := st.ResolveAccessRequest(ctx, bystanderReq.ID, "profile-managed", AccessVerdictGranted, now.Add(time.Minute)); err != nil {
		t.Fatalf("resolve bystander access request: %v", err)
	}

	// Operator-delete the target's footprint.
	if err := st.DeleteProfileFootprint(ctx, "profile-managed"); err != nil {
		t.Fatalf("DeleteProfileFootprint: %v", err)
	}

	// Target engagement gone across the projection tables.
	if _, ok, _ := st.PlaybackProgressFor(ctx, "profile-managed", "media-alpha"); ok {
		t.Fatal("target progress survived operator delete")
	}
	if fav, _ := st.MediaFavorited(ctx, "profile-managed", "media-alpha"); fav {
		t.Fatal("target favorite survived operator delete")
	}
	// Target Saved Filter (per-Profile) hard-deleted.
	if _, err := st.SavedFilterByID(ctx, filter.ID); err == nil {
		t.Fatal("target saved filter survived operator delete")
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack WHERE profile_id = 'profile-managed'`); got != 0 {
		t.Fatalf("target offline_pack rows survived operator delete: %d", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack_item WHERE pack_id = '`+string(targetPack.ID)+`'`); got != 0 {
		t.Fatalf("target offline_pack_item rows survived operator delete: %d", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM access_request WHERE profile_id = 'profile-managed'`); got != 0 {
		t.Fatalf("target access_request rows survived operator delete: %d", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM access_grant WHERE profile_id = 'profile-managed'`); got != 0 {
		t.Fatalf("target access_grant rows survived operator delete: %d", got)
	}
	// Target's server-global Collection + Marker DISOWNED, not deleted.
	if _, err := st.SmartCollectionByID(ctx, targetCol.ID); err != nil {
		t.Fatalf("target collection deleted, not disowned: %v", err)
	}
	if _, ok := collectionAuthor(t, st, targetCol.ID); ok {
		t.Fatal("target collection still owned — not disowned")
	}
	if _, ok := markerAuthor(t, st, targetMarker.ID); ok {
		t.Fatal("target marker still owned — not disowned")
	}

	// Bystander (profile-default) data is entirely untouched.
	if _, ok, _ := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); !ok {
		t.Fatal("operator delete clobbered bystander progress")
	}
	if fav, _ := st.MediaFavorited(ctx, "profile-default", "media-alpha"); !fav {
		t.Fatal("operator delete clobbered bystander favorite")
	}
	if author, ok := collectionAuthor(t, st, bystanderCol.ID); !ok || author != "profile-default" {
		t.Fatalf("operator delete changed bystander collection author: %q ok=%v", author, ok)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM offline_pack WHERE profile_id = 'profile-default'`); got != 1 {
		t.Fatalf("operator delete clobbered bystander offline_pack rows: %d", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM access_request WHERE profile_id = 'profile-default'`); got != 1 {
		t.Fatalf("operator delete clobbered bystander access_request rows: %d", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM access_grant WHERE profile_id = 'profile-default'`); got != 1 {
		t.Fatalf("operator delete clobbered bystander access_grant rows: %d", got)
	}
}

// TestDeleteProfileFootprintUnknownTargetRefused pins that an unknown target id
// is a ValidationError, not a silent no-op — an operator-delete against a typo
// must fail loudly.
func TestDeleteProfileFootprintUnknownTargetRefused(t *testing.T) {
	st := openSeeded(t)
	if err := st.DeleteProfileFootprint(context.Background(), "profile-does-not-exist"); err == nil {
		t.Fatal("DeleteProfileFootprint of unknown profile succeeded — want ValidationError")
	}
}

func mediaIDsInExport(export MyEngagementExport) []MediaID {
	seen := map[MediaID]bool{}
	var ids []MediaID
	add := func(id MediaID) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, row := range export.PlaybackProgress {
		add(row.MediaID)
	}
	for _, row := range export.Favorites {
		add(row.MediaID)
	}
	for _, row := range export.Queue {
		add(row.MediaID)
	}
	return ids
}
