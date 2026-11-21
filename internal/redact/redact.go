// Package redact removes secrets and identifying details from operator text
// before it leaves the process: job failure messages, and the recent server
// log lines the console reads (CORE-1716).
package redact

import (
	"net/netip"
	"regexp"
	"strings"
)

// Marker replaces a redacted secret. Log consumers still see a non-empty
// diagnostic token where the value was.
const Marker = "[REDACTED]"

// secretPatterns matches known secret classes:
//   - Beamfall API keys (bfk_…) and Jellyfin compat integration keys (bfj_…)
//   - Authorization header values (Bearer …, Basic …)
//   - URL query tokens (?token=…, &token=…)
//   - Stream and pack content paths (/api/media/…, /api/pack/…)
//   - Private endpoint URLs (http:// and https://)
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bf[kj]_[A-Za-z0-9_-]+)`),
	regexp.MustCompile(`(?i)(Bearer\s+\S+)`),
	regexp.MustCompile(`(?i)(Basic\s+\S+)`),
	regexp.MustCompile(`(?i)([?&]token=[^&\s"']+)`),
	regexp.MustCompile(`(?i)(/api/media/[^\s"']+)`),
	regexp.MustCompile(`(?i)(/api/pack/[^\s"']+)`),
	regexp.MustCompile(`(?i)(https?://[^\s"']+)`),
}

// Secrets returns s with every known secret class replaced by Marker.
func Secrets(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, Marker)
	}
	return s
}

// secretAttr matches a key=value pair whose key names a secret, as the slog
// text handler writes it: the value is either a quoted string or runs to the
// next space.
var secretAttr = regexp.MustCompile(`(?i)\b([\w.-]*(?:secret|password|passwd|passphrase|token|api_?key|apikey|authorization|cookie|credential|private_?key)[\w.-]*)=("(?:[^"\\]|\\.)*"|\S+)`)

// homePaths matches the user-name segment of a home directory path, on Unix
// (/Users/name, /home/name) and Windows (C:\Users\name, also with the
// doubled backslashes of a quoted log value).
var homePaths = []*regexp.Regexp{
	regexp.MustCompile(`(/(?:Users|home)/)[^/\s"'=]+`),
	regexp.MustCompile(`(?i)([A-Z]:(?:\\\\|\\)Users(?:\\\\|\\))[^\\\s"']+`),
}

// UserMarker replaces the user name in a home directory path.
const UserMarker = "[USER]"

// IPMarker replaces an IP address.
const IPMarker = "[IP]"

// ipCandidate finds runs that could hold an IPv4 or IPv6 address, with an
// optional port or zone. Each run is confirmed with netip before it is
// replaced, so times, durations and hashes stay readable.
var ipCandidate = regexp.MustCompile(`\[?[0-9A-Fa-f.:]*[.:][0-9A-Fa-f.:]*(?:%[0-9A-Za-z]+)?(?:\](?::[0-9]+)?)?`)

// LogLine returns one server log line with every secret, home-directory user
// name and IP address removed: the known secret classes, the value of any
// attribute whose key names a secret, the user segment of a home path, and
// any IPv4 or IPv6 address. It fails closed: a version string shaped like an
// IPv4 address is redacted too.
func LogLine(s string) string {
	s = Secrets(s)
	s = secretAttr.ReplaceAllString(s, "${1}="+Marker)
	for _, re := range homePaths {
		s = re.ReplaceAllString(s, "${1}"+UserMarker)
	}
	return ipCandidate.ReplaceAllStringFunc(s, redactIP)
}

// redactIP replaces a candidate run when it parses as an address, an
// address and port, or either followed by trailing punctuation.
func redactIP(run string) string {
	core := strings.TrimRight(run, ".:")
	tail := run[len(core):]
	if isAddress(core) {
		return IPMarker + tail
	}
	return run
}

func isAddress(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = s[1 : len(s)-1]
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	_, err := netip.ParseAddrPort(s)
	return err == nil
}
