# Tasks (CORE-2683)

- [ ] Store the compat settings in `server_config` with bootstrap fallbacks, trust-zone CIDR validation and allowlist validation.
- [ ] Check library eligibility at refresh time and wire it into the compat refresher.
- [ ] Reload the compat handler's settings in place; force it off on App Store builds.
- [ ] Add the `jellyfinCompat`, `setJellyfinCompat` and `setProfileIntegrationSignIn` graph fields outside App Store builds, audited and admin-only.
- [ ] Extend the App Store suppression conformance test to the new fields.
- [ ] Add the Console page behind the App Store host firewall, with strings in every locale and tests for the enable, allowlist and opt-in flows.
- [ ] Regenerate the wire goldens and publish the additive client-core-wire minor.
- [ ] Check apple-ui for a forbidden-strings check covering compat and *arr names; extend it or file a ticket.
