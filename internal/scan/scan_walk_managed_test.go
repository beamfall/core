package scan

import (
	"errors"
	"fmt"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestSkipManagedAdmissionInFlight proves the scan treats the store's refusal
// of a path a managed import is still admitting as a skip, not a scan failure,
// and passes every other import error through (CORE-1089).
func TestSkipManagedAdmissionInFlight(t *testing.T) {
	inFlight := fmt.Errorf("%w: /library/clip.mkv", store.ErrManagedAdmissionInFlight)
	if err := skipManagedAdmissionInFlight(inFlight, "/library/clip.mkv"); err != nil {
		t.Fatalf("in-flight refusal = %v, want skipped (nil)", err)
	}
	other := errors.New("disk full")
	if err := skipManagedAdmissionInFlight(other, "/library/clip.mkv"); !errors.Is(err, other) {
		t.Fatalf("other import error = %v, want %v passed through", err, other)
	}
}
