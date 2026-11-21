package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
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

var errSubscriptionOPMLLimit = errors.New("subscription OPML byte limit reached")

type subscriptionExportStore interface {
	Libraries(context.Context, store.LibraryFilter) ([]store.Library, error)
	Subscriptions(context.Context, store.LibraryID) ([]store.Subscription, error)
}

type subscriptionOutline struct {
	XMLName xml.Name `xml:"outline"`
	Type    string   `xml:"type,attr"`
	Text    string   `xml:"text,attr"`
	Title   string   `xml:"title,attr"`
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

	body, err := s.subscriptionOPML(ctx, scope.LibraryFilter())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "could not export subscriptions")
		return
	}
	w.Header().Set("Content-Type", subscriptionExportContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="subscriptions.opml"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

func (s *Server) subscriptionOPML(ctx context.Context, filter store.LibraryFilter) ([]byte, error) {
	libraries, err := s.subscriptionExport.Libraries(ctx, filter)
	if err != nil {
		return nil, err
	}
	if s.subscriptionSealed != nil {
		for _, lib := range libraries {
			if s.subscriptionSealed(lib.ID) {
				filter.SealedIDs = append(filter.SealedIDs, lib.ID)
			}
		}
		if len(filter.SealedIDs) != 0 {
			libraries, err = s.subscriptionExport.Libraries(ctx, filter)
			if err != nil {
				return nil, err
			}
		}
	}

	var body bytes.Buffer
	body.WriteString(subscriptionOPMLStart)
	encoder := xml.NewEncoder(subscriptionOPMLWriter{body: &body})
	count := 0
export:
	for _, library := range libraries {
		subscriptions, err := s.subscriptionExport.Subscriptions(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		for _, sub := range subscriptions {
			if sub.AuthRef != "" || !exportableSubscriptionURL(sub.FeedURL) {
				continue
			}
			start := body.Len()
			err := encoder.Encode(subscriptionOutline{
				Type: "rss", Text: sub.Title, Title: sub.Title, XMLURL: sub.FeedURL,
			})
			if errors.Is(err, errSubscriptionOPMLLimit) {
				// A partially encoded outline cannot be included in the document.
				body.Truncate(start)
				break export
			}
			if err != nil {
				return nil, err
			}
			count++
			if count == maxSubscriptionOPMLOutlines || body.Len()+len(subscriptionOPMLEnd) == maxSubscriptionOPMLBytes {
				break export
			}
		}
	}
	body.WriteString(subscriptionOPMLEnd)
	return body.Bytes(), nil
}

// Feed URL query parameters can carry credentials under arbitrary names.
func exportableSubscriptionURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && (u.Scheme == "http" || u.Scheme == "https") &&
		u.User == nil && u.RawQuery == "" && !u.ForceQuery && !strings.Contains(raw, "#")
}
