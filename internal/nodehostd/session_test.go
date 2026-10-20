package nodehostd_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/nodehost"
	"github.com/beamfall/core/internal/nodehostd"
)

// spkiPin mirrors nodehost.leafSPKIPin (the unexported production derivation) so
// the test pins the same identity the wired code pins.
func spkiPin(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// genSelfSigned returns a fresh self-signed leaf (the node/main identity model
// of ADR-0046/0123) and its SPKI pin.
func genSelfSigned(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "beamfall-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, spkiPin(leaf)
}

type fixedMainPin struct{ pin string }

func (f fixedMainPin) ServerSPKIPin(context.Context) (string, error) { return f.pin, nil }

// serveNode starts a nodehostd session listener serving mainCert with
// RequireAnyClientCert (a clone-shaped mTLS config, never a shared browser
// config), returning the address. It is torn down when the test ends.
func serveNode(t *testing.T, admitter nodehostd.SessionAdmitter, mainCert tls.Certificate) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	mtls := &tls.Config{
		Certificates: []tls.Certificate{mainCert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
	srv := nodehostd.NewServer(admitter, nodehostd.ProductionPolicy(), mtls, nil)
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String()
}

func serveSessionResponse(t *testing.T, mainCert tls.Certificate, response string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		conn := tls.Server(raw, &tls.Config{
			Certificates: []tls.Certificate{mainCert},
			ClientAuth:   tls.RequireAnyClientCert,
			MinVersion:   tls.VersionTLS12,
		})
		if err := conn.Handshake(); err != nil {
			return
		}
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		if _, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(header[:]))); err != nil {
			return
		}
		binary.BigEndian.PutUint32(header[:], uint32(len(response)))
		_, _ = conn.Write(append(header[:], response...))
	}()
	return ln.Addr().String()
}

// enrollNodeA enrolls one node via the in-process operator seam (no node-facing
// enroll transport in slice 1) and returns the service, the node id, the node's
// client cert, and the main SPKI pin the node captured.
func enrollNodeA(t *testing.T, nodeCert tls.Certificate, mainPin string) (*nodehost.Service, string) {
	t.Helper()
	svc := nodehost.NewService(func() time.Time { return time.Unix(1000, 0) }, fixedMainPin{pin: mainPin})
	code, err := svc.MintEnrollmentCode(true, time.Minute)
	if err != nil {
		t.Fatalf("mint code: %v", err)
	}
	req := nodehost.EnrollRequest{
		HandshakeVersion: nodehost.EnrollmentHandshakeVersion,
		Code:             code,
		Contract: nodehost.ClientContract{
			Surface:              "origin-node",
			BuildID:              "b1",
			CoreContractRevRange: [2]nodehost.Revision{1, 1},
			FixedVocabularies:    []string{"core", "transcode"},
			DistributionVerified: true,
			NodeCertSPKI:         spkiPin(nodeCert.Leaf),
		},
		NodeLeaf:                      nodeCert.Leaf,
		OperatorConfirmedFingerprints: true, // main/operator-side, never node-sent
	}
	res, err := svc.Enroll(context.Background(), req, nodehostd.ProductionPolicy())
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if res.MainSPKIPin != mainPin {
		t.Fatalf("MainSPKIPin = %q, want %q", res.MainSPKIPin, mainPin)
	}
	return svc, res.Node.ID
}

// TestSessionHappyPath: matching node leaf + matching main leaf ⇒ AdmitServing.
func TestSessionHappyPath(t *testing.T) {
	mainCert, mainPin := genSelfSigned(t)
	nodeCert, _ := genSelfSigned(t)
	svc, nodeID := enrollNodeA(t, nodeCert, mainPin)
	addr := serveNode(t, svc, mainCert)

	adm, err := nodehostd.OpenSession(context.Background(),
		nodehostd.DialConfig{Addr: addr, NodeCert: nodeCert, MainSPKIPin: mainPin}, nodeID, 1)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if adm != nodehost.AdmitServing {
		t.Fatalf("admission = %q, want %q", adm, nodehost.AdmitServing)
	}
}

