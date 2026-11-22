package scan

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// sidecarHydrator reports every Media's sidecar as changed and records each
// sidecar-only hydration, as a hydrator that reads files beside the media
// does when one is edited.
type sidecarHydrator struct {
	fakeMetadataHydrator
	sidecarCalls []MetadataHydrationInput
}

func (*sidecarHydrator) SidecarsChanged(context.Context, store.MediaID, string) (bool, error) {
	return true, nil
}

func (h *sidecarHydrator) HydrateSidecars(_ context.Context, input MetadataHydrationInput) error {
	h.sidecarCalls = append(h.sidecarCalls, input)
	return nil
}

func (h *sidecarHydrator) sidecarPaths() []string {
	var paths []string
	for _, call := range h.sidecarCalls {
		paths = append(paths, filepath.Base(call.Path))
	}
	slices.Sort(paths)
	return paths
}

type failingProber struct{}

func (failingProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	return ffmpeg.Probe{}, errors.New("probe failed")
}

// TestScanRefreshesSidecarsWithoutAProbe (CORE-0361): a version attached to
// an existing Media refreshes that Media's sidecar, and a rescan of unchanged
// files refreshes theirs whether the probe is missing or fails, so an edited
// or removed sidecar never waits on a probe.
func TestScanRefreshesSidecarsWithoutAProbe(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Night Harbor (2004)/Night Harbor (2004) - 1080p.mkv", []byte("night harbor 1080p"))
	writeScanFile(t, root, "Night Harbor (2004)/Night Harbor (2004) - 4K.mkv", []byte("night harbor 4k"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Titles", false, "titled", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	scan := func(prober Prober) *sidecarHydrator {
		t.Helper()
		hydrator := &sidecarHydrator{fakeMetadataHydrator: fakeMetadataHydrator{capability: MetadataHydrationCapability{MediaClasses: []store.MediaClass{store.MediaClassVideo}}}}
		result, err := ScanWithMetadataHydration(context.Background(), st, prober, nil, map[string]FilenameParser{"titled": labelledTitleParser{}}, nil, nil, nil,
			map[string]MetadataHydrator{"titled": hydrator}, Request{LibraryID: library.ID, Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if result.FilesSeen != 2 {
			t.Fatalf("result = %+v", result)
		}
		return hydrator
	}

	first := scan(videoProber{})
	if len(first.calls) != 1 || !slices.Equal(first.sidecarPaths(), []string{"Night Harbor (2004) - 4K.mkv"}) {
		t.Fatalf("first scan: %d hydrations, sidecar refreshes %v; want one hydration and the attached version's refresh", len(first.calls), first.sidecarPaths())
	}
	if first.sidecarCalls[0].MediaID != first.calls[0].MediaID {
		t.Fatalf("attached version refreshed Media %q, want the anchor %q", first.sidecarCalls[0].MediaID, first.calls[0].MediaID)
	}
	want := []string{"Night Harbor (2004) - 1080p.mkv", "Night Harbor (2004) - 4K.mkv"}
	for name, prober := range map[string]Prober{"no prober": nil, "failing prober": failingProber{}} {
		if got := scan(prober).sidecarPaths(); !slices.Equal(got, want) {
			t.Fatalf("%s: sidecar refreshes %v, want %v", name, got, want)
		}
	}
}

// TestScanRefreshesGroupedPartSidecars (CORE-0361 r1): a Media whose files a
// parser groups as parts refreshes its sidecar on a rescan of the unchanged
// parts, like a single unchanged file.
func TestScanRefreshesGroupedPartSidecars(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "01.mp3", []byte("part one"))
	writeScanFile(t, root, "03.mp3", []byte("part two"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Parts", false, "parts", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	scan := func() *sidecarHydrator {
		t.Helper()
		hydrator := &sidecarHydrator{fakeMetadataHydrator: fakeMetadataHydrator{capability: MetadataHydrationCapability{MediaClasses: []store.MediaClass{store.MediaClassAudio}}}}
		if _, err := ScanWithMetadataHydration(context.Background(), st, taggedPartsProber{}, nil, map[string]FilenameParser{"parts": cancellingPartsParser{}}, nil, nil, nil,
			map[string]MetadataHydrator{"parts": hydrator}, Request{LibraryID: library.ID, Root: root}); err != nil {
			t.Fatal(err)
		}
		return hydrator
	}
	if first := scan(); len(first.calls) != 1 {
		t.Fatalf("first scan: %d hydrations, want the group hydrated once", len(first.calls))
	}
	rescan := scan()
	if len(rescan.calls) != 0 || !slices.Equal(rescan.sidecarPaths(), []string{"01.mp3"}) {
		t.Fatalf("rescan: %d hydrations, sidecar refreshes %v; want the group's sidecar refreshed", len(rescan.calls), rescan.sidecarPaths())
	}
}
