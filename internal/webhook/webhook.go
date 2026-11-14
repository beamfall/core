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
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
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
	// receivers and tests). New rejects a URL that is not an absolute http(s)
	// URL with a host, carries user info, or names a refused IP literal.
	URL string
	// AllowPrivateNetwork is the operator's approval for a LAN receiver named
	// by hostname: the default delivery client may then connect to loopback,
	// private, shared and site-local addresses. An endpoint whose URL host is
	// such an IP literal is approved without it. Link-local, multicast,
	// unspecified, other never-routable special-purpose addresses and IPv6
	// forms embedding an IPv4 address stay refused either way.
	AllowPrivateNetwork bool
	// Secret is the endpoint's credential, by format: the HMAC-SHA256 body
	// signing key for generic (required), the bearer token for ntfy
	// (optional), the application token for Gotify (required) and the SMTP
	// password for email (required when Username is set). Discord and Slack
	// carry their credential in the URL and take none.
	Secret []byte
	// EventKinds is the per-endpoint filter. A generic endpoint may list SSE
	// spine kinds (OutboxEvent.Kind, e.g. "media.created") and derived kinds
	// (DerivedKinds); every other format receives derived kinds only. Empty
	// means every kind the format can receive.
	EventKinds []string
	// Name is the operator's display label (console only).
	Name string
	// Format selects the request shape; empty is FormatGeneric.
	Format Format
	// Disabled pauses deliveries without deleting the endpoint. A test-send
	// still works, so an owner can check an endpoint before enabling it.
	Disabled bool
	// From, To and Username are the email format's envelope: the sender
	// address, the recipients, and the optional SMTP AUTH user.
	From     string
	To       []string
	Username string
}

// format returns the endpoint's format, defaulting to generic.
func (e Endpoint) format() Format {
	if e.Format == "" {
		return FormatGeneric
	}
	return e.Format
}

// matches reports whether this endpoint's filter accepts an event kind. Only
// a generic endpoint receives raw spine kinds; derived kinds reach every
// format. An empty filter accepts every kind the format can receive.
func (e Endpoint) matches(kind string) bool {
	if e.format() != FormatGeneric && !isDerivedKind(kind) {
		return false
	}
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

// DeliveryRecorder keeps the bounded delivery log the console reads. The
// FileDeadLetter state file implements it.
type DeliveryRecorder interface {
	RecordDelivery(entry Delivery)
}

// Emitter consumes the event outbox and pushes matching events to each
// configured endpoint with HMAC signing, exponential-backoff retry, and a
// dead-letter for exhausted deliveries. With a Catalog it also derives the
// human-meaningful events in DerivedKinds and renders them per endpoint
// format. Its endpoints can be replaced while it runs (SetEndpoints).
type Emitter struct {
	cfg        Config
	events     EventStore
	deadLetter DeadLetterSink
	cursor     CursorStore
	logger     *slog.Logger
	now        func() time.Time
	sleep      func(ctx context.Context, d time.Duration) error

	// mu guards cfg.Endpoints and clients, which SetEndpoints swaps.
	mu sync.RWMutex
	// clients holds one delivery client per endpoint, by Endpoint.ID.
	clients map[string]endpointClient
	// injected is the caller's client, used for every endpoint when set.
	injected Doer

	derive     *deriver
	deliveries DeliveryRecorder
	artwork    ArtworkSource
	mailer     Mailer
}

// New builds the emitter, or returns (nil, nil) when no endpoints are
// configured — the dormant case the app treats as "plugin off" (ADR-0003:
// opt-in, not always-on). It validates each endpoint up front so a
// misconfiguration fails at boot, not silently at first event.
//
// A nil client installs one bounded default client per endpoint (see
// newDeliveryClient): it never follows redirects and refuses dials outside the
// endpoint's approved address classes. An injected client is used as is for
// every endpoint and owns its own redirect and destination policy.
func New(cfg Config, events EventStore, client Doer, deadLetter DeadLetterSink, logger *slog.Logger) (*Emitter, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, nil // dormant: opt-in plugin with nothing configured
	}
	return buildEmitter(cfg, events, client, deadLetter, logger)
}

