package conductor

import (
	"testing"
	"time"
)

// TestBestCalibrationUnderJitter is the CORE-2244 clock criterion: on a local
// network with jittery, sometimes one-sided delay, the lowest-round-trip
// exchange of eight puts the offset within 5 ms of the truth, where a single
// exchange can be tens of milliseconds out.
func TestBestCalibrationUnderJitter(t *testing.T) {
	const trueOffset = 1234567 * time.Microsecond // member ahead of the server
	server := time.Date(2026, 11, 17, 20, 0, 0, 0, time.UTC)
	// A fixed linear congruential sequence: the test is deterministic.
	state := uint64(2244)
	intn := func(n uint64) time.Duration {
		state = state*6364136223846793005 + 1442695040888963407
		return time.Duration((state >> 33) % n) //nolint:gosec // below n, which is small
	}
	delay := func() time.Duration {
		d := 300*time.Microsecond + time.Duration(intn(1500))*time.Microsecond
		if intn(4) == 0 { // a Wi-Fi retry or a busy main thread, one way only
			d += time.Duration(20+intn(60)) * time.Millisecond
		}
		return d
	}
	worstSingle := time.Duration(0)
	for round := 0; round < 200; round++ {
		samples := make([]Calibration, 0, 8)
		for i := 0; i < 8; i++ {
			out, back := delay(), delay()
			t2 := server.Add(out)
			t3 := t2.Add(50 * time.Microsecond)
			t1 := server.Add(trueOffset)
			t4 := t3.Add(back).Add(trueOffset)
			c := Calibrate(t1, t2, t3, t4)
			samples = append(samples, c)
			if e := (c.Offset - trueOffset).Abs(); e > worstSingle {
				worstSingle = e
			}
			server = server.Add(100 * time.Millisecond)
		}
		// One exchange in each round is clean, as on any usable network.
		samples[intn(8)] = Calibrate(server.Add(trueOffset), server.Add(time.Millisecond),
			server.Add(time.Millisecond), server.Add(2*time.Millisecond).Add(trueOffset))
		best, ok := BestCalibration(samples)
		if !ok {
			t.Fatal("BestCalibration reported no samples")
		}
		if e := (best.Offset - trueOffset).Abs(); e > 5*time.Millisecond {
			t.Fatalf("round %d: filtered offset is %v out, want within 5ms", round, e)
		}
	}
	if worstSingle < 5*time.Millisecond {
		t.Fatalf("the jitter profile never distorted a single exchange (worst %v): the test proves nothing", worstSingle)
	}
	if _, ok := BestCalibration(nil); ok {
		t.Fatal("BestCalibration(nil) reported a sample")
	}
}
