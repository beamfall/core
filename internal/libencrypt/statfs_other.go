//go:build !darwin && !linux

package libencrypt

// freeBytesOnFS has no supported free-space query on this platform, so the
// re-wrap space check is skipped (ok==false) rather than failing the build or
// the operation.
func freeBytesOnFS(string) (uint64, bool, error) { return 0, false, nil }
