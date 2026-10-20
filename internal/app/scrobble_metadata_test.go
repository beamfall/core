package app

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestScrobbleMetadataResolverFillsArtistAlbumFromRelations: MUSIC-7 (ADR-0098
// area 7) — the composition-root resolver walks the Media's plugin-declared
// artist/album relations and entity MBID fields to build the richer outbound
// metadata, via generic store reads (no hardcoded Music slugs in core).
func TestScrobbleMetadataResolverFillsArtistAlbumFromRelations(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	const mediaID = store.MediaID("media-alpha") // a seeded Media in library-local

	// Seed an Artist entity related to the track (relation "appears-on" → Media,
	// the Media-level performance credit MUSIC-1 hydration writes) with a
	// MusicBrainz id field.
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-artist-1", LibraryID: "library-local", Kind: "artist", Title: "An Artist",
		Fields: []store.EntityField{{Name: "musicbrainz-id", Type: store.EntityFieldText, Text: "artist-mbid-1"}},
	}); err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-artist-1", Name: "appears-on", ToMediaID: mediaID,
	}); err != nil {
		t.Fatalf("seed artist relation: %v", err)
	}

	// Seed an Album entity related to the track (relation "album-track" → Media)
	// with a MusicBrainz release id field.
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-album-1", LibraryID: "library-local", Kind: "album", Title: "An Album",
		Fields: []store.EntityField{{Name: "musicbrainz-release-id", Type: store.EntityFieldText, Text: "release-mbid-2"}},
	}); err != nil {
		t.Fatalf("seed album: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-album-1", Name: "album-track", ToMediaID: mediaID,
	}); err != nil {
		t.Fatalf("seed album relation: %v", err)
	}

	resolver := newScrobbleMetadataResolver(st, nil)
	md := resolver.ResolveOutboundAudioMetadata(ctx, mediaID)
	want := graph.OutboundAudioMetadata{
		ContributorName: "An Artist", ReleaseName: "An Album",
		ContributorMBID: "artist-mbid-1", ReleaseMBID: "release-mbid-2",
	}
	if md != want {
		t.Fatalf("resolved metadata = %+v, want %+v", md, want)
	}
}

// TestScrobbleMetadataResolverDegradesWithoutRelations: a Media with no artist/
// album relations resolves to the zero value (degrade-when-absent, ADR-0098).
func TestScrobbleMetadataResolverDegradesWithoutRelations(t *testing.T) {
	st := storetest.Open(t)
	resolver := newScrobbleMetadataResolver(st, nil)
	md := resolver.ResolveOutboundAudioMetadata(context.Background(), "media-alpha")
	if md != (graph.OutboundAudioMetadata{}) {
		t.Fatalf("un-hydrated Media should degrade to empty metadata, got %+v", md)
	}
}

// TestScrobbleMetadataResolverArtistOnly: with only an artist relation (no MBID
// field, no album), the resolver returns just the display name and leaves the
// other fields empty — each field degrades independently.
func TestScrobbleMetadataResolverArtistOnly(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, store.Entity{
		ID: "entity-artist-2", LibraryID: "library-local", Kind: "artist", Title: "Solo Artist",
	}); err != nil {
		t.Fatalf("seed artist: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "entity-artist-2", Name: "appears-on", ToMediaID: "media-alpha",
	}); err != nil {
		t.Fatalf("seed relation: %v", err)
	}
	md := newScrobbleMetadataResolver(st, nil).ResolveOutboundAudioMetadata(ctx, "media-alpha")
	if md.ContributorName != "Solo Artist" || md.ContributorMBID != "" || md.ReleaseName != "" || md.ReleaseMBID != "" {
		t.Fatalf("artist-only resolution mismatch: %+v", md)
	}
}
