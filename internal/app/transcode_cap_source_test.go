package app

import (
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestTranscodeCapSourceFollowsResolvePrecedence covers CORE-2317: the startup
// log names the source of the cap that is in force, in the order
// graph.ResolveTranscodeCap applies.
func TestTranscodeCapSourceFollowsResolvePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored store.TranscodeConfig
		envCap int
		want   string
	}{
		{"env wins over a stored cap", store.TranscodeConfig{MaxConcurrent: 4}, 2, "env/flag (BEAMFALL_TRANSCODE_CAP)"},
		{"stored cap wins over automatic", store.TranscodeConfig{MaxConcurrent: 4}, 0, "console"},
		{"automatic when nothing is set", store.TranscodeConfig{}, 0, "automatic"},
	} {
		if got := transcodeCapSource(tc.stored, tc.envCap); got != tc.want {
			t.Errorf("%s: source = %q, want %q", tc.name, got, tc.want)
		}
	}
}
