# Tasks (CORE-2725)

- [ ] Amend ADR-0171 §2.4 with the key model: minting, scope, revocation, storage at rest, and why expiry alone does not cover the need.
- [ ] Add the `compat_integration_key` migration and store functions (mint, list, revoke, digest lookup, throttled last use), refusing `compat.setup` and non-opted-in Profiles; revoke a Profile's keys when its opt-in is withdrawn.
- [ ] Resolve keys in `IntegrationScope` after the session lookup, under the same Profile, opt-in and scope checks.
- [ ] Add the `jellyfinCompatKeys`, `createJellyfinCompatKey` and `revokeJellyfinCompatKey` admin graph fields outside App Store builds, audited, with the App Store suppression test, wire goldens and destructive-audit inventory.
- [ ] Add the Console keys panel with strings in every locale and tests for mint, list and revoke.
- [ ] Tests: no setup scope, nothing minted by `Auth/Keys` or the admin password, revoked key gets the uniform 401, relay refusal, key survives a 24 hour clock advance.
- [ ] Add the test-only `beamfall_simclock` build tag and record a Seerr run that syncs with a key past 24 hours of simulated clock and stops after revocation.
