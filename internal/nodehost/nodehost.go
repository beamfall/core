// Package nodehost owns the Origin-Node enrollment lifecycle of ADR-0123 §5
// (decision 5 + decision 11) and the ADR-0063 origin-node Compatibility
// Handshake gate: operator-initiated enrollment over cert-pinning mTLS, the
// handshake/revision gate that fails closed, and per-node revoke/rotate.
//
// An Origin Node is a DISTINCT node class enrolled via cert-pinning — NOT a
// store.DeviceRole (ADR-0123 §5 is explicit: it is infrastructure, not a
// person's VIEWER/MANAGER device). The trust is symmetric: the main pins the
// node's certificate SPKI and the node pins the main's SPKI, with a
// WireGuard-style mutual operator confirmation of both displayed fingerprints
// closing the TOFU/LAN-MITM window at pairing time.
//
// This package is the enrollment/trust LOGIC over real x509 material; the
// caller owns the mTLS socket and passes the verified peer leaf, extracted at
// the PeerLeafFromTLS boundary seam from the completed mutual-TLS handshake's
// *tls.ConnectionState (RequireAndVerifyClientCert). The HTTP/relay endpoint
// that runs the handshake is a separate transport-wiring ticket; this package
// is a stdlib-only leaf (no internal deps), mirroring internal/serverrole.
//
// Scope boundary: Revoke here is credential/session revoke only — it stops new
// sessions/serving. The ADR-0123 decision-11 catalog end-state (tombstone vs
// retain-as-Unavailable) is deferred to a later ticket; this package claims no
// catalog decommission effect.
package nodehost

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

// Boundary sentinels. Happy-path logic assumes frozen, valid, non-stale data;
// every disruption is caught at this boundary and mapped to one of these.
var (
	// ErrNotOperator means a non-operator attempted an operator-only action
	// (mint code, revoke, rotate) — all node lifecycle mutations are explicit
	// operator actions.
	ErrNotOperator = errors.New("nodehost: operator action required")
	// ErrEnrollCodeInvalid means the enrollment code is unknown, expired, or
	// already consumed.
	ErrEnrollCodeInvalid = errors.New("nodehost: enrollment code invalid")
	// ErrFingerprintsUnconfirmed means the operator has not confirmed the
	// displayed main+node SPKI fingerprints on both machines.
	ErrFingerprintsUnconfirmed = errors.New("nodehost: operator fingerprint confirmation required")
	// ErrNodeCertInvalid means the node presented no verified mTLS leaf, so no
	// SPKI can be derived to pin.
	ErrNodeCertInvalid = errors.New("nodehost: node certificate invalid")
	// ErrNotMutualTLS means the connection is not a completed mTLS handshake with
	// a client-presented, transport-verified peer certificate chain — enrollment
	// and node-host sessions require mutual TLS.
	ErrNotMutualTLS = errors.New("nodehost: mutual TLS with a verified peer certificate required")
	// ErrNodeSubjectMismatch means the Client Contract's declared node SPKI does
	// not match the SPKI of the authenticated mTLS leaf — the decision cannot be
	// bound to the authenticated node subject (ADR-0063 decision binding).
	ErrNodeSubjectMismatch = errors.New("nodehost: node certificate subject mismatch")
	// ErrMainSubjectMismatch means an inbound main→node operation
	// (request-file-mutation, reap-confirmation ack) presented a TLS leaf whose
	// SPKI does not match the main SPKI pinned at enrollment — the main could not
	// be authenticated against the node's pin, so the operation is rejected
	// (ADR-0123 §5/§9: the main→node leg authenticates the main against the
	// enrollment-time pin so a local attacker cannot spoof a mutation/reap-ack).
	ErrMainSubjectMismatch = errors.New("nodehost: main certificate subject mismatch")
	// ErrMainNotPinned means the node holds no enrollment-time main SPKI pin, so
	// it cannot authenticate any inbound main→node operation — fail closed rather
	// than accept an unauthenticated main.
	ErrMainNotPinned = errors.New("nodehost: no main SPKI pin to authenticate against")
	// ErrMainPinUnavailable means the main server has no served-cert SPKI to hand
	// back as the symmetric pin, so enrollment fails closed (no pin to confirm).
	ErrMainPinUnavailable = errors.New("nodehost: main SPKI pin unavailable")
	// ErrIncompatible means the Compatibility Handshake did not yield a servable
	// decision (server-too-old / client-too-old): the node cannot enroll.
	ErrIncompatible = errors.New("nodehost: compatibility handshake failed")
	// ErrEnrollmentHandshakeUnsupported means the pre-contract enrollment
	// bootstrap version is unknown. It fails closed before session negotiation
	// because no node/main pins exist yet.
	ErrEnrollmentHandshakeUnsupported = errors.New("nodehost: enrollment handshake version unsupported")
	// ErrDownOperationOutsideNodeConnection means a down op arrived outside the
	// node-initiated multiplexed connection pinned by ADR-0129.
	ErrDownOperationOutsideNodeConnection = errors.New("nodehost: down operation outside node-initiated multiplexed connection")
	// ErrNodeNotFound means no enrolled node has the given id.
	ErrNodeNotFound = errors.New("nodehost: node not found")
	// ErrNodeRevoked means the node has been revoked and cannot serve.
	ErrNodeRevoked = errors.New("nodehost: node revoked")
	// ErrNodeKeyRotationRequiresEnrollment means a node presented a new SPKI for
	// certificate renewal. Routine renewal must keep the keypair stable; a
	// deliberate node key rotation is enrollment-grade and needs pairing again.
	ErrNodeKeyRotationRequiresEnrollment = errors.New("nodehost: node key rotation requires enrollment")
)

