package playback

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// joinFragments writes the fMP4 media segments parts, in order, to dst as one
// media segment (CORE-1843). The first part's styp box opens it and every part
// contributes its fragments (moof and mdat, whose sample offsets are relative
// to their own moof, so they move unchanged). sidx boxes are dropped: each
// indexes only the part it came from. The caller supplies a private staging file.
func joinFragments(dst *os.File, open func(string) (*os.File, error), parts []string) error {
	for i, part := range parts {
		f, err := open(part)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return err
		}
		boxes, err := topLevelBoxes(body)
		if err != nil {
			return fmt.Errorf("join segment %s: %w", filepath.Base(part), err)
		}
		for _, box := range boxes {
			if box.kind == "sidx" || (box.kind == "styp" && i > 0) {
				continue
			}
			if _, err := dst.Write(box.data); err != nil {
				return err
			}
		}
	}
	return nil
}

// joinArtifact serializes writers by destination and publishes from a private
// staging file only while the same session still owns the directory.
func (s *Sessions) joinArtifact(ctx context.Context, h *hlsSession, dst string, parts []string, version uint64) error {
	for {
		s.mu.Lock()
		if h.state != sessionRunning {
			s.mu.Unlock()
			return ErrSessionEvicted
		}
		if h.artifactVersion != version {
			s.mu.Unlock()
			return ErrArtifactChanged
		}
		if wait := h.joins[dst]; wait != nil {
			s.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return ctx.Err()
			case <-h.scratch.cutoff:
				return ErrSessionEvicted
			}
		}
		if h.joins == nil {
			h.joins = make(map[string]chan struct{})
		}
		done := make(chan struct{})
		h.joins[dst] = done
		// A same-directory start must wait until the join has published or
		// discarded its staging file. The inputs are already finalized.
		s.holdInspectionLocked(h)
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(h.joins, dst)
			close(done)
			s.releaseInspectionLocked(h)
			s.mu.Unlock()
		}()
		break
	}
	if h.artifactReady(dst) {
		return nil
	}
	rel, err := h.artifactRelativePath(dst)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(h.dir, ".join-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	join := joinFragments
	if s.join != nil {
		join = s.join
	}
	err = join(tmp, func(path string) (*os.File, error) {
		f, _, err := h.openArtifact(path)
		return f, err
	}, parts)
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	s.mu.Lock()
	live := h.state == sessionRunning
	s.mu.Unlock()
	if !live {
		return ErrSessionEvicted
	}
	err = os.Rename(tmp.Name(), filepath.Join(h.dir, rel))
	s.mu.Lock()
	live = h.state == sessionRunning
	s.mu.Unlock()
	if !live {
		return ErrSessionEvicted
	}
	return err
}

// isoBox is one top-level ISO-BMFF box: its four-character type and its bytes,
// header included.
type isoBox struct {
	kind string
	data []byte
}

// topLevelBoxes splits body into its top-level boxes and refuses anything that
// is not a whole sequence of them.
func topLevelBoxes(body []byte) ([]isoBox, error) {
	var boxes []isoBox
	for off := 0; off < len(body); {
		rest := body[off:]
		if len(rest) < 8 {
			return nil, errors.New("trailing bytes are not a box")
		}
		size := uint64(binary.BigEndian.Uint32(rest[:4]))
		header := uint64(8)
		switch size {
		case 0: // the box runs to the end of the file
			size = uint64(len(rest))
		case 1: // a 64-bit size follows the type
			if len(rest) < 16 {
				return nil, errors.New("truncated box header")
			}
			size = binary.BigEndian.Uint64(rest[8:16])
			header = 16
		}
		if size < header || size > uint64(len(rest)) {
			return nil, fmt.Errorf("box %q has size %d with %d bytes left", rest[4:8], size, len(rest))
		}
		boxes = append(boxes, isoBox{kind: string(rest[4:8]), data: rest[:size]})
		off += int(size) // #nosec G115 -- size <= len(rest), an int, so it fits.
	}
	return boxes, nil
}
