package playback

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

const (
	PrecomputeJobKind        store.JobKind = "playback.precompute"
	PrecomputeEvictJobKind   store.JobKind = "playback.precompute.evict"
	precomputeEvictDedupeKey               = "playback-precompute-evict"
)

var (
	ErrPrecomputeAdmission    = errors.New("precompute disk budget is fully reserved")
	ErrPrecomputeInputChanged = errors.New("precompute input generation changed")
	ErrPrecomputeIncomplete   = errors.New("precompute artifact is incomplete")
)

type PrecomputeRequest struct {
	MediaID            store.MediaID
	ContentFingerprint string
	RenditionID        store.PlaybackRenditionID
	RungID             string
	Codec              string
	Height             int
	Bandwidth          int
	SourcePath         string
	Path               string
	Priority           int
}

type PrecomputeSourceRule struct {
	LibraryID store.LibraryID
	Requests  []PrecomputeRequest
}

type PrecomputePayload struct {
	MediaID            store.MediaID             `json:"mediaID"`
	ContentFingerprint string                    `json:"contentFingerprint"`
	RenditionID        store.PlaybackRenditionID `json:"renditionID"`
	RungID             string                    `json:"rungID"`
	Codec              string                    `json:"codec"`
	Height             int                       `json:"height"`
	Bandwidth          int                       `json:"bandwidth"`
	SourcePath         string                    `json:"sourcePath"`
	Path               string                    `json:"path"`
}

type PrecomputeEvictPayload struct {
	CacheRoot string `json:"cacheRoot"`
	MaxBytes  int64  `json:"maxBytes"`
}

type PrecomputeSpec struct {
	MediaID            store.MediaID
	ContentFingerprint string
	RenditionID        store.PlaybackRenditionID
	RungID             string
	Codec              string
	Height             int
	Bandwidth          int
	SourcePath         string
	OutputDir          string
	SourceVideo        VideoSignal
}

type PrecomputeGenerator interface {
	PrecomputeHLS(context.Context, PrecomputeSpec) error
}

type PrecomputeJobHandler struct {
	Store     *store.Store
	Generator PrecomputeGenerator
	Admission *PrecomputeResourceGovernor
	Now       func() time.Time
}

// PrecomputeResourceGovernor reserves estimated output bytes before a
// generator starts. Ready bytes and concurrent reservations share one
// mutex-linearized budget, so no pair of workers can both admit against the
// same apparent free space.
type PrecomputeResourceGovernor struct {
	mu       sync.Mutex
	budget   int64
	reserved int64
}

type PrecomputeReservation struct {
	governor *PrecomputeResourceGovernor
	bytes    int64
	release  sync.Once
}

func NewPrecomputeResourceGovernor(budget int64) *PrecomputeResourceGovernor {
	return &PrecomputeResourceGovernor{budget: budget}
}

