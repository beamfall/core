package ffmpeg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
