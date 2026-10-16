# keyparcel: installed clients

Send encrypted, signed text and files through an existing SSH/SFTP account. The
CLI and Linux GTK app share profiles and peer approvals. You need a supported
Linux x86_64 runtime; these GTK binaries are not universally portable.

## First use

Run `keyparcel --version` or `keyparcel-gtk --version` to identify this build.
`build-info.json` beside this guide records its source revision and build target.

Create/select a dedicated unencrypted SSH identity and enroll its public key on
your SSH server, as described in [installation and enrollment](https://github.com/keyparcel/keyparcel/blob/main/docs/installation.md).
Setup selects an identity; it does not enroll it on the server. For example, with
an already enrolled `~/.ssh/keyparcel` and your SSH alias `parcel-vps`:

```sh
keyparcel setup --host parcel-vps --identity ~/.ssh/keyparcel --default
keyparcel devices
keyparcel-gtk
```

Check the server fingerprint independently before accepting it. Compare each
peer's fingerprint independently, then approve it in Devices or with
`keyparcel trust SHA256:FINGERPRINT --name laptop`. SSH key comments are only
suggested names. See [usage](https://github.com/keyparcel/keyparcel/blob/main/docs/usage.md)
and the [GTK guide](https://github.com/keyparcel/keyparcel/blob/main/desktop/client/README.md).

```sh
keyparcel send --to laptop --text 'hello'
keyparcel inbox
keyparcel receive TRANSFER-ID --stdout
```

Receive verifies the whole package before delivery. CLI receive deletes the
remote transfer after success unless `--keep` is used; the GUI keeps it until
Remove is selected. Verification/output failure retains the remote package.
Loss of the private identity can make existing transfers unreadable; there is no
identity recovery. Read the [security model](https://github.com/keyparcel/keyparcel/blob/main/SECURITY.md).
Linux clipboard copying additionally needs `wl-copy` or `xclip`.

## DEB/RPM packages

Install with `sudo apt install ./keyparcel_*.deb` on Ubuntu 24.04/Debian 13, or
`sudo dnf install ./keyparcel-*.rpm` on Fedora 44. Use the candidate for that
distro. The package manager resolves runtime dependencies and installs both
executables, the launcher and Bash completion under `/usr`.

Remove with `sudo apt remove keyparcel` or `sudo dnf remove keyparcel`. User
identities/configuration are not package-owned and remain in your home directory.

## Binary archive

Use the archive matching its named baseline: `ubuntu24.04-amd64` also supports
Debian 13; `fedora44-x86_64` is for Fedora 44. Install runtime dependencies first:

```sh
# Ubuntu 24.04 / Debian 13:
sudo apt install openssh-client ca-certificates libgtk-4-1 adwaita-icon-theme
# Fedora 44 instead:
sudo dnf install openssh-clients ca-certificates gtk4 adwaita-icon-theme
```

Extract your chosen archive into an empty directory. For example, substitute the
actual version/target filename below, then install under `/usr/local`:

```sh
mkdir keyparcel-candidate
tar -xzf keyparcel-VERSION-TARGET.tar.gz -C keyparcel-candidate
cd keyparcel-candidate
sudo install -Dm755 usr/bin/keyparcel /usr/local/bin/keyparcel
sudo install -Dm755 usr/bin/keyparcel-gtk /usr/local/bin/keyparcel-gtk
sudo install -Dm644 usr/share/applications/keyparcel.desktop /usr/local/share/applications/keyparcel.desktop
sudo install -Dm644 usr/share/bash-completion/completions/keyparcel /usr/local/share/bash-completion/completions/keyparcel
sudo install -d /usr/local/share/doc/keyparcel
sudo install -m644 usr/share/doc/keyparcel/* /usr/local/share/doc/keyparcel/
```

The launcher uses the existing Adwaita `mail-send-symbolic` icon. Ensure
`/usr/local/bin` is on your normal desktop/shell PATH. Avoid installing an archive
alongside the DEB/RPM: the earlier PATH entry would select which version runs.

To remove an archive installation made with those commands:

```sh
sudo rm -- /usr/local/bin/keyparcel /usr/local/bin/keyparcel-gtk \
  /usr/local/share/applications/keyparcel.desktop \
  /usr/local/share/bash-completion/completions/keyparcel \
  /usr/local/share/doc/keyparcel/README.md /usr/local/share/doc/keyparcel/build-info.json \
  /usr/local/share/doc/keyparcel/LICENSE /usr/local/share/doc/keyparcel/NOTICE \
  /usr/local/share/doc/keyparcel/THIRD_PARTY_NOTICES.txt /usr/local/share/doc/keyparcel/copyright
sudo rmdir /usr/local/share/doc/keyparcel
```

These commands do not remove user identities/settings. Signed releases and
publication are separate from these unsigned/unpublished candidate builds.

## License

Copyright 2026 keyparcel contributors. Project code is Apache-2.0; linked Go
components retain their BSD licenses. Full terms and attribution are in the
installed `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.txt` files beside this guide.
