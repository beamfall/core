package playback

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// scratchWriterPath gives path-based writers (including the FFmpeg child)
// an address for the held directory. Renaming or replacing an ancestor may
// change its ordinary pathname while these addresses keep the same inode.
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
		// Use the server's PID since the child has its own descriptor table.
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
