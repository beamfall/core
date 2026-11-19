package playback

import (
	"bufio"
	"bytes"
	"encoding/binary"
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
	session.publicationMu.Lock()
	run.outputErr = errors.Join(err, output.Close())
	run.output = nil
	session.publicationMu.Unlock()
}

func (s *Sessions) publishRun(key SessionKey, session *hlsSession, run *encoderRun, output *ffmpeg.PacedHLSOutput, base int) error {
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
			err := output.Receive(n, playlist, run.processDone, s.segmentFileLimit)
			if errors.Is(err, io.EOF) {
				continue // Wait finished without another fragment
			}
			if err != nil {
				return err
			}
			received = true
			continue
		}
		select {
		case <-processDone:
		case <-run.outputCanceled:
			return ErrSessionEvicted
		case <-changed: // already at the demand limit
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
	playlistPath := filepath.Join(session.dir, rung, playlist)
	if !received {
		if finished {
			return nil, false, finishPacedOutput(output, playlistPath, base, n, linear, run.preserveEvent)
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
	hasMedia, err := fragmentHasMedia(f, info.Size())
	if err != nil {
		return nil, false, fmt.Errorf("validate live encoder fragment %s: %w", name, err)
	}
	var index []byte
	if linear {
		index, _, _, err = pacedPlaylist(output, playlist, base, n)
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
		run.preserveEvent = linear && fileReady(playlistPath)
	}
	// For a truncated VOD input, consume a well-formed empty tail without
	// making it playable. A waiting request observes the reaper's outcome.
	if (linear || hasMedia) && !fileReady(filepath.Join(session.dir, rung, name)) {
		if err := output.Publish(name, f); err != nil {
			return nil, false, err
		}
	}
	if linear {
		if err := publishPacedEventPlaylist(output, playlistPath, index, n, false, run.preserveEvent); err != nil {
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
	if linear || hasMedia {
		run.lastMedia = n
	}
	session.frontier = n
	session.updatePacingLocked()
	return changed, true, nil
}

// fragmentHasMedia validates the full top-level MP4 box sequence before
// reporting whether any mdat box has a payload. Only headers are read.
func fragmentHasMedia(r io.ReaderAt, size int64) (bool, error) {
	if size <= 0 {
		return false, io.ErrUnexpectedEOF
	}
	var hasMedia bool
	var header [16]byte
	for off := int64(0); off < size; {
		if size-off < 8 {
			return false, io.ErrUnexpectedEOF
		}
		if _, err := r.ReadAt(header[:8], off); err != nil {
			return false, err
		}
		boxSize := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		switch boxSize {
		case 0: // box extends to end of file
			boxSize = size - off
		case 1: // 64-bit largesize follows the type
			if size-off < 16 {
				return false, io.ErrUnexpectedEOF
			}
			if _, err := r.ReadAt(header[8:16], off+8); err != nil {
				return false, err
			}
			large := binary.BigEndian.Uint64(header[8:16])
			// #nosec G115 -- size-off is positive (loop guard), so it fits in uint64.
			if large > uint64(size-off) {
				return false, io.ErrUnexpectedEOF
			}
			boxSize = int64(large) // #nosec G115 -- large <= size-off, an int64, so it fits.
			headerSize = 16
		}
		if boxSize < headerSize || boxSize > size-off {
			return false, errors.New("invalid MP4 box size")
		}
		if string(header[4:8]) == "mdat" && boxSize > headerSize {
			hasMedia = true
		}
		off += boxSize
	}
	return hasMedia, nil
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

func finishPacedOutput(output *ffmpeg.PacedHLSOutput, playlistPath string, base, next int, linear, preserveEvent bool) error {
	through := -1
	if linear {
		through = next - 1
	}
	data, last, ended, err := pacedPlaylist(output, filepath.Base(playlistPath), base, through)
	if err != nil {
		return err
	}
	if !ended || last != next-1 || last < base {
		return errors.New("live encoder playlist did not finish with all output published")
	}
	if linear {
		return publishPacedEventPlaylist(output, playlistPath, data, last, true, preserveEvent)
	}
	return nil
}

// An EVENT playlist only grows. Recovery can regenerate a fragment already
// advertised by an earlier run, then eventually extend the same playlist.
func publishPacedEventPlaylist(output *ffmpeg.PacedHLSOutput, path string, index []byte, last int, ended, preserve bool) error {
	end := []byte("#EXT-X-ENDLIST\n")
	var previous []byte
	if preserve {
		var err error
		previous, err = os.ReadFile(path) // #nosec G304 G703 -- fixed playlist name in the private session cache
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if len(previous) > 0 {
		body := bytes.TrimSuffix(previous, end)
		lines := bytes.TrimSuffix(body, []byte("\n"))
		uri := lines[bytes.LastIndexByte(lines, '\n')+1:]
		prior, ok := encoderSegmentNumber(string(uri))
		if !ok {
			return errors.New("invalid published event playlist")
		}
		if prior > last && ended {
			return errors.New("live encoder ended before the published event playlist")
		}
		if prior > last || len(body) != len(previous) || (prior == last && !ended) {
			return nil
		}
		if prior < last {
			marker := fmt.Appendf(nil, "\nseg%05d.m4s\n", prior)
			offset := bytes.Index(index, marker)
			if offset < 0 {
				return errors.New("live encoder playlist does not extend the published prefix")
			}
			previous = append(previous, index[offset+len(marker):]...)
		}
		index = previous
	}
	if ended {
		index = append(index, end...)
	}
	return output.Publish(filepath.Base(path), bytes.NewReader(index))
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
