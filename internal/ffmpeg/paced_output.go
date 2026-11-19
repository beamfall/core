package ffmpeg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// PacedHLSOutput isolates one generation's atomic muxer output from the
// rendition cache. Playback decides when to publish completed files.
type PacedHLSOutput struct {
	Dir   string
	dst   string
	stage *os.File
}

// NewPacedHLSOutput creates an encoder directory inside the session's private
// scratch, where normal teardown and startup orphan cleanup can find it.
func (*Runner) NewPacedHLSOutput(dst string) (*PacedHLSOutput, error) {
	dir, err := os.MkdirTemp(filepath.Dir(dst), ".encoder-")
	if err != nil {
		return nil, err
	}
	stage, err := openHLSStage(dir)
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(dir))
	}
	return &PacedHLSOutput{Dir: dir, dst: dst, stage: stage}, nil
}

// Gate reserves the muxer's temporary segment pathname before it can open
// it. FFmpeg's temp_file mode renames this FIFO after closing the writer.
func (o *PacedHLSOutput) Gate(segment int) error {
	return makeHLSSegmentGate(filepath.Join(o.Dir, fmt.Sprintf("seg%05d.m4s.tmp", segment)))
}

// Receive drains one gated fragment. Reserving the next gate first prevents
// a fast muxer from writing another fragment while the publisher is busy.
func (o *PacedHLSOutput) Receive(segment int, exited <-chan struct{}) error {
	if err := o.Gate(segment + 1); err != nil {
		return err
	}
	name := fmt.Sprintf("seg%05d.m4s", segment)
	f, err := os.CreateTemp(o.Dir, ".receive-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = readHLSSegmentGate(o.stage, name+".tmp", f, exited)
	if closeErr := f.Close(); closeErr != nil {
		return errors.Join(closeErr, err)
	}
	if err != nil {
		return err
	}
	// FFmpeg's rename is the commit marker. If the process dies partway
	// through a write, a closed pipe alone cannot make the bytes playable.
	path := filepath.Join(o.Dir, name)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Lstat(path)
		switch {
		case err == nil && info.Mode()&os.ModeNamedPipe != 0:
			return os.Rename(f.Name(), path)
		case err == nil:
			return fmt.Errorf("ffmpeg: invalid HLS segment commit %q", name)
		case !errors.Is(err, os.ErrNotExist):
			return err
		}
		select {
		case <-exited:
			// Recheck once after Wait: the rename could have raced Lstat.
			info, err = os.Lstat(path)
			if err == nil && info.Mode()&os.ModeNamedPipe != 0 {
				return os.Rename(f.Name(), path)
			}
			return fmt.Errorf("ffmpeg: missing HLS segment commit %q: %w", name, io.ErrUnexpectedEOF)
		case <-ticker.C:
		}
	}
}

// Open opens a completed regular file relative to the pinned muxer directory.
func (o *PacedHLSOutput) Open(name string) (*os.File, error) {
	if !filepath.IsLocal(name) || filepath.Base(name) != name {
		return nil, fmt.Errorf("ffmpeg: invalid HLS filename %q", name)
	}
	return openHLSSidecar(o.stage, name)
}

// Publish copies from an already-opened file (or filtered playlist) into a
// server-owned temp file, then renames it into the cache. Moving an encoder-
// controlled path would let a symlink substitution escape its confinement.
func (o *PacedHLSOutput) Publish(name string, src io.Reader) error {
	if !filepath.IsLocal(name) || filepath.Base(name) != name {
		return fmt.Errorf("ffmpeg: invalid HLS filename %q", name)
	}
	f, err := os.CreateTemp(o.dst, ".publish-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, copyErr := io.Copy(f, src)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(o.dst, name))
}

func (o *PacedHLSOutput) Close() error {
	return errors.Join(o.stage.Close(), os.RemoveAll(o.Dir))
}
