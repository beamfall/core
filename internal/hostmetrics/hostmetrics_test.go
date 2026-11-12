package hostmetrics

import (
	"io"
	"net"
	"testing"
	"time"
)

type fakeProbe struct {
	cpu         float64
	used, total uint64
}

func (p *fakeProbe) cpuPercent() (float64, bool)           { return p.cpu, true }
func (p *fakeProbe) memory() (used, total uint64, ok bool) { return p.used, p.total, true }

// The ring keeps exactly one window, oldest first, and turns the byte
// counters into a rate over the time between samples.
func TestSamplerKeepsOneWindowOldestFirstWithByteRates(t *testing.T) {
	p := &fakeProbe{cpu: 250, used: 4, total: 8}
	s := newSampler(10*time.Second, 30*time.Second, p)
	start := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	s.sample(start)
	for i := 1; i <= 4; i++ {
		s.sent.Add(1000)
		s.received.Add(50)
		p.cpu = float64(10 * i)
		s.sample(start.Add(time.Duration(i) * 10 * time.Second))
	}

	got := s.Samples()
	if len(got) != 3 {
		t.Fatalf("Samples len = %d, want the 3 the window holds", len(got))
	}
	for i, want := range []float64{20, 30, 40} {
		if got[i].CPUPercent != want || !got[i].CPUKnown {
			t.Errorf("sample %d cpu = %v (known %v), want %v", i, got[i].CPUPercent, got[i].CPUKnown, want)
		}
		if got[i].SentBytesPerSecond != 100 || got[i].ReceivedBytesPerSecond != 5 {
			t.Errorf("sample %d rates = %v sent, %v received, want 100 and 5", i, got[i].SentBytesPerSecond, got[i].ReceivedBytesPerSecond)
		}
		if got[i].MemoryUsedBytes != 4 || got[i].MemoryTotalBytes != 8 {
			t.Errorf("sample %d memory = %d/%d, want 4/8", i, got[i].MemoryUsedBytes, got[i].MemoryTotalBytes)
		}
	}
	if !got[0].At.Before(got[2].At) {
		t.Errorf("samples not oldest first: %v then %v", got[0].At, got[2].At)
	}
}

func TestSamplerClampsAnOutOfRangeProcessorReading(t *testing.T) {
	s := newSampler(time.Second, time.Minute, &fakeProbe{cpu: 250})
	s.sample(time.Now())
	if got := s.Samples()[0].CPUPercent; got != 100 {
		t.Fatalf("cpu = %v, want it clamped to 100", got)
	}
}

func TestListenerCountsBytesInBothDirections(t *testing.T) {
	s := newSampler(time.Second, time.Minute, &fakeProbe{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counted := s.Listener(ln)
	defer func() { _ = counted.Close() }()

	done := make(chan error, 1)
	go func() {
		conn, err := counted.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(conn, buf); err != nil {
			done <- err
			return
		}
		_, err = conn.Write([]byte("hello world"))
		done <- err
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("ping!")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, make([]byte, 11)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := s.received.Load(); got != 5 {
		t.Errorf("received = %d, want 5", got)
	}
	if got := s.sent.Load(); got != 11 {
		t.Errorf("sent = %d, want 11", got)
	}
}

func TestTickPercent(t *testing.T) {
	if got, ok := tickPercent(100, 1000, 150, 1200); !ok || got != 25 {
		t.Errorf("tickPercent = %v, %v; want 25, true", got, ok)
	}
	if _, ok := tickPercent(100, 1000, 100, 1000); ok {
		t.Error("tickPercent reported a reading with no elapsed ticks")
	}
}
