## 1. Server

- [x] 1.1 Record a per-AuthRef opt-out when the owner disables a credential that has a bundled default
- [x] 1.2 Stop resolving the bundled default while opted out; lift the opt-out when a new owner key is saved
- [x] 1.3 Report a disabled AuthRef as `credentialStatus` "disabled" in the Console

## 2. Console

- [x] 2.1 Say that Disable stops the provider and does not fall back to a built-in key
- [x] 2.2 Show the disabled state and do not flag a disabled key as needed

## 3. Tests

- [x] 3.1 App test: disabling a bundled credential stops the scraper with no request, survives restart, and a new owner key lifts it
- [x] 3.2 Graph test: audited disable reports "disabled" and saving a key re-enables
- [x] 3.3 Web test: Disable copy, disabled state, and the provider-key needed check
