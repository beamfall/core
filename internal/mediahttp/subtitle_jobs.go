package mediahttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/beamfall/core/internal/playback"
)

// errSubtitlePending reports that a subtitle is still being extracted after the
// request's bounded wait (CORE-2315). The extraction keeps running; the client
// retries and gets a cache hit once it lands.
var errSubtitlePending = errors.New("subtitle extraction in progress")

const (
	// defaultSubtitleWait bounds how long one request waits for an extraction
	// before it answers 503 with Retry-After instead of hanging.
	defaultSubtitleWait = 20 * time.Second
	// subtitleRetryAfterSeconds is the Retry-After hint on a pending subtitle.
	subtitleRetryAfterSeconds = 5
	// subtitleExtractionSlots caps concurrent ffmpeg extractions across
	// different files. Requests for the same file share one extraction.
	subtitleExtractionSlots = 2
)

// BatchSubtitleConverter extracts several text subtitle streams of src in one
// pass, writing outputs[streamIndex] as WebVTT. Satisfied by *ffmpeg.Runner. A
// SubtitleConverter that also implements it extracts every text track of a
// file the first time any of them is requested.
type BatchSubtitleConverter interface {
	ExtractSubtitlesToWebVTT(ctx context.Context, src string, outputs map[int]string) error
}

// writeSubtitlePending answers a subtitle request whose extraction outlived the
// bounded wait: 503 with Retry-After, so the player retries instead of
// treating the track as broken.
func writeSubtitlePending(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(subtitleRetryAfterSeconds))
	http.Error(w, "subtitle extraction in progress; retry", http.StatusServiceUnavailable)
}

// subtitleExtraction is one in-flight extraction of a file's text subtitle
// tracks. indices and errs are written only by the extraction goroutine and
// read only after done is closed.
type subtitleExtraction struct {
	done    chan struct{}
	indices map[int]bool
	errs    map[int]error
}

// subtitleJobs is the per-file singleflight for plaintext subtitle extraction
// and the per-key lock table for encrypted extraction. Its zero value is ready
// to use.
type subtitleJobs struct {
	mu    sync.Mutex
	jobs  map[string]*subtitleExtraction
	keys  map[string]chan struct{}
	slots chan struct{}
}

func (j *subtitleJobs) initLocked() {
	if j.jobs == nil {
		j.jobs = make(map[string]*subtitleExtraction)
		j.keys = make(map[string]chan struct{})
		j.slots = make(chan struct{}, subtitleExtractionSlots)
	}
}

// lockKey takes the exclusive lock for one key, waiting at most wait. It returns
// errSubtitlePending when the wait runs out and ctx's error when the request
// ends first. Unrelated keys never wait on each other.
func (j *subtitleJobs) lockKey(ctx context.Context, key string, wait time.Duration) (func(), error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		j.mu.Lock()
		j.initLocked()
		held, busy := j.keys[key]
		if !busy {
			mine := make(chan struct{})
			j.keys[key] = mine
			j.mu.Unlock()
			return func() {
				j.mu.Lock()
				delete(j.keys, key)
				j.mu.Unlock()
				close(mine)
			}, nil
		}
		j.mu.Unlock()
		select {
		case <-held:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, errSubtitlePending
		}
	}
}

// acquireSlot takes one of the shared extraction slots for a caller that
// extracts on its own request (the encrypted path), so it counts against the
// same cap as background extractions. A caller that waits past wait gets
// errSubtitlePending.
func (j *subtitleJobs) acquireSlot(ctx context.Context, wait time.Duration) (func(), error) {
	j.mu.Lock()
	j.initLocked()
	slots := j.slots
	j.mu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errSubtitlePending
	}
}

func (h *Handler) subtitleWaitLimit() time.Duration {
	if h.subtitleWait > 0 {
		return h.subtitleWait
	}
	return defaultSubtitleWait
}

// awaitSubtitleMember returns the cache member for streamIndex once an
// extraction that covers it has published it. Requests for the same file join
// one extraction; a request that waits longer than the bound gets
// errSubtitlePending while the extraction carries on detached from it.
func (h *Handler) awaitSubtitleMember(ctx context.Context, fingerprint, srcPath string, streamIndex int, member rootedPath) (rootedPath, error) {
	timer := time.NewTimer(h.subtitleWaitLimit())
	defer timer.Stop()
	// A job already running for this file may not cover streamIndex (its track
	// list was read before this stream was known); one more round starts a job
	// that does.
	for range 2 {
		job, err := h.subtitleExtractionFor(ctx, fingerprint, srcPath, streamIndex)
		if err != nil {
			return rootedPath{}, err
		}
		select {
		case <-job.done:
		case <-ctx.Done():
			return rootedPath{}, ctx.Err()
		case <-timer.C:
			return rootedPath{}, errSubtitlePending
		}
		if err := job.errs[streamIndex]; err != nil {
			return rootedPath{}, err
		}
		if rootedRegularFileExists(member) {
			return member, nil
		}
		if job.indices[streamIndex] {
			return rootedPath{}, errors.New("subtitle extraction published no output")
		}
	}
	return rootedPath{}, errors.New("subtitle extraction did not cover the requested stream")
}

