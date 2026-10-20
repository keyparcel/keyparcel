# keyparcel package v1

Use binary age encryption (`age-encryption.org/v1`) addressed to the intended ordinary SSH Ed25519 or RSA public keys. RSA keys must be at least 2048 bits. Recipient groups may mix both key types. Encrypt the complete frame below, including metadata and signature. One ciphertext may be copied to several recipient inboxes.

## Decrypted frame

| Field | Encoding |
| --- | --- |
| Marker | Eleven bytes: ASCII `KEYPARCEL`, byte `00`, byte `01` |
| Manifest length | Unsigned 32-bit big-endian byte count |
| Manifest | UTF-8 JSON bytes |
| Signature length | Unsigned 32-bit big-endian byte count |
| Signature | OpenSSH armored SSHSIG bytes |
| Content | Exactly the manifest's `size` bytes, followed by authenticated age end-of-stream |

The manifest and signature must both be nonempty; their combined length is at most 65,536 bytes, excluding the marker and length fields. Reject lengths before allocating. Inbox listing may read one or more complete authenticated age chunks to obtain metadata; it does not authenticate the remaining content. Receive checks the final age authentication, absence of extra content, exact size, and SHA-256 before delivery.

## Signed manifest

```json
{
  "version": 1,
  "transfer_id": "7e24bc508fd643eca137902db6e81a45",
  "sender": "SHA256:<sender fingerprint>",
  "recipients": ["SHA256:<recipient fingerprint>"],
  "type": "file",
  "filename": "notes.txt",
  "size": 5,
  "sha256": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
}
```

Fingerprints above are placeholders. Actual values are OpenSSH's `SHA256:` plus unpadded Base64 digest of the SSH wire-format public-key blob. Recipients must be nonempty and unique. `type` is `text` or `file`; text uses `filename: null` and valid UTF-8 content. File names are basenames, not paths; separators, NUL, empty names, `.` and `..` are rejected. Size is a nonnegative integer. Hashes are 64 lowercase hex characters.

Sign the exact stored manifest bytes with SSHSIG namespace `keyparcel/v1`. Verification uses a locally approved sender public key, never a key supplied by the package or an unapproved discovery entry. Do not reserialize JSON before verification. The transfer ID must match the remote filename, and the receiving device's fingerprint must be included in recipients. Unknown manifest fields and trailing JSON data are rejected.

The content hash is calculated before signing. New transfers use 16 cryptographically random bytes encoded as 32 lowercase hex characters. Device directory IDs use the SHA-256 of the same public-key blob as fingerprints, encoded as 64 lowercase hex characters.

## Remote names and publication

Relative to the configured root (default `.local/share/keyparcel` under the remote home):

```text
inbox/<device-directory-id>/<transfer-id>.<random-attempt>.part
inbox/<device-directory-id>/<transfer-id>.age
```

Write the exclusive temporary `.part` file, close it successfully, then use ordinary SFTP v3 rename to publish without replacing an existing completed file. Do not use overwrite-capable POSIX rename. Interrupted `.part` uploads are ignored by inbox listing. No persistent server cleanup job is required; abandoned partial files may require manual cleanup.

Human listings use unique prefixes, at least eight hex characters. Receive/delete use a valid full 32-character lowercase hex ID directly, without listing. Shorter prefixes are resolved against all correctly named completed files in the selected inbox, including rejected packages; ambiguity and missing matches are errors. Full IDs remain in JSON, manifests, and remote filenames. Short IDs provide convenience, not cryptographic identity.
