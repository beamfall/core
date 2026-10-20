// Package streamtoken issues and verifies scoped media-byte tokens.
package streamtoken

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

// MaxStreamTokenTTL is the longest lifetime the signer will put on a Stream
// Token. Compatibility-refusal tombstones are retained beyond this bound, so a
// token can never outlive the durable fact that its subject was invalidated.
const MaxStreamTokenTTL = 7 * 24 * time.Hour

// Target identifies a byte-serving purpose.
type Target string

const (
	// TargetMediaDirect authorizes direct Media bytes.
	TargetMediaDirect Target = "media-direct"
	// TargetMediaHLS authorizes HLS transcode playlist and segment bytes.
	TargetMediaHLS Target = "media-hls"
	// TargetMediaAssets authorizes Generated Asset bytes:
	// trickplay manifest and sheets, the preview clip, Marker thumbnails.
	TargetMediaAssets Target = "media-assets"
	// TargetMediaSubtitle authorizes the WebVTT subtitle sidecar:
	// a muxed text subtitle extracted and converted to WebVTT, served as a
	// sidecar track. The sidecar bytes join Reveal exactly like the other media
	// targets — this grant scopes the same (device, Profile, reveal, media,
	// expiry) tuple, so a restricted item's sidecar requires the verifying
	// session to currently hold reveal.
	TargetMediaSubtitle Target = "media-subtitle"
	// TargetMediaSidecar authorizes a sidecar caption track (FEAT-4 / ADR-0005
	// §4): an external .srt/.vtt file discovered next to the media file at scan,
	// served as a WebVTT sidecar. Distinct from TargetMediaSubtitle (a MUXED
	// subtitle stream extracted by index) — a sidecar is its own on-disk file,
	// addressed by its position in the recorded sidecar list, not a stream index.
	// The grant scopes the same (device, Profile, reveal, media, expiry) tuple as
	// the other media targets, so a restricted item's sidecar requires the
	// verifying session to currently hold reveal.
	TargetMediaSidecar Target = "media-sidecar"
	// TargetArtworkCandidate authorizes server-fetched candidate preview bytes.
	TargetArtworkCandidate Target = "artwork-candidate"
	// TargetMediaProxy authorizes the proxy-tee stream of a provisional Media
	//: the server ranges the origin enclosure to the client and
	// tees the bytes to disk. The token fronts the PROXY endpoint — the origin
	// URL is resolved server-side and never reaches the client. The grant scopes
	// the same (device, Profile, reveal, media, expiry) tuple as the other media
	// targets, so a restricted provisional's stream requires the verifying
	// session to currently hold reveal.
	TargetMediaProxy Target = "media-proxy"
)

func validTarget(target Target) bool {
	switch target {
	case TargetMediaDirect, TargetMediaHLS, TargetMediaAssets, TargetMediaSubtitle, TargetMediaSidecar, TargetArtworkCandidate, TargetMediaProxy:
		return true
	default:
		return false
	}
}

