package graph

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store/storetest"
)

const scanEstimateFilesQuery = `query E($root: String, $libraryID: String, $pluginID: String) { scanEstimate(root: $root, libraryID: $libraryID, pluginID: $pluginID) { estimatedFiles } }`

// sidecarClassBundle is a test plugin that declares the given Media classes.
func sidecarClassBundle(id string, classes ...string) plugin.Bundle {
	return plugin.Bundle{
		ID:           plugin.ID(id),
		DisplayName:  plugin.L("Shelf " + id),
		MediaLabel:   plugin.L("Item"),
		MediaClasses: classes,
		Layouts: []plugin.LayoutSpec{
			{ID: "home", Role: "browse", Sections: []plugin.SectionSpec{
				{ID: "everything", Kind: "grid", Title: plugin.L("All"),
					Source: plugin.SourceSpec{Kind: "all-items"}},
			}},
		},
	}
}

// TestScanPreviewAndEstimateFollowPluginImageClass is the CORE-2180 API
// acceptance: a "-thumb" image beside a video counts in the scan preview and
// the estimate for a Library whose plugin declares the image class, and is
// left out for a video-only plugin, as the scan imports it or not.
func TestScanPreviewAndEstimateFollowPluginImageClass(t *testing.T) {
	registry, err := newTestPluginRegistry([]plugin.Bundle{
		sidecarClassBundle("stills", "image", "video"),
		sidecarClassBundle("reels", "video"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pluginID    string
		countsThumb bool
	}{
		{"stills", true},
		{"reels", false},
	} {
		t.Run(tc.pluginID, func(t *testing.T) {
			st := storetest.Open(t)
			ctx := context.Background()
			created, err := st.CreateLibrary(ctx, "Shelf", false, tc.pluginID, "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			want := []string{writeSettledScanFile(t, root, "IMG_1.mov")}
			thumb := writeSettledScanFile(t, root, "IMG_1-thumb.jpg")
			if tc.countsThumb {
				want = append(want, thumb)
			}
			slices.Sort(want)
			if err := st.SetLibraryRoots(ctx, created.ID, []string{root}); err != nil {
				t.Fatal(err)
			}
			handler := newTestHandlerWithPlugins(st, registry)
			openManagerAdminSession(t, st)

			resp := postScanPreviewGraph(t, handler, scanPreviewQuery, map[string]any{"libraryID": string(created.ID)})
			if len(resp.Errors) != 0 {
				t.Fatalf("scanPreview errors = %#v", resp.Errors)
			}
			var adds []string
			for _, change := range resp.Data.ScanPreview.Changes {
				adds = append(adds, change.Path)
			}
			slices.Sort(adds)
			if resp.Data.ScanPreview.Adds != len(want) || !slices.Equal(adds, want) {
				t.Fatalf("scanPreview adds = %d %q, want %q", resp.Data.ScanPreview.Adds, adds, want)
			}

			for name, variables := range map[string]map[string]any{
				"library":         {"libraryID": string(created.ID)},
				"root and plugin": {"root": root, "pluginID": tc.pluginID},
			} {
				body, err := json.Marshal(map[string]any{"query": scanEstimateFilesQuery, "variables": variables})
				if err != nil {
					t.Fatal(err)
				}
				data, errs := post(t, handler, string(body), authorizeManager)
				if len(errs) != 0 {
					t.Fatalf("scanEstimate(%s) errors = %#v", name, errs)
				}
				var est ScanEstimateInfo
				mustUnmarshal(t, data["scanEstimate"], &est)
				if est.EstimatedFiles != len(want) {
					t.Fatalf("scanEstimate(%s) files = %d, want %d", name, est.EstimatedFiles, len(want))
				}
			}
		})
	}
}
