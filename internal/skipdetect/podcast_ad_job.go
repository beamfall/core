package skipdetect

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

const (
	AudioAdRuntimeVersion = "1.0.0"
	AudioAdArmJobKind     = store.JobKind("pod" + "cast-ad-arm")
	AudioAdJobKind        = store.JobKind(store.AudioAdDetectorMethod)
	audioAdSignalTier     = "audio"
	audioAdArmBatchSize   = 64
)

type AudioAdJobStore interface {
	AudioAdMediaAfter(context.Context, store.MediaID, int) ([]store.AudioAdMedia, error)
	AudioAdMediaByID(context.Context, store.MediaID) (store.AudioAdMedia, bool, error)
	DetectionRunEligible(context.Context, store.MediaID, string, store.DetectionIdentity) (bool, error)
	ApplyDetectionRun(context.Context, store.DetectionRun) error
	EnqueueJob(context.Context, store.Job) (store.Job, error)
}

type AudioAdFeatureExtractor interface {
	AudioAdFeatures(context.Context, string, int) (ffmpeg.AudioAdFeatureSet, error)
}

// AudioAdJobConfig is a validated registry projection adapted to the local
// classifier. Identity includes model, artifact, feature, and calibration
// coordinates so any governed artifact upgrade reopens completed Media.
type AudioAdJobConfig struct {
	ArtifactIdentity string
	Identity         store.DetectionIdentity
	Model            AudioAdModel
	Calibration      AudioAdCalibration
	Metrics          func(context.Context, AudioAdMetric)
	Now              func() time.Time
}

// AudioAdMetric is local structured evidence only. The app composition root
// writes it to the local structured log; job-event/SSE payloads are never used.
type AudioAdMetric struct {
	ArtifactIdentity string
	RunID            string
	SignalTier       string
	CandidateCount   int
	CandidateOrdinal int
	Flavor           store.SegmentFlavor
	ClassConfidence  float64
	BoundaryScore    float64
	BoundaryEligible bool
}

type AudioAdJobs struct {
	store       AudioAdJobStore
	extractor   AudioAdFeatureExtractor
	artifactID  string
	identity    store.DetectionIdentity
	model       AudioAdModel
	calibration AudioAdCalibration
	metrics     func(context.Context, AudioAdMetric)
	now         func() time.Time
}

func NewAudioAdJobs(st AudioAdJobStore, extractor AudioAdFeatureExtractor, cfg AudioAdJobConfig) (*AudioAdJobs, error) {
	if st == nil {
		return nil, errors.New("audio ad jobs: store is required")
	}
	if extractor == nil {
		return nil, errors.New("audio ad jobs: feature extractor is required")
	}
	cfg.ArtifactIdentity = strings.TrimSpace(cfg.ArtifactIdentity)
	if cfg.ArtifactIdentity == "" || cfg.ArtifactIdentity != cfg.Identity.ArtifactIdentity {
		return nil, errors.New("audio ad jobs: artifact identity is invalid")
	}
	if err := validateAudioAdIdentity(cfg.Identity); err != nil {
		return nil, err
	}
	if err := cfg.Model.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.Calibration.Validate(); err != nil {
		return nil, err
	}
	if cfg.Model.ModelID != cfg.Identity.ModelID || cfg.Model.ModelVersion != cfg.Identity.ModelVersion ||
		cfg.Model.FeatureSetVersion != cfg.Identity.FeatureSetVersion {
		return nil, errors.New("audio ad jobs: model does not match detection identity")
	}
	if cfg.Calibration.ModelID != cfg.Model.ModelID || cfg.Calibration.ModelVersion != cfg.Model.ModelVersion ||
		cfg.Calibration.FeatureSetVersion != cfg.Model.FeatureSetVersion || cfg.Calibration.Flavor != cfg.Model.Flavor {
		return nil, errors.New("audio ad jobs: calibration does not match model")
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &AudioAdJobs{
		store: st, extractor: extractor, artifactID: cfg.ArtifactIdentity,
		identity: cfg.Identity, model: cfg.Model, calibration: cfg.Calibration,
		metrics: cfg.Metrics, now: cfg.Now,
	}, nil
}

func validateAudioAdIdentity(identity store.DetectionIdentity) error {
	for name, value := range map[string]string{
		"model ID": identity.ModelID, "model version": identity.ModelVersion,
		"model hash": identity.ModelHash, "artifact identity": identity.ArtifactIdentity,
		"feature-set version": identity.FeatureSetVersion, "calibration ID": identity.CalibrationID,
		"calibration version": identity.CalibrationVersion, "calibration hash": identity.CalibrationHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("audio ad jobs: %s is required", name)
		}
	}
	return nil
}

