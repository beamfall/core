package integrationhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// TestPermissionVocabularyInSync proves this package gates exactly the closed
// Core permission vocabulary (plugin.PermissionKinds): if Core adds a permission,
// this fails until it is gated here, so no permission is silently ungated.
func TestPermissionVocabularyInSync(t *testing.T) {
	if !permissionVocabularyInSync() {
		t.Fatal("integrationhost permission vocabulary is out of sync with plugin.PermissionKinds")
	}
}

func TestPermissionVocabularyAssertionRejectsSeededDivergence(t *testing.T) {
	host := make(map[string]bool, len(knownPermissions))
	for kind := range knownPermissions {
		host[kind] = true
	}
	delete(host, PermReadCatalog)
	host["seeded-extra"] = true

	err := permissionVocabularySyncError(host, plugin.PermissionKinds())
	if !errors.Is(err, ErrPermissionVocabularyDiverged) {
		t.Fatalf("seeded divergence should fail with ErrPermissionVocabularyDiverged, got %v", err)
	}
	if !strings.Contains(err.Error(), PermReadCatalog) || !strings.Contains(err.Error(), "seeded-extra") {
		t.Fatalf("divergence error should name missing and extra permissions, got %v", err)
	}
}

// TestGrantScopeConflictRejected proves a grant binding both a Profile and
// all-Libraries is rejected — the Reveal-bypass guard (DB CHECK backs this).
func TestGrantScopeConflictRejected(t *testing.T) {
	g := Grant{PluginID: "p", Kind: PermReadCatalog, BoundProfileID: "prof", AllLibraries: true}
	if err := validateGrant(g); !errors.Is(err, ErrGrantScopeConflict) {
		t.Fatalf("grant binding both Profile and all-Libraries must be rejected, got %v", err)
	}
	// Exactly one binding is fine.
	if err := validateGrant(Grant{PluginID: "p", Kind: PermReadCatalog, BoundProfileID: "prof"}); err != nil {
		t.Fatalf("profile-only grant should validate, got %v", err)
	}
	if err := validateGrant(Grant{PluginID: "p", Kind: PermReadCatalog, AllLibraries: true}); err != nil {
		t.Fatalf("all-libraries-only grant should validate, got %v", err)
	}
}

// fakeLifecycleStore backs LifecycleStore in-memory.
type fakeLifecycleStore struct {
	grants          map[string][]Grant
	declared        map[string][]string
	activeBinding   map[string]string
	approvalBinding map[string]map[string]string
	worker          map[string]WorkerState
	policies        map[string]plugin.OutboundPolicy
	setPolicyErr    error
}

func newFakeLifecycleStore() *fakeLifecycleStore {
	return &fakeLifecycleStore{
		grants:          map[string][]Grant{},
		declared:        map[string][]string{},
		activeBinding:   map[string]string{},
		approvalBinding: map[string]map[string]string{},
		worker:          map[string]WorkerState{},
		policies:        map[string]plugin.OutboundPolicy{},
	}
}

func (f *fakeLifecycleStore) WorkerState(_ context.Context, id string) (WorkerState, bool, error) {
	w, ok := f.worker[id]
	return w, ok, nil
}
func (f *fakeLifecycleStore) SetWorkerState(_ context.Context, s WorkerState, _ time.Time) error {
	f.worker[s.PluginID] = s
	return nil
}
func (f *fakeLifecycleStore) Grants(_ context.Context, id string) ([]Grant, error) {
	return f.grants[id], nil
}
func (f *fakeLifecycleStore) DeclaredPermissions(_ context.Context, id string) ([]string, error) {
	return f.declared[id], nil
}
func (f *fakeLifecycleStore) ActiveManifestBinding(_ context.Context, id string) (string, bool, error) {
	binding := f.activeBinding[id]
	return binding, binding != "", nil
}
func (f *fakeLifecycleStore) GrantApprovalBinding(_ context.Context, id, kind string) (string, bool, error) {
	binding := f.approvalBinding[id][kind]
	return binding, binding != "", nil
}
func (f *fakeLifecycleStore) SetGrantApprovalBinding(_ context.Context, id, kind, binding string) error {
	if f.approvalBinding[id] == nil {
		f.approvalBinding[id] = map[string]string{}
	}
	f.approvalBinding[id][kind] = binding
	return nil
}
func (f *fakeLifecycleStore) SetGrant(_ context.Context, g Grant, _ time.Time) error {
	existing := f.grants[g.PluginID]
	for i, e := range existing {
		if e.Kind == g.Kind {
			existing[i] = g
			f.grants[g.PluginID] = existing
			return nil
		}
	}
	f.grants[g.PluginID] = append(existing, g)
	return nil
}
func (f *fakeLifecycleStore) UpdateSource(_ context.Context, _ string) (UpdateSource, bool, error) {
	return UpdateSource{}, false, nil
}
func (f *fakeLifecycleStore) OutboundPolicy(_ context.Context, id string) (plugin.OutboundPolicy, bool, error) {
	policy, ok := f.policies[id]
	return policy, ok, nil
}
func (f *fakeLifecycleStore) SetOutboundPolicy(_ context.Context, policy plugin.OutboundPolicy, _ time.Time) error {
	if f.setPolicyErr != nil {
		return f.setPolicyErr
	}
	f.policies[string(policy.PluginID)] = policy
	return nil
}

