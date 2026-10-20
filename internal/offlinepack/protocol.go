package offlinepack

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
)

const (
	GrantType    = "beamfall.offline-pack-grant+jws"
	ManifestType = "beamfall.offline-pack-manifest+jws"
	StatusType   = "beamfall.offline-pack-status+jws"

	OfflinePackAudience = "beamfall-offline-pack-v1"
	OfflinePlayRight    = "offline-play"

	GrantLifetime = 30 * 24 * time.Hour
	RefreshWindow = 7 * 24 * time.Hour
	IssuanceSkew  = 5 * time.Minute
)

var (
	ErrInvalidEnvelope = errors.New("offline pack: invalid signed envelope")
	ErrArtifactType    = errors.New("offline pack: artifact type mismatch")
	ErrGrantExpired    = errors.New("offline pack: grant expired")
	ErrGrantReplay     = errors.New("offline pack: grant replay or rollback")
	ErrGeneration      = errors.New("offline pack: revocation generation mismatch")
	ErrNeedsAuthSync   = errors.New("offline pack: authenticated time sync required")
)

type RevocationGenerations struct {
	Grant         uint64 `json:"grant"`
	Enrollment    uint64 `json:"enrollment"`
	Profile       uint64 `json:"profile"`
	LibraryPolicy uint64 `json:"library_policy"`
	Backup        uint64 `json:"backup"`
}

type GrantClaims struct {
	V                   int                   `json:"v"`
	Iss                 string                `json:"iss"`
	Aud                 string                `json:"aud"`
	JTI                 string                `json:"jti"`
	Seq                 uint64                `json:"seq"`
	PackID              string                `json:"pack_id"`
	ProfileID           string                `json:"profile_id"`
	LibraryIDs          []string              `json:"library_ids"`
	DeviceEnrollmentID  string                `json:"device_enrollment_id"`
	DeviceKeyThumbprint string                `json:"device_key_thumbprint"`
	ManifestRevision    uint64                `json:"manifest_revision"`
	ManifestSHA256      string                `json:"manifest_sha256"`
	Rights              []string              `json:"rights"`
	Iat                 int64                 `json:"iat"`
	Nbf                 int64                 `json:"nbf"`
	Exp                 int64                 `json:"exp"`
	Rev                 RevocationGenerations `json:"rev"`
}

type ManifestItem struct {
	MediaID         string `json:"media_id"`
	SelectedVersion string `json:"selected_version"`
	Size            uint64 `json:"size"`
	SHA256          string `json:"sha256"`
}

type ManifestWrap struct {
	Suite          string `json:"suite"`
	DeviceKID      string `json:"device_kid"`
	Envelope       string `json:"envelope"`
	EnvelopeSHA256 string `json:"envelope_sha256"`
}

// ManifestSHA256 is the digest of the stable ManifestBinding projection. The
// projection deliberately excludes Envelope: AAD_wrap contains this digest, so
// hashing the envelope itself would be circular.
type ManifestClaims struct {
	V                   int            `json:"v"`
	Iss                 string         `json:"iss"`
	Aud                 string         `json:"aud"`
	PackID              string         `json:"pack_id"`
	ProfileID           string         `json:"profile_id"`
	LibraryIDs          []string       `json:"library_ids"`
	DeviceEnrollmentID  string         `json:"device_enrollment_id"`
	DeviceKeyThumbprint string         `json:"device_key_thumbprint"`
	ManifestRevision    uint64         `json:"manifest_revision"`
	ManifestSHA256      string         `json:"manifest_sha256"`
	GrantScopeSHA256    string         `json:"grant_scope_sha256"`
	PackExpiresAt       int64          `json:"pack_expires_at"`
	Items               []ManifestItem `json:"items"`
	Wrap                ManifestWrap   `json:"wrap"`
}

type ManifestBinding struct {
	V                   int            `json:"v"`
	Iss                 string         `json:"iss"`
	Aud                 string         `json:"aud"`
	PackID              string         `json:"pack_id"`
	ProfileID           string         `json:"profile_id"`
	LibraryIDs          []string       `json:"library_ids"`
	DeviceEnrollmentID  string         `json:"device_enrollment_id"`
	DeviceKeyThumbprint string         `json:"device_key_thumbprint"`
	ManifestRevision    uint64         `json:"manifest_revision"`
	PackExpiresAt       int64          `json:"pack_expires_at"`
	Items               []ManifestItem `json:"items"`
	WrapSuite           string         `json:"wrap_suite"`
}