// Grant is the signed Stream Token payload.
type Grant struct {
	Target  Target        `json:"target"`
	MediaID store.MediaID `json:"mediaID,omitempty"`
	// Deprecated: PackID carried the Offline Pack subject before ADR-0157 moved
	// offline authorization into the sealed pack-grant envelope
	// (internal/offlinepack). It is never issued and never read; it stays in
	// the wire declaration only so the v1.x streamToken contract remains
	// additive. Remove at the v2 major bump
	// (XCONTRACT-MAJOR-BUMP-PUBLISH-ORDER-1 owns the ordering).
	PackID    string          `json:"packID,omitempty"`
	DeviceID  store.DeviceID  `json:"deviceID"`
	SessionID store.SessionID `json:"sessionID"`
	ProfileID store.ProfileID `json:"profileID"`
	// CompatSubject/CompatDecisionID bind the grant to the server-held
	// compatibility decision current at mint. Both are omitted for subjects
	// with no compatibility decision; a partial pair fails closed at verify.
	CompatSubject    string `json:"compatSubject,omitempty"`
	CompatDecisionID string `json:"compatDecisionID,omitempty"`
	// Restricted is a property of the TARGET media, computed at issue time: is
	// it in a restricted Library? Restricted tokens require the verifying
	// session to currently hold reveal; unrestricted tokens survive a relock.
	Restricted bool `json:"restricted"`
	// AudioCopy carries the audio-only-transcode verdict from the
	// capability decision — known only at mint — to the byte layer, which holds
	// only the signed grant. A TargetMediaHLS token minted for the audio-copy
	// decision sets it true so the shared master.m3u8 fetch seeds the single
	// audio-copy rung regardless of which artifact a pure-HLS player requests
	// first; omitted (false) means the standard transcode ladder, byte-identical
	// to tokens minted before this field existed. Meaningful only for
	// TargetMediaHLS; ignored for every other Target.
	AudioCopy bool `json:"audioCopy,omitempty"`
	// Degraded carries the capacity-degrade verdict (ADR-0010 §6 "drop to this
	// quality"; PANEL-16) from the playbackInfo decision — where party membership
	// and admission are known — to the byte layer, which holds only the signed
	// grant. A TargetMediaHLS token minted for a Watch-Party member who hit the
	// transcode cap sets it true so the byte layer creates the session capped to
	// the single lowest rung and admits it over the cap (keeping the party whole
	// rather than refusing). Omitted (false) is the standard ladder, byte-identical
	// to tokens minted before this field existed. Meaningful only for
	// TargetMediaHLS; ignored for every other Target.
	Degraded bool `json:"degraded,omitempty"`
	// Foreground carries the server-decided HLS admission rank from playbackInfo
	// to the byte layer. Only a signed true value may authorize preemption; the
	// optional request header may lower this rank but cannot raise it.
	// Meaningful only for TargetMediaHLS; ignored for every other Target.
	Foreground bool `json:"foreground,omitempty"`
	// BurnSubtitleIndex carries the selected bitmap-subtitle burn-in verdict from
	// playbackInfo to the byte layer. It is encoded as absolute stream index + 1;
	// zero means no burn-in, preserving old tokens and still allowing stream 0.
	// Meaningful only for TargetMediaHLS; ignored for every other Target.
	BurnSubtitleIndex int `json:"burnSubtitleIndex,omitempty"`
	// SubtitleStreamIndex binds a muxed-text subtitle grant to one absolute
	// ffprobe stream index. It is encoded as index + 1 so stream 0 remains
	// representable while zero keeps legacy grants unbound.
	// Meaningful only for TargetMediaSubtitle.
	SubtitleStreamIndex int `json:"subtitleStreamIndex,omitempty"`
	// SubtitleFileFingerprint binds a muxed-text subtitle grant to the exact
	// Media File negotiated by playbackInfo. The byte route resolves this
	// fingerprint only within the grant's already-visible Media, so a valid
	// fingerprint from another Media cannot redirect extraction. Omitted legacy
	// grants retain the primary-file compatibility path.
	// Meaningful only for TargetMediaSubtitle.
	SubtitleFileFingerprint string `json:"subtitleFileFingerprint,omitempty"`
	// SidecarRawASS carries the effective, quirk-overlay-filtered ASS-rendering
	// capability from playbackInfo to the sidecar byte route. Raw ASS is served
	// only when this signed bit is true; omitted and legacy grants fail closed.
	// Meaningful only for TargetMediaSidecar.
	SidecarRawASS bool `json:"sidecarRawASS,omitempty"`
	// ProfileCapability binds the grant to the CapabilityProfile used at mint.
	// It is the third axis of ADR-0010 §6 session identity and lets the byte
	// route address a cast/AirPlay receiver's new session.
	ProfileCapability string `json:"profileCapability,omitempty"`
	// ReceiverBound marks a grant minted for an ADR-0194 §6 cast/AirPlay
	// receiver profile. Receiver-bound byte requests require a resolved
	// ProfileCapability; ordinary and legacy HLS grants keep their established
	// behavior.
	ReceiverBound bool `json:"receiverBound,omitempty"`
	// ReceiverProfileResolved proves the receiver profile matched an ADR-0161
	// quirks-DB selector at mint. A receiver-bound grant without this signed
	// proof fails closed at the byte route.
	ReceiverProfileResolved bool `json:"receiverProfileResolved,omitempty"`
	// HLSAudioCodec/HLSAudioChannels carry the selected transcode audio target
	// from playbackInfo to the byte layer. Omitted means the legacy HLS floor:
	// AAC stereo. Meaningful only for TargetMediaHLS.
	HLSAudioCodec    string `json:"hlsAudioCodec,omitempty"`
	HLSAudioChannels int    `json:"hlsAudioChannels,omitempty"`
	// HLSAudioGainDB carries a loudness normalization gain only when playbackInfo
	// had a measurement and selected a server transcode for another reason.
	// Meaningful only for TargetMediaHLS.
	HLSAudioGainDB *float64 `json:"hlsAudioGainDB,omitempty"`
	// HLSClientMaxHeight/HLSHEVC/HLSHEVCMaxHeight carry the playbackInfo ladder
	// shape to the byte layer, which has no CapabilityProfile. HEVC is additive
	// and only minted when the client declared HEVC and the server has a verified
	// hardware encoder; omitted fields preserve the legacy H.264 ladder.
	HLSClientMaxHeight int  `json:"hlsClientMaxHeight,omitempty"`
	HLSHEVC            bool `json:"hlsHEVC,omitempty"`
	HLSHEVCMaxHeight   int  `json:"hlsHEVCMaxHeight,omitempty"`
	// PlaybackRenditionID binds an HLS grant to a precomputed playback
	// rendition cache object. The byte route still verifies the
	// normal HLS Stream Token and reveal scope first; this field only selects
	// which disposable cache directory to serve after authorization succeeds.
	PlaybackRenditionID store.PlaybackRenditionID `json:"playbackRenditionID,omitempty"`
	ExpiresAt           time.Time                 `json:"expiresAt"`
}

