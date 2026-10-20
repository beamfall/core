package plugin

import (
	"context"
	"reflect"
	"testing"
)

func TestOutboundAllowlistOwnsValidatedManifestOrigins(t *testing.T) {
	source, err := NewOutboundAllowlist([]OutboundDeclaration{{
		PluginID: "worker",
		Origins:  []string{"HTTPS://API.Example.test:443/", "https://upload.example.test:8443"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := source.DeclaredOutbound(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://api.example.test", "https://upload.example.test:8443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("origins = %#v, want %#v", got, want)
	}
	got[0] = "https://mutated.test"
	again, _ := source.DeclaredOutbound(context.Background(), "worker")
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("caller mutated allowlist source: %#v", again)
	}
	missing, _ := source.DeclaredOutbound(context.Background(), "other")
	if len(missing) != 0 {
		t.Fatalf("undeclared plugin origins = %#v, want empty", missing)
	}
}

func TestOutboundAllowlistRejectsUnsafeManifestOrigins(t *testing.T) {
	for _, origin := range []string{
		"http://api.example.test",
		"https://127.0.0.1",
		"https://user:secret@api.example.test",
		"https://api.example.test/path",
		"https://api.example.test?token=secret",
	} {
		t.Run(origin, func(t *testing.T) {
			if _, err := NewOutboundAllowlist([]OutboundDeclaration{{PluginID: "worker", Origins: []string{origin}}}); err == nil {
				t.Fatalf("unsafe origin %q accepted", origin)
			}
		})
	}
	if _, err := NewOutboundAllowlist([]OutboundDeclaration{{PluginID: "worker", Origins: []string{"https://api.example.test", "https://api.example.test/"}}}); err == nil {
		t.Fatal("duplicate canonical origin accepted")
	}
}

func TestOutboundPolicyRequiresPerOriginOperatorApproval(t *testing.T) {
	policy, err := NewOutboundPolicy(OutboundDeclaration{
		PluginID: "worker",
		Origins:  []string{"https://api.example.test", "https://upload.example.test"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Allows("https://api.example.test") {
		t.Fatal("manifest declaration must not imply operator approval")
	}
	policy, err = policy.Approve("HTTPS://API.EXAMPLE.TEST:443/")
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Allows("https://api.example.test") {
		t.Fatal("operator-approved declared origin should be allowed")
	}
	if policy.Allows("https://upload.example.test") {
		t.Fatal("approval must be per origin")
	}
	if _, err := policy.Approve("https://undeclared.example.test"); err == nil {
		t.Fatal("operator approval accepted an undeclared origin")
	}
	policy, err = policy.Revoke("https://api.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if policy.Allows("https://api.example.test") {
		t.Fatal("revoked origin remained allowed")
	}
}

func TestOutboundPolicyReconcilesManifestChangesFailClosed(t *testing.T) {
	policy, err := NewOutboundPolicy(OutboundDeclaration{
		PluginID: "worker",
		Origins:  []string{"https://kept.example.test", "https://removed.example.test"},
	}, []string{"https://kept.example.test", "https://removed.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err = policy.Reconcile(OutboundDeclaration{
		PluginID: "worker",
		Origins:  []string{"https://kept.example.test", "https://added.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !policy.Allows("https://kept.example.test") {
		t.Fatal("unchanged approved origin lost approval")
	}
	if policy.Allows("https://removed.example.test") {
		t.Fatal("removed declaration retained approval")
	}
	if policy.Allows("https://added.example.test") {
		t.Fatal("new declaration must require fresh operator approval")
	}
}
