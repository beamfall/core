package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBeamHandoffOfferConsumesOnce(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()
	offer := BeamHandoffOffer{
		TokenHash:       "hash-alpha",
		SourceSessionID: "session-default",
		SourceDeviceID:  "device-tv",
		SourceProfileID: "profile-default",
		TargetDeviceID:  "device-mac",
		MediaID:         "media-alpha",
		PositionSeconds: 42,
		ExpiresAt:       now.Add(time.Minute),
		CreatedAt:       now,
	}

	if err := st.CreateBeamHandoffOffer(ctx, offer); err != nil {
		t.Fatalf("CreateBeamHandoffOffer returned error: %v", err)
	}
	got, err := st.ConsumeBeamHandoffOffer(ctx, "hash-alpha", now.Add(10*time.Second))
	if err != nil {
		t.Fatalf("ConsumeBeamHandoffOffer returned error: %v", err)
	}
	if got.MediaID != "media-alpha" || got.PositionSeconds != 42 || got.TargetDeviceID != "device-mac" {
		t.Fatalf("consumed offer = %#v, want media-alpha at 42 for device-mac", got)
	}
	if _, err := st.ConsumeBeamHandoffOffer(ctx, "hash-alpha", now.Add(11*time.Second)); !errors.Is(err, ErrBeamHandoffInvalid) {
		t.Fatalf("replay error = %v, want ErrBeamHandoffInvalid", err)
	}
}

func TestBeamHandoffOfferPersistsClassAndReveal(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(1500, 0).UTC()

	// Explicit class + reveal round-trip.
	set := BeamHandoffOffer{
		TokenHash:       "hash-class",
		SourceSessionID: "session-default",
		SourceDeviceID:  "device-tv",
		SourceProfileID: "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 5,
		MediaClass:      MediaClassAudio,
		SenderRevealed:  true,
		ExpiresAt:       now.Add(time.Minute),
		CreatedAt:       now,
	}
	if err := st.CreateBeamHandoffOffer(ctx, set); err != nil {
		t.Fatalf("create set: %v", err)
	}
	got, err := st.ConsumeBeamHandoffOffer(ctx, "hash-class", now.Add(time.Second))
	if err != nil {
		t.Fatalf("consume set: %v", err)
	}
	if got.MediaClass != MediaClassAudio || !got.SenderRevealed {
		t.Fatalf("consumed = class %q revealed %v, want audio/true", got.MediaClass, got.SenderRevealed)
	}

	// Absent class + reveal default to zero values (absent keeps current behavior).
	bare := set
	bare.TokenHash = "hash-bare"
	bare.MediaClass = ""
	bare.SenderRevealed = false
	bare.CreatedAt = now.Add(time.Second)
	if err := st.CreateBeamHandoffOffer(ctx, bare); err != nil {
		t.Fatalf("create bare: %v", err)
	}
	gotBare, err := st.ConsumeBeamHandoffOffer(ctx, "hash-bare", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("consume bare: %v", err)
	}
	if gotBare.MediaClass != "" || gotBare.SenderRevealed {
		t.Fatalf("bare consumed = class %q revealed %v, want empty/false", gotBare.MediaClass, gotBare.SenderRevealed)
	}
}

func TestBeamHandoffOfferReplacementAndExpiry(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Unix(2000, 0).UTC()

	first := BeamHandoffOffer{
		TokenHash:       "hash-first",
		SourceSessionID: "session-default",
		SourceDeviceID:  "device-tv",
		SourceProfileID: "profile-default",
		MediaID:         "media-alpha",
		PositionSeconds: 10,
		ExpiresAt:       now.Add(time.Minute),
		CreatedAt:       now,
	}
	second := first
	second.TokenHash = "hash-second"
	second.PositionSeconds = 20
	second.CreatedAt = now.Add(time.Second)

	if err := st.CreateBeamHandoffOffer(ctx, first); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := st.CreateBeamHandoffOffer(ctx, second); err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := st.ConsumeBeamHandoffOffer(ctx, "hash-first", now.Add(2*time.Second)); !errors.Is(err, ErrBeamHandoffInvalid) {
		t.Fatalf("replaced offer error = %v, want ErrBeamHandoffInvalid", err)
	}
	got, err := st.ConsumeBeamHandoffOffer(ctx, "hash-second", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("consume second: %v", err)
	}
	if got.PositionSeconds != 20 {
		t.Fatalf("PositionSeconds = %d, want 20", got.PositionSeconds)
	}

	expired := first
	expired.TokenHash = "hash-expired"
	expired.ExpiresAt = now.Add(5 * time.Second)
	expired.CreatedAt = now.Add(3 * time.Second)
	if err := st.CreateBeamHandoffOffer(ctx, expired); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if _, err := st.ConsumeBeamHandoffOffer(ctx, "hash-expired", now.Add(6*time.Second)); !errors.Is(err, ErrBeamHandoffInvalid) {
		t.Fatalf("expired offer error = %v, want ErrBeamHandoffInvalid", err)
	}
}
