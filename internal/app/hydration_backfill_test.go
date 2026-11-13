package app

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// TestHydrationBackfillQueuesScanForMediaImportedBeforeTheMapper pins that a
// Library whose Media predate their plugin's mapper is hydrated: an ordinary
// scan skips unchanged files, so the first start with the mapper queues one
// backfill scan of a Library holding Media without the marker, and records
// the check only once the scan is queued. A Library whose Media all carry
// the marker is recorded without a scan.
func TestHydrationBackfillQueuesScanForMediaImportedBeforeTheMapper(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	library := func(name string) (store.Library, string) {
		t.Helper()
		root := t.TempDir()
		lib, err := st.CreateLibraryWithRoots(ctx, name, false, testHydrationPluginID, "profile-default", []string{root}, "")
		if err != nil {
			t.Fatal(err)
		}
		return lib, root
	}
	importTrack := func(lib store.Library, root, name string) store.MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: root + "/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	mark := func(id store.MediaID) {
		t.Helper()
		if err := st.SetMediaFields(ctx, id, []store.MediaField{{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true}}); err != nil {
			t.Fatal(err)
		}
	}
	stale, staleRoot := library("Stale")
	mark(importTrack(stale, staleRoot, "hydrated"))
	importTrack(stale, staleRoot, "predates-mapper")
	current, currentRoot := library("Current")
	mark(importTrack(current, currentRoot, "current"))

	hydrators := map[string]scanpkg.MetadataHydrator{testHydrationPluginID: newTestAdapterWithMapper(st, testHydrationMapper{})}
	scanJobs := func() []store.Job {
		t.Helper()
		jobs, err := st.ListJobs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []store.Job
		for _, job := range jobs {
			if job.Kind == scanpkg.JobKind {
				out = append(out, job)
			}
		}
		return out
	}
	checked := func(lib store.Library) bool {
		t.Helper()
		ok, err := st.HydrationBackfillChecked(ctx, lib.ID, testHydrationMarkerField)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	queueHydrationBackfills(ctx, refusingEnqueueStore{st}, hydrators, slog.New(slog.DiscardHandler))
	if got := scanJobs(); len(got) != 0 {
		t.Fatalf("scan jobs after a refused enqueue = %+v, want none", got)
	}
	if checked(stale) {
		t.Fatal("a Library whose backfill was not queued is recorded as checked")
	}

	queueHydrationBackfills(ctx, st, hydrators, slog.New(slog.DiscardHandler))
	got := scanJobs()
	if len(got) != 1 || !strings.HasSuffix(got[0].DedupeKey, ":backfill") || !strings.Contains(got[0].DedupeKey, string(stale.ID)) {
		t.Fatalf("scan jobs = %+v, want one backfill scan of %s", got, stale.ID)
	}
	if !checked(stale) || !checked(current) {
		t.Fatalf("checked = stale %v, current %v; want both recorded", checked(stale), checked(current))
	}
}
