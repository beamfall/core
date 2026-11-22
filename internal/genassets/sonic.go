package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// Sonic analysis (CORE-3073, OpenSpec core-1974-sonic-similarity). Core owns
// the scheduling and the storage; the analysis itself is Oscillux's sonic
// profile, which reaches Core as a SonicAnalyzer (owner decision D1: a
// wazero-hosted WASI artifact, CORE-3072). Until that artifact is wired the
// analyser is nil and the job is inert. Core decodes a short excerpt with
// FFmpeg, hands the samples to the analyser and stores the returned vector in
// the ADR-0041 media_embedding table, keyed by the file's content fingerprint.
// Nothing leaves the server.

// SonicSweepJobKind is the durable job that analyses audio-class Media in
// sonic-enabled Libraries. Background class; the composition root gates it
// behind the resource governor, so it runs only while no playback transcode or
// scan is active and yields when one starts (owner decision D2: idle-only).
const SonicSweepJobKind store.JobKind = "sonic-analysis-sweep"

// Excerpt window (owner decision D2): one minute from the middle of the file,
// decoded at 22.05 kHz stereo, about 10 MB of samples held in memory.
const (
	sonicExcerptSeconds    = 60
	sonicExcerptSampleRate = 22050
	sonicExcerptChannels   = 2
	// sonicMaxVectorDims bounds what an analyser may write; the sonic-v1
	// layout is about 32 values.
	sonicMaxVectorDims = 256
)

// SonicAnalyzer turns decoded samples into a feature vector of one versioned
// layout. Implementations are Oscillux's sonic profile behind the CORE-3072
// loader, or a test double.
type SonicAnalyzer interface {
	// Kind is the media_embedding kind the vectors are stored under.
	Kind() store.EmbeddingKind
	// Analyze receives interleaved float32 samples at the given rate and
	// channel count and returns the feature vector.
	Analyze(ctx context.Context, samples []float32, sampleRate, channels int) ([]float32, error)
}

// ExcerptDecoder decodes part of a source to interleaved float32 samples in
// memory. The FFmpeg runner implements it.
type ExcerptDecoder interface {
	AudioExcerpt(ctx context.Context, src string, startSeconds, seconds float64, sampleRate, channels int) ([]float32, error)
}

// SetSonicAnalysis wires the excerpt decoder and the analyser. With either
// nil, sonic analysis is inactive and the sweep finishes without work.
func (s *Service) SetSonicAnalysis(decoder ExcerptDecoder, analyzer SonicAnalyzer) {
	s.sonicDecoder = decoder
	s.sonicAnalyzer = analyzer
}

// SonicAnalysisActive reports whether an analyser and decoder are wired.
func (s *Service) SonicAnalysisActive() bool {
	return s.sonicDecoder != nil && s.sonicAnalyzer != nil
}

type sonicSweepCheckpoint struct {
	Version int    `json:"version"`
	LastID  string `json:"last_id,omitempty"`
	// Wrapped is set once the sweep restarts from the lowest Media ID to pick
	// up candidates that became eligible behind the cursor (a Library enabled
	// or Media added while this deduped sweep was already running).
	Wrapped  bool `json:"wrapped,omitempty"`
	Analysed int  `json:"analysed"`
	Skipped  int  `json:"skipped"`
	Failed   int  `json:"failed"`
}

// NewSonicSweepJob builds the deduped background sonic analysis job. At most
// one is queued, running or yielded at a time.
func NewSonicSweepJob(createdAt time.Time) (store.Job, error) {
	payload, err := json.Marshal(sweepPayload{Version: 1})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind:      SonicSweepJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(SonicSweepJobKind),
		Payload:   payload,
		CreatedAt: createdAt,
	}, nil
}

