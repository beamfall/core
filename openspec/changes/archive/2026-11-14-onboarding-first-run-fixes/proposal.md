# Fix the first-run onboarding path

Tickets: CORE-1598, CORE-1599, CORE-1600, CORE-1601, CORE-1603, CORE-1604, CORE-1606, CORE-1607
(partly), CORE-1608, CORE-1609, CORE-1610 (`queue:core:main`).

## Why

A first-run walkthrough found places where a new owner is misled or stranded: a Library could be
created on a folder that does not exist, the offline `pairing-code` and `reset-admin-password`
commands used a different data directory from the server and could create a new empty database, an
admin sign-in refused for a rebindable LAN hostname gave no way forward, the Console reported "Done"
for a Library that never scanned, and the setup wizard changed its step count and choices on reload.

## What Changes

- Creating a Library, or pointing one at a folder, refuses a folder that is missing, is not a
  directory or cannot be read, before anything is created; the wizard shows the reason by the field.
- `pairing-code` and `reset-admin-password` resolve `--data-dir` as the server does and refuse a data
  directory with no database; `pairing-code` prints a real URL and a local-time expiry on its own.
- A refusal for a rebindable LAN hostname carries the code `lan_hostname_not_allowed` and, for a
  browser on the same LAN, the server's IP URL(s). The DNS-rebinding protection is unchanged.
- A new admin password must be at least 8 characters; existing shorter ones still verify.
- First-run startup prints one setup block last, a fresh data dir logs one migration summary line, and
  an anonymous first page load logs no auth-refused warning.
- The wizard keeps its step count and choices across a reload.
- The Console checklist and Overview report a Library with a missing folder or no successful scan.
- The "Add a device" panel shows expiry, role, address and a link that prefills the code from the URL
  fragment.
- The enroll page shows admin-password sign-in, Strict mode says how to recover, sign-out warns that
  the browser is forgotten, and expiry copy is consistent.
- Wizard radios have readable accessible names.

## Non-goals

- Marking signed-out devices in the device list (CORE-1607). It needs a wire-contract field and
  follows in a separate ticket.
- Changing the DNS-rebinding protection, the setup token, or the sign-in throttles.
- Auto-submitting a code from the enroll link.
