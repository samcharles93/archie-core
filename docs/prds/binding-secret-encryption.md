# Binding secret at-rest encryption -- decision

**Status:** Decided, to be implemented
**Date:** 2026-09-02
**Beads issue:** `archie-core-t2db.7` (P2), parent `archie-core-t2db`

## Problem

`binding.Binding.Secret` (the per-source HMAC shared secret) is persisted as
plaintext in the `bindings.secret TEXT` column. Anyone with read access to the
SQLite file -- the same access level as `captured_events.body` and `tasks.body`
-- sees sender-side shared secrets. A leaked database file leaks secrets even
when the filesystem ACLs were correct at rest.

## Decision

Encrypt the `secret` column at rest with **AES-256-GCM** (stdlib
`crypto/aes` + `crypto/cipher`, no new dependency), transparently inside the
store: encrypt on write, decrypt on read. The store is the data boundary; its
consumers (dispatch loop, HMAC verification) receive the plaintext secret
exactly as today, and the webui already blanks `Secret` on every response.

### Cipher parameters

- **AEAD:** AES-256-GCM, 12-byte random nonce (`crypto/rand`), tag appended.
- **Key length:** 32 bytes, derived as `SHA-256(resolved_material)`. Kept as
  SHA-256 (no Argon2id) because the source is a **32-byte high-entropy** value
  resolved through the secret registry, so re-deriving to 32 bytes merely
  re-shapes entropy rather than defending a weak passphrase.
- **AAD:** a static domain separator, `[]byte("arcie-binding-secret")`, so a
  ciphertext cannot be relocated to a different field/column and still
  authenticate. AAD is intentionally **not** row-bound: binding secrets are
  random HMAC keys and are never used as lookup keys, so cross-row swap is not
  a meaningful attack here.

### Envelope format

```
arcie-binding:v1:<keyFingerprint>:<base64url(nonce || ciphertext || tag)>
```

- `arcie-binding` -- domain marker
- `v1` -- format version (future cipher/KDF change is a new version, old rows
  stay readable)
- `<keyFingerprint>` -- `hex(SHA-256(material))[:16]`, the identity of the key
  that sealed the row, enabling rotation without a one-shot rewrite
- payload -- `base64url` of `nonce ‖ GCM.Seal(plaintext, aad)`

### Key source (confirmed with maintainer)

Resolved through the existing secret registry (`internal/secret`), the
established `SecretRef` pattern already used for `forge.webhook_secret`:

```toml
[bindings]
encryption_key               = { engine = "env", key = "ARCHIE_BINDING_KEY" }
# Rotation: after swapping encryption_key to a new value, retain the previous
# material so rows sealed under the old key still decrypt.
previous_encryption_keys     = [ { engine = "env", key = "ARCHIE_BINDING_KEY_OLD" } ]
```

The active key encrypts new writes; `previous_encryption_keys` are retained for
decrypt-only. Key identity is intrinsic to the material (the fingerprint), so
no manual key IDs are configured.

### Rotation story

Rotate by changing `encryption_key`, moving the old material into
`previous_encryption_keys`. Rows sealed under the old key still decrypt via
their embedded fingerprint; new writes use the new active key. Once no
remaining row references an old fingerprint, remove it from the config. Losing
a fingerprint's material before all its rows are rewritten makes those rows
unreadable -- the same operational rule as any key rotation, and the reason the
old material must be retained.

## Non-goals / deferred

- **`captured_events.body` redaction upgrade** is deferred (confirmed with
  maintainer). The body redaction is a separate best-effort key-name heuristic
  with its own documented limits and its own capture-path surface; it is not
  bundled into this store-scoped change.
- **Key re-derivation on config reload** is a startup concern: the store holds
  the cipher for its lifetime, so a reload that changes `[bindings]` keys
  requires a daemon restart to take effect. Documented, accepted.

## Wiring

- `internal/config`: add `BindingsConfig` (`encryption_key SecretRef`,
  `previous_encryption_keys []SecretRef`) on `Config.Bindings`.
- `internal/app/archied/bootstrap.go` `openStores`: resolve both refs through
  the already-built `secrets` registry, pass the material to `store.Open` via a
  new option `store.WithBindingCipher(cipher)`.
- `internal/store`: add a `BindingCipher` interface (concrete AES-GCM impl in
  `binding_cipher.go`), a `bindingCipher` field on `Store`, and the `Open`
  option. **Nil cipher = plaintext**, so existing call sites and tests change
  nothing and legacy deployments keep current behaviour until they set a key.

## Back-compat & tests

- `store.Open` gains the option variadically; all current call sites compile
  unchanged.
- Existing plaintext rows are unaffected until a cipher is configured; rows
  written before a cipher is installed remain plaintext and read as plaintext.
- Tests: cipher round-trip (Encrypt/Decrypt), wrong-key and tampered-ciphertext
  integrity failure, envelope parse errors, and a store round-trip that asserts
  the persisted column is NOT the plaintext while `GetBinding` returns it.
