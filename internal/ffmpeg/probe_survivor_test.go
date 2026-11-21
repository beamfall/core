package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

// The capability probes stop at a run whose process group survived the kill
// sweep, and the survivor reaches the host through the reporter (CORE-2757).

var errInjectedSurvivor = fmt.Errorf("%w: process group 7", errGroupSurvived)

func TestGroupSurvivorReporterHearsTheSurvivor(t *testing.T) {
	var heard []error
	SetGroupSurvivorReporter(func(err error) { heard = append(heard, err) })
	t.Cleanup(func() { SetGroupSurvivorReporter(nil) })
	if err := reportGroupSurvivor(errInjectedSurvivor); err != errInjectedSurvivor {
		t.Fatalf("reportGroupSurvivor returned %v, want its argument", err)
	}
	if len(heard) != 1 || heard[0] != errInjectedSurvivor {
		t.Fatalf("reporter heard %v, want the one survivor", heard)
	}
	SetGroupSurvivorReporter(nil)
	_ = reportGroupSurvivor(errInjectedSurvivor) // a nil reporter is a no-op
}

// recordingHWAccelRunner fails the encoder named survivor with a survivor and
// passes every other encode, recording each encode it was asked to run.
type recordingHWAccelRunner struct {
	mu       sync.Mutex
	survivor string
	ran      []HWAccelEncode
}

func (r *recordingHWAccelRunner) run(ctx context.Context, encode HWAccelEncode) error {
	r.mu.Lock()
	r.ran = append(r.ran, encode)
	r.mu.Unlock()
	if encode.Encoder == r.survivor {
		return errInjectedSurvivor
	}
	return ctx.Err()
}

func TestHWAccelProbeStopsAtSurvivingFollowUpEncode(t *testing.T) {
	runner := &recordingHWAccelRunner{survivor: "hevc_nvenc"}
	result := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     runner.run,
		GOOS:       "linux",
		DRIPresent: func() bool { return false },
	})
	for _, encode := range runner.ran {
		if encode.Encoder == "av1_nvenc" {
			t.Fatalf("AV1 probe started after the HEVC survivor: %v", runner.ran)
		}
	}
	if result.SupportsHEVC(HWAccelNVENC) {
		t.Fatal("HEVC reported supported after its probe left a survivor")
	}
}

func TestHWAccelProbeSkipsLowPowerRetryAfterSurvivor(t *testing.T) {
	runner := &recordingHWAccelRunner{survivor: "h264_qsv"}
	result := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     runner.run,
		GOOS:       "linux",
		DRIPresent: func() bool { return true },
	})
	for _, encode := range runner.ran {
		if encode.Encoder == "h264_qsv" && encode.LowPower {
			t.Fatalf("low-power retry started after the survivor: %v", runner.ran)
		}
		if slices.Contains([]string{"hevc_qsv", "av1_qsv"}, encode.Encoder) {
			t.Fatalf("QSV follow-up started after the survivor: %v", runner.ran)
		}
	}
	if result.Supports(HWAccelQSV) {
		t.Fatal("QSV reported working after its probe left a survivor")
	}
}

func TestToneMapProbeStopsAtSurvivingLeg(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint("leg", failAt), func(t *testing.T) {
			calls := 0
			probe := ProbeToneMap(context.Background(), ToneMapOptions{
				Runner: func(context.Context, ToneMapRender) ([]byte, error) {
					calls++
					if calls == failAt {
						return nil, errInjectedSurvivor
					}
					return flatFrame(toneMapProbeFrameBytes, 176), nil
				},
			})
			if probe.Status != ToneMapUnsupported || probe.Failure != ToneMapFailureInconclusive {
				t.Fatalf("probe = %s/%s, want unsupported/inconclusive", probe.Status, probe.Failure)
			}
			if calls != failAt {
				t.Fatalf("runner called %d times, want %d: a leg started after the survivor", calls, failAt)
			}
		})
	}
}

func TestDeviceDecodeProbeStopsAtSurvivingSampleEncode(t *testing.T) {
	calls := 0
	probe := ProbeDeviceDecode(context.Background(), DeviceDecodeOptions{
		ToneMapOptions: ToneMapOptions{
			Backend:       HWAccelQSV,
			GOOS:          "linux",
			DevicePresent: alwaysPresent,
		},
		Run: func(context.Context, []string) ([]byte, error) {
			calls++
			return nil, errInjectedSurvivor
		},
	})
	if probe.Status != ToneMapUnsupported || probe.Failure != ToneMapFailureInconclusive {
		t.Fatalf("probe = %s/%s, want unsupported/inconclusive", probe.Status, probe.Failure)
	}
	if calls != 1 {
		t.Fatalf("runner called %d times, want 1: a leg started after the survivor", calls)
	}
}

func TestComponentListingStopsAtSurvivor(t *testing.T) {
	var flags []string
	_, err := probeCapabilities(context.Background(), func(_ context.Context, flag string) ([]byte, error) {
		flags = append(flags, flag)
		return nil, errInjectedSurvivor
	})
	if !errors.Is(err, errGroupSurvived) {
		t.Fatalf("probeCapabilities = %v, want the survivor", err)
	}
	if !slices.Equal(flags, []string{"-filters"}) {
		t.Fatalf("listings run = %v, want only -filters", flags)
	}
}
