package nodehost

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"
)

func mainAndNodeLeaves(t *testing.T) (mainLeaf, nodeLeaf *x509.Certificate) {
	t.Helper()
	return makeLeaf(t), makeLeaf(t)
}

// enrolledMainAuthenticator enrolls a node whose MainSPKIPin equals the SPKI of
// the real main leaf, then builds the node-side authenticator from the pin the
// enrollment handed back — proving the pin the authenticator guards by is the
// enrollment-time pin, not a fabricated value.
func enrolledMainAuthenticator(t *testing.T, mainLeaf, nodeLeaf *x509.Certificate) *MainAuthenticator {
	t.Helper()
	mainSPKI, err := leafSPKIPin(mainLeaf)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(fixedClock(time.Unix(100, 0)), staticPin{pin: mainSPKI})
	res := enrollOne(t, s, nodeLeaf, compatibleContract(t, nodeLeaf), supportedPolicy())
	if res.MainSPKIPin != mainSPKI {
		t.Fatalf("enrollment MainSPKIPin = %q, want %q", res.MainSPKIPin, mainSPKI)
	}
	return NewMainAuthenticator(res.Node.MainSPKIPin)
}

// TestAuthenticateMainAcceptsPinnedMain proves the accept path: a legitimate main
// presenting the leaf the node pinned at enrollment is authenticated, so an
// inbound request-file-mutation / reap-confirmation ack on the main→node leg
// proceeds.
func TestAuthenticateMainAcceptsPinnedMain(t *testing.T) {
	mainLeaf, nodeLeaf := mainAndNodeLeaves(t)
	auth := enrolledMainAuthenticator(t, mainLeaf, nodeLeaf)

	err := auth.AuthenticateDownOperation(DownOperationRequest{
		PresentedMainLeaf:        mainLeaf,
		NodeInitiatedMultiplexed: true,
	})
	if err != nil {
		t.Fatalf("AuthenticateDownOperation(pinned main) = %v, want nil (accept)", err)
	}
}

func TestAuthenticateMainRejectsOutsideNodeInitiatedConnection(t *testing.T) {
	mainLeaf, nodeLeaf := mainAndNodeLeaves(t)
	auth := enrolledMainAuthenticator(t, mainLeaf, nodeLeaf)

	if err := auth.AuthenticateMain(mainLeaf); !errors.Is(err, ErrDownOperationOutsideNodeConnection) {
		t.Fatalf("AuthenticateMain(outside multiplexed conn) = %v, want ErrDownOperationOutsideNodeConnection", err)
	}
}

// TestAuthenticateMainRejectsSpoofedMain proves the reject path: a local attacker
// presenting a DIFFERENT leaf (a rogue/redirected main) fails the enrollment-time
// pin, so a spoofed request-file-mutation / reap-confirmation ack is rejected —
// the §8 Trash guarantee holds.
func TestAuthenticateMainRejectsSpoofedMain(t *testing.T) {
	mainLeaf, nodeLeaf := mainAndNodeLeaves(t)
	auth := enrolledMainAuthenticator(t, mainLeaf, nodeLeaf)

	spoofedMain := makeLeaf(t) // a different keypair → different SPKI than the pin
	err := auth.AuthenticateDownOperation(DownOperationRequest{
		PresentedMainLeaf:        spoofedMain,
		NodeInitiatedMultiplexed: true,
	})
	if !errors.Is(err, ErrMainSubjectMismatch) {
		t.Fatalf("AuthenticateDownOperation(spoofed main) = %v, want ErrMainSubjectMismatch (reject)", err)
	}
}

// TestAuthenticateMainFailsClosedWithoutLeaf rejects an inbound main→node
// operation that presents no certificate at all (an unauthenticated local caller).
func TestAuthenticateMainFailsClosedWithoutLeaf(t *testing.T) {
	mainLeaf, nodeLeaf := mainAndNodeLeaves(t)
	auth := enrolledMainAuthenticator(t, mainLeaf, nodeLeaf)

	err := auth.AuthenticateDownOperation(DownOperationRequest{
		NodeInitiatedMultiplexed: true,
	})
	if !errors.Is(err, ErrNodeCertInvalid) {
		t.Fatalf("AuthenticateDownOperation(nil leaf) = %v, want ErrNodeCertInvalid", err)
	}
}

// TestAuthenticateMainFailsClosedWithoutPin proves an unpinned node never accepts
// any main: a node that captured no main SPKI pin rejects every inbound main→node
// operation rather than trusting an unauthenticated main.
func TestAuthenticateMainFailsClosedWithoutPin(t *testing.T) {
	auth := NewMainAuthenticator("")
	mainLeaf := makeLeaf(t)
	err := auth.AuthenticateDownOperation(DownOperationRequest{
		PresentedMainLeaf:        mainLeaf,
		NodeInitiatedMultiplexed: true,
	})
	if !errors.Is(err, ErrMainNotPinned) {
		t.Fatalf("AuthenticateDownOperation(no pin) = %v, want ErrMainNotPinned", err)
	}
}
