package lyrics

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
)

// maxTagBytes bounds how much of an ID3v2 tag is read.
const maxTagBytes = 16 << 20

// embedded holds the lyric frames found in an ID3v2 tag.
type embedded struct {
	synced   Document // first SYLT frame with millisecond stamps
	unsynced string   // first non-empty USLT frame
}

var errNoTag = errors.New("lyrics: no ID3v2 tag")

// readID3 reads the SYLT and USLT frames of the ID3v2 tag at the start of r.
func readID3(r io.Reader) (embedded, error) {
	var header [10]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return embedded{}, errNoTag
	}
	if string(header[:3]) != "ID3" {
		return embedded{}, errNoTag
	}
	version, flags := header[3], header[5]
	if version < 2 || version > 4 {
		return embedded{}, errNoTag
	}
	size := syncsafe(header[6:10])
	if size <= 0 || size > maxTagBytes {
		return embedded{}, errNoTag
	}
	tag := make([]byte, size)
	if _, err := io.ReadFull(r, tag); err != nil {
		return embedded{}, err
	}
	if flags&0x80 != 0 && version < 4 {
		tag = unsync(tag)
	}
	if flags&0x40 != 0 && version >= 3 {
		tag = skipExtendedHeader(tag, version)
	}
	return parseFrames(tag, version, flags&0x80 != 0), nil
}

func parseFrames(tag []byte, version byte, tagUnsync bool) embedded {
	var out embedded
	idLen, headLen := 4, 10
	if version == 2 {
		idLen, headLen = 3, 6
	}
	for len(tag) >= headLen && tag[0] != 0 {
		id := string(tag[:idLen])
		var size int
		var flags uint16
		switch version {
		case 2:
			size = int(tag[3])<<16 | int(tag[4])<<8 | int(tag[5])
		case 3:
			size = int(binary.BigEndian.Uint32(tag[4:8]))
			flags = binary.BigEndian.Uint16(tag[8:10])
		default:
			size = syncsafe(tag[4:8])
			flags = binary.BigEndian.Uint16(tag[8:10])
		}
		if size < 0 || size > len(tag)-headLen {
			break
		}
		body := tag[headLen : headLen+size]
		tag = tag[headLen+size:]
		body, ok := frameBody(body, version, flags, tagUnsync)
		if !ok {
			continue
		}
		switch id {
		case "SYLT", "SLT":
			if len(out.synced.Lines) == 0 {
				out.synced = parseSYLT(body)
			}
		case "USLT", "ULT":
			if out.unsynced == "" {
				out.unsynced = parseUSLT(body)
			}
		}
	}
	return out
}

// frameBody undoes per-frame flags, refusing compressed or encrypted frames.
func frameBody(body []byte, version byte, flags uint16, tagUnsync bool) ([]byte, bool) {
	switch version {
	case 3:
		if flags&0x00C0 != 0 { // compression, encryption
			return nil, false
		}
		if flags&0x0020 != 0 { // grouping identity
			if len(body) < 1 {
				return nil, false
			}
			body = body[1:]
		}
	case 4:
		if flags&0x000C != 0 { // compression, encryption
			return nil, false
		}
		if flags&0x0040 != 0 { // grouping identity
			if len(body) < 1 {
				return nil, false
			}
			body = body[1:]
		}
		if flags&0x0002 != 0 || tagUnsync { // frame unsynchronisation
			body = unsync(body)
		}
		if flags&0x0001 != 0 { // data length indicator
			if len(body) < 4 {
				return nil, false
			}
			body = body[4:]
		}
	}
	return body, true
}

// parseUSLT reads encoding, language, descriptor and text.
func parseUSLT(body []byte) string {
	if len(body) < 4 {
		return ""
	}
	enc := body[0]
	_, rest := splitTerminated(body[4:], enc)
	return strings.TrimSpace(decodeText(rest, enc))
}

// parseSYLT reads a synchronised lyric frame with millisecond timestamps.
// Entries starting with a line break begin a new line; when no entry does, each
// entry is its own line.
func parseSYLT(body []byte) Document {
	if len(body) < 6 || body[4] != 2 { // timestamp format 2 = milliseconds
		return Document{}
	}
	enc := body[0]
	_, rest := splitTerminated(body[6:], enc)
	type entry struct {
		text  string
		start int
	}
	var entries []entry
	breaks := false
	for len(rest) > 0 {
		raw, after := splitTerminated(rest, enc)
		if len(after) < 4 {
			break
		}
		text := decodeText(raw, enc)
		if strings.HasPrefix(text, "\n") || strings.HasPrefix(text, "\r") {
			breaks = true
		}
		entries = append(entries, entry{text: text, start: int(binary.BigEndian.Uint32(after[:4]))})
		rest = after[4:]
	}
	var lines []Line
	for i, e := range entries {
		startsLine := !breaks || i == 0 || strings.HasPrefix(e.text, "\n") || strings.HasPrefix(e.text, "\r")
		text := strings.Trim(e.text, "\r\n")
		if startsLine {
			lines = append(lines, Line{StartMs: intPtr(e.start), Text: text})
			continue
		}
		lines[len(lines)-1].Text += text
	}
	for i := range lines {
		lines[i].Text = strings.TrimSpace(lines[i].Text)
	}
	if len(lines) == 0 {
		return Document{}
	}
	return Document{Synced: true, Lines: capLines(lines)}
}

// splitTerminated splits b at the encoding's string terminator.
func splitTerminated(b []byte, enc byte) ([]byte, []byte) {
	if enc == 1 || enc == 2 {
		for i := 0; i+1 < len(b); i += 2 {
			if b[i] == 0 && b[i+1] == 0 {
				return b[:i], b[i+2:]
			}
		}
		return b, nil
	}
	for i, c := range b {
		if c == 0 {
			return b[:i], b[i+1:]
		}
	}
	return b, nil
}

func decodeText(b []byte, enc byte) string {
	switch enc {
	case 0:
		runes := make([]rune, len(b))
		for i, c := range b {
			runes[i] = rune(c)
		}
		return string(runes)
	case 1, 2:
		bigEndian := true
		if enc == 1 && len(b) >= 2 {
			switch {
			case b[0] == 0xFF && b[1] == 0xFE:
				bigEndian, b = false, b[2:]
			case b[0] == 0xFE && b[1] == 0xFF:
				b = b[2:]
			}
		}
		units := make([]uint16, len(b)/2)
		for i := range units {
			if bigEndian {
				units[i] = binary.BigEndian.Uint16(b[2*i:])
			} else {
				units[i] = binary.LittleEndian.Uint16(b[2*i:])
			}
		}
		return string(utf16.Decode(units))
	default:
		return strings.ToValidUTF8(string(b), "�")
	}
}

func syncsafe(b []byte) int {
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

// unsync removes the 0x00 inserted after each 0xFF.
func unsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xFF && i+1 < len(b) && b[i+1] == 0 {
			i++
		}
	}
	return out
}

func skipExtendedHeader(tag []byte, version byte) []byte {
	if len(tag) < 4 {
		return nil
	}
	var size int
	if version == 3 {
		size = int(binary.BigEndian.Uint32(tag[:4])) + 4
	} else {
		size = syncsafe(tag[:4])
	}
	if size < 0 || size > len(tag) {
		return nil
	}
	return tag[size:]
}
