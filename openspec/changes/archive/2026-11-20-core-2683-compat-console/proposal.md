# Jellyfin compatibility Console controls and library allowlist (CORE-2683)

## Why

The Jellyfin compatibility route group (ADR-0171) can only be switched on with a bootstrap flag and
a restart, and no library is ever eligible for a compat refresh: the library allowlist does not
exist yet. Operators also have no way to opt a Profile into integration sign-in. ADR-0171 §2.2 to
§2.4 and §3 require an operator-facing, default-off toggle that applies without a restart, a
per-library allowlist that never admits a Reveal-restricted library, trust-zone CIDR settings, a
per-Profile opt-in, and no trace of any of it in App Store binaries.

## What changes

- The server stores the compat settings (enabled, allowed CIDRs, trusted proxy CIDRs, library
  allowlist) in the existing `server_config` table. An unset key falls back to the bootstrap config,
  so existing deployments keep their behaviour.
- CIDRs must lie wholly inside loopback, private (RFC 1918), link-local or IPv6 unique-local space.
  The overlay range 100.64.0.0/10 and public ranges are refused.
- Library IDs must name an existing, non-trashed, non-Reveal-restricted library. Eligibility is
  re-checked at refresh time, so a library restricted after it was allowlisted stops being eligible.
- The compat handler reloads its settings in place; turning the toggle off returns 404 for the whole
  prefix on the next request.
- New admin graph fields `jellyfinCompat`, `setJellyfinCompat` and `setProfileIntegrationSignIn`,
  compiled out of App Store builds. Turning a Profile's opt-in off revokes its integration sessions.
- A web Console page for the toggle, CIDRs, allowlist and opt-ins, with the non-affiliation
  disclaimer and the proxy note from ADR-0171 §2.8. The App Store host firewall hides it.
- App Store builds force the route group off regardless of stored settings.
