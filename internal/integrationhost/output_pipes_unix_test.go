//go:build unix

package integrationhost

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

// TestSuperviseWorkerDescendantHoldingStdoutCannotBlock is a CORE-2753
// regression: a worker that exits while its descendant still holds stdout
// used to block in Cmd.Wait until the descendant exited, before retire could
// kill it. Now the worker's exit starts retirement and its output still
// arrives in full.
func TestSuperviseWorkerDescendantHoldingStdoutCannotBlock(t *testing.T) {
	var out bytes.Buffer
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", "printf ok; sleep 30 &")
	cmd.Stdout = &out
	done := make(chan error, 1)
	go func() { done <- superviseWorker(context.Background(), cmd, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("superviseWorker: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a descendant holding stdout blocked the supervisor")
	}
	if out.String() != "ok" {
		t.Fatalf("stdout = %q, want ok", out.String())
	}
}

// TestOutputPipesAbandonIgnoresHolder covers the failed-retire path: a live
// process still holding the pipe must not stall abandon.
func TestOutputPipesAbandonIgnoresHolder(t *testing.T) {
	var out bytes.Buffer
	cmd := exec.Command("true")
	cmd.Stdout = &out
	pipes, err := newOutputPipes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	holder := exec.Command("sleep", "30")
	holder.ExtraFiles = pipes.writers
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	})
	pipes.started()
	done := make(chan struct{})
	go func() {
		pipes.abandon()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("abandon waited for a pipe holder")
	}
}
