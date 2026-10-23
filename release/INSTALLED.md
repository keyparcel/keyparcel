# keyparcel: installed Linux clients

Share encrypted, signed text and files through an existing SSH account. Choose
`keyparcel-cli`, `keyparcel-gtk`, or both. They share user profiles and approvals
but neither package requires the other. The CLI is headless; GTK requires its
supported Linux desktop libraries. Build/source information is in `build-info.json`
beside this guide; `keyparcel --version` and `keyparcel-gtk --version` also show it.

For macOS CLI packages, see the [macOS guide](https://github.com/keyparcel/keyparcel/blob/main/release/MACOS.md).
For the Android APK, see [installation and enrollment](https://github.com/keyparcel/keyparcel/blob/main/docs/installation.md).

Distro names identify package build baselines. The static CLI archive can run on
other Linux x86_64 distributions with OpenSSH 8.7 or newer; it does not need GTK
or a particular glibc version. GTK archives need compatible shared libraries.

## Install or migrate

DEB packages support Ubuntu 24.04 and Debian 13 amd64; RPM packages support Fedora
44 x86_64. Use the package for your distro. For example, install **one or both**:

```sh
sudo apt install ./keyparcel-cli_*.deb
sudo apt install ./keyparcel-gtk_*.deb
# Fedora 44 instead:
sudo dnf install ./keyparcel-cli-*.rpm
sudo dnf install ./keyparcel-gtk-*.rpm
```

The older prereleases used a combined package named `keyparcel`. **Remove it
before installing the split packages** (`sudo apt remove keyparcel` or
`sudo dnf remove keyparcel`). The new packages conflict with that old name and do
not overwrite its owned files alongside it. A package manager may propose removing
the conflicting package; inspect its transaction rather than forcing installation.
Removing it preserves home-directory configuration, identities and saved files.

CLI installs `/usr/bin/keyparcel`, Bash/zsh completion, `keyparcel(1)`, and its own
`/usr/share/doc/keyparcel-cli` guide/notices. GTK independently installs
`/usr/bin/keyparcel-gtk`, the launcher/icon and `/usr/share/doc/keyparcel-gtk`.
Remove either with `sudo apt remove keyparcel-cli` / `keyparcel-gtk`, or
`sudo dnf remove keyparcel-cli` / `keyparcel-gtk`. The other client remains installed;
user files are not package-owned.

Ubuntu 24.04 initializes zsh completion globally. Fedora 44's default new-user
`~/.zshrc` initializes it; existing homes without completion setup may need
`autoload -Uz compinit; compinit` in `~/.zshrc`. Restart the shell after installation. The package uses the distro's
completion directory; archives install into `/usr/local/share/zsh/site-functions`.
See [shell completion](https://github.com/keyparcel/keyparcel/blob/main/docs/usage.md)
for user-local installation and `fpath` ordering. No startup files are edited.

## First use

Create/select a dedicated unencrypted SSH identity and enroll its public key on
your server, as described in [installation and enrollment](https://github.com/keyparcel/keyparcel/blob/main/docs/installation.md).
Setup selects an identity; it does not enroll it on the server.

**GTK only:** launch `keyparcel-gtk`, enter the SSH target and identity in Settings,
then select **Save & connect**. Use Devices to approve peers after checking their
fingerprints. No CLI executable is needed.

**CLI:** with an already enrolled `~/.ssh/keyparcel` and SSH alias `parcel-vps`:

```sh
keyparcel setup --host parcel-vps --identity ~/.ssh/keyparcel --default
keyparcel devices
keyparcel trust SHA256:FINGERPRINT --name laptop
keyparcel send --to laptop --text 'hello'
keyparcel inbox
keyparcel receive TRANSFER-ID --stdout
man keyparcel
```

Compare the server and peer fingerprints independently before approval. SSH key
comments are only suggested names. See [usage](https://github.com/keyparcel/keyparcel/blob/main/docs/usage.md),
the [GTK guide](https://github.com/keyparcel/keyparcel/blob/main/desktop/client/README.md)
and the [manual source](https://github.com/keyparcel/keyparcel/blob/main/docs/keyparcel.1).
Receiving verifies the whole parcel before delivery. CLI receive deletes the
remote copy after success unless `--keep` is used; GTK retains it until explicitly
removed. Verification/output failure retains it. Losing a private identity can make
existing transfers permanently unreadable; there is no recovery. Read the
[security model](https://github.com/keyparcel/keyparcel/blob/main/SECURITY.md).
CLI clipboard copying additionally needs `wl-copy` or `xclip` on Linux.

## Separate binary archives

Choose a **CLI or GTK** archive for `ubuntu24.04-amd64` (also Debian 13) or
`fedora44-x86_64`. GTK archives are not universally portable. CLI needs OpenSSH
and CA certificates; it does not need GTK. Install the relevant dependencies:

```sh
sudo apt install openssh-client ca-certificates
# Add only for GTK on Ubuntu 24.04 / Debian 13:
sudo apt install libgtk-4-1 adwaita-icon-theme librsvg2-common
# Fedora 44 alternatives:
sudo dnf install openssh-clients ca-certificates
sudo dnf install gtk4 adwaita-icon-theme librsvg2  # GTK only
```

Extract each chosen archive into an empty directory, substituting its actual
version and target. Run the matching block below from that extracted directory.
Do not mix an archive with a DEB/RPM installation of the same client: PATH would
select which copy runs. Remove an earlier archive using its original instructions
before replacing it; do not delete user settings or identities.

```sh
mkdir cli-candidate
tar -xzf keyparcel-cli-VERSION-TARGET.tar.gz -C cli-candidate
cd cli-candidate
sudo install -Dm755 usr/bin/keyparcel /usr/local/bin/keyparcel
sudo install -Dm644 usr/share/bash-completion/completions/keyparcel /usr/local/share/bash-completion/completions/keyparcel
sudo install -Dm644 usr/share/zsh/*/_keyparcel /usr/local/share/zsh/site-functions/_keyparcel
sudo install -Dm644 usr/share/man/man1/keyparcel.1.gz /usr/local/share/man/man1/keyparcel.1.gz
sudo install -d /usr/local/share/doc/keyparcel-cli
sudo install -m644 usr/share/doc/keyparcel-cli/* /usr/local/share/doc/keyparcel-cli/
```

For the GTK archive, from its own extracted directory:

```sh
sudo install -Dm755 usr/bin/keyparcel-gtk /usr/local/bin/keyparcel-gtk
sudo install -Dm644 usr/share/applications/io.github.keyparcel.keyparcel.desktop /usr/local/share/applications/io.github.keyparcel.keyparcel.desktop
sudo install -Dm644 usr/share/icons/hicolor/scalable/apps/io.github.keyparcel.keyparcel.svg /usr/local/share/icons/hicolor/scalable/apps/io.github.keyparcel.keyparcel.svg
sudo install -d /usr/local/share/doc/keyparcel-gtk
sudo install -m644 usr/share/doc/keyparcel-gtk/* /usr/local/share/doc/keyparcel-gtk/
```

Ensure `/usr/local/bin` is on your desktop/shell PATH. To remove **only the CLI**
archive installation made above:

```sh
sudo rm -- /usr/local/bin/keyparcel \
  /usr/local/share/bash-completion/completions/keyparcel \
  /usr/local/share/zsh/site-functions/_keyparcel \
  /usr/local/share/man/man1/keyparcel.1.gz
sudo rm -- /usr/local/share/doc/keyparcel-cli/{README.md,build-info.json,LICENSE,NOTICE,THIRD_PARTY_NOTICES.txt,copyright}
sudo rmdir /usr/local/share/doc/keyparcel-cli
```

To remove **only GTK** installed from its archive:

```sh
sudo rm -- /usr/local/bin/keyparcel-gtk \
  /usr/local/share/applications/io.github.keyparcel.keyparcel.desktop \
  /usr/local/share/icons/hicolor/scalable/apps/io.github.keyparcel.keyparcel.svg
sudo rm -- /usr/local/share/doc/keyparcel-gtk/{README.md,build-info.json,LICENSE,NOTICE,THIRD_PARTY_NOTICES.txt,copyright}
sudo rmdir /usr/local/share/doc/keyparcel-gtk
```

These commands preserve user files and the other client.

## License

Copyright 2026 keyparcel contributors. Project code is Apache-2.0; linked Go
components retain their BSD licenses. Full terms and attribution are in each
client's installed `LICENSE`, `NOTICE` and `THIRD_PARTY_NOTICES.txt` beside this guide.
