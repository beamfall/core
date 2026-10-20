//go:build linux || darwin

package integrationhost

func validateWorkerLaunch(shimPath string) error {
	if shimPath == "" {
		return ErrWorkerShimRequired
	}
	return nil
}
