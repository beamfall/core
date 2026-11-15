package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadmeWebUIBuildClaimMatchesCommittedBundle keeps the README's from-source
// instructions honest about what a clean clone embeds (CORE-1712). The tracked
// app-dist root is a placeholder; the real bundle is built into the ignored
// production subtree. If the tracked root ever becomes a real bundle, this test
// fails so the README is updated in the same change.
func TestReadmeWebUIBuildClaimMatchesCommittedBundle(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	index, err := os.ReadFile(filepath.Join("app-dist", "index.html"))
	if err != nil {
		t.Fatalf("read tracked app-dist/index.html: %v", err)
	}
	if !strings.Contains(string(index), "/web/assets/placeholder.js") {
		t.Fatal("tracked app-dist/index.html is no longer the placeholder; update the README's Web UI build instructions and this test")
	}
	text := strings.Join(strings.Fields(string(readme)), " ")
	for _, stale := range []string{
		"committed and embedded, so a plain Go build serves the UI",
		"Its committed production bundle is",
	} {
		if strings.Contains(text, stale) {
			t.Errorf("README claims a committed production bundle (%q), but only a placeholder is tracked", stale)
		}
	}
	for _, need := range []string{
		"npm --prefix internal/web/app ci",
		"npm --prefix internal/web/app run build",
		"internal/web/app-dist/production",
	} {
		if !strings.Contains(text, need) {
			t.Errorf("README does not state the Web UI build step %q", need)
		}
	}
}
