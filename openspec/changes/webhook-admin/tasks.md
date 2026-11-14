## 1. Emitter

- [x] 1.1 Reloadable endpoint set; the emitter always runs and idles with no enabled endpoint
- [x] 1.2 Endpoint config store that writes `webhook-endpoints.json` and secrets to the `0600` secret store
- [x] 1.3 Formats: generic, discord, slack, ntfy, gotify, email (SMTP)
- [x] 1.4 Derived events: media.added, playback start/stop, activity-log events
- [x] 1.5 Artwork URL from the public base URL; artwork attachment for Discord and ntfy
- [x] 1.6 Delivery log and test-send

## 2. Server

- [x] 2.1 Activity-log tail read after an id
- [x] 2.2 Admin graph operations and app wiring
- [x] 2.3 Regenerate wire goldens through `make regen-wire-goldens` and `make mgmt-manifest`, and publish wire contract 2.5.0

## 3. Console and docs

- [x] 3.1 Webhooks console page: list, add, edit, test, remove, delivery log
- [x] 3.2 Update `docs/api/home-automation.md`
