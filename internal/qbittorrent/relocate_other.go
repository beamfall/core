//go:build !unix

package qbittorrent

// isCrossDevice cannot detect EXDEV on non-unix platforms, so a failed
// hardlink is always treated as a real failure rather than a copy trigger.
// Beamfall ships on darwin/linux (the unix build); this stub keeps the package
// buildable elsewhere.
func isCrossDevice(error) bool {
	return false
}
