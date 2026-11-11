# Keep private feed credentials on the feed origin

Ticket: CORE-0745 (`queue:core:main`).

## Why

`pollSubscription` resolves a private Subscription's `Authorization` header and sends it with the
feed fetch and with every enclosure download. An enclosure URL comes from the feed document, so a
feed (or anyone who can insert an item into it) can name an enclosure on another host or port and
receive the private-feed credential.

## What Changes

- The Subscription Poller sends the feed credential with an enclosure download only when the
  enclosure URL has the same normalized origin as the Subscription's feed URL: same scheme,
  case-insensitive hostname and effective port (`443` for `https`, `80` for `http` when omitted).
- An enclosure on any other origin, or one whose URL does not parse, is downloaded without an
  `Authorization` header.
- `internal/scrape` exports `SameOrigin(a, b string) bool`, built on the normalization its
  redirect check already uses.
- Redirects keep their existing rule: the download client refuses any redirect that leaves the
  origin of the request it started, so a same-origin enclosure that redirects elsewhere is
  refused and the other origin receives nothing.

## Non-goals

- A separate credential binding for cross-origin enclosure hosts. There is no store field for it
  and this change adds no migration. Cross-origin enclosures are fetched anonymously, which is how
  public CDN and signed-URL enclosures already work. A binding can be added later as its own
  change.
- Changing the feed fetch, the redirect policy, or how credentials are stored and resolved.

## Failure modes

- A private feed whose enclosures live on another origin that also requires the feed credential
  now gets a `401`/`403` on those downloads. The poll records the failure as before, keeps the
  entry unseen and retries on the next poll.

## Acceptance evidence

- `TestAuditFeedAuth` in `internal/ingest/ingest_test.go`: a private feed lists a same-origin
  enclosure, an enclosure on another port, and a same-origin enclosure that redirects to that
  other port. Only the feed origin receives the credential; the other origin never does.
- `TestSameOrigin` in `internal/scrape/ssrf_test.go` covers scheme, host case, default and
  explicit ports, other hosts and unparseable URLs.

## Rollback

Revert the change. No stored state depends on it.

## Capabilities

### New Capabilities

- `feed-subscription-credentials`: where a Subscription's private-feed credential may be sent.

### Modified Capabilities

None.