// TestApplyUpdateExpansionRequiresReapproval proves an update introducing a
// permission not already approved flips affected high-risk grants back to
// unapproved (re-approval required) before the new version is launchable.
func TestApplyUpdateExpansionRequiresReapproval(t *testing.T) {
	ctx := context.Background()
	ls := newFakeLifecycleStore()
	audit := &recordingAudit{}
	// Currently approved: read-catalog (high-risk). The update ALSO wants
	// request-file-mutation (a new high-risk kind) — an expansion.
	ls.grants["p"] = []Grant{{PluginID: "p", Kind: PermReadCatalog, Approved: true, AllLibraries: true}}
	ls.approvalBinding["p"] = map[string]string{PermReadCatalog: strings.Repeat("1", 64)}
	lc := NewLifecycle(ls, audit, func() time.Time { return time.Unix(0, 0) })
	lc.SetUpdateCandidateVerifier(func(context.Context, string) (VerifiedUpdateCandidate, error) {
		return VerifiedUpdateCandidate{
			PluginID: "p", Version: "2.0", ArtifactSHA256: strings.Repeat("a", 64),
			ManifestBinding: strings.Repeat("2", 64), Permissions: []string{PermReadCatalog, PermRequestFileMutation},
			ActiveManifestBinding: strings.Repeat("1", 64), ActivePermissions: []string{PermReadCatalog},
		}, nil
	})

	err := lc.ApplyUpdate(ctx, "p")
	if err != nil {
		t.Fatalf("apply-update: %v", err)
	}
	// The unchanged permission keeps approval; the added permission is inert.
	for _, g := range ls.grants["p"] {
		if g.Kind == PermReadCatalog && !g.Approved {
			t.Fatal("unchanged permission unexpectedly lost approval")
		}
		if g.Kind == PermRequestFileMutation && g.Approved {
			t.Fatal("added permission became effective without explicit reapproval")
		}
	}
	if ls.worker["p"].State != "update-staged" || ls.worker["p"].ArtifactSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("worker row not staged with new artifact, got %+v", ls.worker["p"])
	}
}

// TestApplyUpdateNoExpansionKeepsApproval proves a non-expanding update (same or
// narrower permissions) does NOT force re-approval.
func TestApplyUpdateNoExpansionKeepsApproval(t *testing.T) {
	ctx := context.Background()
	ls := newFakeLifecycleStore()
	ls.grants["p"] = []Grant{{PluginID: "p", Kind: PermReadCatalog, Approved: true, AllLibraries: true}}
	ls.approvalBinding["p"] = map[string]string{PermReadCatalog: strings.Repeat("1", 64)}
	lc := NewLifecycle(ls, &recordingAudit{}, func() time.Time { return time.Unix(0, 0) })
	lc.SetUpdateCandidateVerifier(func(context.Context, string) (VerifiedUpdateCandidate, error) {
		return VerifiedUpdateCandidate{
			PluginID: "p", Version: "1.1", ArtifactSHA256: strings.Repeat("b", 64),
			ManifestBinding: strings.Repeat("2", 64), Permissions: []string{PermReadCatalog},
			ActiveManifestBinding: strings.Repeat("1", 64), ActivePermissions: []string{PermReadCatalog},
		}, nil
	})

	if err := lc.ApplyUpdate(ctx, "p"); err != nil {
		t.Fatalf("apply-update: %v", err)
	}
	for _, g := range ls.grants["p"] {
		if g.Kind == PermReadCatalog && !g.Approved {
			t.Fatal("non-expanding update must not force re-approval")
		}
	}
	if got := ls.approvalBinding["p"][PermReadCatalog]; got != strings.Repeat("2", 64) {
		t.Fatalf("retained approval binding = %q, want candidate manifest binding", got)
	}
}