type StatusClaims struct {
	V                  int                   `json:"v"`
	Iss                string                `json:"iss"`
	Aud                string                `json:"aud"`
	PackID             string                `json:"pack_id"`
	DeviceEnrollmentID string                `json:"device_enrollment_id"`
	ManifestRevision   uint64                `json:"manifest_revision"`
	ServerTime         int64                 `json:"server_time"`
	State              string                `json:"state"`
	Rev                RevocationGenerations `json:"rev"`
	Destroy            bool                  `json:"destroy"`
}

type GrantExpectations struct {
	Issuer              string
	PackID              string
	ProfileID           string
	LibraryIDs          []string
	DeviceEnrollmentID  string
	DeviceKeyThumbprint string
	ManifestRevision    uint64
	ManifestSHA256      string
	Rev                 RevocationGenerations
	TrustedNow          time.Time
	Previous            ReplayState
}

type ReplayState struct {
	HighestSeq uint64
	TokenHash  string
	Rev        RevocationGenerations
}

type ValidationDecision struct {
	Destroy bool
	Refresh bool
}

// AuthenticatedGrant is an opaque proof that one Authority authenticated an
// Offline Pack grant. Its zero value and proofs from another Authority fail
// closed when the live bindings are checked.
type AuthenticatedGrant struct {
	authority *Authority
	claims    GrantClaims
	tokenHash string
}

// Grant returns the authenticated payload for the pack subject lookup that
// must precede the live storage and Reveal checks.
func (g AuthenticatedGrant) Grant() GrantClaims { return cloneGrantClaims(g.claims) }

func cloneGrantClaims(claims GrantClaims) GrantClaims {
	claims.LibraryIDs = append([]string(nil), claims.LibraryIDs...)
	claims.Rights = append([]string(nil), claims.Rights...)
	return claims
}

func VerifyGrant(token string, keys map[string]ed25519.PublicKey, expected GrantExpectations) (GrantClaims, ReplayState, ValidationDecision, error) {
	var claims GrantClaims
	if _, err := verifyCompact(token, GrantType, keys, &claims, grantClaimKeys); err != nil {
		return GrantClaims{}, expected.Previous, ValidationDecision{}, err
	}
	return verifyAuthenticatedGrant(claims, grantTokenHash(token), expected)
}

func verifyAuthenticatedGrant(claims GrantClaims, tokenHash string, expected GrantExpectations) (GrantClaims, ReplayState, ValidationDecision, error) {
	if err := validateGrantShape(claims); err != nil {
		return GrantClaims{}, expected.Previous, ValidationDecision{}, err
	}
	decision := ValidationDecision{}
	if err := validateGrantBindings(claims, expected); err != nil {
		if errors.Is(err, ErrGeneration) || errors.Is(err, ErrGrantExpired) {
			decision.Destroy = true
		}
		return GrantClaims{}, expected.Previous, decision, err
	}
	if claims.Seq < expected.Previous.HighestSeq {
		return GrantClaims{}, expected.Previous, decision, ErrGrantReplay
	}
	if claims.Seq == expected.Previous.HighestSeq && expected.Previous.HighestSeq != 0 && tokenHash != expected.Previous.TokenHash {
		return GrantClaims{}, expected.Previous, decision, ErrGrantReplay
	}
	next := ReplayState{HighestSeq: claims.Seq, TokenHash: tokenHash, Rev: claims.Rev}
	decision.Refresh = time.Unix(claims.Exp, 0).Sub(expected.TrustedNow) <= RefreshWindow
	return claims, next, decision, nil
}

func grantTokenHash(token string) string {
	tokenSum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(tokenSum[:])
}

