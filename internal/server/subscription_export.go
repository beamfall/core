package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/beamfall/core/internal/store"
)

const (
	subscriptionExportPath        = "/api/subscriptions/export.opml"
	subscriptionExportContentType = "application/xml; charset=utf-8"
	// Keep the export within the OPML importer's outline and document limits.
	maxSubscriptionOPMLOutlines = 10_000
	maxSubscriptionOPMLBytes    = 64 << 20
	subscriptionOPMLStart       = xml.Header + `<opml version="2.0"><head><title>Subscriptions</title></head><body>`
	subscriptionOPMLEnd         = "</body></opml>\n"
)

var errSubscriptionOPMLLimit = errors.New("subscription OPML exceeds import limits")

type subscriptionExportStore interface {
	Libraries(context.Context, store.LibraryFilter) ([]store.Library, error)
	Subscriptions(context.Context, store.LibraryID) ([]store.Subscription, error)
}

type subscriptionOutline struct {
	XMLName xml.Name `xml:"outline"`
	Type    string   `xml:"type,attr"`
	Text    string   `xml:"text,attr"`
	Title   string   `xml:"title,attr,omitempty"`
	XMLURL  string   `xml:"xmlUrl,attr"`
}

// Expose only Write so the XML encoder cannot bypass the byte limit via WriteString.
type subscriptionOPMLWriter struct{ body *bytes.Buffer }

func (w subscriptionOPMLWriter) Write(p []byte) (int, error) {
	if len(p) > maxSubscriptionOPMLBytes-len(subscriptionOPMLEnd)-w.body.Len() {
		return 0, errSubscriptionOPMLLimit
	}
	return w.body.Write(p)
}

func (s *Server) exportSubscriptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	scope, err := s.auth.ScopeFromRequest(ctx, r)
	if err != nil {
		writeError(w, r, http.StatusUnauthorized, "invalid session")
		return
	}
	if scope.SessionID == "" || scope.APITokenID != "" || scope.APIKeyID != "" || scope.DeviceRole != store.DeviceRoleManager {
		writeError(w, r, http.StatusForbidden, "manager session required")
		return
	}
	if s.subscriptionExport == nil {
		writeError(w, r, http.StatusServiceUnavailable, "subscription export unavailable")
		return
	}

	includePrivate := r.URL.Query().Get("includePrivate") == "true"
	body, err := s.subscriptionOPML(ctx, scope.LibraryFilter(), includePrivate, r.Host)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "could not export subscriptions")
		return
	}
	w.Header().Set("Content-Type", subscriptionExportContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="subscriptions.opml"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body) //nolint:gosec // G705: XML-escaped OPML attachment with nosniff, never rendered as HTML
}

