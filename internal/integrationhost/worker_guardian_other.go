//go:build !unix

package integrationhost

import "os"

func newWorkerGuardianLifetime() (read, write *os.File, shimArgs []string, err error) {
	return nil, nil, nil, nil
}
