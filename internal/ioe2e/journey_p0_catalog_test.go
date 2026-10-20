//go:build io_e2e

package ioe2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/apptest/plugintest"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// scenario:reveal-leak-to-child
// io:reveal-leak-to-child
func TestP0_reveal_leak_to_child(t *testing.T) {
	st := storetest.Open(t)
	h := certJourneyHandler(t, st)
	ctx := context.Background()

	openLib, ids := seedJourneyCertifiedLibrary(t, st, "Open Entries", false, map[string]string{
		"low":  "Open Low",
		"high": "Open High",
	})
	_, restricted := seedJourneyCertifiedLibrary(t, st, "Restricted Entries", true, map[string]string{
		"low": "Restricted Low",
	})
	if _, err := st.SetProfileLibraries(ctx, "profile-managed", []store.LibraryID{"library-local", openLib}); err != nil {
		t.Fatal(err)
	}

	revealed := postJourneyGraph(t, h, authorizeJourneyRevealed,
		`query($kind:String!){ sourceItems(source:{kind:$kind}) { id title } }`,
		map[string]any{"kind": "all-items"})
	if !strings.Contains(revealed.body, "Restricted Low") {
		t.Fatalf("revealed adult did not see restricted fixture before switch: %s", revealed.body)
	}
	switched := postJourneyGraph(t, h, authorizeJourneyRevealed,
		`mutation($profileID:String!){ switchProfile(profileID:$profileID) { profileID revealed } }`,
		map[string]any{"profileID": "profile-managed"})
	switched.mustHaveNoErrors(t)
	afterSwitch := postJourneyGraph(t, h, authorizeJourneyRevealed,
		`query($kind:String!){ sourceItems(source:{kind:$kind}) { id title } }`,
		map[string]any{"kind": "all-items"})
	if strings.Contains(afterSwitch.body, "Restricted Low") {
		t.Fatalf("restricted media leaked after profile switch/relock: %s", afterSwitch.body)
	}

	beforeCeiling := postJourneyGraph(t, h, authorizeJourneyManaged,
		`query($kind:String!){ sourceItems(source:{kind:$kind}) { id title } }`,
		map[string]any{"kind": "all-items"})
	if !strings.Contains(beforeCeiling.body, "Open High") {
		t.Fatalf("managed profile setup did not expose pre-ceiling high item %s: %s", ids["high"], beforeCeiling.body)
	}
	if err := st.SetSessionAdminUntil(ctx, "session-manager", time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	ceiling := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($profileID:String!,$pluginID:String!,$ceiling:String!){ setCertificationCeiling(profileID:$profileID) { id certificationCeilings { pluginID ceiling } } }`,
		map[string]any{"profileID": "profile-managed", "pluginID": "starlog", "ceiling": "low"})
	ceiling.mustHaveNoErrors(t)
	afterCeiling := postJourneyGraph(t, h, authorizeJourneyManaged,
		`query($kind:String!){ sourceItems(source:{kind:$kind}) { id title } }`,
		map[string]any{"kind": "all-items"})
	if !strings.Contains(afterCeiling.body, "Open Low") || strings.Contains(afterCeiling.body, "Open High") {
		t.Fatalf("same managed session did not apply certification ceiling coherently: %s", afterCeiling.body)
	}
	if strings.Contains(afterCeiling.body, "Restricted Low") || !strings.Contains(afterCeiling.body, string(ids["low"])) {
		t.Fatalf("ceiling/reveal result has wrong visibility set: %s", afterCeiling.body)
	}
	_ = restricted
}

// scenario:pair-auth-bypass
// io:pair-auth-bypass
func TestP0_pair_auth_bypass(t *testing.T) {
	st := storetest.Open(t)
	h := basicJourneyHandler(st)
	ctx := context.Background()
	keep := importJourneyMedia(t, st, "library-local", "/tmp/keep.mp4", "Keep", "dup-os")
	drop := importJourneyMedia(t, st, "library-local", "/tmp/drop.mp4", "Drop", "dup-os")

	for name, authz := range map[string]func(*http.Request){
		"unpaired": nil,
		"viewer":   authorizeJourneyViewer,
		"badSecret": func(req *http.Request) {
			authorizeJourneyManager(req)
			req.Header.Set("X-Beamfall-Device-Secret", "wrong")
		},
	} {
		resp := postJourneyGraph(t, h, authz,
			`mutation($libraryID:String!,$keepMediaID:String!){ mergeMedia(libraryID:$libraryID, keepMediaID:$keepMediaID) { affected } }`,
			map[string]any{"libraryID": "library-local", "keepMediaID": string(keep)})
		if len(resp.Errors) == 0 {
			t.Fatalf("%s unauthorized merge succeeded: %s", name, resp.body)
		}
	}
	if _, err := st.Media(ctx, drop, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
		t.Fatalf("unauthorized manager mutation changed catalog: %v", err)
	}

	lib, err := st.CreateLibrary(ctx, "Console-only", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	console := postJourneyGraph(t, h, authorizeJourneyManager,
		`mutation($profileID:String!,$libraryIDs:[String!]!){ setProfileLibraries(profileID:$profileID, libraryIDs:$libraryIDs) { id libraryIDs } }`,
		map[string]any{"profileID": "profile-managed", "libraryIDs": []string{"library-local", string(lib.ID)}})
	if len(console.Errors) == 0 {
		t.Fatalf("manager without admin session reached Console mutation: %s", console.body)
	}
	profile, err := st.Profile(ctx, "profile-managed")
	if err != nil {
		t.Fatal(err)
	}
	if containsLibrary(profile.Library, lib.ID) {
		t.Fatalf("Console mutation changed profile libraries without Admin Session: %#v", profile.Library)
	}
}

// scenario:scan-media-identity-stability
// io:scan-media-identity-stability
// io:fs-scan-walk
func TestP0_scan_media_identity_stability_io_fs_scan_walk(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Scan Identity", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := writeSettledJourneyFile(t, root, "item.mp4", []byte("stable media bytes"))

	first, err := scan.Scan(ctx, st, nil, nil, nil, nil, nil, scan.Request{LibraryID: lib.ID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if first.MediaImported != 1 || len(first.Files) != 1 {
		t.Fatalf("first scan = %+v, want one imported media", first)
	}
	mediaID := first.Files[0].MediaID
	fp := first.Files[0].ContentFingerprint
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: "profile-default", MediaID: mediaID, PositionSeconds: 42, DurationSeconds: 120,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", mediaID, true, store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	second, err := scan.Scan(ctx, st, nil, nil, nil, nil, nil, scan.Request{LibraryID: lib.ID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if second.MediaImported != 1 || len(second.Files) != 1 || second.Files[0].MediaID != mediaID || second.Files[0].ContentFingerprint != fp {
		t.Fatalf("rescan changed fingerprint identity: first=%+v second=%+v", first, second)
	}
	items, err := st.SourceItems(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}}, store.SourceQuery{Kind: "all-items", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || store.MediaID(items[0].ID) != mediaID {
		t.Fatalf("rescan duplicated/orphaned media: %#v", items)
	}
	progress, ok, err := st.PlaybackProgressFor(ctx, "profile-default", mediaID)
	if err != nil || !ok || progress.PositionSeconds != 42 {
		t.Fatalf("My-Data playback progress lost after rescan: progress=%+v ok=%v err=%v", progress, ok, err)
	}
	export, err := st.ExportMyEngagement(ctx, "profile-default", store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(export.Favorites) != 1 || export.Favorites[0].MediaID != mediaID {
		t.Fatalf("My-Data favorite lost after rescan: %#v", export.Favorites)
	}
}

// scenario:audited-delete-bypass
// io:audited-delete-bypass
// io:fs-trash-move
func TestP0_audited_delete_bypass_io_fs_trash_move(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sub, oldID, newID := seedRetentionJourney(t, st)
	one := 1
	if _, err := st.SetSubscriptionRetention(ctx, sub.ID, store.RetentionKeepLastN, &one); err != nil {
		t.Fatal(err)
	}

	trashed, skipped, err := st.ApplySubscriptionRetentionPolicies(ctx)
	if !errors.Is(err, store.ErrTrashDisabledConfirmationRequired) || !skipped || trashed != 0 || st.TrashDisabledSweepSkips() != 1 {
		t.Fatalf("autonomous retention with trash disabled = trashed %d skipped %v skips %d err %v, want confirmation-required skip", trashed, skipped, st.TrashDisabledSweepSkips(), err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	for _, id := range []store.MediaID{oldID, newID} {
		if _, err := st.Media(ctx, id, filter); err != nil {
			t.Fatalf("trash-disabled autonomous op hard-deleted or hid %s: %v", id, err)
		}
	}
	if err := st.TrashMedia(ctx, oldID, filter); err != nil {
		t.Fatalf("interactive audited trash should remain available with trash disabled: %v", err)
	}
	trashedList, err := st.TrashedMediaList(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(trashedList) != 1 || trashedList[0].ID != oldID {
		t.Fatalf("interactive delete did not route through audited trash: %#v", trashedList)
	}
}

type journeyGraphResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	body string
}

func (r journeyGraphResponse) mustHaveNoErrors(t *testing.T) {
	t.Helper()
	if len(r.Errors) != 0 {
		t.Fatalf("graphql errors: %+v\n%s", r.Errors, r.body)
	}
}

func basicJourneyHandler(st *store.Store) *graph.Handler {
	return graph.NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, nil),
		auth.NewService(st),
		st,
		nil,
	)
}

func certJourneyHandler(t *testing.T, st *store.Store) *graph.Handler {
	t.Helper()
	registry, err := plugintest.Registry([]plugin.Bundle{{
		ID:                 "starlog",
		Version:            "0.1.0",
		DisplayName:        plugin.L("Starlog"),
		MediaLabel:         plugin.L("Entry"),
		MediaFields:        []plugin.FieldSpec{{Name: "certification", Type: "text"}},
		CertificationScale: []string{"low", "mid", "high"},
		Layouts:            []plugin.LayoutSpec{{ID: "home", Role: "browse"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.NewService(st)
	authService.SetCertificationScales(map[string][]string{"starlog": []string{"low", "mid", "high"}})
	return graph.NewHandler(
		manifest.NewService(st, func() time.Time { return time.Unix(0, 0).UTC() }, registry),
		authService,
		st,
		registry,
	)
}

func postJourneyGraph(t *testing.T, h *graph.Handler, authorize func(*http.Request), query string, variables map[string]any) journeyGraphResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	if authorize != nil {
		authorize(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out journeyGraphResponse
	out.body = rec.Body.String()
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode graphql response status=%d: %v\n%s", rec.Code, err, out.body)
	}
	return out
}

func authorizeJourneyViewer(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-default")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func authorizeJourneyRevealed(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-revealed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func authorizeJourneyManaged(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

func authorizeJourneyManager(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-manager")
	req.Header.Set("X-Beamfall-Device", "device-mac")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-manager-secret")
}

func seedJourneyCertifiedLibrary(t *testing.T, st *store.Store, title string, restricted bool, entries map[string]string) (store.LibraryID, map[string]store.MediaID) {
	t.Helper()
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, title, restricted, "starlog", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]store.MediaID{}
	for cert, itemTitle := range entries {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:          lib.ID,
			Path:               "/journey/" + strings.ToLower(strings.ReplaceAll(itemTitle, " ", "-")) + ".mp4",
			Title:              itemTitle,
			Container:          "mp4",
			Codec:              "h264",
			Class:              store.MediaClassVideo,
			ContentFingerprint: "sha256:" + strings.ToLower(strings.ReplaceAll(itemTitle, " ", "-")),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{{Name: "certification", Type: store.EntityFieldText, Text: cert}}); err != nil {
			t.Fatal(err)
		}
		ids[cert] = media.ID
	}
	return lib.ID, ids
}

func importJourneyMedia(t *testing.T, st *store.Store, libraryID, path, title, oshash string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID:          store.LibraryID(libraryID),
		Path:               path,
		Title:              title,
		Container:          "mp4",
		Codec:              "h264",
		Class:              store.MediaClassVideo,
		ContentFingerprint: "sha256:" + strings.ToLower(title),
		OSHash:             oshash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media.ID
}

func writeSettledJourneyFile(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedRetentionJourney(t *testing.T, st *store.Store) (store.Subscription, store.MediaID, store.MediaID) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	sub, err := st.AddSubscription(ctx, store.Subscription{
		LibraryID: "library-local",
		FeedURL:   "https://feeds.example.com/p0-retention.xml",
		Root:      "/media/local/p0-retention",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := "/media/local/p0-retention/old.mp3"
	newPath := "/media/local/p0-retention/new.mp3"
	oldMedia := importRetentionMedia(t, st, oldPath, "P0 Old", "sha256:p0-retention-old")
	newMedia := importRetentionMedia(t, st, newPath, "P0 New", "sha256:p0-retention-new")
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "old", oldPath, now); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSubscriptionEntrySeen(ctx, sub.ID, "new", newPath, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ReconcileSubscriptionSeenEntries(ctx); err != nil {
		t.Fatal(err)
	}
	return sub, oldMedia, newMedia
}

func importRetentionMedia(t *testing.T, st *store.Store, path, title, fingerprint string) store.MediaID {
	t.Helper()
	media, _, err := st.ImportMediaFile(t.Context(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               path,
		Title:              title,
		Container:          "mp3",
		Codec:              "mp3",
		Class:              store.MediaClassAudio,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media.ID
}

func containsLibrary(ids []store.LibraryID, want store.LibraryID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
