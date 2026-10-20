//go:build darwin || linux

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func mountProbeLocation(path string, info os.FileInfo) (string, string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", fmt.Errorf("mount probe: filesystem identity unavailable for %s", path)
	}
	device := devIdentity(stat.Dev)
	mount := path
	for {
		parent := filepath.Dir(mount)
		if parent == mount {
			break
		}
		parentInfo, err := os.Stat(parent)
		if err != nil {
			break
		}
		parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
		if !ok || devIdentity(parentStat.Dev) != device {
			break
		}
		mount = parent
	}
	return mountProbeDeviceKey(device), mount, nil
}

func mountProbeFileIdentity(info os.FileInfo) (string, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return "", false
	}
	return fmt.Sprintf("%d:%d", devIdentity(stat.Dev), stat.Ino), true
}

func mountProbeUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) ||
		errors.Is(err, syscall.EXDEV)
}
