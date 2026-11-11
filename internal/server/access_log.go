package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/auth"
)

// principalStreamToken is the credential class of a request whose path
// carries a URL-borne Stream Token (see tokenRoutePrefixes).
const principalStreamToken = "stream_token"

// statusClasses are the label values of beamfall_http_requests_total, indexed
// by status/100 - 1. All five are always reported so a rate query never sees a
// series appear from nothing.
var statusClasses = [5]string{"1xx", "2xx", "3xx", "4xx", "5xx"}

// requestMetrics holds the in-process request counters behind /metrics. Its
// labels are bounded by construction: a status class, nothing per path,
// route, caller or identifier.
type requestMetrics struct {
	byClass       [5]atomic.Uint64
	count         atomic.Uint64
	durationNanos atomic.Uint64
}

func (m *requestMetrics) observe(status int, elapsed time.Duration) {
	if status >= 100 && status < 600 {
		m.byClass[status/100-1].Add(1)
	}
	m.count.Add(1)
	if elapsed > 0 {
		m.durationNanos.Add(uint64(elapsed))
	}
}

func (m *requestMetrics) writePrometheus(body *strings.Builder) {
	for i, class := range statusClasses {
		_, _ = fmt.Fprintf(body, "beamfall_http_requests_total{status_class=%q} %d\n", class, m.byClass[i].Load())
	}
	seconds := float64(m.durationNanos.Load()) / float64(time.Second)
	_, _ = fmt.Fprintf(body, "beamfall_http_request_duration_seconds_sum %s\n", strconv.FormatFloat(seconds, 'f', 6, 64))
	_, _ = fmt.Fprintf(body, "beamfall_http_request_duration_seconds_count %d\n", m.count.Load())
}

// recordAccess emits the per-request access line and counts the request. It
// logs the matched route pattern, the path with any Stream Token redacted and
// no query string, and only the class of the presented credential — never a
// credential value.
func (s *Server) recordAccess(r *http.Request, requestID string, status int, elapsed time.Duration) {
	s.requestMetrics.observe(status, elapsed)
	route := r.Pattern
	if route == "" {
		route = "unmatched"
	}
	slog.InfoContext(r.Context(), "http request",
		"requestID", requestID,
		"method", r.Method,
		"route", route,
		"path", redactTokenPath(r.URL.Path),
		"status", status,
		"latency_ms", elapsed.Milliseconds(),
		"principal", principalClass(r),
	)
}

// principalClass names the credential the request presents. A token route is
// authorized by its URL-borne Stream Token, whatever cookies ride along.
func principalClass(r *http.Request) string {
	for _, prefix := range tokenRoutePrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return principalStreamToken
		}
	}
	return auth.CredentialClass(r)
}
