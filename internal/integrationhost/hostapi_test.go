package integrationhost

import (
	"context"
	"errors"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

type fakeCatalog struct {
	gotFilter store.LibraryFilter
}

func (f *fakeCatalog) Libraries(_ context.Context, filter store.LibraryFilter) ([]store.Library, error) {
	f.gotFilter = filter
	return nil, nil
}

type fakeProfiles struct{ libs []store.LibraryID }

func (f fakeProfiles) Profile(_ context.Context, _ store.ProfileID) (store.Profile, error) {
	return store.Profile{Library: f.libs}, nil
}

type fakeSecrets struct{ store map[string]string }

func (f fakeSecrets) Secret(ref string) (string, error) {
	v, ok := f.store[ref]
	if !ok {
		return "", errors.New("no such secret")
	}
	return v, nil
}
func (f fakeSecrets) SetSecret(ref, value string) error { f.store[ref] = value; return nil }

type recordingAudit struct {
	actions []string
	entries []store.ActivityEntry
}

func (r *recordingAudit) AppendActivity(_ context.Context, e store.ActivityEntry) error {
	r.actions = append(r.actions, e.Action)
	r.entries = append(r.entries, e)
	return nil
}

func hostWith(pluginID string, deps Deps, grants ...Grant) *Host {
	return NewHost(pluginID, deps, fixedGrants{grants: map[string][]Grant{pluginID: grants}}, nil)
}

// TestUngrantedMethodDenied proves every permission-backed method fails closed
// when the plugin holds no grant of that kind (the negative path ADR-0058
// requires).
func TestUngrantedMethodDenied(t *testing.T) {
	ctx := context.Background()
	h := hostWith("p", Deps{Catalog: &fakeCatalog{}, Secrets: fakeSecrets{store: map[string]string{}}})

	if _, err := h.ReadCatalog(ctx); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("read-catalog without grant: want permission denied, got %v", err)
	}
	if _, err := h.GetSecret(ctx, "x"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("get-secret without grant: want permission denied, got %v", err)
	}
	if err := h.RequestIngest(ctx, "lib"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("request-ingest without grant: want permission denied, got %v", err)
	}
}

// TestHighRiskUnapprovedDenied proves a declared-but-unapproved high-risk grant
// denies the method: approval is the gate, not mere declaration.
func TestHighRiskUnapprovedDenied(t *testing.T) {
	ctx := context.Background()
	h := hostWith("p", Deps{Catalog: &fakeCatalog{}},
		Grant{PluginID: "p", Kind: PermReadCatalog, Approved: false, AllLibraries: true})
	if _, err := h.ReadCatalog(ctx); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unapproved high-risk read-catalog: want permission denied, got %v", err)
	}
}

type restrictedManifestGrants struct {
	fixedGrants
	declared []string
}

func (g restrictedManifestGrants) DeclaredPermissions(context.Context, string) ([]string, error) {
	return g.declared, nil
}

func TestStoredGrantCannotExerciseUndeclaredPermission(t *testing.T) {
	source := restrictedManifestGrants{
		fixedGrants: fixedGrants{grants: map[string][]Grant{"p": {{
			PluginID: "p", Kind: PermReadCatalog, Approved: true, AllLibraries: true,
		}}}},
		declared: []string{PermStatusWrite},
	}
	host := NewHost("p", Deps{Catalog: &fakeCatalog{}, Audit: &recordingAudit{}}, source, nil)
	if _, err := host.ReadCatalog(context.Background()); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("undeclared stored grant exercise = %v, want permission denied", err)
	}
}

