package conformance

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/manifest"
)

// The canonical brand accent comes from the web app's generated copy of the
// design-system tokens (internal/web/app/scripts/generate-theme-tokens.mjs, whose
// --check mode tools/gate/design-token-drift.mjs runs against the design-system
// release): the default preset's resolved dark accent, emitted both into
// tokens.css (--theme-dark-accent) and into theme.generated.ts.
const (
	designTokensFile        = embeddedWebClientSrcDir + "/styles/tokens.css"
	generatedThemeFile      = embeddedWebClientSrcDir + "/lib/theme/theme.generated.ts"
	navOverridePageFile     = embeddedWebClientSrcDir + "/areas/manage/pages/NavOverridePage.tsx"
	brandAccentPrimitiveVar = "beam-500"
	defaultAccentRoleVar    = "theme-dark-accent"
)

// TestThemeAccentDefaultMatchesBrandToken fails when core's default theme
// accent, the web app's default accent and the design-system brand accent
// (the default preset's dark accent, which is --beam-500) drift apart (CORE-1174).
func TestThemeAccentDefaultMatchesBrandToken(t *testing.T) {
	tokens, err := os.ReadFile(designTokensFile)
	if err != nil {
		t.Fatalf("read %s: %v", designTokensFile, err)
	}
	root := rootBlocks(t, string(tokens))
	brand := cssVar(root, defaultAccentRoleVar)
	if brand == "" {
		t.Fatalf("%s :root has no --%s", designTokensFile, defaultAccentRoleVar)
	}
	if primitive := cssVar(root, brandAccentPrimitiveVar); !strings.EqualFold(primitive, brand) {
		t.Fatalf("%s :root --%s = %q, want --%s %q; update brandAccentPrimitiveVar if the brand accent moved",
			designTokensFile, defaultAccentRoleVar, brand, brandAccentPrimitiveVar, primitive)
	}

	if !strings.EqualFold(manifest.DefaultAccentColor, brand) {
		t.Errorf("manifest.DefaultAccentColor = %q, want brand accent %q from %s", manifest.DefaultAccentColor, brand, designTokensFile)
	}

	generated, err := os.ReadFile(generatedThemeFile)
	if err != nil {
		t.Fatalf("read %s: %v", generatedThemeFile, err)
	}
	m := regexp.MustCompile(`export const THEME_DEFAULT_ACCENT = "(#[0-9a-fA-F]{6})";`).FindSubmatch(generated)
	if m == nil {
		t.Fatalf("%s: THEME_DEFAULT_ACCENT constant not found", generatedThemeFile)
	}
	if web := string(m[1]); !strings.EqualFold(web, brand) {
		t.Errorf("web THEME_DEFAULT_ACCENT = %q, want brand accent %q from %s", web, brand, designTokensFile)
	}

	page, err := os.ReadFile(navOverridePageFile)
	if err != nil {
		t.Fatalf("read %s: %v", navOverridePageFile, err)
	}
	if !strings.Contains(string(page), "const DEFAULT_ACCENT = THEME_DEFAULT_ACCENT;") {
		t.Errorf("%s: DEFAULT_ACCENT must be the generated THEME_DEFAULT_ACCENT", navOverridePageFile)
	}
}

// rootBlocks joins the bodies of the top-level `:root { ... }` blocks: the
// canonical primitives and the default (Cinema) theme roles.
func rootBlocks(t *testing.T, css string) string {
	t.Helper()
	var bodies []string
	for _, m := range regexp.MustCompile(`(?m)^:root\s*\{([^}]*)\}`).FindAllStringSubmatch(css, -1) {
		bodies = append(bodies, m[1])
	}
	if len(bodies) == 0 {
		t.Fatal("tokens.css has no :root block")
	}
	return strings.Join(bodies, "\n")
}

func cssVar(block, name string) string {
	m := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `:\s*([^;]+);`).FindStringSubmatch(block)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}
