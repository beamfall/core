# Tasks (CORE-2683)

- [x] Store the compat settings in `server_config` with bootstrap fallbacks, trust-zone CIDR validation and allowlist validation.
- [x] Check library eligibility at refresh time and wire it into the compat refresher.
- [x] Reload the compat handler's settings in place; force it off on App Store builds.
- [x] Add the `jellyfinCompat`, `setJellyfinCompat` and `setProfileIntegrationSignIn` graph fields outside App Store builds, audited and admin-only.
- [x] Extend the App Store suppression conformance test to the new fields.
- [x] Add the Console page behind the App Store host firewall, with strings in every locale and tests for the enable, allowlist and opt-in flows.
- [x] Regenerate the wire goldens and publish the additive client-core-wire minor.
- [x] Check apple-ui for a forbidden-strings check covering compat and *arr names; extend it or file a ticket. (Extended the core App Store static check, which scans every App Store target, to the media-server and compat-tool names; beamfall-apple 47839f2c dropped two comment mentions it found.)
