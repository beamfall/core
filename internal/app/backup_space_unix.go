//go:build darwin || linux

package app

import "golang.org/x/sys/unix"

// freeBytesOnFS reports bytes available to an unprivileged writer on dir's filesystem.
func freeBytesOnFS(dir string) (uint64, bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false, err
	}
	//nolint:gosec // G115: Bsize is a positive filesystem block size; the product is a byte count.
	return st.Bavail * uint64(st.Bsize), true, nil
}
