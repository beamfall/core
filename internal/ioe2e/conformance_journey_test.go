//go:build io_e2e

package ioe2e

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/apptest/synthplugin"
	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/plugin"
)

type conformanceJourneyClient struct {
	t       *testing.T
	baseURL string
	client  *http.Client
	session string
	device  string
	secret  string
}

type conformanceGraphResponse struct {
	Data struct {
		CurrentSession struct {
			Revealed bool `json:"revealed"`
		} `json:"currentSession"`
		SetRevealPin *struct {
			Revealed bool `json:"revealed"`
		} `json:"setRevealPin"`
		Reveal *struct {
			Revealed bool `json:"revealed"`
		} `json:"reveal"`
		Relock *struct {
			Revealed bool `json:"revealed"`
		} `json:"relock"`
		SourceItems []struct {
			ID string `json:"id"`
		} `json:"sourceItems"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// TestConformanceJourneyX1ThroughX10OverHTTP is the cross-surface reveal
// journey on the public test seam. It boots the same real listener selected by
// `serve --seed=conformance`, then drives every state transition through HTTP;
// no handler, store, or resolver is called directly.
func TestConformanceJourneyX1ThroughX10OverHTTP(t *testing.T) {
	baseURL := bootConformanceJourneyServer(t)
	viewer := newConformanceJourneyClient(t, baseURL, "session-default", "device-tv", "dev-tv-secret")
	manager := newConformanceJourneyClient(t, baseURL, "session-manager", "device-mac", "dev-manager-secret")

	// X1: the conformance server is reachable through its real listener.
	if status, body := viewer.get("/health"); status != http.StatusOK {
		t.Fatalf("X1 health status = %d body=%s, want 200", status, body)
	}

	// X2: a newly attached viewer starts locked.
	locked := viewer.graphQL(`query { currentSession { revealed } }`)
	requireNoConformanceGraphErrors(t, "X2", locked)
	if locked.Data.CurrentSession.Revealed {
		t.Fatal("X2 currentSession.revealed = true, want false")
	}

	// X3: restricted items do not cross the wire while locked.
	lockedItems := viewer.sourceItems()
	requireConformanceItem(t, "X3", lockedItems, "media-alpha", true)
	requireConformanceItem(t, "X3", lockedItems, "media-archive", false)

	// X4: the manager surface rotates the Profile's reveal PIN.
	set := manager.graphQLVariables(
		`mutation SetPin($pin: String!) { setRevealPin(pin: $pin) { revealed } }`,
		map[string]string{"pin": "7788"},
	)
	requireNoConformanceGraphErrors(t, "X4", set)
	if set.Data.SetRevealPin == nil {
		t.Fatal("X4 setRevealPin returned null")
	}

	// X5: the former PIN no longer unlocks the viewer.
	oldPIN := viewer.graphQLVariables(
		`mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }`,
		map[string]string{"pin": "1234"},
	)
	if len(oldPIN.Errors) != 1 || oldPIN.Errors[0].Message != "reveal PIN mismatch" {
		t.Fatalf("X5 old PIN errors = %#v, want reveal PIN mismatch", oldPIN.Errors)
	}

	// X6: the rotated PIN unlocks the separate viewer session.
	revealed := viewer.graphQLVariables(
		`mutation Reveal($pin: String!) { reveal(pin: $pin) { revealed } }`,
		map[string]string{"pin": "7788"},
	)
	requireNoConformanceGraphErrors(t, "X6", revealed)
	if revealed.Data.Reveal == nil || !revealed.Data.Reveal.Revealed {
		t.Fatalf("X6 reveal = %#v, want revealed=true", revealed.Data.Reveal)
	}

	// X7: the session read model observes the mutation over a fresh request.
	session := viewer.graphQL(`query { currentSession { revealed } }`)
	requireNoConformanceGraphErrors(t, "X7", session)
	if !session.Data.CurrentSession.Revealed {
		t.Fatal("X7 currentSession.revealed = false, want true")
	}

	// X8: reveal-filtered catalog data now includes the restricted item.
	revealedItems := viewer.sourceItems()
	requireConformanceItem(t, "X8", revealedItems, "media-alpha", true)
	requireConformanceItem(t, "X8", revealedItems, "media-archive", true)

	// X9: the revealed viewer can resolve playback over the REST wire seam.
	playbackBody := `{"mediaID":"media-alpha","capabilityProfile":{"client":"conformance-journey","containers":["mp4"],"directCodecs":["h264"]}}`
	status, playback := viewer.post("/api/playback-info", playbackBody)
	if status != http.StatusOK ||
		!strings.Contains(playback, `"decision":"direct-play"`) ||
		!strings.Contains(playback, `"token":"`) {
		t.Fatalf("X9 playback-info status=%d body=%s, want direct-play with token", status, playback)
	}

	// X10: relock propagates to later requests and hides restricted data again.
	relocked := viewer.graphQL(`mutation { relock { revealed } }`)
	requireNoConformanceGraphErrors(t, "X10", relocked)
	if relocked.Data.Relock == nil || relocked.Data.Relock.Revealed {
		t.Fatalf("X10 relock = %#v, want revealed=false", relocked.Data.Relock)
	}
	finalItems := viewer.sourceItems()
	requireConformanceItem(t, "X10", finalItems, "media-alpha", true)
	requireConformanceItem(t, "X10", finalItems, "media-archive", false)
}

