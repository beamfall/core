# Tasks (CORE-2682)

- [ ] Amend ADR-0171 §2.3's dashboard row to name `GET /Sessions` and `GET /Items/Counts`.
- [ ] Serve `GET /Sessions` from `store.ActiveSessions` for opted-in Profiles and visible catalog items only, with opaque session IDs and no device fields.
- [ ] Serve `GET /Items/Counts` from the filtered catalog indexes, keyed by declared item type.
- [ ] Tests: opted-out, disabled and managed Profiles excluded; hidden, sealed and unmapped items excluded; stale and ended playbacks excluded; item fields and ticks mapped; read scope required; counts filtered.
- [ ] Extend the route-inventory and fall-through tests so no session-control route exists.
- [ ] Record a run against a pinned Homepage container showing now-playing for an opted-in Profile.
