// Package racefixture holds a deliberately-racy test that the race detector
// must flag, proving the `-race` tier is live.
//
// The data race is opt-in via BEAMFALL_PROVE_RACE=1 so the normal gate and the
// in-gate `-race -short` subset both stay green. The proof command is
// `make race-prove`, which sets that env var and runs `go test -race` over this
// package; the detector then fails the package (the intended RED). If that
// command ever passes, the race detector is not actually wired into the tier.
package racefixture
