package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePackageManifestRejectsDuplicateKeysAtEveryDepth(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "package-integration.json")) // #nosec G304 -- fixed package test fixture path.
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{
		`{"schemaVersion":1,"schemaVersion":1}`,
		`{"outer":{"id":"one","id":"two"}}`,
		`{"outer":[{"kind":"one","kind":"two"}]}`,
	}
	for _, raw := range tests {
		_, err := ParsePackageManifest([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), "duplicate object key") {
			t.Fatalf("ParsePackageManifest(%s) = %v, want duplicate-key refusal", raw, err)
		}
	}
	if _, err := ParsePackageManifest(base); err != nil {
		t.Fatalf("valid manifest rejected after duplicate-key scan: %v", err)
	}
}

func TestPackageFixturesValidateUnderSharedRules(t *testing.T) {
	for _, tier := range []string{TierDomain, TierWorkflow, TierIntegration} {
		t.Run(tier, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "package-"+tier+".json")) // #nosec G304 -- fixture path from the fixed tier list.
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := ParsePackageManifest(data)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Tier != tier {
				t.Fatalf("tier = %q, want %q", manifest.Tier, tier)
			}
		})
	}
}

func TestPackageWorkflowRejectsIntegrationAuthority(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "package-workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParsePackageManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Outbound = []string{"https://outside.example"}
	if err := ValidatePackageManifest(manifest); err == nil {
		t.Fatal("Workflow package accepted an outbound endpoint")
	}
}

func TestPackageRejectsWrongSchemaVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "package-"+TierDomain+".json")) // #nosec G304 -- fixture path from a package constant.
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParsePackageManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	manifest.SchemaVersion = PackageSchemaVersion + 1
	if err := ValidatePackageManifest(manifest); err == nil {
		t.Fatal("a package manifest with a foreign schemaVersion must be rejected")
	}
}

func TestPackageRejectsCoreOutboundPolicyConfigField(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "package-"+TierIntegration+".json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ParsePackageManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ConfigFields = append(manifest.ConfigFields, ConfigFieldSpec{
		Name:  ReservedOutboundPolicyConfigKey,
		Type:  "text",
		Label: L("Forged outbound policy"),
	})
	if err := ValidatePackageManifest(manifest); err == nil {
		t.Fatal("plugin package claimed Core's reserved outbound-policy config key")
	}
}
