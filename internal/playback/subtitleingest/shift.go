package subtitleingest

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Server-applied subtitle offset. A viewer's persisted nudge is
// applied by re-issuing the WebVTT with every cue timestamp shifted, so the
// correction reaches every client — including a cast receiver that renders a
// side-loaded VTT and has no sub-delay control of its own.
//
// Shifting is a pure text transform over cue timing lines. Cue payload text is
// never touched, so a shift cannot alter what the sanitiser already vetted.

// ShiftWebVTT returns vtt with every cue timing line shifted by offsetMS
// (negative pulls cues earlier, positive pushes them later). A zero offset
// returns the input unchanged. Timestamps that would go negative clamp to zero
// rather than wrapping, so an over-large negative nudge degrades to "from the
// start" instead of dropping cues off the timeline.
func ShiftWebVTT(vtt string, offsetMS int) string {
	if offsetMS == 0 {
		return vtt
	}
	lines := strings.Split(vtt, "\n")
	sawHeader := false
	for blockStart := 0; blockStart < len(lines); {
		if strings.TrimSuffix(lines[blockStart], "\r") == "" {
			blockStart++
			continue
		}
		blockEnd := blockStart + 1
		for blockEnd < len(lines) && strings.TrimSuffix(lines[blockEnd], "\r") != "" {
			blockEnd++
		}
		first := strings.TrimSuffix(lines[blockStart], "\r")
		if !sawHeader || isNonCueBlockHeader(first) {
			sawHeader = true
			blockStart = blockEnd
			continue
		}

		timingLine := blockStart
		if !strings.Contains(first, "-->") {
			timingLine++
		}
		if timingLine >= blockEnd {
			blockStart = blockEnd
			continue
		}
		line := lines[timingLine]
		content := strings.TrimSuffix(line, "\r")
		lineEnding := line[len(content):]
		lines[timingLine] = shiftCueTimingLine(content, offsetMS) + lineEnding
		blockStart = blockEnd
	}
	return strings.Join(lines, "\n")
}

// shiftCueTimingLine shifts both timestamps of one "start --> end" cue line,
// preserving any trailing cue settings (align/line/position) verbatim. A line
// whose timestamps do not parse is returned unchanged — a malformed cue is left
// exactly as the sanitiser produced it rather than being silently rewritten.
func shiftCueTimingLine(line string, offsetMS int) string {
	start, rest, found := strings.Cut(line, "-->")
	if !found {
		return line
	}
	// The end timestamp is the first whitespace-delimited token after "-->";
	// anything after it is cue settings and rides along untouched.
	endField := strings.TrimLeft(rest, " \t")
	leading := rest[:len(rest)-len(endField)]
	end, settings, _ := strings.Cut(endField, " ")

	shiftedStart, okStart := shiftTimestamp(strings.TrimSpace(start), offsetMS)
	shiftedEnd, okEnd := shiftTimestamp(end, offsetMS)
	if !okStart || !okEnd {
		return line
	}
	out := shiftedStart + " -->" + leading + shiftedEnd
	if settings != "" {
		out += " " + settings
	}
	return out
}

// shiftTimestamp parses a WebVTT timestamp ("HH:MM:SS.mmm" or "MM:SS.mmm"),
// applies the millisecond offset, and re-renders it in the full HH:MM:SS.mmm
// form. It reports false when the value is not a timestamp.
func shiftTimestamp(ts string, offsetMS int) (string, bool) {
	millis, ok := parseVTTTimestamp(ts)
	if !ok {
		return "", false
	}
	delta := int64(offsetMS)
	var shifted int64
	switch {
	case delta > 0 && millis > math.MaxInt64-delta:
		return "", false
	case delta < 0 && (delta == math.MinInt64 || millis < -delta):
		shifted = 0
	default:
		shifted = millis + delta
	}
	return formatVTTTimestamp(shifted), true
}

// parseVTTTimestamp converts a WebVTT timestamp to milliseconds. Both the
// three-part (HH:MM:SS.mmm) and two-part (MM:SS.mmm) forms the spec allows are
// accepted.
func parseVTTTimestamp(ts string) (int64, bool) {
	ts = strings.TrimSpace(ts)
	clock, frac, hasFrac := strings.Cut(ts, ".")
	parts := strings.Split(clock, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, false
	}
	var total int64
	for _, part := range parts {
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil || value < 0 {
			return 0, false
		}
		if total > (math.MaxInt64-value)/60 {
			return 0, false
		}
		total = total*60 + value
	}
	if total > math.MaxInt64/1000 {
		return 0, false
	}
	millis := total * 1000
	if hasFrac {
		value, err := strconv.ParseInt(padMillis(frac), 10, 64)
		if err != nil {
			return 0, false
		}
		if millis > math.MaxInt64-value {
			return 0, false
		}
		millis += value
	}
	return millis, true
}

// padMillis normalises a fractional second to exactly three digits, truncating
// or zero-padding as needed.
func padMillis(frac string) string {
	if len(frac) >= 3 {
		return frac[:3]
	}
	return frac + strings.Repeat("0", 3-len(frac))
}

// formatVTTTimestamp renders milliseconds as a full WebVTT HH:MM:SS.mmm value.
func formatVTTTimestamp(millis int64) string {
	hours := millis / 3_600_000
	minutes := millis / 60_000 % 60
	seconds := millis / 1000 % 60
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, millis%1000)
}
