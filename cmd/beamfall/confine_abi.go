package main

// requiredLandlockMinABI is the lowest landlock ABI that --require-landlock
// accepts. LANDLOCK_ACCESS_FS_TRUNCATE only exists from ABI 3: a ruleset built
// on ABI 1 or 2 leaves truncate(2) outside its handled set, neither granted nor
// denied, so a child that reads decrypted bytes could still empty any file its
// user may write. The mandatory confinement refuses those kernels instead of
// narrowing its promise.
const requiredLandlockMinABI = 3

// requiredLandlockEnforceable reports whether a kernel's landlock ABI can carry
// the mandatory confinement. It is the platform-independent half of the Linux
// decision, kept here so the floor stays assertable on a host without landlock.
func requiredLandlockEnforceable(abi int) bool {
	return abi >= requiredLandlockMinABI
}
