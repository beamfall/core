package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type testFaults struct {
	mu    sync.RWMutex
	rules map[string]testFaultRule
}

type testFaultRule struct {
	Status  int            `json:"status"`
	Body    map[string]any `json:"body"`
	DelayMS int            `json:"delayMS"`
	// EmptyJSON returns a valid empty JSON object (`{}`), not a zero-byte body.
	EmptyJSON bool `json:"emptyJSON"`
	Close     bool `json:"close"`
}

type testFaultRequest struct {
	Method string        `json:"method"`
	Path   string        `json:"path"`
	Clear  bool          `json:"clear"`
	Rule   testFaultRule `json:"rule"`
	// Status and Body are shorthands; explicit Rule fields win.
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
}

func newTestFaults() *testFaults {
	return &testFaults{rules: map[string]testFaultRule{}}
}

func testFaultKey(method, path string) string {
	// Path matching is exact against r.URL.Path after ServeMux normalization.
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(path)
}

func (f *testFaults) set(req testFaultRequest) error {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	path := strings.TrimSpace(req.Path)
	if method == "" || path == "" || !strings.HasPrefix(path, "/") {
		return errBadTestFault
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := testFaultKey(method, path)
	if req.Clear {
		delete(f.rules, key)
		return nil
	}
	rule := req.Rule
	if rule.Status == 0 {
		rule.Status = req.Status
	}
	if rule.Body == nil {
		rule.Body = req.Body
	}
	if rule.Status == 0 {
		rule.Status = http.StatusServiceUnavailable
	}
	f.rules[key] = rule
	return nil
}

func (f *testFaults) get(method, path string) (testFaultRule, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	rule, ok := f.rules[testFaultKey(method, path)]
	return rule, ok
}

var errBadTestFault = clientMessage("method and absolute path are required")

type clientMessage string

func (e clientMessage) Error() string { return string(e) }

func (s *Server) SetTestFaults(enabled bool) {
	// Arming is one-way for the process; clear individual rules through the endpoint.
	if !enabled || !s.testFaults.CompareAndSwap(nil, newTestFaults()) {
		return
	}
	s.handle(http.MethodPost, "/api/test/faults", "", s.testFaultControl)
}

func (s *Server) testFaultControl(w http.ResponseWriter, r *http.Request) {
	if !requestIsLoopback(r) {
		writeError(w, r, http.StatusForbidden, "test faults are loopback-only")
		return
	}
	defer r.Body.Close()
	var req testFaultRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBody)).Decode(&req); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	if err := s.testFaults.Load().set(req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) applyTestFault(w http.ResponseWriter, r *http.Request) bool {
	faults := s.testFaults.Load()
	if faults == nil || r.URL.Path == "/api/test/faults" {
		return false
	}
	rule, ok := faults.get(r.Method, r.URL.Path)
	if !ok {
		return false
	}
	if rule.DelayMS > 0 {
		select {
		case <-time.After(time.Duration(rule.DelayMS) * time.Millisecond):
		case <-r.Context().Done():
			return true
		}
	}
	if rule.Close {
		if h, ok := w.(http.Hijacker); ok {
			conn, _, err := h.Hijack()
			if err == nil {
				_ = conn.Close()
				return true
			}
		}
		writeError(w, r, http.StatusServiceUnavailable, "test fault: connection closed")
		return true
	}
	if rule.EmptyJSON {
		writeJSON(w, rule.Status, map[string]any{})
		return true
	}
	body := rule.Body
	if body == nil {
		body = map[string]any{"error": "test fault"}
	}
	writeJSON(w, rule.Status, body)
	return true
}
