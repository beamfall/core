package pairing

import "testing"

func FuzzPairingInputNormalization(f *testing.F) {
	f.Add("pair-1234", "203.0.113.50:6000")
	f.Add("  pa ir-1234  ", "[2001:db8::1]:443")
	f.Add("", "")

	f.Fuzz(func(t *testing.T, code, remoteAddr string) {
		normalized := normalizeCode(code)
		_ = codeAttemptKey(normalized)
		_ = remoteAttemptKey(remoteAddr)
		_, _ = parseRole(code)
	})
}