type audioAdArmPayload struct {
	Version          int    `json:"version"`
	TriggerID        string `json:"trigger_id"`
	ArtifactIdentity string `json:"artifact_identity"`
}

type audioAdArmCheckpoint struct {
	Version  int    `json:"version"`
	LastID   string `json:"last_id,omitempty"`
	Enqueued int    `json:"enqueued"`
	Deduped  int    `json:"deduped"`
}

type audioAdJobPayload struct {
	Version          int           `json:"version"`
	MediaID          store.MediaID `json:"media_id"`
	ArtifactIdentity string        `json:"artifact_identity"`
}

// NewArmJob creates one post-scan debounce arm. TriggerID is the
// settled scan job identity, so a repeated completion callback collapses while
// independent concurrent scans retain independent cursors.
func (p *AudioAdJobs) NewArmJob(triggerID string, createdAt time.Time) (store.Job, error) {
	triggerID = strings.TrimSpace(triggerID)
	if triggerID == "" {
		return store.Job{}, errors.New("audio ad arm: trigger ID is required")
	}
	payload, err := json.Marshal(audioAdArmPayload{
		Version: 1, TriggerID: triggerID, ArtifactIdentity: p.artifactID,
	})
	if err != nil {
		return store.Job{}, err
	}
	return store.Job{
		Kind: AudioAdArmJobKind, Class: store.JobClassBackground,
		DedupeKey: string(AudioAdArmJobKind) + ":" + triggerID + ":" + p.artifactID,
		Payload:   payload, CreatedAt: createdAt,
	}, nil
}

func (p *AudioAdJobs) ArmHandler() jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var payload audioAdArmPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Result{}, err
		}
		if payload.Version != 1 || payload.TriggerID == "" {
			return jobs.Result{}, errors.New("audio ad arm: invalid payload")
		}
		if payload.ArtifactIdentity != p.artifactID {
			return jobs.Result{}, nil
		}
		var checkpoint audioAdArmCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		media, err := p.store.AudioAdMediaAfter(ctx, store.MediaID(checkpoint.LastID), audioAdArmBatchSize)
		if err != nil {
			return jobs.Result{}, err
		}
		for _, item := range media {
			if ctx.Err() != nil {
				break
			}
			eligible, err := p.store.DetectionRunEligible(ctx, item.ID, store.AudioAdDetectorMethod, p.identity)
			if err != nil {
				return jobs.Result{}, err
			}
			if eligible {
				child, err := p.newMediaJob(job.ID, item.ID, p.now())
				if err != nil {
					return jobs.Result{}, err
				}
				created, err := p.store.EnqueueJob(ctx, child)
				if err != nil {
					return jobs.Result{}, err
				}
				if created.ID == child.ID {
					checkpoint.Enqueued++
				} else {
					checkpoint.Deduped++
				}
			}
			checkpoint.LastID = string(item.ID)
		}
		checkpoint.Version = 1
		encoded, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		if len(media) == audioAdArmBatchSize || ctx.Err() != nil {
			return jobs.Result{Checkpoint: encoded}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}

