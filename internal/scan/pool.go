package scan

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// WorkerGovernor is scan's slice of the resource governor (interface at the
// consumer, ADR-0016): GenerationWorkers sizes the fingerprint/probe worker
// pool exactly like ADR-0013 §2 sizes background generation, so the scan
// pipeline can never out-fan the operator's worker budget on weak hardware.
// *governor.Governor satisfies it; a nil WorkerGovernor
// keeps the historical fully-sequential single-worker walk.
type WorkerGovernor interface {
	GenerationWorkers() int
}

// ImageDeriver completes the required still-image artwork after the ordered
// import has established the canonical Media ID. Implementations must be
// idempotent and return promptly when ctx is canceled: a resumed scan may
// safely call it again for the same fingerprint, while cancellation drains
// every owned derivation goroutine before returning.
type ImageDeriver interface {
	DeriveImage(ctx context.Context, id store.MediaID, path, fingerprint string) error
}

// scanWorkers resolves the pool size from the governor seam, clamped to at
// least 1 so a zero/negative configuration can never wedge the scan.
func scanWorkers(gov WorkerGovernor) int {
	if gov == nil {
		return 1
	}
	if workers := gov.GenerationWorkers(); workers > 1 {
		return workers
	}
	return 1
}

// workerTask carries one admitted file through the bounded worker pool. The
// producer (the WalkDir goroutine) fills path/info; one pool worker fills the
// expensive per-file results (fast-path lookup, content hash, OSHash, probe,
// perceptual hash — everything side-effect-free); the consumer reads tasks
// strictly in walk order, so every store WRITE and all order-sensitive state
// (part grouping, checkpoints, progress) stays exactly as sequential.
//
// Memory ceiling: the pool holds at most workers tasks in flight plus a
// workers-sized ordered buffer plus the one task being consumed — O(workers)
// tasks outstanding, each carrying O(1) metadata (the probe's stream
// inventory, two hash strings). The heavy allocations inside hashing and
// frame-grabbing are per-worker transients, so peak memory is bounded by the
// governor's worker budget, never by library size.
type workerTask struct {
	path string
	info fileStat

	// Filled by the worker; the consumer must not read them before done closes.
	stored      store.StoredFingerprint
	fastPath    bool
	fingerprint string
	oshash      string
	probe       probeOutcome
	phash       string
	local       []LocalMetadata
	err         error
	done        chan struct{}
}

// fileStat is the slice of fs.FileInfo the pipeline needs (size + mtime),
// captured at admit time so tasks never hold a live DirEntry.
type fileStat struct {
	size    int64
	modTime time.Time
}

// probeOutcome is one file's probe result as the worker observed it. succeeded
// mirrors the sequential probeSucceeded flag: false means a nil Prober, an
// image (never probed, ADR-0043), or a failed probe — all of which degrade to
// path-derived metadata exactly as before.
type probeOutcome struct {
	succeeded bool
	hasVideo  bool
	hasAudio  bool
	value     ffmpeg.Probe
}

