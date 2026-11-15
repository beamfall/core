package offlinepack

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
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
