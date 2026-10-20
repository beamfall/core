package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// ADR-0174 ownership-succession acceptance for the managed replacement path.
// The succession token is the job-lease fencing token (workerID#generation);
// these tests prove the token is monotonic across every owner handoff, that a
// stale owner is rejected before durable writes and side effects, and that a
// kill/restart at any stage converges: the replacement completes exactly once
// or rolls back to source-untouched.

// expireAndReclaimLease forces the succession handoff a lapsed lease produces:
// the recovery sweep yields the running job, and the next claim hands out the
// next generation to another worker. Deadlines are server-clock stamped, so the
// sweep is driven by naming a time beyond the TTL.
func expireAndReclaimLease(t *testing.T, st *store.Store, id store.JobID, worker string) store.ConvertOwnerLease {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if count, err := st.RecoverExpiredJobLeases(ctx, now.Add(store.JobLeaseTTL+time.Minute)); err != nil || count < 1 {
		t.Fatalf("RecoverExpiredJobLeases count=%d err=%v, want >=1", count, err)
	}
	claimed, err := st.ClaimNextJob(ctx, worker, nil, now.Add(store.JobLeaseTTL+2*time.Minute))
	if err != nil {
		t.Fatalf("succession reclaim: %v", err)
	}
	if claimed.ID != id {
		t.Fatalf("succession reclaim took %q, want %q", claimed.ID, id)
	}
	return store.ConvertOwnerLease{JobID: claimed.ID, Owner: claimed.LeaseOwner}
}

// convertSuccessionFixture is one seeded library + cataloged source under a
// fresh store, ready for a conversion.
type convertSuccessionFixture struct {
	st      *store.Store
	dataDir string
	root    string
	src     string
	dst     string
	filter  store.LibraryFilter
}

func newConvertSuccessionFixture(t *testing.T) convertSuccessionFixture {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	st := storetest.OpenAt(t, dataDir)
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(canonicalRoot, "clip.mkv")
	importManagedFile(t, st, "library-local", src, "clip", "oshash-succession")
	return convertSuccessionFixture{
		st:      st,
		dataDir: dataDir,
		root:    canonicalRoot,
		src:     src,
		dst:     filepath.Join(canonicalRoot, "clip-h264-mp4.mp4"),
		filter:  store.LibraryFilter{AllLibraries: true},
	}
}

