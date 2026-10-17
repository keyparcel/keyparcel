"""Container lifecycle checks share one owned, disposable smoke fixture."""
import json
from pathlib import Path
import subprocess
import tempfile
import time
from fixture import Fixture, DOCKER, command
from run import check_smoke_idle, handle_control, seed
from parcels import samples

with tempfile.TemporaryDirectory(prefix="keyparcel-gtk-smoke-check-") as temporary:
    fixture = Fixture(Path(temporary) / "owned")
    try:
        fixture.start()
        container, client, network = fixture.container, fixture.client, fixture.network
        for request in (["rm", [container]], ["pause", [container]], ["exec", [1]]):
            try:
                handle_control(fixture, "KEYPARCEL_FIXTURE " + json.dumps(request))
            except ValueError:
                pass
            else:
                raise AssertionError(f"Host accepted invalid test control: {request}")
        key = fixture.public("desktop")
        hostkey = fixture.hostkey
        assert fixture.local(["id", "-u"]).strip() != b"0", "GUI fixture runs as root"
        fixture.local(["test", "!", "-e", "/var/run/docker.sock"])
        assert command(DOCKER + ["port", fixture.container]) == b"", "Fixture publishes SSH outside its network"
        config = fixture.directory / "desktop/config.json"
        state = json.loads(config.read_text())
        state["peers"]["bob"] = fixture.public("peer")
        config.write_text(json.dumps(state))
        sent = fixture.cli("peer", "send", "--to", "desktop", "--text", "remembered transfer")
        assert sent, "Peer did not send remembered transfer"
        files = fixture.directory / "files"
        files.mkdir()
        (files / "example.txt").write_text("shared file")
        assert fixture.local(["cat", files / "example.txt"]) == b"shared file", "Shared file mount differs"
        command(DOCKER + ["stop", fixture.container, fixture.client])
        fixture.start()
        fixture.cli("peer", "inbox")
        assert fixture.container == container and fixture.client == client, "Reopen recreated fixture containers"
        assert fixture.hostkey == hostkey and fixture.public("desktop") == key, "Reopen recreated identities"
        assert json.loads(config.read_text())["peers"]["bob"] == fixture.public("peer"), "Reopen lost GUI approval"
        transfers = json.loads(fixture.cli("desktop", "inbox", "--json"))
        assert len(transfers) == 1, "Restart lost transfer"
        transfer_id = transfers[0]["transfer_id"]
        assert (files / "example.txt").read_text() == "shared file", "Restart lost shared file"

        gui = subprocess.Popen(DOCKER + ["exec", fixture.client, "xvfb-run", "-a", "dbus-run-session", "--",
            str(fixture.binary.parent / "keyparcel-gtk"), "--config", str(config),
            "--ssh-config", str(fixture.directory / "desktop/ssh_config")], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            for _ in range(100):
                try:
                    check_smoke_idle(fixture, "down")
                except RuntimeError:
                    break
                if gui.poll() is not None:
                    raise AssertionError("Smoke lifecycle GUI exited before reset check")
                time.sleep(.05)
            else:
                raise AssertionError("Reset allowed a live container GUI")
            check_smoke_idle(fixture, "peer") # Peer shell is allowed alongside GUI.
        finally:
            fixture.local(["pkill", "-TERM", "-f", "^" + str(fixture.binary.parent / "keyparcel-gtk") + " --config"])
            gui.wait(timeout=10)
        check_smoke_idle(fixture, "down")

        shell = subprocess.Popen(DOCKER + ["exec", "-i", fixture.client, "bash", "--rcfile", "/dev/null", "-i"],
                                 stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            for _ in range(100):
                try:
                    check_smoke_idle(fixture, "fresh")
                except RuntimeError:
                    break
                time.sleep(.05)
            else:
                raise AssertionError("Fresh setup allowed a live container peer shell")
            check_smoke_idle(fixture, "up") # GUI is allowed alongside peer shell.
        finally:
            shell.stdin.write(b"exit\n")
            shell.stdin.close()
            shell.wait(timeout=10)

        seed_directory = files / "parcels"
        seed(seed_directory, lambda kind, path: fixture.cli("peer", "send", "--to", "desktop",
            *(["--stdin"] if kind == "text" else ["--file", str(path)]),
            **({"input": path.read_bytes()} if kind == "text" else {})))
        seeded = json.loads(fixture.cli("desktop", "inbox", "--json"))[:6]
        expected = [(kind, "" if kind == "text" else path.name, path.stat().st_size)
            for kind, path in samples(seed_directory)]
        assert [(item["type"], (item.get("filename") or ""), item["size"]) for item in seeded] == expected, "Smoke parcels lost newest-first representative order"
        seed(seed_directory, lambda *_: (_ for _ in ()).throw(AssertionError("Reopen resent smoke parcels")))

        other_trust = Path(temporary) / "other-profile-known_hosts"
        other_trust.write_bytes(b"other profile's approved server\n")
        known = fixture.directory / "desktop/known_hosts"
        known.unlink()
        known.symlink_to(other_trust)
        fixture.start(fresh=True)
        assert not known.is_symlink(), "Fresh mode kept a trust-file symlink"
        assert other_trust.read_bytes() == b"other profile's approved server\n", "Fresh mode changed another profile's trust"
        fixture.start()
        assert not config.exists(), "Reopen auto-configured unfinished fresh setup"
        assert not (fixture.directory / "desktop/known_hosts").read_bytes(), "Fresh mode silently trusted server"
        assert (fixture.directory / "peer/known_hosts").read_bytes(), "Fresh mode cleared peer trust"
        assert fixture.public("desktop") == key and fixture.hostkey == hostkey, "Fresh mode regenerated identities"
        fixture.exec("test", "-f", fixture.inbox_path("desktop") + "/" + transfer_id + ".age")
        assert (files / "example.txt").read_text() == "shared file", "Fresh mode discarded shared file"
        # A foreign state file must not authorize removal of our containers/network.
        foreign = Fixture(Path(temporary) / "foreign")
        foreign.directory.mkdir()
        foreign.state.write_bytes(fixture.state.read_bytes())
        try:
            foreign.down()
        except RuntimeError:
            pass
        else:
            raise AssertionError("Reset accepted another fixture's resources")
        fixture.check_owner()
        print("Container smoke isolation/reuse/fresh/live-reset/ownership checks passed")
    finally:
        fixture.down()
    assert not fixture.directory.exists(), "Reset left fixture state behind"
    for resource in (container, client):
        assert subprocess.run(DOCKER + ["inspect", resource], capture_output=True).returncode, "Reset left container behind"
    assert subprocess.run(DOCKER + ["network", "inspect", network], capture_output=True).returncode, "Reset left network behind"
