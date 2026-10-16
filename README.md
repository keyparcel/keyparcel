<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/branding/logo-dark.svg">
  <img src="assets/branding/logo-light.svg" alt="keyparcel" width="360">
</picture>

[![CI](https://github.com/keyparcel/keyparcel/actions/workflows/ci.yml/badge.svg)](https://github.com/keyparcel/keyparcel/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Share text and files between your devices through your own SSH server.

- **Your infrastructure:** an SSH account you control; no third-party relay or extra
  server software.
- **Across networks:** no shared LAN required, just an SSH server you can reach.
- **Explicit sharing:** choose what to send and who receives it. Your clipboard isn’t
  continuously monitored or synchronized.
- **Private parcels:** content and metadata, including original filenames, are encrypted
  before reaching the server.
- **Simple clients:** Android, Linux desktop and CLI.

## How it works

- Your devices connect to the **same user account** on your SSH server, using it as an
  inbox for end-to-end encrypted parcels.
- Each parcel contains text—such as clipboard content—or a single file.
- Parcels are stored under `~/.local/share/keyparcel` **on the server** by default.
- [age](https://age-encryption.org/) encrypts parcels for recipients’ SSH public keys;
  the sender’s SSH key signs them.
- Parcels appear on the server as `<random-id>.age`. The server can observe encrypted
  sizes, timing and recipient inboxes.
- Only the clients need keyparcel installed. The server needs ordinary SSH/SFTP.

## Quick start

With Go and Make installed, and a dedicated device key **already enrolled** on
an SSH/SFTP server reachable as `parcel-vps`:

```sh
make build
./bin/keyparcel setup --host parcel-vps --identity ~/.ssh/keyparcel --default
./bin/keyparcel send --to self --text 'Hello from keyparcel'
./bin/keyparcel inbox
./bin/keyparcel receive <transfer-id> --stdout
```

Replace `<transfer-id>` with the ID shown by `inbox`. `setup` saves local settings;
it does not enroll the key on your server. Check the server fingerprint when
connecting. To exchange with another device, enroll it too and approve each
other's fingerprints before sending. See [installation and enrollment](docs/installation.md).

GUI clients keep received transfers until you explicitly remove them. CLI receive
removes the recipient's remote copy after successful delivery unless you use
`--keep`. See [usage](docs/usage.md) for saving, copying and deletion behavior.

## Clients

| Client | What it does |
| --- | --- |
| [Android](android/client/README.md) | Text and files, system document pickers, device approval and automatic inbox updates while open |
| [Linux desktop](desktop/client/README.md) | GTK interface for text and files, shared CLI settings, device approval and automatic inbox updates |
| [CLI](docs/usage.md) | Scriptable send/receive, multiple hosts, clipboard copying, inbox watch and Bash completion |

The CLI is exercised on Linux; limited native Intel macOS validation also covers
its clipboard support. Android runtime tests use x86_64 emulators; ARM64 builds
are checked statically, with physical-device execution still deferred. Linux
DEB/RPM/archive and unsigned Android candidate builds are available from source;
signed releases and publication are not yet provided. This is development software.

## Documentation

- [Installation and enrollment](docs/installation.md)
- [CLI usage](docs/usage.md), [Android guide](android/client/README.md), [Linux desktop guide](desktop/client/README.md)
- [Linux screenshots](desktop/client/docs/gallery/README.md)
- [Security model and identity loss](SECURITY.md)
- [Development, tests and local smoke environments](docs/development.md)
- [Package format](PROTOCOL.md)

## License and contributions

Copyright 2026 keyparcel contributors. Licensed under [Apache-2.0](LICENSE),
with [third-party notices](THIRD_PARTY_NOTICES.txt) preserved.
See [Contributing](CONTRIBUTING.md) for development guidance.