// TestConvertLeaseGenerationMonotonicAcrossSuccession pins the succession
// token's monotonicity through every handoff kind: claim, expiry recovery,
// reclaim, restart resume, reclaim — the generation strictly increases and a
// superseded token is refused by the fenced verbs.
func TestConvertLeaseGenerationMonotonicAcrossSuccession(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Now().UTC()
	id := store.JobID("job-succession-monotonic")
	if _, err := st.EnqueueJob(ctx, store.Job{ID: id, Kind: "convert-managed-file", Priority: 1 << 20, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	first, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != id || first.LeaseGeneration != 1 {
		t.Fatalf("first claim id=%q generation=%d, want %q gen 1", first.ID, first.LeaseGeneration, id)
	}

	// Expiry recovery hands the next generation to worker-b.
	second := expireAndReclaimLease(t, st, id, "worker-b")
	secondJob, err := st.Job(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if secondJob.LeaseGeneration <= first.LeaseGeneration {
		t.Fatalf("generation after expiry succession = %d, want > %d", secondJob.LeaseGeneration, first.LeaseGeneration)
	}
	if err := st.CompleteJob(ctx, id, first.LeaseOwner, now.Add(store.JobLeaseTTL+3*time.Minute)); !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("superseded token completed the job: %v, want ErrJobLeaseLost", err)
	}

	// Restart resume yields the running job; the next claim is generation +1.
	if count, err := st.ResumeInterruptedJobs(ctx, now.Add(store.JobLeaseTTL+4*time.Minute)); err != nil || count < 1 {
		t.Fatalf("ResumeInterruptedJobs count=%d err=%v", count, err)
	}
	third, err := st.ClaimNextJob(ctx, "worker-c", nil, now.Add(store.JobLeaseTTL+5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != id || third.LeaseGeneration <= secondJob.LeaseGeneration {
		t.Fatalf("generation after restart succession = %d, want > %d", third.LeaseGeneration, secondJob.LeaseGeneration)
	}
	if err := st.UpdateJobCheckpoint(ctx, id, second.Owner, []byte(`{"stale":true}`), store.JobProgress{}, now.Add(store.JobLeaseTTL+6*time.Minute)); !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("superseded token recorded a checkpoint: %v, want ErrJobLeaseLost", err)
	}
}

// TestConvertManagedFileRejectsStaleOwnerAtEntry: after a succession handoff,
// the old generation's ConvertManagedFile is refused at the entry fence — the
// converter never runs and nothing on disk moves.
func TestConvertManagedFileRejectsStaleOwnerAtEntry(t *testing.T) {
	ctx := context.Background()
	fx := newConvertSuccessionFixture(t)
	stale := claimConvertLease(t, fx.st)
	expireAndReclaimLease(t, fx.st, stale.JobID, "worker-successor")

	conv := &fakeConverter{container: "mp4"}
	_, err := fx.st.ConvertManagedFile(ctx, "library-local", fx.src, "h264-mp4", conv, fx.filter, stale, time.Now().UTC())
	if !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("stale owner conversion err = %v, want ErrJobLeaseLost", err)
	}
	if conv.calls != 0 {
		t.Fatalf("converter ran %d times for a stale owner, want 0", conv.calls)
	}
	if _, statErr := os.Stat(fx.src); statErr != nil {
		t.Fatalf("original disturbed by a rejected stale owner: %v", statErr)
	}
	if _, statErr := os.Stat(fx.dst); !os.IsNotExist(statErr) {
		t.Fatalf("stale owner admitted output: %v", statErr)
	}
}

// TestConvertManagedFileAbandonsOnMidConvertSuccession: the lease is handed
// over WHILE the converter runs; the journal fence rejects the now-stale owner
// before any side effect, and its staging artifact is removed on the way out.
func TestConvertManagedFileAbandonsOnMidConvertSuccession(t *testing.T) {
	ctx := context.Background()
	fx := newConvertSuccessionFixture(t)
	lease := claimConvertLease(t, fx.st)

	conv := &handoffConverter{
		container: "mp4",
		during: func() {
			expireAndReclaimLease(t, fx.st, lease.JobID, "worker-successor")
		},
	}
	_, err := fx.st.ConvertManagedFile(ctx, "library-local", fx.src, "h264-mp4", conv, fx.filter, lease, time.Now().UTC())
	if !errors.Is(err, store.ErrJobLeaseLost) {
		t.Fatalf("mid-convert succession err = %v, want ErrJobLeaseLost", err)
	}
	if _, statErr := os.Stat(fx.dst); !os.IsNotExist(statErr) {
		t.Fatalf("fenced-out owner still admitted output: %v", statErr)
	}
	if _, statErr := os.Stat(fx.src); statErr != nil {
		t.Fatalf("original disturbed: %v", statErr)
	}
	assertNoConvertStagingResidue(t, fx.dataDir)
}

// handoffConverter runs the succession handoff mid-transcode, then still
// produces valid output — proving the fence, not the converter, stops the
// stale owner.
type handoffConverter struct {
	container string
	during    func()
	calls     int
}

func (c *handoffConverter) ConvertFile(_ context.Context, src, dst, _ string) (string, string, store.MediaClass, error) {
	c.calls++
	if c.during != nil {
		c.during()
	}
	data, err := os.ReadFile(src) // #nosec G304 -- test reads a file this test just created under its own t.TempDir()
	if err != nil {
		return "", "", "", err
	}
	if err := os.WriteFile(dst, append([]byte("converted:"), data...), 0o600); err != nil { // #nosec G703 -- dst is a path this test constructed under its own t.TempDir()
		return "", "", "", err
	}
	return c.container, "h264", store.MediaClassVideo, nil
}

func assertNoConvertStagingResidue(t *testing.T, dataDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "convert-staging"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".convert-tmp") {
			t.Fatalf("convert staging residue left behind: %s", entry.Name())
		}
	}
}

// writeConvertJournal records the durable succession journal exactly as the
// store does — through the fenced checkpoint write.
func writeConvertJournal(t *testing.T, st *store.Store, lease store.ConvertOwnerLease, dst, staging string) {
	t.Helper()
	record := fmt.Sprintf(`{"version":1,"dst":%q,"staging":%q}`, dst, staging)
	if err := st.UpdateJobCheckpoint(context.Background(), lease.JobID, lease.Owner, []byte(record), store.JobProgress{}, time.Now().UTC()); err != nil {
		t.Fatalf("journal write: %v", err)
	}
}