// Revision is a node-host contract revision. The start-of-session
// negotiation fails closed on a revision outside the main's supported set.
type Revision int

// Decision is the ADR-0063 Compatibility Decision, bound to the origin-node
// surface with infrastructure (non-UX) behavior.
type Decision string

const (
	// DecisionCompatible: the node may enroll and serve normally.
	DecisionCompatible Decision = "compatible"
	// DecisionDegraded: the node may serve only the proven-safe narrowed subset.
	DecisionDegraded Decision = "degraded"
	// DecisionServerTooOld: the node binary is newer than the main supports →
	// refuse-serving.
	DecisionServerTooOld Decision = "server-too-old"
	// DecisionClientTooOld: the main can no longer safely serve this node →
	// refuse-serving (also the fail-closed catch-all for unknown/unverified).
	DecisionClientTooOld Decision = "client-too-old"
)

// originNodeSurface is the only Client Contract surface this gate accepts.
// Any other surface fails closed.
const originNodeSurface = "origin-node"

// EnrollmentHandshakeVersion is the pre-contract node-enroll bootstrap version.
// It is checked before normal session revision negotiation because no pins exist
// yet; unknown versions fail closed.
const EnrollmentHandshakeVersion = 1

// Admission is the per-session serving outcome. Degraded is first-class: a
// degraded node is admitted to narrowed serving, never full serving and never
// silently refused (ADR-0063 degraded contract).
type Admission string

const (
	// AdmitServing: full serving.
	AdmitServing Admission = "serving"
	// AdmitDegraded: narrowed, proven-safe serving only.
	AdmitDegraded Admission = "degraded"
	// AdmitRefuse: the session is refused (fail-closed).
	AdmitRefuse Admission = "refuse-serving"
)

// decisionAdmission maps a servable Decision to its session Admission. Refuse
// decisions are absent; lookup miss ⇒ AdmitRefuse, so the table is fail-closed.
var decisionAdmission = map[Decision]Admission{
	DecisionCompatible: AdmitServing,
	DecisionDegraded:   AdmitDegraded,
}

// ClientContract is the origin-node Client Contract a node presents at
// enrollment and that the main re-evaluates each session. Every
// field is fail-closed when absent/unknown/unverifiable.
type ClientContract struct {
	// Surface must equal originNodeSurface; any other value fails closed.
	Surface string
	// BuildID identifies the node binary build; checked against revocations, not
	// raw product version. Empty fails closed.
	BuildID string
	// CoreContractRevRange is the node's supported core-contract revision range
	// [min,max] inclusive; compared against the main's supported set.
	CoreContractRevRange [2]Revision
	// FixedVocabularies is the set of fixed vocabularies the node supports.
	FixedVocabularies []string
	// DistributionVerified reports that the node's distribution/enrollment state
	// is server-verified; false fails closed (unverifiable distribution).
	DistributionVerified bool
	// NodeCertSPKI is the node's claimed certificate SPKI pin; it must equal the
	// SPKI derived from the authenticated mTLS leaf (subject binding).
	NodeCertSPKI string
}

