package streamtoken

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

const (
	CurrentRevision  = 2
	PreviousRevision = 1
)

type Operation string
type Kind string
type CloseReason string
type RefusalCode string

const (
	OperationHello               Operation = "circle-hello"
	OperationCatalogRead         Operation = "catalog-read"
	OperationPlaybackAuthorize   Operation = "playback-authorize"
	OperationLeaseRenew          Operation = "lease-renew"
	OperationAccessRequestCreate Operation = "access-request-create"
	OperationAccessRequestStatus Operation = "access-request-status"
	OperationRevokeNotice        Operation = "revoke-notice"
	OperationSessionClose        Operation = "session-close"

	KindRequest  Kind = "request"
	KindResponse Kind = "response"
	KindEvent    Kind = "event"

	CloseRevisionUnsupported  CloseReason = "revision-unsupported"
	CloseRevisionMismatch     CloseReason = "revision-mismatch"
	CloseOperationUnsupported CloseReason = "operation-unsupported"
	ClosePeerRevoked          CloseReason = "peer-revoked"
	CloseProtocolError        CloseReason = "protocol-error"

	RefusalPrincipalUnresolved  RefusalCode = "principal-unresolved"
	RefusalPolicyDenied         RefusalCode = "policy-denied"
	RefusalLeaseExpired         RefusalCode = "lease-expired"
	RefusalLeaseContextMismatch RefusalCode = "lease-context-mismatch"
	RefusalMediaIneligible      RefusalCode = "media-ineligible"
)

var (
	ErrRevisionUnsupported  = errors.New("circle wire revision unsupported")
	ErrRevisionMismatch     = errors.New("circle wire revision mismatch")
	ErrOperationUnsupported = errors.New("circle wire operation unsupported")
	ErrProtocol             = errors.New("circle wire protocol error")
)

type Frame struct {
	Revision        int             `json:"revision"`
	CircleSessionID string          `json:"circleSessionId"`
	Operation       Operation       `json:"operation"`
	Kind            Kind            `json:"kind"`
	RequestID       string          `json:"requestId"`
	Payload         json.RawMessage `json:"payload"`
}

type HelloPayload struct {
	CircleWireRevision int   `json:"circleWireRevision"`
	Supported          []int `json:"supported"`
}

type Negotiation struct {
	Revision    int
	Serve       bool
	CloseReason CloseReason
}

func SupportedRevisions(current int) ([]int, error) {
	if current < 1 {
		return nil, ErrRevisionUnsupported
	}
	return []int{current, current - 1}, nil
}

func Negotiate(ownerCurrent, guestCurrent int) Negotiation {
	owner, err := SupportedRevisions(ownerCurrent)
	if err != nil {
		return Negotiation{CloseReason: CloseRevisionUnsupported}
	}
	guest, err := SupportedRevisions(guestCurrent)
	if err != nil {
		return Negotiation{CloseReason: CloseRevisionUnsupported}
	}
	for _, revision := range owner {
		if slices.Contains(guest, revision) {
			return Negotiation{Revision: revision, Serve: true}
		}
	}
	return Negotiation{CloseReason: CloseRevisionUnsupported}
}

func DecodeFrame(data []byte) (Frame, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var frame Frame
	if err := decoder.Decode(&frame); err != nil {
		return Frame{}, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Frame{}, ErrProtocol
	}
	if err := validateFrame(frame); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func validateFrame(frame Frame) error {
	if frame.Revision < 0 || frame.CircleSessionID == "" || frame.RequestID == "" || len(frame.Payload) == 0 {
		return ErrProtocol
	}
	if !slices.Contains([]Kind{KindRequest, KindResponse, KindEvent}, frame.Kind) {
		return ErrProtocol
	}
	allowedPayload, ok := payloadFields[frame.Operation]
	if !ok {
		return ErrOperationUnsupported
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(frame.Payload, &payload); err != nil || payload == nil {
		return ErrProtocol
	}
	for field := range payload {
		if !slices.Contains(allowedPayload, field) {
			return ErrProtocol
		}
	}
	if raw, ok := payload["reason"]; ok {
		var reason CloseReason
		if json.Unmarshal(raw, &reason) != nil || !slices.Contains(closeReasons, reason) {
			return ErrProtocol
		}
	}
	if raw, ok := payload["refusalCode"]; ok {
		var refusal RefusalCode
		if json.Unmarshal(raw, &refusal) != nil || !slices.Contains(refusalCodes, refusal) {
			return ErrProtocol
		}
	}
	return nil
}

var closeReasons = []CloseReason{
	CloseRevisionUnsupported, CloseRevisionMismatch, CloseOperationUnsupported, ClosePeerRevoked, CloseProtocolError,
}

var refusalCodes = []RefusalCode{
	RefusalPrincipalUnresolved, RefusalPolicyDenied, RefusalLeaseExpired, RefusalLeaseContextMismatch, RefusalMediaIneligible,
}

var payloadFields = map[Operation][]string{
	OperationHello:               {"circleWireRevision", "supported"},
	OperationCatalogRead:         {"circleId", "remoteProfileId", "lease", "refusalCode"},
	OperationPlaybackAuthorize:   {"circleId", "remoteProfileId", "mediaId", "lease", "playbackDecision", "refusalCode"},
	OperationLeaseRenew:          {"lease", "refusalCode"},
	OperationAccessRequestCreate: {"circleId", "remoteProfileId", "mediaId", "refusalCode"},
	OperationAccessRequestStatus: {"accessRequestId", "status", "verdict", "refusalCode"},
	OperationRevokeNotice:        {"circleId"},
	OperationSessionClose:        {"reason"},
}

type Session struct {
	id       string
	revision int
	closed   bool
}

func NewSession(id string, revision int) (*Session, error) {
	if id == "" || revision < 0 {
		return nil, ErrProtocol
	}
	return &Session{id: id, revision: revision}, nil
}

func (s *Session) Revision() int { return s.revision }

func (s *Session) Accept(data []byte, dispatch func(Frame) error) error {
	if s.closed {
		return ErrProtocol
	}
	frame, err := DecodeFrame(data)
	if err != nil {
		s.closed = true
		return err
	}
	if frame.CircleSessionID != s.id || frame.Revision != s.revision {
		s.closed = true
		return ErrRevisionMismatch
	}
	return dispatch(frame)
}
