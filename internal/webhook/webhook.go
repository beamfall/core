// Package webhook is the first-party Webhook Emitter Integration Plugin.
// It is NOT Core: operators who want push delivery (Home
// Assistant automations, Discord notifications, ntfy alerts) configure one or
// more target endpoints; an operator who configures none pays nothing (the
// plugin stays dormant — see app wiring).
//
// The emitter is an in-process consumer of the Core SSE event spine: it polls
// the same append-only event outbox the SSE surface reads by a
// monotonic Seq cursor, and pushes each matching event to each endpoint as an
// HMAC-signed POST. It adds no new events and no new schema — invariant #3: it
// persists its dead-letter as plugin-private JSON under the data directory,
// mirroring the qBittorrent ingest ledger, never a SQLite migration. The HMAC
// secret per endpoint lives in a 0600 secret file resolved by the composition
// root (invariant #5); this package never logs a secret or a signed payload.
//
// Like internal/qbittorrent, this Integration Plugin lives top-level
// rather than under internal/plugin/firstparty/: the composition root
// (internal/app) wires it — it consumes the event outbox and reads the secret
// store — so it cannot sit behind the plugin firewall (firstparty/** is in the
// import graph of cmd/beamfall only, ARCHITECTURE §4). It is wholly generic
// (no domain vocabulary), so it needs no vocab exemption.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/beamfall/core/internal/store"
)

// SignatureHeader carries the lowercase-hex HMAC-SHA256 of the exact POSTed
// body, keyed by the endpoint's secret. A receiver recomputes it over the raw
// body to authenticate the delivery (the GitHub/Stripe webhook idiom).
const SignatureHeader = "X-Beamfall-Signature"

// EventIDHeader carries the outbox Seq of the delivered event so a receiver can
// dedupe redeliveries (a retry re-POSTs the same Seq).
const EventIDHeader = "X-Beamfall-Event-Id"

// EventStore is the slice of the store the emitter consumes: the same
// append-only outbox read the SSE surface uses (interface at the consumer,
// ADR-0016). The emitter reads it unfiltered — the operator who configures
// webhooks is admin-scoped, the same posture as an admin-scoped SSE key,
// so it sees every event the stream can carry.
type EventStore interface {
	ReadEventOutbox(ctx context.Context, afterSeq int64, limit int) ([]store.OutboxEvent, error)
}

// Endpoint is one configured delivery target. The Secret is injected by the
// composition root from a 0600 secret file (invariant #5) — it never rides the
// JSON config on disk and is never logged.
type Endpoint struct {
	// ID is a stable operator-chosen slug, used in dead-letter rows and logs
	// (never the URL, which may embed a token).
	ID string
	// URL is the POST target (https in production; http allowed for LAN
	// receivers and tests).
	URL string
	// Secret keys the HMAC-SHA256 body signature. Required.
	Secret []byte
	// EventKinds is the per-endpoint filter: the subset of the SSE event
	// vocabulary (OutboxEvent.Kind, e.g. "media.created") this endpoint
	// receives. Empty means receive every kind.
	EventKinds []string
}

