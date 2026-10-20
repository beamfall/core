//go:build !unix

package integrationhost

import "errors"

func rootOwnedByCurrentUser(string) (bool, error) {
	return false, errors.New("owner verification unavailable on this platform")
}
