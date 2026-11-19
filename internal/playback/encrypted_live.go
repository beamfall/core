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
	// Fingerprint is the stable identity and generation of the encrypted source object.
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
func (s *Sessions) startEncryptedRung(ctx context.Context, key SessionKey, session *hlsSession, run *encoderRun, rung Rung, segment int) (*ffmpeg.HLSProcess, *encderiv.LiveRun, error) {
	starter, ok := s.transcoder.(encryptedTranscoder)
	if !ok {
		return nil, nil, ErrEncryptedLiveUnavailable
	}
	live, err := encderiv.StartLiveRun(encderiv.LiveRunConfig{
		Session: session.live,
		Rung:    rung.ID,
		Open:    session.secure.Open,
		Size:    session.secure.Size,
		PublishSegment: func(ctx context.Context, name string, commit func() error) error {
			n, _ := encoderSegmentNumber(name) // validated by the upload endpoint
			return s.withPublicationSlot(ctx, key, session, run, n, commit)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	ep := ffmpeg.EncryptedLiveEndpoint{Source: live.SourceURL(), Output: live.OutputURL(), Secret: live.Secret()}
	proc, err := starter.StartEncryptedHLSSegments(ctx, ep, segment, rungSegmentSeconds(rung), session.rendition(rung))
	if err != nil {
		live.Revoke()
		return nil, nil, err
	}
	return proc, live, nil
}

// watchLiveRun kills run's process once its endpoint fails (a refused request,
// a failed upload, or an expired token), and returns once the process exits.
func watchLiveRun(run *encoderRun) {
	select {
	case <-run.exited:
	case <-run.live.Failed():
		run.proc.Kill()
	}
}

// watchLiveRevocation tears the session down once revoked reports true, and
// returns when the session ends either way.
func (s *Sessions) watchLiveRevocation(key SessionKey, session *hlsSession, done <-chan struct{}, revoked func() bool, interval time.Duration) {
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
			var teardown *sessionTeardown
			if s.liveLocked(key, session) {
				teardown = s.removeLocked(key)
			}
			s.mu.Unlock()
			if teardown != nil {
				s.runTeardowns([]sessionTeardown{*teardown})
			}
			return
		}
	}
}

// segmentLacksMedia is the package's segmentLacksMedia for one of the
// session's segments, decrypting it first on an encrypted session.
func (h *hlsSession) segmentLacksMedia(path string) bool {
	if h.live == nil {
		return segmentLacksMedia(path)
	}
	rel, ok := h.live.Rel(path)
	if !ok {
		return false
	}
	f, err := h.live.Open(rel)
	if err != nil {
		return false
	}
	defer f.Close()
	return readerLacksMedia(f, f.Size())
}

// OpenLiveArtifact opens path, a file FilePathShaped returned for key's
// encrypted session, as decrypted bytes. It fails when the session is gone or
// is not encrypted.
func (s *Sessions) OpenLiveArtifact(key SessionKey, path string) (*encderiv.LiveFile, error) {
	s.mu.Lock()
	session, ok := s.sessions[key]
	var live *encderiv.LiveSession
	if ok {
		live = session.live
	}
	s.mu.Unlock()
	if !ok {
		return nil, ErrSessionEvicted
	}
	if live == nil {
		return nil, errors.New("playback: session is not encrypted")
	}
	rel, ok := live.Rel(path)
	if !ok {
		return nil, errors.New("unknown stream file")
	}
	f, err := live.Open(rel)
	if errors.Is(err, encderiv.ErrLiveSessionClosed) {
		return nil, ErrSessionEvicted
	}
	return f, err
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