// CompatibilityPolicy is the main-side known state a ClientContract is
// evaluated against. It is supplied at enroll and re-supplied (current) at each
// session, so a decision is never trusted from enrollment time (ADR-0063
// invalidation: a build revoked or a policy change after enrollment fails an
// already-enrolled node closed).
type CompatibilityPolicy struct {
	// SupportedRevisions is the main's supported node-host contract revisions.
	SupportedRevisions map[Revision]bool
	// RequiredVocabularies must all be present, else the node fails closed.
	RequiredVocabularies []string
	// FullServiceVocabularies must all be present for full serving; required
	// present but a full-service vocab missing ⇒ Degraded (narrowed).
	FullServiceVocabularies []string
	// RevokedBuilds is the set of known-bad build ids; a member fails closed.
	RevokedBuilds map[string]bool
	// Generation is the policy generation; stored on the node so a generation
	// change is observable, and re-evaluation runs against the current policy.
	Generation int
}

// EnrolledNode is the persisted record of an enrolled Origin Node. It carries
// enough to RE-EVALUATE compatibility against the current policy at session
// time (ADR-0063 decision binding/invalidation), not trust the stale decision.
type EnrolledNode struct {
	ID                 string
	NodeCertSPKI       string // derived from the authenticated mTLS leaf
	MainSPKIPin        string // the symmetric pin handed back to the node
	BuildID            string
	Contract           ClientContract
	Admission          Admission // enroll-time admission (Serving|Degraded)
	State              NodeState
	DecisionGeneration int // the policy generation the enroll decision was bound to
}

// NodeState is the lifecycle posture of an enrolled node.
type NodeState string

const (
	// StateActive: the node is enrolled and eligible to serve.
	StateActive NodeState = "active"
	// StateRevoked: the node has been revoked; it can open no new sessions.
	StateRevoked NodeState = "revoked"
)

// MainSPKIProvider supplies the main server's served-cert SPKI pin to hand back
// to the node as the symmetric pin. It mirrors
// remoteaccess.DirectHTTPS.ServerSPKIPin: when no certificate is served yet it
// returns ("", err), which enrollment treats as ErrMainPinUnavailable.
type MainSPKIProvider interface {
	ServerSPKIPin(ctx context.Context) (string, error)
}

// Registry persists enrolled nodes and one-time enrollment codes. The default
// in-memory implementation keeps this package a pure leaf; a later ticket backs
// it with the ADR-0123 node-enrollment migration without changing this contract.
type Registry interface {
	PutCode(code string, expiresAt time.Time) error
	ConsumeCodeAndPutNode(code string, now time.Time, n EnrolledNode) (bool, error)
	PutNode(n EnrolledNode) error
	Node(id string) (EnrolledNode, bool, error)
}

// EnrollRequest is one node's redemption of an enrollment code over mTLS.
type EnrollRequest struct {
	// HandshakeVersion is the explicit pre-contract enrollment bootstrap version.
	HandshakeVersion int
	// Code is the one-time enrollment code minted by an operator.
	Code string
	// Contract is the origin-node Client Contract the node presents.
	Contract ClientContract
	// NodeLeaf is the node's verified mTLS peer certificate (the SPKI is derived
	// from it; a caller-supplied SPKI string is never trusted on its own).
	NodeLeaf *x509.Certificate
	// OperatorConfirmedFingerprints is the WireGuard-style mutual confirmation
	// that the operator compared the displayed main+node SPKI on both machines.
	OperatorConfirmedFingerprints bool
}

// EnrollResult is what enrollment returns to the caller (and, via MainSPKIPin,
// to the enrolling node).
type EnrollResult struct {
	Node        EnrolledNode
	MainSPKIPin string
}

