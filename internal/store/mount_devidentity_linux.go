//go:build linux

package store

// devIdentity returns the platform dev_t as the stable unsigned key; on Linux
// dev_t is already an unsigned 64-bit id.
func devIdentity(d uint64) uint64 { return d }
