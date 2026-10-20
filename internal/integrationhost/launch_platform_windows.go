//go:build windows

package integrationhost

// Integration Workers currently use ExtraFiles for the capability token and
// guardian lifetime. os/exec does not implement ExtraFiles on Windows, so
// reject before creating pipes or starting a process. The Job Object owner is
// retained for a future Windows transport.
func validateWorkerLaunch(_ string) error {
	return ErrWorkerPlatformUnsupported
}
