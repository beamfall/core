package ffmpeg

import (
	"math"
	"strconv"
	"strings"
)

// maxMediaSeconds bounds every timestamp accepted from FFmpeg metadata. One
// hundred leap years is far above a legitimate Media runtime while keeping all
// float-to-integer and timestamp arithmetic well-defined.
const maxMediaSeconds = 100 * 366 * 24 * 60 * 60

// parseBoundedNumber accepts exactly one locale-independent decimal token and,
// when unit is non-empty, exactly one explicit unit token. It rejects trailing
// fields, NaN/Inf, overflow, and values outside the caller-owned semantic range.
func parseBoundedNumber(raw, unit string, minValue, maxValue float64) (float64, bool) {
	fields := strings.Fields(raw)
	wantFields := 1
	if unit != "" {
		wantFields = 2
	}
	if len(fields) != wantFields {
		return 0, false
	}
	if unit != "" && !strings.EqualFold(fields[1], unit) {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	if value < minValue || value > maxValue {
		return 0, false
	}
	return value, true
}

func parseBoundedInteger(raw string, minValue, maxValue int64) (int64, bool) {
	fields := strings.Fields(raw)
	if len(fields) != 1 {
		return 0, false
	}
	value, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || value < minValue || value > maxValue {
		return 0, false
	}
	return value, true
}
