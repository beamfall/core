package subtitleingest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Non-libass clients (AVKit, ExoPlayer, the web <video> element) receive a
// flattened WebVTT projection of an ASS/SSA sidecar rather than the authored
// styling (ADR-0194 §5: "ASS→VTT styling loss is a documented limitation for
// non-libass clients"). FlattenASS produces that projection in-process, keeping
// the sidecar serve path free of an ffmpeg dependency; libass-capable clients
// take the raw-ASS passthrough route instead.
//
// The flattener is intentionally a projection, not a renderer: it recovers cue
// timing and dialogue text and discards positioning, karaoke, and drawing. The
// caller has already run CheckASS at the ingest boundary, so this operates on
// scope-bounded input and never on an unvalidated hostile file.

// FlattenASS converts ASS/SSA dialogue events to WebVTT cues, sorted by start
// time. Override tags ({...}) are stripped, ASS line breaks (\N, \n) become cue
// newlines, and hard spaces (\h) become spaces. Returns a header-only "WEBVTT"
// document when the input carries no [Events] section or no dialogue.
func FlattenASS(raw string) string {
	events := assEventsSection(raw)
	textIdx, ok := assTextFieldIndex(events.formatLine)
	if !ok {
		return "WEBVTT\n"
	}
	type cue struct {
		start    string
		end      string
		text     string
		startKey [4]int
	}
	var cues []cue
	for _, dialogue := range events.dialogueLines {
		fields := strings.SplitN(dialogue, ",", textIdx+1)
		if len(fields) <= textIdx {
			continue
		}
		start, startKey, okS := assTimeToVTT(strings.TrimSpace(fields[assStartField]))
		end, _, okE := assTimeToVTT(strings.TrimSpace(fields[assEndField]))
		if !okS || !okE {
			continue
		}
		text := flattenASSText(fields[textIdx])
		if text == "" {
			continue
		}
		cues = append(cues, cue{start: start, end: end, text: text, startKey: startKey})
	}
	sort.SliceStable(cues, func(i, j int) bool {
		for field := range cues[i].startKey {
			if cues[i].startKey[field] != cues[j].startKey[field] {
				return cues[i].startKey[field] < cues[j].startKey[field]
			}
		}
		return false
	})
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, cue := range cues {
		fmt.Fprintf(&b, "%s --> %s\n%s\n\n", cue.start, cue.end, cue.text)
	}
	return b.String()
}

// ASS Dialogue fields are positionally fixed for Start and End regardless of the
// declared Format order in practice, but the Text field position varies (it is
// always last and may contain commas). Start/End sit at indices 1 and 2 of the
// canonical v4+ Format.
const (
	assStartField = 1
	assEndField   = 2
)

type assEvents struct {
	formatLine    string
	dialogueLines []string
}

// assEventsSection extracts the [Events] section's Format line and Dialogue
// lines. Lines outside [Events] (Script Info, styles) are ignored.
func assEventsSection(raw string) assEvents {
	var out assEvents
	inEvents := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		switch {
		case strings.HasPrefix(trimmed, "["):
			inEvents = strings.EqualFold(trimmed, "[Events]")
		case !inEvents:
			continue
		case strings.HasPrefix(trimmed, "Format:"):
			out.formatLine = strings.TrimSpace(strings.TrimPrefix(trimmed, "Format:"))
		case strings.HasPrefix(trimmed, "Dialogue:"):
			out.dialogueLines = append(out.dialogueLines, strings.TrimSpace(strings.TrimPrefix(trimmed, "Dialogue:")))
		}
	}
	return out
}

// assTextFieldIndex returns the zero-based index of the Text field in the
// [Events] Format line, defaulting to the canonical v4+ position (9) when the
// Format line is absent. Text is always the final field.
func assTextFieldIndex(formatLine string) (int, bool) {
	if formatLine == "" {
		return 9, true // canonical ASS v4+ Dialogue Text position
	}
	fields := strings.Split(formatLine, ",")
	for i, f := range fields {
		if strings.EqualFold(strings.TrimSpace(f), "Text") {
			return i, true
		}
	}
	return 0, false
}

// assTimeToVTT converts an ASS timestamp (H:MM:SS.cc, centisecond precision) to
// a WebVTT timestamp (HH:MM:SS.mmm, millisecond precision).
func assTimeToVTT(ts string) (string, [4]int, bool) {
	parts := strings.Split(ts, ":")
	if len(parts) != 3 {
		return "", [4]int{}, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	secParts := strings.SplitN(parts[2], ".", 2)
	s, err3 := strconv.Atoi(secParts[0])
	if err1 != nil || err2 != nil || err3 != nil || h < 0 || m < 0 || s < 0 {
		return "", [4]int{}, false
	}
	millis := 0
	if len(secParts) == 2 {
		cs, err := strconv.Atoi(padCentis(secParts[1]))
		if err != nil {
			return "", [4]int{}, false
		}
		millis = cs * 10 // centiseconds → milliseconds
	}
	key := [4]int{h, m, s, millis}
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, millis), key, true
}

// padCentis normalises the fractional part to exactly two digits (centiseconds),
// the ASS convention, truncating or zero-padding as needed.
func padCentis(frac string) string {
	switch {
	case len(frac) >= 2:
		return frac[:2]
	case len(frac) == 1:
		return frac + "0"
	default:
		return "00"
	}
}

// flattenASSText strips ASS override-tag groups and translates ASS escapes into
// their WebVTT equivalents, yielding plain cue text.
func flattenASSText(text string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '{':
			depth++
		case c == '}':
			if depth > 0 {
				depth--
			}
		case depth > 0:
			// inside an override group: dropped
		case c == '\\' && i+1 < len(text):
			switch text[i+1] {
			case 'N', 'n':
				b.WriteByte('\n')
				i++
			case 'h':
				b.WriteByte(' ')
				i++
			default:
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	nonBlank := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonBlank = append(nonBlank, line)
		}
	}
	return strings.Join(nonBlank, "\n")
}
