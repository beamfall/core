package store

import (
	"context"
	"strings"
	"testing"
)

func TestImportMediaFilePartsSumsPartDurationsAndUpdatesOnFastPath(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Audio Parts", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllowedIDs: []LibraryID{lib.ID}}

	fp1 := strings.Repeat("a", 64)
	fp2 := strings.Repeat("b", 64)
	fp3 := strings.Repeat("c", 64)
	parts := []ImportMediaFilePart{
		{
			SourceOrdinal: 1,
			File: ImportMediaFile{
				LibraryID:           lib.ID,
				Path:                "/audio/Book/01.mp3",
				Title:               "Book",
				Container:           "mp3",
				Codec:               "mp3",
				Class:               MediaClassAudio,
				ContentFingerprint:  fp1,
				DurationSecondsHint: 120,
			},
		},
		{
			SourceOrdinal: 2,
			File: ImportMediaFile{
				LibraryID:           lib.ID,
				Path:                "/audio/Book/02.mp3",
				Title:               "Book",
				Container:           "mp3",
				Codec:               "mp3",
				Class:               MediaClassAudio,
				ContentFingerprint:  fp2,
				DurationSecondsHint: 180,
			},
		},
		{
			SourceOrdinal: 3,
			File: ImportMediaFile{
				LibraryID:           lib.ID,
				Path:                "/audio/Book/03.mp3",
				Title:               "Book",
				Container:           "mp3",
				Codec:               "mp3",
				Class:               MediaClassAudio,
				ContentFingerprint:  fp3,
				DurationSecondsHint: 90,
			},
		},
	}

	media, files, err := st.ImportMediaFileParts(ctx, parts)
	if err != nil {
		t.Fatalf("ImportMediaFileParts returned error: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("files len = %d, want 3", len(files))
	}
	if media.DurationSeconds != 390 {
		t.Fatalf("initial media.DurationSeconds = %d, want 390 (120+180+90)", media.DurationSeconds)
	}

	items, err := st.SourceItems(ctx, filter, SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateMediaClass(ctx, items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Summary != "7 min" || items[0].DurationSeconds != 390 {
		t.Fatalf("SourceItems = %+v, want Summary=\"7 min\" DurationSeconds=390", items)
	}

	browse, err := st.LibraryBrowse(ctx, filter, LibraryBrowseQuery{LibraryID: lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateMediaClass(ctx, browse.Items); err != nil {
		t.Fatal(err)
	}
	if len(browse.Items) != 1 || browse.Items[0].Summary != "7 min" || browse.Items[0].DurationSeconds != 390 {
		t.Fatalf("LibraryBrowse = %+v, want Summary=\"7 min\" DurationSeconds=390", browse.Items)
	}

	// Simulate an existing catalog row that recorded only Part 1's duration,
	// then re-import with KeepProbeFacts=true (the scan fast-path shape).
	if updated, err := st.SetMultiPartMediaDuration(ctx, media.ID, 120); err != nil || !updated {
		t.Fatalf("SetMultiPartMediaDuration(120) = (%v, %v), want (true, nil)", updated, err)
	}
	for i := range parts {
		parts[i].File.KeepProbeFacts = true
		parts[i].File.Container = "ogg"
		parts[i].File.Codec = "vorbis"
	}
	healed, _, err := st.ImportMediaFileParts(ctx, parts)
	if err != nil {
		t.Fatalf("fast-path ImportMediaFileParts error: %v", err)
	}
	if healed.DurationSeconds != 390 {
		t.Fatalf("healed DurationSeconds = %d, want 390", healed.DurationSeconds)
	}
	if healed.Container != "mp3" || healed.Codec != "mp3" {
		t.Fatalf("KeepProbeFacts must preserve container/codec; got %s/%s", healed.Container, healed.Codec)
	}
}

func TestSetMultiPartMediaDurationSkipsStandaloneMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Single Items", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:           lib.ID,
		Path:                "/audio/single.mp3",
		Title:               "Single",
		Container:           "mp3",
		Codec:               "mp3",
		Class:               MediaClassAudio,
		ContentFingerprint:  strings.Repeat("d", 64),
		DurationSecondsHint: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := st.SetMultiPartMediaDuration(ctx, media.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatalf("SetMultiPartMediaDuration modified standalone Media; want false")
	}
}
