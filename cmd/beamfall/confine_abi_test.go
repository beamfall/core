package main

import "testing"

// TestRequiredLandlockFloorIsABI3 pins the mandatory confinement's floor: ABI 1
// and 2 cannot handle truncate(2), so only ABI 3 and later are enforceable.
func TestRequiredLandlockFloorIsABI3(t *testing.T) {
	for abi, want := range map[int]bool{-1: false, 0: false, 1: false, 2: false, 3: true, 4: true, 5: true, 6: true} {
		if got := requiredLandlockEnforceable(abi); got != want {
			t.Errorf("requiredLandlockEnforceable(%d) = %v, want %v", abi, got, want)
		}
	}
}