func VerifyManifest(token string, keys map[string]ed25519.PublicKey, grant GrantClaims) (ManifestClaims, error) {
	var claims ManifestClaims
	if _, err := verifyCompact(token, ManifestType, keys, &claims, manifestClaimKeys); err != nil {
		return ManifestClaims{}, err
	}
	if err := ValidateManifestClaims(claims); err != nil {
		return ManifestClaims{}, err
	}
	if claims.Iss != grant.Iss || claims.Aud != grant.Aud || claims.PackID != grant.PackID ||
		claims.ProfileID != grant.ProfileID || claims.DeviceEnrollmentID != grant.DeviceEnrollmentID ||
		claims.DeviceKeyThumbprint != grant.DeviceKeyThumbprint || claims.ManifestRevision != grant.ManifestRevision ||
		claims.ManifestSHA256 != grant.ManifestSHA256 || !slices.Equal(claims.LibraryIDs, grant.LibraryIDs) {
		return ManifestClaims{}, errors.New("offline pack: manifest/grant scope mismatch")
	}
	if claims.GrantScopeSHA256 != GrantScopeSHA256(grant) {
		return ManifestClaims{}, errors.New("offline pack: manifest grant-scope digest mismatch")
	}
	return claims, nil
}

func VerifyStatus(token string, keys map[string]ed25519.PublicKey, grant GrantClaims) (StatusClaims, ValidationDecision, error) {
	var claims StatusClaims
	if _, err := verifyCompact(token, StatusType, keys, &claims, statusClaimKeys); err != nil {
		return StatusClaims{}, ValidationDecision{}, err
	}
	if err := validateStatusShape(claims); err != nil {
		return StatusClaims{}, ValidationDecision{}, err
	}
	if claims.Iss != grant.Iss || claims.Aud != grant.Aud || claims.PackID != grant.PackID ||
		claims.DeviceEnrollmentID != grant.DeviceEnrollmentID || claims.ManifestRevision != grant.ManifestRevision {
		return StatusClaims{}, ValidationDecision{}, errors.New("offline pack: status scope mismatch")
	}
	destroy := claims.Destroy || claims.State == "revoked" || claims.State == "expired" || claims.Rev != grant.Rev
	if claims.Rev != grant.Rev {
		return claims, ValidationDecision{Destroy: true}, ErrGeneration
	}
	return claims, ValidationDecision{Destroy: destroy}, nil
}

