package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/serverrole"
)

// TestAttachedRefusesCatalogRowAuthoring asserts the ADR-0123 §2 authority face:
// an Attached Origin Node never authors catalog-identity rows (Media / Library /
// Entity). Each author path must return the authority sentinel.
func TestAttachedRefusesCatalogRowAuthoring(t *testing.T) {
	st := openAttached(t)
	ctx := context.Background()

	t.Run("ImportMediaFile", func(t *testing.T) {
		_, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID:          "lib-x",
			Path:               "/media/x.mp4",
			ContentFingerprint: "fp-x",
			Title:              "X",
			Container:          "mp4",
		})
		requireAuthorityRefusal(t, err)
	})
	t.Run("ImportMediaFileParts", func(t *testing.T) {
		_, _, err := st.ImportMediaFileParts(ctx, []ImportMediaFilePart{{
			File:          ImportMediaFile{LibraryID: "lib-x", Path: "/m/a.mp4", ContentFingerprint: "fp-a", Title: "A", Container: "mp4"},
			SourceOrdinal: 1,
		}})
		requireAuthorityRefusal(t, err)
	})
	t.Run("CreateLibrary", func(t *testing.T) {
		_, err := st.CreateLibrary(ctx, "New Lib", false, "plugin-x", "profile-default")
		requireAuthorityRefusal(t, err)
	})
	t.Run("CreateLibraryWithRoots", func(t *testing.T) {
		_, err := st.CreateLibraryWithRoots(ctx, "New Lib", false, "plugin-x", "profile-default", []string{"/roots/x"}, "")
		requireAuthorityRefusal(t, err)
	})
	t.Run("CreateProvisionalMedia", func(t *testing.T) {
		_, err := st.CreateProvisionalMedia(ctx, ProvisionalMedia{
			LibraryID: "lib-x", GUID: "guid-x", Title: "Prov", TeePath: "/tee/x", Container: "mp4",
		})
		requireAuthorityRefusal(t, err)
	})
	t.Run("UpsertEntity", func(t *testing.T) {
		err := st.UpsertEntity(ctx, Entity{ID: "ent-x", LibraryID: "lib-x", Kind: "artist", Title: "Artist"})
		requireAuthorityRefusal(t, err)
	})
	t.Run("UpsertEntityRelation", func(t *testing.T) {
		err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "ent-a", Name: "rel", ToEntityID: "ent-b"})
		requireAuthorityRefusal(t, err)
	})
}

// TestAttachedRefusesEngagementAuthoring asserts the ADR-0123 §2 authority face:
// an Attached node never authors engagement (events.db projection, decision 12).
func TestAttachedRefusesEngagementAuthoring(t *testing.T) {
	st := openAttached(t)
	ctx := context.Background()

	t.Run("AppendEngagementEvent", func(t *testing.T) {
		_, err := st.AppendEngagementEvent(ctx, EngagementEvent{
			ProfileID: "profile-default", MediaID: "media-x", Kind: "o", Value: 1,
		})
		requireAuthorityRefusal(t, err)
	})
	t.Run("UpsertPlaybackProgress", func(t *testing.T) {
		err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: "profile-default", MediaID: "media-x", PositionSeconds: 10, DurationSeconds: 100,
		}, false, ResumePolicy{})
		requireAuthorityRefusal(t, err)
	})
	t.Run("ApplyImportedPlays", func(t *testing.T) {
		err := st.ApplyImportedPlays(ctx, "profile-default", "media-x", 1, time.Unix(100, 0), EngagementProvenance{})
		requireAuthorityRefusal(t, err)
	})
}

// TestAttachedAllowsByteLocalWrites asserts the byte-local face stays OPEN on an
// Attached node: a curation/byte-local write is NOT blocked by the role guard.
// The write may still fail for an unrelated reason (e.g. the Media does not
// exist), but it must NEVER return the authority sentinel — proving the role
// gate does not over-reach into the byte-local face.
func TestAttachedAllowsByteLocalWrites(t *testing.T) {
	st := openAttached(t)
	ctx := context.Background()

	// SetMediaFields on a non-existent Media: a byte-local/curation write. It will
	// error (no such Media) but the error must not be the authority refusal.
	err := st.SetMediaFields(ctx, "media-does-not-exist", []MediaField{{Name: "k", Type: EntityFieldText, Text: "v"}})
	if errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("byte-local SetMediaFields was refused by the authority guard: %v", err)
	}
}

// TestMainAuthorsFreely confirms the default (Main) store still authors — the
// guard is a no-op for the single-server operator (back-compatible).
func TestMainAuthorsFreely(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("open main store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// CreateLibrary must succeed (or fail for a real reason), never with the
	// authority sentinel, on a Main server.
	if _, err := st.CreateLibrary(context.Background(), "Main Lib", false, "plugin-x", "profile-default"); errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("Main CreateLibrary hit the authority guard: %v", err)
	}
}

func openAttached(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), t.TempDir(), Options{Role: serverrole.Attached})
	if err != nil {
		t.Fatalf("open attached store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func requireAuthorityRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("authority write succeeded on an attached node; it must refuse")
	}
	if !errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
		t.Fatalf("attached authority write error %v does not match the authority sentinel", err)
	}
}
