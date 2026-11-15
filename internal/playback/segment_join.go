package playback

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// joinFragments writes the fMP4 media segments parts, in order, to dst as one
// media segment (CORE-1843). The first part's styp box opens it and every part
// contributes its fragments (moof and mdat, whose sample offsets are relative
// to their own moof, so they move unchanged). sidx boxes are dropped: each
// indexes only the part it came from. The segment is written to a temporary
// file and renamed into place, so a reader never sees it half written.
func joinFragments(dst string, parts []string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".join-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	for i, part := range parts {
		body, err := os.ReadFile(part) // #nosec G304 -- part is an encoder segment inside the session's own output directory
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
			if _, err := tmp.Write(box.data); err != nil {
				return err
			}
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
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
