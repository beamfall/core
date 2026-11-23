//go:build !unix

package cdgrender

import "time"

func processCPU() (time.Duration, bool) { return 0, false }
