package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// BeamHandoffOffer is one single-use Beam session-transfer offer.
type BeamHandoffOffer struct {
	TokenHash       string
	SourceSessionID SessionID
	SourceDeviceID  DeviceID
	SourceProfileID ProfileID
	TargetDeviceID  DeviceID
	MediaID         MediaID
	PositionSeconds int
	// MediaClass is the resolved Media's fixed class at offer time, carried so
	// the claiming device can pick the audio-vs-video surface before its own
	// playback round-trip resolves. Empty for offers minted before this column
	// existed (absent keeps current behavior).
	MediaClass MediaClass
	// SenderRevealed records whether the offering session held Reveal, a UI
	// hint so the receiver can pre-prompt for the Reveal PIN. The server still
	// re-derives Reveal server-side at claim; this never authorizes bytes.
	SenderRevealed bool
	ExpiresAt      time.Time
	CreatedAt      time.Time
	ClaimedAt      time.Time
}

// CreateBeamHandoffOffer stores one open Beam offer, replacing any prior open
// offer from the same source session.
func (s *Store) CreateBeamHandoffOffer(ctx context.Context, offer BeamHandoffOffer) error {
	if offer.TokenHash == "" {
		return ValidationError("token hash is required")
	}
	if offer.SourceSessionID == "" {
		return ValidationError("source session ID is required")
	}
	if offer.SourceDeviceID == "" {
		return ValidationError("source device ID is required")
	}
	if offer.SourceProfileID == "" {
		return ValidationError("source profile ID is required")
	}
	if offer.MediaID == "" {
		return ValidationError("media ID is required")
	}
	if offer.PositionSeconds < 0 {
		return ValidationError("positionSeconds must be non-negative")
	}
	if offer.ExpiresAt.IsZero() {
		return ValidationError("expiresAt is required")
	}
	if offer.CreatedAt.IsZero() {
		offer.CreatedAt = s.serverNow()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE beam_handoff_offer SET claimed_at_ms = ? WHERE source_session_id = ? AND claimed_at_ms IS NULL`,
		timeToMillis(offer.CreatedAt), string(offer.SourceSessionID)); err != nil {
		return err
	}
	var target any
	if offer.TargetDeviceID != "" {
		target = string(offer.TargetDeviceID)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO beam_handoff_offer
			(token_hash, source_session_id, source_device_id, source_profile_id, target_device_id,
			 media_id, position_seconds, media_class, sender_revealed, expires_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, offer.TokenHash, string(offer.SourceSessionID), string(offer.SourceDeviceID),
		string(offer.SourceProfileID), target, string(offer.MediaID), offer.PositionSeconds,
		string(offer.MediaClass), boolInt(offer.SenderRevealed),
		timeToMillis(offer.ExpiresAt), timeToMillis(offer.CreatedAt))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeBeamHandoffOffer atomically marks a valid offer claimed and returns it.
func (s *Store) ConsumeBeamHandoffOffer(ctx context.Context, tokenHash string, now time.Time) (BeamHandoffOffer, error) {
	if tokenHash == "" {
		return BeamHandoffOffer{}, ErrBeamHandoffInvalid
	}
	if now.IsZero() {
		now = s.serverNow()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BeamHandoffOffer{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BeamHandoffOffer{}, err
	}
	defer tx.Rollback()
	offer, err := beamHandoffOfferSQL(ctx, tx, tokenHash, now)
	if err != nil {
		return BeamHandoffOffer{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE beam_handoff_offer
		SET claimed_at_ms = ?
		WHERE token_hash = ? AND claimed_at_ms IS NULL AND expires_at_ms > ?
	`, timeToMillis(now), tokenHash, timeToMillis(now))
	if err != nil {
		return BeamHandoffOffer{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return BeamHandoffOffer{}, err
	}
	if affected != 1 {
		return BeamHandoffOffer{}, ErrBeamHandoffInvalid
	}
	if err := tx.Commit(); err != nil {
		return BeamHandoffOffer{}, err
	}
	return offer, nil
}

// BeamHandoffOffer returns an open offer without consuming it.
func (s *Store) BeamHandoffOffer(ctx context.Context, tokenHash string, now time.Time) (BeamHandoffOffer, error) {
	if tokenHash == "" {
		return BeamHandoffOffer{}, ErrBeamHandoffInvalid
	}
	if now.IsZero() {
		now = s.serverNow()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return BeamHandoffOffer{}, err
	}
	tx, err := s.catalogW.BeginTx(ctx, nil)
	if err != nil {
		return BeamHandoffOffer{}, err
	}
	defer tx.Rollback()
	offer, err := beamHandoffOfferSQL(ctx, tx, tokenHash, now)
	if err != nil {
		return BeamHandoffOffer{}, err
	}
	return offer, tx.Commit()
}

func beamHandoffOfferSQL(ctx context.Context, tx *sql.Tx, tokenHash string, now time.Time) (BeamHandoffOffer, error) {
	var offer BeamHandoffOffer
	var sourceSessionID, sourceDeviceID, sourceProfileID, targetDeviceID, mediaID, mediaClass string
	var senderRevealed int
	var expiresAt, createdAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT source_session_id, source_device_id, source_profile_id, COALESCE(target_device_id, ''),
		       media_id, position_seconds, media_class, sender_revealed, expires_at_ms, created_at_ms
		FROM beam_handoff_offer
		WHERE token_hash = ? AND claimed_at_ms IS NULL AND expires_at_ms > ?
	`, tokenHash, timeToMillis(now)).Scan(&sourceSessionID, &sourceDeviceID, &sourceProfileID,
		&targetDeviceID, &mediaID, &offer.PositionSeconds, &mediaClass, &senderRevealed, &expiresAt, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BeamHandoffOffer{}, ErrBeamHandoffInvalid
		}
		return BeamHandoffOffer{}, err
	}
	offer.TokenHash = tokenHash
	offer.SourceSessionID = SessionID(sourceSessionID)
	offer.SourceDeviceID = DeviceID(sourceDeviceID)
	offer.SourceProfileID = ProfileID(sourceProfileID)
	offer.TargetDeviceID = DeviceID(targetDeviceID)
	offer.MediaID = MediaID(mediaID)
	offer.MediaClass = MediaClass(mediaClass)
	offer.SenderRevealed = senderRevealed != 0
	offer.ExpiresAt = millisToTime(expiresAt)
	offer.CreatedAt = millisToTime(createdAt)
	return offer, nil
}
