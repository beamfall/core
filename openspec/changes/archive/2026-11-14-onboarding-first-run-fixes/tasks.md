## 1. Server and CLI

- [x] 1.1 Check Library folders before create and root update (`store.CheckLibraryRoots`, GraphQL)
- [x] 1.2 Resolve `--data-dir` to an absolute path and add `RequireExisting` for offline commands
- [x] 1.3 `pairing-code`: real URL, local-time expiry, quiet store logging
- [x] 1.4 `lan_hostname_not_allowed` code and `lanUrls` on the admin sign-in and setup refusals
- [x] 1.5 Minimum admin password length on every set path
- [x] 1.6 Setup block printed last, one migration summary line, no warning for anonymous renewal

## 2. Web

- [x] 2.1 Wizard: folder reason by the field, stable step count and choices on reload, radio names
- [x] 2.2 Console: Library folder and first-scan health in the checklist and Overview
- [x] 2.3 Devices: expiry, role, address and fragment link; enroll page sign-in, Strict hint, sign-out hint

## 3. Verify and close

- [x] 3.1 Focused Go and web tests for each item
- [x] 3.2 Run `make gate`
- [x] 3.3 Run `openspec archive onboarding-first-run-fixes` after landing, before completing the tickets