func (g *PrecomputeResourceGovernor) Reserve(estimateBytes int64, readyBytes func() (int64, error)) (*PrecomputeReservation, error) {
	if g == nil || g.budget <= 0 || estimateBytes <= 0 || readyBytes == nil {
		return nil, ErrPrecomputeAdmission
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	ready, err := readyBytes()
	if err != nil {
		return nil, err
	}
	if ready < 0 {
		return nil, errors.New("precompute ready bytes must be non-negative")
	}
	if ready > g.budget-g.reserved || estimateBytes > g.budget-g.reserved-ready {
		return nil, ErrPrecomputeAdmission
	}
	g.reserved += estimateBytes
	return &PrecomputeReservation{governor: g, bytes: estimateBytes}, nil
}

func (r *PrecomputeReservation) Release() {
	if r == nil || r.governor == nil {
		return
	}
	r.release.Do(func() {
		r.governor.mu.Lock()
		r.governor.reserved -= r.bytes
		r.governor.mu.Unlock()
	})
}

func (g *PrecomputeResourceGovernor) reservedBytes() int64 {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reserved
}

type PrecomputeEvictJobHandler struct {
	Store *store.Store
	Now   func() time.Time
}

func RequestPrecomputeRendition(ctx context.Context, st *store.Store, req PrecomputeRequest, now time.Time) (store.Job, error) {
	if st == nil {
		return store.Job{}, errors.New("store is required")
	}
	req, err := hydratePrecomputeRequest(ctx, st, req)
	if err != nil {
		return store.Job{}, err
	}
	if err := validatePrecomputeRequest(req); err != nil {
		return store.Job{}, err
	}
	now = normalizePrecomputeTime(now)
	payload, err := json.Marshal(req.payload())
	if err != nil {
		return store.Job{}, err
	}
	return st.EnqueueJob(ctx, store.Job{
		Kind:      PrecomputeJobKind,
		Class:     store.JobClassBackground,
		Priority:  req.Priority,
		DedupeKey: precomputeDedupeKey(req.RenditionID),
		Payload:   payload,
		CreatedAt: now,
		RunAfter:  now,
	})
}

// PrecomputeMediaRenditions is the explicit generation-setup entry point
// ADR-0057 §2 requires: an operator, Schedule, Source/Collection rule, event,
// or display setup names a Media and the server enqueues one precompute job per
// honest ladder rung the source can back (ADR-0010 §8 — never upscaled). It is
// the shared substrate behind every explicit trigger (the Console mutation, a
// Schedule's BuildJobs, a Source rule) so generation stays uniform and never
// fires on popularity (ADR-0057 §2: no automatic popularity generation in v1).
//
// renditionTarget names the cached codec to precompute (the H.264 baseline the
// HLS ladder transcodes to). cacheRoot is the disposable cache directory each
// rendition's bytes live under (ADR-0057 §3 — server-derived, rebuildable, not
// a Library Version); each rung gets a deterministic sub-path keyed by Media,
// fingerprint, and rung so a regenerate reuses the same cache identity. The
// returned jobs are the enqueued precompute jobs (one per rung). The claimed
// handler installs Generating only after admission, so a deduped re-trigger can
// never invalidate the generation already doing the work.
func PrecomputeMediaRenditions(ctx context.Context, st *store.Store, mediaID store.MediaID, renditionTarget, cacheRoot string, now time.Time) ([]store.Job, error) {
	if st == nil {
		return nil, errors.New("store is required")
	}
	if mediaID == "" {
		return nil, errors.New("media ID is required")
	}
	if strings.TrimSpace(cacheRoot) == "" {
		return nil, errors.New("cache root is required")
	}
	if strings.TrimSpace(renditionTarget) == "" {
		renditionTarget = "h264"
	}
	media, err := st.Media(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return nil, err
	}
	if media.Class == store.MediaClassImage {
		return nil, errors.New("image media cannot be precomputed")
	}
	// Encrypted Libraries fail closed: a precomputed HLS rendition
	// is a plaintext derivative written to the shared cache root, and no encrypted
	// -plane write path exists yet, so refuse to enqueue rather than
	// leak plaintext segments — whether the Library is Sealed or Unsealed.
	encrypted, err := st.LibraryEncrypted(ctx, media.LibraryID)
	if err != nil {
		return nil, err
	}
	if encrypted {
		return nil, nil
	}
	files, err := st.MediaFilesForMedia(ctx, mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("media has no source files")
	}
	source := files[0]
	streams, err := st.MediaFileStreams(ctx, source.ContentFingerprint)
	if err != nil {
		return nil, err
	}
	sourceVideo := VideoSignalFromStreams(streams)
	if reason := sourceVideo.FullTranscodeRefusal(); reason != "" {
		return nil, errors.New(reason)
	}
	rungs := rungsFor(media.VideoWidth, media.VideoHeight)
	now = normalizePrecomputeTime(now)
	out := make([]store.Job, 0, len(rungs))
	for _, rung := range rungs {
		renditionID := PrecomputeRenditionID(mediaID, source.ContentFingerprint, rung.ID)
		req := PrecomputeRequest{
			MediaID:            mediaID,
			ContentFingerprint: source.ContentFingerprint,
			RenditionID:        renditionID,
			RungID:             rung.ID,
			Codec:              renditionTarget,
			Height:             rung.Height,
			Bandwidth:          rung.Bandwidth,
			SourcePath:         source.Path,
			Path:               filepath.Join(cacheRoot, string(renditionID)),
		}
		// A height-unknown source yields the single "source" rung with Height 0;
		// the precompute request requires a positive height, so stamp the source
		// height (still never upscaled — it IS the source) before validating.
		if req.Height <= 0 {
			req.Height = media.VideoHeight
		}
		if req.Height <= 0 {
			// No probed dimensions at all: cannot honestly size a rendition, so
			// skip rather than invent one (ADR-0010 §8: honest to the source).
			continue
		}
		job, err := RequestPrecomputeRendition(ctx, st, req, now)
		if err != nil {
			return out, err
		}
		out = append(out, job)
	}
	return out, nil
}

// PrecomputeRenditionID derives the stable cache identity for one (Media,
// fingerprint, rung) precomputed rendition. It is deterministic so an explicit
// re-trigger reuses the same cache object (regenerate, not duplicate) and so
// the eviction/serving paths can address it.
func PrecomputeRenditionID(mediaID store.MediaID, fingerprint, rungID string) store.PlaybackRenditionID {
	cleanFingerprint := strings.ReplaceAll(fingerprint, ":", "-")
	return store.PlaybackRenditionID(fmt.Sprintf("precompute:%s:%s:%s", mediaID, cleanFingerprint, rungID))
}

// PurgePrecomputedRenditionsForMedia removes every precomputed HLS rendition
// directory belonging to one Media — across all its fingerprints and rungs —
// under the disk cache root, matching on the shared precompute:<mediaID>: prefix
// PrecomputeRenditionID mints (the on-disk directory name). It is the reconcile
// hook for the Encrypted-Library crash-recovery sweep (ADR-0045 §reconcile,
// ENCPRIV-10d): precompute is fail-closed for an Encrypted Library, so any
// rendition dir found for its media is a crash-stranded plaintext HLS leak. An
// empty cache root (no cache configured) is a no-op; missing entries are fine.
// Hard delete is correct — the segments are losslessly regenerable from the
// sealed original, so invariant #6 recoverability does not attach to leaked
// plaintext, which invariant #5 forbids at rest.
func PurgePrecomputedRenditionsForMedia(cacheRoot string, mediaID store.MediaID) error {
	if strings.TrimSpace(cacheRoot) == "" || mediaID == "" {
		return nil
	}
	prefix := fmt.Sprintf("precompute:%s:", mediaID)
	entries, err := os.ReadDir(cacheRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var joined error
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(cacheRoot, entry.Name())); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	publishedMediaDir := filepath.Join(cacheRoot, ".precompute-published", fmt.Sprintf("%x", []byte(mediaID)))
	if err := os.RemoveAll(publishedMediaDir); err != nil {
		joined = errors.Join(joined, err)
	}
	return joined
}

// SweepPrecomputeResidue runs before job workers resume. Every staging entry
// belongs to a process that can no longer be running, while immutable published
// generation directories survive only when a ready catalog row references
// them. This bounds both pre-publish and rename-before-CAS crash residue.
func SweepPrecomputeResidue(ctx context.Context, st *store.Store, cacheRoot string) (int, error) {
	if st == nil {
		return 0, errors.New("store is required")
	}
	if strings.TrimSpace(cacheRoot) == "" {
		return 0, errors.New("cache root is required")
	}
	removed := 0
	stagingRoot := filepath.Join(cacheRoot, ".precompute-staging")
	entries, err := os.ReadDir(stagingRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(stagingRoot, entry.Name())); err != nil {
			return removed, err
		}
		removed++
	}

	paths, err := st.ReadyPlaybackRenditionPaths(ctx)
	if err != nil {
		return removed, err
	}
	referenced := make(map[string]bool, len(paths))
	for _, path := range paths {
		referenced[filepath.Clean(path)] = true
	}
	publishedRoot := filepath.Join(cacheRoot, ".precompute-published")
	mediaDirs, err := os.ReadDir(publishedRoot)
	if errors.Is(err, os.ErrNotExist) {
		return removed, nil
	}
	if err != nil {
		return removed, err
	}
	for _, mediaDir := range mediaDirs {
		mediaPath := filepath.Join(publishedRoot, mediaDir.Name())
		generations, readErr := os.ReadDir(mediaPath)
		if readErr != nil {
			return removed, readErr
		}
		for _, generation := range generations {
			path := filepath.Join(mediaPath, generation.Name())
			if referenced[filepath.Clean(path)] {
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				return removed, err
			}
			removed++
		}
		remaining, readErr := os.ReadDir(mediaPath)
		if readErr == nil && len(remaining) == 0 {
			_ = os.Remove(mediaPath)
		}
	}
	return removed, nil
}

