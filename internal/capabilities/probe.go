package capabilities

import "os"

// probeShareMount auto-detects whether the host exposes a share-mount capability
// (ADR-0124 §5 probe-derived capability). It is NEVER operator-flipped: presence
// is decided by the host, and when absent the capability degrades observably to
// `enabled:false, reason:probe-absent`. The detection is a host fact, surfaced
// through BEAMFALL_PROBE_SHARE_MOUNT so a deployment that has wired share-mount
// support can attest it without the server acquiring a new external surface
// (Safety Corollary); an unset/empty value means the probe is absent, which is
// the default on a stock binary.
func probeShareMount() bool {
	return os.Getenv("BEAMFALL_PROBE_SHARE_MOUNT") == "1"
}
