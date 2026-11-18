package scan

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// releaseNameParser reads release-style names, "Title.Words.YYYY.tags.ext",
// into a title and year and never reports a Version label, like the film and
// TV plugin does for such names.
type releaseNameParser struct{}

func (releaseNameParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	stem := filepath.Base(path)
	stem = stem[:len(stem)-len(filepath.Ext(stem))]
	words := strings.Split(stem, ".")
	for i, word := range words {
		if year, err := strconv.Atoi(word); err == nil && len(word) == 4 && i > 0 {
			return ParsedMedia{Title: strings.Join(words[:i], " "), Class: "title", Year: year}, true
		}
	}
	return ParsedMedia{}, false
}

// frameProber reports a fixed frame size per file name.
type frameProber map[string][2]int

func (p frameProber) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	size := p[filepath.Base(path)]
	return ffmpeg.Probe{Container: "matroska", VideoCodec: "h264", HasVideo: true, VideoWidth: size[0], VideoHeight: size[1]}, nil
}

func scanReleaseLibrary(t *testing.T, st *store.Store, libraryID store.LibraryID, root string, prober Prober) Result {
	t.Helper()
	result, err := Scan(context.Background(), st, prober, nil, map[string]FilenameParser{"titled": releaseNameParser{}}, nil, nil, Request{
		LibraryID: libraryID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return result
}

func createVersionLibrary(t *testing.T, st *store.Store) store.LibraryID {
	t.Helper()
	library, err := st.CreateLibrary(context.Background(), "Titles", false, "titled", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	return library.ID
}

func assertVersions(t *testing.T, got, want map[string][2]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
	for name, w := range want {
		if got[name] != w {
			t.Fatalf("%s = %v, want %v (all: %v)", name, got[name], w, got)
		}
	}
}

// TestScanGroupsUnlabelledCopiesByResolution: two copies of one film in its
// folder whose names carry no Version label (CORE-2473) become two Versions
// of one Media, labelled by their probed resolution, and keep those labels on
// a rescan.
func TestScanGroupsUnlabelledCopiesByResolution(t *testing.T) {
	root := t.TempDir()
	const remux = "Disclosure.Day.2026.REPACK.1080p.Blu-ray.Remux-CiNEPHiLES.mkv"
	const web = "Disclosure.Day.2026.2160p.WEB-DL.H265-CUT.mkv"
	writeScanFile(t, root, "Disclosure Day (2026)/"+remux, []byte("disclosure day remux"))
	writeScanFile(t, root, "Disclosure Day (2026)/"+web, []byte("disclosure day web"))
	// A scope film's 1080p frame is 1920x800: width decides its class.
	prober := frameProber{remux: {1920, 800}, web: {3840, 1600}}
	st := storetest.Open(t)
	libraryID := createVersionLibrary(t, st)

	result := scanReleaseLibrary(t, st, libraryID, root, prober)
	if result.FilesSeen != 2 || result.MediaImported != 1 {
		t.Fatalf("result = files %d media %d, want 2 files in 1 Media", result.FilesSeen, result.MediaImported)
	}
	got := mediaVersions(t, st, libraryID, result)
	media := got[web][0]
	want := map[string][2]string{remux: {media, "1080p"}, web: {media, "2160p"}}
	assertVersions(t, got, want)

	again := scanReleaseLibrary(t, st, libraryID, root, prober)
	assertVersions(t, mediaVersions(t, st, libraryID, again), want)
}

// TestScanLabelsUnprobedUnlabelledCopiesByContainer: without a frame size,
// unlabelled copies take their container; copies in one container that differ
// only by name stay separate Media, since they may be different items.
func TestScanLabelsUnprobedUnlabelledCopiesByContainer(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "a/Night.Harbor.2004.mkv", []byte("harbor mkv"))
	writeScanFile(t, root, "a/Night.Harbor.2004.mp4", []byte("harbor mp4"))
	writeScanFile(t, root, "b/Night.Harbor.2004.Directors.Cut.mkv", []byte("harbor cut"))
	writeScanFile(t, root, "b/Night.Harbor.2004.Theatrical.mkv", []byte("harbor theatrical"))
	st := storetest.Open(t)
	libraryID := createVersionLibrary(t, st)

	result := scanReleaseLibrary(t, st, libraryID, root, nil)
	if result.FilesSeen != 4 || result.MediaImported != 3 {
		t.Fatalf("result = files %d media %d, want 4 files in 3 Media", result.FilesSeen, result.MediaImported)
	}
	got := mediaVersions(t, st, libraryID, result)
	a := got["Night.Harbor.2004.mkv"][0]
	cut, theatrical := got["Night.Harbor.2004.Directors.Cut.mkv"][0], got["Night.Harbor.2004.Theatrical.mkv"][0]
	want := map[string][2]string{
		"Night.Harbor.2004.mkv":               {a, "MKV"},
		"Night.Harbor.2004.mp4":               {a, "MP4"},
		"Night.Harbor.2004.Directors.Cut.mkv": {cut, ""},
		"Night.Harbor.2004.Theatrical.mkv":    {theatrical, ""},
	}
	if a == cut || a == theatrical || cut == theatrical {
		t.Fatalf("media = %v, want three distinct Media", got)
	}
	assertVersions(t, got, want)

	again := scanReleaseLibrary(t, st, libraryID, root, nil)
	assertVersions(t, mediaVersions(t, st, libraryID, again), want)
}

// TestScanAddsAnUnlabelledCopyBesideACatalogedFilm: a copy added to the folder
// of a film an earlier scan cataloged joins it, and the cataloged file moves
// from the default slot to its own resolution label.
func TestScanAddsAnUnlabelledCopyBesideACatalogedFilm(t *testing.T) {
	root := t.TempDir()
	const remux = "Disclosure.Day.2026.REPACK.1080p.Blu-ray.Remux-CiNEPHiLES.mkv"
	const web = "Disclosure.Day.2026.2160p.WEB-DL.H265-CUT.mkv"
	prober := frameProber{remux: {1920, 1080}, web: {3840, 2160}}
	writeScanFile(t, root, "Disclosure Day (2026)/"+remux, []byte("disclosure day remux"))
	st := storetest.Open(t)
	libraryID := createVersionLibrary(t, st)
	first := scanReleaseLibrary(t, st, libraryID, root, prober)
	if len(first.Files) != 1 {
		t.Fatalf("first scan files = %+v", first.Files)
	}
	cataloged := string(first.Files[0].MediaID)

	// The new copy's name sorts before the cataloged one.
	writeScanFile(t, root, "Disclosure Day (2026)/"+web, []byte("disclosure day web"))
	again := scanReleaseLibrary(t, st, libraryID, root, prober)
	want := map[string][2]string{remux: {cataloged, "1080p"}, web: {cataloged, "2160p"}}
	assertVersions(t, mediaVersions(t, st, libraryID, again), want)

	third := scanReleaseLibrary(t, st, libraryID, root, prober)
	assertVersions(t, mediaVersions(t, st, libraryID, third), want)
}

// TestScanKeepsAReplacedUnlabelledCopyOnItsMedia: new bytes written over a
// grouped copy keep its Media and its label on the next scan.
func TestScanKeepsAReplacedUnlabelledCopyOnItsMedia(t *testing.T) {
	root := t.TempDir()
	const remux = "Disclosure.Day.2026.REPACK.1080p.Blu-ray.Remux-CiNEPHiLES.mkv"
	const web = "Disclosure.Day.2026.2160p.WEB-DL.H265-CUT.mkv"
	prober := frameProber{remux: {1920, 800}, web: {3840, 1600}}
	writeScanFile(t, root, "Disclosure Day (2026)/"+remux, []byte("disclosure day remux"))
	writeScanFile(t, root, "Disclosure Day (2026)/"+web, []byte("disclosure day web"))
	st := storetest.Open(t)
	libraryID := createVersionLibrary(t, st)
	got := mediaVersions(t, st, libraryID, scanReleaseLibrary(t, st, libraryID, root, prober))
	media := got[web][0]
	want := map[string][2]string{remux: {media, "1080p"}, web: {media, "2160p"}}
	assertVersions(t, got, want)

	for _, name := range []string{remux, web} {
		writeScanFile(t, root, "Disclosure Day (2026)/"+name, []byte("replaced bytes of "+name))
		again := scanReleaseLibrary(t, st, libraryID, root, prober)
		assertVersions(t, mediaVersions(t, st, libraryID, again), want)
	}
}