func PrecomputeScheduleID(libraryID store.LibraryID) store.ScheduleID {
	return store.ScheduleID("playback-precompute:" + string(libraryID))
}

func (r PrecomputeSourceRule) ScheduleID() store.ScheduleID {
	return PrecomputeScheduleID(r.LibraryID)
}

func (r PrecomputeSourceRule) BuildJobs(now time.Time) ([]store.Job, error) {
	if r.LibraryID == "" {
		return nil, errors.New("library ID is required")
	}
	return PrecomputeScheduleJobs(now, r.Requests)
}

func PrecomputeScheduleJobs(now time.Time, requests []PrecomputeRequest) ([]store.Job, error) {
	now = normalizePrecomputeTime(now)
	out := make([]store.Job, 0, len(requests))
	for _, req := range requests {
		if err := validatePrecomputeRequest(req); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(req.payload())
		if err != nil {
			return nil, err
		}
		out = append(out, store.Job{
			Kind:      PrecomputeJobKind,
			Class:     store.JobClassBackground,
			Priority:  req.Priority,
			DedupeKey: precomputeDedupeKey(req.RenditionID),
			Payload:   payload,
			CreatedAt: now,
			RunAfter:  now,
		})
	}
	return out, nil
}

func RequestPrecomputeEviction(ctx context.Context, st *store.Store, cacheRoot string, maxBytes int64, now time.Time) (store.Job, error) {
	if st == nil {
		return store.Job{}, errors.New("store is required")
	}
	payload, err := precomputeEvictPayload(cacheRoot, maxBytes)
	if err != nil {
		return store.Job{}, err
	}
	now = normalizePrecomputeTime(now)
	return st.EnqueueJob(ctx, store.Job{
		Kind:      PrecomputeEvictJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: precomputeEvictDedupeKey,
		Payload:   payload,
		CreatedAt: now,
		RunAfter:  now,
	})
}

