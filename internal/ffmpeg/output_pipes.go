package ffmpeg

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

// outputPipes gives a supervised child pipes the supervisor owns in place of
// the ones exec.Cmd would create for a writer that is not a file. Cmd.Wait
// then waits only for the process, never for a descendant still holding a
// pipe, so a group that survived the kill sweep cannot block the reap. After
// a clean reap the copies run to EOF, so no output is cut off; after a
// survivor they are abandoned (CORE-2753).
type outputPipes struct {
	readers []*os.File
	writers []*os.File  // the child's ends, closed in the parent after Start
	dests   []io.Writer // where each reader is copied, once started
	copies  sync.WaitGroup
	mu      sync.Mutex
	copyErr error
}

// newOutputPipes replaces cmd.Stdout and cmd.Stderr with owned pipes where
// exec would otherwise copy. A writer used for both streams gets one pipe, as
// exec gives it, so its writes never interleave mid-call.
func newOutputPipes(cmd *exec.Cmd) (*outputPipes, error) {
	o := &outputPipes{}
	stdout, err := o.own(cmd.Stdout)
	if err != nil {
		o.closeAll()
		return nil, err
	}
	stderr := stdout
	if !sameWriter(cmd.Stdout, cmd.Stderr) {
		if stderr, err = o.own(cmd.Stderr); err != nil {
			o.closeAll()
			return nil, err
		}
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return o, nil
}

func sameWriter(a, b io.Writer) (same bool) {
	defer func() { _ = recover() }() // an uncomparable writer is never shared
	return a != nil && a == b
}

// own returns the file the child should write w through. A nil writer or a
// file needs no copy and is returned unchanged.
func (o *outputPipes) own(w io.Writer) (io.Writer, error) {
	if w == nil {
		return nil, nil
	}
	if f, ok := w.(*os.File); ok {
		return f, nil
	}
	r, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	o.readers = append(o.readers, r)
	o.writers = append(o.writers, pw)
	o.dests = append(o.dests, w)
	return pw, nil
}

// started closes the parent's copy of the child's ends, so EOF arrives once
// every process holding them has exited, and starts the copies. Starting them
// only after Start, as exec does, orders cmd.Process before any writer
// callback that signals the child.
func (o *outputPipes) started() {
	for _, w := range o.writers {
		_ = w.Close()
	}
	for i, r := range o.readers {
		o.copies.Add(1)
		go o.copy(o.dests[i], r)
	}
}

func (o *outputPipes) copy(w io.Writer, r *os.File) {
	defer o.copies.Done()
	if _, err := io.Copy(w, r); err != nil && !errors.Is(err, os.ErrClosed) {
		o.mu.Lock()
		if o.copyErr == nil {
			o.copyErr = err
		}
		o.mu.Unlock()
	}
}

// drain waits for the copies to reach EOF and returns the first copy error.
func (o *outputPipes) drain() error {
	o.copies.Wait()
	for _, r := range o.readers {
		_ = r.Close()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.copyErr
}

// abandon stops the copies without waiting for EOF, for a start failure or a
// group that kept a live member.
func (o *outputPipes) abandon() {
	o.closeAll()
	o.copies.Wait()
}

func (o *outputPipes) closeAll() {
	for _, w := range o.writers {
		_ = w.Close()
	}
	for _, r := range o.readers {
		_ = r.Close()
	}
}

// finish ends the copies after the leader was reaped with reapErr: it drains
// them unless the group survived, and reports a copy error only when the
// reap itself succeeded, as Cmd.Wait does.
func (o *outputPipes) finish(reapErr error) error {
	if errors.Is(reapErr, errGroupSurvived) {
		o.abandon()
		return reapErr
	}
	if copyErr := o.drain(); reapErr == nil {
		return copyErr
	}
	return reapErr
}
