# Tasks (CORE-2709)

- [x] Return the fuller `AuthenticateByName` user object with `IsAdministrator` only for setup sessions.
- [x] Add the server-assigned `compat.setup` scope, refuse it from callers, and bind it in the store to the owner, the admin reservation and the 15 minute window.
- [x] Report a setup account ID from the admin sign-in so a Profile sign-in never matches Seerr's setup admin.
- [x] Add setup-scoped `Auth/Keys` POST and GET that echo the caller's own setup token, and prove the token fails after the setup TTL.
- [x] Update the route inventory test.
- [ ] Complete setup from the pinned Seerr container in a recorded run.
- [x] Record the `Auth/Keys` decision in ADR-0171 §2.8.
