package libencrypt

import (
	"bytes"
	"testing"
)

func TestRecoveryKeyFromBytesCopiesAndRendersCanonicalKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x5a}, dekLen)
	key, err := RecoveryKeyFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 0
	if key.Bytes()[0] != 0x5a {
		t.Fatal("RecoveryKeyFromBytes retained the caller's mutable buffer")
	}
	parsed, err := ParseRecoveryKey(key.Display)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsed.Bytes(), key.Bytes()) {
		t.Fatal("derived Recovery Key display did not round-trip")
	}
}
