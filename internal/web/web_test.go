package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestAppAssetsAreEmbedded(t *testing.T) {
	assets := AppAssets()
	for _, name := range []string{"index.html", "manifest.webmanifest", "sw.js", "favicon.svg", "manifest-icon.svg"} {
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	for _, needle := range []string{
		`<div id="root"></div>`,
		`href="/web/manifest.webmanifest"`,
		`src="/web/assets/`,
		`href="/web/assets/`,
	} {
		if !strings.Contains(html, needle) {
			t.Fatalf("index.html does not contain %q", needle)
		}
	}
	for _, match := range regexp.MustCompile(`(?:src|href)="/web/(assets/[^"]+)"`).FindAllStringSubmatch(html, -1) {
		if _, err := fs.Stat(assets, match[1]); err != nil {
			t.Fatalf("index.html references missing app asset %s: %v", match[1], err)
		}
	}
}