func TestApplyUpdateStaleGrantForNewPermissionRequiresExplicitReapproval(t *testing.T) {
	ctx := context.Background()
	const pluginID = "p"
	activeBinding := strings.Repeat("1", 64)
	candidateBinding := strings.Repeat("2", 64)
	ls := newFakeLifecycleStore()
	// The registry previously created an approved row for file mutation, but
	// the verified active artifact never declared it. A candidate that first
	// declares it must not inherit that stale approval.
	ls.grants[pluginID] = []Grant{
		{PluginID: pluginID, Kind: PermReadCatalog, Approved: true, AllLibraries: true},
		{PluginID: pluginID, Kind: PermRequestFileMutation, Approved: true, AllLibraries: true},
	}
	ls.approvalBinding[pluginID] = map[string]string{
		PermReadCatalog:         activeBinding,
		PermRequestFileMutation: activeBinding,
	}
	lc := NewLifecycle(ls, &recordingAudit{}, time.Now)
	lc.SetUpdateCandidateVerifier(func(context.Context, string) (VerifiedUpdateCandidate, error) {
		return VerifiedUpdateCandidate{
			PluginID: pluginID, Version: "2.0.0", ArtifactSHA256: strings.Repeat("a", 64),
			ManifestBinding:       candidateBinding,
			Permissions:           []string{PermReadCatalog, PermRequestFileMutation},
			ActiveManifestBinding: activeBinding,
			ActivePermissions:     []string{PermReadCatalog},
		}, nil
	})

	if err := lc.ApplyUpdate(ctx, pluginID); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	byKind := map[string]Grant{}
	for _, grant := range ls.grants[pluginID] {
		byKind[grant.Kind] = grant
	}
	if !byKind[PermReadCatalog].Approved {
		t.Fatal("permission declared by the active verified manifest lost approval")
	}
	if byKind[PermRequestFileMutation].Approved {
		t.Fatal("stale registry approval became usable when the candidate first declared it")
	}
	if got := ls.approvalBinding[pluginID][PermRequestFileMutation]; got != "" {
		t.Fatalf("new permission retained stale approval binding %q", got)
	}
	if got := ls.worker[pluginID].State; got != "update-staged" {
		t.Fatalf("worker state = %q, want update-staged", got)
	}

	// Only a fresh approval against the staged manifest makes the permission
	// effective.
	ls.declared[pluginID] = []string{PermReadCatalog, PermRequestFileMutation}
	ls.activeBinding[pluginID] = candidateBinding
	if err := lc.ApproveGrant(ctx, Grant{
		PluginID: pluginID, Kind: PermRequestFileMutation, AllLibraries: true,
	}); err != nil {
		t.Fatalf("explicit reapproval: %v", err)
	}
	if got := ls.approvalBinding[pluginID][PermRequestFileMutation]; got != candidateBinding {
		t.Fatalf("explicit approval binding = %q, want staged manifest binding", got)
	}
}

func TestGrantAndUpdateAuditAbsenceOrFailureLeavesStateIneffective(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		audit Auditor
	}{
		{name: "absent", audit: nil},
		{name: "failure", audit: erroringAudit{err: errors.New("audit down")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ls := newFakeLifecycleStore()
			ls.grants["p"] = []Grant{{PluginID: "p", Kind: PermReadCatalog}}
			ls.declared["p"] = []string{PermReadCatalog}
			ls.activeBinding["p"] = strings.Repeat("1", 64)
			ls.worker["p"] = WorkerState{PluginID: "p", Version: "1.0", ArtifactSHA256: strings.Repeat("0", 64), State: "installed"}
			lc := NewLifecycle(ls, tc.audit, time.Now)
			lc.SetUpdateCandidateVerifier(func(context.Context, string) (VerifiedUpdateCandidate, error) {
				return VerifiedUpdateCandidate{
					PluginID: "p", Version: "2.0", ArtifactSHA256: strings.Repeat("1", 64),
					ManifestBinding: strings.Repeat("2", 64), Permissions: []string{PermReadCatalog},
					ActiveManifestBinding: strings.Repeat("1", 64), ActivePermissions: []string{PermReadCatalog},
				}, nil
			})
			if err := lc.ApproveGrant(ctx, Grant{PluginID: "p", Kind: PermReadCatalog}); err == nil {
				t.Fatal("grant approval succeeded without durable audit")
			}
			if ls.grants["p"][0].Approved {
				t.Fatal("grant became effective without durable audit")
			}
			if err := lc.ApplyUpdate(ctx, "p"); err == nil {
				t.Fatal("update succeeded without durable audit")
			}
			if got := ls.worker["p"]; got.Version != "1.0" || got.State != "installed" {
				t.Fatalf("update became effective without durable audit: %+v", got)
			}
		})
	}
}