// TestReadCatalogProfileBoundFilter proves a Profile-bound read grant resolves to
// the bound Profile's Library set (Revealed stays false), and an operator-level
// grant resolves to AllLibraries — the Reveal-scope binding from Gate A.
func TestReadCatalogScopeBinding(t *testing.T) {
	ctx := context.Background()

	// Profile-bound: filter carries exactly the Profile's Libraries, not revealed.
	cat := &fakeCatalog{}
	h := NewHost("p", Deps{Catalog: cat, Profiles: fakeProfiles{libs: []store.LibraryID{"a", "b"}}, Audit: &recordingAudit{}},
		fixedGrants{grants: map[string][]Grant{"p": {{PluginID: "p", Kind: PermReadCatalog, Approved: true, BoundProfileID: "prof"}}}}, nil)
	if _, err := h.ReadCatalog(ctx); err != nil {
		t.Fatalf("read-catalog: %v", err)
	}
	if cat.gotFilter.AllLibraries {
		t.Fatal("profile-bound grant must not resolve to AllLibraries")
	}
	if cat.gotFilter.Revealed {
		t.Fatal("a worker is never a standing Reveal: Revealed must be false")
	}
	if len(cat.gotFilter.AllowedIDs) != 2 {
		t.Fatalf("expected the bound Profile's 2 Libraries, got %v", cat.gotFilter.AllowedIDs)
	}

	// Operator-level: AllLibraries, still not revealed.
	cat2 := &fakeCatalog{}
	h2 := hostWith("p", Deps{Catalog: cat2, Audit: &recordingAudit{}},
		Grant{PluginID: "p", Kind: PermReadCatalog, Approved: true, AllLibraries: true})
	if _, err := h2.ReadCatalog(ctx); err != nil {
		t.Fatalf("read-catalog: %v", err)
	}
	if !cat2.gotFilter.AllLibraries || cat2.gotFilter.Revealed {
		t.Fatalf("operator-level grant must be AllLibraries && !Revealed, got %+v", cat2.gotFilter)
	}
}

// TestSecretsNamespaced proves a worker's secret refs are namespaced per plugin
// so a worker can never read another plugin's or a Core secret.
func TestSecretsNamespaced(t *testing.T) {
	ctx := context.Background()
	secrets := fakeSecrets{store: map[string]string{}}
	h := hostWith("alpha", Deps{Secrets: secrets},
		Grant{PluginID: "alpha", Kind: PermPluginSecret, Approved: true})
	if err := h.SetSecret(ctx, "api-key", "s3cr3t"); err != nil {
		t.Fatalf("set-secret: %v", err)
	}
	if _, ok := secrets.store[plugin.ScopedCredentialRef("alpha", "api-key")]; !ok {
		t.Fatalf("secret was not namespaced per plugin; store keys: %v", keysOf(secrets.store))
	}
	// A different plugin cannot read alpha's secret: its ref namespaces elsewhere.
	other := hostWith("beta", Deps{Secrets: secrets},
		Grant{PluginID: "beta", Kind: PermPluginSecret, Approved: true})
	if _, err := other.GetSecret(ctx, "api-key"); err == nil {
		t.Fatal("plugin beta must not read plugin alpha's secret")
	}
	for _, ref := range []string{"../api-key", `..\\api-key`, plugin.ScopedCredentialRef("alpha", "api-key")} {
		if _, err := other.GetSecret(ctx, ref); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("cross-plugin/traversal ref %q = %v, want permission denied", ref, err)
		}
	}
}

// TestHighRiskAudited proves a high-risk Host method writes an audit record.
func TestHighRiskAudited(t *testing.T) {
	ctx := context.Background()
	audit := &recordingAudit{}
	h := hostWith("p", Deps{Catalog: &fakeCatalog{}, Profiles: fakeProfiles{}, Audit: audit},
		Grant{PluginID: "p", Kind: PermReadCatalog, Approved: true, AllLibraries: true})
	if _, err := h.ReadCatalog(ctx); err != nil {
		t.Fatalf("read-catalog: %v", err)
	}
	if len(audit.actions) != 1 || audit.actions[0] != store.ActivityPluginCatalogRead {
		t.Fatalf("expected one catalog-read audit record, got %v", audit.actions)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
