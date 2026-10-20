package nodehost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

// mtlsPair builds a CA-signed leaf + the CA pool, so a real TLS handshake with
// RequireAndVerifyClientCert produces a populated VerifiedChains.
func mtlsPair(t *testing.T, cn string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Unix(1<<31, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1<<31, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}, pool
}

// nodePeerStateOverMTLS runs an actual mutual-TLS handshake and returns the
// server-side connection state (which carries the node's verified client leaf).
func nodePeerStateOverMTLS(t *testing.T) *tls.ConnectionState {
	t.Helper()
	nodeCert, nodeCA := mtlsPair(t, "origin-node")
	mainCert, mainCA := mtlsPair(t, "main")

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{mainCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    nodeCA,
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	stateCh := make(chan tls.ConnectionState, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		tc := conn.(*tls.Conn)
		if err := tc.Handshake(); err != nil {
			return
		}
		stateCh <- tc.ConnectionState()
		conn.Close()
	}()

	client, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		Certificates: []tls.Certificate{nodeCert},
		RootCAs:      mainCA,
		ServerName:   "localhost",
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	select {
	case st := <-stateCh:
		return &st
	case <-time.After(5 * time.Second):
		t.Fatal("mTLS handshake timed out")
		return nil
	}
}

// TestEnrollOverRealMutualTLS proves the cert-pinning mTLS path end-to-end: a
// real mutual-TLS handshake yields a verified peer leaf via PeerLeafFromTLS, and
// enrolling with that leaf serves; a subsequent session over the same pinned
// leaf is admitted.
func TestEnrollOverRealMutualTLS(t *testing.T) {
	state := nodePeerStateOverMTLS(t)
	leaf, err := PeerLeafFromTLS(state)
	if err != nil {
		t.Fatalf("PeerLeafFromTLS: %v", err)
	}
	spki, err := leafSPKIPin(leaf)
	if err != nil {
		t.Fatal(err)
	}

	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	res, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion: EnrollmentHandshakeVersion,
		Code:             code,
		Contract: ClientContract{
			Surface:              originNodeSurface,
			BuildID:              "build-1",
			CoreContractRevRange: [2]Revision{1, 1},
			FixedVocabularies:    []string{"core", "transcode"},
			DistributionVerified: true,
			NodeCertSPKI:         spki,
		},
		NodeLeaf:                      leaf,
		OperatorConfirmedFingerprints: true,
	}, supportedPolicy())
	if err != nil {
		t.Fatalf("Enroll over mTLS: %v", err)
	}
	if res.Node.Admission != AdmitServing {
		t.Fatalf("admission = %q, want serving", res.Node.Admission)
	}
	adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, supportedPolicy())
	if err != nil || adm != AdmitServing {
		t.Fatalf("session over mTLS adm=%q err=%v, want serving", adm, err)
	}
}

func TestPeerLeafFromTLSFailsClosed(t *testing.T) {
	cases := []struct {
		name  string
		state *tls.ConnectionState
	}{
		{"nil state", nil},
		{"incomplete handshake", &tls.ConnectionState{HandshakeComplete: false}},
		{"no verified chains", &tls.ConnectionState{HandshakeComplete: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PeerLeafFromTLS(tc.state); !errors.Is(err, ErrNotMutualTLS) {
				t.Fatalf("err = %v, want ErrNotMutualTLS", err)
			}
		})
	}
}
