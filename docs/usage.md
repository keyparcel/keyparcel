# CLI usage

[Home](../README.md) · [Installation](installation.md) · [Security](../SECURITY.md)

Commands below assume `bin/` is on your `PATH` and the device key is enrolled.
Set up a host, list devices and approve a peer after comparing fingerprints:

```sh
keyparcel setup --host parcel-vps --identity ~/.ssh/keyparcel --default
keyparcel devices
keyparcel trust 'SHA256:the-peer-fingerprint' --name phone
keyparcel send --to phone --text 'hello'
printf 'hello\n\n' | keyparcel send --to phone --stdin
keyparcel send --to phone --file ./photo.jpg
keyparcel inbox
keyparcel receive <id> --stdout
keyparcel receive <id> --save-to ./Downloads/ --keep
keyparcel receive <id> --copy --keep
keyparcel delete <id>
keyparcel inbox --watch --interval 2s
```

The other device must approve your fingerprint too. `--to self` needs no peer
approval, but still requires the selected identity to be enrolled on the server.
`keyparcel help` lists all commands, including `bench` for server measurements.

## Profiles and server trust

Setup changes local configuration only. Compare the peer fingerprint through a
trusted channel before approval; discovery is not trust. Approvals are global across
configured hosts. Receivers must approve senders too. The built-in recipient `self`
uses the selected host's device key and needs no peer approval, including for
receiving its own signed packages. That key must still be enrolled on the server;
`self` is reserved as a local name.

Each saved target has its own identity, root, and discovery settings. `--host`
overrides the saved default for one invocation. All commands accept `--config PATH`;
remote commands also accept `--ssh-config PATH` to ask OpenSSH to use a specific
configuration file. On Linux, config defaults to
`$XDG_CONFIG_HOME/keyparcel/config.json`, or `~/.config/keyparcel/config.json`.
On macOS, it defaults to `~/Library/Application Support/keyparcel/config.json`.

OpenSSH handles aliases, routing, agents, and host-key verification. keyparcel checks
effective identity configuration before connecting and rejects other eligible
configured identity files. A dedicated SSH alias is useful if your ordinary alias
offers several keys. Password fallback and unrelated multiplexed connections are
disabled. Jump-host authentication remains configured separately by OpenSSH.

Host-key confirmation and SSH diagnostics use the controlling terminal for
interactive commands. Piped input such as `send --stdin` remains transfer data,
and redirected output such as `receive --stdout` or `inbox --json` stays separate
from the prompt. Background jobs and commands without a usable foreground
terminal remain noninteractive; graphical askpass helpers are disabled.

Clients create remote directories as needed, using the account's permissions and
server umask. Custom `setup --root PATH --discovery PATH` settings must reference the
same storage and enrolled-key list on all participating clients.

## Receiving and retention

The GTK and Android GUIs keep received transfers until **Remove & close** is
chosen; Back and closing the result keep them. The following receive defaults
apply to the CLI.

Listing verifies signed metadata, not the complete content. Receive fully verifies
content in temporary storage before output. Successful receive deletes the
recipient's remote copy unless `--keep` is used. Failed delivery retains it; failed
deletion after delivery reports both outcomes and exits unsuccessfully. Receive and
delete use full 32-character transfer IDs directly. Short IDs (at least eight
characters) must uniquely match a completed package in the current inbox; JSON
retains full IDs.

`--save-to PATH` accepts an existing directory or an exact output filename for both
file and text transfers. Directory saves use the original filename for files and
`<full-transfer-id>.txt` for text. An existing directory works with or without a
trailing slash; a trailing slash requires an existing directory. Parent directories
must exist. Save refuses overwrites by default and suggests `--overwrite` when a
regular output file already exists. `receive --save-to PATH --overwrite` replaces
that resolved output file only after verification, whether PATH names a directory or
an exact filename. Existing symlinks and other nonregular output entries are
rejected. `--overwrite` requires `--save-to`.

## Inbox and deletion

Inbox entries are sorted newest first using the server-reported modification time of
the encrypted package, then by full ID ascending for equal timestamps. The `UPLOADED`
column displays local time with seconds and a numeric UTC offset (for example
`2026-10-11 14:32:05 +02:00`); JSON uses `uploaded_at` in UTC RFC 3339. This time is
an approximate upload time from the server's clock, not signed or verified package
metadata.

`delete ID1 ID2 ...` accepts full IDs or unique prefixes. Each transfer is deleted
once, even if selected by both its full ID and a prefix. Invalid, missing, ambiguous,
or failed entries are reported while other valid deletions proceed; any failure gives
a nonzero exit status. `delete --all` removes all completed transfers in the selected
device's inbox on the selected host, including corrupted and unapproved packages,
without confirmation. It cannot be combined with IDs. Both modes use one inbox
listing, report the number deleted, and leave later arrivals, incomplete uploads, and
unrelated files untouched. Incomplete-upload cleanup is deferred.

To receive a retained transfer again under another filename, use:

```sh
keyparcel receive <id> --save-to ~/Downloads/myfile.log
```

## Clipboard

`--copy` accepts fully verified UTF-8 content without NUL bytes, including transfers
sent with `--file`, and preserves whitespace and line breaks. Clipboard content is
limited to 16 MiB (16,777,216 bytes), inclusive, with no override. Ineligible or
oversized content leaves the clipboard unchanged and the remote package retained; use
`--save-to` or `--stdout` instead. This clipboard limit does not restrict `--text`,
`--stdin`, `--stdout`, or file transfers. On macOS the CLI writes an explicit UTF-8
plain-text pasteboard type through the system `osascript` bridge; on Linux it uses
`wl-copy` or `xclip` according to the desktop session. Rich clipboard formats are
deferred.

## Bash completion

Source the supplied script in Bash (add the line to `~/.bashrc` to load it in new
shells):

```sh
source /path/to/keyparcel/completions/keyparcel.bash
```

Completion suggests commands, flags, local peer names, `self`, configured hosts, and
file paths. `trust` queries current discovery for supported, unapproved keys
excluding self; after a fingerprint, successive completions suggest `--name` and a
valid unused remote comment. Remote comments are naming hints, not identity
verification. `receive` and `delete` suggest full incoming IDs from parcel filenames
without reading or verifying their contents. A suggestion can be invalid or from an
unapproved sender; actual receive still fully verifies it before delivery. Remote
completion has a five-second query deadline, does not prompt, and returns no
suggestions when discovery or SSH fails. It uses no cache.
