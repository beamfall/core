package integrationhost

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"time"
)

const (
	legacyNACK = byte(0x00)
	legacyACK  = byte(0x01)

	hostMinVersion       = 1
	hostMaxVersion       = 2
	maxCapabilityNames   = 64
	maxCapabilityNameLen = 64
	maxCapabilityBytes   = 4 << 10
)

const DefaultHandshakeTimeout = 5 * time.Second

type handshakeFrame struct {
	Token        string   `json:"token"`
	MinVersion   int      `json:"minVersion,omitempty"`
	MaxVersion   int      `json:"maxVersion,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`

	minSet          bool
	maxSet          bool
	capabilitiesSet bool
	presentedTokens []string
}

type negotiationAck struct {
	SelectedVersion int      `json:"selectedVersion"`
	Capabilities    []string `json:"capabilities"`
}

type negotiationError struct {
	Error string `json:"error"`
}

// VersionOffer is a worker's supported Integration Host protocol range and
// optional capability offers.
type VersionOffer struct {
	MinVersion   int
	MaxVersion   int
	Capabilities []string
}

// NegotiatedVersion is the host-selected protocol and capability intersection.
type NegotiatedVersion struct {
	SelectedVersion int
	Capabilities    []string
}

func (s *Supervisor) negotiateHost(conn net.Conn) bool {
	timeout := s.handshakeTimeout
	if timeout <= 0 {
		timeout = DefaultHandshakeTimeout
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	raw, err := readFrame(conn)
	if err != nil {
		return false
	}
	handshake, legacy, decodeErr := decodeHandshake(raw)
	authorized := false
	for _, token := range handshake.presentedTokens {
		if s.consumeToken(token) {
			authorized = true
		}
	}
	if decodeErr != nil {
		writeHandshakeError(conn, legacy, "invalid handshake")
		return false
	}
	if !authorized {
		writeHandshakeError(conn, legacy, "unauthorized")
		return false
	}
	if legacy {
		_, err := conn.Write([]byte{legacyACK})
		return err == nil
	}
	ack, errText := s.selectVersion(handshake)
	if errText != "" {
		writeHandshakeError(conn, false, errText)
		return false
	}
	payload, _ := json.Marshal(ack)
	return writeFrame(conn, payload) == nil
}

func decodeHandshake(raw []byte) (handshakeFrame, bool, error) {
	var handshake handshakeFrame
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return handshake, false, fmt.Errorf("invalid handshake")
	}
	seen := map[string]bool{}
	var firstErr error
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return handshake, false, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return handshake, false, fmt.Errorf("invalid handshake key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return handshake, false, err
		}
		if seen[key] && firstErr == nil {
			firstErr = fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		if err := decodeHandshakeMember(&handshake, key, value); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return handshake, false, err
	}
	if decoder.More() {
		return handshake, false, fmt.Errorf("trailing handshake data")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing handshake data")
		}
		return handshake, false, err
	}
	legacy := !handshake.minSet && !handshake.maxSet && !handshake.capabilitiesSet
	return handshake, legacy, firstErr
}

func decodeHandshakeMember(handshake *handshakeFrame, key string, raw json.RawMessage) error {
	switch key {
	case "token":
		var token string
		if err := json.Unmarshal(raw, &token); err != nil {
			return err
		}
		handshake.Token = token
		handshake.presentedTokens = append(handshake.presentedTokens, token)
		return nil
	case "minVersion":
		handshake.minSet = true
		return json.Unmarshal(raw, &handshake.MinVersion)
	case "maxVersion":
		handshake.maxSet = true
		return json.Unmarshal(raw, &handshake.MaxVersion)
	case "capabilities":
		handshake.capabilitiesSet = true
		if bytes.Equal(raw, []byte("null")) {
			return fmt.Errorf("capabilities must be an array")
		}
		return json.Unmarshal(raw, &handshake.Capabilities)
	default:
		return nil
	}
}

func (s *Supervisor) selectVersion(handshake handshakeFrame) (negotiationAck, string) {
	if !handshake.minSet || !handshake.maxSet || handshake.MinVersion <= 0 || handshake.MaxVersion <= 0 || handshake.MinVersion > handshake.MaxVersion {
		return negotiationAck{}, "invalid version range"
	}
	if err := validateCapabilities(handshake.Capabilities); err != nil {
		return negotiationAck{}, "invalid capabilities"
	}
	minVersion := max(handshake.MinVersion, hostMinVersion)
	maxVersion := min(handshake.MaxVersion, hostMaxVersion)
	if minVersion > maxVersion {
		return negotiationAck{}, "no compatible version"
	}
	return negotiationAck{
		SelectedVersion: maxVersion,
		Capabilities:    capabilityIntersection(handshake.Capabilities, s.capabilities),
	}, ""
}

func validateCapabilities(capabilities []string) error {
	if len(capabilities) > maxCapabilityNames {
		return fmt.Errorf("too many capabilities")
	}
	total := 0
	for _, capability := range capabilities {
		if len(capability) > maxCapabilityNameLen {
			return fmt.Errorf("capability name too long")
		}
		total += len(capability)
		if total > maxCapabilityBytes {
			return fmt.Errorf("capabilities too large")
		}
	}
	return nil
}

