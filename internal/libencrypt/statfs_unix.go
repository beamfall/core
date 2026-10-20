//go:build darwin || linux

package libencrypt

import "golang.org/x/sys/unix"

// freeBytesOnFS reports the bytes available to an unprivileged writer on the
// filesystem backing dir. Bavail (not Bfree) is used so the re-wrap space check
// respects the reserved-block margin (ENCPRIV-6 explicit space accounting).
// Bsize is uint32 on darwin and int64 on linux; the uint64 cast covers both.
func freeBytesOnFS(dir string) (uint64, bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false, err
	}
	return st.Bavail * uint64(st.Bsize), true, nil
}
