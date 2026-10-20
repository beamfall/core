package nodehost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// fixedClock returns a deterministic clock.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// staticPin is a MainSPKIProvider returning a fixed pin (or error).
type staticPin struct {
	pin string
	err error
}

func (s staticPin) ServerSPKIPin(context.Context) (string, error) { return s.pin, s.err }

// makeLeaf builds a real self-signed x509 leaf so SPKI derivation runs on
// genuine certificate bytes.
func makeLeaf(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return makeLeafForKey(t, key)
}

func makeLeafForKey(t *testing.T, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
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

// supportedPolicy is a compatible-yielding baseline policy.
func supportedPolicy() CompatibilityPolicy {
	return CompatibilityPolicy{
		SupportedRevisions:      map[Revision]bool{1: true},
		RequiredVocabularies:    []string{"core"},
		FullServiceVocabularies: []string{"transcode"},
		RevokedBuilds:           map[string]bool{},
		Generation:              7,
	}
}

// compatibleContract returns a contract that yields DecisionCompatible, with its
// declared SPKI bound to the given leaf.
func compatibleContract(t *testing.T, leaf *x509.Certificate) ClientContract {
	t.Helper()
	spki, err := leafSPKIPin(leaf)
	if err != nil {
		t.Fatal(err)
	}
	return ClientContract{
		Surface:              originNodeSurface,
		BuildID:              "build-1",
		CoreContractRevRange: [2]Revision{1, 1},
		FixedVocabularies:    []string{"core", "transcode"},
		DistributionVerified: true,
		NodeCertSPKI:         spki,
	}
}

// enrollOne mints a code and enrolls a fresh compatible node, returning it.
func enrollOne(t *testing.T, s *Service, leaf *x509.Certificate, c ClientContract, p CompatibilityPolicy) EnrollResult {
	t.Helper()
	code, err := s.MintEnrollmentCode(true, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion:              EnrollmentHandshakeVersion,
		Code:                          code,
		Contract:                      c,
		NodeLeaf:                      leaf,
		OperatorConfirmedFingerprints: true,
	}, p)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return res
}

func TestEnrollHappyPathServes(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)

	res := enrollOne(t, s, leaf, c, supportedPolicy())

	if res.Node.State != StateActive {
		t.Fatalf("state = %q, want active", res.Node.State)
	}
	if res.Node.Admission != AdmitServing {
		t.Fatalf("admission = %q, want serving", res.Node.Admission)
	}
	if res.MainSPKIPin != "sha256-MAIN" {
		t.Fatalf("main pin = %q", res.MainSPKIPin)
	}
	wantSPKI, _ := leafSPKIPin(leaf)
	if res.Node.NodeCertSPKI != wantSPKI {
		t.Fatalf("node SPKI not derived from leaf: got %q want %q", res.Node.NodeCertSPKI, wantSPKI)
	}
	// enroll→serve happy path: a fresh session over the pinned leaf serves.
	adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, supportedPolicy())
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if adm != AdmitServing {
		t.Fatalf("session admission = %q, want serving", adm)
	}
}

func TestEnrollUnknownHandshakeVersionFailsClosed(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	_, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion:              EnrollmentHandshakeVersion + 1,
		Code:                          code,
		Contract:                      compatibleContract(t, leaf),
		NodeLeaf:                      leaf,
		OperatorConfirmedFingerprints: true,
	}, supportedPolicy())
	if !errors.Is(err, ErrEnrollmentHandshakeUnsupported) {
		t.Fatalf("err = %v, want ErrEnrollmentHandshakeUnsupported", err)
	}
	if _, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion:              EnrollmentHandshakeVersion,
		Code:                          code,
		Contract:                      compatibleContract(t, leaf),
		NodeLeaf:                      leaf,
		OperatorConfirmedFingerprints: true,
	}, supportedPolicy()); err != nil {
		t.Fatalf("known version after unknown attempt: %v", err)
	}
}

