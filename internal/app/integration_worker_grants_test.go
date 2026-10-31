package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestCandidatePermissionsApprovedRequiresGrantAndCurrentBinding: an update
// candidate is approved only when every declared permission has an approved
// grant, and every high-risk grant is bound to the candidate's exact manifest.
func TestCandidatePermissionsApprovedRequiresGrantAndCurrentBinding(t *testing.T) {
	const pluginID = "candidate"
	current := strings.Repeat("c", 64)
	stale := strings.Repeat("d", 64)
	for _, tt := range []struct {
		name        string
		approved    map[string]bool
		bindings    map[string]string
		permissions []string
		manifest    string
		want        bool
	}{
		{
			name:        "low-risk approved grant needs no binding",
			approved:    map[string]bool{integrationhost.PermStatusWrite: true},
			permissions: []string{integrationhost.PermStatusWrite},
			manifest:    current,
			want:        true,
		},
		{
			name:        "declared permission without a grant",
			approved:    map[string]bool{integrationhost.PermStatusWrite: true},
			permissions: []string{integrationhost.PermStatusWrite, integrationhost.PermPluginJob},
			manifest:    current,
		},
		{
			name:        "grant present but unapproved",
			approved:    map[string]bool{integrationhost.PermStatusWrite: false},
			permissions: []string{integrationhost.PermStatusWrite},
			manifest:    current,
		},
		{
			name:        "high-risk grant without binding",
			approved:    map[string]bool{integrationhost.PermReadCatalog: true},
			permissions: []string{integrationhost.PermReadCatalog},
			manifest:    current,
		},
		{
			name:        "high-risk grant bound to another manifest",
			approved:    map[string]bool{integrationhost.PermReadCatalog: true},
			bindings:    map[string]string{integrationhost.PermReadCatalog: stale},
			permissions: []string{integrationhost.PermReadCatalog},
			manifest:    current,
		},
		{
			name:        "high-risk grant with candidate lacking a manifest binding",
			approved:    map[string]bool{integrationhost.PermReadCatalog: true},
			bindings:    map[string]string{integrationhost.PermReadCatalog: current},
			permissions: []string{integrationhost.PermReadCatalog},
		},
		{
			name:        "high-risk grant bound to the candidate manifest",
			approved:    map[string]bool{integrationhost.PermReadCatalog: true, integrationhost.PermStatusWrite: true},
			bindings:    map[string]string{integrationhost.PermReadCatalog: current},
			permissions: []string{integrationhost.PermStatusWrite, integrationhost.PermReadCatalog},
			manifest:    current,
			want:        true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := storetest.Open(t)
			ctx := context.Background()
			for kind, approved := range tt.approved {
				if err := st.SetPluginGrant(ctx, store.PluginGrantRow{
					PluginID: pluginID, Kind: kind, Approved: approved, AllLibraries: true,
				}, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			for kind, binding := range tt.bindings {
				if err := integrationhost.SetGrantApprovalBinding(st.IntegrationArtifactRoot(), pluginID, kind, binding); err != nil {
					t.Fatal(err)
				}
			}
			got, err := candidatePermissionsApproved(ctx, st, pluginID, tt.permissions, tt.manifest)
			if err != nil {
				t.Fatalf("candidatePermissionsApproved: %v", err)
			}
			if got != tt.want {
				t.Fatalf("approved = %v, want %v", got, tt.want)
			}
		})
	}
}
