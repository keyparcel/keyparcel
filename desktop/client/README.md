# Linux GTK client

Send and receive text or files with a GTK interface. The client shares the CLI's
profiles, device identity files, peer approvals and OpenSSH `known_hosts` stores.

[Installation](../../docs/installation.md#linux-desktop) · [Builds and tests](DEVELOPMENT.md) ·
[Security](../../SECURITY.md) · [Screenshots](docs/gallery/README.md)

## Setup and trust

Launch `desktop/client/build/keyparcel-gtk` after [building](DEVELOPMENT.md), or
open it against an existing CLI profile. `--config`, `--ssh-config` and `--host`
select alternate settings. In Settings, select a saved keyparcel profile or enter an SSH target and an
existing unencrypted Ed25519 or supported RSA identity. Selecting a saved profile
only fills the form; the active connection and drafts remain unchanged until
**Save & connect**. **Browse…** accepts any
private-key filename. **Save & connect** saves the shared CLI profile and connects;
failed connections keep the settings and show an error. Advanced settings contain
remote paths and OpenSSH details.

Open **Show enrollment details** to validate the current identity locally and copy
its public key for server enrollment. A matching adjacent `.pub` file supplies its
comment; the private key determines the key and fingerprint. Editing the identity
or choosing another saved profile clears these details. No key is generated.

On first connection, compare the server fingerprint independently before approving
the graphical OpenSSH prompt. Rejection changes no trust; changed keys block
connection. OpenSSH continues to handle aliases, routing and jump hosts.

In Devices, compare the peer fingerprint, edit the suggested name if needed and
**Approve**. A valid unused SSH comment is just a name suggestion. The receiving
peer must approve your fingerprint too. **This device** needs no peer approval,
but its identity must still be enrolled on the server.

Advanced settings offer **Remote access method**: Automatic, SSH commands or SFTP
only. Automatic probes once per connection and falls back when commands are
unsupported; SSH commands requires support. Edits stay in the form until you
connect. Uploads and deletion always use SFTP; all received content is verified
locally before delivery. See [remote access methods](../../docs/usage.md#remote-access-method).

## Send and receive

Send feedback shows a short transfer ID when copies share one ID. Open its
chevron for recipient outcomes, full IDs and Copy ID actions. Successful feedback
closes after five seconds; opening details pauses dismissal. Incomplete or
unconfirmed deliveries stay visible, with failed-only retry and editing controls.

Inbox, Send and Devices are available directly in the navigation bar. Settings
opens separately; Back, Escape or Alt+Left returns to the prior page. Dialogs and
expanded Advanced sections are dismissed first.

Send text or one local file to an approved peer or this device. Use **Choose recipient…** to select a device, then **+ Add another**
to include more devices. Each selection is a removable token; removing the last
restores the chooser button. Closing the chooser leaves the draft unchanged. **Send to N devices**
sends to that selection once. Partial results identify delivered, failed and
unconfirmed copies. **Retry failed recipients** sends only to confirmed failures,
using the original message/file bytes. Unconfirmed deliveries require checking
the recipient inbox before starting another send. **Edit draft** clears the
selection while keeping the content; previous deliveries are not undone.

Draft text,
recipient and file survive navigation, dialog cancellation and failed sends.
Successful sends stay on Send, keep recipients and clear only the sent text or
file attachment. **Discard draft** clears recipients, text and file. Server/identity or approval changes clear affected recipient selections
without discarding content. Drafts do not survive app restarts.

Use the refresh icon beside **Inbox** or **Devices** for an immediate update.

Inbox shows sender, filename/type, size and local upload time. **Details** exposes
the full transfer ID and its copy action. Content is fully verified before display
or a destination write. Every parcel offers **View**, **Save file** and **Delete**.
View accepts up to 16 MiB of verified plaintext, inclusive. Larger parcels have a
disabled View action with an explanation; Save file has no preview size cap.

The viewer shows valid UTF-8 without NUL bytes, including text files. Other content
opens an unsupported-text notice with Save file available. It retains one parcel's
original verified bytes in memory until closed or its trust context changes.
Saving from the viewer uses those bytes without another download. The saved result's
**Back to parcel** restores the same viewer; Inbox saves return **Back to Inbox**.
Canceling or failing a save keeps the originating screen available for retry.

GTK's save dialog suggests the original filename for files, or `<transfer-id>.txt`
for text. Replacing an existing regular file requires confirmation.

Use **Delete** on an Inbox item to remove this device's remote copy without
receiving or saving it. Confirm the deletion, or cancel to keep it. Other
recipients' copies and saved local files are untouched. A failed deletion keeps
the item available for retry. **Remove & close** after receiving/saving still
works as before.

Receiving or saving **keeps the remote copy**. **Remove & close** deletes it
explicitly; Back, window closure, cancellation and failed delivery retain it.
Failed removal leaves the result and retry action available. The
[CLI removes successful receives by default](../../docs/usage.md#receiving-and-retention).

The read-only text preview is selectable and scrollable, with a 65,536 Unicode
character limit and visible truncation notice. The **Copy complete text** icon
above the preview uses the full verified message, not the preview, up to 16 MiB inclusive. Clipboard text must
be UTF-8 without NUL bytes. Transfers and direct Inbox saves have no preview or
clipboard size cap. Saving always uses the complete original bytes, even when the
preview is truncated or unsupported.

## Automatic updates and cancellation

Inbox and Devices update automatically over one reused SSH/SFTP connection. Tabs
show cached data; Refresh requests an immediate update/retry. A failed refresh
keeps the last successful data. The top-bar connection symbol is green when
connected, yellow while connecting, red after failure/loss, and neutral before
configuration. Its accessible state accompanies color; it is not a reachability
probe or an inbox-freshness label. Action errors stay with their screen, while
brief confirmations appear at the bottom.
Reconnections preserve server-key checks and never open approval dialogs in the
background: use **Save & connect** in Settings when approval is needed. Server/trust changes
invalidate affected cached results. Monitoring stops when the app exits.

Network/file operations have a two-minute deadline and a Cancel action; a timeout
or cancellation cleans staging and restores controls. File selection currently
uses local paths. Candidate installs are checked on Ubuntu 24.04, Debian 13 and
Fedora 44; native Wayland and other distributions remain unverified; background transfers and notifications are not implemented.
