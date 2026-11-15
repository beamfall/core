package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/conductor"
)

// TestWatchPartyGraphQLHomeNetworkOnly checks the CORE-0531 edge: the graphQL
// route marks a caller that is not on the home network, so forming a Watch
// Party from outside is refused while the same call from the LAN succeeds.
//
// feature:watch-party
func TestWatchPartyGraphQLHomeNetworkOnly(t *testing.T) {
	srv := newTestServer(t)
	wrapped, ok := srv.graph.(testServerGraphHandler)
	if !ok {
		t.Fatalf("test server graph handler is %T, want testServerGraphHandler", srv.graph)
	}
	wrapped.SetConductor(conductor.NewRegistry())
	body := `{"query":"mutation C($content: String!){ createWatchParty(content:$content, relaxed:false, clockOffsetMS:0, renderLatencyMS:0){ partyID } }","variables":{"content":"media-alpha"}}`

	call := func(remoteAddr string) string {
		t.Helper()
		req := newJSONRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
		req.RemoteAddr = remoteAddr
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("graphql status from %s = %d (body %s)", remoteAddr, rec.Code, rec.Body.String())
		}
		var resp struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Errors) == 0 {
			return ""
		}
		return resp.Errors[0].Message
	}

	if msg := call("127.0.0.1:5000"); msg != "" {
		t.Fatalf("LAN createWatchParty error = %q, want success", msg)
	}
	if msg := call("203.0.113.7:5000"); !strings.Contains(msg, "home network only") {
		t.Fatalf("remote createWatchParty error = %q, want the home-network refusal", msg)
	}
}
