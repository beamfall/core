//go:build !unix && !windows

package ffmpeg

// Unsupported platforms without a reliable process-liveness probe retain
// residue conservatively; normal success/failure/cancellation cleanup remains
// fully supported there.
func ownerProcessAlive(_ int) bool { return true }
