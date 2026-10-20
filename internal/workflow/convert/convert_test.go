package convert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// fakeManagedConverter records the ConvertManagedFile call the handler makes,
// standing in for the store mutation seam.
type fakeManagedConverter struct {
	gotLibrary store.LibraryID
	gotSrc     string
	gotProfile string
	gotFilter  store.LibraryFilter
	gotLease   store.ConvertOwnerLease
	result     store.ConvertResult
	err        error
	calls      int
}

func (f *fakeManagedConverter) ConvertManagedFile(_ context.Context, libraryID store.LibraryID, srcPath, profile string, _ store.ManagedFileConverter, filter store.LibraryFilter, lease store.ConvertOwnerLease, _ time.Time) (store.ConvertResult, error) {
	f.calls++
	f.gotLibrary = libraryID
	f.gotSrc = srcPath
	f.gotProfile = profile
	f.gotFilter = filter
	f.gotLease = lease
	return f.result, f.err
}

func TestNewConvertJobRejectsUnknownProfile(t *testing.T) {
	_, err := NewConvertJob("library-local", "/lib/clip.mkv", "no-such-profile", store.LibraryFilter{}, time.Unix(1, 0))
	if err == nil {
		t.Fatal("NewConvertJob accepted an unknown profile")
	}
}

func TestNewConvertJobRequiresSource(t *testing.T) {
	if _, err := NewConvertJob("library-local", "", "h264-mp4", store.LibraryFilter{}, time.Unix(1, 0)); err == nil {
		t.Fatal("NewConvertJob accepted an empty source path")
	}
}

// TestNewConvertJobBackgroundClassAndDedupe asserts the conversion is
// background-class (so the governor preempts it under interactive load) and
// deduped on library+path+profile (a re-requested identical conversion does not
// stack).
func TestNewConvertJobBackgroundClassAndDedupe(t *testing.T) {
	job, err := NewConvertJob("library-local", "/lib/clip.mkv", "h264-mp4", store.LibraryFilter{AllLibraries: true}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("NewConvertJob: %v", err)
	}
	if job.Kind != JobKind {
		t.Fatalf("kind = %q, want %q", job.Kind, JobKind)
	}
	if job.Class != store.JobClassBackground {
		t.Fatalf("class = %q, want background (governor-preemptible)", job.Class)
	}
	if job.DedupeKey != "convert-managed-file:library-local:/lib/clip.mkv:h264-mp4" {
		t.Fatalf("dedupe key = %q", job.DedupeKey)
	}
	if len(job.Payload) == 0 {
		t.Fatal("payload is empty")
	}
}

// TestHandlerDrivesStoreWithSnapshottedAuthority confirms the handler unmarshals
// the job and calls ConvertManagedFile with the snapshotted library, source,
// profile, and authority filter.
func TestHandlerDrivesStoreWithSnapshottedAuthority(t *testing.T) {
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	job, err := NewConvertJob("library-local", "/lib/clip.mkv", "hevc-mp4", filter, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("NewConvertJob: %v", err)
	}
	mc := &fakeManagedConverter{result: store.ConvertResult{Converted: store.Media{ID: "media-new"}}}
	svc := NewService(mc, nil)

	// The handler passes its job's OWN lease through verbatim — the ADR-0174
	// ownership-succession token every store-side fence checks.
	job.ID = "job-convert-1"
	job.LeaseOwner = "worker-a#7"

	if _, err := Handler(svc)(context.Background(), job); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if mc.calls != 1 {
		t.Fatalf("ConvertManagedFile called %d times, want 1", mc.calls)
	}
	if mc.gotLibrary != "library-local" || mc.gotSrc != "/lib/clip.mkv" || mc.gotProfile != "hevc-mp4" {
		t.Fatalf("store called with library=%q src=%q profile=%q", mc.gotLibrary, mc.gotSrc, mc.gotProfile)
	}
	if len(mc.gotFilter.AllowedIDs) != 1 || mc.gotFilter.AllowedIDs[0] != "library-local" {
		t.Fatalf("authority filter not snapshotted: %#v", mc.gotFilter)
	}
	if mc.gotLease.JobID != "job-convert-1" || mc.gotLease.Owner != "worker-a#7" {
		t.Fatalf("job lease not passed through: %#v", mc.gotLease)
	}
}

// TestHandlerSurfacesStoreError confirms an authority/FFmpeg rejection fails the
// job rather than silently succeeding.
func TestHandlerSurfacesStoreError(t *testing.T) {
	job, err := NewConvertJob("library-local", "/lib/clip.mkv", "h264-mp4", store.LibraryFilter{}, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("NewConvertJob: %v", err)
	}
	mc := &fakeManagedConverter{err: errors.New("path is outside the library roots")}
	if _, err := Handler(NewService(mc, nil))(context.Background(), job); err == nil {
		t.Fatal("Handler swallowed a store rejection")
	}
}