// HasResolvedProfileCapability reports whether the grant carries a capability
// fingerprint produced by playback.CapabilityFingerprint. The byte layer has
// only the signed grant, so missing or malformed capability identities fail
// closed instead of becoming an unverified receiver assumption.
func (g Grant) HasResolvedProfileCapability() bool {
	if len(g.ProfileCapability) != 16 || strings.ToLower(g.ProfileCapability) != g.ProfileCapability {
		return false
	}
	decoded, err := hex.DecodeString(g.ProfileCapability)
	return err == nil && len(decoded) == 8
}

// Recorder records each issued grant in the transparency ledger (ADR-0036 My
// Data: read-only view of outstanding Stream Tokens). It is the single,
// central choke point — every Issue path records once — and it never
// participates in authorizing bytes (Verify uses the signed grant, not the
// ledger). Optional: nil leaves issuance unrecorded, so deployments and tests
// that do not surface the My Data panel pay nothing.
type Recorder interface {
	RecordStreamToken(ctx context.Context, sessionID store.SessionID, profileID store.ProfileID, deviceID store.DeviceID, target string, mediaID store.MediaID, restricted bool, expiresAt time.Time) error
}

// Service signs and validates Stream Tokens.
type Service struct {
	key                 []byte
	now                 func() time.Time
	recorder            Recorder
	role                serverrole.Role
	compatResolver      DecisionResolver
	circleLeaseResolver CircleLeaseResolver
	validateMAC         func(payload, signature []byte) bool
}

// AuthenticatedGrant is an opaque proof that a Service has parsed and
// authenticated a Stream Token. Its fields are deliberately private: callers
// may inspect the signed Grant, but only the issuing Service can consume the
// proof for the live scope/Reveal check.
type AuthenticatedGrant struct {
	service *Service
	grant   Grant
}

// Grant returns the authenticated signed payload for subject lookup.
func (g AuthenticatedGrant) Grant() Grant {
	return g.grant
}

// NewService creates a token service. The zero role (Main) issues tokens
// normally; an Attached Origin Node refuses issuance (ADR-0123 §2 / ADR-0129
// §1: Stream-Token issuance stays main-side).
func NewService(key []byte, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{key: append([]byte(nil), key...), now: now}
}

// SetRole fixes the process authority posture. Set once at boot;
// the default Main keeps existing behavior. An Attached service refuses Issue.
func (s *Service) SetRole(role serverrole.Role) {
	s.role = role
}

// SetRecorder wires the transparency ledger sink. Nil disables it.
func (s *Service) SetRecorder(recorder Recorder) {
	s.recorder = recorder
}

