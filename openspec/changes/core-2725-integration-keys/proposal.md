# Durable, revocable Jellyfin compatibility integration keys (CORE-2725)

## Why

Every compatibility credential expires today: the admin setup session after 15 minutes, an opted-in
Profile's integration session after 24 hours, and neither can be renewed. Seerr and the *arr tools
store one token when they are configured and have no re-authentication flow, so they stop syncing
within a day. Expiry alone cannot fix that, because any expiry short enough to matter forces the
operator to reconfigure every tool on that schedule. ADR-0171 §2.4 needs a standing credential that
is bounded by other means.

## What changes

- A new `compat_integration_key` table holds keys bound to one Profile. It stores only the SHA-256
  digest of a 256-bit random token. The plaintext is returned once, at mint.
- Keys carry `compat.read` and `compat.refresh` only. The store refuses `compat.setup`. A key is
  minted only by an admin from the Console, never by a compatibility route and never from the admin
  password: `Auth/Keys` still creates nothing.
- A key authenticates only on compatibility routes. It goes through the same Profile, opt-in, scope,
  trust-zone and relay checks as an integration session. Revoking it, disabling or demoting the
  Profile, or withdrawing the Profile's opt-in stops it on the next request. Withdrawing the opt-in
  also revokes the Profile's keys. Revoked rows are kept for audit attribution.
- New admin graph fields `jellyfinCompatKeys`, `createJellyfinCompatKey` and
  `revokeJellyfinCompatKey`, audited and compiled out of App Store builds. The list shows each key's
  name, Profile, creation time and last use. Last use is updated at most once a minute.
- The Console compatibility page gains a keys panel: mint (token shown once), list with last use,
  revoke.
- A test-only build tag, `beamfall_simclock`, lets a recorded run move the server's read clock
  forward. Release and App Store builds never set it.
