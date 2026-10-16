# Security model

[Home](README.md) · [Enrollment](docs/installation.md) · [Package format](PROTOCOL.md)

keyparcel is development software. It has not undergone an independent security
audit and does not claim production readiness.

## Trust and verification

Approve the SSH server's fingerprint through a trusted source before connecting.
Approve device fingerprints independently too: discovery lists enrolled public
keys, but does not establish trust. Names and SSH comments are convenience labels.
Receivers must approve senders as well as senders approving recipients.

Packages use age encryption and OpenSSH SSHSIG signatures. Inbox listing verifies
signed metadata; receiving verifies the entire content in temporary storage before
displaying, copying or writing it to a destination. A failed delivery retains the
remote package. See [usage](docs/usage.md#receiving-and-retention) for the different
GUI and CLI removal policies.

## Server and local storage

The server sees ciphertext, public device keys, routing, timing, sizes and opaque
IDs. It can block, delete or replay authentic packages. There is no forward secrecy
or replay ledger. Enrolled keys sharing an SSH account can modify its shared
storage; encryption/signatures protect confidentiality and authenticity, not
availability or account-level isolation. The SFTP dependency has not been audited
here against arbitrary malicious protocol replies.

CLI/GTK identities are software private-key files. Android encrypts its software
identity using a Keystore key in app-private, backup-excluded storage; it must
decrypt that identity for use. Runtime/JNI copies are subject to garbage collection.
This does not establish hardware-backed key protection on physical devices.

Verified plaintext can exist in private staging files, saved destinations,
application memory and the system clipboard. Destination providers on Android do
not guarantee atomic writes: a failed save may leave an empty or partial document,
while the remote transfer remains available. The app does not automatically delete
a chosen destination, which may have been an existing file.

## Identity loss

Losing a device key, clearing Android app data or losing its Keystore key can make
old transfers unreadable. A replacement identity cannot decrypt packages addressed
to the old one. There is no built-in identity recovery: enroll the replacement and
update peer approvals; ask senders to send the data again if needed.

Use dedicated identities and evaluation data. Never include private keys, decrypted
content or sensitive test artifacts in public bug reports.
