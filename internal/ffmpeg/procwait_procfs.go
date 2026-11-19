//go:build aix || solaris

package ffmpeg

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"time"
)

// On AIX and Solaris (including illumos), Go has no non-reaping wait API.
// A zombie's psinfo stays readable until Wait and reports zero live threads.
func waitForProcessExit(pid int) error {
	path := fmt.Sprintf("/proc/%d/psinfo", pid)
	file, err := os.Open(path) // #nosec G304 -- the PID is from exec.Cmd.Start
	if err != nil {
		return err
	}
	defer file.Close()

	// The psinfo header places pr_nlwp after one 32-bit flags field on
	// Solaris and two on AIX.
	offset := int64(4)
	if runtime.GOOS == "aix" {
		offset = 8
	}
	var threads [4]byte
	for {
		if _, err := file.ReadAt(threads[:], offset); err != nil {
			return err
		}
		if binary.NativeEndian.Uint32(threads[:]) == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}
