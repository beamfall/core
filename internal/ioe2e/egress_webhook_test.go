//go:build io

package ioe2e

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/webhook"
)

// oneShotOutbox is a webhook.EventStore that yields exactly one event the first
// time it is read past seq 0, then reports the tail empty. It lets the emitter's
// poll loop deliver a single event without a real store.
type oneShotOutbox struct {
	event store.OutboxEvent
}

func (o *oneShotOutbox) ReadEventOutbox(_ context.Context, afterSeq int64, _ int) ([]store.OutboxEvent, error) {
	if afterSeq < o.event.Seq {
		return []store.OutboxEvent{o.event}, nil
	}
	return nil, nil
}

// TestEgress_webhook_post drives the REAL webhook.Emitter against a loopback
// httptest double and asserts the double received the signed POST.
//
// io:egress-webhook-post
func TestEgress_webhook_post(t *testing.T) {
	type received struct {
		method    string
		path      string
		signature string
		eventID   string
		body      []byte
	}
	got := make(chan received, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case got <- received{
			method:    r.Method,
			path:      r.URL.Path,
			signature: r.Header.Get(webhook.SignatureHeader),
			eventID:   r.Header.Get(webhook.EventIDHeader),
			body:      body,
		}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	events := &oneShotOutbox{event: store.OutboxEvent{
		Seq:           7,
		OccurredAt:    time.Unix(1700000000, 0).UTC(),
		Kind:          "media.created",
		AggregateType: store.OutboxAggregateMedia,
		AggregateID:   "media-123",
		Payload:       []byte(`{"id":"media-123"}`),
	}}
	emitter, err := webhook.New(webhook.Config{
		Endpoints: []webhook.Endpoint{{
			ID:     "test-endpoint",
			URL:    srv.URL,
			Secret: []byte("shhh-secret"),
		}},
		PollInterval: 10 * time.Millisecond,
	}, events, srv.Client(), nil, nil)
	if err != nil {
		t.Fatalf("webhook.New: %v", err)
	}
	if emitter == nil {
		t.Fatal("webhook.New returned a nil emitter for a configured endpoint")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { emitter.Run(ctx); close(done) }()

	var r received
	select {
	case r = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("webhook double never received the POST")
	}
	cancel()
	<-done

	if r.method != http.MethodPost {
		t.Errorf("method = %q, want POST", r.method)
	}
	if r.eventID != "7" {
		t.Errorf("X-Beamfall-Event-Id = %q, want 7", r.eventID)
	}
	// The signature is sha256=<lowercase-hex HMAC of the exact body>.
	if len(r.signature) < len("sha256=")+64 {
		t.Errorf("signature header malformed: %q", r.signature)
	}
	if _, err := hex.DecodeString(r.signature[len("sha256="):]); err != nil {
		t.Errorf("signature is not lowercase hex: %q", r.signature)
	}
	var env struct {
		Kind        string          `json:"kind"`
		AggregateID string          `json:"aggregateID"`
		Payload     json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(r.body, &env); err != nil {
		t.Fatalf("POST body is not the SSE envelope JSON: %v (body=%s)", err, r.body)
	}
	if env.Kind != "media.created" || env.AggregateID != "media-123" {
		t.Errorf("envelope = %+v, want kind=media.created aggregateID=media-123", env)
	}
}
