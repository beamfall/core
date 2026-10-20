package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

type systemSurfaceDisclosureResponse struct {
	MediaID                 string                `json:"mediaID"`
	Decision                string                `json:"decision"`
	Libraries               []struct{ ID string } `json:"libraries"`
	SystemSurfaceDisclosure struct {
		SnapshotVersion string `json:"snapshotVersion"`
		MetadataAllowed bool   `json:"metadataAllowed"`
	} `json:"systemSurfaceDisclosure"`
}

// TestSystemSurfaceDisclosureUsesAllProfilesWithoutReveal proves the system-
// surface authority is not derived from the requesting Profile alone. The
// requesting Profile keeps access to media-alpha throughout; adding another
// Profile with no Library grants changes both playback and manifest disclosure
// to false because the Media/manifest is no longer visible to every Profile.
func TestSystemSurfaceDisclosureUsesAllProfilesWithoutReveal(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("beamfall file bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), "media-alpha", path); err != nil {
		t.Fatal(err)
	}

	before := requestPlaybackDisclosure(t, srv, "media-alpha", authorizeViewer)
	if before.Decision != "direct-play" || before.MediaID != "media-alpha" {
		t.Fatalf("requesting Profile cannot play seeded Media before policy change: %#v", before)
	}
	assertSystemSurfaceDisclosure(t, before, true)
	assertManifestDisclosure(t, srv, true)

	if _, err := st.CreateProfile(
		context.Background(),
		"No Library Access",
		store.ProfileManaged,
		store.LibraryFilter{},
	); err != nil {
		t.Fatal(err)
	}

	after := requestPlaybackDisclosure(t, srv, "media-alpha", authorizeViewer)
	if after.Decision != "direct-play" || after.MediaID != "media-alpha" {
		t.Fatalf("requesting Profile lost access unexpectedly: %#v", after)
	}
	assertSystemSurfaceDisclosure(t, after, false)
	assertManifestDisclosure(t, srv, false)
}

// TestSystemSurfaceDisclosureNeverInheritsRequestReveal proves an unlocked
// requesting session cannot turn its local Reveal into system-surface
// authority. The restricted Media is playable in that session, but remains
// undisclosable because every Profile is evaluated with Reveal off.
func TestSystemSurfaceDisclosureNeverInheritsRequestReveal(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	path := filepath.Join(t.TempDir(), "restricted.bin")
	if err := os.WriteFile(path, []byte("restricted bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(context.Background(), "media-archive", path); err != nil {
		t.Fatal(err)
	}

	response := requestPlaybackDisclosure(t, srv, "media-archive", authorizeRevealed)
	if response.Decision != "direct-play" || response.MediaID != "media-archive" {
		t.Fatalf("revealed requesting Profile cannot play restricted Media: %#v", response)
	}
	assertSystemSurfaceDisclosure(t, response, false)
}

func requestPlaybackDisclosure(
	t *testing.T,
	srv *Server,
	mediaID string,
	authorize func(*http.Request),
) systemSurfaceDisclosureResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"mediaID": mediaID,
		"capabilityProfile": map[string]any{
			"client":       "beamfall-test",
			"containers":   []string{"mp4"},
			"directCodecs": []string{"h264"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
	authorize(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("playback-info status = %d: %s", rec.Code, rec.Body.String())
	}
	var response systemSurfaceDisclosureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func assertManifestDisclosure(t *testing.T, srv *Server, want bool) {
	t.Helper()
	req := newJSONRequest(http.MethodGet, "/api/manifest", nil)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest status = %d: %s", rec.Code, rec.Body.String())
	}
	var response systemSurfaceDisclosureResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Libraries) == 0 {
		t.Fatal("requesting Profile manifest unexpectedly has no Libraries")
	}
	assertSystemSurfaceDisclosure(t, response, want)
	assertGraphManifestDisclosure(t, srv, want)
}

func assertGraphManifestDisclosure(t *testing.T, srv *Server, want bool) {
	t.Helper()
	body := bytes.NewBufferString(`{"query":"query { navigationManifest { profileID libraries { id } systemSurfaceDisclosure { snapshotVersion metadataAllowed } } }"}`)
	req := newJSONRequest(http.MethodPost, "/api/graphql", body)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GraphQL navigationManifest status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data struct {
			NavigationManifest systemSurfaceDisclosureResponse `json:"navigationManifest"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Errors) != 0 {
		t.Fatalf("GraphQL navigationManifest errors = %s", rec.Body.String())
	}
	if len(response.Data.NavigationManifest.Libraries) == 0 {
		t.Fatal("requesting Profile GraphQL Navigation Manifest unexpectedly has no Libraries")
	}
	assertSystemSurfaceDisclosure(t, response.Data.NavigationManifest, want)
}

func assertSystemSurfaceDisclosure(t *testing.T, response systemSurfaceDisclosureResponse, want bool) {
	t.Helper()
	if response.SystemSurfaceDisclosure.SnapshotVersion != "1" {
		t.Fatalf("systemSurfaceDisclosure.snapshotVersion = %q, want 1", response.SystemSurfaceDisclosure.SnapshotVersion)
	}
	if response.SystemSurfaceDisclosure.MetadataAllowed != want {
		t.Fatalf("systemSurfaceDisclosure.metadataAllowed = %t, want %t", response.SystemSurfaceDisclosure.MetadataAllowed, want)
	}
}
