// Package sessiontest supplies scratch-owning session managers for tests.
package sessiontest

import (
	"testing"
	"time"

	"github.com/beamfall/core/internal/playback"
)

func New(t testing.TB, transcoder playback.Transcoder, dataDir string, now func() time.Time) *playback.Sessions {
	t.Helper()
	s, err := playback.NewSessions(transcoder, dataDir, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
