package server

import "strings"

// AccessClass is the access-control classification of a mounted HTTP route
// (GCR-012-A, ADR-0040 §2 / ADR-0060). It is orthogonal to the Reveal
// SurfaceClass (which governs what content a surface may leak): AccessClass
// records WHO a route is for and therefore which credential gate it sits
// behind. Every mounted route MUST resolve to exactly one non-empty class.
// routeAccessClass returns AccessClassUnclassified for any (method, pattern) not
// deliberately classified here, and TestEveryMountedRouteIsClassified fails on
// it — so a new route cannot ship without a reviewed classification, and a
// private route cannot be promoted to the public /v1 line by accident.
// Promotion to /v1 requires explicit Beamfall project-operator approval; see
// docs/api/route-classes.md#private--public-promotion-rule.
type AccessClass string

const (
	// AccessClassUnclassified is the zero value: a route nobody has classified.
	// It is never a legitimate steady state — the completeness test rejects it.
	AccessClassUnclassified AccessClass = ""

	// AccessPublicV1 is the ADR-0040 /v1/ stable developer contract: reachable
	// only by a scoped developer key, per-route scope-gated. This is the single
	// deliberately-public API line.
	AccessPublicV1 AccessClass = "public-v1"

	// AccessPrivateUI is the first-party Core UI surface (/api and /web)
	// consumed by the session/device-authenticated web app. Not a public
	// contract; shape may change with the client.
	AccessPrivateUI AccessClass = "private-ui"

	// AccessAdminAutomation is the admin/automation surface authorized by an
	// admin API token. Beamfall Core currently layers admin authorization onto
	// the private-UI surface via scope rather than dedicated admin-only route
	// paths, so no route carries this class today; the class exists so that any
	// future dedicated admin/automation route is classified (and audited)
	// deliberately rather than defaulting into private-UI.
	AccessAdminAutomation AccessClass = "admin-automation"

	// AccessInternalOnly is the unauthenticated ops / bootstrap / loopback
	// surface: liveness and metrics probes, the AGPL source offer, the
	// pre-enrollment first-run probe, the published moderation contact, the
	// root redirect, and the loopback-only test-fault control. These expose no
	// library content and resolve no session.
	AccessInternalOnly AccessClass = "internal-only"
)

type accessPolicy struct {
	requireCredential         bool
	allowCrossOrigin          bool
	allowTokenByteCrossOrigin bool
	requireCookieCSRF         bool
}

// accessPolicies is the transport policy selected by a route's AccessClass.
// Handlers still validate credential contents, scopes, roles, and route-local
// bootstrap tokens; this layer rejects anonymous class access, unsafe origins,
// and cookie mutations before dispatch.
var accessPolicies = map[AccessClass]accessPolicy{
	AccessPublicV1: {
		requireCredential: true,
		allowCrossOrigin:  true,
	},
	AccessPrivateUI: {
		allowTokenByteCrossOrigin: true,
		requireCookieCSRF:         true,
	},
	AccessAdminAutomation: {
		requireCredential: true,
	},
	AccessInternalOnly: {},
}

// exactRouteClasses maps "METHOD PATTERN" to its AccessClass. Keying on
// method+pattern (not pattern alone) disambiguates routes that share a path
// under different verbs — e.g. POST /api/setup (first-party bootstrap) versus
// GET /api/setup (unauthenticated first-run probe).
var exactRouteClasses = map[string]AccessClass{
	// Internal-only: unauthenticated ops, bootstrap probe, loopback controls.
	"GET /health":                 AccessInternalOnly,
	"GET /metrics":                AccessInternalOnly,
	"GET /source":                 AccessInternalOnly,
	"GET /{$}":                    AccessInternalOnly,
	"GET /api/setup":              AccessInternalOnly,
	"GET /api/moderation/contact": AccessInternalOnly,
	"POST /api/test/faults":       AccessInternalOnly,

	// Private Core UI: session/device-authenticated first-party surfaces.
	// /api/graphql additionally refuses scoped developer keys inside the
	// handler (ADR-0040: their surface is exactly the /v1/ line) — see
	// internal/graph.Handler.ServeHTTP and TestScopedKeySurfaceSplit.
	"POST /api/graphql":                 AccessPrivateUI,
	"POST /api/compat/handshake":        AccessPrivateUI,
	"GET /api/manifest":                 AccessPrivateUI,
	"POST /api/playback-info":           AccessPrivateUI,
	"POST /api/preview-info":            AccessPrivateUI,
	"POST /api/beam/offers":             AccessPrivateUI,
	"POST /api/beam/claims":             AccessPrivateUI,
	"POST /api/pairing-codes":           AccessPrivateUI,
	"POST /api/enroll":                  AccessPrivateUI,
	"POST /api/moderation/report":       AccessPrivateUI,
	"POST /api/moderation/block":        AccessPrivateUI,
	"GET /api/moderation/blocks":        AccessPrivateUI,
	"GET /api/push/notifications/{id}":  AccessPrivateUI,
	"GET /api/media/direct/":            AccessPrivateUI,
	"GET /api/media/artwork-candidate/": AccessPrivateUI,
	"GET /api/media/artwork/":           AccessPrivateUI,
	"POST /api/media/artwork/upload":    AccessPrivateUI,
	"GET /api/entity/artwork/":          AccessPrivateUI,
	"GET /api/media/hls/":               AccessPrivateUI,
	"GET /api/media/assets/":            AccessPrivateUI,
	"GET /api/media/subtitle/":          AccessPrivateUI,
	"GET /api/media/sidecar/":           AccessPrivateUI,
	// Per-Profile subtitle preference writes. They persist
	// viewer state behind a Profile session, exactly like the moderation
	// report/block writes above, so they are private-UI rather than public-v1 or
	// admin-automation: a scoped developer key's closed vocabulary does not cover
	// a viewer's subtitle choices, and the handlers refuse one outright.
	"POST /api/media/subtitle-pin":    AccessPrivateUI,
	"POST /api/media/subtitle-offset": AccessPrivateUI,
	"GET /api/media/proxy/":           AccessPrivateUI,
	"GET /api/pack/authority":         AccessPrivateUI,
	"GET /api/pack/manifest/":         AccessPrivateUI,
	"GET /api/pack/content/":          AccessPrivateUI,
	"GET /api/pack/key/":              AccessPrivateUI,
	"GET /api/pack/status/":           AccessPrivateUI,
	"GET /web/":                       AccessPrivateUI,
	"GET /api/events":                 AccessPrivateUI,
	"POST /api/group/calibrate":       AccessPrivateUI,
	"POST /api/web/enroll":            AccessPrivateUI,
	"POST /api/web/logout":            AccessPrivateUI,
	"POST /api/web/promote":           AccessPrivateUI,
	"POST /api/setup":                 AccessPrivateUI,
}

// routeAccessClass classifies a mounted route. The /v1/ prefix is the single
// public developer line, so it resolves structurally; every other
// route must be the scoped multipart upload or listed in exactRouteClasses.
func routeAccessClass(method, pattern string) AccessClass {
	if strings.HasPrefix(pattern, "/v1/") {
		return AccessPublicV1
	}
	if method+" "+pattern == createMediaArtworkRouteKey {
		return AccessPrivateUI
	}
	return exactRouteClasses[method+" "+pattern]
}

// RouteAccessClass reports the AccessClass of a Route (exported for conformance
// and inventory callers).
func (r Route) RouteAccessClass() AccessClass {
	return routeAccessClass(r.Method, r.Pattern)
}
