package scan

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// labelledTitleParser reads "Title (YYYY).ext" and "Title (YYYY) - label.ext",
// the shape a plugin parser reports a Version label from.
type labelledTitleParser struct{}

var labelledTitlePattern = regexp.MustCompile(`^(.+) \((\d{4})\)(?: - (.+))?$`)

func (labelledTitleParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	stem := filepath.Base(path)
	stem = stem[:len(stem)-len(filepath.Ext(stem))]
	m := labelledTitlePattern.FindStringSubmatch(stem)
	if m == nil {
		return ParsedMedia{}, false
	}
	year, _ := strconv.Atoi(m[2])
	return ParsedMedia{Title: m[1], Class: "title", Year: year, VersionLabel: m[3]}, true
}

func scanVersionLibrary(t *testing.T, st *store.Store, root string) (store.LibraryID, Result) {
	t.Helper()
	library, err := st.CreateLibrary(context.Background(), "Titles", false, "titled", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	return library.ID, rescanVersionLibrary(t, st, library.ID, root)
}

func rescanVersionLibrary(t *testing.T, st *store.Store, libraryID store.LibraryID, root string) Result {
	t.Helper()
	result, err := Scan(context.Background(), st, nil, nil, map[string]FilenameParser{"titled": labelledTitleParser{}}, nil, nil, Request{
		LibraryID: libraryID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return result
}

// mediaVersions maps each scanned file's base name to "mediaID|label".
func mediaVersions(t *testing.T, st *store.Store, libraryID store.LibraryID, result Result) map[string][2]string {
	t.Helper()
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}}
	got := map[string][2]string{}
	for _, imported := range result.Files {
		files, err := st.MediaFilesForMedia(context.Background(), imported.MediaID, filter)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			got[filepath.Base(f.Path)] = [2]string{string(f.MediaID), f.VersionLabel}
		}
	}
	return got
}

// TestScanGroupsFilesOfOneTitleIntoVersions: files of one title in one
// directory become Versions of one Media, labelled by the parser, and stay so
// on a rescan.
func TestScanGroupsFilesOfOneTitleIntoVersions(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Night Harbor (2004)/Night Harbor (2004) - 1080p.mkv", []byte("night harbor 1080p"))
	writeScanFile(t, root, "Night Harbor (2004)/Night Harbor (2004) - 4K.mkv", []byte("night harbor 4k"))
	writeScanFile(t, root, "Coastline (1998)/Coastline (1998) - Extended.mkv", []byte("coastline extended"))
	writeScanFile(t, root, "Coastline (1998)/Coastline (1998).mkv", []byte("coastline"))
	st := storetest.Open(t)

	libraryID, result := scanVersionLibrary(t, st, root)
	if result.FilesSeen != 4 || result.MediaImported != 2 {
		t.Fatalf("result = files %d media %d, want 4 files in 2 Media", result.FilesSeen, result.MediaImported)
	}
	got := mediaVersions(t, st, libraryID, result)
	harbor := got["Night Harbor (2004) - 1080p.mkv"][0]
	coast := got["Coastline (1998).mkv"][0]
	want := map[string][2]string{
		"Night Harbor (2004) - 1080p.mkv": {harbor, "1080p"},
		"Night Harbor (2004) - 4K.mkv":    {harbor, "4K"},
		"Coastline (1998) - Extended.mkv": {coast, "Extended"},
		"Coastline (1998).mkv":            {coast, ""},
	}
	if len(got) != len(want) || harbor == coast {
		t.Fatalf("versions = %v, want %v", got, want)
	}
	for name, w := range want {
		if got[name] != w {
			t.Fatalf("%s = %v, want %v (all: %v)", name, got[name], w, got)
		}
	}

	again := rescanVersionLibrary(t, st, libraryID, root)
	if again.FilesSeen != 4 || again.MediaImported != 2 {
		t.Fatalf("rescan = files %d media %d, want 4 files in 2 Media", again.FilesSeen, again.MediaImported)
	}
	if regot := mediaVersions(t, st, libraryID, again); len(regot) != len(want) {
		t.Fatalf("rescan versions = %v", regot)
	} else {
		for name, w := range want {
			if regot[name] != w {
				t.Fatalf("rescan %s = %v, want %v", name, regot[name], w)
			}
		}
	}
}

// TestScanKeepsUnlabelledDuplicatesAndOtherDirectoriesApart: two unlabelled
// files of one title, or one title in two directories, stay separate Media.
func TestScanKeepsUnlabelledDuplicatesAndOtherDirectoriesApart(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "a/Night Harbor (2004).mkv", []byte("harbor mkv"))
	writeScanFile(t, root, "a/Night Harbor (2004).mp4", []byte("harbor mp4"))
	writeScanFile(t, root, "b/Night Harbor (2004) - 4K.mkv", []byte("harbor 4k elsewhere"))
	st := storetest.Open(t)

	_, result := scanVersionLibrary(t, st, root)
	if result.FilesSeen != 3 || result.MediaImported != 3 {
		t.Fatalf("result = files %d media %d, want 3 separate Media", result.FilesSeen, result.MediaImported)
	}
}
