package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

type baseURLKey struct{}

func parseBaseURL(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("must be an absolute http(s) URL without credentials, query, or fragment")
	}
	u.Path = strings.TrimSuffix(path.Clean("/"+strings.TrimPrefix(u.Path, "/")), "/")
	if u.Path == "." || u.Path == "/" {
		u.Path = ""
	}
	u.RawPath = ""
	return u, nil
}

func baseURLFromRequest(r *http.Request) *url.URL {
	u, _ := r.Context().Value(baseURLKey{}).(*url.URL)
	return u
}

func (s *Server) applyBaseURL(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, bool) {
	if s.baseURL == nil {
		return w, r, true
	}
	prefix := s.baseURL.Path
	if prefix != "" {
		if r.URL.Path == prefix {
			target := *s.baseURL
			target.Path = prefix + "/"
			http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
			return w, r, false
		}
		if !strings.HasPrefix(r.URL.Path, prefix+"/") {
			http.NotFound(w, r)
			return w, r, false
		}
		r2 := r.Clone(r.Context())
		u2 := *r.URL
		u2.Path = strings.TrimPrefix(r.URL.Path, prefix)
		if u2.RawPath != "" {
			u2.RawPath = strings.TrimPrefix(u2.RawPath, prefix)
		}
		r2.URL = &u2
		r = r2
	}
	r = r.WithContext(contextWithBaseURL(r, s.baseURL))
	return &baseURLResponseWriter{ResponseWriter: w, base: s.baseURL}, r, true
}

func contextWithBaseURL(r *http.Request, base *url.URL) context.Context {
	return context.WithValue(r.Context(), baseURLKey{}, base)
}

type baseURLResponseWriter struct {
	http.ResponseWriter
	base *url.URL
}

func (w *baseURLResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *baseURLResponseWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *baseURLResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *baseURLResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, w.base.Path+"/") {
		target = w.base.Path + target
	}
	return pusher.Push(target, options)
}

func (w *baseURLResponseWriter) WriteHeader(status int) {
	w.rewriteLocation()
	w.ResponseWriter.WriteHeader(status)
}

func (w *baseURLResponseWriter) Write(body []byte) (int, error) {
	w.rewriteLocation()
	return w.ResponseWriter.Write(body)
}

func (w *baseURLResponseWriter) rewriteLocation() {
	location := w.Header().Get("Location")
	if location == "" {
		return
	}
	u, err := url.Parse(location)
	if err != nil {
		return
	}
	// Never preserve a generated/inbound host. Redirect authority and prefix
	// always come from the static operator configuration.
	if u.IsAbs() || strings.HasPrefix(location, "/") {
		u.Scheme = w.base.Scheme
		u.Host = w.base.Host
		if !strings.HasPrefix(u.Path, w.base.Path+"/") && u.Path != w.base.Path {
			u.Path = w.base.Path + "/" + strings.TrimPrefix(u.Path, "/")
		}
		w.Header().Set("Location", u.String())
	}
}
