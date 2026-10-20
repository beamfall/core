# Route access classification & private→public promotion rule

Status: normative for Beamfall Core route mounting (GCR-012-A / GCR-012-D,
ADR-0040 §2, ADR-0060).

Every HTTP route the Core server mounts carries an **access class** in addition
to its Reveal `SurfaceClass`. The Reveal class governs *what content a surface
may leak*; the access class records *who the route is for* and therefore which
credential gate it sits behind. The classification lives in
`internal/server/route_class.go` (`routeAccessClass`), and
`TestEveryMountedRouteIsClassified` fails the build if any mounted route is
unclassified — a new route cannot ship without a reviewed classification.

## Classes

| Class | Meaning | Credential gate |
| --- | --- | --- |
| `public-v1` | The ADR-0040 `/v1/` stable developer contract — the single deliberately-public API line. | Scoped developer key, per-route scope. |
| `private-ui` | First-party Core UI surface (`/api`, `/web`) consumed by the web app. Not a public contract; shape may change with the client. | Session/device credential (+ CSRF header on cookie-authenticated writes). |
| `admin-automation` | Admin/automation surface authorized by an admin API token. Core currently layers admin authorization onto the private-UI surface via scope rather than dedicated admin-only paths, so no route carries this class today; it exists so a future dedicated admin route is classified and audited deliberately. | Admin API token. |
| `internal-only` | Unauthenticated ops / bootstrap / loopback: liveness, metrics, AGPL source offer, pre-enrollment first-run probe, published moderation contact, root redirect, loopback-only test-fault control. | None (exposes no library content, resolves no session). |

## Private → public promotion rule

The `/v1/` surface is the **only** public line. Promoting a private route to the
public contract is a deliberate, reviewed act, never an accident:

The approving authority is the **Beamfall project operator**: every promotion
into `/v1/` requires that operator's explicit approval in the promoting change
because it creates the ADR-0040 compatibility promise. Approval by a
deployment's server operator or through routine code review is not sufficient.

1. A route becomes public **only** by mounting it under the `/v1/` prefix with a
   per-route scope in `v1RouteContracts` (`internal/server/v1_contract.go`).
   `routeAccessClass` classifies `/v1/` structurally, so the prefix *is* the
   boundary.
2. The public line is frozen by `TestV1RouteSetIsPinned` against `v1PublicLine`
   (`internal/server/route_class_test.go`). Adding or removing a `/v1` route
   fails that test until the pin is updated in the same change.
3. `docs/api/openapi.json` is the pinned public wire line and must be updated in
   lockstep with any `/v1` route or shape change.
4. No `private-ui`, `internal-only`, or `admin-automation` route may share the
   `/v1/` prefix; `TestAccessClassPolicyConsistency` enforces the 1:1 mapping
   between the `/v1/` prefix and the `public-v1` class.

The effect: a private Core-UI endpoint can never leak onto the public contract
by copy-paste — promotion requires a new `/v1` contract entry, a pin update, and
an openapi.json update, each visible in review.
