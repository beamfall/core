package integrationhost

import (
	"context"
	"strings"
	"testing"
)

func TestSuperviseWorkerReportsStartFailure(t *testing.T) {
	cmd := workerCommandForMissingExecutable()
	err := superviseWorker(context.Background(), cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "launch worker") {
		t.Fatalf("start failure = %v, want launch worker error", err)
	}
}