// Service owns the enrollment lifecycle. It holds no transport; the caller
// authenticates the mTLS connection and passes the verified leaf.
type Service struct {
	now      func() time.Time
	newID    func() (string, error)
	newCode  func() (string, error)
	mainSPKI MainSPKIProvider

	mu  sync.Mutex
	reg Registry
}

// NewService builds a Service over the default in-memory registry.
func NewService(now func() time.Time, mainSPKI MainSPKIProvider) *Service {
	return NewServiceWithRegistry(now, mainSPKI, newMemRegistry())
}

// NewServiceWithRegistry builds a Service over a caller-supplied durable
// Registry. A nil registry falls back to the in-memory test/default registry.
func NewServiceWithRegistry(now func() time.Time, mainSPKI MainSPKIProvider, reg Registry) *Service {
	if now == nil {
		now = time.Now
	}
	if reg == nil {
		reg = newMemRegistry()
	}
	return &Service{
		now:      now,
		newID:    randomID,
		newCode:  randomCode,
		mainSPKI: mainSPKI,
		reg:      reg,
	}
}

// MintEnrollmentCode mints a one-time enrollment code. Operator-initiated: a
// non-operator caller is refused.
func (s *Service) MintEnrollmentCode(operator bool, ttl time.Duration) (string, error) {
	if !operator {
		return "", ErrNotOperator
	}
	code, err := s.newCode()
	if err != nil {
		return "", err
	}
	expiresAt := s.now().Add(ttl).UTC()
	s.mu.Lock()
	err = s.reg.PutCode(code, expiresAt)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	return code, nil
}

// Enroll redeems an enrollment code and, if every boundary check passes,
// enrolls the node as a distinct node class. The checks run in fail-closed
// order so an unknown/unverified/subject-mismatched contract can never reach a
// servable decision. A Compatible or Degraded decision enrolls (Admission set
// accordingly); a refuse decision is rejected with ErrIncompatible.
func (s *Service) Enroll(ctx context.Context, req EnrollRequest, policy CompatibilityPolicy) (EnrollResult, error) {
	if req.HandshakeVersion != EnrollmentHandshakeVersion {
		return EnrollResult{}, ErrEnrollmentHandshakeUnsupported
	}
	if !req.OperatorConfirmedFingerprints {
		return EnrollResult{}, ErrFingerprintsUnconfirmed
	}
	nodeSPKI, err := leafSPKIPin(req.NodeLeaf)
	if err != nil {
		return EnrollResult{}, err
	}
	if err := bindSubject(req.Contract.NodeCertSPKI, nodeSPKI); err != nil {
		return EnrollResult{}, err
	}
	mainPin, err := s.mainPin(ctx)
	if err != nil {
		return EnrollResult{}, err
	}
	admission, err := admissionFor(EvaluateCompatibility(req.Contract, policy))
	if err != nil {
		return EnrollResult{}, err
	}
	id, err := s.newID()
	if err != nil {
		return EnrollResult{}, err
	}
	node, err := s.commitEnroll(req, nodeSPKI, mainPin, policy, admission, id)
	if err != nil {
		return EnrollResult{}, err
	}
	if node.ID == "" {
		return EnrollResult{}, ErrEnrollCodeInvalid
	}
	return EnrollResult{Node: node, MainSPKIPin: mainPin}, nil
}

// commitEnroll consumes the code and persists the node atomically under the
// lock, so a concurrent redemption of the same one-time code cannot double-spend
// it. A consumed/expired code yields a zero EnrolledNode (no id).
func (s *Service) commitEnroll(req EnrollRequest, nodeSPKI, mainPin string, policy CompatibilityPolicy, admission Admission, id string) (EnrolledNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node := EnrolledNode{
		ID:                 id,
		NodeCertSPKI:       nodeSPKI,
		MainSPKIPin:        mainPin,
		BuildID:            req.Contract.BuildID,
		Contract:           req.Contract,
		Admission:          admission,
		State:              StateActive,
		DecisionGeneration: policy.Generation,
	}
	ok, err := s.reg.ConsumeCodeAndPutNode(normalizeCode(req.Code), s.now(), node)
	if err != nil {
		return EnrolledNode{}, err
	}
	if !ok {
		return EnrolledNode{}, nil
	}
	return node, nil
}

