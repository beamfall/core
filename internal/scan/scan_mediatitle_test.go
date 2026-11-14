package scan

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// titledParser parses every file to the container title "Show" and supplies
// a per-file title through MediaTitler.
type titledParser struct{ title string }

func (titledParser) Parse(string, ParseProbe) (ParsedMedia, bool) {
	return ParsedMedia{Title: "Show", Class: "item"}, true
}

func (p titledParser) MediaTitle(string, ParseProbe) string { return p.title }

func scanTitledLibrary(t *testing.T, parser FilenameParser, prober Prober) (store.Media, *recordingIdentifier) {
	t.Helper()
	root := t.TempDir()
	writeScanFile(t, root, "Show/Show - S01E02 - Pilot.mkv", []byte("item bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Titled", false, "titled", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	identifier := &recordingIdentifier{}
	result, err := Scan(context.Background(), st, prober, nil, map[string]FilenameParser{"titled": parser}, nil, identifier, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(result.Files))
	}
	media, err := st.Media(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	return media, identifier
}

func TestMediaTitlerReplacesParsedTitle(t *testing.T) {
	for name, prober := range map[string]Prober{
		"without probe": nil,
		"with probe":    fakeProber{probe: ffmpeg.Probe{HasVideo: true}},
	} {
		t.Run(name, func(t *testing.T) {
			media, identifier := scanTitledLibrary(t, titledParser{title: "  Pilot "}, prober)
			if media.Title != "Pilot" {
				t.Fatalf("media title = %q, want Pilot", media.Title)
			}
			if identifier.calls > 0 && identifier.parsed.Title != "Show" {
				t.Fatalf("identification title = %q, want the parsed title Show", identifier.parsed.Title)
			}
		})
	}
}

func TestMediaTitlerEmptyKeepsParsedTitle(t *testing.T) {
	media, _ := scanTitledLibrary(t, titledParser{}, fakeProber{probe: ffmpeg.Probe{HasVideo: true}})
	if media.Title != "Show" {
		t.Fatalf("media title = %q, want the parsed title Show", media.Title)
	}
}
