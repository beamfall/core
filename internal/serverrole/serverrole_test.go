package serverrole

import (
	"errors"
	"testing"
)

func TestZeroRoleIsMainAndAllowsEveryFace(t *testing.T) {
	t.Parallel()
	var r Role // zero value
	if r != Main {
		t.Fatalf("zero Role = %v, want Main", r)
	}
	if r.Attached() {
		t.Fatal("zero Role reports Attached")
	}
	for _, f := range []Face{FaceCatalogRow, FaceStreamToken, FaceEngagement} {
		if err := r.GuardAuthority(f); err != nil {
			t.Fatalf("Main GuardAuthority(%q) = %v, want nil", f, err)
		}
	}
}

func TestAttachedRefusesEveryAuthorityFace(t *testing.T) {
	t.Parallel()
	r := Attached
	if !r.Attached() {
		t.Fatal("Attached.Attached() = false")
	}
	for _, f := range []Face{FaceCatalogRow, FaceStreamToken, FaceEngagement} {
		err := r.GuardAuthority(f)
		if err == nil {
			t.Fatalf("Attached GuardAuthority(%q) = nil, want refusal", f)
		}
		if !errors.Is(err, ErrAttachedAuthorityRefused) {
			t.Fatalf("Attached GuardAuthority(%q) error %v does not match sentinel", f, err)
		}
		var ae *authorityError
		if !errors.As(err, &ae) || ae.Face() != f {
			t.Fatalf("Attached GuardAuthority(%q) lost its face: %v", f, err)
		}
	}
}
