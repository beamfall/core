# Runbook: Key lifecycle

Custody, rotation, and compromise response for encrypted-library, signing, and stream-token keys.

## Keys in scope

- Encrypted-library DEKs, KEKs, passphrases, and Recovery Keys (`internal/libencrypt`).
- Stream-token signing material (`internal/streamtoken`; keys live in the secure store / `0600`
  files — never plaintext, inv. 5).
- Release/image signing custody (DEC-7: cosign keyless via GitHub OIDC — no long-lived signing key
  to custody for releases).

## Encrypted-library custody model (as built)

Core currently wires `libencrypt.Manager` to one-secret-per-file storage under
`<DataDir>/secrets`; writes use a `0700` directory and atomic `0600` files
(`internal/app/app.go:679-684`, `internal/app/secrets.go:160-221`). The auto-unseal KEK therefore
normally shares a volume with the ciphertext. Its status disclosure explicitly treats theft of
that volume as whole-box theft; copying ciphertext without the secrets directory is the narrower
boundary that auto mode protects (`internal/app/secrets.go:237-250`,
`internal/libencrypt/manager.go:37-42`).

### DEK

The per-library DEK is a random 32-byte key. Plaintext DEKs live in the Manager's in-memory
`unsealed` map only while a library is Unsealed; persisted bundles contain independently wrapped
DEKs, not a plaintext DEK. `Seal` best-effort zeroes the live buffer and removes it from the map
(`internal/libencrypt/libencrypt.go:42-50`, `internal/libencrypt/manager.go:98-168`,
`internal/libencrypt/manager.go:304-378`, `internal/libencrypt/manager.go:583-616`). The persisted
artifact regression rejects a plaintext DEK or Recovery Key and requires the passphrase and
Recovery-Key wraps (`internal/libencrypt/libencrypt_test.go:633-664`).

### KEK

Auto mode generates a random 32-byte KEK, wraps the DEK with AES-256-GCM, and persists the
base64-encoded KEK under a per-library secret-file ref separate from the wrapped bundle. Base64 is
encoding, not encryption: custody is the `0600` file and its volume boundary. Auto-unseal reads that
KEK and retains only the unwrapped DEK in Manager memory
(`internal/libencrypt/manager.go:216-221`, `internal/libencrypt/manager.go:326-370`,
`internal/libencrypt/manager.go:494-524`). Passphrase mode generates neither a KEK nor a KEK-wrapped
DEK; the disk-backed regression requires the KEK file to be absent
(`internal/libencrypt/manager.go:326-335`, `internal/libencrypt/libencrypt_test.go:666-704`).

### Passphrase

The operator supplies the passphrase at provision or unseal time. Core derives a 32-byte key with
Argon2id; only the random salt, KDF parameters, and DEK ciphertext are persisted. The passphrase and
derived key are not stored in the Manager or secret files
(`internal/libencrypt/libencrypt.go:52-77`, `internal/libencrypt/libencrypt.go:171-176`,
`internal/libencrypt/manager.go:117-127`, `internal/libencrypt/manager.go:344-355`,
`internal/libencrypt/manager.go:527-553`). Passphrase mode requires a non-empty passphrase and boots
Sealed, but any non-empty passphrase supplied for auto mode also creates a manual passphrase unwrap
path (`internal/libencrypt/manager.go:279-295`, `internal/libencrypt/manager.go:664-686`). The
disk-tree regression rejects the raw passphrase, derived key, DEK, and Recovery Key
(`internal/libencrypt/libencrypt_test.go:666-706`).

### Recovery Key

Provision and re-wrap each mint a random 32-byte, grouped Crockford-base32 credential with a
checksum. The operation returns its display value to the caller; only `DEKUnderRecovery` persists,
so there is no server-side copy or later retrieval path. The GraphQL provision response marks this
return value as acknowledgement-required (`internal/libencrypt/recoverykey.go:34-99`,
`internal/libencrypt/manager.go:304-343`, `internal/graph/graph_libencrypt.go:185-205`,
`internal/libencrypt/libencrypt_test.go:611-631`). The user-held value independently unwraps the
persisted DEK without a KEK or passphrase (`internal/libencrypt/manager.go:556-580`).

Provision and re-wrap commit their owner-bound key generation only after DB, audit/outbox, artifact,
and response-metadata preparation succeeds. The durable commit record is the final fallible response
step; cleanup is restart-recoverable and does not run on the response tail. One unavoidable window
remains: the process or connection can fail after that commit but before the single GraphQL response
delivers the newly minted Recovery Key. Because the server never escrows that key, retry reports the
generation as already provisioned or rotated instead of reproducing it. Treat that outcome as a lost
Recovery Key and perform an explicit re-wrap from another valid unseal path; if no other path exists,
restore the pre-operation encrypted backup under the previous custody material.

## Encrypted-library lifecycle and compromise response

1. Provisioning leaves the new DEK in memory and the library Unsealed. On restart, auto mode reads
   its KEK and unwraps; passphrase mode remains Sealed until the passphrase or Recovery Key is
   supplied (`internal/libencrypt/manager.go:373-378`, `internal/libencrypt/manager.go:664-686`).
2. Seal before containment work when live plaintext access must stop. Sealing removes the
   in-memory DEK but does not alter wrapped artifacts (`internal/libencrypt/manager.go:583-593`).
3. On suspected DEK, KEK, passphrase, or Recovery Key compromise, use
   `rewrapEncryptedLibrary` with the desired mode and a new passphrase where applicable. The
   operation requires the library to be Unsealed; it generates a fresh DEK, KEK (auto mode), and
   Recovery Key, rewrites every ciphertext class, and commits the new bundle crash-safely before
   retiring the old generation (`internal/libencrypt/manager.go:381-491`,
   `internal/graph/graph_libencrypt.go:285-365`).
4. Save the newly returned Recovery Key before acknowledging the result, then verify unseal and
   representative decrypt/restore paths with the new custody material. Preserve the incident record
   using the [incident runbook](incident.md).
5. Treat already-copied old ciphertext as still recoverable by anyone who held the old Recovery
   Key. Encrypted off-site backup uses a separate BackupKey and is not rotated by the library
   re-wrap; rotate that backup set separately (`internal/graph/graph_libencrypt.go:373-378`).

## Stream-token routine rotation

1. Generate a new key (`kid`) in the secure store.
2. Publish the new `kid` as active; keep the prior `kid` valid for a grace window so issued tokens
   verify during cutover.
3. After the grace window, retire the prior `kid`.

## Stream-token compromise response

1. Rotate the active `kid` immediately.
2. Expire the grace window early; revoke the prior `kid`.
3. Invalidate outstanding tokens signed by the compromised key; force re-issue.
4. File an incident ([incident](incident.md)).

## Stream-token targets

- Rotation cadence: every 12 months or immediately on suspected compromise.
- Grace-window length: expire immediately on compromise; otherwise keep the prior `kid` valid only
  for the shortest deployed-token lifetime, then revoke it.