func ManifestSHA256(binding ManifestBinding) (string, error) {
	if err := validateManifestBinding(binding); err != nil {
		return "", err
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func GrantScopeSHA256(claims GrantClaims) string {
	scope := struct {
		V                   int      `json:"v"`
		Iss                 string   `json:"iss"`
		Aud                 string   `json:"aud"`
		PackID              string   `json:"pack_id"`
		ProfileID           string   `json:"profile_id"`
		LibraryIDs          []string `json:"library_ids"`
		DeviceEnrollmentID  string   `json:"device_enrollment_id"`
		DeviceKeyThumbprint string   `json:"device_key_thumbprint"`
		ManifestRevision    uint64   `json:"manifest_revision"`
		ManifestSHA256      string   `json:"manifest_sha256"`
		Rights              []string `json:"rights"`
	}{
		claims.V, claims.Iss, claims.Aud, claims.PackID, claims.ProfileID,
		claims.LibraryIDs, claims.DeviceEnrollmentID, claims.DeviceKeyThumbprint,
		claims.ManifestRevision, claims.ManifestSHA256, claims.Rights,
	}
	payload, _ := json.Marshal(scope)
	sum := sha256.Sum256(payload)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func NewJTI() (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func validateGrantShape(c GrantClaims) error {
	if c.V != 1 || c.Aud != OfflinePackAudience || c.Iss == "" || c.PackID == "" || c.ProfileID == "" ||
		c.DeviceEnrollmentID == "" || c.DeviceKeyThumbprint == "" || c.ManifestRevision == 0 ||
		!validDigest(c.ManifestSHA256) || c.Seq == 0 {
		return ErrInvalidEnvelope
	}
	jti, err := base64.RawURLEncoding.DecodeString(c.JTI)
	if err != nil || len(jti) != 16 || rawURL(jti) != c.JTI {
		return ErrInvalidEnvelope
	}
	if !sortedUniqueNonEmpty(c.LibraryIDs) || !slices.Equal(c.Rights, []string{OfflinePlayRight}) {
		return ErrInvalidEnvelope
	}
	if c.Exp <= c.Iat || time.Duration(c.Exp-c.Iat)*time.Second > GrantLifetime {
		return ErrInvalidEnvelope
	}
	return nil
}

func validateGrantBindings(c GrantClaims, e GrantExpectations) error {
	now := e.TrustedNow.UTC()
	if now.IsZero() {
		return ErrNeedsAuthSync
	}
	if now.Add(IssuanceSkew).Before(time.Unix(c.Nbf, 0)) || time.Unix(c.Iat, 0).After(now.Add(IssuanceSkew)) {
		return errors.New("offline pack: issuance time outside allowed skew")
	}
	if !time.Unix(c.Exp, 0).After(now) {
		return ErrGrantExpired
	}
	if c.Iss != e.Issuer || c.PackID != e.PackID || c.ProfileID != e.ProfileID ||
		c.DeviceEnrollmentID != e.DeviceEnrollmentID || c.DeviceKeyThumbprint != e.DeviceKeyThumbprint ||
		c.ManifestRevision != e.ManifestRevision || c.ManifestSHA256 != e.ManifestSHA256 ||
		!slices.Equal(c.LibraryIDs, e.LibraryIDs) {
		return errors.New("offline pack: grant scope mismatch")
	}
	if c.Rev != e.Rev {
		return ErrGeneration
	}
	return nil
}

func ValidateManifestClaims(c ManifestClaims) error {
	if c.V != 1 || c.Aud != OfflinePackAudience || c.Iss == "" || c.PackID == "" || c.ProfileID == "" ||
		c.DeviceEnrollmentID == "" || c.DeviceKeyThumbprint == "" || c.ManifestRevision == 0 ||
		!validDigest(c.ManifestSHA256) || !validDigest(c.GrantScopeSHA256) ||
		c.PackExpiresAt <= 0 || !sortedUniqueNonEmpty(c.LibraryIDs) ||
		c.Wrap.Suite != WrapSuite || c.Wrap.DeviceKID != c.DeviceKeyThumbprint ||
		!validDigest(c.Wrap.EnvelopeSHA256) || len(c.Items) == 0 || len(c.Items) > MaxManifestItems {
		return ErrInvalidEnvelope
	}
	envelope, err := DecodeWrappedEnvelope(c.Wrap.Envelope)
	if err != nil {
		return ErrInvalidEnvelope
	}
	envelopeDigest, err := WrappedEnvelopeSHA256(envelope)
	if err != nil || envelopeDigest != c.Wrap.EnvelopeSHA256 {
		return ErrInvalidEnvelope
	}
	seen := make(map[string]struct{}, len(c.Items))
	for _, item := range c.Items {
		if item.MediaID == "" || item.Size > MaxItemSize || !validDigest(item.SHA256) {
			return ErrInvalidEnvelope
		}
		key := item.MediaID + "\x00" + item.SelectedVersion
		if _, exists := seen[key]; exists {
			return ErrInvalidEnvelope
		}
		seen[key] = struct{}{}
	}
	binding := ManifestBinding{
		V: c.V, Iss: c.Iss, Aud: c.Aud, PackID: c.PackID, ProfileID: c.ProfileID,
		LibraryIDs: c.LibraryIDs, DeviceEnrollmentID: c.DeviceEnrollmentID,
		DeviceKeyThumbprint: c.DeviceKeyThumbprint, ManifestRevision: c.ManifestRevision,
		PackExpiresAt: c.PackExpiresAt, Items: c.Items, WrapSuite: c.Wrap.Suite,
	}
	digest, err := ManifestSHA256(binding)
	if err != nil || digest != c.ManifestSHA256 {
		return ErrInvalidEnvelope
	}
	return nil
}

func validateManifestBinding(c ManifestBinding) error {
	if c.V != 1 || c.Iss == "" || c.Aud != OfflinePackAudience || c.PackID == "" || c.ProfileID == "" ||
		!sortedUniqueNonEmpty(c.LibraryIDs) || c.DeviceEnrollmentID == "" || c.DeviceKeyThumbprint == "" ||
		c.ManifestRevision == 0 || c.PackExpiresAt <= 0 || len(c.Items) == 0 ||
		len(c.Items) > MaxManifestItems || c.WrapSuite != WrapSuite {
		return ErrInvalidEnvelope
	}
	for _, item := range c.Items {
		if item.MediaID == "" || item.Size > MaxItemSize || !validDigest(item.SHA256) {
			return ErrInvalidEnvelope
		}
	}
	return nil
}

func validateStatusShape(c StatusClaims) error {
	if c.V != 1 || c.Iss == "" || c.Aud != OfflinePackAudience || c.PackID == "" ||
		c.DeviceEnrollmentID == "" || c.ManifestRevision == 0 || c.ServerTime <= 0 {
		return ErrInvalidEnvelope
	}
	switch c.State {
	case "queued", "downloading", "paused", "failed", "expired", "revoked", "ready":
		return nil
	default:
		return ErrInvalidEnvelope
	}
}

var (
	headerKeys        = []string{"alg", "bfgv", "crit", "kid", "typ"}
	grantClaimKeys    = []string{"aud", "device_enrollment_id", "device_key_thumbprint", "exp", "iat", "iss", "jti", "library_ids", "manifest_revision", "manifest_sha256", "nbf", "pack_id", "profile_id", "rev", "rights", "seq", "v"}
	manifestClaimKeys = []string{"aud", "device_enrollment_id", "device_key_thumbprint", "grant_scope_sha256", "iss", "items", "library_ids", "manifest_revision", "manifest_sha256", "pack_expires_at", "pack_id", "profile_id", "v", "wrap"}
	statusClaimKeys   = []string{"aud", "destroy", "device_enrollment_id", "iss", "manifest_revision", "pack_id", "rev", "server_time", "state", "v"}
)

func verifyCompact(token, typ string, keys map[string]ed25519.PublicKey, dst any, claimKeys []string) (protectedHeader, error) {
	return verifyCompactWith(token, typ, keys, dst, claimKeys, ed25519.Verify)
}

func verifyCompactWith(token, typ string, keys map[string]ed25519.PublicKey, dst any, claimKeys []string, verify func(ed25519.PublicKey, []byte, []byte) bool) (protectedHeader, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	headerJSON, err := decodeCanonicalRawURL(parts[0])
	if err != nil {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	payload, err := decodeCanonicalRawURL(parts[1])
	if err != nil {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	signature, err := decodeCanonicalRawURL(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	if err := validateJSONObject(headerJSON, headerKeys); err != nil {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	var header protectedHeader
	if err := strictUnmarshal(headerJSON, &header); err != nil {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	if header.Alg != "EdDSA" || header.Typ != typ || header.BFGV != 1 ||
		!slices.Equal(header.Crit, []string{"bfgv"}) || header.Kid == "" {
		if header.Typ != typ {
			return protectedHeader{}, ErrArtifactType
		}
		return protectedHeader{}, ErrInvalidEnvelope
	}
	key, ok := keys[header.Kid]
	if !ok || len(key) != ed25519.PublicKeySize {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	if verify == nil || !verify(key, []byte(parts[0]+"."+parts[1]), signature) {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	if err := validateJSONObject(payload, claimKeys); err != nil {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	if err := strictUnmarshal(payload, dst); err != nil {
		return protectedHeader{}, ErrInvalidEnvelope
	}
	return header, nil
}

func validateJSONObject(data []byte, expected []string) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, expected) {
		return ErrInvalidEnvelope
	}
	return nil
}

func strictUnmarshal(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrInvalidEnvelope
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidEnvelope
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidEnvelope
			}
			if _, exists := seen[key]; exists {
				return ErrInvalidEnvelope
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalidEnvelope
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalidEnvelope
		}
	default:
		return ErrInvalidEnvelope
	}
	return nil
}

func rawURL(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func decodeCanonicalRawURL(value string) ([]byte, error) {
	if value == "" || strings.Contains(value, "=") {
		return nil, ErrInvalidEnvelope
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || rawURL(data) != value {
		return nil, ErrInvalidEnvelope
	}
	return data, nil
}

func validDigest(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && rawURL(raw) == value
}

func sortedUniqueNonEmpty(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for i, value := range values {
		if value == "" {
			return false
		}
		if i > 0 && values[i-1] >= value {
			return false
		}
	}
	return true
}