func PrecomputeEvictScheduleJob(cacheRoot string, maxBytes int64) func(time.Time) ([]store.Job, error) {
	return func(now time.Time) ([]store.Job, error) {
		payload, err := precomputeEvictPayload(cacheRoot, maxBytes)
		if err != nil {
			return nil, err
		}
		now = normalizePrecomputeTime(now)
		return []store.Job{{
			Kind:      PrecomputeEvictJobKind,
			Class:     store.JobClassBackground,
			DedupeKey: precomputeEvictDedupeKey,
			Payload:   payload,
			CreatedAt: now,
			RunAfter:  now,
		}}, nil
	}
}

func (h PrecomputeJobHandler) Handle(ctx context.Context, job store.Job) error {
	if h.Store == nil {
		return errors.New("store is required")
	}
	if h.Generator == nil {
		return errors.New("precompute generator is required")
	}
	if h.Admission == nil {
		return errors.New("precompute admission governor is required")
	}
	var payload PrecomputePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("precompute payload: %w", err)
	}
	req := payload.request()
	if err := validatePrecomputeRequest(req); err != nil {
		return err
	}
	if job.ID == "" || strings.TrimSpace(job.LeaseOwner) == "" {
		return errors.New("precompute requires its owning job lease")
	}
	media, err := h.Store.Media(ctx, req.MediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return err
	}
	estimateBytes, err := precomputeEstimateBytes(media.DurationSeconds, req.Bandwidth)
	if err != nil {
		return err
	}
	reservation, err := h.Admission.Reserve(estimateBytes, func() (int64, error) {
		health, err := h.Store.PlaybackRenditionCacheHealth(ctx)
		return health.ReadyBytes, err
	})
	if err != nil {
		return err
	}
	defer reservation.Release()

	started := h.now()
	generation, previousPath, err := h.Store.StartPlaybackRenditionGeneration(
		ctx, req.playbackRendition(store.PlaybackRenditionGenerating, 0, started), job.ID, job.LeaseOwner,
	)
	if err != nil {
		return err
	}
	failGeneration := func() {
		_ = h.Store.FailPlaybackRenditionGeneration(context.WithoutCancel(ctx), req.RenditionID, generation, h.now())
	}

	// A composed rendition's picture never comes from the source, so the
	// source-video gate (refusals, tone-map) does not apply to it.
	var sourceVideo VideoSignal
	if req.RungID != ComposedRungID {
		sourceVideo, err = precomputeSourceVideo(ctx, h.Store, req.ContentFingerprint)
		if err != nil {
			failGeneration()
			return err
		}
	}
	if err := verifyPrecomputeInput(ctx, h.Store, req); err != nil {
		failGeneration()
		return err
	}
	stagingPath, publishedPath := precomputeGenerationPaths(req, job, generation)
	published := false
	defer func() {
		_ = os.RemoveAll(stagingPath)
		if !published {
			_ = os.RemoveAll(publishedPath)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(stagingPath), 0o700); err != nil {
		failGeneration()
		return err
	}

	spec := req.spec()
	spec.OutputDir = stagingPath
	spec.SourceVideo = sourceVideo
	if err := h.Generator.PrecomputeHLS(ctx, spec); err != nil {
		failGeneration()
		return err
	}
	if err := verifyPrecomputeArtifact(stagingPath, req.RungID); err != nil {
		failGeneration()
		return err
	}
	if err := verifyPrecomputeInput(ctx, h.Store, req); err != nil {
		failGeneration()
		return err
	}
	sizeBytes, err := dirSize(stagingPath)
	if err != nil {
		failGeneration()
		return err
	}
	if sizeBytes > estimateBytes {
		failGeneration()
		return fmt.Errorf("precompute artifact exceeds reserved bytes: %d > %d", sizeBytes, estimateBytes)
	}
	if err := os.MkdirAll(filepath.Dir(publishedPath), 0o700); err != nil {
		failGeneration()
		return err
	}
	if err := os.Rename(stagingPath, publishedPath); err != nil {
		failGeneration()
		return err
	}
	ready := req.playbackRendition(store.PlaybackRenditionReady, sizeBytes, h.now())
	ready.Path = publishedPath
	ready.CreatedAt = time.UnixMilli(generation).UTC()
	if err := h.Store.PublishPlaybackRenditionGeneration(ctx, ready, req.SourcePath, generation, job.ID, job.LeaseOwner); err != nil {
		failGeneration()
		if errors.Is(err, store.ErrPlaybackRenditionInputStale) {
			return ErrPrecomputeInputChanged
		}
		return err
	}
	published = true
	cacheRoot := filepath.Dir(req.Path)
	if previousPath != "" && previousPath != publishedPath && pathWithinRoot(cacheRoot, previousPath) {
		_ = os.RemoveAll(previousPath)
	}
	return nil
}

