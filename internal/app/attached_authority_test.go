package app

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

// TestAttachedAppRefusesAuthorityFaces is the ADR-0123 §2 end-to-end check: an
// App composed via New with Role=Attached propagates the role into its store so
// the three authority faces (catalog rows, engagement) refuse through the fully
// wired app, while the byte-local face stays open. Stream-Token issuance refusal
// is covered as a unit in internal/streamtoken; here we assert the composition
// wiring (cfg.Role → store.Options.Role → guardAuthority) holds end-to-end.
func TestAttachedAppRefusesAuthorityFaces(t *testing.T) {
	cfg := testConfig(t)
	cfg.Role = serverrole.Attached
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New (attached) returned error: %v", err)
	}
	defer beamfall.Close()
	ctx := context.Background()

	// Catalog-row authoring face: refused.
	if _, err := beamfall.store.CreateLibrary(ctx, "Lib", false, "plugin-x", "profile-default"); !errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("attached app CreateLibrary err = %v, want authority refusal", err)
	}
	if err := beamfall.store.UpsertEntity(ctx, store.Entity{ID: "e", LibraryID: "lib-x", Kind: "artist", Title: "A"}); !errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("attached app UpsertEntity err = %v, want authority refusal", err)
	}
	// Engagement face: refused.
	if _, err := beamfall.store.AppendEngagementEvent(ctx, store.EngagementEvent{ProfileID: "profile-default", MediaID: "m", Kind: "o", Value: 1}); !errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("attached app AppendEngagementEvent err = %v, want authority refusal", err)
	}
	// Byte-local face: NOT refused by the role guard (may fail for another reason).
	if err := beamfall.store.SetMediaFields(ctx, "m-missing", []store.MediaField{{Name: "k", Type: store.EntityFieldText, Text: "v"}}); errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("attached app SetMediaFields was refused by the authority guard: %v", err)
	}
}

// TestMainAppAuthorsFreely confirms the default (no --attached) app still
// authors catalog rows — the single-server operator story is unchanged.
func TestMainAppAuthorsFreely(t *testing.T) {
	cfg := testConfig(t)
	cfg.SeedDevelopmentData = true // implies an attachable main world
	beamfall, err := New(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatalf("New (main) returned error: %v", err)
	}
	defer beamfall.Close()

	if _, err := beamfall.store.CreateLibrary(context.Background(), "Main Lib", false, "plugin-x", "profile-default"); errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("main app CreateLibrary hit the authority guard: %v", err)
	}
}
