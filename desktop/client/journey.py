"""One real GTK/SSH journey; no mocks, private desktop and disposable server."""
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import gi
gi.require_version("Atspi", "2.0")
gi.require_version("Gdk", "3.0")
from gi.repository import Atspi, Gdk, GLib
from fixture import Fixture

session = Path(sys.argv[1]).resolve()
artifacts = session
assert os.environ.get("KEYPARCEL_DESKTOP_TEST_SESSION") == str(session), "Use test.sh"
Atspi.set_timeout(1000, 2000)
Gdk.init([])

def wait(description, condition, seconds=15):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        while GLib.MainContext.default().pending():
            GLib.MainContext.default().iteration(False)
        try:
            value = condition()
        except GLib.Error:
            value = None  # GTK can replace an accessible node during a query.
        if value:
            return value
        time.sleep(0.05)
    raise AssertionError(f"Timed out: {description}")


def walk(node):
    yield node
    try:
        count = node.get_child_count()
        for index in range(count):
            child = node.get_child_at_index(index)
            if child:
                yield from walk(child)
    except GLib.Error:
        return  # Dialogs may disappear while their tree is being read.


def find(root, name, role=None):
    for node in walk(root):
        if node.get_name() == name and (role is None or node.get_role() == role):
            if node.get_state_set().contains(Atspi.StateType.SHOWING):
                return node


def control(root, name, role=None):
    return wait(f"visible control {name!r}", lambda: find(root, name, role))


def file_dialog(title):
    dialog = control(Atspi.get_desktop(0), title)
    # GTK versions differ in reporting ACTIVE; focus the real native window.
    window = subprocess.check_output(["xdotool", "search", "--onlyvisible", "--pid", str(dialog.get_process_id()), "--name", "^" + re.escape(title) + "$"], timeout=5).decode().splitlines()[-1]
    subprocess.check_call(["xdotool", "windowactivate", "--sync", window], timeout=5)
    wait(f"active dialog {title!r}", lambda: subprocess.check_output(["xdotool", "getactivewindow"], timeout=5).decode().strip() == window)
    return dialog


def editable(root):
    def field():
        for node in walk(root):
            states = node.get_state_set()
            if node.get_role() == Atspi.Role.TEXT and states.contains(Atspi.StateType.SHOWING):
                if states.contains(Atspi.StateType.FOCUSED):
                    if node.get_editable_text_iface() is not None:
                        return node
    return wait(f"focused editable field in {root.get_name()!r}", field)


def filename_field(dialog):
    return wait("save filename entry", lambda: find(dialog, "Name:", Atspi.Role.TEXT) or find(dialog, "Name", Atspi.Role.TEXT))


def text_contents(node):
    return Atspi.Text.get_text(node, 0, Atspi.Text.get_character_count(node))


def activate(node):
    wait(f"enabled control {node.get_name()!r}", lambda: node.get_state_set().contains(Atspi.StateType.SENSITIVE))
    action = node.get_action_iface()
    assert action is not None, f"No accessible action on {node.get_name()!r}"
    # Busy actions can disable their own control before AT-SPI returns.
    # Each caller checks the resulting UI/remote state instead of that boolean.
    action.do_action(0)


def keys(*names):
    # This uses the accessibility registry's X11 input synthesizer.
    values = []
    for name in names:
        found, entries = Gdk.Keymap.get_for_display(Gdk.Display.get_default()).get_entries_for_keyval(Gdk.keyval_from_name(name))
        assert found, f"No keyboard mapping for {name!r}"
        values.append(entries[0].keycode)
    for value in values[:-1]:
        assert Atspi.generate_keyboard_event(value, None, Atspi.KeySynthType.PRESS)
    assert Atspi.generate_keyboard_event(values[-1], None, Atspi.KeySynthType.PRESSRELEASE)
    for value in reversed(values[:-1]):
        assert Atspi.generate_keyboard_event(value, None, Atspi.KeySynthType.RELEASE)


def screenshot(name):
    root = Gdk.get_default_root_window()
    image = Gdk.pixbuf_get_from_window(root, 0, 0, root.get_width(), root.get_height())
    assert image is not None, "Cannot capture the isolated desktop"
    image.savev(str(artifacts / name), "png", [], [])


fixture = Fixture(session / "fixture")
process = None
app = None
logs = []

