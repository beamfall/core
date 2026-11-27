//go:build linux

package main

// hostCanConfineMedia reports whether this kernel can carry the mandatory
// media confinement the spawn seam requires on Linux.
func hostCanConfineMedia() bool {
	return requiredLandlockEnforceable(landlockABI())
}
