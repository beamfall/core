// Package logtail keeps the most recent server log lines in memory, redacted,
// so an admin can read them from the console without shell access to the
// host (CORE-1716). Nothing is written to disk: the ring only mirrors what the
// operator log already writes to stderr.
package logtail

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/beamfall/core/internal/redact"
)

// DefaultCapacity is the number of lines the server keeps for the console.
const DefaultCapacity = 2000

// MaxLineBytes bounds one stored line, so one oversized record cannot pin
// unbounded memory.
const MaxLineBytes = 8 << 10

const truncatedSuffix = " …[truncated]"

// Ring is an io.Writer that keeps the last Capacity complete lines written to
// it, each redacted with redact.LogLine before it is stored. It is safe for
// concurrent use.
type Ring struct {
	mu      sync.Mutex
	lines   []string
	next    int
	full    bool
	partial []byte
}

// NewRing returns a ring that keeps capacity lines; capacity below one keeps
// one.
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{lines: make([]string, capacity)}
}

// Capacity reports how many lines the ring keeps.
func (r *Ring) Capacity() int { return len(r.lines) }

// Write stores every complete line in p. A trailing fragment without a
// newline is held until the rest of the line arrives. It never fails.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rest := p
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			r.partial = appendBounded(r.partial, rest)
			break
		}
		line := appendBounded(r.partial, rest[:i])
		r.partial = r.partial[:0]
		r.store(string(line))
		rest = rest[i+1:]
	}
	return len(p), nil
}

func appendBounded(dst, src []byte) []byte {
	if room := MaxLineBytes + 1 - len(dst); room < len(src) {
		if room <= 0 {
			return dst
		}
		src = src[:room]
	}
	return append(dst, src...)
}

func (r *Ring) store(line string) {
	if len(line) > MaxLineBytes {
		line = redact.LogLine(truncateAtToken(line[:MaxLineBytes])) + truncatedSuffix
	} else {
		line = redact.LogLine(line)
	}
	r.lines[r.next] = line
	r.next++
	if r.next == len(r.lines) {
		r.next = 0
		r.full = true
	}
}

// truncateAtToken drops the token a line limit splits, so redaction sees only
// whole tokens: a cut secret, address or path may no longer match its pattern.
// A quoted value the limit leaves open is dropped with its key, because
// whitespace inside quotes is not a token boundary.
func truncateAtToken(cut string) string {
	quoteStart, inQuote, escaped := -1, false, false
	for i := 0; i < len(cut); i++ {
		switch c := cut[i]; {
		case escaped:
			escaped = false
		case inQuote && c == '\\':
			escaped = true
		case c == '"':
			inQuote = !inQuote
			if inQuote {
				quoteStart = i
			}
		}
	}
	if inQuote {
		cut = cut[:quoteStart]
	}
	if i := strings.LastIndexAny(cut, " \t"); i >= 0 {
		return cut[:i]
	}
	return ""
}

// Lines returns up to limit of the most recent lines, oldest first. A limit
// of zero or less returns every kept line.
func (r *Ring) Lines(limit int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := r.next
	if r.full {
		count = len(r.lines)
	}
	if limit > 0 && limit < count {
		count = limit
	}
	out := make([]string, 0, count)
	start := r.next - count
	if start < 0 {
		start += len(r.lines)
	}
	for i := 0; i < count; i++ {
		out = append(out, r.lines[(start+i)%len(r.lines)])
	}
	return out
}

// Handler is a slog text handler that also tees every record into a Ring.
type Handler struct {
	slog.Handler
	ring *Ring
}

// NewTextHandler returns a text handler writing to w and to ring.
func NewTextHandler(w io.Writer, ring *Ring, opts *slog.HandlerOptions) *Handler {
	return &Handler{Handler: slog.NewTextHandler(io.MultiWriter(ring, w), opts), ring: ring}
}

// Handle writes the record through the wrapped text handler.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	return h.Handler.Handle(ctx, record)
}

// WithAttrs keeps the ring attached to the derived handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{Handler: h.Handler.WithAttrs(attrs), ring: h.ring}
}

// WithGroup keeps the ring attached to the derived handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{Handler: h.Handler.WithGroup(name), ring: h.ring}
}

// Ring returns the ring the handler tees into.
func (h *Handler) Ring() *Ring { return h.ring }

// RingOf returns the ring behind logger, or nil when logger does not tee
// into one.
func RingOf(logger *slog.Logger) *Ring {
	if logger == nil {
		return nil
	}
	if h, ok := logger.Handler().(*Handler); ok {
		return h.ring
	}
	return nil
}