// SessionRequest opens a node-host session over the enrollment mTLS.
type SessionRequest struct {
	// NodeID identifies the enrolled node.
	NodeID string
	// Revision is the node-host contract revision the node offers at
	// start-of-session.
	Revision Revision
	// PresentedLeaf is the node's verified mTLS peer certificate for this
	// session. Its derived SPKI must match the pin captured at enrollment — the
	// cert-pinning enforcement at session time. nil/mismatched fails closed.
	PresentedLeaf *x509.Certificate
}

// OpenSession is the per-session admission gate. It fails closed,
// in order, on: an unknown node; a revoked node; a presented mTLS leaf whose
// SPKI does not match the pin captured at enrollment (cert-pinning enforcement —
// a session cannot serve unless the caller still holds the pinned key); an
// unsupported start-of-session revision; and a stale compatibility decision —
// the stored Client Contract is RE-EVALUATED against the CURRENT policy, so a
// build revoked or a policy change after enrollment fails an already-enrolled
// node closed rather than serving on a stale decision.
func (s *Service) OpenSession(req SessionRequest, policy CompatibilityPolicy) (Admission, error) {
	s.mu.Lock()
	node, ok, err := s.reg.Node(req.NodeID)
	s.mu.Unlock()
	if err != nil {
		return AdmitRefuse, err
	}
	if !ok {
		return AdmitRefuse, ErrNodeNotFound
	}
	if node.State == StateRevoked {
		return AdmitRefuse, ErrNodeRevoked
	}
	if err := verifyPinnedLeaf(req.PresentedLeaf, node.NodeCertSPKI); err != nil {
		return AdmitRefuse, err
	}
	if !policy.SupportedRevisions[req.Revision] {
		return AdmitRefuse, nil
	}
	admission, err := admissionFor(EvaluateCompatibility(node.Contract, policy))
	if err != nil {
		return AdmitRefuse, nil
	}
	return admission, nil
}

// verifyPinnedLeaf fails closed unless the presented leaf's derived SPKI equals
// the pin captured at enrollment — the session-time cert-pinning check.
func verifyPinnedLeaf(presented *x509.Certificate, pinnedSPKI string) error {
	spki, err := leafSPKIPin(presented)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(spki), []byte(pinnedSPKI)) != 1 {
		return ErrNodeSubjectMismatch
	}
	return nil
}

// MainAuthenticator is the NODE-side guard for the main→node leg of the node-host
// contract. It carries the main SPKI the node pinned at
// enrollment (mirroring ADR-0046 §2.1's client→server pin) and authenticates
// every inbound main→node operation — `request-file-mutation`,
// reap-confirmation ack, and the other §9 downward ops — against that pin. A
// spoofed operation from a local attacker presents a different (or no) TLS leaf,
// fails the pin, and is rejected; only the pinned main is accepted. The §8 Trash
// guarantee (a reap-ack cannot be forged) depends on this leg being authenticated.
//
// It is the symmetric counterpart of OpenSession's verifyPinnedLeaf: that guards
// the node→main leg by the node's pin; this guards the main→node leg by the
// main's pin. The node builds it from the MainSPKIPin returned at enrollment; the
// caller owns the mTLS socket and passes the main's verified peer leaf, extracted
// at the PeerLeafFromTLS boundary seam.
type MainAuthenticator struct {
	pinnedMainSPKI string
}

// DownOperationRequest authenticates a main-originated down op carried over the
// node-initiated multiplexed connection. The node runs no listener for this
// contract, so a pinned main leaf alone is not enough.
type DownOperationRequest struct {
	PresentedMainLeaf        *x509.Certificate
	NodeInitiatedMultiplexed bool
}

// NewMainAuthenticator builds the node-side guard from the main SPKI pin the node
// captured at enrollment (EnrollResult.MainSPKIPin / EnrolledNode.MainSPKIPin).
// An empty pin yields a guard that fails every operation closed with
// ErrMainNotPinned, so a node that never captured a pin can never accept an
// unauthenticated main.
func NewMainAuthenticator(pinnedMainSPKI string) *MainAuthenticator {
	return &MainAuthenticator{pinnedMainSPKI: strings.TrimSpace(pinnedMainSPKI)}
}

