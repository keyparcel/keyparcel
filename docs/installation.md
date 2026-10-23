# Installation and enrollment

[Home](../README.md) · [Usage](usage.md) · [Security](../SECURITY.md)

## Requirements

- **CLI on Linux:** x86_64 and OpenSSH 8.7 or newer. The static binary does not
  depend on a particular glibc version or on GTK
- **CLI on macOS:** macOS 13 or later on Apple Silicon or Intel
- **GTK desktop:** Linux with GTK 4.10 or newer and OpenSSH 8.7 or newer. Current
  RC6 GTK binaries also need GLib 2.80 or newer; package managers resolve their
  shared-library dependencies
- **Android:** Android 8 or later on an ARM64 device

The distro names below describe package formats and build baselines, not a
restriction on the CLI archive. On another Linux x86_64 distribution, use its
OpenSSH and CA-certificate packages. GTK archives still need compatible shared
libraries from their build baseline.

## Downloads

Download the [current prerelease](https://github.com/keyparcel/keyparcel/releases/tag/v0.1.0-rc.6):

| Platform | Choose |
| --- | --- |
| Linux x86_64, DEB format | CLI and/or GTK DEBs, built on Ubuntu 24.04 |
| Linux x86_64, RPM format | CLI and/or GTK RPMs, built on Fedora 44 |
| macOS 26 or later, Apple Silicon | CLI package ending in `macos-arm64.pkg` |
| macOS 26 or later, Intel | CLI package ending in `macos-amd64.pkg` |
| Android 8 or later, ARM64 | Signed APK |

Download `SHA256SUMS` from the same release. On Linux,
`sha256sum --ignore-missing -c SHA256SUMS` checks the downloaded files in that
directory. On macOS, use `shasum -a 256 FILE` and compare the result with that
file's entry. Checksums detect changed bytes, not whether software is safe.

### Linux packages and archives

Remove the older combined `keyparcel` package first. Then install one or both
clients using the commands for your distro:

```sh
sudo apt install ./keyparcel-cli_*.deb
sudo apt install ./keyparcel-gtk_*.deb
# Fedora instead:
sudo dnf install ./keyparcel-cli-*.rpm
sudo dnf install ./keyparcel-gtk-*.rpm
```

The [installed-client guide](../release/INSTALLED.md) covers migration, separate
binary archives, dependencies and removal.

### macOS CLI

The published RC6 packages still require macOS 26 or later. New package builds
allow macOS 13 or later; this does not change the existing RC6 downloads.

Open the `.pkg` matching your processor and follow Installer's prompts. It installs
systemwide under `/usr/local` with administrator approval; run `keyparcel` as your
normal user afterward. The package is **unsigned and not notarized**. If macOS
blocks it and you choose to proceed, open **System Settings → Privacy & Security →
Open Anyway** after the blocked attempt, then confirm the prompt. See
[Apple's instructions](https://support.apple.com/en-us/102445).

Open a new terminal and run `keyparcel --version` and `man keyparcel`. Profiles
remain per-user at `~/Library/Application Support/keyparcel/config.json`;
installation does not enroll a device or share its private key with other users.
For optional zsh setup, installed paths and removal, see the
[macOS package guide](../release/MACOS.md).

### Android

Open the signed APK on your ARM64 device and allow installation from that download
source when Android asks. Updates use the same signing key and keep app data;
uninstalling or clearing app data removes the device identity. The app generates
its own key in Settings; enroll its public key as described below.

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

## Source builds

### CLI

Use the Go version in [`go.mod`](../go.mod), Make and OpenSSH's `ssh`:

```sh
make build
./bin/keyparcel help
./bin/keyparcel setup --host parcel-vps --identity ~/.ssh/keyparcel --default
./bin/keyparcel devices
```

`make build` produces `bin/keyparcel`; put it on your `PATH` if desired. For clipboard
copying on Linux, install `wl-copy` for Wayland or `xclip` for X11. macOS uses its
system clipboard tools. CLI commands prefer SSH command reads and require supported
Unix tools by default; use `setup --access-method sftp` for an SFTP-only server.
See [remote access methods](usage.md#remote-access-method) and [peer approval](usage.md#profiles-and-server-trust).

### Linux desktop

The GTK client uses the same profiles, identities, approvals and OpenSSH trust as
the CLI. Native builds need Linux, Go, a C compiler, pkg-config, GTK 4 development
headers/runtime and OpenSSH. From the repository root:

```sh
make desktop-build
desktop/client/build/keyparcel-gtk
```

See the [user guide](../desktop/client/README.md) and
[containerized development workflow](../desktop/client/DEVELOPMENT.md).
Packages target Ubuntu 24.04, Debian 13 and Fedora 44. See the
[installed-client guide](../release/INSTALLED.md) for runtime dependencies.

### Android app

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
