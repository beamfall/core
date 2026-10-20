package libencrypt

import (
	"crypto/sha256"
	"strings"
)

// Recovery Key encoding (ADR-0045 §2.4 — "License-Key-shaped", user-held, never
// escrowed). A Recovery Key is 256 bits of entropy rendered as a human-
// transcribable, grouped, checksummed string — the same SHAPE as a License Key
// (ADR-0031: a signed file/QR a human can read off and type back in), but a
// distinct credential: a License Key proves what you bought; a Recovery Key
// recovers your data. This package does NOT mint License Keys (those are the
// proprietary cloud's, invariant #7) — it mints the data-recovery credential.
//
// Encoding: Crockford base32 (no I/L/O/U — unambiguous to read aloud and type),
// the 256-bit key split into 5-char groups joined by hyphens, with a trailing
// checksum group derived from the key. ParseRecoveryKey is lenient on input
// (case-insensitive, strips hyphens/spaces, maps look-alikes) so an operator who
// transcribed it slightly differently still recovers — but rejects a wrong key
// via the checksum and, ultimately, via the AEAD unwrap.

// crockfordAlphabet is Crockford base32: digits then A-Z minus I, L, O, U.
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// recoveryKeyGroupLen is the dash-separated group size for display.
const recoveryKeyGroupLen = 5

// checksumGroupLen is the number of base32 chars in the trailing checksum group.
// 4 chars of base32 = 20 bits of integrity check — enough to catch a fat-finger,
// not a security boundary (the AEAD tag is the real boundary).
const checksumGroupLen = 4

// RecoveryKey is the user-held credential in both forms: the rendered Display
// string (what the operator saves) and the raw 32-byte secret (what wraps the
// DEK). Display is shown exactly once at provisioning; the raw bytes never
// persist server-side in recoverable form.
type RecoveryKey struct {
	// Display is the grouped, checksummed, human-readable string (e.g.
	// "ABCDE-FGHJK-...-CKSM"). This is what the acknowledgement gate shows once.
	Display string
	raw     []byte // the 32 decoded entropy bytes; never logged, never persisted
}

// Bytes returns the raw 32-byte secret. Callers use it transiently to wrap the
// DEK and must not persist or log it.
func (rk RecoveryKey) Bytes() []byte { return rk.raw }

// MintRecoveryKey is the exported form of generateRecoveryKey: it mints a fresh
// 256-bit Recovery Key and returns both the display form (for the operator to save)
// and the raw bytes (for wrapping a DEK or BackupKey). Used by packages outside
// libencrypt such as the backup package.
func MintRecoveryKey() (RecoveryKey, error) { return generateRecoveryKey() }

// RecoveryKeyFromBytes renders caller-derived 256-bit key material in the
// canonical Recovery Key shape. The input is copied so callers can zero their
// derivation buffer immediately after this call.
func RecoveryKeyFromBytes(raw []byte) (RecoveryKey, error) {
	if len(raw) != dekLen {
		return RecoveryKey{}, ErrUnwrap
	}
	key := append([]byte(nil), raw...)
	return RecoveryKey{Display: encodeRecoveryKey(key), raw: key}, nil
}

// generateRecoveryKey mints a fresh 256-bit Recovery Key and renders its display
// form. The raw bytes wrap the DEK; the display string is surfaced once.
func generateRecoveryKey() (RecoveryKey, error) {
	raw, err := randomBytes(dekLen)
	if err != nil {
		return RecoveryKey{}, err
	}
	return RecoveryKey{Display: encodeRecoveryKey(raw), raw: raw}, nil
}

// encodeRecoveryKey renders 32 entropy bytes as the grouped, checksummed display
// string.
func encodeRecoveryKey(raw []byte) string {
	body := base32Encode(raw)
	check := recoveryChecksum(raw)
	all := body + check
	return groupHyphens(all)
}

// ParseRecoveryKey is the lenient decoder: it accepts the display string in any
// case, ignores hyphens/spaces, and maps Crockford look-alikes (I/L→1, O→0). It
// verifies the trailing checksum group; a checksum mismatch is ErrUnwrap (the
// operator typed it wrong). On success it returns the raw 32-byte secret and the
// canonical display string. It never logs the input.
func ParseRecoveryKey(s string) (RecoveryKey, error) {
	cleaned := normalizeRecoveryInput(s)
	bodyLen := base32EncodedLen(dekLen)
	if len(cleaned) != bodyLen+checksumGroupLen {
		return RecoveryKey{}, ErrUnwrap
	}
	body := cleaned[:bodyLen]
	gotCheck := cleaned[bodyLen:]
	raw, err := base32Decode(body)
	if err != nil {
		return RecoveryKey{}, ErrUnwrap
	}
	if !constantTimeEqual([]byte(body), []byte(base32Encode(raw))) {
		return RecoveryKey{}, ErrUnwrap
	}
	wantCheck := recoveryChecksum(raw)
	// Constant-time compare so a near-miss checksum does not leak via timing.
	if !constantTimeEqual([]byte(gotCheck), []byte(wantCheck)) {
		return RecoveryKey{}, ErrUnwrap
	}
	return RecoveryKey{Display: groupHyphens(body + wantCheck), raw: raw}, nil
}

// recoveryChecksum derives the trailing checksum group from the key bytes: the
// first checksumGroupLen base32 chars of SHA-256(key). Integrity only — not a MAC.
func recoveryChecksum(raw []byte) string {
	sum := sha256.Sum256(raw)
	return base32Encode(sum[:])[:checksumGroupLen]
}

// normalizeRecoveryInput uppercases, strips separators, and maps Crockford
// look-alike characters so a hand-transcribed key still parses.
func normalizeRecoveryInput(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch r {
		case '-', ' ', '\t', '\n', '\r':
			continue
		case 'I', 'L':
			b.WriteByte('1')
		case 'O':
			b.WriteByte('0')
		case 'U':
			b.WriteByte('V') // Crockford maps U→V on input as a transcription aid
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// groupHyphens splits a run of base32 chars into recoveryKeyGroupLen-char groups
// joined by hyphens for display.
func groupHyphens(s string) string {
	var parts []string
	for i := 0; i < len(s); i += recoveryKeyGroupLen {
		end := i + recoveryKeyGroupLen
		if end > len(s) {
			end = len(s)
		}
		parts = append(parts, s[i:end])
	}
	return strings.Join(parts, "-")
}

// base32EncodedLen is the number of base32 chars for n bytes (5 bits/char, ceil).
func base32EncodedLen(n int) int {
	return (n*8 + 4) / 5
}

// base32Encode encodes bytes as Crockford base32 with no padding.
func base32Encode(data []byte) string {
	var out strings.Builder
	var buffer uint64
	var bits uint
	for _, b := range data {
		buffer = buffer<<8 | uint64(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out.WriteByte(crockfordAlphabet[(buffer>>bits)&0x1f])
		}
	}
	if bits > 0 {
		out.WriteByte(crockfordAlphabet[(buffer<<(5-bits))&0x1f])
	}
	return out.String()
}

// base32Decode decodes a Crockford base32 string (already normalized) back to
// bytes, truncating to the byte boundary. An unknown character is an error.
func base32Decode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)*5/8)
	var buffer uint64
	var bits uint
	for i := 0; i < len(s); i++ {
		idx := strings.IndexByte(crockfordAlphabet, s[i])
		if idx < 0 {
			return nil, ErrUnwrap
		}
		buffer = buffer<<5 | uint64(idx)
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(buffer>>bits))
		}
	}
	return out, nil
}
