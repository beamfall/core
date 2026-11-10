## 1. Delivery client

- [ ] 1.1 Install a per-endpoint default client in `webhook.New` that refuses redirects and ignores environment proxies
- [ ] 1.2 Check dialed addresses against the always-refused and local classes
- [ ] 1.3 Approve local addresses for local IP-literal endpoints and `allow_private_network`

## 2. Validation and config

- [ ] 2.1 Reject malformed and unsafe endpoint URLs in `webhook.New`
- [ ] 2.2 Read `allow_private_network` from the endpoint config file

## 3. Tests and docs

- [ ] 3.1 Webhook unit tests: two-server `307`/`308`, dial classes, LAN approval, URL validation
- [ ] 3.2 Loopback egress test for redirect refusal
- [ ] 3.3 Document delivery rules and the LAN opt-in in `docs/api/home-automation.md`

## 4. Verify and close

- [ ] 4.1 Run `make gate`
- [ ] 4.2 Run `openspec archive webhook-egress-bounds` after landing, before completing CORE-0842
