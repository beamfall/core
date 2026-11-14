## 1. Emitter

- [ ] 1.1 Reloadable endpoint set; the emitter always runs and idles with no enabled endpoint
- [ ] 1.2 Endpoint config store that writes `webhook-endpoints.json` and secrets to the `0600` secret store
- [ ] 1.3 Formats: generic, discord, slack, ntfy, gotify, email (SMTP)
- [ ] 1.4 Derived events: media.added, playback start/stop, activity-log events
- [ ] 1.5 Artwork URL from the public base URL; artwork attachment for Discord and ntfy
- [ ] 1.6 Delivery log and test-send

## 2. Server

- [ ] 2.1 Activity-log tail read after an id
- [ ] 2.2 Admin graph operations and app wiring
- [ ] 2.3 Regenerate wire goldens through `make regen-wire-goldens` and `make mgmt-manifest`

## 3. Console and docs

- [ ] 3.1 Webhooks console page: list, add, edit, test, remove, delivery log
- [ ] 3.2 Update `docs/api/home-automation.md`
