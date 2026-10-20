//go:build darwin

package store

// devIdentity reinterprets the platform dev_t (an unsigned 32-bit id packed
// into int32 on darwin) as a stable unsigned key; wraparound is intended.
func devIdentity(d int32) uint64 { // #nosec G115 -- deliberate bit reinterpretation of dev_t.
	return uint64(uint32(d)) // #nosec G115 -- see above.
}
