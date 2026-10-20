package ffmpeg

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
)

// TestBuildShimArgs pins the shim argv grammar: subcommand, --ro/--rw pairs in
// order, the "--" terminator, then the binary and its args verbatim.
func TestBuildShimArgs(t *testing.T) {
	got := buildShimArgs(
		[]string{"/media/in"},
		[]string{"/var/out", "/tmp/seg"},
		"/usr/bin/ffmpeg",
		[]string{"-i", "/media/in/movie.mkv", "--", "/var/out/x.jpg"},
	)
	want := []string{
		shimSubcommand,
		"--ro", "/media/in",
		"--rw", "/var/out",
		"--rw", "/tmp/seg",
		"--",
		"/usr/bin/ffmpeg",
		"-i", "/media/in/movie.mkv", "--", "/var/out/x.jpg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildShimArgs:\n got %q\nwant %q", got, want)
	}
}

// TestHardenedCommandWrapsWithShim proves the spawn seam re-execs the shim with
// the confinement paths and rlimits-by-construction: when shimPath resolves, the
// built command runs the shim (not ffmpeg directly), and the ffmpeg binary +
// args appear after the "--" terminator. Asserting on the constructed argv is
// how criterion (a) — "the seam applies the configured rlimits/sandbox to the
// child" — is verified on darwin, where landlock cannot run: the child is the
// shim, and the shim is what applies the limits.
func TestHardenedCommandWrapsWithShim(t *testing.T) {
	r := New()
	r.SetHardeningShim("/opt/beamfall")
	cmd := r.hardenedCommand(context.Background(),
		"/usr/bin/ffmpeg",
		[]string{"/media/in"},
		[]string{"/var/out"},
		[]string{"-i", "/media/in/m.mkv", "--", "/var/out/o.jpg"},
	)
	if cmd.Path != "/opt/beamfall" {
		t.Fatalf("expected shim binary /opt/beamfall, got %q", cmd.Path)
	}
	want := []string{
		"/opt/beamfall", shimSubcommand,
		"--ro", "/media/in",
		"--rw", "/var/out",
		"--",
		"/usr/bin/ffmpeg",
		"-i", "/media/in/m.mkv", "--", "/var/out/o.jpg",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("hardenedCommand args:\n got %q\nwant %q", cmd.Args, want)
	}
}

// TestHardenedCommandFallbackWhenNoShim proves the un-hardened fallback: a
// Runner with no shim configured (the default) runs ffmpeg directly — never a
// failure, exactly the pre-hardening behavior, and exactly what every non-server
// caller (including test binaries) gets.
func TestHardenedCommandFallbackWhenNoShim(t *testing.T) {
	cmd := New().hardenedCommand(context.Background(),
		"/usr/bin/ffmpeg", []string{"/media/in"}, nil,
		[]string{"-i", "/media/in/m.mkv"},
	)
	if cmd.Path != "/usr/bin/ffmpeg" {
		t.Fatalf("expected direct ffmpeg exec, got %q", cmd.Path)
	}
	want := []string{"/usr/bin/ffmpeg", "-i", "/media/in/m.mkv"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("fallback args:\n got %q\nwant %q", cmd.Args, want)
	}
}

func TestGenerationCommandAppliesLinuxNiceness(t *testing.T) {
	if nicePriority(10) == 0 {
		t.Skip("generation niceness is a no-op on this platform")
	}
	nicePath, err := exec.LookPath("nice")
	if err != nil {
		t.Skip("nice command not available")
	}
	r := New()
	r.SetGenerationNiceness(10)
	cmd := r.generationCommand(context.Background(),
		dirConfinement("/media/in/m.mkv", "/var/out/hls"),
		"/usr/bin/ffmpeg",
		"-i", "/media/in/m.mkv", "--", "/var/out/hls/index.m3u8",
	)
	if cmd.Path != nicePath {
		t.Fatalf("expected nice wrapper %q, got %q", nicePath, cmd.Path)
	}
	want := []string{nicePath, "-n", "10", "/usr/bin/ffmpeg", "-i", "/media/in/m.mkv", "--", "/var/out/hls/index.m3u8"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("generationCommand args:\n got %q\nwant %q", cmd.Args, want)
	}
}

