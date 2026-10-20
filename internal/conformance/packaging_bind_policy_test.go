package conformance

// SECAUDIT-20260817-TRUENAS-LOOPBACK-1: the TrueNAS template publishes the
// plaintext management HTTP port on the host. Its host-bind default must stay
// on the loopback interface so bootstrap/session traffic is never exposed on
// shared or hostile networks by default. Operators keep an explicit override
// (BEAMFALL_HTTP_BIND) for intentional non-loopback exposure behind a TLS or
// reverse-proxy boundary, and the README must document the same default the
// template ships.

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	truenasComposeBindDefaultPattern = regexp.MustCompile(`\$\{BEAMFALL_HTTP_BIND:-([^}]*)\}`)
	truenasReadmeBindDefaultPattern  = regexp.MustCompile("`BEAMFALL_HTTP_BIND` \\(default `([^`]+)`\\)")
)

// isLoopbackHostBind reports whether a compose host-bind value keeps the
// published port on the host loopback interface.
func isLoopbackHostBind(bind string) bool {
	trimmed := strings.Trim(bind, "[]")
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func truenasPackagingFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "packaging", "truenas", name)
	data, err := os.ReadFile(path) // #nosec G304 -- test reads a packaging file at a path derived from the repo root
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func truenasComposeBindDefault(t *testing.T) string {
	t.Helper()
	compose := truenasPackagingFile(t, "docker-compose.yaml")
	match := truenasComposeBindDefaultPattern.FindStringSubmatch(compose)
	if match == nil {
		t.Fatalf("packaging/truenas/docker-compose.yaml must retain the explicit operator override ${BEAMFALL_HTTP_BIND:-<default>} in its ports mapping")
	}
	return match[1]
}

// TestTrueNASTemplateBindDefaultIsLoopback is the packaging policy for the
// real template: a wildcard or otherwise non-loopback host-bind default fails.
func TestTrueNASTemplateBindDefaultIsLoopback(t *testing.T) {
	bindDefault := truenasComposeBindDefault(t)
	if !isLoopbackHostBind(bindDefault) {
		t.Fatalf("packaging/truenas/docker-compose.yaml defaults BEAMFALL_HTTP_BIND to %q; the plaintext management HTTP port must default to a loopback bind (127.0.0.1), with non-loopback exposure left to an explicit operator override behind a TLS/reverse-proxy boundary", bindDefault)
	}
}

// TestTrueNASReadmeAndTemplateBindDefaultsAgree pins the documented default to
// the template default so the README can never drift from what actually ships.
func TestTrueNASReadmeAndTemplateBindDefaultsAgree(t *testing.T) {
	readme := truenasPackagingFile(t, "README.md")
	match := truenasReadmeBindDefaultPattern.FindStringSubmatch(readme)
	if match == nil {
		t.Fatalf("packaging/truenas/README.md must document the bind default as `BEAMFALL_HTTP_BIND` (default `<value>`)")
	}
	documented := match[1]
	templateDefault := truenasComposeBindDefault(t)
	if documented != templateDefault {
		t.Fatalf("packaging/truenas/README.md documents BEAMFALL_HTTP_BIND default %q but the template ships %q; the README and template defaults must agree", documented, templateDefault)
	}
}

// TestLoopbackHostBindPolicy exercises the policy predicate itself: loopback
// defaults and explicit loopback overrides pass; wildcard and non-loopback
// values fail.
func TestLoopbackHostBindPolicy(t *testing.T) {
	cases := []struct {
		bind string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"[::1]", true},
		{"0.0.0.0", false},
		{"::", false},
		{"", false},
		{"192.168.1.10", false},
		{"beamfall.example", false},
	}
	for _, tc := range cases {
		if got := isLoopbackHostBind(tc.bind); got != tc.want {
			t.Errorf("isLoopbackHostBind(%q) = %v, want %v", tc.bind, got, tc.want)
		}
	}
}