// Issue returns a signed token for a grant.
func (s *Service) Issue(ctx context.Context, grant Grant) (string, error) {
	// Authority face: an Attached Origin Node never issues Stream
	// Tokens — the main server stays the sole issuer. Fail closed before signing.
	if err := s.role.GuardAuthority(serverrole.FaceStreamToken); err != nil {
		return "", err
	}
	if len(s.key) == 0 {
		return "", errors.New("stream token key is required")
	}
	if !validTarget(grant.Target) || grant.MediaID == "" || grant.DeviceID == "" || grant.SessionID == "" || grant.ProfileID == "" {
		return "", errors.New("stream token grant is incomplete")
	}
	now := s.now()
	if !grant.ExpiresAt.After(now) {
		return "", errors.New("stream token expiry must be in the future")
	}
	if maximumExpiry := now.Add(MaxStreamTokenTTL); grant.ExpiresAt.After(maximumExpiry) {
		grant.ExpiresAt = maximumExpiry
	}
	grant, err := s.stampCompatDecisionForIssue(ctx, grant)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	signature := s.sign(payload)
	token := encode(payload) + "." + encode(signature)
	// Record in the transparency ledger. Best-effort: a ledger
	// failure must not fail a valid token, so the error is swallowed. The
	// caller's context bounds the synchronous write.
	if s.recorder != nil {
		_ = s.recorder.RecordStreamToken(ctx, grant.SessionID, grant.ProfileID, grant.DeviceID, string(grant.Target), grant.MediaID, grant.Restricted, grant.ExpiresAt)
	}
	return token, nil
}

// Verify checks a token against the current request scope and target.
func (s *Service) Verify(token string, target Target, scope auth.Scope) (Grant, error) {
	authenticated, err := s.Authenticate(token, target)
	if err != nil {
		return Grant{}, err
	}
	return s.VerifyAuthenticated(authenticated, scope)
}

// VerifyAuthenticated applies the live scope and Reveal checks to a grant
// authenticated by this exact Service instance, without parsing or validating
// the token's MAC a second time.
func (s *Service) VerifyAuthenticated(authenticated AuthenticatedGrant, scope auth.Scope) (Grant, error) {
	if authenticated.service == nil || authenticated.service != s {
		return Grant{}, errors.New("invalid authenticated stream grant")
	}
	grant := authenticated.grant
	if grant.DeviceID != scope.DeviceID || grant.SessionID != scope.SessionID || grant.ProfileID != scope.ProfileID {
		return Grant{}, errors.New("stream token scope mismatch")
	}
	if grant.Restricted && !scope.Revealed {
		return Grant{}, errors.New("stream token requires reveal")
	}
	return grant, nil
}

// Authenticate validates the signature, expiry, and target once and returns
// an opaque proof for a later live scope/Reveal check.
func (s *Service) Authenticate(token string, target Target) (AuthenticatedGrant, error) {
	grant, err := s.authenticateGrant(token, target)
	if err != nil {
		return AuthenticatedGrant{}, err
	}
	return AuthenticatedGrant{service: s, grant: grant}, nil
}

// UnverifiedGrant validates the signature, expiry, and target, then returns the grant.
func (s *Service) UnverifiedGrant(token string, target Target) (Grant, error) {
	return s.authenticateGrant(token, target)
}

func (s *Service) authenticateGrant(token string, target Target) (Grant, error) {
	if len(s.key) == 0 {
		return Grant{}, errors.New("stream token key is required")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Grant{}, errors.New("invalid stream token")
	}
	payload, err := decode(parts[0])
	if err != nil {
		return Grant{}, errors.New("invalid stream token payload")
	}
	signature, err := decode(parts[1])
	if err != nil {
		return Grant{}, errors.New("invalid stream token signature")
	}
	validMAC := s.validateMAC
	if validMAC == nil {
		validMAC = func(payload, signature []byte) bool {
			return hmac.Equal(signature, s.sign(payload))
		}
	}
	if !validMAC(payload, signature) {
		return Grant{}, errors.New("invalid stream token signature")
	}
	var grant Grant
	if err := json.Unmarshal(payload, &grant); err != nil {
		return Grant{}, errors.New("invalid stream token payload")
	}
	if grant.Target != target {
		return Grant{}, errors.New("stream token target mismatch")
	}
	if !validTarget(grant.Target) || grant.MediaID == "" {
		return Grant{}, errors.New("stream token target/subject mismatch")
	}
	if !grant.ExpiresAt.After(s.now()) {
		return Grant{}, errors.New("stream token expired")
	}
	return grant, nil
}

func (s *Service) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

func encode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func decode(text string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(text)
}
