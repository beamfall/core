package integrationhost

import (
	"io"
	"os"
	"os/exec"
	"sync"
)

// outputPipes gives a worker pipes the supervisor owns in place of the ones
// exec.Cmd would create for a writer that is not a file. Cmd.Wait then waits
// only for the worker itself, never for a descendant still holding a pipe, so
// neither a tree that outlives its worker nor one that survived retirement can
// block the supervisor. After a clean retire the copies run to EOF, so no
// output is cut off; after a failed one they are abandoned (CORE-2753). The
// ffmpeg supervisor keeps its own copy: the two packages share no imports.
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
	_, err := io.Copy(w, r)
	// Close the read end, as exec does, so a child still writing after w
	// failed gets EPIPE rather than blocking on a full pipe. An error from a
	// read end abandon closed is never reported: drain does not follow abandon.
	_ = r.Close()
	if err != nil {
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