def start():
    global process, app
    log = (session / f"app-{len(logs)}.log").open("w")
    logs.append(log)
    process = subprocess.Popen([("/usr/bin/keyparcel-gtk" if os.environ.get("KEYPARCEL_INSTALLED_IMAGE") else "./build/keyparcel-gtk"), "--config", str(fixture.directory / "desktop/config.json"), "--ssh-config", str(fixture.directory / "desktop/ssh_config"), "--request-timeout", "15s"], stdout=log, stderr=log)
    def application():
        assert process.poll() is None, "GTK client exited; inspect app log"
        for node in walk(Atspi.get_desktop(0)):
            if node.get_role() == Atspi.Role.APPLICATION and node.get_process_id() == process.pid:
                return node
    app = wait("GTK client accessibility tree", application)
    control(app, "keyparcel", Atspi.Role.FRAME)
    # Wait until the startup worker has restored the navigation controls.
    wait("initial settings load", lambda: control(app, "Settings").get_state_set().contains(Atspi.StateType.SENSITIVE))


def click(name, root=None):
    activate(wait(f"visible button {name!r}", lambda: find(root or app, name, Atspi.Role.PUSH_BUTTON) or find(root or app, name, Atspi.Role.TOGGLE_BUTTON)))


def fill(name, value, root=None):
    node = control(root or app, name, Atspi.Role.TEXT)
    assert node.get_editable_text_iface().set_text_contents(value), f"Cannot edit {name}"


