package server

import (
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) sourceOffer(w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(strings.TrimSpace(s.sourceOfferURL))
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
}
