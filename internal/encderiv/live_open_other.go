//go:build !unix

package encderiv

import "os"

func openLiveFile(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 -- path is a validated name inside the session's own directory
}
