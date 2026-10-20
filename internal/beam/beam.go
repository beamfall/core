// Package beam implements Beamfall session handoff between enrolled devices.
package beam

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

const offerTTL = time.Minute

var (
	ErrInvalidRequest   = errors.New("invalid beam handoff request")
	ErrInvalidToken     = errors.New("beam handoff token invalid")
	ErrMediaUnavailable = errors.New("media unavailable")
)

// OfferRequest mints a single-use Beam handoff offer. MediaClass and
// SenderRevealed are optional and, when supplied, are ignored: the server
// derives both authoritatively (from the resolved Media's class and the
// offering session's Reveal state) so a client cannot spoof the receiver's
// surface choice or Reveal pre-prompt. Absent fields keep current behavior.
type OfferRequest struct {
	MediaID         string `json:"mediaID"`
	PositionSeconds int    `json:"positionSeconds"`
	TargetDeviceID  string `json:"targetDeviceID,omitempty"`
	MediaClass      string `json:"mediaClass,omitempty"`
	SenderRevealed  bool   `json:"senderRevealed,omitempty"`
}

// OfferResponse returns the bearer handoff token to the offering client.
// MediaClass is the resolved Media's fixed class ("audio"/"video"/"image");
// SenderRevealed reflects whether the offering session holds Reveal. Both are
// omitted when empty/false so an absent field keeps current behavior.
type OfferResponse struct {
	HandoffToken    string `json:"handoffToken"`
	MediaID         string `json:"mediaID"`
	PositionSeconds int    `json:"positionSeconds"`
	MediaClass      string `json:"mediaClass,omitempty"`
	SenderRevealed  bool   `json:"senderRevealed,omitempty"`
	ExpiresAt       string `json:"expiresAt"`
}

// ClaimRequest redeems a Beam handoff offer.
type ClaimRequest struct {
	HandoffToken string `json:"handoffToken"`
}

// ClaimResponse tells the claiming client what to play and where to start.
// MediaClass lets the receiver pick the audio-vs-video surface before its own
// playbackInfo round-trip resolves; SenderRevealed lets it pre-prompt for the
// Reveal PIN. Both are advisory hints — the server still re-derives Reveal at
// claim time — and are omitted when empty/false to keep current behavior.
type ClaimResponse struct {
	MediaID         string `json:"mediaID"`
	PositionSeconds int    `json:"positionSeconds"`
	SourceDeviceID  string `json:"sourceDeviceID"`
	SourceSessionID string `json:"sourceSessionID"`
	TargetDeviceID  string `json:"targetDeviceID,omitempty"`
	MediaClass      string `json:"mediaClass,omitempty"`
	SenderRevealed  bool   `json:"senderRevealed,omitempty"`
	ExpiresAt       string `json:"expiresAt"`
}

// Service owns Beam handoff token mint/redeem semantics.
type Service struct {
	store *store.Store
	auth  *auth.Service
	now   func() time.Time
}

func NewService(st *store.Store, authz *auth.Service, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: st, auth: authz, now: now}
}

func (s *Service) CreateOffer(ctx context.Context, scope auth.Scope, req OfferRequest) (OfferResponse, error) {
	if scope.SessionID == "" || scope.ProfileID == "" || scope.DeviceID == "" {
		return OfferResponse{}, ErrInvalidRequest
	}
	mediaID := store.MediaID(strings.TrimSpace(req.MediaID))
	if mediaID == "" || req.PositionSeconds < 0 {
		return OfferResponse{}, ErrInvalidRequest
	}
	targetDeviceID := store.DeviceID(strings.TrimSpace(req.TargetDeviceID))
	if targetDeviceID != "" {
		device, err := s.store.Device(ctx, targetDeviceID)
		if err != nil || device.Revoked {
			return OfferResponse{}, ErrInvalidRequest
		}
	}
	media, err := s.store.Media(ctx, mediaID, scope.LibraryFilter())
	if err != nil {
		return OfferResponse{}, ErrMediaUnavailable
	}
	token, err := randomToken()
	if err != nil {
		return OfferResponse{}, err
	}
	now := s.now().UTC()
	expires := now.Add(offerTTL)
	offer := store.BeamHandoffOffer{
		TokenHash:       tokenHash(token),
		SourceSessionID: scope.SessionID,
		SourceDeviceID:  scope.DeviceID,
		SourceProfileID: scope.ProfileID,
		TargetDeviceID:  targetDeviceID,
		MediaID:         media.ID,
		PositionSeconds: req.PositionSeconds,
		MediaClass:      media.Class,
		SenderRevealed:  scope.Revealed,
		ExpiresAt:       expires,
		CreatedAt:       now,
	}
	if err := s.store.CreateBeamHandoffOffer(ctx, offer); err != nil {
		return OfferResponse{}, err
	}
	return OfferResponse{
		HandoffToken:    token,
		MediaID:         string(media.ID),
		PositionSeconds: req.PositionSeconds,
		MediaClass:      string(media.Class),
		SenderRevealed:  scope.Revealed,
		ExpiresAt:       expires.Format(time.RFC3339),
	}, nil
}

func (s *Service) ClaimOffer(ctx context.Context, scope auth.Scope, req ClaimRequest) (ClaimResponse, error) {
	if scope.SessionID == "" || scope.ProfileID == "" || scope.DeviceID == "" {
		return ClaimResponse{}, ErrInvalidToken
	}
	token := strings.TrimSpace(req.HandoffToken)
	if token == "" {
		return ClaimResponse{}, ErrInvalidToken
	}
	now := s.now().UTC()
	hash := tokenHash(token)
	offer, err := s.store.BeamHandoffOffer(ctx, hash, now)
	if err != nil {
		if errors.Is(err, store.ErrBeamHandoffInvalid) {
			return ClaimResponse{}, ErrInvalidToken
		}
		return ClaimResponse{}, err
	}
	if offer.SourceProfileID != scope.ProfileID {
		return ClaimResponse{}, ErrInvalidToken
	}
	if offer.TargetDeviceID != "" && offer.TargetDeviceID != scope.DeviceID {
		return ClaimResponse{}, ErrInvalidToken
	}
	sourceScope, err := s.auth.Scope(ctx, offer.SourceSessionID)
	if err != nil {
		return ClaimResponse{}, ErrInvalidToken
	}
	if sourceScope.DeviceID != offer.SourceDeviceID || sourceScope.ProfileID != offer.SourceProfileID {
		return ClaimResponse{}, ErrInvalidToken
	}
	if _, err := s.store.Media(ctx, offer.MediaID, sourceScope.LibraryFilter()); err != nil {
		return ClaimResponse{}, ErrInvalidToken
	}
	if _, err := s.store.Media(ctx, offer.MediaID, scope.LibraryFilter()); err != nil {
		return ClaimResponse{}, ErrInvalidToken
	}
	consumed, err := s.store.ConsumeBeamHandoffOffer(ctx, hash, now)
	if err != nil {
		if errors.Is(err, store.ErrBeamHandoffInvalid) {
			return ClaimResponse{}, ErrInvalidToken
		}
		return ClaimResponse{}, err
	}
	return ClaimResponse{
		MediaID:         string(consumed.MediaID),
		PositionSeconds: consumed.PositionSeconds,
		SourceDeviceID:  string(consumed.SourceDeviceID),
		SourceSessionID: string(consumed.SourceSessionID),
		TargetDeviceID:  string(consumed.TargetDeviceID),
		MediaClass:      string(consumed.MediaClass),
		SenderRevealed:  consumed.SenderRevealed,
		ExpiresAt:       consumed.ExpiresAt.Format(time.RFC3339),
	}, nil
}

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
