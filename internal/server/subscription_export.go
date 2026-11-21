package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
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
			feedURL, host, public, ok := s.exportableFeedURL(sub, requestHostname)
			if !ok || (!public && !includePrivate) {
				continue
			}
			if count == maxSubscriptionOPMLOutlines {
				return nil, errSubscriptionOPMLLimit
			}
			outline := subscriptionOutline{Type: "rss", Text: sub.Title, Title: sub.Title, XMLURL: feedURL}
			if !public {
				// A private feed's stored title can name its subscriber, and
				// its URL can carry a token that an importer would show as the
				// title. OPML requires text, so use the host.
				outline.Text = host
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

// exportableFeedURL returns the Subscription's feed URL, its host and whether
// the feed is public. The store records no directory provenance for
// Subscriptions, so the only positive evidence of a public feed is a clean
// URL: http(s), no AuthRef, userinfo, query or fragment. Every other feed is unclassified; its
// URL is returned complete, with only userinfo removed, so it re-imports as
// the same feed. ok is false for a feed that is never exported: one on this
// server's own host, or one whose URL is not an absolute http(s) URL.
func (s *Server) exportableFeedURL(sub store.Subscription, requestHostname string) (feedURL, host string, public, ok bool) {
	u, err := url.Parse(sub.FeedURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", false, false
	}
	if sameHostname(u.Hostname(), requestHostname) || (s.baseURL != nil && sameHostname(u.Hostname(), s.baseURL.Hostname())) {
		return "", "", false, false
	}
	public = sub.AuthRef == "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
	u.User = nil
	return u.String(), u.Hostname(), public, true
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