// matches reports whether this endpoint's filter accepts an event kind. An
// empty filter accepts everything.
func (e Endpoint) matches(kind string) bool {
	if len(e.EventKinds) == 0 {
		return true
	}
	for _, k := range e.EventKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Config is the emitter's runtime configuration.
type Config struct {
	// Endpoints are the configured targets. Zero endpoints means the plugin is
	// dormant: New returns (nil, nil) and the app never starts the emitter.
	Endpoints []Endpoint
	// MaxAttempts caps delivery tries per (event, endpoint) before the delivery
	// is dead-lettered. Must be >= 1; zero defaults to defaultMaxAttempts.
	MaxAttempts int
	// BaseBackoff is the first retry delay; each subsequent retry doubles it
	// (exponential backoff). Zero defaults to defaultBaseBackoff.
	BaseBackoff time.Duration
	// PollInterval is how often the emitter checks the outbox for new events.
	// Zero defaults to defaultPollInterval.
	PollInterval time.Duration
}

const (
	defaultMaxAttempts  = 5
	defaultBaseBackoff  = time.Second
	defaultPollInterval = 500 * time.Millisecond
	defaultHTTPTimeout  = 15 * time.Second
	// outboxBatchLimit bounds one poll read so a cold start far behind the tail
	// catches up in bounded steps (mirrors the SSE surface's eventBatchLimit).
	outboxBatchLimit = 256
)

var defaultHTTPClient = &http.Client{Timeout: defaultHTTPTimeout}

// Doer is the HTTP client seam (interface at the consumer). *http.Client
// satisfies it; tests inject a stub.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// DeadLetterSink persists deliveries that exhausted their retries so the
// operator can inspect them. The qBittorrent Ledger is the model: plugin-
// private JSON on disk, never a core schema row (invariant #3).
type DeadLetterSink interface {
	Record(entry DeadLetter) error
}

type CursorStore interface {
	Cursor() int64
	SetCursor(cursor int64) error
}

// DeadLetter is one exhausted delivery: which endpoint, which event, why, and
// when. The payload is retained so the operator can replay it by hand. No
// secret and no signature is stored (the signature is derivable from the
// secret, which the dead-letter must not hold).
type DeadLetter struct {
	EndpointID string          `json:"endpoint_id"`
	EventSeq   int64           `json:"event_seq"`
	EventKind  string          `json:"event_kind"`
	Attempts   int             `json:"attempts"`
	LastError  string          `json:"last_error"`
	Payload    json.RawMessage `json:"payload"`
	At         time.Time       `json:"at"`
}

// Emitter consumes the event outbox and pushes matching events to each
// configured endpoint with HMAC signing, exponential-backoff retry, and a
// dead-letter for exhausted deliveries.
type Emitter struct {
	cfg        Config
	events     EventStore
	client     Doer
	deadLetter DeadLetterSink
	cursor     CursorStore
	logger     *slog.Logger
	now        func() time.Time
	sleep      func(ctx context.Context, d time.Duration) error
}

// New builds the emitter, or returns (nil, nil) when no endpoints are
// configured — the dormant case the app treats as "plugin off" (ADR-0003:
// opt-in, not always-on). It validates each endpoint up front so a
// misconfiguration fails at boot, not silently at first event.
func New(cfg Config, events EventStore, client Doer, deadLetter DeadLetterSink, logger *slog.Logger) (*Emitter, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, nil // dormant: opt-in plugin with nothing configured
	}
	if events == nil {
		return nil, ErrConfigDiagnostic
	}
	if client == nil {
		client = defaultHTTPClient
	}
	if logger == nil {
		logger = slog.Default()
	}
	seen := map[string]bool{}
	for _, ep := range cfg.Endpoints {
		if strings.TrimSpace(ep.ID) == "" {
			return nil, ErrConfigDiagnostic
		}
		if seen[ep.ID] {
			return nil, ErrConfigDiagnostic
		}
		seen[ep.ID] = true
		if strings.TrimSpace(ep.URL) == "" {
			return nil, ErrConfigDiagnostic
		}
		if len(ep.Secret) == 0 {
			return nil, ErrConfigDiagnostic
		}
	}
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = defaultBaseBackoff
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	return &Emitter{
		cfg:        cfg,
		events:     events,
		client:     client,
		deadLetter: deadLetter,
		cursor:     cursorStore(deadLetter),
		logger:     logger,
		now:        time.Now,
		sleep:      sleepCtx,
	}, nil
}

// Run drives the emitter until ctx is canceled: poll the outbox from the cursor,
// deliver each new event to every matching endpoint, advance the cursor. It is
// started as a long-lived goroutine by the app (like the HLS session manager),
// not the job substrate — webhook retry is per (event, endpoint) and must not
// be conflated with job-level retry.
func (e *Emitter) Run(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.PollInterval)
	defer ticker.Stop()
	cursor := e.currentCursor()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next, err := e.drain(ctx, cursor)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				e.logger.Error("webhook emitter outbox read failed", "error", err)
				continue
			}
			cursor = next
		}
	}
}

// drain delivers every pending event after cursor and returns the new cursor.
// The cursor advances past an event once it has been offered to every endpoint
// (delivered, dropped by filter, or dead-lettered) — at-least-once delivery
// with the dead-letter as the terminal sink, never an unbounded re-attempt of
// the whole backlog.
func (e *Emitter) drain(ctx context.Context, cursor int64) (int64, error) {
	for {
		events, err := e.events.ReadEventOutbox(ctx, cursor, outboxBatchLimit)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return cursor, ctxErr
			}
			return cursor, ErrOutboxDiagnostic
		}
		if len(events) == 0 {
			return cursor, nil
		}
		for _, event := range events {
			if err := ctx.Err(); err != nil {
				return cursor, err
			}
			if err := e.deliver(ctx, event); err != nil {
				return cursor, err
			}
			cursor = event.Seq
			if e.cursor != nil {
				if err := e.cursor.SetCursor(cursor); err != nil {
					return cursor, ErrCursorDiagnostic
				}
			}
		}
		if len(events) < outboxBatchLimit {
			return cursor, nil
		}
	}
}

