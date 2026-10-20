package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	music "github.com/beamfall/plugin-music"
)

type musicPackAttester struct{}

func (musicPackAttester) VerifyDeviceKey(context.Context, string, []byte, []byte) error { return nil }

func newMusicPackSvc(t *testing.T, st *store.Store) *offlinepack.Service {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := offlinepack.NewAuthority("spki-sha256(test)", "test-key", privateKey, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	service := offlinepack.NewService(st, authority, time.Now)
	service.SetPackRoot(t.TempDir())
	service.SetDeviceKeyAttestationVerifier(musicPackAttester{})
	deviceKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterDeviceKey(t.Context(), "device-tv", deviceKey.PublicKey().Bytes(), []byte("attested")); err != nil {
		t.Fatal(err)
	}
	for kind, resolver := range music.PackResolvers(nil) {
		service.RegisterResolver(kind, musicPackResolver{store: st, inner: resolver})
	}
	return service
}

func revealedPackFilter() store.LibraryFilter {
	return store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", "library-archive"}, Revealed: true}
}

func packAuthScope() auth.Scope {
	return auth.Scope{
		DeviceID: "device-tv", SessionID: "session-default", ProfileID: "profile-default",
		LibraryIDs: []store.LibraryID{"library-local", "library-archive"}, Revealed: true,
	}
}

func TestMusicPackAlbumResolverYieldsEntityMedia(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	if err := st.UpsertEntity(ctx, store.Entity{ID: "album-1", LibraryID: "library-local", Kind: "album", Title: "Album One"}); err != nil {
		t.Fatalf("UpsertEntity: %v", err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: "album-1", Name: "track", ToMediaID: "media-alpha"}); err != nil {
		t.Fatalf("UpsertEntityRelation: %v", err)
	}
	resolver := musicPackResolver{store: st, inner: mustPackResolver(t, music.PackKindAlbum)}
	got, err := resolver.Resolve(ctx, "album-1", revealedPackFilter())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.MediaIDs) != 1 || got.MediaIDs[0] != "media-alpha" || got.Label != "Album" {
		t.Fatalf("resolved = %+v, want media-alpha/Album", got)
	}
}

func TestMusicPackCollectionResolverFiltersViaCollectionMemberMediaIDs(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	collection, err := st.CreateCollection(ctx, store.Collection{LibraryID: "library-local", Name: "Mix"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if _, err := st.AddCollectionMedia(ctx, collection.ID, []store.MediaID{"media-alpha"}, revealedPackFilter(), time.Now().UTC()); err != nil {
		t.Fatalf("AddCollectionMedia: %v", err)
	}
	resolver := musicPackResolver{store: st, inner: mustPackResolver(t, music.PackKindCollection)}
	got, err := resolver.Resolve(ctx, string(collection.ID), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.MediaIDs) != 1 || got.MediaIDs[0] != "media-alpha" || got.Label != "Playlist" {
		t.Fatalf("resolved = %+v, want media-alpha/Playlist", got)
	}
}

func TestMusicPackTracksResolverKeepsVisibleSubset(t *testing.T) {
	st := storetest.Open(t)
	resolver := musicPackResolver{store: st, inner: mustPackResolver(t, music.PackKindTracks)}
	got, err := resolver.Resolve(context.Background(), "media-alpha, media-archive", store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local", "library-archive"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.MediaIDs) != 1 || got.MediaIDs[0] != "media-alpha" || got.Label != "Tracks" {
		t.Fatalf("resolved = %+v, want media-alpha/Tracks", got)
	}
}

func TestMusicPackStationBuildLeavesNoRowWhenGeneratorUnavailable(t *testing.T) {
	st := storetest.Open(t)
	service := newMusicPackSvc(t, st)
	if _, err := service.BuildPack(context.Background(), packAuthScope(), offlinepack.BuildPackRequest{TargetKind: music.PackKindStation, TargetRef: "station-1"}); err != music.ErrStationGeneratorUnavailable {
		t.Fatalf("BuildPack err = %v, want ErrStationGeneratorUnavailable", err)
	}
	packs, err := st.OfflinePacksForProfile(context.Background(), "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if len(packs) != 0 {
		t.Fatalf("packs = %d, want 0", len(packs))
	}
}

func mustPackResolver(t *testing.T, kind string) music.PackResolver {
	t.Helper()
	resolver, ok := music.PackResolvers(nil)[kind]
	if !ok {
		t.Fatalf("PackResolvers: no resolver for kind %q", kind)
	}
	return resolver
}
