//go:build darwin

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
	return classifyDataDirFilesystem(fsTypeName(stat.Fstypename[:]), 0), nil
}

func fsTypeName(raw []byte) string {
	out := make([]byte, 0, len(raw))
	for _, c := range raw {
		if c == 0 {
			break
		}
		out = append(out, c)
	}
	return string(out)
}
