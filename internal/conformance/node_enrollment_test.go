package conformance

// ADR-0123 §5 / ADR-0063 (origin-node amendment) Origin-Node enrollment
// conformance, wired to the REAL internal/nodehost implementation.
//
// Where node_host_contract_test.go is the spec-side reference for the closed
// vocabulary + proxy hot path, this file binds the runtime enrollment
// lifecycle to the contract so the two cannot drift: enrollment is gated on a
// Compatible/Degraded Compatibility Handshake, a revoked node is refused, and an
// unsupported start-of-session revision fails closed (never serving).

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/beamfall/core/internal/nodehost"
)

type fixedMainPin struct{ pin string }

func (f fixedMainPin) ServerSPKIPin(context.Context) (string, error) { return f.pin, nil }

func enrollmentLeaf(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "origin-node"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1<<31, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func leafPin(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

func enrollmentPolicy() nodehost.CompatibilityPolicy {
	return nodehost.CompatibilityPolicy{
		SupportedRevisions:      map[nodehost.Revision]bool{1: true},
		RequiredVocabularies:    []string{"core"},
		FullServiceVocabularies: []string{"transcode"},
		RevokedBuilds:           map[string]bool{},
		Generation:              1,
	}
}

func compatibleEnrollment(leaf *x509.Certificate) nodehost.EnrollRequest {
	return nodehost.EnrollRequest{
		// Code is filled by the caller after minting.
		HandshakeVersion: nodehost.EnrollmentHandshakeVersion,
		Contract: nodehost.ClientContract{
			Surface:              "origin-node",
			BuildID:              "b1",
			CoreContractRevRange: [2]nodehost.Revision{1, 1},
			FixedVocabularies:    []string{"core", "transcode"},
			DistributionVerified: true,
			NodeCertSPKI:         leafPin(leaf),
		},
		NodeLeaf:                      leaf,
		OperatorConfirmedFingerprints: true,
	}
}

func TestNodeEnrollmentUnknownBootstrapVersionFailsClosed(t *testing.T) {
	s := nodehost.NewService(func() time.Time { return time.Unix(100, 0) }, fixedMainPin{pin: "sha256-MAIN"})
	leaf := enrollmentLeaf(t)
	code, err := s.MintEnrollmentCode(true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req := compatibleEnrollment(leaf)
	req.Code = code
	req.HandshakeVersion = nodehost.EnrollmentHandshakeVersion + 1
	if _, err := s.Enroll(context.Background(), req, enrollmentPolicy()); !errors.Is(err, nodehost.ErrEnrollmentHandshakeUnsupported) {
		t.Fatalf("unknown bootstrap version enroll err = %v, want ErrEnrollmentHandshakeUnsupported", err)
	}
}

// TestNodeEnrollmentGatedOnCompatibilityHandshake binds ADR-0123 §5: a node that
// clears the handshake enrolls and serves; a node failing it cannot enroll.
func TestNodeEnrollmentGatedOnCompatibilityHandshake(t *testing.T) {
	s := nodehost.NewService(func() time.Time { return time.Unix(100, 0) }, fixedMainPin{pin: "sha256-MAIN"})
	leaf := enrollmentLeaf(t)
	policy := enrollmentPolicy()

	code, err := s.MintEnrollmentCode(true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req := compatibleEnrollment(leaf)
	req.Code = code
	res, err := s.Enroll(context.Background(), req, policy)
	if err != nil {
		t.Fatalf("compatible enroll: %v", err)
	}
	if res.Node.State != nodehost.StateActive || res.Node.Admission != nodehost.AdmitServing {
		t.Fatalf("enrolled node = %+v, want active/serving", res.Node)
	}

	// A node whose contract fails the handshake (revision above supported) cannot enroll.
	badLeaf := enrollmentLeaf(t)
	badCode, _ := s.MintEnrollmentCode(true, time.Minute)
	bad := compatibleEnrollment(badLeaf)
	bad.Code = badCode
	bad.Contract.CoreContractRevRange = [2]nodehost.Revision{9, 9}
	if _, err := s.Enroll(context.Background(), bad, policy); !errors.Is(err, nodehost.ErrIncompatible) {
		t.Fatalf("handshake-failing enroll err = %v, want ErrIncompatible (cannot serve)", err)
	}
}

// TestNodeRevokeStopsNewSessions binds ADR-0123 §5/decision 11 (credential
// revoke): a revoked node opens no new sessions.
func TestNodeRevokeStopsNewSessions(t *testing.T) {
	s := nodehost.NewService(func() time.Time { return time.Unix(100, 0) }, fixedMainPin{pin: "sha256-MAIN"})
	leaf := enrollmentLeaf(t)
	policy := enrollmentPolicy()
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	req := compatibleEnrollment(leaf)
	req.Code = code
	res, err := s.Enroll(context.Background(), req, policy)
	if err != nil {
		t.Fatal(err)
	}
	if adm, err := s.OpenSession(nodehost.SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, policy); err != nil || adm != nodehost.AdmitServing {
		t.Fatalf("pre-revoke session adm=%q err=%v, want serving", adm, err)
	}
	if err := s.Revoke(true, res.Node.ID); err != nil {
		t.Fatal(err)
	}
	adm, err := s.OpenSession(nodehost.SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, policy)
	if !errors.Is(err, nodehost.ErrNodeRevoked) || adm != nodehost.AdmitRefuse {
		t.Fatalf("post-revoke session adm=%q err=%v, want refuse + ErrNodeRevoked", adm, err)
	}
}

// TestNodeSessionRevisionNegotiationFailsClosed binds ADR-0129 §2: an
// already-enrolled node whose start-of-session revision is unsupported drops to
// refuse-serving and never serves.
func TestNodeSessionRevisionNegotiationFailsClosed(t *testing.T) {
	s := nodehost.NewService(func() time.Time { return time.Unix(100, 0) }, fixedMainPin{pin: "sha256-MAIN"})
	leaf := enrollmentLeaf(t)
	policy := enrollmentPolicy()
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	req := compatibleEnrollment(leaf)
	req.Code = code
	res, err := s.Enroll(context.Background(), req, policy)
	if err != nil {
		t.Fatal(err)
	}
	adm, err := s.OpenSession(nodehost.SessionRequest{NodeID: res.Node.ID, Revision: 2, PresentedLeaf: leaf}, policy) // revision 2 unsupported
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if adm == nodehost.AdmitServing {
		t.Fatal("unsupported revision proceeded to serving; must fail closed")
	}
	if adm != nodehost.AdmitRefuse {
		t.Fatalf("admission = %q, want refuse-serving", adm)
	}
}
