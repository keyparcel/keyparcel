# keyparcel

[![CI](https://github.com/keyparcel/keyparcel/actions/workflows/ci.yml/badge.svg)](https://github.com/keyparcel/keyparcel/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Send text and files between your devices through an SSH account you already
control. keyparcel encrypts transfers for the receiving devices and signs them
with the sender's key. Your server stores the encrypted packages for later
retrieval; it needs SSH/SFTP, not a keyparcel service.

Use it to move a message, command, photo or document between a phone and a Linux
computer without setting up another messaging service. Devices need not be online
at the same time.

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
