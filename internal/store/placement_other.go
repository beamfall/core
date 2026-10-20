//go:build !darwin && !linux

package store

import "os"

func platformDataDirFilesystem(dataDir string) (dataDirFilesystem, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return dataDirFilesystem{}, err
	}
	return dataDirFilesystem{Name: "unknown"}, nil
}