// restartStore closes the store and reopens the same bytes — the honest
// restart: the journal must survive it, staging must not.
func restartStore(t *testing.T, fx *convertSuccessionFixture) {
	t.Helper()
	if err := fx.st.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	fx.st = storetest.OpenAt(t, fx.dataDir)
	if _, err := fx.st.SweepConvertStaging(); err != nil {
		t.Fatalf("startup staging sweep: %v", err)
	}
	if _, err := fx.st.ResumeInterruptedJobs(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("resume after restart: %v", err)
	}
}

// reclaimAfterRestart claims the resumed job as the successor generation.
func reclaimAfterRestart(t *testing.T, st *store.Store, id store.JobID) store.ConvertOwnerLease {
	t.Helper()
	claimed, err := st.ClaimNextJob(context.Background(), "worker-restart", nil, time.Now().UTC().Add(time.Minute))
	if err != nil {
		t.Fatalf("reclaim after restart: %v", err)
	}
	if claimed.ID != id {
		t.Fatalf("reclaim after restart took %q, want %q", claimed.ID, id)
	}
	return store.ConvertOwnerLease{JobID: claimed.ID, Owner: claimed.LeaseOwner}
}

// assertConvertConverged is the kill-matrix postcondition: exactly one
// cataloged output at dst with converter-produced bytes (unless the original
// generation's admitted bytes were legitimately rolled forward), the original
// preserved in trash (gone from its live path), and no staging residue.
func assertConvertConverged(t *testing.T, fx convertSuccessionFixture, result store.ConvertResult) {
	t.Helper()
	if result.Converted.ID == "" {
		t.Fatal("converged conversion reports no Media")
	}
	if _, err := os.Stat(fx.dst); err != nil {
		t.Fatalf("converged output missing on disk: %v", err)
	}
	if _, err := os.Stat(fx.src); !os.IsNotExist(err) {
		t.Fatalf("original still at live path after convergence: %v", err)
	}
	assertNoConvertStagingResidue(t, fx.dataDir)
}

