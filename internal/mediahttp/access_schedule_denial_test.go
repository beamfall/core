package mediahttp

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beamfall/core/internal/auth"
)

// TestRefuseAccessScheduleEndedOnlyAnswersTheScheduleRefusal is CORE-2950:
// every media transport answers a Stream Token re-verify that fails only on
// the access schedule with 403 and the denial-reason header, and leaves every
// other grant failure to its existing status.
// feature:access-schedules
func TestRefuseAccessScheduleEndedOnlyAnswersTheScheduleRefusal(t *testing.T) {
	for _, err := range []error{auth.ErrAccessOutsideSchedule, fmt.Errorf("grant: %w", auth.ErrAccessOutsideSchedule)} {
		rec := httptest.NewRecorder()
		if !refuseAccessScheduleEnded(rec, err) {
			t.Fatalf("refuseAccessScheduleEnded(%v) = false, want true", err)
		}
		if rec.Code != http.StatusForbidden || rec.Header().Get(auth.DenialReasonHeader) != auth.DenialReasonAccessScheduleEnded {
			t.Fatalf("refusal for %v: status = %d header = %q, want 403 %s", err, rec.Code, rec.Header().Get(auth.DenialReasonHeader), auth.DenialReasonAccessScheduleEnded)
		}
	}
	rec := httptest.NewRecorder()
	if refuseAccessScheduleEnded(rec, errors.New("stream token expired")) {
		t.Fatal("refuseAccessScheduleEnded(other) = true, want false")
	}
	if rec.Body.Len() != 0 || rec.Header().Get(auth.DenialReasonHeader) != "" {
		t.Fatalf("other error wrote a response: header = %q body = %q", rec.Header().Get(auth.DenialReasonHeader), rec.Body.String())
	}
}
