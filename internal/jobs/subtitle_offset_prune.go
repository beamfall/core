package jobs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

// The subtitle-offset nudge sweep. Nudge rows are written by the
// per-Profile offset command and are keyed on a subtitle track's byte
// fingerprint. The sweep hashes every live catalogued sidecar through the same
// ingest guard as the serve path, then reaps only old rows whose fingerprint is
// absent from that reachable set. A correction on a live track is retained
// regardless of when it was written or last played.
//
// It is a background-class job with a dedupe key, like every other seeded sweep:
// a slow run cannot stack behind itself.

// SubtitleOffsetPruneJobKind names the sweep in the job queue.
const SubtitleOffsetPruneJobKind store.JobKind = "subtitle-offset-prune"

// ErrSubtitleSidecarTooLarge identifies a sidecar rejected before its bytes are
// read into memory.
var ErrSubtitleSidecarTooLarge = errors.New("jobs: subtitle sidecar exceeds the size cap")

// SubtitleOffsetPruner is the store seam the sweep needs.
type SubtitleOffsetPruner interface {
	LiveSubtitleSidecars(ctx context.Context) ([]store.LiveSubtitleSidecar, error)
	PruneSubtitleOffsets(ctx context.Context, before time.Time, reachableFingerprints []string) (int64, error)
}

// SubtitleSidecarGuard is the byte-hardening seam supplied by the composition
// root. CheckSize must reject oversized sidecars before Guard receives content.
type SubtitleSidecarGuard interface {
	CheckSize(format string, size int64) error
	Guard(format string, raw []byte) error
}

// SubtitleOffsetPruneHandler reaps old nudges that no live sidecar fingerprint
// can resolve. The clock is injected so the retention edge is testable at an
// exact instant.
func SubtitleOffsetPruneHandler(pruner SubtitleOffsetPruner, guard SubtitleSidecarGuard, now func() time.Time) Handler {
	return func(ctx context.Context, _ store.Job) (Result, error) {
		sidecars, err := pruner.LiveSubtitleSidecars(ctx)
		if err != nil {
			return Result{}, err
		}
		reachable := reachableSubtitleFingerprints(sidecars, guard)
		cutoff := now().UTC().Add(-store.SubtitleOffsetRetention)
		if _, err := pruner.PruneSubtitleOffsets(ctx, cutoff, reachable); err != nil {
			return Result{}, err
		}
		return Result{}, nil
	}
}

func reachableSubtitleFingerprints(sidecars []store.LiveSubtitleSidecar, guard SubtitleSidecarGuard) []string {
	seen := make(map[string]struct{}, len(sidecars))
	for _, sidecar := range sidecars {
		path, ok := liveSidecarPath(sidecar.MediaPath, sidecar.RelPath)
		if !ok {
			continue
		}
		raw, err := readLiveSubtitleSidecar(path, sidecar.Format, guard)
		if err != nil {
			continue
		}
		fingerprint, err := store.SubtitleTrackFingerprint(bytes.NewReader(raw))
		if err != nil {
			continue
		}
		seen[fingerprint] = struct{}{}
	}
	fingerprints := make([]string, 0, len(seen))
	for fingerprint := range seen {
		fingerprints = append(fingerprints, fingerprint)
	}
	return fingerprints
}

func readLiveSubtitleSidecar(path, format string, guard SubtitleSidecarGuard) ([]byte, error) {
	// #nosec G304 -- liveSidecarPath confines the catalogued path before and
	// after symlink resolution.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err := guard.CheckSize(format, info.Size()); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, info.Size()))
	if err != nil {
		return nil, err
	}
	if err := guard.Guard(format, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func liveSidecarPath(mediaPath, relPath string) (string, bool) {
	dir := filepath.Dir(mediaPath)
	joined := filepath.Join(dir, relPath)
	rel, err := filepath.Rel(dir, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	realPath, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", false
	}
	realRel, err := filepath.Rel(realDir, realPath)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) || filepath.IsAbs(realRel) {
		return "", false
	}
	return realPath, true
}
