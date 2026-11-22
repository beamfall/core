//go:build !darwin && !linux

package store

import (
	"os"
	"syscall"
)

func renameMediaUploadNoReplace(_ int, _, _ string) error {
	return syscall.ENOTSUP
}

func platformDataDirFilesystem(dataDir string) (dataDirFilesystem, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return dataDirFilesystem{}, err
	}
	return dataDirFilesystem{Name: "unknown"}, nil
}
