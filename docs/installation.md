# Installation and enrollment

[Home](../README.md) · [Usage](usage.md) · [Security](../SECURITY.md)

Download the [RC5 prerelease](https://github.com/keyparcel/keyparcel/releases/tag/v0.1.0-rc.5):

- **Linux amd64:** install `keyparcel-cli`, `keyparcel-gtk`, or both. DEBs support
  Ubuntu 24.04 and Debian 13; RPMs support Fedora 44. Separate binary archives
  are available for the same baselines.
- **Android ARM64:** install the signed APK on Android 8 or newer.

Verify downloads against `SHA256SUMS` from the same release. Follow the
[tagged installation guide](https://github.com/keyparcel/keyparcel/blob/v0.1.0-rc.5/release/INSTALLED.md)
for package/archive commands and removal of the older combined `keyparcel` package
before migration. Device-key enrollment is still required; see below.

Source builds are described in the client sections below. Local
[candidate builds](../release/README.md) keep Android APKs unsigned; the tag workflow
signs them and prepares a draft for manual publication. Automated ARM64 checks are
static, not runtime or upgrade assurance; see the [current platform status](../README.md#clients).

## Server and device keys

Use an SSH account with SFTP, a writable home/data directory and a readable list of
enrolled public keys (by default `.ssh/authorized_keys`). No server-side keyparcel
application is required. All participating clients must use the same remote root
and discovery paths; defaults are `.local/share/keyparcel` and `.ssh/authorized_keys`.

Each device needs its own ordinary Ed25519 or RSA identity (RSA at least 2048 bits).
The CLI and GTK accept readable, unencrypted private-key files; passphrase-protected
keys, certificates and hardware/agent-only identities are not supported. Create a
dedicated key rather than removing protection from an existing key:

```sh
ssh-keygen -t ed25519 -N '' -f ~/.ssh/keyparcel -C laptop
```

Have the account administrator add the **public** key, `~/.ssh/keyparcel.pub`, to
its authorized keys. Android generates its identity in the app; copy its public
key from Settings for enrollment. The GUI does not upload it automatically.

For the CLI/GTK, a dedicated SSH alias avoids accidentally selecting another key:

```sshconfig
Host parcel-vps
    HostName your-server.example
    User your-user
    IdentityFile ~/.ssh/keyparcel
    IdentitiesOnly yes
```

Replace the server/user values. Verify the server fingerprint through a trusted
source at first connection. Server approval and device approval are separate:
compare each peer's fingerprint independently and approve it on the receiving
and sending clients. A key comment is only a suggested name, never proof of identity.

## CLI

Use the Go version in [`go.mod`](../go.mod), Make and OpenSSH's `ssh`:

```sh
make build
./bin/keyparcel help
./bin/keyparcel setup --host parcel-vps --identity ~/.ssh/keyparcel --default
./bin/keyparcel devices
```

`make build` produces `bin/keyparcel`; put it on your `PATH` if desired. For clipboard
copying on Linux, install `wl-copy` for Wayland or `xclip` for X11. macOS uses its
system `osascript`/AppKit runtime. There is no ongoing macOS CI or full native
platform-parity claim. CLI commands prefer SSH command reads and require supported
Unix tools by default; use `setup --access-method sftp` for an SFTP-only server.
See [remote access methods](usage.md#remote-access-method) and [peer approval](usage.md#profiles-and-server-trust).

## Linux desktop

The GTK client uses the same profiles, identities, approvals and OpenSSH trust as
the CLI. Native builds need Linux, Go, a C compiler, pkg-config, GTK 4 development
headers/runtime and OpenSSH. From the repository root:

```sh
make desktop-build
desktop/client/build/keyparcel-gtk
```

See the [user guide](../desktop/client/README.md) and
[containerized development workflow](../desktop/client/DEVELOPMENT.md).
Automated desktop coverage uses X11. Candidate installs are checked on Ubuntu
24.04, Debian 13 and Fedora 44; see the [release guide](../release/README.md) for
baselines and runtime dependencies. Native Wayland is not validated.

## Android

The Compose app remembers one server and stores its generated identity encrypted
with Android Keystore. Source builds need Go, JDK 21 and the SDK/NDK components
listed in [Android development](../android/DEVELOPMENT.md). Current build scripts
target the x86_64 emulator:

```sh
make android-client-build
```

The separate [candidate build](../release/README.md) produces an unsigned ARM64
APK without running it.

This creates development debug APKs under `android/client/app/build/outputs/apk/`.
For an interactive emulator with a disposable server/CLI peer, follow the
[client development guide](../android/client/DEVELOPMENT.md). It also explains the
separate test and smoke variants; these are not signed release artifacts.
