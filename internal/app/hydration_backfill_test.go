package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/graph"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// rootlessStore reports every Library without roots, the way a Library whose
// roots were all removed reads.
type rootlessStore struct{ *store.Store }

func (rootlessStore) LibraryRoots(context.Context, store.LibraryID) ([]string, error) {
	return nil, nil
}

// sealedMediaStore refuses Media reads, the way an encrypted Library whose
// database is not yet unsealed does.
type sealedMediaStore struct{ *store.Store }

func (sealedMediaStore) MediaLackingFieldInLibrary(context.Context, store.LibraryID, store.MediaClass, string) (map[store.MediaID]struct{}, error) {
	return nil, store.ErrEncryptedLibraryDBDisabled
}

// TestHydrationBackfillRunsAfterRuntimeUnseal pins that an encrypted Library
// sealed at start is checked once an operator unseals it, and only when the
// unseal succeeds.
func TestHydrationBackfillRunsAfterRuntimeUnseal(t *testing.T) {
	ctx := context.Background()
	inner := &unsealRecorder{}
	runs := 0
	lifecycle := hydrationBackfillOnUnseal{encryptedLibraryDBLifecycle: inner, backfill: func(context.Context) { runs++ }}
	inner.err = errors.New("wrong passphrase")
	if err := lifecycle.UnsealEncryptedLibraryDB(ctx, "lib-1", nil); err == nil || runs != 0 {
		t.Fatalf("failed unseal: err %v, backfill runs %d; want the error and no run", err, runs)
	}
	inner.err = nil
	if err := lifecycle.UnsealEncryptedLibraryDB(ctx, "lib-1", nil); err != nil || runs != 1 {
		t.Fatalf("unseal: err %v, backfill runs %d; want no error and one run", err, runs)
	}
	var _ graph.EncryptedLibraryDBLifecycle = lifecycle
	if _, ok := any(lifecycle).(interface {
		AbortEncryptedLibraryDBProvision(context.Context, store.LibraryID) error
	}); !ok {
		t.Fatal("the unseal wrapper hides the staged-provision abort the graph handler asserts for")
	}
}

type unsealRecorder struct {
	encryptedLibraryDBLifecycle
	err error
}

func (r *unsealRecorder) UnsealEncryptedLibraryDB(context.Context, store.LibraryID, store.EncryptedLibraryDBCipher) error {
	return r.err
}

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

	for name, unavailable := range map[string]hydrationBackfillStore{
		"refused enqueue": refusingEnqueueStore{st},
		"no roots":        rootlessStore{st},
		"sealed database": sealedMediaStore{st},
	} {
		queueHydrationBackfills(ctx, unavailable, hydrators, slog.New(slog.DiscardHandler))
		if got := scanJobs(); len(got) != 0 {
			t.Fatalf("%s: scan jobs = %+v, want none", name, got)
		}
		if checked(stale) {
			t.Fatalf("%s: a Library whose backfill was not queued is recorded as checked", name)
		}
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