// subtitleExtractionFor joins the running extraction for fingerprint or starts
// one covering streamIndex. A request that has already gone away never starts
// an extraction.
func (h *Handler) subtitleExtractionFor(ctx context.Context, fingerprint, srcPath string, streamIndex int) (*subtitleExtraction, error) {
	key := safeSubtitleFingerprint(fingerprint)
	h.subtitleJobs.mu.Lock()
	defer h.subtitleJobs.mu.Unlock()
	h.subtitleJobs.initLocked()
	if job, ok := h.subtitleJobs.jobs[key]; ok {
		return job, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	job := &subtitleExtraction{
		done:    make(chan struct{}),
		indices: map[int]bool{streamIndex: true},
		errs:    make(map[int]error),
	}
	h.subtitleJobs.jobs[key] = job
	// The extraction reads the whole source file, so it outlives an impatient
	// request: once started it runs to completion and the next request is a
	// cache hit, instead of every retry starting over from nothing.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), subtitleConvertTimeout)
	go func() {
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				job.errs[streamIndex] = fmt.Errorf("subtitle extraction panicked: %v", recovered)
			}
			h.subtitleJobs.mu.Lock()
			delete(h.subtitleJobs.jobs, key)
			h.subtitleJobs.mu.Unlock()
			close(job.done)
		}()
		h.runSubtitleExtraction(runCtx, job, fingerprint, srcPath)
	}()
	return job, nil
}

// runSubtitleExtraction extracts every text subtitle stream of the file that is
// not cached yet, in one ffmpeg pass when the converter supports it, then
// hardens each output into its cache member.
func (h *Handler) runSubtitleExtraction(ctx context.Context, job *subtitleExtraction, fingerprint, srcPath string) {
	failAll := func(err error) {
		for index := range job.indices {
			job.errs[index] = err
		}
	}
	select {
	case h.subtitleJobs.slots <- struct{}{}:
		defer func() { <-h.subtitleJobs.slots }()
	case <-ctx.Done():
		failAll(ctx.Err())
		return
	}
	h.addTextSubtitleStreams(ctx, job, fingerprint)
	members := make(map[int]rootedPath, len(job.indices))
	staged := make(map[int]string, len(job.indices))
	for index := range job.indices {
		member, err := newRootedPath(h.subtitleDir, fmt.Sprintf("%s.%d.vtt", safeSubtitleFingerprint(fingerprint), index))
		if err != nil {
			job.errs[index] = err
			continue
		}
		members[index] = member
		staged[index] = member.String() + ".staged"
	}
	defer func() {
		for _, path := range staged {
			_ = os.Remove(path)
		}
	}()
	extracted := false
	if batch, ok := h.subtitles.(BatchSubtitleConverter); ok && len(staged) > 1 {
		if err := batch.ExtractSubtitlesToWebVTT(ctx, srcPath, staged); err != nil {
			// One unconvertible track fails the whole pass; per-stream
			// extraction below isolates it so the other tracks still land.
			slog.Warn("batch subtitle extract failed; extracting per stream", "error", err.Error())
		} else {
			extracted = true
		}
	}
	for _, index := range slices.Sorted(maps.Keys(staged)) {
		path := staged[index]
		if !extracted {
			if err := h.subtitles.ExtractSubtitleToWebVTT(ctx, srcPath, index, path); err != nil {
				job.errs[index] = err
				continue
			}
		}
		if err := hardenExtractedWebVTT(h.subtitleDir, path, members[index]); err != nil {
			job.errs[index] = err
		}
	}
}

// addTextSubtitleStreams adds every other text subtitle stream of the file that
// has no cache member yet to the extraction, so the first track a viewer opens
// pays for all of them and switching tracks is a cache hit.
func (h *Handler) addTextSubtitleStreams(ctx context.Context, job *subtitleExtraction, fingerprint string) {
	if h.store == nil {
		return
	}
	streams, err := h.store.MediaFileStreams(ctx, fingerprint)
	if err != nil {
		return
	}
	for _, stream := range streams {
		if stream.Type != "subtitle" || !playback.IsMuxedTextSubtitleCodec(stream.Codec) || job.indices[stream.Index] {
			continue
		}
		member, err := newRootedPath(h.subtitleDir, fmt.Sprintf("%s.%d.vtt", safeSubtitleFingerprint(fingerprint), stream.Index))
		if err != nil || rootedRegularFileExists(member) {
			continue
		}
		job.indices[stream.Index] = true
	}
}
