package offlinepack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// TestFailureReasonIsClosed maps every build error class to its stable
// CORE-1743 failure code, including wrapped and joined errors.
func TestFailureReasonIsClosed(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"empty target":    {ErrEmptyTarget, FailureEmptyTarget},
		"unknown kind":    {fmt.Errorf("x: %w", ErrUnknownTargetKind), FailureUnknownTargetKind},
		"unknown quality": {ErrUnknownQuality, FailureUnknownQuality},
		"attestation":     {fmt.Errorf("%w: bad key", ErrDeviceAttestation), FailureDeviceAttestation},
		"transcode":       {fmt.Errorf("%w: exit 1", ErrTranscode), FailureTranscode},
		"item too large":  {ErrItemTooLarge, FailureByteBound},
		"budget":          {errors.Join(errors.New("ctx"), ErrBuildBudget), FailureByteBound},
		"missing source":  {&fs.PathError{Op: "open", Path: "media-alpha", Err: fs.ErrNotExist}, FailureSourceUnavailable},
		"parts":           {ErrManifestPartsUnsupported, FailurePartsUnsupported},
		"anything else":   {errors.New("disk on fire"), FailureBuild},
	}
	for name, tc := range cases {
		if got := FailureReason(tc.err); got != tc.want {
			t.Errorf("%s: FailureReason = %q, want %q", name, got, tc.want)
		}
	}
}

// retryWindowStore cancels the caller's context right after
// RestartOfflinePackBuild commits and can fail the reread that follows.
type retryWindowStore struct {
	*store.Store
	cancel      context.CancelFunc
	failReread  bool
	afterCommit bool
}

func (s *retryWindowStore) RestartOfflinePackBuild(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID, payload []byte, now time.Time) (bool, error) {
	restarted, err := s.Store.RestartOfflinePackBuild(ctx, id, profileID, payload, now)
	s.afterCommit = restarted
	s.cancel()
	return restarted, err
}

func (s *retryWindowStore) OfflinePackBuildByID(ctx context.Context, id store.OfflinePackID, profileID store.ProfileID) (store.OfflinePackBuild, error) {
	if s.afterCommit && s.failReread {
		return store.OfflinePackBuild{}, errors.New("reread failed")
	}
	return s.Store.OfflinePackBuildByID(ctx, id, profileID)
}

