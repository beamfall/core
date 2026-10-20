package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// Ambient Group Conductor latency-calibration endpoint. The
// LAN-only, four-timestamp NTP-style handshake a device completes when it joins
// an Ambient Group, to establish its clock offset and round-trip against the
// server clock. This is the §8 "latency calibration endpoint": the server
// records the two server-side timestamps (t2 receive, t3 transmit) of the
// exchange; the device supplies its own t1 (send) and measures t4 (receive)
// locally, then computes
//
//	offset    = ((t2 - t1) + (t3 - t4)) / 2
//	roundtrip = (t4 - t1) - (t3 - t2)
//
// with conductor.Calibrate. The device then JOINS the group (the graph
// joinGroup mutation) with the resulting clock offset and its platform-measured
// render latency.
//
// LAN-ONLY ENFORCEMENT lives HERE, at the network boundary (ADR-0035 §8 "Audio:
// LAN-only" + the §"Clock sync" NTP-on-LAN assumption). The handler refuses any
// request whose source IP is not loopback/RFC1918/ULA/link-local
// (requestIsLAN, the same check ADR-0046 §2.1 pins on, reading the real
// connection address — never a forwarded header — so a relay-fronted remote
// caller presents a public source address and is rejected). Because a device can
// obtain a usable calibration ONLY from this LAN-gated endpoint, the whole Group
// Conductor is LAN-scoped transitively: a non-LAN device can never produce the
// offset/latency a meaningful Join requires, so the conductor is never exposed
// over the relay/remote path.

// groupCalibrateRequest is the device's half of the four-timestamp exchange. t1
// is the device's local-clock send instant, as Unix milliseconds; the server
// echoes it back so the device can pair it with t4 without retaining state. The
// other three timestamps are filled by the server (t2/t3) and the device (t4).
type groupCalibrateRequest struct {
	T1MS int64 `json:"t1MS"`
}

// groupCalibrateResponse carries the server-recorded timestamps of the exchange.
// t2 is when the server received the request, t3 when it sent the
// response — both server-clock Unix milliseconds. The device measures t4 on
// receipt and computes offset + round-trip locally with conductor.Calibrate. The
// echoed t1 lets a stateless device pair the exchange without holding the send
// time across the round-trip.
type groupCalibrateResponse struct {
	T1MS int64 `json:"t1MS"`
	T2MS int64 `json:"t2MS"`
	T3MS int64 `json:"t3MS"`
}

// groupCalibrate serves the LAN-only four-timestamp calibration handshake.
// It is session-authenticated (a calibrating device must already
// be enrolled in a Profile — the Group Conductor is strictly same-Profile) and
// LAN-gated (a non-LAN source is refused, scoping the whole conductor to the
// LAN). The handler records t2 as early as possible after auth and t3 as late as
// possible before writing, so the two server timestamps bracket only the server's
// own processing, keeping the device's offset estimate tight.
func (s *Server) groupCalibrate(w http.ResponseWriter, r *http.Request) {
	// t2: server receive instant. Recorded first thing so it reflects arrival,
	// not post-decode processing.
	t2 := time.Now().UTC()
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)

	// LAN-only gate: the conductor is never exposed over the
	// relay/remote path. requestIsLAN reads the real connection address, so a
	// relay-fronted remote caller (public source IP) is rejected here and can
	// never obtain a calibration.
	if !requestIsLAN(r) {
		writeError(w, r, http.StatusForbidden, "group calibration is LAN-only")
		return
	}

	// Session auth: a calibrating device must resolve a Profile-bound scope. The
	// Group Conductor is same-Profile by construction, so a request that cannot
	// resolve a session (or resolves a non-Profile admin/token scope) has no
	// business calibrating into a group.
	scope, err := s.auth.ScopeFromRequest(r.Context(), r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	if scope.ProfileID == "" {
		writeError(w, r, http.StatusForbidden, "a profile session is required to calibrate")
		return
	}

	var req groupCalibrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}

	// t3: server transmit instant. Recorded last thing before writing, so it
	// reflects departure.
	t3 := time.Now().UTC()
	writeJSON(w, http.StatusOK, groupCalibrateResponse{
		T1MS: req.T1MS,
		T2MS: t2.UnixMilli(),
		T3MS: t3.UnixMilli(),
	})
}
