package subtitleingest

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// ASS/SSA override tags reaching libass / libass-wasm are an untrusted-input
// surface with real CVE history: heap overflows and unbounded allocation via
// crafted override tags. Beamfall never parses ASS in-process on
// the server — the raw bytes pass through to a client libass engine or are
// flattened by ffmpeg — so the server's job here is a scope check that rejects
// the structurally hostile shapes before they reach any parser, not a full ASS
// validator.
//
// The limits below bound the three shapes a crafted ASS uses to attack a parser:
// a single monster line (line-length bomb / stack pressure), an override-tag
// flood inside one brace group (per-tag allocation), and an unbounded drawing-
// mode coordinate blob (the `\p` vector path, a documented allocation vector).
// They are deliberately generous against any legitimately authored file — real
// dialogue lines, karaoke, and signs stay well under them.
const (
	// MaxASSLineBytes caps one physical line. A crafted overflow line is orders
	// of magnitude longer than any authored dialogue or sign.
	MaxASSLineBytes = 64 << 10 // 64 KiB
	// MaxASSOverrideTagsPerLine caps override tags (backslash-introduced) inside
	// one line's brace groups. Elaborate karaoke uses dozens; a flood uses
	// thousands.
	MaxASSOverrideTagsPerLine = 256
	// MaxASSDrawingRunBytes caps a single drawing-mode run (the text following a
	// `\p<n>` with n>0, until `\p0`). A real vector sign is small; an unbounded
	// coordinate blob is the attack.
	MaxASSDrawingRunBytes = 16 << 10 // 16 KiB
)

// CheckASS validates raw ASS/SSA bytes against the override-tag scope limits,
// failing CLOSED (ErrCraftedASS) on the first violation. It scans line by line
// and never allocates proportionally to a crafted input, so it is itself safe on
// a hostile file. It does not mutate or re-encode the bytes — byte-identical
// passthrough to libass engines depends on the file being preserved.
func CheckASS(data []byte) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 4096), MaxASSLineBytes+1)
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Bytes()
		if len(raw) > MaxASSLineBytes {
			return fmt.Errorf("%w: line %d is %d bytes", ErrCraftedASS, line, len(raw))
		}
		if err := checkASSLine(string(raw), line); err != nil {
			return err
		}
	}
	// bufio.Scanner reports a token longer than its buffer as ErrTooLong; a line
	// that overruns MaxASSLineBytes is itself the line-length bomb, so translate
	// the scanner error into the crafted-ASS rejection rather than a generic I/O
	// error.
	if err := scanner.Err(); err != nil {
		if err == bufio.ErrTooLong {
			return fmt.Errorf("%w: a line exceeds %d bytes", ErrCraftedASS, MaxASSLineBytes)
		}
		return err
	}
	return nil
}

// checkASSLine enforces the per-line override-tag count and drawing-run length
// limits. Override tags live inside brace groups ("{\pos(1,2)\clip(...)}"); a
// backslash inside a group introduces one tag. Drawing mode opens with "\p<n>"
// (n>0) and closes with "\p0" or end of line, and the run between is the vector
// coordinate blob.
//
// The drawing run is measured wherever drawing mode is OPEN, inside a brace
// group as well as outside it. Scoping it to outside-the-group only ("{\p1" +
// blob + "}") let an attacker park the whole coordinate blob inside the group,
// bounded by nothing but the 4x-weaker line cap — the attacker picks the shape,
// so the limit has to hold for every shape drawing mode can take.
func checkASSLine(line string, num int) error {
	tags := 0
	inGroup := false
	inDrawing := false
	drawingRun := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '{':
			inGroup = true
		case c == '}':
			inGroup = false
		case inGroup && c == '\\':
			tags++
			if tags > MaxASSOverrideTagsPerLine {
				return fmt.Errorf("%w: line %d carries more than %d override tags", ErrCraftedASS, num, MaxASSOverrideTagsPerLine)
			}
			opened, closed := scanDrawingTag(line, i)
			if opened {
				inDrawing = true
				drawingRun = 0
			}
			if closed {
				inDrawing = false
			}
		case inDrawing:
			drawingRun++
			if drawingRun > MaxASSDrawingRunBytes {
				return fmt.Errorf("%w: line %d has a drawing run over %d bytes", ErrCraftedASS, num, MaxASSDrawingRunBytes)
			}
		}
	}
	return nil
}

// scanDrawingTag inspects a "\p<n>" drawing-mode tag beginning at the backslash
// index i, reporting whether it opens drawing mode (n>0) or closes it (n==0). A
// backslash that does not introduce a "\p" tag reports neither.
func scanDrawingTag(line string, i int) (opened, closed bool) {
	rest := line[i+1:]
	if len(rest) == 0 || (rest[0] != 'p' && rest[0] != 'P') {
		return false, false
	}
	digits := rest[1:]
	end := 0
	for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
		end++
	}
	if end == 0 {
		return false, false // "\pos(...)" positioning, not a drawing-mode toggle
	}
	if strings.Trim(digits[:end], "0") == "" {
		return false, true // \p0 closes drawing mode
	}
	return true, false
}
