# Android client

Send text and files between Android and other keyparcel devices using an SSH/SFTP
account. The app remembers one server, approved devices and its generated identity.

[Installation](../../docs/installation.md) · [Development and emulator smoke](DEVELOPMENT.md) ·
[Security and identity loss](../../SECURITY.md)

## Connect and approve devices

1. Open Settings and copy the device's public key for enrollment on your server.
2. Enter the server address, port and username, then choose **Connect**. Storage
   and discovery paths are under **Advanced settings**.
3. Compare the displayed server fingerprint through a trusted source before
   **Approve & connect**. The app remembers that key for this address and port.
4. In Devices, compare the peer's fingerprint independently, edit its suggested
   name if needed, and **Approve**. Have the peer approve your fingerprint too.

The app never uploads its enrollment key automatically. A valid unused SSH key
comment is a naming suggestion, not trust. **This device** sends to your own inbox
without peer approval; the server still needs your public key enrolled.

Changed server keys block connection. Verify a replacement independently before
using **Forget server approval** and reconnecting. Forgetting preserves the device
identity and peer approvals. Changing the endpoint requires approval again;
canceling leaves saved settings unchanged.

Advanced settings offer **Remote access method**: Automatic, SSH commands or SFTP
only. Automatic probes once per connection and falls back when commands are
unsupported; SSH commands requires support. Edits stay in the form until you
connect. Uploads and deletion always use SFTP; all received content is verified
locally before delivery. See [remote access methods](../../docs/usage.md#remote-access-method).

## Send, receive and save

Upload and download bars show encrypted parcel bytes. For multiple recipients,
`1/3` identifies the current recipient; its percentage restarts for the next copy.
100% transferred does not confirm delivery: publication, verification and saving
continue with an indeterminate spinner until the existing result appears.

Send feedback shows a short transfer ID when copies share one ID. Open its
chevron for recipient outcomes, full IDs and Copy ID actions. Successful feedback
closes after five seconds; opening details pauses dismissal. Incomplete or
unconfirmed deliveries stay visible, with failed-only retry and editing controls.

Use Inbox, Send and Devices in the bottom navigation. Settings and Back preserve
in-progress drafts. Text, selected recipient and file survive navigation and picker
cancellation within the running app. Successful sends stay on Send, keep recipients
and clear only the sent text or file attachment. **Discard draft** clears recipients,
text and file. Host/approval changes clear affected recipient selections; drafts are
not saved across app restarts.

Use **Choose recipient…**, then **+ Add another** to select devices; each choice
is a removable token. Removing the last restores the chooser button; Cancel or
Back leaves the draft unchanged. Text and files share the same selection. **Send to N devices** sends the text/file to that selection once.
Partial results distinguish delivered, failed and unconfirmed copies. **Retry
failed recipients** retries only confirmed failures with the original bytes;
check unconfirmed recipients' inboxes before starting another send. **Edit draft**
keeps the content and clears the selection; it does not undo previous deliveries.

For files, **Send → File → Choose file** opens the system document picker. The
original display name is preserved; binary, empty and unknown-size documents are
supported without broad storage permission.

Pull down Inbox or Devices, or use the refresh icon beside either heading, to
refresh. Active refreshes show a native spinner and **Cancel** beside the heading
refresh icon; other operations show progress below the header.

Every Inbox parcel offers **View**, **Save file** and **Delete**. View accepts up to
16 MiB of verified plaintext, inclusive. Larger parcels have a disabled View action
and an explanation below the upload date; direct Inbox Save has no preview size cap. Verification
finishes before displaying content or writing a destination.

The viewer displays valid UTF-8 without NUL bytes, including text files. Unsupported
content opens a notice with Save file available. One parcel's original verified
bytes stay in memory until closed or its trust context changes. Viewer Save uses
those bytes without another download; **Back to parcel** restores the same viewer.
Inbox saves return **Back to Inbox**. Picker cancellation and save failure retain
the originating screen so you can retry.

**Save file** suggests the original filename, or `<transfer-id>.txt` for text.
Select an existing writable document and confirm the system overwrite prompt to
replace it; typing an existing name may instead create a numbered copy.

Use **Delete** on an Inbox item to remove this device's remote copy without
receiving or saving it. Confirm the deletion, or cancel to keep it. Other
recipients' copies and saved local files are untouched. A failed deletion keeps
the item available for retry.

Receiving or saving **keeps the remote transfer**. **Remove & close** removes it
explicitly; Back, picker cancellation, app closure and failed removal retain it.
Failures restore controls so you can retry. A failed provider write/close may leave
an empty or partial destination; the app reports this and does not automatically
delete a document you selected. See [CLI retention differences](../../docs/usage.md#receiving-and-retention).

## Text and clipboard

The read-only text preview supports scrolling/selection and shows up to 65,536
UTF-16 code units, without splitting Unicode characters. Larger messages have a
truncation notice. The complete remote copy remains available.

The copy action uses the **complete verified message**, not the preview. Clipboard
text must be UTF-8 without NUL bytes, at most 16 MiB inclusive; whitespace/newlines
are preserved. Rejection leaves the prior clipboard and remote transfer intact.
Android's smaller clipboard IPC capacity can still prevent a whole-message copy;
the app reports it and offers selection copying. Received content is marked
sensitive for Android's clipboard preview. There is no clipboard cap on transfers.

Public key and fingerprint copy actions preserve their exact values. Received
text is not kept as local history. Clearing app data or losing the identity can
make old transfers unreadable; see [identity loss](../../SECURITY.md#identity-loss).

## Automatic updates and limits

While foregrounded, Inbox and Devices update automatically over one reused
SSH/SFTP connection. Tabs display cached data; **Refresh** requests an immediate
update or retry. The top-bar symbol shows connected (green), connecting (yellow),
failed/lost (red), or unconfigured/suspended (neutral), with an accessible state.
It reports the known connection, not continuous reachability or inbox freshness.
Failed refreshes retain the last successful data. Reconnects enforce saved server trust.
Backgrounding closes the connection; returning reconnects without discarding drafts.
This is not a background transfer or notification service.

Network operations have a two-minute lifetime, with a shorter 30-second connection
setup limit. Document metadata, preparation and saving have separate two-minute
limits. Timeouts cancel work and retain incoming transfers. Large transfers need
private staging space. Multiple hosts, resumable transfers and identity recovery
are not implemented. Runtime validation currently uses x86_64 emulators;
ARM64 builds are checked statically. The tag workflow produces a signed draft APK
for manual device inspection and publication, not an automated ARM runtime test.
Settings shows the build version for bug reports.
