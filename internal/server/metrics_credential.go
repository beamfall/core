package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// The /metrics scrape credential (GCR-012-H).
//
// Locality cannot be proven from the peer address: the default bind is
// 127.0.0.1:8800, so a reverse proxy is necessarily same-host and presents a
// loopback peer for traffic it relays from anywhere. The host-side signals in
// proxy.go narrow that, but they read hop headers the relaying proxy chose to
// add, and a bare `proxy_pass` adds none — so a same-host proxy that neither
// declares itself nor adds a hop header relays a request byte-for-byte
// identical to a host-local client's. No function of the request separates the
// two, so any policy that serves the second uncredentialed serves the first.
//
// The operator's decision (2026-08-07) is therefore to require the credential
// always rather than keep a carve-out no predicate can hold. The process mints
// a bearer at startup and writes it 0600 into its state directory, so the
// ability to scrape follows from the ability to read that file — something on
// this host, running as the server's user. That holds whatever the fronting
// proxy does and whether or not the operator declared it, which is what makes
// the invariant unconditional. The cost is named and accepted: an existing
// uncredentialed local scrape stops working at upgrade, and the metrics runbook
// leads with that.

// SetMetricsScrapeToken installs the scrape credential minted for this run.
// Only the digest is retained, so the plaintext lives in this package neither
// in memory nor anywhere it could reach a log line or an error body. An empty
// token clears the credential, which refuses every bearer — the fail-closed
// reading of a credential that could not be established.
func (s *Server) SetMetricsScrapeToken(token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		s.metricsScrapeDigest = nil
		return
	}
	digest := sha256.Sum256([]byte(token))
	s.metricsScrapeDigest = digest[:]
}

// metricsScrapeAllowed decides /metrics admission. There are exactly two ways
// in: the public opt-in, which keeps its meaning as the operator's explicit
// "serve this to whoever reaches it", and the scrape credential. Nothing else
// is served — proximity is not consulted at all, so the peer address, the
// fronting proxy's headers, and BEAMFALL_KNOWN_PROXIES cannot move the outcome
// and the invariant holds with any of them in any state.
func (s *Server) metricsScrapeAllowed(r *http.Request) bool {
	if s.publicMetrics {
		return true
	}
	return s.metricsScrapeCredentialAccepted(r)
}

// metricsScrapeCredentialAccepted compares the presented bearer against the
// installed credential in constant time. Both sides are reduced to a SHA-256
// digest first, so the comparison runs over a fixed width and the length of the
// presented value cannot steer it. Nothing here reaches a log or a response:
// the caller's refusal is one constant sentence for every failing shape, so a
// wrong bearer and an absent one are indistinguishable to the sender and the
// refusal never becomes an oracle.
//
// A run with no credential established needs no separate arm: the stored digest
// is then nil, and a constant-time comparison of a 32-byte digest against a
// zero-width one cannot report equal, so every bearer is refused.
func (s *Server) metricsScrapeCredentialAccepted(r *http.Request) bool {
	presented, ok := bearerCredential(r)
	if !ok {
		return false
	}
	digest := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(digest[:], s.metricsScrapeDigest) == 1
}

// bearerCredential extracts the credential from an Authorization header. Two
// shapes are refused outright rather than guessed at: more than one
// Authorization header, because there is no principled way to pick which one
// the sender meant, and a value that is not `Bearer …`, because reading another
// scheme's payload as a bearer would admit a credential its holder never
// offered here. The scheme is matched case-insensitively per RFC 7235 §2.1.
//
// Every other malformed shape needs no arm of its own: whatever is returned
// still has to equal the installed credential, and an empty value, a value with
// trailing text, or any other mangling does not. Refusing them separately would
// be a branch that cannot change an outcome. The value is trimmed because
// RFC 7235 §2.1 permits padding between the scheme and the credential, and
// because the same trim is applied to the installed value — a scraper reading
// the credential out of its file must not be refused over spacing.
func bearerCredential(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, credential, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return "", false
	}
	return strings.TrimSpace(credential), true
}