// buildEmitter builds an emitter that may start with zero endpoints: the
// console-managed emitter runs from boot and idles until an owner adds one.
func buildEmitter(cfg Config, events EventStore, client Doer, deadLetter DeadLetterSink, logger *slog.Logger) (*Emitter, error) {
	if events == nil {
		return nil, ErrConfigDiagnostic
	}
	if logger == nil {
		logger = slog.Default()
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
	e := &Emitter{
		cfg:        cfg,
		events:     events,
		injected:   client,
		deadLetter: deadLetter,
		cursor:     cursorStore(deadLetter),
		logger:     logger,
		now:        time.Now,
		sleep:      sleepCtx,
		mailer:     smtpMailer{},
	}
	e.deliveries, _ = deadLetter.(DeliveryRecorder)
	if err := e.SetEndpoints(cfg.Endpoints); err != nil {
		return nil, err
	}
	return e, nil
}

// SetEndpoints validates and installs a new endpoint set. An invalid set
// leaves the running endpoints unchanged.
func (e *Emitter) SetEndpoints(endpoints []Endpoint) error {
	if err := validateSet(endpoints); err != nil {
		return err
	}
	clients := make(map[string]endpointClient, len(endpoints))
	installed := make([]Endpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		allowLocal, _ := validateEndpoint(ep)
		installed = append(installed, ep)
		if e.injected != nil {
			clients[ep.ID] = endpointClient{doer: e.injected, url: ep.URL, allowPrivate: ep.AllowPrivateNetwork}
			continue
		}
		clients[ep.ID] = endpointClient{doer: newDeliveryClient(allowLocal), url: ep.URL, allowPrivate: ep.AllowPrivateNetwork}
		if !allowLocal && looksLikeLANHostname(ep.URL) {
			e.logger.Warn("webhook endpoint hostname looks local but allow_private_network is not set; deliveries to local addresses will be refused and dead-lettered",
				"endpoint", ep.ID)
		}
	}
	e.mu.Lock()
	e.cfg.Endpoints = installed
	e.clients = clients
	e.mu.Unlock()
	return nil
}

// validateSet checks a whole endpoint set without installing it: unique IDs
// and every endpoint valid.
func validateSet(endpoints []Endpoint) error {
	seen := make(map[string]bool, len(endpoints))
	for _, ep := range endpoints {
		if seen[ep.ID] {
			return ErrConfigDiagnostic
		}
		seen[ep.ID] = true
		if _, err := validateEndpoint(ep); err != nil {
			return ErrConfigDiagnostic
		}
	}
	return nil
}

// endpoints returns a snapshot of the installed endpoints.
func (e *Emitter) endpoints() []Endpoint {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Endpoint(nil), e.cfg.Endpoints...)
}

// endpointClient is a delivery client bound to the destination and local
// network approval it was built for.
type endpointClient struct {
	doer         Doer
	url          string
	allowPrivate bool
}

// client returns the delivery client for ep. A delivery that began before a
// reload carries its old endpoint snapshot; when the installed endpoint with
// that ID now has another URL or approval, the snapshot is stale and gets no
// client, so it can never borrow the replacement's egress policy.
func (e *Emitter) client(ep Endpoint) (Doer, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	entry, ok := e.clients[ep.ID]
	if !ok || entry.url != ep.URL || entry.allowPrivate != ep.AllowPrivateNetwork {
		return nil, false
	}
	return entry.doer, true
}

