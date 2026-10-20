package integrationhost

import (
	"context"
	"time"

	"github.com/beamfall/core/internal/causation"
	"github.com/beamfall/core/internal/store"
)

// The Host API depends on the rest of the server only through these narrow
// seams, defined at the consumer. The composition root (internal/app)
// adapts *store.Store and the 0600 secret store to them. A worker reaches Core
// only through methods these seams expose — never a DB handle, admin token, or
// raw filesystem.

// CatalogReader serves Reveal-filtered catalog reads (read-catalog permission).
// The filter is resolved from the plugin's grant before the call; the reader
// applies it the same way every other store read does.
type CatalogReader interface {
	Libraries(ctx context.Context, filter store.LibraryFilter) ([]store.Library, error)
}

// EventReader tails the append-only event outbox (event-subscribe permission),
// Reveal-filtered at delivery through the supplied filter. The composition root
// adapts this to the same delivery boundary the SSE surface uses (a "media"
// event is resolved through the filter; the raw store read is unscoped —
// outbox.go). Pushing the filter into the seam keeps the Reveal boundary in one
// place rather than reimplementing per-aggregate visibility in this package.
type EventReader interface {
	ReadEventOutboxFiltered(ctx context.Context, afterSeq int64, limit int, filter store.LibraryFilter) ([]store.OutboxEvent, error)
	// CausationToken returns the ancestor token retained alongside a descendant
	// outbox event. A root event returns the zero token and the Host mints its
	// chain identity at subscription delivery.
	CausationToken(event store.OutboxEvent) causation.Token
}

// ProfileLibraryReader resolves a bound Profile's Library set so a Profile-bound
// read grant builds its store.LibraryFilter without this package importing auth.
type ProfileLibraryReader interface {
	Profile(ctx context.Context, id store.ProfileID) (store.Profile, error)
}

// SecretStore is the plugin-scoped secret seam (plugin-secret permission). The
// host namespaces every ref as "plugin:<id>:<ref>" before delegating to the
// 0600 secret store, so a worker can never read another plugin's or a Core
// secret. Secrets are never logged, never put in descriptors/status/dead-letter.
type SecretStore interface {
	Secret(ref string) (string, error)
	SetSecret(ref, value string) error
}

// Auditor writes one audit record (status-write and every high-risk method).
// It reuses store.AppendActivity with the closed Activity* constants this ticket
// adds.
type Auditor interface {
	AppendActivity(ctx context.Context, entry store.ActivityEntry) error
}

// JobEnqueuer enqueues a plugin-owned job (plugin-job permission). The worker
// hands off work to the Core job substrate; it never runs Core jobs itself.
type JobEnqueuer interface {
	EnqueuePluginJob(ctx context.Context, pluginID, kind string, payload []byte, runAfter time.Time) (store.JobID, error)
}

// IngestRequester accepts an ingest handoff (request-ingest permission). The
// worker requests a scan/import; it does not bypass scan authority.
type IngestRequester interface {
	RequestIngest(ctx context.Context, pluginID string, libraryID store.LibraryID) error
}

// FileMutationRequester accepts an audited file-mutation request
// (request-file-mutation permission). The worker NEVER writes the filesystem
// directly; it submits a request that routes through the existing audited
// mutation path. The request is audited per call.
type FileMutationRequester interface {
	RequestFileMutation(ctx context.Context, pluginID string, req FileMutationRequest) error
}

// FileMutationRequest is one audited file-mutation a worker asks Core to perform
// on its behalf. Pure data; the server decides whether to honor it.
type FileMutationRequest struct {
	// Op names the requested mutation (e.g. "move", "rename"). Shape-named; the
	// server validates and performs it through the audited mutation path.
	Op string
	// TargetMediaID is the Media the mutation concerns.
	TargetMediaID string
	// Detail is opaque request detail the server interprets per Op.
	Detail string
}

// OutboundRequest is one outbound HTTP call a worker asks the Host to perform on
// its behalf (declared-outbound permission). Pure data: the worker never dials a
// socket through this path — the Host carries the traffic. Method/URL/Header/Body
// are the request shape; the proxy implementation validates them against the
// plugin's declared allowlist and the SSRF contract below before dialing.
type OutboundRequest struct {
	Method string
	URL    string
	Header map[string][]string
	Body   []byte
}

// OutboundResponse is the Host's reply for a carried outbound call. Pure data.
type OutboundResponse struct {
	StatusCode int
	Header     map[string][]string
	Body       []byte
}

// OutboundProxy carries operator-declared outbound HTTP for a worker
// (declared-outbound permission). Unlike the removed advisory ask-then-dial
// method, the worker never opens its own socket through this path: it hands the
// Host an OutboundRequest and the Host performs the fetch. Once a real adapter is
// wired, outbound is therefore enforced by construction, not cooperation. The
// seam is nil-by-default (dormant ⇒ permission-denied), exactly like the
// ingest/file-mutation/jobs Core-handoff seams. HardenedOutboundProxy is the
// production adapter; plugin.OutboundPolicy is its server-owned declaration and
// operator-approval source.
//
// ADR-0012 §9 SSRF contract (binding obligation of any RequestOutbound adapter).
// On every request, including after each redirect, the enforced proxy MUST:
//   - pin scheme+host to the plugin's declared-outbound allowlist and fail closed
//     if the resolved host is not declared;
//   - deny private, link-local, loopback, and cloud-metadata (169.254.169.254) IP
//     ranges;
//   - use a DNS-rebinding-safe dialer (resolve once, then dial the vetted IP);
//   - refuse cross-host redirects; and
//   - apply time and response-size caps.
//
// TODO: OS-level network confinement for native workers is separate and
// still deferred — a native worker retains direct OS socket access outside this
// proxy path. Remediation path: implement OS sandbox confinement (e.g. Linux
// seccomp-BPF network filter or macOS sandbox profile) that blocks all outbound
// sockets except those the host explicitly proxies, making declared-outbound
// enforcement hard rather than cooperative. Track as a post-v1 security
// hardening item; gate removal of this TODO on a passing OS-confinement
// integration test for each supported platform.
type OutboundProxy interface {
	// RequestOutbound performs the fetch on the worker's behalf, enforcing the
	// declared allowlist and the ADR-0012 §9 SSRF contract above server-side. The
	// worker never dials directly through this path.
	RequestOutbound(ctx context.Context, pluginID string, req OutboundRequest) (OutboundResponse, error)
}