func (p *AudioAdJobs) newMediaJob(armID store.JobID, mediaID store.MediaID, createdAt time.Time) (store.Job, error) {
	if armID == "" || mediaID == "" {
		return store.Job{}, errors.New("audio ad job: arm and media IDs are required")
	}
	payload, err := json.Marshal(audioAdJobPayload{
		Version: 1, MediaID: mediaID, ArtifactIdentity: p.artifactID,
	})
	if err != nil {
		return store.Job{}, err
	}
	id, err := randomAudioAdChildJobID()
	if err != nil {
		return store.Job{}, err
	}
	key := string(AudioAdJobKind) + ":" + string(mediaID) + ":" + p.artifactID
	return store.Job{
		ID:   id,
		Kind: AudioAdJobKind, Class: store.JobClassBackground,
		DedupeKey: key, Payload: payload, CreatedAt: createdAt,
	}, nil
}

func randomAudioAdChildJobID() (store.JobID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return store.JobID("job-pod" + "cast-ad-" + hex.EncodeToString(raw[:])), nil
}

func (p *AudioAdJobs) Handler() jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var payload audioAdJobPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return jobs.Result{}, err
		}
		if payload.Version != 1 || payload.MediaID == "" {
			return jobs.Result{}, errors.New("audio ad job: invalid payload")
		}
		if payload.ArtifactIdentity != p.artifactID {
			return jobs.Result{}, nil
		}
		media, ok, err := p.store.AudioAdMediaByID(ctx, payload.MediaID)
		if err != nil || !ok {
			return jobs.Result{}, err
		}
		eligible, err := p.store.DetectionRunEligible(ctx, media.ID, store.AudioAdDetectorMethod, p.identity)
		if err != nil || !eligible {
			return jobs.Result{}, err
		}
		features, err := p.extractor.AudioAdFeatures(ctx, media.Path, media.DurationSeconds)
		if err != nil {
			return jobs.Result{}, err
		}
		inferred, err := p.model.Infer(features)
		if err != nil {
			return jobs.Result{}, err
		}
		candidates := make([]store.DetectionCandidate, 0, len(inferred))
		for _, candidate := range inferred {
			admitted, err := p.calibration.admits(candidate)
			if err != nil {
				return jobs.Result{}, err
			}
			candidates = append(candidates, store.DetectionCandidate{
				SegmentKind: store.SegmentKindAd, StartSeconds: candidate.StartSeconds,
				EndSeconds: candidate.EndSeconds, Flavor: candidate.Flavor,
				ClassConfidence: candidate.ClassConfidence, BoundaryScore: candidate.BoundaryScore,
				BoundaryEligible: admitted,
			})
		}
		run := store.DetectionRun{
			RunID: string(job.ID), MediaID: media.ID, DetectorMethod: store.AudioAdDetectorMethod,
			Identity: p.identity, SignalTier: audioAdSignalTier,
			Candidates: candidates, CompletedAt: p.now(),
		}
		err = p.store.ApplyDetectionRun(ctx, run)
		if errors.Is(err, store.ErrDetectionRunAlreadyCompleted) || errors.Is(err, store.ErrDetectionRunStale) {
			return jobs.Result{}, nil
		}
		if err != nil {
			return jobs.Result{}, err
		}
		p.recordMetrics(ctx, run)
		return jobs.Result{}, nil
	}
}

func (p *AudioAdJobs) recordMetrics(ctx context.Context, run store.DetectionRun) {
	if p.metrics == nil {
		return
	}
	base := AudioAdMetric{
		ArtifactIdentity: p.artifactID, RunID: run.RunID, SignalTier: run.SignalTier,
		CandidateCount: len(run.Candidates), CandidateOrdinal: -1,
	}
	p.metrics(ctx, base)
	for ordinal, candidate := range run.Candidates {
		metric := base
		metric.CandidateOrdinal = ordinal
		metric.Flavor = candidate.Flavor
		metric.ClassConfidence = candidate.ClassConfidence
		metric.BoundaryScore = candidate.BoundaryScore
		metric.BoundaryEligible = candidate.BoundaryEligible
		p.metrics(ctx, metric)
	}
}
