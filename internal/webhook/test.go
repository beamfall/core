package webhook

import (
	"context"
	"strconv"
)

// TestResult is the outcome of one test-send.
type TestResult struct {
	OK          bool
	StatusClass string
	// Diagnostic is the fixed failure class, empty on success.
	Diagnostic string
	DurationMs int64
}

// Test delivers one synthetic notification to the endpoint id, once, with no
// retry and no dead-letter, and records it in the delivery log. A disabled
// endpoint can still be tested.
func (e *Emitter) Test(ctx context.Context, id string) (TestResult, error) {
	var target *Endpoint
	for _, ep := range e.endpoints() {
		if ep.ID == id {
			ep := ep
			target = &ep
			break
		}
	}
	if target == nil {
		return TestResult{}, ErrUnknownEndpoint
	}
	started := e.now()
	n := Notification{
		ID:         "test-" + strconv.FormatInt(started.UnixNano(), 10),
		Kind:       KindTest,
		OccurredAt: started.UTC(),
	}
	status, err := e.sendNotification(ctx, *target, n, genericNotificationBody(n))
	outcome := OutcomeDelivered
	if err != nil {
		outcome = OutcomeFailed
	}
	e.recordDelivery(*target, KindTest, outcome, status, 1, started, true)
	e.flushState()
	return TestResult{
		OK:          err == nil,
		StatusClass: statusClass(status),
		Diagnostic:  diagnosticText(err),
		DurationMs:  e.now().Sub(started).Milliseconds(),
	}, nil
}
