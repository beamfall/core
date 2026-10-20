package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/skipdetect"
	"github.com/beamfall/core/internal/store"
)

const audioAdModelDirectory = store.AudioAdDetectorMethod

type skipDetectionJobs struct {
	store  *store.Store
	wake   *jobWake
	logger *slog.Logger
	audio  *skipdetect.AudioAdJobs
}

// configureSkipDetectionJobs owns all skip-detection construction and job
// registration. The video sweep always exists. Audio-ad inference is added only
// after the cached MODELREG projection passes loader compatibility and safety
// validation; absence or rejection remains inert and enumerates no Media.
func configureSkipDetectionJobs(
	dataDir string,
	st *store.Store,
	extractor *ffmpeg.Runner,
	registry *jobs.Registry,
	resourceGovernor *governor.Governor,
	wake *jobWake,
	logger *slog.Logger,
) *skipDetectionJobs {
	configured := &skipDetectionJobs{store: st, wake: wake, logger: logger}
	video := skipdetect.NewService(st, extractor)
	registry.Register(skipdetect.SweepJobKind, governedGeneration(resourceGovernor, skipdetect.SweepHandler(video)))

	projection, err := loadAudioAdProjection(filepath.Join(dataDir, "models", audioAdModelDirectory))
	if err != nil {
		registerAudioAdHandlers(registry, resourceGovernor, nil)
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("audio ad detector inactive", "reason", "model-absent")
		} else {
			logger.Warn("audio ad detector inactive", "reason", "model-rejected", "error", err)
		}
		return configured
	}
	audio, err := composeAudioAdJobs(st, extractor, projection, audioAdMetricLogger(logger))
	if err != nil {
		registerAudioAdHandlers(registry, resourceGovernor, nil)
		logger.Warn("audio ad detector inactive", "reason", "projection-rejected", "error", err)
		return configured
	}
	configured.audio = audio
	registerAudioAdHandlers(registry, resourceGovernor, audio)
	logger.Info("audio ad detector active",
		"artifact_identity", projection.Manifest.ArtifactID,
		"model_id", projection.Manifest.ModelID,
		"model_version", projection.Manifest.Version,
		"trust", projection.Trust)
	return configured
}

func registerAudioAdHandlers(registry *jobs.Registry, resourceGovernor *governor.Governor, audio *skipdetect.AudioAdJobs) {
	arm := jobs.Handler(func(context.Context, store.Job) (jobs.Result, error) { return jobs.Result{}, nil })
	child := arm
	if audio != nil {
		arm = audio.ArmHandler()
		child = audio.Handler()
	}
	registry.Register(skipdetect.AudioAdArmJobKind, governedGeneration(resourceGovernor, arm))
	registry.Register(skipdetect.AudioAdJobKind, governedGeneration(resourceGovernor, child))
}

func loadAudioAdProjection(root string) (integrationhost.ModelProjection, error) {
	loader, err := integrationhost.NewModelLoader(integrationhost.ModelRuntime{
		Version: skipdetect.AudioAdRuntimeVersion, FeatureSetVersion: ffmpeg.AudioAdFeatureSetVersion,
	}, plugin.PluginTrustRoots{}, nil)
	if err != nil {
		return integrationhost.ModelProjection{}, err
	}
	return loader.ActivateCached(root)
}

func composeAudioAdJobs(
	st skipdetect.AudioAdJobStore,
	extractor skipdetect.AudioAdFeatureExtractor,
	projection integrationhost.ModelProjection,
	metrics func(context.Context, skipdetect.AudioAdMetric),
) (*skipdetect.AudioAdJobs, error) {
	manifest := projection.Manifest
	flavor := store.SegmentFlavor(manifest.Calibration.Flavor)
	calibrationRaw, err := json.Marshal(manifest.Calibration)
	if err != nil {
		return nil, err
	}
	calibrationDigest := sha256.Sum256(calibrationRaw)
	return skipdetect.NewAudioAdJobs(st, extractor, skipdetect.AudioAdJobConfig{
		ArtifactIdentity: manifest.ArtifactID,
		Identity: store.DetectionIdentity{
			ModelID: manifest.ModelID, ModelVersion: manifest.Version,
			ModelHash: manifest.Weights.SHA256, ArtifactIdentity: manifest.ArtifactID,
			FeatureSetVersion:  manifest.FeatureSetVersion,
			CalibrationID:      manifest.Calibration.HoldoutSHA256,
			CalibrationVersion: manifest.Version,
			CalibrationHash:    hex.EncodeToString(calibrationDigest[:]),
		},
		Model: skipdetect.AudioAdModel{
			ModelID: manifest.ModelID, ModelVersion: manifest.Version,
			FeatureSetVersion: manifest.FeatureSetVersion, Flavor: flavor,
			ClassWeights:         skipdetect.AudioAdWeights(projection.Weights.Class),
			StartBoundaryWeights: skipdetect.AudioAdWeights(projection.Weights.StartBoundary),
			EndBoundaryWeights:   skipdetect.AudioAdWeights(projection.Weights.EndBoundary),
		},
		Calibration: skipdetect.AudioAdCalibration{
			ModelID: manifest.ModelID, ModelVersion: manifest.Version,
			FeatureSetVersion: manifest.FeatureSetVersion, Flavor: flavor,
			HoldoutBoundaryPrecision: manifest.Calibration.HoldoutBoundaryPrecision,
			BoundaryPrecisionFloor:   manifest.Calibration.BoundaryPrecisionFloor,
			HoldoutClassF1:           manifest.Calibration.HoldoutClassF1,
		},
		Metrics: metrics,
	})
}

func audioAdMetricLogger(logger *slog.Logger) func(context.Context, skipdetect.AudioAdMetric) {
	return func(ctx context.Context, metric skipdetect.AudioAdMetric) {
		attributes := []any{
			"artifact_identity", metric.ArtifactIdentity,
			"run_id", metric.RunID,
			"signal_tier", metric.SignalTier,
			"candidate_count", metric.CandidateCount,
		}
		if metric.CandidateOrdinal >= 0 {
			attributes = append(attributes,
				"candidate_ordinal", metric.CandidateOrdinal,
				"flavor", metric.Flavor,
				"class_confidence", metric.ClassConfidence,
				"boundary_score", metric.BoundaryScore,
				"boundary_eligible", metric.BoundaryEligible,
			)
		}
		logger.InfoContext(ctx, "audio ad detection completed", attributes...)
	}
}

// enqueue arms both detectors after one settled scan. The classic video sweep
// retains its global active dedupe. The audio arm dedupes by settled scan ID,
// so concurrent scans cannot strand Media behind another arm's cursor.
func (s *skipDetectionJobs) enqueue(ctx context.Context, triggerID string) {
	video, err := skipdetect.NewSweepJob(time.Now().UTC())
	if err == nil {
		_, err = enqueueAndWake(ctx, s.store, s.wake, video)
	}
	if err != nil {
		s.logger.Warn("skip-segment detection sweep enqueue failed", "error", err)
	}
	if s.audio == nil {
		return
	}
	arm, err := s.audio.NewArmJob(triggerID, time.Now().UTC())
	if err == nil {
		_, err = enqueueAndWake(ctx, s.store, s.wake, arm)
	}
	if err != nil {
		s.logger.Warn("audio ad detection arm enqueue failed", "error", err)
	}
}

func (s *skipDetectionJobs) enqueueBoot(ctx context.Context) {
	s.enqueue(ctx, "boot:"+strconv.FormatInt(time.Now().UTC().UnixNano(), 10))
}
