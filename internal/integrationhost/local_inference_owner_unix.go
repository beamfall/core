//go:build unix

package integrationhost

import (
	"errors"
	"os"
	"syscall"
)

func rootOwnedByCurrentUser(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, errors.New("owner metadata unavailable")
	}
	return int64(stat.Uid) == int64(os.Geteuid()), nil
}
