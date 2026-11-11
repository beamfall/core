# Bound webhook delivery redirects and outbound destinations

Ticket: CORE-0842 (`queue:core:main`).

## Why

The Webhook Emitter delivers through the default `http.Client` when the composition root passes no
client. That client follows `307`/`308` redirects and replays the POST, so a configured endpoint
can forward the event body and its `X-Beamfall-Signature` to another origin, including an internal
service. Endpoint URLs are checked only for being non-empty, and nothing limits which address the
delivery socket connects to.

## What Changes

- With no injected client, `webhook.New` installs one HTTP client per endpoint that:
  - never follows a redirect, so a `3xx` answer counts as a failed attempt. It is retried within
    the existing bounded budget and then dead-lettered;
  - ignores environment proxies;
  - checks each address it dials. Unspecified, `0.0.0.0/8`, link-local (including
    `169.254.169.254` cloud metadata), multicast and broadcast addresses are always refused, as
    are the other special-purpose blocks `internal/scrape/public_routability.go` refuses
    (documentation, benchmarking, reserved, IETF protocol assignments) and IPv6 forms that carry
    an IPv4 address (NAT64, 6to4, IPv4-compatible, Teredo), whatever address they embed.
    Loopback, private (RFC 1918, ULA), shared (`100.64.0.0/10`) and deprecated site-local
    (`fec0::/10`) addresses are refused unless the operator approved the endpoint for the local
    network.
- An endpoint is approved for the local network when its URL host is an IP literal in one of those
  local ranges, or when its config entry sets `"allow_private_network": true`. The second case is
  for LAN receivers named by hostname, such as `homeassistant.local`.
- `webhook.New` rejects an endpoint URL before any delivery when it does not parse, is not an
  absolute `http` or `https` URL with a host, carries user info, or names an IP literal in an
  always-refused class.
- `webhook.New` logs one startup warning for a hostname endpoint that looks like a LAN name
  (`localhost`, a single label, or a `.local`, `.lan`, `.home`, `.internal`, `.localdomain` or
  `.home.arpa` suffix) without `allow_private_network`, since such an endpoint configured before
  this change now dead-letters.
- `docs/api/home-automation.md` documents the delivery rules, the LAN opt-in and the upgrade note.

## Non-goals

- Changing HMAC signing, the envelope, retry counts or backoff, or the dead-letter format.
- Following same-origin redirects. Webhook receivers answer the POST directly.
- Reusing `scrape.IsPublicRoutableIP`. The architecture rules allow `webhook` to import only
  `store` and `jsonstate`, and a webhook that the operator approved for the LAN must reach private
  addresses the scraper refuses. A webhook test pins agreement with it instead.
- Applying the policy to an injected `Doer`. A caller that injects a client owns its policy; the
  app passes none.

## Failure modes

- A receiver that relies on redirects stops receiving events. Each delivery is dead-lettered after
  the configured attempts with the fixed status diagnostic.
- A LAN receiver configured by hostname without `allow_private_network` is refused at dial time
  and dead-lettered. The operator adds the flag.
- A deployment that sends webhooks only through an environment proxy can no longer reach the
  endpoint.
- A malformed or unsafe URL fails boot with the fixed configuration diagnostic. Nothing is sent.

## Acceptance evidence

- `internal/webhook/egress_test.go` covers redirect refusal for `307` and `308` with two local
  servers, dial refusal for unapproved local and always-refused addresses, LAN approval by
  literal and by flag, and URL validation.
- `internal/ioe2e/egress_webhook_test.go` runs the real emitter against a redirecting loopback
  double and a second origin, and checks that the second origin receives nothing.

## Rollback

Revert the change. No stored state depends on it. `allow_private_network` is then ignored as an
unknown JSON field.

## Capabilities

### New Capabilities

- `webhook-delivery`: where the Webhook Emitter is allowed to send event deliveries.

### Modified Capabilities

None.