func (s *Server) subscriptionOPML(ctx context.Context, filter store.LibraryFilter, includePrivate bool, requestHost string) ([]byte, error) {
	libraries, err := s.subscriptionExport.Libraries(ctx, filter)
	if err != nil {
		return nil, err
	}
	if s.subscriptionSealed != nil {
		// A fresh query could admit a restored Library whose seal was never checked.
		libraries = slices.DeleteFunc(libraries, func(lib store.Library) bool {
			return s.subscriptionSealed(lib.ID)
		})
	}
	requestHostname := (&url.URL{Host: requestHost}).Hostname()

	var body bytes.Buffer
	body.WriteString(subscriptionOPMLStart)
	encoder := xml.NewEncoder(subscriptionOPMLWriter{body: &body})
	count := 0
	for _, library := range libraries {
		subscriptions, err := s.subscriptionExport.Subscriptions(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		for _, sub := range subscriptions {
			feedURL, private, ok := s.exportableFeedURL(sub, requestHostname)
			if !ok || (private && !includePrivate) {
				continue
			}
			if count == maxSubscriptionOPMLOutlines {
				return nil, errSubscriptionOPMLLimit
			}
			outline := subscriptionOutline{Type: "rss", Text: sub.Title, Title: sub.Title, XMLURL: feedURL}
			if private {
				// A private feed's stored title can name its subscriber.
				outline.Text = feedURL
				outline.Title = ""
			}
			if err := encoder.Encode(outline); err != nil {
				return nil, err
			}
			count++
		}
	}
	body.WriteString(subscriptionOPMLEnd)
	return body.Bytes(), nil
}

// exportableFeedURL returns the Subscription's feed URL without credentials
// and whether the feed is private. A feed is private when it has an AuthRef
// or its URL carries userinfo or a secret-looking query parameter; those are
// removed from the returned URL. ok is false for a feed that cannot be
// exported at all: one on this server's own host, or one whose path holds a
// secret that cannot be removed without changing the feed.
func (s *Server) exportableFeedURL(sub store.Subscription, requestHostname string) (feedURL string, private, ok bool) {
	u, err := url.Parse(sub.FeedURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false, false
	}
	if sameHostname(u.Hostname(), requestHostname) || (s.baseURL != nil && sameHostname(u.Hostname(), s.baseURL.Hostname())) {
		return "", false, false
	}
	for _, segment := range strings.Split(u.Path, "/") {
		// Mixed-case titles with punctuation are common in paths, so only a
		// UUID or an unbroken run of letters and digits counts as a secret.
		segment = strings.TrimSuffix(segment, path.Ext(segment))
		if secretLike(segment, minSecretPathSegment) && (isUUID(segment) || !strings.ContainsAny(segment, secretPunctuation)) {
			return "", false, false
		}
	}
	private = sub.AuthRef != "" || u.User != nil
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	if u.RawQuery != "" {
		kept := make([]string, 0, strings.Count(u.RawQuery, "&")+1)
		for _, pair := range strings.Split(u.RawQuery, "&") {
			// Some servers also split parameters on semicolons.
			if slices.ContainsFunc(strings.Split(pair, ";"), secretQueryParam) {
				private = true
				continue
			}
			kept = append(kept, pair)
		}
		u.RawQuery = strings.Join(kept, "&")
	}
	u.ForceQuery = false
	return u.String(), private, true
}

const (
	// Path segments are often long slugs, so only long opaque values count.
	minSecretPathSegment = 32
	minSecretQueryValue  = 16
	secretPunctuation    = "-_.~+/=%"
)

var secretQueryNameParts = []string{"token", "key", "secret", "pass", "auth", "sig", "session", "cred", "pwd"}

// secretQueryParam reports whether one raw name=value query parameter has a
// credential-like name or an opaque value.
func secretQueryParam(param string) bool {
	rawName, rawValue, _ := strings.Cut(param, "=")
	name, err := url.QueryUnescape(rawName)
	if err != nil {
		return true
	}
	name = strings.ToLower(name)
	return secretLike(rawValue, minSecretQueryValue) ||
		slices.ContainsFunc(secretQueryNameParts, func(part string) bool { return strings.Contains(name, part) })
}

// secretLike reports whether value looks like an opaque credential: a UUID,
// or a long run of letters and digits. Values with punctuation (as in
// base64 or escaped text) must also mix letter case, so lowercase slugs are
// not mistaken for secrets.
func secretLike(value string, minLen int) bool {
	if isUUID(value) {
		return true
	}
	if len(value) < minLen {
		return false
	}
	var digit, lower, upper, separator bool
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case strings.ContainsRune(secretPunctuation, r):
			separator = true
		default:
			return false
		}
	}
	return digit && (lower || upper) && (!separator || (lower && upper))
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// sameHostname compares hostnames case-insensitively, ignoring a trailing dot,
// and compares IP literals as addresses so every spelling of one IP matches.
func sameHostname(a, b string) bool {
	a, b = strings.TrimSuffix(a, "."), strings.TrimSuffix(b, ".")
	if a == "" || b == "" {
		return false
	}
	if addrA, err := netip.ParseAddr(a); err == nil {
		addrB, err := netip.ParseAddr(b)
		return err == nil && addrA.WithZone("").Unmap() == addrB.WithZone("").Unmap()
	}
	return strings.EqualFold(a, b)
}