// TestDefaultRlimitsAreSane guards the rlimit ceilings against an accidental
// zeroing (a zero ceiling would make a real transcode unconstrained-to-broken)
// and pins the documented defaults so a change is deliberate.
func TestDefaultRlimitsAreSane(t *testing.T) {
	lim := DefaultRlimits()
	if lim.CPUSeconds == 0 || lim.ASBytes == 0 || lim.FSizeBytes == 0 || lim.NoFile == 0 {
		t.Fatalf("a zero rlimit ceiling would break or unbound ffmpeg: %+v", lim)
	}
	if lim.CPUSeconds != 6*60*60 {
		t.Errorf("RLIMIT_CPU default changed: got %d", lim.CPUSeconds)
	}
	if lim.ASBytes != 8<<30 {
		t.Errorf("RLIMIT_AS default changed: got %d", lim.ASBytes)
	}
	if lim.FSizeBytes != 32<<30 {
		t.Errorf("RLIMIT_FSIZE default changed: got %d", lim.FSizeBytes)
	}
	if lim.NoFile != 512 {
		t.Errorf("RLIMIT_NOFILE default changed: got %d", lim.NoFile)
	}
}

// TestConfinementComputesAllowedPaths covers criterion (b): the sandbox config
// derives the correct allowed-path set from input + output, granting input dirs
// read-only and output dirs read-write, de-duplicated and cleaned, with the
// input dir dropped from read-only when an output already covers it.
func TestConfinementComputesAllowedPaths(t *testing.T) {
	tests := []struct {
		name   string
		got    confinement
		wantRO []string
		wantRW []string
	}{
		{
			name:   "read-only job (probe) grants only the input dir",
			got:    readConfinement("/media/movies/film.mkv"),
			wantRO: []string{"/media/movies"},
			wantRW: nil,
		},
		{
			name:   "transform job grants input dir RO and output dir RW",
			got:    transformConfinement("/media/movies/film.mkv", "/var/cache/assets/poster.jpg.tmp"),
			wantRO: []string{"/media/movies"},
			wantRW: []string{"/var/cache/assets"},
		},
		{
			name:   "multiple outputs in distinct dirs all become RW",
			got:    transformConfinement("/in/a.mkv", "/out1/x.jpg", "/out2/y.jpg"),
			wantRO: []string{"/in"},
			wantRW: []string{"/out1", "/out2"},
		},
		{
			name:   "output sharing the input dir drops the redundant RO grant",
			got:    transformConfinement("/work/in.mkv", "/work/out.jpg"),
			wantRO: []string{},
			wantRW: []string{"/work"},
		},
		{
			name:   "HLS dir job grants the output directory directly",
			got:    dirConfinement("/media/movies/film.mkv", "/var/transcode/session123"),
			wantRO: []string{"/media/movies"},
			wantRW: []string{"/var/transcode/session123"},
		},
		{
			name:   "uncleaned paths are normalized",
			got:    transformConfinement("/media/./movies/../movies/film.mkv", "/var//cache/out.jpg"),
			wantRO: []string{"/media/movies"},
			wantRW: []string{"/var/cache"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !equalStringSet(tc.got.ReadOnly, tc.wantRO) {
				t.Errorf("ReadOnly: got %q want %q", tc.got.ReadOnly, tc.wantRO)
			}
			if !equalStringSet(tc.got.ReadWrite, tc.wantRW) {
				t.Errorf("ReadWrite: got %q want %q", tc.got.ReadWrite, tc.wantRW)
			}
		})
	}
}

// equalStringSet compares two slices as sets (order-insensitive), treating nil
// and empty as equal — the confinement helpers return cleaned sets whose order
// is an implementation detail.
func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}
