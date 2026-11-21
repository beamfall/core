//go:build unix

package ffmpeg

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestOutputPipesSurvivorCannotBlockReap is the CORE-2753 regression: a
// process outside the reach of the group kill that still holds the child's
// stdout must not block the reap once the group is reported as survived, and
// the survivor error must reach the caller.
func TestOutputPipesSurvivorCannotBlockReap(t *testing.T) {
	var out bytes.Buffer
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", "printf ok")
	cmd.Stdout = &out
	pipes, err := newOutputPipes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	// The holder stands in for a group member the sweep could not kill.
	holder := exec.Command("sleep", "30")
	holder.ExtraFiles = pipes.writers
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_ = holder.Wait()
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pipes.started()
	survived := fmt.Errorf("sweep: %w", errGroupSurvived)
	done := make(chan error, 1)
	go func() { done <- pipes.finish(errors.Join(survived, cmd.Wait())) }()
	select {
	case err := <-done:
		if !errors.Is(err, errGroupSurvived) {
			t.Fatalf("finish = %v, want the survivor error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a pipe-holding survivor blocked the reap")
	}
}

// TestOutputPipesCleanReapKeepsAllOutput proves a clean reap drains the
// pipes to EOF, so a large output written just before exit is never cut off.
func TestOutputPipesCleanReapKeepsAllOutput(t *testing.T) {
	const size = 4 << 20
	var out, errOut bytes.Buffer
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("head -c %d /dev/zero | tr '\\0' x; printf err >&2", size))
	cmd.Stdout, cmd.Stderr = &out, &errOut
	pipes, err := newOutputPipes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pipes.started()
	if err := pipes.finish(cmd.Wait()); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if out.Len() != size || strings.Trim(out.String(), "x") != "" {
		t.Fatalf("stdout kept %d of %d bytes", out.Len(), size)
	}
	if errOut.String() != "err" {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

// TestOutputPipesSharedWriterGetsOnePipe keeps exec's rule that one writer
// for both streams is fed through a single pipe.
func TestOutputPipesSharedWriterGetsOnePipe(t *testing.T) {
	var both bytes.Buffer
	// #nosec G204 -- fixed shell and test-owned script.
	cmd := exec.Command("/bin/sh", "-c", "printf a; printf b >&2; printf c")
	cmd.Stdout, cmd.Stderr = &both, &both
	pipes, err := newOutputPipes(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if len(pipes.readers) != 1 {
		t.Fatalf("got %d pipes, want 1", len(pipes.readers))
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pipes.started()
	if err := pipes.finish(cmd.Wait()); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if both.String() != "abc" {
		t.Fatalf("output = %q, want abc", both.String())
	}
}
