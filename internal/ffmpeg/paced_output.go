package ffmpeg

import (
	"bytes"
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

// Gate reserves the segment pathname before the muxer can open it.
func (o *PacedHLSOutput) Gate(segment int) error {
	return makeHLSSegmentGate(filepath.Join(o.Dir, fmt.Sprintf("seg%05d.m4s", segment)))
}

// Receive drains one gated fragment. Reserving the next gate first prevents
// a fast muxer from writing another fragment while the publisher is busy.
// A zero limit uses the encoder's per-file ceiling. Callers can lower it.
func (o *PacedHLSOutput) Receive(segment int, playlist string, exited <-chan struct{}, limit int64) error {
	if limit <= 0 || limit > rlimitFSizeBytes {
		limit = rlimitFSizeBytes
	}
	if err := o.Gate(segment + 1); err != nil {
		return err
	}
	name := fmt.Sprintf("seg%05d.m4s", segment)
	f, err := os.CreateTemp(o.Dir, ".receive-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = readHLSSegmentGate(o.stage, name, f, exited, limit, func() (bool, error) {
		return o.segmentCommitted(playlist, name)
	})
	if closeErr := f.Close(); closeErr != nil {
		return errors.Join(closeErr, err)
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(o.Dir, name))
}

// FFmpeg closes the segment writer before atomically replacing its playlist.
// Checking that regular-file rename as the commit marker lets confinement
// forbid FIFO renames. The next unopened gate makes this the last URI.
func (o *PacedHLSOutput) segmentCommitted(playlist, name string) (bool, error) {
	f, err := o.Open(playlist)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	var tail [128]byte // final segment URI and optional ENDLIST
	n, err := f.ReadAt(tail[:], max(0, info.Size()-int64(len(tail))))
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	last := bytes.TrimSuffix(tail[:n], []byte("#EXT-X-ENDLIST\n"))
	return bytes.HasSuffix(last, []byte("\n"+name+"\n")), nil
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
