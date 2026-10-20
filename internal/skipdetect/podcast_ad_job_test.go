package skipdetect

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

type audioAdJobStoreFake struct {
	media      []store.AudioAdMedia
	disabled   map[store.MediaID]bool
	receipts   map[string]bool
	active     map[string]store.Job
	requested  []store.Job
	applied    []store.DetectionRun
	applyError error
}

func (f *audioAdJobStoreFake) AudioAdMediaAfter(_ context.Context, after store.MediaID, limit int) ([]store.AudioAdMedia, error) {
	media := append([]store.AudioAdMedia(nil), f.media...)
	sort.Slice(media, func(i, j int) bool { return media[i].ID < media[j].ID })
	result := make([]store.AudioAdMedia, 0, limit)
	for _, item := range media {
		if item.ID <= after || f.disabled[item.ID] {
			continue
		}
		result = append(result, item)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (f *audioAdJobStoreFake) AudioAdMediaByID(_ context.Context, id store.MediaID) (store.AudioAdMedia, bool, error) {
	if f.disabled[id] {
		return store.AudioAdMedia{}, false, nil
	}
	for _, item := range f.media {
		if item.ID == id {
			return item, true, nil
		}
	}
	return store.AudioAdMedia{}, false, nil
}

func (f *audioAdJobStoreFake) DetectionRunEligible(_ context.Context, id store.MediaID, _ string, identity store.DetectionIdentity) (bool, error) {
	return !f.receipts[string(id)+":"+identity.ArtifactIdentity], nil
}

func (f *audioAdJobStoreFake) ApplyDetectionRun(_ context.Context, run store.DetectionRun) error {
	f.applied = append(f.applied, run)
	if f.applyError != nil {
		return f.applyError
	}
	f.receipts[string(run.MediaID)+":"+run.Identity.ArtifactIdentity] = true
	return nil
}

func (f *audioAdJobStoreFake) EnqueueJob(_ context.Context, job store.Job) (store.Job, error) {
	f.requested = append(f.requested, job)
	if active, ok := f.active[job.DedupeKey]; ok {
		return active, nil
	}
	f.active[job.DedupeKey] = job
	return job, nil
}

type audioAdExtractorFake struct {
	features ffmpeg.AudioAdFeatureSet
	err      error
	calls    int
}

func (f *audioAdExtractorFake) AudioAdFeatures(context.Context, string, int) (ffmpeg.AudioAdFeatureSet, error) {
	f.calls++
	return f.features, f.err
}

func audioAdJobsFixture(t *testing.T, st AudioAdJobStore, extractor AudioAdFeatureExtractor, artifact string, flavor store.SegmentFlavor, metrics func(context.Context, AudioAdMetric)) *AudioAdJobs {
	t.Helper()
	model := audioAdTestModel(flavor)
	calibration := AudioAdCalibration{
		ModelID: model.ModelID, ModelVersion: model.ModelVersion,
		FeatureSetVersion: model.FeatureSetVersion, Flavor: flavor,
		HoldoutBoundaryPrecision: 0.99, BoundaryPrecisionFloor: 0.95,
		HoldoutClassF1: 0.01,
	}
	configured, err := NewAudioAdJobs(st, extractor, AudioAdJobConfig{
		ArtifactIdentity: artifact,
		Identity: store.DetectionIdentity{
			ModelID: model.ModelID, ModelVersion: model.ModelVersion,
			ModelHash: "model-hash", ArtifactIdentity: artifact,
			FeatureSetVersion: model.FeatureSetVersion,
			CalibrationID:     "calibration", CalibrationVersion: "1",
			CalibrationHash: "calibration-hash",
		},
		Model: model, Calibration: calibration, Metrics: metrics,
		Now: func() time.Time { return time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return configured
}

func TestAudioAdArmUsesPerMediaDedupeAndCompletedReceipts(t *testing.T) {
	st := &audioAdJobStoreFake{
		media: []store.AudioAdMedia{
			{ID: "media-a", Path: "a.m4a", DurationSeconds: 60},
			{ID: "media-b", Path: "b.m4a", DurationSeconds: 60},
		},
		receipts: map[string]bool{"media-a:artifact-1": true}, active: map[string]store.Job{},
		disabled: map[store.MediaID]bool{},
	}
	configured := audioAdJobsFixture(t, st, &audioAdExtractorFake{}, "artifact-1", store.FlavorInserted, nil)
	arm, err := configured.NewArmJob("settled-scan-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	arm.ID = "arm-1"
	if _, err := configured.ArmHandler()(context.Background(), arm); err != nil {
		t.Fatal(err)
	}
	if len(st.requested) != 1 {
		t.Fatalf("children = %d, want 1", len(st.requested))
	}
	child := st.requested[0]
	wantKey := "podcast-ad-audio:media-b:artifact-1"
	if child.ID == "" || child.DedupeKey != wantKey || child.Kind != AudioAdJobKind {
		t.Fatalf("child = %+v, want caller ID and %q", child, wantKey)
	}

	st.receipts["media-a:artifact-2"] = false
	upgraded := audioAdJobsFixture(t, st, &audioAdExtractorFake{}, "artifact-2", store.FlavorInserted, nil)
	upgradeArm, err := upgraded.NewArmJob("settled-scan-2", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	upgradeArm.ID = "arm-2"
	if _, err := upgraded.ArmHandler()(context.Background(), upgradeArm); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(st.requested, func(job store.Job) bool {
		return job.DedupeKey == "podcast-ad-audio:media-a:artifact-2"
	}) {
		t.Fatal("artifact upgrade did not reopen completed Media")
	}
}

func TestAudioAdPersistedJobValuesStayStable(t *testing.T) {
	if got := string(AudioAdArmJobKind); got != "podcast-ad-arm" {
		t.Fatalf("arm kind = %q", got)
	}
	if got := string(AudioAdJobKind); got != "podcast-ad-audio" {
		t.Fatalf("child kind = %q", got)
	}
	configured := audioAdJobsFixture(t, &audioAdJobStoreFake{}, &audioAdExtractorFake{}, "artifact-1", store.FlavorInserted, nil)
	arm, err := configured.NewArmJob("settled-scan", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if arm.DedupeKey != "podcast-ad-arm:settled-scan:artifact-1" {
		t.Fatalf("arm dedupe key = %q", arm.DedupeKey)
	}
	child, err := configured.newMediaJob("arm-1", "media-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	prefix := "job-podcast-ad-"
	if child.DedupeKey != "podcast-ad-audio:media-a:artifact-1" ||
		!strings.HasPrefix(string(child.ID), prefix) || len(child.ID) != len(prefix)+32 {
		t.Fatalf("child = %+v", child)
	}
}

func TestAudioAdArmCallerIDDistinguishesActiveDedupe(t *testing.T) {
	st := &audioAdJobStoreFake{
		media:    []store.AudioAdMedia{{ID: "media-a", Path: "a.m4a", DurationSeconds: 60}},
		receipts: map[string]bool{}, disabled: map[store.MediaID]bool{},
		active: map[string]store.Job{
			"podcast-ad-audio:media-a:artifact-1": {ID: "already-active"},
		},
	}
	configured := audioAdJobsFixture(t, st, &audioAdExtractorFake{}, "artifact-1", store.FlavorInserted, nil)
	arm, err := configured.NewArmJob("settled-scan", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	arm.ID = "arm-new"
	if _, err := configured.ArmHandler()(context.Background(), arm); err != nil {
		t.Fatal(err)
	}
	if len(st.requested) != 1 || st.requested[0].ID == "" || st.requested[0].ID == "already-active" {
		t.Fatalf("requested child = %+v", st.requested)
	}
}

func TestAudioAdConcurrentScanArmsDoNotLoseEarlierCursorMedia(t *testing.T) {
	st := &audioAdJobStoreFake{receipts: map[string]bool{}, active: map[string]store.Job{}, disabled: map[store.MediaID]bool{}}
	for i := 0; i < audioAdArmBatchSize+1; i++ {
		st.media = append(st.media, store.AudioAdMedia{
			ID:   store.MediaID(time.Unix(int64(i), 0).UTC().Format("150405")),
			Path: "audio.m4a", DurationSeconds: 60,
		})
	}
	configured := audioAdJobsFixture(t, st, &audioAdExtractorFake{}, "artifact-1", store.FlavorInserted, nil)
	armA, err := configured.NewArmJob("settled-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	armA.ID = "arm-a"
	result, err := configured.ArmHandler()(context.Background(), armA)
	if !errors.Is(err, jobs.ErrYielded) {
		t.Fatalf("first arm = %v, want yielded", err)
	}
	armA.Checkpoint = result.Checkpoint
	st.media = append(st.media, store.AudioAdMedia{ID: "000-new", Path: "new.m4a", DurationSeconds: 60})
	armB, err := configured.NewArmJob("settled-b", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	armB.ID = "arm-b"
	if _, err := configured.ArmHandler()(context.Background(), armB); !errors.Is(err, jobs.ErrYielded) {
		t.Fatalf("second arm = %v, want yielded", err)
	}
	if _, err := configured.ArmHandler()(context.Background(), armA); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(st.requested, func(job store.Job) bool {
		return job.DedupeKey == "podcast-ad-audio:000-new:artifact-1"
	}) {
		t.Fatal("concurrent settled scan lost Media behind the first arm cursor")
	}
}

func TestAudioAdHandlerAppliesOneRunAndSeparatesBoundaryEvidence(t *testing.T) {
	st := &audioAdJobStoreFake{
		media:    []store.AudioAdMedia{{ID: "media-a", Path: "a.m4a", DurationSeconds: 60}},
		receipts: map[string]bool{}, active: map[string]store.Job{}, disabled: map[store.MediaID]bool{},
	}
	extractor := &audioAdExtractorFake{features: audioAdTestFeatures()}
	var metrics []AudioAdMetric
	configured := audioAdJobsFixture(t, st, extractor, "artifact-1", store.FlavorInserted, func(_ context.Context, metric AudioAdMetric) {
		metrics = append(metrics, metric)
	})
	child, err := configured.newMediaJob("arm-1", "media-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configured.Handler()(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if len(st.applied) != 1 || len(st.applied[0].Candidates) != 1 {
		t.Fatalf("applied = %+v", st.applied)
	}
	candidate := st.applied[0].Candidates[0]
	if !candidate.BoundaryEligible || candidate.Flavor != store.FlavorInserted {
		t.Fatalf("candidate = %+v", candidate)
	}
	if len(metrics) != 2 || metrics[1].ClassConfidence == 0 || metrics[1].BoundaryScore == 0 ||
		metrics[1].ArtifactIdentity != "artifact-1" || metrics[1].RunID != string(child.ID) || metrics[1].SignalTier != "audio" {
		t.Fatalf("metrics = %+v", metrics)
	}
	if _, err := configured.Handler()(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if len(st.applied) != 1 {
		t.Fatalf("completed receipt reapplied: %d calls", len(st.applied))
	}
}

func TestAudioAdHandlerRecordsZeroCandidateAndDropsHostRead(t *testing.T) {
	for _, test := range []struct {
		name       string
		flavor     store.SegmentFlavor
		features   ffmpeg.AudioAdFeatureSet
		candidates int
		eligible   bool
	}{
		{name: "zero candidate", flavor: store.FlavorInserted, features: ffmpeg.AudioAdFeatureSet{
			Version: ffmpeg.AudioAdFeatureSetVersion, WindowSeconds: ffmpeg.AudioAdFeatureWindowSeconds,
			Frames: []ffmpeg.AudioAdFeatureFrame{{StartSeconds: 0, RMSDBFS: -20, SpectralFlatness: 0}},
		}, candidates: 0},
		{name: "host read", flavor: store.FlavorHostRead, features: audioAdTestFeatures(), candidates: 1, eligible: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			st := &audioAdJobStoreFake{
				media:    []store.AudioAdMedia{{ID: "media-a", Path: "a.m4a", DurationSeconds: 60}},
				receipts: map[string]bool{}, active: map[string]store.Job{}, disabled: map[store.MediaID]bool{},
			}
			configured := audioAdJobsFixture(t, st, &audioAdExtractorFake{features: test.features}, "artifact-1", test.flavor, nil)
			child, err := configured.newMediaJob("arm-1", "media-a", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := configured.Handler()(context.Background(), child); err != nil {
				t.Fatal(err)
			}
			if len(st.applied) != 1 || len(st.applied[0].Candidates) != test.candidates {
				t.Fatalf("applied = %+v", st.applied)
			}
			if test.candidates == 1 && st.applied[0].Candidates[0].BoundaryEligible != test.eligible {
				t.Fatalf("candidate = %+v", st.applied[0].Candidates[0])
			}
			eligible, err := st.DetectionRunEligible(context.Background(), "media-a", store.AudioAdDetectorMethod, configured.identity)
			if err != nil {
				t.Fatal(err)
			}
			if eligible {
				t.Fatal("completed receipt did not terminate future arms")
			}
		})
	}
}

func TestAudioAdHandlerToggleOffIsInert(t *testing.T) {
	st := &audioAdJobStoreFake{
		media:    []store.AudioAdMedia{{ID: "media-a", Path: "a.m4a", DurationSeconds: 60}},
		receipts: map[string]bool{}, active: map[string]store.Job{},
		disabled: map[store.MediaID]bool{"media-a": true},
	}
	extractor := &audioAdExtractorFake{features: audioAdTestFeatures()}
	configured := audioAdJobsFixture(t, st, extractor, "artifact-1", store.FlavorInserted, nil)
	child, err := configured.newMediaJob("arm-1", "media-a", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configured.Handler()(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if extractor.calls != 0 || len(st.applied) != 0 {
		t.Fatalf("toggle-off work: extracts=%d applies=%d", extractor.calls, len(st.applied))
	}
}