// AuthenticateDownOperation fails closed unless the down op rides the
// node-initiated multiplexed connection and the main's presented TLS leaf derives
// to the SPKI the node pinned at enrollment.
func (a *MainAuthenticator) AuthenticateDownOperation(req DownOperationRequest) error {
	if !req.NodeInitiatedMultiplexed {
		return ErrDownOperationOutsideNodeConnection
	}
	if a.pinnedMainSPKI == "" {
		return ErrMainNotPinned
	}
	spki, err := leafSPKIPin(req.PresentedMainLeaf)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(spki), []byte(a.pinnedMainSPKI)) != 1 {
		return ErrMainSubjectMismatch
	}
	return nil
}

// AuthenticateMain preserves the old one-argument boundary as fail-closed. Down
// ops must use AuthenticateDownOperation with NodeInitiatedMultiplexed=true.
func (a *MainAuthenticator) AuthenticateMain(presentedMainLeaf *x509.Certificate) error {
	return a.AuthenticateDownOperation(DownOperationRequest{PresentedMainLeaf: presentedMainLeaf})
}

// RepinMain is the node-side recovery path after an operator deliberately
// rotates the main server key. It never TOFUs: without the same mutual
// fingerprint confirmation used by enrollment, the old pin remains in force.
func (a *MainAuthenticator) RepinMain(operatorConfirmedFingerprints bool, presentedMainLeaf *x509.Certificate) error {
	if !operatorConfirmedFingerprints {
		return ErrFingerprintsUnconfirmed
	}
	spki, err := leafSPKIPin(presentedMainLeaf)
	if err != nil {
		return err
	}
	a.pinnedMainSPKI = spki
	return nil
}