func failedBuildForRetry(t *testing.T, svc *Service, scope auth.Scope) store.OfflinePackID {
	t.Helper()
	id, err := NewPackID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildJobPayloadFor(scope, BuildPackRequest{TargetKind: "media", TargetRef: "media-alpha", Quality: "low", PackID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TrackBuild(t.Context(), payload); err != nil {
		t.Fatal(err)
	}
	if err := svc.FailBuild(t.Context(), id, FailureBuild); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRetryBuildCancelledAfterRestartLeavesNoOrphan covers CORE-1867: once
// RetryBuild commits the restart, a cancelled request either still hands the
// caller the payload to queue, or the row is back to failed. It never returns
// without a payload while the row is building.
func TestRetryBuildCancelledAfterRestartLeavesNoOrphan(t *testing.T) {
	for _, failReread := range []bool{false, true} {
		svc, authService, st, _ := newFixture(t)
		scope := scopeFor(t, authService, "session-default")
		id := failedBuildForRetry(t, svc, scope)
		ctx, cancel := context.WithCancel(t.Context())
		wrapped := &retryWindowStore{Store: st, cancel: cancel, failReread: failReread}
		svc.store = wrapped

		build, payload, err := svc.RetryBuild(ctx, scope, id)
		cancel()
		if !wrapped.afterCommit {
			t.Fatalf("failReread=%v: RetryBuild never restarted the failed build", failReread)
		}
		row, rowErr := st.OfflinePackBuildByID(t.Context(), id, scope.ProfileID)
		if rowErr != nil {
			t.Fatal(rowErr)
		}
		if failReread {
			if err == nil || payload != nil || row.State != store.OfflinePackBuildFailed {
				t.Fatalf("failed reread: err=%v payload=%t row=%q, want an error and the row back to failed", err, payload != nil, row.State)
			}
			continue
		}
		if err != nil || payload == nil || build.State != store.OfflinePackBuildBuilding || row.State != store.OfflinePackBuildBuilding {
			t.Fatalf("cancelled request: err=%v payload=%t build=%q row=%q, want the payload to queue for a building row", err, payload != nil, build.State, row.State)
		}
	}
}

func TestBuildTranscodeStreamsProgressAndSpeed(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	scope := scopeFor(t, authService, "session-default")
	id, err := NewPackID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildJobPayloadFor(scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityMedium, PackID: id,
	})
	if err != nil {
		t.Fatal(err)
	}

	var midProgress float64
	var midSpeed float64
	var regressionProgress float64
	svc.SetTranscoder(func(_ context.Context, _ string, dst string, _ string, _ []store.MediaFileStream, onProgress func(TranscodeProgress)) error {
		if onProgress == nil {
			t.Fatal("expected non-nil onProgress for tracked background build")
		}
		// media-alpha fixture has DurationSeconds=120.
		onProgress(TranscodeProgress{OutTime: 30 * time.Second, Speed: 10.5})
		row, err := st.OfflinePackBuildByID(t.Context(), id, scope.ProfileID)
		if err != nil {
			return err
		}
		midProgress = row.Progress
		midSpeed = svc.BuildTranscodeSpeed(id)

		// Regression and out-of-range duration must stay bounded and monotonic.
		onProgress(TranscodeProgress{OutTime: 10 * time.Second, Speed: 14.1})
		row, err = st.OfflinePackBuildByID(t.Context(), id, scope.ProfileID)
		if err != nil {
			return err
		}
		regressionProgress = row.Progress
		if speed := svc.BuildTranscodeSpeed(id); speed != 14.1 {
			t.Fatalf("live speed after update = %v, want 14.1", speed)
		}
		return os.WriteFile(dst, []byte("transcoded-bytes"), 0o600)
	})

	if _, err := svc.HandleBuildJob(t.Context(), payload); err != nil {
		t.Fatalf("HandleBuildJob: %v", err)
	}
	if midProgress <= 0 || midProgress >= 1 {
		t.Fatalf("mid-transcode progress = %v, want strictly in (0, 1)", midProgress)
	}
	if regressionProgress < midProgress {
		t.Fatalf("progress regressed from %v to %v", midProgress, regressionProgress)
	}
	if midSpeed != 10.5 {
		t.Fatalf("mid-transcode speed = %v, want 10.5", midSpeed)
	}
	if after := svc.BuildTranscodeSpeed(id); after != 0 {
		t.Fatalf("speed after build completion = %v, want 0", after)
	}
}

func TestBuildTranscodeSpeedClearsOnFailure(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	scope := scopeFor(t, authService, "session-default")
	id, err := NewPackID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildJobPayloadFor(scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityLow, PackID: id,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.SetTranscoder(func(_ context.Context, _, _, _ string, _ []store.MediaFileStream, onProgress func(TranscodeProgress)) error {
		onProgress(TranscodeProgress{OutTime: 24 * time.Second, Speed: 9.2})
		if got := svc.BuildTranscodeSpeed(id); got != 9.2 {
			t.Fatalf("live speed during transcode = %v, want 9.2", got)
		}
		return errors.New("encoder failed")
	})
	if _, err := svc.HandleBuildJob(t.Context(), payload); err == nil {
		t.Fatal("expected build failure")
	}
	if after := svc.BuildTranscodeSpeed(id); after != 0 {
		t.Fatalf("speed after transcode failure = %v, want 0", after)
	}
}

func TestBuildMultiPartVideoReportsLiveProgressAndSpeed(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	part1Path := filepath.Join(t.TempDir(), "media-alpha-part1.bin")
	if err := os.WriteFile(part1Path, []byte("part-1-video-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaFileVersion(t.Context(), "media-alpha", store.MediaFile{
		Path: part1Path, ContentFingerprint: "fp-media-alpha-part1", PartOrdinal: 1,
	}, []store.MediaFileStream{{Index: 0, Type: "video", Codec: "h264", Default: true}}); err != nil {
		t.Fatalf("AddMediaFileVersion: %v", err)
	}

	scope := scopeFor(t, authService, "session-default")
	id, err := NewPackID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildJobPayloadFor(scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityLow, PackID: id, ManifestParts: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var partCalls int
	var observedProgress []float64
	var observedSpeeds []float64
	svc.SetTranscoder(func(_ context.Context, _ string, dst string, _ string, _ []store.MediaFileStream, onProgress func(TranscodeProgress)) error {
		partCalls++
		onProgress(TranscodeProgress{OutTime: 50 * time.Second, Duration: 100 * time.Second, Speed: 8.5 + float64(partCalls)})
		row, err := st.OfflinePackBuildByID(t.Context(), id, scope.ProfileID)
		if err != nil {
			t.Fatalf("OfflinePackBuildByID during part %d: %v", partCalls, err)
		}
		observedProgress = append(observedProgress, row.Progress)
		observedSpeeds = append(observedSpeeds, svc.BuildTranscodeSpeed(id))
		return os.WriteFile(dst, []byte("transcoded-part"), 0o600)
	})

	if _, err := svc.HandleBuildJob(t.Context(), payload); err != nil {
		t.Fatalf("HandleBuildJob: %v", err)
	}
	if partCalls != 2 {
		t.Fatalf("transcoder calls = %d, want 2 parts", partCalls)
	}
	if len(observedProgress) != 2 || observedProgress[0] <= 0 || observedProgress[1] <= observedProgress[0] {
		t.Fatalf("multipart progress = %v, want strictly increasing positive percentages", observedProgress)
	}
	if len(observedSpeeds) != 2 || observedSpeeds[0] != 9.5 || observedSpeeds[1] != 10.5 {
		t.Fatalf("multipart speeds = %v, want [9.5 10.5]", observedSpeeds)
	}
	if after := svc.BuildTranscodeSpeed(id); after != 0 {
		t.Fatalf("speed after multipart build = %v, want 0", after)
	}
}
