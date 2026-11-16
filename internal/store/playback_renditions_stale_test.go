package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// claimRenditionJob returns a live job lease that rendition generation writes
// can be fenced on.
func claimRenditionJob(t *testing.T, st *Store, id JobID) Job {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.EnqueueJob(ctx, Job{ID: id, Kind: "precompute", DedupeKey: string(id), CreatedAt: now}); err != nil {
		t.Fatalf("EnqueueJob %s: %v", id, err)
	}
	claimed, err := st.ClaimNextJob(ctx, "worker-"+string(id), nil, now)
	if err != nil {
		t.Fatalf("ClaimNextJob %s: %v", id, err)
	}
	if claimed.ID != id {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, id)
	}
	return claimed
}

func generatingRendition(t *testing.T, id PlaybackRenditionID, mediaID MediaID, fingerprint string, at time.Time) PlaybackRendition {
	t.Helper()
	return PlaybackRendition{
		ID:                 id,
		MediaID:            mediaID,
		ContentFingerprint: fingerprint,
		Kind:               PlaybackRenditionKindHLS,
		Status:             PlaybackRenditionGenerating,
		RungID:             "720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		Path:               filepath.Join(t.TempDir(), string(id)),
		CreatedAt:          at,
		LastUsedAt:         at,
	}
}

func assertRenditionStatus(t *testing.T, st *Store, id PlaybackRenditionID, want PlaybackRenditionStatus) {
	t.Helper()
	st.mu.RLock()
	got, err := st.playbackRenditionSQL(context.Background(), id)
	st.mu.RUnlock()
	if err != nil {
		t.Fatalf("read rendition %s: %v", id, err)
	}
	if got.Status != want {
		t.Fatalf("rendition %s status = %q, want %q", id, got.Status, want)
	}
}

// TestFailStalePlaybackRenditionGenerationsFailsOnlyAbandonedRows pins
// CORE-2054: a generating row no run in this process owns (left by a crashed
// earlier server) is failed, while a generation this process is still running
// is left alone so its publication still succeeds.
func TestFailStalePlaybackRenditionGenerationsFailsOnlyAbandonedRows(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	base := time.Unix(1_000, 0).UTC()

	if err := st.UpsertPlaybackRendition(ctx, generatingRendition(t, "crashed", "media-alpha", "sha256:crashed", base)); err != nil {
		t.Fatalf("Upsert crashed: %v", err)
	}
	job := claimRenditionJob(t, st, "job-live-rendition")
	live := generatingRendition(t, "live", "media-alpha", "sha256:live", base)
	generation, _, err := st.StartPlaybackRenditionGeneration(ctx, live, job.ID, job.LeaseOwner)
	if err != nil {
		t.Fatalf("StartPlaybackRenditionGeneration: %v", err)
	}

	failed, err := st.FailStalePlaybackRenditionGenerations(ctx, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("FailStalePlaybackRenditionGenerations: %v", err)
	}
	if failed != 1 {
		t.Fatalf("failed stale generations = %d, want 1", failed)
	}
	stats, err := st.PlaybackRenditionCacheHealth(ctx)
	if err != nil {
		t.Fatalf("PlaybackRenditionCacheHealth: %v", err)
	}
	if stats.Generating != 1 || stats.Failed != 1 {
		t.Fatalf("stats = %+v, want 1 generating and 1 failed", stats)
	}
	assertRenditionStatus(t, st, "crashed", PlaybackRenditionFailed)
	assertRenditionStatus(t, st, "live", PlaybackRenditionGenerating)

	// A finished run leaves no claim behind: its row is no longer generating,
	// and a later sweep finds nothing to fail.
	if err := st.FailPlaybackRenditionGeneration(ctx, live.ID, generation, base.Add(2*time.Minute)); err != nil {
		t.Fatalf("FailPlaybackRenditionGeneration: %v", err)
	}
	if n := len(st.liveRenditionGenerations); n != 0 {
		t.Fatalf("live generations after fail = %d, want 0", n)
	}
	failed, err = st.FailStalePlaybackRenditionGenerations(ctx, base.Add(3*time.Minute))
	if err != nil || failed != 0 {
		t.Fatalf("second sweep = (%d, %v), want (0, nil)", failed, err)
	}
}

// TestFailStalePlaybackRenditionGenerationsAfterRestart pins CORE-2054's crash
// half: a generation started by a process that then stopped is failed by the
// next process's sweep.
func TestFailStalePlaybackRenditionGenerationsAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := migratedDataDir(t)
	first, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open first: %v", err)
	}
	job := claimRenditionJob(t, first, "job-crashed-rendition")
	at := time.Unix(1_100, 0).UTC()
	if _, _, err := first.StartPlaybackRenditionGeneration(ctx, generatingRendition(t, "restart", "media-alpha", "sha256:restart", at), job.ID, job.LeaseOwner); err != nil {
		t.Fatalf("StartPlaybackRenditionGeneration: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}

	second, err := Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open second: %v", err)
	}
	defer second.Close()
	failed, err := second.FailStalePlaybackRenditionGenerations(ctx, at.Add(time.Hour))
	if err != nil || failed != 1 {
		t.Fatalf("FailStalePlaybackRenditionGenerations = (%d, %v), want (1, nil)", failed, err)
	}
	assertRenditionStatus(t, second, "restart", PlaybackRenditionFailed)
}

// TestUnsealFailsEncryptedGenerationAbandonedDuringSeal pins CORE-2054's Seal
// half: a run that aborted while its Library was Sealed could not write its
// failure (the plane was unreachable), so the next Unseal fails the row. A run
// that is still in flight across the Seal keeps its generating row.
func TestUnsealFailsEncryptedGenerationAbandonedDuringSeal(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	const fingerprint = "sha256:encrypted-stale-rendition"
	media, _, err := st.ImportMediaFile(ctx, encryptedImport(libraryID, fingerprint, "/enc/stale.mkv"))
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	at := time.Unix(1_200, 0).UTC()
	abortedJob := claimRenditionJob(t, st, "job-aborted-enc")
	aborted := generatingRendition(t, "precompute-enc:aborted", media.ID, fingerprint, at)
	abortedGeneration, _, err := st.StartPlaybackRenditionGeneration(ctx, aborted, abortedJob.ID, abortedJob.LeaseOwner)
	if err != nil {
		t.Fatalf("Start aborted: %v", err)
	}
	inflightJob := claimRenditionJob(t, st, "job-inflight-enc")
	inflight := generatingRendition(t, "precompute-enc:inflight", media.ID, fingerprint, at)
	if _, _, err := st.StartPlaybackRenditionGeneration(ctx, inflight, inflightJob.ID, inflightJob.LeaseOwner); err != nil {
		t.Fatalf("Start inflight: %v", err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if err := st.FailPlaybackRenditionGeneration(ctx, aborted.ID, abortedGeneration, at.Add(time.Second)); err != nil {
		t.Fatalf("FailPlaybackRenditionGeneration while Sealed: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB again: %v", err)
	}

	assertRenditionStatus(t, st, aborted.ID, PlaybackRenditionFailed)
	assertRenditionStatus(t, st, inflight.ID, PlaybackRenditionGenerating)
	stats, err := st.PlaybackRenditionCacheHealth(ctx)
	if err != nil {
		t.Fatalf("PlaybackRenditionCacheHealth: %v", err)
	}
	if stats.Generating != 1 || stats.Failed != 1 {
		t.Fatalf("stats = %+v, want 1 generating and 1 failed", stats)
	}
}
