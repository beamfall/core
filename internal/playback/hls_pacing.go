package playback

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

var errPacedOutputDiscarded = errors.New("live encoder output discarded after demand moved")

// publishFileSegments receives one fragment at a time in muxer order. Each
// next pipe stays closed to readers until the previous fragment is published.
func (s *Sessions) publishFileSegments(key SessionKey, session *hlsSession, run *encoderRun, output *ffmpeg.PacedHLSOutput, base int) {
	defer close(run.outputDone)
	err := s.publishRun(key, session, run, output, base)
	if err != nil && !errors.Is(err, ErrSessionEvicted) && !errors.Is(err, errPacedOutputDiscarded) {
		run.proc.Kill()
	}
	run.outputErr = errors.Join(err, output.Close())
}

func (s *Sessions) publishRun(key SessionKey, session *hlsSession, run *encoderRun, output *ffmpeg.PacedHLSOutput, base int) error {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	linear := session.durationSeconds == 0
	playlist := "live.m3u8"
	if linear {
		playlist = "index.m3u8"
	}
	processDone := run.processDone
	received := false
	for n := base; ; {
		select {
		case <-processDone:
			processDone = nil
			if run.processErr != nil || run.proc.StopRequested() {
				return nil // the reaper owns the exit error
			}
		default:
		}
		finished := processDone == nil
		changed, published, err := s.publishNext(key, session, run, output, playlist, base, n, linear, received, finished)
		if err != nil {
			return err
		}
		if published {
			n++
			received = false
			continue
		}
		if finished && !received {
			return nil
		}
		if changed == nil && !received {
			err := output.Receive(n, run.processDone)
			if errors.Is(err, io.EOF) {
				continue // Wait finished without another fragment
			}
			if err != nil {
				return err
			}
			received = true
			continue
		}
		var poll <-chan time.Time
		if changed == nil {
			poll = ticker.C // the muxer still owes us the playlist update
		}
		select {
		case <-processDone:
		case <-run.outputCanceled:
			return ErrSessionEvicted
		case <-changed: // already at the demand limit
		case <-poll:
		}
	}
}

// publishNext serializes the demand check and the cache update with requests
// and encoder replacement for this session. The manager lock never covers
// opening or copying output.
func (s *Sessions) publishNext(key SessionKey, session *hlsSession, run *encoderRun, output *ffmpeg.PacedHLSOutput, playlist string, base, n int, linear, received, finished bool) (<-chan struct{}, bool, error) {
	session.publicationMu.Lock()
	defer session.publicationMu.Unlock()
	s.mu.Lock()
	if !s.liveLocked(key, session) || session.run != run {
		s.mu.Unlock()
		return nil, false, ErrSessionEvicted
	}
	changed := run.pacingChanged
	allowed := n <= session.publicationLimitLocked()
	rung := session.currentRung
	s.mu.Unlock()
	if !received {
		if finished {
			return nil, false, finishPacedOutput(output, playlist, base, n, linear)
		}
		if !allowed {
			return changed, false, nil
		}
		return nil, false, nil
	}
	if !allowed {
		if finished && !linear {
			return nil, false, errPacedOutputDiscarded
		}
		return changed, false, nil
	}

	name := fmt.Sprintf("seg%05d.m4s", n)
	f, err := output.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.Size() == 0 {
		return nil, false, fmt.Errorf("live encoder wrote an empty file %s", name)
	}
	var index []byte
	if linear {
		index, _, _, err = pacedPlaylist(output, playlist, base, n)
		if !finished && (errors.Is(err, os.ErrNotExist) || (err == nil && index == nil)) {
			return nil, false, nil // playlist rename follows the segment rename
		}
		if err != nil {
			return nil, false, err
		}
		if index == nil {
			return nil, false, errors.New("live encoder left a fragment outside its playlist")
		}
	}
	s.mu.Lock()
	if !s.liveLocked(key, session) || session.run != run {
		s.mu.Unlock()
		return nil, false, ErrSessionEvicted
	}
	run.completed = n
	session.updatePacingLocked()
	s.mu.Unlock()
	// The open descriptor keeps the fragment available during copying.
	// Remove its staging name before the cache rename exposes it.
	if err := os.Remove(filepath.Join(output.Dir, name)); err != nil { // #nosec G703 -- generated fragment name in the server-created encoder directory
		return nil, false, err
	}
	if n == base {
		if err := publishPacedInit(output); err != nil {
			return nil, false, err
		}
	}
	// FFmpeg can emit a well-formed, media-free fragment after a truncated
	// input. Consume that placeholder without making it playable. A request
	// waiting for it observes the reaper's outcome.
	hasMedia := linear || !readerLacksMedia(f, info.Size())
	if hasMedia && !fileReady(filepath.Join(session.dir, rung, name)) {
		if err := output.Publish(name, f); err != nil {
			return nil, false, err
		}
	}
	if linear {
		if err := output.Publish(playlist, bytes.NewReader(index)); err != nil {
			return nil, false, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.liveLocked(key, session) || session.run != run {
		return nil, false, ErrSessionEvicted
	}
	run.produced = n
	run.producedAt = time.Now()
	if hasMedia {
		run.lastMedia = n
	}
	session.frontier = n
	session.updatePacingLocked()
	return changed, true, nil
}

func publishPacedInit(output *ffmpeg.PacedHLSOutput) error {
	f, err := output.Open(initSegmentName)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return errors.New("live encoder wrote an empty initialization file")
	}
	return output.Publish(initSegmentName, f)
}

func finishPacedOutput(output *ffmpeg.PacedHLSOutput, playlist string, base, next int, linear bool) error {
	through := -1
	if linear {
		through = next - 1
	}
	data, last, ended, err := pacedPlaylist(output, playlist, base, through)
	if err != nil {
		return err
	}
	if !ended || last != next-1 || last < base {
		return errors.New("live encoder playlist did not finish with all output published")
	}
	if linear {
		return output.Publish(playlist, bytes.NewReader(append(data, "#EXT-X-ENDLIST\n"...)))
	}
	return nil
}

// pacedPlaylist checks segment order and optionally snapshots an event
// playlist up to a published fragment. ENDLIST is added separately after
// the process has exited and the final playlist matches the published run.
func pacedPlaylist(output *ffmpeg.PacedHLSOutput, name string, base, through int) ([]byte, int, bool, error) {
	f, err := output.Open(name)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || scanner.Text() != "#EXTM3U" {
		return nil, 0, false, errors.New("invalid live encoder playlist")
	}
	last, ended := base-1, false
	var snapshot bytes.Buffer
	if through >= base {
		snapshot.WriteString("#EXTM3U\n")
	}
	var result []byte
	for scanner.Scan() {
		line := scanner.Text()
		if line == "#EXT-X-ENDLIST" {
			ended = true
			continue
		}
		if line != "" && line[0] != '#' {
			n, ok := encoderSegmentNumber(line)
			if !ok || ended || n != last+1 {
				return nil, 0, false, errors.New("invalid live encoder playlist segment order")
			}
			last = n
		}
		if through >= base && result == nil {
			snapshot.WriteString(line)
			snapshot.WriteByte('\n')
			if last == through {
				result = snapshot.Bytes()
			}
		}
	}
	return result, last, ended, scanner.Err()
}
