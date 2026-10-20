//go:build unix

package integrationhost

import (
	"os"
	"strconv"
)

const workerGuardianFD = CapabilityTokenFD + 1

func newWorkerGuardianLifetime() (read, write *os.File, shimArgs []string, err error) {
	read, write, err = os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	return read, write, []string{"--guardian-fd", strconv.Itoa(workerGuardianFD)}, nil
}
