package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
)

// EncryptedSource is the plaintext source of a live session on an Encrypted
// Library (ADR-0236). The session engine never sees a source path: FFmpeg
// reads Open's bytes through a per-run loopback endpoint and uploads its
// output back through it, and every file the session keeps is encrypted under
// a session key that lives only in memory.
type EncryptedSource struct {
	// LibraryID binds the session's files to their Library.
	LibraryID string
	// Fingerprint is the required, verified content identity of the source opened by Open.
	Fingerprint string
	// Open returns a fresh plaintext reader of the source; Size is its length.
	Open encderiv.Opener
	Size int64
	// Revoked reports that the session must end now, for example because the
	// Library was sealed. The engine polls it while the session lives.
	Revoked func() bool
}

// ErrEncryptedLiveUnavailable refuses a live session an Encrypted Library
// cannot carry: a burned-in subtitle, a composed picture, an unknown duration,
// or a transcoder without the encrypted path (ADR-0236 D4).
var ErrEncryptedLiveUnavailable = errors.New("playback: live transcode unavailable for this encrypted stream")

// encryptedTranscoder starts an encrypted live run (ffmpeg.Runner implements
// it).
type encryptedTranscoder interface {
	StartEncryptedHLSSegments(ctx context.Context, ep ffmpeg.EncryptedLiveEndpoint, startSegment, segmentSeconds int, rendition ffmpeg.Rendition) (*ffmpeg.HLSProcess, error)
}

// liveRevocationPoll is how often a live encrypted session checks its
// Revoked predicate.
const liveRevocationPoll = time.Second

// encryptedSessionRefusal reports whether a session of this shape can run on
// an Encrypted Library.
func encryptedSessionRefusal(secure *EncryptedSource, durationSeconds int, burnSubtitleIndex int, picture *ComposedPicture) error {
	switch {
	case secure.Open == nil || secure.Size < 0 || secure.LibraryID == "":
		return errors.New("playback: encrypted source is incomplete")
	case picture != nil, burnSubtitleIndex >= 0, durationSeconds <= 0:
		return ErrEncryptedLiveUnavailable
	}
	return nil
}

// startEncryptedRung opens a loopback endpoint for one encoder run and starts
// FFmpeg against it. The run's token lives exactly as long as the process: the
// reaper revokes it on exit, and an endpoint failure kills the process.
func (s *Sessions) startEncryptedRung(ctx context.Context, start *encoderStart, rung Rung, segment int) (*ffmpeg.HLSProcess, error) {
	session, run := start.session, start.run
	starter, ok := s.transcoder.(encryptedTranscoder)
	if !ok {
		return nil, ErrEncryptedLiveUnavailable
	}
	live, err := encderiv.StartLiveRun(encderiv.LiveRunConfig{
		Session:  session.live,
		Rung:     rung.ID,
		Open:     session.secure.Open,
		Size:     session.secure.Size,
		Disabled: true,
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	valid := s.liveStartLocked(start)
	if valid {
		run.live = live
	}
	s.mu.Unlock()
	if !valid {
		live.Revoke()
		return nil, ErrSessionEvicted
	}
	go s.watchLiveRun(run)
	live.Enable()
	if _, owned := s.transcoder.(*ffmpeg.Runner); !owned {
		if err := s.permitEncoder(start.ctx, start); err != nil {
			return nil, err
		}
	}
	ep := ffmpeg.EncryptedLiveEndpoint{Source: live.SourceURL(), Output: live.OutputURL(), Secret: live.Secret()}
	return starter.StartEncryptedHLSSegments(ctx, ep, segment, rungSegmentSeconds(rung), session.rendition(rung))
}

func (s *Sessions) watchLiveRun(run *encoderRun) {
	select {
	case <-run.ctx.Done():
	case <-run.live.Failed():
		s.mu.Lock()
		failed := s.acceptEndpointFailureLocked(run)
		proc := run.proc
		s.mu.Unlock()
		if failed {
			if proc != nil {
				proc.Kill()
			} else {
				run.cancel()
			}
		}
	}
}

// watchLiveRevocation tears the session down once revoked reports true, and
// returns when the session ends either way.
func (s *Sessions) watchLiveRevocation(session *hlsSession, done <-chan struct{}, revoked func() bool, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if !revoked() {
				continue
			}
			s.mu.Lock()
			teardown := s.transitionLocked(session, detachSession{}).teardown
			s.mu.Unlock()
			if teardown != nil {
				s.runTeardowns([]*sessionTeardown{teardown})
			}
			return
		}
	}
}

// inspectSegment checks a confined segment, decrypting it for an encrypted session.
func (h *hlsSession) inspectSegment(path string) (empty, readable bool) {
	if h.live != nil {
		f, err := h.openEncryptedArtifact(path)
		if err != nil {
			return false, false
		}
		defer f.Close()
		return inspectMediaBoxes(f, f.Size())
	}
	f, info, err := h.openArtifact(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	return inspectMediaBoxes(f, info.Size())
}

// writePlaylist stores one of the playlists the session serves itself, under
// its slash-separated name relative to the session directory: encrypted on an
// encrypted session, plain otherwise.
func (h *hlsSession) writePlaylist(rel string, data []byte) error {
	if h.live != nil {
		return h.live.WriteFile(rel, data)
	}
	return os.WriteFile(filepath.Join(h.dir, filepath.FromSlash(rel)), data, 0o600)
}

func (s *Sessions) liveRevocationInterval() time.Duration {
	if s.revocationPoll > 0 {
		return s.revocationPoll
	}
	return liveRevocationPoll
}
