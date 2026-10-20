package push

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/url"

	"golang.org/x/crypto/hkdf"
)

// Credential parsing + the Web Push (RFC 8291 aes128gcm) encryption and RS256
// signing the FCM/Web-Push backends use. The raw credential material is read
// from the 0600 secure store by the composition root and parsed here; this file
// never logs a key and the parsers return only the in-memory key, never the raw
// bytes.

// ParseAPNsKey parses an Apple .p8 APNs auth key (PEM-wrapped PKCS#8 EC private
// key) into the ECDSA key signES256 signs with. The .p8 bytes come from the
// secure store; a malformed key is a clean error at configure time, never a
// boot crash.
func ParseAPNsKey(p8 []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(p8)
	if block == nil {
		return nil, fmt.Errorf("apns key: not PEM-encoded")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns key: %w", err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("apns key: not an ECDSA key")
	}
	return ec, nil
}

// FCMServiceAccount is the subset of a Firebase service-account JSON the FCM v1
// backend needs. The JSON lives in the secure store; ParseFCMServiceAccount
// returns the parsed RSA signing key plus the project/email identifiers.
type FCMServiceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"` // PEM PKCS#8 RSA
	TokenURI    string `json:"token_uri"`
}

// ParseFCMServiceAccount parses a service-account JSON into an FCMConfig-ready
// triple. The PEM private key is parsed into an *rsa.PrivateKey; the raw JSON
// and PEM are never retained or logged.
func ParseFCMServiceAccount(data []byte) (projectID, clientEmail string, key *rsa.PrivateKey, tokenURI string, err error) {
	var sa FCMServiceAccount
	if e := json.Unmarshal(data, &sa); e != nil {
		return "", "", nil, "", fmt.Errorf("fcm service account: %w", e)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return "", "", nil, "", fmt.Errorf("fcm service account: missing project_id/client_email/private_key")
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return "", "", nil, "", fmt.Errorf("fcm service account: private_key not PEM-encoded")
	}
	parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return "", "", nil, "", fmt.Errorf("fcm service account: %w", e)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return "", "", nil, "", fmt.Errorf("fcm service account: private_key is not RSA")
	}
	return sa.ProjectID, sa.ClientEmail, rsaKey, sa.TokenURI, nil
}

// signRS256 builds and signs a compact RS256 (RSA / SHA-256) JWT — the
// service-account OAuth2 assertion FCM exchanges for a bearer token.
func signRS256(key *rsa.PrivateKey, header map[string]string, claims map[string]any) (string, error) {
	if key == nil {
		return "", fmt.Errorf("rs256: nil signing key")
	}
	signingInput, err := jwtSigningInput(header, claims)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + b64url.EncodeToString(sig), nil
}

// originOf returns the scheme://host origin of a push endpoint URL — the VAPID
// JWT audience (RFC 8292).
func originOf(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("webpush: bad endpoint: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("webpush: endpoint missing scheme/host")
	}
	return u.Scheme + "://" + u.Host, nil
}

// encryptAES128GCM encrypts plaintext to a Web Push subscription's keys using
// the RFC 8291 aes128gcm content-encoding (the single scheme modern browsers
// accept). p256dhB64 and authB64 are the subscription's base64url public key and
// auth secret. The output is the full aes128gcm body: a header (salt ‖ rs ‖
// idlen ‖ server public key) followed by one encrypted record.
func encryptAES128GCM(p256dhB64, authB64 string, plaintext []byte, randReader io.Reader) ([]byte, error) {
	uaPubBytes, err := decodeB64(p256dhB64)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad p256dh: %w", err)
	}
	authSecret, err := decodeB64(authB64)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad auth: %w", err)
	}
	p256 := ecdh.P256()
	uaPub, err := p256.NewPublicKey(uaPubBytes)
	if err != nil {
		return nil, fmt.Errorf("webpush: bad subscription public key: %w", err)
	}
	serverPriv, err := p256.GenerateKey(randReader)
	if err != nil {
		return nil, err
	}
	serverPub := serverPriv.PublicKey().Bytes() // 65-byte uncompressed point

	shared, err := serverPriv.ECDH(uaPub)
	if err != nil {
		return nil, err
	}

	salt := make([]byte, 16)
	if _, err := io.ReadFull(randReader, salt); err != nil {
		return nil, err
	}

	// RFC 8291 §3.4: PRK_key = HKDF(auth_secret, ecdh_secret,
	// "WebPush: info\x00" ‖ ua_public ‖ server_public, 32).
	keyInfo := append([]byte("WebPush: info\x00"), uaPubBytes...)
	keyInfo = append(keyInfo, serverPub...)
	ikm := hkdfExtractExpand(authSecret, shared, keyInfo, 32)

	// CEK and nonce derive from the aes128gcm salt (RFC 8188).
	cek := hkdfExtractExpand(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdfExtractExpand(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Single record: plaintext ‖ 0x02 padding delimiter (last record).
	record := append(append([]byte{}, plaintext...), 0x02)
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	// aes128gcm header (RFC 8188 §2.1): salt(16) ‖ rs(4, big-endian) ‖
	// idlen(1) ‖ keyid(server public key, 65) ‖ ciphertext.
	rs := uint32(4096)
	header := make([]byte, 0, 16+4+1+len(serverPub)+len(ciphertext))
	header = append(header, salt...)
	rsBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(rsBytes, rs)
	header = append(header, rsBytes...)
	header = append(header, byte(len(serverPub)))
	header = append(header, serverPub...)
	header = append(header, ciphertext...)
	return header, nil
}

// hkdfExtractExpand runs HKDF-SHA256 (extract then expand) over ikm with the
// given salt and info, returning length bytes.
func hkdfExtractExpand(salt, ikm, info []byte, length int) []byte {
	r := hkdf.New(sha256.New, ikm, salt, info)
	out := make([]byte, length)
	_, _ = io.ReadFull(r, out)
	return out
}

// decodeB64 decodes base64url (with or without padding) — Web Push subscription
// keys are conventionally unpadded base64url, but be lenient.
func decodeB64(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