// TestApproveGrantAudited proves grant approval persists and audits.
func TestApproveGrantAudited(t *testing.T) {
	ctx := context.Background()
	ls := newFakeLifecycleStore()
	ls.declared["p"] = []string{PermReadCatalog}
	ls.activeBinding["p"] = strings.Repeat("1", 64)
	audit := &recordingAudit{}
	lc := NewLifecycle(ls, audit, func() time.Time { return time.Unix(0, 0) })
	if err := lc.ApproveGrant(ctx, Grant{PluginID: "p", Kind: PermReadCatalog, AllLibraries: true}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(ls.grants["p"]) != 1 || !ls.grants["p"][0].Approved {
		t.Fatalf("grant not persisted approved, got %+v", ls.grants["p"])
	}
	if got := ls.approvalBinding["p"][PermReadCatalog]; got != strings.Repeat("1", 64) {
		t.Fatalf("approval binding = %q, want active manifest binding", got)
	}
	if len(audit.actions) != 1 || audit.actions[0] != store.ActivityPluginGrant {
		t.Fatalf("expected one grant audit, got %v", audit.actions)
	}
}

func TestApproveGrantRejectsPermissionAbsentFromVerifiedManifest(t *testing.T) {
	ls := newFakeLifecycleStore()
	ls.declared["p"] = []string{PermStatusWrite}
	ls.activeBinding["p"] = strings.Repeat("1", 64)
	lc := NewLifecycle(ls, &recordingAudit{}, time.Now)
	if err := lc.ApproveGrant(context.Background(), Grant{
		PluginID: "p", Kind: PermReadCatalog, AllLibraries: true,
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("approve undeclared permission = %v, want permission denied", err)
	}
	if len(ls.grants["p"]) != 0 {
		t.Fatalf("undeclared permission persisted: %+v", ls.grants["p"])
	}
}

func TestOutboundOriginApprovalRequiresDeclarationAndReconcilesUpdates(t *testing.T) {
	ctx := context.Background()
	ls := newFakeLifecycleStore()
	audit := &recordingAudit{}
	lc := NewLifecycle(ls, audit, func() time.Time { return time.Unix(0, 0) })

	if err := lc.RegisterOutboundDeclaration(ctx, plugin.OutboundDeclaration{
		PluginID: "p",
		Origins:  []string{"https://kept.example.test", "https://removed.example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := lc.ApproveOutboundOrigin(ctx, "p", "https://undeclared.example.test"); err == nil {
		t.Fatal("approved an origin the plugin did not declare")
	}
	if err := lc.ApproveOutboundOrigin(ctx, "p", "https://kept.example.test"); err != nil {
		t.Fatal(err)
	}
	policy, ok, err := lc.OutboundPolicy(ctx, "p")
	if err != nil || !ok || !policy.Allows("https://kept.example.test") {
		t.Fatalf("approved policy = %+v ok=%v err=%v", policy, ok, err)
	}
	if len(audit.actions) != 1 || audit.actions[0] != store.ActivityPluginGrant {
		t.Fatalf("approval audit = %v, want one plugin grant", audit.actions)
	}
	if got := audit.entries[0]; got.TargetType != "plugin_outbound_origin.approve_requested" ||
		got.TargetID != "p|https://kept.example.test" {
		t.Fatalf("approval audit = %+v, want normalized source-specific request", got)
	}

	if err := lc.RegisterOutboundDeclaration(ctx, plugin.OutboundDeclaration{
		PluginID: "p",
		Origins:  []string{"https://kept.example.test", "https://added.example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	policy, ok, err = lc.OutboundPolicy(ctx, "p")
	if err != nil || !ok {
		t.Fatalf("reconciled policy missing: ok=%v err=%v", ok, err)
	}
	if !policy.Allows("https://kept.example.test") {
		t.Fatal("unchanged origin lost approval")
	}
	if policy.Allows("https://removed.example.test") || policy.Allows("https://added.example.test") {
		t.Fatalf("manifest update failed closed: %+v", policy)
	}
	if err := lc.RevokeOutboundOrigin(ctx, "p", "https://kept.example.test"); err != nil {
		t.Fatal(err)
	}
	policy, _, _ = lc.OutboundPolicy(ctx, "p")
	if policy.Allows("https://kept.example.test") {
		t.Fatal("revoked origin remained approved")
	}
	if len(audit.entries) != 2 {
		t.Fatalf("outbound audit count = %d, want approve and revoke", len(audit.entries))
	}
	if got := audit.entries[1]; got.TargetType != "plugin_outbound_origin.revoke_requested" ||
		got.TargetID != "p|https://kept.example.test" {
		t.Fatalf("revocation audit = %+v, want normalized source-specific request", got)
	}
}

func TestOutboundOriginAuditFailureLeavesPolicyUnchanged(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("audit unavailable")

	t.Run("approval remains denied", func(t *testing.T) {
		ls := newFakeLifecycleStore()
		lc := NewLifecycle(ls, &recordingAudit{}, time.Now)
		if err := lc.RegisterOutboundDeclaration(ctx, plugin.OutboundDeclaration{
			PluginID: "p", Origins: []string{"https://source.example.test"},
		}); err != nil {
			t.Fatal(err)
		}
		lc = NewLifecycle(ls, erroringAudit{err: wantErr}, time.Now)
		if err := lc.ApproveOutboundOrigin(ctx, "p", "https://source.example.test"); !errors.Is(err, wantErr) {
			t.Fatalf("approval audit failure = %v, want %v", err, wantErr)
		}
		policy, _, _ := lc.OutboundPolicy(ctx, "p")
		if policy.Allows("https://source.example.test") {
			t.Fatal("audit failure made outbound approval executable")
		}
	})

	t.Run("revocation remains at prior policy", func(t *testing.T) {
		ls := newFakeLifecycleStore()
		lc := NewLifecycle(ls, &recordingAudit{}, time.Now)
		if err := lc.RegisterOutboundDeclaration(ctx, plugin.OutboundDeclaration{
			PluginID: "p", Origins: []string{"https://source.example.test"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := lc.ApproveOutboundOrigin(ctx, "p", "https://source.example.test"); err != nil {
			t.Fatal(err)
		}
		lc = NewLifecycle(ls, erroringAudit{err: wantErr}, time.Now)
		if err := lc.RevokeOutboundOrigin(ctx, "p", "https://source.example.test"); !errors.Is(err, wantErr) {
			t.Fatalf("revocation audit failure = %v, want %v", err, wantErr)
		}
		policy, _, _ := lc.OutboundPolicy(ctx, "p")
		if !policy.Allows("https://source.example.test") {
			t.Fatal("revocation changed policy without a durable request audit")
		}
	})
}

func TestOutboundOriginPersistenceFailureLeavesRequestAuditAndPolicyUnchanged(t *testing.T) {
	ctx := context.Background()
	ls := newFakeLifecycleStore()
	lc := NewLifecycle(ls, &recordingAudit{}, time.Now)
	if err := lc.RegisterOutboundDeclaration(ctx, plugin.OutboundDeclaration{
		PluginID: "p", Origins: []string{"HTTPS://SOURCE.EXAMPLE.TEST:443"},
	}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("policy store unavailable")
	ls.setPolicyErr = wantErr
	audit := &recordingAudit{}
	lc = NewLifecycle(ls, audit, time.Now)
	if err := lc.ApproveOutboundOrigin(ctx, "p", "HTTPS://SOURCE.EXAMPLE.TEST:443"); !errors.Is(err, wantErr) {
		t.Fatalf("approval persistence failure = %v, want %v", err, wantErr)
	}
	policy, _, _ := lc.OutboundPolicy(ctx, "p")
	if policy.Allows("https://source.example.test") {
		t.Fatal("failed persistence changed executable policy")
	}
	if len(audit.entries) != 1 {
		t.Fatalf("request audit count = %d, want 1", len(audit.entries))
	}
	if got := audit.entries[0]; got.TargetType != "plugin_outbound_origin.approve_requested" ||
		got.TargetID != "p|https://source.example.test" {
		t.Fatalf("request audit = %+v, want truthful normalized approval request", got)
	}
}
