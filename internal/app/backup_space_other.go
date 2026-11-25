//go:build !darwin && !linux

package app

// freeBytesOnFS is unknown off darwin/linux; the precheck is skipped there.
func freeBytesOnFS(string) (uint64, bool, error) { return 0, false, nil }
