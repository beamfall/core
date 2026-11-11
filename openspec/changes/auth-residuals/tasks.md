## 1. Admin Session on ownership transfer

- [ ] 1.1 `Store.SetProfileOwner` clears the Admin Session window of every session not bound to the new owner, in the designation transaction
- [ ] 1.2 Test: the outgoing owner's sessions lose their window; the new owner's session keeps its own

## 2. Setup throttle

- [ ] 2.1 Key the setup throttle on the request source only; requests without a source share one unknown-source lane
- [ ] 2.2 Test: wrong tokens from one source do not throttle a correct token from another source

## 3. Demo enrollment

- [ ] 3.1 Apply the remote-enrollment gate to demo-grant enrollment
- [ ] 3.2 Test: off-LAN demo enrollment is refused when remote enrollment is disabled, accepted when enabled

## 4. Verify and close

- [ ] 4.1 Run `go test -race ./internal/store/ ./internal/pairing/ -run 'Owner|Setup|Demo'`
- [ ] 4.2 Run `make gate`
- [ ] 4.3 Run `openspec archive auth-residuals` after landing, before completing CORE-0728