func capabilityIntersection(offered, supported []string) []string {
	known := make(map[string]bool, len(supported))
	for _, capability := range supported {
		known[capability] = true
	}
	intersection := []string{}
	added := map[string]bool{}
	for _, capability := range offered {
		if known[capability] && !added[capability] {
			intersection = append(intersection, capability)
			added[capability] = true
		}
	}
	sort.Strings(intersection)
	return intersection
}

func writeHandshakeError(conn net.Conn, legacy bool, message string) {
	if legacy {
		_, _ = conn.Write([]byte{legacyNACK})
		return
	}
	payload, _ := json.Marshal(negotiationError{Error: message})
	_ = writeFrame(conn, payload)
}

// NegotiateWorker performs the new-worker handshake. A one-byte legacy ACK
// selects v1 only when v1 is inside offer; framed replies select the highest
// host overlap.
func NegotiateWorker(conn net.Conn, token string, offer VersionOffer, timeout time.Duration) (NegotiatedVersion, error) {
	if offer.MinVersion <= 0 || offer.MaxVersion <= 0 || offer.MinVersion > offer.MaxVersion {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: invalid version range")
	}
	if err := validateCapabilities(offer.Capabilities); err != nil {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: invalid capabilities")
	}
	if timeout <= 0 {
		timeout = DefaultHandshakeTimeout
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	payload, _ := json.Marshal(handshakeFrame{
		Token:        token,
		MinVersion:   offer.MinVersion,
		MaxVersion:   offer.MaxVersion,
		Capabilities: offer.Capabilities,
	})
	if err := writeFrame(conn, payload); err != nil {
		return NegotiatedVersion{}, err
	}
	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		return NegotiatedVersion{}, err
	}
	if first[0] == legacyACK {
		if offer.MinVersion <= 1 && offer.MaxVersion >= 1 {
			return NegotiatedVersion{SelectedVersion: 1, Capabilities: []string{}}, nil
		}
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: legacy host selected unsupported version")
	}
	if first[0] != 0 {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: invalid handshake response")
	}
	var lengthTail [3]byte
	if _, err := io.ReadFull(conn, lengthTail[:]); err != nil {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: legacy handshake rejected")
	}
	var lengthBytes [4]byte
	copy(lengthBytes[1:], lengthTail[:])
	length := binary.BigEndian.Uint32(lengthBytes[:])
	if length == 0 || length > maxFrameBytes {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: invalid handshake response")
	}
	response := make([]byte, length)
	if _, err := io.ReadFull(conn, response); err != nil {
		return NegotiatedVersion{}, err
	}
	ack, rejected, err := decodeNegotiationResponse(response)
	if err != nil {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: invalid handshake response")
	}
	if rejected != "" {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: %s", rejected)
	}
	if ack.SelectedVersion < offer.MinVersion || ack.SelectedVersion > offer.MaxVersion {
		return NegotiatedVersion{}, fmt.Errorf("integrationhost: host selected unsupported version")
	}
	if err := validateNegotiatedCapabilities(ack.Capabilities, offer.Capabilities); err != nil {
		return NegotiatedVersion{}, err
	}
	return NegotiatedVersion(ack), nil
}

func validateNegotiatedCapabilities(selected, offered []string) error {
	if err := validateCapabilities(selected); err != nil || !sort.StringsAreSorted(selected) {
		return fmt.Errorf("integrationhost: invalid negotiated capabilities")
	}
	offerSet := make(map[string]bool, len(offered))
	for _, capability := range offered {
		offerSet[capability] = true
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, capability := range selected {
		if selectedSet[capability] {
			return fmt.Errorf("integrationhost: duplicate negotiated capability")
		}
		selectedSet[capability] = true
		if !offerSet[capability] {
			return fmt.Errorf("integrationhost: host selected unoffered capability")
		}
	}
	return nil
}

func decodeNegotiationResponse(raw []byte) (negotiationAck, string, error) {
	var ack negotiationAck
	var rejected string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ack, "", fmt.Errorf("invalid response")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return ack, "", err
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] {
			return ack, "", fmt.Errorf("invalid response key")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return ack, "", err
		}
		switch key {
		case "selectedVersion":
			if err := json.Unmarshal(value, &ack.SelectedVersion); err != nil {
				return ack, "", err
			}
		case "capabilities":
			if bytes.Equal(value, []byte("null")) {
				return ack, "", fmt.Errorf("capabilities must be an array")
			}
			if err := json.Unmarshal(value, &ack.Capabilities); err != nil {
				return ack, "", err
			}
		case "error":
			if err := json.Unmarshal(value, &rejected); err != nil {
				return ack, "", err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return ack, "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ack, "", fmt.Errorf("trailing response data")
	}
	if rejected != "" {
		if seen["selectedVersion"] || seen["capabilities"] {
			return ack, "", fmt.Errorf("ambiguous response")
		}
		return ack, rejected, nil
	}
	if !seen["selectedVersion"] || !seen["capabilities"] {
		return ack, "", fmt.Errorf("incomplete response")
	}
	return ack, "", nil
}