func bootConformanceJourneyServer(t *testing.T) string {
	t.Helper()
	cfg := config.Default()
	cfg.Addr = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.ServerName = "Conformance Journey"
	cfg.Discovery.Enabled = false
	cfg.SeedProfile = "conformance"
	cfg.AllowConformanceSeed = true

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	beamfall, err := app.New(context.Background(), cfg, logger, []plugin.Bundle{synthplugin.Bundle()})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- beamfall.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("server Run returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server Run did not stop within 10s")
		}
	})

	addrCtx, addrCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer addrCancel()
	addr := beamfall.Addr(addrCtx)
	if addr == "" {
		t.Fatal("conformance server did not bind within 5s")
	}
	return "http://" + addr
}

func newConformanceJourneyClient(t *testing.T, baseURL, session, device, secret string) *conformanceJourneyClient {
	return &conformanceJourneyClient{
		t:       t,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 5 * time.Second},
		session: session,
		device:  device,
		secret:  secret,
	}
}

func (c *conformanceJourneyClient) get(path string) (int, string) {
	return c.do(http.MethodGet, path, "")
}

func (c *conformanceJourneyClient) post(path, body string) (int, string) {
	return c.do(http.MethodPost, path, body)
}

func (c *conformanceJourneyClient) do(method, path, body string) (int, string) {
	c.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("X-Beamfall-Session", c.session)
	req.Header.Set("X-Beamfall-Device", c.device)
	req.Header.Set("X-Beamfall-Device-Secret", c.secret)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}

func (c *conformanceJourneyClient) graphQL(document string) conformanceGraphResponse {
	return c.graphQLVariables(document, nil)
}

func (c *conformanceJourneyClient) graphQLVariables(document string, variables any) conformanceGraphResponse {
	c.t.Helper()
	envelope, err := json.Marshal(struct {
		Query     string `json:"query"`
		Variables any    `json:"variables,omitempty"`
	}{Query: document, Variables: variables})
	if err != nil {
		c.t.Fatal(err)
	}
	status, body := c.post("/api/graphql", string(envelope))
	if status != http.StatusOK {
		c.t.Fatalf("GraphQL status = %d body=%s, want 200", status, body)
	}
	var response conformanceGraphResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		c.t.Fatalf("decode GraphQL response: %v body=%s", err, body)
	}
	return response
}

func (c *conformanceJourneyClient) sourceItems() []struct {
	ID string `json:"id"`
} {
	c.t.Helper()
	response := c.graphQL(`query { sourceItems(source: { kind: "recently-added" }) { id } }`)
	requireNoConformanceGraphErrors(c.t, "sourceItems", response)
	return response.Data.SourceItems
}

func requireNoConformanceGraphErrors(t *testing.T, step string, response conformanceGraphResponse) {
	t.Helper()
	if len(response.Errors) != 0 {
		t.Fatalf("%s GraphQL errors = %#v", step, response.Errors)
	}
}

func requireConformanceItem(t *testing.T, step string, items []struct {
	ID string `json:"id"`
}, id string, want bool) {
	t.Helper()
	got := false
	for _, item := range items {
		if item.ID == id {
			got = true
			break
		}
	}
	if got != want {
		t.Fatalf("%s item %q presence = %v, want %v; items=%#v", step, id, got, want, items)
	}
}
