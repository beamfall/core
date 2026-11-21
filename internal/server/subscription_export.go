package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
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
	_, _ = w.Write(body)
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
			private := sub.AuthRef != ""
			if (private && !includePrivate) || !s.exportableSubscriptionURL(sub.FeedURL, requestHostname) {
				continue
			}
			if count == maxSubscriptionOPMLOutlines {
				return nil, errSubscriptionOPMLLimit
			}
			outline := subscriptionOutline{
				Type: "rss", Text: sub.Title, Title: sub.Title, XMLURL: sub.FeedURL,
			}
			if private {
				outline.Text = sub.FeedURL
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

// Credentials can look like ordinary path segments. Require explicit approval
// of the entire URL and exclude URL components commonly used for credentials.
func (s *Server) exportableSubscriptionURL(raw, requestHostname string) bool {
	if _, approved := s.subscriptionURLs[raw]; !approved {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return false
	}
	hostname := strings.TrimSuffix(u.Hostname(), ".")
	if strings.EqualFold(hostname, strings.TrimSuffix(requestHostname, ".")) {
		return false
	}
	return s.baseURL == nil || !strings.EqualFold(hostname, strings.TrimSuffix(s.baseURL.Hostname(), "."))
}
