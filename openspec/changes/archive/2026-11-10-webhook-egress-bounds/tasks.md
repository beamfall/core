## 1. Delivery client

- [x] 1.1 Install a per-endpoint default client in `webhook.New` that refuses redirects and ignores environment proxies
- [x] 1.2 Check dialed addresses against the always-refused and local classes
- [x] 1.3 Approve local addresses for local IP-literal endpoints and `allow_private_network`
- [x] 1.4 Refuse never-routable special-purpose blocks and IPv4-embedding IPv6 forms for every endpoint

## 2. Validation and config

- [x] 2.1 Reject malformed and unsafe endpoint URLs in `webhook.New`
- [x] 2.2 Read `allow_private_network` from the endpoint config file
- [x] 2.3 Warn at startup for a LAN-looking hostname endpoint without `allow_private_network`

## 3. Tests and docs

- [x] 3.1 Webhook unit tests: two-server `307`/`308`, dial classes, LAN approval, URL validation
- [x] 3.4 Webhook test pinning agreement with the scraper's public-routability predicate
- [x] 3.2 Loopback egress test for redirect refusal
- [x] 3.3 Document delivery rules and the LAN opt-in in `docs/api/home-automation.md`

## 4. Verify and close

- [x] 4.1 Run `make gate`
- [x] 4.2 Run `openspec archive webhook-egress-bounds` after landing, before completing CORE-0842
