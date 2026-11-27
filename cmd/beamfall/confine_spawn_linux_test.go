//go:build linux

package main

// hostCanConfineMedia reports whether this kernel can carry the mandatory
// media confinement the spawn seam requires on Linux.
func hostCanConfineMedia() bool {
	return requiredLandlockEnforceable(landlockABI())
}

// hostHasFilesystemConfinement reports whether this kernel enforces any
// Landlock filesystem ruleset for ordinary best-effort jobs (ABI >= 1).
func hostHasFilesystemConfinement() bool {
	return landlockABI() >= 1
}
