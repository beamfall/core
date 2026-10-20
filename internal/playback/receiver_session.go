package playback

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// CapabilityFingerprint is the stable identity of a declared CapabilityProfile,
// the third axis of ADR-0010 §6 session identity. A cast/AirPlay route change
// substitutes the receiver's profile, so it addresses a new playback session
// instead of reusing the sender's rendition.
func CapabilityFingerprint(profile CapabilityProfile) string {
	if isZeroCapabilityProfile(profile) {
		return ""
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return "unencodable"
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

func isZeroCapabilityProfile(profile CapabilityProfile) bool {
	encoded, err := json.Marshal(profile)
	if err != nil {
		return false
	}
	zero, err := json.Marshal(CapabilityProfile{})
	return err == nil && string(encoded) == string(zero)
}

// isReceiverCapabilityProfile identifies the ADR-0194 §6 receiver selector
// families. The concrete receiver model remains an ADR-0161 quirks-DB selector;
// this only marks the signed byte grant as receiver-bound.
func isReceiverCapabilityProfile(profile CapabilityProfile) bool {
	client := strings.ToLower(strings.TrimSpace(profile.Client))
	return strings.HasPrefix(client, "chromecast-") || client == "appletv-airplay"
}