func (h PrecomputeJobHandler) now() time.Time {
	if h.Now != nil {
		if now := h.Now(); !now.IsZero() {
			return now.UTC()
		}
	}
	return time.Now().UTC()
}

func precomputeEstimateBytes(durationSeconds, bandwidth int) (int64, error) {
	if durationSeconds <= 0 {
		return 0, errors.New("precompute requires a known source duration for bounded admission")
	}
	if bandwidth <= 0 {
		return 0, errors.New("precompute bandwidth must be positive")
	}
	// Reserve the requested video rate plus 256 Kbit/s for audio/container
	// overhead, then add ten percent and one MiB for playlists/init fragments.
	if int64(bandwidth) > math.MaxInt64-256_000 {
		return 0, errors.New("precompute reservation size overflows")
	}
	bitsPerSecond := int64(bandwidth) + 256_000
	if int64(durationSeconds) > math.MaxInt64/bitsPerSecond {
		return 0, errors.New("precompute reservation size overflows")
	}
	bytes := bitsPerSecond * int64(durationSeconds) / 8
	if bytes > (math.MaxInt64-1_048_576)/11*10 {
		return 0, errors.New("precompute reservation size overflows")
	}
	return bytes + bytes/10 + 1_048_576, nil
}

func verifyPrecomputeInput(ctx context.Context, st *store.Store, req PrecomputeRequest) error {
	files, err := st.MediaFilesForMedia(ctx, req.MediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPrecomputeInputChanged, err)
	}
	for _, file := range files {
		if file.ContentFingerprint == req.ContentFingerprint && filepath.Clean(file.Path) == filepath.Clean(req.SourcePath) {
			return nil
		}
	}
	return ErrPrecomputeInputChanged
}

func precomputeGenerationPaths(req PrecomputeRequest, job store.Job, generation int64) (staging, published string) {
	cacheRoot := filepath.Dir(req.Path)
	identity := fmt.Sprintf("%s\x00%s\x00%d\x00%d", req.RenditionID, job.ID, job.LeaseGeneration, generation)
	digest := sha256.Sum256([]byte(identity))
	name := fmt.Sprintf("%x", digest[:])
	mediaDir := fmt.Sprintf("%x", []byte(req.MediaID))
	staging = filepath.Join(cacheRoot, ".precompute-staging", name+".stage")
	published = filepath.Join(cacheRoot, ".precompute-published", mediaDir, name)
	return staging, published
}

