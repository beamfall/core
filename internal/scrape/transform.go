package scrape

// Value transforms: the engine half of the fixed plugin.TransformKinds
// vocabulary. Each kind is a total function over strings that either
// normalizes the input or reports it unparseable — an unparseable value is
// omitted from the Result, exactly like an unresolved path. Deliberately not
// a DSL: kinds are added rarely, when a real scraper needs one.

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// transform applies one fixed-vocabulary transform kind to a mapped value.
// Unknown kinds report false (Validate refuses them long before this runs).
func transform(kind, value string) (string, bool) {
	switch kind {
	case "year-from-date":
		return yearFromDate(value)
	case "date-from-rfc822":
		return dateFromRFC822(value)
	case "seconds-from-clock":
		return secondsFromClock(value)
	}
	return "", false
}

// leadingYearPattern matches ISO-ish dates ("2014-09-05", "2014") — four
// digits followed by a non-digit or end.
var leadingYearPattern = regexp.MustCompile(`^\d{4}(\D|$)`)

// yearFromDate extracts "YYYY" from YYYY-MM-DD…, a bare year, or an RFC-822
// date (TMDB release_date, MusicBrainz date, RSS pubDate).
func yearFromDate(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if leadingYearPattern.MatchString(value) {
		return value[:4], true
	}
	if t, ok := parseRFC822(value); ok {
		return strconv.Itoa(t.Year()), true
	}
	return "", false
}

// dateFromRFC822 normalizes an RFC-822 date (the RSS pubDate shape) to
// "YYYY-MM-DD". Values already in that form pass through.
func dateFromRFC822(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if t, ok := parseRFC822(value); ok {
		return t.Format("2006-01-02"), true
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.Format("2006-01-02"), true
	}
	return "", false
}

// rfc822Layouts covers the pubDate variants real feeds emit: two- and
// four-digit years, numeric and named zones, one- and two-digit days.
var rfc822Layouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	time.RFC822Z,
	time.RFC822,
}

func parseRFC822(value string) (time.Time, bool) {
	for _, layout := range rfc822Layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// secondsFromClock normalizes "HH:MM:SS", "MM:SS", or plain seconds (the
// itunes:duration variants) to a seconds count.
func secondsFromClock(value string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) > 3 {
		return "", false
	}
	total := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return "", false
		}
		total = total*60 + n
	}
	return strconv.Itoa(total), true
}
