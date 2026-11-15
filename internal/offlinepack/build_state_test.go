package offlinepack

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