func verifyPrecomputeArtifact(root, rungID string) error {
	artifactRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("%w: artifact root: %v", ErrPrecomputeIncomplete, err)
	}
	defer artifactRoot.Close()
	master, err := artifactRoot.ReadFile("master.m3u8")
	if err != nil {
		return fmt.Errorf("%w: master playlist: %v", ErrPrecomputeIncomplete, err)
	}
	masterText := string(master)
	if !strings.HasPrefix(masterText, "#EXTM3U\n") || !strings.Contains(masterText, rungID+"/index.m3u8") {
		return fmt.Errorf("%w: invalid master playlist", ErrPrecomputeIncomplete)
	}
	media, err := artifactRoot.ReadFile(filepath.Join(rungID, "index.m3u8"))
	if err != nil {
		return fmt.Errorf("%w: media playlist: %v", ErrPrecomputeIncomplete, err)
	}
	mediaText := string(media)
	if !strings.HasPrefix(mediaText, "#EXTM3U\n") || !strings.Contains(mediaText, "#EXT-X-ENDLIST") {
		return fmt.Errorf("%w: unfinished media playlist", ErrPrecomputeIncomplete)
	}
	if err := verifyPrecomputePlaylistReferences(artifactRoot, rungID, mediaText); err != nil {
		return err
	}
	segments := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink %s", ErrPrecomputeIncomplete, path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("%w: invalid member %s", ErrPrecomputeIncomplete, path)
		}
		if strings.HasSuffix(entry.Name(), ".m4s") || strings.HasSuffix(entry.Name(), ".ts") {
			segments++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if segments == 0 {
		return fmt.Errorf("%w: no media segments", ErrPrecomputeIncomplete)
	}
	return nil
}

func verifyPrecomputePlaylistReferences(root *os.Root, rungID, playlist string) error {
	references := make([]string, 0)
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MAP:") {
			reference, ok := quotedPlaylistAttribute(line, "URI")
			if !ok {
				return fmt.Errorf("%w: invalid media initialization reference", ErrPrecomputeIncomplete)
			}
			references = append(references, reference)
			continue
		}
		if !strings.HasPrefix(line, "#") {
			references = append(references, line)
		}
	}
	segments := 0
	for _, reference := range references {
		if !validPrecomputePlaylistReference(reference) {
			return fmt.Errorf("%w: unsafe media reference %q", ErrPrecomputeIncomplete, reference)
		}
		info, err := root.Lstat(filepath.Join(rungID, reference))
		if err != nil {
			return fmt.Errorf("%w: media reference %q: %v", ErrPrecomputeIncomplete, reference, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("%w: invalid media reference %q", ErrPrecomputeIncomplete, reference)
		}
		if strings.HasSuffix(reference, ".m4s") || strings.HasSuffix(reference, ".ts") {
			segments++
		}
	}
	if segments == 0 {
		return fmt.Errorf("%w: media playlist has no segments", ErrPrecomputeIncomplete)
	}
	return nil
}

func quotedPlaylistAttribute(line, name string) (string, bool) {
	prefix := name + `="`
	start := strings.Index(line, prefix)
	if start < 0 {
		return "", false
	}
	value := line[start+len(prefix):]
	end := strings.IndexByte(value, '"')
	if end < 0 {
		return "", false
	}
	return value[:end], true
}

func validPrecomputePlaylistReference(reference string) bool {
	if reference == "" || filepath.IsAbs(reference) || strings.ContainsAny(reference, `\?#`) {
		return false
	}
	clean := filepath.Clean(reference)
	return clean == reference && clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func (h PrecomputeEvictJobHandler) Handle(ctx context.Context, job store.Job) error {
	if h.Store == nil {
		return errors.New("store is required")
	}
	var payload PrecomputeEvictPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("precompute eviction payload: %w", err)
	}
	_, err := EvictPrecomputedRenditionsOverBudget(ctx, h.Store, payload.CacheRoot, payload.MaxBytes, h.now())
	return err
}

