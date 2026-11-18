package capabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func devTestHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func devTestManifest(t *testing.T, mutate func(record map[string]any, manifest map[string]any)) []byte {
	t.Helper()
	record := map[string]any{
		"buildID":       "android-viewer-app-1.0-7-dev",
		"surface":       string(SurfaceAndroidViewer),
		"artifactHash":  devTestHash("dev"),
		"sequence":      7,
		"distributions": []string{"direct"},
		"roles":         []string{"viewer", "manager"},
	}
	manifest := map[string]any{"schema": FirstPartyBuildManifestSchema, "builds": []any{record}}
	if mutate != nil {
		mutate(record, manifest)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseDevBuildManifestAcceptsADevRecord(t *testing.T) {
	catalog, err := ParseDevBuildManifest(devTestManifest(t, nil))
	if err != nil {
		t.Fatalf("ParseDevBuildManifest: %v", err)
	}
	if len(catalog.Builds) != 1 || catalog.Builds[0].BuildID != "android-viewer-app-1.0-7-dev" {
		t.Fatalf("catalog = %+v", catalog)
	}
	if len(catalog.MinimumSequence) != 0 {
		t.Fatalf("dev catalog carries floors: %v", catalog.MinimumSequence)
	}
	empty, err := ParseDevBuildManifest([]byte(`{"schema":"` + FirstPartyBuildManifestSchema + `","builds":[]}`))
	if err != nil || len(empty.Builds) != 0 {
		t.Fatalf("empty dev manifest = %+v, %v; want empty, nil", empty, err)
	}
}

func TestParseDevBuildManifestRefusesReleasePolicy(t *testing.T) {
	cases := map[string]struct {
		mutate func(record, manifest map[string]any)
		want   string
	}{
		"release build id": {func(r, _ map[string]any) { r["buildID"] = "android-viewer-app-1.0-7" }, "must end in"},
		"bare suffix":      {func(r, _ map[string]any) { r["buildID"] = "-dev" }, ""},
		"app store":        {func(r, _ map[string]any) { r["distributions"] = []string{"direct", "appStore"} }, "distributions"},
		"app store only":   {func(r, _ map[string]any) { r["distributions"] = []string{"appStore"} }, "distributions"},
		"api key role":     {func(r, _ map[string]any) { r["roles"] = []string{"viewer", "apiKey"} }, ""},
		"floor": {func(_, m map[string]any) {
			m["minimumSequence"] = map[string]int{string(SurfaceAndroidViewer): 1}
		}, ""},
		"bad hash": {func(r, _ map[string]any) { r["artifactHash"] = "abc" }, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseDevBuildManifest(devTestManifest(t, tc.mutate))
			if err == nil {
				t.Fatal("ParseDevBuildManifest accepted it")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestWithDevBuildsNeverShadowsACatalogRecord(t *testing.T) {
	release := BuildRecord{BuildID: "x-dev", Surface: SurfaceAndroidViewer, ArtifactHash: devTestHash("release"), Sequence: 3}
	catalog := BuildCatalog{Builds: []BuildRecord{release}}
	dev := BuildCatalog{Builds: []BuildRecord{
		{BuildID: "x-dev", Surface: SurfaceAndroidViewer, ArtifactHash: devTestHash("impostor"), Sequence: 9},
		{BuildID: "y-dev", Surface: SurfaceAndroidViewer, ArtifactHash: devTestHash("y"), Sequence: 9},
	}}
	merged, shadowed := catalog.WithDevBuilds(dev)
	if len(shadowed) != 1 || shadowed[0] != "x-dev" {
		t.Fatalf("shadowed = %v, want [x-dev]", shadowed)
	}
	if len(merged.Builds) != 2 || merged.Builds[0].ArtifactHash != release.ArtifactHash || merged.Builds[1].BuildID != "y-dev" {
		t.Fatalf("merged = %+v", merged.Builds)
	}
	if len(catalog.Builds) != 1 {
		t.Fatalf("WithDevBuilds mutated its receiver: %+v", catalog.Builds)
	}
}
