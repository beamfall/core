package artwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// shelf is the inheritance fixture: one Library of the "shelf" plugin whose
// "box" Entities hold items and are made by "maker" Entities. A box inherits
// from the items it holds; a maker from a box it made, else from an item it
// appears on.
type shelf struct {
	t     *testing.T
	st    *store.Store
	svc   *Service
	lib   store.LibraryID
	sweep int
}

func shelfRules() []EntityPosterRule {
	return []EntityPosterRule{
		{PluginID: "shelf", Kind: "box", Sources: []EntityPosterSource{{Relation: "holds"}}},
		{PluginID: "shelf", Kind: "maker", Sources: []EntityPosterSource{
			{Relation: "made-by", FromKind: "box"},
			{Relation: "appears-on"},
		}},
	}
}

func newShelf(t *testing.T) *shelf {
	t.Helper()
	st := storetest.Open(t)
	lib, err := st.CreateLibrary(context.Background(), "Shelf", false, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, &fakeGrabber{}, t.TempDir())
	svc.SetEntityPosterRules(shelfRules())
	return &shelf{t: t, st: st, svc: svc, lib: lib.ID}
}

func (s *shelf) entity(id store.EntityID, kind string) {
	s.t.Helper()
	s.entityIn(id, kind, s.lib)
}

func (s *shelf) entityIn(id store.EntityID, kind string, lib store.LibraryID) {
	s.t.Helper()
	if err := s.st.UpsertEntity(context.Background(), store.Entity{ID: id, LibraryID: lib, Kind: kind, Title: string(id)}); err != nil {
		s.t.Fatal(err)
	}
}

// item imports one Media item into the shelf Library; a non-empty cover is
// stored as its imported (embedded or scraped) poster.
func (s *shelf) item(title, cover string) store.MediaID {
	s.t.Helper()
	return s.itemIn(title, cover, s.lib)
}

func (s *shelf) itemIn(title, cover string, lib store.LibraryID) store.MediaID {
	s.t.Helper()
	path := filepath.Join(s.t.TempDir(), title+".bin")
	if err := os.WriteFile(path, []byte("content of "+title), 0o600); err != nil {
		s.t.Fatal(err)
	}
	media, _, err := s.st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          lib,
		Path:               path,
		Title:              title,
		Container:          "bin",
		Codec:              "bin",
		ContentFingerprint: store.HashMediaFilePath(path),
		SizeBytes:          1,
		ModTime:            time.Now().UTC(),
	})
	if err != nil {
		s.t.Fatal(err)
	}
	if cover != "" {
		s.cover(media.ID, cover)
	}
	return media.ID
}

func (s *shelf) cover(id store.MediaID, cover string) {
	s.t.Helper()
	path := s.svc.PosterPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(cover), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shelf) relate(relation store.EntityRelation) {
	s.t.Helper()
	if err := s.st.UpsertEntityRelation(context.Background(), relation); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shelf) holds(box store.EntityID, item store.MediaID, order int) {
	s.t.Helper()
	s.relate(store.EntityRelation{FromEntityID: box, Name: "holds", ToMediaID: item, Cardinality: "one", SortOrder: &order})
}

