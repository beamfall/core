package conformance

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/manifest"
)

// The canonical brand accent comes from the web app's inlined copy of the
// generated design-system tokens; tools/gate/design-token-drift.mjs pins that
// copy to beamfall-design-system/tokens/generated/beamfall-tokens.css.
const (
	designTokensFile        = embeddedWebClientSrcDir + "/styles/tokens.css"
	navOverridePageFile     = embeddedWebClientSrcDir + "/areas/manage/pages/NavOverridePage.tsx"
	brandAccentPrimitiveVar = "beam-500"
)

// TestThemeAccentDefaultMatchesBrandToken fails when core's default theme
// accent, the web app's default accent and the design-system brand accent
// (Cinema --accent, which resolves to --beam-500) drift apart (CORE-1174).
func TestThemeAccentDefaultMatchesBrandToken(t *testing.T) {
	tokens, err := os.ReadFile(designTokensFile)
	if err != nil {
		t.Fatalf("read %s: %v", designTokensFile, err)
	}
	root := rootBlocks(t, string(tokens))
	if accent := cssVar(root, "accent"); accent != "var(--"+brandAccentPrimitiveVar+")" {
		t.Fatalf("%s :root --accent = %q, want var(--%s); update brandAccentPrimitiveVar if the brand accent moved",
			designTokensFile, accent, brandAccentPrimitiveVar)
	}
	brand := cssVar(root, brandAccentPrimitiveVar)
	if brand == "" {
		t.Fatalf("%s :root has no --%s", designTokensFile, brandAccentPrimitiveVar)
	}

	if !strings.EqualFold(manifest.DefaultAccentColor, brand) {
		t.Errorf("manifest.DefaultAccentColor = %q, want brand accent %q from %s", manifest.DefaultAccentColor, brand, designTokensFile)
	}

	page, err := os.ReadFile(navOverridePageFile)
	if err != nil {
		t.Fatalf("read %s: %v", navOverridePageFile, err)
	}
	m := regexp.MustCompile(`const DEFAULT_ACCENT = "(#[0-9a-fA-F]{6})";`).FindSubmatch(page)
	if m == nil {
		t.Fatalf("%s: DEFAULT_ACCENT constant not found", navOverridePageFile)
	}
	if web := string(m[1]); !strings.EqualFold(web, brand) {
		t.Errorf("web DEFAULT_ACCENT = %q, want brand accent %q from %s", web, brand, designTokensFile)
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
