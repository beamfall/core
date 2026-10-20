package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSHA256ManifestStrict(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := map[string]string{
		"uppercase":            strings.Repeat("A", 64) + "  file.json\n",
		"single separator":     digest + " file.json\n",
		"traversal":            digest + "  a/../file.json\n",
		"backslash":            digest + "  dir\\file.json\n",
		"normalized duplicate": digest + "  file.json\n" + digest + "  ./file.json\n",
		"surrounding space":    digest + "  file.json \n",
		"malformed":            "malformed\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := filepath.Join(t.TempDir(), "manifest.sha256")
			if err := os.WriteFile(manifest, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := parseSHA256Manifest(manifest); err == nil {
				t.Fatalf("accepted invalid manifest %q", content)
			}
		})
	}
	manifest := filepath.Join(t.TempDir(), "manifest.sha256")
	if err := os.WriteFile(manifest, []byte("\n # comment\n"+digest+"  ./file.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := parseSHA256Manifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got["file.json"] != digest {
		t.Fatalf("normalized manifest = %v", got)
	}
}
