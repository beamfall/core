package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePrefix = "github.com/beamfall/core/internal/"

// archWhitelist mirrors ARCHITECTURE.md §4 for the packages that exist today:
// each internal package may import only the listed internal packages. A
// package or import edge not listed here is a failure, and every key and
// listed target must be a real internal/<pkg> dir (TestArchWhitelistReferences
// real packages, below — the graph is hand-rolled, so the phantom
// media/entity/library/profile/engagement/realtime packages the predecessor's
// SDL-codegen design implied never existed). Subtrees of internal/store
// (storetest, migrate) count as `store`.
var archWhitelist = map[string][]string{
	"apptest":           {"app", "config", "plugin", "store"}, // in-process E2E test harness (mirrors store/storetest, but top-level): boots the app composition root over httptest for the I/O E2E track. Test-support only.
	"ioe2e":             {},                                   // I/O E2E journey package: only io_e2e-tagged *_test.go files (imports apptest/ffmpeg from tests, which the arch walk skips) + an empty untagged doc.go. No production imports.
	"capabilities":      {},                                   // leaf; compile/runtime capability self-report for clients and CI manifest checks (POWERTOOLS-5)
	"config":            {"serverrole"},                       // bootstrap config carries the ADR-0123 §2 attached/main Role
	"serverrole":        {},                                   // leaf; the ADR-0123 §2 two-face authority Role (Main/Attached) + GuardAuthority sentinel, stdlib only, no internal deps
	"nodehost":          {},                                   // leaf; ADR-0123 §5 Origin-Node enrollment lifecycle + ADR-0063 origin-node Compatibility Handshake gate + per-node revoke/rotate (NODE-3), stdlib only over real x509 material, no internal deps
	"nodehostd":         {"nodehost"},                         // node-facing mTLS SESSION transport (ADR-0129 §5: the node dials the main): main-side listener + node-side dialer that pin SPKI in both directions. Composes nodehost's pin logic; nodehost stays the leaf. The app composition root prepares the cloned self-signed mTLS config + policy and passes them in, so no remoteaccess/config edge here.
	"observability":     {},                                   // leaf; ADR-0146 local OTel metrics controller + Prometheus exporter, wired through server/graph interfaces
	"conductor":         {"store"},                            // Ambient Group Conductor (ADR-0035 §8, AV-4): in-memory ephemeral group registry + NTP/drift math; imports store only for its id/time types
	"decodeverify":      {"store", "jobs"},                    // decode-verify sampling job (FEAT-8 / ADR-0009 §9): flags corrupt/truncated files broken; the ffmpeg DecodeVerify seam is a consumer-side interface, so no ffmpeg edge (same shape as genassets)
	"discovery":         {},
	"graphquery":        {},             // leaf; pure GraphQL operation selection and top-level field parsing for the hand-written graph dispatcher
	"graphshape":        {},             // leaf; generic JSON-shape reflection walker used by graph's nested schema artifact
	"jsonstate":         {},             // leaf; atomic JSON file load/save helper for private state stores
	"ffmpeg":            {},             // leaf; the ONE FFmpeg boundary
	"filmtvintegration": {},             // ORG-52: core-side integration test for the Film & TV migration sources (now in beamfall-plugin-filmtv). Only a doc.go package clause + a *_test.go (test imports — jobs/store/migrate + the plugin module — are skipped by the arch walk); no production imports.
	"governor":          {},             // leaf; resource arbitration counters
	"causation":         {},             // leaf; ADR-0125 server-global Integration Worker chain budget, per-worker action rate, and bounded/coalesced abort surface
	"backup":            {"libencrypt"}, // leaf; ZK off-site backup transport (ADR-0115 §1/§2/§4/§5, BACKUP-1): BBK1 envelope (ephemeral DEK wrapped under BackupKey) + BucketClient seam; domain-agnostic, no store/graph/entitlement; depends only on libencrypt for the AES-256-GCM DEK wrap + EncryptStream/DecryptStream
	"libencrypt":        {},             // leaf; per-Encrypted-Library key model (ADR-0045 §2.4, ENC-2) — DEK/KEK/passphrase-KDF/Recovery-Key + Sealed/Unsealed; domain-agnostic crypto over stdlib + x/crypto, persists via a consumer-side SecretStore seam (no internal deps)
	"store":             {"serverrole"}, // the only importer of the SQLite driver; imports serverrole for the ADR-0123 §2 attached-authority guard on catalog/engagement authors
	"racefixture":       {},             // leaf; test-only deliberately-racy RED-proving fixture for the -race tier (ADR-0121 §3, TEST-CORE-1). Only a doc.go package clause + a *_test.go; no production imports.
	"push":              {},             // leaf; ADR-0039 push primitive — persisting registrar + APNs/FCM/Web-Push dispatcher (owns its store/HTTP/credential seams, no internal deps)
	"release":           {},             // leaf; supply-chain integrity core (PANEL-11 / ADR-0013 §A) — signed release manifest, ed25519 sign/verify, pinned trust anchor, SBOM/provenance builders; stdlib only, no internal deps
	"reveal":            {},
	"skins":             {},                              // leaf; user-authored color Skins (ADR-0125) — server-side WCAG-2.2 contrast math for hard-role validation, stdlib only, no internal deps
	"compat":            {"store", "auth"},               // ADR-0171 §2.2/§2.4 Jellyfin compat route group (JFCOMPAT-3): default-off, integration-session auth via auth, store for session/lookup; no other internal deps
	"streamtoken":       {"store", "auth", "serverrole"}, // serverrole gates Issue on the ADR-0123 §2 / ADR-0129 §1 attached authority face
	"updatecheck":       {"release"},                     // opt-in version check (ADR-0013 §7); imports release to verify the signed channel manifest before blessing an update (PANEL-11)
	"jobs":              {"store"},
	"auth":              {"store"},
	"beam":              {"store", "auth"},
	"pairing":           {"store", "auth"},
	"artwork":           {"store", "jobs"},
	"genassets":         {"store", "jobs"},
	"skipdetect":        {"store", "jobs", "ffmpeg"}, // skip-segment detector (ENR-7 / ADR-0048): byte-level intro/recap/credits detection over the generic relation graph; imports store (markers + sibling walk), jobs (the sweep handler), ffmpeg (the hardened chapter/fingerprint seam)
	"scan":              {"store", "jobs", "ffmpeg"},
	"migrate":           {"store", "jobs"}, // Migration Wizard import job handler + match chain (ADR-0037)
	"identify":          {"store", "artwork", "scrape", "plugin"},
	"ingest":            {"store", "jobs", "scan", "scrape"},
	"playback":          {"store", "auth", "ffmpeg", "governor", "streamtoken"},
	"mediahttp":         {"store", "artwork", "genassets", "playback", "streamtoken", "auth"},
	"proxytee":          {"store", "auth", "scrape", "streamtoken"}, // ADR-0052 §2.1: Stream-Token'd proxy-tee stream + provisional→archived promotion; origin fetch through scrape's SSRF gate
	"offlinepack":       {"store", "auth", "streamtoken"},           // ADR-0098 area 6: generic, domain-agnostic Offline Pack substrate — pack rows, Pack-subject Stream Token grants, target validation, manifest building + http.HandlerFunc serving with revoke/relock/seal byte denial. Music target resolvers live in the Music plugin, registered via a core adapter; never here.
	"photoexport":       {"store"},                                  // ADR-0045 §2.9 mode 2: bulk photo-export job substrate — emits byte-exact Custodial-Library originals + a Takeout-style sidecar manifest. Reads the plane-aware store walk; decrypts Encrypted-Library bytes through a composition-wired ByteDecryptor seam. Domain-agnostic (invariant 1): album membership is a generic (relation, kind, title) tuple.

	"plugin":          {"capabilities"}, // near-leaf; package manifests validate capability IDs against the flat namespace (PLUGCONTRACT-CHAIN-1)
	"qbittorrent":     {"store", "jobs", "jsonstate"},
	"integrationhost": {"causation", "store", "plugin"}, // ADR-0058/0125 third-party Integration Worker runtime: artifact verify, out-of-process supervisor + Host API, grants/lifecycle, and mandatory causation admission before mutation seams. Domain-agnostic; imports store (catalog/event/secret/audit row types), plugin (the closed permission vocabulary), and the causation leaf. Reveal/jobs/ingest reach it through consumer-side seams the composition root adapts, so no direct edge.
	"webhook":         {"store", "jsonstate"},           // Webhook Emitter Integration Plugin: consumes the event outbox (ADR-0040 §2.5)
	"scrape":          {"plugin"},

	"watcher":      {"store"},
	"schedule":     {"store"},
	"manifest":     {"store", "plugin", "auth"},
	"web":          {},                     // leaf; the go:embed shim only
	"remoteaccess": {"config"},             // direct-HTTPS/ACME + relay carrier; reads config, owns autocert (ADR-0012/0018/0025)
	"moderation":   {"store", "jsonstate"}, // App Store Guideline 1.2 UGC substrate (NATIVEMGR-4): file-backed report/block/contact store; imports store only for its ProfileID/DeviceID id types (no schema/graph)
	"server":       {"auth", "beam", "capabilities", "manifest", "moderation", "pairing", "mediahttp", "playback", "reveal", "store", "streamtoken", "web"},
}