def select_recipient(name):
    recipient = control(app, "Recipient", Atspi.Role.COMBO_BOX)
    if find(recipient, name, Atspi.Role.LABEL):
        return
    activate(next(n for n in walk(recipient) if n.get_role() == Atspi.Role.TOGGLE_BUTTON))
    popup = wait("recipient choices open", lambda: next((n for n in walk(recipient) if n.get_role() == Atspi.Role.LIST_BOX and n.get_state_set().contains(Atspi.StateType.SHOWING)), None))
    choice = control(popup, name, Atspi.Role.LABEL)
    # GTK exposes row names but no activation action or global popup position.
    bounds = choice.get_component_iface().get_extents(Atspi.CoordType.WINDOW)
    origin = popup.get_component_iface().get_extents(Atspi.CoordType.WINDOW)
    windows = []
    for wid in subprocess.check_output(["xdotool", "search", "--onlyvisible", "--pid", str(process.pid)], timeout=5).decode().splitlines():
        geometry = dict(line.split("=", 1) for line in subprocess.check_output(["xdotool", "getwindowgeometry", "--shell", wid], timeout=5).decode().splitlines())
        windows.append({key: int(value) for key, value in geometry.items()})
    native = min(windows, key=lambda w: abs(w["WIDTH"] - origin.width) + abs(w["HEIGHT"] - origin.height))
    assert Atspi.generate_mouse_event(native["X"] + bounds.x - origin.x + bounds.width // 2, native["Y"] + bounds.y - origin.y + bounds.height // 2, "b1c"), "Cannot choose peer from semantic dropdown row"
    wait(f"dropdown selected {name!r}", lambda: find(recipient, name, Atspi.Role.LABEL))


def status(text):
    return control(app, text, Atspi.Role.LABEL)


def stop():
    global process, app
    if process and process.poll() is None:
        control(app, "keyparcel", Atspi.Role.FRAME)
        window = subprocess.check_output(["xdotool", "search", "--onlyvisible", "--pid", str(process.pid), "--name", "^keyparcel$"], timeout=5).decode().splitlines()[0]
        subprocess.check_call(["xdotool", "windowactivate", "--sync", window], timeout=5)
        keys("Alt_L", "F4")
        process.wait(timeout=8)
        assert process.returncode == 0, "Client failed during normal window shutdown"
    app = None


def ids(name):
    data = json.loads(fixture.cli(name, "inbox", "--json"))
    return {item["transfer_id"] for item in (data or [])}


def peer_send(text=None, file=None):
    args = ["--to", "desktop"]
    if file is not None:
        args += ["--file", str(file)]
    else:
        args += ["--stdin"]
    output = fixture.cli("peer", "send", *args, **({"input": text.encode()} if file is None else {})).decode()
    match = re.search(r"stored ([a-f0-9]{32})", output)
    assert match, f"CLI peer did not store transfer: {output}"
    return match[1]


def refresh():
    click("Inbox")
    click("Refresh inbox")
    status("Inbox refreshed")


def received(id):
    refresh()
    click("Receive transfer " + id)
    status("Verified text; remote copy retained")


def picker_path(dialog, path, save=False):
    if save:
        name = filename_field(dialog)
        assert name.get_editable_text_iface().set_text_contents(str(path))
    else:
        keys("Control_L", "l")
        name = editable(dialog)
        assert name.get_editable_text_iface().set_text_contents(str(path))
    click("Save" if save else "Open", dialog)


def choose_save(id, path):
    refresh()
    click("Save transfer " + id)
    dialog = file_dialog("Save received file")
    picker_path(dialog, path, True)


def clipboard():
    return subprocess.check_output(["xclip", "-selection", "clipboard", "-o"], timeout=30)


def verify_received_text(id, message):
    received(id)
    node = control(app, "Received text")
    assert text_contents(node) == message, "Preview altered whitespace or Unicode"
    click("Copy complete message")
    status("Copied")
    assert clipboard() == message.encode(), "Clipboard differs from verified text"
    assert id in ids("desktop"), "Viewing deleted remote copy"
    screenshot("received-text.png")


def send_text_to_peer(message):
    click("Send")
    status("Ready.")
    select_recipient("bob")
    fill("Message", message)
    before_peer = ids("peer")
    click("Send text")
    outgoing = wait("GUI delivery to approved peer", lambda: ids("peer") - before_peer)
    assert len(outgoing) == 1, "GUI peer send created unexpected transfers"
    assert fixture.cli("peer", "receive", next(iter(outgoing)), "--stdout", "--keep") == message.encode(), "Independent peer received different GUI text"
    click("Send")
    status("Ready.")
    assert text_contents(control(app, "Message")) == "", "Successful text send retained its draft"
    control(control(app, "Recipient", Atspi.Role.COMBO_BOX), "bob", Atspi.Role.LABEL)


started = time.monotonic()
try:
    fixture.start(fresh=True)
    start()
    status("Choose an existing SSH identity and configure a target in Settings.")
    fill("SSH target", "inbox")
    fill("Identity file", str(fixture.directory / "desktop/id_ed25519"))
    click("Save settings")
    status("Settings saved; also available to the CLI")
    click("Copy enrollment key")
    status("Copied")
    assert clipboard().decode().split()[:2] == fixture.public("desktop").split()[:2], "Enrollment key differs from selected CLI identity"
    click("Connect")
    approval = control(Atspi.get_desktop(0), "SSH server approval")
    assert any("SHA256:" in n.get_name() for n in walk(approval)), "OpenSSH fingerprint absent from approval"
    screenshot("host-approval.png")
    previous_approval = approval.get_process_id()
    click("Reject", approval)
    wait("rejected approval helper exits", lambda: not Path(f"/proc/{previous_approval}").exists())
    wait("rejection restores settings", lambda: control(app, "Connect").get_state_set().contains(Atspi.StateType.SENSITIVE))
    assert not (fixture.directory / "desktop/known_hosts").read_text(), "Reject saved server trust"
    click("Connect")
    approval = control(Atspi.get_desktop(0), "SSH server approval")
    assert approval.get_process_id() != previous_approval, "Discovered stale approval dialog"
    click("Approve server key", approval)
    status("Connected. Inbox and Devices update automatically.")
    assert (fixture.directory / "desktop/known_hosts").read_text(), "OpenSSH did not remember approved server key"
    fixture.cli("desktop", "inbox") # Independent CLI immediately reuses graphical approval.

    wait("initial automatic refresh", lambda: text_contents(control(app,"Connection status")) == "Current")
    connection_ids = wait("exactly one persistent GUI session after CLI exit", lambda: (ids if len(ids := fixture.exec("pgrep", "-f", "sshd.*parcel@internal-sftp").decode().splitlines()) == 1 else None))
    fixture.exec("sed", "-i", "s/ peer$/ peer-auto/", "/home/parcel/.ssh/authorized_keys")
    click("Send"); click("Settings"); click("Back"); click("Devices")
    status("Unapproved\n" + fixture.fingerprint("peer") + "\npeer-auto")
    fixture.exec("sed", "-i", "s/ peer-auto$/ peer/", "/home/parcel/.ssh/authorized_keys")
    status("Unapproved\n" + fixture.fingerprint("peer") + "\npeer")
    assert fixture.exec("pgrep", "-f", "sshd.*parcel@internal-sftp").decode().splitlines() == connection_ids, "Polling or tab changes opened another SSH session"
    click("Refresh devices")
    status("Devices refreshed; verify fingerprints before approving")
    # Reuse the enrolled peer with a comment the CLI refuses to suggest.
    fixture.exec("sed", "-i", "s/ peer$/ invalid comment/", "/home/parcel/.ssh/authorized_keys")
    try:
        click("Refresh devices")
        status("Unapproved\n" + fixture.fingerprint("peer") + "\ninvalid comment")
        click("Approve device " + fixture.fingerprint("peer"))
        dialog = control(app, "Approve device", Atspi.Role.DIALOG)
        assert text_contents(control(dialog, "Peer name", Atspi.Role.TEXT)) == "", "Invalid SSH comment prefilled the approval name"
        click("Cancel", dialog)
        wait("invalid-comment approval dialog closes", lambda: not find(app, "Approve device", Atspi.Role.DIALOG))
    finally:
        fixture.exec("sed", "-i", "s/ invalid comment$/ peer/", "/home/parcel/.ssh/authorized_keys")
    click("Refresh devices")
    status("Unapproved\n" + fixture.fingerprint("peer") + "\npeer")
    click("Approve device " + fixture.fingerprint("peer"))
    dialog = control(app, "Approve device", Atspi.Role.DIALOG)
    assert text_contents(control(dialog, "Peer name", Atspi.Role.TEXT)) == "peer", "Approval name did not prefill the peer's SSH comment"
    screenshot("peer-approval.png")
    fill("Peer name", "bob", dialog)
    click("Approve", dialog)
    wait("GUI approval persisted", lambda: "bob" in json.loads((fixture.directory / "desktop/config.json").read_text())["peers"])
    status("Devices refreshed; verify fingerprints before approving")
    state = json.loads((fixture.directory / "desktop/config.json").read_text())
    assert state["peers"]["bob"].split()[:2] == fixture.public("peer").split()[:2], "GUI peer approval differs from CLI config"
    message = "hello 世界 — café\n\tline two\n"
    if os.environ.get("KEYPARCEL_INSTALLED_IMAGE"):
        id = peer_send(message)
        verify_received_text(id, message)
        click("Remove & close")
        status("Removed transfer")
        assert id not in ids("desktop"), "Successful removal retained transfer"
        send_text_to_peer(message)
        print("Installed GTK transfer journey passed (/usr/bin/keyparcel-gtk and /usr/bin/keyparcel)")
        raise SystemExit(0)
    click("Send")
    status("Ready.")
    select_recipient("bob")
    fill("Message", message)
    draft_file = session / "draft-世界.bin"
    draft_file.write_bytes(b"draft bytes")
    click("Choose file")
    picker_path(file_dialog("Choose a file"), draft_file)
    status("File selected")
    click("Inbox")
    click("Devices")
    click("Settings")
    status("Ready.")
    assert not find(app, "Remote storage", Atspi.Role.TEXT), "Advanced paths are prominent in essential Settings"
    screenshot("settings.png")
    activate(control(app, "Advanced"))
    control(app, "Remote storage", Atspi.Role.TEXT)
    screenshot("settings-advanced.png")
    keys("Escape")
    wait("Back closes Advanced", lambda: not find(app, "Remote storage", Atspi.Role.TEXT))
    click("Back")
    control(app, "Devices and approvals", Atspi.Role.LABEL)
    click("Send")
    status("Ready.")
    assert text_contents(control(app, "Message")) == message, "Navigation discarded the text draft"
    control(control(app, "Recipient", Atspi.Role.COMBO_BOX), "bob", Atspi.Role.LABEL)
    control(app, "draft-世界.bin · 11 B", Atspi.Role.LABEL)
    click("Choose file")
    click("Cancel", file_dialog("Choose a file"))
    wait("canceled picker closes", lambda: not find(app, "Choose a file", Atspi.Role.DIALOG))
    control(app, "draft-世界.bin · 11 B", Atspi.Role.LABEL)
    assert text_contents(control(app, "Message")) == message, "Picker cancellation discarded the text draft"
    screenshot("send-draft.png")
    draft_file.unlink()
    click("Send file")
    wait("missing source reports failed send", lambda: any("no such file or directory" in n.get_name().lower() for n in walk(app)))
    assert text_contents(control(app, "Message")) == message, "Failed file send discarded the text draft"
    control(app, "draft-世界.bin · 11 B", Atspi.Role.LABEL)
    control(control(app, "Recipient", Atspi.Role.COMBO_BOX), "bob", Atspi.Role.LABEL)
    draft_file.write_bytes(b"draft bytes")
    # A failed send retains both the input and the selected recipient/file.
    fixture.control("pause")
    try:
        click("Send text")
        status("Sending…")
        click("Cancel operation")
        status("Operation cancelled; remote transfers retained.")
    finally:
        fixture.control("unpause")
    assert text_contents(control(app, "Message")) == message, "Failed send discarded the text draft"
    control(control(app, "Recipient", Atspi.Role.COMBO_BOX), "bob", Atspi.Role.LABEL)
    control(app, "draft-世界.bin · 11 B", Atspi.Role.LABEL)
    click("Settings")
    status("Ready.")
    click("Back")
    control(app, "Message")
    assert text_contents(control(app, "Message")) == message, "Settings Back discarded the draft or returned to Inbox"
    # A CLI approval edit must invalidate the choice, without clearing contents.
    fixture.cli("desktop", "untrust", "bob")
    control(control(app, "Recipient", Atspi.Role.COMBO_BOX), "Choose a recipient", Atspi.Role.LABEL)
    assert not control(app, "Send text", Atspi.Role.PUSH_BUTTON).get_state_set().contains(Atspi.StateType.SENSITIVE), "Removed approval left sending enabled"
    assert text_contents(control(app, "Message")) == message, "Approval removal discarded draft contents"
    control(app, "draft-世界.bin · 11 B", Atspi.Role.LABEL)
    fixture.cli("desktop", "trust", fixture.fingerprint("peer"), "--name", "bob")
    click("Settings")
    status("Ready.")
    click("Back")
    status("Ready.")
    select_recipient("bob")
    config_path = fixture.directory / "desktop/config.json"
    state = json.loads(config_path.read_text())
    state["hosts"]["other"] = state["hosts"]["inbox"].copy()
    config_path.write_text(json.dumps(state))
    click("Settings")
    status("Ready.")
    fill("SSH target", "other")
    click("Use saved host")
    status("Ready.")
    click("Back")
    status("Ready.")
    control(control(app, "Recipient", Atspi.Role.COMBO_BOX), "Choose a recipient", Atspi.Role.LABEL)
    assert text_contents(control(app, "Message")) == message, "Host change discarded draft contents"
    control(app, "draft-世界.bin · 11 B", Atspi.Role.LABEL)
    assert not control(app, "Send file", Atspi.Role.PUSH_BUTTON).get_state_set().contains(Atspi.StateType.SENSITIVE), "Host change left sending enabled without a recipient"
    click("Settings")
    status("Ready.")
    fill("SSH target", "inbox")
    click("Use saved host")
    status("Ready.")
    click("Back")
    status("Ready.")
    click("Discard draft")
    status("Draft discarded")
    assert text_contents(control(app, "Message")) == "", "Explicit discard retained text"
    control(app, "No file selected", Atspi.Role.LABEL)
    # Delete from Inbox without receiving either kind of parcel.
    unrelated = peer_send("keep this unrelated parcel")
    unwanted_file = session / "unwanted.bin"
    unwanted_file.write_bytes(b"local file remains unchanged")
    stale = peer_send("stale approval confirmation")
    click("Inbox")
    click("Delete transfer " + stale)
    dialog = control(app, "Delete inbox copy?", Atspi.Role.DIALOG)
    fixture.cli("desktop", "untrust", "bob")
    control(app, "Not verified: " + stale, Atspi.Role.LABEL)
    click("Delete", dialog)
    status("The inbox or server changed; select the transfer again")
    assert stale in ids("desktop"), "Stale confirmation deleted a parcel after approval removal"
    fixture.cli("desktop", "trust", fixture.fingerprint("peer"), "--name", "bob")
    control(app, "Delete transfer " + stale)
    fixture.cli("desktop", "delete", stale)
    for unwanted in [peer_send("unwanted text"), peer_send(file=unwanted_file)]:
        click("Inbox")
        click("Delete transfer " + unwanted)
        dialog = control(app, "Delete inbox copy?", Atspi.Role.DIALOG)
        control(dialog, "Delete this device's remote inbox copy without downloading it?\nOther recipients' copies and saved local files remain untouched.", Atspi.Role.LABEL)
        click("Cancel", dialog)
        assert unwanted in ids("desktop"), "Cancel deleted an inbox parcel"
        click("Delete transfer " + unwanted)
        control(app, "Delete inbox copy?", Atspi.Role.DIALOG)
        keys("Escape")
        wait("delete confirmation dismissed", lambda: not find(app, "Delete inbox copy?"))
        assert unwanted in ids("desktop"), "Dialog dismissal deleted an inbox parcel"
        # A real remote failure must preserve the row and permit a retry.
        fixture.exec("chmod", "500", fixture.inbox_path("desktop"))
        click("Delete transfer " + unwanted)
        click("Delete", control(app, "Delete inbox copy?", Atspi.Role.DIALOG))
        wait("delete failure", lambda: any("could not delete transfer" in n.get_name() for n in walk(app)))
        assert unwanted in ids("desktop"), "Failed inbox deletion lost the parcel"
        control(app, "Delete transfer " + unwanted)
        fixture.exec("chmod", "700", fixture.inbox_path("desktop"))
        click("Delete transfer " + unwanted)
        click("Delete", control(app, "Delete inbox copy?", Atspi.Role.DIALOG))
        status("Deleted remote inbox copy; saved local files kept")
        assert unwanted not in ids("desktop"), "Inbox delete did not remove the remote parcel"
        assert unrelated in ids("desktop"), "Inbox delete removed an unrelated parcel"
        assert unwanted_file.read_bytes() == b"local file remains unchanged", "Inbox delete modified a local file"
    fixture.cli("desktop", "delete", unrelated)
    removed = peer_send("automatic removal")
    click("Inbox")
    control(app,"Receive transfer " + removed)
    fixture.cli("desktop","delete",removed)
    wait("automatic inbox removal", lambda: not find(app,"Receive transfer " + removed))
    id = peer_send(message)
    control(app,"Receive transfer " + id)
    assert not find(app, id, Atspi.Role.LABEL), "Inbox exposed full transfer ID outside details"
    assert any(re.search(r"Uploaded \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}", n.get_name()) for n in walk(app)), "Inbox lost the absolute timestamp with seconds/timezone"
    screenshot("inbox.png")
    click("Details transfer " + id)
    dialog = control(app, "Transfer details", Atspi.Role.DIALOG)
    control(dialog, id, Atspi.Role.LABEL)
    click("Copy transfer ID", dialog)
    status("Copied")
    assert clipboard() == id.encode(), "Transfer details copied a different ID"
    screenshot("transfer-details.png")
    keys("Escape")
    wait("Back closes transfer details", lambda: not find(app, "Transfer details", Atspi.Role.DIALOG))
    assert id in ids("desktop"), "Closing details removed the transfer"
    verify_received_text(id, message)
    fixture.cli("desktop", "untrust", "bob")
    control(app, "Refresh inbox", Atspi.Role.PUSH_BUTTON)
    assert not find(app, "Received text"), "Approval removal left received text visible"
    assert not find(app, "Copy complete message"), "Approval removal left stale result actions visible"
    fixture.cli("desktop", "trust", fixture.fingerprint("peer"), "--name", "bob")
    assert id in ids("desktop"), "Approval removal deleted the remote transfer"
    received(id)
    keys("Alt_L", "Left")
    control(app, "Refresh inbox", Atspi.Role.PUSH_BUTTON)
    assert id in ids("desktop"), "Keyboard Back deleted remote copy"
    received(id)
    # Removal failure must preserve the viewer and retry action.
    fixture.exec("chmod", "500", fixture.inbox_path("desktop"))
    click("Remove & close")
    wait("removal failure explains permission denial", lambda: any("permission denied" in n.get_name().lower() for n in walk(app)))
    wait("failed removal restores retry", lambda: control(app, "Remove & close").get_state_set().contains(Atspi.StateType.SENSITIVE))
    assert id in ids("desktop"), "Failed removal lost transfer"
    fixture.exec("chmod", "700", fixture.inbox_path("desktop"))
    click("Remove & close")
    status("Removed transfer")
    assert id not in ids("desktop"), "Successful removal retained transfer"

    click("Send")
    status("Ready.")
    select_recipient("This device")
    fill("Message", message)
    click("Send text")
    wait("send completion", lambda: any(n.get_name().startswith("Sent ") for n in walk(app)))
    refresh()
    own = ids("desktop")
    assert len(own) == 1, "Send-to-self did not create exactly one transfer"
    assert fixture.cli("desktop", "receive", next(iter(own)), "--stdout", "--keep") == message.encode(), "Independent CLI received different GUI text"

    send_text_to_peer(message)

    for size in (16 * 1024 * 1024, 16 * 1024 * 1024 + 1):
        large = "x" * size
        incoming = peer_send(large)
        received(incoming)
        preview = control(app, "Received text")
        assert text_contents(preview) == large[:65536], "Large preview was not bounded/exact"
        status("Preview truncated; the complete remote transfer is retained.")
        previous = clipboard()
        click("Copy complete message")
        if size == 16 * 1024 * 1024:
            status("Copied")
            assert clipboard() == large.encode(), "Inclusive clipboard boundary copied only preview"
        else:
            status("Complete message exceeds the 16 MiB clipboard limit; its remote copy is retained.")
            assert clipboard() == previous, "Rejected oversized copy changed clipboard"
        click("Remove & close")
        status("Removed transfer")
        assert incoming not in ids("desktop"), "Large text removal failed"

    # File open/save journeys share the same SSH fixture.
    source = session / "binary-世界.bin"
    source.write_bytes(bytes(range(256)) * (17 * 1024 * 1024 // 256))
    for empty in (False, True):
        if empty:
            source = session / "empty.bin"
            source.write_bytes(b"")
        click("Send")
        status("Ready.")
        select_recipient("This device")
        click("Choose file")
        dialog = file_dialog("Choose a file")
        click("Cancel", dialog)
        status("Selection cancelled; remote transfers retained")
        click("Choose file")
        dialog = file_dialog("Choose a file")
        picker_path(dialog, source)
        status("File selected")
        before = ids("desktop")
        click("Send file")
        wait("file send completion", lambda: any(n.get_name().startswith("Sent ") for n in walk(app)))
        after = ids("desktop")
        new = after - before
        assert len(new) == 1, "GUI file send did not create one transfer"
        dest = session / ("cli-" + source.name)
        fixture.cli("desktop", "receive", next(iter(new)), "--save-to", str(dest), "--keep")
        assert dest.read_bytes() == source.read_bytes(), "Independent CLI file bytes differ"
        item = next(i for i in json.loads(fixture.cli("desktop", "inbox", "--json")) if i["transfer_id"] in new)
        assert item["filename"] == source.name, "GUI send lost original filename"
        click("Send")
        status("Ready.")
        control(app, "No file selected", Atspi.Role.LABEL)
        assert not control(app, "Send file", Atspi.Role.PUSH_BUTTON).get_state_set().contains(Atspi.StateType.SENSITIVE), "Successful file send retained a sendable file draft"
        incoming = peer_send(file=source)
        refresh()
        if not empty: screenshot("inbox-files.png")
        click("Save transfer " + incoming)
        dialog = file_dialog("Save received file")
        field = filename_field(dialog)
        wait(f"incoming save suggestion {source.name!r}", lambda: text_contents(field) == source.name)
        click("Cancel", dialog)
        status("Selection cancelled; remote transfers retained")
        assert incoming in ids("desktop"), "Save cancellation lost incoming file"
        if not empty:
            failed_dir = session / "unwritable"
            failed_dir.mkdir()
            failed = failed_dir / "output.bin"
            refresh()
            click("Save transfer " + incoming)
            dialog = file_dialog("Save received file")
            fixture.control("pause")
            try:
                picker_path(dialog, failed, True)
                status("Verifying and saving file…")
                failed_dir.chmod(0o500)
            finally:
                fixture.control("unpause")
            try:
                wait("unwritable destination reports save failure", lambda: any("permission denied" in n.get_name().lower() for n in walk(app)))
                assert not failed.exists(), "Failed save published destination"
                assert not list(failed_dir.glob(".keyparcel-save-*")), "Failed save left output staging"
                assert incoming in ids("desktop"), "Failed save lost remote file"
            finally:
                failed_dir.chmod(0o700)
        target = session / ("gtk-" + source.name)
        choose_save(incoming, target)
        status("File saved; remote copy retained")
        assert target.read_bytes() == source.read_bytes(), "GTK destination differs from peer file"
        assert incoming in ids("desktop"), "Successful save deleted remote file"
        screenshot("saved-file.png")
        click("Back — keep transfer")
        # Saving again requires explicit replacement; declining protects old bytes.
        target.write_bytes(b"protected destination")
        choose_save(incoming, target)
        dialog = control(app, "Replace", Atspi.Role.PUSH_BUTTON).get_parent().get_parent().get_parent()
        click("Cancel", dialog)
        click("Cancel", file_dialog("Save received file"))
        status("Selection cancelled; remote transfers retained")
        assert target.read_bytes() == b"protected destination", "Declined replacement overwrote destination"
        choose_save(incoming, target)
        click("Replace")
        dialog = control(app, "Confirm action", Atspi.Role.DIALOG)
        click("Confirm", dialog)
        status("File saved; remote copy retained")
        assert target.read_bytes() == source.read_bytes(), "Confirmed replacement wrote different bytes"
        click("Remove & close")
        status("Removed transfer")
        assert incoming not in ids("desktop"), "Saved-file removal failed"
        extra = peer_send(file=source)
        click("Delete transfer " + extra)
        click("Delete", control(app, "Delete inbox copy?", Atspi.Role.DIALOG))
        status("Deleted remote inbox copy; saved local files kept")
        assert extra not in ids("desktop"), "File inbox deletion failed"
        assert target.read_bytes() == source.read_bytes(), "Inbox deletion modified a saved local file"


    known = fixture.directory / "desktop/known_hosts"
    approved = known.read_bytes()
    changed = f"{fixture.hostname} {fixture.public('peer')}\n".encode()
    known.write_bytes(changed)
    try:
        click("Inbox")
        fixture.exec("pkill", "-u", "parcel")
        wait("disconnected transport becomes stale", lambda: text_contents(control(app, "Connection status")) == "Stale")
        click("Refresh inbox")
        wait("changed key is rejected", lambda: any("REMOTE HOST IDENTIFICATION HAS CHANGED" in n.get_name() for n in walk(app)))
        assert known.read_bytes() == changed, "GUI silently replaced a changed host key"
        assert not find(Atspi.get_desktop(0), "SSH server approval"), "Changed host key offered approval"
    finally:
        known.write_bytes(approved)

    # A disconnected/stalled server restores controls, and cancellation is usable.
    refresh()
    fixture.control("pause")
    try:
        click("Refresh inbox")
        status("Checking inbox and devices…")
        click("Cancel operation")
        status("Operation cancelled; remote transfers retained.")
        click("Refresh inbox")
        wait("stalled server reports a bounded failure", lambda: any(n.get_role() == Atspi.Role.LABEL and ("timed out" in n.get_name().lower() or "timeout" in n.get_name().lower() or "deadline exceeded" in n.get_name().lower()) for n in walk(app)), seconds=20)
        wait("timeout restores navigation", lambda: control(app, "Settings").get_state_set().contains(Atspi.StateType.SENSITIVE))
        control(app,"Receive transfer " + next(iter(own)))
    finally:
        fixture.control("unpause")
    wait("automatic reconnection after resume", lambda: text_contents(control(app,"Connection status")) == "Current",seconds=40)
    control(app,"Receive transfer " + next(iter(own)))
    stop()
    start()
    click("Devices")
    click("Refresh devices")
    status("Devices refreshed; verify fingerprints before approving")
    control(app, "Forget device " + fixture.fingerprint("peer"))
    assert not find(Atspi.get_desktop(0), "SSH server approval"), "Restart forgot server trust"
    screenshot("remembered-devices.png")
    ssh_config = fixture.directory / "desktop/ssh_config"
    original_ssh = ssh_config.read_text()
    fixture.exec("sed", "-i", "s/DisableForwarding yes/DisableForwarding no/", "/etc/ssh/sshd_config")
    fixture.exec("kill", "-HUP", "1")
    identity = fixture.directory / "desktop/id_ed25519"
    gateway_identity = fixture.directory / "peer/id_ed25519"
    ssh_config.write_text(f"Host inbox\n HostName 127.0.0.1\n Port 22\n User parcel\n IdentityFile {identity}\n IdentityAgent none\n ProxyJump gateway\n HostKeyAlias fixture-target\n UserKnownHostsFile {known}\n GlobalKnownHostsFile /dev/null\nHost gateway\n HostName {fixture.hostname}\n Port {fixture.port}\n User parcel\n IdentityFile {gateway_identity}\n IdentitiesOnly yes\n IdentityAgent none\n HostKeyAlias fixture-gateway\n UserKnownHostsFile {known}\n GlobalKnownHostsFile /dev/null\n")
    try:
        fixture.exec("pkill", "-u", "parcel")
        wait("jump transport disconnects", lambda: text_contents(control(app, "Connection status")) == "Stale")
        click("Settings")
        click("Connect")
        previous = None
        for endpoint in ("gateway", "target"):
            dialog = wait(f"graphical approval for jump {endpoint}", lambda: (d if (d := find(Atspi.get_desktop(0), "SSH server approval")) and d.get_process_id() != previous else None))
            previous = dialog.get_process_id()
            click("Approve server key", dialog)
            wait(f"approved {endpoint} helper exits", lambda: not Path(f"/proc/{previous}").exists())
        status("Connected. Inbox and Devices update automatically.")
        fixture.cli("desktop", "inbox")
    finally:
        ssh_config.write_text(original_ssh)
        fixture.exec("sed", "-i", "s/DisableForwarding no/DisableForwarding yes/", "/etc/ssh/sshd_config")
        fixture.exec("kill", "-HUP", "1")
    screenshot("jump-host-inbox.png")
    print(f"GTK client journey passed in {time.monotonic() - started:.2f}s; artifacts: {session}")
except Exception:
    screenshot("failure.png")
    with (session / "accessibility.txt").open("w") as output:
        for node in walk(Atspi.get_desktop(0)):
            try:
                output.write(f"{node.get_role_name()}: {node.get_name()!r}\n")
            except GLib.Error:
                output.write("node unavailable during failure capture\n")
    raise
finally:
    if process and process.poll() is None:
        process.terminate()
        try: process.wait(timeout=5)
        except subprocess.TimeoutExpired: process.kill(); process.wait()
    for log in logs: log.close()
    fixture.down()
