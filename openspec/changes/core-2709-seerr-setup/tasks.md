# Tasks (CORE-2709)

- [ ] Return the fuller `AuthenticateByName` user object with `IsAdministrator` only for setup sessions.
- [ ] Add the server-assigned `compat.setup` scope and refuse it from callers.
- [ ] Add setup-scoped `Auth/Keys` POST and GET that echo the caller's own setup token, and prove the token fails after the setup TTL.
- [ ] Update the route inventory test.
- [ ] Complete setup from the pinned Seerr container in a recorded run.
- [ ] Record the `Auth/Keys` decision in ADR-0171 §2.8.