// fill computes the expensive, side-effect-free per-file work: the fast-path
// fingerprint lookup (ADR-0009 identity preserved — an unchanged triple still
// performs ZERO hashes and ZERO probes), then for changed/new files the
// whole-file content hash, the OSHash, the bounded probe, and the perceptual
// hash. Store access here is read-only; the store serializes it internally
// (one write connection, ADR-0011 §1), so concurrent fills are safe alongside
// the consumer's ordered writes.
func (t *workerTask) fill(ctx context.Context, st *store.Store, prober Prober, grabber FrameGrabber) {
	stored, err := st.MediaFileFingerprintByPath(ctx, t.path)
	if err != nil {
		t.err = err
		return
	}
	t.stored = stored
	// The stored mtime is millisecond-resolution (the store truncates on
	// write), so compare at the same granularity — a file mtime carries
	// sub-millisecond precision that would otherwise never match.
	modTimeMS := t.info.modTime.UTC().Truncate(time.Millisecond)
	t.fastPath = stored.OK &&
		stored.ContentFingerprint != "" &&
		stored.SizeBytes == t.info.size &&
		!stored.ModTime.IsZero() &&
		stored.ModTime.Equal(modTimeMS)
	if t.fastPath {
		return
	}
	fingerprint, err := hashFileContent(t.path)
	if err != nil {
		t.err = err
		return
	}
	t.fingerprint = fingerprint
	oshash, err := OSHash(t.path)
	if err != nil {
		t.err = err
		return
	}
	t.oshash = oshash
	if prober != nil && !imageClassForPath(t.path) {
		probeCtx, probeCancel := context.WithTimeout(ctx, probeTimeout)
		probe, probeErr := prober.Probe(probeCtx, t.path)
		probeCancel()
		if probeErr == nil {
			t.probe = probeOutcome{
				succeeded: true,
				hasVideo:  probe.HasVideo,
				hasAudio:  probe.AudioCodec != "",
				value:     probe,
			}
		}
	}
	if grabber != nil && t.probe.hasVideo {
		// Best-effort, like the probe fallback: a failed grab (no FFmpeg,
		// corrupt frame) leaves phash empty for a later scan — the store never
		// overwrites a stored phash with "".
		if phash, phashErr := PHash(ctx, grabber, t.path, t.probe.value.DurationSeconds); phashErr == nil {
			t.phash = phash
		} else {
			slog.Debug("scan: perceptual hash failed", "path", t.path, "error", phashErr.Error())
		}
	}
}

// runScanPipeline walks root and fans admitted files across a bounded worker
// pool: the walk goroutine admits files in order and feeds
// both an unbuffered work channel (workers pull and fill) and a workers-sized
// ordered channel; the calling goroutine consumes the ordered channel, waiting
// on each task's done gate, so consume runs strictly in walk order no matter
// how workers interleave. Control errors from consume (context cancellation,
// the checkpoint-batch yield) stop the pipeline: the shared pipeline context
// is cancelled, the walk unwinds, the remaining ordered tasks are drained
// (their fills are side-effect-free, so discarding them is safe — resume
// re-does them from the checkpoint), and the control error is returned.
// Per-file errors are handled by skipOrPropagate exactly like the sequential
// walk: logged and skipped.
func runScanPipeline(
	ctx context.Context,
	root string,
	workers int,
	admit func(string, fs.DirEntry, error) (*workerTask, error),
	fill func(context.Context, *workerTask),
	consume func(*workerTask) error,
	skipOrPropagate func(string, error) error,
) error {
	pipeCtx, stop := context.WithCancel(ctx)
	defer stop()

	work := make(chan *workerTask)
	// The ordered buffer is the pool's memory ceiling (O(workers) outstanding
	// tasks; see workerTask): sized to the worker count so the walk can keep
	// every worker fed without unbounded read-ahead of a huge library.
	ordered := make(chan *workerTask, workers)

	var workerWG sync.WaitGroup
	workerWG.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer workerWG.Done()
			for t := range work {
				fill(pipeCtx, t)
				close(t.done)
			}
		}()
	}

	walkDone := make(chan error, 1)
	go func() {
		defer close(ordered)
		defer close(work)
		walkDone <- filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr == nil {
				if skipErr := skipExcludedScanDirectory(entry.Name(), entry.IsDir()); skipErr != nil {
					return skipErr
				}
			}
			t, admitErr := admit(path, entry, walkErr)
			if admitErr != nil {
				return skipOrPropagate(path, admitErr)
			}
			if t == nil {
				return pipeCtx.Err()
			}
			// work first, then ordered: every task the consumer waits on is
			// guaranteed to reach a worker, so its done gate always closes.
			select {
			case work <- t:
			case <-pipeCtx.Done():
				return pipeCtx.Err()
			}
			select {
			case ordered <- t:
			case <-pipeCtx.Done():
				return pipeCtx.Err()
			}
			return pipeCtx.Err()
		})
	}()

	var consumeErr error
	for t := range ordered {
		<-t.done
		if consumeErr != nil {
			continue // draining: the pipeline is already stopping
		}
		if err := skipOrPropagate(t.path, consume(t)); err != nil {
			consumeErr = err
			stop()
		}
	}
	workerWG.Wait()
	walkErr := <-walkDone
	if consumeErr != nil {
		return consumeErr
	}
	return walkErr
}
