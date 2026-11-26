package store

import (
	"context"
	"testing"
)

func TestLibraryVisibleMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	all := LibraryFilter{AllLibraries: true}
	added, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/visible/added.mkv", Title: "Added", Container: "mkv", Codec: "h264",
		Class: MediaClassVideo, Year: 2020, ContentFingerprint: "visible-added", VideoWidthHint: 3840, VideoHeightHint: 2160,
	})
	if err != nil {
		t.Fatal(err)
	}
	trashed, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: "library-local", Path: "/visible/trashed.mkv", Title: "Trashed", Container: "mkv", Codec: "h264",
		Class: MediaClassVideo, ContentFingerprint: "visible-trashed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TrashMedia(ctx, trashed.ID, all); err != nil {
		t.Fatal(err)
	}
	items, err := st.LibraryVisibleMedia(ctx, all, "library-local")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[MediaID]VisibleMedia{}
	for _, item := range items {
		byID[item.ID] = item
	}
	alpha, ok := byID["media-alpha"]
	if !ok || alpha.Width != 1280 || alpha.Height != 720 {
		t.Fatalf("media-alpha = %+v (present %v), want 1280x720", alpha, ok)
	}
	got := byID[added.ID]
	if got.Title != "Added" || got.Year != 2020 || got.Width != 3840 || got.Height != 2160 || got.Order <= alpha.Order {
		t.Fatalf("added = %+v, want 3840x2160 ordered after media-alpha (%d)", got, alpha.Order)
	}
	if _, ok := byID[trashed.ID]; ok {
		t.Fatal("a trashed item is visible")
	}

	for name, filter := range map[string]LibraryFilter{
		"restricted without Reveal": all,
		"not granted":               {AllowedIDs: []LibraryID{"library-local"}},
	} {
		items, err := st.LibraryVisibleMedia(ctx, filter, "library-archive")
		if err != nil || len(items) != 0 {
			t.Fatalf("%s: archive items = %v (%v), want none", name, items, err)
		}
	}
	if items, err := st.LibraryVisibleMedia(ctx, LibraryFilter{AllLibraries: true, Revealed: true}, "library-archive"); err != nil || len(items) == 0 {
		t.Fatalf("revealed archive items = %v (%v), want the archive media", items, err)
	}
}

func TestMediaMetadataProvenanceBatch(t *testing.T) {
	ctx := context.Background()
	st, encMedia := seededEncryptedMedia(t, "provenance-batch-private")
	defer func() { _ = st.Close() }()
	if err := st.SetMediaMetadataProvenance(ctx, "media-alpha", "demo", "lookup", "cand-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaMetadataProvenance(ctx, encMedia.ID, "demo", "lookup", "cand-enc"); err != nil {
		t.Fatal(err)
	}
	got, err := st.MediaMetadataProvenanceBatch(ctx, []MediaID{"media-alpha", encMedia.ID, "media-missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 ||
		got["media-alpha"] != (MediaMetadataProvenance{PluginID: "demo", ScraperID: "lookup", CandidateID: "cand-1"}) ||
		got[encMedia.ID] != (MediaMetadataProvenance{PluginID: "demo", ScraperID: "lookup", CandidateID: "cand-enc"}) {
		t.Fatalf("provenance batch = %#v", got)
	}
	if err := st.SealEncryptedLibraryDB(ctx, encMedia.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	sealedGot, err := st.MediaMetadataProvenanceBatch(ctx, []MediaID{"media-alpha", encMedia.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(sealedGot) != 1 || sealedGot["media-alpha"] == (MediaMetadataProvenance{}) {
		t.Fatalf("sealed provenance batch = %#v, want only catalog entry", sealedGot)
	}
	if _, leaked := sealedGot[encMedia.ID]; leaked {
		t.Fatalf("sealed encrypted media provenance leaked: %#v", sealedGot[encMedia.ID])
	}
	if empty, err := st.MediaMetadataProvenanceBatch(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty batch = %v (%v)", empty, err)
	}
}

// A certification ceiling removes above-ceiling and unrated items before the
// list is built, so a count of the list never includes them.
func TestLibraryVisibleMediaAppliesCertification(t *testing.T) {
	st := openSeeded(t)
	libraryID, ids := seedCertifiedLibrary(t, st, "filmtv", false, "low", "mid", "high", "")
	filter := LibraryFilter{AllLibraries: true, Certification: []CertificationConstraint{
		{PluginID: "filmtv", Allowed: []string{"low", "mid"}},
	}}
	items, err := st.LibraryVisibleMedia(t.Context(), filter, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[MediaID]bool{}
	for _, item := range items {
		got[item.ID] = true
	}
	want := map[MediaID]bool{ids["low"]: true, ids["mid"]: true}
	if len(items) != len(want) || !got[ids["low"]] || !got[ids["mid"]] {
		t.Fatalf("LibraryVisibleMedia under a ceiling = %v, want only %v", got, want)
	}
}
