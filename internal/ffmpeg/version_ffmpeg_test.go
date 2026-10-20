//go:build ffmpeg

package ffmpeg

import "testing"

func TestPinnedRuntimeVersion(t *testing.T) {
	got, err := New().Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != PinnedVersion() {
		t.Fatalf("Runner.Version() = %q, want pin %q", got, PinnedVersion())
	}
}