// runSweep drives one whole artwork sweep job to completion through its
// yields, the way the job registry does.
func (s *shelf) runSweep() {
	s.t.Helper()
	ctx := context.Background()
	s.sweep++
	now := time.Now().UTC()
	created, err := s.st.EnqueueJob(ctx, store.Job{
		ID:        store.JobID(fmt.Sprintf("job-inherit-%d", s.sweep)),
		Kind:      SweepJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: fmt.Sprintf("inherit-%d", s.sweep),
		CreatedAt: now,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	job, err := s.st.ClaimNextJob(ctx, "worker", []store.JobClass{store.JobClassBackground}, now)
	if err != nil {
		s.t.Fatal(err)
	}
	if job.ID != created.ID {
		s.t.Fatalf("claimed job %s, want %s", job.ID, created.ID)
	}
	handler := SweepHandler(s.svc)
	for i := 0; ; i++ {
		if i > 1000 {
			s.t.Fatal("sweep never completed")
		}
		result, err := handler(ctx, job)
		if err == nil {
			break
		}
		if !errors.Is(err, jobs.ErrYielded) {
			s.t.Fatal(err)
		}
		job.Checkpoint = result.Checkpoint
	}
	if err := s.st.CompleteJob(ctx, job.ID, job.LeaseOwner, time.Now().UTC()); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shelf) poster(id store.EntityID) string {
	s.t.Helper()
	data, err := os.ReadFile(s.svc.EntityPosterPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		s.t.Fatal(err)
	}
	return string(data)
}

func (s *shelf) wantPoster(id store.EntityID, want string, inherited bool) {
	s.t.Helper()
	if got := s.poster(id); got != want {
		s.t.Fatalf("%s poster = %q, want %q", id, got, want)
	}
	if got := s.svc.EntityPosterInherited(id); got != inherited {
		s.t.Fatalf("%s inherited = %v, want %v", id, got, inherited)
	}
}

// A box with no poster takes the cover of the first item it holds, in the
// order it holds them, and the copy is marked inherited.
func TestSweepInheritsEntityPosterFromMemberMedia(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.holds("entity-box", s.item("third", "cover-third"), 3)
	s.holds("entity-box", s.item("first", ""), 1)
	s.holds("entity-box", s.item("second", "cover-second"), 2)

	s.runSweep()

	// "first" holds only the sweep's own frame grab, so the first real cover
	// in order is "second".
	s.wantPoster("entity-box", "cover-second", true)
	if !s.svc.HasEntityPoster("entity-box") {
		t.Fatal("HasEntityPoster = false after inheritance")
	}
}

// A frame grab is a fallback picture of one item, not a cover: an Entity
// whose items have nothing better stays without a poster.
func TestSweepNeverInheritsGeneratedPoster(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	item := s.item("only", "")
	s.holds("entity-box", item, 1)

	s.runSweep()

	if !s.svc.IsGeneratedArtwork(item, SlotPoster) {
		t.Fatal("fixture: the sweep did not generate a fallback poster for the item")
	}
	s.wantPoster("entity-box", "", false)
}

// A maker takes the poster of a box it made (which the same sweep inherited
// a moment earlier), and one with no box falls back to an item it appears on.
func TestSweepInheritsEntityPosterFromRelatedEntity(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.entity("entity-maker", "maker")
	s.entity("entity-guest", "maker")
	s.holds("entity-box", s.item("one", "cover-one"), 1)
	s.relate(store.EntityRelation{FromEntityID: "entity-box", Name: "made-by", ToEntityID: "entity-maker"})
	s.relate(store.EntityRelation{FromEntityID: "entity-guest", Name: "appears-on", ToMediaID: s.item("loose", "cover-loose")})

	s.runSweep()

	s.wantPoster("entity-maker", "cover-one", true)
	s.wantPoster("entity-guest", "cover-loose", true)
}

// Provider artwork wins: importing a poster replaces the inherited one and
// clears the mark, and the sweep then leaves it alone.
func TestInheritedEntityPosterYieldsToImportedPoster(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.holds("entity-box", s.item("one", "cover-one"), 1)
	s.runSweep()
	s.wantPoster("entity-box", "cover-one", true)

	if err := s.svc.ImportEntityPoster(context.Background(), "entity-box", []byte("provider")); err != nil {
		t.Fatal(err)
	}
	s.wantPoster("entity-box", "provider", false)

	s.runSweep()
	s.wantPoster("entity-box", "provider", false)
}

// Local container artwork is the Entity's own poster: it replaces an
// inherited copy and clears the mark, so the sweep leaves it alone.
func TestInheritedEntityPosterYieldsToLocalArtwork(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.holds("entity-box", s.item("one", "cover-one"), 1)
	s.runSweep()
	s.wantPoster("entity-box", "cover-one", true)

	sidecar := writeSidecar(t, t.TempDir(), "poster.jpg", testJPEG)
	if changed, err := s.svc.ImportEntityLocalArtwork(context.Background(), "entity-box", SlotPoster, filepath.Dir(sidecar), sidecar); err != nil || !changed {
		t.Fatalf("ImportEntityLocalArtwork = %v, %v; want true, nil", changed, err)
	}
	s.wantPoster("entity-box", string(testJPEG), false)

	s.runSweep()
	s.wantPoster("entity-box", string(testJPEG), false)
}

// A poster the provider supplied before any sweep is never replaced.
func TestSweepKeepsImportedEntityPoster(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.holds("entity-box", s.item("one", "cover-one"), 1)
	if err := s.svc.ImportEntityPoster(context.Background(), "entity-box", []byte("provider")); err != nil {
		t.Fatal(err)
	}

	s.runSweep()

	s.wantPoster("entity-box", "provider", false)
}

// A second sweep with nothing changed writes nothing: the inherited poster
// and its source record keep the bytes and timestamps the first sweep left.
func TestSweepInheritanceIsIdempotent(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.entity("entity-maker", "maker")
	s.holds("entity-box", s.item("one", "cover-one"), 1)
	s.relate(store.EntityRelation{FromEntityID: "entity-box", Name: "made-by", ToEntityID: "entity-maker"})
	s.runSweep()

	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	paths := []string{
		s.svc.EntityPosterPath("entity-box"),
		inheritedMarkerPath(s.svc.EntityPosterPath("entity-box")),
		inheritedMarkerPath(s.svc.EntityPosterPath("entity-maker")),
	}
	for _, path := range paths {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	// The maker's poster is the box poster's dependant; ageing the box poster
	// above would otherwise read as a changed source, so re-record it first.
	s.runSweep()
	s.wantPoster("entity-maker", "cover-one", true)
	makerPoster := s.svc.EntityPosterPath("entity-maker")
	if err := os.Chtimes(makerPoster, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(inheritedMarkerPath(makerPoster), old, old); err != nil {
		t.Fatal(err)
	}

	s.runSweep()

	for _, path := range append(paths, makerPoster) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(old) {
			t.Fatalf("%s was rewritten by an unchanged sweep (mtime %s)", filepath.Base(path), info.ModTime())
		}
	}
	s.wantPoster("entity-box", "cover-one", true)
}

// When the cover an Entity inherited from is replaced, the next sweep carries
// the new cover to the Entity and to the Entities that inherited from it.
func TestSweepRefreshesInheritedPosterWhenSourceChanges(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.entity("entity-maker", "maker")
	item := s.item("one", "cover-one")
	s.holds("entity-box", item, 1)
	s.relate(store.EntityRelation{FromEntityID: "entity-box", Name: "made-by", ToEntityID: "entity-maker"})
	s.runSweep()
	s.wantPoster("entity-maker", "cover-one", true)
	stale := filepath.Join(filepath.Dir(s.svc.EntityPosterPath("entity-box")), "poster.w320.jpg")
	if err := os.WriteFile(stale, []byte("old derivative"), 0o600); err != nil {
		t.Fatal(err)
	}

	s.cover(item, "a new and longer cover")
	s.runSweep()

	s.wantPoster("entity-box", "a new and longer cover", true)
	s.wantPoster("entity-maker", "a new and longer cover", true)
	// #nosec G304 -- stale is a test-owned derivative path.
	if data, err := os.ReadFile(stale); err == nil && string(data) == "old derivative" {
		t.Fatal("derivative of the replaced poster survived the refresh")
	}
}

// An inherited poster follows its source: when the item moves to another box
// and nothing else in the first box has a cover, the first box loses the
// poster and the second gains it.
func TestSweepDropsInheritedPosterWhenSourceLeaves(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-box", "box")
	s.entity("entity-crate", "box")
	item := s.item("one", "cover-one")
	s.holds("entity-box", item, 1)
	s.runSweep()
	s.wantPoster("entity-box", "cover-one", true)

	s.holds("entity-crate", item, 1)
	s.runSweep()

	s.wantPoster("entity-box", "", false)
	s.wantPoster("entity-crate", "cover-one", true)
}

// Only declared kinds of the declaring plugin inherit: another kind in the
// same Library, and the same kind name in another plugin's Library, get
// nothing.
func TestSweepSkipsUndeclaredKinds(t *testing.T) {
	s := newShelf(t)
	other, err := s.st.CreateLibrary(context.Background(), "Other", false, "other", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	s.entity("entity-label", "label")
	s.relate(store.EntityRelation{FromEntityID: "entity-label", Name: "holds", ToMediaID: s.item("one", "cover-one")})
	s.entityIn("entity-foreign", "box", other.ID)
	s.relate(store.EntityRelation{FromEntityID: "entity-foreign", Name: "holds", ToMediaID: s.itemIn("two", "cover-two", other.ID)})

	s.runSweep()

	s.wantPoster("entity-label", "", false)
	s.wantPoster("entity-foreign", "", false)
}

// The Entity pass yields like the Media pass: more Entities than one batch
// are all served, across invocations.
func TestSweepInheritsAcrossBatches(t *testing.T) {
	s := newShelf(t)
	item := s.item("shared", "cover-shared")
	count := entitySweepBatchSize*2 + 3
	for i := 0; i < count; i++ {
		id := store.EntityID(fmt.Sprintf("entity-maker-%03d", i))
		s.entity(id, "maker")
		s.relate(store.EntityRelation{FromEntityID: id, Name: "appears-on", ToMediaID: item})
	}

	s.runSweep()

	for i := 0; i < count; i++ {
		s.wantPoster(store.EntityID(fmt.Sprintf("entity-maker-%03d", i)), "cover-shared", true)
	}
}

// Sources are read past the first page: a maker whose only box with a poster
// sorts after a full page of boxes without one still inherits it, and a maker
// that already inherited keeps its poster when a full page of boxes without
// one is added ahead of its source.
func TestSweepReadsPosterSourcesPastOnePage(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-kept", "maker")
	s.entity("entity-a-zzz", "box")
	s.holds("entity-a-zzz", s.item("kept", "cover-kept"), 1)
	s.relate(store.EntityRelation{FromEntityID: "entity-a-zzz", Name: "made-by", ToEntityID: "entity-kept"})
	s.entity("entity-b-zzz", "box")
	s.holds("entity-b-zzz", s.item("fresh", "cover-fresh"), 1)
	s.runSweep()
	s.wantPoster("entity-kept", "cover-kept", true)

	s.entity("entity-fresh", "maker")
	s.relate(store.EntityRelation{FromEntityID: "entity-b-zzz", Name: "made-by", ToEntityID: "entity-fresh"})
	for i := 0; i < entityPosterSourcePage; i++ {
		for _, set := range []struct{ prefix, maker string }{{"a", "entity-kept"}, {"b", "entity-fresh"}} {
			id := store.EntityID(fmt.Sprintf("entity-%s-%03d", set.prefix, i))
			s.entity(id, "box")
			s.relate(store.EntityRelation{FromEntityID: id, Name: "made-by", ToEntityID: store.EntityID(set.maker)})
		}
	}
	s.runSweep()

	s.wantPoster("entity-kept", "cover-kept", true)
	s.wantPoster("entity-fresh", "cover-fresh", true)
}

// snapshotScaler reads its source when called, then waits for release before
// publishing, so a derivative scaled from an old poster lands late.
type snapshotScaler struct {
	fakeGrabber
	started chan struct{}
	release chan struct{}
}

func (s *snapshotScaler) ScaleImage(_ context.Context, src string, _ int, dst string) error {
	// #nosec G304 -- src is a test-owned poster path.
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	close(s.started)
	<-s.release
	// #nosec G703 -- dst is a test-owned derivative path.
	return os.WriteFile(dst, data, 0o600)
}

// A bucket being scaled from the old inherited poster while the poster is
// replaced or removed is published before the old buckets are cleared, never
// after: no derivative of the old poster survives.
func TestInheritedPosterChangeWaitsForDerivativeInFlight(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Service, string, posterCandidate) error
	}{
		{"replace", func(s *Service, dst string, c posterCandidate) error {
			info, ok := c.usable()
			if !ok {
				return errors.New("fixture: candidate not usable")
			}
			_, err := s.copyInheritedPoster(context.Background(), dst, c, info)
			return err
		}},
		{"remove", func(s *Service, dst string, _ posterCandidate) error {
			return s.removeInheritedPoster(context.Background(), dst)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scaler := &snapshotScaler{started: make(chan struct{}), release: make(chan struct{})}
			svc := NewService(storetest.Open(t), scaler, t.TempDir())
			dst := svc.EntityPosterPath("entity-box")
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, []byte("old cover"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(inheritedMarkerPath(dst), []byte(`{"source":"old"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "poster.jpg")
			if err := os.WriteFile(source, []byte("new cover"), 0o600); err != nil {
				t.Fatal(err)
			}

			derived := make(chan error, 1)
			go func() {
				_, err := svc.EntityPosterDerivative(context.Background(), "entity-box", "w320")
				derived <- err
			}()
			<-scaler.started
			changed := make(chan error, 1)
			go func() { changed <- tc.change(svc, dst, posterCandidate{path: source}) }()
			select {
			case err := <-changed:
				changed <- err
			case <-time.After(200 * time.Millisecond):
			}
			close(scaler.release)
			if err := <-derived; err != nil {
				t.Fatal(err)
			}
			if err := <-changed; err != nil {
				t.Fatal(err)
			}

			bucket := filepath.Join(filepath.Dir(dst), SlotPoster+".w320.jpg")
			// #nosec G304 -- bucket is a test-owned derivative path.
			if data, err := os.ReadFile(bucket); err == nil && string(data) == "old cover" {
				t.Fatal("a derivative of the old poster was published after the change")
			}
		})
	}
}
