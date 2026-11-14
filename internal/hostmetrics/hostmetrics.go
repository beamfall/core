// Package hostmetrics keeps a short in-memory history of how loaded the host
// is: processor use, memory use and the bytes this server sent and received.
// It backs the Console dashboard graphs. Samples live only in this process —
// nothing is persisted and nothing leaves the server.
package hostmetrics

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultInterval is how often the host is sampled.
	DefaultInterval = 10 * time.Second
	// DefaultWindow is how much history is kept: one hour, plus slack so a
	// one-hour graph is never short a point at its left edge.
	DefaultWindow = 65 * time.Minute
)

// Sample is one reading of the host.
type Sample struct {
	At time.Time
	// CPUPercent is processor use across every core, 0–100. CPUKnown is false
	// when the platform could not report it for this sample.
	CPUPercent float64
	CPUKnown   bool
	// MemoryUsedBytes and MemoryTotalBytes are physical memory. Both are zero
	// when the platform could not report them.
	MemoryUsedBytes  uint64
	MemoryTotalBytes uint64
	// SentBytesPerSecond and ReceivedBytesPerSecond are this server's own
	// network traffic, averaged over the sample interval.
	SentBytesPerSecond     float64
	ReceivedBytesPerSecond float64
}

// probe reads the host. cpuPercent reports use since the previous call.
type probe interface {
	cpuPercent() (float64, bool)
	memory() (used, total uint64, ok bool)
}

// Sampler samples the host on an interval into a fixed ring.
type Sampler struct {
	interval time.Duration
	probe    probe

	sent     atomic.Uint64
	received atomic.Uint64

	mu           sync.Mutex
	ring         []Sample
	next         int
	full         bool
	lastAt       time.Time
	lastSent     uint64
	lastReceived uint64
}

// New returns a Sampler holding window's worth of samples taken every interval.
// Non-positive arguments take the defaults.
func New(interval, window time.Duration) *Sampler {
	return newSampler(interval, window, newProbe())
}

func newSampler(interval, window time.Duration, p probe) *Sampler {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if window <= 0 {
		window = DefaultWindow
	}
	size := int(window / interval)
	if size < 1 {
		size = 1
	}
	return &Sampler{interval: interval, probe: p, ring: make([]Sample, size)}
}

// Interval is the time between samples.
func (s *Sampler) Interval() time.Duration { return s.interval }

// Run samples until ctx is cancelled. The first sample is taken at once so the
// dashboard is not empty for a whole interval after start.
func (s *Sampler) Run(ctx context.Context) {
	s.sample(time.Now())
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.sample(now)
		}
	}
}

func (s *Sampler) sample(now time.Time) {
	cpu, cpuKnown := s.probe.cpuPercent()
	used, total, memKnown := s.probe.memory()
	if !memKnown {
		used, total = 0, 0
	}
	sent, received := s.sent.Load(), s.received.Load()

	s.mu.Lock()
	defer s.mu.Unlock()
	next := Sample{
		At:               now.UTC(),
		CPUPercent:       clampPercent(cpu),
		CPUKnown:         cpuKnown,
		MemoryUsedBytes:  used,
		MemoryTotalBytes: total,
	}
	if elapsed := now.Sub(s.lastAt).Seconds(); !s.lastAt.IsZero() && elapsed > 0 {
		next.SentBytesPerSecond = float64(sent-s.lastSent) / elapsed
		next.ReceivedBytesPerSecond = float64(received-s.lastReceived) / elapsed
	}
	s.lastAt, s.lastSent, s.lastReceived = now, sent, received
	s.ring[s.next] = next
	s.next = (s.next + 1) % len(s.ring)
	if s.next == 0 {
		s.full = true
	}
}

// Samples returns the held history, oldest first.
func (s *Sampler) Samples() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.full {
		return append([]Sample(nil), s.ring[:s.next]...)
	}
	out := make([]Sample, 0, len(s.ring))
	out = append(out, s.ring[s.next:]...)
	return append(out, s.ring[:s.next]...)
}

// Listener wraps l so every byte read from or written to its connections is
// counted as this server's network traffic.
func (s *Sampler) Listener(l net.Listener) net.Listener {
	return countingListener{Listener: l, sampler: s}
}

type countingListener struct {
	net.Listener
	sampler *Sampler
}

func (l countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, sampler: l.sampler}, nil
}

type countingConn struct {
	net.Conn
	sampler *Sampler
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.sampler.received.Add(uint64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.sampler.sent.Add(uint64(n))
	}
	return n, err
}

func clampPercent(v float64) float64 {
	switch {
	case v != v, v < 0: // NaN or negative
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}

// tickPercent turns two cumulative busy/total tick readings into a percentage.
func tickPercent(prevBusy, prevTotal, busy, total uint64) (float64, bool) {
	if total <= prevTotal || busy < prevBusy {
		return 0, false
	}
	return 100 * float64(busy-prevBusy) / float64(total-prevTotal), true
}
