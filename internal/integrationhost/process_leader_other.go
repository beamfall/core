//go:build darwin || !unix

package integrationhost

// observeLeaderExit is unsupported here: macOS pins the group with a zombie
// child instead, and Windows retires a job object rather than a group, so the
// leader is reaped as soon as it exits.
func observeLeaderExit(int) error { return errLeaderObservationUnsupported }
