//go:build !unix

package playback

import "os"

func openJoinFragment(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 -- path is an encoder segment inside the session's output directory
}
