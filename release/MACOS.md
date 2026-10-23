# keyparcel CLI on macOS

Download a `.pkg` from the [releases page](https://github.com/keyparcel/keyparcel/releases).
Choose `macos-arm64.pkg` for Apple Silicon or `macos-amd64.pkg` for Intel.
New package builds require macOS 13 or later. The published RC6 packages still
require macOS 26; their downloads have not been replaced.

## Install

Open the package and follow Installer's prompts. The package is **unsigned and
not notarized**. If macOS blocks it and you choose to proceed, open **System
Settings → Privacy & Security → Open Anyway** after the blocked attempt, then
confirm the prompt. See [Apple's instructions](https://support.apple.com/en-us/102445).

Installation requires administrator approval and puts the CLI in
`/usr/local/bin/keyparcel` for all users. Open a new terminal and run:

```sh
keyparcel --version
man keyparcel
```

If the command is not found, check that `/usr/local/bin` is on your `PATH`.
The manual is in `/usr/local/share/man/man1`; notices and build information are
in `/usr/local/share/doc/keyparcel-cli`.

Run keyparcel as your normal user, without `sudo`. Profiles and approvals stay
per-user in `~/Library/Application Support/keyparcel/config.json`; the private
identity stays at the path you select. Installing or upgrading does not enroll
keys on a server or replace your user configuration, keys or saved downloads.
Follow [installation and enrollment](https://github.com/keyparcel/keyparcel/blob/main/docs/installation.md)
to create/select and enroll a dedicated identity before connecting.

## Shell completion

The package installs zsh completion into
`/usr/local/share/zsh/site-functions/_keyparcel`. If your `~/.zshrc` does not
already initialize completion, add:

```zsh
autoload -Uz compinit
compinit
```

Open a new shell afterward. No startup file is edited by the installer.
For Bash, source `/usr/local/share/bash-completion/completions/keyparcel` from
your own shell configuration. See the
[CLI guide](https://github.com/keyparcel/keyparcel/blob/main/docs/usage.md#shell-completion)
for user-local completion and examples.

## Remove

A macOS package receipt is not an uninstaller. Remove only the installed files
below with administrator privileges, then run
`sudo pkgutil --forget io.github.keyparcel.keyparcel.cli`:

- `/usr/local/bin/keyparcel`
- `/usr/local/share/man/man1/keyparcel.1.gz`
- `/usr/local/share/bash-completion/completions/keyparcel`
- `/usr/local/share/zsh/site-functions/_keyparcel`
- The files in `/usr/local/share/doc/keyparcel-cli`

Keep your home directory, keys and configuration. Do not remove shared parent
directories such as `/usr/local/bin` or `/usr/local/share`.
