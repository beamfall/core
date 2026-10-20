//go:build !linux && !darwin && !windows

package integrationhost

func validateWorkerLaunch(_ string) error {
	return ErrWorkerPlatformUnsupported
}
