package offlinepack

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type protectedHeader struct {
	Alg  string   `json:"alg"`
	Typ  string   `json:"typ"`
	BFGV int      `json:"bfgv"`
	Crit []string `json:"crit"`
	Kid  string   `json:"kid"`
}

type Authority struct {
	issuer       string
	kid          string
	privateKey   ed25519.PrivateKey
	publicKey    ed25519.PublicKey
	now          func() time.Time
	issueEnabled bool
	verify       func(ed25519.PublicKey, []byte, []byte) bool
}

func NewAuthority(issuer, kid string, privateKey ed25519.PrivateKey, now func() time.Time) (*Authority, error) {
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(kid) == "" {
		return nil, errors.New("offline pack: issuer and kid are required")
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("offline pack: Ed25519 private key is required")
	}
	if now == nil {
		now = time.Now
	}
	pub := privateKey.Public().(ed25519.PublicKey)
	return &Authority{
		issuer:       issuer,
		kid:          kid,
		privateKey:   append(ed25519.PrivateKey(nil), privateKey...),
		publicKey:    append(ed25519.PublicKey(nil), pub...),
		now:          now,
		issueEnabled: true,
		verify:       ed25519.Verify,
	}, nil
}

// AuthenticateGrant parses and authenticates a grant exactly once, returning
// an opaque proof for subject lookup followed by live binding checks.
func (a *Authority) AuthenticateGrant(token string) (AuthenticatedGrant, error) {
	if a == nil {
		return AuthenticatedGrant{}, ErrInvalidEnvelope
	}
	var claims GrantClaims
	if _, err := verifyCompactWith(token, GrantType, a.PublicKeys(), &claims, grantClaimKeys, a.verify); err != nil {
		return AuthenticatedGrant{}, err
	}
	if err := validateGrantShape(claims); err != nil {
		return AuthenticatedGrant{}, err
	}
	return AuthenticatedGrant{authority: a, claims: cloneGrantClaims(claims), tokenHash: grantTokenHash(token)}, nil
}

// VerifyAuthenticatedGrant applies live scope, expiry, generation, and replay
// checks without repeating signature validation.
func (a *Authority) VerifyAuthenticatedGrant(grant AuthenticatedGrant, expected GrantExpectations) (GrantClaims, ReplayState, ValidationDecision, error) {
	if a == nil || grant.authority != a || grant.tokenHash == "" {
		return GrantClaims{}, expected.Previous, ValidationDecision{}, ErrInvalidEnvelope
	}
	return verifyAuthenticatedGrant(grant.claims, grant.tokenHash, expected)
}

func LoadOrCreateAuthority(dir, issuer string, now func() time.Time) (*Authority, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "ed25519.seed")
	seed, err := readOrCreateSeed(path)
	if err != nil {
		return nil, err
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(publicKey)
	kid := "offline-pack-" + base64.RawURLEncoding.EncodeToString(sum[:])
	return NewAuthority(issuer, kid, privateKey, now)
}

func readOrCreateSeed(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: server-owned key-seed path under the configured data dir, not user input
	if err == nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode().Perm() != 0o600 {
			return nil, fmt.Errorf("%s must be mode 0600", path)
		}
		seed, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("offline pack: invalid signing seed")
		}
		return seed, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, err
	}
	encoded := []byte(base64.RawStdEncoding.EncodeToString(seed) + "\n")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, err
	}
	return seed, nil
}

type TrustedTimeInput struct {
	WallClock        time.Time
	ServerTimeAtSync time.Time
	MonotonicElapsed time.Duration
	HighWater        time.Time
	SameGeneration   bool
}

func TrustedNow(in TrustedTimeInput) (time.Time, error) {
	wall := in.WallClock.UTC()
	highWater := in.HighWater.UTC()
	if in.SameGeneration {
		if in.ServerTimeAtSync.IsZero() || in.MonotonicElapsed < 0 {
			return time.Time{}, ErrNeedsAuthSync
		}
		candidate := in.ServerTimeAtSync.UTC().Add(in.MonotonicElapsed)
		return maxTime(wall, candidate, highWater), nil
	}
	if wall.Add(IssuanceSkew).Before(highWater) {
		return time.Time{}, ErrNeedsAuthSync
	}
	return maxTime(wall, highWater), nil
}

func maxTime(values ...time.Time) time.Time {
	var result time.Time
	for _, value := range values {
		if value.After(result) {
			result = value
		}
	}
	return result
}

func (a *Authority) SetIssueEnabled(enabled bool) { a.issueEnabled = enabled }
func (a *Authority) Issuer() string               { return a.issuer }
func (a *Authority) KeyID() string                { return a.kid }

func (a *Authority) PublicKeys() map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{a.kid: append(ed25519.PublicKey(nil), a.publicKey...)}
}

func (a *Authority) PublicKeyDocument() map[string]string {
	return map[string]string{
		a.kid: base64.RawURLEncoding.EncodeToString(a.publicKey),
	}
}

func (a *Authority) sign(typ string, claims any) (string, error) {
	if !a.issueEnabled {
		return "", errors.New("offline pack: this server role cannot issue offline authority")
	}
	headerJSON, err := json.Marshal(protectedHeader{
		Alg: "EdDSA", Typ: typ, BFGV: 1, Crit: []string{"bfgv"}, Kid: a.kid,
	})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := rawURL(headerJSON) + "." + rawURL(payload)
	signature := ed25519.Sign(a.privateKey, []byte(signingInput))
	return signingInput + "." + rawURL(signature), nil
}

func (a *Authority) SignGrant(claims GrantClaims) (string, error) {
	if err := validateGrantShape(claims); err != nil {
		return "", err
	}
	return a.sign(GrantType, claims)
}

func (a *Authority) SignManifest(claims ManifestClaims) (string, error) {
	if err := ValidateManifestClaims(claims); err != nil {
		return "", err
	}
	return a.sign(ManifestType, claims)
}

func (a *Authority) SignStatus(claims StatusClaims) (string, error) {
	if err := validateStatusShape(claims); err != nil {
		return "", err
	}
	return a.sign(StatusType, claims)
}