// archAnythingExcept lists packages whose §4 row is "anything except …".
var archAnythingExcept = map[string][]string{
	"graph": {"server", "app"}, // any capability; never server, app
	"app":   {"conformance"},   // composition root
}

var integrationPluginPackages = map[string]bool{
	"integrationhost": true,
	"qbittorrent":     true,
	"webhook":         true,
}

var workflowAllowedInternalImports = map[string]bool{
	"ffmpeg":   true,
	"governor": true,
	"jobs":     true,
	"store":    true,
}

var workflowForbiddenImports = map[string]bool{
	"database/sql": true,
	"net":          true,
	"net/http":     true,
	"os":           true,
	"os/exec":      true,
	"syscall":      true,
}

func TestArch(t *testing.T) {
	internalDir := requireRoot(t, "internal")
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkg := entry.Name()
		if pkg == "conformance" {
			continue // the enforcement layer may import anything
		}
		if !hasGoFiles(t, filepath.Join(internalDir, pkg)) {
			continue // empty/untracked scratch dirs are not packages
		}
		imports := internalImports(t, filepath.Join(internalDir, pkg))
		checked++
		if forbidden, ok := archAnythingExcept[pkg]; ok {
			for imported := range imports {
				for _, banned := range forbidden {
					if imported == banned {
						t.Errorf("internal/%s imports internal/%s, forbidden by ARCHITECTURE.md §4", pkg, banned)
					}
				}
			}
			continue
		}
		allowed, ok := archWhitelist[pkg]
		if !ok {
			t.Errorf("internal/%s is not listed in the §4 whitelist — add it to ARCHITECTURE.md §4 and this test", pkg)
			continue
		}
		for imported := range imports {
			if !contains(allowed, imported) {
				t.Errorf("internal/%s imports internal/%s, not allowed by ARCHITECTURE.md §4 row %q", pkg, imported, pkg)
			}
		}
	}
	if checked == 0 {
		t.Fatal("arch gate checked zero packages")
	}
}

