package remoteaccess

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Carrier token v2 (relay PROTOCOL.md, section "Carrier token v2"). The home
// server mints one single-use token per dial from the operator's shared HMAC
// secret; the secret itself never crosses the wire. Conformance is pinned by
// the relay's published golden vectors, vendored byte-identical under
// testdata/relay_hmac_token_v2_vectors.json.
const (
	relayTokenV2Tag = "v2"
	// relayTokenV2NonceBytes is the size of the fresh random nonce per token.
	relayTokenV2NonceBytes = 16
	// relayTokenV2Lifetime is the signed lifetime (expires_at - issued_at),
	// equal to relayd's MaxHMACTokenTTL cap.
	relayTokenV2Lifetime = 5 * time.Minute
	// relayTokenV2Backdate is subtracted from the current whole second for
	// issued_at so a home-server clock slightly ahead of the relay is still
	// accepted, including by relays that allow no issued_at leeway.
	relayTokenV2Backdate = 30 * time.Second
)

// mintRelayTokenV2 mints a fresh v2 carrier token for hostnames at now, reading
// the nonce from nonceSrc (crypto/rand in production). The returned bytes are
// the token text placed in Hello.Token; they are a credential and must never be
// logged, stored, or re-sent. Errors never contain the secret or the token.
func mintRelayTokenV2(secret []byte, hostnames []string, now time.Time, nonceSrc io.Reader) ([]byte, error) {
	if len(secret) == 0 {
		return nil, errors.New("relay token secret is empty")
	}
	if len(hostnames) == 0 {
		return nil, errors.New("relay token needs at least one hostname")
	}
	nonce := make([]byte, relayTokenV2NonceBytes)
	if _, err := io.ReadFull(nonceSrc, nonce); err != nil {
		return nil, errors.New("relay token nonce unavailable")
	}
	issuedAt := now.Unix() - int64(relayTokenV2Backdate/time.Second)
	expiresAt := issuedAt + int64(relayTokenV2Lifetime/time.Second)
	return relayTokenV2(secret, hostnames, issuedAt, expiresAt, nonce), nil
}

// relayTokenV2 builds the token text from fixed inputs:
// v2.<issued_at>.<expires_at>.<nonce hex>.<mac hex>.
func relayTokenV2(secret []byte, hostnames []string, issuedAt, expiresAt int64, nonce []byte) []byte {
	nonceHex := hex.EncodeToString(nonce)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(relayTokenV2MACInput(hostnames, issuedAt, expiresAt, nonceHex))
	token := make([]byte, 0, 2+1+10+1+10+1+len(nonceHex)+1+sha256.Size*2)
	token = append(token, relayTokenV2Tag...)
	token = append(token, '.')
	token = strconv.AppendInt(token, issuedAt, 10)
	token = append(token, '.')
	token = strconv.AppendInt(token, expiresAt, 10)
	token = append(token, '.')
	token = append(token, nonceHex...)
	token = append(token, '.')
	token = hex.AppendEncode(token, mac.Sum(nil))
	return token
}

// relayTokenV2MACInput is the authenticated byte string:
// "v2" 0x00 hosts 0x00 issued_at 0x00 expires_at 0x00 nonce-hex, where hosts
// is the normalized hostname list sorted by byte order and joined with LF
// (duplicates kept).
func relayTokenV2MACInput(hostnames []string, issuedAt, expiresAt int64, nonceHex string) []byte {
	hosts := make([]string, len(hostnames))
	for i, h := range hostnames {
		hosts[i] = normalizeRelayHostname(h)
	}
	sort.Strings(hosts)
	in := make([]byte, 0, 64+len(nonceHex))
	in = append(in, relayTokenV2Tag...)
	in = append(in, 0)
	in = append(in, strings.Join(hosts, "\n")...)
	in = append(in, 0)
	in = strconv.AppendInt(in, issuedAt, 10)
	in = append(in, 0)
	in = strconv.AppendInt(in, expiresAt, 10)
	in = append(in, 0)
	in = append(in, nonceHex...)
	return in
}

// normalizeRelayHostname applies the spec's hostname normalization: trim
// surrounding white space, lowercase, then remove one trailing dot.
func normalizeRelayHostname(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}