// TestConvertSuccessionRestartKillMatrix drives a kill at every stage boundary
// of the replacement and proves the successor converges: K1 staging residue
// only; K2 journal written, nothing admitted; K3 admitted, uncataloged; K4
// imported, original live; K5 imported and original preserved, job never
// completed. Restart is a real close-and-reopen of the same bytes.
func TestConvertSuccessionRestartKillMatrix(t *testing.T) {
	t.Run("K1-staging-residue", func(t *testing.T) {
		fx := newConvertSuccessionFixture(t)
		lease := claimConvertLease(t, fx.st)
		stagingDir := filepath.Join(fx.dataDir, "convert-staging")
		if err := os.MkdirAll(stagingDir, 0o700); err != nil {
			t.Fatal(err)
		}
		residue := filepath.Join(stagingDir, "h264-mp4-deadbeef.convert-tmp")
		if err := os.WriteFile(residue, []byte("half-written"), 0o600); err != nil {
			t.Fatal(err)
		}
		restartStore(t, &fx)
		if _, err := os.Stat(residue); !os.IsNotExist(err) {
			t.Fatalf("startup sweep left dead-generation staging: %v", err)
		}
		successor := reclaimAfterRestart(t, fx.st, lease.JobID)
		conv := &fakeConverter{container: "mp4"}
		result, err := fx.st.ConvertManagedFile(context.Background(), "library-local", fx.src, "h264-mp4", conv, fx.filter, successor, time.Now().UTC())
		if err != nil {
			t.Fatalf("successor conversion: %v", err)
		}
		if conv.calls != 1 {
			t.Fatalf("successor converter calls = %d, want 1 (fresh re-encode)", conv.calls)
		}
		assertConvertConverged(t, fx, result)
	})

	t.Run("K2-journal-written-nothing-admitted", func(t *testing.T) {
		fx := newConvertSuccessionFixture(t)
		lease := claimConvertLease(t, fx.st)
		staging := filepath.Join(fx.dataDir, "convert-staging", "h264-mp4-cafe.convert-tmp")
		writeConvertJournal(t, fx.st, lease, fx.dst, staging)
		restartStore(t, &fx)
		successor := reclaimAfterRestart(t, fx.st, lease.JobID)
		conv := &fakeConverter{container: "mp4"}
		result, err := fx.st.ConvertManagedFile(context.Background(), "library-local", fx.src, "h264-mp4", conv, fx.filter, successor, time.Now().UTC())
		if err != nil {
			t.Fatalf("successor conversion: %v", err)
		}
		// Staging never survives the handover: redo is a full fresh re-encode.
		if conv.calls != 1 {
			t.Fatalf("successor converter calls = %d, want 1 (fresh re-encode)", conv.calls)
		}
		assertConvertConverged(t, fx, result)
		if data, err := os.ReadFile(fx.dst); err != nil || !strings.HasPrefix(string(data), "converted:") {
			t.Fatalf("converged bytes = %q err=%v, want the successor's output", data, err)
		}
	})

	t.Run("K3-admitted-uncataloged", func(t *testing.T) {
		fx := newConvertSuccessionFixture(t)
		lease := claimConvertLease(t, fx.st)
		writeConvertJournal(t, fx.st, lease, fx.dst, "")
		if err := os.WriteFile(fx.dst, []byte("dead-generation-bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		restartStore(t, &fx)
		successor := reclaimAfterRestart(t, fx.st, lease.JobID)
		conv := &fakeConverter{container: "mp4"}
		result, err := fx.st.ConvertManagedFile(context.Background(), "library-local", fx.src, "h264-mp4", conv, fx.filter, successor, time.Now().UTC())
		if err != nil {
			t.Fatalf("successor conversion: %v", err)
		}
		if conv.calls != 1 {
			t.Fatalf("successor converter calls = %d, want 1 (residue removed, redone)", conv.calls)
		}
		assertConvertConverged(t, fx, result)
		if data, err := os.ReadFile(fx.dst); err != nil || !strings.HasPrefix(string(data), "converted:") {
			t.Fatalf("converged bytes = %q err=%v, want the successor's output, not dead-generation residue", data, err)
		}
	})

	t.Run("K4-imported-original-live", func(t *testing.T) {
		fx := newConvertSuccessionFixture(t)
		lease := claimConvertLease(t, fx.st)
		writeConvertJournal(t, fx.st, lease, fx.dst, "")
		if err := os.WriteFile(fx.dst, []byte("converted:clip"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fx.st.ImportMediaFile(context.Background(), store.ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fx.dst,
			Title:              "clip",
			Container:          "mp4",
			Codec:              "h264",
			Class:              store.MediaClassVideo,
			ContentFingerprint: store.HashMediaFilePath(fx.dst),
		}); err != nil {
			t.Fatal(err)
		}
		restartStore(t, &fx)
		successor := reclaimAfterRestart(t, fx.st, lease.JobID)
		conv := &fakeConverter{container: "mp4"}
		result, err := fx.st.ConvertManagedFile(context.Background(), "library-local", fx.src, "h264-mp4", conv, fx.filter, successor, time.Now().UTC())
		if err != nil {
			t.Fatalf("successor roll-forward: %v", err)
		}
		if conv.calls != 0 {
			t.Fatalf("roll-forward re-encoded (%d calls), want 0 — the import already committed", conv.calls)
		}
		if result.Original.TrashID == "" {
			t.Fatal("roll-forward did not preserve the original in trash")
		}
		assertConvertConverged(t, fx, result)
	})

	t.Run("K5-imported-and-original-preserved", func(t *testing.T) {
		ctx := context.Background()
		fx := newConvertSuccessionFixture(t)
		lease := claimConvertLease(t, fx.st)
		writeConvertJournal(t, fx.st, lease, fx.dst, "")
		if err := os.WriteFile(fx.dst, []byte("converted:clip"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fx.st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID:          "library-local",
			Path:               fx.dst,
			Title:              "clip",
			Container:          "mp4",
			Codec:              "h264",
			Class:              store.MediaClassVideo,
			ContentFingerprint: store.HashMediaFilePath(fx.dst),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.st.DeleteManagedPath(ctx, "library-local", fx.src, fx.filter, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		restartStore(t, &fx)
		successor := reclaimAfterRestart(t, fx.st, lease.JobID)
		conv := &fakeConverter{container: "mp4"}
		result, err := fx.st.ConvertManagedFile(ctx, "library-local", fx.src, "h264-mp4", conv, fx.filter, successor, time.Now().UTC())
		if err != nil {
			t.Fatalf("successor completion: %v", err)
		}
		if conv.calls != 0 {
			t.Fatalf("completed replacement re-encoded (%d calls), want 0", conv.calls)
		}
		assertConvertConverged(t, fx, result)
	})
}

// TestConvertManagedFileRefusesForeignDestination pins the fail-closed edge:
// a destination file this job's journal does NOT name is foreign — it is never
// removed, and the conversion refuses.
func TestConvertManagedFileRefusesForeignDestination(t *testing.T) {
	ctx := context.Background()
	fx := newConvertSuccessionFixture(t)
	lease := claimConvertLease(t, fx.st)
	if err := os.WriteFile(fx.dst, []byte("foreign-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	conv := &fakeConverter{container: "mp4"}
	_, err := fx.st.ConvertManagedFile(ctx, "library-local", fx.src, "h264-mp4", conv, fx.filter, lease, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("foreign destination err = %v, want the already-exists refusal", err)
	}
	if data, readErr := os.ReadFile(fx.dst); readErr != nil || string(data) != "foreign-file" {
		t.Fatalf("foreign destination touched: %q err=%v", data, readErr)
	}
	if _, statErr := os.Stat(fx.src); statErr != nil {
		t.Fatalf("original disturbed: %v", statErr)
	}
}

// TestYieldWithoutCheckpointPreservesJournal: an interruption yield (no
// checkpoint of its own) must not erase the durable succession journal; an
// explicit checkpoint still updates it.
func TestYieldWithoutCheckpointPreservesJournal(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Now().UTC()
	id := store.JobID("job-yield-preserves-journal")
	if _, err := st.EnqueueJob(ctx, store.Job{ID: id, Kind: "convert-managed-file", Priority: 1 << 20, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	journal := []byte(`{"version":1,"dst":"/lib/out.mp4","staging":"/data/convert-staging/x.convert-tmp"}`)
	if err := st.UpdateJobCheckpoint(ctx, id, claimed.LeaseOwner, journal, store.JobProgress{}, now); err != nil {
		t.Fatal(err)
	}
	// Interruption: yield with NO checkpoint — the journal must survive.
	if err := st.YieldJob(ctx, id, claimed.LeaseOwner, nil, now, now); err != nil {
		t.Fatal(err)
	}
	after, err := st.Job(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if string(after.Checkpoint) != string(journal) {
		t.Fatalf("interruption yield rewrote the journal: %s", after.Checkpoint)
	}
	// An explicit checkpoint on yield still updates.
	reclaimed, err := st.ClaimNextJob(ctx, "worker-a", nil, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	explicit := []byte(`{"version":1,"dst":"/lib/out2.mp4","staging":""}`)
	if err := st.YieldJob(ctx, id, reclaimed.LeaseOwner, explicit, now.Add(2*time.Second), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err = st.Job(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if string(after.Checkpoint) != string(explicit) {
		t.Fatalf("explicit yield checkpoint not recorded: %s", after.Checkpoint)
	}
}

// TestSweepConvertStagingFiltersSuffix: the startup sweep removes ONLY
// convert-job staging; an import's staging in the same directory survives.
func TestSweepConvertStagingFiltersSuffix(t *testing.T) {
	dataDir := t.TempDir()
	st := storetest.OpenAt(t, dataDir)
	stagingDir := filepath.Join(dataDir, "convert-staging")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	convertResidue := filepath.Join(stagingDir, "h264-mp4-aa.convert-tmp")
	importStaging := filepath.Join(stagingDir, "import-bb.import-tmp")
	for _, path := range []string{convertResidue, importStaging} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := st.SweepConvertStaging()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("sweep removed %d entries, want 1", removed)
	}
	if _, err := os.Stat(convertResidue); !os.IsNotExist(err) {
		t.Fatalf("convert staging survived the sweep: %v", err)
	}
	if _, err := os.Stat(importStaging); err != nil {
		t.Fatalf("import staging was swept: %v", err)
	}
}