// validateEndpoint checks one endpoint and reports whether its destination
// approves local address classes.
func validateEndpoint(ep Endpoint) (bool, error) {
	if strings.TrimSpace(ep.ID) == "" || strings.TrimSpace(ep.URL) == "" {
		return false, ErrConfigDiagnostic
	}
	format, ok := ParseFormat(string(ep.Format))
	if !ok {
		return false, ErrConfigDiagnostic
	}
	if !validKinds(format, ep.EventKinds) {
		return false, ErrConfigDiagnostic
	}
	switch format {
	case FormatGeneric, FormatGotify:
		if len(ep.Secret) == 0 {
			return false, ErrConfigDiagnostic
		}
	case FormatEmail:
		if err := validateEmailEndpoint(ep); err != nil {
			return false, err
		}
	}
	allowLocal, err := endpointAllowsLocal(ep)
	if err != nil {
		return false, ErrConfigDiagnostic
	}
	return allowLocal, nil
}

// maxEventKinds bounds one endpoint's filter list.
const maxEventKinds = 64

// validKinds checks a filter list: a non-generic format may list only derived
// kinds; a generic one may also list spine kinds, which are short lowercase
// dotted names.
func validKinds(format Format, kinds []string) bool {
	if len(kinds) > maxEventKinds {
		return false
	}
	for _, kind := range kinds {
		if isDerivedKind(kind) {
			continue
		}
		if format != FormatGeneric || !spineKindShape(kind) {
			return false
		}
	}
	return true
}

func spineKindShape(kind string) bool {
	if kind == "" || len(kind) > 64 {
		return false
	}
	for _, r := range kind {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// Run drives the emitter until ctx is canceled. Each tick drains the outbox
// from the cursor, delivering each new event to every matching endpoint, and
// then, when a Catalog is wired, derives playback and activity events. It is
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
			next, err := e.tick(ctx, cursor)
			cursor = next
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				e.logger.Error("webhook emitter tick failed", "error", err)
			}
		}
	}
}

// tick runs one poll: outbox, then derived playback and activity events.
// With no enabled endpoint it does no reads at all, and the first tick after
// an endpoint is enabled starts from the current tail rather than replaying
// history the owner never subscribed to.
func (e *Emitter) tick(ctx context.Context, cursor int64) (int64, error) {
	defer e.flushState()
	if e.derive == nil {
		return e.drain(ctx, cursor)
	}
	if !e.anyEnabled() {
		e.derive.idle()
		return cursor, nil
	}
	if seeded, next, err := e.derive.seed(ctx, cursor); err != nil {
		return cursor, err
	} else if seeded {
		cursor = next
		if e.cursor != nil {
			if err := e.cursor.SetCursor(cursor); err != nil {
				return cursor, ErrCursorDiagnostic
			}
		}
	}
	next, err := e.drain(ctx, cursor)
	if err != nil {
		return next, err
	}
	if err := e.derive.playback(ctx, e); err != nil {
		return next, err
	}
	return next, e.derive.activity(ctx, e)
}

func (e *Emitter) anyEnabled() bool {
	for _, ep := range e.endpoints() {
		if !ep.Disabled {
			return true
		}
	}
	return false
}

// subscribed reports whether any enabled endpoint receives kind.
func (e *Emitter) subscribed(kind string) bool {
	for _, ep := range e.endpoints() {
		if !ep.Disabled && ep.matches(kind) {
			return true
		}
	}
	return false
}

