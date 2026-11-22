// Package lyrics reads the lyrics of a local audio file: an LRC or plain-text
// sidecar beside it, or lyric frames embedded in its ID3v2 tag. It parses the
// text into lines, timed when the source carries timestamps.
package lyrics

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxLines bounds how many lines one document returns.
const MaxLines = 2000

// Line is one lyric line. StartMs is set only for synced lyrics.
type Line struct {
	StartMs *int
	Text    string
}

// Document is a parsed lyric text. Synced documents have a StartMs on every
// line, in ascending order.
type Document struct {
	Synced bool
	Lines  []Line
}

// Empty reports whether the document has no text worth showing.
func (d Document) Empty() bool {
	for _, line := range d.Lines {
		if strings.TrimSpace(line.Text) != "" {
			return false
		}
	}
	return true
}

var (
	lrcStamp   = regexp.MustCompile(`^\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	lrcTag     = regexp.MustCompile(`^\[([A-Za-z#]+):([^\]]*)\]\s*$`)
	wordStamp  = regexp.MustCompile(`<\d{1,3}:\d{1,2}(?:[.:]\d{1,3})?>`)
	spaceTrail = regexp.MustCompile(`[ \t]+$`)
)

// Parse reads lyric text. Lines carrying LRC timestamps make the document
// synced: untimed lines are dropped and lines sort by start time. Otherwise the
// text is returned as plain lines, outer blank lines trimmed.
func Parse(text string) Document {
	text = strings.ToValidUTF8(strings.TrimPrefix(text, "\uFEFF"), string(utf8.RuneError))
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	raw := strings.Split(text, "\n")

	offset := 0
	var timed []Line
	var plain []string
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if m := lrcTag.FindStringSubmatch(trimmed); m != nil && !lrcStamp.MatchString(trimmed) {
			if strings.EqualFold(m[1], "offset") {
				if v, err := strconv.Atoi(strings.TrimSpace(m[2])); err == nil {
					offset = v
				}
			}
			continue
		}
		var starts []int
		rest := trimmed
		for {
			m := lrcStamp.FindStringSubmatch(rest)
			if m == nil {
				break
			}
			starts = append(starts, stampMs(m[1], m[2], m[3]))
			rest = rest[len(m[0]):]
		}
		if len(starts) == 0 {
			plain = append(plain, spaceTrail.ReplaceAllString(line, ""))
			continue
		}
		body := strings.TrimSpace(wordStamp.ReplaceAllString(rest, ""))
		for _, start := range starts {
			timed = append(timed, Line{StartMs: intPtr(start), Text: body})
		}
	}

	if len(timed) > 0 {
		for i := range timed {
			start := max(*timed[i].StartMs-offset, 0)
			timed[i].StartMs = &start
		}
		sort.SliceStable(timed, func(i, j int) bool { return *timed[i].StartMs < *timed[j].StartMs })
		return Document{Synced: true, Lines: capLines(timed)}
	}
	return plainDocument(plain)
}

func plainDocument(lines []string) Document {
	first, last := 0, len(lines)
	for first < last && strings.TrimSpace(lines[first]) == "" {
		first++
	}
	for last > first && strings.TrimSpace(lines[last-1]) == "" {
		last--
	}
	out := make([]Line, 0, last-first)
	for _, text := range lines[first:last] {
		out = append(out, Line{Text: strings.TrimSpace(text)})
	}
	return Document{Lines: capLines(out)}
}

func stampMs(minutes, seconds, fraction string) int {
	m, _ := strconv.Atoi(minutes)
	s, _ := strconv.Atoi(seconds)
	ms := 0
	if fraction != "" {
		f, _ := strconv.Atoi(fraction)
		switch len(fraction) {
		case 1:
			ms = f * 100
		case 2:
			ms = f * 10
		default:
			ms = f
		}
	}
	return (m*60+s)*1000 + ms
}

func capLines(lines []Line) []Line {
	if len(lines) > MaxLines {
		return lines[:MaxLines]
	}
	return lines
}

func intPtr(v int) *int { return &v }