// Revoke marks an enrolled node revoked (operator action). It is idempotent on
// an already-revoked node. Credential/session scope only — no catalog effect.
func (s *Service) Revoke(operator bool, nodeID string) error {
	if !operator {
		return ErrNotOperator
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok, err := s.reg.Node(nodeID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNodeNotFound
	}
	node.State = StateRevoked
	return s.reg.PutNode(node)
}

// RenewalResult is the refreshed credential binding a clean node certificate
// renewal returns: the node's stable SPKI pin and the unchanged main SPKI pin
// the node keeps. It preserves the node identity (same NodeID).
type RenewalResult struct {
	NodeID       string
	NodeCertSPKI string // unchanged; SPKI survives routine cert renewal
	MainSPKIPin  string // unchanged; re-handed so renewal is self-contained
}

// RenewNodeCertificate records a routine node certificate renewal (operator
// action). The presented leaf must carry the same SPKI pinned at enrollment; a
// new node keypair is refused because deliberate node key rotation is an
// enrollment-grade event, not an in-place lifecycle update.
func (s *Service) RenewNodeCertificate(operator bool, nodeID string, newLeaf *x509.Certificate) (RenewalResult, error) {
	if !operator {
		return RenewalResult{}, ErrNotOperator
	}
	spki, err := leafSPKIPin(newLeaf)
	if err != nil {
		return RenewalResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok, err := s.reg.Node(nodeID)
	if err != nil {
		return RenewalResult{}, err
	}
	if !ok {
		return RenewalResult{}, ErrNodeNotFound
	}
	if node.State == StateRevoked {
		return RenewalResult{}, ErrNodeRevoked
	}
	if subtle.ConstantTimeCompare([]byte(spki), []byte(node.NodeCertSPKI)) != 1 {
		return RenewalResult{}, ErrNodeKeyRotationRequiresEnrollment
	}
	if err := s.reg.PutNode(node); err != nil {
		return RenewalResult{}, err
	}
	return RenewalResult{NodeID: node.ID, NodeCertSPKI: node.NodeCertSPKI, MainSPKIPin: node.MainSPKIPin}, nil
}

// MainRepinResult is the refreshed main identity binding after an
// operator-confirmed main-SPKI re-pin. Node identity and node SPKI are unchanged.
type MainRepinResult struct {
	NodeID       string
	NodeCertSPKI string
	MainSPKIPin  string
}

// RepinMain updates the enrolled node's main-SPKI pin after a deliberate main
// key rotation. It requires the same operator mutual-fingerprint confirmation as
// enrollment and never revokes or re-enrolls the node.
func (s *Service) RepinMain(operator bool, nodeID string, newMainLeaf *x509.Certificate, operatorConfirmedFingerprints bool) (MainRepinResult, error) {
	if !operator {
		return MainRepinResult{}, ErrNotOperator
	}
	if !operatorConfirmedFingerprints {
		return MainRepinResult{}, ErrFingerprintsUnconfirmed
	}
	mainSPKI, err := leafSPKIPin(newMainLeaf)
	if err != nil {
		return MainRepinResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok, err := s.reg.Node(nodeID)
	if err != nil {
		return MainRepinResult{}, err
	}
	if !ok {
		return MainRepinResult{}, ErrNodeNotFound
	}
	if node.State == StateRevoked {
		return MainRepinResult{}, ErrNodeRevoked
	}
	node.MainSPKIPin = mainSPKI
	if err := s.reg.PutNode(node); err != nil {
		return MainRepinResult{}, err
	}
	return MainRepinResult{NodeID: node.ID, NodeCertSPKI: node.NodeCertSPKI, MainSPKIPin: mainSPKI}, nil
}

// mainPin fetches the main server's SPKI pin, mapping an empty/error result to
// ErrMainPinUnavailable so enrollment fails closed without a symmetric pin.
func (s *Service) mainPin(ctx context.Context) (string, error) {
	if s.mainSPKI == nil {
		return "", ErrMainPinUnavailable
	}
	pin, err := s.mainSPKI.ServerSPKIPin(ctx)
	if err != nil || strings.TrimSpace(pin) == "" {
		return "", ErrMainPinUnavailable
	}
	return pin, nil
}

// EvaluateCompatibility is the ADR-0063 origin-node Compatibility Handshake. It
// is fail-closed by construction: every unknown/unverified input is an explicit
// refuse branch, so the zero-value contract fails closed. Degraded is reachable
// only via the final full-service-vocabulary check.
func EvaluateCompatibility(c ClientContract, p CompatibilityPolicy) Decision {
	if c.Surface != originNodeSurface {
		return DecisionClientTooOld
	}
	if c.BuildID == "" || p.RevokedBuilds[c.BuildID] {
		return DecisionClientTooOld
	}
	if !c.DistributionVerified {
		return DecisionClientTooOld
	}
	if !containsAll(c.FixedVocabularies, p.RequiredVocabularies) {
		return DecisionClientTooOld
	}
	if d, mismatched := revisionDecision(c.CoreContractRevRange, p.SupportedRevisions); mismatched {
		return d
	}
	if !containsAll(c.FixedVocabularies, p.FullServiceVocabularies) {
		return DecisionDegraded
	}
	return DecisionCompatible
}

// revisionDecision reports the skew decision for a node's supported revision
// range against the main's supported set. mismatched=false means the ranges
// overlap (proceed). A node range entirely above the main's max ⇒ ServerTooOld;
// entirely below the main's min ⇒ ClientTooOld.
func revisionDecision(nodeRange [2]Revision, supported map[Revision]bool) (Decision, bool) {
	lo, hi := minMaxSupported(supported)
	if len(supported) == 0 {
		return DecisionClientTooOld, true
	}
	if nodeRange[0] > hi {
		return DecisionServerTooOld, true
	}
	if nodeRange[1] < lo {
		return DecisionClientTooOld, true
	}
	if !rangesOverlapSupported(nodeRange, supported) {
		return DecisionClientTooOld, true
	}
	return DecisionCompatible, false
}

// admissionFor maps a Decision to a session Admission, returning ErrIncompatible
// for any refuse decision (the lookup-table miss is the fail-closed default).
func admissionFor(d Decision) (Admission, error) {
	admission, ok := decisionAdmission[d]
	if !ok {
		return AdmitRefuse, ErrIncompatible
	}
	return admission, nil
}

// bindSubject binds the decision to the authenticated node subject: the
// contract's declared SPKI must be present and equal the leaf-derived SPKI.
func bindSubject(declared, derived string) error {
	if declared == "" {
		return ErrNodeSubjectMismatch
	}
	if subtle.ConstantTimeCompare([]byte(declared), []byte(derived)) != 1 {
		return ErrNodeSubjectMismatch
	}
	return nil
}

// PeerLeafFromTLS is the mTLS boundary seam: it extracts the node's verified
// peer leaf from a completed TLS handshake's connection state. The caller's TLS
// listener performs the mutual-TLS handshake (RequireAndVerifyClientCert) and
// hands the resulting *tls.ConnectionState here; this is the single point where
// the cert-pinning mTLS connection becomes the leaf the enrollment/session logic
// pins against. It fails closed (ErrNotMutualTLS) unless the handshake is
// complete and a transport-verified peer chain is present.
func PeerLeafFromTLS(state *tls.ConnectionState) (*x509.Certificate, error) {
	if state == nil || !state.HandshakeComplete {
		return nil, ErrNotMutualTLS
	}
	if len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return nil, ErrNotMutualTLS
	}
	return state.VerifiedChains[0][0], nil
}

// PeerLeafFromPinnedTLS is the boundary seam for the self-signed SPKI-pinning
// transport. Under tls.RequireAnyClientCert there
// is no node CA to build a VerifiedChain, so the node's self-signed leaf arrives
// in PeerCertificates[0], not VerifiedChains — SPKI pinning in OpenSession /
// verifyPinnedLeaf is the real gate, not TLS chain trust. It fails closed
// (ErrNotMutualTLS) unless the handshake is complete and a peer certificate was
// presented. Distinct from PeerLeafFromTLS, which requires a CA-VerifiedChain.
func PeerLeafFromPinnedTLS(state *tls.ConnectionState) (*x509.Certificate, error) {
	if state == nil || !state.HandshakeComplete {
		return nil, ErrNotMutualTLS
	}
	if len(state.PeerCertificates) == 0 || state.PeerCertificates[0] == nil {
		return nil, ErrNotMutualTLS
	}
	return state.PeerCertificates[0], nil
}

// VerifyMainLeafSPKI is the NODE-side main-SPKI pin check: the
// node's dialer sets it as tls.Config.VerifyPeerCertificate, failing the
// handshake closed unless the served main leaf (rawCerts[0]) derives to the
// MainSPKIPin the node captured at enrollment. It is the dial-path counterpart
// of MainAuthenticator's constant-time pin comparison, operating on raw DER
// (the only form available in the VerifyPeerCertificate callback). An empty pin
// fails closed with ErrMainNotPinned so a node that never captured a pin can
// never accept an unauthenticated main.
func VerifyMainLeafSPKI(rawCerts [][]byte, pinnedMainSPKI string) error {
	if strings.TrimSpace(pinnedMainSPKI) == "" {
		return ErrMainNotPinned
	}
	if len(rawCerts) == 0 {
		return ErrMainSubjectMismatch
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return ErrMainSubjectMismatch
	}
	spki, err := leafSPKIPin(leaf)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(spki), []byte(pinnedMainSPKI)) != 1 {
		return ErrMainSubjectMismatch
	}
	return nil
}

// leafSPKIPin derives the HPKP-form SPKI pin from a verified mTLS leaf
// ("sha256-"+base64(SHA-256(SubjectPublicKeyInfo))), matching
// remoteaccess.spkiPin so a node pins the same identity the main serves.
func leafSPKIPin(leaf *x509.Certificate) (string, error) {
	if leaf == nil || len(leaf.RawSubjectPublicKeyInfo) == 0 {
		return "", ErrNodeCertInvalid
	}
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:]), nil
}

// containsAll reports whether have ⊇ want.
func containsAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, v := range have {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// minMaxSupported returns the lowest and highest supported revision.
func minMaxSupported(supported map[Revision]bool) (Revision, Revision) {
	first := true
	var lo, hi Revision
	for r := range supported {
		if first || r < lo {
			lo = r
		}
		if first || r > hi {
			hi = r
		}
		first = false
	}
	return lo, hi
}

// rangesOverlapSupported reports whether any supported revision falls within the
// node's inclusive range.
func rangesOverlapSupported(nodeRange [2]Revision, supported map[Revision]bool) bool {
	for r := range supported {
		if r >= nodeRange[0] && r <= nodeRange[1] {
			return true
		}
	}
	return false
}

func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
