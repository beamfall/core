package server

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"

	"github.com/beamfall/core/internal/store"
)

const (
	subscriptionExportPath        = "/api/subscriptions/export.opml"
	subscriptionExportContentType = "application/xml; charset=utf-8"
)

type subscriptionExportStore interface {
	Libraries(context.Context, store.LibraryFilter) ([]store.Library, error)
	Subscriptions(context.Context, store.LibraryID) ([]store.Subscription, error)
}

type subscriptionOutline struct {
	Type   string `xml:"type,attr"`
	Text   string `xml:"text,attr"`
	Title  string `xml:"title,attr"`
	XMLURL string `xml:"xmlUrl,attr"`
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

	query, err := url.ParseQuery(r.URL.RawQuery)
	private := query["includePrivate"]
	includePrivate := err == nil && len(private) == 1 && private[0] == "true"

	libraries, err := s.subscriptionExport.Libraries(ctx, scope.LibraryFilter())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "could not export subscriptions")
		return
	}
	var doc struct {
		XMLName xml.Name `xml:"opml"`
		Version string   `xml:"version,attr"`
		Title   string   `xml:"head>title"`
		Body    struct {
			Outlines []subscriptionOutline `xml:"outline"`
		} `xml:"body"`
	}
	doc.Version = "2.0"
	doc.Title = "Subscriptions"
	for _, library := range libraries {
		subscriptions, err := s.subscriptionExport.Subscriptions(ctx, library.ID)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "could not export subscriptions")
			return
		}
		for _, sub := range subscriptions {
			if (!includePrivate && sub.AuthRef != "") || !exportableSubscriptionURL(sub.FeedURL) {
				continue
			}
			doc.Body.Outlines = append(doc.Body.Outlines, subscriptionOutline{
				Type: "rss", Text: sub.Title, Title: sub.Title, XMLURL: sub.FeedURL,
			})
		}
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "could not export subscriptions")
		return
	}
	w.Header().Set("Content-Type", subscriptionExportContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="subscriptions.opml"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(append([]byte(xml.Header), body...))
}

// A feed URL can carry credentials independently of AuthRef. Omit URLs with
// userinfo, fragments or recognized credential parameters to preserve feed IDs.
func exportableSubscriptionURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return false
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for key := range query {
		key = strings.ReplaceAll(strings.ReplaceAll(key, "-", ""), "_", "")
		switch strings.ToLower(key) {
		case "accesskey", "accesstoken", "apikey", "apitoken", "auth", "authkey", "authorization",
			"authtoken", "bearer", "bearertoken", "clientsecret", "credential", "credentials",
			"key", "passwd", "password", "secret", "sessiontoken", "sig", "signature", "token",
			"xamzcredential", "xamzsecuritytoken", "xamzsignature", "xgoogcredential", "xgoogsignature":
			return false
		}
	}
	return true
}
