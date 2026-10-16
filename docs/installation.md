# Installation and enrollment

[Home](../README.md) · [Usage](usage.md) · [Security](../SECURITY.md)

Build from source, or build local [Linux and Android candidates](../release/README.md).
Linux DEB/RPM packages and archives have clean-install tests; production ARM64
Android APKs are unsigned and checked statically. Signed/published releases are
not yet provided. Physical ARM64 execution remains deferred.

## Server and device keys

Use an SSH/SFTP account with a writable home/data directory and a readable list of
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
platform-parity claim. [CLI usage](usage.md) covers profiles and peer approval.

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
target the x86_64 emulator; the separate [candidate build](../release/README.md)
produces an unsigned ARM64 APK without running it:


```sh
make android-client-build
```

This creates development debug APKs under `android/client/app/build/outputs/apk/`.
For an interactive emulator with a disposable server/CLI peer, follow the
[client development guide](../android/client/DEVELOPMENT.md). It also explains the
separate test and smoke variants; these are not signed release artifacts.