func (e *Emitter) flushState() {
	if f, ok := e.deadLetter.(interface{ Flush() error }); ok {
		if err := f.Flush(); err != nil {
			e.logger.Error("webhook state write failed", "error", ErrCursorDiagnostic)
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

// deliver offers one spine event to every enabled endpoint whose filter
// accepts it, then any derived event it implies (a new Media is media.added).
func (e *Emitter) deliver(ctx context.Context, event store.OutboxEvent) error {
	body := wireBody(event)
	for _, ep := range e.endpoints() {
		if ep.Disabled || !ep.matches(event.Kind) {
			continue
		}
		if err := e.deliverTo(ctx, ep, event, body); err != nil {
			return err
		}
	}
	if e.derive == nil || event.Kind != store.MediaEventCreated || !e.subscribed(KindMediaAdded) {
		return nil
	}
	n, ok, err := e.derive.mediaAdded(ctx, event)
	if err != nil || !ok {
		return err
	}
	return e.notify(ctx, n)
}

// deliverTo POSTs one event to one endpoint with HMAC signing and exponential-
// backoff retry. On exhaustion it routes the delivery to the dead-letter.
func (e *Emitter) deliverTo(ctx context.Context, ep Endpoint, event store.OutboxEvent, body []byte) error {
	signature := sign(ep.Secret, body)
	return e.withRetry(ctx, ep, event.Seq, event.Kind, body, func() (int, error) {
		return e.send(ctx, ep, genericRequest(ep, strconv.FormatInt(event.Seq, 10), body, signature))
	})
}

// notify offers one derived event to every enabled endpoint that receives it.
func (e *Emitter) notify(ctx context.Context, n Notification) error {
	var targets []Endpoint
	for _, ep := range e.endpoints() {
		if !ep.Disabled && ep.matches(n.Kind) {
			targets = append(targets, ep)
		}
	}
	if len(targets) == 0 {
		return nil
	}
	n = e.withArtwork(ctx, n, targets)
	for _, ep := range targets {
		if err := e.deliverNotification(ctx, ep, n); err != nil {
			return err
		}
	}
	return nil
}

// withArtwork loads the Media's artwork once when an endpoint format can
// attach a file (the public artwork URL needs a session, so a chat app could
// not fetch it itself).
func (e *Emitter) withArtwork(ctx context.Context, n Notification, targets []Endpoint) Notification {
	if e.artwork == nil || n.MediaID == "" || n.artwork != nil {
		return n
	}
	for _, ep := range targets {
		if f := ep.format(); f == FormatDiscord || f == FormatNtfy {
			art, err := e.artwork.Artwork(ctx, store.MediaID(n.MediaID))
			if err == nil && art != nil && len(art.Data) > 0 && len(art.Data) <= maxArtworkBytes {
				n.artwork = art
			}
			return n
		}
	}
	return n
}

// maxArtworkBytes caps an attached image below the smallest common upload
// limit (Discord's default 8 MiB is the tightest).
const maxArtworkBytes = 7 << 20

// deliverNotification renders a derived event for one endpoint and delivers
// it with the same retry and dead-letter policy as a spine event.
func (e *Emitter) deliverNotification(ctx context.Context, ep Endpoint, n Notification) error {
	generic := genericNotificationBody(n)
	return e.withRetry(ctx, ep, 0, n.Kind, generic, func() (int, error) {
		return e.sendNotification(ctx, ep, n, generic)
	})
}

// sendNotification performs one attempt of a derived-event delivery.
func (e *Emitter) sendNotification(ctx context.Context, ep Endpoint, n Notification, generic []byte) (int, error) {
	switch ep.format() {
	case FormatGeneric:
		return e.send(ctx, ep, genericRequest(ep, n.ID, generic, sign(ep.Secret, generic)))
	case FormatEmail:
		if e.mailer == nil {
			return 0, ErrTransportDiagnostic
		}
		return 0, e.mailer.Send(ctx, ep, renderEmail(ep, n))
	}
	out, err := renderNotification(ep, n)
	if err != nil {
		return 0, err
	}
	status, err := e.send(ctx, ep, out)
	if err != nil && out.fallback != nil && status >= 400 && status < 500 {
		return e.send(ctx, ep, *out.fallback)
	}
	return status, err
}

// withRetry runs attempt with exponential backoff up to MaxAttempts, records
// the outcome in the delivery log, and dead-letters an exhausted delivery.
// payload is what the dead-letter keeps for a manual replay; it never holds a
// secret or a signature.
func (e *Emitter) withRetry(ctx context.Context, ep Endpoint, seq int64, kind string, payload []byte, attempt func() (int, error)) error {
	started := e.now()
	var lastErr error
	var status int
	backoff := e.cfg.BaseBackoff
	attempts := 0
	for attempts < e.cfg.MaxAttempts {
		attempts++
		if attempts > 1 {
			if err := e.sleep(ctx, backoff); err != nil {
				return err // interrupted mid-backoff: leave the event for the next drain
			}
			backoff *= 2
		}
		status, lastErr = attempt()
		if lastErr == nil {
			e.recordDelivery(ep, kind, OutcomeDelivered, status, attempts, started, false)
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	e.recordDelivery(ep, kind, OutcomeFailed, status, attempts, started, false)
	// Exhausted: dead-letter so the operator can inspect/replay. The error
	// string is endpoint/status only — never the secret or the signature.
	e.logger.Warn("webhook delivery exhausted, dead-lettering",
		"endpoint", ep.ID, "event_seq", seq, "event_kind", kind,
		"attempts", e.cfg.MaxAttempts, "error", lastErr)
	if e.deadLetter == nil {
		return nil
	}
	if err := e.deadLetter.Record(DeadLetter{
		EndpointID: ep.ID,
		EventSeq:   seq,
		EventKind:  kind,
		Attempts:   e.cfg.MaxAttempts,
		LastError:  lastErr.Error(),
		Payload:    json.RawMessage(payload),
		At:         e.now().UTC(),
	}); err != nil {
		redactedCause := err.Error()
		if len(ep.Secret) > 0 {
			redactedCause = strings.ReplaceAll(redactedCause, string(ep.Secret), "[REDACTED]")
		}
		e.logger.Error("webhook dead-letter write failed", "endpoint", ep.ID, "event_seq", seq, "error", redactedCause)
		return ErrDeadLetterDiagnostic
	}
	return nil
}

func (e *Emitter) recordDelivery(ep Endpoint, kind, outcome string, status, attempts int, started time.Time, test bool) {
	if e.deliveries == nil {
		return
	}
	e.deliveries.RecordDelivery(Delivery{
		EndpointID:  ep.ID,
		EventKind:   kind,
		Outcome:     outcome,
		StatusClass: statusClass(status),
		Attempts:    attempts,
		DurationMs:  e.now().Sub(started).Milliseconds(),
		At:          e.now().UTC(),
		Test:        test,
	})
}

// statusClass reduces an HTTP status to its class ("2xx", "4xx"), the only
// part of a response the delivery log keeps.
func statusClass(status int) string {
	if status < 100 || status > 599 {
		return ""
	}
	return fmt.Sprintf("%dxx", status/100)
}

// genericRequest builds the signed generic-format request.
func genericRequest(ep Endpoint, eventID string, body, signature []byte) outbound {
	header := jsonHeader()
	header.Set(SignatureHeader, "sha256="+hex.EncodeToString(signature))
	header.Set(EventIDHeader, eventID)
	return outbound{method: http.MethodPost, url: ep.URL, header: header, body: body}
}

// post performs one generic delivery attempt of a spine event. A non-2xx
// response (including a refused redirect) or transport error is a failure
// worth retrying.
func (e *Emitter) post(ctx context.Context, ep Endpoint, event store.OutboxEvent, body, signature []byte) error {
	_, err := e.send(ctx, ep, genericRequest(ep, fmt.Sprintf("%d", event.Seq), body, signature))
	return err
}

// send performs one HTTP request and returns the response status (0 when no
// response arrived). The response body is drained and discarded unread.
func (e *Emitter) send(ctx context.Context, ep Endpoint, out outbound) (int, error) {
	client, ok := e.client(ep)
	if !ok {
		return 0, ErrRequestDiagnostic
	}
	req, err := http.NewRequestWithContext(ctx, out.method, out.url, bytes.NewReader(out.body))
	if err != nil {
		return 0, ErrRequestDiagnostic
	}
	for key, values := range out.header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, ErrTransportDiagnostic
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, ErrStatusDiagnostic
	}
	return resp.StatusCode, nil
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
