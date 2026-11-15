package ffmpeg

import (
	"context"
	"reflect"
	"testing"
)

// withHostOS builds encode argv as if on goos for the rest of the test.
func withHostOS(t *testing.T, goos string) {
	t.Helper()
	prev := hostOS
	hostOS = goos
	t.Cleanup(func() { hostOS = prev })
}

func TestProbeHardwareAccelWindowsTriesNVENCQSVAndAMF(t *testing.T) {
	stub := &stubHWAccelRunner{results: map[string]error{
		"h264_amf": nil,
		"hevc_amf": nil,
	}}
	got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
		Runner:     stub.runStrict,
		GOOS:       "windows",
		DRIPresent: func() bool { return false },
	})
	if want := []HWAccel{HWAccelAMF}; !reflect.DeepEqual(got.Working, want) {
		t.Fatalf("Working = %v, want %v", got.Working, want)
	}
	if !got.SupportsHEVC(HWAccelAMF) {
		t.Fatal("SupportsHEVC(amf) = false, want true")
	}
	// No /dev/dri gate on Windows; never VA-API or VideoToolbox. QSV's failed
	// H.264 probe is retried in low-power mode, NVENC's and AMF's are not.
	stub.assertAttempts(t, "h264_nvenc", "h264_qsv", "h264_qsv:low_power",
		"h264_amf", "hevc_amf", "av1_amf")
	var order []HWAccel
	for _, r := range got.Results {
		order = append(order, r.Accelerator)
	}
	if want := []HWAccel{HWAccelQSV, HWAccelVAAPI, HWAccelNVENC, HWAccelVideoToolbox, HWAccelAMF}; !reflect.DeepEqual(order, want) {
		t.Fatalf("Windows report order = %v, want %v", order, want)
	}
}

func TestProbeHardwareAccelReportsAMFOnlyOnWindows(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		got := ProbeHardwareAccel(context.Background(), HWAccelOptions{
			Runner:     (&stubHWAccelRunner{}).runStrict,
			GOOS:       goos,
			DRIPresent: func() bool { return true },
		})
		for _, r := range got.Results {
			if r.Accelerator == HWAccelAMF {
				t.Fatalf("%s report has an amf row", goos)
			}
		}
		if len(got.Results) != len(allHWAccels) {
			t.Fatalf("%s report has %d rows, want %d", goos, len(got.Results), len(allHWAccels))
		}
	}
	if a, ok := HWAccelFromString("amf"); !ok || a != HWAccelAMF {
		t.Fatalf("HWAccelFromString(amf) = %q, %v", a, ok)
	}
}

func TestWindowsQSVInitsOnD3D11(t *testing.T) {
	withHostOS(t, "windows")
	want := []string{"-init_hw_device", "qsv=bfqsv:hw_any,child_device_type=d3d11va"}
	if got := qsvInitArgs(); !equalArgs(got, want) {
		t.Fatalf("qsvInitArgs = %v, want %v", got, want)
	}
	if got := (Rendition{Height: 720, hwAccel: HWAccelQSV}).inputArgs(); !equalArgs(got, want) {
		t.Fatalf("QSV inputArgs = %v, want %v", got, want)
	}
	probe := hwAccelProbeArgs(HWAccelEncode{Encoder: "h264_qsv"})
	if !equalArgs(probe[:2], want) {
		t.Fatalf("QSV probe argv = %v, want it to start with %v", probe, want)
	}
}

func TestRenditionAMFArgs(t *testing.T) {
	r := Rendition{Height: 720, VideoCodec: "h264", VideoKbps: 6000, hwAccel: HWAccelAMF}
	if got, want := r.videoArgs(), []string{"-c:v", "h264_amf"}; !equalArgs(got, want) {
		t.Fatalf("AMF videoArgs = %v, want %v", got, want)
	}
	want := []string{"-vf", "scale=-2:min(ih\\,720),format=nv12",
		"-quality", "speed", "-rc", "vbr_peak", "-b:v", "6000k", "-maxrate", "6000k", "-bufsize", "12000k"}
	if got := r.args(); !equalArgs(got, want) {
		t.Fatalf("AMF args = %v, want %v", got, want)
	}
	if got := r.inputArgs(); len(got) != 0 {
		t.Fatalf("AMF inputArgs = %v, want none", got)
	}
	r.hwLowPower = true
	if got := r.lowPowerArgs(); len(got) != 0 {
		t.Fatalf("AMF lowPowerArgs = %v, want none", got)
	}
	for codec, encoder := range map[string]string{"hevc": "hevc_amf", "av1": "av1_amf"} {
		if got := hwAccelVideoEncoder(HWAccelAMF, codec); got != encoder {
			t.Fatalf("hwAccelVideoEncoder(amf, %s) = %q, want %q", codec, got, encoder)
		}
	}
}