// SonicSweepHandler returns the handler that analyses the next batch of
// candidates, checkpointing by Media ID so a preempted sweep resumes where it
// stopped. A sweep that started past the lowest ID makes one more pass from
// the start, because a re-arm while it ran was deduped into it. A failure is
// counted and skipped, and a later sweep retries it because it still has no
// vector.
func SonicSweepHandler(service *Service) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		if !service.SonicAnalysisActive() {
			return jobs.Result{}, nil
		}
		var checkpoint sonicSweepCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		kind := service.sonicAnalyzer.Kind()
		ids, err := service.store.SonicAnalysisCandidatesAfter(ctx, kind, store.MediaID(checkpoint.LastID), sweepJobBatchSize+1)
		if err != nil {
			return jobs.Result{}, err
		}
		processed := 0
		for _, id := range ids {
			if processed >= sweepJobBatchSize || ctx.Err() != nil {
				break
			}
			analysed, err := service.AnalyzeSonic(ctx, id)
			if ctx.Err() != nil {
				break // Retry the interrupted item.
			}
			switch {
			case err != nil:
				checkpoint.Failed++
			case analysed:
				checkpoint.Analysed++
			default:
				checkpoint.Skipped++
			}
			checkpoint.LastID = string(id)
			processed++
		}
		checkpoint.Version = 1
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		progress := store.JobProgress{
			Current: int64(checkpoint.Analysed),
			Unit:    "items",
			Message: "Analysing audio",
		}
		if err := service.store.UpdateJobCheckpoint(context.WithoutCancel(ctx), job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{Checkpoint: data}, err
		}
		if len(ids) > processed || ctx.Err() != nil {
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		if !checkpoint.Wrapped && checkpoint.LastID != "" {
			// One pass from the start picks up candidates behind the cursor.
			checkpoint.Wrapped = true
			checkpoint.LastID = ""
			data, err := json.Marshal(checkpoint)
			if err != nil {
				return jobs.Result{}, err
			}
			if err := service.store.UpdateJobCheckpoint(context.WithoutCancel(ctx), job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
				return jobs.Result{Checkpoint: data}, err
			}
			return jobs.Result{Checkpoint: data}, jobs.ErrYielded
		}
		return jobs.Result{Checkpoint: data}, nil
	}
}

// AnalyzeSonic analyses one Media item's primary file and stores its vector.
// It reports false with no error when the item is not eligible: not audio, in
// a Sealed or Encrypted Library, in a Library whose toggle is off, or without
// a readable file. Encrypted Libraries are skipped outright because the vector
// store is on the shared plane. The toggle is checked again before decoding
// and the write is conditional on it, so turning analysis off stops a batch
// already selected.
func (s *Service) AnalyzeSonic(ctx context.Context, id store.MediaID) (bool, error) {
	if !s.SonicAnalysisActive() {
		return false, nil
	}
	media, file, ok, err := s.audioSource(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	if s.isEncrypted(media.LibraryID) {
		return false, nil
	}
	enabled, err := s.store.SonicAnalysisEnabled(ctx, media.LibraryID)
	if err != nil || !enabled {
		return false, err
	}
	start := sonicExcerptStart(media.DurationSeconds)
	samples, err := s.sonicDecoder.AudioExcerpt(ctx, file.Path, start, sonicExcerptSeconds, sonicExcerptSampleRate, sonicExcerptChannels)
	if err != nil {
		return false, err
	}
	vector, err := s.sonicAnalyzer.Analyze(ctx, samples, sonicExcerptSampleRate, sonicExcerptChannels)
	if err != nil {
		return false, err
	}
	if err := validSonicVector(vector); err != nil {
		return false, fmt.Errorf("sonic analysis of %s: %w", id, err)
	}
	return s.store.StoreSonicEmbedding(ctx, media.LibraryID, file.ContentFingerprint, s.sonicAnalyzer.Kind(), vector)
}

// sonicExcerptStart centres the excerpt window in the file. A file shorter
// than the window, or of unknown length, is read from the start.
func sonicExcerptStart(durationSeconds int) float64 {
	if durationSeconds <= sonicExcerptSeconds {
		return 0
	}
	return float64(durationSeconds-sonicExcerptSeconds) / 2
}

// validSonicVector refuses analyser output that would poison similarity:
// empty, oversized, non-finite or all-zero vectors.
func validSonicVector(vector []float32) error {
	if len(vector) == 0 || len(vector) > sonicMaxVectorDims {
		return fmt.Errorf("vector has %d values, want 1..%d", len(vector), sonicMaxVectorDims)
	}
	nonZero := false
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return errors.New("vector has a non-finite value")
		}
		nonZero = nonZero || v != 0
	}
	if !nonZero {
		return errors.New("vector is all zero")
	}
	return nil
}
