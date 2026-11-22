package offlinepack

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestManifestPartsGateKeepsLegacyItemShape covers the ADR-0157 Amendment A
// compatibility gate: a client that does not send ManifestParts gets manifest
// items with only the four original keys, even for Media with chapter Markers,
// because the shipped kits reject any other item key.
func TestManifestPartsGateKeepsLegacyItemShape(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	end := 30.0
	if _, err := st.CreateMarker(context.Background(), store.Marker{
		MediaID: "media-alpha", Title: "Opening", StartSeconds: 0, EndSeconds: &end,
	}, filter, time.Unix(1000, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	legacy, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha"})
	if err != nil {
		t.Fatalf("legacy BuildPack: %v", err)
	}
	items := rawManifestItems(t, legacy.Manifest)
	if len(items) != 1 {
		t.Fatalf("legacy manifest items = %d, want 1", len(items))
	}
	keys := make([]string, 0, len(items[0]))
	for key := range items[0] {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if want := []string{"media_id", "selected_version", "sha256", "size"}; !slices.Equal(keys, want) {
		t.Fatalf("legacy manifest item keys = %v, want %v", keys, want)
	}

	aware, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", ManifestParts: true,
	})
	if err != nil {
		t.Fatalf("parts-aware BuildPack: %v", err)
	}
	items = rawManifestItems(t, aware.Manifest)
	if len(items) != 1 {
		t.Fatalf("parts-aware manifest items = %d, want 1", len(items))
	}
	var markers []ManifestMarker
	if err := json.Unmarshal(items[0]["markers"], &markers); err != nil || len(markers) == 0 {
		t.Fatalf("parts-aware manifest markers = %s, %v; want the chapter Markers", items[0]["markers"], err)
	}
	if _, ok := items[0]["part_ordinal"]; ok {
		t.Fatal("single-Part manifest item carries part_ordinal")
	}
}

// rawManifestItems decodes the signed manifest's items as raw key maps
// without verifying the signature.
func rawManifestItems(t *testing.T, token string) []map[string]json.RawMessage {
	t.Helper()
	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("manifest has %d segments, want 3", len(segments))
	}
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims.Items
}