func TestEnrollFingerprintsMustBeConfirmed(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	_, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion:              EnrollmentHandshakeVersion,
		Code:                          code,
		Contract:                      compatibleContract(t, leaf),
		NodeLeaf:                      leaf,
		OperatorConfirmedFingerprints: false,
	}, supportedPolicy())
	if !errors.Is(err, ErrFingerprintsUnconfirmed) {
		t.Fatalf("err = %v, want ErrFingerprintsUnconfirmed", err)
	}
}

func TestEnrollNilLeafRejected(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	_, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion:              EnrollmentHandshakeVersion,
		Code:                          code,
		Contract:                      ClientContract{Surface: originNodeSurface, NodeCertSPKI: "sha256-x"},
		NodeLeaf:                      nil,
		OperatorConfirmedFingerprints: true,
	}, supportedPolicy())
	if !errors.Is(err, ErrNodeCertInvalid) {
		t.Fatalf("err = %v, want ErrNodeCertInvalid", err)
	}
}

func TestEnrollSubjectMismatchRejected(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)
	c.NodeCertSPKI = "sha256-WRONG" // claims a different subject than the leaf
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	_, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion: EnrollmentHandshakeVersion, Code: code, Contract: c, NodeLeaf: leaf, OperatorConfirmedFingerprints: true,
	}, supportedPolicy())
	if !errors.Is(err, ErrNodeSubjectMismatch) {
		t.Fatalf("err = %v, want ErrNodeSubjectMismatch", err)
	}
}

func TestEnrollEmptyDeclaredSPKIRejected(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)
	c.NodeCertSPKI = ""
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	_, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion: EnrollmentHandshakeVersion, Code: code, Contract: c, NodeLeaf: leaf, OperatorConfirmedFingerprints: true,
	}, supportedPolicy())
	if !errors.Is(err, ErrNodeSubjectMismatch) {
		t.Fatalf("err = %v, want ErrNodeSubjectMismatch", err)
	}
}

func TestEnrollMainPinUnavailable(t *testing.T) {
	cases := []struct {
		name string
		prov MainSPKIProvider
	}{
		{"nil provider", nil},
		{"error provider", staticPin{err: errors.New("no served cert")}},
		{"empty pin", staticPin{pin: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewService(fixedClock(time.Unix(100, 0)), tc.prov)
			leaf := makeLeaf(t)
			code, _ := s.MintEnrollmentCode(true, time.Minute)
			_, err := s.Enroll(context.Background(), EnrollRequest{
				HandshakeVersion: EnrollmentHandshakeVersion, Code: code, Contract: compatibleContract(t, leaf), NodeLeaf: leaf,
				OperatorConfirmedFingerprints: true,
			}, supportedPolicy())
			if !errors.Is(err, ErrMainPinUnavailable) {
				t.Fatalf("err = %v, want ErrMainPinUnavailable", err)
			}
		})
	}
}

func TestEnrollIncompatibleCannotServe(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)
	c.CoreContractRevRange = [2]Revision{2, 2} // entirely above supported {1} → server-too-old
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	_, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion: EnrollmentHandshakeVersion, Code: code, Contract: c, NodeLeaf: leaf, OperatorConfirmedFingerprints: true,
	}, supportedPolicy())
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("err = %v, want ErrIncompatible (a node failing the handshake cannot serve)", err)
	}
}

func TestEnrollConsumedCodeRejected(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	req := EnrollRequest{HandshakeVersion: EnrollmentHandshakeVersion, Code: code, Contract: c, NodeLeaf: leaf, OperatorConfirmedFingerprints: true}
	if _, err := s.Enroll(context.Background(), req, supportedPolicy()); err != nil {
		t.Fatal(err)
	}
	_, err := s.Enroll(context.Background(), req, supportedPolicy()) // reuse → consumed
	if !errors.Is(err, ErrEnrollCodeInvalid) {
		t.Fatalf("err = %v, want ErrEnrollCodeInvalid", err)
	}
}

