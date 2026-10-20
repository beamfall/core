package store

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// SubtitleTrackFingerprint hashes the extracted subtitle track bytes. Paths,
// filenames, stream indexes, and container metadata never enter the key, so a
// rename or remux preserves it while a cue text or timing edit replaces it.
func SubtitleTrackFingerprint(track io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, track); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