func (e *Emitter) currentCursor() int64 {
	if e.cursor == nil {
		return 0
	}
	return e.cursor.Cursor()
}

func cursorStore(deadLetter DeadLetterSink) CursorStore {
	cursor, _ := deadLetter.(CursorStore)
	return cursor
}

// deliver offers one event to every endpoint whose filter accepts it.
func (e *Emitter) deliver(ctx context.Context, event store.OutboxEvent) error {
	body := wireBody(event)
	for _, ep := range e.cfg.Endpoints {
		if !ep.matches(event.Kind) {
			continue
		}
		if err := e.deliverTo(ctx, ep, event, body); err != nil {
			return err
		}
	}
	return nil
}

// deliverTo POSTs one event to one endpoint with HMAC signing and exponential-
// backoff retry. On exhaustion it routes the delivery to the dead-letter.
func (e *Emitter) deliverTo(ctx context.Context, ep Endpoint, event store.OutboxEvent, body []byte) error {
	signature := sign(ep.Secret, body)
	var lastErr error
	backoff := e.cfg.BaseBackoff
	for attempt := 1; attempt <= e.cfg.MaxAttempts; attempt++ {
		if attempt > 1 {
			if err := e.sleep(ctx, backoff); err != nil {
				return err // interrupted mid-backoff: leave the event for the next drain
			}
			backoff *= 2
		}
		lastErr = e.post(ctx, ep, event, body, signature)
		if lastErr == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	// Exhausted: dead-letter so the operator can inspect/replay. The error
	// string is endpoint/status only — never the secret or the signature.
	e.logger.Warn("webhook delivery exhausted, dead-lettering",
		"endpoint", ep.ID, "event_seq", event.Seq, "event_kind", event.Kind,
		"attempts", e.cfg.MaxAttempts, "error", lastErr)
	if e.deadLetter == nil {
		return nil
	}
	if err := e.deadLetter.Record(DeadLetter{
		EndpointID: ep.ID,
		EventSeq:   event.Seq,
		EventKind:  event.Kind,
		Attempts:   e.cfg.MaxAttempts,
		LastError:  lastErr.Error(),
		Payload:    json.RawMessage(body),
		At:         e.now().UTC(),
	}); err != nil {
		redactedCause := strings.ReplaceAll(err.Error(), string(ep.Secret), "[REDACTED]")
		e.logger.Error("webhook dead-letter write failed", "endpoint", ep.ID, "event_seq", event.Seq, "error", redactedCause)
		return ErrDeadLetterDiagnostic
	}
	return nil
}

// post performs one delivery attempt. A non-2xx response or transport error is
// a failure worth retrying.
func (e *Emitter) post(ctx context.Context, ep Endpoint, event store.OutboxEvent, body, signature []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, strings.NewReader(string(body)))
	if err != nil {
		return ErrRequestDiagnostic
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, "sha256="+hex.EncodeToString(signature))
	req.Header.Set(EventIDHeader, fmt.Sprintf("%d", event.Seq))
	resp, err := e.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrTransportDiagnostic
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrStatusDiagnostic
	}
	return nil
}

// wireBody serializes one outbox event in the same JSON envelope the SSE
// surface emits (server.writeSSEEvent), so a webhook receiver and an SSE
// consumer see byte-identical event bodies.
func wireBody(event store.OutboxEvent) []byte {
	payload := event.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	body, err := json.Marshal(struct {
		Kind          string          `json:"kind"`
		AggregateType string          `json:"aggregateType"`
		AggregateID   string          `json:"aggregateID"`
		OccurredAt    time.Time       `json:"occurredAt"`
		Payload       json.RawMessage `json:"payload"`
	}{
		Kind:          event.Kind,
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		OccurredAt:    event.OccurredAt,
		Payload:       json.RawMessage(payload),
	})
	if err != nil {
		// Marshaling a fixed-shape struct over store-written fields does not
		// fail on real input; fall back to an empty object so a malformed event
		// never panics the loop.
		return []byte("{}")
	}
	return body
}

// sign returns the HMAC-SHA256 of body keyed by secret — the same idiom as the
// Stream Token signer (internal/streamtoken).
func sign(secret, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return mac.Sum(nil)
}

// sleepCtx waits d or returns ctx.Err() if the context is canceled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
