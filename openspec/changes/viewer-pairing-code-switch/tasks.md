# Tasks (CORE-0393)

- [x] 1. Persist `enrollment.viewer_pairing_codes_disabled` in console config (default off)
- [x] 2. Refuse Viewer mint and redemption in `internal/pairing`; audit each refusal
- [x] 3. Expose `viewerPairingCodesDisabled` on `consoleConfig` / `setConsoleConfig`
- [x] 4. Console Config switch with help text and localized strings
- [x] 5. Tests: store round-trip, pairing refusals, Manager and demo unaffected, graph mutation, web request
- [ ] 6. Regenerate wire goldens in the stack's additive wire-contract minor; evidence file