func TestEnrollExpiredCodeRejected(t *testing.T) {
	clock := time.Unix(100, 0)
	s := NewService(func() time.Time { return clock }, staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)
	code, _ := s.MintEnrollmentCode(true, time.Minute)
	clock = clock.Add(2 * time.Minute) // past expiry
	_, err := s.Enroll(context.Background(), EnrollRequest{
		HandshakeVersion: EnrollmentHandshakeVersion, Code: code, Contract: c, NodeLeaf: leaf, OperatorConfirmedFingerprints: true,
	}, supportedPolicy())
	if !errors.Is(err, ErrEnrollCodeInvalid) {
		t.Fatalf("err = %v, want ErrEnrollCodeInvalid", err)
	}
}

func TestEnrollDegradedAdmission(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)
	c.FixedVocabularies = []string{"core"} // required present, full-service "transcode" missing → degraded
	res := enrollOne(t, s, leaf, c, supportedPolicy())
	if res.Node.Admission != AdmitDegraded {
		t.Fatalf("admission = %q, want degraded", res.Node.Admission)
	}
	adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, supportedPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if adm != AdmitDegraded {
		t.Fatalf("session admission = %q, want degraded (narrowed, not refuse, not serving)", adm)
	}
}

func TestNonOperatorActionsRejected(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	if _, err := s.MintEnrollmentCode(false, time.Minute); !errors.Is(err, ErrNotOperator) {
		t.Fatalf("mint: %v", err)
	}
	if err := s.Revoke(false, "node-x"); !errors.Is(err, ErrNotOperator) {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.RenewNodeCertificate(false, "node-x", makeLeaf(t)); !errors.Is(err, ErrNotOperator) {
		t.Fatalf("renew: %v", err)
	}
}

func TestOpenSessionMismatchedRevisionFailsClosed(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	res := enrollOne(t, s, leaf, compatibleContract(t, leaf), supportedPolicy())
	adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 99, PresentedLeaf: leaf}, supportedPolicy()) // 99 unsupported
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if adm == AdmitServing {
		t.Fatal("mismatched revision proceeded to serving; must fail closed")
	}
	if adm != AdmitRefuse {
		t.Fatalf("admission = %q, want refuse-serving", adm)
	}
}

func TestOpenSessionUnknownNode(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	adm, err := s.OpenSession(SessionRequest{NodeID: "node-missing", Revision: 1, PresentedLeaf: makeLeaf(t)}, supportedPolicy())
	if !errors.Is(err, ErrNodeNotFound) || adm != AdmitRefuse {
		t.Fatalf("adm=%q err=%v, want refuse + ErrNodeNotFound", adm, err)
	}
}

func TestOpenSessionWrongPresentedCertFailsClosed(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	res := enrollOne(t, s, leaf, compatibleContract(t, leaf), supportedPolicy())
	// A different cert than the one pinned at enrollment must not serve.
	adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: makeLeaf(t)}, supportedPolicy())
	if !errors.Is(err, ErrNodeSubjectMismatch) || adm != AdmitRefuse {
		t.Fatalf("adm=%q err=%v, want refuse + ErrNodeSubjectMismatch (cert-pinning enforced at session time)", adm, err)
	}
	// A nil presented cert also fails closed.
	if adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: nil}, supportedPolicy()); err == nil || adm != AdmitRefuse {
		t.Fatalf("nil cert adm=%q err=%v, want refuse + error", adm, err)
	}
}

func TestRevokeStopsNewSessions(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	res := enrollOne(t, s, leaf, compatibleContract(t, leaf), supportedPolicy())

	if err := s.Revoke(true, res.Node.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, supportedPolicy())
	if !errors.Is(err, ErrNodeRevoked) || adm != AdmitRefuse {
		t.Fatalf("after revoke adm=%q err=%v, want refuse + ErrNodeRevoked", adm, err)
	}
	// Revoke is idempotent on an already-revoked node.
	if err := s.Revoke(true, res.Node.ID); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
	// Unknown node revoke → ErrNodeNotFound.
	if err := s.Revoke(true, "node-missing"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("revoke unknown: %v", err)
	}
}

func TestStoreRegistryPersistsRevocationAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	clock := fixedClock(time.Unix(100, 0))
	st, err := store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	leaf := makeLeaf(t)
	s := NewServiceWithRegistry(clock, staticPin{pin: "sha256-MAIN"}, NewStoreRegistry(st))
	s.newID = func() (string, error) { return "node-stable", nil }
	res := enrollOne(t, s, leaf, compatibleContract(t, leaf), supportedPolicy())
	if err := s.Revoke(true, res.Node.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	afterRestart := NewServiceWithRegistry(clock, staticPin{pin: "sha256-MAIN"}, NewStoreRegistry(reopened))
	adm, err := afterRestart.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, supportedPolicy())
	if !errors.Is(err, ErrNodeRevoked) || adm != AdmitRefuse {
		t.Fatalf("after restart adm=%q err=%v, want refuse + ErrNodeRevoked", adm, err)
	}
}

func TestMainSPKIRotationRefusesUntilOperatorRepin(t *testing.T) {
	mainLeaf := makeLeaf(t)
	mainSPKI, _ := leafSPKIPin(mainLeaf)
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: mainSPKI})
	nodeLeaf := makeLeaf(t)
	res := enrollOne(t, s, nodeLeaf, compatibleContract(t, nodeLeaf), supportedPolicy())
	auth := NewMainAuthenticator(res.Node.MainSPKIPin)

	rotatedMainLeaf := makeLeaf(t)
	if err := auth.AuthenticateDownOperation(DownOperationRequest{PresentedMainLeaf: rotatedMainLeaf, NodeInitiatedMultiplexed: true}); !errors.Is(err, ErrMainSubjectMismatch) {
		t.Fatalf("rotated main before re-pin = %v, want ErrMainSubjectMismatch", err)
	}
	if err := auth.RepinMain(false, rotatedMainLeaf); !errors.Is(err, ErrFingerprintsUnconfirmed) {
		t.Fatalf("unconfirmed node re-pin = %v, want ErrFingerprintsUnconfirmed", err)
	}
	repin, err := s.RepinMain(true, res.Node.ID, rotatedMainLeaf, true)
	if err != nil {
		t.Fatalf("RepinMain: %v", err)
	}
	if repin.NodeID != res.Node.ID || repin.NodeCertSPKI != res.Node.NodeCertSPKI {
		t.Fatalf("re-pin changed node identity: %+v, want id=%q nodeSPKI=%q", repin, res.Node.ID, res.Node.NodeCertSPKI)
	}
	if err := auth.RepinMain(true, rotatedMainLeaf); err != nil {
		t.Fatalf("node RepinMain: %v", err)
	}
	if err := auth.AuthenticateDownOperation(DownOperationRequest{PresentedMainLeaf: rotatedMainLeaf, NodeInitiatedMultiplexed: true}); err != nil {
		t.Fatalf("AuthenticateMain(rotated, re-pinned) = %v, want nil", err)
	}
	if adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: nodeLeaf}, supportedPolicy()); err != nil || adm != AdmitServing {
		t.Fatalf("post-repin session adm=%q err=%v, want serving without re-enroll", adm, err)
	}
}

func TestRenewNodeCertificatePreservesNodeSPKI(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := makeLeafForKey(t, key)
	res := enrollOne(t, s, leaf, compatibleContract(t, leaf), supportedPolicy())

	renewedLeaf := makeLeafForKey(t, key)
	renew, err := s.RenewNodeCertificate(true, res.Node.ID, renewedLeaf)
	if err != nil {
		t.Fatalf("RenewNodeCertificate: %v", err)
	}
	wantSPKI, _ := leafSPKIPin(leaf)
	if renew.NodeID != res.Node.ID || renew.NodeCertSPKI != wantSPKI || renew.MainSPKIPin != res.MainSPKIPin {
		t.Fatalf("renew result = %+v, want id=%q spki=%q main=%q", renew, res.Node.ID, wantSPKI, res.MainSPKIPin)
	}
	got, _, err := s.reg.Node(res.Node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != res.Node.ID || got.NodeCertSPKI != wantSPKI {
		t.Fatalf("renewed node = %+v, want same id=%q spki=%q", got, res.Node.ID, wantSPKI)
	}
	if adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: renewedLeaf}, supportedPolicy()); err != nil || adm != AdmitServing {
		t.Fatalf("post-renewal session adm=%q err=%v, want serving", adm, err)
	}
	if _, err := s.RenewNodeCertificate(true, res.Node.ID, makeLeaf(t)); !errors.Is(err, ErrNodeKeyRotationRequiresEnrollment) {
		t.Fatalf("new node key renewal err=%v, want ErrNodeKeyRotationRequiresEnrollment", err)
	}
}

