package logtail

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRingKeepsLastLinesOldestFirst(t *testing.T) {
	r := NewRing(3)
	for _, s := range []string{"one\n", "two\n", "thr", "ee\nfour\n", "partial"} {
		if _, err := r.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(r.Lines(0), ",")
	if got != "two,three,four" {
		t.Fatalf("Lines(0) = %q", got)
	}
	if got := strings.Join(r.Lines(2), ","); got != "three,four" {
		t.Fatalf("Lines(2) = %q", got)
	}
	if r.Capacity() != 3 {
		t.Fatalf("Capacity = %d", r.Capacity())
	}
}

func TestRingBoundsLongLines(t *testing.T) {
	r := NewRing(2)
	long := strings.Repeat("a", MaxLineBytes*3)
	_, _ = r.Write([]byte(long))
	_, _ = r.Write([]byte(long + "\n"))
	lines := r.Lines(0)
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	if len(lines[0]) > MaxLineBytes+len(truncatedSuffix) || !strings.HasSuffix(lines[0], truncatedSuffix) {
		t.Fatalf("line not truncated: len %d", len(lines[0]))
	}
}

func TestRingDropsTheTokenTheLimitSplitsBeforeRedacting(t *testing.T) {
	for name, line := range map[string]string{
		"bare":   "level=INFO token=sk-" + strings.Repeat("x", MaxLineBytes),
		"quoted": `level=INFO password="alpha beta ` + strings.Repeat("x", MaxLineBytes) + `"`,
		"escape": `level=INFO msg="a \" b" password="alpha beta ` + strings.Repeat("x", MaxLineBytes) + `"`,
	} {
		r := NewRing(1)
		_, _ = r.Write([]byte(line + "\n"))
		got := r.Lines(0)[0]
		if got != "level=INFO"+truncatedSuffix && !strings.HasPrefix(got, `level=INFO msg="a \" b"`) {
			t.Errorf("%s: kept %q", name, got)
		}
		for _, leak := range []string{"alpha", "beta", "sk-", "xxx"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: truncated line leaks %q: %q", name, leak, got)
			}
		}
	}
}

func TestHandlerTeesRedactedRecordsIntoRing(t *testing.T) {
	var out bytes.Buffer
	ring := NewRing(10)
	logger := slog.New(NewTextHandler(&out, ring, nil)).With("component", "test")
	logger.Info("connected", "remote", "203.0.113.5:443", "token", "abc123")

	if RingOf(logger) != ring {
		t.Fatal("RingOf did not find the ring through With")
	}
	if !strings.Contains(out.String(), "203.0.113.5") {
		t.Fatalf("stderr output should stay unredacted for the host operator: %q", out.String())
	}
	lines := ring.Lines(0)
	if len(lines) != 1 {
		t.Fatalf("ring lines = %v", lines)
	}
	if strings.Contains(lines[0], "203.0.113.5") || strings.Contains(lines[0], "abc123") {
		t.Fatalf("ring line not redacted: %q", lines[0])
	}
	if RingOf(slog.New(slog.NewTextHandler(&out, nil))) != nil {
		t.Fatal("RingOf should be nil for a plain handler")
	}
}
