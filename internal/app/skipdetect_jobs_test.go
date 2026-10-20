package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/governor"
	"github.com/beamfall/core/internal/integrationhost"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/skipdetect"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestAudioAdArtifactToReceiptAndMarkerJourney(t *testing.T) {
	if audioAdModelDirectory != "podcast-ad-audio" {
		t.Fatalf("model directory = %q", audioAdModelDirectory)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ctx := context.Background()
	dataDir := t.TempDir()
	projection := writeAudioAdModelFixture(t, dataDir, "1.0.0")
	st := storetest.Open(t)
	media := importAudioAdAudioFixture(t, st)
	if err := st.SetSkipDetectionEnabled(ctx, media.LibraryID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	var metrics []skipdetect.AudioAdMetric
	configured, err := composeAudioAdJobs(st, ffmpeg.New(), projection, func(_ context.Context, metric skipdetect.AudioAdMetric) {
		metrics = append(metrics, metric)
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "podcast-ad-journey")
	registry.Register(skipdetect.AudioAdArmJobKind, configured.ArmHandler())
	registry.Register(skipdetect.AudioAdJobKind, configured.Handler())
	arm, err := configured.NewArmJob("settled-scan", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	arm, err = st.EnqueueJob(ctx, arm)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RunOne(ctx, []store.JobClass{store.JobClassBackground}, time.Now()); err != nil {
		t.Fatal(err)
	}
	child, err := registry.RunOne(ctx, []store.JobClass{store.JobClassBackground}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if child.Kind != skipdetect.AudioAdJobKind || child.DedupeKey != "podcast-ad-audio:"+string(media.ID)+":"+projection.Manifest.ArtifactID {
		t.Fatalf("child = %+v", child)
	}
	markers, err := st.MarkersForMedia(ctx, media.ID, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 1 || markers[0].SegmentKind != store.SegmentKindAd || markers[0].Flavor != store.FlavorInserted {
		t.Fatalf("markers = %+v", markers)
	}
	if len(metrics) < 2 || metrics[1].ArtifactIdentity != projection.Manifest.ArtifactID ||
		metrics[1].RunID != string(child.ID) || metrics[1].SignalTier != "audio" ||
		metrics[1].ClassConfidence == 0 || metrics[1].BoundaryScore == 0 {
		t.Fatalf("metrics = %+v", metrics)
	}
	events, err := st.ListJobEvents(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		payload := string(event.Payload)
		if strings.Contains(payload, "class_confidence") || strings.Contains(payload, "boundary_score") ||
			strings.Contains(payload, "artifact_identity") || strings.Contains(payload, "signal_tier") {
			t.Fatalf("client-reachable job event contains detector metrics: %+v", event)
		}
	}
	settled, err := st.Job(ctx, arm.ID)
	if err != nil || settled.Status != store.JobStatusCompleted {
		t.Fatalf("arm = %+v, %v", settled, err)
	}
}

func TestConfigureSkipDetectionJobsRejectsMissingInvalidAndIncompatibleModelsBeforeFanout(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "missing"},
		{name: "invalid", setup: func(t *testing.T, dataDir string) {
			root := filepath.Join(dataDir, "models", audioAdModelDirectory)
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, integrationhost.ModelManifestFilename), []byte("not-json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "incompatible", setup: func(t *testing.T, dataDir string) {
			writeAudioAdModelFiles(t, dataDir, "2.0.0")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if test.setup != nil {
				test.setup(t, dataDir)
			}
			st := storetest.Open(t)
			registry := jobs.NewRegistry(st, "no-model")
			configured := configureSkipDetectionJobs(dataDir, st, ffmpeg.New(), registry, governor.New(), newJobWake(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			configured.enqueue(context.Background(), "settled-scan")
			queued, err := st.ListJobs(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, job := range queued {
				if job.Kind == skipdetect.AudioAdArmJobKind || job.Kind == skipdetect.AudioAdJobKind {
					t.Fatalf("rejected model created podcast job: %+v", job)
				}
			}
		})
	}
}

func TestConfiguredAudioAdArmAndChildAreTranscodePreempted(t *testing.T) {
	dataDir := t.TempDir()
	writeAudioAdModelFixture(t, dataDir, "1.0.0")
	st := storetest.Open(t)
	media := importAudioAdAudioFixture(t, st)
	if err := st.SetSkipDetectionEnabled(context.Background(), media.LibraryID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	g := governor.New()
	registry := jobs.NewRegistry(st, "preemption")
	configured := configureSkipDetectionJobs(dataDir, st, ffmpeg.New(), registry, g, newJobWake(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	arm, err := configured.audio.NewArmJob("settled-scan", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	arm, err = st.EnqueueJob(context.Background(), arm)
	if err != nil {
		t.Fatal(err)
	}

	transcode := g.BeginTranscode()
	if _, err := registry.RunOne(context.Background(), []store.JobClass{store.JobClassBackground}, time.Now()); err != nil {
		t.Fatal(err)
	}
	transcode.Release()
	arm, err = st.Job(context.Background(), arm.ID)
	if err != nil || arm.Status != store.JobStatusYielded {
		t.Fatalf("preempted arm = %+v, %v", arm, err)
	}

	resumeAt := time.Now().Add(time.Minute)
	if _, err := registry.RunOne(context.Background(), []store.JobClass{store.JobClassBackground}, resumeAt); err != nil {
		t.Fatal(err)
	}
	transcode = g.BeginTranscode()
	child, err := registry.RunOne(context.Background(), []store.JobClass{store.JobClassBackground}, resumeAt)
	transcode.Release()
	if err != nil {
		t.Fatal(err)
	}
	child, err = st.Job(context.Background(), child.ID)
	if err != nil || child.Kind != skipdetect.AudioAdJobKind || child.Status != store.JobStatusYielded {
		t.Fatalf("preempted child = %+v, %v", child, err)
	}
}

func writeAudioAdModelFixture(t *testing.T, dataDir, minimumRuntime string) integrationhost.ModelProjection {
	t.Helper()
	root := writeAudioAdModelFiles(t, dataDir, minimumRuntime)
	projection, err := loadAudioAdProjection(root)
	if err != nil {
		t.Fatal(err)
	}
	return projection
}

func writeAudioAdModelFiles(t *testing.T, dataDir, minimumRuntime string) string {
	t.Helper()
	root := filepath.Join(dataDir, "models", audioAdModelDirectory)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	weights := encodeAudioAdWeights(t, [15]float64{10, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0})
	weightsDigest := sha256.Sum256(weights)
	manifest := integrationhost.ModelArtifactManifest{
		SchemaVersion: 1, ArtifactID: "podcast-ad-fixture-cpu", ModelID: "podcast-ad-fixture",
		Version: "1.0.0", TaskClass: integrationhost.PodcastAdModelTaskClass,
		Runtime:           integrationhost.ModelRuntimeCompatibility{MinimumVersion: minimumRuntime, MaximumVersion: "2.9.9"},
		FeatureSetVersion: ffmpeg.AudioAdFeatureSetVersion,
		Weights: integrationhost.ModelWeightsVariant{
			Hardware: integrationhost.PodcastAdModelHardwareCPU, Format: integrationhost.PodcastAdWeightsFormat,
			Size: int64(len(weights)), SHA256: hex.EncodeToString(weightsDigest[:]),
		},
		License:    integrationhost.ModelLicense{Effective: "fixture", AUP: "fixture", Attribution: "fixture"},
		Provenance: integrationhost.ModelProvenance{Source: "fixture", TrainingCorpusSHA256: strings.Repeat("a", 64)},
		Calibration: integrationhost.ModelCalibration{
			Flavor: string(store.FlavorInserted), HoldoutSHA256: strings.Repeat("b", 64),
			HoldoutBoundaryPrecision: 0.99, BoundaryPrecisionFloor: 0.95, HoldoutClassF1: 0.01,
		},
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, integrationhost.ModelManifestFilename), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, integrationhost.ModelWeightsFilename), weights, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func encodeAudioAdWeights(t *testing.T, weights [15]float64) []byte {
	t.Helper()
	var encoded bytes.Buffer
	encoded.WriteString("BFPAW1\x00\x00")
	for _, weight := range weights {
		if err := binary.Write(&encoded, binary.LittleEndian, weight); err != nil {
			t.Fatal(err)
		}
	}
	return encoded.Bytes()
}

func importAudioAdAudioFixture(t *testing.T, st *store.Store) store.Media {
	t.Helper()
	path := filepath.Join(t.TempDir(), "episode.wav")
	writeAudioAdWAV(t, path)
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "audio fixture",
		Container: "wav", Codec: "pcm_s16le", Class: store.MediaClassAudio,
		ContentFingerprint: fingerprint, DurationSecondsHint: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return media
}

func writeAudioAdWAV(t *testing.T, path string) {
	t.Helper()
	const sampleRate = 8192
	const seconds = 2
	const pcmDataBytes uint32 = sampleRate * seconds * 2
	samples := make([]int16, sampleRate*seconds)
	for i := range samples {
		samples[i] = int16(12000 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	mustWriteAudioAdBinary(t, &wav, 36+pcmDataBytes)
	wav.WriteString("WAVEfmt ")
	mustWriteAudioAdBinary(t, &wav, uint32(16))
	mustWriteAudioAdBinary(t, &wav, uint16(1))
	mustWriteAudioAdBinary(t, &wav, uint16(1))
	mustWriteAudioAdBinary(t, &wav, uint32(sampleRate))
	mustWriteAudioAdBinary(t, &wav, uint32(sampleRate*2))
	mustWriteAudioAdBinary(t, &wav, uint16(2))
	mustWriteAudioAdBinary(t, &wav, uint16(16))
	wav.WriteString("data")
	mustWriteAudioAdBinary(t, &wav, pcmDataBytes)
	mustWriteAudioAdBinary(t, &wav, samples)
	if err := os.WriteFile(path, wav.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustWriteAudioAdBinary(t *testing.T, dst *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(dst, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
