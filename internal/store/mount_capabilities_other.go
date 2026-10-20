//go:build !darwin && !linux

package store

import (
	"os"
	"path/filepath"
)

func mountProbeLocation(path string, _ os.FileInfo) (string, string, error) {
	volume := filepath.VolumeName(path)
	return "volume:" + volume, volume + string(filepath.Separator), nil
}

func mountProbeFileIdentity(os.FileInfo) (string, bool) {
	return "", false
}

func mountProbeUnsupported(error) bool {
	return false
}