func TestOpenSessionRejectsInvalidResponse(t *testing.T) {
	mainCert, mainPin := genSelfSigned(t)
	nodeCert, _ := genSelfSigned(t)
	tests := []struct {
		name     string
		response string
	}{
		{name: "wrong type", response: `{"type":"other","admission":"serving"}`},
		{name: "unknown admission", response: `{"type":"open-session-result","admission":"serve-everything"}`},
		{name: "missing decision", response: `{"type":"open-session-result"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := serveSessionResponse(t, mainCert, tt.response)
			adm, err := nodehostd.OpenSession(context.Background(), nodehostd.DialConfig{
				Addr: addr, NodeCert: nodeCert, MainSPKIPin: mainPin,
			}, "node-a", 1)
			if err == nil {
				t.Fatalf("OpenSession admission = %q without error", adm)
			}
			if adm != nodehost.AdmitRefuse {
				t.Fatalf("admission = %q, want %q", adm, nodehost.AdmitRefuse)
			}
		})
	}
}

// TestSessionMismatchedNodeLeaf: the MAIN rejects a node presenting a leaf whose
// SPKI does not match the enrollment-time pin (main→node pin direction).
func TestSessionMismatchedNodeLeaf(t *testing.T) {
	mainCert, mainPin := genSelfSigned(t)
	nodeCertA, _ := genSelfSigned(t)
	nodeCertB, _ := genSelfSigned(t) // different SPKI, never enrolled
	svc, nodeID := enrollNodeA(t, nodeCertA, mainPin)
	addr := serveNode(t, svc, mainCert)

	// Dial claiming node A's id but presenting leaf B.
	adm, err := nodehostd.OpenSession(context.Background(),
		nodehostd.DialConfig{Addr: addr, NodeCert: nodeCertB, MainSPKIPin: mainPin}, nodeID, 1)
	if err == nil {
		t.Fatalf("mismatched node leaf admitted (%q), want refusal", adm)
	}
	if adm != nodehost.AdmitRefuse {
		t.Fatalf("admission = %q, want %q", adm, nodehost.AdmitRefuse)
	}
	if !strings.Contains(err.Error(), "subject mismatch") {
		t.Fatalf("error = %v, want node subject mismatch", err)
	}
}

// TestSessionMismatchedMainLeaf: the NODE rejects a main whose served leaf SPKI
// does not match the enrollment-time MainSPKIPin (node→main pin, ADR-0046 §2.1).
func TestSessionMismatchedMainLeaf(t *testing.T) {
	_, realMainPin := genSelfSigned(t)  // the pin the node captured at enrollment
	impostorCert, _ := genSelfSigned(t) // a DIFFERENT main identity now serving
	nodeCert, _ := genSelfSigned(t)
	svc, nodeID := enrollNodeA(t, nodeCert, realMainPin)
	addr := serveNode(t, svc, impostorCert) // serves the impostor leaf

	adm, err := nodehostd.OpenSession(context.Background(),
		nodehostd.DialConfig{Addr: addr, NodeCert: nodeCert, MainSPKIPin: realMainPin}, nodeID, 1)
	if err == nil {
		t.Fatalf("mismatched main leaf accepted (%q), want handshake rejection", adm)
	}
	if adm != nodehost.AdmitRefuse {
		t.Fatalf("admission = %q, want %q", adm, nodehost.AdmitRefuse)
	}
	if !strings.Contains(err.Error(), "main certificate subject mismatch") {
		t.Fatalf("error = %v, want main subject mismatch (pin failure), not an unrelated dial error", err)
	}
}

// TestSessionRevisionSkewRefused: an unsupported start-of-session revision drops
// to refuse-serving, never admitted (ADR-0129 §2 fail-closed negotiation).
func TestSessionRevisionSkewRefused(t *testing.T) {
	mainCert, mainPin := genSelfSigned(t)
	nodeCert, _ := genSelfSigned(t)
	svc, nodeID := enrollNodeA(t, nodeCert, mainPin)
	addr := serveNode(t, svc, mainCert)

	adm, err := nodehostd.OpenSession(context.Background(),
		nodehostd.DialConfig{Addr: addr, NodeCert: nodeCert, MainSPKIPin: mainPin}, nodeID, 2) // revision 2 unsupported
	if err != nil {
		t.Fatalf("OpenSession transport error: %v", err)
	}
	if adm != nodehost.AdmitRefuse {
		t.Fatalf("admission = %q, want %q (refuse on skew)", adm, nodehost.AdmitRefuse)
	}
}

// TestOpenSessionRequestHasNoOperatorConfirmation guards the deferred slice-1b
// boundary: the node's on-wire request must have no field that could let a node
// self-attest operator fingerprint confirmation (ADR-0123 §5 is operator-side).
func TestOpenSessionRequestHasNoOperatorConfirmation(t *testing.T) {
	// The exported dial surface carries only node-authenticating material, no
	// confirmation field — assert by field names.
	dc := reflect.TypeOf(nodehostd.DialConfig{})
	for i := 0; i < dc.NumField(); i++ {
		name := strings.ToLower(dc.Field(i).Name)
		if strings.Contains(name, "confirm") || strings.Contains(name, "operator") || strings.Contains(name, "fingerprint") {
			t.Fatalf("DialConfig exposes operator-confirmation field %q; confirmation must be main-side only", dc.Field(i).Name)
		}
	}
}

// TestEnrollRequiresMainSideConfirmation guards that the pin logic still refuses
// enrollment without the operator's (main-side) fingerprint confirmation.
func TestEnrollRequiresMainSideConfirmation(t *testing.T) {
	_, mainPin := genSelfSigned(t)
	nodeCert, _ := genSelfSigned(t)
	svc := nodehost.NewService(func() time.Time { return time.Unix(1000, 0) }, fixedMainPin{pin: mainPin})
	code, err := svc.MintEnrollmentCode(true, time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	req := nodehost.EnrollRequest{
		HandshakeVersion: nodehost.EnrollmentHandshakeVersion,
		Code:             code,
		Contract: nodehost.ClientContract{
			Surface: "origin-node", BuildID: "b1",
			CoreContractRevRange: [2]nodehost.Revision{1, 1},
			FixedVocabularies:    []string{"core", "transcode"},
			DistributionVerified: true,
			NodeCertSPKI:         spkiPin(nodeCert.Leaf),
		},
		NodeLeaf:                      nodeCert.Leaf,
		OperatorConfirmedFingerprints: false, // node cannot supply this
	}
	if _, err := svc.Enroll(context.Background(), req, nodehostd.ProductionPolicy()); !errors.Is(err, nodehost.ErrFingerprintsUnconfirmed) {
		t.Fatalf("Enroll without confirmation = %v, want ErrFingerprintsUnconfirmed", err)
	}
}