func (h PrecomputeEvictJobHandler) now() time.Time {
	if h.Now != nil {
		if now := h.Now(); !now.IsZero() {
			return now.UTC()
		}
	}
	return time.Now().UTC()
}

func EvictPrecomputedRenditionsOverBudget(ctx context.Context, st *store.Store, cacheRoot string, maxBytes int64, now time.Time) ([]store.PlaybackRendition, error) {
	if st == nil {
		return nil, errors.New("store is required")
	}
	if cacheRoot == "" {
		return nil, errors.New("cache root is required")
	}
	evicted, err := st.EvictPlaybackRenditionsOverBudget(ctx, maxBytes, now)
	if err != nil {
		return nil, err
	}
	var cleanupErr error
	for _, rendition := range evicted {
		if err := validateKnownRungID(rendition.RungID, rendition.Height); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if !pathWithinRoot(cacheRoot, rendition.Path) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("playback rendition %s path outside cache root", rendition.ID))
			continue
		}
		if err := os.RemoveAll(rendition.Path); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return evicted, cleanupErr
}

func pathWithinRoot(root, path string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func precomputeEvictPayload(cacheRoot string, maxBytes int64) ([]byte, error) {
	if strings.TrimSpace(cacheRoot) == "" {
		return nil, errors.New("cache root is required")
	}
	if maxBytes < 0 {
		return nil, errors.New("cache budget must be non-negative")
	}
	return json.Marshal(PrecomputeEvictPayload{CacheRoot: cacheRoot, MaxBytes: maxBytes})
}

type FFmpegPrecomputeGenerator struct {
	Runner *ffmpeg.Runner
	// Composer resolves composed pictures for ComposedRungID jobs; nil means
	// composed renditions cannot be generated.
	Composer GenerationComposer
}

func (g FFmpegPrecomputeGenerator) PrecomputeHLS(ctx context.Context, spec PrecomputeSpec) error {
	if err := validatePrecomputeSpec(spec); err != nil {
		return err
	}
	runner := g.Runner
	if runner == nil {
		runner = ffmpeg.New()
	}
	if spec.RungID == ComposedRungID {
		return g.precomputeComposedHLS(ctx, runner, spec)
	}
	rungDir := filepath.Join(spec.OutputDir, spec.RungID)
	if err := os.RemoveAll(rungDir); err != nil {
		return err
	}
	if err := os.MkdirAll(rungDir, 0o700); err != nil {
		return err
	}
	proc, err := runner.StartBackgroundHLS(ctx, spec.SourcePath, rungDir, ffmpeg.Rendition{
		Height:         spec.Height,
		VideoKbps:      spec.Bandwidth / 1000,
		AudioCodec:     "aac",
		AudioChannels:  2,
		ColorTransfer:  spec.SourceVideo.ColorTransfer,
		ColorPrimaries: spec.SourceVideo.ColorPrimaries,
		DoViProfile:    spec.SourceVideo.DoViProfile,
		DoViBLCompatID: spec.SourceVideo.DoViBLCompatID,
	})
	if err != nil {
		return err
	}
	if err := proc.Wait(); err != nil {
		return err
	}
	if spec.SourceVideo.NeedsToneMap() {
		if err := os.WriteFile(filepath.Join(rungDir, toneMappedSDRMarker), []byte("ok\n"), 0o600); err != nil {
			return err
		}
	}
	return writePrecomputeMasterPlaylist(filepath.Join(spec.OutputDir, "master.m3u8"), spec)
}

func precomputeSourceVideo(ctx context.Context, st *store.Store, fingerprint string) (VideoSignal, error) {
	streams, err := st.MediaFileStreams(ctx, fingerprint)
	if err != nil {
		return VideoSignal{}, err
	}
	sourceVideo := VideoSignalFromStreams(streams)
	if reason := sourceVideo.FullTranscodeRefusal(); reason != "" {
		return VideoSignal{}, errors.New(reason)
	}
	return sourceVideo, nil
}

func hydratePrecomputeRequest(ctx context.Context, st *store.Store, req PrecomputeRequest) (PrecomputeRequest, error) {
	if req.MediaID == "" {
		return req, nil
	}
	if req.ContentFingerprint != "" && req.SourcePath != "" {
		return req, nil
	}
	files, err := st.MediaFilesForMedia(ctx, req.MediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		return req, err
	}
	if len(files) == 0 {
		return req, errors.New("media has no source files")
	}
	if req.ContentFingerprint == "" {
		req.ContentFingerprint = files[0].ContentFingerprint
	}
	if req.SourcePath == "" {
		req.SourcePath = files[0].Path
	}
	return req, nil
}

func validatePrecomputeRequest(req PrecomputeRequest) error {
	if req.MediaID == "" {
		return errors.New("media ID is required")
	}
	if req.ContentFingerprint == "" {
		return errors.New("content fingerprint is required")
	}
	if req.RenditionID == "" {
		return errors.New("playback rendition ID is required")
	}
	if req.RungID == "" {
		return errors.New("playback rung is required")
	}
	if err := validateKnownRungID(req.RungID, req.Height); err != nil {
		return err
	}
	if req.Codec == "" {
		return errors.New("playback codec is required")
	}
	if req.Height <= 0 || req.Bandwidth <= 0 {
		return errors.New("playback height and bandwidth must be positive")
	}
	if req.SourcePath == "" {
		return errors.New("source path is required")
	}
	if req.Path == "" {
		return errors.New("precomputed output path is required")
	}
	return nil
}

func validatePrecomputeSpec(spec PrecomputeSpec) error {
	return validatePrecomputeRequest(PrecomputeRequest{
		MediaID:            spec.MediaID,
		ContentFingerprint: spec.ContentFingerprint,
		RenditionID:        spec.RenditionID,
		RungID:             spec.RungID,
		Codec:              spec.Codec,
		Height:             spec.Height,
		Bandwidth:          spec.Bandwidth,
		SourcePath:         spec.SourcePath,
		Path:               spec.OutputDir,
	})
}

func (req PrecomputeRequest) payload() PrecomputePayload {
	return PrecomputePayload{
		MediaID:            req.MediaID,
		ContentFingerprint: req.ContentFingerprint,
		RenditionID:        req.RenditionID,
		RungID:             req.RungID,
		Codec:              req.Codec,
		Height:             req.Height,
		Bandwidth:          req.Bandwidth,
		SourcePath:         req.SourcePath,
		Path:               req.Path,
	}
}

func (p PrecomputePayload) request() PrecomputeRequest {
	return PrecomputeRequest{
		MediaID:            p.MediaID,
		ContentFingerprint: p.ContentFingerprint,
		RenditionID:        p.RenditionID,
		RungID:             p.RungID,
		Codec:              p.Codec,
		Height:             p.Height,
		Bandwidth:          p.Bandwidth,
		SourcePath:         p.SourcePath,
		Path:               p.Path,
	}
}

func (req PrecomputeRequest) spec() PrecomputeSpec {
	return PrecomputeSpec{
		MediaID:            req.MediaID,
		ContentFingerprint: req.ContentFingerprint,
		RenditionID:        req.RenditionID,
		RungID:             req.RungID,
		Codec:              req.Codec,
		Height:             req.Height,
		Bandwidth:          req.Bandwidth,
		SourcePath:         req.SourcePath,
		OutputDir:          req.Path,
	}
}

func (req PrecomputeRequest) playbackRendition(status store.PlaybackRenditionStatus, sizeBytes int64, now time.Time) store.PlaybackRendition {
	return store.PlaybackRendition{
		ID:                 req.RenditionID,
		MediaID:            req.MediaID,
		ContentFingerprint: req.ContentFingerprint,
		Kind:               store.PlaybackRenditionKindHLS,
		Status:             status,
		RungID:             req.RungID,
		Codec:              req.Codec,
		Height:             req.Height,
		Bandwidth:          req.Bandwidth,
		Path:               req.Path,
		SizeBytes:          sizeBytes,
		CreatedAt:          normalizePrecomputeTime(now),
		LastUsedAt:         normalizePrecomputeTime(now),
	}
}

func precomputeDedupeKey(id store.PlaybackRenditionID) string {
	return "playback-precompute:" + string(id)
}

func normalizePrecomputeTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

func dirSize(root string) (int64, error) {
	var total int64
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	}); err != nil {
		return 0, err
	}
	return total, nil
}

func writePrecomputeMasterPlaylist(path string, spec PrecomputeSpec) error {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,CODECS=\"avc1.64001f,mp4a.40.2\"\n", spec.Bandwidth)
	fmt.Fprintf(&b, "%s/index.m3u8\n", spec.RungID)
	return os.WriteFile(path, []byte(b.String()), 0o600)
}
