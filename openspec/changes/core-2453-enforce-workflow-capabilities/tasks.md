## Implementation

- [x] Preserve client-core-wire 2.37.0 and enforce the persisted settings in conversion retry and single and batch rename.
- [x] Use the existing admin capability read to filter Manage navigation and search. Preserve manager access when the read is unavailable.
- [x] Display localized server refusals on rename and conversion retry and update the Optional features tips in every shipped locale.
- [x] Cover both capability states, manager fallback and mutation refusals in focused Go and web tests.
