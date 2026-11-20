package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/ffmpeg"
)

// scratchWriterPath gives server-side path users an address for the held
// directory even if an ancestor is renamed or replaced.
func scratchWriterPath(dir *os.File) (string, error) {
	var path string
	switch runtime.GOOS {
	case "darwin":
		var stat unix.Stat_t
		if err := unix.Fstat(int(dir.Fd()), &stat); err != nil {
			return "", err
		}
		path = fmt.Sprintf("/.vol/%d/%d", uint32(stat.Dev), stat.Ino) // #nosec G115 -- volfs names the unsigned 32-bit device identifier.
	case "linux":
		path = fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), dir.Fd())
	default:
		return "", errors.New("playback: scratch writer paths are unavailable on this platform")
	}
	held, err := dir.Stat()
	if err != nil {
		return "", err
	}
	addressed, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("resolve scratch writer directory: %w", err)
	}
	if !os.SameFile(held, addressed) {
		return "", errors.New("playback: scratch writer path changed directory identity")
	}
	return path, nil
}

func (s *Sessions) startPlainRung(ctx context.Context, session *hlsSession, rung Rung, segment int) (*ffmpeg.HLSProcess, error) {
	dir, err := openScratchChild(session.scratch.dir, rung.ID, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	path, err := scratchWriterPath(dir)
	if err != nil {
		return nil, err
	}
	rendition := session.rendition(rung).WithHLSOutputDirectory(dir)
	if picture, ok := rendition.Picture(); ok && picture.TimedTextPath != "" {
		picture.TimedTextPath = filepath.Join(path, composedTimedTextFile)
		rendition = rendition.WithComposedPicture(picture)
	}
	if t := session.keyframeTimeline(rung); t != nil && session.durationSeconds > 0 {
		// Start at the actual keyframe time to match the playlist (CORE-0021).
		return s.transcoder.(keyframeTranscoder).StartHLSKeyframeSegments(ctx, session.src, path, segment, t.segmentStart(segment), rendition)
	}
	if session.durationSeconds > 0 {
		return s.transcoder.StartHLSSegments(ctx, session.src, path, segment, rungSegmentSeconds(rung), rendition)
	}
	return s.transcoder.StartHLS(ctx, session.src, path, rendition)
}