func TestRenewRevokedNodeRefused(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	res := enrollOne(t, s, leaf, compatibleContract(t, leaf), supportedPolicy())
	if err := s.Revoke(true, res.Node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenewNodeCertificate(true, res.Node.ID, leaf); !errors.Is(err, ErrNodeRevoked) {
		t.Fatalf("renew revoked: %v, want ErrNodeRevoked", err)
	}
}

// TestOpenSessionReEvaluatesAgainstCurrentPolicy proves a build revoked AFTER
// enrollment fails the already-enrolled node closed (ADR-0063 invalidation): the
// session decision is not trusted from enroll time.
func TestOpenSessionReEvaluatesAgainstCurrentPolicy(t *testing.T) {
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: "sha256-MAIN"})
	leaf := makeLeaf(t)
	res := enrollOne(t, s, leaf, compatibleContract(t, leaf), supportedPolicy())
	if res.Node.Admission != AdmitServing {
		t.Fatalf("enroll admission = %q, want serving", res.Node.Admission)
	}
	// Build revoked since enrollment, generation bumped.
	revoked := supportedPolicy()
	revoked.RevokedBuilds = map[string]bool{"build-1": true}
	revoked.Generation = 8
	adm, err := s.OpenSession(SessionRequest{NodeID: res.Node.ID, Revision: 1, PresentedLeaf: leaf}, revoked)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if adm != AdmitRefuse {
		t.Fatalf("admission = %q, want refuse (stale decision must fail closed under current policy)", adm)
	}
}

func TestEvaluateCompatibilityTable(t *testing.T) {
	p := supportedPolicy()
	leaf := makeLeaf(t)
	good := compatibleContract(t, leaf)

	tweak := func(f func(c *ClientContract)) ClientContract {
		c := good
		c.FixedVocabularies = append([]string(nil), good.FixedVocabularies...)
		f(&c)
		return c
	}

	cases := []struct {
		name string
		c    ClientContract
		want Decision
	}{
		{"compatible", good, DecisionCompatible},
		{"zero value fails closed", ClientContract{}, DecisionClientTooOld},
		{"wrong surface", tweak(func(c *ClientContract) { c.Surface = "ios-viewer" }), DecisionClientTooOld},
		{"empty build", tweak(func(c *ClientContract) { c.BuildID = "" }), DecisionClientTooOld},
		{"revoked build", tweak(func(c *ClientContract) { c.BuildID = "bad" }), DecisionClientTooOld},
		{"unverified distribution", tweak(func(c *ClientContract) { c.DistributionVerified = false }), DecisionClientTooOld},
		{"missing required vocab", tweak(func(c *ClientContract) { c.FixedVocabularies = []string{"transcode"} }), DecisionClientTooOld},
		{"server too old", tweak(func(c *ClientContract) { c.CoreContractRevRange = [2]Revision{2, 3} }), DecisionServerTooOld},
		{"client too old", tweak(func(c *ClientContract) { c.CoreContractRevRange = [2]Revision{-2, 0} }), DecisionClientTooOld},
		{"degraded: full-service vocab missing", tweak(func(c *ClientContract) { c.FixedVocabularies = []string{"core"} }), DecisionDegraded},
	}
	pol := p
	pol.RevokedBuilds = map[string]bool{"bad": true}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EvaluateCompatibility(tc.c, pol); got != tc.want {
				t.Fatalf("decision = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEvaluateCompatibilityEmptySupportedFailsClosed(t *testing.T) {
	leaf := makeLeaf(t)
	c := compatibleContract(t, leaf)
	p := supportedPolicy()
	p.SupportedRevisions = map[Revision]bool{}
	if got := EvaluateCompatibility(c, p); got == DecisionCompatible || got == DecisionDegraded {
		t.Fatalf("empty supported revisions yielded servable decision %q; must fail closed", got)
	}
}