func TestStreamTokenCompatKeepsBytePackagesCapabilitiesFree(t *testing.T) {
	internalDir := requireRoot(t, "internal")
	for _, pkg := range []string{"playback", "mediahttp"} {
		if internalImports(t, filepath.Join(internalDir, pkg))["capabilities"] {
			t.Errorf("internal/%s imports internal/capabilities; compatibility must stay behind the server adapter", pkg)
		}
	}
}

func TestAppImportsOnlyPluginSDKAmongPluginModules(t *testing.T) {
	enforceAppPluginImportBoundary(t, filepath.Join(repoRoot(t), "internal", "app"), func(imported string) {
		t.Errorf("internal/app imports plugin module %q; external plugin composition belongs in cmd/beamfall and only plugin-sdk is allowed", imported)
	})
}

func TestAppPluginImportBoundaryRejectsProductionAndTestViolations(t *testing.T) {
	dir := t.TempDir()
	for name, source := range map[string]string{
		"production.go": `package fixture
import _ "github.com/beamfall/plugin-production-violation"
`,
		"fixture_test.go": `package fixture
import _ "github.com/beamfall/plugin-test-violation"
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	imports := map[string]bool{}
	enforceAppPluginImportBoundary(t, dir, func(imported string) {
		imports[imported] = true
	})
	for _, want := range []string{
		"github.com/beamfall/plugin-production-violation",
		"github.com/beamfall/plugin-test-violation",
	} {
		if !imports[want] {
			t.Errorf("seeded plugin-module violation %q was not detected", want)
		}
	}
	production := packageImports(t, dir)
	if !production["github.com/beamfall/plugin-production-violation"] {
		t.Error("production import walker missed a production dependency")
	}
	if production["github.com/beamfall/plugin-test-violation"] {
		t.Error("production import walker accepted a test-only dependency")
	}
}

func enforceAppPluginImportBoundary(t *testing.T, dir string, report func(string)) {
	t.Helper()
	const sdkModule = "github.com/beamfall/plugin-sdk"
	for imported := range allGoPackageImports(t, dir) {
		if imported == sdkModule || strings.HasPrefix(imported, sdkModule+"/") {
			continue
		}
		if strings.HasPrefix(imported, "github.com/beamfall/plugin-") || strings.HasPrefix(imported, "github.com/beamfall/beamfall-plugin-") {
			report(imported)
		}
	}
}

func TestAppHookContractUsesShapeNeutralVocabulary(t *testing.T) {
	path := filepath.Join(repoRoot(t), "internal", "app", "hooks.go")
	// #nosec G304 -- path is assembled only from the repository root and fixed literals.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, forbidden := range []string{"Music", "Album", "MBID"} {
		if strings.Contains(source, forbidden) {
			t.Errorf("internal/app hook contract contains domain vocabulary %q", forbidden)
		}
	}
}

func TestWorkflowPluginTier(t *testing.T) {
	workflowDir := filepath.Join(repoRoot(t), "internal", "workflow")
	entries, err := os.ReadDir(workflowDir)
	if errorsIsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !hasGoFiles(t, filepath.Join(workflowDir, entry.Name())) {
			continue
		}
		imports := packageImports(t, filepath.Join(workflowDir, entry.Name()))
		for imported := range imports {
			if workflowForbiddenImports[imported] || strings.HasPrefix(imported, "net/") {
				t.Errorf("internal/workflow/%s imports %q, forbidden by ARCHITECTURE.md §5.2 Workflow Safety Corollary", entry.Name(), imported)
			}
			if strings.HasPrefix(imported, modulePrefix) {
				target := strings.SplitN(strings.TrimPrefix(imported, modulePrefix), "/", 2)[0]
				if !workflowAllowedInternalImports[target] {
					t.Errorf("internal/workflow/%s imports internal/%s; Workflow Plugins may only compose approved core seams", entry.Name(), target)
				}
			}
		}
	}
}

func TestIntegrationPluginNoDirectWriterAuthority(t *testing.T) {
	internalDir := requireRoot(t, "internal")
	for pkg := range integrationPluginPackages {
		dir := filepath.Join(internalDir, pkg)
		if !hasGoFiles(t, dir) {
			t.Fatalf("Integration Plugin tier package internal/%s is missing", pkg)
		}
		assertNoDirectWriterAuthority(t, dir, pkg)
	}
}

func hasGoFiles(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			return true
		}
	}
	return false
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}

// TestArchWhitelistReferencesRealPackages fails if any whitelist key or listed
// import target is not a real internal/<pkg> dir. It is the guard against the
// drift that let phantom packages (media/entity/library/profile/engagement/
// realtime — implied by the predecessor's never-built SDL-codegen design) sit
// in the §4 whitelist: a permissive entry never fails TestArch because nothing
// imports it, so only this existence check catches it.
func TestArchWhitelistReferencesRealPackages(t *testing.T) {
	internalDir := requireRoot(t, "internal")
	real := map[string]bool{}
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			real[entry.Name()] = true
		}
	}
	check := func(name, role string) {
		if !real[name] {
			t.Errorf("arch whitelist references %q (%s) but internal/%s does not exist", name, role, name)
		}
	}
	for pkg, targets := range archWhitelist {
		check(pkg, "row")
		for _, target := range targets {
			check(target, "import target of "+pkg)
		}
	}
	for pkg, targets := range archAnythingExcept {
		check(pkg, "row")
		for _, target := range targets {
			check(target, "forbidden target of "+pkg)
		}
	}
}

// internalImports returns the set of first-level internal packages imported by
// the non-test Go files in dir and its subdirectories. Imports within the same
// package subtree (e.g. store/storetest importing store) are not edges.
func internalImports(t *testing.T, dir string) map[string]bool {
	t.Helper()
	pkg := filepath.Base(dir)
	imports := map[string]bool{}
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			value := strings.Trim(imported.Path.Value, `"`)
			if !strings.HasPrefix(value, modulePrefix) {
				continue
			}
			target := strings.SplitN(strings.TrimPrefix(value, modulePrefix), "/", 2)[0]
			if target == pkg {
				continue
			}
			imports[target] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return imports
}

// packageImports returns production imports only. Architecture and reachability
// checks must not accept a dependency wired solely by a _test.go file.
func packageImports(t *testing.T, dir string) map[string]bool {
	t.Helper()
	return walkPackageImports(t, dir, false)
}

// allGoPackageImports includes test files. The internal/app plugin-module
// firewall covers test code too so tests cannot reintroduce first-party
// composition below cmd/beamfall.
func allGoPackageImports(t *testing.T, dir string) map[string]bool {
	t.Helper()
	return walkPackageImports(t, dir, true)
}

func walkPackageImports(t *testing.T, dir string, includeTests bool) map[string]bool {
	t.Helper()
	imports := map[string]bool{}
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			imports[strings.Trim(imported.Path.Value, `"`)] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return imports
}

func assertNoDirectWriterAuthority(t *testing.T, dir, pkg string) {
	t.Helper()
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.TypeSpec:
				if exposesDirectWriter(node.Type) {
					t.Errorf("internal/%s exposes direct os.File/io.Writer authority in %s:%s; route file mutation through managed-file-ops", pkg, path, node.Name.Name)
				}
			}
			return true
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func exposesDirectWriter(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.StarExpr:
		return isSelector(v.X, "os", "File") || exposesDirectWriter(v.X)
	case *ast.ArrayType:
		return exposesDirectWriter(v.Elt)
	case *ast.MapType:
		return exposesDirectWriter(v.Key) || exposesDirectWriter(v.Value)
	case *ast.ChanType:
		return exposesDirectWriter(v.Value)
	case *ast.StructType:
		for _, field := range v.Fields.List {
			if exposesDirectWriter(field.Type) {
				return true
			}
		}
	case *ast.InterfaceType:
		for _, method := range v.Methods.List {
			if isSelector(method.Type, "io", "Writer") || exposesDirectWriter(method.Type) {
				return true
			}
		}
	case *ast.FuncType:
		return fieldListExposesDirectWriter(v.Params) || fieldListExposesDirectWriter(v.Results)
	}
	return false
}

func fieldListExposesDirectWriter(fields *ast.FieldList) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if isSelector(field.Type, "io", "Writer") || exposesDirectWriter(field.Type) {
			return true
		}
	}
	return false
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
