//go:build !darwin && !linux && !windows

package ffmpeg

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
)

var fallbackProcessStart = sync.OnceValue(func() string {
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(token[:])
})

func ownerProcessStart(pid int) (string, error) {
	if pid != os.Getpid() {
		return "", errors.New("process start identity unavailable")
	}
	identity := fallbackProcessStart()
	if identity == "" {
		return "", errors.New("generating process start identity")
	}
	return identity, nil
}
