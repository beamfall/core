//go:build linux

package store

import (
	"os"

	"golang.org/x/sys/unix"
)

func platformDataDirFilesystem(dataDir string) (dataDirFilesystem, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return dataDirFilesystem{}, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(dataDir, &stat); err != nil {
		return dataDirFilesystem{}, err
	}
	return classifyDataDirFilesystem("", int64(stat.Type)), nil
}
